//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package gitworktree

import (
	"context"
	"errors"
	"os"
	"syscall"
	"time"
)

// registryLockSupported says this platform has the advisory lock the registry
// lease is built on.
const registryLockSupported = true

// lockRegistryFile takes an exclusive lock, waiting until it gets one or until
// the context bounds the wait. The lock belongs to the open file description
// rather than to the process, so two registry writes in one process queue behind
// one another exactly as two processes do.
func lockRegistryFile(ctx context.Context, file *os.File) error {
	return lockRegistryFileHow(ctx, file, syscall.LOCK_EX)
}

// lockRegistryFileShared takes a shared lock, which every other shared holder
// may hold at the same time and no writer may hold beside. It is what a command
// that only reads the bookkeeping takes, so a reader and a writer queue while
// readers do not queue behind each other.
func lockRegistryFileShared(ctx context.Context, file *os.File) error {
	return lockRegistryFileHow(ctx, file, syscall.LOCK_SH)
}

func lockRegistryFileHow(ctx context.Context, file *os.File, how int) error {
	for {
		err := syscall.Flock(int(file.Fd()), how|syscall.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}
