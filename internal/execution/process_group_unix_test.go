//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package execution

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOSProcessRunnerTimeoutTerminatesDescendantsHoldingPipes(t *testing.T) {
	t.Parallel()

	started := time.Now()
	result, err := (OSProcessRunner{}).Run(context.Background(), Command{
		Name:    "/bin/sh",
		Args:    []string{"-c", "sleep 5 & wait"},
		Timeout: 50 * time.Millisecond,
	}, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Status != ProcessTimedOut {
		t.Fatalf("Run() status = %q, want %q", result.Status, ProcessTimedOut)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("Run() returned after %s; descendant kept process pipes open", elapsed)
	}
}

// TestOSProcessRunnerReapsWhatASucceedingCommandLeftRunning is the shape that
// cost the operator a machine: a load test that backgrounds work, points its
// output away from this runner's pipes, and exits 0 before its own cleanup
// runs. Nothing cancels the context on that path, so nothing used to kill the
// group, and the work carried on for hours after the run that spawned it was
// over.
//
// Each descendant here carries its own bound — it sleeps and exits — which is
// the other half of the rule and is what keeps a regression on this test from
// leaving load on the machine that ran it.
func TestOSProcessRunnerReapsWhatASucceedingCommandLeftRunning(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	// The command does not exit until every descendant has said it is running,
	// so a pass here is a pass against work that was genuinely spawned rather
	// than against a loop that never forked anything.
	script := fmt.Sprintf(`for name in one two three; do
	( touch '%[1]s/started.'"$name"; sleep 1; touch '%[1]s/outlived.'"$name" ) >/dev/null 2>&1 &
done
until [ -f '%[1]s/started.one' ] && [ -f '%[1]s/started.two' ] && [ -f '%[1]s/started.three' ]; do :; done
`, directory)

	result, err := (OSProcessRunner{}).Run(context.Background(), Command{
		Name:    "/bin/sh",
		Args:    []string{"-c", script},
		Timeout: 30 * time.Second,
	}, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Status != ProcessSucceeded {
		t.Fatalf("Run() status = %q, want %q", result.Status, ProcessSucceeded)
	}
	for _, name := range []string{"one", "two", "three"} {
		if _, statErr := os.Stat(filepath.Join(directory, "started."+name)); statErr != nil {
			t.Fatalf("descendant %q never started, so this proves nothing about reaping: %v", name, statErr)
		}
	}

	// A descendant that survived the reap finishes its second past the command
	// and leaves its marker. Three times its own sleep is the margin against a
	// loaded machine; the failure is reported the moment a marker appears rather
	// than only at the end of it.
	deadline := time.Now().Add(3 * time.Second)
	for {
		for _, name := range []string{"one", "two", "three"} {
			if _, statErr := os.Stat(filepath.Join(directory, "outlived."+name)); statErr == nil {
				t.Fatalf("descendant %q outlived the command that spawned it", name)
			}
		}
		if time.Now().After(deadline) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}
