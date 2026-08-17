package backlog

import (
	"strconv"
	"strings"
	"testing"

	"yoyodyne/internal/beads"
)

func TestOrderIsPriorityFirstAndHoldsWhatTheProductManagerDidNotDecide(t *testing.T) {
	t.Parallel()

	// The tracker's own order is deliberately not the backlog's: two items at the
	// same priority arrive in the order bd listed them and stay in it, because
	// nothing has decided between them.
	queue := Order([]beads.WorkItem{
		{ID: "yoyodyne-ifd.26", Title: "See and stop what is pulled", Status: statusOpen, Priority: 2},
		{ID: "yoyodyne-ifd.3", Title: "The scheduler that runs it", Status: statusOpen, Priority: 0},
		{ID: "yoyodyne-ifd.25", Title: "Approval moves up to the goals", Status: statusOpen, Priority: 2},
		{ID: "yoyodyne-ifd.4", Title: "The development manager that pulls", Status: statusOpen, Priority: 1},
	})

	var order []string
	for _, entry := range queue.Entries {
		order = append(order, entry.ID)
	}
	want := []string{"yoyodyne-ifd.3", "yoyodyne-ifd.4", "yoyodyne-ifd.26", "yoyodyne-ifd.25"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("order = %v, want %v", order, want)
	}
	// Positions are the backlog's own numbering, counting from one.
	for i, entry := range queue.Entries {
		if entry.Position != i+1 {
			t.Fatalf("entry %s is at position %d, want %d", entry.ID, entry.Position, i+1)
		}
	}
	next, ok := queue.Next()
	if !ok || next.ID != "yoyodyne-ifd.3" {
		t.Fatalf("Next() = %#v, %v", next, ok)
	}
}

func TestOnlyAdmittedUnfinishedWorkIsInTheBacklog(t *testing.T) {
	t.Parallel()

	// Work somebody is already doing has been pulled, and closed work has left
	// the backlog whether it was finished or retired. Neither is still queued.
	queue := Order([]beads.WorkItem{
		{ID: "yoyodyne-1", Title: "Being worked on", Status: "in_progress", Priority: 0},
		{ID: "yoyodyne-2", Title: "Finished", Status: "closed", Priority: 0},
		{ID: "yoyodyne-3", Title: "Admitted and waiting", Status: statusOpen, Priority: 3},
		{ID: "yoyodyne-4", Title: "Admitted and blocked", Status: statusBlocked, Priority: 1},
	})

	if len(queue.Entries) != 2 {
		t.Fatalf("entries = %#v", queue.Entries)
	}
	// A blocked item keeps its place in the order: it is admitted work that is
	// not finished, and hiding it would understate what is queued.
	if queue.Entries[0].ID != "yoyodyne-4" || queue.Entries[0].Ready {
		t.Fatalf("blocked entry = %#v", queue.Entries[0])
	}
	if !queue.Entries[1].Ready {
		t.Fatalf("open entry = %#v", queue.Entries[1])
	}
	if next, ok := queue.Next(); !ok || next.ID != "yoyodyne-3" {
		t.Fatalf("Next() = %#v, %v; the blocked item ahead of it is not pullable", next, ok)
	}
	if queue.Ready() != 1 {
		t.Fatalf("Ready() = %d, want 1", queue.Ready())
	}
}

func TestAnItemWaitingOnUnfinishedWorkKeepsItsPlaceButIsNotPulled(t *testing.T) {
	t.Parallel()

	queue := Order([]beads.WorkItem{
		{
			ID: "yoyodyne-ifd.4", Title: "The development manager that pulls",
			Status: statusOpen, Priority: 0,
			Dependencies: []beads.Dependency{
				{ID: "yoyodyne-ifd.18", Type: blocksDependency, Status: statusOpen},
				// A dependency that is finished holds nothing back.
				{ID: "yoyodyne-ifd.2", Type: blocksDependency, Status: "closed"},
				// Only the blocking relation makes an item wait; a parent does not.
				{ID: "yoyodyne-ifd.1", Type: "parent-child", Status: statusOpen},
			},
		},
		{ID: "yoyodyne-ifd.18", Title: "Give the product manager the backlog", Status: statusOpen, Priority: 1},
	})

	waiting := queue.Entries[0]
	if waiting.Ready || len(waiting.WaitingOn) != 1 || waiting.WaitingOn[0] != "yoyodyne-ifd.18" {
		t.Fatalf("waiting entry = %#v", waiting)
	}
	// The order is unchanged by readiness: what is pulled next skips the entry,
	// it does not move it.
	next, ok := queue.Next()
	if !ok || next.ID != "yoyodyne-ifd.18" || next.Position != 2 {
		t.Fatalf("Next() = %#v, %v", next, ok)
	}
}

func TestRenderSaysTheOrderWhatIsHeldBackAndWhatIsNext(t *testing.T) {
	t.Parallel()

	queue := Order([]beads.WorkItem{
		{ID: "yoyodyne-ifd.3", Title: "The scheduler that runs it", Status: statusOpen, Priority: 0,
			Dependencies: []beads.Dependency{{ID: "yoyodyne-ifd.4", Type: blocksDependency, Status: statusOpen}}},
		{ID: "yoyodyne-ifd.4", Title: "The development manager that pulls", Status: statusOpen, Priority: 1},
		{ID: "yoyodyne-ifd.9", Title: "A run that failed and was blocked", Status: statusBlocked, Priority: 2},
	})

	rendered := queue.Render()
	for _, required := range []string{
		"backlog (3 admitted, 1 ready to pull):",
		"1. [yoyodyne-ifd.3] p0 The scheduler that runs it",
		"waiting on yoyodyne-ifd.4",
		"2. [yoyodyne-ifd.4] p1 The development manager that pulls",
		"3. [yoyodyne-ifd.9] p2 A run that failed and was blocked",
		"blocked; the item says what by",
		"next to be pulled: yoyodyne-ifd.4",
	} {
		if !strings.Contains(rendered, required) {
			t.Fatalf("rendered backlog = %q, want it to contain %q", rendered, required)
		}
	}

	// An empty backlog says so. A blank space is not an answer.
	if empty := (Queue{}).Render(); !strings.Contains(empty, "nothing is admitted") {
		t.Fatalf("empty backlog = %q", empty)
	}

	// A backlog where everything waits says that rather than naming nothing at
	// all, because "nothing is ready" and "nothing is queued" call for opposite
	// responses from an operator.
	stalled := Order([]beads.WorkItem{{ID: "yoyodyne-1", Title: "Blocked", Status: statusBlocked, Priority: 0}})
	if !strings.Contains(stalled.Render(), "nothing is ready to be pulled") {
		t.Fatalf("stalled backlog = %q", stalled.Render())
	}
}

func TestARenderedBacklogIsCutWhileItsCountsStayExact(t *testing.T) {
	t.Parallel()

	items := make([]beads.WorkItem, 0, maxRenderedEntries+5)
	for i := 0; i < maxRenderedEntries+5; i++ {
		items = append(items, beads.WorkItem{
			ID:     "yoyodyne-" + strconv.Itoa(i),
			Title:  strings.Repeat("a very long title ", 40),
			Status: statusOpen,
		})
	}
	rendered := Order(items).Render()

	if !strings.Contains(rendered, "backlog ("+strconv.Itoa(len(items))+" admitted") {
		t.Fatalf("the count was cut with the list: %q", rendered)
	}
	if !strings.Contains(rendered, "5 further admitted item(s) are not listed here.") {
		t.Fatalf("the cut was not declared: %q", rendered)
	}
	for _, line := range strings.Split(rendered, "\n") {
		if len(line) > maxRenderedTitleBytes*2 {
			t.Fatalf("a tracker title escaped its line: %q", line)
		}
	}
}
