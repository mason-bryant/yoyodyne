package orchestrator

// The verb both roles have for the work item that cannot be met as it stands.
//
// The two shapes it exists for are replayed here rather than asserted over a
// hand-built state, because what the item was about is the price: before it, the
// honest can-be-done-by-nobody finding had only expensive exits. A reviewer that
// saw acceptance criteria a ruling forbade could only ask for repair, round after
// round, until the item's budget ran out and the stoppage reached the development
// manager that way — yoyodyne-ifd.100.1 spent three runs and six review rounds
// doing exactly that. A developer in the same position could only land a
// diagnosis, which yoyodyne-ifd.284 then closed its item against.
//
// So each test below asserts the price as well as the routing: one round, no
// repair attempt, nothing integrated, and one entry on the docket.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/landing"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// The reviewer's half of the verb: no change would satisfy this item, so there is
// nothing to hand back and nothing to approve.
const escalateVerdict = `{"decision":"escalate","summary":"the acceptance criteria ask for the conversion a design ruling forbade, so no change here can meet them; this needs replanning"}`

// escalatingPipeline is a run wired to a real docket, which is what an escalation
// has to reach: the whole verb is that the development manager hears about it.
func escalatingPipeline(t *testing.T, tracker *fakeTracker, provider *fakeBackend) (Pipeline, *runstate.Store, *runstate.DocketStore) {
	t.Helper()
	pipeline, store := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{"exit 0"})
	docket, err := runstate.NewDocketStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewDocketStore() error = %v", err)
	}
	pipeline.Docket = docketerOverStore(docket, store, pipeline.Config)
	return pipeline, store, docket
}

// onlyDocketed is the one entry a test expects the run to have raised.
func onlyDocketed(t *testing.T, docket *runstate.DocketStore) triage.Entry {
	t.Helper()
	entries, err := docket.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("docket = %#v, want the one entry this run raised", entries)
	}
	return entries[0]
}

// A developer that finds the item unmeetable says so in the round it is working
// in, and the run ends there. Nothing is checked, nothing is reviewed, nothing is
// integrated, and the item goes back parked with the escalation in front of the
// development manager.
func TestADeveloperEscalationEndsTheRunInTheRoundItWasRaised(t *testing.T) {
	t.Parallel()

	tracker := newOutcomeTracker()
	provider := roleBackend(writeFeature, approveVerdict)
	provider.developerFinalText = "The criteria contradict the entanglement ruling.\n\n" +
		landingBlock(`{"outcome":"escalate","why":"the acceptance criteria ask for what the entanglement ruling forbids, so no change here meets them"}`)
	pipeline, store, docket := escalatingPipeline(t, tracker, provider)

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	// The run succeeded at what it was for. Recording it as a failure would put
	// honesty about an unmeetable item into the same count the failure-storm brake
	// watches, which is the one thing that must not happen to this verb.
	if outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("status = %q, want an escalation to be a run that did its job: %#v", outcome.Status, outcome)
	}
	if outcome.Integration != nil {
		t.Fatalf("an escalation integrated a change: %#v", outcome.Integration)
	}
	// The price. No repair attempt was spent and no reviewer was ever asked,
	// because the developer already knew the answer.
	if outcome.RepairAttempts != 0 {
		t.Errorf("repair attempts = %d, want the run to have ended where the escalation was raised", outcome.RepairAttempts)
	}
	if outcome.ReviewDecision != "" {
		t.Errorf("review decision = %q, want no review bought at all", outcome.ReviewDecision)
	}
	if outcome.Landing != landing.OutcomeEscalate || !outcome.Escalated() {
		t.Fatalf("outcome landing = %q, escalated = %t", outcome.Landing, outcome.Escalated())
	}
	if outcome.EscalatedBy() != domain.RoleDeveloper {
		t.Errorf("raised by %q, want the developer that wrote the claim", outcome.EscalatedBy())
	}
	// The item neither closes nor goes back bare-pullable. Parked is the holding
	// state, and the parking reason is what whoever considers releasing it reads.
	if outcome.WorkItemClosed || tracker.closed {
		t.Fatalf("the item closed against an escalation; calls = %v", tracker.calls)
	}
	if !tracker.reopened || tracker.item.Status != "open" {
		t.Fatalf("the item was left claimed by a run that has ended; calls = %v", tracker.calls)
	}
	if !tracker.item.Parking.Parked() {
		t.Fatalf("the escalated item went back to the backlog unparked; calls = %v", tracker.calls)
	}
	if !strings.Contains(tracker.item.Parking.Reason(), "the entanglement ruling forbids") {
		t.Errorf("the parking reason does not carry the developer's account: %q", tracker.item.Parking)
	}
	if !strings.Contains(tracker.item.Parking.Reason(), "development manager") {
		t.Errorf("the parking reason does not say who releases the item: %q", tracker.item.Parking)
	}
	if !strings.Contains(tracker.notes, "cannot be met as it stands") {
		t.Errorf("the recorded outcome does not say what the run raised: %q", tracker.notes)
	}
	if len(tracker.blockers) > 0 {
		t.Errorf("an escalation made the item wait on other work: %v", tracker.blockers)
	}

	// And the development manager hears about it, which is the whole of the verb.
	entry := onlyDocketed(t, docket)
	if entry.Class != triage.ClassEscalation || entry.RunID != outcome.RunID || entry.WorkItemID != tracker.item.ID {
		t.Fatalf("entry = %#v, want the escalation this run raised", entry)
	}
	if entry.Escalation == nil || entry.Escalation.RaisedBy != domain.RoleDeveloper {
		t.Fatalf("entry escalation = %#v, want the developer's judgement", entry.Escalation)
	}
	if !strings.Contains(entry.Escalation.Reason, "the entanglement ruling forbids") {
		t.Errorf("the entry does not carry the account she decides from: %q", entry.Escalation.Reason)
	}
	// The entry names what was preserved, because a decision to replan or redirect
	// is taken by somebody who may want to read whatever the developer had written.
	if entry.Artifacts.WorktreePath != outcome.WorktreePath || entry.Artifacts.Branch != outcome.Branch {
		t.Errorf("artifacts = %#v, want the preserved worktree and branch", entry.Artifacts)
	}

	// The record says the same, because the decision is taken long after this
	// process has exited.
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !state.Escalated() || state.Discharges() || !state.Parks() {
		t.Errorf("the durable record does not hold the escalation: escalated = %t, discharges = %t, parks = %t",
			state.Escalated(), state.Discharges(), state.Parks())
	}
	// It reports as the succeeded run it is. The word the surfaces read is wired to
	// a blocker recorded — the operator's line for a stoppage says the item is
	// blocked and the harness could not finish it — and an escalated run is
	// neither, so what it raised is said on the item and on the docket instead.
	if state.Outcome() != runstate.OutcomeSucceeded {
		t.Errorf("run outcome = %q, want %q", state.Outcome(), runstate.OutcomeSucceeded)
	}
	if outcome.Ending() != state.Outcome() {
		t.Errorf("the outcome says %q and the record says %q about one run", outcome.Ending(), state.Outcome())
	}
}

// The reviewer's half. yoyodyne-ifd.100.1 replayed: the criteria are the problem
// rather than the change, and the verdict costs the round it was raised in
// instead of the repair rounds that used to be the only way to say it.
func TestAReviewerEscalationCostsOneRoundAndNoRepairAttempt(t *testing.T) {
	t.Parallel()

	tracker := newOutcomeTracker()
	provider := roleBackend(writeFeature, escalateVerdict)
	pipeline, store, docket := escalatingPipeline(t, tracker, provider)

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.Integration != nil {
		t.Fatalf("status = %q, integration = %#v, want an escalation that promoted nothing", outcome.Status, outcome.Integration)
	}
	if outcome.ReviewDecision != review.DecisionEscalate {
		t.Fatalf("review decision = %q, want %q", outcome.ReviewDecision, review.DecisionEscalate)
	}
	// The whole point of the verb: the change is not handed back, so no repair
	// attempt is spent arguing with a wall.
	if outcome.RepairAttempts != 0 {
		t.Errorf("repair attempts = %d, want none: an escalation hands nothing back", outcome.RepairAttempts)
	}
	if outcome.ReviewApproves != "" {
		t.Errorf("approves = %q, want an escalation to approve nothing", outcome.ReviewApproves)
	}
	if outcome.EscalatedBy() != domain.RoleReviewer {
		t.Errorf("raised by %q, want the reviewer that raised it", outcome.EscalatedBy())
	}
	if outcome.WorkItemClosed || tracker.closed {
		t.Fatalf("the item closed against an escalation; calls = %v", tracker.calls)
	}
	if !tracker.item.Parking.Parked() || !strings.Contains(tracker.item.Parking.Reason(), "a design ruling forbade") {
		t.Fatalf("the item is not parked in the reviewer's own words: %q", tracker.item.Parking)
	}

	entry := onlyDocketed(t, docket)
	if entry.Class != triage.ClassEscalation || entry.Escalation == nil ||
		entry.Escalation.RaisedBy != domain.RoleReviewer {
		t.Fatalf("entry = %#v, want the reviewer's escalation", entry)
	}
	if !strings.Contains(entry.Escalation.Reason, "this needs replanning") {
		t.Errorf("the entry does not carry the reviewer's summary: %q", entry.Escalation.Reason)
	}
	// One round, against the same durable counter a repair grant would be
	// truncated against. The escalation charged the round it was raised in and
	// nothing beyond it, which is what leaves the item affordable to replan.
	if entry.Counters.ReviewRounds != 1 {
		t.Errorf("review rounds = %d, want the one round the verdict was raised in", entry.Counters.ReviewRounds)
	}
	if entry.Counters.RepairAttempts != 0 {
		t.Errorf("repair attempts on the entry = %d, want none", entry.Counters.RepairAttempts)
	}
	counters, err := store.Triage().Counters(tracker.item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if counters.ReviewRounds != 1 {
		t.Errorf("durable review rounds = %d, want one", counters.ReviewRounds)
	}
}

// An escalation reaches the development manager by the same delivery a stoppage
// does. It is the one entry that asks for a decision in so many words, so a rule
// that delivered the stoppage and left this on the docket would be carrying the
// expensive half of one question and holding back the cheap one.
func TestAnEscalationIsPutToTheDevelopmentManager(t *testing.T) {
	t.Parallel()

	state := escalatedState("run-escalated0000000000000000000", docketedItem)
	judge := &standingJudge{judgment: Judgment{ConversationID: "chat-1", Decision: "escalate", Reason: "this needs replanning"}}
	docket := &memoryDocket{}
	if _, err := docket.RecordOnce(escalatedEntry(state)); err != nil {
		t.Fatalf("RecordOnce() error = %v", err)
	}
	escalator := Escalator{
		Docket:    docket,
		Runs:      loadableRuns{states: map[string]runstate.State{state.RunID: state}},
		Records:   escalationRecords(t),
		Decisions: judgedItems{counters: map[string]runstate.TriageCounters{}},
		Reruns:    claimedReruns{claimed: map[string][]runstate.Rerun{}},
		Manager:   judge,
		Clock:     escalationClock{},
	}

	sweep, err := escalator.Escalate(context.Background())
	if err != nil {
		t.Fatalf("Escalate() error = %v", err)
	}
	if len(sweep.Escalated) != 1 || !sweep.Escalated[0].Delivered {
		t.Fatalf("escalated = %#v, want the escalation this pass delivered", sweep.Escalated)
	}
	if sweep.Escalated[0].Class != triage.ClassEscalation {
		t.Errorf("class = %q, want the pass to report what she was shown", sweep.Escalated[0].Class)
	}
	if len(judge.shown) != 1 || judge.shown[0].Escalation == nil {
		t.Fatalf("shown = %#v, want the judgement she decides from", judge.shown)
	}
	// What a pass says about it names an escalation rather than a stoppage: an
	// operator told the second in the words of the first goes looking for a
	// failure nobody had.
	if rendered := sweep.Render(); !strings.Contains(rendered, "the escalation raised by run "+state.RunID) {
		t.Errorf("rendered = %q, want the pass to say what it put to her", rendered)
	}
	// And it is put to her once. The delivery record is what bounds it, exactly as
	// it bounds a stoppage.
	again, err := escalator.Escalate(context.Background())
	if err != nil {
		t.Fatalf("Escalate() error = %v", err)
	}
	if len(again.Escalated) != 0 || len(judge.shown) != 1 {
		t.Fatalf("the escalation was put to her twice: %#v", again.Escalated)
	}
}

// escalatedState is a run that ended on a reviewer's escalation: successful,
// nothing integrated, no blocker, and its change preserved.
func escalatedState(runID, workItemID string) runstate.State {
	state := reviewStoppedState(runID, workItemID)
	state.Status = runstate.StatusSucceeded
	state.Phase = runstate.PhaseComplete
	state.RepairAttempts = 0
	state.ReviewRounds = 1
	state.ReviewDecision = runstate.ReviewEscalate
	state.ReviewSummary = "the acceptance criteria ask for what a design ruling forbade; this needs replanning"
	state.ReviewFindings = 0
	state.ReviewFindingDetails = nil
	state.Blocker = ""
	return state
}

func escalatedEntry(state runstate.State) triage.Entry {
	return triage.Entry{
		SchemaVersion: triage.SchemaVersion,
		Key:           triage.Key(triage.ClassEscalation, state.RunID),
		Class:         triage.ClassEscalation,
		ProductID:     domain.ProductID("yoyodyne"),
		RunID:         state.RunID,
		WorkItemID:    state.WorkItemID,
		WorkItemTitle: state.WorkItemTitle,
		RecordedAt:    state.UpdatedAt,
		Escalation: &triage.Escalation{
			RaisedBy: state.EscalatedBy(),
			Reason:   state.EscalationReason(),
		},
	}
}

// A developer that escalates publishes nothing and is never checked. The gate in
// front of the checks is where a change with something wrong with it would be
// caught, and there is no change here to catch anything in — so a run that ran
// the suite anyway would be spending the round the verb exists to save.
func TestADeveloperEscalationRunsNoChecksAndPublishesNothing(t *testing.T) {
	t.Parallel()

	tracker := newOutcomeTracker()
	ran := filepath.Join(t.TempDir(), "check-ran")
	provider := roleBackend(func(request backend.RunRequest) error { return nil }, approveVerdict)
	provider.developerFinalText = landingBlock(`{"outcome":"escalate","why":"this item asks for a store a developer run cannot reach"}`)
	pipeline, _ := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider,
		[]string{"touch " + ran})

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !outcome.Escalated() {
		t.Fatalf("the run did not escalate: %#v", outcome)
	}
	if _, err := os.Stat(ran); err == nil {
		t.Error("the checks ran on a run whose developer had already said the item cannot be met")
	}
	if len(outcome.Checks) != 0 {
		t.Errorf("checks = %#v, want none bought", outcome.Checks)
	}
	if outcome.PullRequest != nil {
		t.Errorf("an escalation published %#v", outcome.PullRequest)
	}
}

// The item that a work item's own tracker cannot be reopened for. An escalation
// is raised after the change is done with, so a tracker that refuses the reopen
// is a run whose item is left claimed — which is a failure rather than something
// to record as a success.
func TestAnEscalationThatCannotParkItsItemFailsRatherThanSayingItDid(t *testing.T) {
	t.Parallel()

	tracker := newOutcomeTracker()
	tracker.reopenErr = errors.New("the tracker refused to reopen the item")
	provider := roleBackend(writeFeature, approveVerdict)
	provider.developerFinalText = landingBlock(`{"outcome":"escalate","why":"the criteria contradict a ruling"}`)
	pipeline, _ := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err == nil {
		t.Fatalf("Run() error = nil, want the run to report the item it could not park: %#v", outcome)
	}
	if outcome.Status == runstate.StatusSucceeded {
		t.Errorf("status = %q, want a run that could not leave its item anywhere to say so", outcome.Status)
	}
	if !strings.Contains(err.Error(), "park the work item") {
		t.Errorf("error = %v, want it to name what could not be done", err)
	}
}

// The item is never left waiting on another work item by an escalation. A
// developer that can name the impediment has the evidence landing for it; this
// verb is the answer for when nothing in the backlog is what the item waits on,
// and the parking is what holds it while the decision is pending.
func TestAnEscalationRefusesToNameAnImpediment(t *testing.T) {
	t.Parallel()

	tracker := newOutcomeTracker()
	provider := roleBackend(writeFeature, approveVerdict)
	provider.developerFinalText = landingBlock(
		`{"outcome":"escalate","why":"the criteria contradict a ruling","blocked_by":"yoyodyne-impediment"}`)
	pipeline, store := newAutomaticPipeline(t, pipelineRepository(t), tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	// The claim is refused where it is read, so the run carries on as one whose
	// developer claimed a landing nobody could read — which withholds the closure
	// rather than closing the item on a claim the harness had to guess at.
	if outcome.Escalated() {
		t.Fatalf("a claim the contract refuses was read as an escalation: %#v", outcome)
	}
	if outcome.LandingProblem == "" {
		t.Fatalf("the refused claim was silently discarded: %#v", outcome)
	}
	if !strings.Contains(outcome.LandingProblem, "only for a landing of evidence") {
		t.Errorf("landing problem = %q, want it to name the rule the claim broke", outcome.LandingProblem)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.Discharges() {
		t.Error("an unreadable claim closed its item")
	}
	if len(tracker.blockers) > 0 {
		t.Errorf("the item was made to wait on the impediment a refused claim named: %v", tracker.blockers)
	}
}
