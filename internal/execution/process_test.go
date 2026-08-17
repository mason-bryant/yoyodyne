package execution

import (
	"context"
	"fmt"
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

func TestOSProcessRunnerOutputLimit(t *testing.T) {
	t.Parallel()

	command := helperCommand("success", "")
	command.MaxOutputBytes = 2
	_, err := (OSProcessRunner{}).Run(context.Background(), command, nil)
	if err == nil || !strings.Contains(err.Error(), "output exceeded") {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestOSProcessRunnerStopsProcessWhenALineExceedsScannerLimit(t *testing.T) {
	t.Parallel()

	command := helperCommand("oversized-line", "")
	command.Timeout = 5 * time.Second
	started := time.Now()
	_, err := (OSProcessRunner{}).Run(context.Background(), command, nil)
	if err == nil || !strings.Contains(err.Error(), "token too long") {
		t.Fatalf("Run() oversized-line error = %v", err)
	}
	if elapsed := time.Since(started); elapsed >= 2*time.Second {
		t.Fatalf("Run() waited %s for timeout after scanner failure", elapsed)
	}
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
	case "endless-chatter":
		for {
			fmt.Println("still working")
			time.Sleep(10 * time.Millisecond)
		}
	case "oversized-line":
		fmt.Print(strings.Repeat("x", 2<<20))
		time.Sleep(5 * time.Second)
		os.Exit(0)
	default:
		os.Exit(98)
	}
}
