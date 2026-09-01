//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package redeploy

import "syscall"

// imageReplacement reports that a process here can replace its own image, which
// is what a redeploy is. It is answered when a session opens rather than at the
// end, so a session that could not restart never drains itself for a restart it
// cannot make.
const imageReplacement = true

// replaceImage hands this process to the operating system to be overwritten by
// the binary at path. Nothing after a successful call runs: there is no process
// left to run it.
func replaceImage(path string, args, env []string) error {
	return syscall.Exec(path, args, env)
}
