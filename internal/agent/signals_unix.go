//go:build unix

package agent

import (
	"os"
	"syscall"
)

func StopSignals() []os.Signal {
	return []os.Signal{syscall.SIGTERM, syscall.SIGINT}
}
