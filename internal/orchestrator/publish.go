package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"yoyodyne/internal/beads"
	"yoyodyne/internal/domain"
	"yoyodyne/internal/gitworktree"
	"yoyodyne/internal/publish"
	"yoyodyne/internal/runstate"
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
// Nothing here can fail the run. The work is integrated and the authoritative
// branch already moved, so a publication that did not finish is an outstanding
// fact for an operator, in the same way an outstanding cleanup is.
func (a *activeRun) publishIntegration(ctx context.Context) {
	if !a.publishing || a.outcome.PullRequest == nil || a.outcome.Integration == nil {
		return
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
		return
	}
	// A forge asked to merge into a branch that moved would reconcile that
	// movement itself. The drift check the promotion made locally is therefore
	// made again here, against the remote, immediately before the merge.
	if err := a.pipeline.Worktrees.VerifyRemoteTarget(ctx, integration); err != nil {
		a.recordPublishFailure(fmt.Errorf("check the remote target branch before merging: %w", err))
		return
	}
	result, err := a.pipeline.Publisher.Merge(ctx, publish.MergeRequest{
		Number:     published.Number,
		HeadCommit: published.HeadCommit,
		Method:     mergeMethod,
	})
	if err != nil {
		a.recordPublishFailure(err)
		return
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
		return
	}
	merged, err := a.awaitMerge(ctx)
	if err != nil {
		a.recordPublishFailure(fmt.Errorf("confirm the pull request merged: %w", err))
		return
	}
	published.State = merged.State
	published.Merged = merged.Merged
	a.state.PullRequest = &published
	a.outcome.PullRequest = &published
	if !merged.Merged {
		a.recordPublishFailure(fmt.Errorf("pull request %d is still %s after the forge accepted the merge into %s",
			published.Number, strings.ToLower(nonEmpty(merged.State, "in an unreported state")), integration.TargetBranch))
		return
	}
	// The forge says it merged; this is what its merge actually did to the
	// branch. The recorded commit is the forge's merge commit, which is the one
	// commit the remote target has that the local one does not.
	remoteTarget, err := a.pipeline.Worktrees.ConfirmRemoteTarget(ctx, integration)
	if err != nil {
		a.recordPublishFailure(fmt.Errorf("confirm the merge reached %s: %w", integration.TargetBranch, err))
		return
	}
	published.MergeCommit = remoteTarget
	// The published branch is debris once its work is on the target, and it is
	// removed on the same evidence the local branch is: the exact commit that was
	// published and merged.
	if err := a.pipeline.Worktrees.DeleteRemoteBranch(ctx, a.worktree, published.HeadCommit); err != nil {
		a.recordPublishFailure(fmt.Errorf("delete the merged remote branch: %w", err))
		return
	}
	a.state.UpdatedAt = a.pipeline.clock().Now()
	if err := a.pipeline.Store.Save(a.state); err != nil {
		a.recordPublishFailure(fmt.Errorf("record the merged pull request: %w", err))
	}
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
		// able to tell the two apart without going to the forge.
		if outcome.PullRequest.MergeQueued {
			lines = append(lines,
				"Merge queued: the forge merges this request once the base branch's requirements are met; `yoyo reconcile` settles the run when it does.")
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
	return lines
}
