//go:build !(darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd)

package gitworktree

import (
	"context"
	"errors"
	"os"
)

// A platform without the advisory lock cannot serialize creation across
// processes, and creating worktrees anyway would trade a refusal for a run
// silently lost to a half-written registration. It is refused for the same
// reason run state refuses to reserve a run there: the harness is only as safe
// as the locking underneath it.
func lockCreationFile(context.Context, *os.File) error {
	return errors.New("cross-process worktree creation locking is unsupported on this platform")
}
