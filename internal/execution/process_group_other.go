//go:build !(aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris)

package execution

import "os/exec"

// Process-group cancellation is implemented for Yoyodyne's supported Unix
// hosts. Other platforms retain os/exec's immediate-process cancellation.
func configureProcessTree(_ *exec.Cmd) {}
