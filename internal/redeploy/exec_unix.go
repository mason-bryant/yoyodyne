//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package redeploy

import "syscall"

// imageReplacement reports that a process here can replace its own image, which
// is what a redeploy is. It is answered at the start rather than at the end
// because a session that discovered it could not restart only after it had
// stopped choosing work and drained itself would have stopped for nothing.
const imageReplacement = true

// replaceImage hands this process to the operating system to be overwritten by
// the binary at path. Nothing after a successful call runs: there is no process
// left to run it.
func replaceImage(path string, args, env []string) error {
	return syscall.Exec(path, args, env)
}
