package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The check stage is bounded as a whole. Here is the 2026-09-19 shape with the
// clock turned down: a list whose checks are each inside their own budget and
// whose sum is not, which under the per-check bound alone ran for two hours.
// The stage ends where its bound is, the run ends as a stoppage rather than
// spending a repair attempt, and what it names is the bound, the check the
// bound stopped, and what the stage had spent — on the run, on the item's
// notes, and in the reason the run gives.
func TestTheCheckStageEndsAtItsBoundNamingTheBoundAndTheCheck(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	// Two checks a minute each and a third that would run for ninety, each
	// inside a two-hour budget of its own, against a thirty-minute stage: the
	// stage's bound is what stops the third, not its own. The clock is the
	// test's rather than the wall's, so the shape is exact and costs no time.
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"make fmtcheck", "make test", "make race", "make vet"})
	// The run's own record is stamped by the wall clock, and an event stamped
	// before the run began is refused, so the stepped clock starts ahead of it.
	clock := &steppingClock{now: time.Now().UTC().Add(time.Hour)}
	pipeline.Checks = checks.Runner{
		Process:      &timedChecks{clock: clock, takes: map[string]time.Duration{"make fmtcheck": time.Minute, "make test": time.Minute, "make race": 90 * time.Minute}},
		Clock:        clock,
		Timeout:      2 * time.Hour,
		StageTimeout: 30 * time.Minute,
	}

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err == nil {
		t.Fatal("Run() error = nil, want the run stopped at the stage bound")
	}
	for _, want := range []string{"check stage reached its 30m0s execution.check_stage_timeout bound during make race, which had run for 28m0s", "the stage had spent 30m0s across 3 check(s)", "landing_checks", checks.ChangedGoPackagesVariable} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Run() error = %v, want it to name %q", err, want)
		}
	}
	// A stage the bound stopped never judged the change, so the developer is
	// not asked to repair anything and the run stops on the first attempt.
	if runs := len(provider.requestsForRole(domain.RoleDeveloper)); runs != 1 {
		t.Fatalf("developer invocations = %d, want only the first attempt", runs)
	}
	if len(outcome.Checks) != 3 || !outcome.Checks[2].StoppedByStage || outcome.Checks[2].Command != "make race" {
		t.Fatalf("outcome checks = %#v, want the third stopped by the stage and the fourth never started", outcome.Checks)
	}
	if outcome.CheckStage == nil || !outcome.CheckStage.StoppedAtBound || outcome.CheckStage.Command != "make race" {
		t.Fatalf("outcome stage = %#v, want it stopped at the bound during the third check", outcome.CheckStage)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.Status != runstate.StatusTimedOut || state.Phase != runstate.PhaseChecking || state.CheckFailure != nil {
		t.Fatalf("state = %#v, want a run the harness stopped on time with nothing to repair", state)
	}
	if state.CheckStage == nil || !state.CheckStage.StoppedAtBound || state.CheckStage.Running() {
		t.Fatalf("recorded stage = %#v, want it ended at the bound", state.CheckStage)
	}
	if !strings.Contains(state.Failure, "check_stage_timeout bound during make race") {
		t.Fatalf("recorded failure = %q, want the bound and the check named", state.Failure)
	}
	notes := strings.Join(tracker.noteRecords, "\n")
	if !strings.Contains(notes, "Check stage: 30m0s of the 30m0s execution.check_stage_timeout bound, stopped at the bound during make race") {
		t.Fatalf("item notes do not say the stage was stopped at its bound:\n%s", notes)
	}
}

// While the checks run, the record says where the stage stands: when it began,
// the bound, and which check it is on. That is what `yoyo status` reads to say
// "checks: 14m of 30m" for a run instead of "checking, 2h elapsed".
func TestTheRecordSaysWhichCheckTheStageIsOnWhileItRuns(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, nil)
	// The second check reads the run's own record while it is the check the
	// stage is on, which is exactly what a status surface does.
	recordPath := filepath.Join(t.TempDir(), "stage.json")
	pipeline.Config.Checks = []string{
		"true",
		"cp " + filepath.Join(store.Root(), pipelineRunID+".json") + " " + recordPath,
	}
	if _, err := pipeline.Run(context.Background(), tracker.item.ID); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	recorded, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("the check could not read the run's record: %v", err)
	}
	for _, want := range []string{`"check_stage"`, `"bound_seconds": 1800`, `"command": "cp `, `"narrowed":`} {
		if !strings.Contains(string(recorded), want) {
			t.Fatalf("record while the stage ran = %s, want it to carry %s", recorded, want)
		}
	}
	if strings.Contains(string(recorded), `"finished_at"`) {
		t.Fatalf("record while the stage ran = %s, want the stage still running", recorded)
	}
	state, err := store.Load(pipelineRunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.CheckStage == nil || state.CheckStage.Running() || state.CheckStage.StoppedAtBound {
		t.Fatalf("recorded stage = %#v, want it ended inside its bound", state.CheckStage)
	}
}

// Every check is told what the change touches, in the shape the Go command
// takes, so a check the operator wrote to narrow itself runs over that and
// nothing else. A change to one package is that package; a repository that is
// no Go module is told the whole module, so a check written for one runs whole.
func TestEveryCheckIsToldWhichGoPackagesTheChangeTouches(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		module  bool
		changes string
		want    string
	}{
		{name: "a change to one package", module: true, changes: "internal/checks/narrow.go", want: "./internal/checks"},
		{name: "a repository that is no Go module", module: false, changes: "feature.txt", want: "./..."},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			repository := pipelineRepository(t)
			if test.module {
				writeCommitted(t, repository, "go.mod", "module example.com/project\n")
				writeCommitted(t, repository, "internal/checks/runner.go", "package checks\n")
				writeCommitted(t, repository, "internal/orchestrator/pipeline.go", "package orchestrator\n")
			}
			tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
			provider := roleBackend(func(request backend.RunRequest) error {
				path := filepath.Join(request.WorkingDirectory, filepath.FromSlash(test.changes))
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					return err
				}
				return os.WriteFile(path, []byte("package checks\n"), 0o600)
			}, approveVerdict)
			told := filepath.Join(t.TempDir(), "told.txt")
			pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{
				`printf '%s' "$` + checks.ChangedGoPackagesVariable + `" > ` + told,
			})
			outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			read, err := os.ReadFile(told)
			if err != nil {
				t.Fatalf("the check did not write what it was told: %v", err)
			}
			if string(read) != test.want {
				t.Fatalf("the check was told %q, want %q", read, test.want)
			}
			state, err := store.Load(outcome.RunID)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if state.CheckStage == nil || !strings.Contains(state.CheckStage.Narrowed, strings.TrimPrefix(test.want, "./...")) {
				t.Fatalf("recorded stage = %#v, want the narrowing recorded", state.CheckStage)
			}
			notes := strings.Join(tracker.noteRecords, "\n")
			if !strings.Contains(notes, "gate narrowed to: ") {
				t.Fatalf("item notes do not say what the gate was narrowed to:\n%s", notes)
			}
		})
	}
}

// steppingClock is a clock the test moves, so a stage's arithmetic is checked
// against known spans rather than against wall-clock sleeps.
type steppingClock struct {
	now time.Time
}

func (c *steppingClock) Now() time.Time { return c.now }

// timedChecks stands in for a project's checks, each taking a known time: a
// command moves the clock by what it takes, cut to the budget it was given,
// and is timed out where the budget was what stopped it.
type timedChecks struct {
	clock *steppingClock
	takes map[string]time.Duration
}

func (r *timedChecks) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	started := r.clock.now
	took := r.takes[command.Args[len(command.Args)-1]]
	status := execution.ProcessSucceeded
	if command.Timeout > 0 && took > command.Timeout {
		took = command.Timeout
		status = execution.ProcessTimedOut
	}
	r.clock.now = r.clock.now.Add(took)
	exitCode := 0
	if status != execution.ProcessSucceeded {
		exitCode = -1
	}
	return execution.ProcessResult{Status: status, ExitCode: exitCode, StartedAt: started, FinishedAt: r.clock.now}, nil
}

// writeCommitted puts a file into the repository's history, so a fixture can
// be a Go module with packages the change under test then touches.
func writeCommitted(t *testing.T, repository, relative, content string) {
	t.Helper()
	path := filepath.Join(repository, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	runPipelineGit(t, repository, "add", relative)
	runPipelineGit(t, repository, "commit", "-m", "add "+relative)
}
