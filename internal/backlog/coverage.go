package backlog

import (
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

// StatusClaimed is the tracker slice that has left the backlog by being pulled.
// Nothing is ever chosen from it, and coverage is read one status wider than the
// order for one reason: a child a run is already carrying is the strongest cover
// there is over its parent's execution, and it is exactly the child a reading of
// the queue alone cannot see.
//
// It is named here rather than beside each reader because two readers that
// disagreed about which slice that is would disagree about which epics are
// covered, which is the whole of what this file exists to stop.
const StatusClaimed = "in_progress"

// maxCoveringChildrenNamed bounds how many children a refusal names before it
// falls back to counting them. The count stays exact either way.
const maxCoveringChildrenNamed = 3

// Coverage is which admitted items have had their execution broken out, and the
// unfinished children that carry it, in the product manager's order.
//
// It lives beside the queue rather than inside the scheduler because it decides
// whether an item can be started at all, and that answer has to be the same
// wherever it is asked. It was not: the scheduling pass passed a covered epic
// over, while the standing status went on reporting the same epic as pullable
// and merely stalled — one question with two answers, which sends an operator to
// investigate a stall that is not one.
type Coverage map[string][]string

// Cover assembles the coverage over one reading of the tracker. admitted is the
// work the queue was assembled from, in whatever order the tracker listed it;
// the order a refusal names children in comes from the queue. claimed is the
// work that has left the queue by being pulled, which covers for the reason
// StatusClaimed states.
//
// Parentage is taken from whichever way the tracker states it rather than from
// the parent field alone, which beads.WorkItem.DecomposedFrom is where the cost
// of getting that wrong is written down.
func Cover(queue Queue, admitted, claimed []beads.WorkItem) Coverage {
	byID := make(map[string]beads.WorkItem, len(admitted))
	for _, item := range admitted {
		byID[item.ID] = item
	}
	coverage := make(Coverage)
	cover := func(item beads.WorkItem) {
		if parent := item.DecomposedFrom(); parent != "" {
			coverage[parent] = append(coverage[parent], item.ID)
		}
	}
	// The queue first, so what a refusal names reads in the product manager's
	// order rather than in whatever order the tracker listed.
	for _, entry := range queue.Entries {
		cover(byID[entry.ID])
	}
	for _, item := range claimed {
		cover(item)
	}
	return coverage
}

// Covering names the unfinished children carrying one item's execution, and
// nothing at all for an item nobody broke down. It is re-derived from the
// tracker at every reading rather than remembered, so an item stops being
// covered when its last unfinished child closes.
func (c Coverage) Covering(id string) []string {
	return c[id]
}

// CoveredReason says that the work has already been broken out, and names what
// broke it out. The children are what makes it actionable: whoever reads that an
// item was passed over needs to see where its execution went, and a container
// left behind after its last child closes is read from the same line.
//
// The naming is bounded rather than exhaustive, for the reason every listing
// here is: an epic decomposed into twenty children would otherwise put twenty
// identifiers into a report whose whole job is to be read.
func CoveredReason(children []string) string {
	named := children
	if len(named) > maxCoveringChildrenNamed {
		named = named[:maxCoveringChildrenNamed]
	}
	reason := fmt.Sprintf("its execution is covered by %d unfinished child item(s): %s",
		len(children), strings.Join(named, ", "))
	if further := len(children) - len(named); further > 0 {
		reason += fmt.Sprintf(", and %d further", further)
	}
	return reason + ". The children are the work; pulling this beside them would make the same change twice"
}
