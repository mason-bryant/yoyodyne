package artifact

// Which role may write which artifact, in code rather than in a persona.
//
// The design states artifact ownership as an authorization boundary rather than
// a prompt convention, and the invariants already enforce it that way: the
// architect owns them, Authorize is what says so, and no mutation reaches the
// filesystem without going through it. This is the same boundary for the rest of
// the canonical documents, deliberately the same model rather than a second one
// beside it. Enforcement that lives only in a persona is enforcement a
// configuration can weaken, and a boundary that holds because a well-behaved
// model honoured it is not one anybody can rely on.
//
// The table it encodes is the one the design records. The product manager owns
// the brief and the goals derived from it; the architect owns the designs and
// specifications, and the decision records with the invariants extracted from
// them. The development manager owns no document at all — its decomposition is
// Beads work rather than Markdown — which falls out of this table rather than
// being stated separately: no kind names it, so it owns none.
//
// What this does not bound by itself is an agent with an editor in its worktree.
// Three things narrow it. Every mutation the harness performs comes through
// here; a revision log that records a change by a role which does not own the
// artifact is reported every time the set is loaded, so a hand-edited claim of
// somebody else's authority is a named problem rather than something only a
// reader would notice; and a developer's diff is gated on the paths these
// documents live in before it reaches a reviewer, so an edit made with an editor
// is refused and handed back unless the work item granted the path
// (internal/protectedpath). That gate is about the file rather than the
// document, which is why it does not replace this: it refuses the edit whether
// or not the editor also touched the revision log, and it says nothing about who
// may amend what.
//
// That report is deliberately not a refusal, and the difference matters most for
// a revision recorded before this rule existed. The revision log is append-only:
// the only way to make a past entry lawful is to rewrite history, which is the
// one thing the log exists to prevent. So a document with such an entry keeps
// loading, keeps governing what is downstream of it, and stays amendable by its
// owner — and the entry keeps being reported until somebody decides what to do
// about it. Refusing instead would drop the document out of the set, cascade
// into the orphan and dangling-reference reports for everything that referred to
// it, and leave a document that could neither load nor be lawfully corrected.

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// ErrUnauthorized is what every unauthorized mutation returns, so a caller can
// tell a refused authority from a malformed artifact.
var ErrUnauthorized = errors.New("only the role that owns an artifact may create, amend, supersede, or retire it")

// Owner returns the role that owns a kind of artifact, and whether the kind is
// one the harness knows. An unknown kind has no owner rather than a default one:
// a document nobody can place is not one anybody can be authorized over.
func Owner(kind Kind) (domain.AgentRole, bool) {
	switch kind {
	case KindBrief, KindGoals, KindNonGoals:
		return domain.RoleProductManager, true
	case KindDesign, KindSpecification, KindDecision:
		return domain.RoleArchitect, true
	default:
		return "", false
	}
}

// Authorize reports whether a role may create, amend, supersede, or retire an
// artifact of a kind. Every other role proposes: a developer that found a design
// problem raises it for the architect, and a reviewer that found intent
// contradicted files a finding, and neither edits the document and carries on as
// if the change had been approved.
func Authorize(role domain.AgentRole, kind Kind) error {
	owner, known := Owner(kind)
	if !known {
		return fmt.Errorf("kind %q must be one of %s", kind, renderKinds())
	}
	if role == owner {
		return nil
	}
	if strings.TrimSpace(string(role)) == "" {
		return fmt.Errorf("%w; no role was named, and a %s artifact is the %s's", ErrUnauthorized, kind, owner)
	}
	return fmt.Errorf("%w; a %s artifact is the %s's, and the %s may propose a change instead", ErrUnauthorized, kind, owner, role)
}

// UnauthorizedRevisions reports every revision across a set that was recorded
// under a role which does not own the artifact it is written in. It is checked
// over the loaded set rather than in Validate for the reason above: the finding
// is about the record of a document rather than about whether the document can
// be read, and the two are not fixed the same way.
//
// One problem is reported per artifact rather than per revision. What somebody
// has to do about it is open the file and decide, and that is one job whether
// the log crossed the boundary once or four times.
func UnauthorizedRevisions(artifacts []Artifact) []ReferenceProblem {
	var problems []ReferenceProblem
	for _, candidate := range artifacts {
		// An artifact whose kind has no owner is not in a set in the first place:
		// Validate refuses the document over the kind, which is the thing to fix.
		owner, known := Owner(candidate.Kind)
		if !known {
			continue
		}
		var crossed []string
		for index, revision := range candidate.Revisions {
			if revision.By == owner {
				continue
			}
			crossed = append(crossed, fmt.Sprintf("revisions[%d] records the %s", index, revision.By))
		}
		if len(crossed) == 0 {
			continue
		}
		problems = append(problems, ReferenceProblem{
			Kind: ProblemUnauthorizedRevision,
			ID:   candidate.ID,
			Path: candidate.Path,
			Reason: fmt.Sprintf("%s; a %s artifact is the %s's, and every change to one is recorded under that role or is not a change anybody authorized",
				strings.Join(crossed, ", "), candidate.Kind, owner),
		})
	}
	return problems
}
