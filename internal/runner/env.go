package runner

import (
	"maps"
	"os/user"
	"slices"
)

const (
	defaultPath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	defaultLang = "C.UTF-8"
)

// The daemon's own environment is never inherited, so a Job cannot read
// anything from the Agent process; the Job's variables may override any
// default. Every identity here belongs to the execution user rather than to
// the Agent, so a Job that runs as somebody else is not told otherwise.
// SHELL is the shell the command is actually running under: the execution
// user's login shell is not part of what os/user answers, and a passwd file
// read behind its back would be wrong for any directory service.
func environment(spec Spec, executionUser *user.User, shell string) []string {
	vars := map[string]string{
		"HOME":           executionUser.HomeDir,
		"USER":           executionUser.Username,
		"LOGNAME":        executionUser.Username,
		"SHELL":          shell,
		"PATH":           defaultPath,
		"LANG":           defaultLang,
		"OMJ_RUN_ID":     spec.RunID,
		"OMJ_JOB_NAME":   spec.JobName,
		"OMJ_MACHINE_ID": spec.MachineID,
	}

	maps.Copy(vars, spec.Env)

	env := make([]string, 0, len(vars))

	for _, key := range slices.Sorted(maps.Keys(vars)) {
		env = append(env, key+"="+vars[key])
	}

	return env
}
