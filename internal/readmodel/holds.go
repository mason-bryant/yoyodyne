package readmodel

// What the harness is holding for a person, and why a status field cannot say
// it.
//
// A work item's status is written when the work stops and never rewritten when
// what stopped it clears. So it answers two questions at once and maintains
// neither: it says an item is blocked whether the block was a dependency that
// has since landed or a stoppage somebody still has to decide about, and it goes
// on saying it after the dependency closes. On 2026-09-04 that hid two-thirds of
// this backlog, two p0 items among it, and the line sat idle for a morning
// because every one of those items read as unpullable and nothing said why.
//
// The two questions are separated here by asking the records instead. What an
// item waits on is the tracker's dependency graph, which the backlog reads for
// itself and which clears on its own as the work lands. What somebody still has
// to release is this: the harness's own durable account of work it stopped and
// has not been told what to do with. Neither is a field anybody has to remember
// to update, which is the whole of the difference.
//
// Nothing here releases anything, and that direction is deliberate. A hold is
// lifted by a person deciding — triage picking the preserved change up, or an
// escalation being answered — and the effect of the decision is that the records
// this reads stop saying the item is held. An item is released by the facts
// changing rather than by anything written back over them.
//
// It lives with the read model rather than beside either caller because the
// scheduler and every operator surface have to give one answer. A surface that
// showed an item as pullable while the scheduler held it would be a
// disagreement only the operator could adjudicate, which is the thing one
// derivation exists to prevent.

import (
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Stoppages is the durable record of the work the harness has stopped: every run
// it has recorded, and every stoppage it has put in front of the development
// manager. Both are needed and neither is enough — a stoppage whose change is
// still on a branch is held whether or not anybody was ever asked about it, and
// one nobody has answered is held whether or not its branch survived.
//
// It is satisfied by *runstate.Store.
type Stoppages interface {
	Recorded() ([]runstate.State, error)
	Escalated() ([]runstate.Escalation, error)
}

// HeldForAPerson is the admitted work somebody has to release before anything
// pulls it, with what each item is waiting for.
//
// A reading that fails is an error rather than an empty answer. The zero Holds
// already means "not read" and holds everything blocked, so a caller that
// reports the failure and carries on with it loses no safety; what it must not
// do is treat a failure as "nothing is held", which would release exactly the
// work this exists to hold.
func HeldForAPerson(stoppages Stoppages) (backlog.Holds, error) {
	runs, err := stoppages.Recorded()
	if err != nil {
		return backlog.Holds{}, fmt.Errorf("read the recorded runs: %w", err)
	}
	escalated, err := stoppages.Escalated()
	if err != nil {
		return backlog.Holds{}, fmt.Errorf("read the escalated stoppages: %w", err)
	}
	return heldForAPerson(runs, escalated), nil
}

// heldForAPerson is the derivation itself, over records already read. It is
// separate so the rule can be tested against run and escalation records without
// a store behind them.
func heldForAPerson(runs []runstate.State, escalated []runstate.Escalation) backlog.Holds {
	reasons := make(map[string]string)
	// The escalations first, so that an item that is both — a stoppage nobody
	// answered whose change is also still preserved — reads as the preserved one.
	// Both are true and either would hold it; the preserved change is the one that
	// says why starting the item over is the wrong move, which is what a reader
	// about to release it needs to know.
	for _, escalation := range escalated {
		if escalation.WorkItemID == "" || strings.TrimSpace(escalation.Decision) != "" {
			continue
		}
		reasons[escalation.WorkItemID] = undecidedStoppage(escalation)
	}
	// One item can have stopped more than once. The most recent stoppage is the
	// one that describes where the work actually is, so a later run's account
	// replaces an earlier one rather than whichever the store happened to list
	// first.
	held := make(map[string]runstate.State, len(runs))
	for _, run := range runs {
		if !preservedStoppage(run) {
			continue
		}
		if previous, seen := held[run.WorkItemID]; seen && previous.UpdatedAt.After(run.UpdatedAt) {
			continue
		}
		held[run.WorkItemID] = run
	}
	for workItemID, run := range held {
		reasons[workItemID] = preservedChange(run)
	}
	return backlog.ReadHolds(reasons)
}

// preservedStoppage reports a run that stopped on this item and left its change
// behind. Both halves matter. A run that ended without a durable blocker was not
// handed to anybody, so nothing is waiting on a decision about it; and a run
// whose branch and worktree are both recorded as removed has nothing left for a
// fresh run to strand.
func preservedStoppage(run runstate.State) bool {
	return run.WorkItemID != "" &&
		run.Status.Terminal() &&
		strings.TrimSpace(run.Blocker) != "" &&
		run.Artifacts().Preserved()
}

// preservedChange says why an item with work still on a branch is not something
// to pull. It names the run because that is what somebody has to go and look at:
// the decision is whether to pick the change up, re-run it, or retire it, and
// none of those is a fresh run started underneath it.
func preservedChange(run runstate.State) string {
	return fmt.Sprintf(
		"run %s stopped on it and its change is preserved, so a fresh run would start over on top of work that is still there; triage decides what happens to it",
		run.RunID)
}

// undecidedStoppage says why an item whose stoppage nobody has answered is not
// something to pull. The three cases are three different people to go to, which
// is why they are not one sentence: one is waiting on the development manager,
// one is waiting on the harness to finish asking her, and one has run out of
// ways to ask and is waiting on whoever reads this.
func undecidedStoppage(escalation runstate.Escalation) string {
	switch {
	case escalation.Delivered():
		return "its stoppage is in front of the development manager and nothing has been decided about it yet"
	case escalation.Attempts >= runstate.MaxEscalationAttempts:
		return fmt.Sprintf(
			"its stoppage could not be put in front of the development manager after %d attempt(s), so it needs a person",
			escalation.Attempts)
	default:
		return "its stoppage has not reached the development manager yet, so nobody has decided anything about it"
	}
}
