package runstate

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/triage"
)

func newTestDocketStore(t *testing.T, root string) *DocketStore {
	t.Helper()
	store, err := NewDocketStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewDocketStore() error = %v", err)
	}
	return store
}

func testDocketEntry(runID, item string) triage.Entry {
	return triage.Entry{
		SchemaVersion: triage.SchemaVersion,
		Key:           triage.Key(triage.ClassStoppedRun, runID),
		Class:         triage.ClassStoppedRun,
		ProductID:     "yoyodyne",
		RunID:         runID,
		WorkItemID:    item,
		RecordedAt:    time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC),
		Blocker:       "Yoyodyne stopped this item: the repair budget was spent.",
		Counters:      triage.Counters{ReviewRounds: 3, ReviewRoundsCap: 4},
	}
}

// A docket entry outlives the process that made it, exactly as a report does:
// the run is settled and its artifacts are removed long before anybody decides
// what becomes of the work.
func TestDocketedWorkSurvivesTheProcessThatDocketedIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newTestDocketStore(t, root)
	first := testDocketEntry("run-0123456789abcdef0123456789abcdef", "yoyodyne-one")
	second := testDocketEntry("run-fedcba9876543210fedcba9876543210", "yoyodyne-two")
	for _, entry := range []triage.Entry{first, second} {
		created, err := store.RecordOnce(entry)
		if err != nil || !created {
			t.Fatalf("RecordOnce() = %t, error = %v", created, err)
		}
	}

	reloaded, err := newTestDocketStore(t, root).List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(reloaded) != 2 || reloaded[0].Key != first.Key || reloaded[1].Key != second.Key {
		t.Fatalf("List() = %#v, want both entries in the order they were docketed", reloaded)
	}
	if reloaded[0].Blocker != first.Blocker || reloaded[0].Counters != first.Counters {
		t.Fatalf("entry did not survive intact: %#v", reloaded[0])
	}
}

// One stoppage is one entry. The run that stopped and the sweep that settles it
// afterwards both docket it, and asking twice about one stoppage means the same
// thing the second time rather than being an error.
func TestDocketingOneStoppageTwiceRecordsItOnce(t *testing.T) {
	t.Parallel()

	store := newTestDocketStore(t, t.TempDir())
	entry := testDocketEntry("run-0123456789abcdef0123456789abcdef", "yoyodyne-one")
	created, err := store.RecordOnce(entry)
	if err != nil || !created {
		t.Fatalf("first RecordOnce() = %t, error = %v", created, err)
	}
	// The second attempt describes the same event with different words and a
	// later moment, which is what a reconciling sweep would produce.
	again := entry
	again.Blocker = "Yoyodyne stopped this item while reconciling an interrupted run."
	again.RecordedAt = entry.RecordedAt.Add(time.Hour)
	created, err = store.RecordOnce(again)
	if err != nil {
		t.Fatalf("second RecordOnce() error = %v", err)
	}
	if created {
		t.Fatalf("the same stoppage was docketed twice")
	}
	entries, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(entries) != 1 || entries[0].Blocker != entry.Blocker {
		t.Fatalf("List() = %#v, want the first account of the one stoppage", entries)
	}
}

// Two processes can both find an absent key and both append. Reading collapses
// them, so a race costs a reader nothing rather than a repeated paragraph.
func TestADocketedEntryWrittenTwiceByARaceIsReadOnce(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newTestDocketStore(t, root)
	entry := testDocketEntry("run-0123456789abcdef0123456789abcdef", "yoyodyne-one")
	if _, err := store.RecordOnce(entry); err != nil {
		t.Fatalf("RecordOnce() error = %v", err)
	}
	raced, err := encodeDocketEntry(entry)
	if err != nil {
		t.Fatalf("encodeDocketEntry() error = %v", err)
	}
	file, err := os.OpenFile(store.Path(), os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("open docket error = %v", err)
	}
	if _, err := file.Write(raced); err != nil {
		t.Fatalf("write raced entry error = %v", err)
	}
	file.Close()

	entries, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("List() = %#v, want one entry for one stoppage", entries)
	}
}

func TestReadingADocketBeforeAnythingStoppedIsNotAFailure(t *testing.T) {
	t.Parallel()

	entries, err := newTestDocketStore(t, t.TempDir()).List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("List() = %#v, want nothing", entries)
	}
}

func TestDocketEntriesAreRefusedWhenTheyDoNotBelongHereOrCannotBeReadBack(t *testing.T) {
	t.Parallel()

	store := newTestDocketStore(t, t.TempDir())
	elsewhere := testDocketEntry("run-0123456789abcdef0123456789abcdef", "yoyodyne-one")
	elsewhere.ProductID = "other-product"
	if _, err := store.RecordOnce(elsewhere); err == nil || !strings.Contains(err.Error(), "does not match store product") {
		t.Fatalf("RecordOnce() error = %v, want the product mismatch refused", err)
	}
	malformed := testDocketEntry("run-0123456789abcdef0123456789abcdef", "yoyodyne-one")
	malformed.Blocker = ""
	if _, err := store.RecordOnce(malformed); err == nil {
		t.Fatalf("RecordOnce() accepted an entry that cannot say what stopped")
	}
	if entries, err := store.List(); err != nil || len(entries) != 0 {
		t.Fatalf("List() = %#v, error = %v, want an untouched docket", entries, err)
	}
}

func TestADocketThatCannotBeReadIsAFailureRatherThanAnEmptyDocket(t *testing.T) {
	t.Parallel()

	store := newTestDocketStore(t, t.TempDir())
	if err := os.MkdirAll(store.Root(), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(store.Path(), []byte("{not json}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := store.List(); err == nil {
		t.Fatalf("List() read a corrupt docket as an empty one")
	}
}
