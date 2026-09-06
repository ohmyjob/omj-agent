package discovery

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

const timerSourcePrefix = "systemd:"

// Systemctl answers questions about systemd. Nothing in this interface can
// start, stop, enable or edit a unit, which is how the package stays read-only
// even though it runs a program.
type Systemctl interface {
	Available() bool
	ListTimers(ctx context.Context) (string, error)
	Show(ctx context.Context, unit string, properties ...string) (string, error)
}

type systemctl struct {
	path string
}

type noSystemctl struct{}

// DefaultSystemctl runs systemctl when it is on the PATH and reports systemd
// as absent otherwise, so a Machine without it degrades to crontabs alone.
func DefaultSystemctl() Systemctl {
	path, err := exec.LookPath("systemctl")
	if err != nil {
		return noSystemctl{}
	}

	return systemctl{path: path}
}

func (s systemctl) Available() bool { return true }

func (s systemctl) ListTimers(ctx context.Context) (string, error) {
	return s.run(ctx, "list-timers", "--all", "--no-pager", "--no-legend")
}

func (s systemctl) Show(ctx context.Context, unit string, properties ...string) (string, error) {
	args := make([]string, 0, len(properties)+3)
	args = append(args, "show", "--no-pager", unit)

	for _, property := range properties {
		args = append(args, "--property="+property)
	}

	return s.run(ctx, args...)
}

// run keeps systemctl's own explanation, which is the part of a failure worth
// reporting: "Failed to connect to bus" says more than "exit status 1".
func (s systemctl) run(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, s.path, args...).Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && len(exit.Stderr) > 0 {
			return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(exit.Stderr)))
		}

		return "", err
	}

	return string(out), nil
}

func (noSystemctl) Available() bool { return false }

func (noSystemctl) ListTimers(context.Context) (string, error) { return "", nil }

func (noSystemctl) Show(context.Context, string, ...string) (string, error) { return "", nil }

func (c *collection) readTimers(ctx context.Context) error {
	if !c.collector.Systemctl.Available() {
		c.collector.Logger.Debug("systemd timers skipped", "reason", "systemctl is not installed on this machine")

		return nil
	}

	listing, err := c.collector.Systemctl.ListTimers(ctx)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}

		c.unreadableSource("systemd", fmt.Errorf("systemctl list-timers: %w", err))

		return nil
	}

	for line := range strings.Lines(listing) {
		if err := ctx.Err(); err != nil {
			return err
		}

		row := strings.TrimSpace(line)
		if row == "" || isListTimersFooter(row) {
			continue
		}

		timer, activates, ok := timerRow(row)
		if !ok {
			c.add(Entry{Source: "systemd", Raw: row, Unparseable: true, Note: "systemctl list-timers printed a row without a .timer unit in it"})

			continue
		}

		// Reading a timer costs two systemctl calls, so once the discovery is
		// full the row is counted instead. A timer left out this way counts
		// once, however many schedules it turns out to have.
		if c.full() {
			c.omitted++

			continue
		}

		if err := c.readTimer(ctx, timer, activates, row); err != nil {
			return err
		}
	}

	return nil
}

func (c *collection) readTimer(ctx context.Context, timer, activates, row string) error {
	source := timerSourcePrefix + timer

	properties, err := c.show(ctx, timer, "Unit", "TimersCalendar", "TimersMonotonic")
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}

		c.unreadableSource(source, err)

		return nil
	}

	// systemd resolves the unit a timer triggers, including the .service it
	// falls back to when the timer names none; the ACTIVATES column is the
	// answer only when this systemd is too old to report the property.
	unit := first(properties["Unit"])
	if unit == "" {
		unit = activates
	}

	command, user, note, err := c.unitCommand(ctx, unit)
	if err != nil {
		return err
	}

	entry := Entry{Source: source, Raw: row, Unit: unit, Command: command, User: user, Note: note}
	entry.IsAgent = runsAgent(command, unit, c.collector.AgentPath)

	// A timer whose command is unknown is not importable, so it is reported
	// the same way an unreadable crontab line is: raw text and a reason.
	if entry.Command == "" {
		entry.Unparseable = true
	}

	schedules := timerSchedules(properties)
	if len(schedules) == 0 {
		entry.Unparseable = true
		entry.Note = joinNotes(entry.Note, "systemd reports neither a calendar nor a monotonic schedule for this timer")

		c.add(entry)

		return nil
	}

	for _, schedule := range schedules {
		scheduled := entry
		scheduled.Schedule = schedule.expression
		scheduled.ScheduleKind = schedule.kind
		scheduled.Timezone = schedule.timezone

		c.add(scheduled)
	}

	return nil
}

// The reason a unit could not be read becomes a note on the entry rather than
// a dropped entry.
func (c *collection) unitCommand(ctx context.Context, unit string) (command, user, note string, err error) {
	if unit == "" {
		return "", "", "this timer triggers no unit, so it has no command", nil
	}

	properties, showErr := c.show(ctx, unit, "ExecStart", "User")
	if showErr != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", "", "", ctxErr
		}

		c.unreadableSource(timerSourcePrefix+unit, showErr)

		return "", "", fmt.Sprintf("systemctl could not be asked what %s runs: %v", unit, showErr), nil
	}

	command, count := execStart(properties)

	switch {
	case count == 0:
		note = fmt.Sprintf("systemd reports no command for %s", unit)
	case count > 1:
		note = fmt.Sprintf("%s runs %d commands in order, joined here with a semicolon", unit, count)
	}

	return command, serviceUser(properties), note, nil
}

// show asks systemd for properties of a unit whose name systemd itself gave
// us. The name is checked all the same, so nothing that looks like an option
// can ever reach the command line.
func (c *collection) show(ctx context.Context, unit string, properties ...string) (map[string][]string, error) {
	if !isUnitName(unit) {
		return nil, fmt.Errorf("%q is not a unit name", unit)
	}

	out, err := c.collector.Systemctl.Show(ctx, unit, properties...)
	if err != nil {
		return nil, err
	}

	return showProperties(out), nil
}

// showProperties reads systemctl show's KEY=value lines. A property may appear
// more than once, ExecStart being the usual reason.
func showProperties(out string) map[string][]string {
	properties := map[string][]string{}

	for line := range strings.Lines(out) {
		key, value, ok := strings.Cut(strings.TrimRight(line, "\r\n"), "=")
		if !ok {
			continue
		}

		properties[key] = append(properties[key], value)
	}

	return properties
}

type timerSchedule struct {
	expression string
	kind       ScheduleKind
	timezone   string
}

// timerSchedules reads the schedules systemd reports for a timer. A timer with
// several of them becomes several entries, each carrying the unit and command
// it shares, because each schedule is one way the work is triggered.
func timerSchedules(properties map[string][]string) []timerSchedule {
	var schedules []timerSchedule

	for _, value := range properties["TimersCalendar"] {
		for _, group := range propertyGroups(value) {
			if _, expression := groupSchedule(group); expression != "" {
				schedules = append(schedules, timerSchedule{expression: expression, kind: KindCalendar, timezone: calendarTimezone(expression)})
			}
		}
	}

	for _, value := range properties["TimersMonotonic"] {
		for _, group := range propertyGroups(value) {
			// The anchor is half of a monotonic schedule, so it is kept:
			// fifteen minutes after boot reads as OnBootUSec=15min.
			if name, expression := groupSchedule(group); expression != "" {
				schedules = append(schedules, timerSchedule{expression: name + "=" + expression, kind: KindMonotonic})
			}
		}
	}

	return schedules
}

// propertyGroups splits the { ... } { ... } records systemctl prints for
// timers and commands.
func propertyGroups(value string) []string {
	var groups []string

	for rest := value; ; {
		open := strings.Index(rest, "{")
		if open < 0 {
			return groups
		}

		rest = rest[open+1:]

		end := strings.Index(rest, "}")
		if end < 0 {
			return groups
		}

		groups = append(groups, strings.TrimSpace(rest[:end]))
		rest = rest[end+1:]
	}
}

// groupSchedule reads the OnCalendar=... or OnBootUSec=... a group opens with,
// leaving the next_elapse systemd appends to it, which is a prediction rather
// than part of the schedule.
func groupSchedule(group string) (name, expression string) {
	head, _, _ := strings.Cut(group, " ; ")

	name, expression, ok := strings.Cut(head, "=")
	if !ok {
		return "", ""
	}

	return strings.TrimSpace(name), strings.TrimSpace(expression)
}

// calendarTimezone returns the timezone a calendar expression ends with, which
// systemd allows from v252 onwards (OnCalendar=Mon *-*-* 09:00 Europe/Lisbon).
func calendarTimezone(expression string) string {
	fields := strings.Fields(expression)
	if len(fields) == 0 {
		return ""
	}

	last := fields[len(fields)-1]
	if last == "UTC" {
		return last
	}

	if strings.Contains(last, "/") && !strings.ContainsAny(last, "0123456789*,:") {
		return last
	}

	return ""
}

// execStart reads the command line of a unit. A unit with several ExecStart
// lines runs them in order, and they are reported joined in that order with a
// note saying so.
func execStart(properties map[string][]string) (command string, count int) {
	var lines []string

	for _, value := range properties["ExecStart"] {
		for _, group := range propertyGroups(value) {
			if line := execArgv(group); line != "" {
				lines = append(lines, line)
			}
		}
	}

	return strings.Join(lines, " ; "), len(lines)
}

// execArgv takes the command line out of a group systemctl prints as
// { path=/usr/bin/foo ; argv[]=/usr/bin/foo --quiet ; ignore_errors=no ; ... },
// where the argument list is the command as the unit writes it.
func execArgv(group string) string {
	_, argv, ok := strings.Cut(group, "argv[]=")
	if !ok {
		return groupField(group, "path")
	}

	// ignore_errors is the field systemd prints after the arguments; the last
	// separator is the fallback for a systemd that prints something else,
	// because an argument may itself contain a semicolon.
	if i := strings.Index(argv, " ; ignore_errors="); i >= 0 {
		return strings.TrimSpace(argv[:i])
	}

	if i := strings.LastIndex(argv, " ; "); i >= 0 {
		return strings.TrimSpace(argv[:i])
	}

	return strings.TrimSpace(argv)
}

func groupField(group, name string) string {
	for _, field := range strings.Split(group, " ; ") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(field), name+"="); ok {
			return strings.TrimSpace(value)
		}
	}

	return ""
}

// serviceUser reports root when the unit names no user, because that is who
// the system manager runs it as.
func serviceUser(properties map[string][]string) string {
	if user := first(properties["User"]); user != "" {
		return user
	}

	return "root"
}

// timerRow reads the UNIT and ACTIVATES columns of a systemctl list-timers
// row. They are the last two columns, and the dates before them hold spaces,
// so the row is read from the right.
func timerRow(row string) (timer, activates string, ok bool) {
	fields := strings.Fields(row)

	for i := len(fields) - 1; i >= 0; i-- {
		if !strings.HasSuffix(fields[i], ".timer") {
			continue
		}

		if i+1 < len(fields) && isUnitName(fields[i+1]) {
			activates = fields[i+1]
		}

		return fields[i], activates, true
	}

	return "", "", false
}

// isListTimersFooter recognises the summary older systemctl versions print
// even when asked for no legend.
func isListTimersFooter(row string) bool {
	if row == "No timers running." || strings.HasPrefix(row, "Pass --all") {
		return true
	}

	count, rest, ok := strings.Cut(row, " ")
	if !ok {
		return false
	}

	if _, err := strconv.Atoi(count); err != nil {
		return false
	}

	return rest == "timers listed." || rest == "timer listed."
}

// isUnitName holds to the characters systemd allows in a unit name, so a
// column that held a dash or n/a is not mistaken for a unit.
func isUnitName(name string) bool {
	if name == "" || len(name) > 255 || name[0] == '-' || !strings.Contains(name, ".") {
		return false
	}

	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '.', r == '_', r == '-', r == '@', r == ':', r == '\\':
		default:
			return false
		}
	}

	return true
}

func joinNotes(notes ...string) string {
	kept := make([]string, 0, len(notes))

	for _, note := range notes {
		if note != "" {
			kept = append(kept, note)
		}
	}

	return strings.Join(kept, "; ")
}

func first(values []string) string {
	if len(values) == 0 {
		return ""
	}

	return values[0]
}
