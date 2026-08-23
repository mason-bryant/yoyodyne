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

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Convergence is what one sweep did to bring local state onto the forge's. It
// reports per branch rather than as a total, because the two failures a reader
// acts on are different: a target held behind the remote is a checkout that is
// out of date, and a branch kept is work somebody still has to decide about.
type Convergence struct {
	Targets  []gitworktree.Catchup `json:"targets"`
	Branches []BranchSweep         `json:"branches"`
	// Publications is the forge's half of the same hygiene: the pull requests of
	// runs whose work landed by another vehicle, closed with that vehicle named.
	Publications []PublicationSweep `json:"publications"`
}

// PublicationSweep is one superseded run's publication and what became of it.
// Both the run that published it and the vehicle its work landed by are named,
// because that pair is the whole of the justification for closing a pull request
// somebody may still be looking at.
type PublicationSweep struct {
	RunID        string       `json:"run_id"`
	WorkItemID   string       `json:"work_item_id"`
	SupersededBy Supersession `json:"superseded_by"`
	PublicationRetirement
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
// its remote counterpart, then every settled run's leftover branch whose work
// those targets already carry is deleted, and then every publication whose work
// landed by another vehicle is closed with that vehicle named.
//
// The order matters and is not an accident. A target that has just caught up
// contains more than it did a moment ago, so the branches are swept afterwards
// and against the branch as it now stands. The publications come last because
// they are about the forge rather than the checkout, and because nothing local
// depends on them.
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
		Targets:      make([]gitworktree.Catchup, 0),
		Branches:     make([]BranchSweep, 0),
		Publications: make([]PublicationSweep, 0),
	}
	for _, target := range recordedTargets(recorded) {
		convergence.Targets = append(convergence.Targets, r.catchUp(ctx, target))
	}
	for _, state := range recorded {
		sweep, swept := r.sweepBranch(ctx, state)
		if swept {
			convergence.Branches = append(convergence.Branches, sweep)
		}
	}
	for _, superseded := range supersededPublications(recorded) {
		sweep, swept := r.sweepPublication(ctx, superseded)
		if swept {
			convergence.Publications = append(convergence.Publications, sweep)
		}
	}
	return convergence, nil
}

// supersededPublication is one run's open publication paired with the run whose
// work landed in its place.
type supersededPublication struct {
	state runstate.State
	by    Supersession
}

// supersededPublications pairs every run holding a publication that will never
// merge with the run whose work landed instead.
//
// The evidence is the harness's own and it is two facts rather than one. The
// superseded run ended and integrated nothing, so its pull request carries work
// no promotion ever took. Another run of the same item did integrate, which is a
// promotion this harness made and recorded — so the work that request was opened
// for is on the target branch by that other vehicle.
//
// The landing run must also have got there after the superseded one began, and
// that clause is what keeps this from closing a publication that came *after* a
// landing: an item worked, closed, reopened and worked again has two runs, and
// only the second one's open request is pending. When the request itself was
// opened is not recorded, but it cannot precede the run that opened it, so the
// run's start is the bound available and the one that errs the safe way.
func supersededPublications(recorded []runstate.State) []supersededPublication {
	landed := make(map[string]runstate.State, len(recorded))
	for _, state := range recorded {
		if state.Integration == nil {
			continue
		}
		// Recorded runs arrive in a fixed order, so an item two runs landed —
		// which nothing here has to arbitrate — names the same one every sweep.
		if _, known := landed[state.WorkItemID]; !known {
			landed[state.WorkItemID] = state
		}
	}
	superseded := make([]supersededPublication, 0)
	for _, state := range recorded {
		by, found := landed[state.WorkItemID]
		if !found || by.RunID == state.RunID || !retirablePublication(state) {
			continue
		}
		if !landedAfter(by, state.StartedAt) {
			continue
		}
		superseded = append(superseded, supersededPublication{state: state, by: supersessionOf(by)})
	}
	return superseded
}

// sweepPublication retires one superseded run's pull request, and reports
// whether this sweep is what had anything to say about it.
//
// The run's record is taken under its own lease and re-read there, for the
// reason settling a run is: the listing is a snapshot another process may have
// moved on from, and the close and the note of it have to be one act. A run a
// live process holds is left to that process and reported by nobody — it is not
// a failure, and a line about it on every sweep would bury the ones that are.
func (r Reconciler) sweepPublication(ctx context.Context, superseded supersededPublication) (PublicationSweep, bool) {
	published := *superseded.state.PullRequest
	sweep := PublicationSweep{
		RunID:        superseded.state.RunID,
		WorkItemID:   superseded.state.WorkItemID,
		SupersededBy: superseded.by,
		PublicationRetirement: PublicationRetirement{
			Number: published.Number,
			URL:    published.URL,
			Branch: published.Branch,
		},
	}
	if r.Publisher == nil {
		sweep.Failure = fmt.Sprintf(
			"run %s left pull request %d open and run %s landed the work instead, and this sweep has no forge access to close it",
			superseded.state.RunID, published.Number, superseded.by.RunID)
		return sweep, true
	}
	state, lease, err := r.Store.AdoptRun(ctx, superseded.state.RunID)
	switch {
	case errors.Is(err, runstate.ErrRunHeld):
		return PublicationSweep{}, false
	case err != nil:
		sweep.Failure = fmt.Errorf("adopt run %s to retire the publication it left: %w", superseded.state.RunID, err).Error()
		return sweep, true
	}
	defer lease.Release()

	// What was read from the listing is asked again of the record just adopted.
	// Another process may have retired this publication in between, and a second
	// close would put a second comment on somebody's pull request.
	if !retirablePublication(state) {
		return PublicationSweep{}, false
	}
	sweep.PublicationRetirement = retirePublication(ctx, r.Publisher, r.Worktrees, state, superseded.by)
	if sweep.Failure != "" {
		return sweep, true
	}
	published = *state.PullRequest
	published.Superseded = superseded.by.Vehicle()
	state.PullRequest = &published
	state.UpdatedAt = r.clock().Now()
	if err := r.Store.Save(state); err != nil {
		// The forge is settled and the record is not, so the next sweep asks the
		// forge about a request it has already closed. That costs a query and adds
		// no second comment — Close reports an already-closed request rather than
		// closing it again — but it repeats forever until somebody looks, so it is
		// never left unsaid.
		sweep.Failure = fmt.Errorf("record that pull request %d of run %s was retired as superseded: %w",
			published.Number, state.RunID, err).Error()
	}
	return sweep, true
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
