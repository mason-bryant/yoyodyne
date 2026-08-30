package execution

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// A process the operating system never began running is told apart from one that
// ran and failed, and by a sentinel rather than by the words of a message.
//
// What the two say about the work is opposite. A process that ran and failed
// answered the question it was asked; one that never started was never asked, so
// nothing it would have produced is evidence about the work — it is evidence
// about the machine. A caller deciding what a round cost has to be able to tell
// them apart without matching on prose.
func TestARunnerNamesAProcessTheMachineNeverStarted(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "no-such-binary")
	_, err := (OSProcessRunner{}).Run(context.Background(), Command{Name: missing}, nil)
	if !errors.Is(err, ErrProcessNotStarted) {
		t.Fatalf("Run() error = %v, want it to name a process that was never started", err)
	}
	// What the operating system said is kept beside the sentinel rather than
	// replaced by it: the sentinel says which class of failure this is, and what
	// is wrapped in it is what says why.
	if !strings.Contains(err.Error(), missing) {
		t.Fatalf("Run() error = %v, want the reason the machine gave carried with it", err)
	}
}
