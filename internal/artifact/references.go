package artifact

// The relationships between artifacts, as opposed to one artifact's own
// identity: whether what a document says it supports is a document that exists,
// and whether anything connects it back to the product brief.
//
// Identity makes a relationship expressible without making it true. A goal that
// names a brief nobody wrote, a design that names a goal renamed last week, and
// a specification that names nothing at all are each a well-formed artifact,
// and each of them breaks the chain the whole scheme is for. Checking it by
// hand means reading every frontmatter block in the repository and holding the
// graph in your head, which is the work nobody does twice — and a chain nobody
// checks is a chain that is only believed.
//
// Nothing here refuses a document. A broken relationship is reported beside a
// set that still holds everything it loaded, for the same reason a
// specification that ignores its structure contract is still read: refusing it
// would lose intent somebody recorded, and leave the reader with a smaller
// repository instead of a named problem.

import (
	"fmt"
	"strings"
)

// ReferenceProblemKind is what is wrong with a relationship. There are two
// things that can be, and they are told apart rather than left to be read out
// of prose, because they are fixed differently: a reference that resolves to
// nothing is a name to correct, and an artifact nothing connects to the brief
// is a relationship somebody has yet to state.
type ReferenceProblemKind string

const (
	// ProblemDanglingReference is a `supports` entry naming an id that no
	// artifact in the set answers to.
	ProblemDanglingReference ReferenceProblemKind = "dangling-reference"
	// ProblemOrphan is an artifact that supports nothing tracing back to the
	// product brief.
	ProblemOrphan ReferenceProblemKind = "orphan"
)

// ReferenceProblem names one broken relationship, and the artifact it is
// written down in. That artifact is in the set: unlike a Problem, which is a
// file that could not be read as an artifact at all, this is a document that
// loaded and whose place in the chain is wrong.
type ReferenceProblem struct {
	Kind ReferenceProblemKind `json:"kind"`
	// ID and Path are the end of the relationship that is written down here, so
	// what is reported names the file somebody has to open. For a dangling
	// reference the other end is the id in the reason, which is the whole of what
	// is known about it — that is what makes it dangling.
	ID     string `json:"id"`
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

func (p ReferenceProblem) String() string {
	return fmt.Sprintf("%s (%s): %s", p.ID, p.Path, p.Reason)
}

// mustTraceToTheBrief reports whether an artifact of this kind has to reach the
// product brief. Two kinds do not. The brief is the root, so nothing is
// upstream of it. A decision record says how the product is built rather than
// what it is for: it is taken in service of the goals but is not a statement of
// intent downstream of them, and requiring every architectural decision to name
// a goal would report the whole decisions directory as broken on the day this
// check arrived, over a relationship nobody has been asked to record.
func mustTraceToTheBrief(kind Kind) bool {
	switch kind {
	case KindBrief, KindDecision:
		return false
	default:
		return true
	}
}

// referenceProblems reports every broken relationship across a whole set: the
// references that resolve to nothing, and the artifacts nothing connects to the
// brief. Both need the set rather than one file, which is why neither is in
// Validate. Problems are reported in the order the set holds its artifacts, so
// two loads of one repository report the same thing in the same order.
//
// refused is the set's own list of files that are in an artifact home and were
// not read as artifacts. It is consulted only to say so: a reference to a
// document that is sitting right there and was refused reads as a document
// nobody wrote otherwise, which sends whoever fixes it looking for a file that
// already exists.
func referenceProblems(artifacts []Artifact, refused []Problem) []ReferenceProblem {
	recorded := make(map[string]Artifact, len(artifacts))
	briefRecorded := false
	for _, candidate := range artifacts {
		recorded[candidate.ID] = candidate
		if candidate.Kind == KindBrief {
			briefRecorded = true
		}
	}

	var problems []ReferenceProblem
	for _, candidate := range artifacts {
		reported := make(map[string]bool, len(candidate.Supports))
		for _, reference := range candidate.Supports {
			if _, resolves := recorded[reference]; resolves || reported[reference] {
				continue
			}
			reported[reference] = true
			problems = append(problems, ReferenceProblem{
				Kind:   ProblemDanglingReference,
				ID:     candidate.ID,
				Path:   candidate.Path,
				Reason: danglingReason(reference, refused),
			})
		}
		if !mustTraceToTheBrief(candidate.Kind) || reachesTheBrief(candidate, recorded) {
			continue
		}
		problems = append(problems, ReferenceProblem{
			Kind:   ProblemOrphan,
			ID:     candidate.ID,
			Path:   candidate.Path,
			Reason: orphanReason(candidate, briefRecorded),
		})
	}
	return problems
}

// reachesTheBrief walks upstream from one artifact and reports whether any of
// what it arrives at is the brief. Only references that resolve are followed:
// one that does not is already reported as the broken relationship it is, and
// guessing what it meant to name would be exactly the inference this scheme
// replaces. Nothing is followed twice, so a cycle is a set of artifacts that
// reach nothing rather than a walk that never ends.
func reachesTheBrief(start Artifact, recorded map[string]Artifact) bool {
	visited := map[string]bool{start.ID: true}
	pending := []Artifact{start}
	for len(pending) > 0 {
		current := pending[0]
		pending = pending[1:]
		for _, reference := range current.Supports {
			upstream, resolves := recorded[reference]
			if !resolves || visited[upstream.ID] {
				continue
			}
			if upstream.Kind == KindBrief {
				return true
			}
			visited[upstream.ID] = true
			pending = append(pending, upstream)
		}
	}
	return false
}

// danglingReason says what a reference names and why nothing answers to it.
func danglingReason(reference string, refused []Problem) string {
	for _, problem := range refused {
		if idForPath(problem.Path) != reference {
			continue
		}
		return fmt.Sprintf("it supports %q, which is not a recorded artifact: %s is in an artifact home and was not read as one (%s)",
			reference, problem.Path, problem.Reason)
	}
	return fmt.Sprintf("it supports %q, and no artifact answers to that id", reference)
}

// orphanReason says why nothing connects an artifact to the brief. The three
// cases are separated because they are three different things to do about it:
// write the brief, state what this supports, or correct what it already
// supports.
func orphanReason(candidate Artifact, briefRecorded bool) string {
	switch {
	case !briefRecorded:
		// Naming the missing root beats reporting every document in the repository
		// as separately unconnected, which is what the other two reasons would say
		// here and neither of them would be the thing to fix.
		return `nothing traces to the product brief, because no artifact of kind "brief" is recorded; the chain has no root yet`
	case len(candidate.Supports) == 0:
		return "it names nothing upstream; every artifact but the brief and the decision records supports something that traces back to the brief"
	default:
		return fmt.Sprintf("it supports %s, and no chain from there reaches the brief", quotedList(candidate.Supports))
	}
}

func quotedList(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, fmt.Sprintf("%q", value))
	}
	return strings.Join(quoted, ", ")
}
