package readmodel

// The run records are what say a re-scoped parent's change never landed, and
// the same derivation every surface reads holds is what hands that to the
// queue: a child building on the change waits for it, and is released by a
// later run promoting it, without anything written onto the child.

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func TestAChildBuildingOnAParentWhoseRunNeverLandedIsHeldUntilARunPromotesIt(t *testing.T) {
	t.Parallel()

	const parent, child = "yoyodyne-ifd.429.13", "yoyodyne-ifd.429.13.1"
	started := time.Date(2026, 9, 25, 9, 0, 0, 0, time.UTC)
	stopped := runstate.State{
		RunID:         "run-757",
		WorkItemID:    parent,
		Status:        runstate.StatusFailed,
		Branch:        "yoyodyne/yoyodyne-ifd-429-13/757",
		TargetBranch:  "main",
		HarnessCommit: "abcdef0123456789abcdef0123456789abcdef01",
		PullRequest:   &runstate.PullRequest{Number: 757},
		StartedAt:     started,
		UpdatedAt:     started,
	}
	items := []beads.WorkItem{
		{ID: parent, Title: "re-scoped", Status: "open", Priority: 1},
		{ID: child, Title: "carry the rest", Description: "Builds on the parent's change.", Status: "open", Priority: 1, Parent: parent},
	}

	held := heldForAPerson([]runstate.State{stopped}, nil, nothingDecided, asRecorded)
	entry := backlog.Order(items, []string{child}, held).Entries[1]
	if entry.ID != child || entry.Ready || !entry.AwaitingLanding {
		t.Fatalf("entry = %#v, want %s held for its parent's change", entry, child)
	}
	for _, want := range []string{"run run-757", "yoyodyne/yoyodyne-ifd-429-13/757", "at commit abcdef01", "pull request #757"} {
		if !strings.Contains(entry.Hold(), want) {
			t.Fatalf("the hold is missing %q: %s", want, entry.Hold())
		}
	}

	promoted := stopped
	promoted.RunID = "run-758"
	promoted.Status = runstate.StatusSucceeded
	promoted.Integration = &runstate.Integration{TargetBranch: "main"}
	promoted.StartedAt = started.Add(time.Hour)
	promoted.UpdatedAt = promoted.StartedAt
	held = heldForAPerson([]runstate.State{stopped, promoted}, nil, nothingDecided, asRecorded)
	if entry := backlog.Order(items, []string{child}, held).Entries[1]; !entry.Ready {
		t.Fatalf("the child is still held after its parent's change was promoted: %s", entry.Hold())
	}
}
