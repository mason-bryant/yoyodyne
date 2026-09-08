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
// has not been told what to do with, and of work it finished whose publication
// it could not. Neither is a field anybody has to remember to update, which is
// the whole of the difference.
//
// Nothing here releases anything, and that direction is deliberate. A hold is
// lifted by a person deciding — triage picking the preserved change up, or an
// escalation being answered — and the effect of the decision is that the records
// this reads stop saying the item is held. An item is released by the facts
// changing rather than by anything written back over them.
//
// # Two holds, two movers
//
// A decision being recorded is not the decision being carried out, and until
// the harness acts the item is held either way. Reporting both as one thing —
// "held for a person" — is what cost the operator days on 2026-09-07:
// thirty-three items read as work the development manager owed a decision on
// while she had decided every one of them, and the gap was the carry-out. So
// each hold says which of the two it is, read from the item's own durable triage
// record: a decision standing about the stoppage that holds it is the harness's
// move, and no decision standing is hers.
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

// Decisions is the durable per-item record of what triage has decided, which is
// what says whether a held item is waiting on a decision or on the carrying out
// of one. It is the record the triage guards spend and refuse against, read
// rather than re-derived from the runs: a second reading of what has been
// decided is a second answer, and this one decides which role a surface sends
// the operator to.
//
// It is satisfied by *runstate.TriageStore.
type Decisions interface {
	Counters(workItemID string) (runstate.TriageCounters, error)
}

// HeldForAPerson is the admitted work somebody has to release before anything
// pulls it, with what each item is waiting for and whose move that is.
//
// A reading that fails is an error rather than an empty answer. The zero Holds
// already means "not read" and holds everything blocked, so a caller that
// reports the failure and carries on with it loses no safety; what it must not
// do is treat a failure as "nothing is held", which would release exactly the
// work this exists to hold.
//
// decisions may be nil, and an item's record may fail to open. Neither costs the
// hold: the item is held exactly as it was and is reported as one nobody has
// decided about, with the reason saying that this reading could not tell. That
// is the conservative direction — it points at the role that would have to
// decide, which is where the answer went before the two were separated — and it
// keeps one unreadable file from making a whole queue unpullable.
func HeldForAPerson(stoppages Stoppages, decisions Decisions) (backlog.Holds, error) {
	runs, err := stoppages.Recorded()
	if err != nil {
		return backlog.Holds{}, fmt.Errorf("read the recorded runs: %w", err)
	}
	escalated, err := stoppages.Escalated()
	if err != nil {
		return backlog.Holds{}, fmt.Errorf("read the escalated stoppages: %w", err)
	}
	return heldForAPerson(runs, escalated, standingDecisions(decisions)), nil
}

// standing is what triage has decided about one item's stoppage: whether a
// decision the harness carries out stands about it, and what stopped this
// reading finding out.
type standing func(workItemID, runID string) (decided bool, problem string)

// standingDecisions reads each item's triage record once, however many of its
// runs are held: one item's stoppages share one record, and a queue of stopped
// runs would otherwise open the same file for each of them.
func standingDecisions(decisions Decisions) standing {
	if decisions == nil {
		return func(string, string) (bool, string) {
			return false, "nothing was wired to read what triage has decided about it"
		}
	}
	read := make(map[string]runstate.TriageCounters)
	failed := make(map[string]string)
	return func(workItemID, runID string) (bool, string) {
		if problem, known := failed[workItemID]; known {
			return false, problem
		}
		counters, seen := read[workItemID]
		if !seen {
			opened, err := decisions.Counters(workItemID)
			if err != nil {
				problem := fmt.Sprintf("what triage has decided about it could not be read: %v", err)
				failed[workItemID] = problem
				return false, problem
			}
			read[workItemID], counters = opened, opened
		}
		return awaitingCarryOut(counters, runID), ""
	}
}

// awaitingCarryOut reports a decision standing about one stoppage that the
// harness has still to act on.
//
// Only the three decisions that buy another attempt are ones the harness
// carries out. A wait, a re-scope and an escalation are decided and leave the
// harness nothing to do, so an item still held under one of them is held by
// what the development manager decided rather than by anything outstanding, and
// naming the harness as its next mover would send an operator to watch for a run
// nothing is going to start.
//
// A granted repair is asked of the grant rather than of the decision, because a
// repair continues the run it was granted for: the same run stops again carrying
// the same decision, so the decision alone would go on claiming a carry-out that
// has already happened. The grant standing unspent is what actually says it has
// not.
//
// A re-run and a merge re-arm are answered from the decision itself, which is
// sufficient because carrying either one out changes what this reading is about.
// A re-run produces a fresh run, and once that run stops it is the latest one the
// item has, so the hold names it instead and nothing stands recorded about it. A
// re-arm the forge then honours settles the publication and lifts the hold.
func awaitingCarryOut(counters runstate.TriageCounters, runID string) bool {
	decision, decided := counters.DecisionOf(runID)
	if !decided || !decision.Spends() {
		return false
	}
	if decision.Decision == runstate.TriageDecisionRepair {
		return counters.GrantOutstanding()
	}
	return true
}

// The clause each hold closes on: whose move follows it. They are two sentences
// rather than one because they are two people, and they are written once here so
// that every surface saying a hold says the same words about who releases it.
const (
	awaitingDecisionClause = "the development manager decides what happens to it, and nothing pulls it until she has"
	awaitingCarryOutClause = "the development manager has already decided what happens to it, so what is outstanding is the harness carrying that decision out rather than a decision"
)

// heldFor is one item's hold: the account of what stopped it, closed by whose
// move follows. A reading that could not say which of the two it is says so in
// the reason and reports the decision as unmade, which is where the answer went
// before the two were told apart.
func heldFor(account string, decided bool, problem string) backlog.Hold {
	switch {
	case problem != "":
		return backlog.Hold{Reason: account + "; " + problem + ", so this is stated as a stoppage nobody has decided about"}
	case decided:
		return backlog.Hold{Reason: account + "; " + awaitingCarryOutClause, Decided: true}
	default:
		return backlog.Hold{Reason: account + "; " + awaitingDecisionClause}
	}
}

// heldForAPerson is the derivation itself, over records already read. It is
// separate so the rule can be tested against run and escalation records without
// a store behind them.
func heldForAPerson(runs []runstate.State, escalated []runstate.Escalation, decided standing) backlog.Holds {
	reasons := make(map[string]backlog.Hold)
	// The escalations first, so that an item that is both — a stoppage nobody
	// answered whose change is also still preserved — reads as the preserved one.
	// Both are true and either would hold it; the preserved change is the one that
	// says why starting the item over is the wrong move, which is what a reader
	// about to release it needs to know.
	for _, escalation := range escalated {
		if escalation.WorkItemID == "" || strings.TrimSpace(escalation.Decision) != "" {
			continue
		}
		// Never a carry-out: an escalation with nothing recorded against it is by
		// construction one nobody has decided, so what it waits on is the decision
		// itself however much triage has decided about the item's other stoppages.
		reasons[escalation.WorkItemID] = backlog.Hold{Reason: undecidedStoppage(escalation)}
	}
	// A publication the forge never merged next. It holds the item for the same
	// reason the merged one below does — the work is on the target branch and a
	// run against it would redo it — but it is answered before the preserved
	// change rather than after, because it says nothing about where the run's own
	// branch went and the preserved-change reason does: an item that is both has a
	// branch somebody has to decide about, and that is what its reader needs.
	for workItemID, run := range latestPerItem(runs, func(run runstate.State) bool {
		return outstandingPublication(run) && !mergeConfirmed(run)
	}) {
		carryOut, problem := decided(workItemID, run.RunID)
		reasons[workItemID] = heldFor(unmergedPublication(run), carryOut, problem)
	}
	for workItemID, run := range latestPerItem(runs, preservedStoppage) {
		carryOut, problem := decided(workItemID, run.RunID)
		reasons[workItemID] = heldFor(preservedChange(run), carryOut, problem)
	}
	// The merged publications last. Only these know the change reached everywhere
	// it was going, so only these may say there is nothing left to do about it —
	// which is worth saying over the preserved-change reason, since a branch left
	// behind a confirmed merge is debris rather than work to pick up.
	for workItemID, run := range latestPerItem(runs, func(run runstate.State) bool {
		return outstandingPublication(run) && mergeConfirmed(run)
	}) {
		carryOut, problem := decided(workItemID, run.RunID)
		reasons[workItemID] = heldFor(mergedPublication(run), carryOut, problem)
	}
	return backlog.ReadHolds(reasons)
}

// latestPerItem is the runs a rule matches, one per work item. One item can have
// stopped, or published, more than once; the most recent run is the one that
// describes where the work actually is, so a later account replaces an earlier
// one rather than whichever the store happened to list first.
func latestPerItem(runs []runstate.State, matches func(runstate.State) bool) map[string]runstate.State {
	latest := make(map[string]runstate.State)
	for _, run := range runs {
		if !matches(run) {
			continue
		}
		if previous, seen := latest[run.WorkItemID]; seen && previous.UpdatedAt.After(run.UpdatedAt) {
			continue
		}
		latest[run.WorkItemID] = run
	}
	return latest
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
//
// It stops short of saying whose move that is, as the two publication accounts
// below do, because heldFor closes every one of them with the answer the item's
// own triage record gives.
func preservedChange(run runstate.State) string {
	return fmt.Sprintf(
		"run %s stopped on it and its change is preserved, so a fresh run would start over on top of work that is still there",
		run.RunID)
}

// outstandingPublication reports a run that promoted its item's change and could
// not finish publishing it. All three halves matter, and together they describe
// the one item shape a developer run can do nothing at all with: the promotion
// put the work on the target branch, which is the authoritative one, so there is
// nothing left to implement, and what is unfinished is a publication that only a
// person or a later sweep settles.
//
// A run still in flight owns its own publication and is not this. What is
// deliberately not asked is whether the run's artifacts survived: an integrated
// run cleans its own up, which is exactly why the preserved-change rule above
// misses this and why yoyodyne-ifd.295 was pulled three times, once per
// developer run that then re-derived that the change had already landed.
//
// It says nothing about whether the forge merged anything, which is a separate
// question with three answers and is asked by mergeConfirmed below.
func outstandingPublication(run runstate.State) bool {
	return run.WorkItemID != "" &&
		run.Status.Terminal() &&
		run.Integration != nil &&
		strings.TrimSpace(run.PublishFailure) != ""
}

// mergeConfirmed reports a publication the forge performed and the harness saw
// it perform. It is what separates a leftover from an unfinished merge, and it
// is asked of the pull request rather than of the promotion, because the
// promotion is local and says nothing about the forge: a merge the forge dropped
// leaves a recorded promotion exactly like a merged one does, and reading that
// as a change the remote carries is the false statement this exists to refuse.
func mergeConfirmed(run runstate.State) bool {
	return run.PullRequest != nil && run.PullRequest.Merged
}

// mergedPublication says why an item whose change is merged everywhere it was
// going is not something to pull. Nothing about the work is unfinished, so the
// only thing a fresh run could do is find that out again.
func mergedPublication(run runstate.State) string {
	return fmt.Sprintf(
		"run %s integrated its change into %s and the forge merged it, so only the publication is unfinished and there is nothing here to implement",
		run.RunID, run.Integration.TargetBranch)
}

// unmergedPublication says why an item the forge has not merged is not something
// to pull either. The work is on the local target branch, which is the
// authoritative one, so a run against it would redo work that has landed; what
// is undecided is the merge, and re-arming one the forge dropped is a bounded
// triage decision rather than a developer's.
func unmergedPublication(run runstate.State) string {
	return fmt.Sprintf(
		"run %s integrated its change into %s and the forge has not merged it, so what is outstanding is the publication rather than the work",
		run.RunID, run.Integration.TargetBranch)
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
