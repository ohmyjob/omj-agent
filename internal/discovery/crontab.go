package discovery

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// crontabFormat tells the two crontab layouts apart: the system files name the
// user a job runs as between the schedule and the command, a user's own
// crontab does not, because the file name is the user.
type crontabFormat int

const (
	systemFormat crontabFormat = iota
	userFormat
)

const crontabSourcePrefix = "crontab:"

// cronNicknames are the shorthands cron accepts in place of five fields.
var cronNicknames = []string{"@reboot", "@yearly", "@annually", "@monthly", "@weekly", "@daily", "@midnight", "@hourly"}

// cronField is one of cron's five schedule fields, with the values it accepts.
type cronField struct {
	name     string
	min, max int
	names    []string
}

// The day of week runs to 7 because both 0 and 7 mean Sunday.
var cronFields = [5]cronField{
	{name: "minute", min: 0, max: 59},
	{name: "hour", min: 0, max: 23},
	{name: "day of month", min: 1, max: 31},
	{name: "month", min: 1, max: 12, names: []string{"jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"}},
	{name: "day of week", min: 0, max: 7, names: []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}},
}

func (c *collection) readSystemCrontab() {
	path := c.collector.Paths.SystemCrontab
	if path == "" {
		return
	}

	c.readCrontab(crontabSourcePrefix+path, path, systemFormat, "")
}

func (c *collection) readCronDirectory() {
	dir := c.collector.Paths.CronDir
	if dir == "" {
		return
	}

	for _, name := range c.readDirectory(crontabSourcePrefix+dir, dir) {
		// cron itself skips a file whose name is not letters, digits,
		// underscores and hyphens, which is how a backup or a package's
		// leftover .dpkg-dist file stays inactive.
		if !isCronDName(name) {
			c.collector.Logger.Debug("cron ignores this file name, so it is not reported", "path", filepath.Join(dir, name))

			continue
		}

		path := filepath.Join(dir, name)
		c.readCrontab(crontabSourcePrefix+path, path, systemFormat, "")
	}
}

func (c *collection) readUserCrontabs() {
	for _, dir := range c.collector.Paths.SpoolDirs {
		for _, user := range c.readDirectory(crontabSourcePrefix+dir, dir) {
			if !isUserName(user) {
				c.collector.Logger.Debug("spool file is not named after a user, so it is not reported", "path", filepath.Join(dir, user))

				continue
			}

			c.readCrontab(crontabSourcePrefix+user, filepath.Join(dir, user), userFormat, user)
		}
	}
}

// readDirectory lists the files of a directory, in the sorted order os.ReadDir
// gives, and skips subdirectories.
func (c *collection) readDirectory(source, dir string) []string {
	listing, err := os.ReadDir(dir)
	if err != nil {
		c.unreadableSource(source, err)

		return nil
	}

	names := make([]string, 0, len(listing))

	for _, entry := range listing {
		if entry.IsDir() {
			continue
		}

		names = append(names, entry.Name())
	}

	return names
}

func (c *collection) readCrontab(source, path string, format crontabFormat, user string) {
	data, err := os.ReadFile(path)
	if err != nil {
		c.unreadableSource(source, err)

		return
	}

	parser := cronParser{source: source, format: format, user: user, agentPath: c.collector.AgentPath}

	for line := range strings.Lines(string(data)) {
		entry, ok := parser.parse(strings.TrimRight(line, "\r\n"))
		if !ok {
			continue
		}

		c.add(entry)
	}
}

// cronParser reads the lines of one crontab. It carries the timezone a CRON_TZ
// or TZ assignment sets, which applies to the lines below it and not to the
// ones above, the way cron reads the file.
type cronParser struct {
	source    string
	format    crontabFormat
	user      string
	agentPath string
	timezone  string
}

// parse returns the entry a line describes. A blank line, a comment and an
// environment assignment describe none, and report ok false.
func (p *cronParser) parse(raw string) (Entry, bool) {
	text := strings.TrimSpace(raw)
	if text == "" || strings.HasPrefix(text, "#") {
		return Entry{}, false
	}

	if name, value, ok := environmentAssignment(text); ok {
		// Vixie cron reads both, CRON_TZ being the unambiguous one.
		if name == "CRON_TZ" || name == "TZ" {
			p.timezone = value
		}

		return Entry{}, false
	}

	entry := Entry{Source: p.source, Raw: raw, ScheduleKind: KindCron, Timezone: p.timezone, User: p.user}

	// A leading hyphen asks Debian's cron not to log the job; it is not part
	// of the schedule.
	schedule, rest, err := cronSchedule(strings.TrimPrefix(text, "-"))
	if err != nil {
		entry.Unparseable = true
		entry.Note = err.Error()

		return entry, true
	}

	entry.Schedule = schedule

	if p.format == systemFormat {
		var user string

		user, rest = cutField(rest)

		if !isUserName(user) {
			entry.Unparseable = true
			entry.Note = fmt.Sprintf("%q is not a user name, and this file names the user between the schedule and the command", user)

			return entry, true
		}

		entry.User = user
	}

	entry.Command = cronCommand(rest)
	if entry.Command == "" {
		entry.Unparseable = true
		entry.Note = "the line has a schedule but no command"

		return entry, true
	}

	entry.IsAgent = runsAgent(entry.Command, "", p.agentPath)

	return entry, true
}

// environmentAssignment recognises the NAME = value lines a crontab may hold.
// A schedule never matches, because its first field cannot be a name followed
// by an equals sign.
func environmentAssignment(text string) (name, value string, ok bool) {
	name, value, ok = strings.Cut(text, "=")
	if !ok {
		return "", "", false
	}

	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsAny(name, " \t") {
		return "", "", false
	}

	for i, r := range name {
		letter := r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')
		if !letter && !(i > 0 && r >= '0' && r <= '9') {
			return "", "", false
		}
	}

	return name, unquoteCronValue(strings.TrimSpace(value)), true
}

func unquoteCronValue(value string) string {
	if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
		return value[1 : len(value)-1]
	}

	return value
}

// cronSchedule reads the schedule at the start of a line and returns the rest
// of it. The schedule is the fields as written, separated by single spaces, so
// a tab-separated line and a space-separated one read the same.
func cronSchedule(text string) (schedule, rest string, err error) {
	if strings.HasPrefix(text, "@") {
		nickname, rest := cutField(text)
		if !slices.Contains(cronNicknames, strings.ToLower(nickname)) {
			return "", "", fmt.Errorf("%q is not one of cron's shorthands (%s)", nickname, strings.Join(cronNicknames, ", "))
		}

		return nickname, rest, nil
	}

	fields := make([]string, 0, len(cronFields))
	rest = text

	for i, field := range cronFields {
		var value string

		value, rest = cutField(rest)
		if value == "" {
			return "", "", fmt.Errorf("a schedule needs five fields, and this line has %d", i)
		}

		if err := field.validate(value); err != nil {
			return "", "", err
		}

		fields = append(fields, value)
	}

	return strings.Join(fields, " "), rest, nil
}

// cutField takes the next whitespace-separated field and returns the rest of
// the line with its leading whitespace removed.
func cutField(text string) (field, rest string) {
	text = strings.TrimLeft(text, " \t")

	i := strings.IndexAny(text, " \t")
	if i < 0 {
		return text, ""
	}

	return text[:i], strings.TrimLeft(text[i:], " \t")
}

func (f cronField) validate(field string) error {
	for _, item := range strings.Split(field, ",") {
		if err := f.validateItem(item); err != nil {
			return err
		}
	}

	return nil
}

func (f cronField) validateItem(item string) error {
	value, step, hasStep := strings.Cut(item, "/")
	if hasStep {
		if n, err := strconv.Atoi(step); err != nil || n < 1 {
			return fmt.Errorf("the %s %q steps by %q, which is not a whole number above zero", f.name, item, step)
		}
	}

	if value == "*" {
		return nil
	}

	from, to, isRange := strings.Cut(value, "-")
	if err := f.validateValue(item, from); err != nil {
		return err
	}

	if !isRange {
		return nil
	}

	return f.validateValue(item, to)
}

func (f cronField) validateValue(item, value string) error {
	if value == "" {
		return fmt.Errorf("the %s %q has an empty value", f.name, item)
	}

	if slices.Contains(f.names, strings.ToLower(value)) {
		return nil
	}

	n, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("the %s %q is neither a number nor a name cron knows", f.name, item)
	}

	if n < f.min || n > f.max {
		return fmt.Errorf("the %s %q is outside %d-%d", f.name, item, f.min, f.max)
	}

	return nil
}

// cronCommand is the command cron would run. Everything from the first
// unescaped percent sign is the standard input cron feeds the command, not
// part of it, and an escaped one is the literal character cron passes on. The
// raw line keeps all of it either way.
func cronCommand(text string) string {
	var command strings.Builder

	for i := 0; i < len(text); i++ {
		switch {
		case text[i] == '\\' && i+1 < len(text) && text[i+1] == '%':
			command.WriteByte('%')
			i++
		case text[i] == '%':
			return strings.TrimRight(command.String(), " \t")
		default:
			command.WriteByte(text[i])
		}
	}

	return strings.TrimRight(command.String(), " \t")
}

func isCronDName(name string) bool {
	if name == "" {
		return false
	}

	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
		default:
			return false
		}
	}

	return true
}

// isUserName holds to the portable name characters, so a command that took the
// user's place in a system crontab is reported unparseable rather than read as
// a user.
func isUserName(name string) bool {
	if name == "" || len(name) > 32 || name[0] == '-' || name[0] == '.' {
		return false
	}

	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-', r == '.':
		default:
			return false
		}
	}

	return true
}
