package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/checks"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
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
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
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

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
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
	if runs := len(provider.RequestsForRole(domain.RoleDeveloper)); runs != 1 {
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
	notes := strings.Join(tracker.NoteRecords, "\n")
	if !strings.Contains(notes, "Check stage: 30m0s of the 30m0s execution.check_stage_timeout bound, stopped at the bound during make race") {
		t.Fatalf("item notes do not say the stage was stopped at its bound:\n%s", notes)
	}
}

// A stage the bound stopped judged nothing, so what the run is owed is its
// checks again on the change it already has — not a re-run from the target
// branch that redoes the development. The stopped run dockets itself saying load
// stopped it and the harness continues it; the carry-out takes it up with nobody
// deciding anything once a slot is free and the load is below the threshold,
// and not before; and the continued run re-runs the checks on the preserved
// change with no developer invoked, then goes on to its review and promotion,
// with the run's and the item's counters exactly where the stop left them.
func TestAStageTheBoundStoppedIsContinuedAtItsChecksByTheHarnessChargingNothing(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	commands := []string{"make fmtcheck", "make test", "make race", "make vet"}
	clock := &steppingClock{now: time.Now().UTC().Add(time.Hour)}
	docket := &memoryDocket{}
	build := func(takes map[string]time.Duration) (Pipeline, *timedChecks) {
		pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, commands), provider)
		checked := &timedChecks{clock: clock, takes: takes}
		pipeline.Checks = checks.Runner{Process: checked, Clock: clock, Timeout: 2 * time.Hour, StageTimeout: 30 * time.Minute}
		pipeline.Docket = docketerOverStore(docket, store, pipeline.Config)
		return pipeline, checked
	}

	// Loaded: make race would run for ninety minutes, and the stage's bound
	// stops it at thirty.
	loaded, _ := build(map[string]time.Duration{"make fmtcheck": time.Minute, "make test": time.Minute, "make race": 90 * time.Minute})
	outcome, runErr := loaded.Run(context.Background(), tracker.Item.ID)
	if runErr == nil || !strings.Contains(runErr.Error(), "check_stage_timeout bound during make race") {
		t.Fatalf("Run() error = %v, want the run stopped at the stage bound", runErr)
	}
	stopped, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !stopped.StoppedAtStageBound() || !stopped.HarnessContinuesCheckStage() {
		t.Fatalf("stopped run = %#v, want one the stage bound stopped that the harness continues", stopped)
	}
	// The stoppage is on the docket, saying load stopped it and that the harness
	// is the one to move.
	if len(docket.entries) != 1 || !docket.entries[0].HarnessContinuesChecks {
		t.Fatalf("docket = %#v, want the stoppage docketed as one the harness continues (run error %v)", docket.entries, runErr)
	}
	rendered := docket.entries[0].Render()
	for _, want := range []string{"Check stage stopped by load", "not by the change", "the harness continues it itself", "Next mover: the harness", "make race"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("docket entry does not say %q:\n%s", want, rendered)
		}
	}
	runs, err := store.Triage().Counters(tracker.Item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}

	// The machine is still loaded: the carry-out offers nothing, so no slot is
	// taken for a continuation that would be stopped the same way.
	calm, checked := build(map[string]time.Duration{"make fmtcheck": time.Minute, "make test": time.Minute, "make race": 5 * time.Minute, "make vet": time.Minute})
	load := 12.0
	intake := newIntakeHoldStore(t)
	continuer := CheckStageContinuer{
		Docket: docket, Redocket: calm.Docket, Runs: store, Intake: intake, Items: tracker, Worktrees: calm.Worktrees.(*gitworktree.Manager),
		Load:     func() (float64, int, bool) { return load, 8, true },
		Capacity: calm.Config.Execution.MaxConcurrentDevelopers,
		Start: func(ctx context.Context, workItemID, runID string) (Outcome, error) {
			return calm.Continue(ctx, workItemID, runID)
		},
	}
	carrying := CarryOut{Docket: docket, Decisions: store.Triage(), Reruns: store.Reruns(), Runs: store, CheckStages: continuer}
	if tasks, err := carrying.Outstanding(); err != nil || len(tasks) != 0 {
		t.Fatalf("Outstanding() under load = %#v, %v; want nothing offered", tasks, err)
	}
	if result, err := continuer.Continue(context.Background(), CheckStageContinueRequest{Run: outcome.RunID}); err != nil || result.Continued || result.LoadHigh == "" {
		t.Fatalf("Continue() under load = %#v, %v; want it waiting on the load", result, err)
	}
	if again, _ := store.Load(outcome.RunID); again.Status != runstate.StatusTimedOut || len(again.CheckStageContinuations) != 0 {
		t.Fatalf("a continuation waiting on the load changed the run: %#v", again)
	}

	// The load falls: the next pull's carry-out continues it with nobody having
	// decided anything.
	load = 2
	tasks, err := carrying.Outstanding()
	if err != nil || len(tasks) != 1 || tasks[0].Decision != DecisionContinueChecks || tasks[0].RunID != outcome.RunID {
		t.Fatalf("Outstanding() = %#v, %v; want the harness's continuation of the stopped stage", tasks, err)
	}
	carried, continued, err := carrying.Carry(context.Background(), tasks[0])
	if err != nil {
		t.Fatalf("Carry() error = %v", err)
	}
	if !carried.Carried || continued.Status != runstate.StatusSucceeded || continued.Integration == nil {
		t.Fatalf("carried = %#v, outcome status %s; want the continued run checked, reviewed, and promoted", carried, continued.Status)
	}
	// The checks ran again, every one of them, on the change the developer
	// attempt left; the developer was not invoked again.
	if len(checked.ran) != len(commands) {
		t.Fatalf("checks run on the continuation = %v, want all of %v", checked.ran, commands)
	}
	for _, dir := range checked.dirs {
		if dir != stopped.WorktreePath {
			t.Fatalf("a check ran in %s, want the preserved worktree %s", dir, stopped.WorktreePath)
		}
	}
	if developer := provider.RequestsForRole(domain.RoleDeveloper); len(developer) != 1 {
		t.Fatalf("developer invocations = %d, want only the first attempt", len(developer))
	}
	if integrated := gitLine(t, repository, "show", "main:feature.txt"); integrated != "implemented" {
		t.Fatalf("integrated feature.txt = %q, want the change the first attempt made", integrated)
	}
	final, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if final.RunID != outcome.RunID || final.RepairAttempts != stopped.RepairAttempts || final.IntegrationRetries != 0 || len(final.CheckStageContinuations) != 1 {
		t.Fatalf("final run = %s, attempts %d, retries %d, continuations %d; want the same run with no attempt charged and one continuation",
			final.RunID, final.RepairAttempts, final.IntegrationRetries, len(final.CheckStageContinuations))
	}
	after, err := store.Triage().Counters(tracker.Item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	// Every budget the record keeps is where the stop left it. What the review
	// that followed the checks wrote about itself — which verdict it last judged,
	// and when — is its own bookmark and spends nothing, so it is set aside.
	after.LastJudged, after.UpdatedAt = runs.LastJudged, runs.UpdatedAt
	if !reflect.DeepEqual(after, runs) {
		t.Fatalf("the item's triage budgets moved across the continuation:\nstopped   %#v\ncontinued %#v", runs, after)
	}
	if claimed, _ := store.Reruns().Claimed(tracker.Item.ID); len(claimed) != 0 {
		t.Fatalf("re-runs claimed = %#v, want none", claimed)
	}
	if !strings.Contains(tracker.Notes, "Continued at its checks") {
		t.Fatalf("item notes do not record the continuation:\n%s", tracker.Notes)
	}
	if closure, closed := docket.closed[docket.entries[0].Key]; !closed || closure.Decision != continuedChecksDocketDecision {
		t.Fatalf("docket closure = %#v, %t; want the entry closed as continued", closure, closed)
	}
}

// A worktree somebody has been in since the bound stopped the stage is not one
// the harness continues on its own: what the checks would judge is no longer the
// change the attempt left. The refusal is written onto the run so the harness
// does not ask again, and the stoppage is put back on the docket as the
// development manager's.
func TestAStageTheHarnessCannotContinueIsHandedToTheDevelopmentManager(t *testing.T) {
	t.Parallel()

	repository, worktreeRoot, store := restartableFixture(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, approveVerdict)
	clock := &steppingClock{now: time.Now().UTC().Add(time.Hour)}
	docket := &memoryDocket{}
	pipeline := automatic(newSharedPipeline(t, repository, worktreeRoot, store, tracker, provider, []string{"make race"}), provider)
	pipeline.Checks = checks.Runner{Process: &timedChecks{clock: clock, takes: map[string]time.Duration{"make race": 90 * time.Minute}}, Clock: clock, Timeout: 2 * time.Hour, StageTimeout: 30 * time.Minute}
	pipeline.Docket = docketerOverStore(docket, store, pipeline.Config)
	outcome, _ := pipeline.Run(context.Background(), tracker.Item.ID)
	stopped, err := store.Load(outcome.RunID)
	if err != nil || !stopped.HarnessContinuesCheckStage() {
		t.Fatalf("stopped run = %#v (error %v), want one the harness continues", stopped, err)
	}
	// Somebody commits in the preserved worktree.
	runPipelineGit(t, stopped.WorktreePath, "commit", "--allow-empty", "-m", "an edit nobody asked for")

	continuer := CheckStageContinuer{
		Docket: docket, Redocket: pipeline.Docket, Runs: store, Intake: newIntakeHoldStore(t), Items: tracker, Worktrees: pipeline.Worktrees.(*gitworktree.Manager),
		Load:     func() (float64, int, bool) { return 1, 8, true },
		Capacity: pipeline.Config.Execution.MaxConcurrentDevelopers,
		Start: func(context.Context, string, string) (Outcome, error) {
			t.Fatal("a refused continuation started the run")
			return Outcome{}, nil
		},
	}
	result, err := continuer.Continue(context.Background(), CheckStageContinueRequest{Run: outcome.RunID})
	if !errors.Is(err, ErrWorktreeNotAsLeft) || result.Continued || result.Refused == "" || result.RecordProblem != "" {
		t.Fatalf("Continue() = %#v, %v; want it refused for the worktree with the refusal recorded", result, err)
	}
	refused, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if refused.Status != runstate.StatusTimedOut || refused.HarnessContinuesCheckStage() || len(refused.CheckStageContinuations) != 0 {
		t.Fatalf("refused run = %#v, want it still stopped and no longer the harness's to continue", refused)
	}
	// Docketed again after the harness closed its own entry, so the stoppage is
	// a question again rather than one the closure answered.
	latest := docket.entries[len(docket.entries)-1]
	if closure, closed := docket.closed[latest.Key]; !closed || !latest.RecordedAt.After(closure.ClosedAt) || latest.HarnessContinuesChecks {
		t.Fatalf("docket = %#v, closure %#v; want the stoppage docketed again, after the closure, as the development manager's", docket.entries, closure)
	}
	if rendered := latest.Render(); strings.Contains(rendered, "Next mover: the harness") || !strings.Contains(rendered, "development manager's decision") {
		t.Fatalf("re-docketed entry does not hand the stoppage to the development manager:\n%s", rendered)
	}
	if tasks, err := (CarryOut{Docket: docket, Decisions: store.Triage(), Reruns: store.Reruns(), Runs: store, CheckStages: continuer}).Outstanding(); err != nil || len(tasks) != 0 {
		t.Fatalf("Outstanding() = %#v, %v; want the harness not to ask again", tasks, err)
	}
}

// Past its bound the harness continues a stopped stage no more: the stoppage is
// the development manager's, and the entry and the record say so.
func TestAStageStoppedPastItsContinuationsIsLeftToTheDevelopmentManager(t *testing.T) {
	t.Parallel()

	state := runstate.State{
		RunID: "run-" + strings.Repeat("a", 32), WorkItemID: "yoyodyne-task", Status: runstate.StatusTimedOut, Phase: runstate.PhaseChecking,
		WorktreePath: "/tmp/w", Branch: "b", BaseCommit: "c", TargetBranch: "main", ProviderSessionID: "session",
		CheckStage: &runstate.CheckStage{StartedAt: time.Now(), BoundSeconds: 1800, Command: "make race", StoppedAtBound: true},
	}
	if !state.HarnessContinuesCheckStage() {
		t.Fatal("a first stop at the bound is not continued by the harness")
	}
	for range runstate.MaxCheckStageContinuations {
		state.CheckStageContinuations = append(state.CheckStageContinuations, runstate.CheckStageContinuation{ContinuedAt: time.Now(), Reason: "continued"})
	}
	if state.HarnessContinuesCheckStage() {
		t.Fatal("a run continued to the bound is still continued by the harness")
	}
	if says := state.CheckStageStopSays(); !strings.Contains(says, "which is its bound") || !strings.Contains(says, "development manager's decision") {
		t.Fatalf("stop says %q, want the bound spent and the decision the development manager's", says)
	}
	refused := state
	refused.CheckStageContinuations = nil
	refused.CheckStageContinuationRefused = "the worktree is not as the harness left it"
	if refused.HarnessContinuesCheckStage() {
		t.Fatal("a run whose continuation was refused is still continued by the harness")
	}
}

// While the checks run, the record says where the stage stands: when it began,
// the bound, and which check it is on. That is what `yoyo status` reads to say
// "checks: 14m of 30m" for a run instead of "checking, 2h elapsed".
func TestTheRecordSaysWhichCheckTheStageIsOnWhileItRuns(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
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
	if _, err := pipeline.Run(context.Background(), tracker.Item.ID); err != nil {
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
			tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
			provider := orchestratortest.RoleBackend(func(request backend.RunRequest) error {
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
			outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
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
			notes := strings.Join(tracker.NoteRecords, "\n")
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
	// ran and dirs are the checks this runner was asked to run and where.
	ran  []string
	dirs []string
}

func (r *timedChecks) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	started := r.clock.now
	r.ran = append(r.ran, command.Args[len(command.Args)-1])
	r.dirs = append(r.dirs, command.Dir)
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
