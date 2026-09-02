package slack

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The window this whole thing exists for, replayed: on 2026-09-01 the watch
// session died on a transient tracker read at 06:05, its last word was that it
// was watching, and for seven and a half hours nothing started while the tracker
// went on reporting work ready. No hold, no stop, no idle poll, no run — nothing
// any surface here reads. It was found by a person noticing.
func TestTheDeadWindowIsNoticedRecordedAndSaidOnce(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	stalls := harness.watchesForStalls(t)
	harness.ready(3)
	harness.watched(t, runstate.WatchWatching, "watching the backlog until stopped", moment)

	// Inside the threshold this is a gap between runs, and it says nothing.
	harness.now = moment.Add(20 * time.Minute)
	cursors := harness.poll(t, harness.start(), notify.KindWatchStarted)
	if _, open, err := stalls.Standing(); err != nil || open {
		t.Fatalf("Standing() = %v %v, want nothing recorded inside the threshold", open, err)
	}

	// Past it, it is a stall: recorded durably, and taken to the operators.
	harness.now = moment.Add(45 * time.Minute)
	delivery := harness.stalled(t, cursors)
	if !delivery.Direct {
		t.Fatal("the stall was posted to the channel alone, want the operators told directly")
	}
	if delivery.Notification.Event.Severity != report.SeverityWarning {
		t.Fatalf("severity = %q, want a stall said as a degraded harness", delivery.Notification.Event.Severity)
	}
	cursors = harness.poll(t, cursors, notify.KindStallNoticed)
	standing, open, err := stalls.Standing()
	if err != nil || !open {
		t.Fatalf("Standing() = %v %v, want the stall recorded", open, err)
	}
	if !standing.Since.Equal(moment) || standing.Ready != 3 {
		t.Fatalf("the record says since %s over %d ready, want %s and 3", standing.Since, standing.Ready, moment)
	}

	// And then the seven and a half hours, at the sink's own fifteen-second poll.
	// It is said once per stall and never once per check, which is what the
	// durable record rather than this cursor guarantees.
	for check := 0; check < 60; check++ {
		harness.now = harness.now.Add(7 * time.Minute)
		cursors = harness.poll(t, cursors)
	}
	events, err := stalls.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("List() = %d stalls, want one across the whole window", len(events))
	}
}

// A fresh sink over a stall that is already open re-says nothing, because what
// makes once mean once is the record rather than any process's cursor. This is
// the crash and the restart, which is the case a cursor alone cannot hold.
func TestARestartedSinkDoesNotSayAStandingStallAgain(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	stalls := harness.watchesForStalls(t)
	harness.ready(2)
	harness.watched(t, runstate.WatchWatching, "watching the backlog until stopped", moment)

	harness.now = moment.Add(time.Hour)
	harness.poll(t, harness.start(), notify.KindWatchStarted, notify.KindStallNoticed)

	// A sink that comes back with no memory at all, on a watermark taken when it
	// came back. A stall that was already open before that is history to it, read
	// past exactly as every other record filed before a watermark is — not
	// announced a second time to somebody who was told about it an hour ago.
	harness.now = harness.now.Add(time.Hour)
	restarted := Cursors{SchemaVersion: CursorsSchemaVersion, Since: harness.now, Streams: map[string]Cursor{}}
	harness.now = harness.now.Add(2 * time.Hour)
	harness.poll(t, restarted)
	events, err := stalls.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(events) != 1 || !events[0].Open() {
		t.Fatalf("List() = %+v, want the one stall still open and not reopened", events)
	}
}

// What it says is what somebody woken at three in the morning has to act on:
// how long nothing has happened, how much was waiting, and — the fact that
// decides what they do — whether the thing that chooses work is dead or is still
// claiming to be watching.
func TestAStallSaysItsAgeItsQueueAndWhatTheChooserLastSaid(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.watchesForStalls(t)
	harness.ready(4)
	harness.watched(t, runstate.WatchWatching, "watching the backlog until stopped", moment)

	// The watch transition is read past inside the threshold, so what the next
	// pass says is the stall and nothing else.
	harness.now = moment.Add(20 * time.Minute)
	cursors := harness.poll(t, harness.start(), notify.KindWatchStarted)
	harness.now = moment.Add(7*time.Hour + 30*time.Minute)
	said := harness.say(t, cursors, notify.KindStallNoticed)
	for _, fact := range []string{"7 hours", "4 items", "watching", "said nothing since"} {
		if !strings.Contains(said.Body, fact) {
			t.Fatalf("body %q does not carry %q", said.Body, fact)
		}
	}
	if !strings.Contains(said.Body, "Next: the operator's") {
		t.Fatalf("body %q does not say whose move follows it", said.Body)
	}
}

// Everything that legitimately accounts for nothing having started, and none of
// them is a stall. Each of these is either a decision somebody made or the
// harness visibly working, and waking them about it is the nagging that makes a
// channel one nobody reads.
func TestNothingAccountedForIsEverSaidAsAStall(t *testing.T) {
	t.Parallel()

	for name, arrange := range map[string]func(*testing.T, *testHarness){
		"the operator held everything": func(t *testing.T, h *testHarness) {
			h.ready(3)
			h.watched(t, runstate.WatchWatching, "watching the backlog until stopped", moment)
			if _, err := h.holds.Hold(moment); err != nil {
				t.Fatalf("Hold() error = %v", err)
			}
		},
		"intake is held": func(t *testing.T, h *testHarness) {
			h.ready(3)
			h.watched(t, runstate.WatchWatching, "watching the backlog until stopped", moment)
			h.hold(t, "reordering the backlog first", moment)
		},
		"a run is in flight": func(t *testing.T, h *testHarness) {
			h.ready(3)
			h.watched(t, runstate.WatchWatching, "watching the backlog until stopped", moment)
			h.record(t, h.run(t, runstate.StatusRunning))
		},
		"the queue is drained": func(t *testing.T, h *testHarness) {
			h.ready(0)
			h.watched(t, runstate.WatchIdle, "the backlog is empty", moment)
		},
		"nobody has ever watched this product": func(t *testing.T, h *testHarness) {
			h.ready(6)
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			harness := newTestHarness(t, time.Time{})
			stalls := harness.watchesForStalls(t)
			arrange(t, harness)

			// Whatever else these states are said with — a hold posts itself, the
			// heartbeat repeats a held line hourly — none of them is ever said as a
			// stall, and none of them records one.
			harness.now = moment.Add(9 * time.Hour)
			cursors := harness.quietPass(t, harness.start())
			harness.now = harness.now.Add(9 * time.Hour)
			harness.quietPass(t, cursors)
			events, err := stalls.List()
			if err != nil {
				t.Fatalf("List() error = %v", err)
			}
			if len(events) != 0 {
				t.Fatalf("List() = %+v, want nothing recorded", events)
			}
		})
	}
}

// A stall that clears closes its event and says nothing about the clearing: what
// cleared it said so itself, as the run that started. And a second stall
// afterwards is a second thing to say rather than the same one said again.
func TestAClearingStallClosesItsEventAndTheNextOneIsSaidAfresh(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	stalls := harness.watchesForStalls(t)
	harness.ready(2)
	harness.watched(t, runstate.WatchWatching, "watching the backlog until stopped", moment)

	harness.now = moment.Add(time.Hour)
	cursors := harness.poll(t, harness.start(), notify.KindWatchStarted, notify.KindStallNoticed)

	// The queue drains, so nothing is waiting on anybody any more. Nothing is
	// said about that — what cleared it is not news — and the event closes with
	// what accounted for it.
	harness.ready(0)
	harness.now = harness.now.Add(time.Minute)
	cursors = harness.poll(t, cursors)
	events, err := stalls.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(events) != 1 || events[0].Open() {
		t.Fatalf("List() = %+v, want the stall closed", events)
	}
	if !strings.Contains(events[0].Cleared, "nothing ready") {
		t.Fatalf("Cleared = %q, want what cleared it recorded", events[0].Cleared)
	}

	// Work is admitted again and still nothing starts. That is a second stall
	// rather than the first one said twice, so it is said afresh.
	harness.ready(5)
	harness.now = harness.now.Add(3 * time.Hour)
	harness.poll(t, cursors, notify.KindStallNoticed)
	events, err = stalls.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(events) != 2 || events[0].Open() || !events[1].Open() {
		t.Fatalf("List() = %+v, want the first closed and a second standing", events)
	}
}

// A tracker that will not answer leaves this unable to tell a stalled machine
// from a drained queue, and it must not guess in either direction: inventing
// ready work wakes somebody for nothing, and assuming none is the silence this
// exists to end.
func TestATrackerThatCannotBeReadRecordsNoStall(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	stalls := harness.watchesForStalls(t)
	harness.feed.Backlog = brokenBacklog{}
	var said []string
	harness.feed.Log = func(format string, args ...any) { said = append(said, format) }
	harness.watched(t, runstate.WatchWatching, "watching the backlog until stopped", moment)

	harness.now = moment.Add(4 * time.Hour)
	harness.poll(t, harness.start(), notify.KindWatchStarted)
	events, err := stalls.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("List() = %+v, want nothing recorded over a tracker nobody could read", events)
	}
	if len(said) == 0 {
		t.Fatal("the sink said nothing about a tracker it could not read")
	}
}

// watchesForStalls gives the feed the product's own stall record, which is what
// every sink the harness builds is given, and hands it back so a test can read
// what was actually written down.
func (h *testHarness) watchesForStalls(t *testing.T) *runstate.StallStore {
	t.Helper()
	stalls, err := runstate.NewStallStore(h.root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStallStore() error = %v", err)
	}
	h.feed.Stalls = stalls
	return stalls
}

// quietPass makes one pass, allows it to say whatever else the records call for,
// and fails only if it said this product had gone quiet.
func (h *testHarness) quietPass(t *testing.T, cursors Cursors) Cursors {
	t.Helper()
	batch, err := h.feed.Poll(context.Background(), cursors)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	advanced := Cursors{SchemaVersion: CursorsSchemaVersion, Since: cursors.Since, Streams: map[string]Cursor{}}
	for stream, cursor := range cursors.Streams {
		advanced.Streams[stream] = cursor
	}
	for _, delivery := range batch.Deliveries {
		advanced.Streams[delivery.Stream] = delivery.Cursor
		if !delivery.Silent() && delivery.Notification.Event.Kind == notify.KindStallNoticed {
			t.Fatal("this product was said to have gone quiet, want something else accounting for it")
		}
	}
	return advanced
}

// stalled makes one pass and returns the delivery the stall stream produced,
// which is where a test reads the parts a rendered message does not carry:
// whether the operators were told directly at all.
func (h *testHarness) stalled(t *testing.T, cursors Cursors) Delivery {
	t.Helper()
	batch, err := h.feed.Poll(context.Background(), cursors)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	for _, delivery := range batch.Deliveries {
		if delivery.Stream == stallStream && !delivery.Silent() {
			return delivery
		}
	}
	t.Fatal("nothing was said about this product having gone quiet")
	return Delivery{}
}
