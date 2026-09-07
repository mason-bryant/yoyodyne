package orchestrator

// Carrying out what the development manager decided, with nobody typing a verb.
//
// The decision has been durable since yoyodyne-ifd.311: the development manager
// records a repair or a re-run in her own conversation, it spends the item's
// budget as it is recorded, and the two actions that act on one read it back
// rather than taking words from whoever ran the command. What that landed was a
// carry-out that carries her decision. What it did not land was one that fires.
//
// Between the decision and the firing sat a person. Thirty-three items stood
// decided and unfired, some for days, because the only thing that executed one
// was the operator's assistant typing `yoyo triage repair`; the development
// manager decided within the hour and then nothing happened, and her own sweeps
// reported it as a standing finding from the day they began. That is the autonomy
// goal failing at the one place the machinery was otherwise complete.
//
// So the harness fires it, on the same pass that chooses everything else it runs.
// What changes is the hand and nothing else: the decision is hers, the gates are
// the ones that already refused a carry-out typed by hand, and the attribution the
// fresh run carries is still read from the record she wrote.
//
// # The gates are the ones that were already there
//
// Nothing here re-implements a gate and nothing here relaxes one. A re-run goes
// through Rerunner and a repair through RepairContinuer, exactly as the verbs do,
// so the intake hold, the item's triage budgets, the once-per-stoppage claim, the
// preserved-work rules, and developer capacity all refuse precisely what they
// refused before. The one gate this reads itself is the operator's pause on
// harness spending, and it is read here rather than left to the pipeline for a
// reason that is specific to the repair: a repair supersedes the stopped run's
// blocker and puts the item back before it starts anything, so a pause met after
// those writes is a paused harness that nonetheless unblocked an item. Reading it
// first is what keeps a paused harness from touching anything at all.
//
// # Every refusal is a finding, because silence is what this replaces
//
// A carry-out that failed quietly would reproduce the condition one item at a
// time: a decision recorded, nothing happening, and nothing anywhere saying why.
// So every gate that stops one is written onto the item's own triage record — the
// gate, what it said, and what would clear it — and the docket entry the
// development manager reads joins it. A decision that cannot be carried out says
// so where she is already looking, and a carry-out that succeeds clears the
// finding as it goes.
//
// # One per pass, and paced when it is refused
//
// A carry-out is a run, so it takes a developer slot and the pass starts it
// exactly as it starts a chosen item: in a goroutine, counted against capacity,
// waited out with everything else. One per pass is what keeps the queue's own
// work from being crowded out by a backlog of decided stoppages, and the next
// pass takes the next.
//
// A refusal that needs something to change is paced. The pass reads the docket
// every poll interval, so an unpaced retry would spend this pass's one carry-out
// on the same refused decision several times a minute and starve every decided
// stoppage behind it. A gate that clears on its own — a pause, a hold, a full
// harness — is not paced, because pacing it would leave a decision uncarried for
// a quarter of an hour after the switch was already open, which is the latency
// this exists to remove.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// CarryOutDocket is the docket the decisions were made against. It is read and
// never written: an entry stands as the record that work stopped however many
// times a decision about it is carried out.
type CarryOutDocket interface {
	List() ([]triage.Entry, error)
}

// CarryOutDecisions is the item's durable triage record: what the development
// manager decided, and what became of the harness's attempts to carry it out.
// The first is read and the second is written, which is the whole of what this
// package adds to that record — nothing here decides anything or spends anything.
//
// It is satisfied by *runstate.TriageStore.
type CarryOutDecisions interface {
	Counters(workItemID string) (runstate.TriageCounters, error)
	RecordCarryOutRefusal(ctx context.Context, workItemID string, refusal runstate.TriageCarryOut, at time.Time) (runstate.TriageCounters, error)
	ClearCarryOut(ctx context.Context, workItemID, runID string, at time.Time) (runstate.TriageCounters, error)
}

// CarryOutReruns is what the harness has already claimed of the re-run decisions.
// It is the other half of what says a decision is outstanding: a decision
// authorizes one re-run, and a claim is what says it was acted on.
//
// It is satisfied by *runstate.RerunStore.
type CarryOutReruns interface {
	Claimed(workItemID string) ([]runstate.Rerun, error)
}

// CarryOutRuns is the durable run state this sweep reads. Both readings answer
// the same question in the two shapes it takes: whether an item has a run in
// flight, which makes it not stopped work at all, and how much of a repair grant
// the item's runs have already been handed, which is what says a repair decision
// still has something left to carry out.
//
// It reads and never writes. What becomes of a run is the run's own to record.
//
// It is satisfied by *runstate.Store.
type CarryOutRuns interface {
	Incomplete() ([]runstate.State, error)
	Recorded() ([]runstate.State, error)
}

// CarryOutRerunner starts a fresh run of an item whose stoppage was decided a
// re-run. It is satisfied by Rerunner.
type CarryOutRerunner interface {
	Rerun(ctx context.Context, request RerunRequest) (RerunResult, error)
}

// CarryOutRepairer re-enters the repair loop of a stopped run whose stoppage was
// decided a repair. It is satisfied by RepairContinuer.
type CarryOutRepairer interface {
	Continue(ctx context.Context, request RepairContinueRequest) (RepairContinueResult, error)
}

// CarryOut fires the decisions the development manager recorded. It decides
// nothing, spends nothing, and grants nothing: what it does is find a decision
// somebody else made that the harness has not acted on, hand it to the action
// that already knows how to act on it, and write down what became of the attempt.
type CarryOut struct {
	Docket CarryOutDocket
	// Decisions is where the decisions are read and where a refused attempt is
	// recorded. Required: a carry-out that could not read the record would be
	// firing on nobody's decision, and one that could not write it would be the
	// silence this exists to end.
	Decisions CarryOutDecisions
	// Reruns and Runs are what has already been carried out of those decisions.
	// Both required: a decision already acted on is not one to act on again, and
	// the two records are the only things that say so.
	Reruns CarryOutReruns
	Runs   CarryOutRuns
	// Rerunner and Repairer are the two actions that carry a decision out. Each is
	// optional on its own — a harness wired with one fires that half and leaves the
	// other for a person, which is what it had before this existed — and a
	// carry-out with neither fires nothing.
	Rerunner CarryOutRerunner
	Repairer CarryOutRepairer
	// Holds is the operator's pause over everything the harness spends. Optional,
	// and a carry-out wired without one is one nothing can pause, which is what
	// every provider invocation was before the switch existed.
	Holds OperatorHolds
	Clock execution.Clock
}

// CarryOutTask is one recorded decision the harness has not acted on: which
// stoppage, what was decided, and the reasoning it was decided on.
//
// The reasoning travels with it because it is what a repair records as why the
// run is going again. It is read from the durable decision rather than composed
// here, for the reason yoyodyne-ifd.311 made it durable: the words attributed to
// the development manager have to be words she wrote.
type CarryOutTask struct {
	WorkItemID string `json:"work_item_id"`
	RunID      string `json:"run_id"`
	DocketKey  string `json:"docket_key"`
	Decision   string `json:"decision"`
	Reason     string `json:"reason"`
}

// CarriedOut is what one attempt came to. It reports an attempt that was stopped
// as carefully as one that fired: a decision that cannot be carried out is the
// thing this whole mechanism exists to stop being silent.
type CarriedOut struct {
	WorkItemID string `json:"work_item_id"`
	RunID      string `json:"run_id"`
	DocketKey  string `json:"docket_key"`
	Decision   string `json:"decision"`
	// Carried reports a run actually started or continued. An attempt a gate
	// stopped is never reported as one that fired.
	Carried bool `json:"carried"`
	// Reason is what the run records as why it is going, which is the development
	// manager's decision cited to the record she wrote it on. It is empty on an
	// attempt that started nothing.
	Reason string `json:"reason,omitempty"`
	// Gate is which gate stopped it, in the durable record's own vocabulary, and
	// Waiting says that gate clears without anybody doing anything. Both are empty
	// on an attempt that fired.
	Gate    string `json:"gate,omitempty"`
	Waiting bool   `json:"waiting,omitempty"`
	// Problem is the whole account of a stopped attempt: what the gate said and
	// what would clear it. It is what a pass prints, and it is the same sentence
	// the item's own record now carries.
	Problem string `json:"problem,omitempty"`
	// RecordProblem is a finding this attempt could not write down. It is reported
	// beside the attempt rather than in place of it, and never left unsaid: a
	// refusal nobody recorded is a decision that reads as never attempted, which is
	// exactly the state this exists to end.
	RecordProblem string `json:"record_problem,omitempty"`
}

// Outstanding is every decision recorded and not carried out, in the docket's own
// order, with the ones nothing can act on now left out.
//
// Three things take an entry out. An item with a run in flight is not stopped
// work whatever the docket said when the entry was written, and the actions would
// refuse it anyway; a decision the harness has already carried out as far as it
// goes is not outstanding at all; and one whose last attempt a gate refused is
// left to its pacing, because the finding recorded then is what says so and
// repeating it every poll would starve every decision behind it.
//
// It reads and writes nothing. What it produces is a list somebody else acts on
// one of, which is what lets the pass take a slot for it before anything is
// attempted.
func (c CarryOut) Outstanding() ([]CarryOutTask, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	entries, err := c.Docket.List()
	if err != nil {
		return nil, fmt.Errorf("read the triage docket: %w", err)
	}
	inFlight, err := c.itemsInFlight()
	if err != nil {
		return nil, err
	}
	// What the repair grants have already bought is counted from every run the
	// product has had, which is the one reading here that grows with the history.
	// It is taken only where a repair decision is actually standing, and once: the
	// ordinary pass has no repair to fire and pays nothing for the accuracy, and a
	// pass that does reads the runs once rather than once per item.
	history := onceRecorded(c.Runs)
	now := c.now()
	read := make(map[string]outstandingItem, len(entries))
	var tasks []CarryOutTask
	var problems []error
	for _, entry := range entries {
		if entry.Class != triage.ClassStoppedRun {
			continue
		}
		if _, busy := inFlight[entry.WorkItemID]; busy {
			continue
		}
		item, seen := read[entry.WorkItemID]
		if !seen {
			item = c.outstandingFor(entry.WorkItemID)
			read[entry.WorkItemID] = item
			if item.problem != nil {
				problems = append(problems, item.problem)
			}
		}
		if item.problem != nil {
			continue
		}
		task, outstanding, err := item.taskFor(entry, now, history)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		if !outstanding {
			continue
		}
		tasks = append(tasks, task)
	}
	return tasks, errors.Join(problems...)
}

// outstandingItem is one work item's record as this sweep reads it: what has been
// decided, what has been claimed of the re-runs, and what stopped either being
// read.
type outstandingItem struct {
	counters runstate.TriageCounters
	claimed  []runstate.Rerun
	problem  error
}

func (c CarryOut) outstandingFor(workItemID string) outstandingItem {
	counters, err := c.Decisions.Counters(workItemID)
	if err != nil {
		return outstandingItem{problem: fmt.Errorf("read what triage has recorded about %s: %w", workItemID, err)}
	}
	claimed, err := c.Reruns.Claimed(workItemID)
	if err != nil {
		return outstandingItem{problem: fmt.Errorf("read the re-runs already carried out for %s: %w", workItemID, err)}
	}
	return outstandingItem{counters: counters, claimed: claimed}
}

// onceRecorded reads every run the product has had, the first time somebody asks
// and not before. A pass with no repair decision standing never asks, which is
// nearly every pass; one that does asks once however many items it walks.
func onceRecorded(runs CarryOutRuns) func() ([]runstate.State, error) {
	var recorded []runstate.State
	var problem error
	read := false
	return func() ([]runstate.State, error) {
		if !read {
			recorded, problem = runs.Recorded()
			if problem != nil {
				problem = fmt.Errorf("read the recorded runs, to count what the repair grants have already bought: %w", problem)
			}
			read = true
		}
		return recorded, problem
	}
}

// repairOutstanding reports a repair grant with rounds the harness has not handed
// to a run yet. It asks the same two records the repair action asks and in the
// same order: what was granted, against what the item's own runs record having
// been continued on. The counters alone cannot answer it — the grant counter is a
// total nothing clears — and a cheaper reading of them would offer a grant the
// action then refuses, which is a finding nobody asked for every pass.
func (i outstandingItem) repairOutstanding(workItemID string, history func() ([]runstate.State, error)) (bool, error) {
	if i.counters.GrantedRounds < 1 {
		return false, nil
	}
	recorded, err := history()
	if err != nil {
		return false, err
	}
	carried := 0
	for _, state := range recorded {
		if state.WorkItemID == workItemID {
			carried += state.CarriedOutRepairAttempts()
		}
	}
	return i.counters.GrantedRounds-carried > 0, nil
}

// taskFor reports the decision standing about one entry's stoppage that the
// harness has not acted on, and whether there is one.
//
// It asks the same two questions of each decision that the action carrying it out
// asks, and asks them the same way round: what was decided, and how much of that
// decision the harness has already spent. The counters alone cannot answer the
// second — they are totals nothing clears — which is why the claims and the
// continuations recorded on the item's runs are read beside them.
//
// The wiring is asked as well, because a carry-out with no action for a decision
// cannot act on it and a task nothing can take is a finding nobody asked for.
func (i outstandingItem) taskFor(entry triage.Entry, now time.Time, history func() ([]runstate.State, error)) (CarryOutTask, bool, error) {
	decision, found := i.counters.DecisionOf(entry.RunID)
	if !found {
		return CarryOutTask{}, false, nil
	}
	switch decision.Decision {
	case runstate.TriageDecisionRerun:
		if !i.rerunOutstanding(entry) {
			return CarryOutTask{}, false, nil
		}
	case runstate.TriageDecisionRepair:
		outstanding, err := i.repairOutstanding(entry.WorkItemID, history)
		if err != nil {
			return CarryOutTask{}, false, err
		}
		if !outstanding {
			return CarryOutTask{}, false, nil
		}
	default:
		// A re-scope, a wait, an escalation, and a merge re-arm are decisions the
		// harness does not carry out: the first three ask for no run at all, and a
		// re-arm is an integration retry rather than work, which the operator still
		// takes by hand.
		return CarryOutTask{}, false, nil
	}
	if stopped, refused := i.counters.CarryOutOf(entry.RunID); refused && stopped.Cooling(now) {
		return CarryOutTask{}, false, nil
	}
	return CarryOutTask{
		WorkItemID: entry.WorkItemID,
		RunID:      entry.RunID,
		DocketKey:  entry.Key,
		Decision:   decision.Decision,
		Reason:     decision.Reason,
	}, true, nil
}

// rerunOutstanding reports a re-run decision the harness has not acted on. Both
// halves are the question, and they are the ones the re-run action itself asks:
// this stoppage's own claim is what makes the once-per-stoppage bound, and the
// count of claims against the count of decisions is what stops one decision
// authorizing a re-run of every stoppage the item ever has.
func (i outstandingItem) rerunOutstanding(entry triage.Entry) bool {
	for _, existing := range i.claimed {
		if existing.DocketKey == entry.Key {
			return false
		}
	}
	return i.counters.Reruns > len(i.claimed)
}

// Carry carries one recorded decision out and writes down what became of the
// attempt. It reports the run's own outcome and failure beside its account, so a
// caller that started this the way it starts any other run settles it the same way.
//
// The order is the order the guarantees need. The operator's pause is read before
// anything is attempted, because the repair writes to the item and the run before
// it starts anything and a paused harness must touch neither; the action is then
// asked, and every other gate refuses inside it exactly as it refuses a carry-out
// somebody typed; and the finding is written last, because until the attempt has
// ended there is nothing to record about it.
func (c CarryOut) Carry(ctx context.Context, task CarryOutTask) (CarriedOut, Outcome, error) {
	if err := c.validate(); err != nil {
		return CarriedOut{}, Outcome{}, err
	}
	carried := CarriedOut{
		WorkItemID: task.WorkItemID,
		RunID:      task.RunID,
		DocketKey:  task.DocketKey,
		Decision:   task.Decision,
	}
	hold, held, err := c.paused()
	if err != nil {
		return c.stopped(ctx, task, carried, runstate.TriageGateHarness, false, err.Error(),
			"the operator's pause becoming readable again"), Outcome{}, nil
	}
	if held {
		return c.stopped(ctx, task, carried, runstate.TriageGateSpendingPause, true,
			fmt.Sprintf("the operator has paused everything the harness spends on a provider, since %s", hold.HeldAt.UTC().Format(time.RFC3339)),
			"`yoyo resume` lifting the pause; nothing was spent and the decision still stands"), Outcome{}, nil
	}
	switch task.Decision {
	case runstate.TriageDecisionRerun:
		return c.rerun(ctx, task, carried)
	case runstate.TriageDecisionRepair:
		return c.repair(ctx, task, carried)
	default:
		return CarriedOut{}, Outcome{}, fmt.Errorf(
			"%q is not a decision the harness carries out; it carries out %q and %q, and every other decision asks for no run at all",
			task.Decision, runstate.TriageDecisionRepair, runstate.TriageDecisionRerun)
	}
}

// rerun starts the fresh run one re-run decision authorizes, and reports the
// three states the action distinguishes: a run that started, a gate that is
// waiting, and a refusal.
func (c CarryOut) rerun(ctx context.Context, task CarryOutTask, carried CarriedOut) (CarriedOut, Outcome, error) {
	if c.Rerunner == nil {
		return c.stopped(ctx, task, carried, runstate.TriageGateHarness, false,
			"nothing is wired to this harness to start a fresh run, so the re-run recorded against this stoppage waits on somebody running `yoyo triage rerun`",
			"a harness wired to start re-runs itself"), Outcome{}, nil
	}
	result, runErr := c.Rerunner.Rerun(ctx, RerunRequest{Run: task.RunID})
	switch {
	case result.IntakeHeld != nil:
		return c.stopped(ctx, task, carried, runstate.TriageGateIntakeHold, true,
			fmt.Sprintf("the operator has held what the harness chooses, since %s", result.IntakeHeld.HeldAt.UTC().Format(time.RFC3339)),
			"`yoyo release` lifting the hold; nothing was claimed, so the stoppage keeps its re-run"), Outcome{}, nil
	case result.CapacityFull != nil:
		return c.stopped(ctx, task, carried, runstate.TriageGateCapacity, true,
			fmt.Sprintf("every developer slot is occupied: %d active, limit %d", result.CapacityFull.Active, result.CapacityFull.Limit),
			"a developer slot freeing, which needs nobody; nothing was claimed, so the stoppage keeps its re-run"), Outcome{}, nil
	case result.PausedBeforeStarting != nil:
		gate, clears := pausedGate(*result.PausedBeforeStarting)
		return c.stopped(ctx, task, carried, gate, true,
			fmt.Sprintf("the fresh run met %s where it would have started", pauseMet(*result.PausedBeforeStarting)), clears), Outcome{}, nil
	case !result.Started:
		gate, clears := carryOutGate(runErr)
		return c.stopped(ctx, task, carried, gate, false, refusalText(runErr), clears), Outcome{}, nil
	}
	carried.Carried = true
	carried.Reason = result.Reason
	carried.RecordProblem = result.RecordProblem
	c.clear(ctx, task, &carried)
	return carried, result.Outcome, runErr
}

// repair re-enters the stopped run's own repair loop on the grant one repair
// decision recorded, handing it the reasoning the development manager recorded
// rather than words from whoever asked: the run and the item both record that
// sentence, and it has to be hers.
func (c CarryOut) repair(ctx context.Context, task CarryOutTask, carried CarriedOut) (CarriedOut, Outcome, error) {
	if c.Repairer == nil {
		return c.stopped(ctx, task, carried, runstate.TriageGateHarness, false,
			"nothing is wired to this harness to continue a stopped run, so the repair granted against this stoppage waits on somebody running `yoyo triage repair`",
			"a harness wired to continue stopped runs itself"), Outcome{}, nil
	}
	result, runErr := c.Repairer.Continue(ctx, RepairContinueRequest{Run: task.RunID, Reason: task.Reason})
	switch {
	case result.IntakeHeld != nil:
		return c.stopped(ctx, task, carried, runstate.TriageGateIntakeHold, true,
			fmt.Sprintf("the operator has held what the harness chooses, since %s", result.IntakeHeld.HeldAt.UTC().Format(time.RFC3339)),
			"`yoyo release` lifting the hold; nothing was spent, so the item keeps its grant"), Outcome{}, nil
	case result.CapacityFull != nil:
		return c.stopped(ctx, task, carried, runstate.TriageGateCapacity, true,
			fmt.Sprintf("every developer slot is occupied: %d active, limit %d", result.CapacityFull.Active, result.CapacityFull.Limit),
			"a developer slot freeing, which needs nobody; nothing was spent, so the item keeps its grant"), Outcome{}, nil
	case !result.Continued:
		gate, clears := carryOutGate(runErr)
		return c.stopped(ctx, task, carried, gate, false, refusalText(runErr), clears), Outcome{}, nil
	}
	carried.Carried = true
	carried.Reason = result.Reason
	carried.RecordProblem = result.RecordProblem
	c.clear(ctx, task, &carried)
	return carried, result.Outcome, runErr
}

// pausedGate names the pause a fresh run met and what lifts it. Each of the four
// is lifted by a different person doing a different thing, so which one it was is
// the whole of what the finding is worth.
func pausedGate(outcome Outcome) (gate, clears string) {
	switch {
	case outcome.PausedByOperator != nil:
		return runstate.TriageGateSpendingPause, "`yoyo resume` lifting the pause; nothing was reserved, so the stoppage keeps its re-run"
	case outcome.PausedByIntake != nil:
		return runstate.TriageGateIntakeHold, "`yoyo release` lifting the hold; nothing was reserved, so the stoppage keeps its re-run"
	case outcome.PausedByDirective != nil:
		return runstate.TriageGateDirective, "the operator resolving the directive that pauses this item; nothing was reserved, so the stoppage keeps its re-run"
	default:
		return runstate.TriageGateWorkItem, "the work this item waits on finishing; nothing was reserved, so the stoppage keeps its re-run"
	}
}

// carryOutGate names which gate a refusal came from and what would clear it.
//
// It is read from the sentinels the actions already export rather than from the
// words of the refusal, for the reason every other classification in this package
// reads a record rather than prose: a gate matched on wording is one that changes
// when somebody rewrites a sentence, and this one is written into a durable record
// somebody acts on.
//
// Everything the sentinels do not cover is the harness's own records, and its
// refusal is carried verbatim rather than summarized. Those refusals already say
// what has to become true — this package's contribution would be a paraphrase of
// one, which is the last thing a finding should carry.
func carryOutGate(err error) (gate, clears string) {
	switch {
	case err == nil:
		// A carry-out that started nothing and refused nothing is a contradiction
		// rather than a state, and it is recorded as one: silence is what this
		// mechanism exists to end, so an ending nobody accounted for is said out loud.
		return runstate.TriageGateHarness, "somebody looking at the harness: it neither started the run nor said why"
	case errors.Is(err, ErrWorktreeNotAsLeft), errors.Is(err, ErrPreservedChangeMissing):
		return runstate.TriageGatePreservedWork,
			"somebody saying what became of the worktree the stopped run preserved; what is in it is what a continued developer would be handed back, so this is a person's to look at"
	case errors.Is(err, ErrItemNotStartable):
		return runstate.TriageGateWorkItem,
			"the work item being put back in a state a run may start on; nothing was spent, so the same decision is carried out once it is"
	case errors.Is(err, runstate.ErrTriageCapReached):
		return runstate.TriageGateBudget,
			"`yoyo triage override` crossing the budget that refused, which is the operator's and nobody else's"
	case errors.Is(err, runstate.ErrRerunTaken):
		return runstate.TriageGateBudget,
			"nothing: triage acts on one docketed stoppage once, so a further attempt at this one is an escalation rather than a larger budget"
	default:
		return runstate.TriageGateHarness, "what the refusal itself names"
	}
}

// refusalText is what a gate said, in words a record can carry. A refusal that
// arrived with no error at all still has to say something: a finding recording an
// empty refusal is the silence this exists to end, written down.
func refusalText(err error) string {
	if err == nil {
		return "the carry-out started nothing and gave no reason, which is a state the harness should not be able to reach"
	}
	return err.Error()
}

// stopped writes the finding for one attempt a gate stopped, and returns the
// account of it.
//
// The write is made under a context detached from the attempt's own, for the
// reason the stopped-work delivery detaches its records: a shutdown cancels the
// very context the attempt ran under, and it lands between the gate refusing and
// this write — which is the case a finding is most needed for and the one an
// attached context would lose.
func (c CarryOut) stopped(ctx context.Context, task CarryOutTask, carried CarriedOut, gate string, waiting bool, refusal, clears string) CarriedOut {
	carried.Gate = gate
	carried.Waiting = waiting
	held := "was refused by"
	if waiting {
		held = "is waiting on"
	}
	carried.Problem = fmt.Sprintf("the %q the development manager decided about the stoppage of run %s %s %s: %s. What clears it: %s",
		task.Decision, task.RunID, held, gate, strings.TrimSpace(refusal), strings.TrimSpace(clears))
	write, stopWriting := recordContext(ctx)
	defer stopWriting()
	if _, err := c.Decisions.RecordCarryOutRefusal(write, task.WorkItemID, runstate.TriageCarryOut{
		RunID:    task.RunID,
		Decision: task.Decision,
		Gate:     gate,
		Refusal:  refusal,
		Clears:   clears,
		Waiting:  waiting,
	}, c.now()); err != nil {
		carried.RecordProblem = fmt.Sprintf(
			"and the finding could not be written onto %s's triage record, so the docket the development manager reads does not carry it and this pass is the only thing that says it: %v",
			task.WorkItemID, err)
	}
	return carried
}

// clear removes the finding a previous attempt left, now that this one fired. A
// finding standing over a decision that has been carried out is the worst kind:
// it reads exactly like the condition this whole mechanism exists to report.
//
// It is detached for the reason the write above is, and a failure is said beside
// the attempt rather than swallowed — a stale finding is a development manager
// told a run is not happening while it runs.
func (c CarryOut) clear(ctx context.Context, task CarryOutTask, carried *CarriedOut) {
	write, stopWriting := recordContext(ctx)
	defer stopWriting()
	if _, err := c.Decisions.ClearCarryOut(write, task.WorkItemID, task.RunID, c.now()); err != nil {
		problem := fmt.Sprintf(
			"the decision was carried out and the finding a previous attempt left on %s's triage record could not be cleared, so the docket still says this decision is not happening: %v",
			task.WorkItemID, err)
		if carried.RecordProblem == "" {
			carried.RecordProblem = problem
			return
		}
		carried.RecordProblem += "; " + problem
	}
}

// itemsInFlight names the work items with a run going. A decision about an item
// something is already running is not one to act on: the item is not stopped work,
// and both actions refuse it anyway — asking here is what keeps the pass from
// spending its one carry-out finding out.
func (c CarryOut) itemsInFlight() (map[string]struct{}, error) {
	incomplete, err := c.Runs.Incomplete()
	if err != nil {
		return nil, fmt.Errorf("read what is already in flight: %w", err)
	}
	busy := make(map[string]struct{}, len(incomplete))
	for _, state := range incomplete {
		busy[state.WorkItemID] = struct{}{}
	}
	return busy, nil
}

// paused reports the operator's pause over everything the harness spends. A pause
// that cannot be read stops the attempt rather than being spent through, exactly
// as it does everywhere else it is read.
func (c CarryOut) paused() (runstate.OperatorHold, bool, error) {
	if c.Holds == nil {
		return runstate.OperatorHold{}, false, nil
	}
	hold, held, err := c.Holds.Held()
	if err != nil {
		return runstate.OperatorHold{}, false, fmt.Errorf("read whether the operator has paused harness activity: %w", err)
	}
	return hold, held, nil
}

func (c CarryOut) validate() error {
	var problems []error
	if c.Docket == nil {
		problems = append(problems, errors.New("carrying out a triage decision requires the docket the decision was made against"))
	}
	if c.Decisions == nil {
		problems = append(problems, errors.New("carrying out a triage decision requires the item's durable triage record, which is what says the decision was made and where a refusal is recorded"))
	}
	if c.Reruns == nil {
		problems = append(problems, errors.New("carrying out a triage decision requires the re-runs already claimed, which is what says a decision has been acted on"))
	}
	if c.Runs == nil {
		problems = append(problems, errors.New("carrying out a triage decision requires the durable run state, because an item with a run in flight is not stopped work"))
	}
	return errors.Join(problems...)
}

func (c CarryOut) now() time.Time {
	if c.Clock == nil {
		return execution.RealClock{}.Now().UTC()
	}
	return c.Clock.Now().UTC()
}

// Render describes what one attempt came to, for whoever asked. An attempt that
// fired says what it started; one a gate stopped says the gate and what clears it,
// which is the same sentence the item's own record now carries.
func (carried CarriedOut) Render() string {
	var rendered strings.Builder
	if carried.Carried {
		fmt.Fprintf(&rendered, "carried out the %q the development manager decided about %s, on the stopped work of run %s\n",
			carried.Decision, carried.WorkItemID, carried.RunID)
	} else {
		fmt.Fprintf(&rendered, "%s\n", carried.Problem)
	}
	if carried.RecordProblem != "" {
		fmt.Fprintf(&rendered, "  %s\n", carried.RecordProblem)
	}
	return rendered.String()
}
