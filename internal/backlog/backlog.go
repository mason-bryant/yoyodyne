// Package backlog is the ordered work the product manager has admitted, and
// the one thing a development manager pulls from.
//
// The ordering is a product decision rather than a convenience: what is worth
// doing, and in what order, is the expression of product intent the product
// manager already owns. Decomposition, dependency structure, and assignment are
// not here, because those stay the development manager's. A role that disagrees
// with the order proposes a change to it, exactly as a downstream role proposes
// a change to a goal, and this package is deliberately read-only so that nothing
// which merely reads the backlog can quietly rewrite it.
//
// The order itself lives in Beads, as the priority the tracker already holds.
// Nothing here stores a second copy of it: a queue assembled from the tracker
// and a queue the tracker reports cannot then disagree.
package backlog

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"yoyodyne/internal/beads"
)

// The tracker statuses admitted work can be in. Open work is waiting to be
// pulled; blocked work has been admitted and is not finished, so it is still in
// the order even though nobody can pull it yet. Everything else has left the
// backlog: claimed work has been pulled already, and closed work is over,
// whether it was finished or retired.
const (
	statusOpen    = "open"
	statusBlocked = "blocked"
)

// blocksDependency is the Beads dependency type that makes one item wait for
// another. It is the same relation the pipeline refuses to run an item under, so
// what the backlog calls ready is what the harness will actually accept.
const blocksDependency = "blocks"

// maxRenderedEntries bounds how many entries a rendered backlog lists. What an
// operator needs from it is what happens next, not an export of the tracker, so
// the list is cut while the counts stay exact.
const maxRenderedEntries = 20

// maxRenderedTitleBytes keeps one tracker-supplied title to one line.
const maxRenderedTitleBytes = 120

// Entry is one admitted work item at the position the product manager's
// ordering puts it in.
type Entry struct {
	// Position is where this item sits in the order, counting from one. It is
	// the backlog's own numbering rather than anything the tracker stores, so it
	// describes this reading of the queue and no more.
	Position int    `json:"position"`
	ID       string `json:"id"`
	Title    string `json:"title"`
	Priority int    `json:"priority"`
	// Ready reports that nothing is holding this item back, which is what
	// separates the next item to pull from the next item in the order.
	Ready bool `json:"ready"`
	// WaitingOn names the unfinished work this item waits for, when the tracker
	// reported any. An entry can be unready without it: an item the tracker
	// records as blocked is held back by whatever produced that blocker, which
	// is written on the item rather than in a dependency.
	WaitingOn []string `json:"waiting_on,omitempty"`
}

// Queue is the backlog in the product manager's order.
type Queue struct {
	Entries []Entry `json:"entries,omitempty"`
}

// Order arranges the work items a tracker holds into the backlog. It keeps the
// admitted, unfinished ones — an item somebody is already working on has been
// pulled, and a closed one has left — and puts them in priority order, highest
// priority first.
//
// Items at the same priority keep the order the tracker listed them in, and
// that is deliberately not presented as a decision: the product manager says
// which of two items comes first by giving one a higher priority, and until it
// does, nothing here invents an order it did not choose.
func Order(items []beads.WorkItem) Queue {
	admitted := make([]beads.WorkItem, 0, len(items))
	for _, item := range items {
		if item.Status == statusOpen || item.Status == statusBlocked {
			admitted = append(admitted, item)
		}
	}
	Sort(admitted)

	queue := Queue{Entries: make([]Entry, 0, len(admitted))}
	for position, item := range admitted {
		waiting := waitingOn(item)
		queue.Entries = append(queue.Entries, Entry{
			Position:  position + 1,
			ID:        item.ID,
			Title:     item.Title,
			Priority:  item.Priority,
			Ready:     item.Status == statusOpen && len(waiting) == 0,
			WaitingOn: waiting,
		})
	}
	return queue
}

// Sort puts work items in the backlog's order in place, highest priority first,
// leaving items of equal priority in the order they arrived. It is exported
// because more than one place shows the same work — the queue an operator reads
// and the tracker state the product manager reasons over — and a listing that
// disagreed with the order would be a listing that misinforms whoever is setting
// it.
func Sort(items []beads.WorkItem) {
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].Priority < items[j].Priority
	})
}

// Next is the item to pull now: the first entry in the product manager's order
// that nothing is holding back. An unready entry is skipped rather than
// promoting the order past it, because the ordering says what matters most, not
// what happens to be startable.
func (q Queue) Next() (Entry, bool) {
	for _, entry := range q.Entries {
		if entry.Ready {
			return entry, true
		}
	}
	return Entry{}, false
}

// Ready counts the entries nothing is holding back.
func (q Queue) Ready() int {
	ready := 0
	for _, entry := range q.Entries {
		if entry.Ready {
			ready++
		}
	}
	return ready
}

// Render describes the backlog for an operator: the order the product manager
// set, what is holding each unready item back, and what would be pulled next. An
// empty backlog is stated rather than printed as nothing at all, because "there
// is no admitted work" is an answer and a blank space is not.
func (q Queue) Render() string {
	if len(q.Entries) == 0 {
		return "backlog: nothing is admitted.\n"
	}
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "backlog (%d admitted, %d ready to pull):\n", len(q.Entries), q.Ready())
	listed := q.Entries
	if len(listed) > maxRenderedEntries {
		listed = listed[:maxRenderedEntries]
	}
	for _, entry := range listed {
		fmt.Fprintf(&rendered, "  %d. [%s] p%d %s\n", entry.Position, entry.ID, entry.Priority,
			singleLine(entry.Title, maxRenderedTitleBytes))
		if entry.Ready {
			continue
		}
		if len(entry.WaitingOn) > 0 {
			fmt.Fprintf(&rendered, "     waiting on %s\n", strings.Join(entry.WaitingOn, ", "))
			continue
		}
		fmt.Fprintln(&rendered, "     blocked; the item says what by")
	}
	if len(q.Entries) > len(listed) {
		fmt.Fprintf(&rendered, "  %d further admitted item(s) are not listed here.\n", len(q.Entries)-len(listed))
	}
	if next, ok := q.Next(); ok {
		fmt.Fprintf(&rendered, "next to be pulled: %s\n", next.ID)
		return rendered.String()
	}
	rendered.WriteString("nothing is ready to be pulled; every admitted item is waiting on something.\n")
	return rendered.String()
}

// waitingOn names the unfinished work an item depends on. A dependency the
// tracker reports as closed is finished and holds nothing back; a listing that
// carries no dependencies at all leaves this empty, and the item's own status is
// then what says whether it is ready.
func waitingOn(item beads.WorkItem) []string {
	var waiting []string
	for _, dependency := range item.Dependencies {
		if dependency.Type == blocksDependency && dependency.Status != "closed" {
			waiting = append(waiting, dependency.ID)
		}
	}
	sort.Strings(waiting)
	return waiting
}

// singleLine folds a value into one bounded line, so tracker prose stays a list
// entry whatever it contains. It is cut on a rune boundary: a line truncated
// mid-rune is not text.
func singleLine(value string, limit int) string {
	folded := strings.Join(strings.Fields(value), " ")
	if len(folded) <= limit {
		return folded
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(folded[cut]) {
		cut--
	}
	return strings.TrimSpace(folded[:cut]) + "..."
}
