package orchestrator

// What a developer claims its change does to the work item is read here, and it
// is the one channel out of a developer that decides something about the run:
// closure follows the claim rather than the integration. A run that lands
// evidence integrates exactly as any other run does and leaves its item open.

import (
	"context"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/landing"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// maxLandingProblemBytes keeps an unreadable claim to a readable line of the
// record, the same bound a lost report is held to.
const maxLandingProblemBytes = 512

// claimLanding takes the developer's landing claim out of what it said and
// records it, returning the rest for the channels that read the reply after it.
//
// Every attempt overwrites what the last one claimed rather than accumulating:
// the claim describes the change as it now stands, and a repair attempt that
// finished the work must not be closed against the previous attempt's evidence
// claim — nor the reverse.
func (a *activeRun) claimLanding(text string) string {
	rest, claim, err := landing.Extract(text)
	a.state.LandingOutcome = ""
	a.state.LandingReason = ""
	a.state.LandingBlockedBy = ""
	a.state.LandingProblem = ""
	a.outcome.Landing = ""
	a.outcome.LandingReason = ""
	a.outcome.LandingBlockedBy = ""
	a.outcome.LandingProblem = ""
	if err != nil {
		// An unreadable claim is not swallowed the way an unreadable report is. A
		// report decides nothing, so losing one costs the operator a note; this
		// decides whether the item closes, and the developer that wrote a block was
		// trying to say something about that. Recording the problem withholds the
		// closure, which leaves an item open for somebody to settle rather than
		// closing one on a claim nobody could read.
		a.state.LandingProblem = singleLine(err.Error(), maxLandingProblemBytes)
		a.outcome.LandingProblem = a.state.LandingProblem
		return rest
	}
	if !claim.Made() {
		return rest
	}
	a.state.LandingOutcome = string(claim.Outcome)
	a.state.LandingReason = claim.Why
	a.outcome.Landing = claim.Outcome
	a.outcome.LandingReason = claim.Why
	// The marker is recorded only where it decides something. A discharging claim
	// that carried one is refused before it reaches here, and storing it against a
	// closure that then happens anyway would leave the record saying the item both
	// closed and waits.
	//
	// What the outcome carries is read back off the record rather than off the
	// claim, so the one derivation that discards a marker naming the item itself
	// decides for the surfaces as well as for the settlement.
	if !claim.Discharges() {
		a.state.LandingBlockedBy = claim.Impediment()
		a.outcome.LandingBlockedBy = a.state.LandingImpediment()
	}
	return rest
}

// claimedLanding rebuilds the claim from the durable record, which is what the
// reviewer is shown and what a work item's notes are written from. It is read
// back rather than carried alongside so that a repair round, a resumed run, and
// a later sweep all describe the same claim from the same place.
func claimedLanding(state runstate.State) landing.Claim {
	return landing.Claim{
		Outcome: landing.Outcome(state.LandingOutcome),
		Why:     state.LandingReason,
		// The marker as the settlement reads it rather than as it was written, so a
		// reviewer told the item is left waiting is looking at an item that is.
		BlockedBy: state.LandingImpediment(),
	}
}

// describeLanding is what a reviewer is told about the claim, so a diagnosis is
// judged as a diagnosis rather than as a missing implementation. A claim that
// could not be read is described as that: the reviewer is the one reader who can
// still tell from the change itself which of the two it was looking at.
func describeLanding(state runstate.State) string {
	if state.LandingProblem != "" {
		return "The developer claimed a landing outcome that could not be read: " + state.LandingProblem +
			"\nJudge the change on what it is, and say in your summary which of the two it looks like."
	}
	return claimedLanding(state).Describe()
}

// LandingDischarges reports whether this run's landing is the kind that closes
// its work item, from the outcome a caller was handed rather than from the
// durable record. It is the outcome's half of runstate.State.LandingDischarges
// and answers over the same two facts, so a surface describing a run from what
// it returned and one describing it from its record cannot disagree about
// whether the item was discharged.
func (o Outcome) LandingDischarges() bool {
	return o.Landing != landing.OutcomeEvidence && o.LandingProblem == ""
}

// UndischargedAccount is what a surface says about an item a run left open: the
// developer's own words where it wrote them, and what went wrong with the claim
// where it did not.
func (o Outcome) UndischargedAccount() string {
	if o.LandingProblem != "" {
		return "the landing outcome it claimed could not be read (" + o.LandingProblem + ")"
	}
	return strings.Join(strings.Fields(o.LandingReason), " ")
}

// UndischargedDisposition is where an item a run did not discharge was left, in
// the words every surface says it in. It is derived here rather than at each
// surface because the two dispositions are what an operator acts on differently:
// a parking waits for a person, and a dependency releases itself.
func (o Outcome) UndischargedDisposition() string {
	if impediment := strings.TrimSpace(o.LandingBlockedBy); impediment != "" {
		return "stays open waiting on " + impediment
	}
	return "is parked until somebody releases it"
}

// renderLandingNote is the claim as one line of a work item's notes, or nothing
// where the developer claimed nothing. It is folded to a single line because the
// notes are a list of them and a paragraph here would read as the run's summary.
func renderLandingNote(outcome Outcome) string {
	if outcome.LandingProblem != "" {
		return "Landing claim: unreadable (" + outcome.LandingProblem + "); this item was not closed against it"
	}
	if outcome.Landing == "" {
		return ""
	}
	line := fmt.Sprintf("Landing claim: %s — %s", outcome.Landing,
		strings.Join(strings.Fields(outcome.LandingReason), " "))
	// Where the claim asked for the item to wait on something rather than be
	// parked, the note says so: the two leave the item in different places, and
	// only one of them releases itself.
	if impediment := strings.TrimSpace(outcome.LandingBlockedBy); impediment != "" {
		line += " (left open waiting on " + impediment + ")"
	}
	return line
}

// undischargedLandingReason is what is written onto an item a landing did not
// discharge. It names the run, so the evidence that landed can be found from the
// item, and the developer's own account of why, so the item does not read
// afterwards as work somebody abandoned.
func undischargedLandingReason(state runstate.State) string {
	if state.LandingProblem != "" {
		return fmt.Sprintf("Yoyodyne run %s integrated its change and claimed a landing outcome that could not be read, so this item was not closed against it: %s. The change is on %s; whether it discharges this item is a person's call.",
			state.RunID, state.LandingProblem, state.TargetBranch)
	}
	if impediment := state.LandingImpediment(); impediment != "" {
		return fmt.Sprintf("Yoyodyne run %s landed evidence for this item and did not discharge it, so the item stays open with its change integrated, waiting on %s. The developer's account: %s",
			state.RunID, impediment, state.LandingReason)
	}
	return fmt.Sprintf("Yoyodyne run %s landed evidence for this item and did not discharge it, so the item stays open with its change integrated and parked. The developer's account: %s",
		state.RunID, state.LandingReason)
}

// undischargedParking is why an item a run did not discharge is not to be pulled
// again yet, in the one field selection actually reads. It is the parking
// default's whole substance: an item returned to the backlog bare is one the next
// pull selects, for another run and another diagnosis of the impediment the last
// one just diagnosed, and the run that made it is recorded as succeeded so no
// brake counts it either.
//
// It carries the developer's own account, because what a parking reason is for is
// the person deciding whether to release it, and the developer's account is the
// one sentence in the record that names what would release this item. The
// reason is bounded to what the tracker holds as one value on one line; the whole
// of it is on the item as notes either way, which is where a truncated account
// can be read in full.
func undischargedParking(state runstate.State) domain.WorkItemParking {
	if !state.LandingParks() {
		return ""
	}
	reason := fmt.Sprintf("yoyodyne run %s landed evidence for this item rather than the work, and did not discharge it: %s",
		state.RunID, state.LandingReason)
	if state.LandingProblem != "" {
		reason = fmt.Sprintf("yoyodyne run %s integrated a change for this item and claimed a landing outcome that could not be read (%s); a person has to say whether the change discharges it",
			state.RunID, state.LandingProblem)
	}
	return domain.WorkItemParking(singleLine(reason, domain.MaxWorkItemParkingBytes))
}

// settleUndischarged puts an item back in the backlog in the state its run's
// landing calls for: parked under the reason above, or open and waiting on the
// impediment the landing named. It is one function because both settlement sites
// — the run's own completion and the sweep that finishes an interrupted one —
// have to leave the item in the same place, and because neither of the two
// dispositions is the same act as the other.
//
// The dependency is recorded before the status, and that order is the whole of
// what stops the leave-open path being the bare openness it replaces: between
// making an item open and making it wait, a watch session polling the queue pulls
// it. The item is still claimed while the dependency is added, so there is no
// such window.
// Settling twice settles once, which is not a nicety here: the reopen is retried
// where the tracker was busy, and a sweep re-runs the whole settlement of a run
// whose reopen never landed. So the dependency is added only where the item does
// not already carry it, rather than added again and refused as a duplicate.
func settleUndischarged(ctx context.Context, tracker WorkTracker, state runstate.State) error {
	if impediment := state.LandingImpediment(); impediment != "" {
		item, err := tracker.Show(ctx, state.WorkItemID)
		if err != nil {
			return fmt.Errorf("read work item %s before making it wait on %s: %w", state.WorkItemID, impediment, err)
		}
		if !waitsOn(item, impediment) {
			if err := tracker.AddBlocker(ctx, state.WorkItemID, impediment); err != nil {
				return fmt.Errorf("make work item %s wait on the impediment its landing named: %w", state.WorkItemID, err)
			}
		}
	}
	_, err := tracker.Reopen(ctx, state.WorkItemID, undischargedLandingReason(state), undischargedParking(state))
	return err
}

// waitsOn reports a blocking dependency the item already carries on the named
// work. It reads the same relation the backlog's own readiness reads, so an item
// this calls settled is one the queue holds back.
func waitsOn(item beads.WorkItem, blockerID string) bool {
	for _, dependency := range item.Dependencies {
		if dependency.Type == "blocks" && dependency.ID == blockerID {
			return true
		}
	}
	return false
}
