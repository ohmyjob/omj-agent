package config

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"
)

const secret = "omj_agent_0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKL"

func TestNewCredential(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "valid", value: secret},
		{name: "surrounding whitespace is trimmed", value: "  " + secret + "\n"},
		{name: "wrong prefix", value: "omj_enroll_0123456789abcdef", wantErr: true},
		{name: "prefix without a secret", value: "omj_agent_", wantErr: true},
		{name: "empty", value: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewCredential(tt.value)

			if tt.wantErr {
				if err == nil {
					t.Fatal("NewCredential() error = nil, want an error")
				}

				if value := strings.TrimSpace(tt.value); len(value) > len(CredentialPrefix) && strings.Contains(err.Error(), value) {
					t.Errorf("NewCredential() error = %q leaks the value", err)
				}

				return
			}

			if err != nil {
				t.Fatalf("NewCredential() error = %v", err)
			}

			if got.Secret() != secret {
				t.Errorf("Secret() = %q, want %q", got.Secret(), secret)
			}
		})
	}
}

func TestCredentialNeverFormatsTheSecret(t *testing.T) {
	credential, err := NewCredential(secret)
	if err != nil {
		t.Fatal(err)
	}

	var log bytes.Buffer
	slog.New(slog.NewTextHandler(&log, nil)).Info("enrolled", "credential", credential)

	outputs := map[string]string{
		"String": credential.String(),
		"%v":     fmt.Sprintf("%v", credential),
		"%+v":    fmt.Sprintf("%+v", credential),
		"%#v":    fmt.Sprintf("%#v", credential),
		"%q":     fmt.Sprintf("%q", credential),
		"slog":   log.String(),
	}

	for verb, output := range outputs {
		if strings.Contains(output, secret) {
			t.Errorf("%s output %q leaks the secret", verb, output)
		}

		if !strings.Contains(output, CredentialPrefix) {
			t.Errorf("%s output %q does not show the prefix", verb, output)
		}
	}
}

func TestLoadCredential(t *testing.T) {
	tests := []struct {
		name    string
		content string
		mode    os.FileMode
		uid     int
		wantErr string
	}{
		{name: "valid", content: secret + "\n", mode: 0o600, uid: os.Getuid()},
		{name: "world readable", content: secret, mode: 0o644, uid: os.Getuid(), wantErr: "has mode 0644; it must be 0600"},
		{name: "group readable", content: secret, mode: 0o640, uid: os.Getuid(), wantErr: "has mode 0640; it must be 0600"},
		{name: "owned by another user", content: secret, mode: 0o600, uid: os.Getuid() + 1, wantErr: "is owned by uid"},
		{name: "empty", content: "\n", mode: 0o600, uid: os.Getuid(), wantErr: "credential must start with"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			paths := testPaths(t)

			if err := os.MkdirAll(paths.ConfigDir, 0o750); err != nil {
				t.Fatal(err)
			}

			if err := os.WriteFile(paths.CredentialFile, []byte(tt.content), 0o600); err != nil {
				t.Fatal(err)
			}

			if err := os.Chmod(paths.CredentialFile, tt.mode); err != nil {
				t.Fatal(err)
			}

			got, err := loadCredential(paths.CredentialFile, tt.uid)

			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("loadCredential() error = %v, want it to contain %q", err, tt.wantErr)
				}

				if !strings.Contains(err.Error(), paths.CredentialFile) {
					t.Errorf("loadCredential() error = %v does not name the file", err)
				}

				if strings.Contains(err.Error(), secret) {
					t.Errorf("loadCredential() error = %v leaks the secret", err)
				}

				return
			}

			if err != nil {
				t.Fatalf("loadCredential() error = %v", err)
			}

			if got.Secret() != secret {
				t.Errorf("Secret() = %q, want %q", got.Secret(), secret)
			}
		})
	}
}

func TestLoadCredentialWithoutAFile(t *testing.T) {
	paths := testPaths(t)

	if _, err := LoadCredential(paths); err == nil {
		t.Fatal("LoadCredential() error = nil, want an error for the missing file")
	}
}

func TestSaveCredential(t *testing.T) {
	paths := testPaths(t)

	credential, err := NewCredential(secret)
	if err != nil {
		t.Fatal(err)
	}

	if err := SaveCredential(paths, credential); err != nil {
		t.Fatalf("SaveCredential() error = %v", err)
	}

	info, err := os.Stat(paths.CredentialFile)
	if err != nil {
		t.Fatal(err)
	}

	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("mode = %04o, want 0600", got)
	}

	got, err := LoadCredential(paths)
	if err != nil {
		t.Fatalf("LoadCredential() error = %v", err)
	}

	if got != credential {
		t.Error("LoadCredential() returned a different credential")
	}
}

func TestSaveCredentialRefusesAnEmptyCredential(t *testing.T) {
	paths := testPaths(t)

	if err := SaveCredential(paths, Credential{}); err == nil {
		t.Fatal("SaveCredential() error = nil, want an error")
	}
}
