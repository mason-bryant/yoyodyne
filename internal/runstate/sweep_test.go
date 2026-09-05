package runstate

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

func newSweepStore(t *testing.T) *SweepStore {
	t.Helper()
	store, err := NewSweepStore(t.TempDir(), "example")
	if err != nil {
		t.Fatalf("NewSweepStore() error = %v", err)
	}
	return store
}

// The cadence is the whole of what the claim is for: a task that fired ten
// minutes ago does not fire again on the next pull, and one whose interval has
// passed does.
func TestClaimPacesTheCadence(t *testing.T) {
	t.Parallel()

	store := newSweepStore(t)
	start := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	if _, err := store.Claim(context.Background(), "a-sweep", time.Hour, start); err != nil {
		t.Fatalf("first Claim() error = %v", err)
	}
	_, err := store.Claim(context.Background(), "a-sweep", time.Hour, start.Add(10*time.Minute))
	if !errors.Is(err, ErrSweepNotDue) {
		t.Fatalf("second Claim() error = %v, want ErrSweepNotDue", err)
	}
	claimed, err := store.Claim(context.Background(), "a-sweep", time.Hour, start.Add(time.Hour))
	if err != nil {
		t.Fatalf("third Claim() error = %v", err)
	}
	if claimed.Firings != 2 {
		t.Errorf("firings = %d, want 2", claimed.Firings)
	}
}

// A task that has never fired is due at once. A schedule turned on at nine that
// produced nothing until ten looks broken for an hour, and the first pass is the
// one most worth having.
func TestATaskThatHasNeverFiredIsDueAtOnce(t *testing.T) {
	t.Parallel()

	store := newSweepStore(t)
	claimed, err := store.Claim(context.Background(), "a-sweep", 24*time.Hour, time.Now())
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if claimed.Firings != 1 {
		t.Errorf("firings = %d, want the first firing", claimed.Firings)
	}
}

// A firing that failed waits for its next cadence rather than being retried at
// once. It is the deliberate opposite of the escalation record beside it: the
// next pass looks at everything this one would have, and retrying at once spends
// turns against whatever was already failing.
func TestAFailedFiringStillMovesTheClock(t *testing.T) {
	t.Parallel()

	store := newSweepStore(t)
	start := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	if _, err := store.Claim(context.Background(), "a-sweep", time.Hour, start); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	settled, err := store.Settle(context.Background(), "a-sweep", "the conversation could not be opened")
	if err != nil {
		t.Fatalf("Settle() error = %v", err)
	}
	if settled.Problem == "" {
		t.Error("the claim records no problem after a firing that failed")
	}
	if _, err := store.Claim(context.Background(), "a-sweep", time.Hour, start.Add(time.Minute)); !errors.Is(err, ErrSweepNotDue) {
		t.Fatalf("Claim() after a failed firing error = %v, want ErrSweepNotDue", err)
	}
}

// A problem is the most recent firing's or it is nothing. A claim still carrying
// last week's failure would send somebody after a fault that cleared six firings
// ago.
func TestANewFiringClearsTheLastOnesProblem(t *testing.T) {
	t.Parallel()

	store := newSweepStore(t)
	start := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	if _, err := store.Claim(context.Background(), "a-sweep", time.Hour, start); err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if _, err := store.Settle(context.Background(), "a-sweep", "it failed"); err != nil {
		t.Fatalf("Settle() error = %v", err)
	}
	claimed, err := store.Claim(context.Background(), "a-sweep", time.Hour, start.Add(time.Hour))
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if claimed.Problem != "" {
		t.Errorf("problem = %q, want it cleared by the firing that followed", claimed.Problem)
	}
}

// Two tasks pace independently: one that fired a minute ago must not hold back
// one that is due.
func TestTasksPaceIndependently(t *testing.T) {
	t.Parallel()

	store := newSweepStore(t)
	now := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	if _, err := store.Claim(context.Background(), "first-sweep", time.Hour, now); err != nil {
		t.Fatalf("Claim(first) error = %v", err)
	}
	if _, err := store.Claim(context.Background(), "second-sweep", time.Hour, now); err != nil {
		t.Fatalf("Claim(second) error = %v", err)
	}
	claimed, found, err := store.Find("first-sweep")
	if err != nil || !found {
		t.Fatalf("Find() = %v, %v, %v", claimed, found, err)
	}
	if claimed.Task != "first-sweep" {
		t.Errorf("task = %q, want the one asked for", claimed.Task)
	}
}

func TestSweepsAreAppendedAndReadBack(t *testing.T) {
	t.Parallel()

	store := newSweepStore(t)
	at := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	recorded := Sweep{
		Task:      "a-sweep",
		Role:      "development-manager",
		StartedAt: at,
		EndedAt:   at.Add(time.Minute),
		Turns:     2,
		CostUSD:   0.42,
		Result: &sweep.Result{
			Status:   sweep.StatusComplete,
			Summary:  "two dead claims, both released",
			Findings: []sweep.Finding{{Issue: "a dead claim", Disposition: sweep.DispositionFixed, Filed: []string{"yoyodyne-ifd.300"}}},
		},
	}
	if err := store.Append(recorded); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if err := store.Append(recorded); err != nil {
		t.Fatalf("second Append() error = %v", err)
	}
	listed, _, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("List() = %d sweeps, want 2", len(listed))
	}
	if listed[0].Result == nil || listed[0].Result.Summary != recorded.Result.Summary {
		t.Errorf("sweep = %+v, want the account as it was written", listed[0])
	}
}

// A product nothing has swept has recorded nothing, which is not a failure to
// read: it is what every project looks like before its first firing.
func TestListOfAnUnsweptProductIsEmpty(t *testing.T) {
	t.Parallel()

	listed, _, err := newSweepStore(t).List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed) != 0 {
		t.Errorf("List() = %v, want nothing", listed)
	}
}

// A pass with neither an account nor a problem is a firing the record could say
// nothing at all about, which is the one state it must not be able to hold: a
// reader would see a sweep that happened and no way to tell whether it found
// nothing or failed.
func TestASweepMustSayWhatBecameOfIt(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	silent := Sweep{Task: "a-sweep", Role: "development-manager", StartedAt: at, EndedAt: at}
	if err := newSweepStore(t).Append(silent); err == nil {
		t.Fatal("a sweep with no result and no problem was recorded")
	}
	// The quiet pass — an account that carries no findings — is the ordinary
	// result on a healthy harness and is a different fact entirely.
	quiet := silent
	quiet.Result = &sweep.Result{Status: sweep.StatusComplete, Summary: "nothing unresolved"}
	if !quiet.FoundNothing() {
		t.Error("a pass that gave an account with no findings is not reported as having found nothing")
	}
	if err := newSweepStore(t).Append(quiet); err != nil {
		t.Fatalf("Append() of a quiet pass error = %v", err)
	}
}

// The record has to hold what a whole firing can actually produce. A firing folds
// at most sweep.MaxMergedTurns turns together and each turn's block is capped at
// sweep.MaxBlockBytes, so an encoded bound below that product is a bound that
// throws the busiest passes' reports away as it writes them.
func TestTheEncodedBoundHoldsTheLargestFiringAnAccountCanReach(t *testing.T) {
	t.Parallel()

	reachable := sweep.MaxMergedTurns * sweep.MaxBlockBytes
	if maxEncodedSweepBytes <= reachable {
		t.Fatalf("the encoded sweep bound is %d bytes and a firing's account can reach %d (%d turns of %d), so the heaviest passes would not store",
			maxEncodedSweepBytes, reachable, sweep.MaxMergedTurns, sweep.MaxBlockBytes)
	}
}

// And the store actually takes one. The bound above is arithmetic; this writes a
// pass-sized account and reads it back, which is what the durable report is for.
func TestAPassSizedAccountIsWrittenAndReadBack(t *testing.T) {
	t.Parallel()

	store := newSweepStore(t)
	at := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	account := &sweep.Result{Status: sweep.StatusComplete, Summary: "a very heavy pass"}
	for i := 0; i < sweep.MaxPassFindings; i++ {
		account.Findings = append(account.Findings, sweep.Finding{
			Issue:       strings.Repeat("a thing that was found ", 40),
			Disposition: sweep.DispositionFiled,
			Filed:       []string{"yoyodyne-ifd.300"},
		})
	}
	for i := 0; i < sweep.MaxPassQuestions; i++ {
		account.Questions = append(account.Questions, "something only a person can settle")
	}
	recorded := Sweep{
		Task:      "a-sweep",
		Role:      "development-manager",
		StartedAt: at,
		EndedAt:   at.Add(time.Minute),
		Turns:     sweep.MaxMergedTurns,
		Result:    account,
	}
	if err := store.Append(recorded); err != nil {
		t.Fatalf("Append() of a pass-sized account error = %v", err)
	}
	listed, _, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed) != 1 || listed[0].Result == nil {
		t.Fatalf("listed = %+v, want the pass-sized account read back", listed)
	}
	if len(listed[0].Result.Findings) != sweep.MaxPassFindings {
		t.Errorf("findings = %d, want the %d that were written", len(listed[0].Result.Findings), sweep.MaxPassFindings)
	}
}

// The failure this log is actually exposed to: a write of a whole pass is not
// atomic, so a process killed partway through one leaves a torn line. Failing the
// listing on it would make one interrupted write cost every report before it,
// permanently, on the only surface those reports are read from.
func TestATornLineDoesNotCostTheReportsAroundIt(t *testing.T) {
	t.Parallel()

	store := newSweepStore(t)
	at := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	before := Sweep{
		Task: "a-sweep", Role: "development-manager",
		StartedAt: at, EndedAt: at.Add(time.Minute),
		Turns: 1, Result: &sweep.Result{Status: sweep.StatusComplete, Summary: "the pass before"},
	}
	after := before
	after.Result = &sweep.Result{Status: sweep.StatusComplete, Summary: "the pass after"}
	if err := store.Append(before); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	// A write that stopped mid-record, as a crash leaves it.
	torn, err := os.OpenFile(store.Path(), os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	if _, err := torn.WriteString(`{"schema_version":1,"product_id":"example","task":"a-sw` + "\n"); err != nil {
		t.Fatalf("WriteString() error = %v", err)
	}
	torn.Close()
	if err := store.Append(after); err != nil {
		t.Fatalf("Append() after the torn line error = %v", err)
	}

	listed, unreadable, err := store.List()
	if err != nil {
		t.Fatalf("List() over a torn log error = %v, want the readable reports back", err)
	}
	if len(listed) != 2 {
		t.Fatalf("listed = %d sweeps, want the two either side of the torn line", len(listed))
	}
	if listed[0].Result.Summary != "the pass before" || listed[1].Result.Summary != "the pass after" {
		t.Errorf("listed = %+v, want both passes in the order they were written", listed)
	}
	// Named rather than dropped: a listing short by a record it never mentioned is
	// a worse answer than the failure it replaced.
	if len(unreadable) != 1 {
		t.Fatalf("unreadable = %+v, want the torn line named", unreadable)
	}
	if unreadable[0].Line != 2 {
		t.Errorf("unreadable line = %d, want the second line of the log", unreadable[0].Line)
	}
	if unreadable[0].Problem == "" {
		t.Error("the torn line is named with no reason, so nobody can tell what happened to it")
	}
}

// A record from another product in this product's log is the same shape of
// problem and gets the same answer: named, and not fatal to the rest.
func TestARecordFromAnotherProductIsNamedRatherThanFatal(t *testing.T) {
	t.Parallel()

	store := newSweepStore(t)
	at := time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC)
	if err := store.Append(Sweep{
		Task: "a-sweep", Role: "development-manager",
		StartedAt: at, EndedAt: at, Turns: 1,
		Result: &sweep.Result{Status: sweep.StatusComplete, Summary: "ours"},
	}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	foreign, err := os.OpenFile(store.Path(), os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	if _, err := foreign.WriteString(`{"schema_version":1,"product_id":"elsewhere","task":"a-sweep","role":"development-manager","started_at":"2026-09-05T09:00:00Z","ended_at":"2026-09-05T09:00:00Z","turns":1,"problem":"theirs"}` + "\n"); err != nil {
		t.Fatalf("WriteString() error = %v", err)
	}
	foreign.Close()

	listed, unreadable, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed) != 1 || listed[0].Result.Summary != "ours" {
		t.Errorf("listed = %+v, want only this product's pass", listed)
	}
	if len(unreadable) != 1 || !strings.Contains(unreadable[0].Problem, "elsewhere") {
		t.Errorf("unreadable = %+v, want the foreign record named", unreadable)
	}
}
