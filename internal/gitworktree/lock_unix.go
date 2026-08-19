//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package gitworktree

import (
	"context"
	"errors"
	"os"
	"syscall"
	"time"
)

// lockCreationFile takes an exclusive lock, waiting until it gets one or until
// the context bounds the wait. The lock belongs to the open file description
// rather than to the process, so two creations in one process queue behind one
// another exactly as two processes do.
func lockCreationFile(ctx context.Context, file *os.File) error {
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
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
