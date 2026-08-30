package runstate

import (
	"context"
	"strings"
	"testing"
	"time"
)

// The accounting the class turns on: a round given back is given back to the
// attempt that was charged for it, and to no other.
func TestAReturnedRoundIsTakenBackFromTheAttemptThatWasChargedForIt(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	for _, attempt := range []string{"run-a#0", "run-a#1", "run-a#2"} {
		if _, err := store.RecordReviewRound(context.Background(), "yoyodyne-ifd.190", attempt, time.Now()); err != nil {
			t.Fatalf("RecordReviewRound(%q) error = %v", attempt, err)
		}
	}

	// A round the record does not stand at is not this caller's to give back: the
	// count belongs to whatever was judged since.
	counters, returned, err := store.ReturnReviewRound(context.Background(), "yoyodyne-ifd.190", "run-a#0", time.Now())
	if err != nil {
		t.Fatalf("ReturnReviewRound() error = %v", err)
	}
	if returned || counters.ReviewRounds != 3 {
		t.Fatalf("returned = %t at %d round(s), want an older attempt refused with the count left at 3", returned, counters.ReviewRounds)
	}

	counters, returned, err = store.ReturnReviewRound(context.Background(), "yoyodyne-ifd.190", "run-a#2", time.Now())
	if err != nil {
		t.Fatalf("ReturnReviewRound() error = %v", err)
	}
	if !returned || counters.ReviewRounds != 2 {
		t.Fatalf("returned = %t at %d round(s), want the head round given back", returned, counters.ReviewRounds)
	}
	// The head is cleared with the round it named, so the record no longer claims
	// a round is counted for an attempt that is not being charged for one.
	if counters.LastRound != "" {
		t.Fatalf("last round = %q after the return, want the head cleared", counters.LastRound)
	}
	// And a second return of the same round gives back nothing: the round is
	// already back, and repeating a settle must not credit the item twice.
	counters, returned, err = store.ReturnReviewRound(context.Background(), "yoyodyne-ifd.190", "run-a#2", time.Now())
	if err != nil {
		t.Fatalf("ReturnReviewRound() error = %v", err)
	}
	if returned || counters.ReviewRounds != 2 {
		t.Fatalf("returned = %t at %d round(s), want the repeat to give back nothing", returned, counters.ReviewRounds)
	}
}

// The grant is a different question from the run's own budget, and the two
// numbers say so. A continuation whose round the environment refused bought the
// item nothing and still cost the run the attempt slot it spent.
func TestAReturnedContinuationLeavesTheGrantUnspentAndTheRunsBudgetIntact(t *testing.T) {
	t.Parallel()

	state := State{RepairContinuations: []RepairContinuation{
		{GrantedAttempts: 1, Reason: "the first grant", ContinuedAt: time.Now()},
		{GrantedAttempts: 1, Reason: "the second grant", ContinuedAt: time.Now()},
	}}
	if carried, granted := state.CarriedOutRepairAttempts(), state.GrantedRepairAttempts(); carried != 2 || granted != 2 {
		t.Fatalf("carried = %d, granted = %d before any return; want 2 and 2", carried, granted)
	}
	if !state.ReturnGrantedRound() {
		t.Fatal("ReturnGrantedRound() gave nothing back, so the grant stays spent on a round that delivered nothing")
	}
	if carried := state.CarriedOutRepairAttempts(); carried != 1 {
		t.Fatalf("carried-out attempts = %d, want the refused round no longer counted against the item's grant", carried)
	}
	if granted := state.GrantedRepairAttempts(); granted != 2 {
		t.Fatalf("granted attempts = %d, want the run's own budget unchanged: the attempt slot was spent", granted)
	}
	// The most recent continuation is the one that bought the round now settling,
	// and it is the only one a settle may return.
	if state.RepairContinuations[0].Returned || !state.RepairContinuations[1].Returned {
		t.Fatalf("returned continuations = %#v, want only the most recent one", state.RepairContinuations)
	}
	// A settle asked twice for the same round gives back nothing more.
	if state.ReturnGrantedRound() {
		t.Fatal("ReturnGrantedRound() gave the same round back twice")
	}
}

// A refusal survives the record it lives in, and a record nothing could have
// written is refused rather than honored: the class returns budget, so a cause
// nothing declared would be a budget nothing accounted for.
func TestAnEnvironmentalRefusalIsHeldToWhatCouldHaveWrittenIt(t *testing.T) {
	t.Parallel()

	refusal := EnvironmentalRefusal{
		Cause:         CauseHandbackMissingChange,
		Detail:        "the worktree holds no change at all against the base commit the run recorded",
		RecordedAt:    time.Now(),
		Refused:       true,
		GrantReturned: true,
	}
	if err := refusal.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want the settled refusal accepted", err)
	}
	described := refusal.Describe()
	for _, want := range []string{string(CauseHandbackMissingChange), "granted repair round it consumed was returned"} {
		if !strings.Contains(described, want) {
			t.Fatalf("Describe() = %q, want it to say %q", described, want)
		}
	}

	for _, test := range []struct {
		name   string
		broken EnvironmentalRefusal
	}{
		{name: "a cause nothing declared", broken: EnvironmentalRefusal{Cause: "the-network-was-slow", RecordedAt: time.Now()}},
		{name: "a return with no refusal", broken: EnvironmentalRefusal{Cause: CauseDirtyPrimary, RecordedAt: time.Now(), RoundReturned: true}},
		{name: "no moment", broken: EnvironmentalRefusal{Cause: CauseSandboxSpawnFailure}},
	} {
		if err := test.broken.Validate(); err == nil {
			t.Errorf("Validate() accepted %s", test.name)
		}
	}
}

// The run record carries the refusal durably, which is what the docket and the
// thread both read it from.
func TestAnEnvironmentalRefusalSurvivesTheRunRecord(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusRunning)
	state.Environmental = &EnvironmentalRefusal{
		Cause:         CauseStaleBinaryDispatch,
		Detail:        "the build that dispatched this round predates the gate the grant relied on",
		RecordedAt:    time.Now().UTC().Truncate(time.Second),
		Refused:       true,
		RoundReturned: true,
		GrantReturned: true,
	}
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	loaded, err := store.Load(state.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	recorded := loaded.Environmental
	if recorded == nil {
		t.Fatal("the loaded run carries no environmental refusal, so the class did not survive the write")
	}
	if recorded.Cause != state.Environmental.Cause || recorded.Detail != state.Environmental.Detail {
		t.Fatalf("loaded environmental = %#v, want the cause and detail as they were recorded", recorded)
	}
	if !recorded.RecordedAt.Equal(state.Environmental.RecordedAt) {
		t.Fatalf("loaded recorded_at = %s, want %s", recorded.RecordedAt, state.Environmental.RecordedAt)
	}
	if !recorded.Refused || !recorded.RoundReturned || !recorded.GrantReturned {
		t.Fatalf("loaded environmental = %#v, want what the settle gave back to have survived the write", recorded)
	}
}
