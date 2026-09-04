package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validConfig() Config {
	cfg := Default()
	cfg.ServerURL = "https://jobs.home.example"
	cfg.MachineID = "0f7a1a3c-4c1c-4a4e-9d2d-4b7a4b3f0f11"

	return cfg
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{name: "valid", mutate: func(*Config) {}},
		{name: "http with insecure_http", mutate: func(c *Config) { c.ServerURL, c.InsecureHTTP = "http://jobs.lan:8000", true }},
		{name: "path prefix", mutate: func(c *Config) { c.ServerURL = "https://home.example/omj" }},
		{name: "concurrency at the upper bound", mutate: func(c *Config) { c.MaxConcurrentRuns = 64 }},
		{name: "missing server_url", mutate: func(c *Config) { c.ServerURL = "" }, wantErr: "server_url is required"},
		{name: "http without insecure_http", mutate: func(c *Config) { c.ServerURL = "http://jobs.lan:8000" }, wantErr: "TLS"},
		{name: "unsupported scheme", mutate: func(c *Config) { c.ServerURL = "ftp://jobs.lan" }, wantErr: "must use https"},
		{name: "not a url", mutate: func(c *Config) { c.ServerURL = "jobs.lan" }, wantErr: "not a valid URL"},
		{name: "unknown log level", mutate: func(c *Config) { c.LogLevel = "verbose" }, wantErr: "log_level must be one of debug, info, warn, error"},
		{name: "no concurrency", mutate: func(c *Config) { c.MaxConcurrentRuns = 0 }, wantErr: "max_concurrent_runs must be between 1 and 64"},
		{name: "too much concurrency", mutate: func(c *Config) { c.MaxConcurrentRuns = 65 }, wantErr: "max_concurrent_runs must be between 1 and 64"},
		{name: "no timeout", mutate: func(c *Config) { c.MaxTimeoutSeconds = 0 }, wantErr: "max_timeout_seconds must be at least 1"},
		{name: "no output", mutate: func(c *Config) { c.MaxOutputBytes = 0 }, wantErr: "max_output_bytes must be at least 1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.mutate(&cfg)

			err := cfg.Validate()

			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v", err)
				}

				return
			}

			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Validate() error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestSaveAndLoad(t *testing.T) {
	paths := testPaths(t)
	cfg := validConfig()
	cfg.InsecureHTTP = true
	cfg.ServerURL = "http://jobs.lan:8000"

	if err := Save(paths, cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	info, err := os.Stat(paths.ConfigFile)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}

	if got := info.Mode().Perm(); got != 0o640 {
		t.Errorf("mode = %04o, want 0640", got)
	}

	got, err := Load(paths)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got != cfg {
		t.Errorf("Load() = %+v, want %+v", got, cfg)
	}

	if entries, _ := os.ReadDir(paths.ConfigDir); len(entries) != 1 {
		t.Errorf("configuration directory holds %d entries, want only agent.conf", len(entries))
	}
}

func TestSaveRefusesAnInvalidConfig(t *testing.T) {
	paths := testPaths(t)
	cfg := validConfig()
	cfg.MaxConcurrentRuns = 0

	if err := Save(paths, cfg); err == nil {
		t.Fatal("Save() error = nil, want a validation error")
	}

	if _, err := os.Stat(paths.ConfigFile); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Stat() error = %v, want the file to be absent", err)
	}
}

func TestLoadReportsProblemsWithTheFileName(t *testing.T) {
	paths := testPaths(t)

	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{name: "missing file", wantErr: "read configuration"},
		{name: "parse error", content: "server_url\n", wantErr: paths.ConfigFile + ": line 1: expected key = value"},
		{name: "validation error", content: "server_url = http://jobs.lan\n", wantErr: paths.ConfigFile + ": server_url"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.content != "" {
				if err := os.MkdirAll(paths.ConfigDir, 0o750); err != nil {
					t.Fatal(err)
				}

				if err := os.WriteFile(paths.ConfigFile, []byte(tt.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			_, err := Load(paths)

			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Load() error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func testPaths(t *testing.T) Paths {
	t.Helper()

	dir := t.TempDir()
	t.Setenv("OMJ_CONFIG_DIR", filepath.Join(dir, "etc"))
	t.Setenv("OMJ_STATE_DIR", filepath.Join(dir, "state"))

	return DefaultPaths()
}
