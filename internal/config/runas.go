package config

import "fmt"

// The execution-user allowlist moves in one direction (PRD §21): agent.conf is
// read here, checked against this machine, and reported to the Server as
// machine metadata. Nothing in this file takes a value a Server response could
// reach, so no response can widen what the Agent will run work as.

const rootUID = 0

// Lookup answers with the numeric uid; a nil Lookup reads the local user
// database.
type RunAsHost struct {
	UID      int
	Username string
	Lookup   func(name string) (uid int, err error)
}

// Err says why the Agent could not run work as that user; UID is meaningful
// only when Err is nil.
type RunAsUser struct {
	Name string
	UID  int
	Err  error
}

type RunAs struct {
	Users []RunAsUser
}

// The Agent's own user leads the list because it is always permitted: it is
// what an Agent without an allowlist runs everything as.
func ResolveRunAs(cfg Config, host RunAsHost) RunAs {
	var resolved RunAs

	if host.Username != "" {
		resolved.Users = append(resolved.Users, RunAsUser{Name: host.Username, UID: host.UID})
	}

	for _, name := range cfg.RunAsAllowed {
		if name == host.Username {
			continue
		}

		resolved.Users = append(resolved.Users, host.check(name))
	}

	return resolved
}

// Startup stops at the first user the Agent cannot run work as: an allowlist it
// cannot honour promises the Server something untrue, which is worse than
// having no allowlist at all.
func (r RunAs) Err() error {
	for _, allowed := range r.Users {
		if allowed.Err != nil {
			return allowed.Err
		}
	}

	return nil
}

// Names is nil rather than empty when this machine knows no user at all, so
// the metadata field is left out instead of being sent as an empty list.
func (r RunAs) Names() []string {
	var names []string

	for _, allowed := range r.Users {
		if allowed.Err == nil {
			names = append(names, allowed.Name)
		}
	}

	return names
}

func (h RunAsHost) check(name string) RunAsUser {
	uid, err := h.lookup(name)
	if err != nil {
		return RunAsUser{Name: name, Err: fmt.Errorf("run_as_allowed lists %q, which is not a user on this machine", name)}
	}

	checked := RunAsUser{Name: name, UID: uid}

	switch {
	case h.UID == rootUID:
		return checked
	case uid == rootUID:
		checked.Err = fmt.Errorf("run_as_allowed lists %q, which is uid 0, but this agent runs as %s; only an agent running as root can run work as root", name, h.identity())
	default:
		checked.Err = fmt.Errorf("run_as_allowed lists %q, but this agent runs as %s and can only run work as itself; only an agent running as root can run work as another user", name, h.identity())
	}

	return checked
}

func (h RunAsHost) lookup(name string) (int, error) {
	if h.Lookup != nil {
		return h.Lookup(name)
	}

	return LookupUser(name)
}

func (h RunAsHost) identity() string {
	if h.Username == "" {
		return fmt.Sprintf("uid %d", h.UID)
	}

	return fmt.Sprintf("%s (uid %d)", h.Username, h.UID)
}
