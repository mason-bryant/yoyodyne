package orchestrator

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The orphan the operator found, four times over: a run publishes a branch and
// opens a pull request, dies, and the attempt that replaces it publishes a
// different branch — the branch name carries the run — so nothing ever revisits
// the first request. It sits open with a green build and no queued merge,
// indistinguishable from pending work.
//
// The sweep closes it, names the vehicle the work actually landed by, and takes
// the remote branch it published with it.
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
	if len(convergence.Unsuperseded) != 0 {
		t.Errorf("unsuperseded = %#v, want nothing left for a person once the orphan is closed", convergence.Unsuperseded)
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
	// What the run kept locally is not this sweep's: the checkout is the checkout
	// sweep's, which holds a tail back, and the local branch carries the only
	// copy of work nothing promoted.
	if commit := publishedCommit(t, fixture.repository, orphan.Branch); commit == "" {
		t.Errorf("local branch %s was deleted, want it left to the branch sweep", orphan.Branch)
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
	if retired.PullRequest.State != "CLOSED" {
		t.Errorf("recorded state = %q, want the record to say what the forge now says", retired.PullRequest.State)
	}
	repeated := fixture.converge(t)
	if len(repeated.Publications) != 0 {
		t.Fatalf("second convergence = %#v, want nothing left to retire", repeated.Publications)
	}
	if len(fixture.forge.closed) != 1 {
		t.Errorf("closed = %#v after a second sweep, want one comment rather than one per pass", fixture.forge.closed)
	}
}

// The population the interim practice left behind: a request somebody closed by
// hand, whose branch they deleted with the forge's own button. The sweep has
// nothing to close and nothing to delete, and what matters is that it records
// the supersession anyway and settles — a retirement that failed on the absent
// branch would be reported on every later sweep forever, and the reconcile
// command would exit non-zero on each of them.
func TestConvergeRetiresAPublicationAlreadyClosedAndDeletedByHand(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	landed := fixture.run(t)
	fixture.forge.performQueuedMerge(t)
	if results := fixture.reconcile(t); len(results) != 1 || results[0].Action != ActionCompleted {
		t.Fatalf("reconciliation = %#v, want the landing run settled", results)
	}
	orphan := fixture.orphan(t, landed, 44)
	// Closed by hand, with the branch deleted from the forge. The record still
	// says what the run recorded at its death — open — which is what the record of
	// every hand-closed request said until the refresh sweep existed.
	fixture.forge.closable[orphan.Branch] = publish.PullRequest{Number: 44, State: "CLOSED"}
	runPipelineGit(t, fixture.repository, "push", "origin", "--delete", "refs/heads/"+orphan.Branch)

	convergence := fixture.converge(t)
	if len(convergence.Publications) != 1 {
		t.Fatalf("publications = %#v, want the hand-closed one retired", convergence.Publications)
	}
	swept := convergence.Publications[0]
	if swept.Failure != "" {
		t.Fatalf("sweep = %#v, want an absent branch read as already deleted rather than failing the retirement", swept)
	}
	if swept.Closed || !swept.BranchDeleted {
		t.Errorf("sweep = %#v, want nothing closed again and the branch reported gone", swept)
	}
	if len(fixture.forge.closed) != 0 {
		t.Errorf("closed = %#v, want no second comment on a request a person already closed", fixture.forge.closed)
	}
	retired, err := fixture.store.Load(orphan.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if retired.PullRequest.Superseded == "" {
		t.Fatalf("recorded supersession = %q, want the retirement recorded so the sweep settles", retired.PullRequest.Superseded)
	}
	if repeated := fixture.converge(t); len(repeated.Publications) != 0 {
		t.Errorf("second convergence = %#v, want nothing left to retire", repeated.Publications)
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
	if len(convergence.Unsuperseded) != 0 {
		t.Fatalf("unsuperseded = %#v, want an integrated run's publication left to the docket rather than named as residue", convergence.Unsuperseded)
	}
	if len(fixture.forge.closed) != 0 {
		t.Fatalf("closed = %#v, want nothing closed", fixture.forge.closed)
	}
}

// An item worked, closed, reopened and worked again has two runs and only the
// second one's open request is pending — the landing came first. Nothing about
// the first run supersedes it, so the ordering is part of the evidence rather
// than an assumption that the failed run must be the older one. It is named
// instead, with the ordering as the reason.
func TestConvergeLeavesAPublicationOpenedAfterTheLandingAlone(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	landed := fixture.run(t)
	fixture.forge.performQueuedMerge(t)
	if results := fixture.reconcile(t); len(results) != 1 || results[0].Action != ActionCompleted {
		t.Fatalf("reconciliation = %#v, want the landing run settled", results)
	}
	// The same orphan, except that it began after the landing run had finished.
	later := fixture.orphanStarted(t, landed, 44, time.Now().UTC().Add(time.Hour))

	convergence := fixture.converge(t)
	if len(convergence.Publications) != 0 {
		t.Fatalf("publications = %#v, want a request that postdates the landing left alone", convergence.Publications)
	}
	if len(fixture.forge.closed) != 0 {
		t.Fatalf("closed = %#v, want nothing closed", fixture.forge.closed)
	}
	if len(convergence.Unsuperseded) != 1 {
		t.Fatalf("unsuperseded = %#v, want the later publication named for a person", convergence.Unsuperseded)
	}
	open := convergence.Unsuperseded[0]
	if open.RunID != later.RunID || open.Number != 44 {
		t.Errorf("unsuperseded = %#v, want run %s and pull request 44 named", open, later.RunID)
	}
	if !strings.Contains(open.Reason, landed.RunID) || !strings.Contains(open.Reason, "before") {
		t.Errorf("reason = %q, want the landing that came first named as the reason", open.Reason)
	}
}

// A request the harness cannot close on its own records is named rather than
// left in silence: no run of its item ever landed, so the work is pending on the
// branch or landed by a vehicle nothing recorded, and which of the two it is
// decides whether the request is merged or closed. A request already closed at
// the forge is not named — there is nothing open to decide about.
func TestConvergeNamesTheOpenPublicationsItCannotSupersede(t *testing.T) {
	t.Parallel()

	fixture := newQueuedFixture(t)
	landed := fixture.run(t)
	fixture.forge.performQueuedMerge(t)
	if results := fixture.reconcile(t); len(results) != 1 || results[0].Action != ActionCompleted {
		t.Fatalf("reconciliation = %#v, want the landing run settled", results)
	}
	// Two failed runs of an item nothing has landed: one still open at the forge,
	// one a person closed, which the refresh has recorded.
	pending := fixture.orphanOf(t, landed, "yoyodyne-other", "run-aaaa1111aaaa1111aaaa1111aaaa1111", 51)
	closed := fixture.orphanOf(t, landed, "yoyodyne-other", "run-bbbb2222bbbb2222bbbb2222bbbb2222", 52)
	closed.PullRequest.State = "CLOSED"
	if err := fixture.store.Save(closed); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	convergence := fixture.converge(t)
	if len(convergence.Publications) != 0 || len(fixture.forge.closed) != 0 {
		t.Fatalf("publications = %#v, closed = %#v, want nothing closed on a guess", convergence.Publications, fixture.forge.closed)
	}
	if len(convergence.Unsuperseded) != 1 {
		t.Fatalf("unsuperseded = %#v, want the one request still open named and the closed one left out", convergence.Unsuperseded)
	}
	open := convergence.Unsuperseded[0]
	if open.RunID != pending.RunID || open.Number != 51 || open.WorkItemID != "yoyodyne-other" {
		t.Errorf("unsuperseded = %#v, want run %s, pull request 51 of yoyodyne-other", open, pending.RunID)
	}
	if !strings.Contains(open.Reason, "no run of yoyodyne-other has landed") || !strings.Contains(open.Reason, pending.Branch) {
		t.Errorf("reason = %q, want the missing landing and the branch the work is on named", open.Reason)
	}
	// Nothing was retired, so nothing was recorded: the request stays exactly as
	// open as it was until somebody decides.
	kept, err := fixture.store.Load(pending.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if kept.PullRequest.Superseded != "" || kept.PullRequest.State != "OPEN" {
		t.Errorf("record = %#v, want it untouched", kept.PullRequest)
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
	return f.deadRun(t, landed, f.tracker.item.ID, "run-11112222333344445555666677778888", number, started)
}

// orphanOf is a dead run of some other item, which is the shape of a request no
// landing of this harness supersedes.
func (f queuedFixture) orphanOf(t *testing.T, landed Outcome, workItemID, runID string, number int) runstate.State {
	t.Helper()
	return f.deadRun(t, landed, workItemID, runID, number, time.Now().UTC().Add(-2*time.Hour))
}

// deadRun records a run that published and then died before anything was
// judged, leaving the pair the operator found: the branch on the remote exactly
// as publishing left it, and the request the forge holds for it.
func (f queuedFixture) deadRun(t *testing.T, landed Outcome, workItemID, runID string, number int, started time.Time) runstate.State {
	t.Helper()
	short := runID[len("run-"):][:8]
	branch := fmt.Sprintf("yoyodyne/%s/%s", workItemID, short)
	// The branch sits on the remote exactly as publishing left it, which is what
	// the deletion is a compare-and-swap against.
	runPipelineGit(t, f.repository, "push", "origin", landed.BaseCommit+":refs/heads/"+branch)
	head := publishedCommit(t, f.remote, branch)
	// Locally the run left the pair the operator found: the branch, and a worktree
	// still checked out on it. Neither is this sweep's to remove, and the checkout
	// is what proves the remote deletion does not need it released first.
	root, err := filepath.EvalSymlinks(f.worktreeRoot)
	if err != nil {
		t.Fatalf("EvalSymlinks() error = %v", err)
	}
	worktreePath := filepath.Join(root, workItemID+"-"+short)
	runPipelineGit(t, f.repository, "branch", branch, landed.BaseCommit)
	runPipelineGit(t, f.repository, "worktree", "add", worktreePath, branch)
	completed := started.Add(time.Minute)
	state := runstate.State{
		SchemaVersion: runstate.StateSchemaVersion,
		RunID:         runID,
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		WorkItemID:    workItemID,
		WorkItemTitle: "Task",
		Backend:       "claude-code",
		Status:        runstate.StatusFailed,
		Phase:         runstate.PhaseDeveloping,
		StartedAt:     started,
		UpdatedAt:     completed,
		CompletedAt:   &completed,
		WorktreePath:  worktreePath,
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

// An item can be landed twice — worked, closed, reopened, worked again — and
// which landing is named decides whether a later run's orphan is seen at all.
// The ordering rule refuses a publication opened after the landing, so naming
// the earlier of the two would refuse an orphan the later one genuinely
// supersedes. The records are ordered so that the earlier landing is the one a
// sweep reads first, which is exactly the case that would go wrong.
func TestSupersededPublicationsNamesTheLatestLandingOfAnItem(t *testing.T) {
	t.Parallel()

	at := func(offset time.Duration) time.Time {
		return time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC).Add(offset)
	}
	ended := func(state runstate.State, when time.Time) runstate.State {
		state.UpdatedAt = when
		state.CompletedAt = &when
		return state
	}
	landing := func(runID string, when time.Time) runstate.State {
		return ended(runstate.State{
			RunID:      runID,
			WorkItemID: "yoyodyne-task",
			Status:     runstate.StatusSucceeded,
			StartedAt:  when.Add(-time.Hour),
			Integration: &runstate.Integration{
				TargetBranch: "main",
				SourceCommit: strings.Repeat(runID[len(runID)-1:], 40),
				TargetCommit: strings.Repeat(runID[len(runID)-1:], 40),
			},
		}, when)
	}
	// Sorted by run identifier, which is the order Recorded() reports: the first
	// landing is read before the orphan and before the landing that supersedes it.
	recorded := []runstate.State{
		landing("run-aaa1", at(0)),
		ended(runstate.State{
			RunID:      "run-bbb2",
			WorkItemID: "yoyodyne-task",
			Status:     runstate.StatusFailed,
			StartedAt:  at(time.Hour),
			Branch:     "yoyodyne/yoyodyne-task/bbb2",
			PullRequest: &runstate.PullRequest{
				Remote: "origin", Branch: "yoyodyne/yoyodyne-task/bbb2", Number: 77,
				URL: "https://example.invalid/pull/77", HeadCommit: strings.Repeat("b", 40), State: "OPEN",
			},
		}, at(2*time.Hour)),
		landing("run-ccc3", at(3*time.Hour)),
	}

	superseded, open := partitionPublications(recorded)
	if len(superseded) != 1 || len(open) != 0 {
		t.Fatalf("superseded = %#v, open = %#v, want the orphan between the two landings selected", superseded, open)
	}
	if superseded[0].state.RunID != "run-bbb2" {
		t.Fatalf("selected run = %q, want the orphan", superseded[0].state.RunID)
	}
	if superseded[0].by.RunID != "run-ccc3" {
		t.Errorf("vehicle = %q, want the latest landing rather than whichever was read first", superseded[0].by.RunID)
	}
}

// A run that has not ended is nobody's orphan yet, whatever its item's other
// runs did: a SIGTERM-killed run still recorded as running is the settle sweep's
// to make terminal first, and it is neither retired nor named here until it is.
func TestPartitionLeavesARunStillRecordedRunningAlone(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	landing := runstate.State{
		RunID: "run-land", WorkItemID: "yoyodyne-task", Status: runstate.StatusSucceeded,
		StartedAt: now, UpdatedAt: now.Add(time.Hour), CompletedAt: func() *time.Time { at := now.Add(time.Hour); return &at }(),
		Integration: &runstate.Integration{TargetBranch: "main", SourceCommit: strings.Repeat("a", 40), TargetCommit: strings.Repeat("a", 40)},
	}
	running := runstate.State{
		RunID: "run-dead", WorkItemID: "yoyodyne-task", Status: runstate.StatusRunning,
		StartedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
		PullRequest: &runstate.PullRequest{
			Remote: "origin", Branch: "yoyodyne/yoyodyne-task/dead", Number: 9,
			URL: "https://example.invalid/pull/9", HeadCommit: strings.Repeat("d", 40), State: "OPEN",
		},
	}
	superseded, open := partitionPublications([]runstate.State{landing, running})
	if len(superseded) != 0 || len(open) != 0 {
		t.Fatalf("superseded = %#v, open = %#v, want a run still recorded running left to the settle sweep", superseded, open)
	}
}

// Under a policy where a person approves the integration, a run succeeds with
// its pull request open and nothing integrated: that request is the deliverable,
// open because the policy says so, and naming it as a request in doubt on every
// sweep would list every pull request such a project has. It is still closed if
// a later run of the item lands, because then it really is superseded.
func TestPartitionLeavesTheDeliverableOfAHumanApprovedRunUnnamed(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	ended := now.Add(time.Hour)
	delivered := runstate.State{
		RunID: "run-done", WorkItemID: "yoyodyne-task", Status: runstate.StatusSucceeded,
		StartedAt: now, UpdatedAt: ended, CompletedAt: &ended,
		PullRequest: &runstate.PullRequest{
			Remote: "origin", Branch: "yoyodyne/yoyodyne-task/done", Number: 12,
			URL: "https://example.invalid/pull/12", HeadCommit: strings.Repeat("e", 40), State: "OPEN",
		},
	}
	superseded, open := partitionPublications([]runstate.State{delivered})
	if len(superseded) != 0 || len(open) != 0 {
		t.Fatalf("superseded = %#v, open = %#v, want a request waiting on a person's approval left unnamed", superseded, open)
	}

	later := ended.Add(time.Hour)
	landing := runstate.State{
		RunID: "run-land", WorkItemID: "yoyodyne-task", Status: runstate.StatusSucceeded,
		StartedAt: ended, UpdatedAt: later, CompletedAt: &later,
		Integration: &runstate.Integration{TargetBranch: "main", SourceCommit: strings.Repeat("a", 40), TargetCommit: strings.Repeat("a", 40)},
	}
	superseded, open = partitionPublications([]runstate.State{delivered, landing})
	if len(superseded) != 1 || superseded[0].state.RunID != "run-done" || len(open) != 0 {
		t.Fatalf("superseded = %#v, open = %#v, want the delivered request superseded once a later run of the item lands", superseded, open)
	}
}
