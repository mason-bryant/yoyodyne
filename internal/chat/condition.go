package chat

// A done-condition no developer run may satisfy is refused where the item is
// written.
//
// Every door into the queue that carries an item's text asks the same question
// of it: does a clause of what done means name a path under one of the
// artifact homes, or a document one of those homes owns, without a grant for
// it? Three items in one week were admitted with one — a design's query list
// to mark, a design's status entry to reconcile, a ruling to record on a design
// — and each cost a run before the reviewer or the developer found the clause
// the run could not meet. The check belongs at admission, where refusing it
// costs the role a sentence and the correction is the one the reviewer would
// have named anyway: take the clause out and say the document's owner amends
// it, or carry the grant where the change behind it is already decided.
//
// The run asks the same question of the item it is handed, before it claims it
// (orchestrator.refuseUngrantedCondition), because two of the four fields a
// grant and a condition are read from are written with the tracker's own
// command rather than through any door here, and because the queue predates
// this gate.

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/protectedpath"
)

// conditionRefusal is why an item whose done-conditions read as given will not
// be admitted, or empty where they name nothing a run may not write. The grants
// are read from the fields given, which are the ones a grant is honoured from.
func (s *Session) conditionRefusal(description, acceptanceCriteria string, grantedFrom ...string) string {
	problems := s.options.ArtifactHomes.ConditionProblems(description, acceptanceCriteria, protectedpath.Grants(grantedFrom...))
	if len(problems) == 0 {
		return ""
	}
	return "its done-conditions name a document no developer run may write, so nothing was written: " + errors.Join(problems...).Error()
}

// updateConditionRefusal judges an update that rewrites the description, as the
// item will read once it lands. The item as the tracker held it was read as the
// action was aimed, so the fields the action leaves alone are read from there;
// where that read failed, the action's own words are all there is, and they are
// judged on their own rather than the update being waved through because the
// item could not be seen.
//
// An update that leaves the description alone — a note, an executor, a title —
// writes no done-condition and is judged on none. That is deliberate: an item
// admitted before this gate with such a condition in its criteria is exactly
// the item the product manager has to be able to write a note onto saying so,
// and the run refuses to start on it either way.
func (s *Session) updateConditionRefusal(outcome *TrackerOutcome, change beads.WorkItemChange) string {
	if change.Description == "" {
		return ""
	}
	current := beads.WorkItem{}
	if outcome.target != nil {
		current = *outcome.target
	}
	title := current.Title
	if change.Title != "" {
		title = change.Title
	}
	return s.conditionRefusal(change.Description, current.AcceptanceCriteria, title, change.Description, current.Design, current.AcceptanceCriteria)
}

// ProposalConditionError reports that a turn proposed work whose done-conditions
// name a document no developer run may write. Like ProposalGoalError it is not a
// broken conversation: the answer is real, nothing was proposed, and what is
// wrong is the one thing the operator would have been relying on — that
// approving the work approves something a run can finish.
type ProposalConditionError struct {
	Err error
}

func (e *ProposalConditionError) Error() string {
	return "the product manager proposed work whose done-conditions name a document no developer run may write: " + e.Err.Error()
}

func (e *ProposalConditionError) Unwrap() error { return e.Err }

// verifyProposalConditions checks every proposal's done-conditions before the
// operator is asked about any of it, for the reason the goals are checked
// there: an approval that then fails has already spent the decision it was
// asking for. A proposal carries a title and a description and can set neither
// design guidance nor acceptance criteria, so those two are the whole of what
// there is to read.
func (s *Session) verifyProposalConditions(proposals []Proposal) error {
	var problems []error
	for _, proposal := range proposals {
		conditions := s.options.ArtifactHomes.ConditionProblems(proposal.Description, "", protectedpath.Grants(proposal.Title, proposal.Description))
		if len(conditions) == 0 {
			continue
		}
		problems = append(problems, fmt.Errorf("%q: %w", strings.TrimSpace(proposal.Title), errors.Join(conditions...)))
	}
	return errors.Join(problems...)
}
