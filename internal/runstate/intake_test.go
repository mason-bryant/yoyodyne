package runstate

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// The hold is placed by the operator's process and read by every process that
// would start work, so it has to survive the gap between them, and it must be
// readable without any lease: the processes reading it are the ones being
// stopped from choosing.
func TestHoldingIntakeSurvivesTheProcessThatPlacedIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newIntakeStoreAt(t, root, "yoyodyne")
	if _, held, err := store.Held(); err != nil || held {
		t.Fatalf("Held() = %t, %v, want nothing holding a fresh state root", held, err)
	}

	heldAt := time.Date(2026, 8, 18, 18, 15, 0, 0, time.UTC)
	recorded, err := store.Hold("the decomposition is heading somewhere odd", heldAt)
	if err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	if !recorded.HeldAt.Equal(heldAt) || recorded.Reason != "the decomposition is heading somewhere odd" {
		t.Fatalf("Hold() = %#v, want the moment it was placed and why", recorded)
	}

	// A second hold says the same thing as the first. Restamping it would make a
	// hold that has been in force since yesterday describe itself as new, and
	// rewriting its reason would rewrite the account of why nothing has started.
	again, err := store.Hold("something else entirely", heldAt.Add(time.Hour))
	if err != nil {
		t.Fatalf("second Hold() error = %v", err)
	}
	if !again.HeldAt.Equal(heldAt) || again.Reason != "the decomposition is heading somewhere odd" {
		t.Fatalf("second Hold() = %#v, want the hold left exactly as it was placed", again)
	}

	// A separate store over the same root is what every other process is.
	loaded, held, err := newIntakeStoreAt(t, root, "yoyodyne").Held()
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
		t.Fatalf("Held() after release = %t, %v, want the harness choosing work again", held, err)
	}
	// Releasing what is not held is what an operator does when they are not sure,
	// and it means the harness should be picking work up, which it is.
	if _, wasHeld, err := store.Release(); err != nil || wasHeld {
		t.Fatalf("second Release() = %t, %v, want a no-op over intake that is running", wasHeld, err)
	}
}

// The hold is about one backlog, so holding what one product's harness may pull
// must leave another's alone. This is the whole reason it is not the switch at
// the state root.
func TestHoldingIntakeOnOneProductLeavesAnotherAlone(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	held := newIntakeStoreAt(t, root, "yoyodyne")
	other := newIntakeStoreAt(t, root, "something-else")
	if _, err := held.Hold("", time.Date(2026, 8, 18, 18, 15, 0, 0, time.UTC)); err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	if _, isHeld, err := other.Held(); err != nil || isHeld {
		t.Fatalf("Held() on the other product = %t, %v, want it unaffected", isHeld, err)
	}
}

// A hold nobody can read must never be reported as a harness that may choose
// work: the quiet an operator is looking at would then be unexplained, which is
// exactly what the hold exists to explain.
func TestAnUnreadableIntakeHoldIsNotReportedAsAbsent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newIntakeStoreAt(t, root, "yoyodyne")
	directory := filepath.Join(root, "products", "yoyodyne")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	for name, content := range map[string]string{
		"not JSON at all":          "held\n",
		"a hold with no moment":    `{"schema_version":1,"product_id":"yoyodyne"}`,
		"an unsupported schema":    `{"schema_version":2,"product_id":"yoyodyne","held_at":"2026-08-18T18:15:00Z"}`,
		"a key nothing writes":     `{"schema_version":1,"product_id":"yoyodyne","held_at":"2026-08-18T18:15:00Z","until":"never"}`,
		"another product's record": `{"schema_version":1,"product_id":"elsewhere","held_at":"2026-08-18T18:15:00Z"}`,
	} {
		if err := os.WriteFile(filepath.Join(directory, "intake-hold.json"), []byte(content), 0o600); err != nil {
			t.Fatalf("%s: WriteFile() error = %v", name, err)
		}
		if _, held, err := store.Held(); err == nil || held {
			t.Fatalf("%s: Held() = %t, %v, want a refusal to read it as absent", name, held, err)
		}
	}
}

func TestTheIntakeHoldIsRefusedARelativeStateRoot(t *testing.T) {
	t.Parallel()

	if _, err := NewIntakeHoldStore("state", "yoyodyne"); err == nil {
		t.Fatal("NewIntakeHoldStore() accepted a relative state root")
	}
}

func newIntakeStoreAt(t *testing.T, root string, productID domain.ProductID) *IntakeHoldStore {
	t.Helper()
	store, err := NewIntakeHoldStore(root, productID)
	if err != nil {
		t.Fatalf("NewIntakeHoldStore() error = %v", err)
	}
	return store
}
