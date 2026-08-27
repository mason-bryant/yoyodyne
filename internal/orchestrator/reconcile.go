package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// ReconcileWorktrees is the repository access reconciliation needs: reading
// what a run's artifacts actually look like now, finishing the removal an
// integrated run already earned, and — for a merge the forge performed after
// the run that asked for it had finished — reading what that merge left on the
// remote target, removing the branch it consumed, and bringing local state onto
// what it produced. It is deliberately narrower than the manager the pipeline
// uses, because reconciliation must never create a worktree and must never
// promote a change: the two writes here that move a ref only ever fast-forward
// a target branch onto a commit that already contains it, or delete a branch
// the target already carries.
type ReconcileWorktrees interface {
	Observe(ctx context.Context, worktree gitworktree.Worktree) (gitworktree.Observation, error)
	CleanupIntegrated(ctx context.Context, request gitworktree.CleanupRequest) (gitworktree.Cleanup, error)
	ConfirmRemoteTarget(ctx context.Context, integration gitworktree.Integration) (string, error)
	DeleteRemoteBranch(ctx context.Context, worktree gitworktree.Worktree, commit string) error
	// The two writes convergence needs, and the only ones here that move a ref.
	// Both are fast-forward-or-nothing and both refuse on the evidence rather
	// than on a record: a target branch is only ever advanced onto a remote
	// commit that already contains it, and a run branch is only ever deleted
	// once the target is proven to carry its work.
	CatchUpTarget(ctx context.Context, targetBranch string) (gitworktree.Catchup, error)
	RemoveMergedBranch(ctx context.Context, branch, targetBranch string) (gitworktree.Removal, error)
	// The two removals that keep the worktree registrations from accumulating
	// without bound, which is what eventually stops a command spawning at all in
	// a machine's next worktree. Neither can lose anything: retiring a preserved
	// checkout records whatever uncommitted work it holds on a run-scoped ref
	// before removing the directory and never touches its branch, and pruning
	// only unregisters checkouts that are no longer on disk.
	RemovePreservedWorktree(ctx context.Context, worktree gitworktree.Worktree, uncommitted gitworktree.UncommittedWork) (gitworktree.WorktreeRemoval, error)
	PruneRegistrations(ctx context.Context) (gitworktree.Prune, error)
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
	// Recorded is every run the harness holds, whatever became of it. Settling a
	// run reads only the outstanding ones; converging local state reads all of
	// them, because the branches and targets a finished run left behind are
	// exactly what it has to sweep.
	Recorded() ([]runstate.State, error)
	// LeasePromotion admits this sweep to move one target branch, waiting its
	// turn behind whatever is promoting into it now. Catching a branch up reads
	// where it is and then moves it, which is the same race a promotion is, so
	// it queues in the same place rather than beside it.
	LeasePromotion(ctx context.Context, targetBranch string) (*runstate.Lease, error)
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
	// Docket is where a run this sweep stops is put in front of the development
	// manager. It is optional: a sweep wired without one settles runs exactly as
	// it would have, and what it settled is still on the work item.
	Docket *Docketer
	Clock  execution.Clock
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
	// DocketProblem names a stoppage this sweep settled and could not put on the
	// triage docket. It is deliberately not a settlement failure: the run is
	// settled and its blocker is on the work item either way, and the blocker is
	// durable on the run itself, so the next docket build finds the same stoppage
	// from the same record. Reporting it as a failed settlement would describe a
	// settled run as outstanding, which no later sweep would correct.
	DocketProblem string `json:"docket_problem,omitempty"`
	// Catchup is the local target branch brought onto the merge commit the forge
	// made, present only on a run whose queued merge this sweep settled. It is
	// reported rather than made durable for the reason the run's own catch-up is:
	// it is idempotent and owned by no run, so a held one is a fact to read
	// rather than a debt to carry.
	Catchup *gitworktree.Catchup `json:"catchup,omitempty"`
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
	// A run waiting out a provider that refused it is not an interrupted run at
	// all: it recorded a deadline and is owed the attempt it was refused.
	// Settling it here would throw away a claimed item and a preserved worktree
	// over a wait that has not finished yet.
	if pausedForUsageLimit(state) {
		result := reconciliationOf(state, ActionResumable)
		result.Detail = fmt.Sprintf("the run is paused for %s and can continue once it asks again, by %s at the latest",
			runstate.DescribePause(state.PauseCause, state.UsageLimitKind), state.UsageLimitResetsAt.UTC().Format(time.RFC3339))
		return result, nil
	}
	// A run held up by an unresolved user directive is not an interrupted run
	// either. It recorded the directive it stopped short for and is owed the rest
	// of the gate once somebody settles it, so settling it here would cancel work
	// the operator only paused — which is the one thing pausing for a directive
	// must never turn into.
	if pausedForDirective(state) {
		result := reconciliationOf(state, ActionResumable)
		result.Detail = fmt.Sprintf("the run is paused for unresolved directive %s and can continue once it is settled: %s",
			state.DirectivePause.DirectiveID, state.DirectivePause.Unresolved)
		return result, nil
	}
	// A run waiting on work its item depends on is not an interrupted run either.
	// It recorded what it waits for and is owed the rest of the gate once that
	// work is closed or unlinked, so settling it here would cancel work somebody
	// only made wait.
	if pausedForDependency(state) {
		result := reconciliationOf(state, ActionResumable)
		result.Detail = fmt.Sprintf("the run is paused because %s waits on unfinished work and can continue once it is closed: %s",
			state.WorkItemID, state.DependencyPause.Summary())
		return result, nil
	}
	// A run parked because the operator paused all harness activity is not an
	// interrupted run either, and it is the one where settling it would be
	// worst: the operator stopped it deliberately and expects to find it where
	// they left it, so ending it here would turn their pause into the cancelled
	// run that pausing exists to avoid.
	if pausedForOperatorHold(state) {
		result := reconciliationOf(state, ActionResumable)
		result.Detail = fmt.Sprintf("the run is parked because the operator paused all harness activity at %s, and can continue once `yoyo resume` lifts it",
			state.OperatorHeldSince.UTC().Format(time.RFC3339))
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
	// A run whose merge the forge queued is not an interrupted run: it finished,
	// and what it still owes is the forge's answer about a merge that lands
	// minutes after the run was over. Asking for that answer is the whole of
	// reconciliation's part in it, and it is asked before the repository is
	// consulted at all: the local promotion such a run recorded is not a claim
	// about anything a later sweep can observe, and a merge nobody has answered
	// for yet must never be settled as a disagreement.
	if queuedMerge(state) {
		return r.settleQueuedMerge(ctx, state)
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
	// target, which is corroboration from outside this record. A merge the forge
	// has not answered for never reaches here at all: settle asks the forge about
	// one before it observes anything.
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
// to a person rather than completing the run on it, and clears the record that
// claimed it: a run that keeps a promotion nothing can prove owes a cleanup that
// can never prove itself, so every later sweep would decide it again. The commit
// and the target it named survive in the blocker on the item.
//
// It writes that record itself rather than through recordTerminalFailure,
// because clearing the promotion has to reach disk even for a run that was
// already terminal, and a run that is already terminal keeps the status it
// recorded for itself.
func (r Reconciler) blockContradictedIntegration(ctx context.Context, state runstate.State, observation gitworktree.Observation, reason string) (Reconciliation, error) {
	itemStatus, err := r.itemStatus(ctx, state.WorkItemID)
	if err != nil {
		return reconciliationOf(state, ActionBlocked), err
	}
	// The blocker reaches the item before the record is disturbed, so an
	// interruption here leaves the run outstanding with the blocker already
	// recorded rather than a settled run nobody was told about.
	notes, err := r.recordBlocker(ctx, state, itemStatus, observation, reason)
	if err != nil {
		return reconciliationOf(state, ActionBlocked), err
	}
	state.Integration = nil
	state.Blocker = runstate.RecordBlocker(notes)
	settled, saveErr := r.saveTerminalFailure(state, reason)
	result := reconciliationOf(settled, ActionBlocked)
	result.Detail = reason
	result.DocketProblem = r.docketStoppedRun(settled)
	return result, saveErr
}

// settleQueuedMerge asks the forge what became of a merge it queued, and
// settles the run on the answer. There are three answers, and the third is the
// one worth stating plainly:
//
//   - The forge merged. The publication finishes exactly as it would have
//     inside the run: the remote target is confirmed to carry the promotion, the
//     merge commit the forge made of it is recorded, the branch the merge
//     consumed is deleted, and the local target branch is caught up onto the
//     merge commit. That last step is done here rather than left to the
//     convergence sweep so that settling a merge is complete on its own: a
//     caller that settles runs without sweeping afterwards must not be the
//     difference between a converged checkout and one silently left behind.
//   - The forge is still holding the merge. Nothing is decided, the run stays
//     outstanding, and a later sweep asks again.
//   - The forge dropped it: the request is closed, or open with no merge queued
//     for it any more. Something the base branch requires went unmet, and the
//     harness does not merge past a requirement — not with administrator
//     privileges, not by asking again. The publication is recorded as
//     outstanding and the item is handed to a person with a durable blocker
//     rather than closed as integrated: the run that promoted the change left
//     that closure to the forge's answer, and this is the answer. The change
//     itself is not at risk — the local target branch it was integrated into is
//     the authoritative one and moved before any of this — so what the blocker
//     asks for is the publication, which is where triage's bounded re-arm of a
//     dropped merge acts. An item some earlier run already closed keeps the
//     closure it has; the outstanding publication is still recorded on it.
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

	if !observed.Merged {
		state.PublishFailure = fmt.Sprintf("the forge dropped the queued merge of pull request %d: it is %s and has no merge queued for it. A requirement of %s went unmet, and the harness does not merge past one, so the pull request needs a person",
			published.Number, strings.ToLower(nonEmpty(observed.State, "in an unreported state")), state.Integration.TargetBranch)
		return r.settleDroppedMerge(ctx, state)
	}
	detail := fmt.Sprintf("the forge merged pull request %d into %s", published.Number, state.Integration.TargetBranch)
	var catchup *gitworktree.Catchup
	if failure := r.finishQueuedPublication(ctx, state, &published); failure != nil {
		state.PublishFailure = failure.Error()
		detail = failure.Error()
	} else {
		// The merge is confirmed on the remote, so the local branch is behind it
		// by the commit the forge made of this run's own promotion. A publication
		// that could not be confirmed deliberately does not reach here: that is
		// the state a person has to look at, and moving the local branch on a
		// merge nothing verified would be deciding it for them.
		settled := r.catchUp(ctx, state.Integration.TargetBranch)
		catchup = &settled
	}
	// The run that integrated the change left the closure to this answer, so this
	// note is where an operator learns how the publication of it ended. It is
	// written before the run is settled: a sweep that stopped in between leaves
	// the merge still queued durably and takes it up again, which repeats a note
	// rather than losing one.
	if _, err := r.Tracker.RecordOutcome(ctx, state.WorkItemID, renderQueuedMergeNotes(state, detail, catchup)); err != nil {
		return reconciliationOf(state, ActionUnsettled), fmt.Errorf("record the settled merge for run %s: %w", state.RunID, err)
	}
	// The run that promoted this change deliberately left the closure to the
	// forge's answer, so making it is this step's work. It is made here rather
	// than left to completeIntegrated so the reason says what actually happened:
	// a reviewed promotion the forge has now merged, not a run somebody
	// interrupted. completeIntegrated then finds the item closed and adds nothing,
	// which keeps the item's account of this merge to the one note above.
	if err := r.closeSettledMerge(ctx, state); err != nil {
		return reconciliationOf(state, ActionUnsettled), err
	}
	result, err := r.completeIntegrated(ctx, state, false)
	result.Detail = detail
	result.Catchup = catchup
	return result, err
}

// closeSettledMerge closes an item whose queued merge the forge has performed.
// An item already closed is left alone, so settling the same merge twice closes
// it once — and so does settling one an older run closed before the closure
// waited on the forge at all.
func (r Reconciler) closeSettledMerge(ctx context.Context, state runstate.State) error {
	itemStatus, err := r.itemStatus(ctx, state.WorkItemID)
	if err != nil {
		return err
	}
	if itemStatus == "closed" {
		return nil
	}
	if _, err := r.Tracker.Complete(ctx, state.WorkItemID, settledMergeCompletionReason(state)); err != nil {
		return fmt.Errorf("close integrated work item for run %s: %w", state.RunID, err)
	}
	return nil
}

// settleDroppedMerge settles a run whose queued merge the forge gave up on. The
// promotion stands and the item is not closed against it: nothing confirmed the
// merge, and closing an item as integrated on a publication that never happened
// is the early close this path exists to avoid. What it leaves instead is a
// durable blocker naming the dropped merge, which is what puts the item in front
// of triage — the one place a dropped merge may be re-armed, once and bounded.
//
// It writes the terminal record itself rather than through recordTerminalFailure
// for the reason blockContradictedIntegration does: the merge is no longer queued
// and that has to reach disk even for a run that finished successfully, or every
// later sweep asks the forge the same settled question again.
//
// An item an earlier run already closed — one promoted before the closure waited
// on the forge — keeps that closure. Reopening it would rewrite history the
// operator has already read; the outstanding publication on it is what they need.
func (r Reconciler) settleDroppedMerge(ctx context.Context, state runstate.State) (Reconciliation, error) {
	reason := state.PublishFailure
	itemStatus, err := r.itemStatus(ctx, state.WorkItemID)
	if err != nil {
		return reconciliationOf(state, ActionBlocked), err
	}
	if _, err := r.Tracker.RecordOutcome(ctx, state.WorkItemID, renderQueuedMergeNotes(state, reason, nil)); err != nil {
		return reconciliationOf(state, ActionUnsettled), fmt.Errorf("record the settled merge for run %s: %w", state.RunID, err)
	}
	if itemStatus == "closed" {
		result, err := r.completeIntegrated(ctx, state, false)
		result.Detail = reason
		return result, err
	}
	// The blocker reaches the item before the record is disturbed, so an
	// interruption here leaves the merge still queued durably and takes the whole
	// settlement up again rather than settling a run nobody was told about.
	notes, err := r.recordBlocker(ctx, state, itemStatus, gitworktree.Observation{}, reason)
	if err != nil {
		return reconciliationOf(state, ActionBlocked), err
	}
	state.Blocker = runstate.RecordBlocker(notes)
	settled, saveErr := r.saveTerminalFailure(state, reason)
	result := reconciliationOf(settled, ActionBlocked)
	result.Detail = reason
	result.DocketProblem = r.docketStoppedRun(settled)
	return result, saveErr
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
	notes, err := r.recordBlocker(ctx, state, itemStatus, observation, reason)
	if err != nil {
		return reconciliationOf(state, ActionBlocked), err
	}
	// The blocker is kept on the run as well as on the item, so what stopped
	// this run is readable from its own record rather than from whichever of the
	// item's notes turns out to have been this one's.
	state.Blocker = runstate.RecordBlocker(notes)
	settled, saveErr := r.recordTerminalFailure(state, reason)
	result := reconciliationOf(settled, ActionBlocked)
	result.Detail = reason
	result.DocketProblem = r.docketStoppedRun(settled)
	return result, saveErr
}

// docketStoppedRun puts a run this sweep stopped in front of the development
// manager. It is idempotent and keyed to the stoppage, so a run that docketed
// its own ending before the process died adds nothing here, and a sweep that
// runs twice over the same run dockets it once.
//
// It never fails the settlement: the blocker is already on the work item and the
// run is already settled by the time this runs, so a docket that could not be
// written is a delivery that did not happen rather than a run that has to be
// settled again. What it could not write is reported instead, and the next
// docket build finds the same stoppage on the run's own record.
func (r Reconciler) docketStoppedRun(state runstate.State) string {
	if r.Docket == nil {
		return ""
	}
	if _, err := r.Docket.RecordStoppedRun(state); err != nil {
		return fmt.Errorf("docket the stopped run %s: %w", state.RunID, err).Error()
	}
	return ""
}

// recordBlocker puts this run's evidence on the work item and returns the words
// it recorded, which are what the run's own record and the triage docket carry
// afterwards. An item already blocked keeps the status it has; the reason is
// still recorded, because this run's evidence is what a replan needs.
func (r Reconciler) recordBlocker(ctx context.Context, state runstate.State, itemStatus string, observation gitworktree.Observation, reason string) (string, error) {
	notes := renderReconcileBlockerNotes(state, observation, reason)
	var err error
	if itemStatus == "blocked" {
		_, err = r.Tracker.RecordOutcome(ctx, state.WorkItemID, notes)
	} else {
		_, err = r.Tracker.Block(ctx, state.WorkItemID, notes)
	}
	if err != nil {
		return "", fmt.Errorf("record blocker for run %s: %w", state.RunID, err)
	}
	return notes, nil
}

// recordTerminalFailure makes an unfinishable run durably terminal in the phase
// it stopped in, so the record still says where it got to. A run that is already
// terminal carries its own record of how it ended and is left exactly as it is.
func (r Reconciler) recordTerminalFailure(state runstate.State, reason string) (runstate.State, error) {
	if state.Status.Terminal() {
		return state, nil
	}
	return r.saveTerminalFailure(state, reason)
}

// saveTerminalFailure writes the terminal record. It is separate from
// recordTerminalFailure because settling can change durable state that has to
// reach disk even when the run was already terminal, and a caller with such a
// change needs the write rather than the skip.
func (r Reconciler) saveTerminalFailure(state runstate.State, reason string) (runstate.State, error) {
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
func renderQueuedMergeNotes(state runstate.State, detail string, catchup *gitworktree.Catchup) string {
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
	return strings.Join(append(lines, renderCatchupNotes(catchup)...), "\n")
}

// settledMergeCompletionReason closes an item on the whole of what happened to
// it: the promotion its own run made and reviewed, and the forge merge that run
// asked for and could not wait out.
func settledMergeCompletionReason(state runstate.State) string {
	return fmt.Sprintf("Reviewed and integrated by Yoyodyne run %s, then merged by the forge: %s is at %s",
		state.RunID, state.Integration.TargetBranch, state.Integration.TargetCommit)
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
	// The relaunch budget is durable for exactly this reader: a run interrupted
	// after absorbing provider deaths has already spent part of it, and a note
	// that omitted them would describe a run with more room than it has.
	if state.TransientRelaunches > 0 {
		lines = append(lines, "Relaunches after a provider death already spent: "+strconv.Itoa(state.TransientRelaunches))
	}
	lines = append(lines, renderObservedArtifacts(state, observation)...)
	if state.CheckFailure != nil {
		lines = append(lines, fmt.Sprintf("Last failing check: %s (exit %d)", state.CheckFailure.Command, state.CheckFailure.ExitCode))
	}
	if state.PathRefusal != nil {
		lines = append(lines, "Refused protected paths: "+strings.Join(state.PathRefusal.Paths, ", "))
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
