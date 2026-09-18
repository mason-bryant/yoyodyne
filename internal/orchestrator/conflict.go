package orchestrator

// What the scheduler will not start beside what, and how it says so.
//
// The scheduler enforces almost nothing, and this is not an exception to that.
// Integration is already serialized by the promotion lease, a target branch that
// moved is already handled by the replay, and a replay that will not apply
// already stops its run with both sides preserved. Every one of those still
// holds whether or not anything here runs.
//
// What they cost is the point. Two runs started at once over the same files are
// one promotion plus, on the loser, a replay, a fresh set of checks, and an
// entirely fresh review — or a stopped run and an item waiting for a person. The
// siblings of one epic are the ordinary way to arrive there: work broken out of
// one piece is work over one part of the repository, and the queue offers all of
// it at once. So the scheduler declines to buy that cost when it can see it
// coming, and sequences the two rather than racing them.
//
// It is choosing rather than enforcing, which is why it is here and not
// downstream. And it is only choosing: nothing here holds an item back past the
// pull it was held at. The conflicts are re-read at every pull from what is
// actually in flight, so an item held at one poll is pulled at the next one
// where the run it would have raced has ended.

import (
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/surface"
)

// maxSequencedItemsNamed bounds how many held-back items one recorded reason
// names before it falls back to counting them. The count stays exact either way,
// for the reason every listing in this package is bounded: a reason nobody
// finishes reading accounts for nothing.
const maxSequencedItemsNamed = 3

// holder is one run in flight as the guard names it: the item it is over, and
// the run itself where the pull can know it. A run this session started has no
// identifier until it reserves, which is several steps after it is started, so
// for that one pull it is named as the session's own rather than by a run.
//
// The run is named because a reader is going to check it. Until
// yoyodyne-ifd.379 the report named the item alone, and an item cannot be checked
// against `yoyo status`, which lists runs: on 2026-09-18 a report that said
// yoyodyne-ifd.272 was in flight, two days after its last run had failed, was
// read as the guard holding a developer slot on a dead run.
type holder struct {
	item string
	run  string
}

// String is how the reason names the run.
func (h holder) String() string {
	if h.run == "" {
		return "the run this session started for " + h.item
	}
	return h.run + " (" + h.item + ")"
}

// conflict is one reason an item was not started now: the work already in
// flight it would race, and what the two of them share.
type conflict struct {
	// With is the run already going, and the item it is over.
	With holder
	// Over is what the two share, in words, because what an operator does about
	// a shared epic and about a shared file are different things.
	Over string
}

// inFlight is what the runs already going have taken, as one pull sees it: the
// epics they were broken out of, and the surfaces they will change.
//
// It is built per pull and grows as that pull starts things. An item started
// three entries ago is in flight as surely as one another process is running,
// and the durable run state does not know about it yet — a run does not appear
// there until it reserves, which is several steps after it is started.
//
// What it is built from is the runs in flight now and nothing else, in the one
// sense runstate.Status.InFlight gives that: pending or running, whatever phase.
// A run that failed — at integration, on a replay conflict, with its branch and
// pull request preserved for a person — is a record, and a record holds no
// epic. See occupiedItems, which is where that reading is made.
type inFlight struct {
	// epics maps an epic identifier to the in-flight run working under it. Both
	// an item's parent and the item itself are keys: two children of one epic
	// race each other, and a child races the epic it was broken out of.
	//
	// The parent read here is the one the tracker states as a field, and
	// deliberately not the wider reading beads.WorkItem.DecomposedFrom does. A
	// tracker that hangs its whole backlog off one root epic states that the
	// wider way too, and holding every item back behind whichever child of the
	// root is already running is serializing the queue rather than declining one
	// race — which the header above says is exactly what this is not. Widening it
	// wants a container epic told from a decomposed one first, and that question
	// is not answered here.
	epics map[string]holder
	// taken is the surfaces each in-flight run holds, in the order the runs
	// were taken, so which conflict is reported for a candidate is stable rather
	// than an artifact of map ordering.
	taken []takenSurfaces
}

type takenSurfaces struct {
	by    holder
	paths []string
}

func newInFlight() *inFlight {
	return &inFlight{epics: map[string]holder{}}
}

// take records a run over an item as work in flight, so nothing that would race
// it is started beside it. The run is the identifier the durable state gave it,
// and empty for a run this pull started itself, which has none yet. An item the
// tracker no longer lists arrives here empty and takes nothing: a run over work
// that has left the queue cannot be compared with anything, and guessing at what
// it touches would hold real work back on no evidence.
func (f *inFlight) take(item beads.WorkItem, run string) {
	id := strings.TrimSpace(item.ID)
	if id == "" {
		return
	}
	by := holder{item: id, run: strings.TrimSpace(run)}
	f.claim(id, by)
	if parent := strings.TrimSpace(item.Parent); parent != "" {
		f.claim(parent, by)
	}
	if paths := surface.Of(item); len(paths) > 0 {
		f.taken = append(f.taken, takenSurfaces{by: by, paths: paths})
	}
}

// claim records one epic identifier against the first in-flight run to hold it.
// The first rather than the last, so a candidate held back at one pull is told
// about the same run at the next one for as long as that run lasts.
func (f *inFlight) claim(epic string, by holder) {
	if _, held := f.epics[epic]; !held {
		f.epics[epic] = by
	}
}

// against reports the in-flight work an item would race, and over what. Most
// items race nothing, which is the answer that keeps unrelated work running
// concurrently: this is a reason to sequence two items, not a reason to
// serialize the queue.
func (f *inFlight) against(item beads.WorkItem) (conflict, bool) {
	id := strings.TrimSpace(item.ID)
	if parent := strings.TrimSpace(item.Parent); parent != "" {
		if by, held := f.epics[parent]; held && by.item != id {
			return conflict{With: by, Over: "the epic " + parent + " both were broken out of"}, true
		}
	}
	// The other direction: a run over the epic this item belongs to. Nothing
	// else catches it — the coverage check reads an item's own children, which
	// says nothing about a parent somebody else is already running.
	if by, held := f.epics[id]; held && by.item != id {
		return conflict{With: by, Over: "the epic " + id + " that run was broken out of"}, true
	}
	mine := surface.Of(item)
	if len(mine) == 0 {
		return conflict{}, false
	}
	for _, taken := range f.taken {
		if taken.by.item == id {
			continue
		}
		if shared, races := surface.Shared(mine, taken.paths); races {
			return conflict{With: taken.by, Over: "the surface " + shared}, true
		}
	}
	return conflict{}, false
}

// reason is what the schedule says about an item held back for this conflict. It
// says what would have been bought as well as what was avoided, because an
// operator reading that the scheduler passed over ready work needs the trade
// rather than the rule.
//
// It is dated to the pull rather than said in the present tense, because the
// schedule that carries it is rendered when the session ends, which can be days
// after the pull that last held the item. A line that said a run "is already in
// flight" was read, on 2026-09-18, as the guard's current reading rather than as
// a session's record of one.
func (c conflict) reason() string {
	return fmt.Sprintf(
		"it would race %s, which was in flight over %s at the last pull that held it back. Sequencing them costs a wait; racing them costs the loser a replay, a fresh set of checks, and a fresh review. It is pulled once that run ends",
		c.With, c.Over)
}

// waited is the same fact in the past tense, for the recorded reason of the
// selection that eventually pulls the item.
func (c conflict) waited() string {
	return fmt.Sprintf("%s was in flight over %s", c.With, c.Over)
}

// sequencing is how conflict-avoidance shaped one selection. Both halves are
// ordinarily empty, and a selection records nothing about either then: most
// items race nothing, and a reason that said so on every run would be a sentence
// nobody reads on the runs where it matters.
type sequencing struct {
	// after is the hold this item itself came out of, where this pass held it
	// back earlier and the run it would have raced has since ended.
	after conflict
	// ahead names the items earlier in the product manager's order that this
	// pull held back for a conflict, which is why this item was pulled before
	// them. The order is chosen rather than departed from: the queue is still
	// read top to bottom, and what moved is only what was startable now.
	ahead []string
}

// reason is what the selection records about having been sequenced. It is
// appended to the ordinary account of why the item was chosen rather than
// replacing it: where the item sat in the order is still the first thing that
// picked it, and this is what happened to that order on the way.
func (s sequencing) reason() string {
	var said strings.Builder
	if s.after.With.item != "" {
		fmt.Fprintf(&said, " It was held back earlier in this session because %s, and was pulled once that cleared.", s.after.waited())
	}
	if len(s.ahead) > 0 {
		named := s.ahead
		if len(named) > maxSequencedItemsNamed {
			named = named[:maxSequencedItemsNamed]
		}
		fmt.Fprintf(&said, " %d item(s) ahead of it in the order (%s", len(s.ahead), strings.Join(named, ", "))
		if further := len(s.ahead) - len(named); further > 0 {
			fmt.Fprintf(&said, ", and %d further", further)
		}
		said.WriteString(") were held back at this pull to keep them from racing work already in flight, so this was pulled in their place.")
	}
	return said.String()
}
