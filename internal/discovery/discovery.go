// Package discovery reads the scheduled work a Machine already has — crontabs
// and systemd timers — and reports it as written.
//
// The package is read-only by construction: it reaches the filesystem through
// os.ReadFile and os.ReadDir alone, and systemd through the Systemctl
// interface, which can only query. TestPackageOnlyReads reads this package's
// own source and fails if that ever stops being true.
package discovery

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

const (
	// MaxEntries and MaxBytes bound one discovery. What does not fit is
	// counted in OmittedEntries rather than left out quietly.
	MaxEntries = 500
	MaxBytes   = 512 << 10

	// entryOverhead is what JSON framing costs an entry on top of its own
	// text, so the payload still fits MaxBytes once it is encoded.
	entryOverhead = 128

	agentBinary = "omj-agent"
)

// ScheduleKind says how a schedule is written: cron's five fields, a systemd
// calendar expression, or a systemd timer counting from an event such as boot.
type ScheduleKind string

const (
	KindCron      ScheduleKind = "cron"
	KindCalendar  ScheduleKind = "calendar"
	KindMonotonic ScheduleKind = "monotonic"
)

// Entry is one piece of scheduled work as its source states it. Raw is the
// text the Agent read, kept even when nothing else could be understood.
type Entry struct {
	Source       string
	Raw          string
	Schedule     string
	ScheduleKind ScheduleKind
	Timezone     string
	Command      string
	User         string

	// Unit is the systemd unit a timer triggers, and is empty for crontabs.
	Unit string

	// IsAgent marks work that runs OMJ's own Agent, so an import cannot
	// schedule the Agent that would run it.
	IsAgent bool

	Unparseable bool

	// Note says why an entry is unparseable, or what had to be interpreted.
	Note string
}

// Unreadable is a source that is there but could not be read, named so the
// report says what it could not see instead of passing over it.
type Unreadable struct {
	Source string
	Reason string
}

type Result struct {
	Entries        []Entry
	Unreadable     []Unreadable
	OmittedEntries int
}

type Paths struct {
	SystemCrontab string
	CronDir       string
	SpoolDirs     []string
}

func (p Paths) empty() bool {
	return p.SystemCrontab == "" && p.CronDir == "" && len(p.SpoolDirs) == 0
}

// Collector reads the local Machine when zero-valued. Paths are taken
// together: a Collector that names one path names them all, so a caller that
// points the collector at its own files never falls back to the real /etc.
type Collector struct {
	Paths      Paths
	Systemctl  Systemctl
	AgentPath  string
	MaxEntries int
	MaxBytes   int
	Logger     *slog.Logger
}

func Collect(ctx context.Context) (Result, error) {
	return Collector{}.Collect(ctx)
}

// Collect reports what it could read and names what it could not; only a
// cancelled context stops it with an error.
func (c Collector) Collect(ctx context.Context) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	col := &collection{collector: c.withDefaults(), entries: []Entry{}, unreadable: []Unreadable{}}

	col.readSystemCrontab()
	col.readCronDirectory()
	col.readUserCrontabs()

	if err := col.readTimers(ctx); err != nil {
		return Result{}, err
	}

	return Result{Entries: col.entries, Unreadable: col.unreadable, OmittedEntries: col.omitted}, nil
}

func (c Collector) withDefaults() Collector {
	if c.Paths.empty() {
		c.Paths = DefaultPaths()
	}

	if c.Systemctl == nil {
		c.Systemctl = DefaultSystemctl()
	}

	if c.AgentPath == "" {
		c.AgentPath = agentPath()
	}

	if c.MaxEntries <= 0 {
		c.MaxEntries = MaxEntries
	}

	if c.MaxBytes <= 0 {
		c.MaxBytes = MaxBytes
	}

	if c.Logger == nil {
		c.Logger = slog.Default()
	}

	return c
}

// agentPath is the Agent's own binary, used to recognise entries that run it.
// An unknown path only costs the exact match; the name still marks them.
func agentPath() string {
	path, err := os.Executable()
	if err != nil {
		return ""
	}

	return path
}

type collection struct {
	collector  Collector
	entries    []Entry
	unreadable []Unreadable
	bytes      int
	omitted    int
	spent      bool
}

func (c *collection) size(e Entry) int {
	return entryOverhead + len(e.Source) + len(e.Raw) + len(e.Schedule) + len(e.ScheduleKind) +
		len(e.Timezone) + len(e.Command) + len(e.User) + len(e.Unit) + len(e.Note)
}

// add keeps an entry while the budget lasts. Once one entry has been left out
// every later one is too, so the report is the beginning of what the Machine
// schedules rather than whichever entries happened to be small. An entry too
// large to ever fit is the exception: it is counted and skipped, so one
// enormous line does not cost the rest of the discovery.
func (c *collection) add(e Entry) {
	size := c.size(e)

	if size > c.collector.MaxBytes {
		c.omitted++
		c.collector.Logger.Debug("scheduled work entry is larger than a whole discovery", "source", e.Source, "bytes", size)

		return
	}

	if c.spent || len(c.entries) >= c.collector.MaxEntries || c.bytes+size > c.collector.MaxBytes {
		c.spent = true
		c.omitted++

		return
	}

	c.bytes += size
	c.entries = append(c.entries, e)
}

func (c *collection) full() bool {
	return c.spent || len(c.entries) >= c.collector.MaxEntries
}

func (c *collection) unreadableSource(source string, err error) {
	if errors.Is(err, os.ErrNotExist) {
		c.collector.Logger.Debug("scheduled work source is absent", "source", source, "error", err)

		return
	}

	if len(c.unreadable) >= c.collector.MaxEntries {
		c.collector.Logger.Warn("unreadable sources are no longer being recorded", "source", source, "limit", c.collector.MaxEntries)

		return
	}

	c.unreadable = append(c.unreadable, Unreadable{Source: source, Reason: err.Error()})
}

// runsAgent reports whether an entry starts OMJ's own Agent. It leans towards
// marking: a false positive is a warning on an import screen, a false negative
// is a Job that runs the Agent that runs it.
func runsAgent(command, unit, executable string) bool {
	if unit != "" && strings.TrimSuffix(unit, filepath.Ext(unit)) == agentBinary {
		return true
	}

	for _, field := range strings.Fields(command) {
		token := strings.Trim(field, `"'`)
		if token == "" {
			continue
		}

		if (executable != "" && token == executable) || filepath.Base(token) == agentBinary {
			return true
		}
	}

	return false
}
