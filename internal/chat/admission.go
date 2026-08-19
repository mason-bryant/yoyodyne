package chat

// What the operator is asked about before work reaches the queue.
//
// The harness began by asking about every work item, because there was nothing
// else it could honestly do: the product manager proposed items directly, and
// the operator was the only thing standing between a proposal and the tracker.
// That is the gate this moves. What the operator approves is a goal, and work
// that traces to a goal they approved is admitted without a further prompt.
//
// Approval moved up a level; it did not disappear, and three things still stop
// and ask. Work that traces to no goal and work that would cut against one are
// raised as concerns rather than proposed at all, so they never reach here. A
// change to the goals themselves is the operator's and reaches the queue
// through nothing: the product manager may argue for one in prose and cannot
// make one. What is left for this file is the fourth case — work that names a
// goal — and the question it answers is whether anybody agreed to that goal.
//
// The whole arrangement rests on that question being answered honestly rather
// than asserted. Moving the gate up only moves it if work cannot reach the
// queue without demonstrably serving an approved goal, so an attribution that
// resolves is not enough: it has to resolve to a goal in a document the
// operator approved, as that document now stands. Anything short of that is put
// to them, which is the gate staying exactly where it was for that item.
//
// # What stops an agent approving its own goal
//
// An approval records `by: operator` on the strength of who ran the command,
// and nothing in the record distinguishes the operator from anything else with a
// shell. That was harmless while the record gated nothing; here it is what lets
// work past, so the boundary has to be somewhere. It is not in the record, and
// it is not asserted in a prompt — it is the two things the harness already
// enforces deterministically.
//
// The roles that reach this file have no tools at all: a conversation is run
// with an empty tool list and a read-only permission mode, so the product
// manager cannot run a command, let alone that one. The roles that do have a
// shell work inside a run's worktree, and a run's change is compared against the
// protected paths before any check runs and before any reviewer sees it — the
// goals live in one of those homes, so an approval a developer wrote is refused
// with the rest of the diff and never reaches the repository the goals are read
// from. The exception is a path the work item grants, and a grant is read from
// the fields the operator and the managers write rather than from the notes a
// run fills in, so nothing can grant itself one.
//
// So the boundary is that an agent cannot write into the goals at all, rather
// than that an approval says who gave it. If either of those two enforcements
// is ever loosened, this is what was resting on them.

import (
	"fmt"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/goal"
)

// Admission is the project's `approvals.work_items` policy, in the terms this
// package thinks in. It is passed in rather than read here for the reason the
// artifact package's approval policy is: what a project asks the operator about
// is the project's decision, and this package's job is only to hold to it.
//
// Its zero value asks about everything. That is deliberate rather than
// incidental: a conversation assembled without a policy is one nobody stated a
// policy for, and the safe reading of no policy is the gate the harness started
// with rather than the one it is allowed to run without.
type Admission struct {
	WorkItems domain.ApprovalMode
}

// PerItemApproval reports the operator being asked about every work item before
// it is admitted.
func (a Admission) PerItemApproval() bool { return a.WorkItems != domain.ApprovalAutomatic }

// perItemApprovalReason is why the operator is being asked about work that
// would otherwise have been admitted. It names the setting, because an operator
// who wants the other behavior has to be able to find the thing to change.
const perItemApprovalReason = `this project asks about every work item before it is admitted, as approvals.work_items is "human"`

// admissionGap says why work serving the named goal is not admitted to the
// queue without the operator being asked, and is empty exactly when it is. It
// is the one place that decision is made, so the proposal path and the tracker
// path can never come to answer it differently.
func (s *Session) admissionGap(named string) string {
	if s.options.Admission.PerItemApproval() {
		return perItemApprovalReason
	}
	return s.options.Goals.Attribute(named).ApprovalGap()
}

// AdmittedItem is one work item the harness put in the queue without asking,
// because it serves a goal the operator approved. It is reported for exactly
// that reason: autonomy the operator cannot see afterwards is indistinguishable
// from work happening behind their back, so what was admitted without a prompt
// is said out loud where a decision would have been.
type AdmittedItem struct {
	ProposalID string `json:"proposal_id"`
	WorkItemID string `json:"work_item_id"`
	Title      string `json:"title"`
	// Goal is the approved goal the work traces to, which is the whole of why
	// nobody was asked.
	Goal string `json:"goal"`
}

// Render describes one admitted item for an operator reading what went into
// the queue without them. Everything in it but the identifiers came from the
// provider, so the goal and the title are indented under the harness's own
// line.
func (a AdmittedItem) Render() string {
	return fmt.Sprintf("[%s] %s\n", a.WorkItemID, a.ProposalID) +
		indent(a.Title) + indent("goal: "+a.Goal)
}

// admitted is what the record keeps about work the harness admitted itself: the
// proposal it came from and the goal that let it through. The reason is written
// down rather than left to be inferred from the policy in force at the time,
// because the policy is a file that changes and the record is not.
type admitted struct {
	PendingProposal
	Reason string `json:"reason"`
}

// admissionReason is what an admitted item's record and its notes say about why
// nobody was asked.
func admissionReason(named string) string {
	return fmt.Sprintf("admitted without asking the operator, because it serves the approved goal %q", named)
}

// approvedGoalNote is the sentence a work item admitted without a prompt
// records about where its authority came from. It is deliberately not the
// sentence an approved proposal records: an item that said the operator
// approved it when they were never asked would be the one lie this whole
// arrangement cannot afford.
func approvedGoalNote(attribution goal.Attribution) string {
	return fmt.Sprintf("admitted by the harness without asking the operator, because it serves a goal the operator approved in %s",
		attribution.Goal.ArtifactID)
}
