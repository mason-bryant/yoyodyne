//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd

package runstate

import (
	"context"
	"errors"
	"os"
	"syscall"
	"testing"
)

// A give-back honours the context it is given, and this is what says so out
// loud: whoever gives an attempt back because a cancellation killed the turn
// cannot hand the cancelled context along and expect the record to move.
//
// The refusal needs another holder of the record to be certain, which is exactly
// why the caller cannot rely on the case where it is absent. An uncontended lock
// is taken without the context being consulted at all, so the same give-back
// under the same cancelled context succeeds or fails depending on what else
// wanted the record at that moment — and a give-back that works most of the time
// is one nothing notices spending an attempt the rest of it.
func TestAGiveBackUnderACancelledContextIsRefusedWhileTheRecordIsHeld(t *testing.T) {
	t.Parallel()

	store := newEscalationStore(t)
	if _, err := store.Attempt(context.Background(), attemptedEscalation()); err != nil {
		t.Fatalf("Attempt() error = %v", err)
	}

	held, err := os.OpenFile(store.path(escalationDocketKey)+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatalf("open the escalation lock: %v", err)
	}
	defer held.Close()
	if err := syscall.Flock(int(held.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("hold the escalation lock: %v", err)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	err = store.Withdraw(cancelled, escalationDocketKey, "the turn was cancelled before she answered")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Withdraw() error = %v, want the cancelled context refused rather than waited out", err)
	}

	// And the attempt it failed to give back is still spent, which is the harm:
	// nothing else gives it back, and the record now counts a turn that decided
	// nothing towards the bound past which the stoppage needs a person.
	recorded, found, err := store.Find(escalationDocketKey)
	if err != nil || !found {
		t.Fatalf("Find() = found %v, error %v", found, err)
	}
	if recorded.Attempts != 1 {
		t.Fatalf("attempts = %d, want the refused give-back to have left the attempt spent", recorded.Attempts)
	}
}
