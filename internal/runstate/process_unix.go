//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package runstate

import (
	"errors"
	"fmt"
	"syscall"
)

// processIsRunning reports whether the process a durable stamp names is still
// there. It is how a holder somebody wrote down is checked without taking
// anything from it: a lease that is observed rather than acquired needs the
// stamp and the operating system to agree, and the operating system's answer is
// which processes exist.
//
// The null signal is delivered to nothing; the kernel does the existence check
// and reports it. A process this one may not signal exists all the same, which
// is the one case where a permission failure is an answer rather than a
// problem.
func processIsRunning(pid int) (bool, error) {
	if pid <= 0 {
		return false, fmt.Errorf("%d is not a process identifier", pid)
	}
	err := syscall.Kill(pid, 0)
	switch {
	case err == nil, errors.Is(err, syscall.EPERM):
		return true, nil
	case errors.Is(err, syscall.ESRCH):
		return false, nil
	default:
		return false, err
	}
}
