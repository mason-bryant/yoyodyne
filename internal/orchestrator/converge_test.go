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

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
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

// The failure this bound exists for: registrations that accumulate with the
// harness's history until an agent's sandbox profile is too large to spawn a
// command with. A settled run keeps its checkout while it is recent, and a run a
// later one has already landed the work of gives its checkout up whenever it
// stopped.
func TestRetirableCheckoutsKeepATailAndTakeWhatASuccessorLanded(t *testing.T) {
	t.Parallel()

	opened := time.Date(2020, 1, 1, 9, 0, 0, 0, time.UTC)
	recorded := make([]runstate.State, 0, retainedSettledCheckouts+2)
	for index := 0; index <= retainedSettledCheckouts; index++ {
		recorded = append(recorded, settledCheckout(
			fmt.Sprintf("run-%032d", index),
			fmt.Sprintf("yoyodyne-item-%d", index),
			opened.Add(time.Duration(index)*time.Hour)))
	}
	oldest := recorded[0]
	newest := recorded[len(recorded)-1]
	// A later run of the newest item's work reached the target branch, which is
	// what answers what that checkout was holding.
	landed := settledCheckout("run-ffffffffffffffffffffffffffffffff", newest.WorkItemID, newest.StartedAt.Add(time.Hour))
	landed.WorktreeRemoved = true
	landed.Integration = &runstate.Integration{TargetBranch: "main"}
	recorded = append(recorded, landed)

	candidates := retirableCheckouts(recorded)
	if len(candidates) != 2 {
		t.Fatalf("candidates = %#v, want the oldest past the tail and the superseded one", candidates)
	}
	retirable := map[string]string{}
	for _, candidate := range candidates {
		retirable[candidate.state.RunID] = candidate.superseded
	}
	superseded, taken := retirable[newest.RunID]
	if !taken || superseded != landed.RunID {
		t.Errorf("the superseded checkout = %q, taken = %t, want it retired naming run %s", superseded, taken, landed.RunID)
	}
	if reason, taken := retirable[oldest.RunID]; !taken || reason != "" {
		t.Errorf("the oldest checkout = %q, taken = %t, want it retired for being past the tail", reason, taken)
	}
}

// A run that still owes a step is never a candidate, for the reason its branch
// is not: settling it may yet need what it left behind.
func TestRetirableCheckoutsLeaveARunThatStillOwesAStepAlone(t *testing.T) {
	t.Parallel()

	opened := time.Date(2020, 1, 1, 9, 0, 0, 0, time.UTC)
	inFlight := settledCheckout("run-0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e", "yoyodyne-in-flight", opened)
	inFlight.Status = runstate.StatusRunning
	inFlight.CompletedAt = nil
	landed := settledCheckout("run-0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d", inFlight.WorkItemID, opened.Add(time.Hour))
	landed.WorktreeRemoved = true
	landed.Integration = &runstate.Integration{TargetBranch: "main"}

	if candidates := retirableCheckouts([]runstate.State{inFlight, landed}); len(candidates) != 0 {
		t.Fatalf("candidates = %#v, want a run that owes a step left alone", candidates)
	}
}

// settledCheckout is a terminal run that left a checkout behind and nothing else
// to do.
func settledCheckout(runID, workItemID string, startedAt time.Time) runstate.State {
	completed := startedAt
	return runstate.State{
		RunID:        runID,
		WorkItemID:   workItemID,
		Status:       runstate.StatusFailed,
		StartedAt:    startedAt,
		UpdatedAt:    startedAt,
		CompletedAt:  &completed,
		WorktreePath: "/checkouts/" + runID,
	}
}

// The scope addition the publication sweep deliberately left open: it retires
// remote branches only, because a preserved checkout may hold work nothing else
// records. A run a later one has landed the work of is the case where something
// else does record it, so the checkout goes — and the run's own record says so,
// because `yoyo status` and the triage docket both read that record as the
// answer to whether the directory is still there.
func TestConvergeRetiresTheCheckoutASupersededRunPreserved(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	worktrees := newWorktreeManager(t, repository, worktreeRoot)
	stoppedRunID := "run-0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f"
	landedRunID := "run-11112222333344445555666677778888"
	worktree, err := worktrees.Create(context.Background(), gitworktree.CreateRequest{
		RunID:        stoppedRunID,
		WorkItemID:   tracker.item.ID,
		BaseRef:      "HEAD",
		TargetBranch: "main",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// Deliberately in the past: the sweep stamps UpdatedAt from the real clock as
	// it records the removal, and a record whose update predates its own start is
	// one the store refuses.
	stoppedAt := time.Date(2020, 1, 1, 9, 0, 0, 0, time.UTC)
	stopped := runstate.State{
		SchemaVersion: runstate.StateSchemaVersion,
		RunID:         stoppedRunID,
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		WorkItemID:    tracker.item.ID,
		Backend:       "claude-code",
		Status:        runstate.StatusFailed,
		Phase:         runstate.PhaseDeveloping,
		StartedAt:     stoppedAt,
		UpdatedAt:     stoppedAt,
		CompletedAt:   &stoppedAt,
		WorktreePath:  worktree.Path,
		Branch:        worktree.Branch,
		BaseCommit:    worktree.BaseCommit,
		TargetBranch:  "main",
	}
	if err := store.Create(stopped); err != nil {
		t.Fatalf("Create() stopped run error = %v", err)
	}
	landedAt := stoppedAt.Add(time.Hour)
	promoted := strings.Repeat("a", 40)
	landed := runstate.State{
		SchemaVersion:     runstate.StateSchemaVersion,
		RunID:             landedRunID,
		ProductID:         "yoyodyne",
		RepositoryID:      "yoyodyne",
		WorkItemID:        tracker.item.ID,
		Backend:           "claude-code",
		Status:            runstate.StatusSucceeded,
		Phase:             runstate.PhaseComplete,
		StartedAt:         landedAt,
		UpdatedAt:         landedAt,
		CompletedAt:       &landedAt,
		WorktreePath:      filepath.Join(worktreeRoot, "yoyodyne-task-11112222"),
		Branch:            "yoyodyne/yoyodyne-task/11112222",
		BaseCommit:        worktree.BaseCommit,
		TargetBranch:      "main",
		ProviderSessionID: "developer-session",
		ProviderModel:     "opus",
		ReviewSessionID:   "reviewer-session",
		ReviewModel:       "opus",
		ReviewDecision:    runstate.ReviewApprove,
		WorktreeRemoved:   true,
		BranchRemoved:     true,
		Integration: &runstate.Integration{
			TargetBranch:         "main",
			SourceCommit:         promoted,
			TargetCommit:         promoted,
			PreviousTargetCommit: worktree.BaseCommit,
		},
	}
	if err := store.Create(landed); err != nil {
		t.Fatalf("Create() landed run error = %v", err)
	}

	convergence, err := Reconciler{Tracker: tracker, Worktrees: worktrees, Store: store}.Converge(context.Background())
	if err != nil {
		t.Fatalf("Converge() error = %v", err)
	}
	if len(convergence.Checkouts) != 1 {
		t.Fatalf("checkouts = %#v, want the superseded run's checkout swept", convergence.Checkouts)
	}
	swept := convergence.Checkouts[0]
	if !swept.Removed || swept.Failure != "" || swept.Kept != "" {
		t.Fatalf("sweep = %#v, want the checkout retired", swept)
	}
	if swept.RunID != stoppedRunID || swept.Superseded != landedRunID || swept.Path != worktree.Path {
		t.Errorf("sweep = %#v, want run %s retired because run %s landed", swept, stoppedRunID, landedRunID)
	}
	if _, err := os.Stat(worktree.Path); !os.IsNotExist(err) {
		t.Errorf("the checkout is still on disk: %v", err)
	}
	// The record is the half every other reader asks. A removal nobody wrote down
	// sends `yoyo status` and the docket after a directory that is gone.
	recorded, err := store.Load(stoppedRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// Both facts are recorded, because they are two: this sweep removed the
	// checkout, and the run that landed the work is what answered for it.
	if !recorded.WorktreeRemoved || !recorded.CheckoutRetired || recorded.ArtifactsRetiredBy != landedRunID {
		t.Errorf("recorded = %#v, want the sweep's removal recorded against the run that superseded it", recorded)
	}
	// And the retirement claims one artifact. Whether the branch may go is a
	// question about the target that this claim never asks.
	if recorded.BranchRemoved {
		t.Errorf("recorded = %#v, want the retirement to claim the checkout and nothing else", recorded)
	}
	// Sweeping again is what every later reconcile does, and a checkout already
	// retired is not swept twice.
	repeated, err := Reconciler{Tracker: tracker, Worktrees: worktrees, Store: store}.Converge(context.Background())
	if err != nil {
		t.Fatalf("second Converge() error = %v", err)
	}
	if len(repeated.Checkouts) != 0 {
		t.Fatalf("second convergence = %#v, want nothing left to retire", repeated.Checkouts)
	}
}

// The other claim, driven the whole way through the sweep: nothing superseded
// this run and nobody decided anything about it, and its checkout goes because
// eight more recent settled ones have accumulated behind it. That is the claim
// the bound actually rests on — the registrations are a resource of the machine,
// and a stoppage nobody landed is still a path every agent's sandbox profile
// carries on every command it spawns.
func TestConvergeRetiresACheckoutPastTheTail(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	worktrees := newWorktreeManager(t, repository, worktreeRoot)
	oldestRunID := "run-0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a0a"
	worktree, err := worktrees.Create(context.Background(), gitworktree.CreateRequest{
		RunID:        oldestRunID,
		WorkItemID:   tracker.item.ID,
		BaseRef:      "HEAD",
		TargetBranch: "main",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	opened := time.Date(2020, 1, 1, 9, 0, 0, 0, time.UTC)
	recordSettledRun(t, store, oldestRunID, tracker.item.ID, opened, worktree)

	// Eight more recent settled checkouts, each of its own work item so nothing
	// supersedes anything. Their directories need not exist: what pushes the
	// oldest past the bound is how many more recent ones the records carry, and
	// only the one actually retired is ever looked for on disk.
	for index := 1; index <= retainedSettledCheckouts; index++ {
		recordSettledRun(t, store,
			fmt.Sprintf("run-%032d", index),
			fmt.Sprintf("yoyodyne-tail-%d", index),
			opened.Add(time.Duration(index)*time.Hour),
			gitworktree.Worktree{})
	}

	convergence, err := Reconciler{Tracker: tracker, Worktrees: worktrees, Store: store}.Converge(context.Background())
	if err != nil {
		t.Fatalf("Converge() error = %v", err)
	}
	if len(convergence.Checkouts) != 1 {
		t.Fatalf("checkouts = %#v, want only the one past the tail swept", convergence.Checkouts)
	}
	swept := convergence.Checkouts[0]
	if !swept.Removed || swept.Failure != "" || swept.Kept != "" {
		t.Fatalf("sweep = %#v, want the checkout retired", swept)
	}
	// It names no successor, because there is none: the tail is a bound on a
	// shared resource rather than a judgement about the work.
	if swept.RunID != oldestRunID || swept.Superseded != "" {
		t.Errorf("sweep = %#v, want run %s retired for being past the tail", swept, oldestRunID)
	}
	if _, err := os.Stat(worktree.Path); !os.IsNotExist(err) {
		t.Errorf("the checkout is still on disk: %v", err)
	}
	recorded, err := store.Load(oldestRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !recorded.WorktreeRemoved || !recorded.CheckoutRetired || recorded.ArtifactsRetiredBy != "" {
		t.Errorf("recorded = %#v, want the sweep's own claim and no successor named", recorded)
	}
}

// The state the operator's own by-hand clearance leaves: the checkout gone from
// disk and the record still saying it is there. It is the one a sweep must
// converge on rather than re-attempt, because it is what the affected machine is
// full of — and it is also what proves the two steps compose, since the prune is
// what clears the registration the retirement then finds nothing behind.
func TestConvergeConvergesOnACheckoutSomebodyAlreadyDeleted(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	worktrees := newWorktreeManager(t, repository, worktreeRoot)
	oldestRunID := "run-0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b0b"
	worktree, err := worktrees.Create(context.Background(), gitworktree.CreateRequest{
		RunID:        oldestRunID,
		WorkItemID:   tracker.item.ID,
		BaseRef:      "HEAD",
		TargetBranch: "main",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	opened := time.Date(2020, 1, 1, 9, 0, 0, 0, time.UTC)
	recordSettledRun(t, store, oldestRunID, tracker.item.ID, opened, worktree)
	for index := 1; index <= retainedSettledCheckouts; index++ {
		recordSettledRun(t, store,
			fmt.Sprintf("run-%032d", index),
			fmt.Sprintf("yoyodyne-tail-%d", index),
			opened.Add(time.Duration(index)*time.Hour),
			gitworktree.Worktree{})
	}
	// Somebody cleared it by hand, which is what an operator does when the sandbox
	// profile has grown too large to spawn a command with. Nothing told the record.
	if err := os.RemoveAll(worktree.Path); err != nil {
		t.Fatalf("RemoveAll() error = %v", err)
	}

	convergence, err := Reconciler{Tracker: tracker, Worktrees: worktrees, Store: store}.Converge(context.Background())
	if err != nil {
		t.Fatalf("Converge() error = %v", err)
	}
	if convergence.PruneFailure != "" {
		t.Fatalf("prune failed: %s", convergence.PruneFailure)
	}
	if len(convergence.Prune.Pruned) != 1 || convergence.Prune.Pruned[0] != worktree.Path {
		t.Fatalf("pruned = %#v, want the registration of the deleted checkout", convergence.Prune.Pruned)
	}
	if len(convergence.Checkouts) != 1 {
		t.Fatalf("checkouts = %#v, want the deleted checkout accounted for once", convergence.Checkouts)
	}
	// A checkout nobody will find is reported as gone rather than as a removal
	// this sweep could not make: the two are the same fact for a caller, and
	// treating them apart is what would re-attempt it for ever.
	swept := convergence.Checkouts[0]
	if !swept.Removed || swept.Failure != "" || swept.Kept != "" {
		t.Fatalf("sweep = %#v, want the already-deleted checkout recorded as gone", swept)
	}
	recorded, err := store.Load(oldestRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !recorded.WorktreeRemoved || !recorded.CheckoutRetired {
		t.Errorf("recorded = %#v, want the record brought onto what is actually there", recorded)
	}
	// The convergence the finding is about: the same candidate must not come back
	// on every later sweep.
	repeated, err := Reconciler{Tracker: tracker, Worktrees: worktrees, Store: store}.Converge(context.Background())
	if err != nil {
		t.Fatalf("second Converge() error = %v", err)
	}
	if len(repeated.Checkouts) != 0 {
		t.Fatalf("second convergence = %#v, want nothing left to retire", repeated.Checkouts)
	}
	if len(repeated.Prune.Pruned) != 0 {
		t.Errorf("second prune = %#v, want nothing stale left", repeated.Prune.Pruned)
	}
}

// newWorktreeManager builds the real worktree manager a sweep is given, which is
// what makes these tests act on a repository rather than on a double.
func newWorktreeManager(t *testing.T, repository, worktreeRoot string) *gitworktree.Manager {
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

// recordSettledRun writes a terminal run that left a checkout behind. Passing a
// created worktree records that one; passing the zero value records a checkout
// that is only ever counted, which is what the runs standing between an old one
// and the tail bound are for.
func recordSettledRun(t *testing.T, store *runstate.Store, runID, workItemID string, startedAt time.Time, worktree gitworktree.Worktree) {
	t.Helper()
	completed := startedAt
	state := runstate.State{
		SchemaVersion: runstate.StateSchemaVersion,
		RunID:         runID,
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		WorkItemID:    workItemID,
		Backend:       "claude-code",
		Status:        runstate.StatusFailed,
		Phase:         runstate.PhaseDeveloping,
		StartedAt:     startedAt,
		UpdatedAt:     startedAt,
		CompletedAt:   &completed,
		WorktreePath:  worktree.Path,
		Branch:        worktree.Branch,
		BaseCommit:    worktree.BaseCommit,
		TargetBranch:  "main",
	}
	if state.WorktreePath == "" {
		state.WorktreePath = "/checkouts/" + runID
		state.Branch = "yoyodyne/" + workItemID + "/" + strings.TrimPrefix(runID, "run-")[:8]
		state.BaseCommit = strings.Repeat("b", 40)
	}
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() run %s error = %v", runID, err)
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
