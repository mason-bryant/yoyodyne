package orchestrator

import (
	"errors"
	"fmt"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The class a run is recorded as stopping at is decided from what stopped it and
// never from the evidence it happens to carry. The case this exists for is a run
// the provider killed while a check failure and a refused path were still on its
// record from an earlier round: every one of those looks like a discriminator,
// and reading the first of them as the reason files a provider death as a change
// that failed its checks.
func TestTheStopClassNamesWhatStoppedTheRunRatherThanWhatItCarries(t *testing.T) {
	t.Parallel()

	carrying := runstate.State{
		CheckFailure: &runstate.CheckFailure{Command: "make test", ExitCode: 1},
		PathRefusal:  &runstate.PathRefusal{Paths: []string{"docs/decisions/x.md"}},
	}
	died := stoppedBy(runstate.StopProvider, errors.New("the provider ended this run without judging the work"))

	for _, test := range []struct {
		name   string
		state  runstate.State
		cause  error
		status runstate.Status
		want   runstate.StopClass
	}{
		{name: "a provider death over leftover evidence", state: carrying, cause: died, status: runstate.StatusFailed, want: runstate.StopProvider},
		{name: "a class survives being wrapped again", state: carrying, cause: fmt.Errorf("run ended: %w", died), status: runstate.StatusFailed, want: runstate.StopProvider},
		{name: "a stop nothing classified", state: carrying, cause: errors.New("save running state: disk full"), status: runstate.StatusFailed, want: runstate.StopHarness},
		{name: "a cancellation over the gate it reached", state: carrying, cause: died, status: runstate.StatusCancelled, want: runstate.StopCancelled},
		{
			name:   "the environment over the gate it reached",
			state:  runstate.State{Environmental: &runstate.EnvironmentalRefusal{Cause: runstate.CauseSandboxSpawnFailure}},
			cause:  stoppedBy(runstate.StopChecks, errors.New("verification infrastructure failed")),
			status: runstate.StatusFailed,
			want:   runstate.StopEnvironmental,
		},
		{
			name:   "a settled environmental record belongs to an earlier round",
			state:  runstate.State{Environmental: &runstate.EnvironmentalRefusal{Cause: runstate.CauseSandboxSpawnFailure, Settled: true}},
			cause:  stoppedBy(runstate.StopReview, errors.New("independent review requires repair")),
			status: runstate.StatusFailed,
			want:   runstate.StopReview,
		},
	} {
		run := &activeRun{state: test.state}
		if got := run.classifyStop(test.cause, test.status); got != test.want {
			t.Errorf("%s: classifyStop() = %q, want %q", test.name, got, test.want)
		}
	}
	if stoppedBy(runstate.StopChecks, nil) != nil {
		t.Error("stoppedBy() made an error out of nothing")
	}
}
