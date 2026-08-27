package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The harness performs, the roles decide. The developer's phase is what causes
// a branch to be pushed and a pull request to be opened, and the reviewer's
// approving verdict is what causes it to be merged — but the harness executes
// both, and neither role is given a credential, a tool, or a request for
// either. On the reviewer's side that is a boundary rather than an arrangement:
// it runs with no tools at all, so the role whose verdict authorizes the merge
// has no way to perform one, and its verdict reaches the target branch only
// through the checks, the independence evidence, and the fast-forward rule that
// already gate integration. The developer's side is weaker and the design says
// so: it has a shell, so what keeps it from pushing is its sandbox and its
// contract rather than something enforced here.
//
// Which branch is authoritative is settled the same way. The local target
// branch is the one that moves, and the forge is then asked to merge the pull
// request that carries exactly that commit. A merge commit is what the forge
// makes of it, so the remote target ends up one commit ahead of the local one
// and identical in content: there is one promotion, one reviewed commit, and
// one answer about where a project's work is, published under a merge commit
// the forge owns. A remote that ends up carrying anything else is reported
// rather than reconciled behind the operator's back.

func (p Pipeline) publishes() bool {
	return p.Config.Approvals.Publishing == domain.ApprovalAutomatic
}

// resolvePublishing decides whether this run publishes. A project that did not
// ask for it never does. A project that did, but whose repository has no
// configured remote, degrades to exactly the local behavior it had before
// publishing existed, and says so; that is a property of the repository rather
// than a misconfiguration. Everything else — no publisher wired, no forge CLI,
// no forge authentication — is a configuration failure reported before any work
// is claimed, because a project that asked to publish and silently did not
// would have no way to notice.
func (p Pipeline) resolvePublishing(ctx context.Context) (bool, string, error) {
	if !p.publishes() {
		return false, "", nil
	}
	if p.Publisher == nil {
		return false, "", errors.New("automatic publishing requires a pull request publisher")
	}
	configured, err := p.Worktrees.RemoteConfigured(ctx)
	if err != nil {
		return false, "", fmt.Errorf("resolve the publishing remote: %w", err)
	}
	if !configured {
		return false, fmt.Sprintf("the repository has no %q remote, so this run stays local", p.Config.Execution.Remote), nil
	}
	availability, err := p.Publisher.Availability(ctx)
	if err != nil {
		return false, "", fmt.Errorf("check the pull request CLI: %w", err)
	}
	if !availability.Installed {
		return false, "", errors.New("automatic publishing requires the GitHub CLI; install `gh` or set approvals.publishing to human")
	}
	if !availability.Authenticated {
		return false, "", errors.New("the GitHub CLI is not authenticated; run `gh auth login` before handing published work to Yoyodyne")
	}
	return true, "", nil
}

// publishAttempt publishes what one developer attempt produced. The harness
// commits the work itself, pushes the run branch, and opens the pull request if
// the branch does not have one yet; a repair attempt updates that same request
// rather than opening another. Publishing happens before the checks run, which
// is deliberate: a pull request is where work is reviewed, and work that does
// not pass yet is exactly what a reviewer should be able to see.
func (a *activeRun) publishAttempt(ctx context.Context) error {
	if !a.publishing {
		return nil
	}
	publication, err := a.pipeline.Worktrees.PublishBranch(ctx, a.worktree, attemptMessage(a.item, a.outcome))
	// The commit the harness made is what permits this worktree's HEAD to have
	// moved, so it is recorded the moment it exists — including when the push
	// that followed it failed. A later step, or a later process resuming this
	// run, then accepts exactly that commit and nothing an agent could put in
	// its place.
	if publication.Commit != "" {
		a.recordHarnessCommit(publication.Commit)
	}
	if errors.Is(err, gitworktree.ErrNoChanges) {
		// An attempt that changed nothing has nothing to publish. It is not a
		// failure here: the checks and the reviewer are what judge an empty
		// change, and they run next.
		return nil
	}
	if err != nil {
		return fmt.Errorf("publish the developer branch: %w", err)
	}
	pullRequest, err := a.pipeline.Publisher.Ensure(ctx, publish.Request{
		Head:  publication.Branch,
		Base:  a.worktree.TargetBranch,
		Title: pullRequestTitle(a.item, a.outcome),
		Body:  pullRequestBody(a.item, a.outcome, a.worktree.TargetBranch),
	})
	if err != nil {
		return fmt.Errorf("open the pull request for the developer branch: %w", err)
	}
	published := &runstate.PullRequest{
		Remote:     publication.Remote,
		Branch:     publication.Branch,
		Number:     pullRequest.Number,
		URL:        pullRequest.URL,
		HeadCommit: publication.Commit,
		State:      pullRequest.State,
		Merged:     pullRequest.Merged,
	}
	a.state.PullRequest = published
	a.outcome.PullRequest = published
	a.state.UpdatedAt = a.pipeline.clock().Now()
	if err := a.pipeline.Store.Save(a.state); err != nil {
		return fmt.Errorf("record the published pull request: %w", err)
	}
	return nil
}

// republishRebase puts a replayed run branch back where the pull request that
// carries it can see it. Replaying a change onto a moved target rewrites the
// branch, so the published head stops being the change the harness would
// promote, and a forge asked to merge the old head would put work on the remote
// that the authoritative local branch does not have.
//
// The remote branch is replaced from exactly the commit the harness published
// there, which is the same compare-and-swap the local target is advanced by. A
// remote branch carrying anything else is refused rather than overwritten, and
// that refusal stops the run: the promotion has not happened yet, so there is
// nothing outstanding to report and nothing to lose by leaving it to a person.
func (a *activeRun) republishRebase(ctx context.Context, rebase gitworktree.Rebase) error {
	if !a.publishing || a.outcome.PullRequest == nil {
		return nil
	}
	published := *a.outcome.PullRequest
	if published.HeadCommit == rebase.HeadCommit {
		return nil
	}
	publication, err := a.pipeline.Worktrees.RepublishBranch(ctx, a.worktree, published.HeadCommit)
	if err != nil {
		return fmt.Errorf("republish the replayed developer branch: %w", err)
	}
	published.HeadCommit = publication.Commit
	a.state.PullRequest = &published
	a.outcome.PullRequest = &published
	a.state.UpdatedAt = a.pipeline.clock().Now()
	if err := a.pipeline.Store.Save(a.state); err != nil {
		return fmt.Errorf("record the republished pull request: %w", err)
	}
	return nil
}

// settleRemoteTarget settles where the remote target branch stands before the
// promotion rather than only after it.
//
// The check publishIntegration makes against the remote runs once the local
// target branch has already moved, and nothing moves it back. A run that finds
// the remote somewhere else there has integrated already: the item closes as
// integrated, an outstanding publication is recorded, and what is left is a
// local target the remote does not carry and no fast-forward reconciles — a
// divergence nothing owns. Asking the same question first is what makes the same
// movement recoverable, because nothing has been promoted yet and there is still
// a change to replay.
//
// A remote that moved onto work this repository can take on is taken on. The
// local target is fast-forwarded onto it, which leaves the change written
// against a commit the target no longer stands at, and the promotion below
// refuses that as drift exactly as it refuses a target another run moved: the
// run replays onto where the target went, runs its checks again, and earns a
// fresh independent verdict. That is what a human push to the target during a
// run costs, and it is a cost rather than a wedge.
//
// A remote that moved somewhere the local branch cannot be brought onto is the
// divergence only a person can settle, and it stops the run here — with both
// branch positions named and the worktree and branch preserved — rather than
// after an item has been closed against it.
func (a *activeRun) settleRemoteTarget(ctx context.Context) error {
	if !a.publishing {
		return nil
	}
	// The question asked before the promotion is the one publishIntegration asks
	// after it, about the target branch as it stands: the remote must be at or
	// behind it, or carry exactly its content above it. The commit this run was
	// written against is both sides of it, because a local target that has since
	// moved away from that commit is drift the promotion itself refuses.
	standing := gitworktree.Integration{
		TargetBranch:         a.worktree.TargetBranch,
		TargetCommit:         a.worktree.BaseCommit,
		PreviousTargetCommit: a.worktree.BaseCommit,
	}
	err := a.pipeline.Worktrees.VerifyRemoteTarget(ctx, standing)
	if err == nil {
		return nil
	}
	if !errors.Is(err, gitworktree.ErrRemoteTargetDrift) {
		return fmt.Errorf("check the remote target branch before promoting: %w", err)
	}
	catchup, catchupErr := a.pipeline.Worktrees.CatchUpTarget(ctx, a.worktree.TargetBranch)
	if catchupErr != nil {
		return fmt.Errorf("bring %s onto what %s has before promoting: %w",
			a.worktree.TargetBranch, a.pipeline.Config.Execution.Remote, catchupErr)
	}
	// A remote with no such branch is not a divergence but a repository whose
	// target has never been published, and the publication that follows the
	// promotion is what puts it there. It reaches here because an absent remote
	// branch is drift to the check above, and it must not stop a first publication.
	if catchup.RemoteCommit == "" {
		return nil
	}
	if catchup.Held != "" {
		return a.blockOnDivergedTarget(catchup)
	}
	a.outcome.Catchup = &catchup
	return nil
}

// mergeMethod is how the harness asks the forge to merge, and it is a constant
// rather than a setting because the choice decides what the remote history is.
// A merge commit is the only method that puts the promoted commit itself on the
// remote target: a squash replaces it with a commit nobody reviewed, and a
// rebase rewrites it — GitHub updates committer information and mints new SHAs
// even for a request that sits directly on its base — leaving the remote
// carrying a copy of the work that the authoritative branch does not have and
// can never fast-forward onto.
//
// The price is that the two branches do not end at the same commit: the forge
// adds its merge commit above the promotion, and the local target does not
// carry it. That is the relationship the harness maintains and checks — the
// promoted commit is on the remote target, and the remote target carries
// exactly its content — rather than an equality no forge merge can produce.
const mergeMethod = publish.MergeCommit

// publishIntegration merges the run's pull request, which the approving verdict
// has just authorized. The harness asks the forge to merge and treats its answer
// as the outcome, rather than pushing the integrated commit at the target
// branch: a repository that requires a pull request before anything reaches its
// target — the ordinary reason to open pull requests at all — refuses that push,
// and the pull request would then be closed as a side effect of its commits
// appearing rather than merged deliberately.
//
// The merge is asked for as of when the requirements are met rather than as of
// now, so a protected branch's required checks are waited for by the forge
// instead of being demanded seconds after the reviewer approved. A queued merge
// therefore ends this step: the request is recorded as queued, the run finishes,
// and everything the merge itself would have settled — the remote target it
// produced, the branch it consumed — is settled by reconciliation once the forge
// has actually merged.
//
// The local target branch stays the authoritative one. It has already moved, so
// everything here checks that the promotion is what reaches the remote: the
// published branch must carry the commit that was integrated, the remote target
// must still hold exactly the content that commit was written against, and
// after the merge it must contain the promoted commit and carry its content.
// The forge's merge commit sits above that and stays on the remote; the local
// branch is never rewritten to take it on.
//
// Almost nothing here can fail the run. The work is integrated and the
// authoritative branch already moved, so a publication that did not finish is an
// outstanding fact for an operator, in the same way an outstanding cleanup is.
//
// The one exception is the remote target having moved after settleRemoteTarget
// looked at it and before this asks the forge — the window a check-then-act
// leaves open, which the promotion lease closes against this machine and against
// nothing else. That is not an unfinished publication but a divergence: the
// local target carries a promotion the remote does not, and no fast-forward
// reconciles the two. Recording it as outstanding and finishing would close the
// item as integrated against a state nobody owns, so it stops the run instead —
// which is still before the item closes, because finish runs after this.
func (a *activeRun) publishIntegration(ctx context.Context) error {
	if !a.publishing || a.outcome.PullRequest == nil || a.outcome.Integration == nil {
		return nil
	}
	integration := *a.outcome.Integration
	published := *a.outcome.PullRequest
	// What the forge merges is the pull request's head, so a promotion that
	// integrated some other commit must not be merged: the remote would receive a
	// change the authoritative branch does not have. It happens when the worktree
	// was dirty at integration time — something wrote to it after the last
	// attempt was published — and it is reported rather than republished, because
	// the checks and the review ran against what was published.
	if published.HeadCommit != integration.SourceCommit {
		a.recordPublishFailure(fmt.Errorf("pull request %d carries %s, but the promotion integrated %s; the published branch is not what would merge",
			published.Number, published.HeadCommit, integration.SourceCommit))
		return nil
	}
	// A forge asked to merge into a branch that moved would reconcile that
	// movement itself. The drift check the promotion made locally is therefore
	// made again here, against the remote, immediately before the merge.
	if err := a.pipeline.Worktrees.VerifyRemoteTarget(ctx, integration); err != nil {
		cause := fmt.Errorf("check the remote target branch before merging: %w", err)
		a.recordPublishFailure(cause)
		if errors.Is(err, gitworktree.ErrRemoteTargetDrift) {
			return a.settlePromotedDivergence(ctx, integration, cause)
		}
		return nil
	}
	result, err := a.pipeline.Publisher.Merge(ctx, publish.MergeRequest{
		Number:     published.Number,
		HeadCommit: published.HeadCommit,
		Method:     mergeMethod,
	})
	if err != nil {
		a.recordPublishFailure(err)
		return nil
	}
	published.MergeMethod = string(mergeMethod)
	// A queued merge is the ordinary answer from a protected branch: the forge
	// performs it once the required checks pass, minutes after this run is over.
	// The run records it as queued and finishes rather than waiting for a
	// confirmation that cannot arrive while it watches — and leaves the published
	// branch on the remote, because that branch is what the forge still has to
	// merge. Reconciliation is what settles the queue afterwards.
	if result.Queued {
		published.MergeQueued = true
		a.state.PullRequest = &published
		a.outcome.PullRequest = &published
		a.state.UpdatedAt = a.pipeline.clock().Now()
		if err := a.pipeline.Store.Save(a.state); err != nil {
			a.recordPublishFailure(fmt.Errorf("record the queued merge: %w", err))
		}
		return nil
	}
	merged, err := a.awaitMerge(ctx)
	if err != nil {
		a.recordPublishFailure(fmt.Errorf("confirm the pull request merged: %w", err))
		return nil
	}
	published.State = merged.State
	published.Merged = merged.Merged
	a.state.PullRequest = &published
	a.outcome.PullRequest = &published
	if !merged.Merged {
		a.recordPublishFailure(fmt.Errorf("pull request %d is still %s after the forge accepted the merge into %s",
			published.Number, strings.ToLower(nonEmpty(merged.State, "in an unreported state")), integration.TargetBranch))
		return nil
	}
	// The forge says it merged; this is what its merge actually did to the
	// branch. The recorded commit is the forge's merge commit, which is the one
	// commit the remote target has that the local one does not.
	remoteTarget, err := a.pipeline.Worktrees.ConfirmRemoteTarget(ctx, integration)
	if err != nil {
		a.recordPublishFailure(fmt.Errorf("confirm the merge reached %s: %w", integration.TargetBranch, err))
		return nil
	}
	published.MergeCommit = remoteTarget
	// The merge left the remote one commit ahead of the local target, so the
	// branch a person reads is now behind the forge by the commit the forge made
	// of this very promotion. Catching it up is the last step of the promotion
	// rather than a chore for afterwards, and it happens here because here is
	// where this run still holds the branch's promotion lease.
	a.catchUpTarget(ctx, integration.TargetBranch)
	// The published branch is debris once its work is on the target, and it is
	// removed on the same evidence the local branch is: the exact commit that was
	// published and merged.
	if err := a.pipeline.Worktrees.DeleteRemoteBranch(ctx, a.worktree, published.HeadCommit); err != nil {
		a.recordPublishFailure(fmt.Errorf("delete the merged remote branch: %w", err))
		return nil
	}
	a.state.UpdatedAt = a.pipeline.clock().Now()
	if err := a.pipeline.Store.Save(a.state); err != nil {
		a.recordPublishFailure(fmt.Errorf("record the merged pull request: %w", err))
	}
	return nil
}

// settlePromotedDivergence decides what a remote target that moved after the
// promotion leaves behind, and it is the residual half of settleRemoteTarget:
// the same movement, arriving in the window a check-then-act cannot close, with
// the local target branch already advanced.
//
// The promotion cannot be taken back, so the only question left is whether the
// two branches can still be brought together. A remote that swept the promotion
// in along the way — a forge merge carrying this commit and somebody else's — is
// caught up onto and leaves an ordinary outstanding publication: the work is on
// both branches and only the merge request did not happen. A remote that has
// gone somewhere the local branch cannot reach is the divergence nothing
// reconciles, and it stops the run so the item is not closed as integrated
// against it.
//
// A remote with no such branch is neither: it is a target this repository has
// never published, and the outstanding publication already says so.
func (a *activeRun) settlePromotedDivergence(ctx context.Context, integration gitworktree.Integration, cause error) error {
	catchup, err := a.pipeline.Worktrees.CatchUpTarget(ctx, integration.TargetBranch)
	if err != nil {
		// Whether the branches can be reconciled is exactly what could not be
		// established, and a promotion whose remote nobody could look at must not
		// close an item either.
		return a.blockOnPromotedDivergence(integration, gitworktree.Catchup{
			TargetBranch: integration.TargetBranch,
			LocalCommit:  integration.TargetCommit,
			Held:         err.Error(),
		}, cause)
	}
	if catchup.RemoteCommit == "" || catchup.Held == "" {
		a.outcome.Catchup = &catchup
		return nil
	}
	return a.blockOnPromotedDivergence(integration, catchup, cause)
}

// mergeConfirmationDelays are the waits between asking the forge whether the
// merge it performed is reported on the pull request itself. A forge's own
// record of a request can lag the merge it just made by a moment, so asking
// once would report a successful publication as outstanding whenever the
// harness happened to be quicker. The waits are few and short: this is a race
// with a remote bookkeeping step, not a state a run should sit on. A merge the
// forge queued rather than performed is never waited for here — it lands long
// after any wait a run could hold, and reconciliation settles it instead.
var mergeConfirmationDelays = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}

// awaitMerge asks the forge whether the pull request it has just merged now
// reports as merged, retrying while it still reports the request open. A
// query that fails is returned immediately: that is the forge being unreachable
// rather than slow to notice, and retrying it would only delay a failure the
// operator has to see.
func (a *activeRun) awaitMerge(ctx context.Context) (publish.PullRequest, error) {
	merged, err := a.pipeline.Publisher.State(ctx, a.worktree.Branch)
	for attempt := 0; err == nil && !merged.Merged && attempt < len(mergeConfirmationDelays); attempt++ {
		if waitErr := a.pipeline.sleep(ctx, mergeConfirmationDelays[attempt]); waitErr != nil {
			// A cancelled or expired run stops waiting, and reports what the forge
			// last said rather than inventing a verdict about it.
			return merged, nil
		}
		merged, err = a.pipeline.Publisher.State(ctx, a.worktree.Branch)
	}
	return merged, err
}

// mergeQueued reports a merge the forge accepted and has not performed. It is
// the one way a run can finish with its promotion made and its publication
// still undecided, which is why it is what defers closing the work item: the
// forge either merges minutes later or drops the request, and only its answer
// says whether the change was integrated anywhere but this repository.
func (a *activeRun) mergeQueued() bool {
	return a.outcome.PullRequest != nil && a.outcome.PullRequest.MergeQueued
}

// catchUpTarget brings the local target branch onto the commit the forge's
// merge left on the remote, and records what happened either way.
//
// Nothing here can fail the run, and unlike an outstanding publication nothing
// here is even recorded durably. A catch-up is idempotent, it belongs to no run
// in particular, and `yoyo reconcile` sweeps every target branch the harness
// knows about — so a catch-up that was held is a fact for whoever reads this run
// rather than a debt the run has to carry.
func (a *activeRun) catchUpTarget(ctx context.Context, targetBranch string) {
	catchup, err := a.pipeline.Worktrees.CatchUpTarget(ctx, targetBranch)
	if err != nil {
		catchup.TargetBranch = targetBranch
		catchup.Held = err.Error()
	}
	a.outcome.Catchup = &catchup
}

// recordPublishFailure records an outstanding publication everywhere it has to
// be visible without recasting a promoted change as a failed one. A failure
// saving the record itself is folded into the reported failure rather than
// hidden: durable state that disagrees with the report is the thing an operator
// cannot diagnose.
func (a *activeRun) recordPublishFailure(cause error) {
	a.outcome.PublishFailure = cause.Error()
	a.state.PublishFailure = a.outcome.PublishFailure
	a.state.UpdatedAt = a.pipeline.clock().Now()
	if err := a.pipeline.Store.Save(a.state); err != nil {
		a.outcome.PublishFailure = errors.Join(cause, fmt.Errorf("record the outstanding publication: %w", err)).Error()
	}
}

// attemptMessage describes one developer attempt in the harness-owned commit
// that publishes it. A published run reaches integration with these commits
// already on its branch, so this is the message the promoted commit carries;
// the review evidence that authorized the promotion is recorded on the work
// item and in durable run state, where it belongs whether or not a run
// published anything.
func attemptMessage(item beads.WorkItem, outcome Outcome) string {
	subject := strings.TrimSpace(fmt.Sprintf("yoyodyne: %s %s", outcome.WorkItemID, singleLine(item.Title, maxCommitSubjectBytes)))
	body := []string{
		"",
		"Run: " + outcome.RunID,
		"Branch: " + outcome.Branch,
		"Base: " + outcome.BaseCommit,
		fmt.Sprintf("Attempt: %d", outcome.RepairAttempts+1),
		"Developer session: " + outcome.ProviderSessionID,
	}
	return subject + "\n" + strings.Join(body, "\n") + "\n"
}

func pullRequestTitle(item beads.WorkItem, outcome Outcome) string {
	return strings.TrimSpace(fmt.Sprintf("%s %s", outcome.WorkItemID, singleLine(item.Title, maxCommitSubjectBytes)))
}

// pullRequestBody says what a reader of the pull request needs and nothing an
// agent wrote. The description is harness evidence — which run produced this,
// what it was written against, and what still has to happen before it merges —
// because a pull request opened by the harness must not become a channel for
// model output nobody reviewed.
func pullRequestBody(item beads.WorkItem, outcome Outcome, base string) string {
	lines := []string{
		fmt.Sprintf("Opened by Yoyodyne for work item `%s`: %s", outcome.WorkItemID, singleLine(item.Title, maxCommitSubjectBytes)),
		"",
		"- Run: `" + outcome.RunID + "`",
		"- Branch: `" + outcome.Branch + "` into `" + base + "`",
		"- Base commit: `" + outcome.BaseCommit + "`",
		"",
		fmt.Sprintf("The harness pushed this branch as part of the developer phase, and asks the forge to merge this request with the `%s` method once the configured checks pass and an independent reviewer approves the change. That request is queued rather than immediate: the forge merges it when this pull request's own requirements are met, so a base branch with required checks is waited for rather than overridden. The reviewing agent has no tools and cannot merge anything; the harness makes the merge request itself, after fast-forwarding the local target branch onto this branch's head. The method is deliberate: a merge commit keeps the reviewed commit itself on the base, which a squash or a rebase would replace with a rewritten copy.", mergeMethod),
	}
	return strings.Join(lines, "\n")
}

// renderPublishNotes carries the publication into the tracker, so an operator
// reading a work item can find the pull request without reconstructing it.
func renderPublishNotes(outcome Outcome) []string {
	var lines []string
	if outcome.PullRequest != nil {
		lines = append(lines,
			fmt.Sprintf("Pull request: #%d %s", outcome.PullRequest.Number, outcome.PullRequest.URL),
			"Pull request merged: "+strconv.FormatBool(outcome.PullRequest.Merged),
		)
		// A queued merge is the ordinary outcome on a protected branch, and it is
		// not the same fact as an unmerged one: the forge has accepted it and will
		// perform it once the required checks pass. A reader of the item has to be
		// able to tell the two apart without going to the forge — and has to be
		// told why this item is not closed, because a queued merge is the one
		// integrated outcome whose closure waits for somebody else's answer.
		if outcome.PullRequest.MergeQueued {
			lines = append(lines,
				"Merge queued: the forge merges this request once the base branch's requirements are met; `yoyo reconcile` settles the run when it does.",
				"This item stays open until then: it is closed when the forge's merge is confirmed, and handed back to you with a blocker if the forge drops it.")
		}
		// The method decides what the remote history looks like, and the merge
		// commit is the one commit the remote target has that the authoritative
		// local branch does not. A reader of the item can tell what the remote
		// history became without going to the forge for it.
		if outcome.PullRequest.MergeMethod != "" {
			lines = append(lines, "Merge method: "+outcome.PullRequest.MergeMethod)
		}
		if outcome.PullRequest.MergeCommit != "" {
			lines = append(lines, fmt.Sprintf("Remote target commit: %s (the forge's merge commit above the promoted commit)", outcome.PullRequest.MergeCommit))
		}
	}
	if outcome.PublishSkipped != "" {
		lines = append(lines, "Publishing skipped: "+outcome.PublishSkipped)
	}
	if outcome.PublishFailure != "" {
		lines = append(lines,
			"Publication outstanding: "+outcome.PublishFailure,
			"The change is integrated into the local target branch, which is the authoritative one; only its publication is unfinished.",
		)
	}
	return append(lines, renderCatchupNotes(outcome.Catchup)...)
}

// renderCatchupNotes says where the local target branch was left relative to
// the forge. A branch that was already there is not reported at all: it is the
// ordinary state and a note for it would say nothing.
func renderCatchupNotes(catchup *gitworktree.Catchup) []string {
	switch {
	case catchup == nil:
		return nil
	case catchup.Held != "":
		return []string{
			fmt.Sprintf("Local %s was left at %s: %s", catchup.TargetBranch, nonEmpty(catchup.LocalCommit, "an unresolved commit"), catchup.Held),
			"`yoyo reconcile` catches it up on its next sweep.",
		}
	case catchup.Advanced:
		lines := []string{fmt.Sprintf("Local %s caught up to %s, the commit the forge's merge left on the remote", catchup.TargetBranch, catchup.RemoteCommit)}
		if len(catchup.Discarded) > 0 {
			lines = append(lines, "Discarded export churn to let it through: "+strings.Join(catchup.Discarded, ", "))
		}
		return lines
	default:
		return nil
	}
}
