package slack

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The whole system waiting on a person is the one message whose delivery cannot
// depend on the reader looking, so it goes to every operator the project mapped
// rather than to whichever of them the harness picked. The alarm is one message
// and the ask is a reply under it, so what interrupts somebody is a line and what
// they read when they have a moment is the whole of it.
func TestAStoppedSystemIsCarriedToEveryMappedOperator(t *testing.T) {
	t.Parallel()

	posts := &recordedPosts{}
	feed := &fixedFeed{deliveries: []Delivery{stoppedDelivery()}}
	sink := newEscalatingSink(t, t.TempDir(), feed, posts, "U0FIRST", "U0SECOND")

	if err := sink.Once(context.Background()); err != nil {
		t.Fatalf("Once() error = %v", err)
	}

	if len(posts.opens) != 2 || posts.opens[0] != "U0FIRST" || posts.opens[1] != "U0SECOND" {
		t.Fatalf("opened %v, want a conversation with every mapped operator", posts.opens)
	}
	// Two each: the alarm, and the ask in the thread it hangs from.
	if len(posts.directPosts) != 4 {
		t.Fatalf("said %d direct messages, want the alarm and the ask for each of the two", len(posts.directPosts))
	}
	alarm := posts.directPosts[0]
	if alarm.ThreadTS != "" {
		t.Fatalf("the alarm is threaded under %q, want it to open the thread rather than reply into one", alarm.ThreadTS)
	}
	if !strings.Contains(alarm.Text, "intake is held") {
		t.Fatalf("the alarm reads %q, want it to say what stopped", alarm.Text)
	}
	ask := posts.directPosts[1]
	if ask.ThreadTS == "" {
		t.Fatalf("the ask is not in a thread, want it under the alarm rather than beside it")
	}
	for _, letter := range []string{"(a)", "(b)", "(c)"} {
		if !strings.Contains(ask.Text, letter) {
			t.Fatalf("the ask reads %q, want the options lettered so a reply can name one", ask.Text)
		}
	}

	// What was said to whom is durable, so a sink that comes back does not wake
	// anybody a second time about the same thing.
	directs, err := sink.store.LoadDirects()
	if err != nil {
		t.Fatalf("LoadDirects() error = %v", err)
	}
	for _, member := range []string{"U0FIRST", "U0SECOND"} {
		sent, told := directs.Told(stoppedDelivery().Telling, member)
		if !told || !sent.Asked {
			t.Fatalf("%s is recorded as %#v, want the telling recorded as finished", member, sent)
		}
		if len(sent.Options) != 3 {
			t.Fatalf("%s was offered %d options, want the ones the message actually listed kept for the answer", member, len(sent.Options))
		}
	}
}

// A second pass over a state that has already been told to everybody says
// nothing more. Being interrupted twice about one thing is how a tier that is
// reserved for the system stopping stops being read.
func TestAnOperatorIsNotToldTheSameTellingTwice(t *testing.T) {
	t.Parallel()

	posts := &recordedPosts{}
	feed := &fixedFeed{deliveries: []Delivery{stoppedDelivery()}}
	sink := newEscalatingSink(t, t.TempDir(), feed, posts, "U0FIRST")

	if err := sink.Once(context.Background()); err != nil {
		t.Fatalf("Once() error = %v", err)
	}
	said := len(posts.directPosts)
	if err := sink.Once(context.Background()); err != nil {
		t.Fatalf("Once() error = %v", err)
	}
	if len(posts.directPosts) != said {
		t.Fatalf("said %d direct messages over two passes, want the second to add nothing", len(posts.directPosts))
	}
}

// The repair this round was handed back for. The setup document offers deleting
// `im:write` to anybody who would rather not be messaged, and there is
// deliberately no switch that turns this tier off — so a workspace that will not
// open a direct message has to cost the escalation tier and nothing else. It
// used to fail the whole delivery pass, which left the documented choice as a
// channel that permanently reported late with no way back but a reinstall.
func TestAMissingDirectMessageScopeCostsTheTierAndNotTheChannel(t *testing.T) {
	t.Parallel()

	posts := &recordedPosts{refuseOpen: "missing_scope"}
	feed := &fixedFeed{deliveries: []Delivery{
		milestone(1, notify.KindRunStarted),
		stoppedDelivery(),
	}}
	sink := newEscalatingSink(t, t.TempDir(), feed, posts, "U0FIRST")
	var said []string
	sink.log = func(format string, args ...any) { said = append(said, fmt.Sprintf(format, args...)) }

	if err := sink.Once(context.Background()); err != nil {
		t.Fatalf("Once() error = %v, want a missing scope to cost the tier rather than the pass", err)
	}

	// The channel keeps its pace: the ordinary milestone opened its thread and was
	// said in it, and both cursors were written. That is the whole of what
	// "reporting is unaffected" means, and none of it used to happen.
	if len(posts.directPosts) != 0 {
		t.Fatalf("said %d direct messages, want none where the workspace will not open one", len(posts.directPosts))
	}
	if len(posts.requests) == 0 {
		t.Fatal("nothing was posted in the channel, want the tier's refusal to cost the channel nothing")
	}
	cursors, err := sink.store.LoadCursors()
	if err != nil {
		t.Fatalf("LoadCursors() error = %v", err)
	}
	if got := cursors.Streams["run:run-a"].Position; got != 1 {
		t.Fatalf("the milestone's cursor is at %d, want the channel's own stream to have moved on", got)
	}
	if cursors.Streams[escalationStream].Standing == "" {
		t.Fatal("the escalation cursor did not advance, so the pass would meet the same refusal forever")
	}

	refusals := refusalsAmong(said)
	if len(refusals) != 1 {
		t.Fatalf("said %q, want the refusal said exactly once", said)
	}
	if !strings.Contains(refusals[0], "im:write") {
		t.Fatalf("said %q, want the line to name the scope that grants it", refusals[0])
	}
	if !strings.Contains(refusals[0], "channel is unaffected") {
		t.Fatalf("said %q, want the line to say the channel is still reporting", refusals[0])
	}
}

// The same refusal every fifteen seconds is how a log stops being read, and this
// tier has no off switch to spare it. It is said on the first pass and not again
// while it stands.
func TestAStandingDirectMessageRefusalIsSaidOnce(t *testing.T) {
	t.Parallel()

	posts := &recordedPosts{refuseOpen: "missing_scope"}
	feed := &fixedFeed{deliveries: []Delivery{stoppedDelivery()}}
	sink := newEscalatingSink(t, t.TempDir(), feed, posts, "U0FIRST")
	var said []string
	sink.log = func(format string, args ...any) { said = append(said, fmt.Sprintf(format, args...)) }

	for pass := 0; pass < 3; pass++ {
		// Every pass has the state to say again, so only the standing refusal
		// stops the line being repeated. The watermark is kept as the first pass
		// took it, because re-taking it is a different thing to say and not what
		// this is counting.
		cursors, err := sink.store.LoadCursors()
		if err != nil {
			t.Fatalf("LoadCursors() error = %v", err)
		}
		delete(cursors.Streams, escalationStream)
		if err := sink.store.SaveCursors(cursors); err != nil {
			t.Fatalf("SaveCursors() error = %v", err)
		}
		if err := sink.Once(context.Background()); err != nil {
			t.Fatalf("Once() error = %v", err)
		}
	}
	if refusals := refusalsAmong(said); len(refusals) != 1 {
		t.Fatalf("said %q over three passes, want a standing refusal said once", said)
	}
}

// refusalsAmong picks the lines about direct messages being refused out of
// everything the sink said. A pass says other things — which moment it is
// reporting from, what it posted — and counting those as repetitions would make
// this test about the sink's whole log rather than about the one line that must
// not repeat.
func refusalsAmong(said []string) []string {
	var refusals []string
	for _, line := range said {
		if strings.Contains(line, "nobody can be messaged directly") {
			refusals = append(refusals, line)
		}
	}
	return refusals
}

// A workspace having a bad minute is not the documented opt-out, so it is handed
// back to the pass and tried again. Silencing the tier on a transient refusal
// would be the harness turning off the thing it deliberately gave no off switch.
func TestATransientRefusalStillCostsThePassRatherThanTheTier(t *testing.T) {
	t.Parallel()

	posts := &recordedPosts{refuseOpen: "internal_error"}
	feed := &fixedFeed{deliveries: []Delivery{stoppedDelivery()}}
	sink := newEscalatingSink(t, t.TempDir(), feed, posts, "U0FIRST")

	if err := sink.Once(context.Background()); err == nil {
		t.Fatal("Once() = nil, want a workspace that is merely down retried rather than silenced")
	}
	if sink.escalating != "" {
		t.Fatalf("escalating = %q, want a transient refusal not remembered as standing", sink.escalating)
	}
}

// A project that has mapped nobody is told nothing, and that already reads as an
// absence rather than a failure. It is here beside the refusal above because the
// two are the same answer — the tier goes quiet and the channel does not — and a
// change that made one of them fail the pass should fail this too.
func TestNobodyToMessageCostsThePassNothing(t *testing.T) {
	t.Parallel()

	posts := &recordedPosts{}
	feed := &fixedFeed{deliveries: []Delivery{stoppedDelivery()}}
	sink := newEscalatingSink(t, t.TempDir(), feed, posts)

	if err := sink.Once(context.Background()); err != nil {
		t.Fatalf("Once() error = %v, want nobody to tell to cost the pass nothing", err)
	}
	if len(posts.opens) != 0 {
		t.Fatalf("opened %v, want nothing opened for a project that mapped nobody", posts.opens)
	}
}

// The noise discipline, and the reason this tier stays worth reading. A single
// item that is blocked has an owner and a docket, and something that interrupted
// every operator for each of those would teach them to ignore the ones that
// matter. It stays a channel message with its own mark.
func TestASingleBlockedItemIsNotPushedToAnybody(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	blocked := harness.run(t, runstate.StatusFailed)
	blocked.Failure = "independent review requires repair after 2 of 2 permitted attempts"
	harness.record(t, blocked)

	batch, err := harness.feed.Poll(context.Background(), harness.start())
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	for _, delivery := range batch.Deliveries {
		if delivery.Direct {
			t.Fatalf("a blocked item produced %q as a direct message, want it left in the channel", delivery.Notification.Event.Kind)
		}
	}
	// It is still said, because the channel is where a blocked item belongs.
	if !says(batch, notify.KindBlockerRecorded) {
		t.Fatal("a blocked item said nothing at all, want it reported in the channel")
	}
}

// Releasing the brake cancels its own follow-ups, and it does so by the cursor
// forgetting the state rather than by anything cancelling anything: what cleared
// it is what says so.
func TestReleasingTheBrakeCancelsItsFollowUps(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.hold(t, "the harness held intake after runs kept blocking", moment)

	cursors := harness.poll(t, harness.start(), notify.KindIntakeHeld, notify.KindEscalationRaised)
	if cursors.Streams[escalationStream].Standing == "" {
		t.Fatal("the brake was pushed to nobody, want the state standing on the cursor")
	}

	if _, _, err := harness.intake.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	cursors = harness.poll(t, cursors, notify.KindIntakeReleased)
	if standing := cursors.Streams[escalationStream].Standing; standing != "" {
		t.Fatalf("the escalation is still standing on %q after the brake was released", standing)
	}

	// And nothing is said again however long it stays released.
	harness.now = harness.now.Add(6 * time.Hour)
	harness.poll(t, cursors)
}

// Two states standing at once is one operator interrupted twice about a machine
// that has stopped once, so they are derived one at a time and in the order
// somebody would clear them.
func TestOnlyOneStoppedStateIsPushedAtATime(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.hold(t, "the harness held intake after runs kept blocking", moment)
	if _, err := harness.holds.Hold(moment); err != nil {
		t.Fatalf("Hold() error = %v", err)
	}

	batch, err := harness.feed.Poll(context.Background(), harness.start())
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	pushed := 0
	for _, delivery := range batch.Deliveries {
		if delivery.Direct {
			pushed++
		}
	}
	if pushed != 1 {
		t.Fatalf("pushed %d states at once, want the one an operator would clear first", pushed)
	}
}

// says reports whether a batch carries one kind, for the tests that care what was
// said rather than in what order.
func says(batch Batch, want notify.Kind) bool {
	for _, delivery := range batch.Deliveries {
		if !delivery.Silent() && delivery.Notification.Event.Kind == want {
			return true
		}
	}
	return false
}

// stoppedDelivery is one state that has stopped the whole system, as the feed
// hands it to the sink: the alarm, the ask that goes under it, and the name of
// this saying of it.
func stoppedDelivery() Delivery {
	raised := notify.Escalation{
		Stopped: "intake is held, so nothing new is being chosen however much is ready",
		Why:     "intake is released by a person. The harness will not start choosing again on its own.",
		Since:   moment,
		Record:  "`yoyo status`, which says what is ready behind the hold",
		Options: []notify.Option{
			{Text: "release intake and let the line choose from the backlog again"},
			{Text: "keep it held until what stopped it is dealt with, and say what that is"},
			{Text: "something else, or you want more of the record in front of you first"},
		},
		Recommendation: "(a), on the evidence there is",
		Topic:          notify.Product(),
	}
	return Delivery{
		Stream:       escalationStream,
		Cursor:       Cursor{Position: 1, Standing: intakeMark + stamp(moment), Said: moment},
		Direct:       true,
		Telling:      intakeMark + stamp(moment) + "#0",
		Notification: notify.FromEscalation(raised, moment),
		InThread:     notify.EscalationOptions(raised, moment),
	}
}

// newEscalatingSink is a sink that can message the operators it was given: it has
// somewhere to record what they answer, which is what the ask promises them.
func newEscalatingSink(t *testing.T, root string, feed Feed, posts *recordedPosts, operators ...string) *Sink {
	t.Helper()
	store, err := NewStore(root, testProduct)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	directives, err := runstate.NewDirectiveStore(root, testProduct)
	if err != nil {
		t.Fatalf("NewDirectiveStore() error = %v", err)
	}
	sink, err := New(Options{
		Channel:    "C1",
		Store:      store,
		API:        newTestAPI(t, posts.handle),
		Feed:       feed,
		Directives: directives,
		Operators:  operators,
		Now:        func() time.Time { return moment },
		Log:        func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	sink.pace.sleep = func(context.Context, time.Duration) error { return nil }
	return sink
}
