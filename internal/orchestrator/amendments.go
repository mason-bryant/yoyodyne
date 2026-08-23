package orchestrator

// A change an agent proposes to a document it does not own leaves the run
// through here. Nothing in this file may change what a run did: a proposal is
// recorded beside the run exactly as a report is, it decides nothing about the
// work, and a proposal the harness cannot read or cannot keep is named on the
// outcome rather than failing the attempt it arrived with.
//
// That is the point of the channel. A developer that had to choose between
// editing the design and being ignored would eventually edit the design; a
// developer whose proposal costs it nothing and reaches the architect has no
// reason to.

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// AmendmentRecorder is where proposed artifact changes are kept. It is
// satisfied by runstate.AmendmentStore.
type AmendmentRecorder interface {
	Append(proposal amendment.Proposal) error
}

// collectAmendments records the changes one agent invocation proposed. Every
// failure here is noted and swallowed, for the same reason a report's is: a run
// that failed because an agent argued with the design would teach every agent to
// stop arguing with it.
func (a *activeRun) collectAmendments(role domain.AgentRole, entries []amendment.Entry) {
	if len(entries) == 0 {
		return
	}
	// A pipeline with nowhere to record loses all of them at once, and says so
	// once: one note per lost proposal would describe the same missing store
	// several times over.
	if a.pipeline.Amendments == nil {
		a.noteAmendmentProblem(role, fmt.Errorf("nothing records proposed amendments for this run, so the %d change(s) the %s proposed were not kept", len(entries), role))
		return
	}
	artifacts, err := a.artifacts()
	if err != nil {
		a.noteAmendmentProblem(role, err)
		return
	}
	collected, problem := amendment.Collect(entries, amendment.Attribution{
		Role:         role,
		Agent:        a.pipeline.agentNameForRole(role),
		RunID:        a.state.RunID,
		WorkItemID:   a.state.WorkItemID,
		ProductID:    a.pipeline.Config.Product.ID,
		RepositoryID: string(a.pipeline.Config.Product.RepositoryID),
	}, artifacts, a.pipeline.clock().Now())
	if problem != nil {
		// Collection is per-proposal, so this names the ones that could not be
		// recorded while the ones that could still are.
		a.noteAmendmentProblem(role, problem)
	}
	for _, proposal := range collected {
		// A developer that could not be talked out of its argument makes it again on
		// every repair attempt, and each collection would otherwise mint a fresh id
		// for it: one disagreement would arrive as up to repair_attempts_before_replan
		// separate proposals, and whoever decides would answer the same argument
		// several times to clear the queue. The second and later copies within a run
		// are dropped rather than noted, because nothing was lost — the first one is
		// recorded and is the one waiting.
		request := newAmendmentRequest(proposal)
		if a.alreadyProposed(request) {
			continue
		}
		if err := a.pipeline.Amendments.Append(proposal); err != nil {
			a.noteAmendmentProblem(role, err)
			continue
		}
		// Remembered only once it is actually recorded, so a proposal the log
		// refused is not treated as already made: if the developer argues it again
		// on the next attempt and the log has recovered, that attempt keeps it.
		a.rememberAmendment(request)
		a.outcome.Amendments = append(a.outcome.Amendments, proposal)
	}
}

// sameArgumentSimilarity is how much of the wording two changes to one document
// have to share before the second is the first restated. An agent asked the same
// question twice rarely answers it to the byte, so a comparison that only caught
// an exact repeat would let a clause reworded between attempts through as a new
// argument — which is what a run whose five proposals held two such pairs
// actually did.
//
// It is set high, and that ceiling is the safe half of the trade. A repeat that
// gets through costs whoever decides one extra proposal to read; a distinct
// argument mistaken for a repeat is dropped silently and is simply gone, which
// is the cost this whole channel exists to avoid. On the run that reported the
// problem, the restatement-of-a-clause pair shared 0.96 of its wording while the
// most alike pair the architect judged distinct shared 0.33, so anything in
// between separates them — and nothing lexical separates a proposal restated at
// length from a genuinely different change to the same document, which is why
// that second kind still reaches the decider rather than being guessed at.
const sameArgumentSimilarity = 0.85

// amendmentRequest is a proposal reduced to what makes two of them the same
// argument: the document, and the words the change is made of. The reasoning is
// deliberately not part of it — a developer that restates its case differently on
// the next attempt is making the same request, and treating that as new would
// defeat the whole of this.
type amendmentRequest struct {
	artifact string
	words    map[string]bool
	// change is the wording itself, which is what a change carrying no words at
	// all is compared by. Nothing else here would tell two of those apart.
	change string
}

func newAmendmentRequest(proposal amendment.Proposal) amendmentRequest {
	return amendmentRequest{
		artifact: proposal.Artifact,
		words:    changeWords(proposal.Change),
		change:   strings.TrimSpace(proposal.Change),
	}
}

var changeWordPattern = regexp.MustCompile(`[\p{L}\p{N}]+`)

// changeWords is the change as the set of words it is made of, so that the same
// request phrased with a clause moved, a word swapped, or different punctuation
// reduces to nearly the same thing.
func changeWords(change string) map[string]bool {
	words := map[string]bool{}
	for _, word := range changeWordPattern.FindAllString(strings.ToLower(change), -1) {
		words[word] = true
	}
	return words
}

// sameArgument reports whether the two ask the same thing of the same document.
// The comparison is how much of their wording they share, which is the same
// question the exact match was asking with no tolerance for an agent phrasing it
// twice.
func (r amendmentRequest) sameArgument(other amendmentRequest) bool {
	if r.artifact != other.artifact {
		return false
	}
	if len(r.words) == 0 || len(other.words) == 0 {
		return r.change == other.change
	}
	shared := 0
	for word := range r.words {
		if other.words[word] {
			shared++
		}
	}
	union := len(r.words) + len(other.words) - shared
	return float64(shared) >= sameArgumentSimilarity*float64(union)
}

// alreadyProposed reports whether this run has recorded the same argument
// already. Every proposal made so far is compared rather than one looked up,
// which is affordable because a reply proposes at most three changes and a run
// makes a bounded number of attempts.
func (a *activeRun) alreadyProposed(request amendmentRequest) bool {
	for _, made := range a.proposedAmendments {
		if made.sameArgument(request) {
			return true
		}
	}
	return false
}

func (a *activeRun) rememberAmendment(request amendmentRequest) {
	a.proposedAmendments = append(a.proposedAmendments, request)
}

// artifacts is the recorded artifact set a proposal's document is resolved
// against, read once per run and only when something actually proposes a
// change. Loading it is deliberately not part of starting a run: a repository
// with no artifact homes yet runs work exactly as it always did, and only a run
// that proposes a change to a document is told there are none.
func (a *activeRun) artifacts() (artifact.Set, error) {
	if a.artifactSet != nil {
		return *a.artifactSet, nil
	}
	product := a.pipeline.Config.Product
	store := artifact.Store{
		RepositoryRoot: a.pipeline.Repository,
		Homes:          []string{product.Specifications, product.Designs, product.Decisions},
		// The invariants carry the identity scheme this one was modeled on rather
		// than this one, so they are not artifacts in the set. They are the
		// architect's either way, and an invariant is amended through its own
		// lifecycle.
		Excluded: []string{product.Invariants},
	}
	set, err := store.Load()
	if err != nil {
		return artifact.Set{}, fmt.Errorf("load the recorded artifacts to resolve the proposal against: %w", err)
	}
	a.artifactSet = &set
	return set, nil
}

// maxAmendmentProblemBytes keeps one lost proposal to a readable line of the
// outcome.
const maxAmendmentProblemBytes = 512

// noteAmendmentProblem records a proposal that did not reach the durable log. It
// accumulates rather than replaces, because losing the first proposal and then
// losing a second is two facts.
func (a *activeRun) noteAmendmentProblem(role domain.AgentRole, cause error) {
	problem := fmt.Sprintf("a change the %s proposed was not recorded: %s", role, singleLine(cause.Error(), maxAmendmentProblemBytes))
	if a.outcome.AmendmentProblem == "" {
		a.outcome.AmendmentProblem = problem
		return
	}
	a.outcome.AmendmentProblem += "; " + problem
}
