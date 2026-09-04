//go:build unix

package agent

import (
	"os"
	"syscall"
)

// StopSignals are the signals that ask the Agent to stop: SIGTERM from
// systemd and SIGINT from a terminal.
func StopSignals() []os.Signal {
	return []os.Signal{syscall.SIGTERM, syscall.SIGINT}
}
