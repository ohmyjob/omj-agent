package config

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	full := Config{
		ServerURL:         "https://jobs.home.example",
		MachineID:         "0f7a1a3c-4c1c-4a4e-9d2d-4b7a4b3f0f11",
		InsecureHTTP:      true,
		LogLevel:          "debug",
		MaxConcurrentRuns: 8,
		MaxTimeoutSeconds: 3600,
		MaxOutputBytes:    1048576,
	}

	tests := []struct {
		name    string
		input   string
		want    Config
		wantErr string
	}{
		{name: "empty file keeps the defaults", input: "", want: Default()},
		{
			name: "every key",
			input: `server_url = https://jobs.home.example
machine_id = 0f7a1a3c-4c1c-4a4e-9d2d-4b7a4b3f0f11
insecure_http = true
log_level = debug
max_concurrent_runs = 8
max_timeout_seconds = 3600
max_output_bytes = 1048576
`,
			want: full,
		},
		{
			name: "comments, blank lines, quotes and missing spaces",
			input: `# Written by omj-agent enroll

server_url="https://jobs.home.example"
  machine_id =   "0f7a1a3c-4c1c-4a4e-9d2d-4b7a4b3f0f11"
insecure_http=true
log_level = "debug"
max_concurrent_runs=8
max_timeout_seconds = 3600
max_output_bytes = 1048576
`,
			want: full,
		},
		{name: "a lone quote is kept", input: `server_url = "https://x`, want: withServerURL(`"https://x`)},
		{name: "unknown key", input: "server_url = https://x\nshell = /bin/zsh\n", wantErr: `line 2: unknown key "shell"`},
		{name: "missing equals sign", input: "\n\nserver_url https://x\n", wantErr: "line 3: expected key = value"},
		{name: "duplicate key", input: "log_level = info\nlog_level = debug\n", wantErr: `line 2: duplicate key "log_level"`},
		{name: "invalid boolean", input: "insecure_http = yes\n", wantErr: `line 1: insecure_http: invalid value "yes"`},
		{name: "invalid integer", input: "max_concurrent_runs = four\n", wantErr: `line 1: max_concurrent_runs: invalid value "four"`},
		{name: "invalid byte count", input: "max_output_bytes = 1MiB\n", wantErr: `line 1: max_output_bytes: invalid value "1MiB"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(strings.NewReader(tt.input))

			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Parse() error = %v, want it to contain %q", err, tt.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}

			if got != tt.want {
				t.Errorf("Parse() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func withServerURL(serverURL string) Config {
	cfg := Default()
	cfg.ServerURL = serverURL

	return cfg
}
