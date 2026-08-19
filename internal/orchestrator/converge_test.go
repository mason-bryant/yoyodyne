package orchestrator

import (
	"strings"
	"testing"
)

// The seam the operator drew a line under: a merge the forge performs after its
// run is over leaves the primary checkout behind, and pulling it forward was a
// person's job. It is judgement-free — a fast-forward onto a commit that
// already contains the local branch — so the sweep takes it.
func TestConvergeCatchesTheTargetUpToAMergeTheForgePerformedLater(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	outcome := fixture.run(t)
	fixture.forge.performQueuedMerge(t)
	if settled := fixture.reconcile(t); len(settled) != 1 || settled[0].Action != ActionCompleted {
		t.Fatalf("reconciliation = %#v, want the queued merge settled", settled)
	}
	// The run finished before the merge did, so nothing has caught the local
	// branch up and it is still exactly where the promotion left it.
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
