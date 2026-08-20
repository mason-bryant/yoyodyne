package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// The decision a development manager records when the ground moved under a
// correct change, and the guidance it leaves on the item for the developer that
// runs it again.
const (
	rerunReasoning = "the change is correct and its base moved under it: main took a rename this branch predates, so nothing here needs repairing and it needs running again from the start"
	// priorRunID is the stopped run in the end-to-end test, which has to be a
	// different run to the one the test pipeline reserves for the fresh attempt.
	priorRunID    = "run-99998888777766665555444433332222"
	rerunGuidance = "Triaged: to be run again from the start. The preserved branch yoyodyne/task/abc holds the finished rename; cherry-pick its second commit rather than writing it again."
)

// startedRun is one call of the re-run action's starter: what it was asked to
// run, and the selection it was asked to run it under.
type startedRun struct {
	workItemID string
	selection  runstate.Selection
}

// rerunHarness is the durable state a re-run acts on, held together so a test
// can drive one decision without rebuilding four stores.
type rerunHarness struct {
	docket  *memoryDocket
	runs    *runstate.Store
	intake  *runstate.IntakeHoldStore
	reruns  *runstate.RerunStore
	started []startedRun
	// outcome is what the starter reports, and failure what it returns. A test
	// that cares about what the action does after a run sets them.
	outcome Outcome
	failure error
	// retirement is what the fake retirer reports, and retired counts the times
	// it was asked. A harness that leaves it zero is one nothing should retire.
	retirement gitworktree.Retirement
	retired    int
	retireErr  error
}

func (h *rerunHarness) RetirePreserved(context.Context, gitworktree.Worktree, string) (gitworktree.Retirement, error) {
	h.retired++
	return h.retirement, h.retireErr
}

func (h *rerunHarness) rerunner() Rerunner {
	return Rerunner{
		Docket:    h.docket,
		Runs:      h.runs,
		Intake:    h.intake,
		Reruns:    h.reruns,
		Decisions: h.runs.Triage(),
		Preserved: h,
		Clock:     docketClock{},
		Start: func(_ context.Context, workItemID string, selection runstate.Selection) (Outcome, error) {
			h.started = append(h.started, startedRun{workItemID: workItemID, selection: selection})
			return h.outcome, h.failure
		},
	}
}

// newRerunHarness records one stopped run, dockets it, and leaves everything
// else as a fresh product: no hold, no re-run claimed, nothing in flight.
func newRerunHarness(t *testing.T, state runstate.State) *rerunHarness {
	t.Helper()
	root := t.TempDir()
	runs, err := runstate.NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewStore() error = %v", err)
	}
	if err := runs.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	intake, err := runstate.NewIntakeHoldStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewIntakeHoldStore() error = %v", err)
	}
	docket := &memoryDocket{}
	if _, err := docketerOver(nil, docket).RecordStoppedRun(state); err != nil {
		t.Fatalf("RecordStoppedRun() error = %v", err)
	}
	// The decision itself: the development manager recorded a re-run of this
	// item, which spent the item's re-run budget. That footprint is what the
	// action reads to know somebody decided this.
	recordRerunDecision(t, runs, state.WorkItemID)
	return &rerunHarness{
		docket: docket,
		runs:   runs,
		intake: intake,
		reruns: runs.Reruns(),
		outcome: Outcome{
			RunID:      "run-fedcba9876543210fedcba9876543210",
			WorkItemID: state.WorkItemID,
			Status:     runstate.StatusSucceeded,
		},
	}
}

// recordRerunDecision is what the development manager's triage does to the
// item's durable record when it decides a re-run: it spends the item's one
// re-run before anything acts on the decision.
func recordRerunDecision(t *testing.T, runs *runstate.Store, workItemID string) {
	t.Helper()
	if _, err := runs.Triage().RecordRerun(context.Background(), workItemID, docketedNow, rerunCaps); err != nil {
		t.Fatalf("RecordRerun() error = %v", err)
	}
}

// rerunCaps are the harness defaults the decision is recorded against: triage
// acts alone once per item, under the configured round cap.
var rerunCaps = runstate.TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 1, MergeRearms: 2}

// integrated is the fresh run landing its work, which is what retires what the
// stopped run preserved.
func (h *rerunHarness) integrated() {
	h.outcome.Integration = &gitworktree.Integration{
		TargetBranch: "main",
		SourceCommit: strings.Repeat("c", 40),
		TargetCommit: strings.Repeat("d", 40),
	}
	h.retirement = gitworktree.Retirement{
		Worktree: gitworktree.WorktreeRemoval{Path: "/state/worktrees/task", Removed: true},
		Branch:   gitworktree.Removal{Branch: "yoyodyne/task/abc", Removed: true},
	}
}

func rerunRequest() RerunRequest {
	return RerunRequest{Run: docketedRunID, Reason: rerunReasoning}
}

// The invariant's second half: work the harness chose accounts for itself. A
// re-run is the development manager choosing, so that is what the run records,
// in the words the decision was reasoned in.
func TestARerunRecordsTheTriageDecisionAsWhyTheRunExists(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	result, err := harness.rerunner().Rerun(context.Background(), rerunRequest())
	if err != nil {
		t.Fatalf("Rerun() error = %v", err)
	}
	if len(harness.started) != 1 || harness.started[0].workItemID != docketedItem {
		t.Fatalf("started = %#v, want the docketed item run once", harness.started)
	}
	selection := harness.started[0].selection
	if selection.By != runstate.SelectedByDevelopmentManager {
		t.Fatalf("selected by = %q, want the development manager whose triage chose it", selection.By)
	}
	if !strings.Contains(selection.Reason, rerunReasoning) {
		t.Fatalf("reason = %q, want the reasoning the decision was recorded with", selection.Reason)
	}
	// The stoppage it settles is named as well as the reasoning: a reason that
	// only carried the argument would not say which stopped run it was about.
	if !strings.Contains(selection.Reason, docketedRunID) {
		t.Fatalf("reason = %q, want the stopped run it settles named", selection.Reason)
	}
	if selection.Reason != result.Reason {
		t.Fatalf("recorded reason %q is not what the result reports (%q)", selection.Reason, result.Reason)
	}
	// A selection the run state would refuse would be a reason nothing records.
	if err := selection.Validate(); err != nil {
		t.Fatalf("the recorded selection is not one a run may carry: %v", err)
	}
}

// The invariant's first half. The development manager naming an item is not the
// operator naming it, so the hold applies — and nothing is claimed under one,
// which is what leaves the stoppage its one re-run for afterwards.
func TestAHeldIntakeStartsNoRerunAndSpendsNothing(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	held, err := harness.intake.Hold("the queue is heading somewhere odd", docketedNow)
	if err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	result, err := harness.rerunner().Rerun(context.Background(), rerunRequest())
	if err != nil {
		t.Fatalf("Rerun() error = %v, want a held intake reported rather than a failure", err)
	}
	if result.Started || len(harness.started) != 0 {
		t.Fatalf("started = %t / %#v, want nothing started under a hold", result.Started, harness.started)
	}
	if result.IntakeHeld == nil || !result.IntakeHeld.HeldAt.Equal(held.HeldAt) {
		t.Fatalf("intake held = %#v, want the hold that stopped it", result.IntakeHeld)
	}
	if _, claimed, err := harness.reruns.Find(triage.Key(triage.ClassStoppedRun, docketedRunID)); err != nil || claimed {
		t.Fatalf("claimed = %t, error = %v, want the stoppage to keep its re-run", claimed, err)
	}
	// Released, the same decision is carried out: the hold delayed the re-run
	// rather than consuming it.
	if _, _, err := harness.intake.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if _, err := harness.rerunner().Rerun(context.Background(), rerunRequest()); err != nil {
		t.Fatalf("Rerun() after the hold was released error = %v", err)
	}
	if len(harness.started) != 1 {
		t.Fatalf("started = %#v, want the re-run to have run once the hold was lifted", harness.started)
	}
}

// Triage acts on one stoppage once. The bound is the docket entry rather than
// the item, because what a second re-run of one stoppage means is that nobody
// decided anything new.
func TestOneDocketedStoppageIsRerunOnce(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	if _, err := harness.rerunner().Rerun(context.Background(), rerunRequest()); err != nil {
		t.Fatalf("Rerun() error = %v", err)
	}
	_, err := harness.rerunner().Rerun(context.Background(), rerunRequest())
	if !errors.Is(err, runstate.ErrRerunTaken) {
		t.Fatalf("second Rerun() error = %v, want the entry's re-run to be spent", err)
	}
	if len(harness.started) != 1 {
		t.Fatalf("started = %#v, want exactly one run for one stoppage", harness.started)
	}
}

// One decision buys one re-run, and the item's counter alone cannot say so: it
// is a total nothing clears, so a second stoppage of an already re-run item
// would pass it on the strength of a decision that was about the first stoppage
// and has already been carried out. What has been claimed is read back against
// what was decided, so the second stoppage is refused until somebody decides
// about it — which past the cap is an escalation rather than a larger budget.
func TestASecondStoppageOfAnAlreadyRerunItemIsRefused(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	if _, err := harness.rerunner().Rerun(context.Background(), rerunRequest()); err != nil {
		t.Fatalf("Rerun() error = %v", err)
	}
	// The fresh run stopped too, and its stoppage was docketed like any other.
	second := stoppedState()
	second.RunID = harness.outcome.RunID
	second.Blocker = "Yoyodyne stopped this item: the ground moved again."
	if err := harness.runs.Create(second); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := docketerOver(nil, harness.docket).RecordStoppedRun(second); err != nil {
		t.Fatalf("RecordStoppedRun() error = %v", err)
	}

	_, err := harness.rerunner().Rerun(context.Background(), RerunRequest{Run: second.RunID, Reason: rerunReasoning})
	if err == nil || !strings.Contains(err.Error(), "has carried out 1") {
		t.Fatalf("Rerun() error = %v, want a refusal naming the decision already carried out", err)
	}
	if len(harness.started) != 1 {
		t.Fatalf("started = %#v, want the item run again once in total", harness.started)
	}
	if _, claimed, _ := harness.reruns.Find(triage.Key(triage.ClassStoppedRun, second.RunID)); claimed {
		t.Fatalf("the refused second stoppage was claimed anyway")
	}
	// Deciding about the second stoppage is what makes it actionable — and the
	// cap is what makes that a person's decision rather than this action's.
	if _, err := harness.runs.Triage().RecordRerun(context.Background(), second.WorkItemID, docketedNow, rerunCaps); err == nil {
		t.Fatal("RecordRerun() gave a second re-run of one item, which the cap refuses")
	}
}

// The architect's condition: the stoppage has to be over, and it is proved from
// the run's own record rather than assumed from the entry being on the docket.
// The docket says what was true when it was written.
func TestARerunIsRefusedWhileAnythingOfTheStoppedRunIsStillLive(t *testing.T) {
	t.Parallel()

	for _, refusal := range []struct {
		name  string
		state func(runstate.State) runstate.State
		want  string
	}{
		{
			// A run that was docketed and has since been picked up again is owed
			// the rest of its own step, and a re-run would be a second developer
			// on one item.
			name: "the stopped run is running again",
			state: func(state runstate.State) runstate.State {
				state.Status = runstate.StatusRunning
				state.CompletedAt = nil
				return state
			},
			want: "resumable",
		},
		{
			// A run whose blocker was lifted stopped for a reason nobody has to
			// decide about any more.
			name: "the blocker no longer stands",
			state: func(state runstate.State) runstate.State {
				state.Blocker = ""
				return state
			},
			want: "no durable blocker",
		},
	} {
		t.Run(refusal.name, func(t *testing.T) {
			t.Parallel()

			// The docket entry is made from the stoppage as it was, and the run
			// record then moves on: that is exactly the disagreement this checks.
			harness := newRerunHarness(t, stoppedState())
			moved := refusal.state(stoppedState())
			if err := harness.runs.Save(moved); err != nil {
				t.Fatalf("Save() error = %v", err)
			}
			_, err := harness.rerunner().Rerun(context.Background(), rerunRequest())
			if err == nil || !strings.Contains(err.Error(), refusal.want) {
				t.Fatalf("Rerun() error = %v, want a refusal naming %q", err, refusal.want)
			}
			if len(harness.started) != 0 {
				t.Fatalf("started = %#v, want nothing started", harness.started)
			}
			if _, claimed, _ := harness.reruns.Find(triage.Key(triage.ClassStoppedRun, docketedRunID)); claimed {
				t.Fatalf("a refused re-run spent the stoppage's claim")
			}
		})
	}
}

// Two live runs of one item cannot exist. The reservation refuses the second
// anyway; refusing here is what keeps the collision from costing the stoppage
// its only re-run.
func TestARerunIsRefusedWhileTheItemHasARunInFlight(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	inFlight := stoppedState()
	inFlight.RunID = "run-11112222333344445555666677778888"
	inFlight.Status = runstate.StatusRunning
	inFlight.CompletedAt = nil
	inFlight.Blocker = ""
	if err := harness.runs.Create(inFlight); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	_, err := harness.rerunner().Rerun(context.Background(), rerunRequest())
	if err == nil || !strings.Contains(err.Error(), inFlight.RunID) {
		t.Fatalf("Rerun() error = %v, want a refusal naming the run in flight", err)
	}
	if len(harness.started) != 0 {
		t.Fatalf("started = %#v, want nothing started", harness.started)
	}
}

// A stoppage nothing docketed is not triage's to act on: the docket entry is
// what the one re-run is counted against.
func TestARerunIsRefusedForARunNothingDocketed(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	harness.docket.entries = nil
	_, err := harness.rerunner().Rerun(context.Background(), rerunRequest())
	if err == nil || !strings.Contains(err.Error(), "triage docket") {
		t.Fatalf("Rerun() error = %v, want a refusal naming the docket", err)
	}
}

// What the stopped run preserved is kept while the fresh run has not landed,
// and retired once it has. Both are recorded, because an artifact whose
// disposition nobody wrote down is the orphan nobody discovers.
func TestThePreservedArtifactsAreKeptUntilTheFreshRunIntegratesAndThenRetired(t *testing.T) {
	t.Parallel()

	t.Run("kept while nothing has landed", func(t *testing.T) {
		t.Parallel()

		harness := newRerunHarness(t, stoppedState())
		result, err := harness.rerunner().Rerun(context.Background(), rerunRequest())
		if err != nil {
			t.Fatalf("Rerun() error = %v", err)
		}
		if result.Preserved.Disposition != runstate.PreservedKept {
			t.Fatalf("disposition = %q, want the artifacts kept", result.Preserved.Disposition)
		}
		if harness.retired != 0 {
			t.Fatalf("retirements = %d, want nothing retired before the work landed", harness.retired)
		}
		recorded, found, err := harness.reruns.Find(result.DocketKey)
		if err != nil || !found {
			t.Fatalf("Find() = %t, error = %v", found, err)
		}
		if recorded.Preserved.Branch != "yoyodyne/task/abc" || recorded.Preserved.WorktreePath != "/state/worktrees/task" {
			t.Fatalf("recorded artifacts = %#v, want the two the stopped run preserved", recorded.Preserved)
		}
		if recorded.RunID != harness.outcome.RunID {
			t.Fatalf("recorded run = %q, want the fresh run it started", recorded.RunID)
		}
	})

	t.Run("retired once the work has landed", func(t *testing.T) {
		t.Parallel()

		harness := newRerunHarness(t, stoppedState())
		harness.integrated()
		result, err := harness.rerunner().Rerun(context.Background(), rerunRequest())
		if err != nil {
			t.Fatalf("Rerun() error = %v", err)
		}
		if harness.retired != 1 {
			t.Fatalf("retirements = %d, want the preserved artifacts retired once", harness.retired)
		}
		if result.Preserved.Disposition != runstate.PreservedRetired || result.Preserved.RetiredAt == nil {
			t.Fatalf("disposition = %#v, want a dated retirement", result.Preserved)
		}
		recorded, _, err := harness.reruns.Find(result.DocketKey)
		if err != nil {
			t.Fatalf("Find() error = %v", err)
		}
		if recorded.Preserved.Disposition != runstate.PreservedRetired {
			t.Fatalf("recorded disposition = %q, want the retirement durable", recorded.Preserved.Disposition)
		}
		// The stopped run's own record is what everything else in the harness
		// reads to find out whether those artifacts are still there, so the
		// removal is written onto it as well.
		stopped, err := harness.runs.Load(docketedRunID)
		if err != nil {
			t.Fatalf("Load() error = %v", err)
		}
		if !stopped.WorktreeRemoved || !stopped.BranchRemoved {
			t.Fatalf("run %s still advertises what was retired: worktree removed = %t, branch removed = %t (%s)",
				stopped.RunID, stopped.WorktreeRemoved, stopped.BranchRemoved, result.RecordProblem)
		}
		// The removal is evidence rather than an assertion: a stopped run
		// promoted nothing, so what earned it is the run that superseded it.
		if stopped.ArtifactsRetiredBy != harness.outcome.RunID {
			t.Fatalf("retired by = %q, want the fresh run that superseded it", stopped.ArtifactsRetiredBy)
		}
		if result.RecordProblem != "" {
			t.Fatalf("record problem = %q, want every record written", result.RecordProblem)
		}
	})

	t.Run("says so when the removal could not be written onto the stopped run", func(t *testing.T) {
		t.Parallel()

		harness := newRerunHarness(t, stoppedState())
		harness.integrated()
		rerunner := harness.rerunner()
		rerunner.Runs = unwritableRuns{RerunRuns: harness.runs}
		result, err := rerunner.Rerun(context.Background(), rerunRequest())
		if err != nil {
			t.Fatalf("Rerun() error = %v, want the run to stand and the record named", err)
		}
		// The artifacts really are gone, so the re-run's own record says retired.
		if result.Preserved.Disposition != runstate.PreservedRetired {
			t.Fatalf("disposition = %q, want the retirement that happened", result.Preserved.Disposition)
		}
		if !strings.Contains(result.RecordProblem, "still says otherwise") {
			t.Fatalf("record problem = %q, want the stale run record named", result.RecordProblem)
		}
	})

	t.Run("retires nothing while somebody else owns the stopped run", func(t *testing.T) {
		t.Parallel()

		harness := newRerunHarness(t, stoppedState())
		harness.integrated()
		// Another process is acting on the stopped run. Removing its artifacts
		// without being able to record the removal is the stale record this
		// refuses to create.
		_, lease, err := harness.runs.AdoptRun(context.Background(), docketedRunID)
		if err != nil {
			t.Fatalf("AdoptRun() error = %v", err)
		}
		defer lease.Release()

		result, err := harness.rerunner().Rerun(context.Background(), rerunRequest())
		if err != nil {
			t.Fatalf("Rerun() error = %v", err)
		}
		if harness.retired != 0 {
			t.Fatalf("retirements = %d, want nothing removed while another owner holds the run", harness.retired)
		}
		if result.Preserved.Disposition != runstate.PreservedKept {
			t.Fatalf("disposition = %q, want the artifacts kept", result.Preserved.Disposition)
		}
		if !strings.Contains(result.Preserved.Problem, "could not be taken") {
			t.Fatalf("problem = %q, want why nothing was retired", result.Preserved.Problem)
		}
	})

	t.Run("names only what survived a retirement that failed part way", func(t *testing.T) {
		t.Parallel()

		harness := newRerunHarness(t, stoppedState())
		harness.integrated()
		// The worktree went and the branch deletion then failed, which is what a
		// run with no recorded integration target does to a retirement.
		harness.retirement = gitworktree.Retirement{
			Worktree: gitworktree.WorktreeRemoval{Path: "/state/worktrees/task", Removed: true},
		}
		harness.retireErr = errors.New("integration target branch is required")
		result, err := harness.rerunner().Rerun(context.Background(), rerunRequest())
		if err != nil {
			t.Fatalf("Rerun() error = %v, want a retirement problem recorded rather than a failed re-run", err)
		}
		if result.Preserved.Disposition != runstate.PreservedKept {
			t.Fatalf("disposition = %q, want the branch that survived reported as kept", result.Preserved.Disposition)
		}
		if result.Preserved.WorktreePath != "" {
			t.Fatalf("preserved worktree = %q, want the one that was removed left unnamed", result.Preserved.WorktreePath)
		}
		if !strings.Contains(result.Preserved.Problem, "integration target branch is required") {
			t.Fatalf("problem = %q, want what stopped the retirement", result.Preserved.Problem)
		}
	})

	t.Run("kept with the reason when it could not be retired", func(t *testing.T) {
		t.Parallel()

		harness := newRerunHarness(t, stoppedState())
		harness.integrated()
		harness.retirement = gitworktree.Retirement{
			Worktree: gitworktree.WorktreeRemoval{Path: "/state/worktrees/task", Removed: true},
			Branch: gitworktree.Removal{
				Branch: "yoyodyne/task/abc",
				Kept:   "yoyodyne/task/abc is not contained in main, so it still carries work nothing has promoted",
			},
		}
		result, err := harness.rerunner().Rerun(context.Background(), rerunRequest())
		if err != nil {
			t.Fatalf("Rerun() error = %v", err)
		}
		if result.Preserved.Disposition != runstate.PreservedKept {
			t.Fatalf("disposition = %q, want what survived reported as kept", result.Preserved.Disposition)
		}
		if !strings.Contains(result.Preserved.Problem, "not contained in main") {
			t.Fatalf("problem = %q, want why the branch was kept", result.Preserved.Problem)
		}
		// The worktree did go, and a reader must not be sent after it.
		if result.Preserved.WorktreePath != "" || result.Preserved.Branch == "" {
			t.Fatalf("preserved = %#v, want only what actually survived", result.Preserved)
		}
	})
}

// The whole action over the real pipeline: the guidance the development manager
// recorded on the item reaches the developer that runs it again, and the run
// that reaches the durable record says the development manager chose it and why.
func TestARerunHandsTheDevelopmentManagersGuidanceToTheDeveloper(t *testing.T) {
	t.Parallel()

	pipelined := newPipelinedRerun(t, nil)
	result, err := pipelined.rerunner.Rerun(context.Background(), RerunRequest{Run: priorRunID, Reason: rerunReasoning})
	if err != nil {
		t.Fatalf("Rerun() error = %v", err)
	}
	if result.Outcome.Integration == nil {
		t.Fatalf("the fresh run did not integrate: %#v", result.Outcome)
	}
	developer := pipelined.provider.requestsForRole(domain.RoleDeveloper)
	if len(developer) != 1 {
		t.Fatalf("developer invocations = %d, want the fresh attempt", len(developer))
	}
	if !strings.Contains(developer[0].Prompt, rerunGuidance) {
		t.Fatalf("the developer was not given the triage guidance:\n%s", developer[0].Prompt)
	}
	fresh, err := pipelined.runs.Load(result.Outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if fresh.Selection == nil || fresh.Selection.By != runstate.SelectedByDevelopmentManager {
		t.Fatalf("recorded selection = %#v, want the development manager's", fresh.Selection)
	}
	if !strings.Contains(fresh.Selection.Reason, rerunReasoning) {
		t.Fatalf("recorded reason = %q, want the triage reasoning", fresh.Selection.Reason)
	}
	if fresh.Selection.At.IsZero() {
		t.Fatalf("recorded selection carries no moment: %#v", fresh.Selection)
	}
}

// A hold the operator places after the claim is taken still stops the run. That
// second reading is the enforcement — the action's own reading before the claim
// only keeps a held harness from spending the stoppage's re-run on a start that
// would decline — and it is the pipeline that makes it, on the same hold record
// the action read.
func TestAHoldArrivingAfterTheClaimStillStopsTheFreshRun(t *testing.T) {
	t.Parallel()

	var pipelined *pipelinedRerun
	pipelined = newPipelinedRerun(t, func() {
		// The operator holds intake while the claim is being taken, which is the
		// window the action itself cannot cover.
		if _, err := pipelined.intake.Hold("stop choosing while I reorder the queue", docketedNow); err != nil {
			t.Fatalf("Hold() error = %v", err)
		}
	})
	result, err := pipelined.rerunner.Rerun(context.Background(), RerunRequest{Run: priorRunID, Reason: rerunReasoning})
	if err != nil {
		t.Fatalf("Rerun() error = %v", err)
	}
	// The action started the run, and the run declined to claim anything.
	if !result.Started || result.Outcome.PausedByIntake == nil {
		t.Fatalf("outcome = %#v, want the fresh run held by intake", result.Outcome)
	}
	if result.Outcome.RunID != "" || result.Outcome.Integration != nil {
		t.Fatalf("outcome = %#v, want nothing reserved and nothing integrated", result.Outcome)
	}
	if invocations := len(pipelined.provider.requestsForRole(domain.RoleDeveloper)); invocations != 0 {
		t.Fatalf("developer invocations = %d, want none under a hold", invocations)
	}
}

// pipelinedRerun is the re-run action over the real pipeline: one repository,
// one state root, and one intake hold record read by both the action and the
// run it starts.
type pipelinedRerun struct {
	rerunner Rerunner
	runs     *runstate.Store
	intake   *runstate.IntakeHoldStore
	provider *fakeBackend
}

// newPipelinedRerun builds it over a stopped, docketed, decided-about run.
// beforeStart is called between the claim and the run, which is where a test
// puts something that arrives while the harness is committing to the work.
func newPipelinedRerun(t *testing.T, beforeStart func()) *pipelinedRerun {
	t.Helper()
	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{
		ID:     docketedItem,
		Title:  docketedTitle,
		Status: "open",
		// Where a triage decision lands: the item's notes, which every run's
		// context bundle carries to the developer.
		Notes: rerunGuidance,
	}}
	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})

	// One intake record for both readings: the pipeline reads it where it would
	// start the work, and the action reads it before it claims anything. A test
	// that wired them to two records would prove nothing about either, which is
	// why the pipeline's own is replaced here rather than left where the shared
	// fixture put it.
	root := t.TempDir()
	intake, err := runstate.NewIntakeHoldStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewIntakeHoldStore() error = %v", err)
	}
	pipeline.Intake = intake
	reruns, err := runstate.NewRerunStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewRerunStore() error = %v", err)
	}
	// The stoppage this decision is about, recorded in the same store the fresh
	// run is reserved in, so what is in flight is read from one place. It is a
	// different run to the one this pipeline will reserve, because the item has
	// been run twice: once into a stoppage, and once again on the decision.
	stopped := stoppedState()
	stopped.RunID = priorRunID
	if err := store.Create(stopped); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	docket := &memoryDocket{}
	if _, err := docketerOver(nil, docket).RecordStoppedRun(stopped); err != nil {
		t.Fatalf("RecordStoppedRun() error = %v", err)
	}
	recordRerunDecision(t, store, stopped.WorkItemID)

	return &pipelinedRerun{
		runs:     store,
		intake:   intake,
		provider: provider,
		rerunner: Rerunner{
			Docket:    docket,
			Runs:      store,
			Intake:    intake,
			Reruns:    reruns,
			Decisions: store.Triage(),
			Start: func(ctx context.Context, workItemID string, selection runstate.Selection) (Outcome, error) {
				if beforeStart != nil {
					beforeStart()
				}
				fresh := pipeline
				fresh.Selection = selection
				return fresh.Run(ctx, workItemID)
			},
		},
	}
}

// A re-run with no reasoning is refused rather than recorded: an accounted run
// is the whole of what the record is for.
func TestARerunWithoutTheDecisionsReasoningIsRefused(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	_, err := harness.rerunner().Rerun(context.Background(), RerunRequest{Run: docketedRunID})
	if err == nil || !strings.Contains(err.Error(), "reasoning") {
		t.Fatalf("Rerun() error = %v, want a refusal naming the missing reasoning", err)
	}
	if len(harness.started) != 0 {
		t.Fatalf("started = %#v, want nothing started", harness.started)
	}
}

// A reason longer than a run's recorded selection may hold is folded to fit
// rather than refusing to carry out the decision.
func TestALongDecisionIsFoldedIntoWhatTheRunCanRecord(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	request := rerunRequest()
	request.Reason = strings.Repeat("the ground moved. ", runstate.MaxSelectionReasonBytes/10)
	result, err := harness.rerunner().Rerun(context.Background(), request)
	if err != nil {
		t.Fatalf("Rerun() error = %v", err)
	}
	if len(result.Reason) > runstate.MaxSelectionReasonBytes {
		t.Fatalf("reason is %d bytes, which a run's selection would refuse", len(result.Reason))
	}
	if err := harness.started[0].selection.Validate(); err != nil {
		t.Fatalf("the folded selection is not one a run may carry: %v", err)
	}
}

// A record that could not be updated after the run never becomes silence: the
// run happened, and where its artifacts stand is what somebody has to be told.
func TestARecordThatCouldNotBeSettledIsReportedBesideTheRun(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	rerunner := harness.rerunner()
	rerunner.Reruns = unsettlableReruns{RerunRecords: harness.reruns}
	result, err := rerunner.Rerun(context.Background(), rerunRequest())
	if err != nil {
		t.Fatalf("Rerun() error = %v", err)
	}
	if !result.Started {
		t.Fatalf("the run was not started: %#v", result)
	}
	if !strings.Contains(result.RecordProblem, "could not be recorded") {
		t.Fatalf("record problem = %q, want the unrecorded disposition named", result.RecordProblem)
	}
}

// The reason a re-run records names the development manager, so the harness
// checks that one actually decided this rather than taking the caller's word:
// the decision spends the item's re-run budget as it is recorded, and an item
// carrying none is an item nobody decided this about.
func TestARerunNobodyDecidedIsRefused(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runs, err := runstate.NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewStore() error = %v", err)
	}
	if err := runs.Create(stoppedState()); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	intake, err := runstate.NewIntakeHoldStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewIntakeHoldStore() error = %v", err)
	}
	docket := &memoryDocket{}
	if _, err := docketerOver(nil, docket).RecordStoppedRun(stoppedState()); err != nil {
		t.Fatalf("RecordStoppedRun() error = %v", err)
	}
	started := 0
	rerunner := Rerunner{
		Docket: docket, Runs: runs, Intake: intake, Reruns: runs.Reruns(), Decisions: runs.Triage(),
		Start: func(context.Context, string, runstate.Selection) (Outcome, error) {
			started++
			return Outcome{}, nil
		},
	}
	_, err = rerunner.Rerun(context.Background(), rerunRequest())
	if err == nil || !strings.Contains(err.Error(), "no re-run of") {
		t.Fatalf("Rerun() error = %v, want a refusal naming the missing decision", err)
	}
	if started != 0 {
		t.Fatalf("started = %d, want nothing started on nobody's decision", started)
	}
	if _, claimed, _ := runs.Reruns().Find(triage.Key(triage.ClassStoppedRun, docketedRunID)); claimed {
		t.Fatalf("a re-run nobody decided spent the stoppage's claim")
	}
}

// The recorded reason separates what the harness verified from what it was
// told, because only one of the two is checkable.
func TestTheRecordedReasonSeparatesTheDecisionFromTheProseItWasGiven(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	result, err := harness.rerunner().Rerun(context.Background(), rerunRequest())
	if err != nil {
		t.Fatalf("Rerun() error = %v", err)
	}
	for _, want := range []string{
		"durable triage budget",
		"reasoning given to the harness when it was asked to",
		rerunReasoning,
	} {
		if !strings.Contains(result.Reason, want) {
			t.Fatalf("reason %q is missing %q", result.Reason, want)
		}
	}
}

// unwritableRuns reads exactly as the store does, including taking the stopped
// run's lease, and cannot write the removal back onto it.
type unwritableRuns struct {
	RerunRuns
}

func (unwritableRuns) Save(runstate.State) error {
	return errors.New("the run record is unwritable")
}

// unsettlableReruns claims exactly as the store does and then cannot write down
// what became of the run.
type unsettlableReruns struct {
	RerunRecords
}

func (unsettlableReruns) Settle(context.Context, string, string, runstate.PreservedArtifacts) (runstate.Rerun, error) {
	return runstate.Rerun{}, errors.New("the record is unwritable")
}

// A re-run wired without the parts it needs refuses before it reads anything.
func TestARerunnerWithoutItsPartsRefuses(t *testing.T) {
	t.Parallel()

	_, err := Rerunner{}.Rerun(context.Background(), rerunRequest())
	if err == nil {
		t.Fatal("Rerun() with nothing wired did not refuse")
	}
	for _, want := range []string{"triage docket", "intake hold", "one per docketed stoppage", "triage record", "start a run"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal is missing %q: %v", want, err)
		}
	}
}
