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
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// killedShow is what the tracker's own client reports for a `bd show` the
// harness stopped on time: nothing judged anything, the store was busy, and the
// next attempt is as likely to answer as this one was not. It is the exact shape
// of the failure that ended three runs in two days.
func killedShow() error {
	return errors.New("bd show failed with status timed_out and exit code -1: signal: killed")
}

// A tracker read that times out at the reviewing boundary is waited out rather
// than allowed to end the run. This is the case the whole boundary exists for:
// run-b0b6d18d died exactly here on yoyodyne-ifd.436.4, with the change already
// approved and the promotion the only step left, on one `bd show` that a second
// attempt would have survived.
func TestATimedOutDependencyReadAtTheReviewingBoundaryIsWaitedOutAndTheRunPromotes(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	tracker.transientShowErr = killedShow()

	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	// The store goes busy the moment the reviewer has answered, so the two reads
	// it refuses are the ones the promotion makes — the boundary the approved
	// change is lost at rather than one earlier in the run.
	reviewed := provider.run
	provider.run = func(request backend.RunRequest) (backend.RunResult, error) {
		result, err := reviewed(request)
		if request.Role == domain.RoleReviewer {
			tracker.showFailures = 2
		}
		return result, err
	}
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider)
	var waits []time.Duration
	pipeline.Sleep = func(_ context.Context, delay time.Duration) error {
		waits = append(waits, delay)
		return nil
	}

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Paused || outcome.Integration == nil || outcome.Status != runstate.StatusSucceeded {
		t.Fatalf("outcome = %#v, want the run to have carried on to its promotion", outcome)
	}
	if !tracker.closed {
		t.Fatal("the work item was not closed, so the run did not finish")
	}
	// Continued on the same session, by the same run, with neither role asked
	// again: a read that had to be waited out costs nothing but the wait.
	if outcome.ProviderSessionID != "developer-session" {
		t.Errorf("session = %q, want the run continued on the developer session it already had", outcome.ProviderSessionID)
	}
	if developed := len(provider.requestsForRole(domain.RoleDeveloper)); developed != 1 {
		t.Errorf("developer invocations = %d, want the waited-out read to have bought no further attempt", developed)
	}
	if judged := len(provider.requestsForRole(domain.RoleReviewer)); judged != 1 {
		t.Errorf("reviewer invocations = %d, want the change judged once", judged)
	}
	if tracker.showFailures != 0 {
		t.Errorf("unused refusals = %d, want both of them met", tracker.showFailures)
	}

	// Each wait is recorded on the run before it is taken, which is what lets a
	// process that dies mid-wait come back to the window it had already spent.
	var taken []runstate.Retry
	for _, retry := range outcome.Retries {
		if retry.Boundary == runstate.RetryDependencyRead {
			taken = append(taken, retry)
		}
	}
	if len(taken) != 2 {
		t.Fatalf("recorded waits at the dependency read = %d, want the two that were taken: %#v", len(taken), outcome.Retries)
	}
	if taken[0].Attempt != 1 || taken[1].Attempt != 2 {
		t.Errorf("recorded attempts = %d and %d, want them counted in order", taken[0].Attempt, taken[1].Attempt)
	}
	for _, retry := range taken {
		if !strings.Contains(retry.Failure, "timed_out") {
			t.Errorf("recorded failure = %q, want the tracker's own message", retry.Failure)
		}
	}
	// The Fibonacci series every other recoverable boundary gets, and no other.
	if len(waits) != 2 || waits[0] != time.Second || waits[1] != time.Second {
		t.Errorf("waits = %v, want the first two intervals of the shared series", waits)
	}
	// And the item says the read was waited out, which is the only sign from the
	// outside that a store under load nearly cost a finished change.
	if !strings.Contains(tracker.notes, runstate.RetryDependencyRead) {
		t.Errorf("item notes = %q, want them to name the boundary that was waited out", tracker.notes)
	}
}

// A tracker that goes on refusing for the whole window parks the run rather than
// failing it. Two hours of a store nobody can read says nothing about the
// change, so what the run keeps is everything it would need to carry on: its
// claim, its branch, its worktree, and its developer session.
func TestATrackerReadThatSpendsItsWholeWindowParksTheRunAndTheStoreAnsweringResumesIt(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	tracker.transientShowErr = killedShow()

	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	reviewed := provider.run
	busy := false
	provider.run = func(request backend.RunRequest) (backend.RunResult, error) {
		result, err := reviewed(request)
		if request.Role == domain.RoleReviewer && !busy {
			// Far more refusals than the window has room for, so what ends the
			// asking is the window rather than the fake running out of answers. It
			// is armed once: the store comes back before the run is picked up again.
			busy = true
			tracker.showFailures = 1000
		}
		return result, err
	}
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"exit 0"}), provider)
	pipeline.Sleep = func(context.Context, time.Duration) error { return nil }

	parked, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !parked.Paused || parked.Status != runstate.StatusRunning {
		t.Fatalf("outcome = %#v, want a parked run still in flight rather than a failed one", parked)
	}
	if parked.PausedByTracker == nil || parked.PausedByTracker.Boundary != runstate.RetryDependencyRead {
		t.Fatalf("the parked outcome does not name the read that went unanswered: %#v", parked.PausedByTracker)
	}
	if parked.PausedByTracker.Attempts < 2 {
		t.Errorf("recorded attempts = %d, want the window's worth of them", parked.PausedByTracker.Attempts)
	}
	if parked.Integration != nil || tracker.closed || tracker.blocked {
		t.Fatalf("the park promoted, closed, or blocked the work: %#v (closed=%t blocked=%t)", parked, tracker.closed, tracker.blocked)
	}
	if !tracker.claimed {
		t.Fatal("the park gave up the claim on the work item")
	}

	parkedState, err := store.Load(parked.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if parkedState.Status.Terminal() || parkedState.TrackerPause == nil {
		t.Fatalf("parked state = %#v, want a run still in flight carrying its park", parkedState)
	}
	if _, err := os.Stat(parkedState.WorktreePath); err != nil {
		t.Fatalf("the parked run's worktree did not survive: %v", err)
	}
	if parkedState.ProviderSessionID == "" || parkedState.Branch == "" {
		t.Fatalf("parked state = %#v, want the branch and developer session preserved", parkedState)
	}
	// The sweep leaves it where it is rather than settling it, which is the whole
	// difference between a park and the cancelled run it replaces.
	reconciler := Reconciler{Tracker: tracker, Worktrees: newObserver(t, repository, worktreeRoot), Store: store}
	results, err := reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 1 || results[0].Action != ActionResumable {
		t.Fatalf("reconciliations = %#v, want the parked run left resumable", results)
	}

	// The store answering is what lifts the park: the same run carries on in the
	// worktree and on the session it already had, and the developer is not asked
	// for an attempt it already made. The gate itself is re-earned rather than
	// skipped, which is what every park short of the promotion costs.
	tracker.showFailures = 0
	resumed, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("resumed Run() error = %v", err)
	}
	if resumed.RunID != parked.RunID {
		t.Fatalf("resumed run = %q, want the parked run %q continued rather than a new one", resumed.RunID, parked.RunID)
	}
	if resumed.Paused || resumed.Integration == nil || !tracker.closed {
		t.Fatalf("the resumed run did not finish: %#v (closed=%t)", resumed, tracker.closed)
	}
	if resumed.ProviderSessionID != parked.ProviderSessionID {
		t.Errorf("session = %q, want the parked run's own session %q", resumed.ProviderSessionID, parked.ProviderSessionID)
	}
	if developed := len(provider.requestsForRole(domain.RoleDeveloper)); developed != 1 {
		t.Errorf("developer invocations = %d, want the resumed run to continue with the change it preserved", developed)
	}
	finished, err := store.Load(resumed.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if finished.TrackerPause != nil {
		t.Fatalf("a finished run still carries a tracker park: %#v", finished.TrackerPause)
	}
}
