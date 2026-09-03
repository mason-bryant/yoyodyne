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

func testDocketClosure(entry triage.Entry, decision string) triage.Closure {
	return triage.Closure{
		SchemaVersion: triage.ClosureSchemaVersion,
		Key:           entry.Key,
		ProductID:     entry.ProductID,
		RunID:         entry.RunID,
		WorkItemID:    entry.WorkItemID,
		Decision:      decision,
		Reason:        "the findings dispute the item rather than the change",
		DecidedBy:     "the development manager in conversation chat-0123456789abcdef",
		ClosedAt:      time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC),
	}
}

// A decision settles a stoppage, and the record of it outlives the process that
// made it exactly as the entry does: the next reader finds the entry closed
// rather than finding the same question again.
func TestADecisionThatClosedAnEntryOutlivesTheProcessThatRecordedIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newTestDocketStore(t, root)
	entry := testDocketEntry("run-0123456789abcdef0123456789abcdef", "yoyodyne-one")
	if _, err := store.RecordOnce(entry); err != nil {
		t.Fatalf("RecordOnce() error = %v", err)
	}
	closed, err := store.Close(testDocketClosure(entry, "escalate"))
	if err != nil || !closed {
		t.Fatalf("Close() = %t, error = %v", closed, err)
	}

	entries, err := newTestDocketStore(t, root).List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(entries) != 1 || entries[0].Closed == nil {
		t.Fatalf("List() = %#v, want the entry carrying the decision that closed it", entries)
	}
	if entries[0].Closed.Decision != "escalate" || entries[0].Closed.DecidedBy == "" {
		t.Fatalf("closure = %#v, want the decision and who made it", entries[0].Closed)
	}
}

// The stoppage stays on the log when it is closed, which is what stops the
// scan that re-derives the docket from the same durable records docketing it
// again. An entry that came back would be the same question a second time,
// with the decision recorded nowhere the harness reads.
func TestAClosedEntryIsNotDocketedAgain(t *testing.T) {
	t.Parallel()

	store := newTestDocketStore(t, t.TempDir())
	entry := testDocketEntry("run-0123456789abcdef0123456789abcdef", "yoyodyne-one")
	if _, err := store.RecordOnce(entry); err != nil {
		t.Fatalf("RecordOnce() error = %v", err)
	}
	if _, err := store.Close(testDocketClosure(entry, "wait")); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	created, err := store.RecordOnce(entry)
	if err != nil {
		t.Fatalf("RecordOnce() error = %v", err)
	}
	if created {
		t.Fatalf("a settled stoppage was docketed a second time")
	}
	entries, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(entries) != 1 || entries[0].Closed == nil {
		t.Fatalf("List() = %#v, want one entry, still closed", entries)
	}
}

// One stoppage is settled once. The first decision is the one that took the
// entry off the docket, and asking again changes nothing rather than being an
// error — for the reason docketing twice is not one.
func TestClosingOneEntryTwiceRecordsTheFirstDecision(t *testing.T) {
	t.Parallel()

	store := newTestDocketStore(t, t.TempDir())
	entry := testDocketEntry("run-0123456789abcdef0123456789abcdef", "yoyodyne-one")
	if _, err := store.RecordOnce(entry); err != nil {
		t.Fatalf("RecordOnce() error = %v", err)
	}
	if _, err := store.Close(testDocketClosure(entry, "escalate")); err != nil {
		t.Fatalf("first Close() error = %v", err)
	}
	closed, err := store.Close(testDocketClosure(entry, "rerun"))
	if err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if closed {
		t.Fatalf("the same stoppage was closed twice")
	}
	entries, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if entries[0].Closed == nil || entries[0].Closed.Decision != "escalate" {
		t.Fatalf("closure = %#v, want the decision that closed it first", entries[0].Closed)
	}
}

// A closure names the entry it settles, and one that names nothing is refused:
// it would leave the stoppage on the docket and the decision joined to nobody,
// which is exactly what a mistyped key looks like.
func TestAClosureIsRefusedWhenItNamesNoDocketedStoppage(t *testing.T) {
	t.Parallel()

	store := newTestDocketStore(t, t.TempDir())
	entry := testDocketEntry("run-0123456789abcdef0123456789abcdef", "yoyodyne-one")
	unknown := testDocketClosure(entry, "wait")
	if _, err := store.Close(unknown); err == nil || !strings.Contains(err.Error(), "no docket entry keyed") {
		t.Fatalf("Close() error = %v, want a closure of nothing refused", err)
	}
	if _, err := store.RecordOnce(entry); err != nil {
		t.Fatalf("RecordOnce() error = %v", err)
	}
	elsewhere := testDocketClosure(entry, "wait")
	elsewhere.ProductID = "other-product"
	if _, err := store.Close(elsewhere); err == nil || !strings.Contains(err.Error(), "does not match store product") {
		t.Fatalf("Close() error = %v, want the product mismatch refused", err)
	}
	undecided := testDocketClosure(entry, "")
	if _, err := store.Close(undecided); err == nil {
		t.Fatalf("Close() accepted a closure that says nothing about what was decided")
	}
}

// Nothing bounds what a role writes as its reasoning, and a settled stoppage
// left on the docket because the decision behind it was wordy is the one failure
// closing must not have. So the reasoning is cut and says it was cut, rather
// than the closure being refused.
func TestALongDecisionIsCutRatherThanLeavingTheEntryOpen(t *testing.T) {
	t.Parallel()

	store := newTestDocketStore(t, t.TempDir())
	entry := testDocketEntry("run-0123456789abcdef0123456789abcdef", "yoyodyne-one")
	if _, err := store.RecordOnce(entry); err != nil {
		t.Fatalf("RecordOnce() error = %v", err)
	}
	wordy := testDocketClosure(entry, "escalate")
	wordy.Reason = strings.Repeat("x", triage.MaxMessageBytes+1)
	closed, err := store.Close(wordy)
	if err != nil || !closed {
		t.Fatalf("Close() = %t, error = %v, want the entry closed anyway", closed, err)
	}
	entries, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if entries[0].Closed == nil || !strings.Contains(entries[0].Closed.Reason, "cut") {
		t.Fatalf("closure = %#v, want the reasoning cut and said to be cut", entries[0].Closed)
	}
}

// A closure log that cannot be read must never be read as a docket where
// nothing has been decided: every entry would come back as an open question.
func TestClosuresThatCannotBeReadAreAFailureRatherThanAnOpenDocket(t *testing.T) {
	t.Parallel()

	store := newTestDocketStore(t, t.TempDir())
	entry := testDocketEntry("run-0123456789abcdef0123456789abcdef", "yoyodyne-one")
	if _, err := store.RecordOnce(entry); err != nil {
		t.Fatalf("RecordOnce() error = %v", err)
	}
	if err := os.WriteFile(store.ClosurePath(), []byte("{not json}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := store.List(); err == nil {
		t.Fatalf("List() read a corrupt closure log as a docket nobody had decided")
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
