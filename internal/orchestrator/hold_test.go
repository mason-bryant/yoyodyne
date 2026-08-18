package orchestrator

// What the operator's pause does to a run: it starts nothing, it parks what is
// already under way at the next provider call, and it costs the run nothing it
// had already earned.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A held harness starts nothing. Nothing is claimed, no worktree is made, and
// the provider is not asked so much as whether it is installed — which is the
// whole point of pausing rather than letting each boundary discover it.
func TestRunStartsNothingWhileTheOperatorHoldsActivity(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := roleBackend(func(backend.RunRequest) error { return nil }, approveVerdict)
	pipeline, store := newPipeline(t, repository, tracker, provider, []string{"exit 0"})
	holds := newOperatorHoldStore(t)
	pipeline.Holds = holds
	heldAt := baseTime
	if _, err := holds.Hold(heldAt); err != nil {
		t.Fatalf("Hold() error = %v", err)
	}

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v, want a pause rather than a failure", err)
	}
	if !outcome.Paused || outcome.PausedByOperator == nil {
		t.Fatalf("outcome = %#v, want work paused by the operator's hold", outcome)
	}
	if !outcome.PausedByOperator.HeldAt.Equal(heldAt.UTC()) {
		t.Fatalf("held at = %s, want the moment the operator paused (%s)", outcome.PausedByOperator.HeldAt, heldAt.UTC())
	}
	if outcome.PauseCause != runstate.PauseOperatorHold {
		t.Fatalf("pause cause = %q, want %q", outcome.PauseCause, runstate.PauseOperatorHold)
	}
	// Nothing was started, so there is nothing to resume, reconcile, or clean up.
	if outcome.RunID != "" || outcome.WorktreePath != "" {
		t.Fatalf("outcome = %#v, want no run and no worktree behind the pause", outcome)
	}
	if tracker.claimed || len(tracker.calls) > 1 {
		t.Fatalf("the tracker was disturbed by a paused harness: claimed=%t calls=%v", tracker.claimed, tracker.calls)
	}
	if len(provider.requests) != 0 {
		t.Fatalf("the provider was invoked while activity was paused: %#v", provider.requests)
	}
	incomplete, err := store.Incomplete()
	if err != nil {
		t.Fatalf("Incomplete() error = %v", err)
	}
	if len(incomplete) != 0 {
		t.Fatalf("runs in flight = %d, want a paused harness to have reserved nothing", len(incomplete))
	}
}

// A hold placed while a developer is working reaches that run at its next
// provider call — the review — and the run carries on by itself the moment the
// operator lifts it. Nothing about the change the developer already made is
// asked for again.
func TestARunParksAtItsNextProviderCallAndCarriesOnWhenTheHoldLifts(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	holds := newOperatorHoldStore(t)
	// The operator pauses while the developer is mid-attempt, which is the case
	// the boundary exists for: the invocation already streaming is not interrupted.
	provider := roleBackend(func(request backend.RunRequest) error {
		if _, err := holds.Hold(baseTime); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	clock := &pausingClock{now: baseTime}
	pipeline := waiting(automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider),
		clock, 6*time.Hour, time.Minute)
	pipeline.Holds = holds

	var parked runstate.State
	// The operator lifts the hold while the run is asleep on it, and what durable
	// state said at that moment is what proves the park was recorded before any
	// waiting started.
	clock.onSleep = func() {
		if _, _, err := holds.Release(); err != nil {
			t.Errorf("Release() error = %v", err)
		}
		states, err := store.Incomplete()
		if err != nil {
			t.Errorf("Incomplete() error = %v", err)
			return
		}
		if len(states) == 1 {
			parked = states[0]
		}
	}

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if parked.OperatorHeldSince == nil || !parked.OperatorHeldSince.Equal(baseTime.UTC()) {
		t.Fatalf("parked state = %#v, want the hold durable before the wait began", parked.OperatorHeldSince)
	}
	if parked.PauseCause != runstate.PauseOperatorHold || parked.Status.Terminal() {
		t.Fatalf("parked state = %#v, want a non-terminal run parked on the operator's hold", parked)
	}
	// The developer's work is untouched by the park: its session, its branch, and
	// its worktree are all still there for the review the run goes on to make.
	if parked.WorktreePath == "" || parked.Branch == "" || parked.ProviderSessionID != provider.developerSession {
		t.Fatalf("the park did not preserve the run's artifacts or session: %#v", parked)
	}
	if clock.longestSlice() > operatorHoldProbe {
		t.Fatalf("longest single sleep = %s, want no longer than the %s hold probe", clock.longestSlice(), operatorHoldProbe)
	}
	// Lifting the hold is all it took: nobody restarted anything.
	if outcome.Integration == nil || !tracker.closed || tracker.blocked {
		t.Fatalf("the run did not carry on to completion: %#v (blocked=%t)", outcome, tracker.blocked)
	}
	if outcome.Paused || outcome.PausedByOperator != nil {
		t.Fatalf("a run that finished still reports itself held: %#v", outcome)
	}
	finished, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if finished.OperatorHeldSince != nil || finished.PauseCause != "" {
		t.Fatalf("a finished run still carries a park: %#v", finished)
	}
	// The time the hold cost is accounted under its own kind, and never against
	// what a provider refusal is allowed to spend.
	if finished.OperatorHeld() != clock.waited() {
		t.Fatalf("operator-held time = %s, want the %s actually waited", finished.OperatorHeld(), clock.waited())
	}
	if finished.UsageLimitPausedSeconds != 0 {
		t.Fatalf("the hold was charged to the provider's pause budget: %ds", finished.UsageLimitPausedSeconds)
	}
}

// A hold outlasting what this process will stay open for exits with the run
// still in flight, and a later invocation picks it up once the operator has
// lifted it. Nothing is cleaned up in between: the item stays claimed and the
// artifacts stay put.
func TestARunParkedOnAHoldExitsResumableAndIsPickedUpWhenItLifts(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	holds := newOperatorHoldStore(t)
	first := roleBackend(func(request backend.RunRequest) error {
		if _, err := holds.Hold(baseTime); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	firstClock := &pausingClock{now: baseTime}
	// No time at all is spent holding this process open, so the park is durable
	// and the run is left for a later invocation.
	firstPipeline := waiting(automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, first, []string{"exit 0"}), first),
		firstClock, 6*time.Hour, 0)
	firstPipeline.Holds = holds

	paused, err := firstPipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("parked Run() error = %v", err)
	}
	if !paused.Paused || paused.Status != runstate.StatusRunning || paused.PausedByOperator == nil {
		t.Fatalf("outcome = %#v, want a parked run still in flight", paused)
	}
	if len(firstClock.slept) != 0 {
		t.Fatalf("waits = %v, want a run that exited rather than holding the process open", firstClock.slept)
	}
	if tracker.blocked || tracker.closed || !tracker.claimed {
		t.Fatalf("the park disturbed the work item: blocked=%t closed=%t claimed=%t", tracker.blocked, tracker.closed, tracker.claimed)
	}
	parked, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if parked.Status.Terminal() || parked.OperatorHeldSince == nil {
		t.Fatalf("parked state = %#v, want a non-terminal run carrying its hold", parked)
	}
	if _, err := os.Stat(parked.WorktreePath); err != nil {
		t.Fatalf("the parked run's worktree did not survive: %v", err)
	}

	// A second invocation while the hold still stands leaves the run exactly
	// where it is rather than resuming it into a boundary that would park again.
	duringHold := roleBackend(func(backend.RunRequest) error { return nil }, approveVerdict)
	duringPipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, duringHold, []string{"exit 0"}), duringHold)
	duringPipeline.Holds = holds
	stillHeld, err := duringPipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() during the hold error = %v", err)
	}
	if !stillHeld.Paused || stillHeld.PausedByOperator == nil || len(duringHold.requests) != 0 {
		t.Fatalf("an invocation during the hold did not leave the run alone: %#v %#v", stillHeld, duringHold.requests)
	}

	// Once the operator resumes, the same run is picked up and finished.
	if _, _, err := holds.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	second := roleBackend(func(backend.RunRequest) error { return nil }, approveVerdict)
	secondClock := &pausingClock{now: baseTime.Add(2 * time.Hour)}
	secondPipeline := waiting(automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, second, []string{"exit 0"}), second),
		secondClock, 6*time.Hour, time.Minute)
	secondPipeline.Holds = holds
	outcome, err := secondPipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("resumed Run() error = %v", err)
	}
	if outcome.RunID != paused.RunID || outcome.WorktreePath != paused.WorktreePath {
		t.Fatalf("resumed run = %#v, want the parked run %s in %s", outcome, paused.RunID, paused.WorktreePath)
	}
	if len(secondClock.slept) != 0 {
		t.Fatalf("waits = %v, want no wait once the hold is lifted", secondClock.slept)
	}
	if outcome.Integration == nil || !tracker.closed || tracker.blocked {
		t.Fatalf("the resumed run did not complete normally: %#v (blocked=%t)", outcome, tracker.blocked)
	}
	if claims := countCalls(tracker.calls, "claim"); claims != 1 {
		t.Fatalf("claims = %d, want the item claimed once across the hold", claims)
	}
	finished, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// The whole span the operator held it is accounted, including the part no
	// process was awake for: what the ledger is answering is why time passed.
	if finished.OperatorHeld() != 2*time.Hour {
		t.Fatalf("operator-held time = %s, want the two hours the hold stood", finished.OperatorHeld())
	}
	if finished.OperatorHeldSince != nil {
		t.Fatalf("a finished run still carries a park: %#v", finished.OperatorHeldSince)
	}
}

// Reconciliation settles what a killed process left behind, and a run the
// operator parked is not that. Settling it would turn their pause into exactly
// the cancelled run with a claimed item that pausing exists to avoid.
func TestReconcileLeavesARunParkedOnAHoldAlone(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	holds := newOperatorHoldStore(t)
	provider := roleBackend(func(request backend.RunRequest) error {
		if _, err := holds.Hold(baseTime); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline := waiting(automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider),
		&pausingClock{now: baseTime}, 6*time.Hour, 0)
	pipeline.Holds = holds
	parked, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil || !parked.Paused {
		t.Fatalf("Run() error = %v, paused = %t", err, parked.Paused)
	}
	before, err := store.Load(parked.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	results := reconcileSweep(t, repository, worktreeRoot, store, tracker)
	if len(results) != 1 || results[0].Action != ActionResumable {
		t.Fatalf("reconciliation = %#v, want the parked run left resumable", results)
	}
	if !strings.Contains(results[0].Detail, "the operator paused all harness activity") {
		t.Fatalf("reconciliation did not report why the run is parked: %q", results[0].Detail)
	}
	after, err := store.Load(parked.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if after.Status != before.Status || after.Phase != before.Phase || after.OperatorHeldSince == nil {
		t.Fatalf("reconciliation disturbed a parked run: %#v", after)
	}
	if tracker.blocked || tracker.closed {
		t.Fatalf("reconciliation acted on the item of a parked run: blocked=%t closed=%t", tracker.blocked, tracker.closed)
	}
}

// A hold nobody can read is not a harness that may spend. It stops the run
// rather than being treated as an absence, for the reason an unreadable
// directive does: proceeding on that reading is the whole failure the switch
// exists to prevent.
func TestAHoldThatCannotBeReadStopsTheRun(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := roleBackend(func(backend.RunRequest) error { return nil }, approveVerdict)
	pipeline, _ := newPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline.Holds = unreadableHolds{}

	if _, err := pipeline.Run(context.Background(), tracker.item.ID); err == nil {
		t.Fatal("Run() succeeded over a hold it could not read")
	}
	if len(provider.requests) != 0 || tracker.claimed {
		t.Fatalf("a run spent against a hold it could not read: %#v claimed=%t", provider.requests, tracker.claimed)
	}
}

type unreadableHolds struct{}

func (unreadableHolds) Held() (runstate.OperatorHold, bool, error) {
	return runstate.OperatorHold{}, false, errors.New("the operator hold is unreadable")
}

// The store is built where every product on the machine reads it: one file at
// the state root rather than one per product, because one switch pauses the
// machine.
func TestTheHoldIsOneSwitchAtTheStateRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	holds, err := runstate.NewOperatorHoldStore(root)
	if err != nil {
		t.Fatalf("NewOperatorHoldStore() error = %v", err)
	}
	if _, err := holds.Hold(baseTime); err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "operator-hold.json")); err != nil {
		t.Fatalf("the hold was not recorded at the state root: %v", err)
	}
}
