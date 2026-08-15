package checks

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"yoyodyne/internal/execution"
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

type recordingRunner struct {
	command execution.Command
}

func (r *recordingRunner) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	r.command = command
	return execution.ProcessResult{Status: execution.ProcessSucceeded}, nil
}
