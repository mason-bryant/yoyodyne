package orchestrator

// The sweep continuing a run that exited on its in-process usage-limit bound.
//
// A run waiting out a provider's refusal sleeps its probes inside one process
// until that process has spent execution.usage_limit_in_process_pause on it,
// and then exits with the run still in flight and its deadline recorded. Until
// yoyodyne-ifd.428.5 only `yoyo run` on the item continued one, and a run
// nobody typed it for stood exactly as the phantom 428.4 settled for a provider
// stopped on time: no live process, no ending, a developer slot held and the
// in-flight guard refusing every item beside it. Neither that settlement nor
// the claim audit touched it, because both are right not to — nothing about
// such a run is wrong, and settling it would throw away a wait that was
// served. What it needed was continuing, and this is the sweep doing that.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// WaitContinuation is what the sweep did about one run that had exited on its
// in-process usage-limit bound with its deadline passed.
type WaitContinuation struct {
	RunID      string `json:"run_id"`
	WorkItemID string `json:"work_item_id"`
	// Waited is what the run was waiting out, in the words every other surface
	// uses for it.
	Waited string `json:"waited"`
	// Deadline is the recorded deadline that had passed, which is the condition
	// the continuation was decided on.
	Deadline time.Time `json:"deadline"`
	// Continued reports that the sweep recorded the continuation on the run and
	// handed it to its pipeline. A run left where it was — held by a process
	// that adopted it between the listing and the lease, or no longer waiting by
	// the time the lease was taken — says why in Detail and is not a failure.
	Continued bool   `json:"continued"`
	Detail    string `json:"detail,omitempty"`
	// Outcome is what the continued run came to, in full: the sweep hosts the
	// continuation exactly as `yoyo run` would, so what it reports is what that
	// verb would have reported.
	Outcome *Outcome `json:"outcome,omitempty"`
	// Failure is the pipeline's refusal or failure to continue the run, or the
	// sweep's own failure to record the continuation. A continued run that ended
	// stopped is not a failure here — its stoppage is on its item and on the
	// docket, as it would be for any run — but a continuation nothing ran is.
	Failure string `json:"failure,omitempty"`
}

// sweepContinuationEntry is how long after a sweep records a continuation the
// run is read as being entered by that sweep's pipeline rather than as a wait
// nothing is serving. It covers the gap between the sweep releasing the run's
// lease and the pipeline adopting it, which is a tracker read and a directive
// read and is over in seconds; past it, a record still standing on the same
// deadline is a continuation that was refused, and is taken up again.
const sweepContinuationEntry = time.Minute

// exitedWait reports a run asleep on a recorded usage-limit deadline that has
// already passed. Whether a process is serving the wait is not readable from
// the record — a process exited on the in-process bound leaves the record
// exactly as one still asleep does — so it is answered by the lease, which is
// why every caller takes the lease before it decides anything on this.
//
// An outage wait is excluded on purpose, though it shares the deadline field.
// It has no in-process bound, so a process serving one never exits and leaves
// it; and its deadline is the next probe into a provider answering nobody,
// which the watch session is already probing on the configured interval, so a
// sweep continuing one would be a second prober on a cadence nobody configured.
func exitedWait(state runstate.State, now time.Time) bool {
	if !pausedForUsageLimit(state) || pausedForProviderOutage(state) {
		return false
	}
	return !state.UsageLimitResetsAt.After(now)
}

// pausedForProviderOutage reports a paused run whose wait is on a provider
// answering nobody rather than on a clock.
func pausedForProviderOutage(state runstate.State) bool {
	_, away := runstate.PausedForProviderOutage(state.PauseCause)
	return away
}

// ContinueWaits continues every run that exited on its in-process usage-limit
// bound and whose recorded deadline has since passed with nothing serving the
// wait. It is the one step of the sweep that invokes a provider, and it does so
// under the rule the rest of the sweep keeps: it never starts a second
// developer for an item. What it continues is the run's own attempt, in the
// worktree and developer session the run already has, through the same
// Pipeline.Continue a triage carry-out re-enters a run by — so a run continued
// here is indistinguishable afterwards from one somebody typed `yoyo run` for,
// bar the continuation on its record saying the sweep did it.
//
// The decision is made under the run's lease and the continuation is made
// after releasing it, for the reason the repair carry-out does the same: the
// lease is what says no process is serving the wait — a process asleep on it
// holds the lease for the whole of its sleep — and continuing the run is the
// pipeline adopting it, which the sweep holding the lease would refuse. The
// continuation is written onto the record before the lease is released, so a
// run whose continuation then fails still says the sweep took it up, and the
// failure is reported beside it rather than the record saying nothing. That
// record is also what a second sweep running beside this one reads in the gap
// between the release and the adoption: it finds the continuation just
// recorded and leaves the run to the sweep that recorded it, rather than
// recording another and reporting the pipeline's refusal of it as a failure.
//
// Every continuation found is hosted at once rather than one after another: a
// continued run is a developer attempt, which takes as long as it takes, and a
// second run waiting on the first would be a second slot held for nothing. Each
// already holds its slot, so nothing here reads capacity. A sweep wired with no
// Continue takes this step as a reading and continues nothing, which is what
// the conversation's settle is.
func (r Reconciler) ContinueWaits(ctx context.Context) ([]WaitContinuation, error) {
	if err := r.validate(); err != nil {
		return nil, err
	}
	if r.Continue == nil {
		return nil, nil
	}
	outstanding, err := r.Store.Outstanding()
	if err != nil {
		return nil, fmt.Errorf("discover outstanding runs: %w", err)
	}
	var results []WaitContinuation
	var launched []int
	for _, recorded := range outstanding {
		if !exitedWait(recorded, r.clock().Now()) {
			continue
		}
		result, continued := r.takeUpWait(ctx, recorded)
		results = append(results, result)
		if continued {
			launched = append(launched, len(results)-1)
		}
	}
	// The continuations are hosted here, once every decision has been made and
	// every lease released, so a run that takes hours never holds the sweep's
	// reading of the runs beside it.
	var wait sync.WaitGroup
	for _, index := range launched {
		wait.Add(1)
		go func(result *WaitContinuation) {
			defer wait.Done()
			outcome, err := r.Continue(ctx, result.WorkItemID, result.RunID)
			// A refusal made before the run was entered carries no outcome worth
			// reporting; one made inside it — a stop, a park — does, beside the error.
			if err == nil || outcome.RunID != "" || outcome.Paused {
				result.Outcome = &outcome
			}
			if err != nil {
				result.Failure = err.Error()
			}
		}(&results[index])
	}
	wait.Wait()
	return results, nil
}

// takeUpWait decides one run under its lease and records the continuation on
// it. The second return says the run is to be continued once the lease is
// released; a run that is not carries its reason in the result.
func (r Reconciler) takeUpWait(ctx context.Context, recorded runstate.State) (WaitContinuation, bool) {
	result := WaitContinuation{
		RunID:      recorded.RunID,
		WorkItemID: recorded.WorkItemID,
		Waited:     runstate.DescribePause(recorded.PauseCause, recorded.UsageLimitKind),
		Deadline:   recorded.UsageLimitResetsAt.UTC(),
	}
	state, lease, err := r.Store.AdoptRun(ctx, recorded.RunID)
	switch {
	case errors.Is(err, runstate.ErrRunHeld):
		// A process is serving the wait after all — one still asleep inside its
		// in-process bound, or a `yoyo run` somebody typed — and the run is its.
		result.Detail = "a live process holds this run, so it is serving the wait itself"
		return result, false
	case err != nil:
		result.Failure = fmt.Errorf("adopt run %s: %w", recorded.RunID, err).Error()
		return result, false
	}
	defer lease.Release()
	// The listing is a snapshot, so the run is asked again under the lease: one
	// that cleared its pause, or parked on a later deadline, between the two is
	// left as it now stands.
	now := r.clock().Now().UTC()
	if !exitedWait(state, now) {
		result.Detail = "the run is no longer waiting past a deadline, so it is left as it stands"
		return result, false
	}
	result.Deadline = state.UsageLimitResetsAt.UTC()
	result.Waited = runstate.DescribePause(state.PauseCause, state.UsageLimitKind)
	// A continuation already recorded for this same deadline a moment ago is a
	// sweep beside this one that has released the lease and not yet had the
	// pipeline adopt the run. The lease still keeps two developers off it — the
	// second Continue would refuse on the run being held — but recording a second
	// continuation and reporting that refusal as a failure would make repeating
	// the sweep unsafe for this one step. The window is short on purpose: a
	// continuation the pipeline refused outright leaves the record exactly as
	// this reads it, and a later sweep has to take the run up again rather than
	// reading the refusal as a continuation in progress for good.
	if last, ok := state.LastSweepContinuation(); ok && last.Deadline.Equal(result.Deadline) && now.Sub(last.ContinuedAt) < sweepContinuationEntry {
		result.Detail = fmt.Sprintf("a sweep took this run up at %s and its continuation is being entered, so it is left to that sweep",
			last.ContinuedAt.UTC().Format(time.RFC3339))
		return result, false
	}
	if len(state.SweepContinuations) >= runstate.MaxSweepContinuations {
		// The record refuses one more, and a run the provider has refused on this
		// many deadlines is one the pause budget should have stopped; it is left
		// for `yoyo run` and said so, rather than the record being grown past its
		// bound or a fresh one invented.
		result.Failure = fmt.Sprintf("the sweep has continued run %s %d times, which is the record's bound; `yoyo run %s` continues it by hand",
			state.RunID, len(state.SweepContinuations), state.WorkItemID)
		return result, false
	}
	continuation := runstate.SweepContinuation{
		Cause:       state.PauseCause,
		Deadline:    result.Deadline,
		ContinuedAt: now,
		Reason:      sweepContinuationReason(state, result.Waited, result.Deadline),
	}
	if continuation.Cause == "" {
		// A record written before an overload was waitable names no cause, and
		// reads as the usage limit it always meant.
		continuation.Cause = runstate.PauseUsageLimit
	}
	state.SweepContinuations = append(append([]runstate.SweepContinuation{}, state.SweepContinuations...), continuation)
	state.UpdatedAt = now
	if err := r.Store.Save(state); err != nil {
		result.Failure = fmt.Errorf("record the sweep's continuation on run %s: %w", state.RunID, err).Error()
		return result, false
	}
	result.Continued = true
	result.Detail = continuation.Reason
	return result, true
}

// sweepContinuationReason is the sweep's account of why it continued a run
// rather than leaving it as the wait it was: what was observed, and what was
// decided from it. It is what the run's record carries, so a reader of the
// record is told the run came back on the harness's account and not a person's.
func sweepContinuationReason(state runstate.State, waited string, deadline time.Time) string {
	return fmt.Sprintf(
		"the run was recorded as paused for %s with its deadline %s passed, no live process held it, and no ending was recorded, so the reconcile sweep continued it in its own worktree and developer session rather than leaving it to hold a developer slot until somebody typed `yoyo run %s`",
		waited, deadline.Format(time.RFC3339), state.WorkItemID)
}
