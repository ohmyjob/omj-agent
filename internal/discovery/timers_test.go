package discovery

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixtureListing = "testdata/systemctl/list-timers.txt"

// fakeSystemctl answers from the fixture directory the way systemctl answers
// from the system, and records what it was asked so a test can prove the
// package only ever reads.
type fakeSystemctl struct {
	dir     string
	listing string
	listErr error
	calls   []string
}

func (f *fakeSystemctl) Available() bool { return true }

func (f *fakeSystemctl) ListTimers(context.Context) (string, error) {
	f.calls = append(f.calls, "list-timers")

	return f.listing, f.listErr
}

func (f *fakeSystemctl) Show(_ context.Context, unit string, properties ...string) (string, error) {
	f.calls = append(f.calls, "show "+unit+" "+strings.Join(properties, ","))

	data, err := os.ReadFile(filepath.Join(f.dir, unit))
	if err != nil {
		return "", fmt.Errorf("unit %s could not be found", unit)
	}

	return string(data), nil
}

func TestCollectReadsSystemdTimers(t *testing.T) {
	result, systemctl := collectTimers(t, readFixture(t, fixtureListing))

	want := []Entry{
		{Source: "systemd:apt-daily.timer", Schedule: "*-*-* 06,18:00:00", ScheduleKind: KindCalendar, Command: "/usr/lib/apt/apt.systemd.daily update", User: "root", Unit: "apt-daily.service"},
		{Source: "systemd:logrotate.timer", Schedule: "*-*-* 00:00:00", ScheduleKind: KindCalendar, Command: "/usr/sbin/logrotate /etc/logrotate.conf", User: "root", Unit: "logrotate.service"},
		{Source: "systemd:logrotate.timer", Schedule: "OnBootUSec=15min", ScheduleKind: KindMonotonic, Command: "/usr/sbin/logrotate /etc/logrotate.conf", User: "root", Unit: "logrotate.service"},
		{
			Source: "systemd:backup.timer", Schedule: "Sat *-*-* 03:15:00 Europe/Lisbon", ScheduleKind: KindCalendar, Timezone: "Europe/Lisbon",
			Command: "/usr/bin/borg create ::daily /srv ; /usr/bin/borg prune --keep-daily 7", User: "deploy", Unit: "backup.service",
			Note: "backup.service runs 2 commands in order, joined here with a semicolon",
		},
		{Source: "systemd:omj-report.timer", Schedule: "daily", ScheduleKind: KindCalendar, Command: "/usr/local/bin/omj-agent status", User: "ohmyjob", Unit: "omj-report.service", IsAgent: true},
		{
			Source: "systemd:broken-schedule.timer", Command: "/usr/local/bin/nightly --once", User: "root", Unit: "broken-schedule.service",
			Unparseable: true, Note: "systemd reports neither a calendar nor a monotonic schedule for this timer",
		},
		{
			Source: "systemd:no-command.timer", Schedule: "Mon..Fri 09:00", ScheduleKind: KindCalendar, User: "root", Unit: "no-command.service",
			Unparseable: true, Note: "systemd reports no command for no-command.service",
		},
		{Source: "systemd", Unparseable: true, Note: "systemctl list-timers printed a row without a .timer unit in it"},
	}

	assertEntries(t, entriesWithoutRaw(t, result.Entries, fixtureListing), want)

	if len(result.Unreadable) != 1 || result.Unreadable[0].Source != "systemd:vanished.timer" {
		t.Errorf("unreadable = %+v, want the timer systemctl could not describe", result.Unreadable)
	}

	for _, call := range systemctl.calls {
		if call != "list-timers" && !strings.HasPrefix(call, "show ") {
			t.Errorf("systemctl was asked %q, and this package may only list and show", call)
		}
	}
}

func TestCollectDegradesWithoutSystemd(t *testing.T) {
	result, err := Collector{Paths: crontabFixtures(), Systemctl: noSystemctl{}, Logger: discardLogger()}.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	if len(result.Entries) == 0 {
		t.Fatal("no entries, want the crontabs a machine without systemd still has")
	}

	for _, entry := range result.Entries {
		if strings.HasPrefix(entry.Source, timerSourcePrefix) {
			t.Errorf("entry from %q, want crontabs only", entry.Source)
		}
	}

	if len(result.Unreadable) != 0 {
		t.Errorf("unreadable = %+v, want no complaint about the systemd this machine does not have", result.Unreadable)
	}
}

func TestCollectRecordsAnUnusableSystemctl(t *testing.T) {
	systemctl := &fakeSystemctl{dir: "testdata/systemctl", listErr: fmt.Errorf("exit status 1: %s", "Failed to connect to bus: No such file or directory")}

	result, err := Collector{Paths: noCrontabs(t), Systemctl: systemctl, Logger: discardLogger()}.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	if len(result.Unreadable) != 1 || result.Unreadable[0].Source != "systemd" || !strings.Contains(result.Unreadable[0].Reason, "Failed to connect to bus") {
		t.Errorf("unreadable = %+v, want systemd named with the reason systemctl gave", result.Unreadable)
	}
}

func TestCollectStopsWhenTheContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := (Collector{Paths: crontabFixtures(), Systemctl: noSystemctl{}}).Collect(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Collect() error = %v, want %v", err, context.Canceled)
	}
}

func TestTimerRow(t *testing.T) {
	tests := []struct {
		name          string
		row           string
		wantTimer     string
		wantActivates string
		wantOK        bool
	}{
		{
			name:      "unit and activates are the last two columns",
			row:       "Sat 2026-09-06 06:12:34 UTC  6h left  Fri 2026-09-05 06:11:03 UTC  17h ago  apt-daily.timer  apt-daily.service",
			wantTimer: "apt-daily.timer", wantActivates: "apt-daily.service", wantOK: true,
		},
		{
			name:      "a timer that activates nothing",
			row:       "n/a  n/a  Fri 2026-09-05 06:11:03 UTC  17h ago  lonely.timer",
			wantTimer: "lonely.timer", wantOK: true,
		},
		{
			name:      "a dash in place of the activated unit",
			row:       "n/a  n/a  n/a  n/a  lonely.timer  -",
			wantTimer: "lonely.timer", wantOK: true,
		},
		{name: "a row without a timer", row: "a row systemd should never have printed"},
		{name: "an empty row"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			timer, activates, ok := timerRow(tt.row)

			if timer != tt.wantTimer || activates != tt.wantActivates || ok != tt.wantOK {
				t.Errorf("timerRow(%q) = %q, %q, %v, want %q, %q, %v", tt.row, timer, activates, ok, tt.wantTimer, tt.wantActivates, tt.wantOK)
			}
		})
	}
}

func TestIsListTimersFooter(t *testing.T) {
	tests := []struct {
		row  string
		want bool
	}{
		{row: "7 timers listed.", want: true},
		{row: "1 timer listed.", want: true},
		{row: "No timers running.", want: true},
		{row: "Pass --all to see loaded but inactive timers.", want: true},
		{row: "Sat 2026-09-06 06:12:34 UTC 6h left n/a n/a apt-daily.timer apt-daily.service", want: false},
		{row: "7 timers", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.row, func(t *testing.T) {
			if got := isListTimersFooter(tt.row); got != tt.want {
				t.Errorf("isListTimersFooter(%q) = %v, want %v", tt.row, got, tt.want)
			}
		})
	}
}

func TestCalendarTimezone(t *testing.T) {
	tests := []struct {
		expression string
		want       string
	}{
		{expression: "Sat *-*-* 03:15:00 Europe/Lisbon", want: "Europe/Lisbon"},
		{expression: "*-*-* 06:00:00 UTC", want: "UTC"},
		{expression: "*-*-* 06,18:00:00"},
		{expression: "daily"},
		{expression: "Mon..Fri 09:00"},
		{expression: ""},
	}

	for _, tt := range tests {
		t.Run(tt.expression, func(t *testing.T) {
			if got := calendarTimezone(tt.expression); got != tt.want {
				t.Errorf("calendarTimezone(%q) = %q, want %q", tt.expression, got, tt.want)
			}
		})
	}
}

func TestExecStart(t *testing.T) {
	tests := []struct {
		name      string
		property  []string
		want      string
		wantCount int
	}{
		{
			name:      "the argument list is the command",
			property:  []string{"{ path=/usr/bin/borg ; argv[]=/usr/bin/borg create ::daily /srv ; ignore_errors=no ; start_time=[n/a] }"},
			want:      "/usr/bin/borg create ::daily /srv",
			wantCount: 1,
		},
		{
			name:      "a semicolon inside the command is kept",
			property:  []string{`{ path=/bin/sh ; argv[]=/bin/sh -c cd /srv ; ./run ; ignore_errors=no ; start_time=[n/a] }`},
			want:      "/bin/sh -c cd /srv ; ./run",
			wantCount: 1,
		},
		{
			name:      "commands run in order are joined in order",
			property:  []string{"{ path=/usr/bin/a ; argv[]=/usr/bin/a ; ignore_errors=no }", "{ path=/usr/bin/b ; argv[]=/usr/bin/b --last ; ignore_errors=no }"},
			want:      "/usr/bin/a ; /usr/bin/b --last",
			wantCount: 2,
		},
		{
			name:      "the path stands in when there is no argument list",
			property:  []string{"{ path=/usr/bin/only ; ignore_errors=no }"},
			want:      "/usr/bin/only",
			wantCount: 1,
		},
		{name: "a unit with no command", property: []string{""}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			command, count := execStart(map[string][]string{"ExecStart": tt.property})

			if command != tt.want || count != tt.wantCount {
				t.Errorf("execStart() = %q, %d, want %q, %d", command, count, tt.want, tt.wantCount)
			}
		})
	}
}

func TestShowProperties(t *testing.T) {
	properties := showProperties("Unit=logrotate.service\nTimersCalendar={ OnCalendar=*-*-* 00:00:00 ; next_elapse=n/a }\nTimersMonotonic=\nnot a property\n")

	if got := first(properties["Unit"]); got != "logrotate.service" {
		t.Errorf("Unit = %q, want logrotate.service", got)
	}

	if got := first(properties["TimersMonotonic"]); got != "" {
		t.Errorf("TimersMonotonic = %q, want an empty value", got)
	}

	if _, ok := properties["not a property"]; ok {
		t.Error("a line without an equals sign became a property")
	}
}

func collectTimers(t *testing.T, listing string) (Result, *fakeSystemctl) {
	t.Helper()

	systemctl := &fakeSystemctl{dir: "testdata/systemctl", listing: listing}

	result, err := Collector{Paths: noCrontabs(t), Systemctl: systemctl, AgentPath: "/usr/local/bin/omj-agent", Logger: discardLogger()}.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	return result, systemctl
}

func readFixture(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	return string(data)
}
