// Package config loads and saves the agent's configuration and credential.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/ohmyjob/omj-agent/internal/atomicfile"
)

type Paths struct {
	ConfigDir      string
	ConfigFile     string
	CredentialFile string
	StateDir       string
	StateFile      string
}

type Config struct {
	ServerURL         string
	MachineID         string
	InsecureHTTP      bool
	LogLevel          string
	MaxConcurrentRuns int
	MaxTimeoutSeconds int
	MaxOutputBytes    int64
	RunAsAllowed      []string
}

const (
	configFileMode os.FileMode = 0o640
	configDirMode  os.FileMode = 0o750

	minConcurrentRuns = 1
	maxConcurrentRuns = 64
)

var logLevels = []string{"debug", "info", "warn", "error"}

func Default() Config {
	return Config{
		LogLevel:          "info",
		MaxConcurrentRuns: 4,
		MaxTimeoutSeconds: 259200,
		MaxOutputBytes:    104857600,
	}
}

func Load(paths Paths) (Config, error) {
	data, err := os.ReadFile(paths.ConfigFile)
	if err != nil {
		return Config{}, fmt.Errorf("read configuration: %w", err)
	}

	cfg, err := Parse(bytes.NewReader(data))
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", paths.ConfigFile, err)
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", paths.ConfigFile, err)
	}

	return cfg, nil
}

func (c Config) Validate() error {
	if err := c.validateServerURL(); err != nil {
		return err
	}

	if !slices.Contains(logLevels, c.LogLevel) {
		return fmt.Errorf("log_level must be one of %s, got %q", strings.Join(logLevels, ", "), c.LogLevel)
	}

	if c.MaxConcurrentRuns < minConcurrentRuns || c.MaxConcurrentRuns > maxConcurrentRuns {
		return fmt.Errorf("max_concurrent_runs must be between %d and %d, got %d", minConcurrentRuns, maxConcurrentRuns, c.MaxConcurrentRuns)
	}

	if c.MaxTimeoutSeconds < 1 {
		return fmt.Errorf("max_timeout_seconds must be at least 1, got %d", c.MaxTimeoutSeconds)
	}

	if c.MaxOutputBytes < 1 {
		return fmt.Errorf("max_output_bytes must be at least 1, got %d", c.MaxOutputBytes)
	}

	return c.validateRunAsAllowed()
}

// The file alone cannot say whether a user exists or whether this Agent could
// ever become it; that needs the machine and is ResolveRunAs.
func (c Config) validateRunAsAllowed() error {
	seen := make(map[string]bool, len(c.RunAsAllowed))

	for _, name := range c.RunAsAllowed {
		if name == "" {
			return errors.New("run_as_allowed has an empty entry; separate the users with a single comma")
		}

		if seen[name] {
			return fmt.Errorf("run_as_allowed lists %q twice", name)
		}

		seen[name] = true
	}

	return nil
}

func (c Config) validateServerURL() error {
	if c.ServerURL == "" {
		return errors.New("server_url is required")
	}

	parsed, err := url.Parse(c.ServerURL)
	if err != nil || parsed.Host == "" {
		return fmt.Errorf("server_url %q is not a valid URL", c.ServerURL)
	}

	switch parsed.Scheme {
	case "https":
		return nil
	case "http":
		if c.InsecureHTTP {
			return nil
		}

		return fmt.Errorf("server_url %q uses plain http, so the connection would not be protected by TLS; use https or set insecure_http = true", c.ServerURL)
	default:
		return fmt.Errorf("server_url %q must use https (or http with insecure_http = true)", c.ServerURL)
	}
}

func Save(paths Paths, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(paths.ConfigFile), configDirMode); err != nil {
		return fmt.Errorf("create configuration directory: %w", err)
	}

	if err := atomicfile.Write(paths.ConfigFile, []byte(cfg.String()), configFileMode); err != nil {
		return fmt.Errorf("save configuration: %w", err)
	}

	return nil
}

func (c Config) String() string {
	var b strings.Builder

	fmt.Fprintf(&b, "server_url = %s\n", c.ServerURL)
	fmt.Fprintf(&b, "machine_id = %s\n", c.MachineID)
	fmt.Fprintf(&b, "insecure_http = %s\n", strconv.FormatBool(c.InsecureHTTP))
	fmt.Fprintf(&b, "log_level = %s\n", c.LogLevel)
	fmt.Fprintf(&b, "max_concurrent_runs = %d\n", c.MaxConcurrentRuns)
	fmt.Fprintf(&b, "max_timeout_seconds = %d\n", c.MaxTimeoutSeconds)
	fmt.Fprintf(&b, "max_output_bytes = %d\n", c.MaxOutputBytes)

	// An absent key means the agent's own user and nothing else, so a machine
	// without an allowlist keeps a file an older agent still reads.
	if len(c.RunAsAllowed) > 0 {
		fmt.Fprintf(&b, "run_as_allowed = %s\n", strings.Join(c.RunAsAllowed, ", "))
	}

	return b.String()
}
