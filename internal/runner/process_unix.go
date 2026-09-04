//go:build unix

package runner

import (
	"os"
	"os/exec"
	"syscall"
)

// A process group of its own lets timeout and cancellation terminate every
// descendant of the shell, not only the shell.
func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func processGroupID(pid int) int {
	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		return pid
	}

	return pgid
}

func exitStatus(state *os.ProcessState) (int, os.Signal) {
	if state == nil {
		return -1, nil
	}

	if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return 128 + int(status.Signal()), status.Signal()
	}

	return state.ExitCode(), nil
}
