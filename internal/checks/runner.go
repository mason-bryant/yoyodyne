package checks

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// defaultTimeout is what a runner built without a budget gives each check. The
// harness wires execution.check_timeout into every runner it builds, so this
// only covers a runner assembled in code, and it matches that configured
// default so the two never describe different behavior.
const defaultTimeout = 30 * time.Minute

type Result struct {
	Command string                  `json:"command"`
	Process execution.ProcessResult `json:"process"`
	Passed  bool                    `json:"passed"`
	// Timeout is the budget this check was given. It is recorded beside the
	// result rather than left implicit because elapsed time alone says nothing
	// about how close a suite is to the ceiling: a check that grows past the
	// budget kills work that was passing, and the only warning is the two
	// numbers side by side before it happens.
	Timeout time.Duration `json:"timeout"`
}

// Elapsed is how long the check actually ran.
func (r Result) Elapsed() time.Duration {
	return r.Process.FinishedAt.Sub(r.Process.StartedAt)
}

type Runner struct {
	Process execution.ProcessRunner
	Clock   execution.Clock
	Shell   string
	// Timeout is the total budget each check gets, the whole time it may run
	// rather than the time it may stay quiet: a suite that keeps printing is
	// still spending it. Zero falls back to defaultTimeout.
	Timeout      time.Duration
	RedactValues []string
}

func (r Runner) Run(ctx context.Context, runID, directory string, commands []string, lastSequence uint64, sink func(execution.Event) error) ([]Result, uint64, error) {
	if r.Process == nil {
		return nil, lastSequence, errors.New("check process runner is required")
	}
	if strings.TrimSpace(runID) == "" || strings.TrimSpace(directory) == "" {
		return nil, lastSequence, errors.New("run id and check directory are required")
	}
	clock := r.Clock
	if clock == nil {
		clock = execution.RealClock{}
	}
	shell := r.Shell
	if shell == "" {
		shell = "/bin/sh"
	}
	timeout := r.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	sequence := execution.NewSequence(lastSequence)
	lastAccepted := lastSequence
	results := make([]Result, 0, len(commands))
	redactor := execution.NewRedactor(r.RedactValues...)
	for _, command := range commands {
		if strings.TrimSpace(command) == "" {
			return results, lastAccepted, errors.New("check command cannot be empty")
		}
		safeCommand := redactor.Redact(command)
		if err := emit(runID, sequence, clock, sink, execution.EventCommandStarted, map[string]any{"command": safeCommand, "kind": "check"}); err != nil {
			return results, lastAccepted, err
		}
		lastAccepted = sequence.Last()
		var observerErrors []error
		processResult, err := r.Process.Run(ctx, execution.Command{
			Name: shell,
			Args: []string{"-c", command},
			Dir:  directory,
			// The checks are the project's own commands, so a toolchain that
			// cannot write its build cache fails them at setup with nothing
			// about the change to show for it. The environment is this
			// process's own with the cache pointed inside the repository being
			// checked -- the same redirect the run's own probe was given, so
			// the two share what has already been compiled.
			Env:     execution.WithGoBuildCache(nil, directory),
			Timeout: timeout,
			// Every line a check prints becomes an event in this run's log, so
			// the log is where the whole of it is once the runner stops growing
			// its in-memory copy. A suite that prints a great deal is not a
			// suite that failed, and it is never a reason to stop the command
			// producing it.
			OutputRecord: "the event log for " + runID,
			Redactor:     redactor,
		}, func(output execution.Output) {
			if len(observerErrors) > 0 {
				return
			}
			if observerErr := emit(runID, sequence, clock, sink, execution.EventProcessOutput, map[string]any{
				"kind":    "check",
				"command": safeCommand,
				"stream":  output.Stream,
				"text":    output.Text,
			}); observerErr != nil {
				observerErrors = append(observerErrors, observerErr)
				return
			}
			lastAccepted = sequence.Last()
		})
		if err != nil {
			return results, lastAccepted, fmt.Errorf("run check %q: %w", safeCommand, err)
		}
		if len(observerErrors) > 0 {
			return results, lastAccepted, errors.Join(observerErrors...)
		}
		passed := processResult.Status == execution.ProcessSucceeded
		result := Result{Command: safeCommand, Process: processResult, Passed: passed, Timeout: timeout}
		results = append(results, result)
		// Every check reports what it spent against what it was allowed, not
		// only the one that ran out: a suite walking toward its ceiling is
		// visible in the event stream long before it reaches it.
		if err := emit(runID, sequence, clock, sink, execution.EventCommandCompleted, map[string]any{
			"command":   safeCommand,
			"kind":      "check",
			"passed":    passed,
			"status":    processResult.Status,
			"exit_code": processResult.ExitCode,
			"elapsed":   result.Elapsed().String(),
			"timeout":   timeout.String(),
			// A check whose output outgrew what the result retains says so
			// here, beside what it exited with: the events above hold every
			// line, and this is what tells a reader that the result carries
			// less than they do.
			"output_truncated": processResult.OutputTruncated,
			"truncated_bytes":  processResult.TruncatedBytes,
		}); err != nil {
			return results, lastAccepted, err
		}
		lastAccepted = sequence.Last()
		if !passed {
			break
		}
	}
	return results, lastAccepted, nil
}

func emit(runID string, sequence *execution.Sequence, clock execution.Clock, sink func(execution.Event) error, eventType execution.EventType, payload any) error {
	event, err := execution.NewEvent(runID, sequence.Next(), clock.Now(), eventType, "harness.checks", payload)
	if err != nil {
		return err
	}
	if sink != nil {
		if err := sink(event); err != nil {
			return fmt.Errorf("persist check event: %w", err)
		}
	}
	return nil
}
