package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A directive recorded while a developer is working reaches the run it affects.
// This is the case the whole thing exists for: the directive arrives after the
// work has started, and the run stops at its gate rather than putting a change
// through checks, review, and promotion against intent that is being rewritten.
// Nothing is cancelled by it — the claim, the branch, and the worktree all
// survive — and resolving the directive is what lets the same run finish.
func TestADirectiveRecordedDuringARunPausesItAtTheGateAndResolvingItResumesTheRun(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	directives := newDirectiveStore(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}

	// The directive is recorded from inside the developer's invocation, which is
	// exactly when an operator gives one: the work is already under way, so
	// nothing the run checked before it started could have seen this.
	var held directive.Directive
	provider := roleBackend(func(request backend.RunRequest) error {
		if request.Role != domain.RoleDeveloper {
			return nil
		}
		held = pausingDirective(t, directives, directive.KindArtifact, nil)
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider)
	pipeline.Directives = directives

	paused, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !paused.Paused || paused.Status != runstate.StatusRunning {
		t.Fatalf("outcome = %#v, want a paused run still in flight", paused)
	}
	if paused.PausedByDirective == nil || paused.PausedByDirective.ID != held.ID {
		t.Fatalf("the paused outcome does not name the directive that paused it: %#v", paused.PausedByDirective)
	}
	// The pause names what is unresolved, because a pause nobody can name a
	// reason for is a pause nobody can lift.
	if paused.PausedByDirective.Unresolved != held.Unresolved {
		t.Fatalf("unresolved = %q, want %q", paused.PausedByDirective.Unresolved, held.Unresolved)
	}
	if paused.Integration != nil || tracker.closed || tracker.blocked {
		t.Fatalf("the pause promoted, closed, or blocked the work: %#v (closed=%t blocked=%t)", paused, tracker.closed, tracker.blocked)
	}
	if !tracker.claimed {
		t.Fatal("the pause gave up the claim on the work item")
	}
	// What the item records has to say which directive and what about it, so an
	// operator reading a claimed item that has gone quiet can act on it.
	for _, wanted := range []string{held.ID, held.Unresolved, "paused"} {
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
	if pausedState.DirectivePause == nil || pausedState.DirectivePause.DirectiveID != held.ID {
		t.Fatalf("the pause was not made durable: %#v", pausedState.DirectivePause)
	}
	if _, err := os.Stat(pausedState.WorktreePath); err != nil {
		t.Fatalf("the paused run's worktree did not survive: %v", err)
	}

	// While the directive is unresolved the work stays paused, and nothing starts
	// a second attempt at it.
	stillPaused, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("second Run() error = %v", err)
	}
	if !stillPaused.Paused || stillPaused.PausedByDirective == nil {
		t.Fatalf("outcome = %#v, want the work still paused for the directive", stillPaused)
	}
	if developed := len(provider.requestsForRole(domain.RoleDeveloper)); developed != 1 {
		t.Fatalf("developer invocations = %d, want the paused run not to have been re-developed", developed)
	}

	// Settling the directive is what releases the work: the same run continues
	// from the gate it stopped at and finishes.
	if _, err := directives.Resolve(held.ID, "the goal keeps its original wording", time.Now()); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
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
	if finished.DirectivePause != nil {
		t.Fatalf("a finished run still carries a directive pause: %#v", finished.DirectivePause)
	}
}

// A directive that is already unresolved stops work before it starts. Nothing is
// claimed, no worktree is made, and no provider is invoked: the point of pausing
// is that the work is not done, and doing it and then reporting a pause would be
// exactly the failure of recording a directive that enforces nothing.
func TestADirectiveStopsWorkFromStartingAtAll(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	directives := newDirectiveStore(t)
	held := pausingDirective(t, directives, directive.KindAmbiguous, nil)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := roleBackend(func(backend.RunRequest) error { return nil }, approveVerdict)
	pipeline, _ := newPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline.Directives = directives

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !outcome.Paused || outcome.PausedByDirective == nil || outcome.PausedByDirective.ID != held.ID {
		t.Fatalf("outcome = %#v, want the item paused for the recorded directive", outcome)
	}
	if outcome.RunID != "" || outcome.WorktreePath != "" {
		t.Fatalf("outcome = %#v, want nothing started for a paused item", outcome)
	}
	if tracker.claimed {
		t.Fatal("the item was claimed for work the directive paused")
	}
	if invoked := len(provider.requests); invoked != 0 {
		t.Fatalf("provider invocations = %d, want none", invoked)
	}
}

// A scoped directive reaches the work it named and nothing else. An operator who
// knows which items a change of mind touches must be able to say so, or every
// directive would stop the whole product.
func TestAScopedDirectivePausesOnlyTheWorkItNames(t *testing.T) {
	t.Parallel()

	repository, _, _ := restartableFixture(t)
	directives := newDirectiveStore(t)
	pausingDirective(t, directives, directive.KindAmbiguous, []string{"yoyodyne-other"})
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := roleBackend(func(request backend.RunRequest) error {
		if request.Role != domain.RoleDeveloper {
			return nil
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, _ := newPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline = automatic(pipeline, provider)
	pipeline.Directives = directives

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Paused || outcome.Integration == nil {
		t.Fatalf("outcome = %#v, want an unaffected item to run to completion", outcome)
	}
}

// A resolved directive constrains nothing. Settling one is exactly what lets the
// work it stopped carry on, so a resolved record must never keep pausing.
func TestAResolvedDirectiveStopsNothing(t *testing.T) {
	t.Parallel()

	repository, _, _ := restartableFixture(t)
	directives := newDirectiveStore(t)
	held := pausingDirective(t, directives, directive.KindArtifact, nil)
	if _, err := directives.Resolve(held.ID, "the design keeps its current shape", time.Now()); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := roleBackend(func(request backend.RunRequest) error {
		if request.Role != domain.RoleDeveloper {
			return nil
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, _ := newPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline = automatic(pipeline, provider)
	pipeline.Directives = directives

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Paused || outcome.Integration == nil {
		t.Fatalf("outcome = %#v, want a resolved directive to stop nothing", outcome)
	}
}

// An operational directive is in effect from the moment it is recorded and stops
// nothing. Pausing every run for every instruction the operator gives would make
// the mechanism unusable, which is why only two kinds pause.
func TestAnOperationalDirectivePausesNothing(t *testing.T) {
	t.Parallel()

	repository, _, _ := restartableFixture(t)
	directives := newDirectiveStore(t)
	recorded := directive.Directive{
		SchemaVersion: directive.SchemaVersion,
		ID:            newDirectiveID(t),
		ProductID:     "yoyodyne",
		Kind:          directive.KindOperational,
		ReceivedBy:    domain.RoleProductManager,
		ReceivedAt:    baseTime,
		Text:          "prefer table-driven tests in this repository",
	}
	if err := directives.Record(recorded); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := roleBackend(func(request backend.RunRequest) error {
		if request.Role != domain.RoleDeveloper {
			return nil
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, _ := newPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline = automatic(pipeline, provider)
	pipeline.Directives = directives

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Paused || outcome.Integration == nil {
		t.Fatalf("outcome = %#v, want an operational directive to pause nothing", outcome)
	}
}

// Reconciliation leaves a directive-paused run exactly where it is. Settling one
// here would cancel work the operator only paused, and the whole constraint on
// pausing is that it is not a cancellation.
func TestReconciliationLeavesADirectivePausedRunResumable(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	directives := newDirectiveStore(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	// Recorded while the developer works, so the run reaches the gate with a
	// worktree and a claim to leave behind rather than never starting.
	var held directive.Directive
	provider := roleBackend(func(request backend.RunRequest) error {
		if request.Role != domain.RoleDeveloper {
			return nil
		}
		held = pausingDirective(t, directives, directive.KindAmbiguous, nil)
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider)
	pipeline.Directives = directives

	paused, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !paused.Paused {
		t.Fatalf("outcome = %#v, want a paused run to reconcile", paused)
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
	if !strings.Contains(results[0].Detail, held.ID) {
		t.Fatalf("detail = %q, want it to name the directive holding the run up", results[0].Detail)
	}
	after, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if after.Status.Terminal() || after.DirectivePause == nil {
		t.Fatalf("reconciliation disturbed the paused run: %#v", after)
	}
	if _, err := os.Stat(after.WorktreePath); err != nil {
		t.Fatalf("reconciliation removed the paused run's worktree: %v", err)
	}
}

// A pipeline with nowhere to read directives from must refuse to run. A harness
// that cannot find out what has been directed looks exactly like one that has
// been directed nothing, and acting on that reading is the failure enforcement
// exists to prevent.
func TestAPipelineWithNoDirectiveRecordRefusesToRun(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := roleBackend(func(backend.RunRequest) error { return nil }, approveVerdict)
	pipeline, _ := newPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline.Directives = nil

	if _, err := pipeline.Run(context.Background(), tracker.item.ID); err == nil ||
		!strings.Contains(err.Error(), "durable user directives are required") {
		t.Fatalf("Run() error = %v, want a refusal naming the missing directive record", err)
	}
}

// pausingDirective records one directive of a kind that pauses work and returns
// it, so a test can name what it expects the run to stop for.
func pausingDirective(t *testing.T, store *runstate.DirectiveStore, kind directive.Kind, scope []string) directive.Directive {
	t.Helper()
	recorded := directive.Directive{
		SchemaVersion: directive.SchemaVersion,
		ID:            newDirectiveID(t),
		ProductID:     "yoyodyne",
		Kind:          kind,
		ReceivedBy:    domain.RoleProductManager,
		ReceivedAt:    baseTime,
		Text:          "the goal this serves is changing",
		Unresolved:    "whether the goal still covers this work",
		Scope:         scope,
	}
	if kind == directive.KindArtifact {
		recorded.Artifact = "docs/product/goals/v1-goals.md"
	}
	if err := store.Record(recorded); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	return recorded
}

func newDirectiveID(t *testing.T) string {
	t.Helper()
	id, err := directive.NewID()
	if err != nil {
		t.Fatalf("directive.NewID() error = %v", err)
	}
	return id
}
