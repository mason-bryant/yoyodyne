//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package runstate

import (
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
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

// Every lock in this package is let go of through releaseStateFile, and this is
// why: a descriptor somebody else still shares keeps the lock alive past a
// close, so a release that only closed would hand the next acquirer a refusal
// nobody could act on. A duplicate is that sharing without the fork that
// ordinarily produces it.
func TestReleasingAStateFileDropsTheLockADuplicateStillShares(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "reservation.lock")
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	if held, err := tryLockStateFile(file); err != nil || !held {
		t.Fatalf("tryLockStateFile() = %t, %v, want the lock taken", held, err)
	}
	shared, err := syscall.Dup(int(file.Fd()))
	if err != nil {
		t.Fatalf("Dup() of the locked file error = %v", err)
	}
	defer syscall.Close(shared)
	if err := releaseStateFile(file); err != nil {
		t.Fatalf("releaseStateFile() error = %v", err)
	}

	next, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("OpenFile() for the next holder error = %v", err)
	}
	defer next.Close()
	if held, err := tryLockStateFile(next); err != nil || !held {
		t.Fatalf("tryLockStateFile() with the released description still shared = %t, %v, want the lock free", held, err)
	}
}

// The lock belongs to the open file description rather than to the descriptor,
// so a process that forked while a conversation was held shares the holder's
// lock until that child execs — and the harness forks Git and check processes
// constantly. A release that let the close drop the lock would leave it with
// the child for that window, and the very next hold would be refused a
// conversation nobody holds. Release drops it itself, so the reacquire cannot
// be refused however many descriptors still share the description; a duplicate
// stands in for the forked child's, being the same sharing without the fork.
func TestReleasingAConversationDropsTheLockADuplicateStillShares(t *testing.T) {
	t.Parallel()

	store := newConversationStore(t, t.TempDir())
	identity := ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager}
	held, err := store.Hold(identity)
	if err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	shared, err := syscall.Dup(int(held.file.Fd()))
	if err != nil {
		t.Fatalf("Dup() of the held lease error = %v", err)
	}
	defer syscall.Close(shared)
	if err := held.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}

	regained, err := store.Hold(identity)
	if err != nil {
		t.Fatalf("Hold() with the released description still shared error = %v", err)
	}
	if err := regained.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
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
