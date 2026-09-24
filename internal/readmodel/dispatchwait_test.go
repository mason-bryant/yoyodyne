package readmodel

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func dispatchNote(session, item string, attempt int, delay time.Duration, at time.Time) runstate.WatchTransition {
	return runstate.WatchTransition{
		SessionID: session,
		State:     runstate.WatchWatching,
		At:        at,
		DispatchWait: &runstate.DispatchWait{
			WorkItemID:   item,
			Boundary:     runstate.RetryDependencyRead,
			Attempt:      attempt,
			DelaySeconds: int64(delay / time.Second),
			At:           at,
			Failure:      "bd show failed with status timed_out",
		},
	}
}

// Only a dispatch's latest wait says anything about it, and it stands until the
// retry it precedes has had time to answer — not while an earlier wait's retry
// has already been made, not past the grace after its own end, and not once the
// session that started the dispatch has stopped.
func TestADispatchWaitStandsOnlyWhileItsLatestWaitHasNotLapsed(t *testing.T) {
	t.Parallel()

	idle := runstate.WatchTransition{SessionID: "watch-1", State: runstate.WatchIdle, At: moment.Add(-time.Hour)}
	first := dispatchNote("watch-1", "yoyodyne-a", 1, time.Second, moment.Add(-10*time.Minute))
	second := dispatchNote("watch-1", "yoyodyne-a", 5, 20*time.Minute, moment.Add(-5*time.Minute))
	other := dispatchNote("watch-1", "yoyodyne-b", 1, time.Second, moment.Add(-30*time.Second))
	sessions := []runstate.WatchTransition{idle, first, second, other}

	standing := WaitingOnTracker(sessions, moment)
	if len(standing) != 2 || standing[0].WorkItemID != "yoyodyne-a" || standing[0].Attempt != 5 || standing[1].WorkItemID != "yoyodyne-b" {
		t.Fatalf("WaitingOnTracker() = %#v, want each item's latest wait, oldest first", standing)
	}
	if !strings.Contains(standing[0].Says(), "retry 5") || !strings.Contains(standing[0].Says(), "timed_out") {
		t.Fatalf("Says() = %q, want the retry and the failure named", standing[0].Says())
	}

	// Past its own end and the grace for the retry it precedes, a wait is over.
	if lapsed := WaitingOnTracker(sessions, second.DispatchWait.Until().Add(dispatchWaitGrace)); len(lapsed) != 0 {
		t.Fatalf("WaitingOnTracker() after every wait lapsed = %#v, want none", lapsed)
	}
	// A session that stopped took its dispatches with it.
	stopped := append(append([]runstate.WatchTransition(nil), sessions...),
		runstate.WatchTransition{SessionID: "watch-1", State: runstate.WatchStopped, At: moment.Add(-time.Second)})
	if ended := WaitingOnTracker(stopped, moment); len(ended) != 0 {
		t.Fatalf("WaitingOnTracker() after the session stopped = %#v, want none", ended)
	}
	// And the notes are not where the session got to.
	if live := Live(sessions); len(live) != 1 || live[0].State != runstate.WatchIdle {
		t.Fatalf("Live() = %#v, want the session's own idle transition", live)
	}
}

// The running line says a dispatch waiting out the tracker in its head as well
// as under it, because the head is all the brief rendering a channel carries
// keeps.
func TestTheRunningLineSaysADispatchWaitingOutTheTracker(t *testing.T) {
	t.Parallel()

	wait := DispatchWait{DispatchWait: *dispatchNote("watch-1", "yoyodyne-a", 2, time.Minute, moment).DispatchWait, SessionID: "watch-1"}
	standing := Standing{Running: []RunningRun{}, Dispatching: []DispatchWait{wait}}
	rendered := standing.renderRunning()
	if !strings.HasPrefix(rendered, "Running: no run yet, and 1 dispatch waiting out a tracker failure before claiming anything:\n") {
		t.Fatalf("renderRunning() = %q, want the wait in the head of the line", rendered)
	}
	if !strings.Contains(rendered, "  the dispatch for yoyodyne-a is waiting out a tracker failure") {
		t.Fatalf("renderRunning() = %q, want the wait named under the line", rendered)
	}
	if head := brief(rendered); !strings.Contains(head, "1 dispatch waiting out a tracker failure") {
		t.Fatalf("brief() = %q, want the brief line to keep the wait", head)
	}

	standing.Running = []RunningRun{{RunID: "run-1", WorkItemID: "yoyodyne-b", Phase: runstate.PhaseDeveloping}}
	standing.Dispatching = []DispatchWait{wait, wait}
	if rendered := standing.renderRunning(); !strings.HasPrefix(rendered, "Running (1 developer run, and 2 dispatches waiting out a tracker failure before claiming anything):\n") {
		t.Fatalf("renderRunning() = %q, want the runs and the waits counted together", rendered)
	}
}
