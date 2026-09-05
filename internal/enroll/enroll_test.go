package enroll

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ohmyjob/omj-agent/internal/config"
	"github.com/ohmyjob/omj-agent/internal/protocol"
	"github.com/ohmyjob/omj-agent/internal/sysinfo"
	"github.com/ohmyjob/omj-agent/internal/version"
)

const (
	testToken      = "omj_enroll_aB3dE5fG7hJ9kL1mN3pQ5rS7tU9vW1xY"
	testCredential = "omj_agent_K7fP2mQ9xR4tW1yZ6bN3vC8hJ5lD0sA2eG4iU7oY9pT1rF3k"
	testMachineID  = "0f7a1a3c-4c1c-4a4e-9d2d-4b7a4b3f0f11"
)

type recorded struct {
	mu      sync.Mutex
	hits    int
	headers http.Header
	body    protocol.EnrollRequest
}

func (r *recorded) Hits() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.hits
}

func fakeServer(t *testing.T, status int, body any, header http.Header) (*httptest.Server, *recorded) {
	t.Helper()

	rec := &recorded{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.mu.Lock()
		defer rec.mu.Unlock()

		rec.hits++
		rec.headers = r.Header.Clone()

		if r.URL.Path != protocol.BasePath+"/enroll" || r.Method != http.MethodPost {
			http.Error(w, "unexpected request", http.StatusNotFound)

			return
		}

		if err := json.NewDecoder(r.Body).Decode(&rec.body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}

		for key, values := range header {
			w.Header()[key] = values
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)

		if body != nil {
			_ = json.NewEncoder(w).Encode(body)
		}
	}))

	t.Cleanup(server.Close)

	return server, rec
}

func successBody() protocol.EnrollResponse {
	return protocol.EnrollResponse{MachineID: testMachineID, Credential: testCredential, ServerTime: time.Now().UTC()}
}

func errorBody(code protocol.ErrorCode, message string) protocol.ErrorResponse {
	return protocol.ErrorResponse{Error: code, Message: message}
}

func testPaths(t *testing.T) config.Paths {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "ohmyjob")

	return config.Paths{
		ConfigDir:      dir,
		ConfigFile:     filepath.Join(dir, "agent.conf"),
		CredentialFile: filepath.Join(dir, "agent.credential"),
	}
}

type chownCall struct {
	path     string
	uid, gid int
}

type fakeSystem struct {
	mu    sync.Mutex
	calls []chownCall
	users map[string][2]int
}

func (f *fakeSystem) lookup(name string) (int, int, error) {
	ids, ok := f.users[name]
	if !ok {
		return 0, 0, errors.New("unknown user " + name)
	}

	return ids[0], ids[1], nil
}

func (f *fakeSystem) chown(path string, uid, gid int) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, chownCall{path: path, uid: uid, gid: gid})

	return nil
}

func testInfo() sysinfo.Info {
	return sysinfo.Info{
		Hostname:      "nas01",
		OS:            "linux",
		OSVersion:     "Debian GNU/Linux 12",
		Arch:          "arm64",
		KernelVersion: "6.1.0",
		ReportedIPs:   []string{"192.168.1.20"},
		AgentUser:     "ohmyjob",
		AgentUID:      998,
	}
}

func rootInfo() sysinfo.Info {
	info := testInfo()
	info.AgentUser, info.AgentUID = "root", 0

	return info
}

func testOptions(t *testing.T, serverURL string, sys *fakeSystem, log io.Writer) Options {
	t.Helper()

	if sys == nil {
		sys = &fakeSystem{users: map[string][2]int{DefaultOwner: {998, 998}}}
	}

	if log == nil {
		log = io.Discard
	}

	return Options{
		ServerURL:    serverURL,
		Token:        testToken,
		InsecureHTTP: true,
		Paths:        testPaths(t),
		Collect:      func(context.Context) (sysinfo.Info, error) { return testInfo(), nil },
		System: System{
			UID:         1000,
			Username:    "tester",
			LookupUser:  sys.lookup,
			Chown:       sys.chown,
			ServiceUnit: filepath.Join(t.TempDir(), "omj-agent.service"),
		},
		Logger: slog.New(slog.NewTextHandler(log, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
}

func reasonOf(t *testing.T, err error) Reason {
	t.Helper()

	var enrollErr *Error
	if !errors.As(err, &enrollErr) {
		t.Fatalf("error %v is not an *enroll.Error", err)
	}

	return enrollErr.Reason
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}

	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s has mode %04o, want %04o", path, got, want)
	}
}

// TestEnrollReportsTheExecutionUserAllowlist covers the first half of the
// list's one-way trip: what agent.conf allows is what the Server is told, and
// a list this machine could not honour stops enrollment before the token is
// spent.
func TestEnrollReportsTheExecutionUserAllowlist(t *testing.T) {
	tests := []struct {
		name     string
		existing string
		info     sysinfo.Info
		want     []string
		wantErr  string
	}{
		{name: "no allowlist is the agent's own user", info: testInfo(), want: []string{"ohmyjob"}},
		{name: "an allowlist a root agent can honour", existing: "run_as_allowed = deploy\n", info: rootInfo(), want: []string{"root", "deploy"}},
		{name: "a user who does not exist", existing: "run_as_allowed = backup\n", info: rootInfo(), wantErr: "not a user on this machine"},
		{name: "a user an unprivileged agent cannot become", existing: "run_as_allowed = deploy\n", info: testInfo(), wantErr: "can only run work as itself"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, rec := fakeServer(t, http.StatusCreated, successBody(), nil)
			sys := &fakeSystem{users: map[string][2]int{DefaultOwner: {998, 998}, "root": {0, 0}, "deploy": {1001, 1001}}}
			opts := testOptions(t, server.URL, sys, nil)
			opts.Collect = func(context.Context) (sysinfo.Info, error) { return tt.info, nil }

			if tt.existing != "" {
				if err := os.MkdirAll(opts.Paths.ConfigDir, 0o750); err != nil {
					t.Fatal(err)
				}

				if err := os.WriteFile(opts.Paths.ConfigFile, []byte(tt.existing), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			_, err := Enroll(context.Background(), opts)

			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Enroll() error = %v, want it to contain %q", err, tt.wantErr)
				}

				if reasonOf(t, err) != ReasonInvalidInput {
					t.Errorf("reason = %v, want ReasonInvalidInput", reasonOf(t, err))
				}

				if rec.Hits() != 0 {
					t.Errorf("server hits = %d, want the token left unspent", rec.Hits())
				}

				return
			}

			if err != nil {
				t.Fatalf("Enroll() error: %v", err)
			}

			if !reflect.DeepEqual(rec.body.RunAsAllowed, tt.want) {
				t.Errorf("run_as_allowed = %#v, want %#v", rec.body.RunAsAllowed, tt.want)
			}
		})
	}
}

func TestEnrollWritesConfigAndCredential(t *testing.T) {
	server, rec := fakeServer(t, http.StatusCreated, successBody(), nil)
	opts := testOptions(t, server.URL, nil, nil)
	opts.Name = "NAS in the basement"

	if err := os.MkdirAll(opts.Paths.ConfigDir, 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(opts.Paths.ConfigFile, []byte("log_level = debug\nmax_concurrent_runs = 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := Enroll(context.Background(), opts)
	if err != nil {
		t.Fatalf("Enroll() error: %v", err)
	}

	if result.MachineID != testMachineID {
		t.Errorf("MachineID = %q, want %q", result.MachineID, testMachineID)
	}

	if result.Owner != "tester" || result.NextStep != "omj-agent run" {
		t.Errorf("result = %+v, want owner tester and the run hint", result)
	}

	assertMode(t, opts.Paths.ConfigFile, 0o640)
	assertMode(t, opts.Paths.CredentialFile, 0o600)

	cfg, err := config.Load(opts.Paths)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.ServerURL != server.URL || cfg.MachineID != testMachineID || !cfg.InsecureHTTP {
		t.Errorf("config = %+v, want the server, machine id and insecure flag", cfg)
	}

	if cfg.LogLevel != "debug" || cfg.MaxConcurrentRuns != 2 {
		t.Errorf("config = %+v, want the existing keys kept", cfg)
	}

	credential, err := config.LoadCredential(opts.Paths)
	if err != nil {
		t.Fatalf("LoadCredential() error: %v", err)
	}

	if credential.Secret() != testCredential {
		t.Errorf("credential = %q, want %q", credential.Secret(), testCredential)
	}

	if rec.Hits() != 1 {
		t.Errorf("server hits = %d, want 1", rec.Hits())
	}

	if got := rec.headers.Get(protocol.HeaderAgentVersion); got != rec.body.AgentVersion || got != version.Version {
		t.Errorf("agent version header %q and body %q must both be %q", got, rec.body.AgentVersion, version.Version)
	}

	if rec.headers.Get("Authorization") != "" {
		t.Error("enroll must not send a credential")
	}

	if rec.body.Token != testToken || rec.body.Hostname != "nas01" || rec.body.OS != "linux" || !rec.body.InsecureHTTP {
		t.Errorf("request body = %+v, want the token and the collected metadata", rec.body)
	}

	if rec.body.Name == nil || *rec.body.Name != "NAS in the basement" {
		t.Errorf("request name = %v, want the friendly name", rec.body.Name)
	}
}

func TestEnrollOmitsAnEmptyName(t *testing.T) {
	server, rec := fakeServer(t, http.StatusCreated, successBody(), nil)
	opts := testOptions(t, server.URL, nil, nil)

	if _, err := Enroll(context.Background(), opts); err != nil {
		t.Fatalf("Enroll() error: %v", err)
	}

	if rec.body.Name != nil {
		t.Errorf("request name = %q, want it omitted so the server uses the hostname", *rec.body.Name)
	}
}

func TestEnrollServerErrors(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		body        any
		header      http.Header
		wantReason  Reason
		wantMessage string
	}{
		{name: "token invalid", status: http.StatusUnauthorized, body: errorBody(protocol.ErrTokenInvalid, "token unknown"), wantReason: ReasonTokenInvalid, wantMessage: "Add Machine"},
		{name: "token expired", status: http.StatusGone, body: errorBody(protocol.ErrTokenExpired, "expired"), wantReason: ReasonTokenExpired, wantMessage: "expired"},
		{name: "unsupported os", status: http.StatusUnprocessableEntity, body: errorBody(protocol.ErrUnsupportedOS, "windows is not supported"), wantReason: ReasonUnsupportedOS, wantMessage: "windows is not supported"},
		{name: "validation failed", status: http.StatusUnprocessableEntity, body: errorBody(protocol.ErrValidationFailed, "hostname is required"), wantReason: ReasonUnknown, wantMessage: "hostname is required"},
		{name: "protocol rejected", status: http.StatusUpgradeRequired, body: protocol.ErrorResponse{Error: protocol.ErrAgentTooOld, Message: "agent too old", SupportedProtocolVersions: []int{1, 2}, MinAgentVersion: "0.2.0"}, wantReason: ReasonVersionRejected, wantMessage: "supports 1, 2"},
		{name: "throttled", status: http.StatusTooManyRequests, body: errorBody(protocol.ErrThrottled, "slow down"), header: http.Header{"Retry-After": []string{"30"}}, wantReason: ReasonThrottled, wantMessage: "try again in 30s"},
		{name: "server error", status: http.StatusInternalServerError, body: nil, wantReason: ReasonUnknown, wantMessage: "refused the enrollment"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, _ := fakeServer(t, tt.status, tt.body, tt.header)
			opts := testOptions(t, server.URL, nil, nil)

			_, err := Enroll(context.Background(), opts)
			if err == nil {
				t.Fatal("Enroll() succeeded, want an error")
			}

			if got := reasonOf(t, err); got != tt.wantReason {
				t.Errorf("reason = %d, want %d (%v)", got, tt.wantReason, err)
			}

			if !strings.Contains(err.Error(), tt.wantMessage) {
				t.Errorf("message = %q, want it to contain %q", err.Error(), tt.wantMessage)
			}

			if _, statErr := os.Stat(opts.Paths.CredentialFile); statErr == nil {
				t.Error("credential file written after a failed enrollment")
			}
		})
	}
}

func TestEnrollVersionRejectedNamesTheMinimumAgent(t *testing.T) {
	server, _ := fakeServer(t, http.StatusUpgradeRequired, protocol.ErrorResponse{Error: protocol.ErrAgentTooOld, Message: "too old", MinAgentVersion: "0.2.0"}, nil)

	_, err := Enroll(context.Background(), testOptions(t, server.URL, nil, nil))

	if err == nil || !strings.Contains(err.Error(), "agent 0.2.0 or newer") {
		t.Errorf("error = %v, want the minimum agent version", err)
	}
}

func TestEnrollTLSFailure(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(server.Close)

	opts := testOptions(t, server.URL, nil, nil)
	opts.InsecureHTTP = false

	_, err := Enroll(context.Background(), opts)

	if got := reasonOf(t, err); got != ReasonUnreachable {
		t.Errorf("reason = %d, want unreachable (%v)", got, err)
	}

	if !strings.Contains(err.Error(), "TLS certificate") {
		t.Errorf("message = %q, want it to mention the certificate", err.Error())
	}
}

func TestEnrollNetworkFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	address := listener.Addr().String()

	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = Enroll(context.Background(), testOptions(t, "http://"+address, nil, nil))

	if got := reasonOf(t, err); got != ReasonUnreachable {
		t.Errorf("reason = %d, want unreachable (%v)", got, err)
	}

	if !strings.Contains(err.Error(), "could not reach") {
		t.Errorf("message = %q, want it to say the server could not be reached", err.Error())
	}
}

func TestEnrollRefusesAnEnrolledMachineUnlessForced(t *testing.T) {
	server, rec := fakeServer(t, http.StatusCreated, successBody(), nil)
	opts := testOptions(t, server.URL, nil, nil)

	existing := config.Default()
	existing.ServerURL = "https://old.example.com"
	existing.MachineID = "11111111-1111-1111-1111-111111111111"

	if err := config.Save(opts.Paths, existing); err != nil {
		t.Fatal(err)
	}

	_, err := Enroll(context.Background(), opts)

	if got := reasonOf(t, err); got != ReasonAlreadyEnrolled {
		t.Errorf("reason = %d, want already enrolled (%v)", got, err)
	}

	if !strings.Contains(err.Error(), existing.MachineID) || !strings.Contains(err.Error(), "--force") {
		t.Errorf("message = %q, want the old machine id and the --force hint", err.Error())
	}

	if rec.Hits() != 0 {
		t.Errorf("server hits = %d, want none before --force", rec.Hits())
	}

	opts.Force = true

	if _, err := Enroll(context.Background(), opts); err != nil {
		t.Fatalf("Enroll(--force) error: %v", err)
	}

	cfg, err := config.Load(opts.Paths)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.MachineID != testMachineID || cfg.ServerURL != server.URL {
		t.Errorf("config = %+v, want the new enrollment", cfg)
	}
}

func TestEnrollReplacesAnUnreadableConfigOnlyWhenForced(t *testing.T) {
	server, _ := fakeServer(t, http.StatusCreated, successBody(), nil)

	var log bytes.Buffer

	opts := testOptions(t, server.URL, nil, &log)

	if err := os.MkdirAll(opts.Paths.ConfigDir, 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(opts.Paths.ConfigFile, []byte("this is not = a config\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Enroll(context.Background(), opts)

	if got := reasonOf(t, err); got != ReasonUnknown || !strings.Contains(err.Error(), "--force") {
		t.Errorf("error = %v, want a hint about --force", err)
	}

	opts.Force = true

	if _, err := Enroll(context.Background(), opts); err != nil {
		t.Fatalf("Enroll(--force) error: %v", err)
	}

	if !strings.Contains(log.String(), "replacing an unreadable configuration file") {
		t.Errorf("log = %q, want the replacement warning", log.String())
	}
}

func TestEnrollPermissionDenied(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root ignores directory modes")
	}

	server, rec := fakeServer(t, http.StatusCreated, successBody(), nil)
	opts := testOptions(t, server.URL, nil, nil)

	if err := os.MkdirAll(opts.Paths.ConfigDir, 0o500); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.Chmod(opts.Paths.ConfigDir, 0o700) }) //nolint:gosec // A directory needs its execute bit to be removed.

	_, err := Enroll(context.Background(), opts)

	if got := reasonOf(t, err); got != ReasonPermission {
		t.Errorf("reason = %d, want permission (%v)", got, err)
	}

	if !strings.Contains(err.Error(), opts.Paths.ConfigDir) || !strings.Contains(err.Error(), "sudo") {
		t.Errorf("message = %q, want the directory and the sudo hint", err.Error())
	}

	if rec.Hits() != 0 {
		t.Errorf("server hits = %d, want none when the files cannot be written", rec.Hits())
	}
}

func TestEnrollOwnership(t *testing.T) {
	tests := []struct {
		name       string
		uid        int
		user       string
		wantOwner  string
		wantChown  bool
		wantIDs    [2]int
		wantReason Reason
	}{
		{name: "root hands the files to the service user", uid: 0, wantOwner: DefaultOwner, wantChown: true, wantIDs: [2]int{998, 998}},
		{name: "root honours --user", uid: 0, user: "svc", wantOwner: "svc", wantChown: true, wantIDs: [2]int{1500, 1500}},
		{name: "root without the service user keeps the files", uid: 0, user: "", wantOwner: "root"},
		{name: "a plain user keeps the files", uid: 1000, wantOwner: "tester"},
		{name: "a plain user may name itself", uid: 1000, user: "tester", wantOwner: "tester"},
		{name: "a plain user may not name someone else", uid: 1000, user: "svc", wantReason: ReasonPermission},
		{name: "an unknown user is refused", uid: 0, user: "nobody-here", wantReason: ReasonInvalidInput},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, rec := fakeServer(t, http.StatusCreated, successBody(), nil)
			sys := &fakeSystem{users: map[string][2]int{DefaultOwner: {998, 998}, "svc": {1500, 1500}}}

			if tt.name == "root without the service user keeps the files" {
				delete(sys.users, DefaultOwner)
			}

			opts := testOptions(t, server.URL, sys, nil)
			opts.User = tt.user
			opts.System.UID = tt.uid

			if tt.uid == 0 {
				opts.System.Username = "root"
			}

			result, err := Enroll(context.Background(), opts)

			if tt.wantReason != ReasonUnknown {
				if got := reasonOf(t, err); got != tt.wantReason {
					t.Errorf("reason = %d, want %d (%v)", got, tt.wantReason, err)
				}

				if rec.Hits() != 0 {
					t.Errorf("server hits = %d, want none when the owner is refused", rec.Hits())
				}

				return
			}

			if err != nil {
				t.Fatalf("Enroll() error: %v", err)
			}

			if result.Owner != tt.wantOwner {
				t.Errorf("owner = %q, want %q", result.Owner, tt.wantOwner)
			}

			if !tt.wantChown {
				if len(sys.calls) != 0 {
					t.Errorf("chown calls = %v, want none", sys.calls)
				}

				return
			}

			want := []chownCall{
				{path: opts.Paths.ConfigFile, uid: tt.wantIDs[0], gid: tt.wantIDs[1]},
				{path: opts.Paths.CredentialFile, uid: tt.wantIDs[0], gid: tt.wantIDs[1]},
			}

			if len(sys.calls) != len(want) || sys.calls[0] != want[0] || sys.calls[1] != want[1] {
				t.Errorf("chown calls = %v, want %v", sys.calls, want)
			}
		})
	}
}

func TestEnrollNeverLogsTheSecrets(t *testing.T) {
	server, _ := fakeServer(t, http.StatusCreated, successBody(), nil)

	var log bytes.Buffer

	opts := testOptions(t, server.URL, nil, &log)

	result, err := Enroll(context.Background(), opts)
	if err != nil {
		t.Fatalf("Enroll() error: %v", err)
	}

	for _, secret := range []string{testToken, testCredential} {
		if strings.Contains(log.String(), secret) {
			t.Errorf("debug log contains a secret: %q", log.String())
		}
	}

	if strings.Contains(strings.Join([]string{result.MachineID, result.Owner, result.NextStep, result.ConfigFile, result.CredentialFile}, " "), testCredential) {
		t.Error("result carries the credential")
	}
}

func TestEnrollPointsAtSystemdWhenTheUnitExists(t *testing.T) {
	server, _ := fakeServer(t, http.StatusCreated, successBody(), nil)
	opts := testOptions(t, server.URL, nil, nil)

	if err := os.WriteFile(opts.System.ServiceUnit, []byte("[Unit]\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := Enroll(context.Background(), opts)
	if err != nil {
		t.Fatalf("Enroll() error: %v", err)
	}

	if result.NextStep != "systemctl enable --now omj-agent" {
		t.Errorf("next step = %q, want the systemctl hint", result.NextStep)
	}
}

func TestEnrollInvalidInput(t *testing.T) {
	tests := []struct {
		name  string
		apply func(*Options)
		want  string
	}{
		{name: "missing server", apply: func(o *Options) { o.ServerURL = "" }, want: "server URL is required"},
		{name: "token without the prefix", apply: func(o *Options) { o.Token = "abc" }, want: "omj_enroll_"},
		{name: "plain http without the flag", apply: func(o *Options) { o.InsecureHTTP = false }, want: "TLS"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, rec := fakeServer(t, http.StatusCreated, successBody(), nil)
			opts := testOptions(t, server.URL, nil, nil)
			tt.apply(&opts)

			_, err := Enroll(context.Background(), opts)

			if got := reasonOf(t, err); got != ReasonInvalidInput {
				t.Errorf("reason = %d, want invalid input (%v)", got, err)
			}

			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("message = %q, want it to contain %q", err.Error(), tt.want)
			}

			if rec.Hits() != 0 {
				t.Errorf("server hits = %d, want none", rec.Hits())
			}
		})
	}
}

func TestEnrollReportsACollectFailure(t *testing.T) {
	server, _ := fakeServer(t, http.StatusCreated, successBody(), nil)
	opts := testOptions(t, server.URL, nil, nil)
	opts.Collect = func(context.Context) (sysinfo.Info, error) { return sysinfo.Info{}, context.Canceled }

	_, err := Enroll(context.Background(), opts)

	if got := reasonOf(t, err); got != ReasonUnknown || !strings.Contains(err.Error(), "collect machine information") {
		t.Errorf("error = %v, want the collect failure", err)
	}
}

func TestDefaultSystemReadsTheHost(t *testing.T) {
	sys := DefaultSystem()

	if sys.UID != os.Getuid() || sys.LookupUser == nil || sys.Chown == nil || sys.ServiceUnit != DefaultServiceUnit {
		t.Errorf("DefaultSystem() = %+v, want the host values", sys)
	}

	if _, _, err := sys.LookupUser("no-such-user-omj"); err == nil {
		t.Error("LookupUser found a user that does not exist")
	}
}
