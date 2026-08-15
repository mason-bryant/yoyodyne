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
	default:
		os.Exit(98)
	}
}
