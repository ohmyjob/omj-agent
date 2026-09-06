//go:build unix

package runner

import (
	"context"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func currentUser(t *testing.T) *user.User {
	t.Helper()

	current, err := user.Current()
	if err != nil {
		t.Fatalf("current user: %v", err)
	}

	return current
}

func TestLookupUser(t *testing.T) {
	current := currentUser(t)

	tests := []struct {
		name    string
		runAs   string
		want    string
		wantErr string
	}{
		{name: "no user is the one the agent runs as", want: current.Username},
		{name: "a named user is looked up", runAs: current.Username, want: current.Username},
		{name: "a user this machine does not have", runAs: "omj-nobody-at-all", wantErr: "omj-nobody-at-all"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := lookupUser(tt.runAs)

			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error = %v, want one naming %q", err, tt.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("lookupUser: %v", err)
			}

			if got.Username != tt.want {
				t.Errorf("username = %q, want %q", got.Username, tt.want)
			}
		})
	}
}

// The Agent's own user needs no credential at all, so an unprivileged Agent
// keeps spawning exactly as it did before per-Job users existed.
func TestDropPrivilegesLeavesTheAgentsOwnUserAlone(t *testing.T) {
	cmd := exec.Command("true")
	configureProcess(cmd)

	if err := dropPrivileges(cmd, currentUser(t)); err != nil {
		t.Fatalf("dropPrivileges: %v", err)
	}

	if cmd.SysProcAttr.Credential != nil {
		t.Errorf("credential = %+v, want none", cmd.SysProcAttr.Credential)
	}
}

func TestCredentialOfCarriesTheSupplementaryGroups(t *testing.T) {
	current := currentUser(t)

	uid, err := numericID(current.Uid, "uid", current.Username)
	if err != nil {
		t.Fatalf("numericID: %v", err)
	}

	credential, err := credentialOf(current, uid)
	if err != nil {
		t.Fatalf("credentialOf: %v", err)
	}

	ids, err := current.GroupIds()
	if err != nil {
		t.Fatalf("group ids: %v", err)
	}

	if len(credential.Groups) != len(ids) {
		t.Errorf("groups = %v, want %d of them", credential.Groups, len(ids))
	}

	if strconv.FormatUint(uint64(credential.Gid), 10) != current.Gid {
		t.Errorf("gid = %d, want %s", credential.Gid, current.Gid)
	}
}

func TestNumericIDRejectsAnUnparseableID(t *testing.T) {
	if _, err := numericID("S-1-5-21", "uid", "deploy"); err == nil || !strings.Contains(err.Error(), "S-1-5-21") {
		t.Fatalf("error = %v, want one naming the id", err)
	}
}

func TestCheckWorkingDir(t *testing.T) {
	owner := uint32(os.Getuid()) //nolint:gosec // a real uid is never negative on unix.
	stranger := owner + 1

	tests := []struct {
		name       string
		mode       os.FileMode
		credential *syscall.Credential
		wantErr    bool
	}{
		{name: "no credential is the agent's own user and is left alone", mode: 0o000},
		{name: "the owner may enter their own directory", mode: 0o700, credential: &syscall.Credential{Uid: owner}},
		{name: "the owner without the execute bit may not", mode: 0o600, credential: &syscall.Credential{Uid: owner}, wantErr: true},
		{name: "anybody may enter a world-executable directory", mode: 0o701, credential: &syscall.Credential{Uid: stranger}},
		{name: "a stranger may not enter a private directory", mode: 0o700, credential: &syscall.Credential{Uid: stranger}, wantErr: true},
		{name: "root enters anything", mode: 0o000, credential: &syscall.Credential{Uid: rootUID}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "work")
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatalf("mkdir: %v", err)
			}

			if err := os.Chmod(dir, tt.mode); err != nil {
				t.Fatalf("chmod: %v", err)
			}

			//nolint:gosec // A directory has to keep its execute bit to be removed again.
			t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

			info, err := os.Stat(dir)
			if err != nil {
				t.Fatalf("stat: %v", err)
			}

			err = checkWorkingDir(dir, info, tt.credential)

			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, want an error = %v", err, tt.wantErr)
			}

			if tt.wantErr && !strings.Contains(err.Error(), dir) {
				t.Errorf("error = %v, want one naming %q", err, dir)
			}
		})
	}
}

// The group path needs a directory whose group the credential lists, which is
// the current user's own primary group.
func TestCheckWorkingDirAcceptsAGroupMember(t *testing.T) {
	dir := t.TempDir()

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}

	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skip("no stat_t on this platform")
	}

	if err := os.Chmod(dir, 0o710); err != nil { //nolint:gosec // The execute bit is the permission under test.
		t.Fatalf("chmod: %v", err)
	}

	credential := &syscall.Credential{Uid: uint32(os.Getuid()) + 1, Groups: []uint32{stat.Gid}} //nolint:gosec // a real uid is never negative on unix.

	if err := checkWorkingDir(dir, info, credential); err != nil {
		t.Errorf("checkWorkingDir: %v, want the group member to be let in", err)
	}
}

// Naming the user the Agent already runs as must reach the same process, the
// same environment and the same working directory as naming nobody.
func TestStartAsTheAgentsOwnUser(t *testing.T) {
	current := currentUser(t)

	_, sink := run(t, Spec{Command: "id -un; printf '%s\\n' \"$USER\" \"$HOME\"", RunAs: current.Username})

	lines := strings.Fields(sink.String(Stdout))
	want := []string{current.Username, current.Username, current.HomeDir}

	if len(lines) != len(want) {
		t.Fatalf("output = %q, want %v", sink.String(Stdout), want)
	}

	for i, value := range want {
		if lines[i] != value {
			t.Errorf("line %d = %q, want %q", i, lines[i], value)
		}
	}
}

func TestStartRefusesAUserThisMachineDoesNotHave(t *testing.T) {
	process, err := Start(context.Background(), Spec{Command: "true", RunAs: "omj-nobody-at-all"}, newRecordingSink())

	if process != nil {
		t.Errorf("process = %v, want nil", process)
	}

	if err == nil || !strings.Contains(err.Error(), "omj-nobody-at-all") {
		t.Fatalf("error = %v, want one naming the user", err)
	}
}
