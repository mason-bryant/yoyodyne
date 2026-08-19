package runstate

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The hold is placed by the operator's process and read by every process that
// would spend, so it has to survive the gap between them exactly as run state
// does — and it must be readable by all of them at once, without any lease,
// because the processes reading it are the ones the operator is pausing.
func TestTheHoldSurvivesTheProcessThatPlacedIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newHoldStoreAt(t, root)
	if _, held, err := store.Held(); err != nil || held {
		t.Fatalf("Held() = %t, %v, want nothing holding a fresh state root", held, err)
	}

	heldAt := time.Date(2026, 8, 18, 18, 15, 0, 0, time.UTC)
	recorded, err := store.Hold(heldAt)
	if err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	if !recorded.HeldAt.Equal(heldAt) {
		t.Fatalf("Hold() = %#v, want the moment it was placed", recorded)
	}

	// A second pause says the same thing as the first, and must not restamp it:
	// how long the harness has been quiet is what the record is for.
	again, err := store.Hold(heldAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("second Hold() error = %v", err)
	}
	if !again.HeldAt.Equal(heldAt) {
		t.Fatalf("second Hold() = %#v, want the hold left as it was placed", again)
	}

	// A separate store over the same root is what every other process is.
	loaded, held, err := newHoldStoreAt(t, root).Held()
	if err != nil || !held {
		t.Fatalf("Held() = %t, %v, want the recorded hold", held, err)
	}
	if !loaded.HeldAt.Equal(heldAt) {
		t.Fatalf("Held() = %#v, want the hold as it was placed", loaded)
	}

	lifted, wasHeld, err := store.Release()
	if err != nil || !wasHeld {
		t.Fatalf("Release() = %t, %v, want the hold lifted", wasHeld, err)
	}
	if !lifted.HeldAt.Equal(heldAt) {
		t.Fatalf("Release() = %#v, want it to report what was lifted", lifted)
	}
	if _, held, err := store.Held(); err != nil || held {
		t.Fatalf("Held() after release = %t, %v, want nothing holding activity", held, err)
	}
	// Resuming what is not held is what an operator does when they are not sure,
	// and it means the harness should be running, which it is.
	if _, wasHeld, err := store.Release(); err != nil || wasHeld {
		t.Fatalf("second Release() = %t, %v, want a no-op over a harness that is running", wasHeld, err)
	}
}

// A hold nobody can read must never be reported as a harness that may spend.
// Reading it is the last thing between an operator's decision and their money,
// so a record that is not one is an error rather than an absence.
func TestAnUnreadableHoldIsNotReportedAsAbsent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newHoldStoreAt(t, root)
	for name, content := range map[string]string{
		"not JSON at all":       "paused\n",
		"a hold with no moment": `{"schema_version":1}`,
		"an unsupported schema": `{"schema_version":2,"held_at":"2026-08-18T18:15:00Z"}`,
		"a key nothing writes":  `{"schema_version":1,"held_at":"2026-08-18T18:15:00Z","until":"never"}`,
	} {
		if err := os.WriteFile(filepath.Join(root, "operator-hold.json"), []byte(content), 0o600); err != nil {
			t.Fatalf("%s: WriteFile() error = %v", name, err)
		}
		if _, held, err := store.Held(); err == nil || held {
			t.Fatalf("%s: Held() = %t, %v, want a refusal to read it as absent", name, held, err)
		}
	}
}

// The store is one file at the state root rather than one per product, because
// what makes an operator pause — an account, a bill, an afternoon away — is
// theirs rather than any product's.
func TestTheHoldIsRefusedARelativeStateRoot(t *testing.T) {
	t.Parallel()

	if _, err := NewOperatorHoldStore("state"); err == nil {
		t.Fatal("NewOperatorHoldStore() accepted a relative state root")
	}
}

func newHoldStoreAt(t *testing.T, root string) *OperatorHoldStore {
	t.Helper()
	store, err := NewOperatorHoldStore(root)
	if err != nil {
		t.Fatalf("NewOperatorHoldStore() error = %v", err)
	}
	return store
}
