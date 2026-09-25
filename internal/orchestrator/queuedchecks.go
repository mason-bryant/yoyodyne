package orchestrator

// A queued merge is read with its checks.
//
// Until yoyodyne-ifd.429.16 a merge the forge had queued was read for one thing
// only — whether it was still queued — and the record never carried whether it
// could land. Pull request 609 sat queued 33 hours against a red build; 713 sat
// 31 commits behind main failing two tests its change never touched, and the
// development manager waited on it because the record said the merge was
// queued; on 2026-09-25 four requests sat that way past every later merge, and
// each cost a person a look at the forge. A merge the forge is holding for
// checks that will never pass is not a merge that is going to happen.
//
// So every sweep that finds a merge still queued reads the head's checks too,
// writes them onto the publication, and decides on them:
//
//   - Checks passing or still running: the merge stays queued, with the
//     reading beside it.
//   - A head behind its target whose failing checks name no file the change
//     touches: the failure is one the change met rather than brought, and the
//     remedy is the one a promotion that lost its race takes. The queued merge
//     is withdrawn and the run is put back at its promotion, where the target
//     is found moved and the change is replayed onto it, checked and reviewed
//     again, and queued again — under the same integration-retry budget a
//     replay spends, and hosted by the sweep that hosts runs.
//   - Anything else red — a failing check naming a file the change touches, or a
//     head already level with its target, where nothing but the change differs
//     — is the change's own failure, or one no update can fix. The queued merge
//     is withdrawn and the item is handed back with the check named, as a
//     dropped merge is, rather than left queued.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/oneline"
	"github.com/mason-bryant/yoyodyne/internal/publish"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// ReconcileChecks is the forge access reading a queued merge's checks needs:
// the checks themselves, and withdrawing the queued merge before the request is
// handed back or its head rewritten. A merge left armed across either would be
// landed by the forge the moment its checks turned green — for a rewritten head,
// a change no reviewer has seen.
//
// It is satisfied by publish.GitHub.
type ReconcileChecks interface {
	Checks(ctx context.Context, number int, base string) (publish.CheckReading, error)
	DisableAutoMerge(ctx context.Context, number int) error
}

// ActionUpdating reports a queued merge whose head fell behind its target and
// failed checks the change does not touch, which the sweep withdrew and put back
// at its promotion to be replayed onto the target and queued again. The run is
// live again and is hosted by ContinueUpdates.
const ActionUpdating ReconcileAction = "updating"

// settleStillQueued is the reading of a merge the forge still holds: its checks
// are read, written onto the publication, and decided on.
//
// A reconciler wired without check access reads the merge as it always did. A
// reading that fails leaves the merge queued and says so, and nothing is
// written: a check state nobody could read is not a red one.
func (r Reconciler) settleStillQueued(ctx context.Context, state runstate.State) (Reconciliation, error) {
	published := *state.PullRequest
	target := state.Integration.TargetBranch
	result := reconciliationOf(state, ActionQueued)
	result.Detail = fmt.Sprintf("the forge still has the merge of pull request %d into %s queued", published.Number, target)
	if r.Checks == nil {
		return result, nil
	}
	reading, err := r.Checks.Checks(ctx, published.Number, target)
	if err != nil {
		result.Detail += fmt.Sprintf("; its checks could not be read (%v), so it is left queued and the next sweep reads them again", err)
		return result, nil
	}
	checks := recordedChecks(reading, r.clock().Now())
	published.Checks = &checks
	state.PullRequest = &published
	state.UpdatedAt = r.clock().Now()
	if err := r.Store.Save(state); err != nil {
		return reconciliationOf(state, ActionUnsettled), fmt.Errorf("record the checks of pull request %d on run %s: %w", published.Number, state.RunID, err)
	}
	result = reconciliationOf(state, ActionQueued)
	result.Detail = fmt.Sprintf("the forge still has the merge of pull request %d into %s queued; %s", published.Number, target, checks.Describe(target))
	switch {
	case !checks.Red():
		return result, nil
	case checks.BehindBy > 0 && !checks.ChangeFails():
		return r.updateQueuedHead(ctx, state, result)
	case checks.ChangeFails():
		return r.handBackRedMerge(ctx, state, fmt.Sprintf(
			"the forge's checks on pull request %d fail on this change: %s. The harness withdrew the queued merge rather than leave a red change queued, and the pull request needs its change repaired",
			published.Number, checks.Describe(target)))
	default:
		return r.handBackRedMerge(ctx, state, fmt.Sprintf(
			"the forge's checks on pull request %d fail with its head level with %s, so nothing but this change differs from the target and bringing it up to date would change nothing: %s. The harness withdrew the queued merge rather than leave a red change queued, and the pull request needs a person",
			published.Number, target, checks.Describe(target)))
	}
}

// recordedChecks is the forge's reading as the record keeps it: each failing
// check with the files its annotations named, and which of those the change
// itself touches.
func recordedChecks(reading publish.CheckReading, now time.Time) runstate.PullRequestChecks {
	touched := make(map[string]bool, len(reading.Files))
	for _, file := range reading.Files {
		touched[file] = true
	}
	checks := runstate.PullRequestChecks{
		HeadCommit: reading.HeadCommit,
		ReadAt:     now.UTC(),
		Pending:    len(reading.Pending),
		Passing:    reading.Passing,
		BehindBy:   reading.BehindBy,
	}
	for _, failed := range reading.Failing {
		if len(checks.Failing) == runstate.MaxRecordedFailingChecks {
			break
		}
		failing := runstate.FailingCheck{Name: boundedCheckName(failed.Name)}
		for _, path := range failed.Paths {
			if touched[path] && len(failing.OnChange) < runstate.MaxRecordedCheckPaths {
				failing.OnChange = append(failing.OnChange, path)
			}
			if len(failing.Paths) < runstate.MaxRecordedCheckPaths {
				failing.Paths = append(failing.Paths, path)
			}
		}
		sort.Strings(failing.OnChange)
		checks.Failing = append(checks.Failing, failing)
	}
	return checks
}

func boundedCheckName(name string) string {
	if name = oneline.Bound(name, 200); name == "" {
		return "an unnamed check"
	}
	return name
}

// handBackRedMerge withdraws a red queued merge and settles the run as the
// dropped merge it now is, with the checks named: the item is handed to a
// person with a durable blocker, and the publication goes on the docket where
// triage's bounded re-arm is decided.
//
// The merge is withdrawn before anything is written. A withdrawal that fails
// leaves the record queued and the next sweep asks again; a record written
// first would hand back a merge the forge could still land.
func (r Reconciler) handBackRedMerge(ctx context.Context, state runstate.State, reason string) (Reconciliation, error) {
	published := *state.PullRequest
	if err := r.Checks.DisableAutoMerge(ctx, published.Number); err != nil {
		return reconciliationOf(state, ActionUnsettled), fmt.Errorf("withdraw the red queued merge of pull request %d for run %s: %w", published.Number, state.RunID, err)
	}
	published.MergeQueued = false
	state.PullRequest = &published
	state.PublishFailure = reason
	state.MergeDrop = &runstate.MergeDrop{At: r.clock().Now(), Reason: reason}
	return r.settleDroppedMerge(ctx, state)
}

// updateQueuedHead brings a queued head that fell behind its target and failed
// checks it did not bring back to its promotion, where the pipeline replays it
// onto the target, re-earns the checks and the review, and queues the merge
// again.
//
// What it will not do is hand the replay a budget the run has spent, or a run it
// cannot replay: a local promotion is already on the target and has no head to
// rewrite, and a run whose worktree, branch, or sessions are gone has nothing to
// replay from. Those are handed back as a red merge is. Where the replay is
// possible and the moment is not — no sweep hosting runs, intake held, every
// slot taken — the merge is left queued, saying why, for the next sweep.
func (r Reconciler) updateQueuedHead(ctx context.Context, state runstate.State, result Reconciliation) (Reconciliation, error) {
	published := *state.PullRequest
	target := state.Integration.TargetBranch
	describe := published.Checks.Describe(target)
	if refusal := unreplayable(state); refusal != "" {
		return r.handBackRedMerge(ctx, state, fmt.Sprintf(
			"the forge's checks on pull request %d fail on files this change does not touch while its head is behind %s, and the harness cannot bring it up to date: %s: %s. The harness withdrew the queued merge rather than leave a red change queued, and the pull request needs a person",
			published.Number, target, refusal, describe))
	}
	if state.IntegrationRetries >= r.IntegrationRetries {
		return r.handBackRedMerge(ctx, state, fmt.Sprintf(
			"the forge's checks on pull request %d fail on files this change does not touch while its head is behind %s, and bringing it up to date would be integration retry %d of %d permitted: %s. The harness withdrew the queued merge rather than leave a red change queued, and the pull request needs a person",
			published.Number, target, state.IntegrationRetries+1, r.IntegrationRetries, describe))
	}
	waiting := func(why string) (Reconciliation, error) {
		result.Detail += fmt.Sprintf("; its head is to be brought up to date onto %s, and %s, so it is left queued for the next sweep", target, why)
		return result, nil
	}
	if !r.HostsRuns {
		return waiting("this pass hosts no runs — `yoyo reconcile` does")
	}
	if r.Intake != nil {
		hold, held, err := r.Intake.Held()
		if err != nil {
			return waiting(fmt.Sprintf("whether intake is held could not be read (%v)", err))
		}
		if held {
			return waiting(fmt.Sprintf("intake is held (%s)", nonEmpty(hold.Reason, "no reason recorded")))
		}
	}
	if free, err := r.slotFree(); err != nil || !free {
		if err != nil {
			return waiting(fmt.Sprintf("what is in flight could not be read (%v)", err))
		}
		return waiting("every developer slot is taken")
	}

	// The merge is withdrawn first, because the replay rewrites the head: a merge
	// left armed would land the rewritten head the moment its checks passed,
	// before any reviewer had seen it. A withdrawal that fails writes nothing.
	if err := r.Checks.DisableAutoMerge(ctx, published.Number); err != nil {
		return reconciliationOf(state, ActionUnsettled), fmt.Errorf("withdraw the queued merge of pull request %d before updating run %s: %w", published.Number, state.RunID, err)
	}
	reason := fmt.Sprintf("the forge's checks on pull request %d fail on files this change does not touch while its head is %d commit(s) behind %s (%s), so the reconcile sweep withdrew the queued merge and put the run back at its promotion to be brought up to date onto %s, checked and reviewed again, and queued again, as a replay is; this is integration retry %d of %d permitted",
		published.Number, published.Checks.BehindBy, target, describe, target, state.IntegrationRetries+1, r.IntegrationRetries)
	// The item is told first, as a resumption tells it first: a run made live
	// behind an item that says nothing about it is one nobody reading the item
	// can account for.
	if _, err := r.Tracker.RecordOutcome(ctx, state.WorkItemID, renderQueuedUpdateNotes(state, reason)); err != nil {
		return reconciliationOf(state, ActionUnsettled), fmt.Errorf("record the update of run %s: %w", state.RunID, err)
	}
	now := r.clock().Now()
	resumed := state
	resumed.IntegrationResumptions = append(append([]runstate.IntegrationResumption{}, state.IntegrationResumptions...),
		runstate.IntegrationResumption{
			Cause:             runstate.CauseQueuedHeadBehind,
			Reason:            boundedReason(reason),
			ResumedAt:         now,
			SupersededFailure: state.Failure,
			SupersededBlocker: state.Blocker,
		})
	// The promotion the queued merge carried is taken back off the record: the
	// change is about to be replayed onto a new base, and what is promoted and
	// queued afterwards is the replayed change, recorded by the run that makes it.
	resumed.Integration = nil
	published.MergeQueued = false
	// The reading was about the head the replay is about to replace, and its
	// account travels on the resumption above; left on the publication it would
	// be said beside the replayed head's queued merge as though it were that
	// head's, until a sweep read the new one.
	published.Checks = nil
	resumed.PullRequest = &published
	resumed.PublishFailure = ""
	resumed.Failure = ""
	resumed.Blocker = ""
	resumed.Status = runstate.StatusRunning
	resumed.Phase = runstate.PhaseIntegrating
	resumed.CompletedAt = nil
	resumed.SettledQuietSince = nil
	resumed.UpdatedAt = now
	if err := r.Store.Save(resumed); err != nil {
		return reconciliationOf(state, ActionUnsettled), fmt.Errorf("put run %s back at its promotion, whose queued merge the forge no longer holds: %w", state.RunID, err)
	}
	updating := reconciliationOf(resumed, ActionUpdating)
	updating.Detail = reason
	return updating, nil
}

// unreplayable says why a queued run cannot be put back at its promotion, and
// nothing for one that can.
func unreplayable(state runstate.State) string {
	switch {
	case !state.Integration.ThroughPullRequest:
		return "its change was promoted onto the local target already, so there is no unlanded head to bring up to date"
	case state.WorktreePath == "" || state.WorktreeRemoved:
		return "its worktree is gone"
	case state.Branch == "" || state.BranchRemoved:
		return "its branch is gone"
	case state.ReviewDecision != runstate.ReviewApprove || strings.TrimSpace(state.ReviewSessionID) == "":
		return "its record carries no standing approval"
	case strings.TrimSpace(state.ProviderSessionID) == "":
		return "its record names no developer session"
	case state.ResumptionsLeft() == 0:
		return fmt.Sprintf("its promotion has been resumed %d times, which is the record's bound", runstate.MaxIntegrationResumptions)
	}
	return ""
}

// boundedReason keeps a resumption's reason inside the record's bound.
func boundedReason(reason string) string {
	return oneline.Bound(reason, runstate.MaxSelectionReasonBytes)
}

// slotFree counts the runs in flight against the configured limit, from the same
// records the reservation counts. A reconciler with no limit set has no room.
func (r Reconciler) slotFree() (bool, error) {
	if r.Capacity <= 0 {
		return false, nil
	}
	outstanding, err := r.Store.Outstanding()
	if err != nil {
		return false, err
	}
	inFlight := 0
	for _, state := range outstanding {
		if state.Status.InFlight() {
			inFlight++
		}
	}
	return inFlight < r.Capacity, nil
}

// renderQueuedUpdateNotes tells the work item its queued merge was withdrawn to
// bring its head up to date.
func renderQueuedUpdateNotes(state runstate.State, reason string) string {
	return strings.Join([]string{
		"Yoyodyne withdrew the merge this run left queued with the forge, to bring its head up to date onto the target.",
		"Outcome: " + reason,
		"Run: " + state.RunID,
		fmt.Sprintf("Pull request: #%d %s", state.PullRequest.Number, state.PullRequest.URL),
		"The change is replayed onto the target, checked and reviewed again, and its merge queued again by the same run; if the replay conflicts or the review does not approve it, the run stops and the item is handed back as any run's is.",
	}, "\n")
}

// updatingQueuedHead reports a run the sweep put back at its promotion to bring
// its queued head up to date, and that nothing has taken up yet.
func updatingQueuedHead(state runstate.State) bool {
	return resumableIntegration(state) && state.UpdatingQueuedHead()
}

// UpdateContinuation is what the sweep did about one run it put back at its
// promotion to bring a queued head up to date.
type UpdateContinuation struct {
	RunID      string `json:"run_id"`
	WorkItemID string `json:"work_item_id"`
	// Continued reports the run handed to its pipeline. A run left where it was
	// says why in Detail and is not a failure.
	Continued bool     `json:"continued"`
	Detail    string   `json:"detail,omitempty"`
	Outcome   *Outcome `json:"outcome,omitempty"`
	Failure   string   `json:"failure,omitempty"`
}

// ContinueUpdates hosts every run the sweep put back at its promotion to bring a
// queued head up to date and that no process holds. It is the same hosting
// ContinueWaits gives a wait: the run's own pipeline re-enters the run named,
// through Pipeline.Continue, in its own worktree — where the promotion finds the
// target moved, replays the change onto it, and re-earns the gate. A sweep wired
// with no Continue takes this step as a reading and continues nothing.
//
// The run is asked for under its lease and continued after the lease is
// released, because continuing it is the pipeline adopting it. A run a process
// holds is that process's; one that is no longer waiting at its promotion is
// left as it stands.
func (r Reconciler) ContinueUpdates(ctx context.Context) ([]UpdateContinuation, error) {
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
	var results []UpdateContinuation
	var launched []int
	for _, recorded := range outstanding {
		if !updatingQueuedHead(recorded) {
			continue
		}
		result := UpdateContinuation{RunID: recorded.RunID, WorkItemID: recorded.WorkItemID}
		state, lease, err := r.Store.AdoptRun(ctx, recorded.RunID)
		switch {
		case errors.Is(err, runstate.ErrRunHeld):
			result.Detail = "a live process holds this run, so it is updating it itself"
		case err != nil:
			result.Failure = fmt.Errorf("adopt run %s: %w", recorded.RunID, err).Error()
		case !updatingQueuedHead(state):
			lease.Release()
			result.Detail = "the run is no longer waiting at its promotion, so it is left as it stands"
		default:
			lease.Release()
			result.Continued = true
			result.Detail = state.IntegrationResumptions[len(state.IntegrationResumptions)-1].Reason
		}
		results = append(results, result)
		if result.Continued {
			launched = append(launched, len(results)-1)
		}
	}
	var wait sync.WaitGroup
	for _, index := range launched {
		wait.Add(1)
		go func(result *UpdateContinuation) {
			defer wait.Done()
			outcome, err := r.Continue(ctx, result.WorkItemID, result.RunID)
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

var _ ReconcileChecks = publish.GitHub{}
