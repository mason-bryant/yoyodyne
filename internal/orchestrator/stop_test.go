package orchestrator

// What the operator's stop does to a run they did not start, and what holding
// intake does to work they did not ask for. The two are deliberately different:
// a stop ends one run and leaves its artifacts for reconciliation, and a hold on
// intake ends nothing at all.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A stop recorded while the developer is working reaches the run at its next
// provider call — the review — and ends it there. The invocation already
// streaming is not interrupted, because throwing away a generation that has been
// paid for is the cost that made killing processes the wrong verb.
func TestARunStopsAtItsNextProviderCallWhenTheOperatorAsksIt(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := roleBackend(func(request backend.RunRequest) error {
		// The operator stops the run mid-attempt, from a process that holds
		// nothing: this is the file they write beside the run.
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
	if !strings.Contains(err.Error(), "the operator stopped this run") ||
		!strings.Contains(err.Error(), "it is rewriting the wrong file") {
		t.Fatalf("Run() error = %v, want it to name the stop and the reason given", err)
	}
	if outcome.Status != runstate.StatusCancelled {
		t.Fatalf("status = %q, want a stopped run recorded as cancelled", outcome.Status)
	}
	if outcome.Integration != nil || tracker.closed {
		t.Fatalf("a stopped run promoted its work: %#v (closed=%t)", outcome.Integration, tracker.closed)
	}
	// The reviewer was never asked. Buying a verdict on a change nobody is going
	// to take is exactly what stopping at the boundary avoids.
	if reviews := countRoleRequests(provider.requests, "reviewer"); reviews != 0 {
		t.Fatalf("reviewer invocations = %d, want the stop to have landed before the review", reviews)
	}
	stopped, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !stopped.Status.Terminal() || stopped.CompletedAt == nil {
		t.Fatalf("stopped state = %#v, want a terminal run", stopped)
	}
	// The artifacts are left exactly where a run cancelled in its own process
	// leaves them, which is what lets the same reconciliation settle both.
	if stopped.WorktreeRemoved || stopped.BranchRemoved {
		t.Fatalf("stopped state = %#v, want the artifacts preserved for settling", stopped)
	}
	if _, err := os.Stat(stopped.WorktreePath); err != nil {
		t.Fatalf("the stopped run's worktree did not survive: %v", err)
	}
	if !strings.Contains(tracker.notes, "the operator stopped this run") {
		t.Fatalf("the work item was not told why it stopped: %q", tracker.notes)
	}
}

// A run whose process exited before it reached a boundary is picked up by a
// later invocation. Without this, a stop the operator asked for would be
// forgotten by exactly the restart that was meant to be safe.
func TestAStopAlreadyAskedForEndsAResumedRunRatherThanContinuingIt(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	holds := newOperatorHoldStore(t)
	// A hold is the simplest way to leave a run in flight with a process that has
	// gone: the run parks, records why, and the invocation returns.
	first := roleBackend(func(request backend.RunRequest) error {
		if _, err := holds.Hold(baseTime); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	firstPipeline := waiting(automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, first, []string{"exit 0"}), first),
		&pausingClock{now: baseTime}, 6*time.Hour, 0)
	firstPipeline.Holds = holds

	parked, err := firstPipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("parked Run() error = %v", err)
	}
	if !parked.Paused || parked.Status != runstate.StatusRunning {
		t.Fatalf("outcome = %#v, want a parked run still in flight", parked)
	}

	// The operator stops the parked run and lifts the pause. Only one of those
	// decisions is about this item, and the run has to honor that one.
	if err := store.RecordStop(runstate.StopRequest{
		SchemaVersion: runstate.StopSchemaVersion,
		ProductID:     "yoyodyne",
		RunID:         parked.RunID,
		WorkItemID:    tracker.item.ID,
		RequestedAt:   baseTime.Add(time.Hour),
		Reason:        "we are doing something else first",
	}); err != nil {
		t.Fatalf("RecordStop() error = %v", err)
	}
	if _, _, err := holds.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}

	second := roleBackend(func(backend.RunRequest) error { return nil }, approveVerdict)
	secondPipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, second, []string{"exit 0"}), second)
	secondPipeline.Holds = holds
	outcome, err := secondPipeline.Run(context.Background(), tracker.item.ID)
	if err == nil {
		t.Fatal("resumed Run() error = nil, want the stop honored")
	}
	if outcome.RunID != parked.RunID || outcome.Status != runstate.StatusCancelled {
		t.Fatalf("resumed run = %#v, want the parked run %s ended as cancelled", outcome, parked.RunID)
	}
	// Nothing was asked of the provider. A run somebody stopped must not spend
	// one more invocation on its way to being stopped.
	if len(second.requests) != 0 {
		t.Fatalf("the provider was invoked resuming a stopped run: %#v", second.requests)
	}
	if outcome.Integration != nil || tracker.closed {
		t.Fatalf("a stopped run promoted its work: %#v (closed=%t)", outcome.Integration, tracker.closed)
	}
}

// Holding intake stops the harness choosing work and nothing else. The item is
// left exactly where it was, so lifting the hold is all that stands between here
// and the work starting.
func TestHeldIntakeStartsNothingTheHarnessChose(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := roleBackend(func(backend.RunRequest) error { return nil }, approveVerdict)
	pipeline, store := newPipeline(t, repository, tracker, provider, []string{"exit 0"})
	intake := newIntakeHoldStore(t)
	pipeline.Intake = intake
	pipeline.Selection = runstate.Selection{
		By:     runstate.SelectedByDevelopmentManager,
		Reason: "the highest-priority admitted item nothing was holding back",
		At:     baseTime,
	}
	heldAt := baseTime
	if _, err := intake.Hold(runstate.IntakeHolderOperator, "the queue looks wrong", heldAt); err != nil {
		t.Fatalf("Hold() error = %v", err)
	}

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v, want a pause rather than a failure", err)
	}
	if !outcome.Paused || outcome.PausedByIntake == nil {
		t.Fatalf("outcome = %#v, want work the harness declined to choose", outcome)
	}
	if !outcome.PausedByIntake.HeldAt.Equal(heldAt.UTC()) || outcome.PausedByIntake.Reason != "the queue looks wrong" {
		t.Fatalf("intake hold = %#v, want when it was placed and why", outcome.PausedByIntake)
	}
	if outcome.RunID != "" || outcome.WorktreePath != "" {
		t.Fatalf("outcome = %#v, want nothing reserved behind a held intake", outcome)
	}
	if tracker.claimed {
		t.Fatalf("a held intake claimed the work item: calls=%v", tracker.calls)
	}
	if len(provider.requests) != 0 {
		t.Fatalf("the provider was invoked while intake was held: %#v", provider.requests)
	}
	incomplete, err := store.Incomplete()
	if err != nil {
		t.Fatalf("Incomplete() error = %v", err)
	}
	if len(incomplete) != 0 {
		t.Fatalf("runs in flight = %d, want a held intake to have reserved nothing", len(incomplete))
	}
}

// The hold is on what the harness chooses, not on the operator. An item they
// name runs while it stands, which is the distinction between this and the pause
// over everything — and it is also how they inspect the thing that worried them.
func TestHeldIntakeStillRunsAnItemTheOperatorNames(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := roleBackend(func(backend.RunRequest) error { return nil }, approveVerdict)
	pipeline, store := newPipeline(t, repository, tracker, provider, []string{"exit 0"})
	intake := newIntakeHoldStore(t)
	pipeline.Intake = intake
	pipeline.Selection = runstate.OperatorSelection("the operator ran this item by name", baseTime)
	if _, err := intake.Hold(runstate.IntakeHolderOperator, "the queue looks wrong", baseTime); err != nil {
		t.Fatalf("Hold() error = %v", err)
	}

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.PausedByIntake != nil || outcome.RunID == "" {
		t.Fatalf("outcome = %#v, want the operator's own item run despite the hold", outcome)
	}
	// The reason is recorded with the run, which is what makes a survey able to
	// say why anything is in flight rather than only that it is.
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.Selection == nil {
		t.Fatal("the run recorded nothing about why it was started")
	}
	if state.Selection.By != runstate.SelectedByOperator ||
		state.Selection.Reason != "the operator ran this item by name" {
		t.Fatalf("selection = %#v, want who chose the work and why", state.Selection)
	}
	if state.Selection.At.IsZero() {
		t.Fatalf("selection = %#v, want the moment the choice was made", state.Selection)
	}
}

// A pipeline that says nothing about why it is running an item records nothing,
// rather than a selection with an empty reason. The two read very differently to
// whoever is deciding whether to trust what the harness is doing.
func TestARunWithNoStatedSelectionRecordsNoReason(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := roleBackend(func(backend.RunRequest) error { return nil }, approveVerdict)
	pipeline, store := newPipeline(t, repository, tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.Selection != nil {
		t.Fatalf("selection = %#v, want nothing recorded rather than an empty reason", state.Selection)
	}
}

// countRoleRequests counts the invocations made of one role, so a test can say
// which provider calls a stop landed in front of.
func countRoleRequests(requests []backend.RunRequest, role string) int {
	counted := 0
	for _, request := range requests {
		if string(request.Role) == role {
			counted++
		}
	}
	return counted
}
