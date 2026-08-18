package checks

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

const defaultTimeout = 10 * time.Minute

type Result struct {
	Command string                  `json:"command"`
	Process execution.ProcessResult `json:"process"`
	Passed  bool                    `json:"passed"`
}

type Runner struct {
	Process      execution.ProcessRunner
	Clock        execution.Clock
	Shell        string
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
			Name:     shell,
			Args:     []string{"-c", command},
			Dir:      directory,
			Timeout:  timeout,
			Redactor: redactor,
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
		result := Result{Command: safeCommand, Process: processResult, Passed: passed}
		results = append(results, result)
		if err := emit(runID, sequence, clock, sink, execution.EventCommandCompleted, map[string]any{
			"command":   safeCommand,
			"kind":      "check",
			"passed":    passed,
			"status":    processResult.Status,
			"exit_code": processResult.ExitCode,
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
