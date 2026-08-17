package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"yoyodyne/internal/execution"
	"yoyodyne/internal/gitworktree"
	"yoyodyne/internal/publish"
	"yoyodyne/internal/runstate"
)

// ReconcileWorktrees is the repository access reconciliation needs: reading
// what a run's artifacts actually look like now, finishing the removal an
// integrated run already earned, and — for a merge the forge performed after
// the run that asked for it had finished — reading what that merge left on the
// remote target and removing the branch it consumed. It is deliberately
// narrower than the manager the pipeline uses, because reconciliation must
// never create a worktree or promote a change.
type ReconcileWorktrees interface {
	Observe(ctx context.Context, worktree gitworktree.Worktree) (gitworktree.Observation, error)
	CleanupIntegrated(ctx context.Context, request gitworktree.CleanupRequest) (gitworktree.Cleanup, error)
	ConfirmRemoteTarget(ctx context.Context, integration gitworktree.Integration) (string, error)
	DeleteRemoteBranch(ctx context.Context, worktree gitworktree.Worktree, commit string) error
}

// ReconcilePullRequests is the forge access reconciliation needs: what the
// forge now says about a pull request whose merge it queued. It can only ask,
// never merge, and that is the point — a queued merge the forge dropped means a
// requirement went unmet, and satisfying it is a person's work rather than
// something a sweep should force.
type ReconcilePullRequests interface {
	State(ctx context.Context, head string) (publish.PullRequest, error)
}

// ReconcileStore is the durable run state reconciliation reads and settles.
// AdoptRun is what keeps a reconciled run singular: a run a live process still
// holds is left to that process rather than decided about from outside.
type ReconcileStore interface {
	Outstanding() ([]runstate.State, error)
	AdoptRun(ctx context.Context, runID string) (runstate.State, *runstate.Lease, error)
	Save(state runstate.State) error
}

// Reconciler settles the runs an interrupted process left behind. It compares
// durable run state against what the repository and the work tracker actually
// show, and then either finishes the run's own remaining step or records a
// durable blocker naming what a person has to decide.
//
// It has no backend and never invokes a provider. A lost process handle says
// nothing about what a developer did, so recovering from one is a question
// about recorded evidence and observable artifacts — never a reason to start a
// second developer for an item. A run the pipeline can still continue on its
// own is therefore left exactly as it is rather than resumed from here.
type Reconciler struct {
	Tracker   WorkTracker
	Worktrees ReconcileWorktrees
	Store     ReconcileStore
	// Publisher answers what became of a merge the forge queued. It is required
	// only to settle a run that has one, which is a run a publishing project
	// produced; a purely local project never records one.
	Publisher ReconcilePullRequests
	Clock     execution.Clock
}

// ReconcileAction names what reconciliation did with one run.
type ReconcileAction string

const (
	// ActionHeld reports a run a live process owns, which was left untouched.
	ActionHeld ReconcileAction = "held"
	// ActionResumable reports a run whose own pipeline can continue it from
	// durable state, so it was left exactly as the interrupted process left it.
	ActionResumable ReconcileAction = "resumable"
	// ActionCompleted reports a run whose integrated work was carried to its
	// terminal state: the item closed and the run's artifacts removed.
	ActionCompleted ReconcileAction = "completed"
	// ActionBlocked reports a run nothing could finish, whose work item now
	// carries a durable blocker.
	ActionBlocked ReconcileAction = "blocked"
	// ActionFailed reports a run recorded terminal without a blocker, because
	// it left nothing behind for anyone to act on.
	ActionFailed ReconcileAction = "failed"
	// ActionUnsettled reports a run reconciliation could not decide. It stays
	// outstanding, so the next sweep takes it up again.
	ActionUnsettled ReconcileAction = "unsettled"
	// ActionQueued reports a run whose merge the forge has accepted and not yet
	// performed. Nothing about it can be decided until the forge merges the
	// request or drops the queued merge, so the run is left outstanding and the
	// next sweep asks again.
	ActionQueued ReconcileAction = "queued"
)

// Reconciliation is what happened to one run. Failure records that
// reconciliation itself could not finish, which leaves the run outstanding for
// the next attempt rather than silently settled.
type Reconciliation struct {
	RunID           string                `json:"run_id"`
	WorkItemID      string                `json:"work_item_id"`
	Action          ReconcileAction       `json:"action"`
	Status          runstate.Status       `json:"status,omitempty"`
	Phase           runstate.Phase        `json:"phase,omitempty"`
	Detail          string                `json:"detail,omitempty"`
	Integration     *runstate.Integration `json:"integration,omitempty"`
	WorktreeRemoved bool                  `json:"worktree_removed"`
	BranchRemoved   bool                  `json:"branch_removed"`
	CleanupFailure  string                `json:"cleanup_failure,omitempty"`
	Failure         string                `json:"failure,omitempty"`
}

// Reconcile settles every run that still owes a step. One run that cannot be
// settled is reported as such and never stops the sweep: an unreconcilable run
// must not hide the others from the operator reading the report.
func (r Reconciler) Reconcile(ctx context.Context) ([]Reconciliation, error) {
	if err := r.validate(); err != nil {
		return nil, err
	}
	outstanding, err := r.Store.Outstanding()
	if err != nil {
		return nil, fmt.Errorf("discover outstanding runs: %w", err)
	}
	results := make([]Reconciliation, 0, len(outstanding))
	for _, recorded := range outstanding {
		results = append(results, r.reconcileRun(ctx, recorded))
	}
	return results, nil
}

// reconcileRun takes the run's lease and settles what it finds under it. The
// state is re-read by AdoptRun, so nothing is decided from the listing snapshot
// that another process may have moved on from in the meantime.
func (r Reconciler) reconcileRun(ctx context.Context, recorded runstate.State) Reconciliation {
	state, lease, err := r.Store.AdoptRun(ctx, recorded.RunID)
	switch {
	case errors.Is(err, runstate.ErrRunHeld):
		result := reconciliationOf(recorded, ActionHeld)
		result.Detail = "a live process holds this run"
		return result
	case err != nil:
		result := reconciliationOf(recorded, ActionUnsettled)
		result.Failure = fmt.Errorf("adopt run %s: %w", recorded.RunID, err).Error()
		return result
	}
	defer lease.Release()

	result, err := r.settle(ctx, state)
	if err != nil {
		// A step that failed partway is never described as settled, however
		// much of it succeeded. What it did achieve is still reported, because
		// the next sweep and an operator both act on that.
		result.Failure = err.Error()
		result.Action = ActionUnsettled
	}
	return result
}

// settle decides one run from its durable state and what the repository shows.
func (r Reconciler) settle(ctx context.Context, state runstate.State) (Reconciliation, error) {
	// A run its own pipeline can still continue is left alone. Ending it here
	// would discard a change that can still be finished, and finishing it here
	// would mean starting the developer reconciliation must never start.
	if resumableRepair(state) {
		result := reconciliationOf(state, ActionResumable)
		result.Detail = fmt.Sprintf("the repair loop can continue from durable state at attempt %d", state.RepairAttempts)
		return result, nil
	}
	// A run waiting out an exhausted provider usage limit is not an interrupted
	// run at all: it recorded a deadline and is owed the attempt it was refused.
	// Settling it here would throw away a claimed item and a preserved worktree
	// over a wait that has not finished yet.
	if pausedForUsageLimit(state) {
		result := reconciliationOf(state, ActionResumable)
		result.Detail = fmt.Sprintf("the run is paused for an exhausted %s usage limit and can continue once it resets at %s",
			nonEmpty(state.UsageLimitKind, "provider"), state.UsageLimitResetsAt.UTC().Format(time.RFC3339))
		return result, nil
	}
	// A run whose provider the harness stopped on time is not an interrupted run
	// either: it recorded what stopped it and is owed the rest of the attempt it
	// was making, in the worktree and session that attempt already established.
	if stoppedProviderIsResumable(state) {
		result := reconciliationOf(state, ActionResumable)
		result.Detail = fmt.Sprintf("the run's provider was stopped because %s and it can continue from durable state",
			describeProviderStop(state.ProviderStop))
		return result, nil
	}
	observation := gitworktree.Observation{}
	if state.WorktreePath != "" {
		var err error
		observation, err = r.Worktrees.Observe(ctx, worktreeOf(state))
		if err != nil {
			return reconciliationOf(state, ActionUnsettled), fmt.Errorf("observe run %s artifacts: %w", state.RunID, err)
		}
	}
	// Recorded integration means the work is already promoted, whatever else
	// was interrupted afterwards. It is still only a claim a process wrote down,
	// and reconciliation exists for state a process that died wrote, so it is
	// reconciled against what the repository shows rather than trusted.
	if state.Integration != nil {
		if disagreement := contradictedIntegration(state, observation); disagreement != "" {
			return r.blockContradictedIntegration(ctx, state, observation, disagreement)
		}
		// A run whose merge the forge queued is not an interrupted run: it
		// finished, and what it still owes is the forge's answer about a merge
		// that lands minutes after the run was over. Asking for that answer is
		// the whole of reconciliation's part in it.
		if queuedMerge(state) {
			return r.settleQueuedMerge(ctx, state)
		}
		return r.completeIntegrated(ctx, state, false)
	}
	// An interruption inside the integration step is the one boundary durable
	// state cannot describe: the promotion either landed or it did not, and
	// only the repository knows which. Containment of the branch's commit in
	// the recorded target is that answer.
	if state.Phase == runstate.PhaseIntegrating && observation.BranchIntegrated {
		return r.recoverIntegration(ctx, state, observation)
	}
	return r.abandon(ctx, state, observation)
}

// queuedMerge reports a run waiting on a merge the forge accepted and has not
// performed yet. It is the one thing a finished run can still owe, and it is
// only ever owed by a run that recorded the promotion the merge carries.
func queuedMerge(state runstate.State) bool {
	return state.Integration != nil && state.PullRequest != nil && state.PullRequest.MergeQueued
}

// contradictedIntegration reports what the repository says that a recorded
// integration does not, and nothing at all when the two agree or when the
// repository cannot answer. A record is only ever contradicted on positive
// evidence: the artifacts of a run that got far enough are legitimately gone,
// and their absence must never read as a promotion that never happened.
func contradictedIntegration(state runstate.State, observation gitworktree.Observation) string {
	integration := *state.Integration
	// Nothing was observed of the target, so there is nothing to reconcile the
	// record against.
	if state.WorktreePath == "" || state.TargetBranch == "" {
		return ""
	}
	// Removing either artifact required proving this exact commit had reached
	// this exact target, so a run that got that far is corroborated by a proof
	// the harness already made rather than by the target as it stands now.
	if state.WorktreeRemoved || state.BranchRemoved {
		return ""
	}
	// A merge the forge performed carried the same promotion onto the remote
	// target, which is corroboration from outside this record.
	if state.PullRequest != nil && state.PullRequest.Merged {
		return ""
	}
	if !observation.TargetExists {
		return fmt.Sprintf("the run recorded commit %s as integrated into %s, but that branch does not exist",
			integration.SourceCommit, integration.TargetBranch)
	}
	// Integration only ever fast-forwards the target onto the promoted commit,
	// so a target still standing where the promotion left it carries it.
	if observation.TargetCommit == integration.TargetCommit {
		return ""
	}
	// Past that, only the branch that carried the commit answers containment,
	// and only while it still exists and still points at the recorded commit.
	// The base commit is in the target by construction and proves nothing.
	answered := observation.BranchExists &&
		observation.BranchCommit == integration.SourceCommit &&
		observation.BranchCommit != state.BaseCommit
	if !answered || observation.BranchIntegrated {
		return ""
	}
	return fmt.Sprintf("the run recorded commit %s as integrated into %s, but %s does not contain it",
		integration.SourceCommit, integration.TargetBranch, integration.TargetBranch)
}

// blockContradictedIntegration hands a promotion the repository does not carry
// to a person rather than completing the run on it. The record itself is
// cleared, because a run that keeps it owes a cleanup that can never prove
// itself and would be swept up again on every pass; the commit and the target
// it named survive in the blocker on the item and in the run's recorded failure.
func (r Reconciler) blockContradictedIntegration(ctx context.Context, state runstate.State, observation gitworktree.Observation, reason string) (Reconciliation, error) {
	itemStatus, err := r.itemStatus(ctx, state.WorkItemID)
	if err != nil {
		return reconciliationOf(state, ActionBlocked), err
	}
	state.Integration = nil
	return r.blockRun(ctx, state, itemStatus, observation, reason)
}

// settleQueuedMerge asks the forge what became of a merge it queued, and
// settles the run on the answer. There are three answers, and the third is the
// one worth stating plainly:
//
//   - The forge merged. The publication finishes exactly as it would have
//     inside the run: the remote target is confirmed to carry the promotion, the
//     merge commit the forge made of it is recorded, and the branch the merge
//     consumed is deleted.
//   - The forge is still holding the merge. Nothing is decided, the run stays
//     outstanding, and a later sweep asks again.
//   - The forge dropped it: the request is closed, or open with no merge queued
//     for it any more. Something the base branch requires went unmet, and the
//     harness does not merge past a requirement — not with administrator
//     privileges, not by asking again. The publication is recorded as
//     outstanding and named on the work item, for a person. The change itself is
//     not at risk: the local target branch it was integrated into is the
//     authoritative one and moved before any of this.
func (r Reconciler) settleQueuedMerge(ctx context.Context, state runstate.State) (Reconciliation, error) {
	published := *state.PullRequest
	if r.Publisher == nil {
		return reconciliationOf(state, ActionUnsettled), fmt.Errorf(
			"run %s is waiting on the queued merge of pull request %d, and reconciliation has no forge access to ask about it",
			state.RunID, published.Number)
	}
	observed, err := r.Publisher.State(ctx, published.Branch)
	if err != nil {
		return reconciliationOf(state, ActionUnsettled), fmt.Errorf("ask the forge about the queued merge for run %s: %w", state.RunID, err)
	}
	if !observed.Merged && observed.AutoMerge {
		result := reconciliationOf(state, ActionQueued)
		result.Detail = fmt.Sprintf("the forge still has the merge of pull request %d into %s queued",
			published.Number, state.Integration.TargetBranch)
		return result, nil
	}
	published.State = observed.State
	published.Merged = observed.Merged
	published.MergeQueued = false
	state.PullRequest = &published

	var detail string
	switch {
	case observed.Merged:
		detail = fmt.Sprintf("the forge merged pull request %d into %s", published.Number, state.Integration.TargetBranch)
		if failure := r.finishQueuedPublication(ctx, state, &published); failure != nil {
			state.PublishFailure = failure.Error()
			detail = failure.Error()
		}
	default:
		state.PublishFailure = fmt.Sprintf("the forge dropped the queued merge of pull request %d: it is %s and has no merge queued for it. A requirement of %s went unmet, and the harness does not merge past one, so the pull request needs a person",
			published.Number, strings.ToLower(nonEmpty(observed.State, "in an unreported state")), state.Integration.TargetBranch)
		detail = state.PublishFailure
	}
	// The item was closed by the run that integrated the change, so this note is
	// the only place an operator learns how the publication of it ended. It is
	// written before the run is settled: a sweep that stopped in between leaves
	// the merge still queued durably and takes it up again, which repeats a note
	// rather than losing one.
	if _, err := r.Tracker.RecordOutcome(ctx, state.WorkItemID, renderQueuedMergeNotes(state, detail)); err != nil {
		return reconciliationOf(state, ActionUnsettled), fmt.Errorf("record the settled merge for run %s: %w", state.RunID, err)
	}
	result, err := r.completeIntegrated(ctx, state, false)
	result.Detail = detail
	return result, err
}

// finishQueuedPublication does what the run would have done had the merge
// happened while it watched: it confirms that the promotion is what reached the
// remote target, records the merge commit the forge made of it, and deletes the
// branch the merge consumed. A failure here is an outstanding publication rather
// than an unsettled run — the merge already happened, and asking again would not
// change what it produced.
func (r Reconciler) finishQueuedPublication(ctx context.Context, state runstate.State, published *runstate.PullRequest) error {
	integration := gitworktree.Integration{
		Branch:               state.Branch,
		TargetBranch:         state.Integration.TargetBranch,
		SourceCommit:         state.Integration.SourceCommit,
		TargetCommit:         state.Integration.TargetCommit,
		PreviousTargetCommit: state.Integration.PreviousTargetCommit,
	}
	remoteTarget, err := r.Worktrees.ConfirmRemoteTarget(ctx, integration)
	if err != nil {
		return fmt.Errorf("confirm the queued merge reached %s: %w", integration.TargetBranch, err)
	}
	published.MergeCommit = remoteTarget
	// The published branch is debris once its work is on the target, and it is
	// removed on the same evidence the run would have used: the exact commit that
	// was published and merged.
	if err := r.Worktrees.DeleteRemoteBranch(ctx, worktreeOf(state), published.HeadCommit); err != nil {
		return fmt.Errorf("delete the merged remote branch: %w", err)
	}
	return nil
}

// recoverIntegration records the promotion an interrupted process made but
// never wrote down, and then finishes the run from there. The evidence is
// reconstructed from what integration guarantees rather than from the target as
// it stands now: integration refuses a target that drifted from the recorded
// base, and it only ever fast-forwards, so the target moved from exactly the
// base commit to exactly the branch's commit.
func (r Reconciler) recoverIntegration(ctx context.Context, state runstate.State, observation gitworktree.Observation) (Reconciliation, error) {
	// The promotion is only this run's to claim if this run's review approved
	// it. Without that the commit in the target needs a person, not a closed
	// item.
	if state.ReviewDecision != runstate.ReviewApprove {
		reason := fmt.Sprintf("commit %s from this run's branch is contained in %s, but the run recorded no approving review for it",
			observation.BranchCommit, state.TargetBranch)
		itemStatus, err := r.itemStatus(ctx, state.WorkItemID)
		if err != nil {
			return reconciliationOf(state, ActionBlocked), err
		}
		return r.blockRun(ctx, state, itemStatus, observation, reason)
	}
	state.Integration = &runstate.Integration{
		TargetBranch:         state.TargetBranch,
		SourceCommit:         observation.BranchCommit,
		TargetCommit:         observation.BranchCommit,
		PreviousTargetCommit: state.BaseCommit,
	}
	// The durable invariants are checked before the tracker is touched, not
	// after. Closing an item and only then discovering that the evidence for it
	// cannot be recorded would leave a closed item behind a run that stays
	// outstanding forever.
	if err := state.Validate(); err != nil {
		reason := fmt.Sprintf("commit %s from this run's branch is contained in %s, but the run's evidence does not support recording that promotion: %v",
			observation.BranchCommit, state.TargetBranch, err)
		itemStatus, statusErr := r.itemStatus(ctx, state.WorkItemID)
		if statusErr != nil {
			return reconciliationOf(state, ActionBlocked), statusErr
		}
		state.Integration = nil
		return r.blockRun(ctx, state, itemStatus, observation, reason)
	}
	return r.completeIntegrated(ctx, state, true)
}

// completeIntegrated carries a run whose work is already promoted to its
// terminal state. It keeps the pipeline's ordering: the outcome and the closed
// item first, then the durable terminal record, and only then the removal of
// artifacts. That order is what stops a closed item from ever sitting behind a
// run that still says something is in flight.
func (r Reconciler) completeIntegrated(ctx context.Context, state runstate.State, recovered bool) (Reconciliation, error) {
	itemStatus, err := r.itemStatus(ctx, state.WorkItemID)
	if err != nil {
		return reconciliationOf(state, ActionCompleted), err
	}
	// An item the interrupted process already closed is left alone, so
	// reconciling twice records one outcome and one closure.
	if itemStatus != "closed" {
		if _, err := r.Tracker.RecordOutcome(ctx, state.WorkItemID, renderReconciledIntegrationNotes(state, recovered)); err != nil {
			return reconciliationOf(state, ActionCompleted), fmt.Errorf("record reconciled outcome for run %s: %w", state.RunID, err)
		}
		if _, err := r.Tracker.Complete(ctx, state.WorkItemID, reconciledCompletionReason(state)); err != nil {
			return reconciliationOf(state, ActionCompleted), fmt.Errorf("close integrated work item for run %s: %w", state.RunID, err)
		}
	}
	if !state.Status.Terminal() {
		completedAt := r.clock().Now()
		state.Status = runstate.StatusSucceeded
		state.CompletedAt = &completedAt
	}
	state.Phase = runstate.PhaseCleaningUp
	state.UpdatedAt = r.clock().Now()
	if err := r.Store.Save(state); err != nil {
		return reconciliationOf(state, ActionCompleted), fmt.Errorf("save reconciled run state for %s: %w", state.RunID, err)
	}

	cleanup, cleanupErr := r.Worktrees.CleanupIntegrated(ctx, gitworktree.CleanupRequest{
		Worktree:     worktreeOf(state),
		TargetBranch: state.Integration.TargetBranch,
		SourceCommit: state.Integration.SourceCommit,
	})
	state.WorktreeRemoved = cleanup.WorktreeRemoved
	state.BranchRemoved = cleanup.BranchRemoved
	if cleanupErr != nil {
		// The run stays outstanding so the next sweep tries again. Repeating
		// cleanup is safe: it refuses anything that is not this run's proven
		// artifacts and does nothing at all over artifacts already gone.
		cleanupErr = fmt.Errorf("clean up reconciled run %s: %w", state.RunID, cleanupErr)
		state.CleanupFailure = cleanupErr.Error()
		state.UpdatedAt = r.clock().Now()
		if saveErr := r.Store.Save(state); saveErr != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("record outstanding cleanup for run %s: %w", state.RunID, saveErr))
		}
		return reconciliationOf(state, ActionCompleted), cleanupErr
	}
	// Nothing is left to remove, so the run is complete and the outstanding
	// cleanup marker the interrupted process left is no longer true.
	state.CleanupFailure = ""
	state.Phase = runstate.PhaseComplete
	state.UpdatedAt = r.clock().Now()
	if err := r.Store.Save(state); err != nil {
		return reconciliationOf(state, ActionCompleted), fmt.Errorf("save completed run state for %s: %w", state.RunID, err)
	}
	result := reconciliationOf(state, ActionCompleted)
	result.Detail = "integrated work was closed and its artifacts removed"
	if recovered {
		result.Detail = "integration recovered from the repository, then closed and cleaned up"
	}
	return result, nil
}

// abandon settles a run whose remaining work cannot be finished from durable
// state: an interrupted developer attempt leaves uncommitted work nobody has
// judged, and re-running the developer is exactly what reconciliation must not
// do. The run becomes terminal either way. When the item is still claimed or
// the run's artifacts survive, the item also carries a durable blocker, because
// something is preserved that a person has to replan, reuse, or retire.
func (r Reconciler) abandon(ctx context.Context, state runstate.State, observation gitworktree.Observation) (Reconciliation, error) {
	reason := fmt.Sprintf("the run was interrupted in the %s phase with nothing integrated, and no attempt of the harness can finish it", nonEmpty(string(state.Phase), "unrecorded"))
	itemStatus, err := r.itemStatus(ctx, state.WorkItemID)
	if err != nil {
		return reconciliationOf(state, ActionFailed), err
	}
	if itemStatus == "in_progress" || preservedArtifacts(observation) {
		return r.blockRun(ctx, state, itemStatus, observation, reason)
	}
	if _, err := r.Tracker.RecordOutcome(ctx, state.WorkItemID, renderReconciledFailureNotes(state, observation, reason)); err != nil {
		return reconciliationOf(state, ActionFailed), fmt.Errorf("record reconciled failure for run %s: %w", state.RunID, err)
	}
	settled, err := r.recordTerminalFailure(state, reason)
	result := reconciliationOf(settled, ActionFailed)
	result.Detail = reason
	return result, err
}

// blockRun records the durable blocker a stopped run leaves behind and makes
// the run terminal. The blocker is recorded before the run is closed out, so an
// interruption here leaves the run outstanding and the blocker recorded rather
// than a settled run nobody was told about.
func (r Reconciler) blockRun(ctx context.Context, state runstate.State, itemStatus string, observation gitworktree.Observation, reason string) (Reconciliation, error) {
	notes := renderReconcileBlockerNotes(state, observation, reason)
	var err error
	// An item already blocked keeps the status it has; the reason is still
	// recorded, because this run's evidence is what a replan needs.
	if itemStatus == "blocked" {
		_, err = r.Tracker.RecordOutcome(ctx, state.WorkItemID, notes)
	} else {
		_, err = r.Tracker.Block(ctx, state.WorkItemID, notes)
	}
	if err != nil {
		return reconciliationOf(state, ActionBlocked), fmt.Errorf("record blocker for run %s: %w", state.RunID, err)
	}
	settled, err := r.recordTerminalFailure(state, reason)
	result := reconciliationOf(settled, ActionBlocked)
	result.Detail = reason
	return result, err
}

// recordTerminalFailure makes an unfinishable run durably terminal in the phase
// it stopped in, so the record still says where it got to. A run that is already
// terminal keeps the status and failure it recorded, and is still written back:
// settling can have changed what the state carries, and a change that never
// reaches disk leaves the run outstanding for the next sweep to decide again.
func (r Reconciler) recordTerminalFailure(state runstate.State, reason string) (runstate.State, error) {
	if !state.Status.Terminal() {
		completedAt := r.clock().Now()
		state.Status = runstate.StatusFailed
		state.UpdatedAt = completedAt
		state.CompletedAt = &completedAt
		state.Failure = "reconciled after an interrupted run: " + reason
	}
	if err := r.Store.Save(state); err != nil {
		return state, fmt.Errorf("save reconciled run state for %s: %w", state.RunID, err)
	}
	return state, nil
}

// itemStatus reads the tracker's own view of the item, which is the half of
// reconciliation durable run state cannot supply: a run that died before it
// wrote anything down may still have claimed its item.
func (r Reconciler) itemStatus(ctx context.Context, workItemID string) (string, error) {
	item, err := r.Tracker.Show(ctx, workItemID)
	if err != nil {
		return "", fmt.Errorf("load work item %s: %w", workItemID, err)
	}
	return item.Status, nil
}

// preservedArtifacts reports whether anything this run created is still there.
// A run whose artifacts are already gone leaves nothing to hand to a person.
func preservedArtifacts(observation gitworktree.Observation) bool {
	return observation.WorktreeRegistered || observation.WorktreePresent || observation.BranchExists
}

// worktreeOf rebuilds the worktree identity from what was recorded when it was
// created, never from what the repository looks like now. Every manager call
// revalidates ownership of these fields before acting on them.
func worktreeOf(state runstate.State) gitworktree.Worktree {
	return gitworktree.Worktree{
		RunID:         state.RunID,
		WorkItemID:    state.WorkItemID,
		Path:          state.WorktreePath,
		Branch:        state.Branch,
		BaseCommit:    state.BaseCommit,
		TargetBranch:  state.TargetBranch,
		HarnessCommit: state.HarnessCommit,
	}
}

func reconciliationOf(state runstate.State, action ReconcileAction) Reconciliation {
	return Reconciliation{
		RunID:           state.RunID,
		WorkItemID:      state.WorkItemID,
		Action:          action,
		Status:          state.Status,
		Phase:           state.Phase,
		Integration:     state.Integration,
		WorktreeRemoved: state.WorktreeRemoved,
		BranchRemoved:   state.BranchRemoved,
		CleanupFailure:  state.CleanupFailure,
	}
}

func (r Reconciler) validate() error {
	var problems []error
	if r.Tracker == nil {
		problems = append(problems, errors.New("work tracker is required"))
	}
	if r.Worktrees == nil {
		problems = append(problems, errors.New("worktree observer is required"))
	}
	if r.Store == nil {
		problems = append(problems, errors.New("state store is required"))
	}
	if len(problems) > 0 {
		return errors.Join(problems...)
	}
	return nil
}

func (r Reconciler) clock() execution.Clock {
	if r.Clock == nil {
		return execution.RealClock{}
	}
	return r.Clock
}

// renderReconciledIntegrationNotes explains a closure the run itself never got
// to record. It distinguishes integration the run wrote down from integration
// found in the repository afterwards, because only the second one is a claim
// reconciliation made on the run's behalf.
func renderReconciledIntegrationNotes(state runstate.State, recovered bool) string {
	headline := "Yoyodyne reconciled an interrupted run: the change was already integrated, so the item is being closed and the run's artifacts removed."
	if recovered {
		headline = "Yoyodyne reconciled an interrupted run: its integration commit was found in the target branch even though the run never recorded it, so the item is being closed and the run's artifacts removed."
	}
	lines := []string{
		headline,
		"Run: " + state.RunID,
		"Phase when interrupted: " + string(state.Phase),
		"Branch: " + state.Branch,
		"Integrated into: " + state.Integration.TargetBranch,
		"Integrated commit: " + state.Integration.SourceCommit,
		"Previous target commit: " + state.Integration.PreviousTargetCommit,
	}
	if state.ReviewSessionID != "" {
		lines = append(lines, "Reviewer session: "+state.ReviewSessionID)
	}
	if state.ReviewDecision != "" {
		lines = append(lines, "Review decision: "+state.ReviewDecision)
	}
	return strings.Join(lines, "\n")
}

// renderQueuedMergeNotes tells the work item what became of a merge that was
// still queued when its run finished. The item was closed by that run — the
// change was integrated into the authoritative local branch, which is what
// closed it — so this note is the only place an operator learns whether the
// publication of it completed, and what is left if it did not.
func renderQueuedMergeNotes(state runstate.State, detail string) string {
	lines := []string{
		"Yoyodyne settled the merge this run left queued with the forge.",
		"Outcome: " + detail,
		"Run: " + state.RunID,
		fmt.Sprintf("Pull request: #%d %s", state.PullRequest.Number, state.PullRequest.URL),
		"Pull request merged: " + strconv.FormatBool(state.PullRequest.Merged),
	}
	if state.PullRequest.MergeCommit != "" {
		lines = append(lines, fmt.Sprintf("Remote target commit: %s (the forge's merge commit above the promoted commit)", state.PullRequest.MergeCommit))
	}
	if state.PublishFailure != "" {
		lines = append(lines,
			"Publication outstanding: "+state.PublishFailure,
			"The change is integrated into the local target branch, which is the authoritative one; only its publication is unfinished.")
	}
	return strings.Join(lines, "\n")
}

func reconciledCompletionReason(state runstate.State) string {
	return fmt.Sprintf("Reconciled by Yoyodyne after run %s was interrupted: %s is at %s",
		state.RunID, state.Integration.TargetBranch, state.Integration.TargetCommit)
}

// renderReconcileBlockerNotes hands a stopped run to a person. It names only
// artifacts that were actually observed, so nobody is sent after a worktree or
// a branch that is no longer there.
func renderReconcileBlockerNotes(state runstate.State, observation gitworktree.Observation, reason string) string {
	lines := []string{
		"Yoyodyne stopped this item while reconciling an interrupted run. No developer was restarted for it.",
		"Reason: " + reason,
		"Run: " + state.RunID,
		"Phase when interrupted: " + nonEmpty(string(state.Phase), "unrecorded"),
	}
	if state.RepairAttempts > 0 {
		lines = append(lines, "Repair attempts already spent: "+strconv.Itoa(state.RepairAttempts))
	}
	lines = append(lines, renderObservedArtifacts(state, observation)...)
	if state.CheckFailure != nil {
		lines = append(lines, fmt.Sprintf("Last failing check: %s (exit %d)", state.CheckFailure.Command, state.CheckFailure.ExitCode))
	}
	if state.ReviewSummary != "" {
		lines = append(lines, "Last review summary: "+state.ReviewSummary)
	}
	for _, finding := range state.ReviewFindingDetails {
		location := ""
		if finding.File != "" {
			location = fmt.Sprintf(" (%s:%d)", finding.File, finding.Line)
		}
		lines = append(lines, fmt.Sprintf("Finding [%s]%s: %s", finding.Severity, location, finding.Message))
	}
	return strings.Join(lines, "\n")
}

// renderReconciledFailureNotes records a run that left nothing behind. It is
// deliberately not a blocker: the item is not claimed and no artifact survives,
// so the work is simply available again.
func renderReconciledFailureNotes(state runstate.State, observation gitworktree.Observation, reason string) string {
	lines := []string{
		"Yoyodyne reconciled an interrupted run that left nothing behind. The item is not blocked and remains available.",
		"Reason: " + reason,
		"Run: " + state.RunID,
	}
	return strings.Join(append(lines, renderObservedArtifacts(state, observation)...), "\n")
}

// renderObservedArtifacts reports what the repository actually shows, which is
// what separates a preserved change from a recorded path that no longer exists.
func renderObservedArtifacts(state runstate.State, observation gitworktree.Observation) []string {
	if state.WorktreePath == "" {
		return []string{"No worktree was recorded for this run."}
	}
	var lines []string
	if observation.WorktreePresent {
		lines = append(lines,
			"Preserved worktree: "+state.WorktreePath,
			"Uncommitted changes in the worktree: "+strconv.FormatBool(observation.WorktreeDirty))
	} else {
		lines = append(lines, "Recorded worktree is gone: "+state.WorktreePath)
	}
	if observation.BranchExists {
		lines = append(lines, "Preserved branch: "+state.Branch+" at "+observation.BranchCommit)
	} else {
		lines = append(lines, "Recorded branch is gone: "+state.Branch)
	}
	if state.TargetBranch != "" {
		lines = append(lines, "Integration target: "+state.TargetBranch)
	}
	return lines
}
