package orchestrator

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// gitAt runs one git command somewhere a fixture helper is not already bound.
func gitAt(t *testing.T, directory string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, arguments...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v: %s", arguments, directory, err, output)
	}
}

// The third settle outcome: the forge performed the merge and the finishing
// steps failed. The break on that path is the only thing keeping the local
// branch off a publication nothing confirmed, and this is what fails if it is
// removed.
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

// The orphan the operator found, four times over: a run publishes a branch and
// opens a pull request, dies, and the attempt that replaces it publishes a
// different branch — the branch name carries the run — so nothing ever revisits
// the first request. It sits open with a green build and no queued merge,
// indistinguishable from pending work.
//
// The sweep closes it, names the vehicle the work actually landed by, and takes
// the branch it published with it.
func TestConvergeClosesThePublicationARelaunchSuperseded(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	landed := fixture.run(t)
	fixture.forge.performQueuedMerge(t)
	if results := fixture.reconcile(t); len(results) != 1 || results[0].Action != ActionCompleted {
		t.Fatalf("reconciliation = %#v, want the landing run settled", results)
	}
	orphan := fixture.orphan(t, landed, 44)

	convergence := fixture.converge(t)
	if len(convergence.Publications) != 1 {
		t.Fatalf("publications = %#v, want the superseded one swept", convergence.Publications)
	}
	swept := convergence.Publications[0]
	if !swept.Closed || !swept.BranchDeleted || swept.Failure != "" {
		t.Fatalf("sweep = %#v, want the request closed and its branch deleted", swept)
	}
	if swept.RunID != orphan.RunID || swept.Number != 44 {
		t.Errorf("sweep = %#v, want run %s and pull request 44 named", swept, orphan.RunID)
	}
	if swept.SupersededBy.RunID != landed.RunID || swept.SupersededBy.Commit != landed.Integration.SourceCommit {
		t.Errorf("superseded by = %#v, want the run that landed the work (%s)", swept.SupersededBy, landed.RunID)
	}

	// The comment is what makes the close readable by whoever opened the request.
	if len(fixture.forge.closed) != 1 {
		t.Fatalf("closed = %#v, want exactly one request closed", fixture.forge.closed)
	}
	comment := fixture.forge.closed[0].Comment
	for _, expected := range []string{landed.RunID, landed.Integration.SourceCommit, orphan.RunID, "yoyodyne-task"} {
		if !strings.Contains(comment, expected) {
			t.Errorf("close comment does not name %q:\n%s", expected, comment)
		}
	}
	// The branch the closed request carried is the other half of the orphan.
	if commit := publishedCommit(t, fixture.remote, orphan.Branch); commit != "" {
		t.Errorf("remote branch %s is still at %q, want it deleted with the request", orphan.Branch, commit)
	}

	// The run's own record says which vehicle retired it, which is what stops the
	// next sweep asking the forge about a request it has already closed.
	retired, err := fixture.store.Load(orphan.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !strings.Contains(retired.PullRequest.Superseded, landed.RunID) {
		t.Errorf("recorded supersession = %q, want the vehicle that landed the work named", retired.PullRequest.Superseded)
	}
	repeated := fixture.converge(t)
	if len(repeated.Publications) != 0 {
		t.Fatalf("second convergence = %#v, want nothing left to retire", repeated.Publications)
	}
	if len(fixture.forge.closed) != 1 {
		t.Errorf("closed = %#v after a second sweep, want one comment rather than one per pass", fixture.forge.closed)
	}
}

// The publication of a run that *did* integrate is the opposite case and belongs
// to a person: the forge dropped a merge it had queued, something the base
// branch required went unmet, and the harness does not merge past a requirement.
// Closing it would retire a publication that is genuinely outstanding.
func TestConvergeLeavesThePublicationOfAnIntegratedRunAlone(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	fixture.run(t)
	fixture.forge.dropQueuedMerge()
	if results := fixture.reconcile(t); len(results) != 1 {
		t.Fatalf("reconciliation = %#v, want the dropped merge settled", results)
	}

	convergence := fixture.converge(t)
	if len(convergence.Publications) != 0 {
		t.Fatalf("publications = %#v, want an outstanding publication left for a person", convergence.Publications)
	}
	if len(fixture.forge.closed) != 0 {
		t.Fatalf("closed = %#v, want nothing closed", fixture.forge.closed)
	}
}

// An item worked, closed, reopened and worked again has two runs and only the
// second one's open request is pending — the landing came first. Nothing about
// the first run supersedes it, so the ordering is part of the evidence rather
// than an assumption that the failed run must be the older one.
func TestConvergeLeavesAPublicationOpenedAfterTheLandingAlone(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	landed := fixture.run(t)
	fixture.forge.performQueuedMerge(t)
	if results := fixture.reconcile(t); len(results) != 1 || results[0].Action != ActionCompleted {
		t.Fatalf("reconciliation = %#v, want the landing run settled", results)
	}
	// The same orphan, except that it began after the landing run had finished.
	fixture.orphanStarted(t, landed, 44, time.Now().UTC().Add(time.Hour))

	convergence := fixture.converge(t)
	if len(convergence.Publications) != 0 {
		t.Fatalf("publications = %#v, want a request that postdates the landing left alone", convergence.Publications)
	}
	if len(fixture.forge.closed) != 0 {
		t.Fatalf("closed = %#v, want nothing closed", fixture.forge.closed)
	}
}

// orphan records the run a relaunch left behind: an earlier attempt at the same
// item that published a branch and opened a pull request, and then died without
// integrating anything.
func (f queuedFixture) orphan(t *testing.T, landed Outcome, number int) runstate.State {
	t.Helper()
	return f.orphanStarted(t, landed, number, time.Now().UTC().Add(-2*time.Hour))
}

// orphanStarted is the same run with its start stated, because when the dead run
// began relative to the landing is part of what says its publication is
// superseded rather than pending.
func (f queuedFixture) orphanStarted(t *testing.T, landed Outcome, number int, started time.Time) runstate.State {
	t.Helper()
	const runID = "run-11112222333344445555666677778888"
	branch := "yoyodyne/yoyodyne-task/11112222"
	// The branch sits on the remote exactly as publishing left it, which is what
	// the deletion is a compare-and-swap against.
	runPipelineGit(t, f.repository, "push", "origin", landed.BaseCommit+":refs/heads/"+branch)
	head := publishedCommit(t, f.remote, branch)
	completed := started.Add(time.Minute)
	state := runstate.State{
		SchemaVersion: runstate.StateSchemaVersion,
		RunID:         runID,
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		WorkItemID:    f.tracker.item.ID,
		WorkItemTitle: f.tracker.item.Title,
		Backend:       "claude-code",
		Status:        runstate.StatusFailed,
		Phase:         runstate.PhaseDeveloping,
		StartedAt:     started,
		UpdatedAt:     completed,
		CompletedAt:   &completed,
		WorktreePath:  filepath.Join(f.worktreeRoot, "orphan"),
		Branch:        branch,
		BaseCommit:    landed.BaseCommit,
		TargetBranch:  "main",
		Failure:       "the process was killed before the change was judged",
		PullRequest: &runstate.PullRequest{
			Remote:     "origin",
			Branch:     branch,
			Number:     number,
			URL:        fmt.Sprintf("https://example.invalid/pull/%d", number),
			HeadCommit: head,
			State:      "OPEN",
		},
	}
	if err := f.store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	f.forge.holds(branch, number)
	return state
}
