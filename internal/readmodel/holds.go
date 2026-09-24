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
// # A preserved change is looked for, not read off a flag
//
// Whether a stopped run's change is still there is answered by the repository
// rather than by the run's record. The record's removal flags are what a sweep
// or a cleanup remembered to write, and a flag is exactly the kind of field this
// derivation exists to stop trusting: on 2026-09-19 the product manager's
// stale-state repair cleared yoyodyne-ifd.372's blocked status as "no longer
// held behind a preserved run" on the strength of the record, while the item's
// own notes still said the run's branch and worktree were checked and there. So
// a stopped run is held where its branch or its worktree exists, checked as the
// hold is read; where the check could not be made it is held as if they did,
// with the reason saying so; and a stopped run about which a triage decision
// stands that the harness has still to carry out — a repair continuation first
// among them — is held whatever became of its artifacts, because what that
// decision continues is the run, and a fresh pull would start over beside it.
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
	"context"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
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

// Remains is what the repository actually holds of a stopped run's change: its
// branch, and its checkout. It is asked rather than the run's record read,
// because the record's removal flags are what something remembered to write and
// the repository is what is there. It is satisfied by *gitworktree.Manager.
type Remains interface {
	Survives(ctx context.Context, worktree gitworktree.Worktree) (gitworktree.Survival, error)
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
//
// remains may be nil too, and a run's artifacts may fail to be looked for. A
// reading wired without it falls back to what each run's record says survived,
// and says in the reason that nothing looked; a look that failed holds the run
// as if its change were there, for the reason an unread hold holds everything.
func HeldForAPerson(ctx context.Context, stoppages Stoppages, decisions Decisions, remains Remains) (backlog.Holds, error) {
	runs, err := stoppages.Recorded()
	if err != nil {
		return backlog.Holds{}, fmt.Errorf("read the recorded runs: %w", err)
	}
	escalated, err := stoppages.Escalated()
	if err != nil {
		return backlog.Holds{}, fmt.Errorf("read the escalated stoppages: %w", err)
	}
	return heldForAPerson(runs, escalated, standingDecisions(decisions), Looking(ctx, remains, nil)), nil
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
		// The rule itself is triage.AwaitingCarryOut, which the development
		// manager's docket reads too: one item given two answers to whether a
		// carry-out is outstanding is one item given two next movers, which is a
		// disagreement only the operator could adjudicate.
		return counters.AwaitingCarryOut(runID), ""
	}
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
func heldForAPerson(runs []runstate.State, escalated []runstate.Escalation, decided standing, look Look) backlog.Holds {
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
	// The stoppages, each looked at rather than read: a run whose change the
	// repository still holds, a run whose change nothing could look for, and a run
	// about which a decision stands that the harness has still to carry out. The
	// third is held with nothing of it surviving, because what the decision
	// continues is the run itself — a repair grant re-enters its preserved
	// session — and a fresh pull would start over beside it.
	remaining := make(map[string]triage.Found)
	for workItemID, run := range latestPerItem(runs, func(run runstate.State) bool {
		if !stoppage(run) {
			return false
		}
		found := look(run)
		if found.Holds() {
			remaining[run.RunID] = found
			return true
		}
		carryOut, _ := decided(run.WorkItemID, run.RunID)
		return carryOut
	}) {
		found, preserved := remaining[run.RunID]
		// An approved change the environment stopped is answered before the item's
		// triage record is read at all, and without reading it: what such a stoppage
		// waits on is the harness resuming the promotion, whatever has or has not
		// been decided about the item's other stoppages. It is reported as the
		// harness's move — the same wait the surfaces already call a carry-out —
		// because the alternative is naming the development manager on a stoppage
		// the docket (triage.Entry.renderNextMover) tells her she owes nothing
		// about, and an item given two next movers is a disagreement only the
		// operator can settle.
		if run.IntegrationStop != nil {
			reasons[workItemID] = backlog.Hold{Reason: stoppedIntegration(run, found, preserved), Decided: true}
			continue
		}
		carryOut, problem := decided(workItemID, run.RunID)
		if !preserved {
			reasons[workItemID] = heldFor(continuedStoppage(run), carryOut, problem)
			continue
		}
		reasons[workItemID] = heldFor(preservedChange(run, found), carryOut, problem)
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

// stoppage reports a run that stopped on this item and left it for somebody.
// Whether its change survived is deliberately not asked here: that is the
// repository's answer rather than the record's, and the derivation asks for it.
//
// Two endings are one, and reading only the first is what cost yoyodyne-ifd.436.4
// a second run of work that was already approved. A durable blocker is the
// harness having handed the item to somebody, and it is the obvious one. The
// other is a run that died inside its own process: it hands nobody a blocker, on
// purpose, because the harness may yet resume it — so its record ends `failed`
// rather than `stopped` while its change sits on a branch exactly as a
// stoppage's does. run-b0b6d18d ended that way on an approved change the
// environment stopped, and read to the pull as an item with nothing holding it.
// To a reader the two are the same fact, and the hold covers them the same way.
func stoppage(run runstate.State) bool {
	if run.WorkItemID == "" || !run.Status.Terminal() {
		return false
	}
	return strings.TrimSpace(run.Blocker) != "" || run.DiedInItsOwnProcess()
}

// preservedChange says why an item with work still on a branch is not something
// to pull. It names the run because that is what somebody has to go and look at:
// the decision is whether to pick the change up, re-run it, or retire it, and
// none of those is a fresh run started underneath it. It names what was found,
// and how, because that is what a reader about to release the item checks: a
// branch and a worktree checked and there are a different claim from a record
// that says so, and a look that failed is a third.
//
// It stops short of saying whose move that is, as the two publication accounts
// below do, because heldFor closes every one of them with the answer the item's
// own triage record gives.
func preservedChange(run runstate.State, found triage.Found) string {
	if found.Unknown {
		return fmt.Sprintf(
			"run %s stopped on it and %s, so it is held as preserved: a fresh run would start over on top of work that may still be there",
			run.RunID, uncheckable(found))
	}
	return fmt.Sprintf(
		"run %s stopped on it and its change is preserved (%s), so a fresh run would start over on top of work that is still there",
		run.RunID, whatWasFound(found))
}

// whatWasFound is what the look came to, in the words every account of a
// preserved change says it in: what is there, and how that was established. The
// two are said together because they are different claims — a branch checked and
// there is not a branch a record says nothing removed — and a reader about to
// release the item acts on the difference.
func whatWasFound(found triage.Found) string {
	var there []string
	if found.BranchThere {
		there = append(there, "branch")
	}
	if found.WorktreeThere {
		there = append(there, "worktree")
	}
	checked := "checked and there"
	if !found.Looked() {
		checked = "as its record says, nothing having been wired to look"
	}
	return strings.Join(there, " and ") + " " + checked
}

// uncheckable says of a look that failed what it could not establish and why.
func uncheckable(found triage.Found) string {
	return fmt.Sprintf("whether its branch or its worktree is still there could not be checked: %s", found.Unchecked)
}

// stoppedIntegration says why an item whose approved change the environment
// stopped short of its promotion is not something to pull, and who finishes it.
//
// It is its own account rather than the preserved-change one closed by a clause,
// and the ordering is the point: the scheduler cuts a hold's reason to a line
// when it says why an item was passed over, so the fact that decides what
// anybody does — the approval stands, the environment is what stopped it, and
// `yoyo triage resume` is the verb — has to come before the evidence rather than
// after it. Said the other way round, the verb was the part that fell off.
//
// What was found of the change is still said, because it is what a reader about
// to release the item checks; it is last because it is the part that can be cut
// without leaving somebody unable to act.
func stoppedIntegration(run runstate.State, found triage.Found, preserved bool) string {
	account := fmt.Sprintf(
		"run %s stopped on it with its change approved, and the environment is what stopped the promotion, so `yoyo triage resume` finishes it rather than a decision or a fresh run",
		run.RunID)
	switch {
	case !preserved:
		return account
	case found.Unknown:
		return account + " (" + uncheckable(found) + ")"
	default:
		return account + " (" + whatWasFound(found) + ")"
	}
}

// continuedStoppage says why an item whose stopped run left nothing behind is
// still not something to pull: a decision about that stoppage stands recorded
// and the harness has yet to act on it. What it acts on is the run — a repair
// grant re-enters the session the run preserved, a re-run starts the item over
// from what the run recorded — so a fresh pull meanwhile would be a second run
// on the same work, and the first thing the carried-out decision met would be
// the fresh run's claim.
func continuedStoppage(run runstate.State) string {
	return fmt.Sprintf(
		"run %s stopped on it and a decision about that stoppage is recorded and not yet carried out, so a fresh run would start beside the continuation that decision buys",
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
