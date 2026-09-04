// Package agent runs the main loop: it asks the Server for work, verifies
// every lease before anything else, starts the process and hands it to a
// reporter, and rides out Server outages without ever killing a running
// process.
package agent

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/ohmyjob/omj-agent/internal/client"
	"github.com/ohmyjob/omj-agent/internal/config"
	"github.com/ohmyjob/omj-agent/internal/output"
	"github.com/ohmyjob/omj-agent/internal/protocol"
	"github.com/ohmyjob/omj-agent/internal/runner"
	"github.com/ohmyjob/omj-agent/internal/state"
	"github.com/ohmyjob/omj-agent/internal/sysinfo"
)

const (
	// MaxSlots is the largest number of new Runs one work request may ask for.
	MaxSlots = 16

	// MaxPollWait is the longest wait the work request may ask for.
	MaxPollWait = 25 * time.Second

	// CancelRequested is the cancel reason handed to the runner when the
	// Server asks for a Run to stop.
	CancelRequested = "cancel_requested"

	// A lease is refused when its expiry is further in the past than the
	// clocks of the two machines are allowed to disagree.
	clockTolerance = 5 * time.Second
)

// Settings is the timing the last work response asked the Agent to apply.
type Settings struct {
	HeartbeatInterval time.Duration
	FlushInterval     time.Duration
	ChunkBytes        int
	PollWait          time.Duration
}

var DefaultSettings = Settings{
	HeartbeatInterval: 15 * time.Second,
	FlushInterval:     output.DefaultFlushInterval,
	ChunkBytes:        output.DefaultChunkBytes,
	PollWait:          MaxPollWait,
}

// Reporter owns a Run from the moment the Agent hands it over until the
// Server has accepted its outcome; task 012 implements the real one.
type Reporter interface {
	Report(ctx context.Context, run *Run)
}

// Resender answers a lease whose run id already has a stored outcome by
// sending that outcome again; task 012 implements the real one.
type Resender interface {
	Resend(ctx context.Context, lease protocol.RunLease, outcome state.RecentRun) error
}

type Options struct {
	Config   config.Config
	Client   *client.Client
	Info     sysinfo.Info
	State    *state.Store
	Runner   runner.Runner
	Buffer   *output.Buffer
	Reporter Reporter
	Resender Resender
	Logger   *slog.Logger
	Now      func() time.Time
	Backoff  *client.Backoff
	Sleep    client.Sleeper
}

type Agent struct {
	cfg      config.Config
	client   *client.Client
	info     sysinfo.Info
	state    *state.Store
	runner   runner.Runner
	buffer   *output.Buffer
	reporter Reporter
	resender Resender
	logger   *slog.Logger
	now      func() time.Time
	backoff  *client.Backoff
	sleep    client.Sleeper

	registry registry
	rejected rejected
	runs     sync.WaitGroup

	mu       sync.Mutex
	settings Settings
}

func New(opts Options) (*Agent, error) {
	if opts.Client == nil || opts.State == nil || opts.Buffer == nil {
		return nil, errors.New("agent: a client, a state store and an output buffer are required")
	}

	if opts.Config.MachineID == "" {
		return nil, errors.New("agent: the configuration has no machine_id; run omj-agent enroll first")
	}

	a := &Agent{
		cfg:      opts.Config,
		client:   opts.Client,
		info:     opts.Info,
		state:    opts.State,
		runner:   opts.Runner,
		buffer:   opts.Buffer,
		reporter: opts.Reporter,
		resender: opts.Resender,
		logger:   opts.Logger,
		now:      opts.Now,
		backoff:  opts.Backoff,
		sleep:    opts.Sleep,
		registry: newRegistry(),
		rejected: newRejected(),
		settings: DefaultSettings,
	}

	if a.reporter == nil {
		a.reporter = waitReporter{agent: a}
	}

	if a.resender == nil {
		a.resender = noResender{}
	}

	if a.logger == nil {
		a.logger = slog.Default()
	}

	if a.now == nil {
		a.now = time.Now
	}

	if a.backoff == nil {
		a.backoff = &client.Backoff{}
	}

	if a.sleep == nil {
		a.sleep = sleep
	}

	return a, nil
}

// Run polls until the context ends. A stop is not a failure, so it returns
// nil; Runs still in progress keep their goroutines, which Wait collects.
func (a *Agent) Run(ctx context.Context) error {
	for ctx.Err() == nil {
		response, err := a.client.Work(ctx, a.workRequest())
		if err != nil {
			if ctx.Err() != nil {
				break
			}

			if err := a.sleep(ctx, a.delayAfter(err)); err != nil {
				break
			}

			continue
		}

		a.backoff.Reset()
		a.apply(response.Config)
		a.cancel(response.CancelRunIDs)

		cancelled := make(map[string]bool, len(response.CancelRunIDs))
		for _, id := range response.CancelRunIDs {
			cancelled[id] = true
		}

		for _, lease := range response.Runs {
			a.handleLease(ctx, lease, cancelled[lease.RunID])
		}
	}

	a.logger.Info("agent loop stopped")

	return nil
}

// Wait blocks until every Run handed to the reporter has been released.
func (a *Agent) Wait() {
	a.runs.Wait()
}

// Settings is what the last work response asked for, or the defaults.
func (a *Agent) Settings() Settings {
	a.mu.Lock()
	defer a.mu.Unlock()

	return a.settings
}

// ActiveRuns lists the Runs the Agent currently owns, oldest first.
func (a *Agent) ActiveRuns() []*Run {
	return a.registry.all()
}

func (a *Agent) workRequest() protocol.WorkRequest {
	free := a.cfg.MaxConcurrentRuns - a.registry.count()

	return protocol.WorkRequest{
		WaitSeconds:     int(a.Settings().PollWait / time.Second),
		Slots:           min(max(free, 0), MaxSlots),
		ActiveRuns:      a.registry.active(),
		MachineMetadata: a.info.WorkMetadata(a.cfg.InsecureHTTP),
	}
}

// delayAfter decides how long to wait before the next work request. A
// rejected credential or protocol cannot be fixed by waiting, so those
// retry slowly with a message an operator can act on.
func (a *Agent) delayAfter(err error) time.Duration {
	var apiErr *client.APIError

	switch {
	case client.IsUnauthorized(err):
		a.logger.Error("credential rejected; run omj-agent enroll again", "retry_in", client.AuthRetryInterval)

		return client.AuthRetryInterval
	case client.IsUnsupportedProtocol(err) && errors.As(err, &apiErr):
		a.logger.Error("server refused this agent version; update the agent or the server",
			"code", apiErr.Code,
			"supported_protocol_versions", apiErr.SupportedProtocolVersions,
			"min_agent_version", apiErr.MinAgentVersion,
			"agent_protocol_version", protocol.ProtocolVersion,
			"retry_in", client.AuthRetryInterval)

		return client.AuthRetryInterval
	case client.IsRetryable(err):
		delay := max(a.backoff.Next(), client.RetryAfter(err))
		a.logger.Warn("server unreachable; retrying", "error", err, "retry_in", delay)

		return delay
	default:
		delay := a.backoff.Next()
		a.logger.Error("work request rejected; retrying", "error", err, "retry_in", delay)

		return delay
	}
}

func (a *Agent) apply(received protocol.AgentConfig) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if received.HeartbeatIntervalSeconds > 0 {
		a.settings.HeartbeatInterval = time.Duration(received.HeartbeatIntervalSeconds) * time.Second
	}

	if received.OutputFlushIntervalMS > 0 {
		a.settings.FlushInterval = time.Duration(received.OutputFlushIntervalMS) * time.Millisecond
	}

	if received.OutputChunkBytes > 0 {
		a.settings.ChunkBytes = received.OutputChunkBytes
	}

	if received.PollWaitSeconds > 0 {
		a.settings.PollWait = min(time.Duration(received.PollWaitSeconds)*time.Second, MaxPollWait)
	}
}

func (a *Agent) cancel(ids []string) {
	for _, id := range ids {
		run, ok := a.registry.get(id)
		if !ok || run.Process == nil {
			continue
		}

		a.logger.Info("cancellation requested", "run_id", id)
		run.Process.Cancel(CancelRequested)
	}
}

func (a *Agent) launch(run *Run) {
	a.registry.add(run)
	a.runs.Add(1)

	go func() {
		defer a.runs.Done()
		defer a.registry.remove(run.Lease.RunID)

		// The reporter outlives the polling context: an Agent that is
		// stopping still has to tell the Server how its Runs ended.
		a.reporter.Report(context.Background(), run)
	}()
}

func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// OutcomeOf translates how a process ended into what the finish report and
// the state file record.
func OutcomeOf(result runner.Result) (protocol.RunStatus, *int, *protocol.FinishReason) {
	code := result.ExitCode

	switch {
	case result.TimedOut:
		return protocol.RunStatusTimedOut, &code, nil
	case result.Cancelled:
		if result.Reason == string(protocol.ReasonAgentStopped) {
			reason := protocol.ReasonAgentStopped

			return protocol.RunStatusCancelled, &code, &reason
		}

		return protocol.RunStatusCancelled, &code, nil
	case result.ExitCode == 0 && result.Err == nil:
		return protocol.RunStatusSuccess, &code, nil
	default:
		return protocol.RunStatusFailed, &code, nil
	}
}

// waitReporter stands in until task 012: it waits for the process, records
// the outcome locally and releases the Run's memory.
type waitReporter struct {
	agent *Agent
}

func (r waitReporter) Report(_ context.Context, run *Run) {
	var outcome state.Outcome

	switch {
	case run.CancelledBeforeStart:
		outcome = state.Outcome{Status: string(protocol.RunStatusCancelled)}
	case run.SpawnErr != nil:
		outcome = state.Outcome{Status: string(protocol.RunStatusFailed)}
	default:
		status, code, _ := OutcomeOf(run.Process.Wait())
		outcome = state.Outcome{Status: string(status), ExitCode: code}
	}

	run.Chunker.Close()

	if err := r.agent.state.MarkFinished(run.Lease.RunID, outcome); err != nil {
		r.agent.logger.Error("state not saved", "run_id", run.Lease.RunID, "error", err)
	}

	r.agent.buffer.Forget(run.Lease.RunID)
}

type noResender struct{}

func (noResender) Resend(context.Context, protocol.RunLease, state.RecentRun) error {
	return nil
}
