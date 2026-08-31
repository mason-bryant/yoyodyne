package orchestrator

// The dispatch half of the founding case.
//
// Every recorded instance of a repair round being lost was a dispatch that
// started something fresh in place of the handback: a new run, a new worktree
// cut off the target branch, and a developer handed the reviewer's findings
// about a change that was not in front of it. The refusal in the fresh path
// catches that after the fact and reads a record to do it; these tests are the
// other end, where a repair says which run it means and gets an entry point that
// cannot start one.

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// What the item is for: a repair carried out reserves nothing and creates
// nothing. The run named goes on in the worktree it stopped in, and the
// repository has exactly the worktrees it had before the dispatch.
func TestARepairDispatchCreatesNoWorktree(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	docket := &memoryDocket{}

	stopped := stopWithPreservedChange(t, repository, worktreeRoot, store, tracker, docket)
	if _, err := store.Triage().GrantRepair(context.Background(), tracker.item.ID, 2, docketedNow, handbackCaps); err != nil {
		t.Fatalf("GrantRepair() error = %v", err)
	}
	worktreesBefore := worktreeDirectories(t, worktreeRoot)
	runsBefore := runsRecordedFor(t, store, tracker.item.ID)

	second := roleBackend(func(backend.RunRequest) error { return nil }, approveVerdict)
	continuing := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, second, []string{"exit 0"}), second)

	result, err := repairContinuerOver(t, continuing, store, docket, tracker).
		Continue(context.Background(), RepairContinueRequest{Run: stopped.RunID, Reason: continueReasoning})
	if err != nil {
		t.Fatalf("Continue() error = %v", err)
	}
	if !result.Continued || result.Outcome.RunID != stopped.RunID {
		t.Fatalf("continued run = %s, want the stopped run %s continued", result.Outcome.RunID, stopped.RunID)
	}
	// The structural half: nothing cut a worktree, so there was no fresh one for
	// the handback to be delivered into by mistake. What the continued run then
	// removed on its way out is not this question, so what is measured is what
	// appeared rather than how many there are.
	if cut := worktreesCutSince(t, worktreeRoot, worktreesBefore); len(cut) != 0 {
		t.Fatalf("worktrees cut by the dispatch = %v, want none", cut)
	}
	if after := runsRecordedFor(t, store, tracker.item.ID); after != runsBefore {
		t.Fatalf("recorded runs = %d, want the dispatch to have reserved none beyond the %d already recorded", after, runsBefore)
	}
}

// A repair dispatched to a run that is not going refuses rather than starting
// one in its place. This is the substitution itself, refused at the dispatch:
// the run was never made live, so there is nothing to continue, and the answer
// to that is never a fresh worktree off the target branch.
func TestARepairDispatchRefusesWhereTheRunItNamesIsNotInFlight(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	stopped := stopWithPreservedChange(t, repository, worktreeRoot, store, tracker, &memoryDocket{})
	// Somebody puts the item back, which is what lets a fresh run past every
	// other gate. The run itself is still stopped: nothing re-entered it.
	tracker.item.Status = "open"
	worktreesBefore := worktreeDirectories(t, worktreeRoot)

	fresh := roleBackend(func(request backend.RunRequest) error {
		t.Errorf("a developer was invoked in %s, in place of the repair run %s is owed", request.WorkingDirectory, stopped.RunID)
		return nil
	}, approveVerdict)
	dispatching := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, fresh, []string{"exit 0"}), fresh)
	dispatching.NewRunID = runstate.NewRunID

	_, err := dispatching.Continue(context.Background(), tracker.item.ID, stopped.RunID)
	if !errors.Is(err, ErrNoRunToContinue) {
		t.Fatalf("Continue() error = %v, want the dispatch refused for finding no run to continue", err)
	}
	// The mismatch is named, and so is the decision that does start the item over.
	for _, want := range []string{stopped.RunID, "no run is in flight", "yoyo triage rerun"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal = %v, is missing %q", err, want)
		}
	}
	if len(fresh.requests) != 0 {
		t.Fatalf("provider invocations = %d, want nothing spent", len(fresh.requests))
	}
	inFlight, err := store.Incomplete()
	if err != nil {
		t.Fatalf("Incomplete() error = %v", err)
	}
	if len(inFlight) != 0 {
		t.Fatalf("runs in flight = %#v, want the refusal to have reserved nothing", inFlight)
	}
	if cut := worktreesCutSince(t, worktreeRoot, worktreesBefore); len(cut) != 0 {
		t.Fatalf("worktrees cut by the refused dispatch = %v, want none", cut)
	}
}

// And a repair dispatched while a different run of the same item is going does
// not pick that one up. Continuing it would spend the grant that was decided
// about one change on another, so the run in flight is named and left alone.
func TestARepairDispatchRefusesADifferentRunInFlight(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	stopped := stopWithPreservedChange(t, repository, worktreeRoot, store, tracker, &memoryDocket{})
	// The run that is actually going, made live exactly as a re-entry leaves one.
	reEnterAt(t, store, tracker, stopped.RunID, runstate.PhaseDeveloping)

	other := roleBackend(func(request backend.RunRequest) error {
		t.Errorf("a developer was invoked in %s for a run the dispatch never named", request.WorkingDirectory)
		return nil
	}, approveVerdict)
	dispatching := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, other, []string{"exit 0"}), other)

	decided, err := runstate.NewRunID()
	if err != nil {
		t.Fatalf("NewRunID() error = %v", err)
	}
	_, err = dispatching.Continue(context.Background(), tracker.item.ID, decided)
	if !errors.Is(err, ErrNoRunToContinue) {
		t.Fatalf("Continue() error = %v, want the dispatch refused for naming a run that is not the one in flight", err)
	}
	var mismatch ContinuationMismatchError
	if !errors.As(err, &mismatch) || mismatch.InFlight != stopped.RunID {
		t.Fatalf("refusal = %v, want it to name %s as the run in flight", err, stopped.RunID)
	}
	if len(other.requests) != 0 {
		t.Fatalf("provider invocations = %d, want nothing spent", len(other.requests))
	}
	// The run that is going was left exactly as it was, so the decision about it
	// is still there to be carried out.
	untouched, err := store.Load(stopped.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if untouched.Status != runstate.StatusRunning || untouched.Phase != runstate.PhaseDeveloping {
		t.Fatalf("run in flight = %s/%s, want the refusal to have left it running and developing", untouched.Status, untouched.Phase)
	}
}

// worktreesCutSince is the worktrees that appeared while a dispatch was being
// made, which is the question these tests ask: a repair that cut one cut it for
// a change that already exists somewhere else.
func worktreesCutSince(t *testing.T, worktreeRoot string, before []string) []string {
	t.Helper()
	existing := make(map[string]bool, len(before))
	for _, name := range before {
		existing[name] = true
	}
	var cut []string
	for _, name := range worktreeDirectories(t, worktreeRoot) {
		if !existing[name] {
			cut = append(cut, name)
		}
	}
	return cut
}

// worktreeDirectories is what the repository's worktree root actually holds,
// which is how these tests ask whether a dispatch cut a fresh one.
func worktreeDirectories(t *testing.T, worktreeRoot string) []string {
	t.Helper()
	entries, err := os.ReadDir(worktreeRoot)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	var directories []string
	for _, entry := range entries {
		if entry.IsDir() {
			directories = append(directories, entry.Name())
		}
	}
	return directories
}

// runsRecordedFor is how many runs the store holds for one work item, which is
// how these tests ask whether a dispatch reserved one.
func runsRecordedFor(t *testing.T, store *runstate.Store, workItemID string) int {
	t.Helper()
	runs, err := store.Runs(workItemID)
	if err != nil {
		t.Fatalf("Runs() error = %v", err)
	}
	return len(runs)
}
