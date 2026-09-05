package slack

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
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
		"a run is in flight and still moving": func(t *testing.T, h *testHarness) {
			h.ready(3)
			h.watched(t, runstate.WatchWatching, "watching the backlog until stopped", moment)
			h.record(t, h.run(t, runstate.StatusRunning))
			// The run's record is stamped at `moment` and these passes are hours
			// later, so the window is what decides whether it still counts as
			// moving. It is widened past them here because this case is about a run
			// that is demonstrably working; the phantom is its own test below.
			h.feed.RunActivityWindow = 24 * time.Hour
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
	// what accounted for it. It closes at the next due reading rather than the
	// next poll, because only the tracker can say the queue drained and that is
	// asked once a heartbeat.
	harness.ready(0)
	harness.now = harness.now.Add(DefaultHeartbeat + time.Minute)
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

// An idle product asks the tracker once a heartbeat and not once a poll.
//
// A drained queue is deliberately not one of the states that account for the
// quiet — nothing but the tracker can say the queue is drained — so the check
// that spends the read is true on every poll of a perfectly healthy idle
// product. Without a second gate that is a `bd` process every fifteen seconds
// forever, on the machine that is behaving best.
func TestAnIdleProductAsksTheTrackerOnceAHeartbeatRatherThanOnceAPoll(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	stalls := harness.watchesForStalls(t)
	backlog := harness.tallies(0)
	harness.watched(t, runstate.WatchIdle, "the backlog is empty", moment)

	// Six hours of a healthy idle machine, at the sink's own fifteen-second poll.
	harness.now = moment.Add(time.Hour)
	cursors := harness.quietPass(t, harness.start())
	for check := 0; check < 6*60*4; check++ {
		harness.now = harness.now.Add(15 * time.Second)
		cursors = harness.quietPass(t, cursors)
	}

	// One read an hour over six hours, rather than one every fifteen seconds.
	// The figure is reported either way, because what this guards against is a
	// regression back towards the unbounded case — 1440 reads over this window —
	// and the number is the only thing that says how far from it this is.
	t.Logf("the tracker was read %d time(s) over six idle hours, against 1440 polls", backlog.asked)
	if backlog.asked > 7 {
		t.Fatalf("the tracker was read %d time(s) over six idle hours, want about one an hour", backlog.asked)
	}
	// Never reading it is the opposite failure and is not a pass: a watchdog that
	// asks nothing notices nothing.
	if backlog.asked == 0 {
		t.Fatal("the tracker was never read, so a stall could not have been noticed at all")
	}
	events, err := stalls.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("List() = %+v, want nothing recorded for a drained queue", events)
	}
}

// A run whose process is gone does not account for the quiet.
//
// This is the case the whole instrument exists for. A killed run leaves durable
// state saying it is in flight until `yoyo reconcile` settles it, so reading
// "in flight" as "working" would silence the watchdog for exactly the crash it
// is watching for. What separates the two is the run's own record moving: a
// working run stamps every provider event onto it, and a dead one stamps
// nothing.
func TestARunWhoseProcessIsGoneDoesNotSilenceTheWatchdog(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	stalls := harness.watchesForStalls(t)
	harness.ready(3)
	harness.watched(t, runstate.WatchWatching, "watching the backlog until stopped", moment)
	// A run in flight, whose record stops moving at `moment` because the process
	// carrying it was killed. Nothing settles it and nothing rewrites it.
	harness.record(t, harness.run(t, runstate.StatusRunning))

	// While the record is still fresh the run accounts for the quiet, which is
	// the behaviour a working run relies on.
	harness.now = moment.Add(30 * time.Minute)
	cursors := harness.quietPass(t, harness.start())
	if _, open, err := stalls.Standing(); err != nil || open {
		t.Fatalf("Standing() = %v %v, want a moving run to account for the quiet", open, err)
	}

	// Past the window the record has stopped saying anything about a live
	// process, and the stall is noticed.
	harness.now = moment.Add(readmodel.DefaultRunActivityWindow + time.Hour)
	harness.poll(t, cursors, notify.KindStallNoticed)
	standing, open, err := stalls.Standing()
	if err != nil || !open {
		t.Fatalf("Standing() = %v %v, want the stall recorded over a phantom run", open, err)
	}
	if !standing.Since.Equal(moment) {
		t.Fatalf("the record says since %s, want %s — the moment the last run started", standing.Since, moment)
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

// The other window, replayed: 2026-09-05, 12:13Z to 13:43Z. The session was
// alive and polling, nothing started because the provider would not serve any
// more, and the alarm fired at half an hour saying nothing accounted for it —
// when the pause was the whole of the accounting. What this asks for is the
// opposite of the test above: the same silence, and no stall.
func TestAProviderWindowIsSaidOnceAsANoteAndNeverAsAStall(t *testing.T) {
	t.Parallel()

	// The window as the session recorded it: entered at 12:13Z, lifting at 13:43Z.
	opened := time.Date(2026, 9, 5, 12, 13, 0, 0, time.UTC)
	lifts := time.Date(2026, 9, 5, 13, 43, 0, 0, time.UTC)
	harness := newTestHarness(t, time.Time{})
	stalls := harness.watchesForStalls(t)
	harness.ready(3)
	harness.watched(t, runstate.WatchWatching, "watching the backlog until stopped", opened.Add(-time.Hour))
	harness.waitingOnProvider(t, "waiting on the provider's usage window until 13:43Z", opened, &lifts)

	// Half an hour in — past the threshold that fired the alarm — and it is a note
	// in the channel rather than a warning on somebody's phone.
	harness.now = opened.Add(30 * time.Minute)
	start := harness.start()
	delivery := harness.stalled(t, start)
	if delivery.Notification.Event.Kind != notify.KindProviderWindow {
		t.Fatalf("kind = %q, want the provider's window rather than a stall", delivery.Notification.Event.Kind)
	}
	if delivery.Notification.Event.Severity != report.SeverityNote {
		t.Fatalf("severity = %q, want a note", delivery.Notification.Event.Severity)
	}
	if delivery.Direct {
		t.Fatal("the provider's window was taken to somebody directly, want it said in the channel")
	}
	said, err := notify.Render(delivery.Notification.Topic, delivery.Notification.Speaker, delivery.Notification.Event)
	if err != nil {
		t.Fatalf("the provider's window could not be said: %v", err)
	}
	if !strings.Contains(said.Body, "waiting on the provider's usage window until 13:43Z") {
		t.Fatalf("body %q does not say what the harness is waiting on, with the reset time", said.Body)
	}
	if !strings.Contains(said.Body, "Next: nobody's") {
		t.Fatalf("body %q does not say the window is nobody's move", said.Body)
	}
	// The session's own account of the poll reaches the channel beside it, which
	// is where the same words are said as the session's rather than the sink's.
	cursors := harness.poll(t, start, notify.KindWatchStarted, notify.KindWatchIdle, notify.KindProviderWindow)

	// And then the ninety minutes, at the sink's own fifteen-second poll. Nothing
	// is recorded as a stall and the note is not said twice.
	for harness.now.Add(5 * time.Minute).Before(lifts) {
		harness.now = harness.now.Add(5 * time.Minute)
		cursors = harness.quietPass(t, cursors)
	}
	events, err := stalls.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("List() = %+v, want nothing recorded: the pause was the accounting", events)
	}
}

// A window the session recorded and then never came out of is not an account of
// anything. The record is the last word of a session that has said nothing
// since, which is exactly the shape of a session that died — so past the
// deadline it accounts for nothing and the watchdog says what it always said.
func TestAWindowThatHasLiftedStopsAccountingForTheQuiet(t *testing.T) {
	t.Parallel()

	opened := time.Date(2026, 9, 5, 12, 13, 0, 0, time.UTC)
	lifts := time.Date(2026, 9, 5, 13, 43, 0, 0, time.UTC)
	harness := newTestHarness(t, time.Time{})
	stalls := harness.watchesForStalls(t)
	harness.ready(3)
	harness.watched(t, runstate.WatchWatching, "watching the backlog until stopped", opened.Add(-time.Hour))
	harness.waitingOnProvider(t, "waiting on the provider's usage window until 13:43Z", opened, &lifts)

	harness.now = lifts.Add(-time.Minute)
	cursors := harness.poll(t, harness.start(),
		notify.KindWatchStarted, notify.KindWatchIdle, notify.KindProviderWindow)

	// The window lifted and the session went on saying nothing. That is a stall.
	harness.now = lifts.Add(readmodel.DefaultStallThreshold + time.Minute)
	harness.poll(t, cursors, notify.KindStallNoticed)
	if _, open, err := stalls.Standing(); err != nil || !open {
		t.Fatalf("Standing() = %v %v, want a stall once the window it was waiting on has lifted", open, err)
	}
}

// watchesForStalls gives the feed the product's own stall record, which is what
// every sink the harness builds is given, and hands it back so a test can read
// what was actually written down.
// tallyBacklog answers what is ready and counts how often it was asked, which is
// the whole of what the cost rule is about: `bd` is a process the sink spawns,
// and the number of times it spawns one is the thing under test.
type tallyBacklog struct {
	count int
	asked int
}

func (b *tallyBacklog) Ready(context.Context) (int, error) {
	b.asked++
	return b.count, nil
}

// tallies gives the feed a backlog that counts the reads made of it.
func (h *testHarness) tallies(count int) *tallyBacklog {
	backlog := &tallyBacklog{count: count}
	h.feed.Backlog = backlog
	return backlog
}

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
