package runstate

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// The record of a stale blocked status the claim cleared is read back as it was
// written, on the run and on the summary every surface projects, and it is
// refused where it says something the harness does not name: an outcome that is
// not one of the three, or a confirmed clear nothing read.
func TestAStaleBlockClearIsRecordedAndRefusedOnItsContract(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusFailed)
	state.Failure = "claim work item: the clear of the stale blocked status on yoyodyne-test was never confirmed"
	state.StaleBlockClear = &StaleBlockClear{Outcome: domain.StaleBlockClearUnconfirmed, Reads: 5, Status: "blocked"}
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	loaded, err := store.Load(state.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.StaleBlockClear == nil || *loaded.StaleBlockClear != *state.StaleBlockClear {
		t.Fatalf("loaded clear = %#v, want %#v", loaded.StaleBlockClear, state.StaleBlockClear)
	}
	history, err := store.History(RunQuery{})
	if err != nil {
		t.Fatalf("History() error = %v", err)
	}
	if len(history.Runs) != 1 || history.Runs[0].StaleBlockClear == nil || *history.Runs[0].StaleBlockClear != *state.StaleBlockClear {
		t.Fatalf("summary = %#v, want the clear projected", history.Runs)
	}

	for _, refused := range []StaleBlockClear{
		{Outcome: "landed", Reads: 1, Status: "open"},
		{Outcome: domain.StaleBlockClearConfirmed, Reads: 0, Status: "open"},
		{Outcome: domain.StaleBlockClearConfirmedLate, Reads: -1, Status: "open"},
	} {
		state.StaleBlockClear = &refused
		if err := state.Validate(); err == nil || !strings.Contains(err.Error(), "stale_block_clear") {
			t.Fatalf("Validate() with %#v = %v, want the clear refused", refused, err)
		}
	}
}

// Each of the three endings is said in its own words, so a surface printing the
// record can tell a clear that landed late from one that never landed without
// deriving anything.
func TestAStaleBlockClearDescribesItsEnding(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		clear StaleBlockClear
		want  string
	}{
		{StaleBlockClear{Outcome: domain.StaleBlockClearConfirmed, Reads: 1, Status: "open"}, "on the first read"},
		{StaleBlockClear{Outcome: domain.StaleBlockClearConfirmedLate, Reads: 3, Status: "open"}, "on read 3"},
		{StaleBlockClear{Outcome: domain.StaleBlockClearUnconfirmed, Reads: 5, Status: "blocked"}, `5 read(s) returned status "blocked"`},
	} {
		if got := tc.clear.Describe(); !strings.Contains(got, tc.want) {
			t.Fatalf("Describe(%#v) = %q, want it to say %q", tc.clear, got, tc.want)
		}
	}
	if got := (StaleBlockClear{Outcome: domain.StaleBlockClearUnconfirmed, Reads: 5, Status: "blocked"}).Describe(); !strings.Contains(got, "left for the next pull") || strings.Contains(got, "cleared") {
		t.Fatalf("Describe() = %q, want an unconfirmed clear said as one and never as cleared", got)
	}
}
