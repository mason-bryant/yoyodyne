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

// A resumable stall past its developer attempt says which step the repair
// continues it at, and the renderers print that step verbatim, so the docket
// accepts only the checks and the review there, spelled as the phases are.
func TestADocketedStallIsContinuedOnlyAtTheChecksOrTheReview(t *testing.T) {
	t.Parallel()

	stall := func(step string) triage.Entry {
		entry := testDocketEntry("run-0123456789abcdef0123456789abcdef", "yoyodyne-one")
		entry.SessionResumable = true
		entry.ResumesAt = step
		entry.Artifacts.DeveloperSession = "developer-session"
		entry.Artifacts.WorktreePath = "/worktrees/yoyodyne-one"
		entry.Artifacts.Branch = "yoyodyne/yoyodyne-one/01234567"
		return entry
	}
	for _, phase := range []Phase{PhaseChecking, PhaseReviewing} {
		if got, ok := StallResumeStep(string(phase)); !ok || got != phase {
			t.Fatalf("StallResumeStep(%q) = %q, %t, want the %s phase", phase, got, ok, phase)
		}
		store := newTestDocketStore(t, t.TempDir())
		if _, err := store.RecordOnce(stall(string(phase))); err != nil {
			t.Fatalf("RecordOnce() error = %v for a stall resumed at %s", err, phase)
		}
		if entries, err := store.List(); err != nil || len(entries) != 1 || entries[0].ResumesAt != string(phase) {
			t.Fatalf("List() = %#v, error = %v, want the step read back", entries, err)
		}
	}
	for _, step := range []string{string(PhaseDeveloping), string(PhaseIntegrating), "reveiwing"} {
		if _, ok := StallResumeStep(step); ok {
			t.Fatalf("StallResumeStep(%q) accepted a step a stall is not continued at", step)
		}
		store := newTestDocketStore(t, t.TempDir())
		if _, err := store.RecordOnce(stall(step)); err == nil || !strings.Contains(err.Error(), "resumes_at: \""+step+"\" is not a step") {
			t.Fatalf("RecordOnce() error = %v, want a stall resumed at %q refused", err, step)
		}
	}
	if _, err := decodeDocketEntry([]byte(`{"resumes_at":"developing"}`)); err == nil {
		t.Fatal("decodeDocketEntry() accepted an entry naming a step a stall is not continued at")
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

// A key names a run rather than one moment of it. A repair continues the run
// that stopped, so a run that dies again after being repaired derives exactly
// the key its settled entry carries — and refusing that would leave a live
// blocker on live work with nothing anywhere that mentions it.
func TestAStoppageAfterTheDecisionThatSettledTheLastOneIsDocketedAgain(t *testing.T) {
	t.Parallel()

	store := newTestDocketStore(t, t.TempDir())
	entry := testDocketEntry("run-0123456789abcdef0123456789abcdef", "yoyodyne-one")
	if _, err := store.RecordOnce(entry); err != nil {
		t.Fatalf("RecordOnce() error = %v", err)
	}
	closure := testDocketClosure(entry, "repair")
	if _, err := store.Close(closure); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	// The repaired run dies again, with a blocker of its own.
	again := entry
	again.RecordedAt = closure.ClosedAt.Add(time.Hour)
	again.Blocker = "Yoyodyne stopped this item: the push was refused by the remote."
	created, err := store.RecordOnce(again)
	if err != nil || !created {
		t.Fatalf("RecordOnce() = %t, error = %v, want the new stoppage docketed", created, err)
	}
	entries, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("List() = %#v, want one entry for the one key", entries)
	}
	if entries[0].Closed != nil || entries[0].Blocker != again.Blocker {
		t.Fatalf("entry = %#v, want the new stoppage, undecided", entries[0])
	}
	// And the settled stoppage arriving again is still that same settled one: a
	// scan re-deriving it from the run's record must not put it back.
	stale := entry
	stale.RecordedAt = closure.ClosedAt.Add(-time.Minute)
	created, err = store.RecordOnce(stale)
	if err != nil || created {
		t.Fatalf("RecordOnce() = %t, error = %v, want the settled stoppage left alone", created, err)
	}
}

// One key can go round the whole lifecycle more than once: stopped, decided,
// stopped again, decided again. What a reader is shown is the stoppage it stands
// at now and the decision made about that one — the first stoppage and the
// decision that settled it are history, and reading either back would put a
// settled question in front of somebody or hide a live one.
func TestAKeyStandsAtItsLatestStoppageAndTheDecisionMadeAboutIt(t *testing.T) {
	t.Parallel()

	store := newTestDocketStore(t, t.TempDir())
	first := testDocketEntry("run-0123456789abcdef0123456789abcdef", "yoyodyne-one")
	if _, err := store.RecordOnce(first); err != nil {
		t.Fatalf("RecordOnce() error = %v", err)
	}
	repaired := testDocketClosure(first, "repair")
	if _, err := store.Close(repaired); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	second := first
	second.RecordedAt = repaired.ClosedAt.Add(time.Hour)
	second.Blocker = "Yoyodyne stopped this item: the push was refused by the remote."
	if created, err := store.RecordOnce(second); err != nil || !created {
		t.Fatalf("RecordOnce() = %t, error = %v", created, err)
	}
	escalated := testDocketClosure(second, "escalate")
	escalated.ClosedAt = second.RecordedAt.Add(time.Hour)
	if closed, err := store.Close(escalated); err != nil || !closed {
		t.Fatalf("Close() = %t, error = %v, want the second stoppage settled", closed, err)
	}

	entries, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("List() = %#v, want the one key", entries)
	}
	if entries[0].Blocker != second.Blocker {
		t.Fatalf("entry = %#v, want the stoppage the key stands at", entries[0])
	}
	if entries[0].Closed == nil || entries[0].Closed.Decision != "escalate" {
		t.Fatalf("closure = %#v, want the decision made about that stoppage", entries[0].Closed)
	}
}

// The decision that means "not yet" holds until the moment it names, and the
// entry is a question again after it — so the next decision about it is recorded
// rather than refused as a repeat.
func TestADecisionThatLapsedCanBeMadeAgain(t *testing.T) {
	t.Parallel()

	store := newTestDocketStore(t, t.TempDir())
	entry := testDocketEntry("run-0123456789abcdef0123456789abcdef", "yoyodyne-one")
	if _, err := store.RecordOnce(entry); err != nil {
		t.Fatalf("RecordOnce() error = %v", err)
	}
	waited := testDocketClosure(entry, "wait")
	waited.RevisitAfter = waited.ClosedAt.Add(2 * time.Hour)
	if _, err := store.Close(waited); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	// While it holds, the stoppage is settled and a second decision is the same
	// decision again.
	repeated := waited
	repeated.ClosedAt = waited.ClosedAt.Add(time.Hour)
	repeated.RevisitAfter = repeated.ClosedAt.Add(2 * time.Hour)
	if closed, err := store.Close(repeated); err != nil || closed {
		t.Fatalf("Close() = %t, error = %v, want the standing decision kept", closed, err)
	}
	// Once it has lapsed, the entry is a question again and the answer to it is a
	// decision of its own.
	next := testDocketClosure(entry, "escalate")
	next.ClosedAt = waited.RevisitAfter.Add(time.Minute)
	closed, err := store.Close(next)
	if err != nil || !closed {
		t.Fatalf("Close() = %t, error = %v, want the lapsed decision replaced", closed, err)
	}
	entries, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if entries[0].Closed == nil || entries[0].Closed.Decision != "escalate" {
		t.Fatalf("closure = %#v, want the decision that stands now", entries[0].Closed)
	}
}

// A decision made before the stoppage it claims to settle was docketed is one
// nothing would ever join, so it is refused rather than written where no reader
// would find it.
func TestADecisionOlderThanTheStoppageItNamesIsRefused(t *testing.T) {
	t.Parallel()

	store := newTestDocketStore(t, t.TempDir())
	entry := testDocketEntry("run-0123456789abcdef0123456789abcdef", "yoyodyne-one")
	if _, err := store.RecordOnce(entry); err != nil {
		t.Fatalf("RecordOnce() error = %v", err)
	}
	early := testDocketClosure(entry, "escalate")
	early.ClosedAt = entry.RecordedAt.Add(-time.Hour)
	if _, err := store.Close(early); err == nil || !strings.Contains(err.Error(), "some earlier stoppage") {
		t.Fatalf("Close() error = %v, want a decision older than the stoppage refused", err)
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

// An attempt that never became a run is the docket entry with the least behind
// it — no run, no blocker, no change — and it is what a sweep reads to see that
// two items were tried and failed rather than seeing nothing at all. So it is
// written through the real store and read back whole, and the same dead dispatch
// docketed by a second session is one entry.
func TestAnAttemptThatNeverBecameARunIsDocketedAndReadBack(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newTestDocketStore(t, root)
	failure := "repository is not ready for an isolated run: the primary checkout has uncommitted changes"
	entry := triage.Entry{
		SchemaVersion: triage.SchemaVersion,
		Key:           triage.AttemptKey("yoyodyne-ifd.353", failure),
		Class:         triage.ClassUnstartedAttempt,
		ProductID:     "yoyodyne",
		WorkItemID:    "yoyodyne-ifd.353",
		WorkItemTitle: "A stall the watchdog can see is one the operator is told about",
		RecordedAt:    time.Date(2026, 9, 13, 6, 25, 0, 0, time.UTC),
		Failure:       failure,
		Attempt:       &triage.Attempt{SelectedBecause: "first in the product manager's order", ExcludedForTheSession: true},
		Counters:      triage.Counters{ReviewRoundsCap: 4},
	}
	created, err := store.RecordOnce(entry)
	if err != nil || !created {
		t.Fatalf("RecordOnce() = %t, error = %v", created, err)
	}
	again, err := newTestDocketStore(t, root).RecordOnce(entry)
	if err != nil || again {
		t.Fatalf("RecordOnce() again = %t, error = %v, want the same dead dispatch recorded once", again, err)
	}
	reloaded, err := newTestDocketStore(t, root).List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(reloaded) != 1 || reloaded[0].Class != triage.ClassUnstartedAttempt || reloaded[0].RunID != "" {
		t.Fatalf("List() = %#v, want the one runless entry back", reloaded)
	}
	if reloaded[0].Failure != failure || reloaded[0].Attempt == nil || !reloaded[0].Attempt.ExcludedForTheSession {
		t.Fatalf("entry did not survive intact: %#v", reloaded[0])
	}
}
