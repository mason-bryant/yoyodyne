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

import "github.com/mason-bryant/yoyodyne/internal/runstate"

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

// awaitingForgeAttention is one unpublished promotion as the attention line
// carries it, with whose move it is. Four cases have four different movers: a
// merge the forge is holding is the forge's, a merge it dropped is the
// development manager's to re-arm or a person's to make by hand, a request
// nothing ever asked the forge to merge is the operator's, and a promotion
// whose record holds no request at all is the harness's — the next reconcile
// looks the request up by the run's branch and arms its merge. All four are
// settled by the same sweep once the forge records the merge, and the sentence
// Attention.Whose derives from the record says so.
func awaitingForgeAttention(state runstate.State) Attention {
	// The predicate that selects a state here requires the promotion to be
	// recorded, so it cannot be missing; a reading of every recorded run must
	// still not be able to panic on a record if that predicate is ever widened,
	// so a missing one leaves the field empty rather than being dereferenced,
	// and the sentence says so — a placeholder belongs in the sentence, not in
	// a field a surface would act on.
	publication := Publication{Branch: state.Branch}
	if state.Integration != nil {
		publication.TargetBranch = state.Integration.TargetBranch
	}
	if state.MergeDrop != nil {
		dropped := *state.MergeDrop
		publication.MergeDrop = &dropped
	}
	// The mover is read off the same fields, in the same order, that Whose
	// reads them in: a queued merge is the forge's whatever was dropped before
	// it was re-armed.
	mover := MoverHarness
	if state.PullRequest != nil {
		published := *state.PullRequest
		publication.PullRequest = &published
		switch {
		case published.MergeQueued:
			mover = MoverForge
		case publication.MergeDrop != nil:
			mover = MoverDevelopmentManager
		default:
			mover = MoverOperator
		}
	}
	return Attention{
		Kind:        AttentionPublication,
		ID:          state.RunID,
		Mover:       mover,
		WorkItemID:  state.WorkItemID,
		Publication: &publication,
	}
}
