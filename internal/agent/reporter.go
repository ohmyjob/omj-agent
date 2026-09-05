package agent

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ohmyjob/omj-agent/internal/client"
	"github.com/ohmyjob/omj-agent/internal/output"
	"github.com/ohmyjob/omj-agent/internal/protocol"
	"github.com/ohmyjob/omj-agent/internal/runner"
	"github.com/ohmyjob/omj-agent/internal/state"
)

type Ticker func(interval time.Duration) (ticks <-chan time.Time, stop func())

func realTicker(interval time.Duration) (<-chan time.Time, func()) {
	ticker := time.NewTicker(interval)

	return ticker.C, ticker.Stop
}

type reporter struct {
	agent *Agent
}

func (r reporter) Report(ctx context.Context, run *Run) {
	p := &report{
		agent:      r.agent,
		ctx:        ctx,
		run:        run,
		runID:      run.Lease.RunID,
		logger:     r.agent.logger.With("run_id", run.Lease.RunID, "job", run.Lease.JobName),
		backoff:    &client.Backoff{Rand: r.agent.backoff.Rand, Sleep: r.agent.sleep},
		retryReady: make(chan struct{}, 1),
		heartbeats: run.Process != nil,
	}

	p.deliver()
}

// Resend makes one attempt; the Server repeats the lease if the answer is lost.
func (r reporter) Resend(ctx context.Context, lease protocol.RunLease, outcome state.RecentRun) error {
	request := protocol.FinishRequest{
		Status:     protocol.RunStatus(outcome.Status),
		ExitCode:   outcome.ExitCode,
		StartedAt:  outcome.StartedAt,
		FinishedAt: outcome.FinishedAt,
	}

	if request.Status == protocol.RunStatusLost {
		reason := protocol.ReasonAgentRestarted
		request.Reason = &reason
	}

	_, err := r.agent.client.FinishRun(ctx, lease.RunID, request)
	if err != nil && !client.IsConflict(err, protocol.ErrRunFinished) {
		return fmt.Errorf("finish %s again: %w", lease.RunID, err)
	}

	return nil
}

// report is the delivery of one Run. Its flags are only touched by the
// goroutine running deliver, so they need no lock.
type report struct {
	agent   *Agent
	ctx     context.Context
	run     *Run
	runID   string
	logger  *slog.Logger
	backoff *client.Backoff

	retryReady chan struct{}

	// waiting: a backoff sleep is running and output must not be sent until
	// it ends. disconnected: the last request failed, so a heartbeat has to
	// succeed before output is replayed. outputDone: the Server wants no more
	// output. heartbeats: the Server still accepts heartbeats.
	waiting         bool
	disconnected    bool
	outputDone      bool
	heartbeats      bool
	serverTruncated bool
}

func (p *report) deliver() {
	var result *runner.Result

	if p.run.Process != nil {
		exited := p.supervise()
		result = &exited
	}

	lastSeq, truncated := p.run.Chunker.Close()
	p.drain()

	request := p.finishRequest(result, lastSeq, truncated || p.serverTruncated || p.agent.buffer.Truncated(p.runID))
	p.finish(request)

	outcome := state.Outcome{Status: string(request.Status), ExitCode: request.ExitCode, StartedAt: request.StartedAt}
	if err := p.agent.state.MarkFinished(p.runID, outcome); err != nil {
		p.logger.Error("state not saved", "error", err)
	}

	p.agent.buffer.Forget(p.runID)
}

func (p *report) supervise() runner.Result {
	settings := p.agent.Settings()

	flush, stopFlush := p.agent.ticker(settings.FlushInterval)
	defer stopFlush()

	heartbeat, stopHeartbeat := p.agent.ticker(settings.HeartbeatInterval)
	defer stopHeartbeat()

	exited := make(chan runner.Result, 1)

	go func() { exited <- p.run.Process.Wait() }()

	for {
		select {
		case result := <-exited:
			return result
		case <-flush:
			p.sendOutput()
		case <-p.retryReady:
			p.waiting = false
			p.sendOutput()
		case <-heartbeat:
			p.sendHeartbeat()
		}
	}
}

func (p *report) sendOutput() {
	if p.outputDone || p.waiting {
		return
	}

	batch := p.agent.buffer.NextBatch(p.runID)
	if len(batch) == 0 {
		return
	}

	if p.disconnected {
		// A Run the Server marked lost accepts output only once a heartbeat
		// has moved it back to running.
		if err := p.heartbeat(); err != nil {
			p.retryLater(err)

			return
		}
	}

	response, err := p.agent.client.AppendOutput(p.ctx, p.runID, outputRequest(batch))
	if err != nil {
		p.outputFailed(err)

		return
	}

	p.accept(response)
}

func (p *report) accept(response protocol.OutputResponse) {
	p.backoff.Reset()
	p.agent.buffer.AckUpTo(p.runID, response.LastOutputSeq)

	if response.Truncated {
		p.serverTruncated = true
		p.outputDone = true
		p.logger.Info("output limit reached; sending no more output")
	}

	if response.CancelRequested {
		p.cancel()
	}
}

func (p *report) outputFailed(err error) {
	switch {
	case client.IsConflict(err, protocol.ErrRunNotRunning), client.IsNotFound(err):
		p.outputDone = true
		p.logger.Info("server no longer accepts output", "error", err)
	case client.IsRetryable(err):
		p.disconnected = true
		p.logger.Warn("output not delivered; keeping it", "error", err)
		p.retryLater(err)
	default:
		p.outputDone = true
		p.logger.Error("output rejected; sending no more output", "error", err)
	}
}

// retryLater starts one backoff sleep off the loop so heartbeats and the
// process exit are still noticed while the Server is unreachable.
func (p *report) retryLater(err error) {
	if p.waiting || !client.IsRetryable(err) {
		return
	}

	p.waiting = true
	delay := max(p.backoff.Next(), client.RetryAfter(err))

	go func() {
		_ = p.agent.sleep(p.ctx, delay)
		p.retryReady <- struct{}{}
	}()
}

func (p *report) sendHeartbeat() {
	if !p.heartbeats {
		return
	}

	if err := p.heartbeat(); err != nil && p.heartbeats {
		// A missed heartbeat is never a reason to stop the process; the next
		// tick tries again.
		p.logger.Warn("heartbeat failed", "error", err)
	}
}

func (p *report) heartbeat() error {
	response, err := p.agent.client.Heartbeat(p.ctx, p.runID)
	if err == nil {
		p.disconnected = false

		if response.CancelRequested {
			p.cancel()
		}

		return nil
	}

	switch {
	case client.IsConflict(err, protocol.ErrRunNotRunning), client.IsNotFound(err):
		p.heartbeats = false
		p.outputDone = true
		p.logger.Info("server no longer accepts heartbeats", "error", err)
	case client.IsRetryable(err):
		p.disconnected = true
	}

	return err
}

func (p *report) cancel() {
	if p.run.Process == nil {
		return
	}

	p.logger.Info("cancellation requested")
	p.run.Process.Cancel(CancelRequested)
}

// Drain before reporting finish so the outcome cannot overtake buffered output.
func (p *report) drain() {
	for !p.outputDone {
		batch := p.agent.buffer.NextBatch(p.runID)
		if len(batch) == 0 {
			return
		}

		err := client.Retry(p.ctx, p.backoff, func(ctx context.Context) error {
			if p.disconnected {
				if err := p.heartbeat(); err != nil {
					return err
				}
			}

			response, err := p.agent.client.AppendOutput(ctx, p.runID, outputRequest(batch))
			if err != nil {
				if client.IsRetryable(err) {
					p.disconnected = true
				}

				return err
			}

			p.accept(response)

			return nil
		})

		if p.ctx.Err() != nil {
			p.logger.Warn("output not delivered; the agent is stopping", "error", err)

			return
		}

		if err != nil && !p.outputDone {
			p.outputFailed(err)
		}
	}
}

func (p *report) finishRequest(result *runner.Result, lastSeq uint64, truncated bool) protocol.FinishRequest {
	request := protocol.FinishRequest{
		FinishedAt:      p.agent.now(),
		LastOutputSeq:   lastSeq,
		OutputTruncated: truncated,
	}

	switch {
	case p.run.CancelledBeforeStart:
		request.Status = protocol.RunStatusCancelled
	case p.run.SpawnErr != nil:
		reason := protocol.ReasonSpawnFailed
		request.Status = protocol.RunStatusFailed
		request.Reason = &reason
	default:
		startedAt := p.run.StartedAt
		request.Status, request.ExitCode, request.Reason = OutcomeOf(*result)
		request.StartedAt = &startedAt
	}

	return request
}

// finish reports the outcome until the Server has it or says it will never
// take it; either way the outcome stays on record locally.
func (p *report) finish(request protocol.FinishRequest) {
	err := client.Retry(p.ctx, p.backoff, func(ctx context.Context) error {
		_, err := p.agent.client.FinishRun(ctx, p.runID, request)

		return err
	})

	attrs := []any{"status", request.Status, "exit_code", exitCode(request.ExitCode)}

	switch {
	case err == nil:
		p.logger.Info("run finished", attrs...)
	case client.IsConflict(err, protocol.ErrRunFinished):
		p.logger.Info("run finished; the server already had an outcome", append(attrs, "error", err)...)
	case client.IsConflict(err, protocol.ErrNotLeased), client.IsNotFound(err):
		p.logger.Warn("outcome dropped; the server does not know this run", append(attrs, "error", err)...)
	default:
		p.logger.Error("outcome not delivered; it is kept for the next lease", append(attrs, "error", err)...)
	}
}

func outputRequest(batch []output.Chunk) protocol.OutputRequest {
	chunks := make([]protocol.OutputChunk, len(batch))
	for i, chunk := range batch {
		chunks[i] = chunk.Protocol()
	}

	return protocol.OutputRequest{Chunks: chunks}
}

func exitCode(code *int) any {
	if code == nil {
		return nil
	}

	return *code
}
