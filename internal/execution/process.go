package execution

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

const defaultMaxOutputBytes = 8 << 20

type ProcessStatus string

const (
	ProcessSucceeded ProcessStatus = "succeeded"
	ProcessFailed    ProcessStatus = "failed"
	ProcessCancelled ProcessStatus = "cancelled"
	ProcessTimedOut  ProcessStatus = "timed_out"
)

type Stream string

const (
	StreamStdout Stream = "stdout"
	StreamStderr Stream = "stderr"
)

type Command struct {
	Name           string
	Args           []string
	Dir            string
	Env            []string
	Stdin          io.Reader
	Timeout        time.Duration
	MaxOutputBytes int
	Redactor       Redactor
}

type Output struct {
	Stream    Stream
	Text      string
	Timestamp time.Time
}

type ProcessResult struct {
	Status     ProcessStatus
	ExitCode   int
	StartedAt  time.Time
	FinishedAt time.Time
	Stdout     string
	Stderr     string
}

type OutputObserver func(Output)

type ProcessRunner interface {
	Run(ctx context.Context, command Command, observer OutputObserver) (ProcessResult, error)
}

type Clock interface {
	Now() time.Time
}

type RealClock struct{}

func (RealClock) Now() time.Time {
	return time.Now().UTC()
}

type OSProcessRunner struct {
	Clock Clock
}

func (r OSProcessRunner) Run(ctx context.Context, command Command, observer OutputObserver) (ProcessResult, error) {
	if strings.TrimSpace(command.Name) == "" {
		return ProcessResult{}, errors.New("command name is required")
	}
	clock := r.Clock
	if clock == nil {
		clock = RealClock{}
	}
	maxOutput := command.MaxOutputBytes
	if maxOutput == 0 {
		maxOutput = defaultMaxOutputBytes
	}
	if maxOutput < 0 {
		return ProcessResult{}, errors.New("max output bytes cannot be negative")
	}
	if ctx.Err() != nil {
		now := clock.Now()
		status := ProcessCancelled
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			status = ProcessTimedOut
		}
		return ProcessResult{
			Status:     status,
			ExitCode:   -1,
			StartedAt:  now,
			FinishedAt: now,
		}, nil
	}

	runCtx := ctx
	cancel := func() {}
	if command.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, command.Timeout)
	}
	defer cancel()

	process := exec.CommandContext(runCtx, command.Name, command.Args...)
	configureProcessTree(process)
	process.Dir = command.Dir
	process.Stdin = command.Stdin
	if command.Env != nil {
		process.Env = command.Env
	}
	stdout, err := process.StdoutPipe()
	if err != nil {
		return ProcessResult{}, fmt.Errorf("create stdout pipe: %w", err)
	}
	stderr, err := process.StderrPipe()
	if err != nil {
		return ProcessResult{}, fmt.Errorf("create stderr pipe: %w", err)
	}

	result := ProcessResult{StartedAt: clock.Now(), ExitCode: -1}
	if err := process.Start(); err != nil {
		return result, fmt.Errorf("start %q: %w", command.Name, err)
	}

	outputs := make(chan Output)
	scanErrors := make(chan error, 2)
	var scanners sync.WaitGroup
	scanners.Add(2)
	go scanOutput(stdout, StreamStdout, clock, command.Redactor, outputs, scanErrors, &scanners)
	go scanOutput(stderr, StreamStderr, clock, command.Redactor, outputs, scanErrors, &scanners)
	go func() {
		scanners.Wait()
		close(outputs)
		close(scanErrors)
	}()

	var stdoutBuffer bytes.Buffer
	var stderrBuffer bytes.Buffer
	outputBytes := 0
	outputLimitExceeded := false
	for output := range outputs {
		lineBytes := len(output.Text) + 1
		if outputBytes+lineBytes > maxOutput {
			outputLimitExceeded = true
			continue
		}
		outputBytes += lineBytes
		if output.Stream == StreamStdout {
			stdoutBuffer.WriteString(output.Text)
			stdoutBuffer.WriteByte('\n')
		} else {
			stderrBuffer.WriteString(output.Text)
			stderrBuffer.WriteByte('\n')
		}
		if observer != nil {
			observer(output)
		}
	}

	waitErr := process.Wait()
	result.FinishedAt = clock.Now()
	result.Stdout = stdoutBuffer.String()
	result.Stderr = stderrBuffer.String()
	if process.ProcessState != nil {
		result.ExitCode = process.ProcessState.ExitCode()
	}

	var readErrors []error
	for scanErr := range scanErrors {
		if scanErr != nil {
			readErrors = append(readErrors, scanErr)
		}
	}
	if outputLimitExceeded {
		readErrors = append(readErrors, fmt.Errorf("process output exceeded %d bytes", maxOutput))
	}
	if len(readErrors) > 0 {
		return result, errors.Join(readErrors...)
	}

	switch {
	case errors.Is(runCtx.Err(), context.DeadlineExceeded):
		result.Status = ProcessTimedOut
	case errors.Is(ctx.Err(), context.Canceled):
		result.Status = ProcessCancelled
	case waitErr != nil:
		result.Status = ProcessFailed
	default:
		result.Status = ProcessSucceeded
	}
	return result, nil
}

func scanOutput(reader io.Reader, stream Stream, clock Clock, redactor Redactor, outputs chan<- Output, scanErrors chan<- error, waitGroup *sync.WaitGroup) {
	defer waitGroup.Done()

	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		outputs <- Output{
			Stream:    stream,
			Text:      redactor.Redact(scanner.Text()),
			Timestamp: clock.Now(),
		}
	}
	if err := scanner.Err(); err != nil {
		scanErrors <- fmt.Errorf("read %s: %w", stream, err)
	}
}

type Redactor struct {
	values []string
}

func NewRedactor(values ...string) Redactor {
	seen := make(map[string]struct{})
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		for _, candidate := range redactionCandidates(value) {
			if _, exists := seen[candidate]; exists {
				continue
			}
			seen[candidate] = struct{}{}
			filtered = append(filtered, candidate)
		}
	}
	// Replace longer overlapping values first. Otherwise redacting "token"
	// before "token-suffix" would persist "[REDACTED]-suffix".
	sort.SliceStable(filtered, func(i, j int) bool {
		return len(filtered[i]) > len(filtered[j])
	})
	return Redactor{values: filtered}
}

func redactionCandidates(value string) []string {
	if value == "" {
		return nil
	}
	candidates := []string{value}
	if !strings.ContainsAny(value, "\r\n") {
		return candidates
	}
	// Output is scanned one line at a time. Retain the complete value for
	// other call sites and also protect every non-empty line independently.
	for _, line := range strings.FieldsFunc(value, func(r rune) bool {
		return r == '\r' || r == '\n'
	}) {
		if line != value {
			candidates = append(candidates, line)
		}
	}
	return candidates
}

// SensitiveEnvironmentValues returns values held in conventionally sensitive
// environment variables. Provider authentication remains CLI-managed, but
// subprocess output still needs these values removed before it becomes a
// durable event or result.
func SensitiveEnvironmentValues(environment []string) []string {
	seen := make(map[string]struct{})
	values := make([]string, 0)
	for _, entry := range environment {
		name, value, found := strings.Cut(entry, "=")
		if !found || value == "" || !sensitiveEnvironmentName(name) {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	return values
}

func sensitiveEnvironmentName(name string) bool {
	upper := strings.ToUpper(name)
	for _, marker := range []string{
		"TOKEN",
		"PASSWORD",
		"PASSWD",
		"API_KEY",
		"PRIVATE_KEY",
		"CLIENT_SECRET",
		"ACCESS_KEY",
		"CREDENTIAL",
	} {
		if strings.Contains(upper, marker) {
			return true
		}
	}
	return strings.HasSuffix(upper, "_SECRET")
}

func (r Redactor) Redact(value string) string {
	for _, secret := range r.values {
		value = strings.ReplaceAll(value, secret, "[REDACTED]")
	}
	return value
}
