//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package runstate

import (
	"context"
	"errors"
	"os"
	"syscall"
	"time"
)

// tryLockStateFile takes an exclusive lock without waiting, reporting whether
// it got it. A run lease is held for as long as a process acts on the run, so
// waiting for one would mean queueing behind a developer rather than refusing a
// duplicate.
func tryLockStateFile(file *os.File) (bool, error) {
	err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return false, nil
	}
	return false, err
}

func lockStateFile(ctx context.Context, file *os.File) error {
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

// unlockStateFile drops the lock while the descriptor is still open, so that
// closing the file is no longer the only thing that can release it. An
// interrupted unlock is simply retried: the descriptor is still open and still
// this process's, which is exactly what an interrupted close is not.
func unlockStateFile(file *os.File) error {
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}

// closeStateFile closes a lock file, reporting everything except an interrupted
// close.
func closeStateFile(file *os.File) error {
	return closeLockError(file.Close())
}

// closeLockError says what a caller should be told about closing a lock file.
//
// A close is the one step of a release that gets no second attempt. Go does not
// retry an interrupted close and marks the descriptor closed either way, so
// there is no handle left to try again with; and retrying by hand would close
// whichever descriptor took that number next, which is the bug the retry was
// meant to avoid. On every platform this file builds for the descriptor is
// deallocated even when close reports EINTR, so an interrupted close leaves
// nothing a caller can act on — and because the lock is dropped before the close
// is attempted, it leaves nothing held either. Reporting it would be reporting a
// release that happened.
func closeLockError(err error) error {
	if errors.Is(err, syscall.EINTR) {
		return nil
	}
	return err
}
