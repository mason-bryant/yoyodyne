//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package runstate

import (
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// A lease is unlocked before it is closed, so the close is no longer the only
// thing that can release it. What proves the lock is gone is another holder
// taking it while the first descriptor is deliberately still open.
func TestUnlockingALeaseFileFreesItWithoutClosingIt(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "conversation.lock")
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	defer file.Close()
	if held, err := tryLockStateFile(file); err != nil || !held {
		t.Fatalf("tryLockStateFile() = %t, %v, want the lock taken", held, err)
	}
	if err := unlockStateFile(file); err != nil {
		t.Fatalf("unlockStateFile() error = %v", err)
	}

	next, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("OpenFile() for the next holder error = %v", err)
	}
	defer next.Close()
	if held, err := tryLockStateFile(next); err != nil || !held {
		t.Fatalf("tryLockStateFile() after unlock = %t, %v, want the lock free before the close", held, err)
	}
}

// An interrupted close is not a failure to release: the lock is already dropped,
// the descriptor is deallocated, and nothing is left to retry — while a close
// that failed for any other reason is still the caller's to hear about.
func TestAnInterruptedCloseIsNotAFailureToRelease(t *testing.T) {
	t.Parallel()

	// os.File.Close wraps the errno the way callers actually receive it, so the
	// tolerance has to see through the wrapper rather than only the bare errno.
	if err := closeLockError(&fs.PathError{Op: "close", Path: "conversation.lock", Err: syscall.EINTR}); err != nil {
		t.Fatalf("closeLockError(EINTR) = %v, want an interrupted close tolerated", err)
	}
	if err := closeLockError(syscall.EINTR); err != nil {
		t.Fatalf("closeLockError(bare EINTR) = %v, want an interrupted close tolerated", err)
	}
	if err := closeLockError(nil); err != nil {
		t.Fatalf("closeLockError(nil) = %v, want a close that worked reported as such", err)
	}
	if err := closeLockError(&fs.PathError{Op: "close", Path: "conversation.lock", Err: syscall.EIO}); err == nil {
		t.Fatal("closeLockError(EIO) = nil, want a close that failed reported")
	}
}
