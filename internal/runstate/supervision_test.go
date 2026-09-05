package runstate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
	"github.com/mason-bryant/yoyodyne/internal/supervision"
)

// The whole of what restart recovery rests on: the request was written before
// the role was invoked, so a process that died carrying it left a record the
// next process reads. Nothing needed the session that died to still be there.
func TestARequestOutlivesTheProcessThatOpenedIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newTestSupervisionStore(t, root)
	opened := testStoredRequest(1)
	opened.Attempts = []supervision.Attempt{testStoredAttempt(1, "harness-a", opened.OpenedAt)}
	if err := store.SaveRequest(opened); err != nil {
		t.Fatalf("SaveRequest() error = %v", err)
	}

	// A second process, addressing the same state root.
	reopened := newTestSupervisionStore(t, root)
	loaded, err := reopened.LoadRequest(opened.ID)
	if err != nil {
		t.Fatalf("LoadRequest() error = %v", err)
	}
	carried, running := loaded.InFlight()
	if !running || carried.Holder != "harness-a" {
		t.Fatalf("loaded = %+v, want the attempt the dead process opened", loaded)
	}
}

// The restart the whole slice is for, end to end: three requests interrupted in
// three different states, read back off the disk by a process that holds
// nothing, and each one taken to the only place it can go.
func TestARestartReadsBackWhatEachInterruptedRequestNeeds(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newTestSupervisionStore(t, root)

	// One was being carried when the process died and has attempts left.
	interrupted := testStoredRequest(1)
	interrupted.Attempts = []supervision.Attempt{testStoredAttempt(1, "harness-a", interrupted.OpenedAt)}

	// One had its answer recorded and its ending not yet written, which is the
	// window a crash can land in and the one that must not be paid for twice.
	answered := testStoredRequest(2)
	answered.Topic = "chat-two"
	answered.Attempts = []supervision.Attempt{testStoredAttempt(1, "harness-a", answered.OpenedAt)}
	answered.Response = &supervision.Response{
		Text:    "It costs a design revision.",
		At:      answered.OpenedAt.Add(time.Minute),
		Attempt: 1,
	}

	// One had spent its last attempt, so nothing will finish it.
	abandoned := testStoredRequest(3)
	abandoned.Topic = "chat-three"
	abandoned.CycleLimit = 1
	abandoned.Attempts = []supervision.Attempt{testStoredAttempt(1, "harness-a", abandoned.OpenedAt)}

	for _, request := range []supervision.Request{interrupted, answered, abandoned} {
		if err := store.SaveRequest(request); err != nil {
			t.Fatalf("SaveRequest(%s) error = %v", request.ID, err)
		}
	}

	restarted := newTestSupervisionStore(t, root)
	requests, err := restarted.Requests()
	if err != nil {
		t.Fatalf("Requests() error = %v", err)
	}
	if len(requests) != 3 {
		t.Fatalf("Requests() = %d records, want 3", len(requests))
	}

	// A restart holds no leases, because the process that held them is gone.
	plan := supervision.Survey(supervision.State{Requests: requests})

	if len(plan.Deliver) != 1 || plan.Deliver[0].RequestID != interrupted.ID || !plan.Deliver[0].Reclaimed {
		t.Fatalf("Deliver = %#v, want only the interrupted request reclaimed", plan.Deliver)
	}
	settled := make(map[string]supervision.Settlement, len(plan.Settle))
	for _, settlement := range plan.Settle {
		settled[settlement.RequestID] = settlement
	}
	if settled[answered.ID].Outcome != supervision.OutcomeAnswered {
		t.Fatalf("Settle = %#v, want the recorded answer settled rather than asked again", plan.Settle)
	}
	if settled[abandoned.ID].Outcome != supervision.OutcomeUnresolved || !settled[abandoned.ID].Escalate {
		t.Fatalf("Settle = %#v, want the abandoned request ended and escalated", plan.Settle)
	}
	if len(plan.Degraded) != 1 || plan.Degraded[0].RequestID != abandoned.ID {
		t.Fatalf("Degraded = %#v, want the request nothing will finish named", plan.Degraded)
	}
}

// A request is one record as it now stands rather than a stream of events about
// one, so recording an attempt against it replaces it.
func TestSavingARequestAgainReplacesIt(t *testing.T) {
	t.Parallel()

	store := newTestSupervisionStore(t, t.TempDir())
	request := testStoredRequest(1)
	if err := store.SaveRequest(request); err != nil {
		t.Fatalf("SaveRequest() error = %v", err)
	}
	request.Attempts = []supervision.Attempt{testStoredAttempt(1, "harness-b", request.OpenedAt)}
	request.UpdatedAt = request.OpenedAt.Add(time.Minute)
	if err := store.SaveRequest(request); err != nil {
		t.Fatalf("SaveRequest() again error = %v", err)
	}

	listed, err := store.Requests()
	if err != nil {
		t.Fatalf("Requests() error = %v", err)
	}
	if len(listed) != 1 || len(listed[0].Attempts) != 1 {
		t.Fatalf("Requests() = %#v, want one record carrying the attempt", listed)
	}
}

// Requests are listed oldest first, which is the order they are delivered in.
func TestRequestsAreListedOldestFirst(t *testing.T) {
	t.Parallel()

	store := newTestSupervisionStore(t, t.TempDir())
	for _, n := range []int{3, 1, 2} {
		if err := store.SaveRequest(testStoredRequest(n)); err != nil {
			t.Fatalf("SaveRequest() error = %v", err)
		}
	}
	listed, err := store.Requests()
	if err != nil {
		t.Fatalf("Requests() error = %v", err)
	}
	for index, want := range []int{1, 2, 3} {
		if listed[index].ID != testStoredRequestID(want) {
			t.Fatalf("Requests()[%d] = %q, want %q", index, listed[index].ID, testStoredRequestID(want))
		}
	}
}

// A product whose roles have never asked each other anything is not a failure to
// read.
func TestAProductWithNoRecordsReadsEmpty(t *testing.T) {
	t.Parallel()

	store := newTestSupervisionStore(t, t.TempDir())
	requests, err := store.Requests()
	if err != nil || requests != nil {
		t.Fatalf("Requests() = %#v, %v, want nothing and no failure", requests, err)
	}
	records, err := store.Readiness()
	if err != nil || records != nil {
		t.Fatalf("Readiness() = %#v, %v, want nothing and no failure", records, err)
	}
}

func TestAnIdentifierThatNamesNothingSaysSo(t *testing.T) {
	t.Parallel()

	store := newTestSupervisionStore(t, t.TempDir())
	if _, err := store.LoadRequest(testStoredRequestID(9)); !errors.Is(err, ErrNoRequest) {
		t.Fatalf("LoadRequest() error = %v, want ErrNoRequest", err)
	}
	if _, err := store.LoadReadiness(fmt.Sprintf("readiness-%032x", 9)); !errors.Is(err, ErrNoReadiness) {
		t.Fatalf("LoadReadiness() error = %v, want ErrNoReadiness", err)
	}
}

// Nothing that came from outside names a path.
func TestAnInvalidIdentifierNamesNoPath(t *testing.T) {
	t.Parallel()

	store := newTestSupervisionStore(t, t.TempDir())
	if _, err := store.LoadRequest("../../escape"); err == nil || errors.Is(err, ErrNoRequest) {
		t.Fatalf("LoadRequest() error = %v, want the identifier refused", err)
	}
}

// A record belongs to the product whose store it is in. One from elsewhere is
// refused rather than filed.
func TestARecordFromAnotherProductIsRefused(t *testing.T) {
	t.Parallel()

	store := newTestSupervisionStore(t, t.TempDir())
	elsewhere := testStoredRequest(1)
	elsewhere.ProductID = "other"
	if err := store.SaveRequest(elsewhere); err == nil ||
		!strings.Contains(err.Error(), "does not match store product") {
		t.Fatalf("SaveRequest() error = %v, want the product mismatch refused", err)
	}
}

// A record this build cannot claim to understand is refused rather than read
// loosely: acting on a request whose terms have changed under us is worse than
// declining to read it.
func TestARecordWithAnUnknownFieldIsRefused(t *testing.T) {
	t.Parallel()

	store := newTestSupervisionStore(t, t.TempDir())
	request := testStoredRequest(1)
	if err := store.SaveRequest(request); err != nil {
		t.Fatalf("SaveRequest() error = %v", err)
	}
	path := filepath.Join(store.RequestsRoot(), request.ID+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the record back: %v", err)
	}
	tampered := strings.Replace(string(raw), "\"schema_version\": 1,",
		"\"schema_version\": 1,\n  \"authority\": \"granted\",", 1)
	if tampered == string(raw) {
		t.Fatalf("the test did not change the record: %s", raw)
	}
	if err := os.WriteFile(path, []byte(tampered), 0o600); err != nil {
		t.Fatalf("write the tampered record: %v", err)
	}
	if _, err := store.LoadRequest(request.ID); err == nil || !strings.Contains(err.Error(), "authority") {
		t.Fatalf("LoadRequest() error = %v, want the unknown field named", err)
	}
}

// Judgments are not revised. A role that judges an item again records another
// one, both stay readable, and which stands is a question about the records.
func TestJudgingAnItemAgainKeepsBothRecords(t *testing.T) {
	t.Parallel()

	store := newTestSupervisionStore(t, t.TempDir())
	first := testStoredReadiness(1)
	first.Disposition = supervision.DispositionDesignNeeded
	second := testStoredReadiness(2)
	second.JudgedAt = first.JudgedAt.Add(time.Hour)
	for _, record := range []supervision.Readiness{first, second} {
		if err := store.SaveReadiness(record); err != nil {
			t.Fatalf("SaveReadiness() error = %v", err)
		}
	}

	records, err := store.Readiness()
	if err != nil {
		t.Fatalf("Readiness() error = %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("Readiness() = %#v, want both judgments kept", records)
	}
	current := supervision.Current(records)
	if len(current) != 1 || current[0].ID != second.ID {
		t.Fatalf("Current() = %#v, want the later judgment standing", current)
	}
}

// Confinement is decided against the filesystem rather than against the path
// string. The identifier pattern is a lexical check, and a directory under the
// state root can be replaced by a symlink at any time — so a write aimed
// through one is refused rather than landing outside the root it declared.
func TestAWriteThroughASymlinkOutOfTheRootIsRefused(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	elsewhere := t.TempDir()
	store := newTestSupervisionStore(t, root)

	// The requests directory is replaced by a link out of the state root, which
	// is exactly what a lexical check cannot see.
	if err := os.MkdirAll(store.Root(), 0o700); err != nil {
		t.Fatalf("create the supervision root: %v", err)
	}
	if err := os.Symlink(elsewhere, store.RequestsRoot()); err != nil {
		t.Fatalf("plant the symlink: %v", err)
	}

	err := store.SaveRequest(testStoredRequest(1))
	if err == nil {
		t.Fatalf("SaveRequest() wrote through a symlink out of the state root")
	}
	var escape *repowrite.EscapeError
	if !errors.As(err, &escape) {
		t.Fatalf("SaveRequest() error = %v, want the escape named", err)
	}
	if entries, readErr := os.ReadDir(elsewhere); readErr != nil || len(entries) != 0 {
		t.Fatalf("the write landed outside the root: %v, %v", entries, readErr)
	}
}

func newTestSupervisionStore(t *testing.T, root string) *SupervisionStore {
	t.Helper()
	store, err := NewSupervisionStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewSupervisionStore() error = %v", err)
	}
	return store
}

func testStoredRequestID(n int) string { return fmt.Sprintf("request-%032x", n) }

// testStoredAttempt is one attempt as the harness opens it: still running, and
// already naming what it is about to spend, since an invocation nobody could
// attribute is one the contract refuses to record.
func testStoredAttempt(number int, holder string, at time.Time) supervision.Attempt {
	return supervision.Attempt{
		Number:         number,
		Holder:         holder,
		StartedAt:      at,
		Backend:        "claudecode",
		Model:          "opus",
		AccountAlias:   "work",
		ConfigRevision: "cfg-0a1b2c3d",
	}
}

func testStoredRequest(n int) supervision.Request {
	opened := time.Date(2026, 8, 22, 9, 0, 0, 0, time.UTC).Add(time.Duration(n) * time.Minute)
	return supervision.Request{
		SchemaVersion: supervision.SchemaVersion,
		ID:            testStoredRequestID(n),
		ProductID:     "yoyodyne",
		Topic:         "chat-one",
		Kind:          supervision.KindConsult,
		From:          domain.RoleProductManager,
		To:            domain.RoleArchitect,
		Subject:       "what would this cost, and what am I missing?",
		CycleLimit:    supervision.DefaultCycleLimit,
		OpenedAt:      opened,
		UpdatedAt:     opened,
	}
}

func testStoredReadiness(n int) supervision.Readiness {
	return supervision.Readiness{
		SchemaVersion: supervision.SchemaVersion,
		ID:            fmt.Sprintf("readiness-%032x", n),
		ProductID:     "yoyodyne",
		Item:          "yoyodyne-ifd.142",
		Judgment:      supervision.JudgmentArchitecture,
		Disposition:   supervision.DispositionClear,
		Evidence:      "the contract is ratified and the slice is bounded to three properties",
		Against:       []supervision.Reference{{What: "artifact", ID: "v1-goals", Revision: "r7"}},
		JudgedBy:      domain.RoleArchitect,
		JudgedAt:      time.Date(2026, 8, 22, 8, 0, 0, 0, time.UTC),
	}
}
