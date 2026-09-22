package orchestrator

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A run that exited on its in-process usage-limit bound has the phantom shape
// 428.4 settled for a provider stopped on time — no live process, no ending, a
// developer slot held — and nothing wrong with it: it is a wait whose process
// went away. Inside the deadline it is the wait it is, and the sweep leaves it
// exactly as it stands. Past the deadline with nothing holding its lease the
// sweep continues it itself, in the same worktree and developer session, the
// run's record says the sweep did, and the wait ends with nobody typing
// `yoyo run`.
func TestTheSweepContinuesARunThatExitedOnItsInProcessUsageLimitBound(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	resetsAt := baseTime.Add(2 * time.Hour)
	limit := &backend.UsageLimit{Kind: "five_hour", ResetsAt: resetsAt}

	// The first process refuses the attempt on the limit and exits on its
	// in-process bound, leaving the run in flight with the deadline recorded.
	first := usageLimitBackend(1, limit, approveVerdict)
	firstClock := &pausingClock{now: baseTime}
	firstPipeline := waiting(automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, first, []string{"exit 0"}), first),
		firstClock, 6*time.Hour, time.Minute)
	paused, err := firstPipeline.Run(context.Background(), tracker.item.ID)
	if err != nil || !paused.Paused {
		t.Fatalf("Run() error = %v, paused = %t", err, paused.Paused)
	}
	exited, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if exited.UsageLimitResetsAt == nil || !exited.UsageLimitResetsAt.Equal(resetsAt) {
		t.Fatalf("exited run = %#v, want the deadline recorded", exited)
	}
	tracker.item.Status = "in_progress"

	// Inside the deadline the run is the wait it is, whether or not a process
	// is asleep on it, and a sweep with a continuation wired continues nothing.
	continued := 0
	serving := usageLimitBackend(0, limit, approveVerdict)
	servingClock := &pausingClock{now: resetsAt.Add(time.Minute)}
	servingPipeline := waiting(automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, serving, []string{"exit 0"}), serving),
		servingClock, 6*time.Hour, time.Minute)
	reconciler := Reconciler{
		Tracker:   tracker,
		Worktrees: newObserver(t, repository, worktreeRoot),
		Store:     store,
		Clock:     &pausingClock{now: resetsAt.Add(-time.Minute)},
		Continue: func(ctx context.Context, workItemID, runID string) (Outcome, error) {
			continued++
			return servingPipeline.Continue(ctx, workItemID, runID)
		},
	}
	results, err := reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(results) != 1 || results[0].Action != ActionResumable {
		t.Fatalf("reconciliation = %#v, want the waiting run left resumable", results)
	}
	if !strings.Contains(results[0].Detail, "can continue once it asks again") || !strings.Contains(results[0].Detail, "continues it") {
		t.Fatalf("reconciliation detail %q does not say the run is waiting and what continues it after the deadline", results[0].Detail)
	}
	early, err := reconciler.ContinueWaits(context.Background())
	if err != nil {
		t.Fatalf("ContinueWaits() error = %v", err)
	}
	if len(early) != 0 || continued != 0 {
		t.Fatalf("ContinueWaits() inside the deadline = %#v with %d continuation(s), want the wait left as it is", early, continued)
	}
	untouched, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if untouched.Status != exited.Status || untouched.UsageLimitResetsAt == nil || len(untouched.SweepContinuations) != 0 {
		t.Fatalf("a sweep inside the deadline disturbed the waiting run: %#v", untouched)
	}

	// Past the deadline, with nothing holding the lease, the settle says the
	// sweep continues it and the continuation step does.
	reconciler.Clock = servingClock
	results, err = reconciler.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("Reconcile() past the deadline error = %v", err)
	}
	if len(results) != 1 || results[0].Action != ActionResumable {
		t.Fatalf("reconciliation past the deadline = %#v, want the run left for the continuation step", results)
	}
	for _, want := range []string{"has passed with no process serving the wait", "`yoyo reconcile` continues it"} {
		if !strings.Contains(results[0].Detail, want) {
			t.Fatalf("reconciliation detail %q does not say %q", results[0].Detail, want)
		}
	}
	continuations, err := reconciler.ContinueWaits(context.Background())
	if err != nil {
		t.Fatalf("ContinueWaits() error = %v", err)
	}
	if len(continuations) != 1 || continued != 1 {
		t.Fatalf("ContinueWaits() = %#v with %d continuation(s), want the one exited run continued once", continuations, continued)
	}
	continuation := continuations[0]
	if continuation.RunID != paused.RunID || continuation.WorkItemID != tracker.item.ID || !continuation.Continued || continuation.Failure != "" {
		t.Fatalf("continuation = %#v, want the exited run continued without failure", continuation)
	}
	if !continuation.Deadline.Equal(resetsAt) || !strings.Contains(continuation.Waited, "five_hour usage limit") {
		t.Fatalf("continuation = %#v, want the recorded deadline and what was waited out", continuation)
	}
	// The wait ended without a person: the continued run is the same run, in the
	// same worktree and developer session, and it landed.
	if continuation.Outcome == nil || continuation.Outcome.RunID != paused.RunID || continuation.Outcome.Integration == nil {
		t.Fatalf("continued outcome = %#v, want the same run integrated", continuation.Outcome)
	}
	if continuation.Outcome.WorktreePath != exited.WorktreePath || continuation.Outcome.ProviderSessionID != exited.ProviderSessionID {
		t.Fatalf("continued outcome = %#v, want the exited run's worktree %q and session %q", continuation.Outcome, exited.WorktreePath, exited.ProviderSessionID)
	}
	if len(serving.requests) == 0 || serving.requests[0].SessionID != exited.ProviderSessionID {
		t.Fatalf("continued attempt requests = %#v, want the developer session the refused attempt established", serving.requests)
	}

	// The record says the sweep continued it, and says what it saw.
	landed, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if landed.Status != runstate.StatusSucceeded || landed.UsageLimitResetsAt != nil {
		t.Fatalf("landed run = %#v, want a succeeded run no longer waiting", landed)
	}
	recorded, ok := landed.LastSweepContinuation()
	if !ok || len(landed.SweepContinuations) != 1 {
		t.Fatalf("sweep continuations = %#v, want the one continuation recorded", landed.SweepContinuations)
	}
	if recorded.Cause != runstate.PauseUsageLimit || !recorded.Deadline.Equal(resetsAt) || !recorded.ContinuedAt.Equal(servingClock.now) {
		t.Fatalf("recorded continuation = %#v, want the cause, the deadline that had passed, and when the sweep took it up", recorded)
	}
	for _, want := range []string{"the reconcile sweep continued it", "no live process held it", resetsAt.UTC().Format(time.RFC3339), "own worktree and developer session"} {
		if !strings.Contains(recorded.Reason, want) {
			t.Fatalf("recorded reason %q does not say %q", recorded.Reason, want)
		}
	}
	if err := landed.Validate(); err != nil {
		t.Fatalf("landed record does not validate: %v", err)
	}

	// A further sweep finds nothing waiting and continues nothing.
	again, err := reconciler.ContinueWaits(context.Background())
	if err != nil {
		t.Fatalf("second ContinueWaits() error = %v", err)
	}
	if len(again) != 0 || continued != 1 {
		t.Fatalf("second ContinueWaits() = %#v with %d continuation(s), want nothing to continue", again, continued)
	}
}

// A run a live process is serving is that process's, however far past the
// deadline the sweep reads it: the process asleep on the wait wakes at the
// deadline itself. The sweep reports it held and continues nothing, and a
// sweep wired with no continuation continues nothing at all.
func TestTheSweepLeavesAWaitALiveProcessIsServing(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	resetsAt := baseTime.Add(2 * time.Hour)
	limit := &backend.UsageLimit{Kind: "five_hour", ResetsAt: resetsAt}
	first := usageLimitBackend(1, limit, approveVerdict)
	firstPipeline := waiting(automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, first, []string{"exit 0"}), first),
		&pausingClock{now: baseTime}, 6*time.Hour, time.Minute)
	paused, err := firstPipeline.Run(context.Background(), tracker.item.ID)
	if err != nil || !paused.Paused {
		t.Fatalf("Run() error = %v, paused = %t", err, paused.Paused)
	}

	later := &pausingClock{now: resetsAt.Add(time.Minute)}
	unwired := Reconciler{Tracker: tracker, Worktrees: newObserver(t, repository, worktreeRoot), Store: store, Clock: later}
	nothing, err := unwired.ContinueWaits(context.Background())
	if err != nil {
		t.Fatalf("ContinueWaits() error = %v", err)
	}
	if len(nothing) != 0 {
		t.Fatalf("an unwired sweep continued something: %#v", nothing)
	}

	// Another process holds the run for the length of the sweep.
	_, lease, err := store.AdoptRun(context.Background(), paused.RunID)
	if err != nil {
		t.Fatalf("AdoptRun() error = %v", err)
	}
	defer lease.Release()
	continued := false
	wired := unwired
	wired.Continue = func(context.Context, string, string) (Outcome, error) {
		continued = true
		return Outcome{}, errors.New("nothing should be continued while a process holds the run")
	}
	held, err := wired.ContinueWaits(context.Background())
	if err != nil {
		t.Fatalf("ContinueWaits() error = %v", err)
	}
	if len(held) != 1 || held[0].Continued || held[0].Failure != "" || !strings.Contains(held[0].Detail, "a live process holds this run") {
		t.Fatalf("ContinueWaits() over a held run = %#v, want it reported held and left", held)
	}
	if continued {
		t.Fatal("the sweep continued a run a live process was serving")
	}
	unchanged, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(unchanged.SweepContinuations) != 0 || unchanged.UsageLimitResetsAt == nil {
		t.Fatalf("the sweep wrote to a run a live process holds: %#v", unchanged)
	}
}

// A continuation the pipeline refuses is reported as the failure it is, beside
// the record already saying the sweep took the run up: the run is still holding
// its slot with nothing serving it, which is the state this step exists to end.
func TestAContinuationThePipelineRefusesIsReportedAsAFailure(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	resetsAt := baseTime.Add(2 * time.Hour)
	limit := &backend.UsageLimit{Kind: "five_hour", ResetsAt: resetsAt}
	first := usageLimitBackend(1, limit, approveVerdict)
	firstPipeline := waiting(automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, first, []string{"exit 0"}), first),
		&pausingClock{now: baseTime}, 6*time.Hour, time.Minute)
	paused, err := firstPipeline.Run(context.Background(), tracker.item.ID)
	if err != nil || !paused.Paused {
		t.Fatalf("Run() error = %v, paused = %t", err, paused.Paused)
	}

	clock := &pausingClock{now: resetsAt.Add(time.Minute)}
	refusals := 0
	reconciler := Reconciler{
		Tracker:   tracker,
		Worktrees: newObserver(t, repository, worktreeRoot),
		Store:     store,
		Clock:     clock,
		Continue: func(context.Context, string, string) (Outcome, error) {
			refusals++
			return Outcome{}, errors.New("the provider is not installed")
		},
	}
	continuations, err := reconciler.ContinueWaits(context.Background())
	if err != nil {
		t.Fatalf("ContinueWaits() error = %v", err)
	}
	if len(continuations) != 1 || !continuations[0].Continued || !strings.Contains(continuations[0].Failure, "the provider is not installed") {
		t.Fatalf("ContinueWaits() = %#v, want the refused continuation reported with the refusal", continuations)
	}
	recorded, err := store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(recorded.SweepContinuations) != 1 || recorded.UsageLimitResetsAt == nil || recorded.Status != runstate.StatusRunning {
		t.Fatalf("recorded run = %#v, want the continuation recorded and the run still in flight and waiting", recorded)
	}

	// A sweep running beside the first reads the continuation just recorded for
	// this deadline and leaves the run to it, rather than recording a second
	// continuation and reporting the pipeline's refusal of it as a failure — so
	// repeating the sweep is as safe for this step as for every other.
	beside, err := reconciler.ContinueWaits(context.Background())
	if err != nil {
		t.Fatalf("ContinueWaits() beside the first error = %v", err)
	}
	if len(beside) != 1 || beside[0].Continued || beside[0].Failure != "" || !strings.Contains(beside[0].Detail, "its continuation is being entered") {
		t.Fatalf("ContinueWaits() beside the first = %#v, want the run left to the sweep that took it up", beside)
	}
	if refusals != 1 {
		t.Fatalf("Continue was called %d times, want once", refusals)
	}
	// Once that window has passed with the record still standing on the same
	// deadline, the continuation was refused rather than entered, and a later
	// sweep takes the run up again.
	clock.now = clock.now.Add(sweepContinuationEntry)
	later, err := reconciler.ContinueWaits(context.Background())
	if err != nil {
		t.Fatalf("later ContinueWaits() error = %v", err)
	}
	if len(later) != 1 || !later[0].Continued || refusals != 2 {
		t.Fatalf("later ContinueWaits() = %#v with %d refusal(s), want the run taken up again", later, refusals)
	}
	recorded, err = store.Load(paused.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(recorded.SweepContinuations) != 2 {
		t.Fatalf("sweep continuations = %#v, want the second attempt recorded beside the first", recorded.SweepContinuations)
	}
}
