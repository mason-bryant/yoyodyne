package readmodel

// A dispatch waiting out a tracker failure before it has claimed anything, read
// once for every surface that says it.
//
// Since yoyodyne-ifd.428.6 the tracker read a dispatch makes before it claims an
// item or adopts a run is waited out on the recovery rule, for up to two hours.
// No run record exists yet at that point, so for the whole of the wait the slot
// it holds was on no running line, and the silence it made looked to the stall
// alarm exactly like a process that had hung. The session that started the
// dispatch now writes each wait onto the watch log as it is taken; this is the
// one reading of those entries the running line, the stall, and the alarm share.

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// dispatchWaitGrace is how long past its own end a wait still accounts for the
// slot it holds. The attempt it precedes is a tracker call bounded at thirty
// seconds, and a wait that stopped standing the moment it ended would leave that
// call — and the next wait's entry, written only once it fails — as a gap every
// surface reads as nothing happening.
const dispatchWaitGrace = time.Minute

// DispatchWait is one dispatch standing in a wait on the tracker, as the session
// that started it recorded.
type DispatchWait struct {
	runstate.DispatchWait
	// SessionID is the session whose dispatch this is.
	SessionID string `json:"session_id"`
}

// Says is the wait as the clause every surface states it in. It names the item,
// that nothing has been claimed yet, which retry the dispatch is waiting to make
// and when, and the failure it waited out, which is the whole of what tells it
// from a hung process.
func (w DispatchWait) Says() string {
	said := fmt.Sprintf("the dispatch for %s is waiting out a tracker failure before it claims anything: retry %d asks again at %s",
		w.WorkItemID, w.Attempt, w.Until().UTC().Format("15:04:05")+"Z")
	if failure := strings.TrimSpace(w.Failure); failure != "" {
		said += ", after " + singleLine(failure, maxRefusalBytes)
	}
	return said
}

// WaitingOnTracker is every dispatch standing in a wait on the tracker at the
// moment asked about, oldest first.
//
// A dispatch's latest wait is the only one that says anything about it; an
// earlier one is an attempt it has already made. The latest stands until it has
// ended and the attempt after it has had time to answer, and no longer: a
// dispatch whose process died mid-wait writes nothing further, and a record that
// went on accounting for its slot after the wait it names would silence the
// alarm for exactly the hang this exists to tell apart from a wait. A wait whose
// session has since stopped stands for nothing either, because the dispatch went
// with it.
func WaitingOnTracker(sessions []runstate.WatchTransition, now time.Time) []DispatchWait {
	latest := make(map[string]DispatchWait)
	stopped := make(map[string]time.Time)
	for _, transition := range sessions {
		if transition.State == runstate.WatchStopped && transition.At.After(stopped[transition.SessionID]) {
			stopped[transition.SessionID] = transition.At
		}
		if transition.DispatchWait == nil {
			continue
		}
		wait := DispatchWait{DispatchWait: *transition.DispatchWait, SessionID: transition.SessionID}
		if held, seen := latest[wait.WorkItemID]; seen && held.At.After(wait.At) {
			continue
		}
		latest[wait.WorkItemID] = wait
	}
	standing := make([]DispatchWait, 0, len(latest))
	for _, wait := range latest {
		if !now.Before(wait.Until().Add(dispatchWaitGrace)) {
			continue
		}
		if end, ended := stopped[wait.SessionID]; ended && !end.Before(wait.At) {
			continue
		}
		standing = append(standing, wait)
	}
	sort.SliceStable(standing, func(first, second int) bool {
		if !standing[first].At.Equal(standing[second].At) {
			return standing[first].At.Before(standing[second].At)
		}
		return standing[first].WorkItemID < standing[second].WorkItemID
	})
	return standing
}
