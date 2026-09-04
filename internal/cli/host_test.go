package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ohmyjob/omj-agent/internal/config"
	"github.com/ohmyjob/omj-agent/internal/doctor"
	"github.com/ohmyjob/omj-agent/internal/protocol"
	"github.com/ohmyjob/omj-agent/internal/state"
)

const (
	testMachineID = "0f7a1a3c-4c1c-4a4e-9d2d-4b7a4b3f0f11"
	testRunID     = "5b1e2c7a-8d4f-4c3b-9a2e-7f6d5c4b3a21"
)

var testNow = time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)

type fakePinger struct {
	response protocol.PingResponse
	version  string
	err      error
}

func (f fakePinger) Ping(context.Context) (protocol.PingResponse, error) { return f.response, f.err }

func (f fakePinger) ServerVersion() string { return f.version }

type fakeSystemctl struct {
	available bool
	answers   map[string]string
}

func (f fakeSystemctl) Available() bool { return f.available }

func (f fakeSystemctl) Run(_ context.Context, args ...string) (string, error) {
	return f.answers[strings.Join(args, " ")], nil
}

func testPaths(t *testing.T) config.Paths {
	t.Helper()

	dir := t.TempDir()
	paths := config.Paths{
		ConfigDir:      dir,
		ConfigFile:     filepath.Join(dir, "agent.conf"),
		CredentialFile: filepath.Join(dir, "agent.credential"),
		StateDir:       filepath.Join(dir, "state"),
		StateFile:      filepath.Join(dir, "state", "state.json"),
	}

	if err := os.MkdirAll(paths.StateDir, 0o750); err != nil {
		t.Fatal(err)
	}

	return paths
}

func enrolledConfig() config.Config {
	cfg := config.Default()
	cfg.ServerURL = "https://omj.example.com"
	cfg.MachineID = testMachineID

	return cfg
}

func writeConfig(t *testing.T, paths config.Paths, cfg config.Config) {
	t.Helper()

	if err := config.Save(paths, cfg); err != nil {
		t.Fatal(err)
	}
}

func writeCredential(t *testing.T, paths config.Paths) {
	t.Helper()

	credential, err := config.NewCredential(testCredential)
	if err != nil {
		t.Fatal(err)
	}

	if err := config.SaveCredential(paths, credential); err != nil {
		t.Fatal(err)
	}
}

// healthyHost is an enrolled machine whose Server, clock and service are
// all in order, with one Run in progress.
func healthyHost(t *testing.T) doctor.Host {
	t.Helper()

	paths := testPaths(t)
	writeConfig(t, paths, enrolledConfig())
	writeCredential(t, paths)

	store, err := state.Load(paths.StateFile)
	if err != nil {
		t.Fatal(err)
	}

	if err := store.MarkActive(state.ActiveRun{RunID: testRunID, PID: 4242, PGID: 4242, StartedAt: testNow.Add(-2 * time.Minute)}); err != nil {
		t.Fatal(err)
	}

	return doctor.Host{
		Paths:    paths,
		UID:      998,
		Username: "ohmyjob",
		Now:      func() time.Time { return testNow },
		Dial: func(config.Config, config.Credential) (doctor.Pinger, error) {
			return fakePinger{
				response: protocol.PingResponse{MachineID: testMachineID, ServerVersion: "0.3.0", ServerTime: testNow.Add(400 * time.Millisecond)},
				version:  "0.3.0",
			}, nil
		},
		Systemctl: fakeSystemctl{available: true, answers: map[string]string{
			"show omj-agent -p LoadState,User": "LoadState=loaded\nUser=ohmyjob",
			"is-enabled omj-agent":             "enabled",
			"is-active omj-agent":              "active",
			"show omj-agent -p NoNewPrivileges,PrivateTmp,ProtectSystem,ProtectHome": "NoNewPrivileges=no\nPrivateTmp=no\nProtectSystem=\nProtectHome=",
		}},
	}
}

// unhealthyHost is the same machine run as root, with a credential anyone
// can read and no systemd.
func unhealthyHost(t *testing.T) doctor.Host {
	t.Helper()

	host := healthyHost(t)
	host.UID = 0
	host.Username = "root"
	host.Systemctl = fakeSystemctl{}

	chmod(t, host.Paths.CredentialFile, 0o644)

	return host
}

func chmod(t *testing.T, path string, mode os.FileMode) {
	t.Helper()

	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}
