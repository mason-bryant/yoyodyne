package checks

import (
	"context"
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
