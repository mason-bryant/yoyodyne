package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// blockedBy is the tracker's answer once a development manager has linked one
// item behind another: the dependency exists and the work it names is not
// closed.
func blockedBy(id string) []beads.Dependency {
	return []beads.Dependency{{ID: id, Type: blocksDependency, Status: "open"}}
}

// A dependency link applied while the developer is working reaches the run it
// was applied to. This is the case the whole gate exists for, and it is the
// sequence that produced it: the link was added after the item was already
// claimed, nothing re-read it, and the run went on to spend a review round on
// work that should never have been dispatched. Now it stops at the gate instead
// — nothing is cancelled, the claim and the worktree survive, and closing what it
// waits on is what lets the same run finish.
func TestADependencyLinkedDuringARunPausesItAtTheGateAndClearingItResumesTheRun(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}

	// The link is applied from inside the developer's invocation, which is when a
	// development manager triaging the queue applies one: the work is already
	// under way, so nothing the run read before it started could have seen it.
	provider := roleBackend(func(request backend.RunRequest) error {
		if request.Role != domain.RoleDeveloper {
			return nil
		}
		tracker.item.Dependencies = blockedBy("yoyodyne-blocker")
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider)

	paused, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !paused.Paused || paused.Status != runstate.StatusRunning {
		t.Fatalf("outcome = %#v, want a paused run still in flight", paused)
	}
	if paused.PausedByDependency == nil || paused.PausedByDependency.Summary() != "yoyodyne-blocker" {
		t.Fatalf("the paused outcome does not name what it waits on: %#v", paused.PausedByDependency)
	}
	if paused.Integration != nil || tracker.closed || tracker.blocked {
		t.Fatalf("the pause promoted, closed, or blocked the work: %#v (closed=%t blocked=%t)", paused, tracker.closed, tracker.blocked)
	}
	if !tracker.claimed {
		t.Fatal("the pause gave up the claim on the work item")
	}
	// The reviewer is the round the missing gate burned, so it is the assertion
	// that says the gate is doing its job rather than merely reporting.
	if reviewed := len(provider.requestsForRole(domain.RoleReviewer)); reviewed != 0 {
		t.Fatalf("reviewer invocations = %d, want the change not judged while the item waits on other work", reviewed)
	}
	// What the item records has to name the work it waits on, so an operator
	// reading a claimed item that has gone quiet can act on it.
	for _, wanted := range []string{"yoyodyne-blocker", "paused"} {
		if !strings.Contains(tracker.notes, wanted) {
			t.Fatalf("item notes = %q, want them to mention %q", tracker.notes, wanted)
		}
	}

	pausedState, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if pausedState.Status.Terminal() {
		t.Fatalf("paused state = %#v, want a run still in flight rather than a cancelled one", pausedState)
	}
	if pausedState.DependencyPause == nil || pausedState.DependencyPause.Summary() != "yoyodyne-blocker" {
		t.Fatalf("the pause was not made durable: %#v", pausedState.DependencyPause)
	}
	if _, err := os.Stat(pausedState.WorktreePath); err != nil {
		t.Fatalf("the paused run's worktree did not survive: %v", err)
	}

	// While the item still waits, the work stays paused and nothing starts a
	// second attempt at it. This is the "replayed sequence dispatches nothing"
	// half: the link is now in place before the run starts, and it stops it.
	stillPaused, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	if !stillPaused.Paused || stillPaused.PausedByDependency == nil {
		t.Fatalf("outcome = %#v, want the work still paused for what it waits on", stillPaused)
	}
	if developed := len(provider.requestsForRole(domain.RoleDeveloper)); developed != 1 {
		t.Fatalf("developer invocations = %d, want the paused run not to have been re-developed", developed)
	}

	// Closing what it waited on is what releases the work: the same run continues
	// from the gate it stopped at and finishes.
	tracker.item.Dependencies = []beads.Dependency{{ID: "yoyodyne-blocker", Type: blocksDependency, Status: "closed"}}
	resumed, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("resumed Run() error = %v", err)
	}
	if resumed.RunID != paused.RunID {
		t.Fatalf("resumed run = %q, want the paused run %q continued rather than a new one", resumed.RunID, paused.RunID)
	}
	if resumed.Paused || resumed.Integration == nil || !tracker.closed {
		t.Fatalf("the resumed run did not finish: %#v (closed=%t)", resumed, tracker.closed)
	}
	// The gate was re-earned rather than skipped, and the developer was not asked
	// for a second attempt it did not need.
	if developed := len(provider.requestsForRole(domain.RoleDeveloper)); developed != 1 {
		t.Fatalf("developer invocations = %d, want the resumed run to continue at the gate", developed)
	}
	if reviewed := len(provider.requestsForRole(domain.RoleReviewer)); reviewed != 1 {
		t.Fatalf("reviewer invocations = %d, want the change reviewed once after the pause", reviewed)
	}
	finished, err := store.Load(resumed.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if finished.DependencyPause != nil {
		t.Fatalf("a finished run still carries a dependency pause: %#v", finished.DependencyPause)
	}
}

// The repair loop is where a run spends most of its time, so it is where a
// dependency link is as likely to arrive as a directive: the developer is being
// handed findings and asked again. A link applied in that window has to stop the
// run before it buys another round and before it promotes anything, or it would
// bind every part of the run except the longest one.
func TestADependencyLinkedDuringARepairAttemptStopsTheRunBeforeItPromotes(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}

	// The first attempt is judged as needing repair, and the link lands during the
	// repair attempt the findings bought. The reviewer would approve the next
	// change it was shown, so a run that failed to ask again here would promote
	// work a development manager had just made wait on something else.
	attempts := 0
	provider := roleBackend(func(request backend.RunRequest) error {
		if request.Role != domain.RoleDeveloper {
			return nil
		}
		attempts++
		if attempts == 2 {
			tracker.item.Dependencies = blockedBy("yoyodyne-blocker")
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, repairVerdict, approveVerdict)
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider)

	paused, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !paused.Paused || paused.PausedByDependency == nil || paused.PausedByDependency.Summary() != "yoyodyne-blocker" {
		t.Fatalf("outcome = %#v, want the run paused for the dependency linked mid-repair", paused)
	}
	if paused.Integration != nil || tracker.closed {
		t.Fatalf("the run promoted work a dependency had already paused: %#v (closed=%t)", paused, tracker.closed)
	}
	// The repair attempt already under way is not taken away from the run, and no
	// further round is bought: the pause is where the next one would have begun.
	if developed := len(provider.requestsForRole(domain.RoleDeveloper)); developed != 2 {
		t.Fatalf("developer invocations = %d, want the attempt in flight finished and no further round", developed)
	}
	if reviewed := len(provider.requestsForRole(domain.RoleReviewer)); reviewed != 1 {
		t.Fatalf("reviewer invocations = %d, want the repaired change not reviewed while paused", reviewed)
	}
	pausedState, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if pausedState.Status.Terminal() || pausedState.DependencyPause == nil {
		t.Fatalf("paused state = %#v, want a run still in flight carrying its pause", pausedState)
	}
}

// A run reconciliation walks past is left alone when it is waiting on work its
// item depends on. It is owed the rest of its gate once that work closes, so
// settling it would cancel work somebody only made wait — the same mistake
// settling a directive pause would be.
func TestReconciliationLeavesADependencyPausedRunResumable(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := roleBackend(func(request backend.RunRequest) error {
		if request.Role != domain.RoleDeveloper {
			return nil
		}
		tracker.item.Dependencies = blockedBy("yoyodyne-blocker")
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider)

	paused, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !paused.Paused || paused.PausedByDependency == nil {
		t.Fatalf("outcome = %#v, want a run paused for what its item waits on", paused)
	}

	reconciler := Reconciler{Tracker: tracker, Worktrees: newObserver(t, repository, worktreeRoot), Store: store}
	results, err := reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("reconciliations = %d, want the one paused run", len(results))
	}
	if results[0].Action != ActionResumable {
		t.Fatalf("action = %q, want the paused run left resumable rather than settled", results[0].Action)
	}
	if !strings.Contains(results[0].Detail, "yoyodyne-blocker") {
		t.Fatalf("detail = %q, want it to name the work holding the run up", results[0].Detail)
	}
	after, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if after.Status.Terminal() || after.DependencyPause == nil {
		t.Fatalf("reconciliation disturbed the paused run: %#v", after)
	}
	if _, err := os.Stat(after.WorktreePath); err != nil {
		t.Fatalf("reconciliation removed the paused run's worktree: %v", err)
	}
}
