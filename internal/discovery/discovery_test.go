package discovery

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

var crontabFixtureFiles = []string{
	"testdata/etc/crontab",
	"testdata/etc/cron.d/certbot",
	"testdata/etc/cron.d/statistics",
	"testdata/spool/deploy",
	"testdata/spool/root",
}

func TestCollectReadsEveryCrontab(t *testing.T) {
	result := collectCrontabs(t, Collector{Paths: crontabFixtures(), Systemctl: noSystemctl{}, AgentPath: "/usr/local/bin/omj-agent", Logger: discardLogger()})

	want := []Entry{
		{Source: "crontab:testdata/etc/crontab", Schedule: "17 * * * *", ScheduleKind: KindCron, Command: "cd / && run-parts --report /etc/cron.hourly", User: "root"},
		{Source: "crontab:testdata/etc/crontab", Schedule: "25 6 * * *", ScheduleKind: KindCron, Command: "test -x /usr/sbin/anacron || { cd / && run-parts --report /etc/cron.daily; }", User: "root"},
		{Source: "crontab:testdata/etc/crontab", Schedule: "30 4 * * 7", ScheduleKind: KindCron, Timezone: "Europe/Lisbon", Command: "/usr/local/bin/weekly-backup.sh", User: "backup"},
		{Source: "crontab:testdata/etc/crontab", Schedule: "@daily", ScheduleKind: KindCron, Timezone: "Europe/Lisbon", Command: "/usr/local/bin/omj-agent discover", User: "root", IsAgent: true},
		{
			Source: "crontab:testdata/etc/crontab", Schedule: "*/5 * * * *", ScheduleKind: KindCron, Timezone: "Europe/Lisbon",
			Unparseable: true, Note: `"/usr/local/bin/no-user-in-this-line" is not a user name, and this file names the user between the schedule and the command`,
		},
		{
			Source: "crontab:testdata/etc/crontab", ScheduleKind: KindCron, Timezone: "Europe/Lisbon",
			Unparseable: true, Note: `the minute "70" is outside 0-59`,
		},
		{
			Source: "crontab:testdata/etc/cron.d/certbot", Schedule: "0 */12 * * *", ScheduleKind: KindCron, User: "root",
			Command: `test -x /usr/bin/certbot -a \! -d /run/systemd/system && perl -e 'sleep int(rand(43200))' && certbot -q renew`,
		},
		{Source: "crontab:testdata/etc/cron.d/statistics", Schedule: "*/5 * * * *", ScheduleKind: KindCron, Command: "/usr/local/bin/collect --quiet", User: "stats"},
		{Source: "crontab:testdata/etc/cron.d/statistics", Schedule: "0 0 * * mon-fri,sat", ScheduleKind: KindCron, Command: "/usr/local/bin/roll", User: "stats"},
		{
			Source: "crontab:testdata/etc/cron.d/statistics", Schedule: "17 3 * * *", ScheduleKind: KindCron, User: "stats",
			Unparseable: true, Note: "the line has a schedule but no command",
		},
		{Source: "crontab:deploy", Schedule: "*/10 * * * *", ScheduleKind: KindCron, Command: "cd /srv/app && ./bin/queue-tick", User: "deploy"},
		{
			Source: "crontab:deploy", ScheduleKind: KindCron, User: "deploy",
			Unparseable: true, Note: `the minute "this" is neither a number nor a name cron knows`,
		},
		{Source: "crontab:root", Schedule: "0 3 * * *", ScheduleKind: KindCron, Command: "/usr/local/bin/tidy-logs --quiet", User: "root"},
		{Source: "crontab:root", Schedule: "@reboot", ScheduleKind: KindCron, Command: "/usr/local/bin/warm-cache", User: "root"},
	}

	assertEntries(t, entriesWithoutRaw(t, result.Entries, crontabFixtureFiles...), want)

	if result.OmittedEntries != 0 || len(result.Unreadable) != 0 {
		t.Errorf("omitted %d entries and could not read %+v, want all of them read", result.OmittedEntries, result.Unreadable)
	}
}

func TestCollectSkipsFilesCronItselfIgnores(t *testing.T) {
	result := collectCrontabs(t, Collector{Paths: crontabFixtures(), Systemctl: noSystemctl{}, Logger: discardLogger()})

	for _, entry := range result.Entries {
		if strings.Contains(entry.Source, "dpkg-dist") {
			t.Errorf("entry from %q, which cron does not run because of its name", entry.Source)
		}
	}
}

func TestCollectNamesWhatItCouldNotRead(t *testing.T) {
	paths := Paths{
		// A directory where a crontab should be and a file where a spool
		// directory should be are both there, and neither can be read.
		SystemCrontab: "testdata/etc",
		CronDir:       filepath.Join(t.TempDir(), "cron.d"),
		SpoolDirs:     []string{"testdata/etc/crontab"},
	}

	result, err := Collector{Paths: paths, Systemctl: noSystemctl{}, Logger: discardLogger()}.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	if len(result.Entries) != 0 {
		t.Errorf("entries = %+v, want none", result.Entries)
	}

	// The absent cron.d is not a failure, so it is not in the list.
	want := []string{"crontab:testdata/etc", "crontab:testdata/etc/crontab"}

	var got []string
	for _, source := range result.Unreadable {
		got = append(got, source.Source)

		if source.Reason == "" {
			t.Errorf("%q was reported unreadable without a reason", source.Source)
		}
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("unreadable = %v, want %v", got, want)
	}
}

func TestCollectStopsAtTheEntryLimit(t *testing.T) {
	full := collectCrontabs(t, Collector{Paths: crontabFixtures(), Systemctl: noSystemctl{}, Logger: discardLogger()})

	limited := collectCrontabs(t, Collector{Paths: crontabFixtures(), Systemctl: noSystemctl{}, MaxEntries: 3, Logger: discardLogger()})

	if len(limited.Entries) != 3 {
		t.Errorf("kept %d entries, want 3", len(limited.Entries))
	}

	if want := len(full.Entries) - 3; limited.OmittedEntries != want {
		t.Errorf("omitted %d entries, want %d", limited.OmittedEntries, want)
	}
}

func TestCollectStopsAtTheByteLimit(t *testing.T) {
	full := collectCrontabs(t, Collector{Paths: crontabFixtures(), Systemctl: noSystemctl{}, Logger: discardLogger()})

	budget := (&collection{}).size(full.Entries[0])

	limited := collectCrontabs(t, Collector{Paths: crontabFixtures(), Systemctl: noSystemctl{}, MaxBytes: budget, Logger: discardLogger()})

	if len(limited.Entries) != 1 {
		t.Errorf("kept %d entries, want the one that fitted", len(limited.Entries))
	}

	if want := len(full.Entries) - 1; limited.OmittedEntries != want {
		t.Errorf("omitted %d entries, want %d", limited.OmittedEntries, want)
	}
}

func TestBudget(t *testing.T) {
	small := Entry{Source: "crontab:root", Raw: "0 3 * * * /usr/local/bin/tidy-logs"}

	t.Run("nothing is kept after the first entry left out", func(t *testing.T) {
		col := &collection{collector: Collector{MaxEntries: 2, MaxBytes: MaxBytes, Logger: discardLogger()}}

		col.add(small)
		col.add(small)
		col.add(small)

		if len(col.entries) != 2 || col.omitted != 1 || !col.full() {
			t.Errorf("kept %d, omitted %d, full %v, want 2, 1, true", len(col.entries), col.omitted, col.full())
		}
	})

	t.Run("an entry that cannot fit any discovery is counted and skipped", func(t *testing.T) {
		col := &collection{collector: Collector{MaxEntries: 10, MaxBytes: 2 * entryOverhead, Logger: discardLogger()}}

		col.add(Entry{Source: "crontab:root", Raw: strings.Repeat("x", 4*entryOverhead)})
		col.add(small)

		if len(col.entries) != 1 || col.omitted != 1 {
			t.Errorf("kept %d, omitted %d, want the small entry kept and the huge one counted", len(col.entries), col.omitted)
		}
	})
}

func TestCollectChangesNothing(t *testing.T) {
	before := fingerprint(t, "testdata")

	systemctl := &fakeSystemctl{dir: "testdata/systemctl", listing: readFixture(t, fixtureListing)}

	if _, err := (Collector{Paths: crontabFixtures(), Systemctl: systemctl, Logger: discardLogger()}).Collect(context.Background()); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	if after := fingerprint(t, "testdata"); after != before {
		t.Error("the files under testdata changed, and a discovery must leave every machine exactly as it found it")
	}
}

func TestRunsAgent(t *testing.T) {
	const executable = "/tmp/build/omj-agent"

	tests := []struct {
		name    string
		command string
		unit    string
		want    bool
	}{
		{name: "the installed binary", command: "/usr/local/bin/omj-agent run", want: true},
		{name: "found on the path", command: "omj-agent status", want: true},
		{name: "quoted inside a shell command", command: `sh -c "omj-agent discover"`, want: true},
		{name: "this very binary", command: executable + " run", want: true},
		{name: "the agent's own unit", unit: "omj-agent.service", want: true},
		{name: "a unit that only starts like it", unit: "omj-agent-report.service", command: "/usr/local/bin/report", want: false},
		{name: "a binary that only starts like it", command: "/opt/omj/omj-agent-helper --once", want: false},
		{name: "someone else's work", command: "/usr/bin/borg create ::daily /srv", unit: "backup.service", want: false},
		{name: "nothing at all", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := runsAgent(tt.command, tt.unit, executable); got != tt.want {
				t.Errorf("runsAgent(%q, %q) = %v, want %v", tt.command, tt.unit, got, tt.want)
			}
		})
	}
}

func TestDefaultPaths(t *testing.T) {
	paths := DefaultPaths()

	if runtime.GOOS != "linux" {
		if !paths.empty() {
			t.Errorf("DefaultPaths() = %+v on %s, want nothing until that platform's layout is added", paths, runtime.GOOS)
		}

		return
	}

	if paths.SystemCrontab != "/etc/crontab" || paths.CronDir != "/etc/cron.d" || len(paths.SpoolDirs) == 0 {
		t.Errorf("DefaultPaths() = %+v, want the places Linux keeps crontabs", paths)
	}
}

func crontabFixtures() Paths {
	return Paths{SystemCrontab: "testdata/etc/crontab", CronDir: "testdata/etc/cron.d", SpoolDirs: []string{"testdata/spool"}}
}

// noCrontabs points a collector at paths that do not exist, so a test about
// systemd reads no crontabs, least of all the ones this machine really has.
func noCrontabs(t *testing.T) Paths {
	t.Helper()

	dir := t.TempDir()

	return Paths{SystemCrontab: filepath.Join(dir, "crontab"), CronDir: filepath.Join(dir, "cron.d"), SpoolDirs: []string{filepath.Join(dir, "spool")}}
}

func collectCrontabs(t *testing.T, collector Collector) Result {
	t.Helper()

	result, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	return result
}

func assertEntries(t *testing.T, got, want []Entry) {
	t.Helper()

	if len(got) != len(want) {
		for i, entry := range got {
			t.Logf("entry %d: %+v", i, entry)
		}

		t.Fatalf("got %d entries, want %d", len(got), len(want))
	}

	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("entry %d:\n got %+v\nwant %+v", i, got[i], want[i])
		}
	}
}

// entriesWithoutRaw checks that every entry kept text that appears in the
// source it names, and returns the entries without it so the tables stay
// readable.
func entriesWithoutRaw(t *testing.T, entries []Entry, files ...string) []Entry {
	t.Helper()

	var sources strings.Builder
	for _, file := range files {
		sources.WriteString(readFixture(t, file))
	}

	kept := make([]Entry, len(entries))

	for i, entry := range entries {
		if entry.Raw == "" || !strings.Contains(sources.String(), entry.Raw) {
			t.Errorf("entry %d kept %q, which is not text its source holds", i, entry.Raw)
		}

		entry.Raw = ""
		kept[i] = entry
	}

	return kept
}

// fingerprint is the content and mode of every file in a tree, so a test can
// say that reading it changed none of them.
func fingerprint(t *testing.T, root string) string {
	t.Helper()

	sum := sha256.New()

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}

		if entry.IsDir() {
			fmt.Fprintf(sum, "%s %o\n", path, info.Mode())

			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		fmt.Fprintf(sum, "%s %o %d %x\n", path, info.Mode(), info.Size(), sha256.Sum256(data))

		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}

	return fmt.Sprintf("%x", sum.Sum(nil))
}

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}
