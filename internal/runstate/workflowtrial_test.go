package runstate

// What a run record may say about the workflow instance it is observed through.
//
// The two fields are an observation rather than anything the run depends on, so
// almost nothing is refused here — a run that names no instance is the ordinary
// run and stays valid forever. What is refused is a record that could not have
// been written by an executor: an instance identifier nothing could be stored
// under, and a divergence with no instance to have diverged from, which would
// report an observation about a run nothing observed.

import "testing"

// What the trial observed is on the summary as well as on the record, because
// the soak is counted off it and every surface reads the summary. A field the
// record holds and the projection drops is a soak nobody can count without
// opening the state directory.
func TestTheSummaryCarriesWhatTheTrialObserved(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	observed := testState(t, StatusSucceeded)
	observed.WorkflowInstanceID = observed.RunID + "-delivery"
	observed.WorkflowDivergence = `the run performed "check" and its instance stands in "review"`
	if err := store.Create(observed); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	history, err := store.History(RunQuery{WorkItemID: observed.WorkItemID})
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if len(history.Runs) != 1 {
		t.Fatalf("history selected %d runs, want the one that was recorded", len(history.Runs))
	}
	summary := history.Runs[0]
	if summary.WorkflowInstanceID != observed.WorkflowInstanceID {
		t.Errorf("summary instance = %q, want %q", summary.WorkflowInstanceID, observed.WorkflowInstanceID)
	}
	if summary.WorkflowDivergence != observed.WorkflowDivergence {
		t.Errorf("summary divergence = %q, want %q", summary.WorkflowDivergence, observed.WorkflowDivergence)
	}
}

func TestARunThatWasNeverObservedIsStillAValidRecord(t *testing.T) {
	t.Parallel()

	state := testState(t, StatusSucceeded)
	if state.WorkflowInstanceID != "" || state.WorkflowDivergence != "" {
		t.Fatalf("a record nobody observed already names an instance (%q, %q)", state.WorkflowInstanceID, state.WorkflowDivergence)
	}
	if err := state.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestAnObservedRunRecordsTheInstanceAndWhatItDiverged(t *testing.T) {
	t.Parallel()

	state := testState(t, StatusRunning)
	state.WorkflowInstanceID = state.RunID + "-delivery"
	state.WorkflowDivergence = "the run performed \"check\" and its instance stands in \"review\""
	if err := state.Validate(); err != nil {
		t.Fatalf("Validate() error = %v; a run in the trial records both", err)
	}
}

func TestAWorkflowObservationIsHeldToWhatCouldHaveBeenWritten(t *testing.T) {
	t.Parallel()

	for name, corrupt := range map[string]func(*State){
		"an instance identifier nothing could be stored under": func(s *State) {
			s.WorkflowInstanceID = "Delivery Trial"
		},
		"a divergence with no instance to have diverged from": func(s *State) {
			s.WorkflowDivergence = "the definition and the run are no longer in the same place"
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			state := testState(t, StatusRunning)
			corrupt(&state)
			if err := state.Validate(); err == nil {
				t.Fatal("Validate() accepted a record that could not have been written")
			}
		})
	}
}
