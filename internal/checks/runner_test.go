package checks

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

func TestRunnerStopsAfterFailedCheck(t *testing.T) {
	t.Parallel()

	var events []execution.Event
	results, lastSequence, err := (Runner{Process: execution.OSProcessRunner{}}).Run(
		context.Background(),
		Request{RunID: "run-0123456789abcdef0123456789abcdef", Directory: t.TempDir(), Commands: []string{"printf 'ok\\n'", "exit 3", "exit 0"}, LastSequence: 5},
		func(event execution.Event) error {
			events = append(events, event)
			return nil
		},
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(results) != 2 || !results[0].Passed || results[1].Passed || results[1].Process.ExitCode != 3 {
		t.Fatalf("Run() results = %#v", results)
	}
	if len(events) != 5 || lastSequence != 10 {
		t.Fatalf("events = %d, last sequence = %d", len(events), lastSequence)
	}
}

func TestRunnerUsesANonLoginShell(t *testing.T) {
	t.Parallel()

	process := &recordingRunner{}
	_, _, err := (Runner{Process: process}).Run(
		context.Background(),
		Request{RunID: "run-0123456789abcdef0123456789abcdef", Directory: t.TempDir(), Commands: []string{"true"}, LastSequence: 0},
		nil,
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !reflect.DeepEqual(process.command.Args, []string{"-c", "true"}) {
		t.Fatalf("shell args = %#v, want non-login shell", process.command.Args)
	}
}

func TestRunnerReturnsOnlyLastAcceptedEventSequence(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		rejectType execution.EventType
		wantLast   uint64
	}{
		{name: "started", rejectType: execution.EventCommandStarted, wantLast: 5},
		{name: "output", rejectType: execution.EventProcessOutput, wantLast: 6},
		{name: "completed", rejectType: execution.EventCommandCompleted, wantLast: 7},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, lastSequence, err := (Runner{Process: execution.OSProcessRunner{}}).Run(
				context.Background(),
				Request{RunID: "run-0123456789abcdef0123456789abcdef", Directory: t.TempDir(), Commands: []string{"printf 'output\\n'"}, LastSequence: 5},
				func(event execution.Event) error {
					if event.Type == test.rejectType {
						return errors.New("event rejected")
					}
					return nil
				},
			)
			if err == nil || !strings.Contains(err.Error(), "event rejected") {
				t.Fatalf("Run() error = %v", err)
			}
			if lastSequence != test.wantLast {
				t.Fatalf("last sequence = %d, want %d", lastSequence, test.wantLast)
			}
		})
	}
}

func TestRunnerRedactsSensitiveCheckOutputBeforeEvents(t *testing.T) {
	t.Parallel()

	var events []execution.Event
	secret := "check-secret-value"
	results, _, err := (Runner{
		Process:      execution.OSProcessRunner{},
		RedactValues: []string{secret},
	}).Run(
		context.Background(),
		Request{RunID: "run-0123456789abcdef0123456789abcdef", Directory: t.TempDir(), Commands: []string{"printf 'check-secret-value\\n'"}, LastSequence: 0},
		func(event execution.Event) error {
			events = append(events, event)
			return nil
		},
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(results) != 1 || strings.Contains(results[0].Process.Stdout, secret) || !strings.Contains(results[0].Process.Stdout, "[REDACTED]") {
		t.Fatalf("Run() result did not redact output: %#v", results)
	}
	for _, event := range events {
		if strings.Contains(string(event.Payload), secret) {
			t.Fatalf("event persisted sensitive output: %s", event.Payload)
		}
	}
}

func TestRunnerGivesEveryCheckTheConfiguredBudget(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		configure time.Duration
		want      time.Duration
	}{
		{name: "configured", configure: 45 * time.Minute, want: 45 * time.Minute},
		{name: "unset falls back", configure: 0, want: defaultTimeout},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			process := &recordingRunner{}
			// The stage bound is kept out of the way, because what is under test
			// is the per-check budget and the stage bound cuts a check to what the
			// stage has left.
			results, _, err := (Runner{Process: process, Timeout: test.configure, StageTimeout: 2 * time.Hour}).Run(
				context.Background(),
				Request{RunID: "run-0123456789abcdef0123456789abcdef", Directory: t.TempDir(), Commands: []string{"true"}, LastSequence: 0},
				nil,
			)
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if process.command.Timeout != test.want {
				t.Fatalf("command timeout = %s, want %s", process.command.Timeout, test.want)
			}
			if len(results) != 1 || results[0].Timeout != test.want {
				t.Fatalf("Run() results = %#v, want the budget recorded as %s", results, test.want)
			}
		})
	}
}

func TestRunnerReportsElapsedAgainstTheBudgetOnEveryCheck(t *testing.T) {
	t.Parallel()

	started := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	process := &recordingRunner{result: execution.ProcessResult{
		Status:     execution.ProcessTimedOut,
		ExitCode:   -1,
		StartedAt:  started,
		FinishedAt: started.Add(11 * time.Minute),
	}}
	var completed []execution.Event
	results, _, err := (Runner{Process: process, Timeout: 10 * time.Minute}).Run(
		context.Background(),
		Request{RunID: "run-0123456789abcdef0123456789abcdef", Directory: t.TempDir(), Commands: []string{"make test"}, LastSequence: 0},
		func(event execution.Event) error {
			if event.Type == execution.EventCommandCompleted {
				completed = append(completed, event)
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(results) != 1 || results[0].Elapsed() != 11*time.Minute || results[0].Timeout != 10*time.Minute {
		t.Fatalf("Run() results = %#v", results)
	}
	if len(completed) != 1 {
		t.Fatalf("completion events = %d, want 1", len(completed))
	}
	payload := string(completed[0].Payload)
	for _, want := range []string{`"elapsed":"11m0s"`, `"timeout":"10m0s"`} {
		if !strings.Contains(payload, want) {
			t.Fatalf("completion payload = %s, want it to contain %s", payload, want)
		}
	}
}

// A check is one of the project's own commands, so a toolchain that cannot
// write its build cache fails it at setup with nothing about the change to show
// for it. The redirect is the same one the run's own probe was given, pointed
// inside the repository being checked, so the two share what is already built.
func TestEveryCheckIsGivenABuildCacheTheRunMayWrite(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	if err := os.MkdirAll(filepath.Join(directory, ".git"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	process := &recordingRunner{}
	if _, _, err := (Runner{Process: process}).Run(
		context.Background(),
		Request{RunID: "run-0123456789abcdef0123456789abcdef", Directory: directory, Commands: []string{"true"}, LastSequence: 0},
		nil,
	); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	want := "GOCACHE=" + filepath.Join(directory, ".git", "yoyodyne", "go-build")
	if !slices.Contains(process.command.Env, want) {
		t.Fatalf("the check's environment does not carry %q: %v", want, process.command.Env)
	}
}

// A check verbose enough to outrun what the runner retains still passes on its
// own exit, and the run's record says the retained copy is cut rather than
// leaving a short one to be read as everything the check said. The whole of it
// is in the event log, because every line was emitted there on the way past.
func TestACheckTooVerboseToRetainStillPassesAndSaysItWasCut(t *testing.T) {
	t.Parallel()

	const runID = "run-0123456789abcdef0123456789abcdef"
	marker := "[output truncated at 8388608 bytes; the whole of it is in " + execution.EventLogOf(runID) + "]"
	process := &recordingRunner{result: execution.ProcessResult{
		Status:           execution.ProcessSucceeded,
		Stdout:           "the part that fit\n" + marker + "\n",
		OutputTruncation: marker,
	}}
	var events []execution.Event
	results, _, err := (Runner{Process: process}).Run(
		context.Background(),
		Request{RunID: runID, Directory: t.TempDir(), Commands: []string{"noisy-check"}, LastSequence: 0},
		func(event execution.Event) error {
			events = append(events, event)
			return nil
		},
	)
	if err != nil {
		t.Fatalf("Run() error = %v, want a verbose check to be no error at all", err)
	}
	if len(results) != 1 || !results[0].Passed {
		t.Fatalf("Run() results = %#v, want the check judged on its own exit", results)
	}
	if process.command.OutputRecord != execution.EventLogOf(runID) {
		t.Fatalf("check ran with OutputRecord %q, want the run's event log named", process.command.OutputRecord)
	}
	var said bool
	for _, event := range events {
		if event.Type == execution.EventProcessOutput && strings.Contains(string(event.Payload), "output truncated") {
			said = true
		}
	}
	if !said {
		t.Fatal("no event says the check's output was truncated, so the record is silent about a cut copy")
	}
}

type recordingRunner struct {
	command execution.Command
	result  execution.ProcessResult
}

func (r *recordingRunner) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	r.command = command
	if r.result.Status == "" {
		return execution.ProcessResult{Status: execution.ProcessSucceeded}, nil
	}
	return r.result, nil
}

// A stage is bounded as a whole, not only check by check. With three checks
// each inside its own budget, the stage still ends where its bound is: the
// check running when the bound arrives is given only what the stage has left,
// and a check the stage has nothing left for is recorded as stopped without
// being started — so the record says which check the bound stopped rather than
// showing a check that ran for no time.
func TestTheStageBoundStopsTheCheckItArrivesDuringAndStartsNothingAfterIt(t *testing.T) {
	t.Parallel()

	clock := &steppingClock{now: time.Date(2026, 9, 19, 6, 0, 0, 0, time.UTC)}
	process := &advancingRunner{clock: clock, runs: 12 * time.Minute}
	var started []string
	results, _, err := (Runner{Process: process, Clock: clock, Timeout: 30 * time.Minute, StageTimeout: 30 * time.Minute}).Run(
		context.Background(),
		Request{
			RunID: "run-0123456789abcdef0123456789abcdef", Directory: t.TempDir(),
			Commands: []string{"make fmtcheck", "make test", "make race", "make vet"},
			Started:  func(command string, _ time.Duration) { started = append(started, command) },
		},
		nil,
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	// 12m, 12m, and then only 6m of the 30m stage remain for the third check.
	// Each check is given what the stage has left where that is less than its
	// own budget, so the second is cut to 18m and passes inside it, and the
	// third is cut to 6m and stopped there.
	if len(results) != 3 {
		t.Fatalf("results = %d, want the stage to end at the third check: %#v", len(results), results)
	}
	if got := process.budgets; !reflect.DeepEqual(got, []time.Duration{30 * time.Minute, 18 * time.Minute, 6 * time.Minute}) {
		t.Fatalf("budgets given = %v, want each check cut to what the stage had left", got)
	}
	third := results[2]
	if third.Passed || third.Process.Status != execution.ProcessTimedOut || !third.StoppedByStage {
		t.Fatalf("third result = %#v, want it stopped by the stage", third)
	}
	if third.StageTimeout != 30*time.Minute || third.StageElapsed != 30*time.Minute {
		t.Fatalf("stage figures = %s of %s, want the stage recorded as spent whole", third.StageElapsed, third.StageTimeout)
	}
	if results[0].StoppedByStage || results[1].StoppedByStage || results[1].StageElapsed != 24*time.Minute {
		t.Fatalf("earlier results = %#v, want them passed with the stage's running spend on each", results[:2])
	}
	if !reflect.DeepEqual(started, []string{"make fmtcheck", "make test", "make race"}) {
		t.Fatalf("started = %v, want the fourth check never started", started)
	}
}

func TestACheckTheStageHasNothingLeftForIsRecordedAsStoppedWithoutRunning(t *testing.T) {
	t.Parallel()

	clock := &steppingClock{now: time.Date(2026, 9, 19, 6, 0, 0, 0, time.UTC)}
	process := &advancingRunner{clock: clock, runs: 10 * time.Minute}
	var completed []execution.Event
	results, _, err := (Runner{Process: process, Clock: clock, Timeout: 10 * time.Minute, StageTimeout: 10 * time.Minute}).Run(
		context.Background(),
		Request{RunID: "run-0123456789abcdef0123456789abcdef", Directory: t.TempDir(), Commands: []string{"make test", "make race"}},
		func(event execution.Event) error {
			if event.Type == execution.EventCommandCompleted {
				completed = append(completed, event)
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(results) != 2 || len(process.budgets) != 1 {
		t.Fatalf("results = %#v with %d processes run, want the second check recorded and never run", results, len(process.budgets))
	}
	second := results[1]
	if !second.StoppedByStage || second.Process.Status != execution.ProcessTimedOut || second.Elapsed() != 0 {
		t.Fatalf("second result = %#v, want it stopped by the stage with no elapsed time", second)
	}
	if len(completed) != 2 {
		t.Fatalf("completion events = %d, want one per recorded check", len(completed))
	}
	payload := string(completed[1].Payload)
	for _, want := range []string{`"stopped_by_stage":true`, `"stage_timeout":"10m0s"`, `"stage_elapsed":"10m0s"`} {
		if !strings.Contains(payload, want) {
			t.Fatalf("completion payload = %s, want it to contain %s", payload, want)
		}
	}
}

// A check killed at its own budget with the stage still to spare is the
// per-check bound's doing, and raising the stage bound would not have saved it.
func TestACheckStoppedAtItsOwnBudgetIsNotStoppedByTheStage(t *testing.T) {
	t.Parallel()

	clock := &steppingClock{now: time.Date(2026, 9, 19, 6, 0, 0, 0, time.UTC)}
	process := &advancingRunner{clock: clock, runs: 10 * time.Minute, status: execution.ProcessTimedOut}
	results, _, err := (Runner{Process: process, Clock: clock, Timeout: 10 * time.Minute, StageTimeout: time.Hour}).Run(
		context.Background(),
		Request{RunID: "run-0123456789abcdef0123456789abcdef", Directory: t.TempDir(), Commands: []string{"make race"}},
		nil,
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(results) != 1 || results[0].StoppedByStage || results[0].Process.Status != execution.ProcessTimedOut {
		t.Fatalf("results = %#v, want a check stopped at its own budget", results)
	}
}

// What the harness knows about the change reaches every check through its
// environment, beside the build cache it is already given.
func TestEveryCheckIsToldWhatTheRequestCarries(t *testing.T) {
	t.Parallel()

	process := &recordingRunner{}
	if _, _, err := (Runner{Process: process}).Run(
		context.Background(),
		Request{
			RunID: "run-0123456789abcdef0123456789abcdef", Directory: t.TempDir(), Commands: []string{"true"},
			Env: []string{ChangedGoPackagesVariable + "=./internal/checks"},
		},
		nil,
	); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !slices.Contains(process.command.Env, ChangedGoPackagesVariable+"=./internal/checks") {
		t.Fatalf("the check's environment does not carry the narrowing: %v", process.command.Env)
	}
	if !slices.ContainsFunc(process.command.Env, func(entry string) bool { return strings.HasPrefix(entry, "PATH=") }) {
		t.Fatal("the check's environment lost this process's own")
	}
}

// steppingClock is a clock the test moves, so a stage's arithmetic is checked
// against known spans rather than against wall-clock sleeps.
type steppingClock struct {
	now time.Time
}

func (c *steppingClock) Now() time.Time { return c.now }

func (c *steppingClock) advance(by time.Duration) { c.now = c.now.Add(by) }

// advancingRunner stands in for a check that takes a known time: each run moves
// the clock by `runs`, cut to the budget it was given, and reports timed out
// where the budget was what stopped it.
type advancingRunner struct {
	clock   *steppingClock
	runs    time.Duration
	status  execution.ProcessStatus
	budgets []time.Duration
}

func (r *advancingRunner) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	r.budgets = append(r.budgets, command.Timeout)
	started := r.clock.Now()
	took := r.runs
	status := execution.ProcessSucceeded
	if r.status != "" {
		status = r.status
	}
	if command.Timeout > 0 && took > command.Timeout {
		took = command.Timeout
		status = execution.ProcessTimedOut
	}
	r.clock.advance(took)
	exitCode := 0
	if status != execution.ProcessSucceeded {
		exitCode = -1
	}
	return execution.ProcessResult{Status: status, ExitCode: exitCode, StartedAt: started, FinishedAt: r.clock.Now()}, nil
}

// A request may run with a budget of its own and no stage bound, which is what
// a landing asks for: the suite moved to the landing is the one too long for
// the gate's stage bound, so each landing check is given its own budget whole
// and the list may take the sum of them.
func TestARequestMayRunUnboundedWithItsOwnBudget(t *testing.T) {
	t.Parallel()

	clock := &steppingClock{now: time.Date(2026, 9, 19, 6, 0, 0, 0, time.UTC)}
	process := &advancingRunner{clock: clock, runs: 50 * time.Minute}
	results, _, err := (Runner{Process: process, Clock: clock, Timeout: 10 * time.Minute, StageTimeout: 30 * time.Minute}).Run(
		context.Background(),
		Request{
			RunID: "run-0123456789abcdef0123456789abcdef", Directory: t.TempDir(),
			Commands: []string{"make race", "make test"},
			Timeout:  2 * time.Hour, Unbounded: true,
		},
		nil,
	)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(results) != 2 || !results[0].Passed || !results[1].Passed {
		t.Fatalf("results = %#v, want both checks run whole past the runner's own bounds", results)
	}
	if got := process.budgets; !reflect.DeepEqual(got, []time.Duration{2 * time.Hour, 2 * time.Hour}) {
		t.Fatalf("budgets given = %v, want the request's own budget each", got)
	}
	if results[1].StoppedByStage || results[1].StageTimeout != 0 || results[1].StageElapsed != 100*time.Minute {
		t.Fatalf("second result = %#v, want no stage bound and the list's spend recorded", results[1])
	}
}
