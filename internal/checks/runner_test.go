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
		"run-0123456789abcdef0123456789abcdef",
		t.TempDir(),
		[]string{"printf 'ok\\n'", "exit 3", "exit 0"},
		5,
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
		"run-0123456789abcdef0123456789abcdef",
		t.TempDir(),
		[]string{"true"},
		0,
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
				"run-0123456789abcdef0123456789abcdef",
				t.TempDir(),
				[]string{"printf 'output\\n'"},
				5,
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
		"run-0123456789abcdef0123456789abcdef",
		t.TempDir(),
		[]string{"printf 'check-secret-value\\n'"},
		0,
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
			results, _, err := (Runner{Process: process, Timeout: test.configure}).Run(
				context.Background(),
				"run-0123456789abcdef0123456789abcdef",
				t.TempDir(),
				[]string{"true"},
				0,
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
		"run-0123456789abcdef0123456789abcdef",
		t.TempDir(),
		[]string{"make test"},
		0,
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
		"run-0123456789abcdef0123456789abcdef",
		directory,
		[]string{"true"},
		0,
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
		runID,
		t.TempDir(),
		[]string{"noisy-check"},
		0,
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
