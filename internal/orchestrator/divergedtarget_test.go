package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A target branch the harness will not catch up to the remote's holds intake by
// itself until the branches converge. Before yoyodyne-ifd.428.26 the brake
// rightly did not count the refusal, so a wedged target let the line go on
// pulling: every item spent a whole development and review before stopping on
// the same divergence, until a person ran the unwedging steps.
//
// Here a scratch target is wedged under a real run, which records the
// divergence against the product; the pull after it starts nothing; a sweep
// over branches still diverged lifts nothing; and once the branches are settled
// the next sweep finds them converged, lifts the record, and the pull after it
// chooses again with nothing released.
func TestAWedgedTargetHoldsThePullUntilASweepFindsTheBranchesConverged(t *testing.T) {
	t.Parallel()

	repository, remote := publishedRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &orchestratortest.Forge{Remote: remote}
	// A rewritten remote history, which no fast-forward answers.
	forge.OnEnsure = func() { rewriteRemoteTarget(t, remote, "main") }
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, store := newPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})
	divergences, err := runstate.NewDivergedTargetStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewDivergedTargetStore() error = %v", err)
	}
	pipeline.DivergedTargets = divergences

	harness := newScheduleHarness(readyItems("yoyodyne-task", "yoyodyne-next")...)
	harness.divergences = divergences
	harness.run = func(h *scheduleHarness, id string) (Outcome, error) {
		h.retire(id)
		if id != "yoyodyne-task" {
			return h.complete(id), nil
		}
		return pipeline.Run(context.Background(), id)
	}
	scheduler := Scheduler{Open: harness.open, Sleep: harness.sleep, Now: harness.clock}

	// The first pull starts the task, whose promotion is refused on the wedge.
	schedule, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	standing, err := divergences.Standing()
	if err != nil {
		t.Fatalf("Standing() error = %v", err)
	}
	if len(standing) != 1 || standing[0].TargetBranch != "main" || standing[0].RemoteCommit != publishedCommit(t, remote, "main") ||
		standing[0].WorkItemID != "yoyodyne-task" || standing[0].RunID != pipelineRunID || standing[0].Refusals != 1 {
		t.Fatalf("recorded divergences = %#v, want the refused promotion recorded against main", standing)
	}
	if !strings.Contains(standing[0].Held, "only a person can say which history is right") {
		t.Fatalf("recorded divergence = %#v, want the catch-up's own account carried", standing[0])
	}
	// The second pull starts nothing: the next item is ready and the drain stops
	// on the divergence rather than spending a run finding it again.
	if order := harness.pullOrder(); len(order) != 1 || order[0] != "yoyodyne-task" {
		t.Fatalf("pulled %v, want only the task that met the wedge", order)
	}
	if schedule.Stopped != ScheduleDivergedTarget {
		t.Fatalf("stopped = %q, want the drain stopped on the diverged target: %s", schedule.Stopped, schedule.Render())
	}
	if len(schedule.DivergedTargets) != 1 || !strings.Contains(schedule.Render(), "Unwedging a target branch that diverged from the forge") {
		t.Fatalf("schedule does not name the divergence with its recovery:\n%s", schedule.Render())
	}
	// It is not the brake and not an intake hold: nothing was placed to release.
	if schedule.Braked != nil {
		t.Fatalf("schedule braked on %#v, want the divergence held by its own record", schedule.Braked)
	}
	if _, held, _ := harness.Held(); held {
		t.Fatal("intake is held, want no hold anybody would have to release")
	}

	reconciler := Reconciler{Tracker: tracker, Worktrees: newObserver(t, repository, t.TempDir()), Store: store, Divergences: divergences}

	// A sweep over branches still diverged lifts nothing, and a pull after it
	// still starts nothing.
	convergence, err := reconciler.Converge(context.Background())
	if err != nil {
		t.Fatalf("Converge() error = %v", err)
	}
	if len(convergence.Divergences) != 0 {
		t.Fatalf("lifted %#v over branches still diverged, want nothing lifted", convergence.Divergences)
	}
	if standing, err := divergences.Standing(); err != nil || len(standing) != 1 {
		t.Fatalf("Standing() = %#v, %v; want the divergence still standing", standing, err)
	}
	if again, err := scheduler.Schedule(context.Background()); err != nil || again.Stopped != ScheduleDivergedTarget || len(again.Started) != 0 {
		t.Fatalf("Schedule() = %q with %d started, %v; want nothing started while the wedge stands", again.Stopped, len(again.Started), err)
	}

	// A person settles the branches the way docs/operations.md says: the target
	// is put back onto the remote's history.
	runPipelineGit(t, repository, "fetch", "origin", "main")
	runPipelineGit(t, repository, "reset", "--hard", "origin/main")

	convergence, err = reconciler.Converge(context.Background())
	if err != nil {
		t.Fatalf("Converge() error = %v", err)
	}
	if len(convergence.Divergences) != 1 || convergence.Divergences[0].Lifted == nil || convergence.Divergences[0].TargetBranch != "main" {
		t.Fatalf("lifted = %#v, want the divergence on main lifted by the converged sweep", convergence.Divergences)
	}
	if standing, err := divergences.Standing(); err != nil || len(standing) != 0 {
		t.Fatalf("Standing() = %#v, %v; want nothing standing once the branches converged", standing, err)
	}

	// And the line resumes with nothing released: the next item is pulled.
	resumed, err := scheduler.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if order := harness.pullOrder(); len(order) != 2 || order[1] != "yoyodyne-next" {
		t.Fatalf("pulled %v, want the next item pulled once the sweep lifted the divergence: %s", order, resumed.Render())
	}
	if resumed.Stopped == ScheduleDivergedTarget {
		t.Fatalf("stopped = %q, want the divergence gone from the pull", resumed.Stopped)
	}
}

// A watching session waits a divergence out rather than stopping on it, and
// says why each time it waits, so the watch log carries the recovery.
func TestAWatchingSessionWaitsOutADivergedTarget(t *testing.T) {
	t.Parallel()

	divergences, err := runstate.NewDivergedTargetStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewDivergedTargetStore() error = %v", err)
	}
	if _, err := divergences.Notice(runstate.DivergedTargetObservation{
		TargetBranch: "main",
		LocalCommit:  "4d7e805",
		RemoteCommit: "9f1c2ab",
		Held:         "main on origin is at 9f1c2ab, which does not contain the local main at 4d7e805; only a person can say which history is right",
		RunID:        "run-one",
		WorkItemID:   "yoyodyne-one",
	}); err != nil {
		t.Fatalf("Notice() error = %v", err)
	}
	harness := newScheduleHarness(readyItems("yoyodyne-two")...)
	harness.divergences = divergences
	harness.onSleep = func(_ *scheduleHarness, sleeps int) bool { return sleeps < 3 }

	schedule, err := Scheduler{Open: harness.open, Watching: true, Sleep: harness.sleep, Now: harness.clock}.Schedule(context.Background())
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if order := harness.pullOrder(); len(order) != 0 {
		t.Fatalf("pulled %v, want nothing chosen while the divergence stands", order)
	}
	if schedule.Polls < 3 || schedule.Stopped != ScheduleCancelled {
		t.Fatalf("polls = %d, stopped = %q; want the session waiting the divergence out poll after poll", schedule.Polls, schedule.Stopped)
	}
}
