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
// waiting on somebody. The other place is the run's own state, which is what that
// role's next invocation opens with.
func (a *activeRun) noteAmendmentProblem(role domain.AgentRole, cause error) {
	problem := fmt.Sprintf("a change the %s proposed was not recorded: %s", role, singleLine(cause.Error(), maxAmendmentProblemBytes))
	a.carryAmendmentRefusal(role, problem)
	if a.outcome.AmendmentProblem == "" {
		a.outcome.AmendmentProblem = problem
		return
	}
	a.outcome.AmendmentProblem += "; " + problem
}

// carryAmendmentRefusal puts one refusal where the role that earned it reads it
// next. The words are the harness's own and are carried verbatim: what is wrong
// with the block is the whole of what its author needs to write a different one,
// and a paraphrase is the harness guessing at that.
//
// The role is recorded with them because every reader of this list is a
// particular agent being told what it itself proposed. Only the developer's reply
// is scanned for proposals today, so only the developer can earn one — but this
// is called with whatever role collected, and a refusal that arrived without its
// proposer would be shown to whoever was invoked next under a heading claiming
// they wrote it, while its actual author went on believing it had landed. That is
// the failure this exists to end, moved one role over, so what decides who is
// shown a refusal is recorded rather than assumed.
//
// Past the bound the refusal is kept on the outcome and dropped from what is
// carried, which is the direction that cannot cost an agent anything it has not
// already been told: a role's own reply drops what that role was shown, so a run
// that fills this has proposed more changes than one block may carry several
// times over, and the first ones say the same thing as the rest.
func (a *activeRun) carryAmendmentRefusal(role domain.AgentRole, problem string) {
	if len(a.state.RefusedAmendments) >= runstate.MaxCarriedAmendmentRefusals {
		return
	}
	a.state.RefusedAmendments = append(a.state.RefusedAmendments, runstate.AmendmentRefusal{
		Role:    role,
		Problem: singleLine(problem, runstate.MaxAmendmentRefusalBytes),
	})
}

// clearCarriedAmendmentRefusals spends the refusals one role's invocation was
// shown, and leaves every other role's where they are: what the clear records is
// that this role has been told, which says nothing about anybody else.
//
// It is called where the reply to that invocation is recorded rather than where
// the prompt is built, so a prompt rebuilt after an interrupted attempt still
// carries them — only a reply proves the agent was actually told.
func (a *activeRun) clearCarriedAmendmentRefusals(role domain.AgentRole) {
	kept := make([]runstate.AmendmentRefusal, 0, len(a.state.RefusedAmendments))
	for _, refused := range a.state.RefusedAmendments {
		if refused.Role != role {
			kept = append(kept, refused)
		}
	}
	if len(kept) == 0 {
		a.state.RefusedAmendments = nil
		return
	}
	a.state.RefusedAmendments = kept
}

// carriedAmendmentRefusals is what one role's next turn opens with, ahead of
// everything else it is handed. It renders that role's own refusals and no
// others: the whole of what it says is "you proposed this and it was not
// recorded", which is false of anything another agent proposed.
//
// It says the same three things a refused tracker block says to the role that
// sent it: nothing happened, here is why in the harness's own words, and ask
// again if you still want it. The one it adds is the durable-record half, because
// that is the failure this exists to end — a developer that believed its proposal
// had landed wrote into a checked-in document that it had raised one, and the
// claim outlived the run that made it.
func carriedAmendmentRefusals(role domain.AgentRole, refused []runstate.AmendmentRefusal) string {
	var mine []string
	for _, refusal := range refused {
		if refusal.Role == role {
			mine = append(mine, refusal.Problem)
		}
	}
	if len(mine) == 0 {
		return ""
	}
	var rendered strings.Builder
	rendered.WriteString("# A change you proposed was not recorded\n\n")
	rendered.WriteString("The harness refused it, so nothing is waiting on anybody: no owner was asked, and no decision will ever come back. ")
	rendered.WriteString("Do not describe the proposal as raised, and do not write into your change — or into anything else that outlives this run — that you have raised one.\n\n")
	rendered.WriteString("The refusal, in the harness's own words:\n\n")
	for _, problem := range mine {
		rendered.WriteString("- " + problem + "\n")
	}
	rendered.WriteString("\nIf the change is still worth proposing, propose it again in a block that answers what the refusal says was wrong with the one before it. If it is not, say so in your summary and leave it at that.\n\n")
	return rendered.String()
}
