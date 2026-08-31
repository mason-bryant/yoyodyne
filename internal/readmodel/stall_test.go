package readmodel

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// held is the whole log as one condition, for the cases that have it in hand.
func held(sessions ...runstate.WatchTransition) func() ([]runstate.WatchTransition, error) {
	return func() ([]runstate.WatchTransition, error) { return sessions, nil }
}

// Every reason says whose move it is. This is what makes an unnamed reason
// impossible rather than unlikely: a reason added to the set without an answer
// here would be a state the harness can report and nobody can act on, which is
// the whole failure the taxonomy exists to end.
func TestEveryReasonSaysWhoseMoveItIs(t *testing.T) {
	t.Parallel()
	for _, reason := range Reasons() {
		if strings.TrimSpace(reason.Whose()) == "" {
			t.Fatalf("reason %q says whose move it is: %q", reason, reason.Whose())
		}
	}
	// And nothing outside the set has an answer, so a reason invented at a call
	// site cannot borrow one.
	if Reason("something-nobody-named").Whose() != "" {
		t.Fatalf("a reason outside the taxonomy was given a whose-move")
	}
}

// The order is the order an operator acts in, and each state is named as itself.
// A hold placed over a full machine is still the hold: it is what a person would
// do something about, and the slots are a fact about a harness that is working.
func TestTheStallIsNamedInTheOrderAnOperatorActsIn(t *testing.T) {
	t.Parallel()
	stopped := runstate.WatchTransition{SessionID: "watch-1", State: runstate.WatchStopped, At: moment.Add(-time.Hour)}
	idle := runstate.WatchTransition{SessionID: "watch-1", State: runstate.WatchIdle, At: moment.Add(-time.Hour)}
	watching := runstate.WatchTransition{SessionID: "watch-1", State: runstate.WatchWatching, At: moment.Add(-time.Hour)}

	for _, testCase := range []struct {
		name       string
		conditions Conditions
		want       Reason
	}{
		{
			name: "the operator's hold outranks everything",
			conditions: Conditions{
				OperatorHold: runstate.OperatorHold{HeldAt: moment.Add(-time.Hour)}, OperatorHeld: true,
				IntakeHold: runstate.IntakeHold{HeldAt: moment}, IntakeHeld: true,
				Running: 2, Capacity: 2, Sessions: held(stopped),
			},
			want: ReasonOperatorHold,
		},
		{
			name: "then the intake hold",
			conditions: Conditions{
				IntakeHold: runstate.IntakeHold{HeldAt: moment}, IntakeHeld: true,
				Running: 2, Capacity: 2, Sessions: held(stopped),
			},
			want: ReasonIntakeHold,
		},
		{
			name:       "then a machine with every slot taken",
			conditions: Conditions{Running: 2, Capacity: 2, Sessions: held(stopped)},
			want:       ReasonNoCapacity,
		},
		{
			name:       "a live session choosing nothing is idle, not absent",
			conditions: Conditions{Sessions: held(watching, idle)},
			want:       ReasonSessionIdle,
		},
		{
			name:       "a log whose every session ended is nobody choosing",
			conditions: Conditions{Sessions: held(watching, stopped)},
			want:       ReasonNoWatchSession,
		},
		{
			name:       "a product nobody has ever watched is its own state",
			conditions: Conditions{Sessions: held()},
			want:       ReasonUnwatched,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			stall := WhyNothingStarts(testCase.conditions)
			if stall.Reason != testCase.want {
				t.Fatalf("reason = %q (%q), want %q", stall.Reason, stall.Says, testCase.want)
			}
			if strings.TrimSpace(stall.Says) == "" {
				t.Fatalf("reason %q said nothing", stall.Reason)
			}
		})
	}
}

// A session watching settles it. The harness would start the next pullable item,
// which is what makes a startable item's absence from the not-startable line
// mean something.
func TestASessionChoosingWorkIsNoStall(t *testing.T) {
	t.Parallel()
	stall := WhyNothingStarts(Conditions{Sessions: held(
		runstate.WatchTransition{SessionID: "watch-1", State: runstate.WatchIdle, At: moment.Add(-2 * time.Hour)},
		runstate.WatchTransition{SessionID: "watch-2", State: runstate.WatchWatching, At: moment.Add(-time.Hour)},
	)})
	if stall.Stopped() {
		t.Fatalf("stall = %+v, want nothing stopping the choosing", stall)
	}
	if stall.Refusal() != "" || stall.Mark() != "" {
		t.Fatalf("a stall that is not stopping anything said something: %q / %q", stall.Refusal(), stall.Mark())
	}
}

// An idle session is not an absent one. Telling an operator to start a watch
// session that is already running and sitting idle is the disagreement between
// two derivations of this that made it one, and it is the answer that sends
// somebody to the wrong place.
func TestAnIdleSessionIsNotToldToStartOne(t *testing.T) {
	t.Parallel()
	stall := WhyNothingStarts(Conditions{Sessions: held(
		runstate.WatchTransition{SessionID: "watch-1", State: runstate.WatchIdle, At: moment.Add(-time.Hour)},
	)})
	if stall.Reason != ReasonSessionIdle {
		t.Fatalf("reason = %q, want the idle session named as itself", stall.Reason)
	}
	if strings.Contains(stall.Refusal(), "yoyo work --watch") {
		t.Fatalf("an idle session was told to start a session: %q", stall.Refusal())
	}
}

// A watch log that cannot be read is not a stall. A reason invented over a
// record nobody could open is the confident emptiness every answer here is
// written to avoid, so what is reported is that the question could not be asked.
func TestAnUnreadableWatchLogInventsNoReason(t *testing.T) {
	t.Parallel()
	unreadable := WhyNothingStarts(Conditions{
		Sessions: func() ([]runstate.WatchTransition, error) { return nil, errors.New("the state directory is gone") },
	})
	if unreadable.Stopped() {
		t.Fatalf("stall = %+v, want no reason at all", unreadable)
	}
	if !strings.Contains(unreadable.Problem, "the state directory is gone") {
		t.Fatalf("problem = %q", unreadable.Problem)
	}
	unwired := WhyNothingStarts(Conditions{})
	if unwired.Stopped() || !strings.Contains(unwired.Problem, "nothing was wired") {
		t.Fatalf("stall = %+v, want the missing source said", unwired)
	}
}

// The watch log is read only where the question reaches it. The switches and the
// machine's own capacity are answered from records the caller already holds, and
// a surface that would spend a read to answer this spends it only when it has to.
func TestTheWatchLogIsReadOnlyWhenItIsNeeded(t *testing.T) {
	t.Parallel()
	reads := 0
	conditions := Conditions{
		IntakeHold: runstate.IntakeHold{HeldAt: moment}, IntakeHeld: true,
		Sessions: func() ([]runstate.WatchTransition, error) {
			reads++
			return nil, nil
		},
	}
	if stall := WhyNothingStarts(conditions); stall.Reason != ReasonIntakeHold {
		t.Fatalf("reason = %q", stall.Reason)
	}
	if reads != 0 {
		t.Fatalf("the watch log was read %d times behind a held switch", reads)
	}
	conditions.IntakeHeld = false
	if stall := WhyNothingStarts(conditions); stall.Reason != ReasonUnwatched {
		t.Fatalf("reason = %q", stall.Reason)
	}
	if reads != 1 {
		t.Fatalf("the watch log was read %d times, want once", reads)
	}
}

// A different state re-arms the clock a surface repeats itself on, and the same
// state standing keeps it. The mark is what carries that across a restart, so it
// names the reason and when it began and nothing else.
func TestTheMarkNamesTheStateAndItsAge(t *testing.T) {
	t.Parallel()
	idle := WhyNothingStarts(Conditions{Sessions: held(
		runstate.WatchTransition{SessionID: "watch-1", State: runstate.WatchIdle, At: moment.Add(-time.Hour)},
	)})
	same := WhyNothingStarts(Conditions{Sessions: held(
		runstate.WatchTransition{SessionID: "watch-1", State: runstate.WatchIdle, At: moment.Add(-time.Hour)},
	)})
	later := WhyNothingStarts(Conditions{Sessions: held(
		runstate.WatchTransition{SessionID: "watch-1", State: runstate.WatchIdle, At: moment},
	)})
	if idle.Mark() != same.Mark() {
		t.Fatalf("one standing state marked two ways: %q and %q", idle.Mark(), same.Mark())
	}
	if idle.Mark() == later.Mark() {
		t.Fatalf("a state that began at a different time kept the last one's mark: %q", later.Mark())
	}
	if !strings.HasPrefix(idle.Mark(), string(ReasonSessionIdle)+":") {
		t.Fatalf("mark = %q, want it to name the reason", idle.Mark())
	}
}

// A stall over an empty queue is a state of the machine rather than something
// waiting on a person. The attention line is what an operator reads to find what
// will wait forever without them, and it is worth nothing if a quiet machine
// fills it.
func TestAStallHoldingNothingBackIsNotAttention(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Sessions = fakeSessions{transitions: []runstate.WatchTransition{
		{SessionID: "watch-1", State: runstate.WatchStopped, At: moment.Add(-time.Hour)},
	}}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.NeedsHuman) != 0 {
		t.Fatalf("needs a human = %+v, want nothing waiting on anybody", standing.NeedsHuman)
	}
	// The same stall over admitted work is waiting on somebody, and says who.
	sources.Tracker = statusTracker{fakeTracker{
		byStatus: map[string][]beads.WorkItem{"open": {{ID: "item-1", Status: "open"}}},
		ready:    []beads.WorkItem{{ID: "item-1"}},
	}}
	standing = ReadStanding(context.Background(), sources)
	if len(standing.NeedsHuman) != 1 || standing.NeedsHuman[0].Whose != ReasonNoWatchSession.Whose() {
		t.Fatalf("needs a human = %+v", standing.NeedsHuman)
	}
}

// What the stall could not read is said even when the queue is empty. It is a
// gap in the reading rather than a fact about the work, and a line that reported
// a quiet backlog while it could not tell whether anything was choosing from it
// would be the confident emptiness this whole format is written against.
func TestAnEmptyQueueDoesNotSwallowAnUnreadableStall(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.Sessions = fakeSessions{fail: errors.New("the state directory is gone")}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.NotStartable) != 0 {
		t.Fatalf("not startable = %+v, want nothing over an empty queue", standing.NotStartable)
	}
	if !strings.Contains(standing.NotStartableProblem, "the state directory is gone") {
		t.Fatalf("not-startable problem = %q", standing.NotStartableProblem)
	}
}

// A held switch is on the attention line once. It is there in its own right,
// because a hold waits on the operator whether or not it is currently what stops
// the choosing, and a stall that repeated it would be one state said twice in a
// reading whose whole value is that it can be trusted.
func TestAHeldSwitchIsSaidOnceOnTheAttentionLine(t *testing.T) {
	t.Parallel()
	sources := quietSources()
	sources.IntakeHolds = fakeIntakeHolds{
		hold: runstate.IntakeHold{HeldAt: moment.Add(-time.Hour), Reason: "the overnight looked wrong"},
		held: true,
	}
	sources.Tracker = statusTracker{fakeTracker{
		byStatus: map[string][]beads.WorkItem{"open": {{ID: "item-1", Status: "open"}}},
		ready:    []beads.WorkItem{{ID: "item-1"}},
	}}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.NeedsHuman) != 1 {
		t.Fatalf("needs a human = %+v, want the hold said once", standing.NeedsHuman)
	}
}
