//go:build !(aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris)

package execution

import "os/exec"

// Process-group cancellation is implemented for Yoyodyne's supported Unix
// hosts. Other platforms retain os/exec's immediate-process cancellation.
func configureProcessTree(_ *exec.Cmd) {}

// Reaping what a command left running is process-group work, so there is
// nothing to do where no group was made. A descendant on such a host outlives
// the command that spawned it, bounded only by whatever timeout that descendant
// carries.
func reapProcessTree(_ *exec.Cmd) {}
