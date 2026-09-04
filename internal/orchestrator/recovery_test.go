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
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
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

// resettingPushWorktrees drops the connection under the first branch push, after
// the push itself has been made. That is the shape the real failure takes: the
// harness commits, the push goes out, and the reply never arrives — so the
// worktree is left clean and the retry is made against a worktree that has
// nothing more to commit.
type resettingPushWorktrees struct {
	WorktreeManager
	// reported is what each PublishBranch call answered with, so a test can hold
	// the retry's answer against the one the dropped attempt gave.
	reported []gitworktree.Publication
}

func (w *resettingPushWorktrees) PublishBranch(ctx context.Context, worktree gitworktree.Worktree, message string) (gitworktree.Publication, error) {
	publication, err := w.WorktreeManager.PublishBranch(ctx, worktree, message)
	w.reported = append(w.reported, publication)
	if err == nil && len(w.reported) == 1 {
		return publication, connectionReset("push " + worktree.Branch)
	}
	return publication, err
}

// The push is the boundary whose retry rests on an assumption rather than on a
// documented contract: publishing again after a dropped push commits nothing,
// because the commit the dropped attempt made is already in the worktree, and
// answers with that same commit rather than reporting that the attempt changed
// nothing. An empty change is what publishAttempt treats as nothing to publish,
// so if that assumption were wrong the retry would silently skip opening the
// pull request and the run would publish nothing at all — a recoverable failure
// converted into an unrecoverable one, with no failure anywhere to say so.
func TestAResetPushIsRetriedAndDoesNotReadAsAnEmptyChange(t *testing.T) {
	t.Parallel()

	repository, remote := publishedRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &fakeForge{remote: remote}
	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, store := newPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})
	pushes := &resettingPushWorktrees{WorktreeManager: pipeline.Worktrees}
	pipeline.Worktrees = pushes
	pipeline = waiting(pipeline, &pausingClock{now: baseTime}, 6*time.Hour, 6*time.Hour)

	outcome, err := pipeline.Run(context.Background(), "yoyodyne-task")
	if err != nil {
		t.Fatalf("Run() error = %v, want the reset push waited out", err)
	}
	if len(pushes.reported) != 2 {
		t.Fatalf("branch pushes = %d, want the dropped one and the retry", len(pushes.reported))
	}
	// The assumption, asserted: the retry answered with the commit the dropped
	// attempt had already made, rather than reporting an empty change.
	dropped, retried := pushes.reported[0], pushes.reported[1]
	if retried.Commit == "" || retried.Commit != dropped.Commit {
		t.Fatalf("retried push reported %#v, want the commit %q the dropped push made", retried, dropped.Commit)
	}
	// And the publication actually happened, which is what an empty change would
	// have skipped without failing anything.
	if len(forge.opened) != 1 {
		t.Fatalf("pull requests opened = %d, want the retry to have opened exactly one", len(forge.opened))
	}
	if outcome.PullRequest == nil || !outcome.PullRequest.Merged || outcome.PublishFailure != "" {
		t.Fatalf("outcome = %#v, want a clean publication of the pushed commit", outcome.PullRequest)
	}
	if outcome.PullRequest.HeadCommit != dropped.Commit {
		t.Errorf("published head = %q, want the commit the dropped push made, %q", outcome.PullRequest.HeadCommit, dropped.Commit)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if attempts := state.RetryAttempts(runstate.RetryPublishBranch); attempts != 1 {
		t.Fatalf("retries recorded at the push = %d, want the one reset that was waited out: %#v", attempts, state.Retries)
	}
}

// Waiting is a gap in time, and evidence has to survive it. A merge reissued
// after a wait that can reach half an hour is a merge performed now, so the
// remote target has to be read now: an integration authorized by a check made
// before the wait is authorized by evidence a later change to the target branch
// may have invalidated, which is the case
// integration-requires-revision-bound-evidence names. The head commit pins the
// candidate and says nothing about the base, so nothing else here would catch a
// target that moved while the run waited.
func TestARetriedMergeVerifiesTheRemoteTargetAgainBeforeItIsAsked(t *testing.T) {
	t.Parallel()

	repository, remote := publishedRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	// The first merge is dropped, and the target moves during the wait that
	// follows — the window a check made in front of the retry would leave open.
	forge := &fakeForge{remote: remote, mergeResets: 1}
	forge.afterMergeReset = func() { driftRemoteTarget(t, remote, "main") }
	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, store := newPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})
	pipeline = waiting(pipeline, &pausingClock{now: baseTime}, 6*time.Hour, 6*time.Hour)

	outcome, err := pipeline.Run(context.Background(), "yoyodyne-task")
	if err == nil {
		t.Fatal("Run() error = nil, want the target that moved during the wait to stop the run")
	}
	// The retry re-read the target and refused, so the forge was never asked to
	// merge into a branch it would have reconciled itself.
	if len(forge.merges) != 0 {
		t.Fatalf("the harness merged into a target that moved while it waited: %#v", forge.merges)
	}
	if !strings.Contains(outcome.PublishFailure, "check the remote target branch before merging") {
		t.Fatalf("publish failure = %q, want the re-read that refused the retried merge", outcome.PublishFailure)
	}
	if !outcome.Blocked || !tracker.blocked {
		t.Fatalf("blocked = %t (tracker %t), want the divergence handed to a person", outcome.Blocked, tracker.blocked)
	}
	// And the wait itself is on the record, so what happened reads as a reset
	// waited out and a target that moved underneath it rather than as a bare
	// divergence.
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if attempts := state.RetryAttempts(runstate.RetryMerge); attempts != 1 {
		t.Fatalf("retries recorded at the merge = %d, want the one reset that was waited out: %#v", attempts, state.Retries)
	}
}

// dyingReviewerBackend serves the developer once and then kills the first deaths
// reviewer invocations the way a dropped connection does, approving afterwards.
func dyingReviewerBackend(deaths int) *fakeBackend {
	provider := &fakeBackend{developerSession: "developer-session", reviewerSession: "reviewer-session"}
	killed := 0
	provider.run = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role != domain.RoleReviewer {
			if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600); err != nil {
				return backend.RunResult{}, err
			}
			return backend.RunResult{
				Backend: domain.BackendClaudeCode, SessionID: provider.developerSession,
				ResolvedModel: developerResolved, FinalText: "implemented the work item",
				Process: execution.ProcessResult{Status: execution.ProcessSucceeded}, LastEvent: request.LastSequence,
			}, nil
		}
		if killed < deaths {
			killed++
			return backend.RunResult{
				Backend: domain.BackendClaudeCode, SessionID: provider.reviewerSession,
				IsError: true, StopReason: "api_error", FinalText: connectionClosedMessage,
				TransientFailure: &backend.TransientFailure{Detail: "api_error: " + connectionClosedMessage},
				Process:          execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1},
				LastEvent:        request.LastSequence,
			}, nil
		}
		return backend.RunResult{
			Backend: domain.BackendClaudeCode, SessionID: provider.reviewerSession,
			ResolvedModel: reviewerResolved, FinalText: approveVerdict,
			Process: execution.ProcessResult{Status: execution.ProcessSucceeded}, LastEvent: request.LastSequence,
		}, nil
	}
	return provider
}

// The reviewer half of the provider recovery, which is the costliest case the
// rule covers: the change is built, checked, and waiting on the one invocation
// that has to happen before it can be promoted, so a dropped connection there
// stops a run with nothing at all wrong with its work. It shares the developer's
// relaunch budget and the developer's recovery window, and neither is charged to
// the change.
func TestARecoverableReviewerDeathCarriesOnPastTheRelaunchBudget(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	// Four reviewer deaths against a budget of two: two relaunches, then two
	// waits, then the verdict.
	provider := dyingReviewerBackend(4)
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
	if outcome.Integration == nil || !outcome.WorkItemClosed {
		t.Fatalf("outcome = %#v, want the approved change promoted and the item closed", outcome)
	}
	if reviews := len(provider.requestsForRole(domain.RoleReviewer)); reviews != 5 {
		t.Fatalf("reviewer invocations = %d, want the four deaths and the one that answered", reviews)
	}
	// The change was never handed back: nothing here is a fault in the work, so
	// neither the repair budget nor the developer is charged for it.
	if outcome.RepairAttempts != 0 {
		t.Errorf("repair attempts = %d, want the provider's weather charged to nobody", outcome.RepairAttempts)
	}
	if developers := len(provider.requestsForRole(domain.RoleDeveloper)); developers != 1 {
		t.Errorf("developer invocations = %d, want the change developed once", developers)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// The budget and the window are the same two the developer's deaths spend,
	// which is what stops a run alternating between the roles absorbing twice
	// what either is worth.
	if state.TransientRelaunches != 2 {
		t.Errorf("relaunches = %d, want the configured budget spent", state.TransientRelaunches)
	}
	if attempts := state.RetryAttempts(runstate.RetryProviderInvocation); attempts != 2 {
		t.Fatalf("provider retries = %d, want the two deaths past the budget: %#v", attempts, state.Retries)
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
