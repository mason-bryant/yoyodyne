package orchestrator

// What a developer claims its change does to the work item is read here, and it
// is the one channel out of a developer that decides something about the run:
// closure follows the claim rather than the integration. A run that lands
// evidence integrates exactly as any other run does and leaves its item open.

import (
	"fmt"
	"strings"

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
	a.state.LandingProblem = ""
	a.outcome.Landing = ""
	a.outcome.LandingReason = ""
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
	return rest
}

// claimedLanding rebuilds the claim from the durable record, which is what the
// reviewer is shown and what a work item's notes are written from. It is read
// back rather than carried alongside so that a repair round, a resumed run, and
// a later sweep all describe the same claim from the same place.
func claimedLanding(state runstate.State) landing.Claim {
	return landing.Claim{Outcome: landing.Outcome(state.LandingOutcome), Why: state.LandingReason}
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
	return fmt.Sprintf("Landing claim: %s — %s", outcome.Landing,
		strings.Join(strings.Fields(outcome.LandingReason), " "))
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
	return fmt.Sprintf("Yoyodyne run %s landed evidence for this item and did not discharge it, so the item stays open with its change integrated. The developer's account: %s",
		state.RunID, state.LandingReason)
}
