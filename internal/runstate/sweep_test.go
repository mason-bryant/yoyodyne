package runstate

import (
	"context"
	"errors"
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
	listed, err := store.List()
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

	listed, err := newSweepStore(t).List()
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
