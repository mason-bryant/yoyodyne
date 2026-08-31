package orchestrator

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
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The decision a development manager records when the change is nearly right and
// the run ran out of attempts: the findings are the ones worth acting on, and
// the developer that wrote the change is the one to act on them.
const continueReasoning = "the reviewer's two findings are both right and both small, and the change is otherwise the one we asked for; it needs the developer that wrote it to answer them rather than a fresh run"

// continueCaps are the harness defaults the development manager's decision is
// recorded against, with room in the round budget so a test measuring the grant
// is measuring the grant.
var continueCaps = runstate.TriageCaps{ReviewRounds: 6, RepairGrants: 1, Reruns: 1, MergeRearms: 2}

// continueGrantRounds is what triage.repair_grant_attempts says a grant is worth
// in these tests, which is what the development manager's decision spends.
const continueGrantRounds = 2

// fakeOwnership stands in for the two questions a re-entry asks of the preserved
// worktree: whether it is as the harness left it, and whether the change is
// still in it. What each was asked about, and what each says.
type fakeOwnership struct {
	err   error
	asked []gitworktree.Worktree
	// changed is what the preserved worktree holds. A nil value is the ordinary
	// case — the change is still there — so a test about anything else is the only
	// one that has to say so; an empty non-nil slice is the worktree a handback
	// must refuse. readErr is what stopped the reading where nothing could be read.
	changed []string
	readErr error
	read    []gitworktree.Worktree
}

func (f *fakeOwnership) VerifyOwnedHead(_ context.Context, worktree gitworktree.Worktree) error {
	f.asked = append(f.asked, worktree)
	return f.err
}

func (f *fakeOwnership) ChangedPaths(_ context.Context, worktree gitworktree.Worktree) ([]string, error) {
	f.read = append(f.read, worktree)
	if f.readErr != nil {
		return nil, f.readErr
	}
	if f.changed == nil {
		return []string{"internal/orchestrator/repaircontinue.go"}, nil
	}
	return f.changed, nil
}

// continueHarness is the durable state a repair-continue acts on, held together
// so a test can drive one decision without rebuilding four stores.
type continueHarness struct {
	docket    *memoryDocket
	runs      *runstate.Store
	intake    *runstate.IntakeHoldStore
	tracker   *fakeTracker
	ownership *fakeOwnership
	// started is the continuations the carry-out dispatched.
	started []continuedRun
	outcome Outcome
	failure error
	// capacity is execution.max_concurrent_developers as the action reads it.
	capacity int
}

func (h *continueHarness) continuer() RepairContinuer {
	return RepairContinuer{
		Docket:             h.docket,
		Runs:               h.runs,
		Intake:             h.intake,
		Decisions:          h.runs.Triage(),
		Items:              h.tracker,
		Worktrees:          h.ownership,
		ConfiguredAttempts: 2,
		Capacity:           h.capacity,
		Clock:              docketClock{},
		Start: func(_ context.Context, workItemID, runID string) (Outcome, error) {
			h.started = append(h.started, continuedRun{workItemID: workItemID, runID: runID})
			return h.outcome, h.failure
		},
	}
}

// continuedRun is one continuation the carry-out dispatched: the item, and the
// run it named to be re-entered. The run is recorded because naming it is what
// keeps a repair from being carried out as a fresh run of the same item.
type continuedRun struct {
	workItemID string
	runID      string
}

// continuableState is the stopped run this action is about: one whose repair
// budget was spent on findings its developer never resolved, with the branch,
// the worktree, and the session it stopped in all preserved.
func continuableState() runstate.State {
	state := stoppedState()
	state.ProviderSessionID = "developer-session"
	return state
}

// newContinueHarness is an undecided product with the development manager's
// repair decision recorded on it, which is the ordinary case: the decision is
// made in the conversation and this action carries it out.
func newContinueHarness(t *testing.T, state runstate.State) *continueHarness {
	t.Helper()
	harness := newUndecidedHarness(t, state)
	// The decision itself: the development manager recorded a repair of this
	// item, which spent the item's repair-grant budget and sized the grant from
	// the configuration. That footprint is what the action reads to know somebody
	// decided this and how much it is worth.
	recordRepairDecision(t, harness.runs, state.WorkItemID)
	return harness
}

// newUndecidedHarness records one stopped run, dockets it, and leaves everything
// else as a fresh product: no hold, nothing decided, nothing in flight, and the
// item blocked exactly as the run stopping left it.
func newUndecidedHarness(t *testing.T, state runstate.State) *continueHarness {
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
	return &continueHarness{
		docket:    docket,
		runs:      runs,
		intake:    intake,
		tracker:   &fakeTracker{item: beads.WorkItem{ID: state.WorkItemID, Title: state.WorkItemTitle, Status: "blocked"}},
		ownership: &fakeOwnership{},
		capacity:  2,
		outcome:   Outcome{RunID: state.RunID, WorkItemID: state.WorkItemID, Status: runstate.StatusSucceeded},
	}
}

// recordRepairDecision is what the development manager's triage does to the
// item's durable record when it decides a repair: it spends the item's one
// grant, truncated to the rounds the cap still has room for, before anything
// acts on the decision.
func recordRepairDecision(t *testing.T, runs *runstate.Store, workItemID string) runstate.RepairGrant {
	t.Helper()
	granted, err := runs.Triage().GrantRepair(context.Background(), workItemID, continueGrantRounds, docketedNow, continueCaps)
	if err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
	return granted
}

func continueRequest() RepairContinueRequest {
	return RepairContinueRequest{Run: docketedRunID, Reason: continueReasoning}
}

// carried reports how much of the item's grant the harness has handed to the
// stopped run, which is what a refused carry-out must leave at zero.
func (h *continueHarness) carried(t *testing.T) int {
	t.Helper()
	return h.reload(t).GrantedRepairAttempts()
}

// spent reports what the item's durable triage record now says, which is what
// every other reader of the same budget reads.
func (h *continueHarness) spent(t *testing.T) runstate.TriageCounters {
	t.Helper()
	counters, err := h.runs.Triage().Counters(docketedItem)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	return counters
}

func (h *continueHarness) reload(t *testing.T) runstate.State {
	t.Helper()
	state, err := h.runs.Load(docketedRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return state
}

func (h *continueHarness) save(t *testing.T, state runstate.State) {
	t.Helper()
	if err := h.runs.Save(state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
}

// What the action is for: the same run goes on, on the change it already has,
// with the grant recorded where the loop that spends it will read it.
func TestARepairContinuesTheSameRunUnderTheConfiguredGrant(t *testing.T) {
	t.Parallel()

	harness := newContinueHarness(t, continuableState())
	result, err := harness.continuer().Continue(context.Background(), continueRequest())
	if err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	if !result.Continued || len(harness.started) != 1 || harness.started[0].workItemID != docketedItem {
		t.Fatalf("started = %#v, continued = %t, want the docketed item continued once", harness.started, result.Continued)
	}
	// And the dispatch named the run it verified rather than the item alone. A
	// dispatch that named only the item is one a fresh run satisfies, which is
	// what every recorded loss of a repair round actually was.
	if harness.started[0].runID != docketedRunID {
		t.Fatalf("continued run = %q, want the docketed run %q named by the dispatch", harness.started[0].runID, docketedRunID)
	}
	// Nothing started over: the run the docket entry names is the run that goes
	// on, in the worktree, branch, and session it stopped in.
	state := harness.reload(t)
	if state.RunID != docketedRunID || state.Status != runstate.StatusRunning || state.Phase != runstate.PhaseDeveloping {
		t.Fatalf("continued run = %s %s/%s, want the docketed run running and developing again", state.RunID, state.Status, state.Phase)
	}
	if state.CompletedAt != nil {
		t.Fatalf("completed at = %v, want a run that is going again to be recorded as unfinished", state.CompletedAt)
	}
	if state.WorktreePath != continuableState().WorktreePath || state.Branch != continuableState().Branch || state.ProviderSessionID != "developer-session" {
		t.Fatalf("continued run lost what it stopped with: %#v", state)
	}
	// The reviewer's findings are what the continued attempt is handed back, in
	// the words the reviewer wrote them.
	if len(state.ReviewFindingDetails) != 1 || state.ReviewFindingDetails[0].Message != "add the missing file" {
		t.Fatalf("findings = %#v, want the reviewer's own findings intact", state.ReviewFindingDetails)
	}
	// The grant is on the run, where the repair loop reads its budget from.
	if len(state.RepairContinuations) != 1 {
		t.Fatalf("continuations = %#v, want the one grant this carry-out made", state.RepairContinuations)
	}
	granted := state.RepairContinuations[0]
	if granted.GrantedAttempts != continueGrantRounds {
		t.Fatalf("granted = %#v, want the configured grant of two attempts in full", granted)
	}
	// The attempt this re-entry is about is counted as it is granted, exactly as
	// the repair loop counts its own, so the grant is worth what it says.
	if state.RepairAttempts != continuableState().RepairAttempts+1 {
		t.Fatalf("repair attempts = %d, want the continued attempt counted", state.RepairAttempts)
	}
	if budget := state.RepairBudget(2); budget != 4 || result.RepairBudget != budget {
		t.Fatalf("repair budget = %d (result %d), want the configured two plus the granted two", budget, result.RepairBudget)
	}
	// The item's durable budget is where the grant came from, and this carried
	// the whole of it out rather than spending a second one.
	if spent := harness.spent(t); spent.RepairGrants != 1 || spent.GrantedRounds != continueGrantRounds {
		t.Fatalf("counters = %#v, want the development manager's one grant and no second", spent)
	}
}

// The harness carries decisions out; it does not make them. An item nobody
// granted a repair is an item nobody decided this about, and the size of what a
// grant is worth is read from that record rather than from the configuration a
// second time.
func TestARepairIsRefusedWithoutTheDevelopmentManagersGrant(t *testing.T) {
	t.Parallel()

	harness := newUndecidedHarness(t, continuableState())
	_, err := harness.continuer().Continue(context.Background(), continueRequest())
	if err == nil || !strings.Contains(err.Error(), "granted "+docketedItem+" no repair") {
		t.Fatalf("Continue() error = %v, want a refusal naming the missing decision", err)
	}
	if len(harness.started) != 0 || harness.tracker.claimed {
		t.Fatalf("started = %#v, claimed = %t, want nothing continued on nobody's decision", harness.started, harness.tracker.claimed)
	}
	// Decided, the same carry-out runs, and it hands the run the rounds the
	// decision was worth rather than a number of its own.
	recordRepairDecision(t, harness.runs, docketedItem)
	result, err := harness.continuer().Continue(context.Background(), continueRequest())
	if err != nil {
		t.Fatalf("Continue() after the decision was recorded error = %v", err)
	}
	if result.Granted != continueGrantRounds || result.Decided != continueGrantRounds {
		t.Fatalf("granted = %d of %d decided, want the recorded grant carried out", result.Granted, result.Decided)
	}
}

// The invariant's second half, and the item's own requirement: the reasoning is
// recorded durably in both places a later reader looks — on the run, which is
// what outlives the process, and on the item, which is what a person reads.
func TestARepairRecordsTheTriageReasoningOnTheRunAndTheItem(t *testing.T) {
	t.Parallel()

	harness := newContinueHarness(t, continuableState())
	result, err := harness.continuer().Continue(context.Background(), continueRequest())
	if err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	recorded := harness.reload(t).RepairContinuations[0]
	if !strings.Contains(recorded.Reason, continueReasoning) {
		t.Fatalf("run reason = %q, want the reasoning the decision was recorded with", recorded.Reason)
	}
	// The stoppage it settles and the grant it verified are named as well as the
	// argument: a reason carrying only the prose would not say what was spent.
	for _, want := range []string{docketedRunID, docketedItem, "2 further repair attempt(s)"} {
		if !strings.Contains(recorded.Reason, want) {
			t.Fatalf("run reason = %q, is missing %q", recorded.Reason, want)
		}
	}
	if recorded.Reason != result.Reason || !strings.Contains(harness.tracker.notes, result.Reason) {
		t.Fatalf("the item's notes (%q) and the run (%q) do not carry the same reasoning", harness.tracker.notes, recorded.Reason)
	}
	// A reason the run state would refuse to hold would be a reason nothing
	// records.
	if err := harness.reload(t).Validate(); err != nil {
		t.Fatalf("the continued run is not one the store would hold: %v", err)
	}
}

// The architect's constraint (b). A run that is going again has not stopped, and
// the blocker on its record is what the docket, the status surface, and
// reconciliation all read as the fact that it has.
func TestARepairSupersedesTheBlockerOnBothTheRunAndTheItem(t *testing.T) {
	t.Parallel()

	harness := newContinueHarness(t, continuableState())
	result, err := harness.continuer().Continue(context.Background(), continueRequest())
	if err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	state := harness.reload(t)
	if state.Blocker != "" || state.Failure != "" {
		t.Fatalf("continued run still says it stopped: blocker = %q, failure = %q", state.Blocker, state.Failure)
	}
	// Clearing it does not lose it: the words it was recorded in travel with the
	// continuation that superseded them.
	superseded := continuableState().Blocker
	if state.RepairContinuations[0].SupersededBlocker != superseded || result.SupersededBlocker != superseded {
		t.Fatalf("superseded blocker = %q / %q, want the blocker the run stopped on", state.RepairContinuations[0].SupersededBlocker, result.SupersededBlocker)
	}
	// The docket agrees, which is what stops the same stoppage being docketed a
	// second time behind a run that is going again.
	if stoppedRun(state) {
		t.Fatalf("a run that is going again is still docketable as stopped work: %#v", state)
	}
	// And on the item: the re-entry is what puts it back, rather than somebody
	// remembering to reopen it first.
	if !harness.tracker.claimed || harness.tracker.item.Status != "in_progress" {
		t.Fatalf("item status = %q, claimed = %t, want the item put back by the re-entry itself", harness.tracker.item.Status, harness.tracker.claimed)
	}
	// The decision is recorded before the claim, so the item never reads as work
	// somebody quietly restarted.
	if got := strings.Join(harness.tracker.calls, ","); got != "record,claim" {
		t.Fatalf("tracker calls = %q, want the decision recorded and then the item claimed", got)
	}
}

// The per-item grant counter is what bounds this: triage acts alone once, so an
// item whose grant has been carried out has no decision of its own left to act
// on, and a second is an escalation rather than a larger budget.
func TestASecondRepairOfOneItemIsRefusedOnceTheGrantIsCarriedOut(t *testing.T) {
	t.Parallel()

	harness := newContinueHarness(t, continuableState())
	if _, err := harness.continuer().Continue(context.Background(), continueRequest()); err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	// The continued run stopped again and was docketed like any other stoppage.
	second := harness.reload(t)
	second.Status = runstate.StatusFailed
	completed := docketedNow
	second.CompletedAt = &completed
	second.Blocker = "Yoyodyne stopped this item: the granted repair budget was spent too."
	harness.save(t, second)

	_, err := harness.continuer().Continue(context.Background(), continueRequest())
	if err == nil || !strings.Contains(err.Error(), "the harness has carried out 2") {
		t.Fatalf("second Continue() error = %v, want a refusal naming the grant already carried out", err)
	}
	if len(harness.started) != 1 {
		t.Fatalf("started = %#v, want exactly one continuation for one grant", harness.started)
	}
	// And deciding a second is what the cap makes a person's decision rather than
	// this action's.
	if _, err := harness.runs.Triage().GrantRepair(context.Background(), docketedItem, continueGrantRounds, docketedNow, continueCaps); !errors.Is(err, runstate.ErrTriageCapReached) {
		t.Fatalf("GrantRepair() error = %v, want a second grant of one item refused", err)
	}
}

// The other bound the item names: the rounds cap, which is what an item may cost
// in total across every run of it. Past it another repair is not triage's to
// grant at all, so the decision this action carries out is never made — and this
// finds no grant to act on.
func TestARepairIsRefusedOnceTheRoundCapHasNoRoomLeft(t *testing.T) {
	t.Parallel()

	harness := newUndecidedHarness(t, continuableState())
	for round := 0; round < continueCaps.ReviewRounds; round++ {
		if _, err := harness.runs.Triage().RecordReviewRound(context.Background(), docketedItem,
			runstate.RoundKey(docketedRunID, round), countingProcess, docketedNow); err != nil {
			t.Fatalf("RecordReviewRound() error = %v", err)
		}
	}
	// The development manager's own decision is what the cap refuses, and it
	// refuses it by the round budget rather than by the grant's own.
	_, grantErr := harness.runs.Triage().GrantRepair(context.Background(), docketedItem, continueGrantRounds, docketedNow, continueCaps)
	var capped runstate.TriageCapError
	if !errors.As(grantErr, &capped) || capped.Budget != runstate.TriageReviewRoundBudget {
		t.Fatalf("GrantRepair() error = %v, want the review round budget to refuse it", grantErr)
	}

	_, err := harness.continuer().Continue(context.Background(), continueRequest())
	if err == nil || !strings.Contains(err.Error(), "no repair") {
		t.Fatalf("Continue() error = %v, want nothing to carry out past the cap", err)
	}
	if len(harness.started) != 0 || harness.tracker.claimed {
		t.Fatalf("started = %#v, claimed = %t, want nothing continued past the cap", harness.started, harness.tracker.claimed)
	}
	if state := harness.reload(t); state.Blocker == "" || !state.Status.Terminal() {
		t.Fatalf("a refused repair superseded the blocker anyway: %#v", state)
	}
}

// A grant the round cap cut is carried out at the size it was recorded, not the
// size the configuration asks for: the cut is what says the item is at the end
// of what it will be given, and a carry-out reading the configuration again
// would hand the run attempts the cap never let it have.
func TestARepairCarriesOutTheGrantAtTheSizeTheCapLeftIt(t *testing.T) {
	t.Parallel()

	harness := newUndecidedHarness(t, continuableState())
	for round := 0; round < continueCaps.ReviewRounds-1; round++ {
		if _, err := harness.runs.Triage().RecordReviewRound(context.Background(), docketedItem,
			runstate.RoundKey(docketedRunID, round), countingProcess, docketedNow); err != nil {
			t.Fatalf("RecordReviewRound() error = %v", err)
		}
	}
	if granted := recordRepairDecision(t, harness.runs, docketedItem); granted.Rounds != 1 || !granted.Truncated {
		t.Fatalf("the decision granted %#v, want it cut to the one round the cap had left", granted)
	}

	result, err := harness.continuer().Continue(context.Background(), continueRequest())
	if err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	if result.Granted != 1 || result.Decided != 1 || !result.Truncated {
		t.Fatalf("granted = %d of %d decided, truncated = %t, want the one round the cap left", result.Granted, result.Decided, result.Truncated)
	}
	if granted := harness.reload(t).RepairContinuations[0]; granted.GrantedAttempts != 1 {
		t.Fatalf("recorded grant = %#v, want the run handed only what the cap left", granted)
	}
	if budget := harness.reload(t).RepairBudget(2); budget != 3 {
		t.Fatalf("repair budget = %d, want the configured two plus the one round granted", budget)
	}
	if !strings.Contains(result.Reason, "already cut to 1") {
		t.Fatalf("reason = %q, want the cut said out loud", result.Reason)
	}
}

// The invariant's first half. Continuing a run spends on a provider and the
// development manager naming the item is not the operator naming it, so the hold
// applies — and nothing is spent under one, which is what leaves the item its
// grant for afterwards.
func TestAHeldIntakeContinuesNothingAndSpendsNothing(t *testing.T) {
	t.Parallel()

	harness := newContinueHarness(t, continuableState())
	held, err := harness.intake.Hold("the queue is heading somewhere odd", docketedNow)
	if err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	result, err := harness.continuer().Continue(context.Background(), continueRequest())
	if err != nil {
		t.Fatalf("Continue() error = %v, want a held intake reported rather than a failure", err)
	}
	if result.Continued || len(harness.started) != 0 {
		t.Fatalf("continued = %t / %#v, want nothing continued under a hold", result.Continued, harness.started)
	}
	if result.IntakeHeld == nil || !result.IntakeHeld.HeldAt.Equal(held.HeldAt) {
		t.Fatalf("intake held = %#v, want the hold that stopped it", result.IntakeHeld)
	}
	if carried := harness.carried(t); carried != 0 {
		t.Fatalf("carried out = %d, want the item to keep its grant", carried)
	}
	if state := harness.reload(t); state.Blocker == "" {
		t.Fatalf("a held carry-out superseded the blocker anyway: %#v", state)
	}
	// Released, the same decision is carried out: the hold delayed the repair
	// rather than consuming it.
	if _, _, err := harness.intake.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if _, err := harness.continuer().Continue(context.Background(), continueRequest()); err != nil {
		t.Fatalf("Continue() after the hold was released error = %v", err)
	}
	if len(harness.started) != 1 {
		t.Fatalf("started = %#v, want the repair to have run once the hold was lifted", harness.started)
	}
}

// The architect's constraint (a). What a continued developer is handed back is
// whatever is in that worktree, so a worktree something has touched since the
// blocker is a person's to look at — and asking before anything is spent is what
// makes the refusal free.
func TestARepairRefusesAWorktreeThatIsNotAsTheHarnessLeftIt(t *testing.T) {
	t.Parallel()

	harness := newContinueHarness(t, continuableState())
	harness.ownership.err = errors.New("worktree HEAD is 9f9f9f, want the commit the harness recorded (aaaaaa)")

	_, err := harness.continuer().Continue(context.Background(), continueRequest())
	if !errors.Is(err, ErrWorktreeNotAsLeft) {
		t.Fatalf("Continue() error = %v, want the worktree refused", err)
	}
	// The refusal names what it is escalating and why, because nothing else here
	// will notice the worktree.
	if !strings.Contains(err.Error(), "a person's to look at") {
		t.Fatalf("refusal = %v, want it to say whose decision this now is", err)
	}
	if len(harness.started) != 0 {
		t.Fatalf("started = %#v, want nothing continued", harness.started)
	}
	if carried := harness.carried(t); carried != 0 {
		t.Fatalf("carried out = %d, want a refused re-entry to have spent nothing of the grant", carried)
	}
	// The item is left blocked, which is the durable state an escalation would
	// have made anyway.
	if harness.tracker.claimed || harness.tracker.item.Status != "blocked" {
		t.Fatalf("item status = %q, claimed = %t, want it left waiting on a person", harness.tracker.item.Status, harness.tracker.claimed)
	}
	if state := harness.reload(t); state.Blocker == "" || !state.Status.Terminal() {
		t.Fatalf("a refused repair superseded the blocker anyway: %#v", state)
	}
	// The gate was asked about this run's own worktree, from the run's record
	// rather than from the docket entry that describes it.
	if len(harness.ownership.asked) != 1 || harness.ownership.asked[0].RunID != docketedRunID {
		t.Fatalf("ownership asked about %#v, want the stopped run's own worktree", harness.ownership.asked)
	}
}

// The failure this item was filed for: a handback that arrives on a worktree
// holding none of the change it is a repair of. It is refused rather than
// carried out, because what a continued developer would be given is the
// reviewer's findings about a change that is not in front of it — which is
// delivered as an empty repair or as the same change reinvented by hand.
func TestARepairRefusesAWorktreeThatHoldsNoneOfThePreservedChange(t *testing.T) {
	t.Parallel()

	harness := newContinueHarness(t, continuableState())
	// As the harness left it, and empty: the ownership gate passes and this is the
	// only thing that catches it.
	harness.ownership.changed = []string{}

	_, err := harness.continuer().Continue(context.Background(), continueRequest())
	if !errors.Is(err, ErrPreservedChangeMissing) {
		t.Fatalf("Continue() error = %v, want the handback refused for holding no change", err)
	}
	// The refusal names what it is escalating and why, because a worktree that
	// looks valid is what made this silent in the first place.
	if !strings.Contains(err.Error(), "a person's to look at") {
		t.Fatalf("refusal = %v, want it to say whose decision this now is", err)
	}
	if len(harness.started) != 0 {
		t.Fatalf("started = %#v, want nothing continued", harness.started)
	}
	if carried := harness.carried(t); carried != 0 {
		t.Fatalf("carried out = %d, want a refused re-entry to have spent nothing of the grant", carried)
	}
	if harness.tracker.claimed || harness.tracker.item.Status != "blocked" {
		t.Fatalf("item status = %q, claimed = %t, want it left waiting on a person", harness.tracker.item.Status, harness.tracker.claimed)
	}
	if state := harness.reload(t); state.Blocker == "" || !state.Status.Terminal() {
		t.Fatalf("a refused repair superseded the blocker anyway: %#v", state)
	}
	// The change was read from the stopped run's own worktree, from the run's
	// record rather than from the docket entry that describes it.
	if len(harness.ownership.read) != 1 || harness.ownership.read[0].RunID != docketedRunID {
		t.Fatalf("the change was read from %#v, want the stopped run's own worktree", harness.ownership.read)
	}
}

// A preserved worktree nobody can read at all is the same answer as an empty
// one: there is no change to hand a developer, and which of the two happened is
// a person's to find out.
func TestARepairRefusesAPreservedWorktreeItCannotRead(t *testing.T) {
	t.Parallel()

	harness := newContinueHarness(t, continuableState())
	harness.ownership.readErr = errors.New("worktree is not registered with the expected branch")

	_, err := harness.continuer().Continue(context.Background(), continueRequest())
	if !errors.Is(err, ErrPreservedChangeMissing) {
		t.Fatalf("Continue() error = %v, want the handback refused for a change it could not read", err)
	}
	if len(harness.started) != 0 || harness.carried(t) != 0 {
		t.Fatalf("started = %#v, carried = %d, want nothing continued and nothing spent", harness.started, harness.carried(t))
	}
}

// The defect this action was built after: every pre-flight refusal is asked
// before the counter is spent, so a refused carry-out costs the item nothing and
// asking again once it no longer applies carries out the same decision.
func TestEveryRefusalIsAskedBeforeTheGrantIsSpent(t *testing.T) {
	t.Parallel()

	for _, refusal := range []struct {
		name    string
		arrange func(*testing.T, *continueHarness)
		want    string
	}{
		{
			// A run that was docketed and has since been picked up again is owed
			// the rest of its own step.
			name: "the stopped run is running again",
			arrange: func(t *testing.T, h *continueHarness) {
				state := h.reload(t)
				state.Status = runstate.StatusRunning
				state.CompletedAt = nil
				h.save(t, state)
			},
			want: "resumable",
		},
		{
			// A run that stopped with no failure ever returned to its developer
			// has no repair loop to re-enter.
			name: "nothing was ever returned to the developer",
			arrange: func(t *testing.T, h *continueHarness) {
				state := h.reload(t)
				state.ReviewFindingDetails = nil
				state.ReviewFindings = 0
				state.CheckFailure = nil
				state.PathRefusal = nil
				h.save(t, state)
			},
			want: "no repair loop to re-enter",
		},
		{
			// A run whose artifacts triage already retired has nothing left to
			// continue in.
			name: "what it preserved has been retired",
			arrange: func(t *testing.T, h *continueHarness) {
				state := h.reload(t)
				state.WorktreeRemoved = true
				state.BranchRemoved = true
				state.ArtifactsRetiredBy = "run-11112222333344445555666677778888"
				h.save(t, state)
			},
			want: "already been retired",
		},
		{
			// An item somebody closed is not one a stopped run may be continued
			// on, whatever its budget still says.
			name:    "the item was closed",
			arrange: func(_ *testing.T, h *continueHarness) { h.tracker.item.Status = "closed" },
			want:    `status is "closed"`,
		},
		{
			// An item waiting on other work is refused for the reason a fresh run
			// of it would be.
			name: "the item waits on other work",
			arrange: func(_ *testing.T, h *continueHarness) {
				h.tracker.item.Dependencies = []beads.Dependency{{ID: "yoyodyne-ifd.9", Type: "blocks", Status: "open"}}
			},
			want: "is blocked by",
		},
	} {
		t.Run(refusal.name, func(t *testing.T) {
			t.Parallel()
			harness := newContinueHarness(t, continuableState())
			refusal.arrange(t, harness)

			_, err := harness.continuer().Continue(context.Background(), continueRequest())
			if err == nil || !strings.Contains(err.Error(), refusal.want) {
				t.Fatalf("Continue() error = %v, want a refusal naming %q", err, refusal.want)
			}
			if len(harness.started) != 0 {
				t.Fatalf("started = %#v, want nothing continued", harness.started)
			}
			if carried := harness.carried(t); carried != 0 {
				t.Fatalf("carried out = %d, want a refused carry-out to have spent nothing of the grant", carried)
			}
		})
	}
}

// A full harness says nothing about whether the run should go on, and stops
// being true on its own. So it is a state to wait on rather than a refusal, and
// waiting costs the item nothing because it is asked before the grant.
func TestAFullHarnessWaitsRatherThanSpendingTheGrant(t *testing.T) {
	t.Parallel()

	harness := newContinueHarness(t, continuableState())
	harness.capacity = 1
	other := continuableState()
	other.RunID = "run-11112222333344445555666677778888"
	other.WorkItemID = "yoyodyne-ifd.other"
	other.Status = runstate.StatusRunning
	other.CompletedAt = nil
	other.Blocker = ""
	if err := harness.runs.Create(other); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	result, err := harness.continuer().Continue(context.Background(), continueRequest())
	if err != nil {
		t.Fatalf("Continue() error = %v, want a full harness reported rather than a failure", err)
	}
	if result.Continued || result.CapacityFull == nil || result.CapacityFull.Limit != 1 {
		t.Fatalf("result = %#v, want the harness reported as full", result)
	}
	if carried := harness.carried(t); carried != 0 {
		t.Fatalf("carried out = %d, want the item to keep its grant", carried)
	}
	if !strings.Contains(result.Render(), "keeps its repair grant") {
		t.Fatalf("render = %q, want it to say the decision still stands", result.Render())
	}
}

// A run something is already running is not work that has stopped, whatever the
// docket entry said when it was written.
func TestARepairIsRefusedWhileTheItemHasARunInFlight(t *testing.T) {
	t.Parallel()

	harness := newContinueHarness(t, continuableState())
	live := continuableState()
	live.RunID = "run-11112222333344445555666677778888"
	live.Status = runstate.StatusRunning
	live.CompletedAt = nil
	live.Blocker = ""
	if err := harness.runs.Create(live); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	_, err := harness.continuer().Continue(context.Background(), continueRequest())
	if err == nil || !strings.Contains(err.Error(), "in flight") {
		t.Fatalf("Continue() error = %v, want the live run to refuse it", err)
	}
	if carried := harness.carried(t); carried != 0 {
		t.Fatalf("carried out = %d, want nothing of the grant spent", carried)
	}
}

// A decision names the stoppage it settles and carries the reasoning it was
// made on. Neither is guessed at, and a run nothing docketed is not a stoppage
// this may act on.
func TestARepairNeedsADocketedStoppageAndTheReasoning(t *testing.T) {
	t.Parallel()

	harness := newContinueHarness(t, continuableState())
	for _, request := range []struct {
		name string
		ask  RepairContinueRequest
		want string
	}{
		{name: "no reasoning", ask: RepairContinueRequest{Run: docketedRunID}, want: "reasoning"},
		{name: "not a run", ask: RepairContinueRequest{Run: "yoyodyne-ifd.102.5", Reason: continueReasoning}, want: "not a run identifier"},
		{
			name: "not on the docket",
			ask:  RepairContinueRequest{Run: "run-11112222333344445555666677778888", Reason: continueReasoning},
			want: "no stoppage to repair",
		},
	} {
		t.Run(request.name, func(t *testing.T) {
			if _, err := harness.continuer().Continue(context.Background(), request.ask); err == nil || !strings.Contains(err.Error(), request.want) {
				t.Fatalf("Continue() error = %v, want %q", err, request.want)
			}
		})
	}
}

// A carry-out wired without what bounds it, or without the parts that make the
// re-entry safe, refuses rather than inventing either.
func TestARepairRefusesToActWithoutWhatBoundsIt(t *testing.T) {
	t.Parallel()

	_, err := RepairContinuer{}.Continue(context.Background(), continueRequest())
	if err == nil {
		t.Fatal("Continue() with nothing wired started something")
	}
	for _, want := range []string{"triage docket", "intake hold", "triage budget", "work item", "worktree", "developer capacity"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal = %v, is missing %q", err, want)
		}
	}
}

// The whole of it, over a real repository: a run that spends its repair budget
// and blocks, then goes on under a grant — same branch, same worktree, same
// developer session — and lands the change it already had.
func TestARepairContinuationLandsTheChangeTheStoppedRunAlreadyHad(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	// The first developer leaves the reviewer something to object to, and the
	// reviewer keeps objecting until the run's repair budget is spent.
	stopping := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("incomplete\n"), 0o600)
	}, repairVerdict)
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, stopping, []string{"test -f feature.txt"}), stopping)

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err == nil || !strings.Contains(err.Error(), "independent review requires repair") {
		t.Fatalf("Run() error = %v, want the repair budget spent", err)
	}
	if !tracker.blocked || outcome.Integration != nil {
		t.Fatalf("the stopped run did not block its item: blocked = %t, integration = %#v", tracker.blocked, outcome.Integration)
	}
	stopped, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	docket := &memoryDocket{}
	if _, err := docketerOverStore(docket, store, pipeline.Config).RecordStoppedRun(stopped); err != nil {
		t.Fatalf("RecordStoppedRun() error = %v", err)
	}
	worktrees, err := gitworktree.New(gitworktree.Options{
		Runner:         execution.OSProcessRunner{},
		RepositoryRoot: repository,
		WorktreeRoot:   worktreeRoot,
	})
	if err != nil {
		t.Fatalf("gitworktree.New() error = %v", err)
	}
	intake, err := runstate.NewIntakeHoldStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewIntakeHoldStore() error = %v", err)
	}
	// The continued attempt answers the findings; the reviewer then approves.
	continuing := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	// The development manager's decision, recorded exactly as the conversation
	// records one: it spends the item's repair grant, and the three rounds the
	// stopped run cost leave the cap room for one of the two it asks for.
	granted, err := store.Triage().GrantRepair(context.Background(), tracker.item.ID,
		TriageRepairGrantRounds(pipeline.Config.Triage), time.Now(), TriageCaps(pipeline.Config.Execution, pipeline.Config.Triage))
	if err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
	if granted.Rounds != 1 || !granted.Truncated {
		t.Fatalf("the decision granted %#v, want it cut to the one round the cap had left", granted)
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
			return automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, continuing, []string{"test -f feature.txt"}), continuing).
				Continue(ctx, workItemID, runID)
		},
	}

	result, err := continuer.Continue(context.Background(), RepairContinueRequest{Run: outcome.RunID, Reason: continueReasoning})
	if err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	if !result.Continued || result.Outcome.RunID != outcome.RunID {
		t.Fatalf("result = %#v, want the same run continued rather than a fresh one", result)
	}
	// The grant was carried out at the size the round cap left it, and one
	// further attempt was all the change needed.
	if result.Granted != 1 || !result.Truncated {
		t.Fatalf("granted = %d, truncated = %t, want the grant carried out at the size the cap left it", result.Granted, result.Truncated)
	}
	if result.Outcome.Integration == nil || !tracker.closed {
		t.Fatalf("the continued run did not land its change: %#v, closed = %t", result.Outcome.Integration, tracker.closed)
	}
	// It continued the change the stopped run already had: the same branch and
	// the same worktree, in the developer session that already held the context.
	if result.Outcome.Branch != stopped.Branch || result.Outcome.WorktreePath != stopped.WorktreePath {
		t.Fatalf("continued run moved: branch %q worktree %q, want %q and %q",
			result.Outcome.Branch, result.Outcome.WorktreePath, stopped.Branch, stopped.WorktreePath)
	}
	developerRequests := continuing.requestsForRole(domain.RoleDeveloper)
	if len(developerRequests) != 1 {
		t.Fatalf("continued developer invocations = %d, want the one attempt the grant bought", len(developerRequests))
	}
	continued := developerRequests[0]
	if continued.SessionID != stopping.developerSession || continued.WorkingDirectory != stopped.WorktreePath {
		t.Fatalf("continued attempt = session %q in %q, want the stopped run's own session and worktree", continued.SessionID, continued.WorkingDirectory)
	}
	// What it was handed back is the reviewer's findings, unedited, numbered
	// against the budget the grant made.
	for _, want := range []string{"repair attempt 3 of 3", `"message": "add the missing file"`} {
		if !strings.Contains(continued.Prompt, want) {
			t.Fatalf("continued prompt is missing %q:\n%s", want, continued.Prompt)
		}
	}
	if integrated := gitLine(t, repository, "show", "main:feature.txt"); integrated != "implemented" {
		t.Fatalf("integrated feature.txt = %q, want the repaired content", integrated)
	}
}
