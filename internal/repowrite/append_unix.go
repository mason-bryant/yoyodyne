//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package repowrite

import (
	"os"
	"syscall"
)

// appendFlags open a file for appending and refuse a symlink standing where it
// goes. O_NOFOLLOW applies to the final component only, which is exactly the
// gap left over once every component above it has been resolved: the open fails
// with a link there rather than writing through it.
const appendFlags = os.O_CREATE | os.O_WRONLY | os.O_APPEND | syscall.O_NOFOLLOW
