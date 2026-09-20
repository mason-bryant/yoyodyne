package readmodel

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The running line names the preferred label beside each developer slot: which
// slot each run is in, and the free slots with what each prefers. It is read off
// the labels each run recorded at its claim, from the same derivation the
// scheduler fills the free slots from.
func TestTheRunningLineNamesThePreferredLabelBesideEachSlot(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Capacity = 3
	sources.Slots = []domain.DeveloperSlot{{Prefer: []string{"dashboard"}}, {}}
	sources.Runs = fakeRuns{
		incomplete: []runstate.State{
			{RunID: "run-a", WorkItemID: "yoyodyne-ifd.1", Status: runstate.StatusRunning, Phase: runstate.PhaseDeveloping, StartedAt: moment.Add(-2 * time.Hour)},
			{RunID: "run-b", WorkItemID: "yoyodyne-ifd.2", Status: runstate.StatusRunning, Phase: runstate.PhaseDeveloping, StartedAt: moment.Add(-time.Hour), WorkItemLabels: []string{"dashboard"}},
		},
		prices: map[string]runstate.ItemPrice{
			"yoyodyne-ifd.1": {Runs: []runstate.RunPrice{{RunID: "run-a", CostUSD: 1}}},
			"yoyodyne-ifd.2": {Runs: []runstate.RunPrice{{RunID: "run-b", CostUSD: 2}}},
		},
	}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.DeveloperSlots) != 3 {
		t.Fatalf("developer slots = %+v, want the three configured", standing.DeveloperSlots)
	}
	if standing.Running[1].Slot != 1 || standing.Running[0].Slot != 2 {
		t.Fatalf("running = %+v, want the dashboard run in slot 1 and the other in slot 2", standing.Running)
	}
	rendered := standing.Render()
	for _, want := range []string{
		"  yoyodyne-ifd.1 — developing, 2h00m elapsed, $1.00 so far, in developer slot 2\n",
		"  yoyodyne-ifd.2 — developing, 1h00m elapsed, $2.00 so far, in developer slot 1 (prefers the dashboard label)\n",
		"  developer slot 3 is free and prefers no label\n",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered:\n%s\nmissing: %q", rendered, want)
		}
	}
	// The brief rendering keeps the head and drops the slots with the entries.
	if brief := standing.RenderBrief(); strings.Contains(brief, "developer slot") {
		t.Fatalf("brief rendering names slots:\n%s", brief)
	}
}

func TestAFreePreferringSlotIsNamedUnderAnEmptyRunningLine(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Capacity = 2
	sources.Slots = []domain.DeveloperSlot{{}, {Prefer: []string{"reliability", "dashboard"}}}
	standing := ReadStanding(context.Background(), sources)
	rendered := standing.Render()
	want := "Running: nothing\n  developer slot 1 is free and prefers no label\n  developer slot 2 is free and prefers the reliability or dashboard labels\n"
	if !strings.HasPrefix(rendered, want) {
		t.Fatalf("rendered:\n%s\nwant it to open with:\n%s", rendered, want)
	}
}

// A project whose slots prefer nothing reads exactly as it did: no slot is
// carried and none is named.
func TestSlotsPreferringNothingAreNotNamed(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Capacity = 2
	sources.Slots = []domain.DeveloperSlot{{}, {}}
	standing := ReadStanding(context.Background(), sources)
	if standing.DeveloperSlots != nil {
		t.Fatalf("developer slots = %+v, want none carried where none prefers a label", standing.DeveloperSlots)
	}
	if rendered := standing.Render(); strings.Contains(rendered, "developer slot") {
		t.Fatalf("rendered names slots:\n%s", rendered)
	}
}

// Runs that could not be read leave the slots unsaid rather than reported free.
func TestSlotsAreNotReportedFreeOverRunsNobodyCouldRead(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Capacity = 1
	sources.Slots = []domain.DeveloperSlot{{Prefer: []string{"dashboard"}}}
	sources.Runs = fakeRuns{failIncomplete: context.DeadlineExceeded}
	standing := ReadStanding(context.Background(), sources)
	if standing.DeveloperSlots != nil {
		t.Fatalf("developer slots = %+v, want none over runs that could not be read", standing.DeveloperSlots)
	}
}
