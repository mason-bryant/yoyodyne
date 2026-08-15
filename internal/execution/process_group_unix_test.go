//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package execution

import (
	"context"
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
