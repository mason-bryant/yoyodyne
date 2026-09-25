package triage

// Which of the docket's entries the development manager is shown, in what
// order, and how the next pass resumes past what this one showed.
//
// The docket is a log, and on 2026-09-25 it held 187 entries, 125 of them on
// work items already closed and several of them repeats of one stopped run. The
// window a conversation carries was filled newest first by whatever was on the
// log, so it listed eleven entries nothing could act on while twelve stopped runs
// waited behind them for between seven and thirty-six days. A bounded window is
// not optional — a docket of hundreds delivered whole is the turn — so what is
// decided here is what the bound is spent on.
//
// Three things decide it. Only live entries are in it: the work item is not
// closed, and nobody has decided about the entry, or the decision has lapsed or
// the harness was stopped carrying it out. Each stopped run is in it once, however
// many entries it left. And it is a walk rather than a listing, exactly as the
// report pile is: the oldest stoppage first, anything critical ahead of it, and a
// durable position that the next pass resumes past, so an entry one pass could not
// show is the first thing the next pass shows.

import (
	"sort"
	"strings"
	"time"
)

// WindowPosition is how far through the live docket, oldest stoppage first, the
// window has been carried. It is a point in that order rather than a count, for
// the reason the report pile's position is: the docket changes between passes, an
// index would name a different entry every time, and a moment plus the key that
// breaks its ties names the same one for as long as it is live.
type WindowPosition struct {
	Since time.Time `json:"since,omitempty"`
	Key   string    `json:"key,omitempty"`
}

// Started reports whether a position names anywhere at all. The zero position is
// the oldest end of the docket, which is where a window that has shown nothing
// stands.
func (p WindowPosition) Started() bool {
	return p.Key != "" || !p.Since.IsZero()
}

// passed reports whether a window at this position has already been carried past
// a stoppage, in the order Live puts the docket in.
func (p WindowPosition) passed(stoppage Stoppage) bool {
	if !p.Started() {
		return false
	}
	switch {
	case stoppage.Since.Before(p.Since):
		return true
	case stoppage.Since.After(p.Since):
		return false
	default:
		return stoppage.Entry.Key <= p.Key
	}
}

// Stoppage is one live stoppage as the window carries it: the entry that speaks
// for it, when the work first stopped, and how many further entries about the
// same run were folded into it.
type Stoppage struct {
	Entry Entry
	// Since is the earliest moment any entry about this run was docketed, which is
	// how long the stoppage has waited for somebody. It is what the window orders
	// by rather than the entry's own moment, because the entry kept may be a later
	// one about the same run and the wait began at the first.
	Since  time.Time
	Folded int
}

// At is the position a window is left at by being carried past one stoppage.
func (s Stoppage) At() WindowPosition {
	return WindowPosition{Since: s.Since, Key: s.Entry.Key}
}

// LiveDocket is the docket as the window reads it: the live stoppages, oldest
// first, and how many entries were left out of them and why. The counts are
// kept rather than dropped because a window that silently shows a subset is one
// a reader takes for the whole.
type LiveDocket struct {
	Stoppages []Stoppage
	// Dead counts entries on a work item the tracker holds as closed. Nothing a
	// development manager decides about one changes anything, because the work it
	// stopped is finished or withdrawn.
	Dead int
	// Settled counts entries a decision still standing has settled. The docket's
	// own build already leaves these out; they are counted here as well so a
	// caller handing the window the whole log is shown the same thing.
	Settled int
	// Folded counts entries that were a second word about a run already listed.
	Folded int
}

// Live reads the docket for the window. closed answers whether the tracker holds
// a work item as closed; nil is a tracker that could not be asked, and then no
// entry is taken for dead, because hiding a stoppage on the strength of a
// listing nobody read is the one direction this must not fail in. An item the
// answer does not know is live for the same reason.
func Live(entries []Entry, closed func(workItemID string) bool, now time.Time) LiveDocket {
	var docket LiveDocket
	index := map[string]int{}
	for _, entry := range entries {
		if !entry.Undecided(now) {
			docket.Settled++
			continue
		}
		if closed != nil && closed(strings.TrimSpace(entry.WorkItemID)) {
			docket.Dead++
			continue
		}
		subject := entry.stoppage()
		at, seen := index[subject]
		if !seen {
			index[subject] = len(docket.Stoppages)
			docket.Stoppages = append(docket.Stoppages, Stoppage{Entry: entry, Since: entry.RecordedAt})
			continue
		}
		docket.Folded++
		held := &docket.Stoppages[at]
		held.Folded++
		if entry.RecordedAt.Before(held.Since) {
			held.Since = entry.RecordedAt
		}
		if speaksFor(entry, held.Entry) {
			held.Entry = entry
		}
	}
	sort.SliceStable(docket.Stoppages, func(i, j int) bool {
		left, right := docket.Stoppages[i], docket.Stoppages[j]
		if !left.Since.Equal(right.Since) {
			return left.Since.Before(right.Since)
		}
		return left.Entry.Key < right.Entry.Key
	})
	return docket
}

// stoppage names what one entry is about for the purpose of folding repeats: the
// run where there is one, since a run that stopped is one thing to decide however
// many entries it left, and the entry's own key where there is none.
func (e Entry) stoppage() string {
	if run := strings.TrimSpace(e.RunID); run != "" {
		return "run:" + run
	}
	return "key:" + e.Key
}

// speaksFor reports whether a candidate should stand for its run in place of the
// entry already standing. A critical one does, because it is what the reader
// most needs to see; otherwise the later one does, because it is what the run
// stands at now.
func speaksFor(candidate, standing Entry) bool {
	if candidate.Critical() != standing.Critical() {
		return candidate.Critical()
	}
	return candidate.RecordedAt.After(standing.RecordedAt)
}

// Undecided reports an entry that is still a question at a moment: nobody has
// decided about it, the decision has lapsed, or the harness tried to carry the
// decision out and a gate stopped it.
func (e Entry) Undecided(at time.Time) bool {
	return e.Closed == nil || !e.Closed.Holds(at) || e.CarryOutStopped()
}

// CarryOutStopped reports a settled entry whose decision the harness has tried to
// carry out since it was decided, and been stopped. The finding has to be about
// this decision — made after it — because a finding about an earlier decision on
// the same stoppage is one this decision has since superseded.
func (e Entry) CarryOutStopped() bool {
	return e.Closed != nil && e.CarryOut != nil && e.CarryOut.RefusedAt.After(e.Closed.ClosedAt)
}

// Critical reports an entry that must not wait its turn in the walk: a role
// having said the item cannot be met as it stands, which parks the item until the
// development manager decides, and a decision of hers the harness was stopped
// carrying out by a gate that will not clear on its own.
func (e Entry) Critical() bool {
	if e.Class == ClassEscalation {
		return true
	}
	return e.CarryOutStopped() && !e.CarryOut.Waiting
}

// Window is what one pass is offered of the live docket, in the two parts a
// caller has to keep apart. Urgent jumps the walk and Next is the walk: the
// position advances over what was shown from Next and never over what jumped,
// because a critical at the far end of the docket would otherwise carry the
// position past everything between.
type Window struct {
	Urgent []Stoppage
	Next   []Stoppage
}

// Walk is what a window at one position is offered. Next begins past the
// position and carries on from the oldest end once it reaches the newest, so the
// walk is a cycle through everything live: nothing is buried behind the
// position, and a pass is never offered less than the docket holds because the
// last pass happened to end near the newest end.
func Walk(stoppages []Stoppage, position WindowPosition) Window {
	var window Window
	var behind []Stoppage
	for _, one := range stoppages {
		switch {
		case one.Entry.Critical():
			window.Urgent = append(window.Urgent, one)
		case position.passed(one):
			behind = append(behind, one)
		default:
			window.Next = append(window.Next, one)
		}
	}
	window.Next = append(window.Next, behind...)
	return window
}
