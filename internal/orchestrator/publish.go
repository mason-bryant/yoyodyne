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
// branch is the one that moves; the merge of the pull request is the arrival of
// that exact fast-forwarded commit on the remote. There is one commit, one
// promotion, and one answer about where a project's work is.

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

// publishIntegration merges the run's pull request, which the approving verdict
// has just authorized. The merge is the arrival of the integrated commit on the
// remote target branch: the harness pushes exactly the fast-forward it already
// made locally, so the published branch can never carry a commit the
// authoritative local branch does not, and the pull request is merged by the
// same commit the promotion produced rather than by a second, differently
// shaped one.
//
// Nothing here can fail the run. The work is integrated and the local target
// branch — the authoritative one — already moved, so a publication that did not
// finish is an outstanding fact for an operator, in the same way an outstanding
// cleanup is.
func (a *activeRun) publishIntegration(ctx context.Context) {
	if !a.publishing || a.outcome.PullRequest == nil || a.outcome.Integration == nil {
		return
	}
	if err := a.pipeline.Worktrees.PublishIntegration(ctx, a.worktree, *a.outcome.Integration); err != nil {
		a.recordPublishFailure(fmt.Errorf("push the integrated target branch: %w", err))
		return
	}
	merged, err := a.awaitMerge(ctx)
	if err != nil {
		a.recordPublishFailure(fmt.Errorf("confirm the pull request merged: %w", err))
		return
	}
	published := *a.outcome.PullRequest
	published.State = merged.State
	published.Merged = merged.Merged
	a.state.PullRequest = &published
	a.outcome.PullRequest = &published
	if !merged.Merged {
		a.recordPublishFailure(fmt.Errorf("pull request %d is still %s after the integrated commit was pushed to %s",
			published.Number, strings.ToLower(nonEmpty(merged.State, "in an unreported state")), a.outcome.Integration.TargetBranch))
		return
	}
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
// pushed commit merged the pull request. A forge notices its commits reaching
// the base shortly after the push rather than during it, so asking once would
// report a successful publication as outstanding whenever the harness happened
// to be quicker. The waits are few and short: this is a race with a remote
// bookkeeping step, not a state a run should sit on.
var mergeConfirmationDelays = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}

// awaitMerge asks the forge whether the promotion merged the pull request,
// retrying while it still reports the request open. A query that fails is
// returned immediately: that is the forge being unreachable rather than slow to
// notice, and retrying it would only delay a failure the operator has to see.
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
		"The harness pushed this branch as part of the developer phase, and merges it itself once the configured checks pass and an independent reviewer approves the change. The reviewing agent has no tools and cannot merge anything; the merge is a fast-forward the harness performs.",
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
