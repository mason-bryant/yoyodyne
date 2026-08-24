package orchestrator

// Post-merge hygiene. Automatic integration includes its own aftermath: a merge
// whose local convergence needs a human is integration that is only half
// automatic. Everything here is judgement-free by construction — a fast-forward
// onto a commit that already contains the local branch, and the deletion of a
// branch whose work the target provably carries — so it belongs to the harness
// rather than to any role that decides things.
//
// It lives beside reconciliation because it is the same shape of work: a sweep
// over durable state, safe to repeat, that finishes what an interrupted or
// finished run left owed. A run's own settle path catches its target up while
// it still holds the promotion lease; this is what covers every run that could
// not, every merge the forge performed after its run was over, and every branch
// a cleanup could not reach.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// retainedSettledCheckouts is how many settled runs keep the checkout they
// preserved. Past that tail a settled run's checkout is retired, because the
// registrations are a shared resource rather than a per-run one: every one of
// them is a path an agent's sandbox profile carries on every command it spawns,
// and a machine that has been running the harness for a month cannot spawn a
// command at all. The tail is what keeps the evidence of a recent stoppage where
// somebody looking into one expects to find it.
//
// Nothing is lost past it. A checkout holding uncommitted work is kept whatever
// the tail says, and no branch is touched here, so what a retired registration
// leaves behind is commits on a branch this sweep only ever deletes on proof the
// target already carries them.
//
// It is a constant rather than a setting because it bounds a resource of the
// machine rather than describing the project: a repository whose registrations
// have to grow further has a problem no number here fixes.
const retainedSettledCheckouts = 8

// Convergence is what one sweep did to bring local state onto the forge's, and
// to bound what the repository's worktree bookkeeping carries. It reports per
// artifact rather than as a total, because the failures a reader acts on are
// different: a target held behind the remote is a checkout that is out of date,
// a branch kept is work somebody still has to decide about, and a checkout kept
// is a directory somebody has to open.
type Convergence struct {
	Targets   []gitworktree.Catchup `json:"targets"`
	Checkouts []CheckoutSweep       `json:"checkouts"`
	Branches  []BranchSweep         `json:"branches"`
	// Prune is what pruning the registrations whose checkout is already gone
	// removed and what it left, and PruneFailure is why it could not be done. A
	// prune that failed never stops the sweep: what it clears is bookkeeping
	// nothing else reads, so the rest of the pass is worth making either way.
	Prune        gitworktree.Prune `json:"prune"`
	PruneFailure string            `json:"prune_failure,omitempty"`
}

// CheckoutSweep is one settled run's preserved checkout and what became of it.
// The run is named because that is how an operator finds what the checkout was
// for, and Superseded says which of the two claims retired it.
type CheckoutSweep struct {
	RunID      string `json:"run_id"`
	WorkItemID string `json:"work_item_id"`
	Path       string `json:"path"`
	// Superseded names the later run of the same work item whose promotion
	// landed. It is empty on a checkout retired for being past the tail, which is
	// the weaker of the two claims and says so by naming nothing.
	Superseded string `json:"superseded,omitempty"`
	Removed    bool   `json:"removed"`
	Kept       string `json:"kept,omitempty"`
	Failure    string `json:"failure,omitempty"`
}

// BranchSweep is one settled run's leftover branch and what became of it. The
// run is named because that is how an operator finds what the branch was for;
// the branch is only ever removed on containment proved in the repository.
type BranchSweep struct {
	RunID        string `json:"run_id"`
	WorkItemID   string `json:"work_item_id"`
	Branch       string `json:"branch"`
	TargetBranch string `json:"target_branch"`
	Commit       string `json:"commit,omitempty"`
	Removed      bool   `json:"removed"`
	Kept         string `json:"kept,omitempty"`
	Failure      string `json:"failure,omitempty"`
}

// Converge brings the primary checkout and the local branches onto what the
// forge has, and brings the repository's worktree bookkeeping back to the runs
// that are actually live: the stale registrations are pruned, every target
// branch the harness knows about is fast-forwarded onto its remote counterpart,
// the checkouts of settled runs past the tail are retired, and then every
// settled run's leftover branch whose work those targets already carry is
// deleted.
//
// The order matters and is not an accident. Pruning goes first because it is the
// only step that reaches a registration no run record knows about, and every
// step below reads that same bookkeeping. A target that has just caught up
// contains more than it did a moment ago, so the branches are swept against the
// branch as it now stands. And the checkouts are retired before the branches,
// because Git refuses to delete a branch a checkout still holds: retiring the
// checkout in the same pass is what lets the branch under it go in the same
// pass.
//
// One thing that cannot be converged never stops the sweep, for the reason one
// unreconcilable run does not: what could not be done is reported beside
// everything that could, so an operator reading the sweep sees the whole of it.
func (r Reconciler) Converge(ctx context.Context) (Convergence, error) {
	if err := r.validate(); err != nil {
		return Convergence{}, err
	}
	recorded, err := r.Store.Recorded()
	if err != nil {
		return Convergence{}, fmt.Errorf("discover recorded runs: %w", err)
	}
	convergence := Convergence{
		Targets:   make([]gitworktree.Catchup, 0),
		Checkouts: make([]CheckoutSweep, 0),
		Branches:  make([]BranchSweep, 0),
	}
	prune, pruneErr := r.Worktrees.PruneRegistrations(ctx)
	convergence.Prune = prune
	if pruneErr != nil {
		convergence.PruneFailure = fmt.Errorf("prune the stale worktree registrations: %w", pruneErr).Error()
	}
	for _, target := range recordedTargets(recorded) {
		convergence.Targets = append(convergence.Targets, r.catchUp(ctx, target))
	}
	for _, candidate := range retirableCheckouts(recorded) {
		convergence.Checkouts = append(convergence.Checkouts, r.retireCheckout(ctx, candidate))
	}
	for _, state := range recorded {
		sweep, swept := r.sweepBranch(ctx, state)
		if swept {
			convergence.Branches = append(convergence.Branches, sweep)
		}
	}
	return convergence, nil
}

// checkoutCandidate is one settled run whose preserved checkout may be retired,
// and the claim that makes it retirable.
type checkoutCandidate struct {
	state runstate.State
	// superseded names the later run of the same work item whose promotion
	// landed, and is empty on a candidate that is retirable only for being past
	// the tail.
	superseded string
}

// landedRun is the most recent run of one work item whose promotion reached its
// target branch.
type landedRun struct {
	runID     string
	startedAt time.Time
}

// retirableCheckouts picks the settled runs whose preserved checkout may go,
// newest first.
//
// Two claims make one retirable, and they are not the same claim. A run whose
// work item a later run has since landed is superseded: what its checkout held
// has been answered by something that reached the target branch, so it is
// retirable however recently it stopped. Everything else is retirable only past
// the tail, which is a bound on a shared resource rather than a judgement about
// the work — and which is why the tail is generous and the removal it authorizes
// is the narrowest one there is.
//
// A run that still owes a step is never a candidate, for the reason its branch
// is not: settling it may yet need what it left, and deciding that is
// reconciliation's rather than hygiene's.
func retirableCheckouts(recorded []runstate.State) []checkoutCandidate {
	landed := landedRuns(recorded)
	preserved := make([]runstate.State, 0, len(recorded))
	for _, state := range recorded {
		if state.Outstanding() || state.WorktreePath == "" || state.WorktreeRemoved {
			continue
		}
		preserved = append(preserved, state)
	}
	// Newest first, so the tail is the checkouts of the most recent stoppages —
	// which are the ones somebody is still likely to open.
	sort.SliceStable(preserved, func(first, second int) bool {
		return preserved[first].StartedAt.After(preserved[second].StartedAt)
	})
	candidates := make([]checkoutCandidate, 0, len(preserved))
	for index, state := range preserved {
		successor := landed[state.WorkItemID]
		if successor.runID != "" && successor.runID != state.RunID && successor.startedAt.After(state.StartedAt) {
			candidates = append(candidates, checkoutCandidate{state: state, superseded: successor.runID})
			continue
		}
		if index < retainedSettledCheckouts {
			continue
		}
		candidates = append(candidates, checkoutCandidate{state: state})
	}
	return candidates
}

// landedRuns names, per work item, the most recent run whose promotion reached
// the target branch. It is the evidence that makes an earlier run's preserved
// checkout retirable: what that run was holding has been done by something else,
// and what it still holds is a directory nobody is going to open.
func landedRuns(recorded []runstate.State) map[string]landedRun {
	landed := make(map[string]landedRun, len(recorded))
	for _, state := range recorded {
		if state.Integration == nil || state.WorkItemID == "" {
			continue
		}
		if existing, known := landed[state.WorkItemID]; known && !state.StartedAt.After(existing.startedAt) {
			continue
		}
		landed[state.WorkItemID] = landedRun{runID: state.RunID, startedAt: state.StartedAt}
	}
	return landed
}

// retireCheckout removes one settled run's preserved checkout and writes the
// removal onto that run's own record, under the run's own lease so that the two
// are one act. Every other reader in the harness — `yoyo status`, a docket entry
// built later, the next sweep — asks that record whether the checkout is still
// there, so a removal nobody wrote down would send all of them after a directory
// that is gone.
//
// Nothing here fails the sweep. A checkout that has to be looked at by hand, a
// run a live process has taken up again, and a record that could not be written
// are each a fact to report beside everything else the pass did.
func (r Reconciler) retireCheckout(ctx context.Context, candidate checkoutCandidate) CheckoutSweep {
	sweep := CheckoutSweep{
		RunID:      candidate.state.RunID,
		WorkItemID: candidate.state.WorkItemID,
		Path:       candidate.state.WorktreePath,
		Superseded: candidate.superseded,
	}
	state, lease, err := r.Store.AdoptRun(ctx, candidate.state.RunID)
	if err != nil {
		if errors.Is(err, runstate.ErrRunHeld) {
			sweep.Kept = "a live process holds this run"
			return sweep
		}
		sweep.Failure = fmt.Errorf("take run %s to retire the checkout it preserved: %w", candidate.state.RunID, err).Error()
		return sweep
	}
	defer lease.Release()

	// The record is re-read under the lease, so nothing is removed on the
	// strength of a listing another process has already moved on from.
	if state.Outstanding() || state.WorktreePath == "" || state.WorktreeRemoved {
		sweep.Kept = "the run owes a step again, or its checkout is already recorded as gone"
		return sweep
	}
	removal, err := r.Worktrees.RemovePreservedWorktree(ctx, worktreeOf(state))
	if err != nil {
		sweep.Failure = fmt.Errorf("retire the checkout run %s preserved: %w", state.RunID, err).Error()
		return sweep
	}
	if !removal.Removed {
		// A checkout that survived always says why, and a sweep repeats itself until
		// somebody acts on that reason — which is right, because the reason is a
		// directory a person has to look at. What must never happen is the same
		// candidate coming back with nothing said about it: an entry with neither a
		// removal nor a reason is a silent no-op re-attempted on every later pass,
		// so one is named as the thing to inspect rather than left empty.
		sweep.Kept = removal.Kept
		if sweep.Kept == "" {
			sweep.Kept = fmt.Sprintf("%s was neither retired nor kept for a stated reason, so it has to be inspected by hand", state.WorktreePath)
		}
		return sweep
	}
	state.WorktreeRemoved = true
	// This sweep removed it, so the record says so on every retirement rather than
	// only on the ones no successor answers for. Which of the two claims it was
	// taken on is a second fact beside that one: a superseded checkout also names
	// the run that landed the work, and a checkout taken for being past the tail
	// names nothing, because there is nothing to name. Recording only the
	// successor would file a sweep's removal under a decision triage never made.
	state.CheckoutRetired = true
	if candidate.superseded != "" {
		state.ArtifactsRetiredBy = candidate.superseded
	}
	// When the run ended is what dates it; this dates the last thing the harness
	// did to what it left behind, which is what UpdatedAt has always meant.
	state.UpdatedAt = r.clock().Now()
	if err := r.Store.Save(state); err != nil {
		sweep.Failure = fmt.Errorf(
			"the checkout run %s preserved was retired, and its own record still says otherwise, so anything reading that run will name a directory that is gone: %w",
			state.RunID, err).Error()
		return sweep
	}
	sweep.Removed = true
	return sweep
}

// catchUp moves one target branch under that branch's promotion lease, so a
// catch-up and a promotion never read the same branch and then both move it.
// The lease is the harness's own and this is the harness's own process; nothing
// an agent can reach acquires it, and nothing here performs a promotion.
func (r Reconciler) catchUp(ctx context.Context, targetBranch string) gitworktree.Catchup {
	lease, err := r.Store.LeasePromotion(ctx, targetBranch)
	if err != nil {
		return gitworktree.Catchup{
			TargetBranch: targetBranch,
			Held:         fmt.Errorf("wait for a turn to move %s: %w", targetBranch, err).Error(),
		}
	}
	// Releasing is this process letting the next promotion in, and the operating
	// system does it anyway when the process exits, so a close that failed says
	// nothing about the branch below.
	defer func() { _ = lease.Release() }()

	catchup, err := r.Worktrees.CatchUpTarget(ctx, targetBranch)
	if err != nil {
		catchup.TargetBranch = targetBranch
		catchup.Held = err.Error()
	}
	return catchup
}

// sweepBranch retires one settled run's branch, and reports whether the run had
// a branch worth asking about at all.
//
// A run that still owes a step is left entirely alone: settling it may yet need
// the branch, and reconciliation is what decides that. A run in flight is one of
// those, so a live developer's branch is never a candidate here.
func (r Reconciler) sweepBranch(ctx context.Context, state runstate.State) (BranchSweep, bool) {
	if state.Outstanding() || state.Branch == "" || state.TargetBranch == "" {
		return BranchSweep{}, false
	}
	sweep := BranchSweep{
		RunID:        state.RunID,
		WorkItemID:   state.WorkItemID,
		Branch:       state.Branch,
		TargetBranch: state.TargetBranch,
	}
	removal, err := r.Worktrees.RemoveMergedBranch(ctx, state.Branch, state.TargetBranch)
	if err != nil {
		sweep.Commit = removal.Commit
		sweep.Failure = fmt.Errorf("remove the merged branch of run %s: %w", state.RunID, err).Error()
		return sweep, true
	}
	// A branch that is already gone is the ordinary outcome — cleanup removed it
	// when the run finished — and reporting it as a sweep would bury the ones
	// that are actually still there.
	if removal.Commit == "" {
		return BranchSweep{}, false
	}
	sweep.Commit = removal.Commit
	sweep.Removed = removal.Removed
	sweep.Kept = removal.Kept
	return sweep, true
}

// recordedTargets lists the distinct target branches the recorded runs name, in
// a fixed order so one sweep's report reads like the next one's.
func recordedTargets(recorded []runstate.State) []string {
	seen := make(map[string]struct{}, len(recorded))
	targets := make([]string, 0, len(recorded))
	for _, state := range recorded {
		if state.TargetBranch == "" {
			continue
		}
		if _, known := seen[state.TargetBranch]; known {
			continue
		}
		seen[state.TargetBranch] = struct{}{}
		targets = append(targets, state.TargetBranch)
	}
	sort.Strings(targets)
	return targets
}
