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
//
// The leftover checkouts are the same shape of debris one step further out, and
// they are the half that costs the machine rather than only the repository:
// every worktree registration is a path an agent's sandbox profile denies on
// every command it spawns, so registrations that accumulate with the harness's
// history eventually stop commands spawning at all. Retiring them is as
// judgement-free as the rest — a checkout holding uncommitted work is kept, and
// what the others carried is on branches this does not touch.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// settledWorktreeTail is how many settled runs keep their checkout on disk. It
// is what stops the sweep taking the evidence out from under the person it was
// preserved for: a run that stopped a few minutes ago is the one somebody is
// about to open, and leaving the last few where they are costs nothing.
//
// Past the tail a checkout is debris, and the machine pays for it. Every
// registration is a path an agent's sandbox profile denies on every command it
// spawns, so a repository that keeps them all eventually cannot spawn a command
// at all — the failure this bound exists for, reached at 180 registrations on
// the harness's own machine. Nothing is lost by going past it: the retirement
// keeps a checkout holding uncommitted work, and every commit the rest carried
// is on a branch the sweep beside this one only deletes once the target
// provably carries it.
const settledWorktreeTail = 8

// Convergence is what one sweep did to bring local state onto the forge's and
// to keep what the runs left behind bounded. It reports per artifact rather than
// as a total, because the failures a reader acts on are different: a target held
// behind the remote is a checkout that is out of date, a branch kept is work
// somebody still has to decide about, and a checkout kept is a directory
// somebody has to look at by hand.
type Convergence struct {
	Targets   []gitworktree.Catchup `json:"targets"`
	Worktrees []WorktreeSweep       `json:"worktrees"`
	Branches  []BranchSweep         `json:"branches"`
	// Registrations is the repository-wide prune that runs between the two. It
	// is not per run because what it removes is exactly what no run record
	// names any more.
	Registrations RegistrationSweep `json:"registrations"`
}

// WorktreeSweep is one settled run's leftover checkout and what became of it.
// It reads like the branch sweep beside it and for the same reason: the run is
// named because that is how an operator finds what the checkout was for, and a
// kept one is a fact somebody may still have to act on.
type WorktreeSweep struct {
	RunID      string `json:"run_id"`
	WorkItemID string `json:"work_item_id"`
	Path       string `json:"path"`
	Removed    bool   `json:"removed"`
	Kept       string `json:"kept,omitempty"`
	Failure    string `json:"failure,omitempty"`
}

// RegistrationSweep is what the repository-wide prune removed, and what stopped
// it where it could not run. A prune that failed never fails the sweep, for the
// reason one unremovable branch does not: what could not be done is reported
// beside everything that could.
type RegistrationSweep struct {
	Pruned  []string `json:"pruned"`
	Failure string   `json:"failure,omitempty"`
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
// forge has: every target branch the harness knows about is fast-forwarded onto
// its remote counterpart, the checkouts of settled runs past the tail are
// retired, the registrations of checkouts that are already gone are pruned, and
// then every settled run's leftover branch whose work those targets already
// carry is deleted.
//
// The order matters and is not an accident. A target that has just caught up
// contains more than it did a moment ago, so the branches are swept afterwards
// and against the branch as it now stands — and the checkouts go before the
// branches for the reason a retirement does them in that order, because a
// branch a checkout still holds is kept and that checkout is the one being
// retired just above. The prune sits between them, so a registration something
// else emptied stops holding its branch in the same pass rather than the next.
//
// One branch that cannot be converged never stops the sweep, for the reason one
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
		Targets:       make([]gitworktree.Catchup, 0),
		Worktrees:     make([]WorktreeSweep, 0),
		Branches:      make([]BranchSweep, 0),
		Registrations: RegistrationSweep{Pruned: make([]string, 0)},
	}
	for _, target := range recordedTargets(recorded) {
		convergence.Targets = append(convergence.Targets, r.catchUp(ctx, target))
	}
	for _, state := range sweepableWorktrees(recorded) {
		sweep, swept := r.sweepWorktree(ctx, state)
		if swept {
			convergence.Worktrees = append(convergence.Worktrees, sweep)
		}
	}
	convergence.Registrations = r.pruneRegistrations(ctx)
	for _, state := range recorded {
		sweep, swept := r.sweepBranch(ctx, state)
		if swept {
			convergence.Branches = append(convergence.Branches, sweep)
		}
	}
	return convergence, nil
}

// sweepableWorktrees lists the settled runs whose checkout this sweep may
// retire: the ones past the tail.
//
// A run that still owes a step is never a candidate, for the reason its branch
// is not — a live developer is working in that checkout, and whether it is still
// needed is reconciliation's question rather than hygiene's. Neither is a run
// whose record already says the checkout is gone, which is every run whose work
// was integrated and cleaned up.
//
// The tail is held back from the newest end, and it is a tail of checkouts that
// are actually there rather than of records: a run this sweep already retired
// records that, so it drops out of the candidates instead of occupying a slot
// that was meant to keep somebody's evidence on disk.
func sweepableWorktrees(recorded []runstate.State) []runstate.State {
	candidates := make([]runstate.State, 0, len(recorded))
	for _, state := range recorded {
		if state.Outstanding() || state.WorktreePath == "" || state.WorktreeRemoved {
			continue
		}
		candidates = append(candidates, state)
	}
	// Newest first, so what is held back is the most recent evidence rather than
	// whichever runs the store happened to list first.
	sort.SliceStable(candidates, func(left, right int) bool {
		first, second := settledAt(candidates[left]), settledAt(candidates[right])
		if first.Equal(second) {
			return candidates[left].RunID > candidates[right].RunID
		}
		return first.After(second)
	})
	if len(candidates) <= settledWorktreeTail {
		return nil
	}
	return candidates[settledWorktreeTail:]
}

// settledAt is when a run stopped being something anybody was watching. A run
// with no completion time recorded is ordered by when its record was last
// written, which is the closest thing it has to one.
func settledAt(state runstate.State) time.Time {
	if state.CompletedAt != nil {
		return *state.CompletedAt
	}
	return state.UpdatedAt
}

// sweepWorktree retires one settled run's checkout, and reports whether there
// was anything there to ask about at all.
//
// A registration that is already gone is the ordinary outcome — cleanup removed
// it when the run integrated, or an earlier sweep did — and reporting it would
// bury the ones that are actually still there. Nothing here decides anything:
// the retirement keeps a checkout holding uncommitted work, keeps a directory
// Git is not managing, and never touches the branch.
//
// It is done under the run's own lease, which is what makes the removal and the
// record of it one act. `yoyo status`, the triage docket, and a re-run all read
// the run's record as the answer to whether that directory is still there, so a
// removal written down under a different snapshot than the one it acted on is
// exactly the reader sent after a checkout that is gone.
func (r Reconciler) sweepWorktree(ctx context.Context, recorded runstate.State) (WorktreeSweep, bool) {
	sweep := WorktreeSweep{
		RunID:      recorded.RunID,
		WorkItemID: recorded.WorkItemID,
		Path:       recorded.WorktreePath,
	}
	state, lease, err := r.Store.AdoptRun(ctx, recorded.RunID)
	switch {
	case errors.Is(err, runstate.ErrRunHeld):
		// A live process owns this run, so it is that process's checkout rather
		// than debris, whatever the listing snapshot said a moment ago.
		return WorktreeSweep{}, false
	case err != nil:
		// Nothing is removed: an artifact retired without its record being
		// writable is the stale record this took the lease to avoid.
		sweep.Failure = fmt.Errorf("take the record of run %s to retire its checkout: %w", recorded.RunID, err).Error()
		return sweep, true
	}
	defer lease.Release()

	// The state is re-read by AdoptRun, so a run something else settled, retired,
	// or re-entered in the meantime is never swept from the snapshot this loop
	// started with.
	if state.Outstanding() || state.WorktreePath == "" || state.WorktreeRemoved {
		return WorktreeSweep{}, false
	}
	removal, err := r.Worktrees.RemovePreservedWorktree(ctx, worktreeOf(state))
	if err != nil {
		sweep.Failure = fmt.Errorf("retire the checkout of run %s: %w", state.RunID, err).Error()
		return sweep, true
	}
	if !removal.Registered && removal.Kept == "" {
		return WorktreeSweep{}, false
	}
	sweep.Removed = removal.Removed
	sweep.Kept = removal.Kept
	if removal.Removed {
		sweep.Failure = r.recordSweptWorktree(state)
	}
	return sweep, true
}

// recordSweptWorktree writes the removal onto the run it belongs to, and reports
// what stopped it where it could not. A record left saying the checkout is still
// there is worse than one that was never swept: the artifact is gone either way,
// and only one of the two sends somebody looking for it.
func (r Reconciler) recordSweptWorktree(state runstate.State) string {
	swept := r.clock().Now()
	state.WorktreeRemoved = true
	state.WorktreeSweptAt = &swept
	// When the run ended is what dates it; this dates the last thing the harness
	// did to what it left behind, which is what UpdatedAt has always meant.
	state.UpdatedAt = swept
	if err := r.Store.Save(state); err != nil {
		return fmt.Sprintf(
			"the checkout of run %s was retired and its own record still says otherwise, so anything reading that run will name a directory that is gone: %v",
			state.RunID, err)
	}
	return ""
}

// pruneRegistrations removes the registrations of checkouts that are no longer
// on disk, whichever run or person left them behind. It is what covers the ones
// no run record names any more — a checkout somebody deleted by hand, one whose
// run record is itself gone, one from a product this harness no longer holds —
// which a sweep driven from run state cannot see and which cost every later
// command a deny path in its sandbox profile all the same.
func (r Reconciler) pruneRegistrations(ctx context.Context) RegistrationSweep {
	prune, err := r.Worktrees.PruneRegistrations(ctx)
	sweep := RegistrationSweep{Pruned: prune.Pruned}
	if sweep.Pruned == nil {
		sweep.Pruned = make([]string, 0)
	}
	if err != nil {
		sweep.Failure = fmt.Errorf("prune the registrations of worktrees that are already gone: %w", err).Error()
	}
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
