package backlog

import (
	"strconv"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
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
	}, []string{"yoyodyne-ifd.26", "yoyodyne-ifd.3", "yoyodyne-ifd.25", "yoyodyne-ifd.4"})

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
		// A tracker that reports a claimed or closed item as ready cannot put it
		// back in the queue: what has left the backlog has left it.
	}, []string{"yoyodyne-1", "yoyodyne-2", "yoyodyne-3", "yoyodyne-4"})

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
				{ID: "yoyodyne-ifd.18", Type: blocksDependency},
				// A dependency whose item has left the backlog is finished work,
				// and a Beads listing says that nowhere on the dependency itself:
				// it reads exactly the same after the blocker is closed. What says
				// so is that nothing in the queue is that item any more.
				{ID: "yoyodyne-ifd.2", Type: blocksDependency},
				// Only the blocking relation makes an item wait; a parent does not.
				{ID: "yoyodyne-ifd.1", Type: "parent-child"},
			},
		},
		{ID: "yoyodyne-ifd.18", Title: "Give the product manager the backlog", Status: statusOpen, Priority: 1},
		// The tracker holds the first one back and offers the second, which is
		// what it does when a blocker is unfinished.
	}, []string{"yoyodyne-ifd.18"})

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

// A Beads listing records that a dependency exists and says nothing about
// whether it is finished: the entry reads identically after the blocker is
// closed. So a dependency whose item has left the backlog names nobody's wait,
// and an item is not held back for a blocker that is already done.
func TestADependencyOnFinishedWorkHoldsNothingBack(t *testing.T) {
	t.Parallel()

	queue := Order([]beads.WorkItem{{
		ID: "yoyodyne-ifd.4", Title: "The development manager that pulls",
		Status: statusOpen, Priority: 0,
		// The blocker is closed, so it is not in the backlog any more. The
		// dependency to it is still listed, exactly as Beads lists it.
		Dependencies: []beads.Dependency{{ID: "yoyodyne-ifd.18", Type: blocksDependency}},
	}}, []string{"yoyodyne-ifd.4"})

	entry := queue.Entries[0]
	if !entry.Ready || len(entry.WaitingOn) != 0 {
		t.Fatalf("an item whose blocker is finished was held back: %#v", entry)
	}
	if next, ok := queue.Next(); !ok || next.ID != "yoyodyne-ifd.4" {
		t.Fatalf("Next() = %#v, %v", next, ok)
	}
	if strings.Contains(queue.Render(), "waiting on") {
		t.Fatalf("a finished blocker was named as a wait: %q", queue.Render())
	}
}

// The failure this exists for, at the grain the queue is read at: an item whose
// execution is a conversation with a role, sitting at the top of the order with
// the tracker reporting it as perfectly pullable — which it is, for anything but
// a developer run. It keeps its place and is never what comes next, and the line
// that says so says it will not become pullable rather than reading like a wait.
func TestWorkAConversationCarriesIsNeverTheNextThingToPull(t *testing.T) {
	t.Parallel()

	queue := Order([]beads.WorkItem{
		{
			ID: "yoyodyne-ifd.138", Title: "Promote the brief", Status: statusOpen, Priority: 0,
			Executor: domain.WorkItemExecutorConversation,
		},
		{ID: "yoyodyne-ifd.144", Title: "Mark the conversation items", Status: statusOpen, Priority: 1},
	}, []string{"yoyodyne-ifd.138", "yoyodyne-ifd.144"})

	held := queue.Entries[0]
	if held.Ready {
		t.Fatalf("work no run can carry was reported ready: %#v", held)
	}
	// It is passed over rather than reordered: where the product manager put it is
	// still where it is.
	if held.Position != 1 || held.Executor != domain.WorkItemExecutorConversation {
		t.Fatalf("the entry moved or lost its marker: %#v", held)
	}
	if next, ok := queue.Next(); !ok || next.ID != "yoyodyne-ifd.144" {
		t.Fatalf("Next() = %#v, %v", next, ok)
	}
	if queue.Ready() != 1 {
		t.Fatalf("ready = %d, want the conversation item out of the count", queue.Ready())
	}
	rendered := queue.Render()
	if !strings.Contains(rendered, `its executor is "conversation" rather than a developer run`) {
		t.Fatalf("rendered backlog = %q", rendered)
	}
	// The three other holds are waits, and reading this one as one would send
	// somebody looking for a blocker to release.
	if strings.Contains(rendered, "waiting on") {
		t.Fatalf("an executor was reported as a wait: %q", rendered)
	}
}

// Readiness is the tracker's answer rather than one inferred from a listing.
// A listing that carries no dependencies looks exactly like work with none, so
// an item the tracker does not offer is held back whatever its listing shows —
// otherwise a blocked item would be named as the next thing to pull on any
// listing that leaves dependencies out.
func TestAnItemTheTrackerDoesNotOfferIsNotPulledHoweverCleanItsListingLooks(t *testing.T) {
	t.Parallel()

	// Neither item lists a dependency, which is what a listing without dependency
	// data looks like from here. The tracker offers only the second.
	queue := Order([]beads.WorkItem{
		{ID: "yoyodyne-ifd.3", Title: "Blocked, but the listing does not say so", Status: statusOpen, Priority: 0},
		{ID: "yoyodyne-ifd.4", Title: "Actually pullable", Status: statusOpen, Priority: 1},
	}, []string{"yoyodyne-ifd.4"})

	held := queue.Entries[0]
	if held.Ready {
		t.Fatalf("an item the tracker does not offer was reported ready: %#v", held)
	}
	next, ok := queue.Next()
	if !ok || next.ID != "yoyodyne-ifd.4" {
		t.Fatalf("Next() = %#v, %v", next, ok)
	}
	// It says the tracker is what is holding it rather than inventing a blocker
	// nobody reported.
	if !strings.Contains(queue.Render(), "the tracker does not report it as ready to pull") {
		t.Fatalf("rendered backlog = %q", queue.Render())
	}

	// The degenerate case is the one that matters most: a readiness answer that
	// arrives empty makes the whole queue unready rather than making all of it
	// pullable.
	none := Order([]beads.WorkItem{{ID: "yoyodyne-ifd.3", Title: "Admitted", Status: statusOpen}}, nil)
	if _, ok := none.Next(); ok || none.Ready() != 0 {
		t.Fatalf("an empty ready set produced %#v", none.Entries)
	}
}

func TestRenderSaysTheOrderWhatIsHeldBackAndWhatIsNext(t *testing.T) {
	t.Parallel()

	queue := Order([]beads.WorkItem{
		{ID: "yoyodyne-ifd.3", Title: "The scheduler that runs it", Status: statusOpen, Priority: 0,
			Dependencies: []beads.Dependency{{ID: "yoyodyne-ifd.4", Type: blocksDependency, Status: statusOpen}}},
		{ID: "yoyodyne-ifd.4", Title: "The development manager that pulls", Status: statusOpen, Priority: 1},
		{ID: "yoyodyne-ifd.9", Title: "A run that failed and was blocked", Status: statusBlocked, Priority: 2},
		// The tracker offers the one item nothing is holding: the first waits for
		// unfinished work and the third carries a blocker of its own.
	}, []string{"yoyodyne-ifd.4"})

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
	stalled := Order([]beads.WorkItem{{ID: "yoyodyne-1", Title: "Blocked", Status: statusBlocked, Priority: 0}}, nil)
	if !strings.Contains(stalled.Render(), "nothing is ready to be pulled") {
		t.Fatalf("stalled backlog = %q", stalled.Render())
	}
}

func TestARenderedBacklogIsCutWhileItsCountsStayExact(t *testing.T) {
	t.Parallel()

	items := make([]beads.WorkItem, 0, maxRenderedEntries+5)
	ready := make([]string, 0, maxRenderedEntries+5)
	for i := 0; i < maxRenderedEntries+5; i++ {
		id := "yoyodyne-" + strconv.Itoa(i)
		items = append(items, beads.WorkItem{
			ID:     id,
			Title:  strings.Repeat("a very long title ", 40),
			Status: statusOpen,
		})
		ready = append(ready, id)
	}
	rendered := Order(items, ready).Render()

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
