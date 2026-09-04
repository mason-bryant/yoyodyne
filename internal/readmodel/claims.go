package readmodel

// A claim with nothing behind it, which is the other state nothing in the record
// announces.
//
// The silence reading beside this one asks whether anything at all has started.
// It catches a line that has stopped over a queue with work in it, and it
// structurally cannot catch this one: an item the harness claimed has left the
// ready queue, so a machine whose only startable work is sitting under dead
// claims reads as a drained queue from every surface here — nothing held,
// nothing running, nothing ready, nothing wrong. That is what happened on four
// nights of the week of 2026-09-01, twice in one hour on the last of them, and
// each time the line was idle until somebody looked in the morning.
//
// So this reads the claims against the runs. Not "what does the tracker say is
// being worked on" but "does the harness actually have a run alive for it" — a
// question answered from the runs' own records rather than from the claim, which
// is why it survives the death of the process that made the claim. The claim is
// the one thing a dead run leaves behind that nothing else cleans up: the run's
// record stops moving and the item stays claimed forever.
//
// What is here is only the reading. Giving the claim back is the harness's, and
// saying so is the surface's, exactly as the silence reading beside it divides
// the same work.

import (
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// DefaultDeadClaimThreshold is how long a claim may have nothing alive behind it
// before that is a dead claim rather than the gap between a run ending and the
// tracker catching up.
//
// Half an hour is chosen against what it costs to be wrong in each direction, and
// the two costs are not symmetric. Acting early releases an item something is
// still finishing, which the scheduler then declines to start anyway — a run in
// flight for an item is what keeps it out of every pull — so the price is a
// message nobody needed. Acting late is the failure this exists to end, and the
// window that was actually paid for was a night at a time. It is the same half
// hour DefaultStallThreshold takes, deliberately: an operator reading both
// messages should not have to hold two different ideas of how long is too long.
const DefaultDeadClaimThreshold = 30 * time.Minute

// Claim is one item the tracker says somebody has already pulled: the identifier
// and what it is called. It is passed in rather than read here for the reason
// Activity's fields are — the caller has read the claimed slice already, and a
// second reading is a second chance for one pass to report one machine two ways.
type Claim struct {
	WorkItemID string
	Title      string
}

// DeadClaim is one claim with nothing alive behind it: the item, the run whose
// death left it, when that run last said anything, and what the record says
// became of it.
type DeadClaim struct {
	WorkItemID string `json:"work_item_id"`
	Title      string `json:"title,omitempty"`
	// RunID is the most recent run recorded for the item, which is the one whose
	// ending or whose silence left the claim behind.
	RunID string `json:"run_id,omitempty"`
	// Since is the last moment any run for this item recorded anything, which is
	// what the age of the dead claim is measured from.
	Since time.Time `json:"since,omitempty"`
	// Because is what the record says about that run, as the clause a message is
	// written around. It is what tells a run the harness finished from a process
	// somebody killed, and the two want different things done about them.
	Because string `json:"because,omitempty"`
}

// DeadClaims reports the claims that have nothing alive behind them.
//
// A claim with no recorded run at all is never one of them, and that is a
// boundary rather than an oversight. The harness reserves a run before it claims
// anything, so every claim it makes has a record; a claim with none was made by
// somebody else — a person at a terminal, an agent working by hand — and giving
// back work a person took is not this pass's to do.
//
// A claim is kept wherever the run behind it is working or waiting; see
// holdsItsClaim, which is the whole of the safety of this. Being wrong in that
// direction leaves an item stuck for somebody to notice, and being wrong in the
// other puts a second developer on work the first is still holding.
func DeadClaims(claims []Claim, runs []runstate.State, now time.Time, threshold, within time.Duration) []DeadClaim {
	if threshold <= 0 {
		threshold = DefaultDeadClaimThreshold
	}
	if within <= 0 {
		within = DefaultRunActivityWindow
	}
	recorded := map[string][]runstate.State{}
	for _, run := range runs {
		recorded[run.WorkItemID] = append(recorded[run.WorkItemID], run)
	}
	var dead []DeadClaim
	for _, claim := range claims {
		behind := recorded[claim.WorkItemID]
		if len(behind) == 0 {
			continue
		}
		latest, since, held := readClaim(behind, now, within)
		if held || since.IsZero() || now.Sub(since) < threshold {
			continue
		}
		// An item whose latest run already promoted its change is not work to be
		// started again, whatever its claim says. The run got as far as putting the
		// change on the target branch and stopped somewhere after it — a tracker
		// write that failed is the way there — so the item wants closing rather than
		// developing a second time, and `yoyo reconcile` is what closes it from the
		// promotion the record holds. Giving this claim back would buy the same
		// change twice and a conflict at the end of it.
		if latest.Integration != nil {
			continue
		}
		dead = append(dead, DeadClaim{
			WorkItemID: claim.WorkItemID,
			Title:      claim.Title,
			RunID:      latest.RunID,
			Since:      since,
			Because:    whatBecameOfIt(latest, since),
		})
	}
	return dead
}

// readClaim reads one item's runs: the most recent of them, the last moment any
// of them recorded anything, and whether one of them still holds the claim.
//
// The most recent is by the same ordering the run store keeps — the start orders
// them, and the last update settles a tie — because what is true of an item now
// is decided by the last run that said anything about it.
func readClaim(runs []runstate.State, now time.Time, within time.Duration) (latest runstate.State, since time.Time, held bool) {
	for _, run := range runs {
		if moment := lastWord(run); moment.After(since) {
			since = moment
		}
		if holdsItsClaim(run, now, within) {
			held = true
		}
		if latest.RunID == "" || laterRun(run, latest) {
			latest = run
		}
	}
	return latest, since, held
}

// holdsItsClaim reports a run that still owns the item it claimed: one that is
// visibly working, and one that is deliberately waiting.
//
// The first is the run's own UpdatedAt, which is the test ActiveRuns uses. The
// second is the half that cannot be read from a clock at all. A run that stopped
// short for a directive, for work its item depends on, for the operator's pause,
// for a provider that refused it, or for a repair it is owed does not just go
// quiet — it returns, and its process exits, leaving a record nothing writes to
// again until somebody continues it. Its item stays claimed on purpose, with the
// worktree and the developer session that continuation needs, and giving that
// claim back would put a second run on work that is waiting to be picked up
// where it left off.
//
// This is deliberately wider than the pipeline's own account of which runs it can
// continue, and it is not a second copy of it: that one decides whether to resume
// a run and checks the artifacts and the phase it would resume into, and this
// only asks whether anything at all is recorded that says the run is owed
// something. Being wider is the safe direction, because every way it is wrong
// keeps a claim rather than taking one.
func holdsItsClaim(run runstate.State, now time.Time, within time.Duration) bool {
	if run.Status.Terminal() {
		return false
	}
	if !run.UpdatedAt.IsZero() && now.Sub(run.UpdatedAt) < within {
		return true
	}
	return run.UsageLimitResetsAt != nil ||
		run.PauseCause != "" ||
		run.ProviderStop != "" ||
		run.DirectivePause != nil ||
		run.DependencyPause != nil ||
		run.OperatorHeldSince != nil ||
		run.RepairAttempts > 0
}

// laterRun reports the more recent of two runs on one item, in the order
// runstate.Store.Runs keeps: the start orders them, whatever either goes on to
// do, and the last update settles the tie two runs started at one instant leave.
func laterRun(run, held runstate.State) bool {
	if !run.StartedAt.Equal(held.StartedAt) {
		return run.StartedAt.After(held.StartedAt)
	}
	return run.UpdatedAt.After(held.UpdatedAt)
}

// lastWord is the last moment one run recorded anything. All three stamps are
// read rather than only the update, because a record that ended carries its
// ending on CompletedAt and a record that never moved past its reservation
// carries nothing but its start.
func lastWord(run runstate.State) time.Time {
	moment := run.UpdatedAt
	if run.CompletedAt != nil && run.CompletedAt.After(moment) {
		moment = *run.CompletedAt
	}
	if run.StartedAt.After(moment) {
		moment = run.StartedAt
	}
	return moment
}

// whatBecameOfIt is the clause a message about a dead claim is written around.
//
// The two cases are said apart because they are two different things to do
// about. A run that ended left the claim behind by finishing without giving it
// back, and the item is free the moment the claim goes. A run that simply stopped
// saying anything is a process somebody or something killed, and its record is
// still in flight — so releasing the claim makes the tracker true and the
// scheduler will not pull the item until `yoyo reconcile` settles the run that is
// still holding its slot.
func whatBecameOfIt(latest runstate.State, since time.Time) string {
	stamp := since.UTC().Format(time.RFC3339)
	if latest.Status.Terminal() {
		return fmt.Sprintf("its run %s ended %s at %s and the claim outlived it",
			latest.RunID, latest.Outcome(), stamp)
	}
	return fmt.Sprintf("its run %s is recorded as still in flight and last said anything at %s, so the process holding it is gone; `yoyo reconcile` settles the run itself",
		latest.RunID, stamp)
}
