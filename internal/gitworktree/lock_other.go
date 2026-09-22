//go:build !(darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd)

package gitworktree

import (
	"context"
	"errors"
	"os"
)

// registryLockSupported says this platform has no advisory lock to build the
// registry lease on. A write refuses; a read carries on without queueing, on the
// tolerance that covered it before the lease existed — see leaseRegistryShared.
const registryLockSupported = false

// A platform without the advisory lock cannot serialize worktree registry
// writes across processes, and creating or removing worktrees anyway would trade
// a refusal for a run silently lost to a half-written registration. It is
// refused for the same reason run state refuses to reserve a run there: the
// harness is only as safe as the locking underneath it.
func lockRegistryFile(context.Context, *os.File) error {
	return errors.New("cross-process worktree registry locking is unsupported on this platform")
}

// lockRegistryFileShared is never reached: leaseRegistryShared asks for no lease
// at all where the lock is unsupported, because a reader refused here would stop
// every registration-walking command rather than the writes this platform cannot
// make safe.
func lockRegistryFileShared(context.Context, *os.File) error {
	return errors.New("cross-process worktree registry locking is unsupported on this platform")
}
