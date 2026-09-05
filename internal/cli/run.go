package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/ohmyjob/omj-agent/internal/agent"
	"github.com/ohmyjob/omj-agent/internal/client"
	"github.com/ohmyjob/omj-agent/internal/config"
	"github.com/ohmyjob/omj-agent/internal/output"
	"github.com/ohmyjob/omj-agent/internal/runner"
	"github.com/ohmyjob/omj-agent/internal/state"
	"github.com/ohmyjob/omj-agent/internal/sysinfo"
)

// ExitForcedStop tells a supervisor that the agent was stopped by a second
// signal before every Run was reported, which is not a configuration error.
const ExitForcedStop = 3

type daemon interface {
	Run(ctx context.Context) error
	Wait()
}

type runCommand struct {
	paths          config.Paths
	loadConfig     func(config.Paths) (config.Config, error)
	loadCredential func(config.Paths) (config.Credential, error)
	collect        func(context.Context) (sysinfo.Info, error)
	loadState      func(path string) (*state.Store, error)
	lookupUser     func(name string) (uid int, err error)
	newAgent       func(agent.Options) (daemon, error)
}

func runRun(args []string, stdout, stderr io.Writer) int {
	return runCommand{
		paths:          config.DefaultPaths(),
		loadConfig:     config.Load,
		loadCredential: config.LoadCredential,
		collect:        sysinfo.Collect,
		loadState:      state.Load,
		lookupUser:     config.LookupUser,
		newAgent:       newDaemon,
	}.run(args, stdout, stderr)
}

func newDaemon(opts agent.Options) (daemon, error) {
	return agent.New(opts)
}

func (c runCommand) run(args []string, stdout, stderr io.Writer) int {
	var logLevel, logFormat string

	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&logLevel, "log-level", "", "debug, info, warn or error (defaults to log_level in agent.conf)")
	flags.StringVar(&logFormat, "log-format", "text", "text or json")

	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitOK
		}

		return ExitUsage
	}

	if logFormat != "text" && logFormat != "json" {
		fmt.Fprintf(stderr, "omj-agent run: --log-format must be text or json, got %q\n", logFormat)

		return ExitUsage
	}

	if _, ok := parseLevel(logLevel); logLevel != "" && !ok {
		fmt.Fprintf(stderr, "omj-agent run: --log-level must be debug, info, warn or error, got %q\n", logLevel)

		return ExitUsage
	}

	cfg, err := c.loadConfig(c.paths)
	if err != nil {
		fmt.Fprintf(stderr, "omj-agent run: %v\n", err)

		return ExitError
	}

	if logLevel == "" {
		logLevel = cfg.LogLevel
	}

	level, _ := parseLevel(logLevel)
	logger := newLogger(stdout, logFormat, level)

	a, err := c.build(context.Background(), cfg, logger)
	if err != nil {
		fmt.Fprintf(stderr, "omj-agent run: %v\n", err)

		return ExitError
	}

	err = a.Run(context.Background())
	a.Wait()

	switch {
	case errors.Is(err, agent.ErrForcedStop):
		fmt.Fprintf(stderr, "omj-agent run: %v\n", err)

		return ExitForcedStop
	case err != nil:
		fmt.Fprintf(stderr, "omj-agent run: %v\n", err)

		return ExitError
	default:
		return ExitOK
	}
}

func (c runCommand) build(ctx context.Context, cfg config.Config, logger *slog.Logger) (daemon, error) {
	credential, err := c.loadCredential(c.paths)
	if err != nil {
		return nil, err
	}

	apiClient, err := client.New(client.Options{ServerURL: cfg.ServerURL, Credential: credential, InsecureHTTP: cfg.InsecureHTTP, Logger: logger})
	if err != nil {
		return nil, err
	}

	info, err := c.collect(ctx)
	if err != nil {
		return nil, fmt.Errorf("collect machine information: %w", err)
	}

	store, err := c.loadState(c.paths.StateFile)
	if err != nil {
		return nil, err
	}

	// An allowlist this agent could not honour is refused here rather than
	// reported to the Server as if it were true.
	runAs := config.ResolveRunAs(cfg, config.RunAsHost{UID: info.AgentUID, Username: info.AgentUser, Lookup: c.lookupUser})
	if err := runAs.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", c.paths.ConfigFile, err)
	}

	return c.newAgent(agent.Options{
		Config:       cfg,
		RunAsAllowed: runAs.Names(),

		Client: apiClient,
		Info:   info,
		State:  store,
		Runner: runner.Runner{MaxTimeout: time.Duration(cfg.MaxTimeoutSeconds) * time.Second},
		Buffer: output.NewBuffer(output.BufferOptions{}),
		Logger: logger,
	})
}

func parseLevel(name string) (slog.Level, bool) {
	switch name {
	case "debug":
		return slog.LevelDebug, true
	case "info":
		return slog.LevelInfo, true
	case "warn":
		return slog.LevelWarn, true
	case "error":
		return slog.LevelError, true
	default:
		return slog.LevelInfo, false
	}
}

// newLogger writes to stdout because journald captures it under systemd.
func newLogger(stdout io.Writer, format string, level slog.Level) *slog.Logger {
	opts := &slog.HandlerOptions{Level: level}

	if format == "json" {
		return slog.New(slog.NewJSONHandler(stdout, opts))
	}

	return slog.New(slog.NewTextHandler(stdout, opts))
}
