//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package execution

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// configureProcessTree makes the command the leader of a new process group and
// replaces CommandContext's single-process cancellation with a group kill.
// Shell checks frequently spawn descendants that inherit stdout and stderr; if
// only the shell dies, those descendants can keep the pipes open indefinitely.
func configureProcessTree(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		if err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL); err != nil {
			if errors.Is(err, syscall.ESRCH) {
				return os.ErrProcessDone
			}
			return err
		}
		return nil
	}
}
