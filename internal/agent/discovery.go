package agent

import (
	"context"
	"sync"

	"github.com/ohmyjob/omj-agent/internal/client"
	"github.com/ohmyjob/omj-agent/internal/discovery"
	"github.com/ohmyjob/omj-agent/internal/protocol"
)

// Collector is what answers a discovery request. The Agent holds one so a
// test can report a known Machine without one on disk.
type Collector interface {
	Collect(ctx context.Context) (discovery.Result, error)
}

// discoveries keeps one report at a time off the poll loop. unsupported is
// set by a 404 and never cleared: a Server without the endpoint will not grow
// one while this process runs, so asking again only fills the log.
type discoveries struct {
	mu          sync.Mutex
	inFlight    bool
	unsupported bool

	wg sync.WaitGroup
}

func (d *discoveries) begin() bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.inFlight || d.unsupported {
		return false
	}

	d.inFlight = true

	return true
}

func (d *discoveries) end(unsupported bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.inFlight = false

	if unsupported {
		d.unsupported = true
	}
}

// discoverIfAsked answers a discovery request in its own goroutine, because
// collecting reads every crontab and queries systemd, and the poll loop owes
// its Runs their heartbeats meanwhile.
//
// It takes the poll context rather than the reporters' one, so a stop ends a
// discovery in flight instead of holding shutdown open for it. A Run's
// outcome is the only thing worth the stop budget: a discovery is evidence
// the Server asks for again, and nothing is lost by not sending it now.
func (a *Agent) discoverIfAsked(ctx context.Context, requested bool) {
	if !requested || !a.discoveries.begin() {
		return
	}

	a.discoveries.wg.Add(1)

	go func() {
		defer a.discoveries.wg.Done()

		a.discoveries.end(a.report(ctx))
	}()
}

// report answers with whether this Server turned out not to implement
// discovery at all, which is the one failure worth remembering.
func (a *Agent) report(ctx context.Context) (unsupported bool) {
	result, err := a.collector().Collect(ctx)
	if err != nil {
		if ctx.Err() == nil {
			a.logger.Error("scheduled work could not be read", "error", err)
		}

		return false
	}

	request := DiscoveryPayload(result)

	response, err := a.client.Discovery(ctx, request)

	switch {
	case err == nil:
		a.logger.Info("reported what this machine already schedules",
			"entries", response.Entries,
			"omitted_entries", request.OmittedEntries,
			"unreadable_sources", len(request.UnreadableSources))

		return false
	case ctx.Err() != nil:
		return false
	case client.IsNotFound(err):
		a.logger.Info("this server does not collect scheduled work; not asking again")

		return true
	default:
		a.logger.Warn("scheduled work was not reported", "error", err)

		return false
	}
}

func (a *Agent) collector() Collector {
	if a.discoverer != nil {
		return a.discoverer
	}

	return discovery.Collector{Logger: a.logger}
}

// DiscoveryPayload is the wire form of what a collection found. The two
// halves are kept apart on purpose: internal/discovery may not import the
// protocol, because the test that proves it only reads names every package it
// is allowed to touch.
func DiscoveryPayload(result discovery.Result) protocol.DiscoveryRequest {
	entries := make([]protocol.DiscoveredEntry, 0, len(result.Entries))
	for _, entry := range result.Entries {
		entries = append(entries, protocol.DiscoveredEntry{
			Source:       entry.Source,
			Raw:          entry.Raw,
			Schedule:     optional(entry.Schedule),
			ScheduleKind: optional(string(entry.ScheduleKind)),
			Timezone:     optional(entry.Timezone),
			Command:      optional(entry.Command),
			RunAs:        optional(entry.User),
			Unit:         optional(entry.Unit),
			IsAgent:      entry.IsAgent,
			Unparseable:  entry.Unparseable,
			Note:         optional(entry.Note),
		})
	}

	sources := make([]protocol.UnreadableSource, 0, len(result.Unreadable))
	for _, source := range result.Unreadable {
		sources = append(sources, protocol.UnreadableSource{Source: source.Source, Reason: source.Reason})
	}

	return protocol.DiscoveryRequest{
		Truncated:         result.OmittedEntries > 0,
		OmittedEntries:    result.OmittedEntries,
		UnreadableSources: sources,
		Entries:           entries,
	}
}

// optional distinguishes a field the source did not state from one it stated
// as empty; the Server's schema is nullable rather than defaulted.
func optional(value string) *string {
	if value == "" {
		return nil
	}

	return &value
}
