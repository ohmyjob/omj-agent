//go:build unix

package runner

import (
	"errors"
	"fmt"
	"syscall"
	"time"
)

func (p *Process) terminateGroup() {
	p.signalGroup(syscall.SIGTERM)

	grace := time.NewTimer(p.grace)
	defer grace.Stop()

	select {
	case <-p.done:
		return
	case <-grace.C:
	}

	p.signalGroup(syscall.SIGKILL)

	<-p.done
}

func (p *Process) signalGroup(signal syscall.Signal) {
	err := syscall.Kill(-p.pgid, signal)
	if err == nil || errors.Is(err, syscall.ESRCH) {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.terminateErr == nil {
		p.terminateErr = fmt.Errorf("send %s to process group %d: %w", signal, p.pgid, err)
	}
}

func groupAlive(pgid int) bool {
	return syscall.Kill(-pgid, 0) == nil
}
