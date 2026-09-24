package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
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
	// What the sweep did and what became of the run are different facts, and the
	// second is what says whether anybody's work is gone. This run made nothing,
	// which is stated as itself rather than as work thrown away.
	if outcome := results[0].Outcome; outcome != runstate.OutcomeFailed {
		t.Errorf("outcome = %q, want %q", outcome, runstate.OutcomeFailed)
	}
	if remains := results[0].Artifacts().Describe(); remains != "no artifacts recorded" {
		t.Errorf("what remains = %q, want the absence stated rather than work reported as removed", remains)
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

// A stoppage can be settled onto a record that is already terminal: the last
// write of the process that died lands after the sweep listed the run and
// before it adopts it. The stoppage still has to reach that record. Every
// surface answers "why did this stop" from the reason and the blocker on the
// run, so a sweep that left the record as it found it handed a person three
// empty answers.
func TestReconcileRecordsAStoppageSettledOntoAnAlreadyTerminalRun(t *testing.T) {
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

	// The terminal record the interrupted process was still to write is a
	// cancellation with no reason on it, which is what stopping a run leaves.
	cancelled := &cancelledBetweenListingAndAdoption{ReconcileStore: store, at: execution.RealClock{}.Now()}
	results := reconcileSweep(t, repository, worktreeRoot, cancelled, tracker)
	if len(results) != 1 || results[0].Action != ActionBlocked {
		t.Fatalf("reconciliation = %#v, want the stoppage settled onto the terminal run", results)
	}
	if !cancelled.settled {
		t.Fatal("the run was never made terminal, so this settled an interruption rather than a terminal record")
	}
	settled, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// The run keeps the status it recorded for itself; what the sweep adds is
	// what stopped it and why.
	if settled.Status != runstate.StatusCancelled {
		t.Fatalf("settled status = %q, want the status the run recorded for itself", settled.Status)
	}
	if !strings.Contains(settled.Blocker, "no attempt of the harness can finish it") {
		t.Fatalf("blocker = %q, want the words the item was blocked in", settled.Blocker)
	}
	if !strings.Contains(settled.Failure, "reconciled after an interrupted run") ||
		!strings.Contains(settled.Failure, "no attempt of the harness can finish it") {
		t.Fatalf("failure = %q, want the reason a person reads for why this stopped", settled.Failure)
	}
	if outcome := settled.Outcome(); outcome != runstate.OutcomeStopped {
		t.Errorf("outcome = %q, want %q", outcome, runstate.OutcomeStopped)
	}
	// `yoyo status` prints its reason from the read model rather than from the
	// record, so the reason has to survive into the summary.
	history, err := store.History(runstate.RunQuery{})
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if len(history.Runs) != 1 || history.Runs[0].Failure != settled.Failure {
		t.Fatalf("history = %#v, want the settled run's own reason", history.Runs)
	}
	// The triage docket is the other surface a person triages from, and a build
	// after this sweep reads the same record rather than the sweep's own copy of
	// it.
	docket := &memoryDocket{}
	build, err := docketerOver([]runstate.State{settled}, docket).Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(build.Entries) != 1 || build.Entries[0].Blocker != settled.Blocker {
		t.Fatalf("docket = %#v, want one entry carrying the run's blocker", build.Entries)
	}
}

// cancelledBetweenListingAndAdoption records the terminal write of the process
// that died in the window a sweep leaves for it: after the runs to settle are
// listed and before the one it settles is adopted and re-read. What it writes is
// a cancellation, which is the terminal record that names no reason of its own.
type cancelledBetweenListingAndAdoption struct {
	ReconcileStore
	at      time.Time
	settled bool
}

func (s *cancelledBetweenListingAndAdoption) Outstanding() ([]runstate.State, error) {
	outstanding, err := s.ReconcileStore.Outstanding()
	if err != nil || s.settled {
		return outstanding, err
	}
	for _, state := range outstanding {
		cancelled := state
		cancelled.Status = runstate.StatusCancelled
		completedAt := s.at
		cancelled.CompletedAt = &completedAt
		cancelled.UpdatedAt = completedAt
		if err := s.ReconcileStore.Save(cancelled); err != nil {
			return nil, err
		}
		s.settled = true
	}
	return outstanding, nil
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
	// the evidence that authorized it. What it approved goes with it: the two are
	// one verdict, and the record refuses one without the other.
	interrupted, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	interrupted.ReviewDecision = ""
	interrupted.ReviewApproves = ""
	if err := store.Save(interrupted); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	results := reconcileSweep(t, repository, worktreeRoot, store, tracker)
	if len(results) != 1 || results[0].Action != ActionBlocked {
		t.Fatalf("reconciliation = %#v, want an unapproved promotion blocked", results)
	}
	// The run is handed to a person with its change intact, and the sweep says
	// both. Reporting the durable status alone would put "failed" over a branch
	// and worktree that are still there, which is the reading this vocabulary
	// exists to stop.
	if outcome := results[0].Outcome; outcome != runstate.OutcomeStopped {
		t.Errorf("outcome = %q, want %q", outcome, runstate.OutcomeStopped)
	}
	if remains := results[0].Artifacts().Describe(); remains != "work preserved" {
		t.Errorf("what remains = %q, want the preserved change named", remains)
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

// A recorded integration is a claim a process wrote down, and reconciliation
// exists for state a process that died wrote. A promotion the target does not
// carry has to reach a person rather than close an item over work that is not
// there.
func TestReconcileBlocksARecordedIntegrationTheTargetDoesNotCarry(t *testing.T) {
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
	interrupted, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if interrupted.Integration == nil {
		t.Fatalf("interrupted state = %#v, want the recorded integration this test contradicts", interrupted)
	}
	// Put the target back where it was before the promotion, which is what the
	// repository looks like when the record outran what actually landed.
	gitOutput(t, repository, "update-ref", "refs/heads/main", interrupted.BaseCommit)

	results := reconcileSweep(t, repository, worktreeRoot, store, tracker)
	if len(results) != 1 || results[0].Action != ActionBlocked {
		t.Fatalf("reconciliation = %#v, want the uncarried promotion blocked", results)
	}
	// The run itself closed the item when it recorded the promotion, so the
	// blocker is what reopens the question rather than a note beside it.
	if !tracker.blocked || tracker.item.Status != "blocked" {
		t.Fatalf("tracker = %#v, want the item blocked for a person", tracker)
	}
	if !strings.Contains(tracker.blockReason, "does not contain it") ||
		!strings.Contains(tracker.blockReason, interrupted.Integration.SourceCommit) {
		t.Fatalf("blocker reason = %q, want the recorded commit and the target that lacks it", tracker.blockReason)
	}
	settled, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settled.Status != runstate.StatusFailed || settled.Integration != nil {
		t.Fatalf("settled state = %#v, want a failed run claiming no promotion", settled)
	}
	// Nothing the run built was swept away on the strength of a record the
	// repository denies, and the target was not moved to make the record true.
	if _, err := os.Stat(filepath.Join(settled.WorktreePath, "feature.txt")); err != nil {
		t.Fatalf("blocked run lost its preserved change: %v", err)
	}
	if branches := gitOutput(t, repository, "branch", "--list", settled.Branch); strings.TrimSpace(branches) == "" {
		t.Fatal("blocked run lost its preserved branch")
	}
	if head := gitLine(t, repository, "rev-parse", "refs/heads/main"); head != settled.BaseCommit {
		t.Fatalf("main = %q, want the untouched base %q", head, settled.BaseCommit)
	}
	// The blocked run keeps nothing that would make it owe cleanup, so the next
	// sweep leaves it alone instead of deciding it over again.
	if again := reconcileSweep(t, repository, worktreeRoot, store, tracker); len(again) != 0 {
		t.Fatalf("second reconciliation = %#v, want nothing outstanding", again)
	}
}

// The same contradiction on a record that already said the run succeeded, which
// is the shape a process killed after the completing save leaves. The sweep
// deliberately keeps the status such a record wrote for itself, so what stops it
// reading as work that landed is the precedence rule in the read model: the
// blocker this sweep saves outranks the status, and every surface says "stopped".
func TestReconcileDoesNotLeaveAContradictedPromotionReadingAsSucceeded(t *testing.T) {
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
	interrupted, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if interrupted.Integration == nil {
		t.Fatalf("interrupted state = %#v, want the recorded integration this test contradicts", interrupted)
	}
	// The run reached its terminal status before the process died, short of the
	// complete phase, which is what leaves the cleanup outstanding.
	completedAt := interrupted.UpdatedAt
	interrupted.Status = runstate.StatusSucceeded
	interrupted.CompletedAt = &completedAt
	if err := store.Save(interrupted); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	gitOutput(t, repository, "update-ref", "refs/heads/main", interrupted.BaseCommit)

	results := reconcileSweep(t, repository, worktreeRoot, store, tracker)
	if len(results) != 1 || results[0].Action != ActionBlocked {
		t.Fatalf("reconciliation = %#v, want the uncarried promotion blocked", results)
	}
	if !tracker.blocked || tracker.item.Status != "blocked" {
		t.Fatalf("tracker = %#v, want the item blocked for a person", tracker)
	}
	settled, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// The status stays what the run recorded — the sweep's own rule, unchanged —
	// and the blocker beside it is what the outcome is read from.
	if settled.Status != runstate.StatusSucceeded || settled.Integration != nil {
		t.Fatalf("settled state = %#v, want the recorded status kept and no promotion claimed", settled)
	}
	if settled.Outcome() != runstate.OutcomeStopped {
		t.Fatalf("settled outcome = %q, want a stoppage somebody owns", settled.Outcome())
	}
	if !strings.Contains(settled.Failure, "does not contain it") {
		t.Fatalf("settled failure = %q, want the reason the sweep settled it on", settled.Failure)
	}
	if !strings.Contains(settled.Blocker, "does not contain it") {
		t.Fatalf("settled blocker = %q, want the blocker the item carries", settled.Blocker)
	}
	// Nothing the run built was swept away on the strength of a record the
	// repository denies. The run closed the item when it recorded the promotion,
	// and the blocker above is what reopens the question.
	if _, err := os.Stat(filepath.Join(settled.WorktreePath, "feature.txt")); err != nil {
		t.Fatalf("blocked run lost its preserved change: %v", err)
	}
	// The settled run owes nothing further, so the next sweep leaves it alone
	// rather than deciding it again.
	if again := reconcileSweep(t, repository, worktreeRoot, store, tracker); len(again) != 0 {
		t.Fatalf("second reconciliation = %#v, want nothing outstanding", again)
	}
}

// Observing the target must not turn every target that moved on into a
// disagreement. A promotion other work was committed on top of is still in the
// target, and the run that made it is owed its completion.
func TestReconcileCompletesARecordedIntegrationTheTargetMovedPast(t *testing.T) {
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
	// Somebody else's commit lands on the target, so it no longer stands where
	// this run's promotion left it.
	if err := os.WriteFile(filepath.Join(repository, "later.txt"), []byte("later work\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	runPipelineGit(t, repository, "add", "later.txt")
	runPipelineGit(t, repository, "commit", "-m", "later work")

	results := reconcileSweep(t, repository, worktreeRoot, store, tracker)
	if len(results) != 1 || results[0].Action != ActionCompleted || results[0].Failure != "" {
		t.Fatalf("reconciliation = %#v, want the promotion the target still carries completed", results)
	}
	if tracker.blocked {
		t.Fatalf("reconciliation blocked a promotion the target carries: %q", tracker.blockReason)
	}
	settled, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settled.Phase != runstate.PhaseComplete || !settled.WorktreeRemoved || !settled.BranchRemoved {
		t.Fatalf("settled state = %#v, want a completed run with its artifacts removed", settled)
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

// A run waiting out a provider usage limit is not an interrupted run: it
// recorded a deadline and is owed the attempt it was refused. Settling it would
// throw away a claimed item and a preserved worktree over a wait that has not
// finished yet.
func TestReconcileLeavesARunPausedForAUsageLimitAlone(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	resetsAt := baseTime.Add(2 * time.Hour)
	limit := &backend.UsageLimit{Kind: "five_hour", ResetsAt: resetsAt}

	// Pause the run by refusing its first attempt with a wait too long to hold
	// this process open.
	first := usageLimitBackend(1, limit, approveVerdict)
	firstClock := &pausingClock{now: baseTime}
	firstPipeline := waiting(automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, first, []string{"exit 0"}), first),
		firstClock, 6*time.Hour, time.Minute)
	paused, err := firstPipeline.Run(context.Background(), tracker.item.ID)
	if err != nil || !paused.Paused {
		t.Fatalf("Run() error = %v, paused = %t", err, paused.Paused)
	}
	before, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	results := reconcileSweep(t, repository, worktreeRoot, store, tracker)
	if len(results) != 1 || results[0].Action != ActionResumable {
		t.Fatalf("reconciliation = %#v, want the paused run left resumable", results)
	}
	if !strings.Contains(results[0].Detail, "paused for an exhausted five_hour usage limit") {
		t.Fatalf("reconciliation did not report why the run is waiting: %q", results[0].Detail)
	}
	after, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if after.Status != before.Status || after.Phase != before.Phase || after.UsageLimitResetsAt == nil {
		t.Fatalf("reconciliation disturbed a paused run: %#v", after)
	}
	if tracker.blocked || tracker.closed {
		t.Fatalf("reconciliation acted on the item of a paused run: blocked=%t closed=%t", tracker.blocked, tracker.closed)
	}

	// The run is still exactly as resumable afterwards: once the deadline passes
	// the pipeline picks it up and finishes it.
	second := usageLimitBackend(0, limit, approveVerdict)
	secondClock := &pausingClock{now: resetsAt.Add(time.Minute)}
	resumed := waiting(automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, second, []string{"exit 0"}), second),
		secondClock, 6*time.Hour, time.Minute)
	outcome, err := resumed.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("resumed Run() error = %v", err)
	}
	if outcome.RunID != before.RunID || outcome.Integration == nil {
		t.Fatalf("resumed run = %#v, want the reconciled run integrated", outcome)
	}
}

// A run whose provider the harness stopped on time is not an interrupted run
// either, for as long as the grace lasts: it is owed the rest of an attempt, in
// the worktree and session that attempt established, and settling it inside the
// grace would discard a change a `yoyo run` somebody is about to type can still
// finish. TestReconcileSettlesAStoppedRunNothingContinued is the other side of
// the grace.
func TestReconcileLeavesARunWithAStoppedProviderAlone(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	first := providerStopBackend(1, execution.ProcessStalled, approveVerdict)
	firstPipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, first, []string{"exit 0"}), first)
	paused, err := firstPipeline.Run(context.Background(), tracker.item.ID)
	if err != nil || !paused.Paused {
		t.Fatalf("Run() error = %v, paused = %t", err, paused.Paused)
	}
	before, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	results := reconcileSweep(t, repository, worktreeRoot, store, tracker)
	if len(results) != 1 || results[0].Action != ActionResumable {
		t.Fatalf("reconciliation = %#v, want the stopped run left resumable", results)
	}
	if !strings.Contains(results[0].Detail, "stopped emitting events") {
		t.Fatalf("reconciliation did not report why the provider was stopped: %q", results[0].Detail)
	}
	// The sweep says how long the run is left resumable, because a reading that
	// said only "resumable" was what let two of these sit for a day and a half.
	if !strings.Contains(results[0].Detail, "settles it as a stopped run") {
		t.Fatalf("reconciliation did not say the grace ends in a settlement: %q", results[0].Detail)
	}
	after, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if after.Status != before.Status || after.Phase != before.Phase || after.ProviderStop != before.ProviderStop {
		t.Fatalf("reconciliation disturbed a stopped run: %#v", after)
	}
	if tracker.blocked || tracker.closed {
		t.Fatalf("reconciliation acted on the item of a stopped run: blocked=%t closed=%t", tracker.blocked, tracker.closed)
	}

	second := providerStopBackend(0, execution.ProcessStalled, approveVerdict)
	resumed := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, second, []string{"exit 0"}), second)
	outcome, err := resumed.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("resumed Run() error = %v", err)
	}
	if outcome.RunID != before.RunID || outcome.Integration == nil {
		t.Fatalf("resumed run = %#v, want the reconciled run integrated", outcome)
	}
}

// A run the harness stopped on time that nothing then continued is a run whose
// process has vanished: its record goes on saying "running" with no live process
// behind it and no ending ever written, so it holds a developer slot, the
// in-flight guard refuses every item beside it, the claim audit leaves it as a
// wait, and every sweep reports it resumable while nothing resumes it. Two of
// these did exactly that from 2026-09-20 07:20 until somebody asked. Past the
// grace the sweep settles it as an environmental stop naming what it observed,
// the item is blocked with the same account, the artifacts are left exactly as
// they were, and the stoppage is docketed — so the development manager's
// repair-continue has an entry to carry out against, and the slot is free.
func TestReconcileSettlesAStoppedRunNothingContinued(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	first := providerStopBackend(1, execution.ProcessStalled, approveVerdict)
	firstPipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, first, []string{"exit 0"}), first)
	paused, err := firstPipeline.Run(context.Background(), tracker.item.ID)
	if err != nil || !paused.Paused {
		t.Fatalf("Run() error = %v, paused = %t", err, paused.Paused)
	}
	stopped, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// The process that hosted the run has returned: nothing holds the lease, and
	// the item still reads as claimed, which is exactly the state the two runs of
	// 2026-09-20 were found in.
	tracker.item.Status = "in_progress"

	docket, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewDocketStore() error = %v", err)
	}
	// The grace has passed and nothing continued the run. The clock is the
	// sweep's own rather than a shortened grace, so what is tested is the default
	// the operator gets.
	later := &pausingClock{now: stopped.UpdatedAt.Add(DefaultVanishedGrace)}
	reconciler := Reconciler{
		Tracker:   tracker,
		Worktrees: newObserver(t, repository, worktreeRoot),
		Store:     store,
		Docket:    docketerOverStore(docket, store, firstPipeline.Config),
		Clock:     later,
	}
	results, err := reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 1 || results[0].Action != ActionBlocked || results[0].Failure != "" {
		t.Fatalf("reconciliation = %#v, want the vanished run settled as blocked", results)
	}
	for _, want := range []string{"no live process behind it", "stopped emitting events", "no ending was ever recorded", "the harness settled it as an environmental stop"} {
		if !strings.Contains(results[0].Detail, want) {
			t.Fatalf("reconciliation detail %q does not say %q", results[0].Detail, want)
		}
	}
	if results[0].Outcome != runstate.OutcomeStopped {
		t.Fatalf("outcome = %q, want the run read as stopped work somebody owns", results[0].Outcome)
	}

	// The record is terminal with the account on it, the stop it carried is now
	// evidence in the refusal rather than a promise to continue, and the change is
	// exactly where the developer left it.
	settled, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !settled.Status.Terminal() || settled.CompletedAt == nil || settled.Blocker == "" || settled.ProviderStop != "" {
		t.Fatalf("settled run = %#v, want a terminal record carrying the blocker and no provider stop", settled)
	}
	if !strings.Contains(settled.Failure, "the harness settled it as an environmental stop") {
		t.Fatalf("settled run's reason does not say the harness settled it: %q", settled.Failure)
	}
	refusal := settled.Environmental
	if refusal == nil || refusal.Cause != runstate.CauseProcessVanished || !refusal.Settled {
		t.Fatalf("environmental refusal = %#v, want the vanished process recorded and settled", refusal)
	}
	for _, want := range []string{"no live process held run " + paused.RunID, "no ending was recorded", stopped.UpdatedAt.UTC().Format(time.RFC3339), "stopped emitting events"} {
		if !strings.Contains(refusal.Detail, want) {
			t.Fatalf("refusal detail %q does not say %q", refusal.Detail, want)
		}
	}
	// The stopped attempt had written to its worktree, so the round delivered
	// something and is not refused in the class's sense: nothing is given back,
	// and the change is what the development manager decides about.
	if refusal.Refused || refusal.GrantReturned || refusal.RoundReturned {
		t.Fatalf("refusal = %#v, want a round that delivered left spent", refusal)
	}
	if settled.WorktreeRemoved || settled.BranchRemoved || settled.WorktreePath != stopped.WorktreePath || settled.Branch != stopped.Branch {
		t.Fatalf("settled run = %#v, want the branch and worktree preserved exactly as the stopped run's were", settled)
	}
	if _, err := os.Stat(filepath.Join(settled.WorktreePath, "partial.txt")); err != nil {
		t.Fatalf("the stopped attempt's work is not where it was left: %v", err)
	}
	if !tracker.blocked || !strings.Contains(tracker.blockReason, "the harness settled it as an environmental stop") {
		t.Fatalf("item blocked = %t with reason %q, want the item blocked with the sweep's account", tracker.blocked, tracker.blockReason)
	}

	// The slot and the in-flight guard read the same listing, and the run is no
	// longer in it.
	incomplete, err := store.Incomplete()
	if err != nil {
		t.Fatalf("Incomplete() error = %v", err)
	}
	if len(incomplete) != 0 {
		t.Fatalf("in flight = %#v, want the settled run holding no slot", incomplete)
	}

	// And the stoppage is on the docket, carrying the environmental account, so a
	// repair-continue decided about it has something to carry out against.
	entries, err := docket.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(entries) != 1 || entries[0].RunID != paused.RunID || entries[0].Class != triage.ClassStoppedRun || entries[0].Closed != nil {
		t.Fatalf("docket = %#v, want the vanished run docketed as a stopped run nobody has decided about", entries)
	}
	entry := entries[0]
	if entry.Environmental == nil || entry.Environmental.Cause != string(runstate.CauseProcessVanished) || entry.Environmental.Account == "" {
		t.Fatalf("docketed environmental = %#v, want the vanished process and its accounting", entry.Environmental)
	}
	if !strings.Contains(entry.Blocker, "the harness settled it as an environmental stop") {
		t.Fatalf("docketed blocker is not the sweep's account:\n%s", entry.Blocker)
	}
	if entry.Artifacts.WorktreePath != stopped.WorktreePath || entry.Artifacts.Branch != stopped.Branch || entry.Artifacts.WorktreeRemoved || entry.Artifacts.BranchRemoved {
		t.Fatalf("artifacts = %#v, want the preserved worktree and branch", entry.Artifacts)
	}
	// Nothing was returned to this run's developer, so what it is owed is the
	// attempt the harness stopped it in, and the entry says so through the
	// durable store rather than only in the process that wrote it.
	// TestARepairContinuesAFirstAttemptStallInItsOwnSession is what carries that
	// decision out.
	if !entry.SessionResumable || entry.Artifacts.DeveloperSession != stopped.ProviderSessionID {
		t.Fatalf("entry = %#v, want the preserved developer session named and reported resumable", entry)
	}

	// A second sweep finds a settled run and nothing to do: the stoppage is keyed
	// to the run, so it is docketed once however many sweeps walk past it.
	again, err := reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("second Reconcile() error = %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("second reconciliation = %#v, want nothing outstanding", again)
	}
	entries, err = docket.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("docket = %#v, want the one entry", entries)
	}
}

// The whole of it, over a real repository, for the case one of the two runs of
// 2026-09-20 was actually in: a run inside its repair loop — a failing check
// handed back to the developer — whose repair attempt the harness stopped on
// time and nothing continued. The sweep settles and dockets it, the development manager decides
// repair-continue about the entry, and the carry-out re-enters the same run in
// the same worktree and session and lands the change. Before this, the decision
// was refused for want of a docketed stoppage, because the run never recorded
// one.
func TestARepairContinueCarriesOutOnARunTheSweepSettledForAVanishedProcess(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	// The first attempt leaves the check failing; the repair attempt the check
	// hands back is the one the harness stops on time.
	stalling := &fakeBackend{developerSession: "developer-session", reviewerSession: "reviewer-session"}
	attempts := 0
	stalling.run = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role != domain.RoleDeveloper {
			return backend.RunResult{}, fmt.Errorf("unexpected role %q before the repair attempt stalled", request.Role)
		}
		attempts++
		if attempts == 1 {
			if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("incomplete\n"), 0o600); err != nil {
				return backend.RunResult{}, err
			}
			return backend.RunResult{
				Backend: domain.BackendClaudeCode, SessionID: stalling.developerSession, ResolvedModel: developerResolved,
				FinalText: "implemented the work item", Process: execution.ProcessResult{Status: execution.ProcessSucceeded}, LastEvent: request.LastSequence,
			}, nil
		}
		return backend.RunResult{
			Backend: domain.BackendClaudeCode, SessionID: stalling.developerSession, IsError: true,
			StopReason: string(execution.ProcessStalled), Process: execution.ProcessResult{Status: execution.ProcessStalled, ExitCode: -1}, LastEvent: request.LastSequence,
		}, nil
	}
	checks := []string{"test -f fixed.txt"}
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, stalling, checks), stalling)
	paused, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil || !paused.Paused || paused.ProviderStop != runstate.ProviderStopStalled {
		t.Fatalf("Run() error = %v, outcome = %#v, want the repair attempt stopped on time", err, paused)
	}
	stopped, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if stopped.CheckFailure == nil || stopped.RepairAttempts != 1 {
		t.Fatalf("stopped run = %#v, want the failing check on the record with one repair attempt spent", stopped)
	}
	tracker.item.Status = "in_progress"

	docket := &memoryDocket{}
	reconciler := Reconciler{
		Tracker:   tracker,
		Worktrees: newObserver(t, repository, worktreeRoot),
		Store:     store,
		Docket:    docketerOverStore(docket, store, pipeline.Config),
		Clock:     &pausingClock{now: stopped.UpdatedAt.Add(DefaultVanishedGrace)},
	}
	results, err := reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 1 || results[0].Action != ActionBlocked {
		t.Fatalf("reconciliation = %#v, want the vanished run settled as blocked", results)
	}
	if len(docket.entries) != 1 || docket.entries[0].Check == nil || docket.entries[0].Environmental == nil {
		t.Fatalf("docket = %#v, want the stoppage docketed with the failing check and the vanished process on it", docket.entries)
	}

	// The development manager decides repair-continue about the docketed
	// stoppage, exactly as the conversation records one, and the carry-out
	// re-enters the run rather than refusing for want of a stoppage.
	if _, err := store.Triage().GrantRepair(context.Background(), tracker.item.ID, triageDecided(runstate.TriageDecisionRepair, paused.RunID),
		TriageRepairGrantRounds(pipeline.Config.Triage), time.Now(), TriageCaps(pipeline.Config.Execution, pipeline.Config.Triage)); err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
	worktrees, err := gitworktree.New(gitworktree.Options{Runner: execution.OSProcessRunner{}, RepositoryRoot: repository, WorktreeRoot: worktreeRoot})
	if err != nil {
		t.Fatalf("gitworktree.New() error = %v", err)
	}
	intake, err := runstate.NewIntakeHoldStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewIntakeHoldStore() error = %v", err)
	}
	continuing := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "fixed.txt"), []byte("fixed\n"), 0o600)
	}, approveVerdict)
	continuer := RepairContinuer{
		Docket:             docket,
		Runs:               store,
		Intake:             intake,
		Decisions:          store.Triage(),
		Items:              tracker,
		Worktrees:          worktrees,
		ConfiguredAttempts: pipeline.Config.Execution.RepairAttemptsBeforeReplan,
		Capacity:           pipeline.Config.Execution.MaxConcurrentDevelopers,
		Start: func(ctx context.Context, workItemID, runID string) (Outcome, error) {
			return automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, continuing, checks), continuing).
				Continue(ctx, workItemID, runID)
		},
	}
	result, err := continuer.Continue(context.Background(), RepairContinueRequest{Run: paused.RunID})
	if err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	if !result.Continued || result.Outcome.RunID != paused.RunID || result.Outcome.Integration == nil || !tracker.closed {
		t.Fatalf("result = %#v, closed = %t, want the same run continued and its change landed", result, tracker.closed)
	}
	continued := continuing.requestsForRole(domain.RoleDeveloper)
	if len(continued) != 1 || continued[0].SessionID != stalling.developerSession || continued[0].WorkingDirectory != stopped.WorktreePath {
		t.Fatalf("continued attempts = %#v, want one, in the stopped run's own session and worktree", continued)
	}
	// The vanished process's account belonged to the round the continuation
	// superseded, and a run that landed carries neither it nor a stop.
	landed, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if landed.Environmental != nil || landed.ProviderStop != "" || landed.Blocker != "" {
		t.Fatalf("landed run = %#v, want the settled stoppage superseded", landed)
	}
}

// The other run of 2026-09-23, and the one nothing could carry on: a stall in
// the run's first attempt, with the developer session preserved and no failure
// ever returned. The sweep settles and dockets it after the half hour, the entry
// says the session is resumable, and the repair the development manager records
// is carried out as a continuation of that session at the point it stalled
// rather than refused for want of a repair input. Before this the refusal left
// a re-run as the only decision that could be carried out, and a re-run starts
// over from the target branch with the session gone and the uncommitted work in
// the preserved worktree gone with it.
func TestARepairContinuesAFirstAttemptStallInItsOwnSession(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	// The first developer attempt writes half of the change and is then stopped
	// on time, which is the whole of what a stall leaves: a session, a worktree
	// with uncommitted work in it, and nothing anybody judged.
	stalling := providerStopBackend(1, execution.ProcessStalled, approveVerdict)
	checks := []string{"exit 0"}
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, stalling, checks), stalling)
	paused, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil || !paused.Paused || paused.ProviderStop != runstate.ProviderStopStalled {
		t.Fatalf("Run() error = %v, outcome = %#v, want the first attempt stopped on time", err, paused)
	}
	stopped, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// Nothing was handed back, which is what used to refuse the repair: no
	// findings, no failing check, no refused paths, and no attempt spent.
	if stopped.RepairAttempts != 0 || stopped.CheckFailure != nil || len(stopped.ReviewFindingDetails) != 0 || stopped.PathRefusal != nil {
		t.Fatalf("stopped run = %#v, want a first attempt with nothing returned to its developer", stopped)
	}
	if stopped.ProviderSessionID == "" {
		t.Fatalf("stopped run = %#v, want the developer session it stalled in preserved", stopped)
	}
	tracker.item.Status = "in_progress"

	docket := &memoryDocket{}
	reconciler := Reconciler{
		Tracker:   tracker,
		Worktrees: newObserver(t, repository, worktreeRoot),
		Store:     store,
		Docket:    docketerOverStore(docket, store, pipeline.Config),
		Clock:     &pausingClock{now: stopped.UpdatedAt.Add(DefaultVanishedGrace)},
	}
	results, err := reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 1 || results[0].Action != ActionBlocked {
		t.Fatalf("reconciliation = %#v, want the stalled run settled after the grace", results)
	}

	// The entry carries the fact the decision turns on: the session is there and
	// can simply be carried on, and the continuation costs the item neither a
	// round nor an attempt.
	if len(docket.entries) != 1 {
		t.Fatalf("docket = %#v, want the stalled run docketed once", docket.entries)
	}
	entry := docket.entries[0]
	if !entry.SessionResumable || entry.Artifacts.DeveloperSession != stopped.ProviderSessionID {
		t.Fatalf("entry = %#v, want the preserved session named and reported resumable", entry)
	}
	for _, want := range []string{
		"Nothing was judged",
		"the session it stopped in is preserved",
		"yoyo triage repair " + paused.RunID,
		"spends no review round and no repair attempt",
	} {
		if !strings.Contains(entry.Render(), want) {
			t.Fatalf("entry does not say %q:\n%s", want, entry.Render())
		}
	}

	// The development manager records a repair about that stoppage, exactly as
	// the conversation records one.
	if _, err := store.Triage().GrantRepair(context.Background(), tracker.item.ID, triageDecided(runstate.TriageDecisionRepair, paused.RunID),
		TriageRepairGrantRounds(pipeline.Config.Triage), time.Now(), TriageCaps(pipeline.Config.Execution, pipeline.Config.Triage)); err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
	roundsBefore, err := store.Triage().Counters(tracker.item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	worktrees, err := gitworktree.New(gitworktree.Options{Runner: execution.OSProcessRunner{}, RepositoryRoot: repository, WorktreeRoot: worktreeRoot})
	if err != nil {
		t.Fatalf("gitworktree.New() error = %v", err)
	}
	intake, err := runstate.NewIntakeHoldStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewIntakeHoldStore() error = %v", err)
	}
	// What the continued developer finds is asked where it is handed over rather
	// than afterwards: a run that lands removes its worktree on purpose, so the
	// only moment the half-written work can be proved to have survived is the
	// moment the attempt it belongs to is resumed.
	survived := false
	continuing := roleBackend(func(request backend.RunRequest) error {
		if _, err := os.Stat(filepath.Join(request.WorkingDirectory, "partial.txt")); err == nil {
			survived = true
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	continuer := RepairContinuer{
		Docket:             docket,
		Runs:               store,
		Intake:             intake,
		Decisions:          store.Triage(),
		Items:              tracker,
		Worktrees:          worktrees,
		ConfiguredAttempts: pipeline.Config.Execution.RepairAttemptsBeforeReplan,
		Capacity:           pipeline.Config.Execution.MaxConcurrentDevelopers,
		Start: func(ctx context.Context, workItemID, runID string) (Outcome, error) {
			return automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, continuing, checks), continuing).
				Continue(ctx, workItemID, runID)
		},
	}
	result, err := continuer.Continue(context.Background(), RepairContinueRequest{Run: paused.RunID})
	if err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	if !result.Continued || !result.Stall || result.Outcome.RunID != paused.RunID || result.Outcome.Integration == nil {
		t.Fatalf("result = %#v, want the stalled run carried on and its change landed", result)
	}

	// The same session, in the same worktree, with the half-written work still
	// in it.
	continued := continuing.requestsForRole(domain.RoleDeveloper)
	if len(continued) != 1 || continued[0].SessionID != stopped.ProviderSessionID || continued[0].WorkingDirectory != stopped.WorktreePath {
		t.Fatalf("continued attempts = %#v, want one, in the stalled run's own session and worktree", continued)
	}
	if !survived {
		t.Fatal("the stalled attempt's uncommitted work was not in the worktree the continuation was handed")
	}

	// And a stall judged nothing, so the continuation is charged nothing: no
	// repair attempt on the run, and no review round on the item beyond the one
	// the continued run's own verdict bought.
	landed, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(landed.RepairContinuations) != 1 || !landed.RepairContinuations[0].Stall {
		t.Fatalf("continuations = %#v, want the one continuation recorded as a stall", landed.RepairContinuations)
	}
	if landed.RepairAttempts != 0 {
		t.Fatalf("repair attempts = %d, want a stall to count none", landed.RepairAttempts)
	}
	roundsAfter, err := store.Triage().Counters(tracker.item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if roundsAfter.ReviewRounds != roundsBefore.ReviewRounds {
		t.Fatalf("review rounds = %d, want the %d the item stood at: an approving verdict charges none and a stall judges nothing",
			roundsAfter.ReviewRounds, roundsBefore.ReviewRounds)
	}
}

// The same stall one step later: a first attempt that completed, passed its
// checks, and whose reviewer the harness then stopped on time. The sweep settles
// and dockets it with nothing handed back, exactly as it does a stall in the
// attempt — and before this the repair was refused, because only a stall in the
// developing phase was continuable, so a re-run discarding the finished branch
// was the only decision left. The entry now names the review as where the
// repair continues it, and the carry-out asks the review again on the change the
// run already has: no developer is invoked, and the branch the attempt produced
// is the one that lands.
func TestARepairContinuesAFirstAttemptStalledInItsReviewAtTheReview(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	stalling := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	stalling.run = stoppingReviewer(stalling.run, stalling.reviewerSession)
	checks := []string{"test -f feature.txt"}
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, stalling, checks), stalling)
	paused, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil || !paused.Paused || paused.ProviderStop != runstate.ProviderStopStalled {
		t.Fatalf("Run() error = %v, outcome = %#v, want the review stopped on time", err, paused)
	}
	stopped, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if stopped.Phase != runstate.PhaseReviewing || stopped.RepairAttempts != 0 || handedBackRepair(stopped) {
		t.Fatalf("stopped run = %#v, want a first attempt stopped at its review with nothing handed back", stopped)
	}
	tracker.item.Status = "in_progress"

	docket := &memoryDocket{}
	reconciler := Reconciler{
		Tracker:   tracker,
		Worktrees: newObserver(t, repository, worktreeRoot),
		Store:     store,
		Docket:    docketerOverStore(docket, store, pipeline.Config),
		Clock:     &pausingClock{now: stopped.UpdatedAt.Add(DefaultVanishedGrace)},
	}
	results, err := reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 1 || results[0].Action != ActionBlocked {
		t.Fatalf("reconciliation = %#v, want the stalled review settled after the grace", results)
	}
	settled, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if settled.Environmental == nil || settled.Environmental.Cause != runstate.CauseProcessVanished || settled.Phase != runstate.PhaseReviewing {
		t.Fatalf("settled run = %#v, want a vanished-process stoppage at the review", settled)
	}

	// The entry says the repair continues the run at its review, and names that
	// as the next mover's verb.
	if len(docket.entries) != 1 {
		t.Fatalf("docket = %#v, want the stalled review docketed once", docket.entries)
	}
	entry := docket.entries[0]
	if !entry.SessionResumable || entry.ResumesAt != string(runstate.PhaseReviewing) {
		t.Fatalf("entry = %#v, want the stall reported resumable at the review", entry)
	}
	rendered := entry.Render()
	for _, want := range []string{
		"`yoyo triage repair " + paused.RunID + "` continues the run at the reviewing phase",
		"with no developer attempt",
		"Next mover: you",
		"a repair (`yoyo triage repair " + paused.RunID + "`) continues it at the reviewing phase it stalled in",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("entry does not say %q:\n%s", want, rendered)
		}
	}

	if _, err := store.Triage().GrantRepair(context.Background(), tracker.item.ID, triageDecided(runstate.TriageDecisionRepair, paused.RunID),
		TriageRepairGrantRounds(pipeline.Config.Triage), time.Now(), TriageCaps(pipeline.Config.Execution, pipeline.Config.Triage)); err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
	worktrees, err := gitworktree.New(gitworktree.Options{Runner: execution.OSProcessRunner{}, RepositoryRoot: repository, WorktreeRoot: worktreeRoot})
	if err != nil {
		t.Fatalf("gitworktree.New() error = %v", err)
	}
	intake, err := runstate.NewIntakeHoldStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewIntakeHoldStore() error = %v", err)
	}
	// What the reviewer is shown is asked where it is handed over: a run that
	// lands removes its worktree, so the moment the review is asked again is the
	// only moment the attempt's change can be proved to be what it judges.
	reviewedTheAttempt := false
	continuing := roleBackend(func(request backend.RunRequest) error {
		return errors.New("a stall at the review is owed no developer attempt")
	}, approveVerdict)
	reviewing := continuing.run
	continuing.run = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleReviewer && request.WorkingDirectory == stopped.WorktreePath {
			if content, err := os.ReadFile(filepath.Join(request.WorkingDirectory, "feature.txt")); err == nil && string(content) == "implemented\n" {
				reviewedTheAttempt = true
			}
		}
		return reviewing(request)
	}
	continuer := RepairContinuer{
		Docket:             docket,
		Runs:               store,
		Intake:             intake,
		Decisions:          store.Triage(),
		Items:              tracker,
		Worktrees:          worktrees,
		ConfiguredAttempts: pipeline.Config.Execution.RepairAttemptsBeforeReplan,
		Capacity:           pipeline.Config.Execution.MaxConcurrentDevelopers,
		Start: func(ctx context.Context, workItemID, runID string) (Outcome, error) {
			return automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, continuing, checks), continuing).
				Continue(ctx, workItemID, runID)
		},
	}
	result, err := continuer.Continue(context.Background(), RepairContinueRequest{Run: paused.RunID})
	if err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	if !result.Continued || !result.Stall || result.ResumesAt != runstate.PhaseReviewing {
		t.Fatalf("result = %#v, want the stall continued at the review", result)
	}
	if result.Outcome.RunID != paused.RunID || result.Outcome.Integration == nil || !tracker.closed {
		t.Fatalf("result = %#v, closed = %t, want the same run's change landed", result, tracker.closed)
	}
	if !strings.Contains(result.Reason, "continued at the reviewing phase") || !strings.Contains(result.Render(), "at the reviewing phase, the step it stalled in") {
		t.Fatalf("result does not say it was continued at the review:\nreason: %s\n%s", result.Reason, result.Render())
	}

	// No developer attempt, and the review asked again on the attempt's change in
	// the preserved worktree.
	if developers := continuing.requestsForRole(domain.RoleDeveloper); len(developers) != 0 {
		t.Fatalf("developer invocations = %#v, want none for a stall at the review", developers)
	}
	if reviews := continuing.requestsForRole(domain.RoleReviewer); len(reviews) != 1 {
		t.Fatalf("review invocations = %d, want the review asked again once", len(reviews))
	}
	if !reviewedTheAttempt {
		t.Fatal("the review was not asked again on the completed attempt's change in the preserved worktree")
	}

	// The branch kept is the one that landed, and the continuation counted no
	// attempt.
	landed, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if landed.Branch != stopped.Branch || landed.WorktreePath != stopped.WorktreePath || landed.BaseCommit != stopped.BaseCommit {
		t.Fatalf("landed run = %#v, want the stalled run's own branch and worktree carried through", landed)
	}
	if landed.RepairAttempts != 0 || len(landed.RepairContinuations) != 1 || !landed.RepairContinuations[0].Stall {
		t.Fatalf("landed run = %#v, want one stall continuation and no repair attempt", landed)
	}
	if landed.Environmental != nil || landed.Blocker != "" {
		t.Fatalf("landed run = %#v, want the settled stoppage superseded", landed)
	}
}

// A stall at the review is continued only on the change it was stopped over:
// the continuation asks for it before the grant is spent, exactly as a repair
// does, rather than taking the exemption a stall mid-attempt has.
func TestAStallAtTheReviewIsHeldToTheRepairsContentCheck(t *testing.T) {
	t.Parallel()

	state := runstate.State{
		RunID:             "run-0123456789abcdef0123456789abcdef",
		WorkItemID:        "yoyodyne-task",
		Status:            runstate.StatusFailed,
		Phase:             runstate.PhaseReviewing,
		Blocker:           "settled",
		WorktreePath:      "/worktrees/yoyodyne-task",
		Branch:            "yoyodyne/yoyodyne-task/01234567",
		BaseCommit:        "base",
		TargetBranch:      "main",
		ProviderSessionID: "developer-session",
		Environmental:     &runstate.EnvironmentalRefusal{Cause: runstate.CauseProcessVanished, Settled: true},
	}
	if !continuableStall(state) || !stallResumesPastTheAttempt(state) || !resumesAnExistingChange(state) {
		t.Fatalf("a review-phase stall is continuable = %t, past the attempt = %t, owes a change = %t; want all three",
			continuableStall(state), stallResumesPastTheAttempt(state), resumesAnExistingChange(state))
	}
	if phase := continuedPhase(state, true); phase != runstate.PhaseReviewing {
		t.Fatalf("continued phase = %q, want the review it stalled in", phase)
	}
	state.Phase = runstate.PhaseChecking
	if !continuableStall(state) || continuedPhase(state, true) != runstate.PhaseChecking {
		t.Fatalf("a checks-phase stall is not continued at its checks")
	}
	// A run in its repair loop that stalls at its review or its checks had its
	// work judged before, and a failure returned: it is a repair's, not a stall's,
	// so nothing on its entry or its continuation says nothing was judged.
	for _, phase := range []runstate.Phase{runstate.PhaseReviewing, runstate.PhaseChecking} {
		repairing := state
		repairing.Phase = phase
		repairing.RepairAttempts = 1
		repairing.ReviewFindingDetails = []runstate.Finding{{Severity: "major", Message: "fix it"}}
		if continuableStall(repairing) || stallResumesPastTheAttempt(repairing) || continuedPhase(repairing, continuableStall(repairing)) != runstate.PhaseDeveloping {
			t.Fatalf("a %s-phase run carrying the reviewer's findings was admitted as a stall rather than a repair", phase)
		}
		if err := continuableRepair(repairing); err != nil {
			t.Fatalf("continuableRepair() = %v, want a repair-loop run still carried out as a repair", err)
		}
	}
	// A stall mid-attempt is still continued at the attempt, and still only where
	// nothing was handed back.
	state.Phase = runstate.PhaseDeveloping
	if !continuableStall(state) || stallResumesPastTheAttempt(state) || continuedPhase(state, true) != runstate.PhaseDeveloping {
		t.Fatalf("a developing-phase stall is not continued at its attempt")
	}
	state.CheckFailure = &runstate.CheckFailure{Command: "exit 1", ExitCode: 1}
	if continuableStall(state) {
		t.Fatal("a developing-phase run carrying a failing check was admitted as a stall rather than a repair")
	}
}

// A vanished process that left nothing behind is a round the item must not have
// paid for, and the repair grant that bought it is given back — which is the
// environmental class's own rule, applied to the one round nothing else will
// ever settle.
func TestAVanishedProcessThatDeliveredNothingReturnsItsGrant(t *testing.T) {
	t.Parallel()

	now := baseTime
	state := runstate.State{
		RunID:        "run-0123456789abcdef0123456789abcdef",
		WorkItemID:   "yoyodyne-task",
		Status:       runstate.StatusRunning,
		Phase:        runstate.PhaseDeveloping,
		UpdatedAt:    now.Add(-time.Hour),
		WorktreePath: "/worktrees/yoyodyne-task",
		Branch:       "yoyodyne/yoyodyne-task/01234567",
		BaseCommit:   "base",
		ProviderStop: runstate.ProviderStopStalled,
		RepairContinuations: []runstate.RepairContinuation{{
			GrantedAttempts: 2, Reason: "granted", ContinuedAt: now.Add(-2 * time.Hour),
		}},
	}
	clean := gitworktree.Observation{WorktreePresent: true, BranchExists: true, BranchCommit: "base"}
	refusal := vanishedRefusal(&state, clean, now)
	if err := refusal.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if !refusal.Refused || !refusal.GrantReturned || refusal.RoundReturned {
		t.Fatalf("refusal = %#v, want the round refused and the grant returned", refusal)
	}
	if state.CarriedOutRepairAttempts() != 0 {
		t.Fatalf("carried out = %d, want the returned grant not counted as carried out", state.CarriedOutRepairAttempts())
	}
	if !strings.Contains(refusal.Describe(), "granted repair round it consumed was returned") {
		t.Fatalf("Describe() = %q, want the return said", refusal.Describe())
	}

	// A branch that moved past the base is a delivery, however clean the
	// worktree is: the harness commits what a developer leaves before it advances.
	committed := runstate.State{RunID: state.RunID, WorktreePath: state.WorktreePath, BaseCommit: "base", ProviderStop: runstate.ProviderStopStalled, UpdatedAt: state.UpdatedAt}
	delivered := vanishedRefusal(&committed, gitworktree.Observation{WorktreePresent: true, BranchExists: true, BranchCommit: "ahead"}, now)
	if delivered.Refused || delivered.GrantReturned {
		t.Fatalf("refusal = %#v, want a round that delivered left spent", delivered)
	}
	if err := delivered.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}
