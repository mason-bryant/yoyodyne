package maintain

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The machine a pass runs on, as a test arranges it: whether the provider is
// answering, what the checkout and the binary look like, what the supervisor
// knows about the children, and what every command answers.
type world struct {
	t         *testing.T
	sweeps    *runstate.SweepStore
	outage    bool
	replaced  bool
	installed time.Time
	children  map[config.ServiceName]runstate.SupervisedChild
	restarted []config.ServiceName
	commands  [][]string
	head      string
	changed   string
	dirty     string
	reconcile execution.ProcessResult
	buildErr  bool
	now       time.Time
}

func newWorld(t *testing.T) *world {
	t.Helper()
	sweeps, err := runstate.NewSweepStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	return &world{
		t:         t,
		sweeps:    sweeps,
		installed: time.Date(2026, 9, 19, 11, 0, 0, 0, time.UTC),
		children:  map[config.ServiceName]runstate.SupervisedChild{},
		head:      "abc123def456abc123def456abc123def456abc1",
		reconcile: execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "main caught up\n"},
		now:       time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC),
	}
}

func (w *world) Standing() (runstate.ProviderOutage, bool, error) {
	if !w.outage {
		return runstate.ProviderOutage{}, false, nil
	}
	return runstate.ProviderOutage{
		SchemaVersion: runstate.ProviderOutageSchemaVersion,
		ProductID:     "yoyodyne",
		Cause:         domain.ProviderUnauthenticated,
		Since:         w.now.Add(-time.Hour),
		LastSeen:      w.now,
		Refusals:      3,
	}, true, nil
}

func (w *world) Replaced() (bool, error)         { return w.replaced, nil }
func (w *world) InstalledAt() (time.Time, error) { return w.installed, nil }

func (w *world) ChildState(name config.ServiceName) (runstate.SupervisedChild, bool) {
	state, known := w.children[name]
	return state, known
}

func (w *world) Restart(_ context.Context, name config.ServiceName, reason string) (runstate.SupervisedChild, error) {
	state, known := w.children[name]
	if !known || state.State != runstate.ChildRunning {
		return state, errors.New("not running")
	}
	w.restarted = append(w.restarted, name)
	state.State = runstate.ChildDown
	state.Reason = reason
	w.children[name] = state
	return state, nil
}

func (w *world) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	invocation := append([]string{filepath.Base(command.Name)}, command.Args...)
	w.commands = append(w.commands, invocation)
	joined := strings.Join(invocation, " ")
	switch {
	case strings.HasPrefix(joined, "yoyo reconcile"):
		return w.reconcile, nil
	case joined == "git rev-parse HEAD":
		return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: w.head + "\n"}, nil
	case strings.HasPrefix(joined, "git diff --name-only"):
		return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: w.changed}, nil
	case strings.HasPrefix(joined, "git status --porcelain"):
		return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: w.dirty}, nil
	case joined == "make build":
		if w.buildErr {
			return execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 2, Stderr: "cmd/yoyo/main.go:1: undefined: x"}, nil
		}
		w.replaced = true
		return execution.ProcessResult{Status: execution.ProcessSucceeded}, nil
	}
	return execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 127, Stderr: "no such command in the test"}, nil
}

func (w *world) ran(match string) bool {
	for _, invocation := range w.commands {
		if strings.Contains(strings.Join(invocation, " "), match) {
			return true
		}
	}
	return false
}

func (w *world) pass(repository string) *Pass {
	return &Pass{
		Product:    "yoyodyne",
		Every:      10 * time.Minute,
		Program:    filepath.Join(repository, "bin", "yoyo"),
		Config:     filepath.Join(repository, ".yoyodyne", "config.yaml"),
		Repository: repository,
		Environ:    []string{"PATH=/usr/bin"},
		Commit:     "0123456789abcdef0123456789abcdef01234567",
		Claims:     w.sweeps,
		Outages:    w,
		Deployment: w,
		Residents:  w,
		Runner:     w,
		Now:        func() time.Time { return w.now },
		Log:        w.t.Logf,
	}
}

// recorded is the one pass the store holds, with each step by name.
func (w *world) recorded() (runstate.Sweep, map[string]runstate.SweepStep) {
	w.t.Helper()
	passes, unreadable, err := w.sweeps.List()
	if err != nil || len(unreadable) != 0 {
		w.t.Fatalf("List() = %d unreadable, %v", len(unreadable), err)
	}
	if len(passes) != 1 {
		w.t.Fatalf("recorded %d passes, want one", len(passes))
	}
	steps := map[string]runstate.SweepStep{}
	for _, step := range passes[0].Steps {
		steps[step.Name] = step
	}
	return passes[0], steps
}

// The pass never writes a tracker status: the interim job's bare `bd update
// --status` is the reason this rule is stated, and it is held by every test
// here rather than by one.
func (w *world) assertNoTrackerWrite() {
	w.t.Helper()
	for _, invocation := range w.commands {
		if invocation[0] == "bd" {
			w.t.Fatalf("the pass ran %v; it never writes the tracker itself", invocation)
		}
	}
}

// While the provider is not answering, nothing is restarted: a build that is
// installed and waiting stays waiting, the sink is left alone, and the
// supervisor is not asked to restart. The record says so step by step rather
// than quietly doing less — the provider step names the outage, and the
// redeploy step is skipped with the same reason.
func TestNothingIsRestartedWhileTheProviderIsNotAnswering(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	w.outage = true
	w.replaced = true
	w.children[config.ServiceSlack] = runstate.SupervisedChild{Service: config.ServiceSlack, State: runstate.ChildRunning, PID: 7}
	w.children[config.ServiceScheduler] = runstate.SupervisedChild{Service: config.ServiceScheduler, State: runstate.ChildRunning, PID: 8}
	pass := w.pass(t.TempDir())

	outcome := pass.Run(context.Background())

	if outcome.TakeUpDeploy || outcome.Skipped {
		t.Fatalf("outcome = %+v, want the pass taken and no restart asked for", outcome)
	}
	if len(w.restarted) != 0 {
		t.Fatalf("restarted %v, want nothing restarted while the provider is not authenticated", w.restarted)
	}
	recorded, steps := w.recorded()
	if !recorded.HarnessPass() || recorded.Task != config.MaintenanceTaskName || recorded.Result == nil {
		t.Fatalf("recorded = %+v, want the harness's own maintenance pass with its summary", recorded)
	}
	if got := steps[StepProvider]; got.Outcome != runstate.StepRan || !strings.Contains(got.Detail, "not authenticated") || !strings.Contains(got.Detail, "nothing is restarted this pass") {
		t.Errorf("provider step = %+v, want the outage named and the hold said", got)
	}
	if got := steps[StepReconcile]; got.Outcome != runstate.StepRan || !strings.Contains(got.Detail, "main caught up") {
		t.Errorf("reconcile step = %+v, want it run whatever the provider is doing", got)
	}
	if got := steps[StepRebuild]; got.Outcome != runstate.StepSkipped || !strings.Contains(got.Detail, "already installed") {
		t.Errorf("rebuild step = %+v, want it skipped over the build already waiting", got)
	}
	if got := steps[StepRedeploy]; got.Outcome != runstate.StepSkipped || !strings.Contains(got.Detail, "nothing is restarted while") || !strings.Contains(got.Detail, "not authenticated") {
		t.Errorf("redeploy step = %+v, want it skipped with the provider as the reason", got)
	}
	if got := steps[StepSlack]; got.Outcome != runstate.StepRan || !strings.Contains(got.Detail, "pid 7") {
		t.Errorf("slack step = %+v, want the sink's state as the supervisor's look left it", got)
	}
	if !w.ran("yoyo reconcile --config") {
		t.Errorf("reconcile was not run from the running binary; ran %v", w.commands)
	}
	w.assertNoTrackerWrite()
}

// With the provider answering and a build installed over the running one, the
// pass takes it up: the sink started before the install is stopped for the
// supervisor to start again, the scheduler is left to take the build up
// itself, and the supervisor is asked to restart once the record is written.
func TestAnInstalledBuildIsTakenUpWithTheSchedulerLeftToDrainItself(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	w.replaced = true
	w.children[config.ServiceSlack] = runstate.SupervisedChild{Service: config.ServiceSlack, State: runstate.ChildRunning, PID: 7, StartedAt: w.installed.Add(-time.Hour)}
	w.children[config.ServiceScheduler] = runstate.SupervisedChild{Service: config.ServiceScheduler, State: runstate.ChildRunning, PID: 8, StartedAt: w.installed.Add(-time.Hour)}
	pass := w.pass(t.TempDir())

	outcome := pass.Run(context.Background())

	if !outcome.TakeUpDeploy {
		t.Fatal("the pass did not ask the supervisor to restart into the installed build")
	}
	if strings.Join(serviceNames(w.restarted), ",") != "slack" {
		t.Fatalf("restarted %v, want the sink alone; the scheduler is never stopped for a deploy", w.restarted)
	}
	_, steps := w.recorded()
	got := steps[StepRedeploy]
	if got.Outcome != runstate.StepRan {
		t.Fatalf("redeploy step = %+v, want it run", got)
	}
	for _, want := range []string{"the sink was stopped", "the scheduler is left to take the build up itself", "never stopped for a deploy", "the supervisor restarts into the installed build"} {
		if !strings.Contains(got.Detail, want) {
			t.Errorf("redeploy detail = %q, want %q in it", got.Detail, want)
		}
	}
	w.assertNoTrackerWrite()

	// A sink this supervisor started after the install is already the new build,
	// and is not stopped again on the next pass: the restart that did not
	// happen must not become a sink bounced every ten minutes.
	w.restarted = nil
	w.children[config.ServiceSlack] = runstate.SupervisedChild{Service: config.ServiceSlack, State: runstate.ChildRunning, PID: 9, StartedAt: w.installed.Add(time.Minute)}
	w.now = w.now.Add(10 * time.Minute)
	if outcome := pass.Run(context.Background()); !outcome.TakeUpDeploy || len(w.restarted) != 0 {
		t.Errorf("second pass: outcome %+v, restarted %v; want the supervisor asked again and the sink left running", outcome, w.restarted)
	}
}

// The rebuild step builds and installs when the checkout has moved past the
// running build in something that goes into the binary, and only when the
// running binary is this repository's own and the checkout is clean; then the
// redeploy step takes what it built up. Each reason not to build is on the
// record as a skip.
func TestTheCheckoutMovingPastTheRunningBuildRebuildsAndInstalls(t *testing.T) {
	t.Parallel()

	repository := t.TempDir()
	w := newWorld(t)
	w.changed = "internal/cli/product.go\n"
	pass := w.pass(repository)

	outcome := pass.Run(context.Background())

	if !w.ran("make build") {
		t.Fatalf("make build was not run; ran %v", w.commands)
	}
	if !outcome.TakeUpDeploy {
		t.Fatal("the build that was made was not taken up on the same pass")
	}
	_, steps := w.recorded()
	if got := steps[StepRebuild]; got.Outcome != runstate.StepRan || !strings.Contains(got.Detail, "built and installed") || !strings.Contains(got.Detail, "abc123def456") {
		t.Errorf("rebuild step = %+v, want the build recorded with the tip", got)
	}
	w.assertNoTrackerWrite()

	for name, arrange := range map[string]struct {
		arrange func(w *world) *Pass
		skip    string
	}{
		"the running binary is not in the repository": {
			arrange: func(w *world) *Pass {
				pass := w.pass(repository)
				pass.Program = "/usr/local/bin/yoyo"
				return pass
			},
			skip: "not inside the repository",
		},
		"the running build is the tip": {
			arrange: func(w *world) *Pass {
				pass := w.pass(repository)
				pass.Commit = w.head
				return pass
			},
			skip: "is the checkout's tip",
		},
		"nothing that goes into the binary changed": {
			arrange: func(w *world) *Pass {
				w.changed = ""
				return w.pass(repository)
			},
			skip: "nothing that goes into the binary changed",
		},
		"the checkout has uncommitted changes": {
			arrange: func(w *world) *Pass {
				w.changed = "internal/cli/product.go\n"
				w.dirty = " M internal/cli/product.go\n"
				return w.pass(repository)
			},
			skip: "uncommitted changes",
		},
		"the running binary carries no revision": {
			arrange: func(w *world) *Pass {
				w.changed = "internal/cli/product.go\n"
				pass := w.pass(repository)
				pass.Commit = ""
				return pass
			},
			skip: "carries no revision",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t)
			pass := arrange.arrange(w)
			outcome := pass.Run(context.Background())
			if w.ran("make build") || outcome.TakeUpDeploy {
				t.Fatalf("a build was made or taken up; ran %v", w.commands)
			}
			_, steps := w.recorded()
			if got := steps[StepRebuild]; got.Outcome != runstate.StepSkipped || !strings.Contains(got.Detail, arrange.skip) {
				t.Errorf("rebuild step = %+v, want it skipped saying %q", got, arrange.skip)
			}
			if got := steps[StepRedeploy]; got.Outcome != runstate.StepSkipped {
				t.Errorf("redeploy step = %+v, want it skipped with nothing to take up", got)
			}
			w.assertNoTrackerWrite()
		})
	}
}

// A step that fails is on the record as failed, in the record's problem and on
// the cadence's claim, and the steps after it are still taken.
func TestAFailedStepIsRecordedAndTheRestOfThePassIsTaken(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	w.reconcile = execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "reconcile failed: the tracker did not answer"}
	w.changed = "internal/x.go\n"
	w.buildErr = true
	pass := w.pass(t.TempDir())

	pass.Run(context.Background())

	recorded, steps := w.recorded()
	if got := steps[StepReconcile]; got.Outcome != runstate.StepFailed || !strings.Contains(got.Detail, "the tracker did not answer") {
		t.Errorf("reconcile step = %+v, want it failed with what reconcile said", got)
	}
	if got := steps[StepRebuild]; got.Outcome != runstate.StepFailed || !strings.Contains(got.Detail, "undefined: x") {
		t.Errorf("rebuild step = %+v, want it failed with what make said", got)
	}
	if _, took := steps[StepSlack]; !took {
		t.Error("the slack step was not taken after a failure before it")
	}
	if !strings.Contains(recorded.Problem, "reconcile:") || !strings.Contains(recorded.Problem, "rebuild:") {
		t.Errorf("problem = %q, want both failures named", recorded.Problem)
	}
	claim, found, err := w.sweeps.Find(config.MaintenanceTaskName)
	if err != nil || !found || !strings.Contains(claim.Problem, "reconcile:") {
		t.Errorf("claim = %+v, %t, %v; want the failure settled on the cadence", claim, found, err)
	}
	if recorded.Result == nil || !strings.Contains(recorded.Result.Summary, "2 failed") {
		t.Errorf("summary = %+v, want the failures counted", recorded.Result)
	}
}

// The cadence: due at once when nothing has fired, not due again until the
// interval has passed, and durable across a supervisor that came back.
func TestThePassIsDueOnItsCadence(t *testing.T) {
	t.Parallel()

	w := newWorld(t)
	pass := w.pass(t.TempDir())
	if !pass.Due(w.now) {
		t.Fatal("a pass nothing has fired is not due")
	}
	pass.Run(context.Background())
	if pass.Due(w.now.Add(5 * time.Minute)) {
		t.Error("the pass is due five minutes after it fired, on a ten-minute cadence")
	}
	if !pass.Due(w.now.Add(10 * time.Minute)) {
		t.Error("the pass is not due once its interval has passed")
	}
	if !strings.Contains(pass.Describe(), "every 10m0s; last pass at 2026-09-19T12:00:00Z") || !strings.Contains(pass.Describe(), "next at 2026-09-19T12:10:00Z") {
		t.Errorf("Describe() = %q", pass.Describe())
	}

	// A supervisor that came back reads the claim rather than firing again.
	again := w.pass(t.TempDir())
	if again.Due(w.now.Add(5 * time.Minute)) {
		t.Error("a new supervisor fired the pass five minutes after the last one did")
	}
	w.now = w.now.Add(5 * time.Minute)
	if outcome := again.Run(context.Background()); !outcome.Skipped {
		t.Error("a pass claimed before its interval was taken")
	}
}

func serviceNames(names []config.ServiceName) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, string(name))
	}
	return out
}
