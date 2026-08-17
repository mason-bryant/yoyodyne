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
	// ProcessStalled is a process that stopped producing output for longer than
	// its idle bound allowed. It is deliberately not ProcessTimedOut: a stalled
	// process demonstrably stopped doing anything, while a timed-out one may have
	// been working the whole time and simply ran out of budget.
	ProcessStalled ProcessStatus = "stalled"
)

type Stream string

const (
	StreamStdout Stream = "stdout"
	StreamStderr Stream = "stderr"
)

type Command struct {
	Name  string
	Args  []string
	Dir   string
	Env   []string
	Stdin io.Reader
	// Timeout is the total budget: how long the process may run at all,
	// whatever it is doing. It is the right and only bound for a short command
	// whose duration is known, such as a Git invocation.
	Timeout time.Duration
	// IdleTimeout bounds the gap between one line of output and the next, which
	// is a different question from the total budget: a process still producing
	// output is demonstrably working, however long it has been running, and one
	// producing nothing is stalled however recently it started. Zero or less
	// disables the check, which is what a command whose output arrives in one
	// burst at the end needs.
	IdleTimeout    time.Duration
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
	cancelTimeout := func() {}
	if command.Timeout > 0 {
		runCtx, cancelTimeout = context.WithTimeout(ctx, command.Timeout)
	}
	defer cancelTimeout()
	processCtx, stopProcess := context.WithCancel(runCtx)
	defer stopProcess()

	process := exec.CommandContext(processCtx, command.Name, command.Args...)
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
	go scanOutput(stdout, StreamStdout, clock, command.Redactor, outputs, scanErrors, stopProcess, &scanners)
	go scanOutput(stderr, StreamStderr, clock, command.Redactor, outputs, scanErrors, stopProcess, &scanners)
	go func() {
		scanners.Wait()
		close(outputs)
		close(scanErrors)
	}()

	var stdoutBuffer bytes.Buffer
	var stderrBuffer bytes.Buffer
	outputBytes := 0
	outputLimitExceeded := false
	stalled := false
	idle := newIdleWatch(command.IdleTimeout)
	defer idle.stop()
drain:
	for {
		select {
		case output, received := <-outputs:
			if !received {
				break drain
			}
			// Any line at all is proof the process is still doing something, so
			// the idle bound starts over from here rather than from the start.
			idle.reset()
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
		case <-idle.expired():
			// Nothing has been produced for the whole idle bound, so the process
			// is not working on anything this runner can see. Terminate the tree
			// and keep draining until both pipes close, so whatever it did say
			// before it went quiet is still reported.
			stalled = true
			stopProcess()
			idle.stop()
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
	case stalled:
		// The runner stopped this process itself, so the kill it observes is its
		// own and says nothing about what the process was doing.
		result.Status = ProcessStalled
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

// idleWatch bounds the gap between one line of process output and the next. It
// is the whole of the runner's liveness signal: a process that keeps producing
// output keeps resetting it, and only one that produces nothing at all trips it.
type idleWatch struct {
	timeout time.Duration
	timer   *time.Timer
}

func newIdleWatch(timeout time.Duration) *idleWatch {
	watch := &idleWatch{timeout: timeout}
	if timeout > 0 {
		watch.timer = time.NewTimer(timeout)
	}
	return watch
}

// expired reports the idle bound elapsing. A disabled watch returns a nil
// channel, which never fires, so a caller selecting on it needs no special case.
func (w *idleWatch) expired() <-chan time.Time {
	if w.timer == nil {
		return nil
	}
	return w.timer.C
}

func (w *idleWatch) reset() {
	if w.timer == nil {
		return
	}
	w.timer.Reset(w.timeout)
}

// stop disables the watch for good. It is idempotent, so a watch that has
// already tripped can still be cleaned up by the caller's deferred stop.
func (w *idleWatch) stop() {
	if w.timer == nil {
		return
	}
	w.timer.Stop()
	w.timer = nil
}

func scanOutput(reader io.Reader, stream Stream, clock Clock, redactor Redactor, outputs chan<- Output, scanErrors chan<- error, stopProcess context.CancelFunc, waitGroup *sync.WaitGroup) {
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
		// A scanner that stops draining can leave the child blocked on a full
		// pipe. Terminate the process tree immediately so the other stream
		// closes and Run can return the read failure without waiting for timeout.
		stopProcess()
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
