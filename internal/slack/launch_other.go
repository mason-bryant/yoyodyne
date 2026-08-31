//go:build !(aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris)

package slack

import "os/exec"

// Detaching into a session of its own is implemented for Yoyodyne's supported
// Unix hosts. Elsewhere the sink is an ordinary child of whatever started it.
func detachProcess(_ *exec.Cmd) {}
