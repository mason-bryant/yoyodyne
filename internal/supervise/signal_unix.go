//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package supervise

import (
	"errors"
	"fmt"
	"syscall"
)

// terminate sends the stop signal every harness process answers: the one that
// cancels its work and, past its grace, ends it. A process that is already
// gone is reported so rather than signalled, because a stop of what has
// already stopped is not a failure.
func terminate(pid int) error {
	err := syscall.Kill(pid, syscall.SIGTERM)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, syscall.ESRCH):
		return fmt.Errorf("pid %d is not running", pid)
	default:
		return err
	}
}
