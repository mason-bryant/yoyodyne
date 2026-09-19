package runstate

import (
	"strings"
	"testing"
	"time"
)

// approvedStoppedState is a run whose change was approved and whose promotion
// the environment then refused, with the stop recorded on it.
func approvedStoppedState(t *testing.T) State {
	t.Helper()
	state := integratedState(t, PhaseIntegrating)
	state.Status = StatusFailed
	state.Integration = nil
	state.Failure = "integrate approved change: primary checkout is not ready for integration"
	state.IntegrationStop = &IntegrationStop{
		Cause:      CauseDirtyPrimary,
		Detail:     state.Failure,
		Phase:      PhaseIntegrating,
		RecordedAt: state.UpdatedAt,
	}
	return state
}

// The record is what makes a stop resumable, and the shape of one is held to
// what a resumption needs: an approving verdict, no promotion, and a cause the
// harness records.
func TestAnIntegrationStopIsOnlyRecordedOnAnApprovedUnpromotedChange(t *testing.T) {
	t.Parallel()

	stopped := approvedStoppedState(t)
	if err := stopped.Validate(); err != nil {
		t.Fatalf("Validate() error = %v, want the stopped run valid", err)
	}
	if !stopped.ResumableIntegration() || !stopped.ApprovedAwaitingIntegration() {
		t.Fatalf("the stopped run is not resumable: %#v", stopped)
	}
	for name, shape := range map[string]func(State) State{
		"beside a promotion": func(state State) State {
			state.Status = StatusSucceeded
			state.Failure = ""
			state.Integration = &Integration{
				TargetBranch: "main", SourceCommit: strings.Repeat("b", 40),
				TargetCommit: strings.Repeat("b", 40), PreviousTargetCommit: strings.Repeat("a", 40),
			}
			return state
		},
		"without an approval": func(state State) State {
			state.ReviewDecision = ReviewRepair
			return state
		},
		"at a phase before the review": func(state State) State {
			state.IntegrationStop.Phase = PhaseDeveloping
			return state
		},
		"with a cause nothing records": func(state State) State {
			state.IntegrationStop.Cause = "a reason somebody typed"
			return state
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := shape(approvedStoppedState(t)).Validate(); err == nil {
				t.Fatal("Validate() accepted an integration stop the record cannot have written")
			}
		})
	}
}

// What a resumed run reads as while it promotes, and what it does not read as
// once the promotion is over or the replay has put it back through the gate.
func TestARunReadsAsResumingItsIntegrationOnlyWhileItPromotesAgain(t *testing.T) {
	t.Parallel()

	resumed := approvedStoppedState(t)
	resumed.Status = StatusRunning
	resumed.CompletedAt = nil
	resumed.Failure = ""
	resumed.IntegrationStop = nil
	resumed.IntegrationResumptions = []IntegrationResumption{{
		Cause: CauseDirtyPrimary, Reason: "resumed", ResumedAt: resumed.UpdatedAt,
		SupersededFailure: "integrate approved change: primary checkout is not ready for integration",
	}}
	if err := resumed.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if !resumed.ResumingIntegration() {
		t.Fatalf("a resumed run at its promotion does not read as resuming: %#v", resumed)
	}
	replayed := resumed
	replayed.Phase = PhaseChecking
	replayed.ReviewDecision = ""
	replayed.ReviewSessionID = ""
	if replayed.ResumingIntegration() {
		t.Fatal("a resumed run put back through the gate still reads as resuming its integration")
	}
	landed := resumed
	landed.Status = StatusSucceeded
	completed := landed.UpdatedAt
	landed.CompletedAt = &completed
	landed.Phase = PhaseComplete
	if landed.ResumingIntegration() {
		t.Fatal("a resumed run that landed still reads as resuming its integration")
	}
	// A resumption with no account of itself is refused, exactly as a repair
	// continuation is.
	unaccounted := resumed
	unaccounted.IntegrationResumptions = []IntegrationResumption{{Cause: CauseDirtyPrimary, ResumedAt: time.Now()}}
	if err := unaccounted.Validate(); err == nil {
		t.Fatal("Validate() accepted a resumption with no reason")
	}
}

// The summary a listing reads carries both facts, so a surface that shows the
// reason shows what the reason means beside it.
func TestTheSummaryCarriesTheIntegrationStopAndTheResumption(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	stopped := approvedStoppedState(t)
	if err := store.Create(stopped); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	summary := store.summarize(stopped)
	if summary.IntegrationStop == nil || summary.IntegrationStop.Cause != CauseDirtyPrimary || summary.ResumingIntegration {
		t.Fatalf("summary = stop %#v, resuming %t; want the stop carried and the run not resuming", summary.IntegrationStop, summary.ResumingIntegration)
	}
	resumed := stopped
	resumed.Status = StatusRunning
	resumed.CompletedAt = nil
	resumed.Failure = ""
	resumed.IntegrationStop = nil
	resumed.IntegrationResumptions = []IntegrationResumption{{Cause: CauseDirtyPrimary, Reason: "resumed", ResumedAt: resumed.UpdatedAt}}
	summary = store.summarize(resumed)
	if summary.IntegrationStop != nil || !summary.ResumingIntegration {
		t.Fatalf("summary = stop %#v, resuming %t; want the resumed run read as resuming", summary.IntegrationStop, summary.ResumingIntegration)
	}
}
