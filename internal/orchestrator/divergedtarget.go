package orchestrator

// A target branch the harness will not catch up to the remote holds intake by
// itself.
//
// yoyodyne-ifd.428.2 stopped diverged-target refusals counting toward the
// failure-storm brake, correctly: a divergence is a stop the harness made and
// not a verdict on any change. What that left was a wedged target letting a
// watching session go on pulling, each item spending a whole development and
// review before stopping on the same divergence, until a person ran the
// unwedging steps. So the refusal is also recorded against the product here,
// and a watching session reads that record at every pull and chooses nothing
// while it stands. It is neither the brake nor an intake hold: nothing releases
// it, and nothing needs to, because what ends it is the branches converging —
// which the convergence sweep of `yoyo reconcile` finds, and lifts the record
// on, whoever settled them.

import (
	"fmt"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// DivergedTargets is the product's record of the target branches the harness
// will not catch up to the remote, as a run writes it. It is satisfied by
// *runstate.DivergedTargetStore.
type DivergedTargets interface {
	Notice(observed runstate.DivergedTargetObservation) (runstate.DivergedTarget, error)
}

// ScheduleDivergences is the same record as a watching session reads it.
type ScheduleDivergences interface {
	Standing() ([]runstate.DivergedTarget, error)
}

// ReconcileDivergences is the same record as the convergence sweep reads and
// lifts it.
type ReconcileDivergences interface {
	Standing() ([]runstate.DivergedTarget, error)
	Clear(targetBranch string) (runstate.DivergedTarget, bool, error)
}

// noticeDivergedTarget records the refusal on the product. The run stops
// exactly as it would have whether or not the record could be written — its
// blocker is what hands the item to a person — so a record that could not be
// written is said on the outcome rather than failing anything.
func (a *activeRun) noticeDivergedTarget(catchup gitworktree.Catchup) {
	p := a.pipeline
	if p.DivergedTargets == nil {
		return
	}
	if _, err := p.DivergedTargets.Notice(runstate.DivergedTargetObservation{
		TargetBranch: catchup.TargetBranch,
		Remote:       p.Config.Execution.Remote,
		LocalCommit:  catchup.LocalCommit,
		RemoteCommit: catchup.RemoteCommit,
		Held:         catchup.Held,
		RunID:        a.state.RunID,
		WorkItemID:   a.state.WorkItemID,
		At:           p.clock().Now(),
	}); err != nil {
		a.outcome.DivergedTargetProblem = fmt.Errorf("record that %s will not catch up to the remote's: %w", catchup.TargetBranch, err).Error()
	}
}

// divergedTarget reads whether a target branch stands diverged, and reports the
// first where one does. A pull wired without the record reads none. A record
// that cannot be read is said on the schedule and the pull is made as though
// none stood, for the reason an unreadable outage is: the run it starts meets
// the divergence itself if there is one, stops on it, and writes it again.
func (s Scheduler) divergedTarget(schedule *Schedule, pull Pull) (runstate.DivergedTarget, bool) {
	if pull.Divergences == nil {
		return runstate.DivergedTarget{}, false
	}
	standing, err := pull.Divergences.Standing()
	if err != nil {
		schedule.DivergedTargetProblem = fmt.Sprintf("whether a target branch stands diverged could not be read, so the pull was made as though none did: %v", err)
		return runstate.DivergedTarget{}, false
	}
	schedule.DivergedTargets = standing
	if len(standing) == 0 {
		return runstate.DivergedTarget{}, false
	}
	return standing[0], true
}

// divergedTargetReason is what a watching session records as the reason it is
// choosing nothing while a divergence stands.
func divergedTargetReason(diverged runstate.DivergedTarget) string {
	return diverged.Says() + "; " + runstate.DivergedTargetRecovery
}

// DivergenceLift is one divergence the convergence sweep lifted, or could not.
type DivergenceLift struct {
	TargetBranch string                   `json:"target_branch"`
	Lifted       *runstate.DivergedTarget `json:"lifted,omitempty"`
	Failure      string                   `json:"failure,omitempty"`
}

// liftDivergences lifts the recorded divergence on every target the sweep found
// converged with the remote — advanced onto it, or already level — and leaves
// every other standing. A catch-up held for any reason is not converged, so a
// branch the sweep could not bring on, or could not ask about, keeps its record.
func (r Reconciler) liftDivergences(targets []gitworktree.Catchup) []DivergenceLift {
	lifts := make([]DivergenceLift, 0)
	if r.Divergences == nil {
		return lifts
	}
	for _, catchup := range targets {
		if catchup.Held != "" {
			continue
		}
		lifted, found, err := r.Divergences.Clear(catchup.TargetBranch)
		switch {
		case err != nil:
			lifts = append(lifts, DivergenceLift{TargetBranch: catchup.TargetBranch, Failure: fmt.Errorf("lift the divergence recorded on %s: %w", catchup.TargetBranch, err).Error()})
		case found:
			lifts = append(lifts, DivergenceLift{TargetBranch: catchup.TargetBranch, Lifted: &lifted})
		}
	}
	return lifts
}

// divergedTargetsToCatchUp adds every target branch the record names to the
// ones the runs name, so a divergence is asked about even where the runs that
// met it are no longer in the store's listing.
func (r Reconciler) divergedTargetsToCatchUp(targets []string) ([]string, string) {
	if r.Divergences == nil {
		return targets, ""
	}
	standing, err := r.Divergences.Standing()
	if err != nil {
		return targets, fmt.Sprintf("the recorded divergences could not be read, so none was lifted: %v", err)
	}
	known := make(map[string]bool, len(targets))
	for _, target := range targets {
		known[target] = true
	}
	for _, diverged := range standing {
		if !known[diverged.TargetBranch] {
			known[diverged.TargetBranch] = true
			targets = append(targets, diverged.TargetBranch)
		}
	}
	return targets, ""
}
