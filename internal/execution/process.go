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

// defaultMaxOutputBytes bounds what a result carries back in memory when a
// caller names no bound of its own. It is a bound on the copy and never on the
// process: a command that says more than this keeps running, keeps being
// observed, and finishes on its merits.
//
// It was the latter until yoyodyne-ifd.258. A run whose provider process wrote
// more than this had every later line dropped before the observer saw it —
// which for the Claude Code adapter is the stream the events and the cost are
// parsed from — and then had the whole invocation failed for the overflow.
// run-32e3f059 died that way on 2026-09-03 with nothing recorded of what it had
// spent, and a parity harness diffing execution traces is exactly the workload
// that bursts stdout. Retaining less is the answer to a process that says a lot;
// killing it is not.
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

// ErrProcessNotStarted reports a process the operating system never began
// running: the binary could not be executed, or the sandbox it would have run
// in refused to spawn it. It is a sentinel rather than only a message because a
// caller has to tell it from a process that ran and failed — nothing was asked
// and nothing answered, so what the invocation would have produced is not
// evidence about the work but about the machine.
var ErrProcessNotStarted = errors.New("the process was never started")

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
	IdleTimeout time.Duration
	// MaxOutputBytes bounds what the result retains of stdout and stderr
	// together, and nothing else. Output past it is left out of the retained
	// copy and replaced by a marker naming where the whole of it is; the process
	// is not stopped, the observer still sees every line, and the result is
	// whatever the process itself earned. Zero takes defaultMaxOutputBytes, and
	// a negative bound is a caller's mistake rather than an unlimited one.
	MaxOutputBytes int
	// OutputRecord names the durable record that holds the whole of this
	// command's output — the run's event log, for a caller whose observer writes
	// every line to one. The truncation marker names it, so a reader of the
	// bounded copy is sent to the rest rather than told that some is missing.
	// This is the rule a Slack message too long to post already follows. A
	// caller that keeps nothing durable leaves it empty, and the marker says
	// that instead rather than pointing at a record nobody wrote.
	OutputRecord string
	Redactor     Redactor
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
	// OutputTruncated says Stdout and Stderr are a bounded copy rather than the
	// whole of what the process said. It is not a failure and never decides the
	// status: the process ran to whatever end it ran to, and this describes what
	// the result kept of it. A caller that parses retained output has to consult
	// it, because a bounded copy parses as a shorter answer rather than as an
	// error.
	OutputTruncated bool
	// TruncatedBytes is how much of the output the result did not keep, which is
	// what a record of the truncation quotes.
	TruncatedBytes int
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
		return result, fmt.Errorf("%w: start %q: %w", ErrProcessNotStarted, command.Name, err)
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
	stdoutDropped := 0
	stderrDropped := 0
	// Retention stops for good at the first line that does not fit rather than
	// per line, so the copy is the start of what was said and not an arbitrary
	// selection from it: a short line kept after a long one was dropped would
	// read as consecutive output that never was.
	retaining := true
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
			switch {
			case !retaining || outputBytes+lineBytes > maxOutput:
				// Past the bound the runner stops keeping and carries on. The
				// observer still gets the line, because the observer is what
				// writes output to the run's durable record: the whole of it
				// survives there even though this result carries only the start.
				retaining = false
				if output.Stream == StreamStdout {
					stdoutDropped += lineBytes
				} else {
					stderrDropped += lineBytes
				}
			case output.Stream == StreamStdout:
				outputBytes += lineBytes
				stdoutBuffer.WriteString(output.Text)
				stdoutBuffer.WriteByte('\n')
			default:
				outputBytes += lineBytes
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
	result.Stdout = retainedOutput(stdoutBuffer.String(), StreamStdout, stdoutDropped, command.OutputRecord)
	result.Stderr = retainedOutput(stderrBuffer.String(), StreamStderr, stderrDropped, command.OutputRecord)
	result.TruncatedBytes = stdoutDropped + stderrDropped
	result.OutputTruncated = result.TruncatedBytes > 0
	if process.ProcessState != nil {
		result.ExitCode = process.ProcessState.ExitCode()
	}

	var readErrors []error
	for scanErr := range scanErrors {
		if scanErr != nil {
			readErrors = append(readErrors, scanErr)
		}
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

// retainedOutput is one stream's bounded copy, with a marker where the bound cut
// it. Nothing is added to a copy that kept everything, so an ordinary result
// reads exactly as it always did.
//
// The marker sits on top of the bound rather than inside it, which is the one
// place this differs from the Slack rule it otherwise follows. The bound there
// is what the surface will accept and the marker has to fit under it; the bound
// here is on how much of a process's output the harness holds in memory, and a
// caller that then has a smaller bound of its own applies it to the copy it was
// handed, marker and all.
func retainedOutput(text string, stream Stream, dropped int, record string) string {
	if dropped <= 0 {
		return text
	}
	return text + truncationMarker(stream, dropped, record)
}

// truncationMarker says what the bounded copy left out and where the whole of it
// is, in the words a Slack message too long to post already uses. A copy that
// stopped without saying so is the failure this replaced: silence reads as a
// process that stopped talking rather than as a harness that stopped listening.
func truncationMarker(stream Stream, dropped int, record string) string {
	if where := strings.TrimSpace(record); where != "" {
		return fmt.Sprintf("… %d further bytes of %s truncated; the whole of it is in %s\n", dropped, stream, where)
	}
	return fmt.Sprintf("… %d further bytes of %s truncated; nothing durable holds the rest\n", dropped, stream)
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
