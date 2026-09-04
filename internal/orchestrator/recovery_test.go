package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/recovery"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The failure this is about: on 2026-09-03 four runs died at their final publish
// or integrate step, each on one connection reset the next attempt would have
// survived, each with the work completed and some of it already reviewed. A
// single reset must no longer be able to record that as a failure.
func TestOneConnectionResetAtTheForgeNoLongerLosesCompletedWork(t *testing.T) {
	t.Parallel()

	for _, boundary := range []struct {
		name      string
		forge     func(remote string) *fakeForge
		retriedAt string
	}{
		{
			name:      "opening the pull request",
			forge:     func(remote string) *fakeForge { return &fakeForge{remote: remote, ensureResets: 1} },
			retriedAt: runstate.RetryOpenPullRequest,
		},
		{
			name:      "merging the pull request",
			forge:     func(remote string) *fakeForge { return &fakeForge{remote: remote, mergeResets: 1} },
			retriedAt: runstate.RetryMerge,
		},
	} {
		t.Run(boundary.name, func(t *testing.T) {
			t.Parallel()

			repository, remote := publishedRepository(t)
			tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
			forge := boundary.forge(remote)
			provider := roleBackend(func(request backend.RunRequest) error {
				return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
			}, approveVerdict)
			pipeline, store := newPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})
			pipeline = waiting(pipeline, &pausingClock{now: baseTime}, 6*time.Hour, 6*time.Hour)

			outcome, err := pipeline.Run(context.Background(), "yoyodyne-task")
			if err != nil {
				t.Fatalf("Run() error = %v, want the reset waited out", err)
			}
			if outcome.PublishFailure != "" {
				t.Fatalf("publish failure = %q, want the reset to have cost nothing", outcome.PublishFailure)
			}
			if outcome.PullRequest == nil || !outcome.PullRequest.Merged {
				t.Fatalf("pull request = %#v, want the approved request merged", outcome.PullRequest)
			}
			// The retry is visible in the run's own record, with the interval it
			// waited: a wait nobody can see is a run that merely looks slow.
			state, err := store.Load(outcome.RunID)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if attempts := state.RetryAttempts(boundary.retriedAt); attempts != 1 {
				t.Fatalf("retries recorded at %q = %d, want exactly the one reset that was waited out: %#v",
					boundary.retriedAt, attempts, state.Retries)
			}
			retry := state.Retries[0]
			if retry.Attempt != 1 || retry.Delay() != recovery.Interval(1) {
				t.Errorf("retry = %#v, want the first interval of the backoff, %s", retry, recovery.Interval(1))
			}
			if !strings.Contains(retry.Failure, "Connection reset by peer") {
				t.Errorf("recorded failure = %q, want the transport's own words", retry.Failure)
			}
			if !strings.Contains(tracker.notes, "Waited out a recoverable failure while "+boundary.retriedAt) {
				t.Errorf("the work item does not say the network was waited out:\n%s", tracker.notes)
			}
		})
	}
}

// A forge that never comes back has to escalate rather than retry for ever, and
// what it escalates has to say the network was retried: a run handed to a person
// reporting the last reset as though it were the first is what took a day to
// diagnose.
func TestASpentRecoveryWindowEscalatesWithTheRetriesNamed(t *testing.T) {
	t.Parallel()

	repository, remote := publishedRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	// Far more resets than the window has room for, so what stops the run is the
	// window rather than the forge recovering.
	forge := &fakeForge{remote: remote, mergeResets: 1000}
	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, store := newPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})
	pipeline = waiting(pipeline, &pausingClock{now: baseTime}, 6*time.Hour, 6*time.Hour)

	outcome, err := pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	// The promotion is made and the item is closed: what could not finish is the
	// publication, which is exactly what an outstanding one is for.
	if outcome.PublishFailure == "" {
		t.Fatal("outcome reports no publish failure; a forge that never answered has to leave one")
	}
	if !strings.Contains(outcome.PublishFailure, "handed to a person") ||
		!strings.Contains(outcome.PublishFailure, runstate.RetryMerge) {
		t.Errorf("publish failure does not say the network was retried and given up on:\n%s", outcome.PublishFailure)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// Every wait is inside the window, and the last one reached the cap: a series
	// that stopped short of either would be bounding something else.
	waited := state.RetryWaited(runstate.RetryMerge)
	if waited > recovery.Window {
		t.Errorf("waited %s at the merge, which is past the %s window", waited, recovery.Window)
	}
	if attempts := state.RetryAttempts(runstate.RetryMerge); attempts < 15 {
		t.Errorf("retries at the merge = %d over %s; the window should buy the whole series", attempts, waited)
	}
	if last := state.Retries[len(state.Retries)-1]; last.Delay() != recovery.MaxInterval {
		t.Errorf("last interval = %s, want the %s cap", last.Delay(), recovery.MaxInterval)
	}
}

// The provider half of the same rule. The relaunch budget is the right bound for
// a provider dying in ways nobody can classify; a dropped connection is not one
// of those, so the run carries on past the budget rather than blocking an item
// whose change is already written.
func TestARecoverableProviderDeathCarriesOnPastTheRelaunchBudget(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	// Four deaths against a budget of two: two relaunches, then two waits.
	provider := transientDeathBackend(4, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline.Config.Execution.TransientRelaunchesBeforeBlocking = 2
	pipeline = waiting(pipeline, &pausingClock{now: baseTime}, 6*time.Hour, 6*time.Hour)

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v, want the dropped connections waited out", err)
	}
	if tracker.blocked || outcome.Blocked {
		t.Fatalf("the run blocked the item: tracker=%t outcome=%t", tracker.blocked, outcome.Blocked)
	}
	if attempts := len(provider.requestsForRole(domain.RoleDeveloper)); attempts != 5 {
		t.Fatalf("developer invocations = %d, want the four deaths and the attempt that served the work", attempts)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// The budget is spent exactly as configured and the recovery is counted apart
	// from it: they bound different things and a reader has to be able to tell
	// which one carried the run.
	if state.TransientRelaunches != 2 {
		t.Errorf("relaunches = %d, want the configured budget spent", state.TransientRelaunches)
	}
	if attempts := state.RetryAttempts(runstate.RetryProviderInvocation); attempts != 2 {
		t.Fatalf("provider retries = %d, want the two deaths past the budget: %#v", attempts, state.Retries)
	}
	// The session is the whole reason a relaunch is worth taking, and a wait past
	// the budget must not drop it.
	if state.ProviderSessionID != provider.developerSession {
		t.Errorf("session = %q, want the reissued attempts to have continued %q", state.ProviderSessionID, provider.developerSession)
	}
}

// The conservative half: a death the harness cannot classify keeps the behavior
// it always had, so the relaunch budget goes on being the bound for everything
// the recoverable classes do not name.
func TestAnUnclassifiableProviderDeathStillBlocksOnTheRelaunchBudget(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := opaqueDeathBackend(10, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline.Config.Execution.TransientRelaunchesBeforeBlocking = 2

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err == nil {
		t.Fatal("Run() error = nil, want the budget to stop a death nothing classifies")
	}
	if !outcome.Blocked {
		t.Fatalf("outcome = %#v, want the item blocked", outcome)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(state.Retries) != 0 {
		t.Errorf("retries = %#v, want none: nothing here was classified as recoverable", state.Retries)
	}
}

// A process that dies mid-wait must come back to the window it had already
// spent. Without this the recovery is unbounded across restarts, which is the
// difference between waiting a network out and never stopping.
func TestARestartCannotBuyAFreshRecoveryWindow(t *testing.T) {
	t.Parallel()

	state := runstate.State{}
	spent := time.Duration(0)
	for attempt := 1; ; attempt++ {
		delay := recovery.Interval(attempt)
		if spent+delay > recovery.Window {
			break
		}
		state.Retries = append(state.Retries, runstate.Retry{
			Boundary: runstate.RetryMerge, Attempt: attempt,
			DelaySeconds: int64(delay / time.Second), At: baseTime,
		})
		spent += delay
	}
	if got := state.RetryWaited(runstate.RetryMerge); got != spent {
		t.Fatalf("RetryWaited() = %s, want the %s the record carries", got, spent)
	}
	// Read back off the record, the next attempt is past the window rather than
	// the first of a new series.
	next := state.RetryAttempts(runstate.RetryMerge) + 1
	if spent+recovery.Interval(next) <= recovery.Window {
		t.Fatalf("attempt %d still fits in the window after %s; the record is not bounding anything", next, spent)
	}
	// A boundary this run has not met yet keeps its own window, because a network
	// that dropped a push says nothing about a merge.
	if waited := state.RetryWaited(runstate.RetryPublishBranch); waited != 0 {
		t.Errorf("the push boundary has waited %s, want a window of its own", waited)
	}
}

func TestRecordedRetriesAreValidatedAndBounded(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("x", runstate.MaxRetryFailureBytes+64)
	if bounded := boundedFailureDetail(long); len(bounded) > runstate.MaxRetryFailureBytes {
		t.Errorf("bounded detail is %d bytes, which exceeds the %d byte bound", len(bounded), runstate.MaxRetryFailureBytes)
	}
	valid := runstate.Retry{Boundary: runstate.RetryMerge, Attempt: 1, DelaySeconds: 1, At: baseTime}
	if err := valid.Validate(); err != nil {
		t.Errorf("Validate() error = %v, want a recorded retry to be valid", err)
	}
	for _, invalid := range []runstate.Retry{
		{Attempt: 1, DelaySeconds: 1, At: baseTime},
		{Boundary: runstate.RetryMerge, Attempt: 0, DelaySeconds: 1, At: baseTime},
		{Boundary: runstate.RetryMerge, Attempt: 1, DelaySeconds: -1, At: baseTime},
		{Boundary: runstate.RetryMerge, Attempt: 1, DelaySeconds: 1},
		{Boundary: runstate.RetryMerge, Attempt: 1, DelaySeconds: 1, At: baseTime, Failure: long},
	} {
		if err := invalid.Validate(); err == nil {
			t.Errorf("Validate() error = nil for %#v, want the contract violation reported", invalid)
		}
	}
}

// Nothing that is an answer about the work may be turned into a wait, whichever
// boundary it arrives at. This is the assertion that the wrapping is a wait
// around the existing behavior rather than a second opinion about it.
func TestAForgeRefusalIsReportedAtOnceRatherThanRetried(t *testing.T) {
	t.Parallel()

	repository, remote := publishedRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	refusal := errors.New("the forge refused to merge pull request 1 with the merge method: the pull request conflicts with the base branch (DIRTY)")
	forge := &fakeForge{remote: remote, mergeErr: refusal}
	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, store := newPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})
	pipeline = waiting(pipeline, &pausingClock{now: baseTime}, 6*time.Hour, 6*time.Hour)

	outcome, err := pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(outcome.PublishFailure, "conflicts with the base branch") {
		t.Fatalf("publish failure = %q, want the forge's own refusal", outcome.PublishFailure)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(state.Retries) != 0 {
		t.Errorf("retries = %#v, want a refusal reported at once", state.Retries)
	}
}
