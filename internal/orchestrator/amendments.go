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
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
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
		key := amendmentKey(proposal)
		if a.proposedAmendments[key] {
			continue
		}
		if err := a.pipeline.Amendments.Append(proposal); err != nil {
			a.noteAmendmentProblem(role, err)
			continue
		}
		// Remembered only once it is actually recorded, so a proposal the log
		// refused is not treated as already made: if the developer argues it again
		// on the next attempt and the log has recovered, that attempt keeps it.
		a.rememberAmendment(key)
		a.outcome.Amendments = append(a.outcome.Amendments, proposal)
	}
}

// amendmentKey is what makes two proposals the same argument: the same change to
// the same document. The reasoning is deliberately not part of it — a developer
// that restates its case differently on the next attempt is making the same
// request, and treating that as new would defeat the whole of this.
func amendmentKey(proposal amendment.Proposal) string {
	return proposal.Artifact + "\x00" + proposal.Change
}

func (a *activeRun) rememberAmendment(key string) {
	if a.proposedAmendments == nil {
		a.proposedAmendments = map[string]bool{}
	}
	a.proposedAmendments[key] = true
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
	// The homes and what is excluded from them are assembled in one place rather
	// than restated here, so a proposal resolves against exactly the set every
	// other reader sees. The invariants are excluded there: they carry the
	// identity scheme this one was modeled on rather than this one, and an
	// invariant is amended through its own lifecycle either way.
	store := artifact.StoreFor(a.pipeline.Repository, a.pipeline.Config.Product)
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
//
// It goes to two places, which is the whole of what makes a refusal something the
// proposer can act on. The outcome is what reaches the operator, and it was for a
// long time the only place it reached: the agent that wrote the block was never
// told, so a developer whose proposal was refused carried on believing it was
// waiting on somebody. The other place is the run's own state, which is what the
// developer's next invocation opens with.
func (a *activeRun) noteAmendmentProblem(role domain.AgentRole, cause error) {
	problem := fmt.Sprintf("a change the %s proposed was not recorded: %s", role, singleLine(cause.Error(), maxAmendmentProblemBytes))
	a.carryAmendmentRefusal(problem)
	if a.outcome.AmendmentProblem == "" {
		a.outcome.AmendmentProblem = problem
		return
	}
	a.outcome.AmendmentProblem += "; " + problem
}

// carryAmendmentRefusal puts one refusal where this run's next developer
// invocation reads it. The words are the harness's own and are carried verbatim:
// what is wrong with the block is the whole of what the developer needs to write
// a different one, and a paraphrase is the harness guessing at that.
//
// Past the bound the refusal is kept on the outcome and dropped from what is
// carried, which is the direction that cannot cost the agent anything it has not
// already been told: the list is emptied by every reply, so a reply that fills it
// has proposed more changes than one block may carry several times over, and the
// first ones say the same thing as the rest.
func (a *activeRun) carryAmendmentRefusal(problem string) {
	if len(a.state.RefusedAmendments) >= runstate.MaxCarriedAmendmentRefusals {
		return
	}
	a.state.RefusedAmendments = append(a.state.RefusedAmendments, singleLine(problem, runstate.MaxAmendmentRefusalBytes))
}

// clearCarriedAmendmentRefusals spends the refusals an invocation was shown. It
// is called where the reply to that invocation is recorded rather than where the
// prompt is built, so a prompt rebuilt after an interrupted attempt still carries
// them: what the clear means is that the developer has been told, and only a
// reply proves that.
func (a *activeRun) clearCarriedAmendmentRefusals() {
	a.state.RefusedAmendments = nil
}

// carriedAmendmentRefusals is what a developer's next turn opens with, ahead of
// everything else it is handed.
//
// It says the same three things a refused tracker block says to the role that
// sent it: nothing happened, here is why in the harness's own words, and ask
// again if you still want it. The one it adds is the durable-record half, because
// that is the failure this exists to end — a developer that believed its proposal
// had landed wrote into a checked-in document that it had raised one, and the
// claim outlived the run that made it.
func carriedAmendmentRefusals(refused []string) string {
	if len(refused) == 0 {
		return ""
	}
	var rendered strings.Builder
	rendered.WriteString("# A change you proposed was not recorded\n\n")
	rendered.WriteString("The harness refused it, so nothing is waiting on anybody: no owner was asked, and no decision will ever come back. ")
	rendered.WriteString("Do not describe the proposal as raised, and do not write into your change — or into anything else that outlives this run — that you have raised one.\n\n")
	rendered.WriteString("The refusal, in the harness's own words:\n\n")
	for _, problem := range refused {
		rendered.WriteString("- " + problem + "\n")
	}
	rendered.WriteString("\nIf the change is still worth proposing, propose it again in a block that answers what the refusal says was wrong with the one before it. If it is not, say so in your summary and leave it at that.\n\n")
	return rendered.String()
}
