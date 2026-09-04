package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/ohmyjob/omj-agent/internal/atomicfile"
)

const (
	CredentialPrefix = "omj_agent_"

	credentialFileMode os.FileMode = 0o600
	redactedCredential             = CredentialPrefix + "…"
)

// Credential keeps the raw value behind Secret() so that formatting or
// logging it by accident prints only the prefix.
type Credential struct {
	value string
}

func NewCredential(value string) (Credential, error) {
	value = strings.TrimSpace(value)

	if !strings.HasPrefix(value, CredentialPrefix) || len(value) == len(CredentialPrefix) {
		return Credential{}, fmt.Errorf("credential must start with %q followed by the secret", CredentialPrefix)
	}

	return Credential{value: value}, nil
}

func (c Credential) Secret() string { return c.value }

func (c Credential) IsZero() bool { return c.value == "" }

func (c Credential) String() string { return redactedCredential }

func (c Credential) GoString() string { return "config.Credential{" + redactedCredential + "}" }

func (c Credential) LogValue() slog.Value { return slog.StringValue(redactedCredential) }

func LoadCredential(paths Paths) (Credential, error) {
	return loadCredential(paths.CredentialFile, os.Getuid())
}

func loadCredential(path string, currentUID int) (Credential, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Credential{}, fmt.Errorf("read credential: %w", err)
	}

	if mode := info.Mode().Perm(); mode != credentialFileMode {
		return Credential{}, fmt.Errorf("%s has mode %04o; it must be %04o so only the agent can read it", path, mode, credentialFileMode)
	}

	if owner, known := fileOwner(info); known && owner != currentUID {
		return Credential{}, fmt.Errorf("%s is owned by uid %d but the agent runs as uid %d", path, owner, currentUID)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return Credential{}, fmt.Errorf("read credential: %w", err)
	}

	credential, err := NewCredential(string(data))
	if err != nil {
		return Credential{}, fmt.Errorf("%s: %w", path, err)
	}

	return credential, nil
}

func SaveCredential(paths Paths, credential Credential) error {
	if credential.IsZero() {
		return errors.New("save credential: credential is empty")
	}

	if err := os.MkdirAll(filepath.Dir(paths.CredentialFile), configDirMode); err != nil {
		return fmt.Errorf("create configuration directory: %w", err)
	}

	if err := atomicfile.Write(paths.CredentialFile, []byte(credential.Secret()+"\n"), credentialFileMode); err != nil {
		return fmt.Errorf("save credential: %w", err)
	}

	return nil
}
