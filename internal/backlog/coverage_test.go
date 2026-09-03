package backlog

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// Parentage stated as an edge is the shape this project's own tracker hands
// over, and a derivation that read only the parent field saw a backlog with
// nothing decomposed in it. Both shapes are read, and the coverage names the
// children in the order the queue put them in.
func TestCoverNamesTheUnfinishedChildrenInTheQueuesOrder(t *testing.T) {
	t.Parallel()

	epic := beads.WorkItem{ID: "yoyodyne-ifd.121", Title: "A readable README", Status: statusOpen, Priority: 0}
	edged := beads.WorkItem{
		ID: "yoyodyne-ifd.121.2", Title: "Split it", Status: statusOpen, Priority: 1,
		Dependencies: []beads.Dependency{
			{IssueID: "yoyodyne-ifd.121.2", ID: epic.ID, Type: "parent-child"},
		},
	}
	fielded := beads.WorkItem{
		ID: "yoyodyne-ifd.121.3", Title: "Write the index", Status: statusBlocked, Priority: 2, Parent: epic.ID,
	}
	admitted := []beads.WorkItem{fielded, epic, edged}
	queue := Order(admitted, []string{epic.ID, edged.ID})

	coverage := Cover(queue, admitted, nil)
	covering := coverage.Covering(epic.ID)
	if strings.Join(covering, ",") != strings.Join([]string{edged.ID, fielded.ID}, ",") {
		t.Fatalf("covering = %v, want both children in the product manager's order", covering)
	}
	// A child covers its parent and never itself, and an item nobody broke down
	// is covered by nothing at all.
	if named := coverage.Covering(edged.ID); len(named) != 0 {
		t.Fatalf("covering the child = %v, want nothing", named)
	}
}

// What covers an item is an unfinished child wherever it sits, and a claimed
// child is exactly the one a reading of the queue alone would miss: it has left
// the backlog, and it is a run in flight over the very same change.
func TestCoverReadsTheClaimedChildTheQueueCannotSee(t *testing.T) {
	t.Parallel()

	epic := beads.WorkItem{ID: "yoyodyne-epic", Title: "Rewrite it", Status: statusOpen, Priority: 0}
	claimed := beads.WorkItem{
		ID: "yoyodyne-epic.2", Title: "Rewrite it", Status: StatusClaimed, Priority: 1, Parent: epic.ID,
	}
	admitted := []beads.WorkItem{epic}
	queue := Order(admitted, []string{epic.ID})

	if named := Cover(queue, admitted, nil).Covering(epic.ID); len(named) != 0 {
		t.Fatalf("covering from the queue alone = %v, want the claimed child to be invisible to it", named)
	}
	covering := Cover(queue, admitted, []beads.WorkItem{claimed}).Covering(epic.ID)
	if len(covering) != 1 || covering[0] != claimed.ID {
		t.Fatalf("covering = %v, want the claimed child", covering)
	}
}

// Coverage is re-derived from the tracker at every reading rather than
// remembered, so an item stops being covered when its last unfinished child
// leaves. Holding a container back forever would strand real work behind a
// decomposition that finished.
func TestCoverLeavesAContainerWhoseChildrenHaveClosedUncovered(t *testing.T) {
	t.Parallel()

	epic := beads.WorkItem{ID: "yoyodyne-epic", Title: "Rewrite it", Status: statusOpen, Priority: 0}
	closed := beads.WorkItem{ID: "yoyodyne-epic.2", Title: "Rewrite it", Status: "closed", Priority: 1, Parent: epic.ID}
	admitted := []beads.WorkItem{epic, closed}
	queue := Order(admitted, []string{epic.ID})

	if named := Cover(queue, admitted, nil).Covering(epic.ID); len(named) != 0 {
		t.Fatalf("covering = %v, want a closed child to cover nothing", named)
	}
}

// The refusal names where the execution went, and bounds the naming without
// losing the count: an epic decomposed into twenty children must not put twenty
// identifiers into a report whose whole job is to be read.
func TestCoveredReasonNamesTheChildrenAndCountsTheRest(t *testing.T) {
	t.Parallel()

	one := CoveredReason([]string{"yoyodyne-epic.2"})
	if !strings.Contains(one, "1 unfinished child item(s): yoyodyne-epic.2") {
		t.Fatalf("reason = %q, want the one child named", one)
	}
	if strings.Contains(one, "further") {
		t.Fatalf("reason = %q, want nothing counted beyond the child it named", one)
	}

	many := CoveredReason([]string{"c1", "c2", "c3", "c4", "c5"})
	if !strings.Contains(many, "5 unfinished child item(s): c1, c2, c3, and 2 further") {
		t.Fatalf("reason = %q, want three named and the count exact", many)
	}
	if strings.Contains(many, "c4") {
		t.Fatalf("reason = %q, want the naming bounded", many)
	}
}
