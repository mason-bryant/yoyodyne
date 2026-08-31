//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package slack

import (
	"os/exec"
	"syscall"
)

// detachProcess makes the sink the leader of a session of its own, so it has no
// controlling terminal and is not in the process group of whatever started it.
// A signal sent to the pass's group — which is what closing a terminal or
// unloading a launchd job sends — then stops the pass and leaves reporting up.
func detachProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
