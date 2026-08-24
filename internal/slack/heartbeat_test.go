package slack

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The overnight this exists for: intake held at two minutes past midnight, a
// watch session that reached its budget and stopped, and ten hours in which the
// channel could not be told from a broken one. Both of those said themselves
// once, as they happened. What was missing is the hour after that.
func TestAHeldLineWithReadyWorkSaysSoAgainWhileItStands(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(3)
	harness.watched(t, runstate.WatchStopped, "the session spent the budget it was given", moment)
	harness.hold(t, "the harness held intake after runs kept blocking", moment)

	// The first sighting arms the channel's clock and says nothing there: the hold
	// said itself when it was placed, and repeating it a poll later would be the
	// sink saying what the channel already has. The push half beside it goes at
	// once, because nothing else is going to reach the operator at all.
	cursors := harness.poll(t, harness.start(),
		notify.KindWatchStopped, notify.KindIntakeHeld, notify.KindEscalationRaised)
	harness.now = harness.now.Add(59 * time.Minute)
	cursors = harness.poll(t, cursors)

	harness.now = harness.now.Add(2 * time.Minute)
	cursors = harness.poll(t, cursors, notify.KindLineWaiting, notify.KindEscalationRaised)
	// And again the hour after that, for as long as it stands.
	harness.now = harness.now.Add(time.Hour)
	harness.poll(t, cursors, notify.KindLineWaiting, notify.KindEscalationRaised)
}

// What it says is what somebody woken by it has to act on: which state, how long
// it has stood, and how much work is waiting behind it.
func TestTheHeartbeatNamesTheStateItsAgeAndWhatIsWaiting(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(4)
	harness.watched(t, runstate.WatchStopped, "the session spent the budget it was given", moment)
	held := harness.now
	harness.hold(t, "reordering the backlog first", held)

	cursors := harness.poll(t, harness.start(),
		notify.KindWatchStopped, notify.KindIntakeHeld, notify.KindEscalationRaised)
	harness.now = held.Add(10 * time.Hour)
	said := harness.say(t, cursors, notify.KindLineWaiting)
	for _, fact := range []string{"intake is held", "reordering the backlog first", "10 hours", "4 items"} {
		if !strings.Contains(said.Body, fact) {
			t.Fatalf("body %q does not carry %q", said.Body, fact)
		}
	}
}

// A line nobody is holding, with a session choosing work, is not waiting on
// anybody. The heartbeat stops the moment the state clears, and it says nothing
// about the clearing: what cleared it said so itself.
func TestTheHeartbeatStopsWhenTheStateClears(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(2)
	harness.watched(t, runstate.WatchWatching, "watching the backlog until stopped", moment)
	harness.hold(t, "looking at something first", moment)

	cursors := harness.poll(t, harness.start(),
		notify.KindWatchStarted, notify.KindIntakeHeld, notify.KindEscalationRaised)
	harness.now = harness.now.Add(time.Hour)
	cursors = harness.poll(t, cursors, notify.KindLineWaiting, notify.KindEscalationRaised)

	if _, _, err := harness.intake.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	cursors = harness.poll(t, cursors, notify.KindIntakeReleased)
	if standing := cursors.Streams[heartbeatStream].Standing; standing != "" {
		t.Fatalf("the heartbeat is still standing on %q after the state cleared", standing)
	}
	harness.now = harness.now.Add(4 * time.Hour)
	harness.poll(t, cursors)
}

// The other half of the rule, and the half that makes the channel readable: an
// idle line with nothing ready is not waiting on anybody, so it stays exactly as
// silent as it was.
func TestAnIdleLineWithNothingReadyStaysSilent(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(0)
	harness.watched(t, runstate.WatchIdle, "the backlog is empty", moment)

	cursors := harness.poll(t, harness.start(), notify.KindWatchIdle)
	for hour := 0; hour < 5; hour++ {
		harness.now = harness.now.Add(time.Hour)
		cursors = harness.poll(t, cursors)
	}
}

// A product nobody has ever watched has no line that stopped. An operator who
// runs items by name has a queue by choice, and an hourly message about it is
// the nagging this is written not to be.
func TestAProductNobodyHasWatchedIsNotAStalledLine(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(6)

	cursors := harness.poll(t, harness.start())
	harness.now = harness.now.Add(3 * time.Hour)
	harness.poll(t, cursors)
}

// Work visibly moving is not a stalled line whatever else is true, and a message
// saying nothing is being chosen while a run posts its way through a review is
// false in the way that teaches people to stop reading.
func TestALineWithARunInFlightIsNotWaiting(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(2)
	harness.watched(t, runstate.WatchStopped, "the session spent the budget it was given", moment)
	harness.record(t, harness.run(t, runstate.StatusRunning))

	cursors := harness.poll(t, harness.start(), notify.KindRunStarted, notify.KindWatchStopped)
	harness.now = harness.now.Add(2 * time.Hour)
	harness.poll(t, cursors)
}

// A tracker that will not answer leaves the sink unable to tell a line waiting on
// somebody from an honestly quiet one. It must not guess in either direction: it
// says so where it says everything else about itself, and asks again at the next
// interval rather than at the next poll.
func TestATrackerThatCannotBeReadIsSaidRatherThanGuessedAt(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	var said []string
	harness.feed.Log = func(format string, args ...any) { said = append(said, format) }
	harness.feed.Backlog = brokenBacklog{}
	harness.watched(t, runstate.WatchStopped, "the session spent the budget it was given", moment)

	cursors := harness.poll(t, harness.start(), notify.KindWatchStopped)
	harness.now = harness.now.Add(time.Hour)
	cursors = harness.poll(t, cursors)
	if len(said) != 1 {
		t.Fatalf("the sink said %v about a tracker it could not read, want it said once", said)
	}
	// The clock is reset rather than left where it was, so the next attempt is at
	// the next interval rather than fifteen seconds later.
	harness.poll(t, cursors)
	if len(said) != 1 {
		t.Fatalf("the sink said %v, want the retry held to the heartbeat interval", said)
	}
}

// A different state is a different thing to do something about, so it is armed
// afresh rather than inheriting a clock that has already run out — the sink says
// the state that stands now, once it has stood for an interval, rather than the
// moment it replaces one that had.
func TestANewStateIsArmedRatherThanInheritingTheLastOnesClock(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(1)
	harness.watched(t, runstate.WatchStopped, "the session spent the budget it was given", moment)

	cursors := harness.poll(t, harness.start(), notify.KindWatchStopped)
	harness.now = harness.now.Add(2 * time.Hour)
	cursors = harness.poll(t, cursors, notify.KindLineWaiting)

	// The operator holds intake, which is now what has stopped the line.
	harness.hold(t, "looking at the queue", harness.now)
	cursors = harness.poll(t, cursors, notify.KindIntakeHeld, notify.KindEscalationRaised)
	harness.now = harness.now.Add(30 * time.Minute)
	cursors = harness.poll(t, cursors)
	harness.now = harness.now.Add(31 * time.Minute)
	harness.poll(t, cursors, notify.KindLineWaiting, notify.KindEscalationRaised)
}

// One log holds every session a product has had, and nothing stops two running
// at once. A session ending while another carries on watching is the last line of
// the log saying "stopped" over a line that is still being pulled from, so the
// sessions are read one at a time rather than off the end of the log.
func TestASessionEndingBesideOneStillWatchingIsNotAStoppedLine(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(3)
	harness.watchedAs(t, "watch-0123456789abcdef0123456789abcdef", runstate.WatchWatching, "watching the backlog until stopped", moment)
	harness.watchedAs(t, "watch-fedcba9876543210fedcba9876543210", runstate.WatchWatching, "watching the backlog until stopped", moment.Add(time.Minute))
	harness.watchedAs(t, "watch-fedcba9876543210fedcba9876543210", runstate.WatchStopped, "the session spent the budget it was given", moment.Add(2*time.Minute))

	cursors := harness.poll(t, harness.start(),
		notify.KindWatchStarted, notify.KindWatchStarted, notify.KindWatchStopped)
	harness.now = harness.now.Add(3 * time.Hour)
	cursors = harness.poll(t, cursors)

	// Once the session that was still watching ends too, nobody is choosing.
	harness.watchedAs(t, "watch-0123456789abcdef0123456789abcdef", runstate.WatchStopped, "the scheduler was cancelled", harness.now)
	cursors = harness.poll(t, cursors, notify.KindWatchStopped)
	harness.now = harness.now.Add(time.Hour)
	harness.poll(t, cursors, notify.KindLineWaiting)
}

// ready gives the feed a tracker that reports this many items ready to pull, and
// the hourly cadence the sink ships with.
func (h *testHarness) ready(count int) {
	h.feed.Backlog = countedBacklog{count: count}
}

func (h *testHarness) hold(t *testing.T, reason string, at time.Time) {
	t.Helper()
	if _, err := h.intake.Hold(reason, at); err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
}

// say makes one pass, checks it said exactly what was expected, and returns the
// message as the channel would have it — which is where a test reads what a
// persona actually says rather than only which kind was selected.
func (h *testHarness) say(t *testing.T, cursors Cursors, want notify.Kind) notify.Message {
	t.Helper()
	batch, err := h.feed.Poll(context.Background(), cursors)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	for _, delivery := range batch.Deliveries {
		if delivery.Silent() {
			continue
		}
		if delivery.Notification.Event.Kind != want {
			t.Fatalf("said %q, want %q", delivery.Notification.Event.Kind, want)
		}
		message, err := notify.Render(delivery.Notification.Topic, delivery.Notification.Speaker, delivery.Notification.Event)
		if err != nil {
			t.Fatalf("a selected notification could not be said: %v", err)
		}
		return message
	}
	t.Fatalf("nothing was said, want %q", want)
	return notify.Message{}
}

type countedBacklog struct {
	count int
}

func (b countedBacklog) Ready(context.Context) (int, error) { return b.count, nil }

type brokenBacklog struct{}

func (brokenBacklog) Ready(context.Context) (int, error) {
	return 0, errors.New("bd: executable file not found in $PATH")
}
