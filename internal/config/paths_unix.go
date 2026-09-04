//go:build unix

package config

import (
	"os"
	"path/filepath"
	"syscall"
)

const (
	defaultConfigDir = "/etc/ohmyjob"
	defaultStateDir  = "/var/lib/ohmyjob"
)

func DefaultPaths() Paths {
	configDir := defaultConfigDir
	if dir := os.Getenv("OMJ_CONFIG_DIR"); dir != "" {
		configDir = dir
	}

	stateDir := defaultStateDir
	if dir := os.Getenv("OMJ_STATE_DIR"); dir != "" {
		stateDir = dir
	}

	return Paths{
		ConfigDir:      configDir,
		ConfigFile:     filepath.Join(configDir, "agent.conf"),
		CredentialFile: filepath.Join(configDir, "agent.credential"),
		StateDir:       stateDir,
		StateFile:      filepath.Join(stateDir, "state.json"),
	}
}

func fileOwner(info os.FileInfo) (uid int, known bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}

	return int(stat.Uid), true
}
