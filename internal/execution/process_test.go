package execution

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestOSProcessRunnerSuccessAndRedaction(t *testing.T) {
	t.Parallel()

	var observed []Output
	result, err := (OSProcessRunner{}).Run(context.Background(), helperCommand("success", "secret-value"), func(output Output) {
		observed = append(observed, output)
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Status != ProcessSucceeded || result.ExitCode != 0 {
		t.Fatalf("Run() result = %#v", result)
	}
	if strings.Contains(result.Stdout+result.Stderr, "secret-value") {
		t.Fatal("result persisted an unredacted secret")
	}
	if !strings.Contains(result.Stdout, "[REDACTED]") || !strings.Contains(result.Stderr, "warning") {
		t.Fatalf("Run() stdout = %q, stderr = %q", result.Stdout, result.Stderr)
	}
	if len(observed) != 2 {
		t.Fatalf("observed %d outputs, want 2", len(observed))
	}
}

func TestOSProcessRunnerRedactsEveryLineOfAMultilineSecret(t *testing.T) {
	t.Parallel()

	secret := "private-key-header\nprivate-key-body\nprivate-key-footer"
	result, err := (OSProcessRunner{}).Run(context.Background(), helperCommand("success", secret), nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	for _, fragment := range strings.Split(secret, "\n") {
		if strings.Contains(result.Stdout+result.Stderr, fragment) {
			t.Fatalf("result persisted multiline secret fragment %q: %q", fragment, result.Stdout+result.Stderr)
		}
	}
	if strings.Count(result.Stdout, "[REDACTED]") != 3 {
		t.Fatalf("Run() stdout = %q, want three redactions", result.Stdout)
	}
}

func TestOSProcessRunnerFailure(t *testing.T) {
	t.Parallel()

	result, err := (OSProcessRunner{}).Run(context.Background(), helperCommand("failure", ""), nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Status != ProcessFailed || result.ExitCode != 7 {
		t.Fatalf("Run() result = %#v", result)
	}
}

func TestOSProcessRunnerTimeout(t *testing.T) {
	t.Parallel()

	command := helperCommand("sleep", "")
	command.Timeout = 20 * time.Millisecond
	result, err := (OSProcessRunner{}).Run(context.Background(), command, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Status != ProcessTimedOut {
		t.Fatalf("Run() status = %q, want %q", result.Status, ProcessTimedOut)
	}
}

// A process that says nothing for the whole idle bound is stopped as stalled,
// long before a total budget it would otherwise have to exhaust.
func TestOSProcessRunnerStopsASilentProcessAsStalled(t *testing.T) {
	t.Parallel()

	command := helperCommand("sleep", "")
	command.Timeout = 30 * time.Second
	command.IdleTimeout = 50 * time.Millisecond
	started := time.Now()
	result, err := (OSProcessRunner{}).Run(context.Background(), command, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Status != ProcessStalled {
		t.Fatalf("Run() status = %q, want %q", result.Status, ProcessStalled)
	}
	if elapsed := time.Since(started); elapsed >= 5*time.Second {
		t.Fatalf("Run() waited %s; the stall was not detected on the idle bound", elapsed)
	}
}

// A process that keeps producing output keeps proving it is working, so an idle
// bound far shorter than its total runtime never stops it.
//
// The bound has to absorb the child's own startup as well as the gaps between
// its lines: the watch begins when the process is started, and the first line
// cannot arrive until the helper binary has finished coming up, which is slow
// under the race detector and slower again beside every other parallel test.
// That is a property of this fixture rather than of a provider, whose idle bound
// is minutes and whose startup is nothing beside it.
func TestOSProcessRunnerLeavesAChattyProcessAlone(t *testing.T) {
	t.Parallel()

	command := helperCommand("chatter", "")
	command.Timeout = 60 * time.Second
	command.IdleTimeout = 2 * time.Second
	started := time.Now()
	result, err := (OSProcessRunner{}).Run(context.Background(), command, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Status != ProcessSucceeded {
		t.Fatalf("Run() status = %q, want %q for a process that never went quiet", result.Status, ProcessSucceeded)
	}
	// Outliving the idle bound is the whole claim: without it a process that
	// simply finished quickly would pass this test.
	if elapsed := time.Since(started); elapsed <= command.IdleTimeout {
		t.Fatalf("Run() returned after %s, which never outlived the %s idle bound", elapsed, command.IdleTimeout)
	}
	if lines := strings.Count(result.Stdout, "\n"); lines < 2 {
		t.Fatalf("Run() stdout = %q, want the chatter it kept producing", result.Stdout)
	}
}

// The total budget still bounds a process that is alive and producing output,
// and what stops it is reported as the budget rather than as a stall.
func TestOSProcessRunnerTimesOutAChattyProcessOnItsTotalBudget(t *testing.T) {
	t.Parallel()

	command := helperCommand("endless-chatter", "")
	command.Timeout = 150 * time.Millisecond
	command.IdleTimeout = 30 * time.Second
	result, err := (OSProcessRunner{}).Run(context.Background(), command, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Status != ProcessTimedOut {
		t.Fatalf("Run() status = %q, want %q", result.Status, ProcessTimedOut)
	}
}

func TestOSProcessRunnerCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := (OSProcessRunner{}).Run(ctx, helperCommand("sleep", ""), nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Status != ProcessCancelled {
		t.Fatalf("Run() status = %q, want %q", result.Status, ProcessCancelled)
	}
}

// A process that outruns the retained bound is truncated and not killed. The
// run it belongs to has to complete on its own merits: a run that died of its
// own diagnostics took its work and the account of what it cost with it.
func TestOSProcessRunnerTruncatesOutputInsteadOfFailingTheProcess(t *testing.T) {
	t.Parallel()

	command := helperCommand("success", "")
	command.MaxOutputBytes = 2
	command.OutputRecord = EventLogOf("run-0123456789abcdef")
	var observed []Output
	result, err := (OSProcessRunner{}).Run(context.Background(), command, func(output Output) {
		observed = append(observed, output)
	})
	if err != nil {
		t.Fatalf("Run() error = %v, want output past the bound to be no error at all", err)
	}
	if result.Status != ProcessSucceeded || result.ExitCode != 0 {
		t.Fatalf("Run() result = %#v, want the process judged on its own exit", result)
	}
	// Every line still reaches the observer, which is what puts the whole of the
	// output in the record the marker names -- and, for a provider stream, what
	// keeps the terminal result the run is priced from from being dropped.
	if len(observed) != 2 {
		t.Fatalf("observed %d outputs, want the 2 the process produced", len(observed))
	}
	if result.OutputTruncation == "" {
		t.Fatal("Run() reported no truncation for output the bound cut")
	}
	if !strings.Contains(result.OutputTruncation, EventLogOf("run-0123456789abcdef")) {
		t.Fatalf("truncation marker = %q, want the durable record named", result.OutputTruncation)
	}
	if !strings.Contains(result.Stdout, result.OutputTruncation) {
		t.Fatalf("Run() stdout = %q, want the cut copy to carry the marker", result.Stdout)
	}
	if !strings.Contains(result.Stderr, result.OutputTruncation) {
		t.Fatalf("Run() stderr = %q, want the cut copy to carry the marker", result.Stderr)
	}
}

// A caller that kept the retained copy and nothing else is told the rest is
// gone, rather than sent after a durable record nobody wrote.
func TestOSProcessRunnerSaysWhenNothingHoldsTheOutputItCut(t *testing.T) {
	t.Parallel()

	command := helperCommand("success", "")
	command.MaxOutputBytes = 2
	result, err := (OSProcessRunner{}).Run(context.Background(), command, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(result.OutputTruncation, "not retained") {
		t.Fatalf("truncation marker = %q, want the absence of a record said plainly", result.OutputTruncation)
	}
}

// What is retained of a cut stream is a prefix ending at the marker. A stream
// that has lost a line keeps losing them, rather than resuming wherever a later
// short line happened to fit and leaving a copy with a hole in the middle.
func TestOSProcessRunnerRetainsAPrefixOfACutStream(t *testing.T) {
	t.Parallel()

	command := helperCommand("counted-chatter", "")
	// Wide enough for the first few lines and nowhere near all of them, so the
	// cut lands mid-stream rather than at either end.
	command.MaxOutputBytes = 40
	result, err := (OSProcessRunner{}).Run(context.Background(), command, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.OutputTruncation == "" {
		t.Fatalf("Run() stdout = %q, want the bound to have cut it", result.Stdout)
	}
	lines := strings.Split(strings.TrimSuffix(result.Stdout, "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("Run() stdout = %q, want the lines that fit and then the marker", result.Stdout)
	}
	if lines[len(lines)-1] != result.OutputTruncation {
		t.Fatalf("last retained line = %q, want the marker to end the prefix", lines[len(lines)-1])
	}
	// The retained lines are the first ones the process wrote, in order and with
	// none missing between them.
	for index, line := range lines[:len(lines)-1] {
		if want := fmt.Sprintf("line %d", index); line != want {
			t.Fatalf("retained line %d = %q, want %q; the copy is not a prefix", index, line, want)
		}
	}
}

// A process that stayed under the bound is never marked, so the marker's
// presence is the whole of the fact a reader has to check.
func TestOSProcessRunnerLeavesOutputUnderTheBoundUnmarked(t *testing.T) {
	t.Parallel()

	result, err := (OSProcessRunner{}).Run(context.Background(), helperCommand("success", ""), nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.OutputTruncation != "" {
		t.Fatalf("Run() truncation = %q, want none for output that fit", result.OutputTruncation)
	}
}

// One line too long to hold is cut with a marker, and neither the process nor
// the run dies of it. This used to fail the whole invocation with "token too
// long": a provider stream puts one tool result on one line, so a large enough
// result took the run's work and the account of what it cost with it.
func TestOSProcessRunnerTruncatesAnOversizedLineInsteadOfFailingTheProcess(t *testing.T) {
	t.Parallel()

	command := helperCommand("oversized-line", "")
	command.Timeout = 30 * time.Second
	var observed []Output
	result, err := (OSProcessRunner{}).Run(context.Background(), command, func(output Output) {
		observed = append(observed, output)
	})
	if err != nil {
		t.Fatalf("Run() error = %v, want an oversized line to be no error at all", err)
	}
	if result.Status != ProcessSucceeded || result.ExitCode != 0 {
		t.Fatalf("Run() result status = %q exit = %d, want the process judged on its own exit", result.Status, result.ExitCode)
	}
	// The long line, then the ordinary one after it: the stream keeps being read
	// past the cut rather than ending at it.
	if len(observed) != 2 {
		t.Fatalf("observed %d outputs, want the 2 the process produced", len(observed))
	}
	if !observed[0].LineTruncated {
		t.Fatal("the oversized line was not reported as truncated")
	}
	if !strings.Contains(observed[0].Text, "line truncated at") {
		t.Fatalf("oversized line ends %q, want the marker naming the cut", tail(observed[0].Text))
	}
	if !strings.HasPrefix(observed[0].Text, strings.Repeat("x", 1024)) {
		t.Fatal("the retained part of the oversized line is not a prefix of what the process wrote")
	}
	if observed[1].Text != "after the long line" || observed[1].LineTruncated {
		t.Fatalf("second output = %#v, want the untouched line that followed the cut one", observed[1])
	}
	if !strings.Contains(result.Stdout, "after the long line") {
		t.Fatal("the retained copy lost the line that followed the cut one")
	}
}

// A secret straddling the cut must not have its leading half kept: redaction
// replaces whole values, so a partial one survives it.
func TestOSProcessRunnerDoesNotCutThroughASecret(t *testing.T) {
	t.Parallel()

	secret := "sk-cut-straddling-credential"
	command := helperCommand("straddling-secret", secret)
	command.Timeout = 30 * time.Second
	result, err := (OSProcessRunner{}).Run(context.Background(), command, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	for length := 8; length <= len(secret); length++ {
		if strings.Contains(result.Stdout, secret[:length]) {
			t.Fatalf("the cut line retained %q, a leading part of the secret that redaction cannot replace", secret[:length])
		}
	}
}

func TestReadLineBoundsAndDrainsALongLine(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name    string
		stream  string
		bound   int
		want    []string
		dropped []int
	}{
		{
			name:    "lines under the bound are untouched",
			stream:  "alpha\nbeta\n",
			bound:   16,
			want:    []string{"alpha", "beta"},
			dropped: []int{0, 0},
		},
		{
			name:    "a blank line is still a line",
			stream:  "\nbeta\n",
			bound:   16,
			want:    []string{"", "beta"},
			dropped: []int{0, 0},
		},
		{
			name:    "a carriage return before the newline is not part of the line",
			stream:  "alpha\r\n",
			bound:   16,
			want:    []string{"alpha"},
			dropped: []int{0},
		},
		{
			name:    "an unterminated last line is reported",
			stream:  "alpha\nbeta",
			bound:   16,
			want:    []string{"alpha", "beta"},
			dropped: []int{0, 0},
		},
		{
			// The long line is cut and the one after it is read whole, which is
			// the whole claim: the tail was drained rather than left in the pipe.
			name:    "a long line is cut and the stream carries on",
			stream:  "aaaaaaaaaa\nbeta\n",
			bound:   4,
			want:    []string{"aaaa", "beta"},
			dropped: []int{6, 0},
		},
		{
			name:    "a long unterminated last line is cut",
			stream:  "aaaaaaaaaa",
			bound:   4,
			want:    []string{"aaaa"},
			dropped: []int{6},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			// Smaller than the shortest line above, so every case reads through
			// the buffer-full path a megabyte-long line reaches in production.
			reader := bufio.NewReaderSize(strings.NewReader(testCase.stream), 16)
			for index, want := range testCase.want {
				line, dropped, err := readLine(reader, testCase.bound)
				if err != nil && !errors.Is(err, io.EOF) {
					t.Fatalf("readLine() line %d error = %v", index, err)
				}
				if string(line) != want || dropped != testCase.dropped[index] {
					t.Fatalf("readLine() line %d = %q dropped %d, want %q dropped %d", index, line, dropped, want, testCase.dropped[index])
				}
			}
			line, dropped, err := readLine(reader, testCase.bound)
			if !errors.Is(err, io.EOF) || len(line) > 0 || dropped > 0 {
				t.Fatalf("readLine() after the last line = %q dropped %d err %v, want an empty end of stream", line, dropped, err)
			}
		})
	}
}

// tail is the end of a string, for a failure message that must not print a
// megabyte of it.
func tail(value string) string {
	if len(value) <= 80 {
		return value
	}
	return "…" + value[len(value)-80:]
}

func TestSensitiveEnvironmentValues(t *testing.T) {
	t.Parallel()

	got := SensitiveEnvironmentValues([]string{
		"PATH=/usr/bin",
		"ANTHROPIC_API_KEY=anthropic-secret",
		"GH_TOKEN=github-secret",
		"SERVICE_PASSWORD=password-secret",
		"DUPLICATE_TOKEN=github-secret",
		"EMPTY_SECRET=",
		"MALFORMED",
	})
	want := []string{"anthropic-secret", "github-secret", "password-secret"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("SensitiveEnvironmentValues() = %#v, want %#v", got, want)
	}
}

func TestRedactorReplacesOverlappingSecretsLongestFirst(t *testing.T) {
	t.Parallel()

	redactor := NewRedactor("token", "token-suffix")
	got := redactor.Redact("short=token long=token-suffix")
	if got != "short=[REDACTED] long=[REDACTED]" {
		t.Fatalf("Redact() = %q", got)
	}
}

func helperCommand(mode, secret string) Command {
	return Command{
		Name:     os.Args[0],
		Args:     []string{"-test.run=TestProcessHelper", "--", mode, secret},
		Env:      append(os.Environ(), "GO_WANT_PROCESS_HELPER=1"),
		Redactor: NewRedactor(secret),
	}
}

func TestProcessHelper(t *testing.T) {
	if os.Getenv("GO_WANT_PROCESS_HELPER") != "1" {
		return
	}
	args := os.Args
	separator := 0
	for index, arg := range args {
		if arg == "--" {
			separator = index
			break
		}
	}
	if separator == 0 || len(args) <= separator+1 {
		os.Exit(99)
	}
	mode := args[separator+1]
	secret := ""
	if len(args) > separator+2 {
		secret = args[separator+2]
	}
	switch mode {
	case "success":
		fmt.Printf("result %s\n", secret)
		fmt.Fprintln(os.Stderr, "warning")
		os.Exit(0)
	case "failure":
		fmt.Fprintln(os.Stderr, "failed")
		os.Exit(7)
	case "sleep":
		time.Sleep(5 * time.Second)
		os.Exit(0)
	case "chatter":
		// Long enough overall to outlive an idle bound, and never quiet for
		// anywhere near long enough to trip one. The end is a wall-clock deadline
		// rather than a line count so that a loaded machine makes this process
		// chattier, never longer.
		deadline := time.Now().Add(4 * time.Second)
		for line := 0; time.Now().Before(deadline); line++ {
			fmt.Printf("working %d\n", line)
			time.Sleep(20 * time.Millisecond)
		}
		os.Exit(0)
	case "counted-chatter":
		// Numbered so a retained copy can be checked for being a prefix rather
		// than only for being short.
		for line := 0; line < 40; line++ {
			fmt.Printf("line %d\n", line)
		}
		os.Exit(0)
	case "endless-chatter":
		for {
			fmt.Println("still working")
			time.Sleep(10 * time.Millisecond)
		}
	case "oversized-line":
		// One line past the per-line bound and then an ordinary one, so a reader
		// is asked both to cut the long line and to keep reading after it.
		fmt.Printf("%s\n", strings.Repeat("x", maxLineBytes+(1<<16)))
		fmt.Println("after the long line")
		os.Exit(0)
	case "straddling-secret":
		// The secret sits astride the cut, half of it inside the bound and half
		// outside, which is the only place a cut can leave a partial credential
		// that redaction has no whole value to replace.
		fmt.Printf("%s%s%s\n",
			strings.Repeat("x", maxLineBytes-len(secret)/2),
			secret,
			strings.Repeat("x", 1<<16))
		os.Exit(0)
	default:
		os.Exit(98)
	}
}
