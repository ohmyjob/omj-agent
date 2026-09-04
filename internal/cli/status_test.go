package cli

import (
	"bytes"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/ohmyjob/omj-agent/internal/config"
	"github.com/ohmyjob/omj-agent/internal/doctor"
	"github.com/ohmyjob/omj-agent/internal/state"
)

func TestStatusOnAHealthyHost(t *testing.T) {
	host := healthyHost(t)

	var stdout, stderr bytes.Buffer

	if got := (hostCommand{host: host}).status(nil, &stdout, &stderr); got != ExitOK {
		t.Fatalf("status = %d, want %d (stderr %q)", got, ExitOK, stderr.String())
	}

	want := strings.Join([]string{
		"Configuration   " + host.Paths.ConfigFile,
		"Server URL      https://omj.example.com",
		"Machine         " + testMachineID,
		"User            ohmyjob (uid 998)",
		"Limits          4 concurrent runs, timeout up to 259200 s, output up to 104857600 bytes",
		"Server          PASS https://omj.example.com is reachable, server version 0.3.0 over TLS; server time 2026-09-04T12:00:00Z; clock skew +400ms",
		"Active runs     1",
		"  " + testRunID + "  pid 4242  started 2026-09-04T11:58:00Z",
		"Service         active",
		"",
	}, "\n")

	if stdout.String() != want {
		t.Errorf("stdout =\n%s\nwant\n%s", stdout.String(), want)
	}

	if strings.Contains(stdout.String(), testCredential) {
		t.Error("the credential was printed")
	}
}

func TestStatusOnAnUnhealthyHost(t *testing.T) {
	host := unhealthyHost(t)

	var stdout, stderr bytes.Buffer

	if got := (hostCommand{host: host}).status(nil, &stdout, &stderr); got != ExitOK {
		t.Fatalf("status = %d, want %d: a status report never fails (stderr %q)", got, ExitOK, stderr.String())
	}

	want := strings.Join([]string{
		"Configuration   " + host.Paths.ConfigFile,
		"Server URL      https://omj.example.com",
		"Machine         " + testMachineID,
		"User            root (uid 0)",
		"Limits          4 concurrent runs, timeout up to 259200 s, output up to 104857600 bytes",
		"Server          FAIL " + host.Paths.CredentialFile + " has mode 0644; it must be 0600 so only the agent can read it",
		"Active runs     1",
		"  " + testRunID + "  pid 4242  started 2026-09-04T11:58:00Z",
		"Service         systemd not present",
		"",
	}, "\n")

	if stdout.String() != want {
		t.Errorf("stdout =\n%s\nwant\n%s", stdout.String(), want)
	}
}

func TestStatusWorksOffline(t *testing.T) {
	host := healthyHost(t)
	host.Dial = func(config.Config, config.Credential) (doctor.Pinger, error) {
		return fakePinger{err: &net.OpError{Op: "dial", Err: errors.New("connection refused")}}, nil
	}

	var stdout, stderr bytes.Buffer

	if got := (hostCommand{host: host}).status(nil, &stdout, &stderr); got != ExitOK {
		t.Fatalf("status = %d, want %d", got, ExitOK)
	}

	if want := "Server          FAIL could not reach https://omj.example.com: dial: connection refused\n"; !strings.Contains(stdout.String(), want) {
		t.Errorf("stdout = %q, want it to contain %q", stdout.String(), want)
	}

	if strings.Contains(stdout.String()+stderr.String(), "goroutine") {
		t.Error("a stack trace was printed")
	}
}

func TestStatusBeforeEnrollment(t *testing.T) {
	host := healthyHost(t)

	if err := os.Remove(host.Paths.ConfigFile); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer

	if got := (hostCommand{host: host}).status(nil, &stdout, &stderr); got != ExitOK {
		t.Fatalf("status = %d, want %d", got, ExitOK)
	}

	for _, line := range []string{"Server URL      not enrolled; run omj-agent enroll", "Machine         not enrolled", "Limits          unknown", "Server          not checked; fix the configuration first"} {
		if !strings.Contains(stdout.String(), line) {
			t.Errorf("stdout = %q, want it to contain %q", stdout.String(), line)
		}
	}
}

func TestStatusWithABrokenConfiguration(t *testing.T) {
	host := healthyHost(t)

	if err := os.WriteFile(host.Paths.ConfigFile, []byte("server_url = ftp://omj.example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer

	hostCommand{host: host}.status(nil, &stdout, &stderr)

	if !strings.Contains(stdout.String(), "Server URL      FAIL "+host.Paths.ConfigFile) || !strings.Contains(stdout.String(), "Machine         unknown") {
		t.Errorf("stdout = %q, want the configuration error", stdout.String())
	}
}

func TestStatusReportsAnUnreadableStateFile(t *testing.T) {
	host := healthyHost(t)
	host.LoadState = func(string) (*state.Store, error) { return nil, errors.New("read state: permission denied") }

	var stdout, stderr bytes.Buffer

	hostCommand{host: host}.status(nil, &stdout, &stderr)

	if !strings.Contains(stdout.String(), "Active runs     FAIL read state: permission denied") {
		t.Errorf("stdout = %q, want the state error", stdout.String())
	}
}

func TestStatusWithoutAnInstalledUnit(t *testing.T) {
	host := healthyHost(t)
	host.Systemctl = fakeSystemctl{available: true}

	var stdout, stderr bytes.Buffer

	hostCommand{host: host}.status(nil, &stdout, &stderr)

	if !strings.Contains(stdout.String(), "Service         omj-agent is not installed") {
		t.Errorf("stdout = %q, want the missing unit", stdout.String())
	}
}

func TestStatusAndDoctorRejectArguments(t *testing.T) {
	for _, run := range map[string]func([]string, io.Writer, io.Writer) int{"status": hostCommand{}.status, "doctor": hostCommand{}.doctor} {
		var stdout, stderr bytes.Buffer

		if got := run([]string{"extra"}, &stdout, &stderr); got != ExitUsage || !strings.Contains(stderr.String(), "takes no arguments") {
			t.Errorf("code = %d, stderr = %q, want usage error", got, stderr.String())
		}

		if got := run([]string{"--help"}, &stdout, &stderr); got != ExitOK {
			t.Errorf("--help = %d, want %d", got, ExitOK)
		}
	}
}
