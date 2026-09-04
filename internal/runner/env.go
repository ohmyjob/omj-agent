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
// anything from the Agent process; the Job's variables may override any default.
func environment(spec Spec, serviceUser *user.User) []string {
	vars := map[string]string{
		"HOME":           serviceUser.HomeDir,
		"USER":           serviceUser.Username,
		"LOGNAME":        serviceUser.Username,
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
