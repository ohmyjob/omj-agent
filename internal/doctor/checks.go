package doctor

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ohmyjob/omj-agent/internal/client"
	"github.com/ohmyjob/omj-agent/internal/config"
	"github.com/ohmyjob/omj-agent/internal/protocol"
)

type Status string

const (
	Pass Status = "PASS"
	Warn Status = "WARN"
	Fail Status = "FAIL"

	// MaxClockSkew is how far the two clocks may disagree before leases and
	// heartbeats start expiring at the wrong moments.
	MaxClockSkew = 30 * time.Second

	unit = "omj-agent"
)

var hardeningDirectives = []string{"NoNewPrivileges", "PrivateTmp", "ProtectSystem", "ProtectHome"}

// errSkipped marks a probe that never ran because an earlier check failed.
var errSkipped = errors.New("not checked")

type Check struct {
	Name   string
	Status Status
	Detail string
}

type Report struct {
	Checks []Check
}

func (r Report) Failed() bool {
	for _, check := range r.Checks {
		if check.Status == Fail {
			return true
		}
	}

	return false
}

func Run(ctx context.Context, host Host) Report {
	h := host.WithDefaults()

	cfg, configCheck := h.checkConfig()
	credential, credentialCheck := h.checkCredential()

	probe := Probe{Err: errSkipped}
	if configCheck.Status != Fail && credentialCheck.Status != Fail {
		probe = h.Probe(ctx, cfg, credential)
	}

	runAsCheck := Check{"execution users", Warn, "not checked; fix the configuration first"}
	if configCheck.Status != Fail {
		runAsCheck = h.checkRunAs(cfg)
	}

	return Report{Checks: []Check{
		configCheck,
		credentialCheck,
		h.checkStateDir(),
		h.ServerCheck(cfg, probe),
		checkProtocol(probe),
		checkClock(probe),
		h.checkUnitUser(ctx),
		h.checkUnitState(ctx),
		h.checkHardening(ctx),
		h.checkRoot(),
		runAsCheck,
	}}
}

func (h Host) checkConfig() (config.Config, Check) {
	cfg, err := h.LoadConfig(h.Paths)

	switch {
	case errors.Is(err, os.ErrNotExist):
		return cfg, Check{"configuration", Fail, h.Paths.ConfigFile + " is missing; run omj-agent enroll"}
	case err != nil:
		return cfg, Check{"configuration", Fail, err.Error() + "; fix the file and run omj-agent doctor again"}
	default:
		return cfg, Check{"configuration", Pass, h.Paths.ConfigFile + " is valid"}
	}
}

func (h Host) checkCredential() (config.Credential, Check) {
	credential, err := h.LoadCredential(h.Paths)

	switch {
	case errors.Is(err, os.ErrNotExist):
		return credential, Check{"credential", Fail, h.Paths.CredentialFile + " is missing; run omj-agent enroll"}
	case err != nil:
		return credential, Check{"credential", Fail, fmt.Sprintf("%v; fix with chmod 0600 %s and chown %s %s", err, h.Paths.CredentialFile, h.Username, h.Paths.CredentialFile)}
	default:
		return credential, Check{"credential", Pass, h.Paths.CredentialFile + " has mode 0600 and the right owner"}
	}
}

func (h Host) checkStateDir() Check {
	dir := h.Paths.StateDir

	if _, err := os.Stat(dir); err != nil {
		return Check{"state directory", Fail, fmt.Sprintf("%s: %v; create it with mkdir -p %s && chown %s %s", dir, err, dir, h.Username, dir)}
	}

	probe, err := os.CreateTemp(dir, ".doctor-*")
	if err != nil {
		return Check{"state directory", Fail, fmt.Sprintf("%s is not writable by %s: %v; fix with chown %s %s", dir, h.Username, err, h.Username, dir)}
	}

	_ = probe.Close()
	_ = os.Remove(probe.Name())

	return Check{"state directory", Pass, dir + " is writable"}
}

func (h Host) ServerCheck(cfg config.Config, probe Probe) Check {
	var (
		apiErr  *client.APIError
		certErr *tls.CertificateVerificationError
		netErr  net.Error
	)

	switch {
	case errors.Is(probe.Err, errSkipped):
		return Check{"server", Warn, "not checked; fix the configuration and credential first"}
	case errors.Is(probe.Err, client.ErrNoCredential):
		return Check{"server", Fail, "not enrolled; run omj-agent enroll"}
	case errors.As(probe.Err, &certErr):
		return Check{"server", Fail, fmt.Sprintf("could not verify the TLS certificate of %s: %v; if the server uses its own certificate authority, install it on this machine", cfg.ServerURL, certErr)}
	case client.IsUnauthorized(probe.Err):
		return Check{"server", Fail, "the server rejected the credential; run omj-agent enroll --force with a new token"}
	case errors.As(probe.Err, &apiErr):
		return reachableCheck(cfg, probe.ServerVersion)
	case errors.As(probe.Err, &netErr), errors.Is(probe.Err, context.DeadlineExceeded):
		return Check{"server", Fail, fmt.Sprintf("could not reach %s: %v", cfg.ServerURL, probe.Err)}
	case probe.Err != nil:
		return Check{"server", Fail, fmt.Sprintf("%s answered unexpectedly: %v", cfg.ServerURL, probe.Err)}
	default:
		return reachableCheck(cfg, probe.ServerVersion)
	}
}

func reachableCheck(cfg config.Config, serverVersion string) Check {
	detail := cfg.ServerURL + " is reachable"
	if serverVersion != "" {
		detail += ", server version " + serverVersion
	}

	if cfg.InsecureHTTP {
		return Check{"server", Warn, detail + "; the connection is plain HTTP, so the credential and every command travel unencrypted"}
	}

	return Check{"server", Pass, detail + " over TLS"}
}

func checkProtocol(probe Probe) Check {
	var apiErr *client.APIError

	switch {
	case errors.Is(probe.Err, errSkipped):
		return Check{"protocol", Warn, "not checked; fix the configuration and credential first"}
	case client.IsUnsupportedProtocol(probe.Err) && errors.As(probe.Err, &apiErr):
		return Check{"protocol", Fail, versionRejected(apiErr)}
	case probe.Err != nil:
		return Check{"protocol", Warn, "not checked; the server did not answer"}
	default:
		return Check{"protocol", Pass, fmt.Sprintf("the server accepts protocol %d", protocol.ProtocolVersion)}
	}
}

func versionRejected(apiErr *client.APIError) string {
	detail := "the server rejected this agent: " + apiErr.Message

	if len(apiErr.SupportedProtocolVersions) > 0 {
		versions := make([]string, len(apiErr.SupportedProtocolVersions))
		for i, v := range apiErr.SupportedProtocolVersions {
			versions[i] = strconv.Itoa(v)
		}

		detail += fmt.Sprintf(" (this agent speaks protocol %d, the server supports %s)", protocol.ProtocolVersion, strings.Join(versions, ", "))
	}

	if apiErr.MinAgentVersion != "" {
		detail += "; the server requires agent " + apiErr.MinAgentVersion + " or newer"
	}

	return detail + "; update the agent"
}

func checkClock(probe Probe) Check {
	if probe.Err != nil {
		return Check{"clock", Warn, "not checked; the server did not answer"}
	}

	skew := probe.Skew.Round(100 * time.Millisecond)

	if skew.Abs() > MaxClockSkew {
		return Check{"clock", Fail, fmt.Sprintf("this machine is %s %s the server; enable time synchronisation (timedatectl set-ntp true)", skew.Abs(), aheadOrBehind(skew))}
	}

	return Check{"clock", Pass, fmt.Sprintf("within %s of the server", skew.Abs())}
}

func aheadOrBehind(skew time.Duration) string {
	if skew < 0 {
		return "ahead of"
	}

	return "behind"
}

func (h Host) checkUnitUser(ctx context.Context) Check {
	if !h.Systemctl.Available() {
		return Check{"service user", Warn, "systemd not present; the service user cannot be checked"}
	}

	properties, err := h.show(ctx, "LoadState", "User")
	if err != nil {
		return Check{"service user", Warn, "systemctl failed: " + err.Error()}
	}

	if properties["LoadState"] != "loaded" {
		return Check{"service user", Warn, unit + ".service is not installed"}
	}

	serviceUser := properties["User"]
	if serviceUser == "" {
		serviceUser = "root"
	}

	if serviceUser != h.Username {
		return Check{"service user", Warn, fmt.Sprintf("doctor runs as %s but the service runs as %s; the file checks above reflect %s", h.Username, serviceUser, h.Username)}
	}

	return Check{"service user", Pass, "the service runs as " + serviceUser + ", like doctor"}
}

func (h Host) checkUnitState(ctx context.Context) Check {
	if !h.Systemctl.Available() {
		return Check{"service", Warn, "systemd not present; start the agent with omj-agent run"}
	}

	enabled, _ := h.Systemctl.Run(ctx, "is-enabled", unit)
	active, _ := h.Systemctl.Run(ctx, "is-active", unit)

	if enabled == "" {
		enabled = "not installed"
	}

	if active == "" {
		active = "unknown"
	}

	if enabled != "enabled" || active != "active" {
		return Check{"service", Fail, fmt.Sprintf("%s is %s and %s; run systemctl enable --now %s", unit, enabled, active, unit)}
	}

	return Check{"service", Pass, unit + " is enabled and active"}
}

func (h Host) checkHardening(ctx context.Context) Check {
	if !h.Systemctl.Available() {
		return Check{"hardening", Warn, "systemd not present; no hardening directives apply"}
	}

	properties, err := h.show(ctx, hardeningDirectives...)
	if err != nil {
		return Check{"hardening", Warn, "systemctl failed: " + err.Error()}
	}

	var active []string

	for _, directive := range hardeningDirectives {
		value := properties[directive]
		if value == "" || value == "no" {
			continue
		}

		if value == "yes" {
			active = append(active, directive)
		} else {
			active = append(active, directive+"="+value)
		}
	}

	if properties["NoNewPrivileges"] == "yes" {
		return Check{"hardening", Warn, "NoNewPrivileges is on, so sudo inside Jobs will fail; active: " + strings.Join(active, ", ")}
	}

	if len(active) == 0 {
		return Check{"hardening", Pass, "no optional hardening directives are active"}
	}

	return Check{"hardening", Pass, "active: " + strings.Join(active, ", ")}
}

func (h Host) checkRoot() Check {
	if h.UID == 0 {
		return Check{"privileges", Warn, "running as root, so every Job runs with root privileges; prefer install.sh --user"}
	}

	return Check{"privileges", Pass, fmt.Sprintf("running as %s (uid %d)", h.Username, h.UID)}
}

// checkRunAs reports the allowlist as the Agent resolves it, so an entry the
// Agent could not honour is named here instead of only stopping the daemon.
func (h Host) checkRunAs(cfg config.Config) Check {
	runAs := config.ResolveRunAs(cfg, config.RunAsHost{UID: h.UID, Username: h.Username, Lookup: h.LookupUser})

	var usable, problems []string

	for _, allowed := range runAs.Users {
		if allowed.Err != nil {
			problems = append(problems, allowed.Err.Error())

			continue
		}

		usable = append(usable, fmt.Sprintf("%s (uid %d)", allowed.Name, allowed.UID))
	}

	detail := "may run work as " + strings.Join(usable, ", ")
	if len(usable) == 0 {
		detail = "no user to run work as; this machine has no name for uid " + strconv.Itoa(h.UID)
	}

	if len(problems) > 0 {
		return Check{"execution users", Fail, detail + "; " + strings.Join(problems, "; ")}
	}

	if len(cfg.RunAsAllowed) == 0 {
		return Check{"execution users", Pass, detail + " only; run_as_allowed is not set"}
	}

	return Check{"execution users", Pass, detail}
}

func (h Host) show(ctx context.Context, properties ...string) (map[string]string, error) {
	out, err := h.Systemctl.Run(ctx, "show", unit, "-p", strings.Join(properties, ","))
	if err != nil {
		return nil, err
	}

	values := make(map[string]string, len(properties))

	for line := range strings.SplitSeq(out, "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if found {
			values[key] = value
		}
	}

	return values, nil
}
