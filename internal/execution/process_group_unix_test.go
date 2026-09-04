//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package execution

import (
	"context"
	"testing"
	"time"
)

// A timeout terminates the whole process tree, so a descendant still holding the
// pipes does not keep the runner waiting past the budget.
//
// The deadline is seconds rather than milliseconds because it has to outlast two
// spawns and not one: the shell's, and the descendant the shell forks after it.
// A deadline that expires before that fork lands kills a process group the
// descendant has not joined yet, so the descendant survives and the test fails
// for a reason it was never asking about -- which is what a loaded machine made
// it do at fifty milliseconds. The descendant then sleeps far longer than the
// budget, and the bound below sits between the two, so a pass and the failure
// this guards against are seconds apart rather than milliseconds.
func TestOSProcessRunnerTimeoutTerminatesDescendantsHoldingPipes(t *testing.T) {
	t.Parallel()

	started := time.Now()
	result, err := (OSProcessRunner{}).Run(context.Background(), Command{
		Name:    "/bin/sh",
		Args:    []string{"-c", "sleep 30 & wait"},
		Timeout: 2 * time.Second,
	}, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Status != ProcessTimedOut {
		t.Fatalf("Run() status = %q, want %q", result.Status, ProcessTimedOut)
	}
	if elapsed := time.Since(started); elapsed > 15*time.Second {
		t.Fatalf("Run() returned after %s; descendant kept process pipes open", elapsed)
	}
}
