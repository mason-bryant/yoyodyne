package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// carryOutOver wires a carry-out onto the same durable state a re-run acts on,
// so what the sweep fires and what the verb fires are one action rather than two
// accounts of one.
func (h *rerunHarness) carryOut() CarryOut { return h.carryOutAt(0) }

// carryOutAt is the same carry-out reading the world a while later, which is what
// the pacing of a refused decision is measured against.
func (h *rerunHarness) carryOutAt(after time.Duration) CarryOut {
	return CarryOut{
		Docket:    h.docket,
		Decisions: h.runs.Triage(),
		Reruns:    h.reruns,
		Runs:      h.runs,
		Rerunner:  h.rerunner(),
		Holds:     h.holds,
		Clock:     laterClock{after: after},
	}
}

// laterClock is the harness clock moved on by a fixed amount, so a test can put a
// refusal's pacing behind it without spending the interval.
type laterClock struct{ after time.Duration }

func (c laterClock) Now() time.Time { return docketedNow.Add(c.after) }

// theOneOutstanding is the single decision the sweep should have found, or a
// failure naming what it found instead: every sequence below turns on the sweep
// choosing exactly one thing to fire.
func theOneOutstanding(t *testing.T, carrying CarryOut) CarryOutTask {
	t.Helper()
	outstanding, err := carrying.Outstanding()
	if err != nil {
		t.Fatalf("Outstanding() error = %v", err)
	}
	if len(outstanding) != 1 {
		t.Fatalf("outstanding = %#v, want the one decision nobody has acted on", outstanding)
	}
	return outstanding[0]
}

// The condition this exists to end: a decision the development manager recorded
// causes the work, with nobody typing a verb. What it fires is the same action
// the verb fires, under the same attribution.
func TestARecordedDecisionIsCarriedOutWithNobodyAskingForIt(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	carrying := harness.carryOut()
	task := theOneOutstanding(t, carrying)
	if task.Decision != runstate.TriageDecisionRerun || task.RunID != docketedRunID {
		t.Fatalf("task = %#v, want the re-run decided about the docketed stoppage", task)
	}
	// The reasoning travels from the record the development manager wrote rather
	// than from anything this package composed.
	if task.Reason != rerunReasoning {
		t.Fatalf("reason = %q, want the reasoning the decision was recorded with", task.Reason)
	}
	carried, outcome, err := carrying.Carry(context.Background(), task)
	if err != nil {
		t.Fatalf("Carry() error = %v", err)
	}
	if !carried.Carried || len(harness.started) != 1 {
		t.Fatalf("carried = %#v, started = %#v, want the decision fired once", carried, harness.started)
	}
	if outcome.RunID == "" {
		t.Fatalf("outcome = %#v, want the fresh run the carry-out started", outcome)
	}
	if !strings.Contains(carried.Reason, rerunReasoning) || !strings.Contains(carried.Reason, decidedIn) {
		t.Fatalf("reason = %q, want the decision cited to the record it was read from", carried.Reason)
	}
	// And it is not outstanding afterwards, which is what stops the next pass
	// firing the same decision again.
	outstanding, err := carrying.Outstanding()
	if err != nil {
		t.Fatalf("Outstanding() after the carry-out error = %v", err)
	}
	if len(outstanding) != 0 {
		t.Fatalf("outstanding = %#v, want nothing left once the decision has been acted on", outstanding)
	}
}

// The gate the invariant turns on: a carry-out is the harness choosing work, so
// the operator's hold stops it — and stops it visibly, because a decision that
// silently fails to fire is the condition this replaces rather than a new form
// of it.
func TestAHeldIntakeStopsTheCarryOutAndSaysSoOnTheItem(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	if _, err := harness.intake.Hold("the queue is heading somewhere odd", docketedNow); err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	carrying := harness.carryOut()
	carried, _, err := carrying.Carry(context.Background(), theOneOutstanding(t, carrying))
	if err != nil {
		t.Fatalf("Carry() error = %v, want a gate reported rather than a failure", err)
	}
	if carried.Carried || len(harness.started) != 0 {
		t.Fatalf("carried = %#v, started = %#v, want nothing fired under a hold", carried, harness.started)
	}
	if carried.Gate != runstate.TriageGateIntakeHold || !carried.Waiting {
		t.Fatalf("gate = %q, waiting = %t, want the intake hold reported as a gate that clears on its own", carried.Gate, carried.Waiting)
	}
	// The finding is on the item's own record, which is where the docket entry the
	// development manager reads joins it from.
	counters, err := harness.runs.Triage().Counters(docketedItem)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	recorded, found := counters.CarryOutOf(docketedRunID)
	if !found {
		t.Fatalf("counters = %#v, want the refusal recorded against the stoppage it was about", counters)
	}
	if recorded.Gate != runstate.TriageGateIntakeHold || !strings.Contains(recorded.Clears, "yoyo release") {
		t.Fatalf("finding = %#v, want the gate named and what clears it", recorded)
	}
	// Nothing was spent, so releasing the hold carries out the same decision.
	if _, _, err := harness.intake.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if _, _, err := carrying.Carry(context.Background(), theOneOutstanding(t, carrying)); err != nil {
		t.Fatalf("Carry() after the hold was released error = %v", err)
	}
	if len(harness.started) != 1 {
		t.Fatalf("started = %#v, want the decision carried out once the hold was lifted", harness.started)
	}
}

// A decision the harness fires while the operator has paused spending is one it
// must not begin at all: a repair writes to the item and the run before it starts
// anything, so a pause noticed later is a paused harness that nonetheless
// unblocked an item.
func TestTheOperatorsPauseStopsTheCarryOutBeforeAnythingIsAttempted(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	if _, err := harness.holds.Hold(docketedNow); err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	carrying := harness.carryOut()
	carried, _, err := carrying.Carry(context.Background(), theOneOutstanding(t, carrying))
	if err != nil {
		t.Fatalf("Carry() error = %v, want the pause reported rather than a failure", err)
	}
	if carried.Carried || len(harness.started) != 0 {
		t.Fatalf("carried = %#v, started = %#v, want nothing attempted under a pause", carried, harness.started)
	}
	if carried.Gate != runstate.TriageGateSpendingPause || !carried.Waiting {
		t.Fatalf("gate = %q, waiting = %t, want the pause reported as a gate that clears on its own", carried.Gate, carried.Waiting)
	}
	if _, claimed, err := harness.reruns.Find(triage.Key(triage.ClassStoppedRun, docketedRunID)); err != nil || claimed {
		t.Fatalf("claimed = %t, error = %v, want the stoppage to keep its re-run", claimed, err)
	}
}

// yoyodyne-ifd.299's guarantee, exercised through the pass rather than assumed:
// a carry-out refused before anything was claimed leaves the budget where it was,
// so the same decision is carried out by asking again once the refusal no longer
// applies. The item's own state is the refusal that provoked it.
func TestACarryOutRefusedPreFlightSpendsNothingAndSaysWhatWouldClearIt(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	// The item is still blocked by the run that stopped, which is what a fresh run
	// of it would start from.
	harness.item.Status = "closed"
	carrying := harness.carryOut()
	carried, _, err := carrying.Carry(context.Background(), theOneOutstanding(t, carrying))
	if err != nil {
		t.Fatalf("Carry() error = %v, want the refusal reported rather than raised", err)
	}
	if carried.Carried || len(harness.started) != 0 {
		t.Fatalf("carried = %#v, started = %#v, want nothing started on an item no run may start on", carried, harness.started)
	}
	if carried.Gate != runstate.TriageGateWorkItem || carried.Waiting {
		t.Fatalf("gate = %q, waiting = %t, want the item's own state named as a gate somebody has to open", carried.Gate, carried.Waiting)
	}
	// Nothing was claimed, which is the whole of what makes asking again worth
	// anything.
	if _, claimed, err := harness.reruns.Find(triage.Key(triage.ClassStoppedRun, docketedRunID)); err != nil || claimed {
		t.Fatalf("claimed = %t, error = %v, want the stoppage to keep its re-run", claimed, err)
	}
	counters, err := harness.runs.Triage().Counters(docketedItem)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if counters.Reruns != 1 {
		t.Fatalf("reruns = %d, want the decision's own spend and nothing more", counters.Reruns)
	}
	// And the same decision fires once the item is back, on the budget that was
	// never touched. It is asked past the pacing the refusal put on it, which is
	// what a later pass reaches on its own.
	harness.item.Status = "open"
	carrying = harness.carryOutAt(runstate.TriageCarryOutRetryDelay + time.Minute)
	if _, _, err := carrying.Carry(context.Background(), theOneOutstanding(t, carrying)); err != nil {
		t.Fatalf("Carry() after the item was put back error = %v", err)
	}
	if len(harness.started) != 1 {
		t.Fatalf("started = %#v, want the same decision carried out once the item was put back", harness.started)
	}
}

// A refusal that needs somebody to act is paced, or one refused decision spends
// every pass's single carry-out and starves the decided stoppages behind it.
func TestARefusedDecisionIsLeftAloneUntilItsPacingHasPassed(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	harness.item.Status = "closed"
	carrying := harness.carryOut()
	if _, _, err := carrying.Carry(context.Background(), theOneOutstanding(t, carrying)); err != nil {
		t.Fatalf("Carry() error = %v", err)
	}
	outstanding, err := carrying.Outstanding()
	if err != nil {
		t.Fatalf("Outstanding() error = %v", err)
	}
	if len(outstanding) != 0 {
		t.Fatalf("outstanding = %#v, want a refused decision left to its pacing rather than tried again at once", outstanding)
	}
	// The finding stands on the record the whole time, which is what keeps the
	// pacing from being silence.
	counters, err := harness.runs.Triage().Counters(docketedItem)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if _, found := counters.CarryOutOf(docketedRunID); !found {
		t.Fatalf("counters = %#v, want the refusal readable for as long as it stands", counters)
	}
}

// A gate that clears without anybody doing anything is not paced, or a decision
// waits a quarter of an hour after the switch it was waiting on was opened.
func TestAGateThatClearsOnItsOwnIsTriedAgainAtOnce(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	if _, err := harness.intake.Hold("the queue is heading somewhere odd", docketedNow); err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	carrying := harness.carryOut()
	if _, _, err := carrying.Carry(context.Background(), theOneOutstanding(t, carrying)); err != nil {
		t.Fatalf("Carry() error = %v", err)
	}
	if _, _, err := harness.intake.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	outstanding, err := carrying.Outstanding()
	if err != nil {
		t.Fatalf("Outstanding() error = %v", err)
	}
	if len(outstanding) != 1 {
		t.Fatalf("outstanding = %#v, want the decision offered again the moment the hold was lifted", outstanding)
	}
}

// A decision whose carry-out fired once is not offered again, and neither is a
// stoppage of an item something is already running: both would be the harness
// spending a slot to be refused.
func TestDecisionsNothingCanActOnAreNotOffered(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	live := stoppedState()
	live.RunID = "run-aaaabbbbccccddddeeeeffff00001111"
	live.Status = runstate.StatusRunning
	live.CompletedAt = nil
	live.Blocker = ""
	if err := harness.runs.Create(live); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	outstanding, err := harness.carryOut().Outstanding()
	if err != nil {
		t.Fatalf("Outstanding() error = %v", err)
	}
	if len(outstanding) != 0 {
		t.Fatalf("outstanding = %#v, want nothing offered for an item with a run in flight", outstanding)
	}
}

// The three decisions that ask for no run at all are not the harness's to fire.
// A carry-out that acted on one would be starting work nobody decided to start.
func TestOnlyTheTwoDecisionsThatAskForARunAreCarriedOut(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	if _, err := harness.runs.Triage().RecordDecision(context.Background(), docketedItem,
		triageDecided(runstate.TriageDecisionWait, docketedRunID), docketedNow); err != nil {
		t.Fatalf("RecordDecision() error = %v", err)
	}
	outstanding, err := harness.carryOut().Outstanding()
	if err != nil {
		t.Fatalf("Outstanding() error = %v", err)
	}
	if len(outstanding) != 0 {
		t.Fatalf("outstanding = %#v, want nothing fired for a decision that asks for no run", outstanding)
	}
}

// yoyodyne-ifd.309's mechanism, exercised rather than assumed: a decision that
// only exists because a recorded cap crossing permitted it is carried out like
// any other, and the run's own account names the crossing it stands on.
func TestADecisionStandingOnARecordedCapCrossingIsCarriedOut(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	// The item is at the end of its one re-run, so a second decision is refused
	// until somebody crosses that cap.
	second := "run-cccc2222dddd3333eeee4444ffff5555"
	stopped := stoppedState()
	stopped.RunID = second
	if err := harness.runs.Create(stopped); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := docketerOver(nil, harness.docket).RecordStoppedRun(stopped); err != nil {
		t.Fatalf("RecordStoppedRun() error = %v", err)
	}
	ctx := context.Background()
	_, err := harness.runs.Triage().RecordRerun(ctx, docketedItem, triageDecided(runstate.TriageDecisionRerun, second), docketedNow, rerunCaps)
	if !errors.Is(err, runstate.ErrTriageCapReached) {
		t.Fatalf("RecordRerun() error = %v, want the cap to refuse the second decision", err)
	}
	if _, err := harness.runs.Triage().Override(ctx, docketedItem, runstate.TriageOverride{
		Budget:    runstate.TriageRerunBudget,
		Cap:       2,
		DecidedBy: "mason-bryant",
		Reason:    "the first re-run met a broken toolchain rather than the work",
	}, docketedNow, rerunCaps); err != nil {
		t.Fatalf("Override() error = %v", err)
	}
	if _, err := harness.runs.Triage().RecordRerun(ctx, docketedItem, triageDecided(runstate.TriageDecisionRerun, second), docketedNow, rerunCaps); err != nil {
		t.Fatalf("RecordRerun() after the crossing error = %v", err)
	}
	carrying := harness.carryOut()
	outstanding, err := carrying.Outstanding()
	if err != nil {
		t.Fatalf("Outstanding() error = %v", err)
	}
	if len(outstanding) != 2 {
		t.Fatalf("outstanding = %#v, want both decided stoppages offered", outstanding)
	}
	carried, _, err := carrying.Carry(ctx, outstanding[0])
	if err != nil {
		t.Fatalf("Carry() error = %v", err)
	}
	if !carried.Carried {
		t.Fatalf("carried = %#v, want the decision the crossing permitted carried out", carried)
	}
	if !strings.Contains(carried.Reason, "operator override") || !strings.Contains(carried.Reason, "mason-bryant") {
		t.Fatalf("reason = %q, want the crossing the decision stands on named in the run's own account", carried.Reason)
	}
}

// A carry-out with nothing wired to it fires nothing rather than reporting that
// it did, which is the direction every optional part of a pull fails in.
func TestACarryOutWithoutItsPartsRefuses(t *testing.T) {
	t.Parallel()

	if _, err := (CarryOut{}).Outstanding(); err == nil {
		t.Fatalf("Outstanding() = nil, want a refusal naming what is missing")
	}
	if _, _, err := (CarryOut{}).Carry(context.Background(), CarryOutTask{}); err == nil {
		t.Fatalf("Carry() = nil, want a refusal naming what is missing")
	}
}

// A gate is named from the sentinel the action exports rather than from the words
// of its refusal, because a gate matched on wording is one that changes when
// somebody rewrites a sentence — and this one is written into a record somebody
// acts on.
func TestEveryGateIsNamedFromTheRefusalsSentinel(t *testing.T) {
	t.Parallel()

	for name, refusal := range map[string]struct {
		err  error
		want string
	}{
		"a worktree somebody has been in": {WorktreeSurgeryError{RunID: docketedRunID}, runstate.TriageGatePreservedWork},
		"a worktree holding no change":    {MissingPreservedChangeError{RunID: docketedRunID}, runstate.TriageGatePreservedWork},
		"an item no run may start on":     {ErrItemNotStartable, runstate.TriageGateWorkItem},
		"a budget that is spent":          {runstate.TriageCapError{Action: runstate.TriageRerun}, runstate.TriageGateBudget},
		"a stoppage already re-run":       {runstate.RerunTakenError{}, runstate.TriageGateBudget},
		"anything else":                   {errors.New("the store would not answer"), runstate.TriageGateHarness},
	} {
		gate, clears := carryOutGate(refusal.err)
		if gate != refusal.want {
			t.Fatalf("%s: gate = %q, want %q", name, gate, refusal.want)
		}
		if strings.TrimSpace(clears) == "" {
			t.Fatalf("%s: nothing says what would clear it, which is the whole of what a finding is worth", name)
		}
	}
	// Every gate the record permits is one this file declares, so a finding can
	// never name something the vocabulary does not have.
	for _, gate := range []string{runstate.TriageGateSpendingPause, runstate.TriageGateIntakeHold, runstate.TriageGateCapacity} {
		if !strings.Contains(strings.Join(runstate.TriageGateVocabulary(), "|"), gate) {
			t.Fatalf("gate %q is not in the vocabulary", gate)
		}
	}

}

// Outstanding and Carry stand in for the two halves of a carry-out: the reading
// that costs no slot, and the firing that takes one. They are separate here for
// the reason they are separate in the interface — the pass has to know there is
// something to fire before it spends a slot on finding out.
func (h *scheduleHarness) Outstanding() ([]CarryOutTask, error) {
	h.mu.Lock()
	outstanding := h.outstanding
	h.mu.Unlock()
	return outstanding(h)
}

func (h *scheduleHarness) Carry(_ context.Context, task CarryOutTask) (CarriedOut, Outcome, error) {
	h.mu.Lock()
	h.carried = append(h.carried, task)
	carry := h.carry
	h.mu.Unlock()
	return carry(h, task)
}

// outstandingUntilCarried is the fake reading what the real one reads: a decision
// the harness has attempted is not outstanding on the next pull — either it fired
// and is spent, or a gate stopped it and its pacing has it. Without that a drain
// would offer the same decision on every pull for ever, which is the loop the
// durable records exist to close.
func outstandingUntilCarried(tasks ...CarryOutTask) func(*scheduleHarness) ([]CarryOutTask, error) {
	return func(h *scheduleHarness) ([]CarryOutTask, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		var waiting []CarryOutTask
		for _, task := range tasks {
			attempted := false
			for _, done := range h.carried {
				attempted = attempted || done.WorkItemID == task.WorkItemID
			}
			if !attempted {
				waiting = append(waiting, task)
			}
		}
		return waiting, nil
	}
}

// decidedTask is one recorded decision the pass may fire, named after the item
// it is about so a test reads the report the way an operator would.
func decidedTask(workItemID string) CarryOutTask {
	return CarryOutTask{
		WorkItemID: workItemID,
		RunID:      docketedRunID,
		DocketKey:  triage.Key(triage.ClassStoppedRun, docketedRunID),
		Decision:   runstate.TriageDecisionRerun,
		Reason:     rerunReasoning,
	}
}

// The pass fires a recorded decision itself, against the same capacity the
// queue's own work is chosen against: this is the whole of what "no person
// involved" means, and the run it starts is accounted for like any other.
func TestAPassCarriesOutARecordedDecisionBesideTheQueuesOwnWork(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-ifd.500")...)
	harness.capacity = 2
	harness.outstanding = outstandingUntilCarried(decidedTask("yoyodyne-ifd.346"))
	harness.carry = func(h *scheduleHarness, task CarryOutTask) (CarriedOut, Outcome, error) {
		return CarriedOut{
			WorkItemID: task.WorkItemID, RunID: task.RunID, Decision: task.Decision,
			Carried: true, Reason: "the development manager's triage decided a re-run, " + rerunReasoning,
		}, h.complete(task.WorkItemID), nil
	}
	schedule, err := (Scheduler{Open: harness.open}).Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	// Once, however many pulls the drain made: the item the decision is about is
	// occupied by the run this started, exactly as a pulled item is, so nothing
	// puts a second developer on it.
	if len(harness.carried) != 1 || harness.carried[0].WorkItemID != "yoyodyne-ifd.346" {
		t.Fatalf("carried = %#v, want the decision fired exactly once", harness.carried)
	}
	if len(schedule.CarriedOut) != 1 || !schedule.CarriedOut[0].Carried {
		t.Fatalf("carried out = %#v, want the pass to account for the decision it fired", schedule.CarriedOut)
	}
	// It is a run as well, so it is among the started runs with the reason the
	// action recorded rather than the placeholder the pass started it under.
	var started *Started
	for index := range schedule.Started {
		if schedule.Started[index].WorkItemID == "yoyodyne-ifd.346" {
			started = &schedule.Started[index]
		}
	}
	if started == nil {
		t.Fatalf("started = %#v, want the carried-out decision accounted for as a run", schedule.Started)
	}
	if !strings.Contains(started.Reason, rerunReasoning) {
		t.Fatalf("reason = %q, want the reasoning the decision was recorded with", started.Reason)
	}
	// And the queue's own work was pulled beside it, because a carry-out takes one
	// slot rather than the pass.
	if len(schedule.Started) != 2 {
		t.Fatalf("started = %#v, want the queue's work pulled beside the decision", schedule.Started)
	}
}

// One per pull, so a backlog of decided stoppages reaches a developer over
// several polls rather than spending the whole harness at once.
func TestAPullFiresOneDecisionHoweverManyAreOutstanding(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness()
	harness.capacity = 3
	harness.outstanding = outstandingUntilCarried(
		decidedTask("yoyodyne-ifd.346"), decidedTask("yoyodyne-ifd.347"), decidedTask("yoyodyne-ifd.348"))
	harness.carry = func(h *scheduleHarness, task CarryOutTask) (CarriedOut, Outcome, error) {
		return CarriedOut{
			WorkItemID: task.WorkItemID, RunID: task.RunID, Decision: task.Decision,
			Carried: true, Reason: rerunReasoning,
		}, h.complete(task.WorkItemID), nil
	}
	// One pull's worth: the bound the operator puts on a pass stops it at the
	// first run, which is the carry-out.
	schedule, err := (Scheduler{Open: harness.open, Limit: 1}).Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(harness.carried) != 1 || harness.carried[0].WorkItemID != "yoyodyne-ifd.346" {
		t.Fatalf("carried = %#v, want one decision fired, the oldest first", harness.carried)
	}
	if schedule.Stopped != ScheduleLimitReached {
		t.Fatalf("stopped = %q, want the pass bounded by the limit it was given", schedule.Stopped)
	}
}

// A decision a gate stopped started nothing, so it must not be counted as a run
// that failed: pricing it would charge the session for a run that does not
// exist, and counting it toward the failure storm would have the brake hold
// intake because a decision was waiting on the intake hold.
func TestADecisionAGateStoppedIsNotARunThatFailed(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness()
	harness.blockedRuns = 1
	harness.outstanding = outstandingUntilCarried(decidedTask("yoyodyne-ifd.346"))
	harness.carry = func(_ *scheduleHarness, task CarryOutTask) (CarriedOut, Outcome, error) {
		return CarriedOut{
			WorkItemID: task.WorkItemID, RunID: task.RunID, Decision: task.Decision,
			Gate: runstate.TriageGateIntakeHold, Waiting: true,
			Problem: "the re-run is waiting on the operator's hold on what the harness chooses",
		}, Outcome{}, nil
	}
	schedule, err := (Scheduler{Open: harness.open}).Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.CarriedOut) != 0 {
		t.Fatalf("carried out = %#v, want an attempt a gate stopped reported as a problem rather than as work done", schedule.CarriedOut)
	}
	if !strings.Contains(schedule.CarryOutProblem, runstate.TriageGateIntakeHold) {
		t.Fatalf("problem = %q, want the gate that stopped it named on the pass", schedule.CarryOutProblem)
	}
	if schedule.Failed() {
		t.Fatalf("schedule = %#v, want a stopped carry-out not to fail the pass", schedule)
	}
	if schedule.BlockedInARow != 0 || schedule.Braked != nil {
		t.Fatalf("blocked in a row = %d, braked = %#v, want nothing counted toward the failure storm", schedule.BlockedInARow, schedule.Braked)
	}
	if len(schedule.Started) != 1 || schedule.Started[0].Failure != "" || schedule.Started[0].Declined == "" {
		t.Fatalf("started = %#v, want a start that never became a run rather than a run that failed", schedule.Started)
	}
}

// A reading of the decisions that failed leaves the queue's own work exactly as
// it was and says so, because a decision fired on a record nobody could read
// would be the one thing worse than one that waits.
func TestAReadingOfTheDecisionsThatFailedDoesNotStopThePass(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-ifd.500")...)
	harness.outstanding = func(*scheduleHarness) ([]CarryOutTask, error) {
		return nil, errors.New("the triage record would not be read")
	}
	harness.carry = func(_ *scheduleHarness, _ CarryOutTask) (CarriedOut, Outcome, error) {
		t.Fatalf("nothing should be fired from a reading that failed")
		return CarriedOut{}, Outcome{}, nil
	}
	schedule, err := (Scheduler{Open: harness.open}).Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if !strings.Contains(schedule.CarryOutReadProblem, "would not be read") {
		t.Fatalf("problem = %q, want the reading that failed said out loud", schedule.CarryOutReadProblem)
	}
	if len(schedule.Started) != 1 || schedule.Started[0].WorkItemID != "yoyodyne-ifd.500" {
		t.Fatalf("started = %#v, want the queue's own work pulled regardless", schedule.Started)
	}
}

// The finding reaches the development manager where she is already looking. It is
// joined onto the entry where the docket is read rather than written into the log,
// because the attempt is made after the decision, which is made after the entry.
func TestTheDocketCarriesTheGateThatStoppedTheCarryOut(t *testing.T) {
	t.Parallel()

	harness := newRerunHarness(t, stoppedState())
	harness.item.Status = "closed"
	carrying := harness.carryOut()
	if _, _, err := carrying.Carry(context.Background(), theOneOutstanding(t, carrying)); err != nil {
		t.Fatalf("Carry() error = %v", err)
	}
	docket := docketerDeciding(nil, harness.docket, harness.runs.Triage(), harness.reruns)
	built, err := docket.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(built.Entries) != 1 {
		t.Fatalf("entries = %#v, want the one docketed stoppage", built.Entries)
	}
	entry := built.Entries[0]
	if entry.CarryOut == nil {
		t.Fatalf("entry = %#v, want the refusal joined onto it", entry)
	}
	if entry.CarryOut.Gate != runstate.TriageGateWorkItem || entry.CarryOut.Decision != runstate.TriageDecisionRerun {
		t.Fatalf("carry-out = %#v, want the gate that refused and the decision it was carrying out", entry.CarryOut)
	}
	if !strings.Contains(entry.Render(), runstate.TriageGateWorkItem) {
		t.Fatalf("rendered entry does not name the gate:\n%s", entry.Render())
	}
	// And it goes once the decision is carried out, because a finding standing over
	// a run that is happening is the worst thing this could say.
	harness.item.Status = "open"
	later := harness.carryOutAt(runstate.TriageCarryOutRetryDelay + time.Minute)
	if _, _, err := later.Carry(context.Background(), theOneOutstanding(t, later)); err != nil {
		t.Fatalf("Carry() after the item was put back error = %v", err)
	}
	cleared, err := docket.Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if cleared.Entries[0].CarryOut != nil {
		t.Fatalf("carry-out = %#v, want the finding gone once the decision was carried out", cleared.Entries[0].CarryOut)
	}
}

// carryOut wires the firing of a repair over the state a repair-continue acts
// on, so what the pass fires and what the verb fires are one action.
func (h *continueHarness) carryOut() CarryOut {
	return CarryOut{
		Docket:    h.docket,
		Decisions: h.runs.Triage(),
		Reruns:    h.runs.Reruns(),
		Runs:      h.runs,
		Repairer:  h.continuer(),
		Clock:     docketClock{},
	}
}

// grantedAgainstTheStoppage records the repair the development manager decided
// about the docketed stoppage itself, which is what her conversation writes: a
// decision naming another run of the same item is refused where it is recorded.
func grantedAgainstTheStoppage(t *testing.T, harness *continueHarness) {
	t.Helper()
	if _, err := harness.runs.Triage().GrantRepair(context.Background(), docketedItem,
		triageDecided(runstate.TriageDecisionRepair, docketedRunID), continueGrantRounds, docketedNow, continueCaps); err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
}

// The other half of the vocabulary the harness fires, and the half where the
// reasoning matters most: a repair records the development manager's words on the
// run and on the item, so they have to be the words she wrote rather than
// anything the pass composed.
func TestARecordedRepairIsCarriedOutOnTheReasoningSheRecorded(t *testing.T) {
	t.Parallel()

	harness := newUndecidedHarness(t, continuableState())
	grantedAgainstTheStoppage(t, harness)
	carrying := harness.carryOut()
	task := theOneOutstanding(t, carrying)
	if task.Decision != runstate.TriageDecisionRepair || task.Reason != rerunReasoning {
		t.Fatalf("task = %#v, want the repair decided about this stoppage and the reasoning it was recorded with", task)
	}
	carried, outcome, err := carrying.Carry(context.Background(), task)
	if err != nil {
		t.Fatalf("Carry() error = %v", err)
	}
	if !carried.Carried || len(harness.started) != 1 {
		t.Fatalf("carried = %#v, started = %#v, want the stopped run continued once", carried, harness.started)
	}
	// The same run rather than a fresh one, which is the whole difference between
	// the two decisions the harness fires.
	if harness.started[0].runID != docketedRunID {
		t.Fatalf("started = %#v, want the stopped run re-entered rather than a fresh one", harness.started)
	}
	if outcome.RunID != docketedRunID {
		t.Fatalf("outcome = %#v, want the continued run", outcome)
	}
	if !strings.Contains(carried.Reason, rerunReasoning) {
		t.Fatalf("reason = %q, want the reasoning read from the record she wrote", carried.Reason)
	}
	// The grant is carried out, so nothing offers the same decision again.
	outstanding, err := carrying.Outstanding()
	if err != nil {
		t.Fatalf("Outstanding() error = %v", err)
	}
	if len(outstanding) != 0 {
		t.Fatalf("outstanding = %#v, want the grant spent", outstanding)
	}
}

// The preserved-work rule the item names, met by the pass rather than by a
// person: what is in that worktree is what a continued developer would be handed
// back, so a repair the harness cannot make safely says so and spends nothing.
func TestAPreservedWorktreeSomebodyHasBeenInStopsTheCarryOutAndSaysWhy(t *testing.T) {
	t.Parallel()

	harness := newUndecidedHarness(t, continuableState())
	grantedAgainstTheStoppage(t, harness)
	harness.ownership.err = errors.New("HEAD is a commit the harness did not make")
	carrying := harness.carryOut()
	carried, _, err := carrying.Carry(context.Background(), theOneOutstanding(t, carrying))
	if err != nil {
		t.Fatalf("Carry() error = %v, want the refusal reported rather than raised", err)
	}
	if carried.Carried || len(harness.started) != 0 {
		t.Fatalf("carried = %#v, started = %#v, want nothing continued in a worktree somebody has been in", carried, harness.started)
	}
	if carried.Gate != runstate.TriageGatePreservedWork || carried.Waiting {
		t.Fatalf("gate = %q, waiting = %t, want the preserved work named as a gate a person has to open", carried.Gate, carried.Waiting)
	}
	if harness.carried(t) != 0 {
		t.Fatalf("the grant was spent on a repair that never happened")
	}
}

// The gate the pass itself would otherwise swallow. A held intake stops the pull
// before it chooses anything, so a carry-out placed after that would never be
// attempted while the hold stood — and a decision that cannot be carried out with
// nothing anywhere saying why is the one outcome this mechanism forbids. Nothing
// is claimed under the hold: the action reads it again and refuses, and what the
// pass keeps is the refusal.
func TestAHeldIntakeStillAttemptsTheCarryOutSoTheRefusalIsRecorded(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness(readyItems("yoyodyne-ifd.500")...)
	harness.capacity = 2
	harness.held = &runstate.IntakeHold{HeldAt: harness.now, Reason: "the queue is heading somewhere odd"}
	harness.outstanding = outstandingUntilCarried(decidedTask("yoyodyne-ifd.346"))
	harness.carry = func(_ *scheduleHarness, task CarryOutTask) (CarriedOut, Outcome, error) {
		// What the action reports when it reads the same hold the pass just read.
		// Nothing is claimed and nothing is started; the finding is the whole of it.
		return CarriedOut{
			WorkItemID: task.WorkItemID, RunID: task.RunID, Decision: task.Decision,
			Gate: runstate.TriageGateIntakeHold, Waiting: true,
			Problem: "the \"rerun\" the development manager decided is waiting on " + runstate.TriageGateIntakeHold,
		}, Outcome{}, nil
	}
	schedule, err := (Scheduler{Open: harness.open}).Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(harness.carried) != 1 {
		t.Fatalf("carried = %#v, want the decision attempted so the hold has somewhere to be recorded", harness.carried)
	}
	if !strings.Contains(schedule.CarryOutProblem, runstate.TriageGateIntakeHold) {
		t.Fatalf("problem = %q, want the hold named as the gate that stopped it", schedule.CarryOutProblem)
	}
	// The pass still chose nothing, which is what the hold is for.
	if schedule.Stopped != ScheduleIntakeHeld || schedule.IntakeHeld == nil {
		t.Fatalf("stopped = %q, held = %#v, want the pass to have chosen nothing under the hold", schedule.Stopped, schedule.IntakeHeld)
	}
	for _, started := range schedule.Started {
		if started.WorkItemID == "yoyodyne-ifd.500" {
			t.Fatalf("started = %#v, want no queue work chosen under a held intake", schedule.Started)
		}
	}
}

// The same for the other gate the pass short-circuits on. A full harness stops
// the pull before it reaches the queue, so a carry-out placed after that would be
// silent for as long as every slot was taken.
func TestAFullHarnessStillAttemptsTheCarryOutSoTheRefusalIsRecorded(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness()
	harness.capacity = 1
	// One slot, taken by a run this pass did not start.
	harness.inFlight["yoyodyne-ifd.400"] = runningState("run-aaaabbbbccccddddeeeeffff00002222", "yoyodyne-ifd.400")
	harness.outstanding = outstandingUntilCarried(decidedTask("yoyodyne-ifd.346"))
	harness.carry = func(_ *scheduleHarness, task CarryOutTask) (CarriedOut, Outcome, error) {
		return CarriedOut{
			WorkItemID: task.WorkItemID, RunID: task.RunID, Decision: task.Decision,
			Gate: runstate.TriageGateCapacity, Waiting: true,
			Problem: "the \"rerun\" the development manager decided is waiting on " + runstate.TriageGateCapacity,
		}, Outcome{}, nil
	}
	schedule, err := (Scheduler{Open: harness.open}).Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(harness.carried) != 1 {
		t.Fatalf("carried = %#v, want the decision attempted so the full harness has somewhere to be recorded", harness.carried)
	}
	if !strings.Contains(schedule.CarryOutProblem, runstate.TriageGateCapacity) {
		t.Fatalf("problem = %q, want developer capacity named as the gate that stopped it", schedule.CarryOutProblem)
	}
}

// A reading that failed and an attempt that fired are different facts about one
// pass, and the pass has to keep both: folding them into one line had the
// successful attempt erase the account of the decision nothing could read.
func TestAFiredDecisionDoesNotEraseAReadingThatFailed(t *testing.T) {
	t.Parallel()

	harness := newScheduleHarness()
	harness.capacity = 2
	readable := outstandingUntilCarried(decidedTask("yoyodyne-ifd.346"))
	harness.outstanding = func(h *scheduleHarness) ([]CarryOutTask, error) {
		tasks, _ := readable(h)
		// Part of the record answered and part of it did not, which is what
		// Outstanding reports when one item's triage record cannot be read.
		return tasks, errors.New("the triage record of yoyodyne-ifd.347 would not be read")
	}
	harness.carry = func(h *scheduleHarness, task CarryOutTask) (CarriedOut, Outcome, error) {
		return CarriedOut{
			WorkItemID: task.WorkItemID, RunID: task.RunID, Decision: task.Decision,
			Carried: true, Reason: rerunReasoning,
		}, h.complete(task.WorkItemID), nil
	}
	schedule, err := (Scheduler{Open: harness.open}).Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if len(schedule.CarriedOut) != 1 {
		t.Fatalf("carried out = %#v, want the decision that could be read fired", schedule.CarriedOut)
	}
	if !strings.Contains(schedule.CarryOutReadProblem, "yoyodyne-ifd.347") {
		t.Fatalf("read problem = %q, want the decision nothing could read still accounted for", schedule.CarryOutReadProblem)
	}
	if schedule.CarryOutProblem != "" {
		t.Fatalf("problem = %q, want nothing said about a gate, since none stopped the attempt", schedule.CarryOutProblem)
	}
}
