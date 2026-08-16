package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"yoyodyne/internal/backend"
	"yoyodyne/internal/beads"
	"yoyodyne/internal/execution"
	"yoyodyne/internal/gitworktree"
	"yoyodyne/internal/runstate"
)

// TestReconcileSettlesAnInterruptionAtEveryPhaseBoundary drives a real run to
// each boundary and then kills its persistence, which is what a killed process
// leaves behind: the last state it managed to write, with no terminal record
// after it. Every one of those states has to reconcile to a correct terminal
// state or to an explicit blocker, and never by restarting a developer.
func TestReconcileSettlesAnInterruptionAtEveryPhaseBoundary(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		// haltAt and haltAfter choose the boundary: writes stop for good once
		// the run reaches haltAt, after haltAfter writes there were allowed.
		haltAt    runstate.Phase
		haltAfter int
		// wantInterrupted is the phase that survives on disk.
		wantInterrupted runstate.Phase
		wantAction      ReconcileAction
		// wantPromoted reports whether the interrupted run had already moved
		// the target branch before it died.
		wantPromoted bool
	}{
		{
			name: "interrupted while developing", haltAt: runstate.PhaseChecking,
			wantInterrupted: runstate.PhaseDeveloping, wantAction: ActionBlocked,
		},
		{
			name: "interrupted after checks and before review", haltAt: runstate.PhaseReviewing,
			wantInterrupted: runstate.PhaseChecking, wantAction: ActionBlocked,
		},
		{
			name: "interrupted while reviewing", haltAt: runstate.PhaseIntegrating,
			wantInterrupted: runstate.PhaseReviewing, wantAction: ActionBlocked,
		},
		// The promotion landed but nothing recorded it. Only the repository can
		// say so, and reconciliation has to believe it rather than block work
		// that is already in the target branch.
		{
			name: "interrupted inside integration", haltAt: runstate.PhaseCompleting,
			wantInterrupted: runstate.PhaseIntegrating, wantAction: ActionCompleted, wantPromoted: true,
		},
		{
			name: "interrupted between integration and cleanup", haltAt: runstate.PhaseCleaningUp,
			wantInterrupted: runstate.PhaseCompleting, wantAction: ActionCompleted, wantPromoted: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository, worktreeRoot, store := restartableFixture(t)
			tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
			provider := roleBackend(func(request backend.RunRequest) error {
				return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
			}, approveVerdict)
			halting := &haltingStore{StateStore: store, at: test.haltAt, after: test.haltAfter}
			pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, halting, tracker, provider, []string{"exit 0"}), provider)

			if _, err := pipeline.Run(context.Background(), tracker.item.ID); err == nil || !halting.halted {
				t.Fatalf("interrupted Run() error = %v, halted = %t", err, halting.halted)
			}
			interrupted, err := store.Load(pipelineRunID)
			if err != nil {
				t.Fatalf("Load() interrupted state error = %v", err)
			}
			if interrupted.Status.Terminal() || interrupted.Phase != test.wantInterrupted {
				t.Fatalf("interrupted state = %#v, want a live run in phase %q", interrupted, test.wantInterrupted)
			}
			base := gitLine(t, repository, "rev-parse", "refs/heads/main")
			if promoted := base != interrupted.BaseCommit; promoted != test.wantPromoted {
				t.Fatalf("main promoted = %t, want %t", promoted, test.wantPromoted)
			}
			providerRequests := len(provider.requests)

			results := reconcileSweep(t, repository, worktreeRoot, store, tracker)
			if len(results) != 1 || results[0].RunID != interrupted.RunID {
				t.Fatalf("reconciliation = %#v, want the interrupted run", results)
			}
			if results[0].Action != test.wantAction || results[0].Failure != "" {
				t.Fatalf("reconciliation = %#v, want action %q", results[0], test.wantAction)
			}
			// The whole point of reconciliation is that a lost process handle
			// never buys the item a second developer.
			if len(provider.requests) != providerRequests {
				t.Fatalf("reconciliation issued %d provider request(s)", len(provider.requests)-providerRequests)
			}

			settled, err := store.Load(interrupted.RunID)
			if err != nil {
				t.Fatalf("Load() settled state error = %v", err)
			}
			if !settled.Status.Terminal() || settled.CompletedAt == nil {
				t.Fatalf("settled state = %#v, want a terminal run", settled)
			}
			if test.wantAction == ActionBlocked {
				assertBlockedAndPreserved(t, repository, tracker, settled)
			} else {
				assertCompletedAndCleaned(t, repository, tracker, settled)
			}

			// Reconciling again settles nothing, because nothing is left
			// outstanding: a second sweep must not re-close or re-block an item.
			calls := len(tracker.calls)
			if again := reconcileSweep(t, repository, worktreeRoot, store, tracker); len(again) != 0 {
				t.Fatalf("second reconciliation = %#v, want nothing outstanding", again)
			}
			if len(tracker.calls) != calls {
				t.Fatalf("second reconciliation made %v", tracker.calls[calls:])
			}
		})
	}
}

// assertBlockedAndPreserved checks the half of the contract that applies to a
// run nothing could finish: the item carries a blocker, the work survives, and
// nothing was promoted.
func assertBlockedAndPreserved(t *testing.T, repository string, tracker *fakeTracker, settled runstate.State) {
	t.Helper()
	if settled.Status != runstate.StatusFailed || settled.Integration != nil {
		t.Fatalf("settled state = %#v, want a failed run with nothing integrated", settled)
	}
	if !tracker.blocked || tracker.closed {
		t.Fatalf("tracker = %#v, want a blocked and unclosed item", tracker)
	}
	if !strings.Contains(tracker.blockReason, "No developer was restarted") ||
		!strings.Contains(tracker.blockReason, "Preserved worktree: "+settled.WorktreePath) ||
		!strings.Contains(tracker.blockReason, "Preserved branch: "+settled.Branch) {
		t.Fatalf("blocker reason = %q", tracker.blockReason)
	}
	if _, err := os.Stat(filepath.Join(settled.WorktreePath, "feature.txt")); err != nil {
		t.Fatalf("blocked run lost its preserved change: %v", err)
	}
	if branches := gitOutput(t, repository, "branch", "--list", settled.Branch); strings.TrimSpace(branches) == "" {
		t.Fatal("blocked run lost its preserved branch")
	}
	if head := gitLine(t, repository, "rev-parse", "refs/heads/main"); head != settled.BaseCommit {
		t.Fatalf("main = %q, want the untouched base %q", head, settled.BaseCommit)
	}
}

// assertCompletedAndCleaned checks the half that applies to a run whose work is
// already promoted: the item is closed exactly once and both artifacts are gone.
func assertCompletedAndCleaned(t *testing.T, repository string, tracker *fakeTracker, settled runstate.State) {
	t.Helper()
	if settled.Status != runstate.StatusSucceeded || settled.Phase != runstate.PhaseComplete {
		t.Fatalf("settled state = %#v, want a completed run", settled)
	}
	if settled.Integration == nil || !settled.WorktreeRemoved || !settled.BranchRemoved || settled.CleanupFailure != "" {
		t.Fatalf("settled state = %#v, want recorded integration and removed artifacts", settled)
	}
	if !tracker.closed || tracker.blocked {
		t.Fatalf("tracker = %#v, want a closed and unblocked item", tracker)
	}
	if closes := countCalls(tracker.calls, "complete"); closes != 1 {
		t.Fatalf("item closed %d time(s), want exactly once", closes)
	}
	if head := gitLine(t, repository, "rev-parse", "refs/heads/main"); head != settled.Integration.SourceCommit {
		t.Fatalf("main = %q, want the integrated commit %q", head, settled.Integration.SourceCommit)
	}
	if _, err := os.Stat(settled.WorktreePath); !os.IsNotExist(err) {
		t.Fatalf("completed run left its worktree behind: %v", err)
	}
	if branches := gitOutput(t, repository, "branch", "--list", settled.Branch); strings.TrimSpace(branches) != "" {
		t.Fatalf("completed run left its branch behind: %q", branches)
	}
}

// A cleanup that never ran leaves a succeeded, closed run with real artifacts
// still on disk. Reconciliation owes exactly the removal, and owes it without
// re-running anything the run already did.
func TestReconcileFinishesCleanupThatNeverRan(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider)
	pipeline.Worktrees = &hookedWorktrees{
		WorktreeManager: pipeline.Worktrees,
		beforeCleanup:   func() error { return errors.New("worktree is busy") },
	}

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.CleanupFailure == "" || outcome.WorktreeRemoved {
		t.Fatalf("outcome = %#v, want a succeeded run with outstanding cleanup", outcome)
	}

	results := reconcileSweep(t, repository, worktreeRoot, store, tracker)
	if len(results) != 1 || results[0].Action != ActionCompleted || results[0].Failure != "" {
		t.Fatalf("reconciliation = %#v", results)
	}
	settled, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	assertCompletedAndCleaned(t, repository, tracker, settled)
	if len(provider.requests) != 2 {
		t.Fatalf("provider requests = %d, want the original developer and reviewer only", len(provider.requests))
	}
}

// The artifacts of a run whose completion was never written down are already
// gone. Repeating cleanup over them has to be a no-op rather than an error,
// because that is the only way reconciliation can be safe to run repeatedly.
func TestReconcileSettlesARunWhoseArtifactsAreAlreadyGone(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	// Cleanup runs for real and removes both artifacts; only the record of it
	// never lands, which is exactly the pre-cleanup marker left on disk.
	losing := &interruptingStore{StateStore: store, failPhase: runstate.PhaseComplete, failAlways: true}
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, losing, tracker, provider, []string{"exit 0"}), provider)

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	recorded, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if recorded.Phase != runstate.PhaseCleaningUp || recorded.WorktreeRemoved || recorded.BranchRemoved {
		t.Fatalf("recorded state = %#v, want the pre-cleanup marker", recorded)
	}
	if _, err := os.Stat(outcome.WorktreePath); !os.IsNotExist(err) {
		t.Fatalf("the worktree this test needs gone is still there: %v", err)
	}

	results := reconcileSweep(t, repository, worktreeRoot, store, tracker)
	if len(results) != 1 || results[0].Action != ActionCompleted || results[0].Failure != "" {
		t.Fatalf("reconciliation = %#v, want a clean completion over absent artifacts", results)
	}
	settled, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	assertCompletedAndCleaned(t, repository, tracker, settled)
	// The item was already closed by the run itself, so reconciliation records
	// no second closure and no second outcome note.
	if closes := countCalls(tracker.calls, "complete"); closes != 1 {
		t.Fatalf("item closed %d time(s), want exactly once", closes)
	}
}

// A run its own pipeline can still continue is not reconciliation's to settle.
// Ending it would throw away a change that can still be finished.
func TestReconcileLeavesAResumableRepairLoopAlone(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	write := func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}
	interrupted := &interruptedStore{StateStore: store, atAttempt: 2}
	first := roleBackend(write, repairVerdict)
	firstPipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, interrupted, tracker, first, []string{"exit 0"}), first)
	if _, err := firstPipeline.Run(context.Background(), tracker.item.ID); err == nil || !interrupted.stopped {
		t.Fatalf("interrupted Run() error = %v, stopped = %t", err, interrupted.stopped)
	}
	before, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	results := reconcileSweep(t, repository, worktreeRoot, store, tracker)
	if len(results) != 1 || results[0].Action != ActionResumable {
		t.Fatalf("reconciliation = %#v, want the run left resumable", results)
	}
	after, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if after.Status != before.Status || after.Phase != before.Phase || after.RepairAttempts != before.RepairAttempts {
		t.Fatalf("reconciliation changed a resumable run: %#v", after)
	}
	if tracker.blocked || tracker.closed {
		t.Fatalf("reconciliation acted on the item of a resumable run: %#v", tracker)
	}

	// The run is still exactly as resumable afterwards, so the pipeline picks
	// it up and finishes it rather than starting a second run for the item.
	second := roleBackend(write, approveVerdict)
	resumed := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, second, []string{"exit 0"}), second)
	outcome, err := resumed.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("resumed Run() error = %v", err)
	}
	if outcome.RunID != before.RunID || outcome.Integration == nil {
		t.Fatalf("resumed run = %#v, want the reconciled run integrated", outcome)
	}
}

// A run somebody is still acting on has an owner. Reconciliation must not
// decide anything about it from outside, however interrupted its state looks.
func TestReconcileLeavesRunsALiveProcessHolds(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	halting := &haltingStore{StateStore: store, at: runstate.PhaseChecking}
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, halting, tracker, provider, []string{"exit 0"}), provider)
	if _, err := pipeline.Run(context.Background(), tracker.item.ID); err == nil {
		t.Fatal("interrupted Run() error = nil")
	}

	_, lease, err := store.AdoptRun(context.Background(), pipelineRunID)
	if err != nil {
		t.Fatalf("AdoptRun() error = %v", err)
	}
	defer lease.Release()

	results := reconcileSweep(t, repository, worktreeRoot, store, tracker)
	if len(results) != 1 || results[0].Action != ActionHeld || results[0].Failure != "" {
		t.Fatalf("reconciliation = %#v, want the held run left alone", results)
	}
	held, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if held.Status.Terminal() || tracker.blocked {
		t.Fatalf("reconciliation settled a held run: state = %#v, blocked = %t", held, tracker.blocked)
	}
}

// A run interrupted before it created anything leaves nothing for a person to
// act on, so it is recorded terminal without blocking the item.
func TestReconcileFailsARunThatLeftNothingBehind(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	now := execution.RealClock{}.Now()
	if err := store.Create(runstate.State{
		SchemaVersion: runstate.StateSchemaVersion,
		RunID:         pipelineRunID,
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		WorkItemID:    tracker.item.ID,
		Backend:       "claude-code",
		Status:        runstate.StatusPending,
		StartedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	results := reconcileSweep(t, repository, worktreeRoot, store, tracker)
	if len(results) != 1 || results[0].Action != ActionFailed || results[0].Failure != "" {
		t.Fatalf("reconciliation = %#v, want a plain terminal failure", results)
	}
	if tracker.blocked {
		t.Fatal("reconciliation blocked an item that has nothing preserved")
	}
	if !strings.Contains(tracker.notes, "left nothing behind") || !strings.Contains(tracker.notes, "No worktree was recorded") {
		t.Fatalf("notes = %q", tracker.notes)
	}
	settled, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settled.Status != runstate.StatusFailed || settled.CompletedAt == nil {
		t.Fatalf("settled state = %#v", settled)
	}
}

// A promotion found in the target branch is only this run's to claim if this
// run's reviewer approved it. Without that the commit needs a person.
func TestReconcileBlocksAnUnapprovedPromotionItFinds(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	halting := &haltingStore{StateStore: store, at: runstate.PhaseCompleting}
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, halting, tracker, provider, []string{"exit 0"}), provider)
	if _, err := pipeline.Run(context.Background(), tracker.item.ID); err == nil {
		t.Fatal("interrupted Run() error = nil")
	}
	// Strip the approval the run recorded, leaving the promoted commit without
	// the evidence that authorized it.
	interrupted, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	interrupted.ReviewDecision = ""
	if err := store.Save(interrupted); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	results := reconcileSweep(t, repository, worktreeRoot, store, tracker)
	if len(results) != 1 || results[0].Action != ActionBlocked {
		t.Fatalf("reconciliation = %#v, want an unapproved promotion blocked", results)
	}
	if !tracker.blocked || tracker.closed {
		t.Fatalf("tracker = %#v, want a blocked and unclosed item", tracker)
	}
	if !strings.Contains(tracker.blockReason, "no approving review") {
		t.Fatalf("blocker reason = %q", tracker.blockReason)
	}
	settled, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settled.Integration != nil || settled.Status != runstate.StatusFailed {
		t.Fatalf("settled state = %#v, want no integration claimed", settled)
	}
}

// A promotion the durable invariants refuse to describe is handed to a person
// rather than claimed. The item must not be closed behind evidence that cannot
// be written down, which would leave it settled nowhere.
func TestReconcileBlocksAPromotionItCannotRecord(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	halting := &haltingStore{StateStore: store, at: runstate.PhaseCompleting}
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, halting, tracker, provider, []string{"exit 0"}), provider)
	if _, err := pipeline.Run(context.Background(), tracker.item.ID); err == nil {
		t.Fatal("interrupted Run() error = nil")
	}
	// Integration insists on two demonstrably separate invocations. A run whose
	// reviewer session is missing cannot claim one, however the repository looks.
	interrupted, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	interrupted.ReviewSessionID = ""
	if err := store.Save(interrupted); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	results := reconcileSweep(t, repository, worktreeRoot, store, tracker)
	if len(results) != 1 || results[0].Action != ActionBlocked {
		t.Fatalf("reconciliation = %#v, want the unrecordable promotion blocked", results)
	}
	if !tracker.blocked || tracker.closed {
		t.Fatalf("tracker = %#v, want a blocked and unclosed item", tracker)
	}
	if !strings.Contains(tracker.blockReason, "does not support recording that promotion") {
		t.Fatalf("blocker reason = %q", tracker.blockReason)
	}
	settled, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settled.Integration != nil || settled.Status != runstate.StatusFailed {
		t.Fatalf("settled state = %#v, want no integration claimed", settled)
	}
}

// A sweep that cannot finish must settle nothing and hide nothing. The run
// stays outstanding and a later sweep completes it, which is what makes
// reconciliation safe to simply run again.
func TestReconcileLeavesARunOutstandingWhenItsCleanupFails(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	halting := &haltingStore{StateStore: store, at: runstate.PhaseCleaningUp}
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, halting, tracker, provider, []string{"exit 0"}), provider)
	if _, err := pipeline.Run(context.Background(), tracker.item.ID); err == nil {
		t.Fatal("interrupted Run() error = nil")
	}

	worktrees := &refusingCleanup{ReconcileWorktrees: newObserver(t, repository, worktreeRoot), refusals: 1}
	results, err := Reconciler{Tracker: tracker, Worktrees: worktrees, Store: store}.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 1 || results[0].Action != ActionUnsettled || !strings.Contains(results[0].Failure, "worktree is busy") {
		t.Fatalf("reconciliation = %#v, want the run left unsettled", results)
	}
	unsettled, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !unsettled.Outstanding() || unsettled.CleanupFailure == "" {
		t.Fatalf("unsettled state = %#v, want an outstanding run carrying its cleanup failure", unsettled)
	}
	if _, err := os.Stat(filepath.Join(unsettled.WorktreePath, "feature.txt")); err != nil {
		t.Fatalf("refused cleanup removed the worktree anyway: %v", err)
	}

	// The next sweep finishes the job, and the item is still closed exactly
	// once across both of them.
	again := reconcileSweep(t, repository, worktreeRoot, store, tracker)
	if len(again) != 1 || again[0].Action != ActionCompleted || again[0].Failure != "" {
		t.Fatalf("second reconciliation = %#v", again)
	}
	settled, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	assertCompletedAndCleaned(t, repository, tracker, settled)
}

// Reconcile is a sweep, not a single-run command: one run it cannot settle must
// not hide the others from the operator reading the report.
func TestReconcileReportsEveryRunEvenWhenOneCannotBeSettled(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	halting := &haltingStore{StateStore: store, at: runstate.PhaseCleaningUp}
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, halting, tracker, provider, []string{"exit 0"}), provider)
	if _, err := pipeline.Run(context.Background(), tracker.item.ID); err == nil {
		t.Fatal("interrupted Run() error = nil")
	}
	// A second recorded run, interrupted before it built anything. It is
	// settled on its own regardless of what happens to the first one.
	now := execution.RealClock{}.Now()
	bare := runstate.State{
		SchemaVersion: runstate.StateSchemaVersion,
		RunID:         "run-abcdef0123456789abcdef0123456789",
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		WorkItemID:    tracker.item.ID,
		Backend:       "claude-code",
		Status:        runstate.StatusPending,
		StartedAt:     now,
		UpdatedAt:     now,
	}
	if err := store.Create(bare); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	worktrees := &refusingCleanup{ReconcileWorktrees: newObserver(t, repository, worktreeRoot), refusals: 1}
	results, err := Reconciler{Tracker: tracker, Worktrees: worktrees, Store: store}.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("reconciliation = %#v, want both outstanding runs reported", results)
	}
	actions := map[string]ReconcileAction{}
	for _, result := range results {
		actions[result.RunID] = result.Action
	}
	// The bare run built nothing and its item is already closed by the first
	// run, so it settles as a plain terminal failure rather than a blocker.
	if actions[pipelineRunID] != ActionUnsettled || actions[bare.RunID] != ActionFailed {
		t.Fatalf("reconciliation = %#v, want the failed cleanup unsettled and the bare run settled", results)
	}
}

// refusingCleanup refuses the first cleanup attempts and then delegates, which
// is how a sweep that could not finish is exercised end to end.
type refusingCleanup struct {
	ReconcileWorktrees
	refusals int
}

func (w *refusingCleanup) CleanupIntegrated(ctx context.Context, request gitworktree.CleanupRequest) (gitworktree.Cleanup, error) {
	if w.refusals > 0 {
		w.refusals--
		return gitworktree.Cleanup{}, errors.New("worktree is busy")
	}
	return w.ReconcileWorktrees.CleanupIntegrated(ctx, request)
}

func newObserver(t *testing.T, repository, worktreeRoot string) ReconcileWorktrees {
	t.Helper()
	worktrees, err := gitworktree.New(gitworktree.Options{
		Runner:                execution.OSProcessRunner{},
		RepositoryRoot:        repository,
		WorktreeRoot:          worktreeRoot,
		AllowedPrimaryChanges: []string{".beads/interactions.jsonl", ".beads/issues.jsonl"},
	})
	if err != nil {
		t.Fatalf("gitworktree.New() error = %v", err)
	}
	return worktrees
}

// reconcileSweep runs exactly one reconciliation over the durable state a
// restarted process would find.
func reconcileSweep(t *testing.T, repository, worktreeRoot string, store ReconcileStore, tracker *fakeTracker) []Reconciliation {
	t.Helper()
	worktrees := newObserver(t, repository, worktreeRoot)
	results, err := Reconciler{Tracker: tracker, Worktrees: worktrees, Store: store}.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	return results
}

// haltingStore stops accepting writes for good once a run reaches a chosen
// phase, after letting `after` writes through there. Nothing the run does
// afterwards survives, including the terminal failure record it would normally
// write, so what is left on disk is what a killed process leaves.
type haltingStore struct {
	StateStore
	at     runstate.Phase
	after  int
	seen   int
	halted bool
}

func (s *haltingStore) Save(state runstate.State) error {
	if !s.halted && state.Phase == s.at {
		if s.seen >= s.after {
			s.halted = true
		} else {
			s.seen++
		}
	}
	if s.halted {
		return errors.New("state store is unavailable")
	}
	return s.StateStore.Save(state)
}
