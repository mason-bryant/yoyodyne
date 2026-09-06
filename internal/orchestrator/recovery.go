package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/recovery"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A run meets the network at its most expensive moments. The publish step, the
// remote checks the promotion is made against, the merge request, and every
// provider invocation all go over somebody else's connection, and until
// yoyodyne-ifd.264 a single reset at any of them ended the run: on 2026-09-03
// four runs died that way, each at its final publish or integrate step, each
// with the work already completed and some of it already reviewed, and the
// intake brake then held the whole line three times over.
//
// None of those failures judged anything. So a boundary that fails on a class
// nobody has to argue about — the connection reset, the network drop, the
// transport-level refusal internal/recovery recognizes — is waited out on a
// Fibonacci backoff and asked again, and only a run that has spent the whole of
// that boundary's window records the failure it would have recorded at once
// before.
//
// Two properties matter more than the retrying. Every wait is recorded durably
// before it is taken, so a process that dies mid-wait comes back to what it had
// spent rather than to a fresh window, and so an operator reading a run that
// succeeded can see how close it came. And exhausting the window escalates
// rather than going quiet: what the boundary would have produced is produced,
// with the attempts and the intervals named in front of it.

// recovering runs one boundary and asks it again while it goes on failing
// recoverably. Anything else — an answer, or a failure whose class says the next
// attempt earns the identical one — is returned exactly as the boundary produced
// it, which is what keeps this a wait around the existing behavior rather than a
// second opinion about it.
func (a *activeRun) recovering(ctx context.Context, boundary string, attempt func(context.Context) error) error {
	for {
		err := attempt(ctx)
		if err == nil || !recovery.Recoverable(err) {
			return err
		}
		retried, recordErr := a.recoverFrom(ctx, boundary, err)
		if recordErr != nil {
			return errors.Join(err, recordErr)
		}
		if !retried {
			return a.exhausted(boundary, err)
		}
	}
}

// recoveringValue is recovering around a boundary that answers with something.
// The value the last attempt produced is kept whichever way it went, because
// several of these report something worth having beside their failure — the
// harness commit a push made before the push itself failed, first among them.
func recoveringValue[T any](ctx context.Context, a *activeRun, boundary string, attempt func(context.Context) (T, error)) (T, error) {
	var value T
	err := a.recovering(ctx, boundary, func(ctx context.Context) error {
		var attemptErr error
		value, attemptErr = attempt(ctx)
		return attemptErr
	})
	return value, err
}

// recoverProvider decides what a provider death does once the relaunch budget is
// spent. That budget bounds how much of the provider's weather one run absorbs,
// and it is the right bound for weather nobody can classify; a death that is
// plainly a dropped connection is not weather in that sense, and stopping on it
// is exactly what cost four runs their work. So the budget is spent first, and
// then this carries on past it for as long as the failure class stays clearly
// recoverable and the window has room.
//
// It reports false for everything else, which leaves the run blocking on the
// spent budget exactly as it always did — including for a death whose class this
// build does not recognize, which is the conservative half of the decision.
func (a *activeRun) recoverProvider(ctx context.Context, failure backend.TransientFailure) (bool, error) {
	if !recovery.RecoverableDetail(failure.Detail) {
		return false, nil
	}
	return a.recoverFrom(ctx, runstate.RetryProviderInvocation, errors.New(failure.Detail))
}

// recoverFrom records one recoverable failure and waits the interval its place
// in the backoff earns. It reports false when there is no wait left to take —
// the boundary's window is spent, or the run itself is ending — which is what
// hands the failure back to the caller to report the way it always reported it.
//
// The record is written before the wait, not after it. That is the same
// discipline every counter here follows and it is load-bearing for the same
// reason: a process that dies in the middle of a half-hour wait must come back
// to the window it had already spent rather than to a whole one, or a boundary
// that keeps failing across a restart would retry without bound.
func (a *activeRun) recoverFrom(ctx context.Context, boundary string, cause error) (bool, error) {
	attempt := a.state.RetryAttempts(boundary) + 1
	delay := recovery.Interval(attempt)
	if a.state.RetryWaited(boundary)+delay > recovery.Window {
		return false, nil
	}
	if err := a.recordRetry(boundary, attempt, delay, cause); err != nil {
		return false, err
	}
	if err := a.pipeline.sleep(ctx, delay); err != nil {
		// The run is over — cancelled, or out of time — so the boundary is left as
		// it failed rather than asked again under a context that has already ended.
		return false, nil
	}
	return true, nil
}

// recordRetry appends one waited-out failure to the run's record.
func (a *activeRun) recordRetry(boundary string, attempt int, delay time.Duration, cause error) error {
	retry := runstate.Retry{
		Boundary:     boundary,
		Attempt:      attempt,
		DelaySeconds: int64(delay / time.Second),
		At:           a.pipeline.clock().Now(),
		Failure:      boundedFailureDetail(cause.Error()),
	}
	a.state.Retries = append(a.state.Retries, retry)
	a.state.UpdatedAt = retry.At
	a.outcome.Retries = a.state.Retries
	if err := a.pipeline.Store.Save(a.state); err != nil {
		return fmt.Errorf("record the wait before asking again after %s failed: %w", boundary, err)
	}
	return nil
}

// exhausted is what a boundary that went on failing recoverably produces. It
// wraps the failure rather than replacing it, so everything downstream — an
// outstanding publication, a blocker on the item, a reported failure — reads as
// what it always read, with the attempts and the time in front of it. That is
// the escalation: a run handed to a person says the network was retried and for
// how long, rather than reporting the last reset as though it were the first.
func (a *activeRun) exhausted(boundary string, cause error) error {
	attempts := a.state.RetryAttempts(boundary)
	waited := a.state.RetryWaited(boundary)
	if attempts == 0 {
		// Nothing was waited at all, which is a run that ended under the boundary
		// rather than a window that ran out. Saying it was retried would be untrue.
		return cause
	}
	return fmt.Errorf("%s kept failing on something a later attempt could have survived, and %d retr(ies) over %s did not outlast it, so it is handed to a person rather than retried further: %w",
		boundary, attempts, waited.Round(time.Second), cause)
}

// recovering is the same rule where a sweep meets the network rather than where
// a run does. A settlement finishes the publication its run could not, so it
// reaches the same forge over the same connection, and until yoyodyne-ifd.301 a
// single reset there was recorded as an outstanding publication at once — twice
// on yoyodyne-ifd.295, on the one step that was left.
//
// It is used at that step and no other. A sweep settles its runs one at a time
// under each one's lease, so a window waited out here holds up every run behind
// it; that is worth paying where the alternative is a leftover only a person can
// remove, and is not worth paying for a reading the next sweep takes again.
//
// It waits on the boundary's own window, which is durable on the run, so a sweep
// that takes up where a previous one left off spends what is left rather than a
// whole window again. The record is written before the wait for the reason the
// run's is, and the state the caller holds is advanced with it: this is the
// record the settlement goes on to save.
func (r Reconciler) recovering(ctx context.Context, state *runstate.State, boundary string, attempt func(context.Context) error) error {
	for {
		err := attempt(ctx)
		if err == nil || !recovery.Recoverable(err) {
			return err
		}
		waited := state.RetryAttempts(boundary)
		delay := recovery.Interval(waited + 1)
		if state.RetryWaited(boundary)+delay > recovery.Window {
			return reconcilerExhausted(*state, boundary, err)
		}
		at := r.clock().Now()
		state.Retries = append(state.Retries, runstate.Retry{
			Boundary:     boundary,
			Attempt:      waited + 1,
			DelaySeconds: int64(delay / time.Second),
			At:           at,
			Failure:      boundedFailureDetail(err.Error()),
		})
		state.UpdatedAt = at
		if saveErr := r.Store.Save(*state); saveErr != nil {
			return errors.Join(err, fmt.Errorf("record the wait before asking again after %s failed: %w", boundary, saveErr))
		}
		if sleepErr := r.sleep(ctx, delay); sleepErr != nil {
			// The sweep is over — cancelled, or out of time — so the boundary is left
			// as it failed rather than asked again under a context that has ended.
			return err
		}
	}
}

// reconcilerExhausted is activeRun.exhausted for a sweep: the same words, over
// the record the sweep is holding rather than the one a run owns.
func reconcilerExhausted(state runstate.State, boundary string, cause error) error {
	attempts := state.RetryAttempts(boundary)
	waited := state.RetryWaited(boundary)
	if attempts == 0 {
		return cause
	}
	return fmt.Errorf("%s kept failing on something a later attempt could have survived, and %d retr(ies) over %s did not outlast it, so it is handed to a person rather than retried further: %w",
		boundary, attempts, waited.Round(time.Second), cause)
}

// sleep waits out one interval, cut short by a cancelled context so a shutdown
// is never held up by a wait the backoff has grown to half an hour.
func (r Reconciler) sleep(ctx context.Context, duration time.Duration) error {
	if r.Sleep != nil {
		return r.Sleep(ctx, duration)
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// renderRetryNotes carries the recoverable failures a run waited out onto the
// work item, one line per boundary with the attempts, the intervals, and the
// last failure that was waited out.
//
// It is per boundary rather than per retry deliberately. Twenty lines saying a
// connection was reset is an outage said twenty times, and what a reader of the
// item needs is that a boundary was retried, how long for, and what it was
// failing on; the run's own record holds each one whole for whoever wants them.
func renderRetryNotes(outcome Outcome) []string {
	if len(outcome.Retries) == 0 {
		return nil
	}
	var order []string
	byBoundary := map[string][]runstate.Retry{}
	for _, retry := range outcome.Retries {
		if _, seen := byBoundary[retry.Boundary]; !seen {
			order = append(order, retry.Boundary)
		}
		byBoundary[retry.Boundary] = append(byBoundary[retry.Boundary], retry)
	}
	lines := make([]string, 0, len(order))
	for _, boundary := range order {
		retries := byBoundary[boundary]
		intervals := make([]string, 0, len(retries))
		var waited time.Duration
		for _, retry := range retries {
			intervals = append(intervals, retry.Delay().String())
			waited += retry.Delay()
		}
		lines = append(lines, fmt.Sprintf("Waited out a recoverable failure while %s: %d retr(ies) over %s, waiting %s; last failure: %s",
			boundary, len(retries), waited.Round(time.Second), strings.Join(intervals, ", "), retries[len(retries)-1].Failure))
	}
	return lines
}

// boundedFailureDetail keeps one recorded failure inside the bound the run state
// holds retries to, cut on a rune boundary so what is kept stays text.
func boundedFailureDetail(detail string) string {
	if len(detail) <= runstate.MaxRetryFailureBytes {
		return detail
	}
	cut := runstate.MaxRetryFailureBytes
	for cut > 0 && !utf8.RuneStart(detail[cut]) {
		cut--
	}
	return detail[:cut]
}
