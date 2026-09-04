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

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// The tracker statuses admitted work can be in. Open work is waiting to be
// pulled; blocked work has been admitted and is not finished, so it is still in
// the order. Everything else has left the backlog: claimed work has been pulled
// already, and closed work is over, whether it was finished or retired.
//
// Neither of them decides whether an item can be pulled, and the blocked one
// deliberately does not. A status is a field somebody sets and nothing
// maintains: it is written when the work stops and never rewritten when what
// stopped it clears, so an item whose every blocker closed months ago still
// reads as blocked forever. On 2026-09-04 that was two-thirds of this backlog,
// two p0 items among it, and the line sat idle for a morning with nothing
// reporting why. What decides here is the dependencies themselves and the holds
// below, which are records of the thing rather than a description of it.
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

// Holds is the admitted work somebody has to release before anything pulls it,
// keyed by item, with what each one is waiting for. It is what separates a
// governance hold — a stoppage whose change is still preserved, an escalation
// nobody has answered — from a dependency block, which clears on its own as the
// work it names lands.
//
// It is a type rather than a bare map so that "nothing is held" and "what is
// held could not be read" are different values rather than one empty map. The
// zero value is the second, and it holds back every item whose status is
// blocked: a reader that cannot tell the two apart must not release either,
// because releasing a stoppage starts a fresh run over a change a branch is
// still holding, while holding a releasable item costs one pull.
type Holds struct {
	reasons map[string]string
	read    bool
}

// ReadHolds is the holds as a reader actually found them, which may be none at
// all. It is the constructor rather than a literal because an empty Holds and an
// unread one mean opposite things, and only a reader that got an answer may say
// the first.
func ReadHolds(reasons map[string]string) Holds {
	return Holds{reasons: reasons, read: true}
}

// unreadHold is what an entry says when nothing could read the holds. It names
// the gap rather than the item, because there is nothing wrong with the item:
// what is missing is the reading that would say whether anybody still has to
// release it. It is deliberately not one of the reasons carried on the entry,
// which are facts about an item somebody goes and acts on; this is a fact about
// the reading, and reporting it against each item would send somebody after a
// decision nobody owes.
const unreadHold = "blocked, and what is holding it could not be read, so nothing releases it here"

// Reason is what somebody has to release before one named item is pulled, and
// whether anything is holding it at all. It answers for what was read and no
// more: an unread Holds holds every blocked item, and that refusal belongs to
// the entry rather than here, because it is a fact about the reading rather than
// about the item.
func (h Holds) Reason(workItemID string) (string, bool) {
	reason := strings.TrimSpace(h.reasons[workItemID])
	return reason, reason != ""
}

// awaiting is what somebody has to do before this item is pulled, and is empty
// for an item nothing is holding.
func (h Holds) awaiting(item beads.WorkItem) string {
	return strings.TrimSpace(h.reasons[item.ID])
}

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
	Status   string `json:"status"`
	// Executor is what carries this item's execution where that is not a
	// developer run, and is empty for the ordinary work that is. It is on the
	// entry rather than left in the tracker because the queue is where anybody
	// decides what to pull: an item nobody can run has to say so where the order
	// is read, not only where the item is opened.
	Executor domain.WorkItemExecutor `json:"executor,omitempty"`
	// Parking is why this item is deliberately not to be pulled, and is empty for
	// the ordinary work that is. It is on the entry for the reason the executor
	// is, and against a failure that has already happened once: a parking that is
	// not in the queue is a parking the queue's readers do not share, and what
	// they do instead is pull the work.
	Parking domain.WorkItemParking `json:"parking,omitempty"`
	// Ready reports that nothing is holding this item back, which is what
	// separates the next item to pull from the next item in the order.
	Ready bool `json:"ready"`
	// Awaiting is what somebody has to release before this item is pulled, and is
	// empty for the ordinary work nobody is holding. It is on the entry for the
	// reason the parking is: a hold that is not in the queue is a hold the
	// queue's readers do not share, and a hold nobody can read off the item is
	// exactly the state a blocked status left this backlog in.
	Awaiting string `json:"awaiting,omitempty"`
	// WaitingOn names the unfinished work this item waits for. It explains an
	// unready entry; it never decides one. Both halves of that are forced by what
	// a listing actually carries: a listing that omitted dependencies would make
	// every item look unblocked, and the dependencies it does carry say what an
	// item depends on without saying whether that work is finished. So a
	// dependency is named here only when the depended-on item is itself still in
	// the backlog, and what decides readiness is the tracker's own account of what
	// can be pulled.
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
//
// ready names the items the tracker itself reports as pullable, and an open item
// missing from it is unready however clean its listing looks. For open work
// readiness is taken rather than inferred, because neither thing a listing
// offers can decide it. A listing that carries no dependencies at all is
// indistinguishable from work with none, so inferring "nothing listed, therefore
// nothing is in the way" would name a blocked item as the next thing to pull.
// And the dependencies a listing does carry say what an item depends on without
// saying whether that work is done, so inferring the opposite from their
// presence would hold an item back for a blocker that finished long ago. The
// tracker answers this from its own dependency graph; this only asks.
//
// Blocked work is the one thing the tracker cannot be asked, because it answers
// by the same status field nobody maintains: an item whose blockers all closed
// is still missing from that list, forever. So its readiness is computed here
// from what the item actually waits on — the blocking dependencies its listing
// records, judged the way every dependency gate in the harness judges them —
// with held naming what somebody still has to release. That is the whole of the
// difference: a dependency block clears itself as the work it names lands, and a
// governance hold is somebody's to lift and is never lifted here.
//
// What the tracker is not asked is what could carry the work, or whether the
// work is to be started at all. An item whose executor is a persona conversation
// is admitted work in the order like any other and is never the next thing to
// pull, because there is no run that could take it. An item somebody parked is
// admitted work in the order too, and is never the next thing to pull because
// somebody decided it is not to be started yet. The tracker answers neither: it
// knows about dependencies, and not about the harness's roles or the product
// manager's deferrals, so it reports both as ready forever.
func Order(items []beads.WorkItem, ready []string, held Holds) Queue {
	pullable := make(map[string]struct{}, len(ready))
	for _, id := range ready {
		pullable[id] = struct{}{}
	}
	admitted := make([]beads.WorkItem, 0, len(items))
	for _, item := range items {
		if item.Status == statusOpen || item.Status == statusBlocked {
			admitted = append(admitted, item)
		}
	}
	Sort(admitted)
	// Unfinished work is what is still in the backlog, which is what makes a
	// dependency worth naming: an item this one depends on that is no longer
	// queued has been done or pulled, and is not what anybody is waiting for.
	unfinished := make(map[string]struct{}, len(admitted))
	for _, item := range admitted {
		unfinished[item.ID] = struct{}{}
	}

	queue := Queue{Entries: make([]Entry, 0, len(admitted))}
	for position, item := range admitted {
		_, reportedReady := pullable[item.ID]
		awaiting := held.awaiting(item)
		waiting := waitingOn(item, unfinished)
		queue.Entries = append(queue.Entries, Entry{
			Position: position + 1,
			ID:       item.ID,
			Title:    item.Title,
			Priority: item.Priority,
			Status:   item.Status,
			Executor: item.Executor,
			Parking:  item.Parking,
			Awaiting: awaiting,
			// An item whose execution is not a developer run is never the next thing
			// to pull, however clear the dependency answer about it is, and neither is
			// one somebody parked or one somebody is holding. All three are a
			// different axis from the dependency reading rather than a second opinion
			// on it: what an item waits for says nothing about what could carry the
			// work, about whether the work is wanted now, or about whether somebody
			// has yet to decide what happens to it. Deciding all of them here is what
			// keeps the answer the same everywhere the order is read, instead of one
			// for each thing that reads it — which is exactly what parking-by-priority
			// was, and what it cost.
			Ready: startable(item, reportedReady, waiting, held) && awaiting == "" &&
				item.Executor.DeveloperRun() && !item.Parking.Parked(),
			WaitingOn: waiting,
		})
	}
	return queue
}

// startable reports nothing unfinished standing between this item and a run.
//
// The two statuses are asked differently because only one of them has an
// authority to defer to. Open work is what the tracker's own ready list
// describes, so that answer is taken. Blocked work is missing from that list by
// construction — the list is computed from the same status — so what is left is
// the item's own account of what it waits for, and an item waiting on nothing
// unfinished is startable whatever its status says.
//
// Blocked work is startable only where the holds were actually read, and that is
// the one place the reading itself decides something. Without it a dependency
// block and a stoppage somebody has to release are the same item to this, and
// releasing the second starts a run over a change that is still there.
func startable(item beads.WorkItem, reportedReady bool, waiting []string, held Holds) bool {
	if item.Status == statusBlocked {
		return held.read && len(waiting) == 0
	}
	return reportedReady
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

// Parked counts the entries somebody has deliberately taken out of reach. It is
// counted apart from the unready rest because it is the one kind of unready that
// is a decision rather than a wait: an operator reading that nothing is pullable
// needs to know how much of that is work somebody parked, and how much is work
// waiting on something that will clear on its own.
func (q Queue) Parked() int {
	parked := 0
	for _, entry := range q.Entries {
		if entry.Parking.Parked() {
			parked++
		}
	}
	return parked
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
	fmt.Fprintf(&rendered, "backlog (%d admitted, %d ready to pull", len(q.Entries), q.Ready())
	// Parked work is counted in the header rather than left to be inferred from
	// the entries, because the whole point of parking is that it is a decision
	// somebody can see they made. A backlog with none says nothing, which keeps
	// the line about the queue rather than about a state most queues are not in.
	if parked := q.Parked(); parked > 0 {
		fmt.Fprintf(&rendered, ", %d parked", parked)
	}
	rendered.WriteString("):\n")
	listed := q.Entries
	if len(listed) > maxRenderedEntries {
		listed = listed[:maxRenderedEntries]
	}
	for _, entry := range listed {
		// A parked entry is marked on its own line rather than only explained
		// underneath, so parking is visible as parking while the list is being
		// skimmed. Reading it off the priority is exactly what stopped working.
		parked := ""
		if entry.Parking.Parked() {
			parked = " parked"
		}
		fmt.Fprintf(&rendered, "  %d. [%s] p%d%s %s\n", entry.Position, entry.ID, entry.Priority, parked,
			singleLine(entry.Title, maxRenderedTitleBytes))
		if entry.Ready {
			continue
		}
		fmt.Fprintf(&rendered, "     %s\n", entry.Hold())
	}
	if len(q.Entries) > len(listed) {
		fmt.Fprintf(&rendered, "  %d further admitted item(s) are not listed here.\n", len(q.Entries)-len(listed))
	}
	if next, ok := q.Next(); ok {
		fmt.Fprintf(&rendered, "next to be pulled: %s\n", next.ID)
		return rendered.String()
	}
	rendered.WriteString("nothing is ready to be pulled; every admitted item is held back by something, and each entry above says by what.\n")
	return rendered.String()
}

// Hold says what is keeping an unready entry from being pulled. The five
// answers are different things to act on: an executor no run can be, a parking
// somebody decided, a hold somebody has to release, named work it waits for, and
// the tracker simply not offering it, which is what a dependency the listing did
// not carry looks like from here.
//
// The first three answer before the fourth because they are the ones that are
// not waiting for anything. Named work is an item that will be pulled once
// something clears; these three never will be until a person acts, and reading
// "waiting on" against any of them would send somebody looking for a blocker to
// release. Among the three, the executor answers first: an item that is both is
// one no run could take even after the parking is lifted, so naming the parking
// there would offer a release that changes nothing. The hold answers last of the
// three for the same reason in the other direction — it is the one somebody can
// actually act on today, so it must not shadow the two that would still refuse
// the item afterwards.
//
// It is exported because the refusal is the same fact wherever the queue is
// read, and the standing status names it per item: a second surface wording the
// same refusal differently is the disagreement one derivation exists to prevent.
func (e Entry) Hold() string {
	switch {
	case !e.Executor.DeveloperRun():
		return fmt.Sprintf("its executor is %q rather than a developer run, so no run carries it out; the item says which conversation does", e.Executor)
	case e.Parking.Parked():
		return "parked, so no pull selects it however far the queue drains: " + e.Parking.Reason()
	case e.Awaiting != "":
		return e.Awaiting
	case len(e.WaitingOn) > 0:
		return "waiting on " + strings.Join(e.WaitingOn, ", ")
	case e.Status == statusBlocked:
		// Blocked work with nothing waiting and no hold is pullable, so this is only
		// reached where nothing could read the holds. Saying the tracker did not
		// offer it would be true and useless: the tracker never offers blocked work,
		// and what actually held it is the reading that did not happen.
		return unreadHold
	default:
		return "the tracker does not report it as ready to pull"
	}
}

// waitingOn names the unfinished work an item depends on: the blocking
// dependencies its listing recorded whose own item is still in the backlog.
//
// The membership test is what keeps this honest where the listing says nothing.
// A dependency in a Beads listing may record only that the relation exists — it
// then reads the same after the blocker is closed as before — so a dependency
// alone says nothing about whether anybody is still waiting. What does say so is
// whether the depended-on item is still queued.
//
// A status the listing does carry is believed either way, and that is what makes
// this the same reading the run-start gate makes. Work the tracker reports as
// closed is nobody's wait; work it reports as anything else is a wait even when
// the item has left this backlog, which is exactly what a blocker somebody is
// running right now looks like from here. Believing only the closed half would
// call an item startable that the pipeline then refuses at the door.
func waitingOn(item beads.WorkItem, unfinished map[string]struct{}) []string {
	var waiting []string
	for _, dependency := range item.Dependencies {
		if dependency.Type != blocksDependency || dependency.Status == "closed" {
			continue
		}
		_, queued := unfinished[dependency.ID]
		if queued || dependency.Status != "" {
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
