package doctor

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ohmyjob/omj-agent/internal/client"
	"github.com/ohmyjob/omj-agent/internal/config"
	"github.com/ohmyjob/omj-agent/internal/protocol"
)

const (
	testCredential = "omj_agent_K7fP2mQ9xR4tW1yZ6bN3vC8hJ5lD0sA2eG4iU7oY9pT1rF3k"
	testMachineID  = "0f7a1a3c-4c1c-4a4e-9d2d-4b7a4b3f0f11"
	hardeningQuery = "show omj-agent -p NoNewPrivileges,PrivateTmp,ProtectSystem,ProtectHome"
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
	err       error
}

func (f fakeSystemctl) Available() bool { return f.available }

func (f fakeSystemctl) Run(_ context.Context, args ...string) (string, error) {
	return f.answers[strings.Join(args, " ")], f.err
}

func healthySystemctl() fakeSystemctl {
	return fakeSystemctl{available: true, answers: map[string]string{
		"show omj-agent -p LoadState,User": "LoadState=loaded\nUser=ohmyjob",
		"is-enabled omj-agent":             "enabled",
		"is-active omj-agent":              "active",
		hardeningQuery:                     "NoNewPrivileges=no\nPrivateTmp=no\nProtectSystem=\nProtectHome=",
	}}
}

func pingerFor(pinger Pinger) func(config.Config, config.Credential) (Pinger, error) {
	return func(config.Config, config.Credential) (Pinger, error) { return pinger, nil }
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

func writeEnrollment(t *testing.T, paths config.Paths, cfg config.Config) {
	t.Helper()

	if err := config.Save(paths, cfg); err != nil {
		t.Fatal(err)
	}

	credential, err := config.NewCredential(testCredential)
	if err != nil {
		t.Fatal(err)
	}

	if err := config.SaveCredential(paths, credential); err != nil {
		t.Fatal(err)
	}
}

func healthyHost(t *testing.T) Host {
	t.Helper()

	paths := testPaths(t)
	writeEnrollment(t, paths, enrolledConfig())

	return Host{
		Paths:    paths,
		UID:      998,
		Username: "ohmyjob",
		Now:      func() time.Time { return testNow },
		Dial: pingerFor(fakePinger{
			response: protocol.PingResponse{MachineID: testMachineID, ServerVersion: "0.3.0", ServerTime: testNow.Add(400 * time.Millisecond)},
			version:  "0.3.0",
		}),
		Systemctl: healthySystemctl(),
	}
}

// allow writes an execution-user allowlist and answers lookups from a small
// local user database, so the check never depends on who is running the tests.
func allow(t *testing.T, host *Host, users ...string) {
	t.Helper()

	cfg := enrolledConfig()
	cfg.RunAsAllowed = users
	writeEnrollment(t, host.Paths, cfg)

	known := map[string]int{"root": 0, "ohmyjob": 998, "deploy": 1001, "www-data": 33}

	host.LookupUser = func(name string) (int, error) {
		uid, ok := known[name]
		if !ok {
			return 0, errors.New("unknown user " + name)
		}

		return uid, nil
	}
}

func find(t *testing.T, report Report, name string) Check {
	t.Helper()

	for _, check := range report.Checks {
		if check.Name == name {
			return check
		}
	}

	t.Fatalf("no check named %q in %+v", name, report.Checks)

	return Check{}
}

func TestRunOnAHealthyHost(t *testing.T) {
	report := Run(t.Context(), healthyHost(t))

	if report.Failed() {
		t.Fatalf("Failed() = true for %+v", report.Checks)
	}

	wantNames := []string{"configuration", "credential", "state directory", "server", "protocol", "clock", "service user", "service", "hardening", "privileges", "execution users"}

	for i, check := range report.Checks {
		if check.Name != wantNames[i] {
			t.Errorf("check %d = %q, want %q", i, check.Name, wantNames[i])
		}

		if check.Status != Pass {
			t.Errorf("%s = %s %q, want PASS", check.Name, check.Status, check.Detail)
		}
	}

	if got := find(t, report, "clock").Detail; got != "within 400ms of the server" {
		t.Errorf("clock detail = %q", got)
	}
}

func TestChecks(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(t *testing.T, host *Host)
		check      string
		wantStatus Status
		wantDetail string
	}{
		{
			name:       "a 0644 credential",
			setup:      func(t *testing.T, host *Host) { chmod(t, host.Paths.CredentialFile, 0o644) },
			check:      "credential",
			wantStatus: Fail,
			wantDetail: "has mode 0644; it must be 0600 so only the agent can read it; fix with chmod 0600 ",
		},
		{
			name:       "a missing credential",
			setup:      func(t *testing.T, host *Host) { remove(t, host.Paths.CredentialFile) },
			check:      "credential",
			wantStatus: Fail,
			wantDetail: "agent.credential is missing; run omj-agent enroll",
		},
		{
			name:       "a missing configuration",
			setup:      func(t *testing.T, host *Host) { remove(t, host.Paths.ConfigFile) },
			check:      "configuration",
			wantStatus: Fail,
			wantDetail: "agent.conf is missing; run omj-agent enroll",
		},
		{
			name: "an invalid configuration",
			setup: func(t *testing.T, host *Host) {
				write(t, host.Paths.ConfigFile, "server_url = http://omj.example.com\n")
			},
			check:      "configuration",
			wantStatus: Fail,
			wantDetail: "TLS",
		},
		{
			name:       "the server is not probed while the configuration is broken",
			setup:      func(t *testing.T, host *Host) { remove(t, host.Paths.ConfigFile) },
			check:      "server",
			wantStatus: Warn,
			wantDetail: "not checked; fix the configuration and credential first",
		},
		{
			name:       "a missing state directory",
			setup:      func(t *testing.T, host *Host) { remove(t, host.Paths.StateDir) },
			check:      "state directory",
			wantStatus: Fail,
			wantDetail: "create it with mkdir -p ",
		},
		{
			name: "an unreachable server",
			setup: func(_ *testing.T, host *Host) {
				host.Dial = pingerFor(fakePinger{err: &net.OpError{Op: "dial", Err: errors.New("connection refused")}})
			},
			check:      "server",
			wantStatus: Fail,
			wantDetail: "could not reach https://omj.example.com: dial: connection refused",
		},
		{
			name: "a certificate that cannot be verified",
			setup: func(_ *testing.T, host *Host) {
				host.Dial = pingerFor(fakePinger{err: &tls.CertificateVerificationError{Err: errors.New("x509: certificate signed by unknown authority")}})
			},
			check:      "server",
			wantStatus: Fail,
			wantDetail: "could not verify the TLS certificate of https://omj.example.com",
		},
		{
			name: "a rejected credential",
			setup: func(_ *testing.T, host *Host) {
				host.Dial = pingerFor(fakePinger{err: &client.APIError{Status: http.StatusUnauthorized, Code: protocol.ErrInvalidCredential}})
			},
			check:      "server",
			wantStatus: Fail,
			wantDetail: "run omj-agent enroll --force",
		},
		{
			name: "a rejected protocol still counts as reachable",
			setup: func(_ *testing.T, host *Host) {
				host.Dial = pingerFor(fakePinger{version: "0.9.0", err: &client.APIError{Status: http.StatusUpgradeRequired, Message: "agent too old", SupportedProtocolVersions: []int{2}, MinAgentVersion: "0.2.0"}})
			},
			check:      "server",
			wantStatus: Pass,
			wantDetail: "https://omj.example.com is reachable, server version 0.9.0 over TLS",
		},
		{
			name: "a rejected protocol",
			setup: func(_ *testing.T, host *Host) {
				host.Dial = pingerFor(fakePinger{err: &client.APIError{Status: http.StatusUpgradeRequired, Message: "agent too old", SupportedProtocolVersions: []int{2}, MinAgentVersion: "0.2.0"}})
			},
			check:      "protocol",
			wantStatus: Fail,
			wantDetail: "agent too old (this agent speaks protocol 1, the server supports 2); the server requires agent 0.2.0 or newer; update the agent",
		},
		{
			name: "the protocol is not checked without an answer",
			setup: func(_ *testing.T, host *Host) {
				host.Dial = pingerFor(fakePinger{err: &net.OpError{Op: "dial", Err: errors.New("connection refused")}})
			},
			check:      "protocol",
			wantStatus: Warn,
			wantDetail: "not checked; the server did not answer",
		},
		{
			name: "a clock running behind",
			setup: func(_ *testing.T, host *Host) {
				host.Dial = pingerFor(fakePinger{response: protocol.PingResponse{ServerTime: testNow.Add(45 * time.Second)}})
			},
			check:      "clock",
			wantStatus: Fail,
			wantDetail: "this machine is 45s behind the server; enable time synchronisation",
		},
		{
			name: "a clock running ahead",
			setup: func(_ *testing.T, host *Host) {
				host.Dial = pingerFor(fakePinger{response: protocol.PingResponse{ServerTime: testNow.Add(-31 * time.Second)}})
			},
			check:      "clock",
			wantStatus: Fail,
			wantDetail: "this machine is 31s ahead of the server",
		},
		{
			name: "plain http",
			setup: func(t *testing.T, host *Host) {
				cfg := enrolledConfig()
				cfg.ServerURL = "http://omj.example.com"
				cfg.InsecureHTTP = true
				writeEnrollment(t, host.Paths, cfg)
			},
			check:      "server",
			wantStatus: Warn,
			wantDetail: "the connection is plain HTTP",
		},
		{
			name:       "running as root",
			setup:      func(_ *testing.T, host *Host) { host.UID = 0; host.Username = "root" },
			check:      "privileges",
			wantStatus: Warn,
			wantDetail: "running as root, so every Job runs with root privileges; prefer install.sh --user",
		},
		{
			name:       "no systemd for the service user",
			setup:      func(_ *testing.T, host *Host) { host.Systemctl = fakeSystemctl{} },
			check:      "service user",
			wantStatus: Warn,
			wantDetail: "systemd not present",
		},
		{
			name:       "no systemd for the service",
			setup:      func(_ *testing.T, host *Host) { host.Systemctl = fakeSystemctl{} },
			check:      "service",
			wantStatus: Warn,
			wantDetail: "systemd not present; start the agent with omj-agent run",
		},
		{
			name:       "no systemd for the hardening",
			setup:      func(_ *testing.T, host *Host) { host.Systemctl = fakeSystemctl{} },
			check:      "hardening",
			wantStatus: Warn,
			wantDetail: "systemd not present",
		},
		{
			name: "systemctl failing",
			setup: func(_ *testing.T, host *Host) {
				host.Systemctl = fakeSystemctl{available: true, err: errors.New("System has not been booted with systemd")}
			},
			check:      "service user",
			wantStatus: Warn,
			wantDetail: "systemctl failed: System has not been booted with systemd",
		},
		{
			name: "a unit that is not installed",
			setup: func(_ *testing.T, host *Host) {
				host.Systemctl = fakeSystemctl{available: true, answers: map[string]string{"show omj-agent -p LoadState,User": "LoadState=not-found\nUser="}}
			},
			check:      "service user",
			wantStatus: Warn,
			wantDetail: "omj-agent.service is not installed",
		},
		{
			name: "a unit running as another user",
			setup: func(_ *testing.T, host *Host) {
				systemctl := healthySystemctl()
				systemctl.answers["show omj-agent -p LoadState,User"] = "LoadState=loaded\nUser="
				host.Systemctl = systemctl
			},
			check:      "service user",
			wantStatus: Warn,
			wantDetail: "doctor runs as ohmyjob but the service runs as root",
		},
		{
			name: "a stopped unit",
			setup: func(_ *testing.T, host *Host) {
				systemctl := healthySystemctl()
				systemctl.answers["is-enabled omj-agent"] = "disabled"
				systemctl.answers["is-active omj-agent"] = "inactive"
				host.Systemctl = systemctl
			},
			check:      "service",
			wantStatus: Fail,
			wantDetail: "omj-agent is disabled and inactive; run systemctl enable --now omj-agent",
		},
		{
			name: "a unit that systemd does not know",
			setup: func(_ *testing.T, host *Host) {
				systemctl := healthySystemctl()
				delete(systemctl.answers, "is-enabled omj-agent")
				delete(systemctl.answers, "is-active omj-agent")
				host.Systemctl = systemctl
			},
			check:      "service",
			wantStatus: Fail,
			wantDetail: "omj-agent is not installed and unknown",
		},
		{
			name: "NoNewPrivileges",
			setup: func(_ *testing.T, host *Host) {
				systemctl := healthySystemctl()
				systemctl.answers[hardeningQuery] = "NoNewPrivileges=yes\nPrivateTmp=yes\nProtectSystem=\nProtectHome="
				host.Systemctl = systemctl
			},
			check:      "hardening",
			wantStatus: Warn,
			wantDetail: "NoNewPrivileges is on, so sudo inside Jobs will fail; active: NoNewPrivileges, PrivateTmp",
		},
		{
			name: "hardening that keeps sudo working",
			setup: func(_ *testing.T, host *Host) {
				systemctl := healthySystemctl()
				systemctl.answers[hardeningQuery] = "NoNewPrivileges=no\nPrivateTmp=yes\nProtectSystem=full\nProtectHome=read-only"
				host.Systemctl = systemctl
			},
			check:      "hardening",
			wantStatus: Pass,
			wantDetail: "active: PrivateTmp, ProtectSystem=full, ProtectHome=read-only",
		},
		{
			name: "an allowlist a root agent can honour",
			setup: func(t *testing.T, host *Host) {
				host.UID, host.Username = 0, "root"
				allow(t, host, "deploy", "www-data")
			},
			check:      "execution users",
			wantStatus: Pass,
			wantDetail: "may run work as root (uid 0), deploy (uid 1001), www-data (uid 33)",
		},
		{
			name: "an allowlist naming a user who does not exist",
			setup: func(t *testing.T, host *Host) {
				host.UID, host.Username = 0, "root"
				allow(t, host, "backup")
			},
			check:      "execution users",
			wantStatus: Fail,
			wantDetail: `may run work as root (uid 0); run_as_allowed lists "backup", which is not a user on this machine`,
		},
		{
			name:       "an allowlist an unprivileged agent cannot honour",
			setup:      func(t *testing.T, host *Host) { allow(t, host, "deploy") },
			check:      "execution users",
			wantStatus: Fail,
			wantDetail: "this agent runs as ohmyjob (uid 998) and can only run work as itself",
		},
		{
			name:       "the allowlist is not checked while the configuration is broken",
			setup:      func(t *testing.T, host *Host) { remove(t, host.Paths.ConfigFile) },
			check:      "execution users",
			wantStatus: Warn,
			wantDetail: "not checked; fix the configuration first",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host := healthyHost(t)
			tt.setup(t, &host)

			got := find(t, Run(t.Context(), host), tt.check)

			if got.Status != tt.wantStatus || !strings.Contains(got.Detail, tt.wantDetail) {
				t.Errorf("%s = %s %q, want %s containing %q", tt.check, got.Status, got.Detail, tt.wantStatus, tt.wantDetail)
			}

			if strings.Contains(got.Detail, "goroutine") {
				t.Errorf("%s leaked a stack trace: %q", tt.check, got.Detail)
			}
		})
	}
}

func TestReportFailed(t *testing.T) {
	if (Report{Checks: []Check{{Status: Pass}, {Status: Warn}}}).Failed() {
		t.Error("Failed() = true without a FAIL")
	}

	if !(Report{Checks: []Check{{Status: Pass}, {Status: Fail}}}).Failed() {
		t.Error("Failed() = false with a FAIL")
	}
}

func TestProbeReportsADialError(t *testing.T) {
	host := Host{Dial: func(config.Config, config.Credential) (Pinger, error) { return nil, errors.New("bad url") }}.WithDefaults()

	if probe := host.Probe(t.Context(), config.Config{}, config.Credential{}); probe.Err == nil || probe.Err.Error() != "bad url" {
		t.Errorf("Probe().Err = %v, want bad url", probe.Err)
	}
}

func TestDefaultHostReadsTheMachine(t *testing.T) {
	host := DefaultHost()

	if host.Paths.ConfigFile == "" || host.Now == nil || host.LoadConfig == nil || host.LoadCredential == nil || host.LoadState == nil || host.Dial == nil || host.Systemctl == nil {
		t.Errorf("DefaultHost() left a field empty: %+v", host)
	}

	if _, err := host.Dial(enrolledConfig(), config.Credential{}); err != nil {
		t.Errorf("Dial() = %v", err)
	}

	if host.Systemctl.Available() {
		if out, err := host.Systemctl.Run(t.Context(), "--version"); err != nil || !strings.HasPrefix(out, "systemd") {
			t.Errorf("systemctl --version = %q, %v", out, err)
		}
	} else if out, err := host.Systemctl.Run(t.Context(), "--version"); out != "" || err != nil {
		t.Errorf("absent systemctl answered %q, %v", out, err)
	}
}

func chmod(t *testing.T, path string, mode os.FileMode) {
	t.Helper()

	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func remove(t *testing.T, path string) {
	t.Helper()

	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
