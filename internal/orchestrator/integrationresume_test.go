package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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

// resumeOwnership stands in for the three questions a resumption asks of the
// repository: whether the primary checkout is one a promotion can be made from,
// whether the preserved worktree is as the harness left it, and whether the
// change is still in it.
type resumeOwnership struct {
	fakeOwnership
	readyErr error
	asked    int
}

func (f *resumeOwnership) ValidateReady(context.Context) error {
	f.asked++
	return f.readyErr
}

// resumeHarness is the durable state a resumption acts on, held together so a
// test can drive one without rebuilding four stores.
type resumeHarness struct {
	docket    *memoryDocket
	runs      *runstate.Store
	intake    *runstate.IntakeHoldStore
	tracker   *fakeTracker
	ownership *resumeOwnership
	started   []continuedRun
	outcome   Outcome
	failure   error
	capacity  int
	// recorded is the run as it was created, which is what a refusal has to
	// leave.
	recorded runstate.State
}

func (h *resumeHarness) resumer() IntegrationResumer {
	return IntegrationResumer{
		Docket:    h.docket,
		Runs:      h.runs,
		Intake:    h.intake,
		Items:     h.tracker,
		Worktrees: h.ownership,
		Capacity:  h.capacity,
		Clock:     docketClock{},
		Start: func(_ context.Context, workItemID, runID string) (Outcome, error) {
			h.started = append(h.started, continuedRun{workItemID: workItemID, runID: runID})
			return h.outcome, h.failure
		},
	}
}

// approvedStoppedState is the run this action is about: one whose change the
// reviewer approved, whose promotion the environment then refused, with the
// branch, the worktree, and the session it stopped in all preserved. It is the
// shape yoyodyne-ifd.309's rerun stopped in on 2026-09-18, twice.
func approvedStoppedState() runstate.State {
	completed := docketedNow.Add(-time.Hour)
	return runstate.State{
		SchemaVersion:     runstate.StateSchemaVersion,
		RunID:             docketedRunID,
		ProductID:         "yoyodyne",
		RepositoryID:      "yoyodyne",
		WorkItemID:        docketedItem,
		WorkItemTitle:     docketedTitle,
		Backend:           "claude-code",
		Status:            runstate.StatusFailed,
		Phase:             runstate.PhaseIntegrating,
		StartedAt:         completed.Add(-time.Hour),
		UpdatedAt:         completed,
		CompletedAt:       &completed,
		WorktreePath:      "/state/worktrees/task",
		Branch:            "yoyodyne/task/abc",
		BaseCommit:        strings.Repeat("a", 40),
		HarnessCommit:     strings.Repeat("c", 40),
		TargetBranch:      "main",
		ProviderSessionID: "developer-session",
		ProviderModel:     "opus",
		ReviewSessionID:   "reviewer-session",
		ReviewModel:       "opus",
		ReviewDecision:    runstate.ReviewApprove,
		ReviewApproves:    "implementation",
		ReviewSummary:     "the change matches the acceptance criteria",
		ReviewRounds:      1,
		RepairAttempts:    1,
		Failure:           "integrate approved change: primary checkout is not ready for integration: the primary checkout is not as the harness left it: primary repository has uncommitted changes: AGENTS.md",
		Environmental:     &runstate.EnvironmentalRefusal{Cause: runstate.CauseDirtyPrimary, RecordedAt: completed, Settled: true},
		IntegrationStop: &runstate.IntegrationStop{
			Cause:      runstate.CauseDirtyPrimary,
			Detail:     "integrate approved change: primary checkout is not ready for integration",
			Phase:      runstate.PhaseIntegrating,
			RecordedAt: completed,
		},
	}
}

func newResumeHarness(t *testing.T, state runstate.State) *resumeHarness {
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
	return &resumeHarness{
		docket:  docket,
		runs:    runs,
		intake:  intake,
		tracker: &fakeTracker{item: beads.WorkItem{ID: state.WorkItemID, Title: state.WorkItemTitle, Status: "in_progress"}},
		// The checkout is clean again, which is the ordinary case: the operator
		// committed the edit that stopped the run.
		ownership: &resumeOwnership{},
		capacity:  2,
		outcome:   Outcome{RunID: state.RunID, WorkItemID: state.WorkItemID, Status: runstate.StatusSucceeded},
		recorded:  state,
	}
}

func (h *resumeHarness) reload(t *testing.T) runstate.State {
	t.Helper()
	state, err := h.runs.Load(docketedRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return state
}

func resumeRequest() IntegrationResumeRequest {
	return IntegrationResumeRequest{Run: docketedRunID}
}

// assertNothingWritten is what every refusal has to leave: the run exactly as it
// stopped, the item untouched, and nothing dispatched.
func (h *resumeHarness) assertNothingWritten(t *testing.T) {
	t.Helper()
	state := h.reload(t)
	if state.Status != runstate.StatusFailed || len(state.IntegrationResumptions) != 0 || (state.IntegrationStop == nil) != (h.recorded.IntegrationStop == nil) {
		t.Fatalf("a refused resumption changed the run: %s/%s, resumptions %#v, stop %#v", state.Status, state.Phase, state.IntegrationResumptions, state.IntegrationStop)
	}
	if len(h.tracker.calls) != 0 {
		t.Fatalf("a refused resumption wrote to the item: %v", h.tracker.calls)
	}
	if len(h.started) != 0 {
		t.Fatalf("a refused resumption dispatched something: %#v", h.started)
	}
	if _, closed := h.docket.closed[triage.Key(triage.ClassStoppedRun, docketedRunID)]; closed {
		t.Fatal("a refused resumption closed the docket entry")
	}
}

// What the action is for: the same run goes on at the promotion it stopped
// short of, with its approval standing, and nothing about it is counted as an
// attempt or a round.
func TestAResumptionMakesTheStoppedRunLiveAtItsPromotionChargingNothing(t *testing.T) {
	t.Parallel()

	harness := newResumeHarness(t, approvedStoppedState())
	result, err := harness.resumer().Resume(context.Background(), resumeRequest())
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if !result.Resumed || len(harness.started) != 1 || harness.started[0] != (continuedRun{workItemID: docketedItem, runID: docketedRunID}) {
		t.Fatalf("started = %#v, resumed = %t, want the docketed run continued once", harness.started, result.Resumed)
	}
	if result.Cause != runstate.CauseDirtyPrimary || !strings.Contains(result.Stopped, "dirty-primary") {
		t.Fatalf("result = %#v, want the stop it supersedes named", result)
	}
	state := harness.reload(t)
	if state.Status != runstate.StatusRunning || state.Phase != runstate.PhaseIntegrating || state.CompletedAt != nil {
		t.Fatalf("resumed run = %s/%s completed %v, want it running at the integrating phase", state.Status, state.Phase, state.CompletedAt)
	}
	if !state.ResumingIntegration() {
		t.Fatalf("resumed run does not read as resuming its integration: %#v", state)
	}
	// The approval stands exactly as the reviewer left it.
	if state.ReviewDecision != runstate.ReviewApprove || state.ReviewSessionID != "reviewer-session" || state.ReviewApproves != "implementation" {
		t.Fatalf("resumed run lost its approval: decision %q session %q approves %q", state.ReviewDecision, state.ReviewSessionID, state.ReviewApproves)
	}
	// Nothing was counted: not an attempt, not a round, not a grant.
	if state.RepairAttempts != 1 || state.ReviewRounds != 1 || len(state.RepairContinuations) != 0 {
		t.Fatalf("resumed run was charged: attempts %d rounds %d continuations %#v", state.RepairAttempts, state.ReviewRounds, state.RepairContinuations)
	}
	counters, err := harness.runs.Triage().Counters(docketedItem)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if counters.ReviewRounds != 0 || counters.RepairGrants != 0 || counters.Reruns != 0 || counters.CommittedRounds != 0 {
		t.Fatalf("the item's triage record was spent by a resumption: %#v", counters)
	}
	// The resumption is a continuation on the record, carrying what it superseded.
	if len(state.IntegrationResumptions) != 1 {
		t.Fatalf("resumptions = %#v, want the one this made", state.IntegrationResumptions)
	}
	resumed := state.IntegrationResumptions[0]
	if resumed.Cause != runstate.CauseDirtyPrimary || !strings.Contains(resumed.SupersededFailure, "uncommitted changes: AGENTS.md") {
		t.Fatalf("resumption = %#v, want the stop's cause and failure carried", resumed)
	}
	if !strings.Contains(resumed.Reason, "No review round, repair grant, or re-run was spent") {
		t.Fatalf("reason = %q, want it to say what the resumption cost", resumed.Reason)
	}
	if state.IntegrationStop != nil || state.Failure != "" || state.Blocker != "" || state.Environmental != nil {
		t.Fatalf("resumed run still carries its stop: stop %#v failure %q blocker %q environmental %#v", state.IntegrationStop, state.Failure, state.Blocker, state.Environmental)
	}
	// The item was told and put back, and the docket entry closed in the
	// harness's name rather than left for the development manager to decide.
	if !strings.Contains(harness.tracker.notes, "Resumed: the integration of run "+docketedRunID) || !harness.tracker.claimed {
		t.Fatalf("item notes = %q, claimed = %t; want the resumption recorded and the item put back", harness.tracker.notes, harness.tracker.claimed)
	}
	closure, closed := harness.docket.closed[triage.Key(triage.ClassStoppedRun, docketedRunID)]
	if !closed || closure.Decision != resumedDocketDecision || !strings.Contains(closure.DecidedBy, "the harness") {
		t.Fatalf("docket closure = %#v, want the stoppage settled as resumed by the harness", closure)
	}
}

// Reasoning given to the command is recorded beside the harness's own account,
// attributed to the command rather than quoted as anybody's decision.
func TestAResumptionRecordsReasoningItWasGivenBesideItsOwnAccount(t *testing.T) {
	t.Parallel()

	harness := newResumeHarness(t, approvedStoppedState())
	request := resumeRequest()
	request.Reason = "the AGENTS.md edit landed as PR 524, so the checkout is clean again"
	if _, err := harness.resumer().Resume(context.Background(), request); err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	reason := harness.reload(t).IntegrationResumptions[0].Reason
	if !strings.Contains(reason, "The reasoning given to the harness when it was asked to: the AGENTS.md edit landed") {
		t.Fatalf("reason = %q, want the reasoning attributed to the command", reason)
	}
}

// A run the environment did not stop is somebody's to decide about, and every
// shape of that is refused naming what it is rather than promoted on a record
// that says something else.
func TestAResumptionIsRefusedForARunThatIsNotAnApprovedChangeTheEnvironmentStopped(t *testing.T) {
	t.Parallel()

	for name, shape := range map[string]func(runstate.State) runstate.State{
		"the environment did not stop it": func(state runstate.State) runstate.State {
			state.IntegrationStop = nil
			state.Environmental = nil
			state.Failure = "integration lost its target branch after 2 of 2 permitted retry(s)"
			return state
		},
		"it was never approved": func(state runstate.State) runstate.State {
			state.IntegrationStop = nil
			state.Environmental = nil
			state.ReviewDecision = runstate.ReviewRepair
			state.ReviewApproves = ""
			state.Phase = runstate.PhaseReviewing
			state.ReviewFindings = 1
			state.ReviewFindingDetails = []runstate.Finding{{Severity: "blocker", Message: "add the missing file"}}
			state.Blocker = "Yoyodyne stopped this item: the repair budget was spent."
			return state
		},
		"its worktree was retired": func(state runstate.State) runstate.State {
			state.WorktreeRemoved = true
			swept := docketedNow.Add(-30 * time.Minute)
			state.WorktreeSweptAt = &swept
			return state
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			harness := newResumeHarness(t, shape(approvedStoppedState()))
			_, err := harness.resumer().Resume(context.Background(), resumeRequest())
			if !errors.Is(err, ErrNotResumable) {
				t.Fatalf("Resume() error = %v, want the run refused as not resumable", err)
			}
			harness.assertNothingWritten(t)
		})
	}
}

// The checkout is what stopped the run, and it is asked before anything is
// written: a run made live again into the same refusal would stop again with
// its record saying it was resumed.
func TestAResumptionIsRefusedWhileTheCheckoutIsStillNotReady(t *testing.T) {
	t.Parallel()

	harness := newResumeHarness(t, approvedStoppedState())
	harness.ownership.readyErr = fmt.Errorf("%w: primary repository has uncommitted changes: AGENTS.md", gitworktree.ErrPrimaryNotReady)
	_, err := harness.resumer().Resume(context.Background(), resumeRequest())
	if !errors.Is(err, ErrCheckoutNotReady) || !strings.Contains(err.Error(), "AGENTS.md") {
		t.Fatalf("Resume() error = %v, want the checkout refused naming what it carries", err)
	}
	harness.assertNothingWritten(t)
}

// What is promoted is whatever is in the worktree, so a worktree somebody has
// been in, or one that lost the change, refuses to a person exactly as a repair
// does.
func TestAResumptionRefusesAWorktreeThatIsNotAsTheHarnessLeftItOrIsEmpty(t *testing.T) {
	t.Parallel()

	touched := newResumeHarness(t, approvedStoppedState())
	touched.ownership.err = errors.New("worktree HEAD is deadbeef, want the commit the harness recorded")
	if _, err := touched.resumer().Resume(context.Background(), resumeRequest()); !errors.Is(err, ErrWorktreeNotAsLeft) {
		t.Fatalf("Resume() error = %v, want the touched worktree refused", err)
	}
	touched.assertNothingWritten(t)

	emptied := newResumeHarness(t, approvedStoppedState())
	emptied.ownership.changed = []string{}
	if _, err := emptied.resumer().Resume(context.Background(), resumeRequest()); !errors.Is(err, ErrPreservedChangeMissing) {
		t.Fatalf("Resume() error = %v, want the empty worktree refused", err)
	}
	emptied.assertNothingWritten(t)
}

// A held intake and a full harness are waits rather than refusals, and neither
// writes anything.
func TestAResumptionWaitsOnAHeldIntakeOrAFullHarness(t *testing.T) {
	t.Parallel()

	held := newResumeHarness(t, approvedStoppedState())
	if _, err := held.intake.Hold(runstate.IntakeHolderOperator, "the queue is heading somewhere odd", docketedNow); err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	result, err := held.resumer().Resume(context.Background(), resumeRequest())
	if err != nil || result.Resumed || result.IntakeHeld == nil {
		t.Fatalf("Resume() = %#v, error = %v; want the hold reported and nothing resumed", result, err)
	}
	held.assertNothingWritten(t)

	full := newResumeHarness(t, approvedStoppedState())
	full.capacity = 1
	other := approvedStoppedState()
	other.RunID = "run-ffffffffffffffffffffffffffffffff"
	other.WorkItemID = "yoyodyne-other"
	other.Status = runstate.StatusRunning
	other.Phase = runstate.PhaseDeveloping
	other.CompletedAt = nil
	other.IntegrationStop = nil
	other.Environmental = nil
	other.Failure = ""
	if err := full.runs.Create(other); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	result, err = full.resumer().Resume(context.Background(), resumeRequest())
	if err != nil || result.Resumed || result.CapacityFull == nil {
		t.Fatalf("Resume() = %#v, error = %v; want the full harness reported and nothing resumed", result, err)
	}
	full.assertNothingWritten(t)
}

// The pipeline's own reading of a resumed run: it is picked up at its promotion
// and nowhere else, and a run at that phase nothing resumed on purpose is not.
func TestOnlyARunResumedOnPurposeIsPickedUpAtItsPromotion(t *testing.T) {
	t.Parallel()

	resumed := approvedStoppedState()
	resumed.Status = runstate.StatusRunning
	resumed.CompletedAt = nil
	resumed.IntegrationStop = nil
	resumed.Failure = ""
	resumed.Environmental = nil
	resumed.IntegrationResumptions = []runstate.IntegrationResumption{{Cause: runstate.CauseDirtyPrimary, Reason: "resumed", ResumedAt: docketedNow}}
	if !resumableIntegration(resumed) {
		t.Fatalf("a resumed run at its promotion is not picked up: %#v", resumed)
	}
	died := resumed
	died.IntegrationResumptions = nil
	if resumableIntegration(died) {
		t.Fatal("a run a process died in at the integrating phase was picked up as a resumption")
	}
	unapproved := resumed
	unapproved.ReviewDecision = ""
	unapproved.ReviewSessionID = ""
	if resumableIntegration(unapproved) {
		t.Fatal("a run with no approval standing was picked up at its promotion")
	}
}

// timingOutTracker is a tracker whose Nth read dies the way a `bd` killed under
// load does, which is the second of yoyodyne-ifd.309's two stops. Everything
// else it answers as the tracker under it does.
type timingOutTracker struct {
	*fakeTracker
	failShowAt int
	shows      int
}

func (f *timingOutTracker) Show(ctx context.Context, id string) (beads.WorkItem, error) {
	f.shows++
	if f.shows == f.failShowAt {
		return beads.WorkItem{}, errors.New("bd show failed with status timed_out and exit code -1: ")
	}
	return f.fakeTracker.Show(ctx, id)
}

// The 309 shape, over a real repository and a forge: the change is approved,
// the promotion is refused for an uncommitted edit in the primary checkout, the
// resumed promotion dies on a tracker read that timed out, and the second
// resumption reaches a merged pull request — with every counter exactly where
// the review left it, and the run's record saying each resumption was a
// continuation rather than an attempt.
func TestAnApprovedChangeStoppedByTheEnvironmentResumesToAMergedPullRequestChargingNothing(t *testing.T) {
	t.Parallel()

	repository, remote := publishedRepository(t)
	worktreeRoot := filepath.Join(t.TempDir(), "worktrees")
	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewStore() error = %v", err)
	}
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &fakeForge{remote: remote}
	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	// The operator's uncommitted edit lands in the primary checkout while the
	// reviewer is judging the change, which is what the first stop was.
	dirtying := filepath.Join(repository, "AGENTS.md")
	serve := provider.run
	provider.run = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleReviewer {
			if err := os.WriteFile(dirtying, []byte("an edit nobody committed\n"), 0o600); err != nil {
				return backend.RunResult{}, err
			}
		}
		return serve(request)
	}
	checks := []string{"test -f feature.txt"}
	build := func(tracker WorkTracker) Pipeline {
		return publishing(automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, checks), provider), forge)
	}

	outcome, err := build(tracker).Run(context.Background(), tracker.item.ID)
	if err == nil || !errors.Is(err, gitworktree.ErrPrimaryNotReady) {
		t.Fatalf("Run() error = %v, want the promotion refused for the dirty checkout", err)
	}
	if outcome.Integration != nil || outcome.PullRequest == nil || outcome.PullRequest.Merged || outcome.ReviewDecision != "approve" {
		t.Fatalf("outcome = integration %#v, pull request %#v, review %q; want an approved, published, unpromoted change", outcome.Integration, outcome.PullRequest, outcome.ReviewDecision)
	}
	if outcome.IntegrationStop == nil || outcome.IntegrationStop.Cause != runstate.CauseDirtyPrimary || outcome.IntegrationStop.Phase != runstate.PhaseIntegrating {
		t.Fatalf("integration stop = %#v, want the dirty checkout recorded at the integrating phase", outcome.IntegrationStop)
	}
	if !strings.Contains(tracker.notes, "Integration stop: approved, then stopped at the integrating phase by the environment: dirty-primary") ||
		!strings.Contains(tracker.notes, "`yoyo triage resume "+outcome.RunID+"`") {
		t.Fatalf("item notes do not name the stop and the verb that resumes it:\n%s", tracker.notes)
	}
	stopped, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !stopped.ResumableIntegration() {
		t.Fatalf("the stopped run is not resumable on its own record: %#v", stopped)
	}
	// Where the review left the item's counters, which is where they have to
	// still be once the change has landed.
	left, err := store.Triage().Counters(tracker.item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	docket := &memoryDocket{}
	docketer := docketerOverStore(docket, store, build(tracker).Config)
	if _, err := docketer.RecordStoppedRun(stopped); err != nil {
		t.Fatalf("RecordStoppedRun() error = %v", err)
	}
	if entry := docket.entries[0]; entry.IntegrationStop == nil || entry.IntegrationStop.Cause != string(runstate.CauseDirtyPrimary) {
		t.Fatalf("docket entry = %#v, want the stop carried onto it", docket.entries[0])
	} else if rendered := entry.Render(); !strings.Contains(rendered, "Next mover: the harness") || !strings.Contains(rendered, "yoyo triage resume "+outcome.RunID) {
		t.Fatalf("docket entry does not say the harness resumes it:\n%s", rendered)
	}
	worktrees, err := gitworktree.New(gitworktree.Options{Runner: execution.OSProcessRunner{}, RepositoryRoot: repository, WorktreeRoot: worktreeRoot})
	if err != nil {
		t.Fatalf("gitworktree.New() error = %v", err)
	}
	intake := newIntakeHoldStore(t)
	resumer := func(pipeline Pipeline, observed *[]runstate.State) IntegrationResumer {
		return IntegrationResumer{
			Docket: docket, Runs: store, Intake: intake, Items: tracker, Worktrees: worktrees,
			Capacity: pipeline.Config.Execution.MaxConcurrentDevelopers,
			Start: func(ctx context.Context, workItemID, runID string) (Outcome, error) {
				// What `yoyo status` would read while the promotion is going.
				live, err := store.Load(runID)
				if err != nil {
					return Outcome{}, err
				}
				*observed = append(*observed, live)
				return pipeline.Continue(ctx, workItemID, runID)
			},
		}
	}

	// The resumption is refused while the checkout is still dirty, and writes
	// nothing: the run is exactly as it stopped.
	if _, err := resumer(build(tracker), new([]runstate.State)).Resume(context.Background(), IntegrationResumeRequest{Run: outcome.RunID}); !errors.Is(err, ErrCheckoutNotReady) {
		t.Fatalf("Resume() error = %v, want the dirty checkout refused before anything is written", err)
	}
	if again, err := store.Load(outcome.RunID); err != nil || again.Status != runstate.StatusFailed || len(again.IntegrationResumptions) != 0 {
		t.Fatalf("a refused resumption changed the run: %#v (error %v)", again, err)
	}
	if err := os.Remove(dirtying); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}

	// The first resumption: the promotion goes again and dies on the tracker read
	// the gate makes before it promotes — the second of 309's two stops. The
	// resumed run's own read is the second Show the continued pipeline makes; the
	// first is the continuation loading the item.
	var observed []runstate.State
	flaky := &timingOutTracker{fakeTracker: tracker, failShowAt: 2}
	result, err := resumer(build(flaky), &observed).Resume(context.Background(), IntegrationResumeRequest{Run: outcome.RunID})
	if !result.Resumed || err == nil || !strings.Contains(err.Error(), "waits on: bd show failed with status timed_out") {
		t.Fatalf("Resume() = resumed %t, error = %v; want the run resumed and then stopped by the tracker", result.Resumed, err)
	}
	if len(observed) != 1 || !observed[0].ResumingIntegration() {
		t.Fatalf("the run in flight did not read as resuming its integration: %#v", observed)
	}
	stopped, err = store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if stopped.Status != runstate.StatusFailed || stopped.IntegrationStop == nil || stopped.IntegrationStop.Cause != runstate.CauseTransportFailure {
		t.Fatalf("after the tracker died: %s, stop %#v; want the run stopped again with the transport failure recorded", stopped.Status, stopped.IntegrationStop)
	}
	if len(stopped.IntegrationResumptions) != 1 || stopped.IntegrationResumptions[0].Cause != runstate.CauseDirtyPrimary {
		t.Fatalf("resumptions = %#v, want the first resumption recorded as a continuation", stopped.IntegrationResumptions)
	}
	// The stoppage is docketed afresh, because it stopped again after the
	// resumption closed the first entry.
	if _, err := docketer.RecordStoppedRun(stopped); err != nil {
		t.Fatalf("RecordStoppedRun() error = %v", err)
	}
	if entry := docket.entries[0]; entry.IntegrationStop == nil || entry.IntegrationStop.Cause != string(runstate.CauseTransportFailure) || entry.Closed != nil {
		t.Fatalf("re-docketed entry = %#v, want the second stop carried and the entry open", entry)
	}

	// The second resumption reaches the merged pull request.
	observed = nil
	result, err = resumer(build(tracker), &observed).Resume(context.Background(), IntegrationResumeRequest{Run: outcome.RunID})
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if !result.Resumed || result.Outcome.Integration == nil || result.Outcome.PullRequest == nil || !result.Outcome.PullRequest.Merged {
		t.Fatalf("result = %#v, want the approved change promoted and its pull request merged", result)
	}
	if len(forge.merges) != 1 || !tracker.closed {
		t.Fatalf("forge merges = %d, item closed = %t; want one merge and the item closed on it", len(forge.merges), tracker.closed)
	}
	assertRemoteCarriesPromotion(t, repository, remote, "main", result.Outcome.Integration.TargetCommit)
	if integrated := gitLine(t, repository, "show", "main:feature.txt"); integrated != "implemented" {
		t.Fatalf("integrated feature.txt = %q, want the approved content", integrated)
	}
	if len(observed) != 1 || !observed[0].ResumingIntegration() || len(observed[0].IntegrationResumptions) != 2 {
		t.Fatalf("the run in flight did not read as resuming its integration a second time: %#v", observed)
	}
	// Nothing was invoked: the developer's attempt and the reviewer's verdict are
	// the ones the first run made.
	if developer, reviewer := provider.requestsForRole(domain.RoleDeveloper), provider.requestsForRole(domain.RoleReviewer); len(developer) != 1 || len(reviewer) != 1 {
		t.Fatalf("invocations = %d developer, %d reviewer; want the one attempt and the one review the first run made", len(developer), len(reviewer))
	}
	// And every counter is where the review left it.
	landed, err := store.Triage().Counters(tracker.item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if !reflect.DeepEqual(landed, left) {
		t.Fatalf("the item's triage record moved across two resumptions:\nleft   %#v\nlanded %#v", left, landed)
	}
	final, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if final.Status != runstate.StatusSucceeded || final.RepairAttempts != 0 || final.ReviewRounds != 1 || len(final.IntegrationResumptions) != 2 {
		t.Fatalf("final run = %s, attempts %d, rounds %d, resumptions %d; want it succeeded with no attempt or round added", final.Status, final.RepairAttempts, final.ReviewRounds, len(final.IntegrationResumptions))
	}
	if !strings.Contains(tracker.notes, "was approved by an independent reviewer, and was integrated automatically") {
		t.Fatalf("item notes do not record the integration:\n%s", tracker.notes)
	}
}

// A replay that conflicts is the one outcome that leaves the resumed path, and
// it leaves it exactly as a first promotion's conflict does: the run stops for
// a person, both sides preserved, with nothing charged and no environmental stop
// recorded for a conflict the environment does not answer for.
func TestAResumedPromotionWhoseReplayConflictsStopsForAPersonChargingNothing(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	dirtying := filepath.Join(repository, "AGENTS.md")
	serve := provider.run
	provider.run = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleReviewer {
			if err := os.WriteFile(dirtying, []byte("an edit nobody committed\n"), 0o600); err != nil {
				return backend.RunResult{}, err
			}
		}
		return serve(request)
	}
	build := func() Pipeline {
		return automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider)
	}
	outcome, err := build().Run(context.Background(), tracker.item.ID)
	if err == nil || !errors.Is(err, gitworktree.ErrPrimaryNotReady) {
		t.Fatalf("Run() error = %v, want the promotion refused for the dirty checkout", err)
	}
	stopped, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	left, err := store.Triage().Counters(tracker.item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	docket := &memoryDocket{}
	if _, err := docketerOverStore(docket, store, build().Config).RecordStoppedRun(stopped); err != nil {
		t.Fatalf("RecordStoppedRun() error = %v", err)
	}
	// The checkout is put right, and the target moves onto a conflicting edit of
	// the same file: the ground moved under the approved change.
	if err := os.Remove(dirtying); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(repository, "feature.txt"), []byte("someone else's version\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	runPipelineGit(t, repository, "add", "feature.txt")
	runPipelineGit(t, repository, "commit", "-m", "a conflicting edit on main")
	worktrees, err := gitworktree.New(gitworktree.Options{Runner: execution.OSProcessRunner{}, RepositoryRoot: repository, WorktreeRoot: worktreeRoot})
	if err != nil {
		t.Fatalf("gitworktree.New() error = %v", err)
	}
	resumer := IntegrationResumer{
		Docket: docket, Runs: store, Intake: newIntakeHoldStore(t), Items: tracker, Worktrees: worktrees,
		Capacity: 1,
		Start: func(ctx context.Context, workItemID, runID string) (Outcome, error) {
			return build().Continue(ctx, workItemID, runID)
		},
	}
	result, err := resumer.Resume(context.Background(), IntegrationResumeRequest{Run: outcome.RunID})
	if !result.Resumed || !errors.Is(err, gitworktree.ErrRebaseConflict) {
		t.Fatalf("Resume() = resumed %t, error = %v; want the resumed promotion stopped on the replay conflict", result.Resumed, err)
	}
	if !tracker.blocked || !strings.Contains(tracker.blockReason, "cannot be replayed") {
		t.Fatalf("blocked = %t, reason = %q; want the conflict recorded for a person", tracker.blocked, tracker.blockReason)
	}
	conflicted, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if conflicted.Status != runstate.StatusFailed || conflicted.Blocker == "" || conflicted.IntegrationRetries != 1 {
		t.Fatalf("conflicted run = %s, blocker %q, retries %d; want it stopped on its first replay", conflicted.Status, conflicted.Blocker, conflicted.IntegrationRetries)
	}
	// A conflict is a person's, so it is not recorded as a stop the harness can
	// resume past; and both sides survive.
	if conflicted.IntegrationStop != nil || conflicted.ResumableIntegration() {
		t.Fatalf("a replay conflict was recorded as an environmental stop: %#v", conflicted.IntegrationStop)
	}
	if conflicted.WorktreeRemoved || conflicted.BranchRemoved {
		t.Fatal("the conflicted run's artifacts were removed")
	}
	if theirs := gitLine(t, repository, "show", "main:feature.txt"); theirs != "someone else's version" {
		t.Fatalf("main feature.txt = %q, want the target left where it was", theirs)
	}
	if conflicted.RepairAttempts != 0 || len(conflicted.RepairContinuations) != 0 {
		t.Fatalf("the conflict charged an attempt: %d attempts, %#v", conflicted.RepairAttempts, conflicted.RepairContinuations)
	}
	landed, err := store.Triage().Counters(tracker.item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if !reflect.DeepEqual(landed, left) {
		t.Fatalf("the item's triage record moved across a resumption that conflicted:\nleft   %#v\nlanded %#v", left, landed)
	}
}
