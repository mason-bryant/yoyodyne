package runstate

import (
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// The question this answers is "what has been failing?", so the runs that ended
// without succeeding are selectable on their own, newest first, priced from the
// same evidence the ledger uses.
func TestHistoryReportsFailedRunsNewestFirstWithTheirReasons(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	succeeded := testState(t, StatusSucceeded)
	succeeded.WorkItemID = "yoyodyne-ifd.41"
	succeeded.Phase = PhaseComplete
	succeeded.ProviderSessionID = "session-developer"
	if err := store.Create(succeeded); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	appendCostEvents(t, store, succeeded.RunID, 1, execution.EventRunCompleted, 19.0)

	failed := testState(t, StatusFailed)
	failed.WorkItemID = "yoyodyne-ifd.2.7"
	failed.Phase = PhaseChecking
	failed.StartedAt = succeeded.StartedAt.Add(time.Hour)
	failed.UpdatedAt = failed.StartedAt
	completedAt := failed.StartedAt
	failed.CompletedAt = &completedAt
	failed.Failure = "verification failed: make test exited with 2"
	failed.CheckFailure = &CheckFailure{Command: "make test", ExitCode: 2, Output: "FAIL"}
	failed.ProviderSessionID = "session-developer-2"
	if err := store.Create(failed); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	appendCostEvents(t, store, failed.RunID, 1, execution.EventRunFailed, 8.91)

	cancelled := testState(t, StatusCancelled)
	cancelled.WorkItemID = "yoyodyne-ifd.2.7"
	cancelled.Phase = PhaseDeveloping
	cancelled.StartedAt = succeeded.StartedAt.Add(-time.Hour)
	cancelled.UpdatedAt = cancelled.StartedAt
	cancelledAt := cancelled.StartedAt
	cancelled.CompletedAt = &cancelledAt
	cancelled.Failure = "context canceled"
	if err := store.Create(cancelled); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	history, err := store.History(RunQuery{FailedOnly: true})
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if history.Recorded != 3 || history.Matched != 2 {
		t.Fatalf("history = %#v, want 2 of 3 recorded runs selected", history)
	}
	// A cancelled run did not land its work either, and it records why, so it
	// belongs beside the failed one rather than among the successes.
	if history.Runs[0].RunID != failed.RunID || history.Runs[1].RunID != cancelled.RunID {
		t.Fatalf("history order = %q, %q; want newest first", history.Runs[0].RunID, history.Runs[1].RunID)
	}
	reported := history.Runs[0]
	if !reported.Failed() || reported.Failure != failed.Failure || reported.Phase != PhaseChecking {
		t.Fatalf("failed run = %#v", reported)
	}
	if reported.FailingCheck == nil || reported.FailingCheck.Command != "make test" || reported.FailingCheck.ExitCode != 2 {
		t.Fatalf("failing check = %#v", reported.FailingCheck)
	}
	// The failed attempt spent real money and is priced from its own log, exactly
	// as the ledger prices it.
	if !reported.CostKnown() || reported.CostUSD != 8.91 || reported.Invocations != 1 {
		t.Fatalf("failed run price = %#v", reported)
	}
}

// The bookkeeping failures are recorded apart from the run's own so that neither
// can be read as the other, and a listing that ran them together would undo
// that. An integrated run carrying an outstanding cleanup is the case: the work
// landed, and only the janitorial step is unfinished.
func TestHistoryKeepsBookkeepingFailuresApartFromTheRunsOwn(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := integratedState(t, PhaseCleaningUp)
	state.CleanupFailure = "remove worktree: directory is busy"
	state.PublishFailure = "push branch: remote rejected"
	state.CompletionRecordingFailure = "save completed run state after cleanup: disk full"
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	history, err := store.History(RunQuery{})
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if len(history.Runs) != 1 {
		t.Fatalf("history = %#v", history)
	}
	reported := history.Runs[0]
	if reported.Failed() || reported.Failure != "" {
		t.Fatalf("an integrated run with unfinished cleanup reads as failed: %#v", reported)
	}
	if reported.CleanupFailure != state.CleanupFailure || reported.PublishFailure != state.PublishFailure ||
		reported.CompletionRecordingFailure != state.CompletionRecordingFailure {
		t.Fatalf("bookkeeping failures = %#v", reported)
	}
	// Cleanup that did not finish is exactly what still owes somebody a step.
	if !reported.Integrated || !reported.Outstanding {
		t.Fatalf("reported = %#v, want an integrated run that is still outstanding", reported)
	}
	// A merge the forge has not performed is the other thing a finished run can
	// owe, and it is carried so a reader can tell the two apart rather than only
	// being told that something is owed.
	queued := integratedState(t, PhaseComplete)
	queued.RunID = mustRunID(t)
	queued.WorktreeRemoved = true
	queued.BranchRemoved = true
	queued.PullRequest = &PullRequest{
		Remote:      "origin",
		Branch:      queued.Branch,
		Number:      73,
		URL:         "https://forge.example/pull/73",
		HeadCommit:  queued.Integration.SourceCommit,
		MergeQueued: true,
	}
	if err := store.Create(queued); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	waiting, err := store.History(RunQuery{WorkItemID: queued.WorkItemID})
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if len(waiting.Runs) != 1 {
		t.Fatalf("history = %#v", waiting)
	}
	if !waiting.Runs[0].MergeQueued || !waiting.Runs[0].Outstanding {
		t.Fatalf("queued run = %#v, want an outstanding run waiting on its queued merge", waiting.Runs[0])
	}

	// Selecting the runs that went wrong must not sweep either of them up:
	// nothing about their work failed.
	failed, err := store.History(RunQuery{FailedOnly: true})
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if failed.Matched != 0 || len(failed.Runs) != 0 {
		t.Fatalf("failed history = %#v", failed)
	}
}

// A limited listing has to say what it was limited from, or it reads as the
// whole record. One item's runs are selectable the same way.
func TestHistoryLimitsWhatItReportsWithoutHidingWhatItSelected(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	started := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	for index := 0; index < 4; index++ {
		state := testState(t, StatusFailed)
		state.WorkItemID = "yoyodyne-ifd.2.7"
		if index == 3 {
			state.WorkItemID = "yoyodyne-ifd.41"
		}
		state.StartedAt = started.Add(time.Duration(index) * time.Hour)
		state.UpdatedAt = state.StartedAt
		completedAt := state.StartedAt
		state.CompletedAt = &completedAt
		state.Failure = "developer reported failure: api_error"
		if err := store.Create(state); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
	}

	history, err := store.History(RunQuery{Limit: 2})
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if len(history.Runs) != 2 || history.Matched != 4 || history.Recorded != 4 {
		t.Fatalf("history = %#v, want 2 of 4 shown", history)
	}
	if !history.Runs[0].StartedAt.After(history.Runs[1].StartedAt) {
		t.Fatalf("history order = %v, %v; want newest first", history.Runs[0].StartedAt, history.Runs[1].StartedAt)
	}

	item, err := store.History(RunQuery{WorkItemID: "yoyodyne-ifd.41"})
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if item.Matched != 1 || len(item.Runs) != 1 || item.Runs[0].WorkItemID != "yoyodyne-ifd.41" {
		t.Fatalf("item history = %#v", item)
	}
	// Reading one item's runs must still say how much record it was read out of.
	if item.Recorded != 4 {
		t.Fatalf("item history recorded = %d, want 4", item.Recorded)
	}
}

// A run whose event log is gone is reported as unpriceable rather than as free,
// for the reason the ledger does it: a zero meaning "no record" corrupts every
// figure it is read into.
func TestHistoryReportsARunWithNoSurvivingLogAsUnpriced(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	lost := testState(t, StatusFailed)
	lost.Failure = "developer backend failed"
	lost.ProviderSessionID = "session-developer"
	if err := store.Create(lost); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	history, err := store.History(RunQuery{})
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if len(history.Runs) != 1 {
		t.Fatalf("history = %#v", history)
	}
	if history.Runs[0].CostKnown() || history.Runs[0].CostUSD != 0 {
		t.Fatalf("run = %#v, want an unpriced run", history.Runs[0])
	}
}
