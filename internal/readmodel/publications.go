package readmodel

// What is awaiting the forge, answered once.
//
// A promotion is on the local target branch the moment a run integrates it, and
// on the remote only once the forge merges the pull request that carries it. In
// between it is awaiting the forge, and so is a promotion whose merge the forge
// dropped: the change reads as landed everywhere else while the request sits on
// the forge unmerged, waiting on whoever finds out.
//
// Until yoyodyne-ifd.357 two surfaces answered "what is awaiting the forge" from
// two different readings. The channel's heartbeat counted it from the record's
// own predicate, runstate.State.AwaitingForge, and the four lines derived it from
// the runs that still owe a step — which a run settled after a dropped merge is
// not, so its unpublished promotion was in the hourly count and on no line at a
// terminal. This is the one reading both take.

import (
	"fmt"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// AwaitingForge is every recorded promotion whose publication the forge has not
// finished, by the record's own predicate and nothing else. It is a function
// over the states a caller has already read rather than a reading of its own,
// because the heartbeat counts it from the same reading it selects its
// crossings from, and two readings of the same files a moment apart could
// disagree about one run.
func AwaitingForge(states []runstate.State) []runstate.State {
	awaiting := make([]runstate.State, 0, len(states))
	for _, state := range states {
		if state.AwaitingForge() {
			awaiting = append(awaiting, state)
		}
	}
	return awaiting
}

// awaitingForgeAttention says one unpublished promotion on the attention line,
// with whose move it is. Four cases have four different movers: a merge the
// forge is holding is the forge's, a merge it dropped is the development
// manager's to re-arm or a person's to make by hand, a request nothing ever
// asked the forge to merge is the operator's, and a promotion whose record holds
// no request at all is the harness's — the next reconcile looks the request up
// by the run's branch and arms its merge. All four are settled by the same
// sweep once the forge records the merge, and the line says so, because that is
// what stops a reader going looking for a lever that is not there.
func awaitingForgeAttention(state runstate.State) Attention {
	// The predicate that selects a state here requires the promotion to be
	// recorded, so it cannot be missing; a reading of every recorded run must
	// still not be able to panic on a record if that predicate is ever widened,
	// so a missing one is named rather than dereferenced.
	target := "an unrecorded target"
	if state.Integration != nil {
		target = state.Integration.TargetBranch
	}
	if state.PullRequest == nil {
		return Attention{
			What: fmt.Sprintf("run %s promoted %s into %s and its record holds no pull request for branch %s, so nothing has asked the forge to merge it",
				state.RunID, state.WorkItemID, target, state.Branch),
			Whose: "the harness's — `yoyo reconcile` looks the request up on the forge by that branch, records it, and arms its merge; a forge that holds none is said on every sweep",
		}
	}
	published := *state.PullRequest
	what := fmt.Sprintf("run %s promoted %s into %s and the forge has not published it: pull request #%d %s",
		state.RunID, state.WorkItemID, target, published.Number, published.URL)
	switch {
	case published.MergeQueued:
		return Attention{
			What:  what,
			Whose: "the forge's — it merges once the base branch's requirements are met, and `yoyo reconcile` settles the run when it does",
		}
	case state.MergeDrop != nil:
		return Attention{
			What:  what,
			Whose: "the development manager's — the forge dropped the merge; `yoyo triage rearm` repeats it once, or a person merges the request by hand, and `yoyo reconcile` settles it once the forge records the merge",
		}
	default:
		return Attention{
			What:  what,
			Whose: "the operator's — the request is on the forge unmerged; merge it, or leave it, and `yoyo reconcile` settles it once the forge records the merge",
		}
	}
}
