package orchestrator

// What a listing says became of a run is derived from the durable record, and
// the whole derivation turns on one field: a run that ended on a blocker reads
// as stopped, and one nothing blocked does not. That is a claim about what this
// package writes, so it is checked here rather than over states a test built by
// hand — a rendering test that assigns Blocker itself would pass just as happily
// if nothing ever recorded one.
//
// So each of these drives a real ending through the pipeline and then reads the
// history back the way `yoyo status` does.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A review nobody repaired is the stoppage the operator misread as a discarded
// run. It ends on a blocker the item carries, and everything it made is still
// there, so the history has to say both.
func TestAnUnrepairedReviewIsReadBackAsStoppedWithItsWorkPreserved(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, repairVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline.Config.Execution.RepairAttemptsBeforeReplan = 1

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err == nil {
		t.Fatal("Run() error = nil, want the spent repair budget reported as what ended the run")
	}
	if !tracker.blocked || !outcome.Blocked {
		t.Fatalf("spent repair budget did not block the item: tracker = %t, outcome = %t", tracker.blocked, outcome.Blocked)
	}

	reported := onlyRecordedRun(t, store)
	// The durable status is still what it always was; what changed is the word a
	// listing says over it.
	if reported.Status != runstate.StatusFailed {
		t.Fatalf("status = %q, want the durable status untouched", reported.Status)
	}
	if reported.Outcome != runstate.OutcomeStopped {
		t.Fatalf("outcome = %q, want %q: the item carries a blocker and a person decides next",
			reported.Outcome, runstate.OutcomeStopped)
	}
	if !reported.Preserved() {
		t.Fatalf("run = %#v, want its change reported as preserved", reported)
	}
	// And the artifacts a reader is sent to are the ones the run actually made,
	// rather than a path the listing invented.
	if reported.Branch != outcome.Branch || reported.WorktreePath != outcome.WorktreePath {
		t.Fatalf("run names branch %q and worktree %q, want the run's own %q and %q",
			reported.Branch, reported.WorktreePath, outcome.Branch, outcome.WorktreePath)
	}
	if _, err := os.Stat(filepath.Join(reported.WorktreePath, "feature.txt")); err != nil {
		t.Fatalf("the listing reported preserved work that is not there: %v", err)
	}
	if reported.ProviderSessionID == "" || reported.ReviewFindings != 1 {
		t.Fatalf("run = %#v, want the preserved session and the finding recorded against it", reported)
	}
	// Selecting the runs that went wrong still finds it: the vocabulary separates
	// them within that selection rather than taking one of them out of it.
	failed, err := store.History(runstate.RunQuery{FailedOnly: true})
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if len(failed.Runs) != 1 || failed.Runs[0].Outcome != runstate.OutcomeStopped {
		t.Fatalf("failed listing = %#v, want the stoppage still selected", failed.Runs)
	}
}

// An operator stop is the other half of the same claim. Nothing judged the
// change and nothing was handed to anybody, so no blocker is recorded — which is
// exactly what keeps this run out of the stopped vocabulary while its work is
// preserved just as thoroughly.
func TestAnOperatorStopIsReadBackAsCancelledRatherThanStopped(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := roleBackend(func(request backend.RunRequest) error {
		if err := store.RecordStop(runstate.StopRequest{
			SchemaVersion: runstate.StopSchemaVersion,
			ProductID:     "yoyodyne",
			RunID:         request.RunID,
			WorkItemID:    "yoyodyne-task",
			RequestedAt:   baseTime,
			Reason:        "it is rewriting the wrong file",
		}); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider)

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err == nil {
		t.Fatal("Run() error = nil, want the stop reported as what ended the run")
	}
	if outcome.Blocked || tracker.blocked {
		t.Fatalf("an operator stop blocked the item: outcome = %t, tracker = %t", outcome.Blocked, tracker.blocked)
	}

	reported := onlyRecordedRun(t, store)
	if reported.Status != runstate.StatusCancelled {
		t.Fatalf("status = %q, want a stopped run recorded as cancelled", reported.Status)
	}
	if reported.Outcome != runstate.OutcomeCancelled {
		t.Fatalf("outcome = %q, want %q: nothing judged the change and nobody was handed anything",
			reported.Outcome, runstate.OutcomeCancelled)
	}
	// The distinction is worth nothing if a cancel is preserved less thoroughly
	// than a stoppage: the operator's question is the same over both.
	if !reported.Preserved() || reported.Branch == "" || reported.WorktreePath == "" {
		t.Fatalf("run = %#v, want the cancelled run's artifacts named and preserved", reported)
	}
	if _, err := os.Stat(filepath.Join(reported.WorktreePath, "feature.txt")); err != nil {
		t.Fatalf("the listing reported preserved work that is not there: %v", err)
	}
	// The field the whole derivation turns on: a cancel records none, which is
	// what stops it reading as work waiting on a decision nobody made.
	state, err := store.Load(reported.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if strings.TrimSpace(state.Blocker) != "" {
		t.Fatalf("an operator stop recorded a blocker: %q", state.Blocker)
	}
}

// onlyRecordedRun reads the history back the way the listing does and returns
// the single run it holds, so a test asserts over what an operator would be
// shown rather than over the state file behind it.
func onlyRecordedRun(t *testing.T, store *runstate.Store) runstate.RunSummary {
	t.Helper()
	history, err := store.History(runstate.RunQuery{})
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if len(history.Runs) != 1 {
		t.Fatalf("history = %#v, want the one run this test made", history.Runs)
	}
	return history.Runs[0]
}
