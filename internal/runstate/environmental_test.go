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
	charging := chargingProcess(t)
	for _, attempt := range []string{"run-a#0", "run-a#1", "run-a#2"} {
		if _, err := store.RecordReviewRound(context.Background(), "yoyodyne-ifd.190", attempt, charging, time.Now()); err != nil {
			t.Fatalf("RecordReviewRound(%q) error = %v", attempt, err)
		}
	}

	// A round the record does not stand at is not this caller's to give back: the
	// count belongs to whatever was judged since.
	counters, round, err := store.ReturnReviewRound(context.Background(), "yoyodyne-ifd.190", "run-a#0", charging, time.Now())
	if err != nil {
		t.Fatalf("ReturnReviewRound() error = %v", err)
	}
	if round.Returned || counters.ReviewRounds != 3 {
		t.Fatalf("returned = %t at %d round(s), want an older attempt refused with the count left at 3", round.Returned, counters.ReviewRounds)
	}
	// And an older attempt is not a round somebody else is holding: the record
	// stands somewhere else entirely, which says nothing about who charged what.
	if round.Mismatched {
		t.Fatalf("return of an older attempt = %#v, want it refused for the record standing elsewhere rather than reported as another process's round", round)
	}

	counters, round, err = store.ReturnReviewRound(context.Background(), "yoyodyne-ifd.190", "run-a#2", charging, time.Now())
	if err != nil {
		t.Fatalf("ReturnReviewRound() error = %v", err)
	}
	if !round.Returned || counters.ReviewRounds != 2 {
		t.Fatalf("returned = %t at %d round(s), want the head round given back", round.Returned, counters.ReviewRounds)
	}
	// The head is cleared with the round it named, so the record no longer claims
	// a round is counted for an attempt that is not being charged for one.
	if counters.LastRound != "" || counters.LastRoundCharger != "" {
		t.Fatalf("last round = %q charged by %q after the return, want the head cleared", counters.LastRound, counters.LastRoundCharger)
	}
	// And a second return of the same round gives back nothing: the round is
	// already back, and repeating a settle must not credit the item twice.
	counters, round, err = store.ReturnReviewRound(context.Background(), "yoyodyne-ifd.190", "run-a#2", charging, time.Now())
	if err != nil {
		t.Fatalf("ReturnReviewRound() error = %v", err)
	}
	if round.Returned || counters.ReviewRounds != 2 {
		t.Fatalf("returned = %t at %d round(s), want the repeat to give back nothing", round.Returned, counters.ReviewRounds)
	}
}

// The guard the re-entered run turns on, at the store: the round at the head is
// given back to the process that charged it and to no other. A run picked up
// again after its process died carries the same run identifier at the same
// attempt number, so the attempt alone would hand the new process a round the
// old one spent on a verdict the item really got.
func TestARoundIsGivenBackOnlyToTheProcessThatChargedIt(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	charged, reEntered := chargingProcess(t), chargingProcess(t)
	if charged == reEntered {
		t.Fatal("two processes were minted the same identity, so nothing here tells them apart")
	}
	if _, err := store.RecordReviewRound(context.Background(), "yoyodyne-ifd.196", "run-a#1", charged, time.Now()); err != nil {
		t.Fatalf("RecordReviewRound() error = %v", err)
	}
	// The re-entered process re-asks the review of the attempt already at the
	// head, which counts nothing — so it has bought nothing to give back.
	counters, err := store.RecordReviewRound(context.Background(), "yoyodyne-ifd.196", "run-a#1", reEntered, time.Now())
	if err != nil {
		t.Fatalf("RecordReviewRound() error = %v", err)
	}
	if counters.ReviewRounds != 1 || counters.LastRoundCharger != charged {
		t.Fatalf("counters = %d round(s) charged by %q, want the one round left with the process that charged it", counters.ReviewRounds, counters.LastRoundCharger)
	}

	counters, round, err := store.ReturnReviewRound(context.Background(), "yoyodyne-ifd.196", "run-a#1", reEntered, time.Now())
	if err != nil {
		t.Fatalf("ReturnReviewRound() error = %v", err)
	}
	if round.Returned || counters.ReviewRounds != 1 {
		t.Fatalf("returned = %t at %d round(s), want the predecessor's round left spent", round.Returned, counters.ReviewRounds)
	}
	// And the refusal says which of the two it was, because a round somebody else
	// is holding and a record standing at some other attempt leave the item in
	// different places.
	if !round.Mismatched || round.ChargedBy != charged {
		t.Fatalf("round = %#v, want the mismatch reported against %q", round, charged)
	}
	if counters.LastRound != "run-a#1" || counters.LastRoundCharger != charged {
		t.Fatalf("head = %q charged by %q, want the refused return to have left the record alone", counters.LastRound, counters.LastRoundCharger)
	}

	// The process that did charge it still gets it back.
	counters, round, err = store.ReturnReviewRound(context.Background(), "yoyodyne-ifd.196", "run-a#1", charged, time.Now())
	if err != nil {
		t.Fatalf("ReturnReviewRound() error = %v", err)
	}
	if !round.Returned || round.Mismatched || counters.ReviewRounds != 0 {
		t.Fatalf("returned = %#v at %d round(s), want the charging process given its round back", round, counters.ReviewRounds)
	}
}

// A record written before rounds carried the process that charged them names
// nobody, and a return against it is refused rather than honored. Leaving a
// round spent costs an item a round it should have kept; crediting one nothing
// says this process spent is a budget nothing bounds.
func TestARoundNothingAttributesIsNotReturnedToWhoeverAsks(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	// A round counted with no charger beside it, which is the only shape a record
	// written before this accounting can be in. It is a valid record: the charger
	// is what a later write added, not something that was always required.
	legacy, err := store.update(context.Background(), "yoyodyne-ifd.196", time.Now(), func(stored *TriageCounters) error {
		stored.ReviewRounds, stored.LastRound = 1, "run-a#0"
		return nil
	})
	if err != nil {
		t.Fatalf("seeding a record from before this accounting: %v", err)
	}
	if legacy.LastRoundCharger != "" {
		t.Fatalf("seeded charger = %q, want the record to name nobody", legacy.LastRoundCharger)
	}

	after, round, err := store.ReturnReviewRound(context.Background(), "yoyodyne-ifd.196", "run-a#0", chargingProcess(t), time.Now())
	if err != nil {
		t.Fatalf("ReturnReviewRound() error = %v", err)
	}
	if round.Returned || after.ReviewRounds != 1 {
		t.Fatalf("returned = %t at %d round(s), want a round nobody is recorded as having charged left spent", round.Returned, after.ReviewRounds)
	}
	if !round.Mismatched || round.ChargedBy != "" {
		t.Fatalf("round = %#v, want the mismatch reported against nobody", round)
	}
}

// chargingProcess is one process's identity, minted the way the harness mints
// it. A test that wants two of them asks twice, which is the whole of what a run
// re-entered by a second process differs by.
func chargingProcess(t *testing.T) string {
	t.Helper()
	charging, err := NewChargingProcess()
	if err != nil {
		t.Fatalf("NewChargingProcess() error = %v", err)
	}
	return charging
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
		Settled:       true,
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
		{name: "a cause nothing declared", broken: EnvironmentalRefusal{Cause: "the-network-was-slow", RecordedAt: time.Now(), Settled: true}},
		{name: "a return with no refusal", broken: EnvironmentalRefusal{Cause: CauseDirtyPrimary, RecordedAt: time.Now(), Settled: true, RoundReturned: true}},
		{name: "a refusal no settle made", broken: EnvironmentalRefusal{Cause: CauseDirtyPrimary, RecordedAt: time.Now(), Refused: true}},
		{name: "no moment", broken: EnvironmentalRefusal{Cause: CauseSandboxSpawnFailure}},
	} {
		if err := test.broken.Validate(); err == nil {
			t.Errorf("Validate() accepted %s", test.name)
		}
	}
}

// Describe never claims an accounting that did not happen. The three readings
// that would mislead an operator are the ones asserted: a round nothing has
// decided about yet, a refusal that reached nothing to give back, and a refusal
// whose return could not be written — where the item's counters really are
// higher than the round cost it.
func TestAnEnvironmentalRefusalDescribesOnlyWhatItActuallyGaveBack(t *testing.T) {
	t.Parallel()

	unsettled := EnvironmentalRefusal{Cause: CauseSandboxSpawnFailure, RecordedAt: time.Now()}
	if described := unsettled.Describe(); !strings.Contains(described, "has not settled") {
		t.Fatalf("Describe() = %q, want an undecided round said as one", described)
	}

	nothingToReturn := EnvironmentalRefusal{Cause: CauseDirtyPrimary, RecordedAt: time.Now(), Settled: true, Refused: true}
	described := nothingToReturn.Describe()
	if !strings.Contains(described, "reached nothing that spends") {
		t.Fatalf("Describe() = %q, want a refusal with nothing to give back said as one", described)
	}
	if strings.Contains(described, "was returned") {
		t.Fatalf("Describe() = %q claims a return it never made", described)
	}

	unpaid := EnvironmentalRefusal{
		Cause:      CauseHandbackMissingChange,
		RecordedAt: time.Now(),
		Settled:    true,
		Refused:    true,
		Problem:    "the review round attempt run-a#3 was charged could not be returned",
	}
	described = unpaid.Describe()
	if !strings.Contains(described, "could not be written") || !strings.Contains(described, "counters are higher") {
		t.Fatalf("Describe() = %q, want the one state where the counters are wrong said plainly", described)
	}
	if strings.Contains(described, "stands where it did") || strings.Contains(described, "was returned") {
		t.Fatalf("Describe() = %q tells a reader the item stands where it did while its counters say otherwise", described)
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
		Settled:       true,
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
	if !recorded.Settled || !recorded.Refused || !recorded.RoundReturned || !recorded.GrantReturned {
		t.Fatalf("loaded environmental = %#v, want what the settle gave back to have survived the write", recorded)
	}
}
