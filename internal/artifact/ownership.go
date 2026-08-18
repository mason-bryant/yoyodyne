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
// What this does not bound is an agent with an editor in its worktree, which is
// the same gap the design records for pushing and merging. Two things narrow it.
// Every mutation the harness performs comes through here, and a revision log
// that records a change by a role which does not own the artifact is refused
// when the file is read, so a hand-edited claim of somebody else's authority is
// a document that stops loading rather than one that quietly governs.

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
