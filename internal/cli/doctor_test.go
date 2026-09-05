package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestDoctorOnAHealthyHost(t *testing.T) {
	host := healthyHost(t)

	var stdout, stderr bytes.Buffer

	if got := (hostCommand{host: host}).doctor(nil, &stdout, &stderr); got != ExitOK {
		t.Fatalf("doctor = %d, want %d (stderr %q)", got, ExitOK, stderr.String())
	}

	want := strings.Join([]string{
		"PASS  configuration    " + host.Paths.ConfigFile + " is valid",
		"PASS  credential       " + host.Paths.CredentialFile + " has mode 0600 and the right owner",
		"PASS  state directory  " + host.Paths.StateDir + " is writable",
		"PASS  server           https://omj.example.com is reachable, server version 0.3.0 over TLS",
		"PASS  protocol         the server accepts protocol 1",
		"PASS  clock            within 400ms of the server",
		"PASS  service user     the service runs as ohmyjob, like doctor",
		"PASS  service          omj-agent is enabled and active",
		"PASS  hardening        no optional hardening directives are active",
		"PASS  privileges       running as ohmyjob (uid 998)",
		"PASS  execution users  may run work as ohmyjob (uid 998) only; run_as_allowed is not set",
		"",
	}, "\n")

	if stdout.String() != want {
		t.Errorf("stdout =\n%s\nwant\n%s", stdout.String(), want)
	}

	if stderr.Len() != 0 || strings.Contains(stdout.String(), testCredential) {
		t.Errorf("stderr = %q; credential printed = %t", stderr.String(), strings.Contains(stdout.String(), testCredential))
	}
}

func TestDoctorOnAnUnhealthyHost(t *testing.T) {
	host := unhealthyHost(t)

	var stdout, stderr bytes.Buffer

	if got := (hostCommand{host: host}).doctor(nil, &stdout, &stderr); got != ExitError {
		t.Fatalf("doctor = %d, want %d", got, ExitError)
	}

	credential := host.Paths.CredentialFile
	want := strings.Join([]string{
		"PASS  configuration    " + host.Paths.ConfigFile + " is valid",
		"FAIL  credential       " + credential + " has mode 0644; it must be 0600 so only the agent can read it; fix with chmod 0600 " + credential + " and chown root " + credential,
		"PASS  state directory  " + host.Paths.StateDir + " is writable",
		"WARN  server           not checked; fix the configuration and credential first",
		"WARN  protocol         not checked; fix the configuration and credential first",
		"WARN  clock            not checked; the server did not answer",
		"WARN  service user     systemd not present; the service user cannot be checked",
		"WARN  service          systemd not present; start the agent with omj-agent run",
		"WARN  hardening        systemd not present; no hardening directives apply",
		"WARN  privileges       running as root, so every Job runs with root privileges; prefer install.sh --user",
		"PASS  execution users  may run work as root (uid 0) only; run_as_allowed is not set",
		"",
	}, "\n")

	if stdout.String() != want {
		t.Errorf("stdout =\n%s\nwant\n%s", stdout.String(), want)
	}

	if !strings.Contains(stderr.String(), "at least one check failed") || strings.Contains(stdout.String(), testCredential) {
		t.Errorf("stderr = %q; credential printed = %t", stderr.String(), strings.Contains(stdout.String(), testCredential))
	}
}
