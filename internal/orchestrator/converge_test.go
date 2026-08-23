package orchestrator

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The seam the operator drew a line under: a merge the forge performs after its
// run is over leaves the primary checkout behind, and pulling it forward was a
// person's job. It is judgement-free — a fast-forward onto a commit that
// already contains the local branch — so the sweep takes it.
func TestConvergeCatchesUpATargetNoRunIsGoingToFinish(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	outcome := fixture.run(t)
	// The forge merges after the run is over, and nothing settles the run: the
	// sweep is driven on its own, so what it does here is its own work rather
	// than the settle path's. That is the case it exists for — a target branch
	// left behind the forge by something no run is going to finish.
	fixture.forge.performQueuedMerge(t)
	if local := publishedCommit(t, fixture.repository, "main"); local != outcome.Integration.TargetCommit {
		t.Fatalf("local main = %q, want the promoted commit %q before the sweep", local, outcome.Integration.TargetCommit)
	}

	convergence := fixture.converge(t)
	merge := publishedCommit(t, fixture.remote, "main")
	if len(convergence.Targets) != 1 || !convergence.Targets[0].Advanced || convergence.Targets[0].TargetBranch != "main" {
		t.Fatalf("targets = %#v, want main advanced", convergence.Targets)
	}
	if convergence.Targets[0].RemoteCommit != merge {
		t.Errorf("caught up to %q, want the forge's merge commit %q", convergence.Targets[0].RemoteCommit, merge)
	}
	if local := publishedCommit(t, fixture.repository, "main"); local != merge {
		t.Errorf("local main = %q, want the forge's merge commit %q", local, merge)
	}

	// Sweeping again is what every later `yoyo reconcile` does, and a repository
	// already level with the forge has nothing left to converge.
	repeated := fixture.converge(t)
	if len(repeated.Targets) != 1 || repeated.Targets[0].Advanced || repeated.Targets[0].Held != "" {
		t.Fatalf("second convergence = %#v, want nothing left to catch up", repeated)
	}
	if len(repeated.Branches) != 0 {
		t.Fatalf("second convergence = %#v, want no branch left to sweep", repeated)
	}
}

// Settling a merge is complete on its own. The convergence sweep runs in the
// same `yoyo reconcile` today, so a catch-up left to it would look identical
// from the command line — but it would make a converged checkout depend on who
// called what, and a caller that only settles runs would leave the branch
// silently behind. So the settle path catches up itself, and this drives
// Reconcile alone to prove it.
func TestReconcileSettlesAQueuedMergeAndCatchesTheTargetUpItself(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	outcome := fixture.run(t)
	fixture.forge.performQueuedMerge(t)

	results := fixture.reconcile(t)
	if len(results) != 1 || results[0].Action != ActionCompleted || results[0].Failure != "" {
		t.Fatalf("reconciliation = %#v, want the queued merge settled", results)
	}
	merge := publishedCommit(t, fixture.remote, "main")
	if merge == outcome.Integration.TargetCommit {
		t.Fatalf("remote main = %q, want the forge's merge commit above the promoted commit", merge)
	}
	catchup := results[0].Catchup
	if catchup == nil || !catchup.Advanced || catchup.Held != "" {
		t.Fatalf("catch-up = %#v, want main advanced by the settle itself", catchup)
	}
	if catchup.TargetBranch != "main" || catchup.RemoteCommit != merge {
		t.Errorf("catch-up = %#v, want main brought onto %q", catchup, merge)
	}
	// The whole point: no convergence sweep has run, and the checkout is level
	// with the forge anyway.
	if local := publishedCommit(t, fixture.repository, "main"); local != merge {
		t.Errorf("local main = %q, want the forge's merge commit %q without a sweep", local, merge)
	}
	if !strings.Contains(fixture.tracker.notes, "caught up to "+merge) {
		t.Errorf("tracker notes do not report the catch-up:\n%s", fixture.tracker.notes)
	}
}

// A merge the forge dropped is one of the two settle outcomes that reach a person. The
// publication is outstanding, nothing about it is confirmed, and the local
// branch must not be moved on it — deciding that is exactly what the harness is
// not allowed to do here.
func TestReconcileDoesNotCatchUpWhenTheForgeDroppedTheQueuedMerge(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	outcome := fixture.run(t)
	fixture.forge.dropQueuedMerge()

	results := fixture.reconcile(t)
	if len(results) != 1 {
		t.Fatalf("reconciliation = %#v, want the dropped merge settled", results)
	}
	if results[0].Catchup != nil {
		t.Fatalf("catch-up = %#v, want none for a publication nothing confirmed", results[0].Catchup)
	}
	if local := publishedCommit(t, fixture.repository, "main"); local != outcome.Integration.TargetCommit {
		t.Errorf("local main = %q, want it left at the promoted commit %q", local, outcome.Integration.TargetCommit)
	}
}

// Dead local branches are the other half of the same hygiene. A settled run
// whose branch survived — a cleanup that could not finish, an interruption
// between the two removals — leaves a branch whose work the target already
// carries, and nothing about deleting it is a decision.
func TestConvergeRemovesTheLeftoverBranchOfASettledRun(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	outcome := fixture.run(t)
	fixture.forge.performQueuedMerge(t)
	if settled := fixture.reconcile(t); len(settled) != 1 || settled[0].Action != ActionCompleted {
		t.Fatalf("reconciliation = %#v, want the queued merge settled", settled)
	}
	// The branch this run's cleanup removed, back where a cleanup that never
	// finished would have left it.
	runPipelineGit(t, fixture.repository, "branch", outcome.Branch, outcome.Integration.SourceCommit)

	convergence := fixture.converge(t)
	if len(convergence.Branches) != 1 {
		t.Fatalf("branches = %#v, want the leftover branch swept", convergence.Branches)
	}
	swept := convergence.Branches[0]
	if !swept.Removed || swept.Failure != "" || swept.Kept != "" {
		t.Fatalf("sweep = %#v, want the branch removed", swept)
	}
	if swept.Branch != outcome.Branch || swept.RunID != outcome.RunID || swept.TargetBranch != "main" {
		t.Errorf("sweep = %#v, want it to name run %q's branch %q", swept, outcome.RunID, outcome.Branch)
	}
	if branches := strings.TrimSpace(gitOutput(t, fixture.repository, "for-each-ref", "--format=%(refname)", "refs/heads/"+outcome.Branch)); branches != "" {
		t.Errorf("leftover branch survived: %q", branches)
	}
}

// A run that still owes a step is left entirely alone. Settling it may yet need
// the branch, and deciding that is reconciliation's rather than hygiene's — so a
// live developer's branch is never a candidate here.
func TestConvergeLeavesTheBranchOfARunThatStillOwesAStep(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	outcome := fixture.run(t)
	// The forge is still holding the merge, so the run is outstanding.
	runPipelineGit(t, fixture.repository, "branch", outcome.Branch, outcome.Integration.SourceCommit)

	convergence := fixture.converge(t)
	if len(convergence.Branches) != 0 {
		t.Fatalf("branches = %#v, want an outstanding run's branch left alone", convergence.Branches)
	}
	if commit := strings.TrimSpace(gitOutput(t, fixture.repository, "rev-parse", "refs/heads/"+outcome.Branch)); commit != outcome.Integration.SourceCommit {
		t.Errorf("branch = %q, want it left at %q", commit, outcome.Integration.SourceCommit)
	}
	// The target is still caught up, because a run in flight says nothing about
	// where the branch it will promote into belongs.
	if len(convergence.Targets) != 1 || convergence.Targets[0].TargetBranch != "main" {
		t.Errorf("targets = %#v, want main considered", convergence.Targets)
	}
}

// The composition the settle path's self-catch-up leans on: what the settle
// does not catch up — a remote that moved past the merge, evidence in the way
// — is finished by the sweep, and a sweep held on evidence finishes on the
// next pass once the evidence clears. Settle owning the common case is only
// safe because the sweep still owns the rest.
func TestACatchupHeldDuringSettleIsFinishedByALaterSweep(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	outcome := fixture.run(t)
	fixture.forge.performQueuedMerge(t)

	// Another machine's work lands on the remote target above the merge,
	// changing the same file the run shipped — and the primary checkout holds
	// an unsaved edit to that file, so catching up would overwrite it.
	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	if _, err := fixture.forge.git("worktree", "add", elsewhere, "main"); err != nil {
		t.Fatalf("open a worktree on the remote: %v", err)
	}
	if err := os.WriteFile(filepath.Join(elsewhere, "feature.txt"), []byte("another machine's change\n"), 0o600); err != nil {
		t.Fatalf("change the file remotely: %v", err)
	}
	gitAt(t, elsewhere, "-c", "user.name=Elsewhere", "-c", "user.email=elsewhere@example.invalid", "commit", "-am", "another machine's merge")
	if err := os.WriteFile(filepath.Join(fixture.repository, "feature.txt"), []byte("unsaved edit\n"), 0o600); err != nil {
		t.Fatalf("dirty the primary checkout: %v", err)
	}

	// The settle completes the run and leaves the moved target to the sweep.
	results := fixture.reconcile(t)
	if len(results) != 1 || results[0].Action != ActionCompleted {
		t.Fatalf("reconciliation = %#v, want the queued merge settled", results)
	}
	if catchup := results[0].Catchup; catchup != nil && catchup.Advanced {
		t.Fatalf("catch-up = %#v, want the settle to leave the moved target alone", catchup)
	}
	if local := publishedCommit(t, fixture.repository, "main"); local != outcome.Integration.TargetCommit {
		t.Fatalf("local main = %q, want it left at the promoted commit %q", local, outcome.Integration.TargetCommit)
	}

	// The sweep refuses on the evidence while the edit is in the way.
	held := fixture.converge(t)
	if len(held.Targets) != 1 || held.Targets[0].Advanced || held.Targets[0].Held == "" {
		t.Fatalf("convergence = %#v, want main held on the unsaved edit", held)
	}

	// The evidence clears, and the next sweep finishes the catch-up.
	if err := os.WriteFile(filepath.Join(fixture.repository, "feature.txt"), []byte("implemented\n"), 0o600); err != nil {
		t.Fatalf("restore the primary checkout: %v", err)
	}
	convergence := fixture.converge(t)
	if len(convergence.Targets) != 1 || !convergence.Targets[0].Advanced || convergence.Targets[0].Held != "" {
		t.Fatalf("convergence = %#v, want main caught up by the sweep", convergence)
	}
	remote := publishedCommit(t, fixture.remote, "main")
	if local := publishedCommit(t, fixture.repository, "main"); local != remote {
		t.Errorf("local main = %q, want the remote tip %q after the sweep", local, remote)
	}
}

// The accumulation this bound exists for: every worktree registration is a path
// an agent's sandbox profile denies on every command it spawns, so a machine
// that keeps them all eventually cannot spawn a command in the next worktree at
// all. Only the most recent settled runs keep their checkout; the rest are past
// the tail and swept.
func TestSweepableWorktreesHoldsBackTheMostRecentSettledRuns(t *testing.T) {
	t.Parallel()

	stoppedAt := baseTime
	settled := func(runID string) runstate.State {
		stoppedAt = stoppedAt.Add(time.Minute)
		completed := stoppedAt
		return runstate.State{
			RunID:        runID,
			WorkItemID:   "yoyodyne-task",
			Status:       runstate.StatusFailed,
			CompletedAt:  &completed,
			WorktreePath: "/worktrees/" + runID,
			Branch:       "yoyodyne/" + runID,
		}
	}
	// Three more settled runs than the tail holds, oldest first, so the store's
	// order is not what decides which ones survive.
	recorded := make([]runstate.State, 0, settledWorktreeTail+3)
	for index := 0; index < settledWorktreeTail+3; index++ {
		recorded = append(recorded, settled(fmt.Sprintf("run-%02d", index)))
	}
	// A run still in flight, a run whose record already says the checkout is
	// gone, and a run that never had one are none of this sweep's business.
	inFlight := settled("run-in-flight")
	inFlight.Status = runstate.StatusRunning
	inFlight.CompletedAt = nil
	cleanedUp := settled("run-cleaned-up")
	cleanedUp.WorktreeRemoved = true
	neverHadOne := settled("run-no-worktree")
	neverHadOne.WorktreePath = ""
	recorded = append(recorded, inFlight, cleanedUp, neverHadOne)

	swept := sweepableWorktrees(recorded)
	if len(swept) != 3 {
		t.Fatalf("sweepable = %#v, want the three runs past the tail", swept)
	}
	for _, state := range swept {
		switch state.RunID {
		case "run-00", "run-01", "run-02":
		default:
			t.Errorf("run %s was swept, want only the three oldest", state.RunID)
		}
	}
	// Nothing past the tail means nothing to sweep, which is the ordinary state
	// of a machine that is keeping up with itself.
	if held := sweepableWorktrees(recorded[:settledWorktreeTail]); len(held) != 0 {
		t.Fatalf("sweepable = %#v, want the whole tail held back", held)
	}
}

// What the sweep may and may not take. A checkout holding uncommitted work is
// the one thing nothing else records, so it is kept with the reason; one holding
// nothing is debris, and every commit it carried is still on the branch this
// deliberately does not touch.
func TestSweepingAWorktreeKeepsUncommittedWorkAndRetiresTheRest(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	halting := &haltingStore{StateStore: store, at: runstate.PhaseChecking}
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, halting, tracker, provider, []string{"exit 0"}), provider)
	if _, err := pipeline.Run(context.Background(), tracker.item.ID); err == nil || !halting.halted {
		t.Fatalf("interrupted Run() error = %v, halted = %t", err, halting.halted)
	}
	if results := reconcileSweep(t, repository, worktreeRoot, store, tracker); len(results) != 1 || results[0].Action != ActionBlocked {
		t.Fatalf("reconciliation = %#v, want the interrupted run blocked with its artifacts preserved", results)
	}
	settled, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	reconciler := Reconciler{Tracker: tracker, Worktrees: newObserver(t, repository, worktreeRoot), Store: store}

	kept, swept := reconciler.sweepWorktree(context.Background(), settled)
	if !swept || kept.Removed || !strings.Contains(kept.Kept, "uncommitted work") {
		t.Fatalf("sweep = %#v, swept = %t, want the developer's unsaved work kept", kept, swept)
	}
	if _, err := os.Stat(filepath.Join(settled.WorktreePath, "feature.txt")); err != nil {
		t.Fatalf("the uncommitted work was removed: %v", err)
	}

	// Somebody took what they wanted out of it, so the checkout now holds
	// nothing and the sweep may have it.
	if err := os.Remove(filepath.Join(settled.WorktreePath, "feature.txt")); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	retired, swept := reconciler.sweepWorktree(context.Background(), settled)
	if !swept || !retired.Removed || retired.Kept != "" || retired.Failure != "" || retired.RecordProblem != "" {
		t.Fatalf("sweep = %#v, swept = %t, want the empty checkout retired", retired, swept)
	}
	if _, err := os.Stat(settled.WorktreePath); !os.IsNotExist(err) {
		t.Errorf("the checkout is still on disk: %v", err)
	}
	// The branch is deliberately untouched: whether it may go is the branch
	// sweep's question and it needs the target to answer it.
	if branches := strings.TrimSpace(gitOutput(t, repository, "for-each-ref", "--format=%(refname)", "refs/heads/"+settled.Branch)); branches == "" {
		t.Error("the branch was deleted with the checkout")
	}

	// The removal is written onto the run it belongs to. `yoyo status`, the
	// docket, and a re-run all read that record as the answer to whether the
	// directory is there, so one that still said "preserved" would send every one
	// of them after a checkout that is gone.
	recorded, err := store.Load(settled.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !recorded.WorktreeRemoved || recorded.WorktreeSweptAt == nil {
		t.Fatalf("record = %#v, want the retirement written down", recorded)
	}
	if recorded.BranchRemoved {
		t.Error("the record claims the branch was removed, which the sweep never touches")
	}

	// Asking again says there was nothing there, so a sweep that runs on every
	// pass does not report the same long-gone checkout forever.
	if again, swept := reconciler.sweepWorktree(context.Background(), recorded); swept {
		t.Fatalf("sweep = %#v, want a checkout that is already gone reported as nothing to do", again)
	}
}

// The case the by-hand cleanup produces across many records at once: the
// checkout is gone and this sweep is not what removed it. Nothing needs printing
// — nobody has to read about a directory that was already not there — but the
// record does need writing, or every reader of that run keeps being sent to it
// and the run keeps occupying a slot in the tail it has no checkout to fill.
func TestSweepingRecordsACheckoutSomethingElseAlreadyRemoved(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	halting := &haltingStore{StateStore: store, at: runstate.PhaseChecking}
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, halting, tracker, provider, []string{"exit 0"}), provider)
	if _, err := pipeline.Run(context.Background(), tracker.item.ID); err == nil || !halting.halted {
		t.Fatalf("interrupted Run() error = %v, halted = %t", err, halting.halted)
	}
	if results := reconcileSweep(t, repository, worktreeRoot, store, tracker); len(results) != 1 || results[0].Action != ActionBlocked {
		t.Fatalf("reconciliation = %#v, want the interrupted run blocked with its artifacts preserved", results)
	}
	settled, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	// An operator clears the machine by hand: the checkout is removed and its
	// registration with it, exactly as `git worktree remove` does. The second
	// --force is what Git wants for a working tree the developer left unclean,
	// which is every preserved checkout worth removing by hand.
	runPipelineGit(t, repository, "worktree", "remove", "--force", "--force", settled.WorktreePath)
	if _, err := os.Stat(settled.WorktreePath); !os.IsNotExist(err) {
		t.Fatalf("the checkout is still on disk: %v", err)
	}

	reconciler := Reconciler{Tracker: tracker, Worktrees: newObserver(t, repository, worktreeRoot), Store: store}
	sweep, swept := reconciler.sweepWorktree(context.Background(), settled)
	if swept {
		t.Fatalf("sweep = %#v, want nothing reported for a checkout that was already gone", sweep)
	}
	recorded, err := store.Load(settled.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !recorded.WorktreeRemoved || recorded.WorktreeSweptAt == nil {
		t.Fatalf("record = %#v, want the run told its checkout is gone even though this sweep did not remove it", recorded)
	}
	// Which is what takes it out of the candidates, so it stops holding a slot in
	// the tail that was meant for a checkout somebody can still open. A full tail
	// of newer runs stands beside it: without the record this one is the oldest
	// candidate and would be probed again on every pass forever.
	beside := make([]runstate.State, 0, settledWorktreeTail+1)
	for index := 0; index < settledWorktreeTail; index++ {
		later := recorded.CompletedAt.Add(time.Duration(index+1) * time.Minute)
		beside = append(beside, runstate.State{
			RunID:        fmt.Sprintf("run-later-%02d", index),
			Status:       runstate.StatusFailed,
			CompletedAt:  &later,
			WorktreePath: fmt.Sprintf("/worktrees/later-%02d", index),
		})
	}
	if candidates := sweepableWorktrees(append(beside, recorded)); len(candidates) != 0 {
		t.Fatalf("candidates = %#v, want the run dropped once its record says the checkout is gone", candidates)
	}
}

// The registrations no sweep driven from run state can see: a checkout somebody
// deleted without telling Git. It costs every later command a deny path in its
// sandbox profile all the same, and the prune is what reaches it.
func TestConvergePrunesARegistrationWhoseCheckoutIsGone(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	fixture.run(t)
	stray := filepath.Join(t.TempDir(), "checkout-somebody-deleted")
	runPipelineGit(t, fixture.repository, "worktree", "add", "--detach", stray, "HEAD")
	if err := os.RemoveAll(stray); err != nil {
		t.Fatalf("RemoveAll() error = %v", err)
	}

	convergence := fixture.converge(t)
	if convergence.Registrations.Failure != "" {
		t.Fatalf("the prune failed: %s", convergence.Registrations.Failure)
	}
	if len(convergence.Registrations.Pruned) != 1 {
		t.Fatalf("pruned = %#v, want only the stale registration", convergence.Registrations.Pruned)
	}
	// The recorded path is Git's own, which resolves the symlinks a temporary
	// directory is reached through, so the name is what identifies it.
	if pruned := filepath.Base(convergence.Registrations.Pruned[0]); pruned != filepath.Base(stray) {
		t.Errorf("pruned %q, want the registration of %q", convergence.Registrations.Pruned[0], stray)
	}
	// Sweeping again finds nothing stale, so a sweep on every pass does not
	// repeat itself.
	repeated := fixture.converge(t)
	if len(repeated.Registrations.Pruned) != 0 || repeated.Registrations.Failure != "" {
		t.Fatalf("second prune = %#v, want nothing stale left", repeated.Registrations)
	}
}

// gitAt runs one git command somewhere a fixture helper is not already bound.
func gitAt(t *testing.T, directory string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, arguments...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v: %s", arguments, directory, err, output)
	}
}

// The second outcome that reaches a person: the forge performed the merge and
// the finishing steps failed. The break on that path is the only thing keeping
// the local branch off a publication nothing confirmed, and this is what fails
// if it is removed.
func TestASettleThatCannotFinishThePublicationLeavesTheLocalBranchAlone(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	fixture.forge.replayMerge = true
	outcome := fixture.run(t)
	// The forge merges as a replay: the remote's new tip carries the change's
	// content without carrying the promoted commit, so confirming the
	// publication honestly fails even though a merge really happened.
	fixture.forge.performQueuedMerge(t)

	results := fixture.reconcile(t)
	if len(results) != 1 {
		t.Fatalf("reconciliation = %#v, want the one queued merge examined", results)
	}
	if results[0].Catchup != nil {
		t.Fatalf("catch-up = %#v, want none on a publication that could not be confirmed", results[0].Catchup)
	}
	if local := publishedCommit(t, fixture.repository, "main"); local != outcome.Integration.TargetCommit {
		t.Errorf("local main = %q, want it still at the promoted commit %q", local, outcome.Integration.TargetCommit)
	}
}
