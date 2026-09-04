package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/ohmyjob/omj-agent/internal/config"
	"github.com/ohmyjob/omj-agent/internal/doctor"
)

// probeTimeout bounds the one ping status and doctor make; a report must not
// hang on a server that never answers.
const probeTimeout = 15 * time.Second

type hostCommand struct {
	host doctor.Host
}

func runStatus(args []string, stdout, stderr io.Writer) int {
	return hostCommand{host: doctor.DefaultHost()}.status(args, stdout, stderr)
}

// status is a report, so it exits 0 whatever it finds; only a usage error
// is a failure.
func (c hostCommand) status(args []string, stdout, stderr io.Writer) int {
	if stop, code := parseNoFlags("status", args, stderr); stop {
		return code
	}

	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()

	h := c.host.WithDefaults()
	row := func(label, value string) { fmt.Fprintf(stdout, "%-16s%s\n", label, value) }

	row("Configuration", h.Paths.ConfigFile)

	cfg, err := h.LoadConfig(h.Paths)

	switch {
	case errors.Is(err, os.ErrNotExist):
		row("Server URL", "not enrolled; run omj-agent enroll")
		row("Machine", "not enrolled")
	case err != nil:
		row("Server URL", "FAIL "+err.Error())
		row("Machine", "unknown")
	default:
		row("Server URL", cfg.ServerURL)
		row("Machine", cfg.MachineID)
	}

	row("User", fmt.Sprintf("%s (uid %d)", h.Username, h.UID))

	if err == nil {
		row("Limits", fmt.Sprintf("%d concurrent runs, timeout up to %d s, output up to %d bytes", cfg.MaxConcurrentRuns, cfg.MaxTimeoutSeconds, cfg.MaxOutputBytes))
		row("Server", c.serverLine(ctx, h, cfg))
	} else {
		row("Limits", "unknown")
		row("Server", "not checked; fix the configuration first")
	}

	c.activeRuns(h, row, stdout)
	row("Service", c.serviceLine(ctx, h))

	return ExitOK
}

func (c hostCommand) serverLine(ctx context.Context, h doctor.Host, cfg config.Config) string {
	credential, err := h.LoadCredential(h.Paths)
	if err != nil {
		return "FAIL " + err.Error()
	}

	probe := h.Probe(ctx, cfg, credential)
	check := h.ServerCheck(cfg, probe)
	line := string(check.Status) + " " + check.Detail

	if probe.Err == nil {
		line += fmt.Sprintf("; server time %s; clock skew %s", probe.Response.ServerTime.UTC().Format(time.RFC3339), signed(probe.Skew.Round(100*time.Millisecond)))
	}

	return line
}

func (c hostCommand) activeRuns(h doctor.Host, row func(string, string), stdout io.Writer) {
	store, err := h.LoadState(h.Paths.StateFile)
	if err != nil {
		row("Active runs", "FAIL "+err.Error())

		return
	}

	active := store.Active()
	if len(active) == 0 {
		row("Active runs", "none")

		return
	}

	row("Active runs", fmt.Sprint(len(active)))

	for _, run := range active {
		fmt.Fprintf(stdout, "  %s  pid %d  started %s\n", run.RunID, run.PID, run.StartedAt.UTC().Format(time.RFC3339))
	}
}

func (c hostCommand) serviceLine(ctx context.Context, h doctor.Host) string {
	if !h.Systemctl.Available() {
		return "systemd not present"
	}

	active, _ := h.Systemctl.Run(ctx, "is-active", "omj-agent")
	if active == "" {
		return "omj-agent is not installed"
	}

	return active
}

func signed(d time.Duration) string {
	if d < 0 {
		return "-" + d.Abs().String()
	}

	return "+" + d.String()
}

// parseNoFlags reports whether the command is already finished (help shown
// or bad input) and the exit code to use in that case.
func parseNoFlags(name string, args []string, stderr io.Writer) (stop bool, code int) {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(stderr)

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return true, ExitOK
		}

		return true, ExitUsage
	}

	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "omj-agent %s takes no arguments\n", name)

		return true, ExitUsage
	}

	return false, ExitOK
}
