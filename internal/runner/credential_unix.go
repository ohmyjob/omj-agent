//go:build unix

package runner

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"slices"
	"strconv"
	"syscall"
)

// Root is exempt from the permission bits, so a directory it cannot enter does
// not exist.
const rootUID = 0

const (
	ownerExecute = 0o100
	groupExecute = 0o010
	otherExecute = 0o001
)

// dropPrivileges gives the child the execution user's credential, and none at
// all when that is the user the Agent already runs as, which is what keeps an
// unprivileged Agent working.
func dropPrivileges(cmd *exec.Cmd, executionUser *user.User) error {
	uid, err := numericID(executionUser.Uid, "uid", executionUser.Username)
	if err != nil {
		return err
	}

	if uid == uint32(os.Getuid()) { //nolint:gosec // Getuid is a real uid and cannot be negative on unix.
		return nil
	}

	credential, err := credentialOf(executionUser, uid)
	if err != nil {
		return err
	}

	cmd.SysProcAttr.Credential = credential

	return nil
}

// The supplementary groups travel with the uid because a Job that cannot read
// what its user's groups grant is a Job running as the wrong user in all but
// name.
func credentialOf(executionUser *user.User, uid uint32) (*syscall.Credential, error) {
	gid, err := numericID(executionUser.Gid, "gid", executionUser.Username)
	if err != nil {
		return nil, err
	}

	ids, err := executionUser.GroupIds()
	if err != nil {
		return nil, fmt.Errorf("groups of %s: %w", executionUser.Username, err)
	}

	groups := make([]uint32, 0, len(ids))

	for _, id := range ids {
		group, err := numericID(id, "group", executionUser.Username)
		if err != nil {
			return nil, err
		}

		groups = append(groups, group)
	}

	return &syscall.Credential{Uid: uid, Gid: gid, Groups: groups}, nil
}

func numericID(value, kind, username string) (uint32, error) {
	id, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%s %q of %s: %w", kind, value, username, err)
	}

	return uint32(id), nil
}

// checkWorkingDir names both the directory and the user, because exec would
// otherwise fail with a bare permission error that reads as though the command
// itself were missing.
func checkWorkingDir(dir string, info os.FileInfo, credential *syscall.Credential) error {
	if credential == nil || canEnter(info, credential) {
		return nil
	}

	return fmt.Errorf("working directory %q cannot be entered by uid %d", dir, credential.Uid)
}

func canEnter(info os.FileInfo, credential *syscall.Credential) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || credential.Uid == rootUID {
		return true
	}

	mode := info.Mode().Perm()

	switch {
	case stat.Uid == credential.Uid:
		return mode&ownerExecute != 0
	case slices.Contains(credential.Groups, stat.Gid):
		return mode&groupExecute != 0
	default:
		return mode&otherExecute != 0
	}
}
