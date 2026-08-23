package slack

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The second person this project recognizes. The tier is defined by reaching all
// of them: the system is stopped on whoever gets to it first, and picking one
// would be the harness deciding whose evening this is.
const testSecondOperator = "U0SECOND"

// The brake tripping is the state today's overnight was about. The channel line
// was correct and was found by somebody looking; this is the half that does not
// wait to be looked at, and it owes four things — what stopped, why it is theirs,
// whose move follows, and where the whole of it is.
func TestTheBrakeTrippingReachesEveryOperatorWithTheFourThings(t *testing.T) {
	t.Parallel()

	held := time.Now().Add(-3 * time.Hour)
	sink, _, posts := newSteeringSinkWithFeed(t, &stoppedFeed{delivery: brakeDelivery(t, held)},
		testOperator, testSecondOperator)
	if err := sink.Once(context.Background()); err != nil {
		t.Fatalf("Once() error = %v", err)
	}

	if len(posts.opened) != 2 || posts.opened[0] != testOperator || posts.opened[1] != testSecondOperator {
		t.Fatalf("opened = %v, want a direct message with each operator", posts.opened)
	}
	if len(posts.requests) != 4 {
		t.Fatalf("posts = %#v, want an alarm and an ask under it for each of two operators", posts.requests)
	}

	alarm := posts.requests[0]
	if alarm.Channel != directChannel(testOperator) || alarm.ThreadTS != "" {
		t.Fatalf("alarm = %#v, want it at the top of that operator's own conversation", alarm)
	}
	// What stopped, why it is theirs, whose move follows, and where the record is.
	for _, owed := range []string{"intake is held", "released by a person", "Next: the operator's", "`yoyo status`"} {
		if !strings.Contains(alarm.Text, owed) {
			t.Fatalf("alarm %q does not say %q", alarm.Text, owed)
		}
	}

	ask := posts.requests[1]
	if ask.ThreadTS != posts.timestamps[0] {
		t.Fatalf("ask = %#v, want it in the thread under the alarm", ask)
	}
	for _, owed := range []string{"(a) ", "(b) ", "(c) ", "Recommended:"} {
		if !strings.Contains(ask.Text, owed) {
			t.Fatalf("ask %q does not carry %q", ask.Text, owed)
		}
	}
	// The second operator gets the same pair, in their own conversation.
	if posts.requests[2].Channel != directChannel(testSecondOperator) {
		t.Fatalf("posts = %#v, want the second operator told in their own conversation", posts.requests)
	}
}

// The answer in the thread is the decision, and it is recorded against which
// option was chosen rather than as the sentence somebody typed. That is the whole
// of why the options are lettered: a decision nobody can name later is what the
// ask exists to prevent.
func TestTheAnswerInTheDirectThreadIsTheDecision(t *testing.T) {
	t.Parallel()

	held := time.Now().Add(-3 * time.Hour)
	sink, directives, posts := newSteeringSinkWithFeed(t, &stoppedFeed{delivery: brakeDelivery(t, held)}, testOperator)
	if err := sink.Once(context.Background()); err != nil {
		t.Fatalf("Once() error = %v", err)
	}
	before := len(posts.requests)

	sink.steering.handle(context.Background(), answerIn(directChannel(testOperator), testOperator,
		"b — leave it held until I have read what the brake caught", "1750000009.000100", posts.timestamps[0]))

	recorded := onlyDirective(t, directives)
	if !strings.HasPrefix(recorded.Text, "chose (b) ") {
		t.Fatalf("text = %q, want the option that was chosen rather than only the words around it", recorded.Text)
	}
	if !strings.Contains(recorded.Text, "leave it held until I have read what the brake caught") {
		t.Fatalf("text = %q, want what the operator said kept beside the option", recorded.Text)
	}
	// The ask was about the whole line, so the decision is too. Narrowing it to
	// some item would record it against work it was never about.
	if len(recorded.Scope) != 0 {
		t.Fatalf("scope = %v, want a decision about the line to reach every item", recorded.Scope)
	}
	if recorded.Kind != directive.KindOperational || recorded.Pauses() {
		t.Fatalf("recorded = %+v, want a decision in force rather than one that stops more work", recorded)
	}

	if len(posts.requests) != before+1 {
		t.Fatalf("posts = %d, want one receipt for the decision", len(posts.requests))
	}
	receipt := posts.requests[len(posts.requests)-1]
	if receipt.ThreadTS != posts.timestamps[0] || receipt.Channel != directChannel(testOperator) {
		t.Fatalf("receipt = %#v, want it under the ask it answers", receipt)
	}
	if !strings.Contains(receipt.Text, recorded.ID) || !strings.Contains(receipt.Text, "chose (b)") {
		t.Fatalf("receipt = %q, want it to say what was decided and where it was written", receipt.Text)
	}
}

// The one ask whose options end the state themselves. The other two are ended by
// an act the harness may not perform for the operator, so their options name the
// command; a directive nobody has settled is ended by resolving it, which is
// already something a reply may do. So answering settles it, the work it stopped
// picks up, and the ask does not come back an hour later at somebody who believes
// they answered it — which is the noise the whole tier's discipline exists to
// prevent.
func TestAnsweringADirectiveAskSettlesItAndTheAskStops(t *testing.T) {
	t.Parallel()

	feed := &stoppedFeed{}
	sink, directives, posts := newSteeringSinkWithFeed(t, feed, testOperator)
	paused := pausingDirective(t, directives)
	reading := &HarnessFeed{Directives: directives}
	feed.delivery = directiveDelivery(t, reading, time.Now())
	if err := sink.Once(context.Background()); err != nil {
		t.Fatalf("Once() error = %v", err)
	}
	alarm := posts.timestamps[0]

	// The letter alone is not an answer, for the reason the command line gives:
	// the work resumes on the answer rather than on the act of answering.
	sink.steering.handle(context.Background(), answerIn(directChannel(testOperator), testOperator,
		"a", "1750000011.000100", alarm))
	held, err := directives.Load(paused)
	if err != nil || !held.Pauses() {
		t.Fatalf("directive = %+v (err %v), want it still holding the work", held, err)
	}
	if refusal := posts.requests[len(posts.requests)-1]; !strings.Contains(refusal.Text, "rather than on the act of answering") {
		t.Fatalf("refusal = %q, want it to say what a bare letter is missing", refusal.Text)
	}

	sink.steering.handle(context.Background(), answerIn(directChannel(testOperator), testOperator,
		"a — the second one, and say so in the design", "1750000011.000200", alarm))

	settled, err := directives.Load(paused)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !settled.Resolved() || settled.Pauses() {
		t.Fatalf("directive = %+v, want the answer to have settled it rather than left it standing", settled)
	}
	if letter, chose := directive.Decided(settled.Resolution); !chose || letter != "a" {
		t.Fatalf("resolution %q records %q (%t), want the option that was chosen", settled.Resolution, letter, chose)
	}
	if !strings.Contains(settled.Resolution, "the second one, and say so in the design") {
		t.Fatalf("resolution = %q, want what the operator actually answered", settled.Resolution)
	}
	// Nothing was written beside it: the answer settled the directive rather than
	// leaving a second one for somebody else to settle.
	recorded, err := directives.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 1 {
		t.Fatalf("recorded = %+v, want the one directive, settled", recorded)
	}
	if receipt := posts.requests[len(posts.requests)-1]; !strings.Contains(receipt.Text, paused) ||
		!strings.Contains(receipt.Text, "the second one, and say so in the design") {
		t.Fatalf("receipt = %q, want it to say which directive was settled and how", receipt.Text)
	}

	// And the state is gone, so the follow-up an hour later has nothing to say.
	if _, found, err := reading.directiveStoppage(); err != nil || found {
		t.Fatalf("directiveStoppage() found %t (err %v), want the ask to stop once it has been answered", found, err)
	}
}

// An answer that names no option is refused, visibly and with the letters in it.
// The person reading the refusal is in a chat client rather than looking at the
// message they were answering.
func TestAnAnswerThatNamesNoOptionIsRefusedAndSaysWhichThereAre(t *testing.T) {
	t.Parallel()

	held := time.Now().Add(-3 * time.Hour)
	sink, directives, posts := newSteeringSinkWithFeed(t, &stoppedFeed{delivery: brakeDelivery(t, held)}, testOperator)
	if err := sink.Once(context.Background()); err != nil {
		t.Fatalf("Once() error = %v", err)
	}

	sink.steering.handle(context.Background(), answerIn(directChannel(testOperator), testOperator,
		"leave it for now I think", "1750000009.000200", posts.timestamps[0]))

	recorded, err := directives.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 0 {
		t.Fatalf("recorded = %+v, want nothing written from an answer that decided nothing", recorded)
	}
	refusal := posts.requests[len(posts.requests)-1]
	for _, named := range []string{"(a)", "(b)", "(c)"} {
		if !strings.Contains(refusal.Text, named) {
			t.Fatalf("refusal %q does not name %q", refusal.Text, named)
		}
	}
}

// The identity check is the channel's, and it applies in a direct thread
// explicitly rather than by assuming a conversation with two members in it.
func TestAnAnswerFromSomebodyWithoutDirectWorkDecidesNothing(t *testing.T) {
	t.Parallel()

	held := time.Now().Add(-3 * time.Hour)
	sink, directives, posts := newSteeringSinkWithFeed(t, &stoppedFeed{delivery: brakeDelivery(t, held)}, testOperator)
	if err := sink.Once(context.Background()); err != nil {
		t.Fatalf("Once() error = %v", err)
	}

	sink.steering.handle(context.Background(), answerIn(directChannel(testOperator), testStranger,
		"a", "1750000009.000300", posts.timestamps[0]))

	recorded, err := directives.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 0 {
		t.Fatalf("recorded = %+v, want nothing from somebody this project has not granted direct-work", recorded)
	}
	if refusal := posts.requests[len(posts.requests)-1]; !strings.Contains(refusal.Text, "direct-work") {
		t.Fatalf("refusal = %q, want it to say why nothing was recorded", refusal.Text)
	}
}

// A pass that was refused halfway through the operators is tried again, and the
// people it already reached are not woken a second time about the same saying.
func TestOneSayingInterruptsEachPersonOnce(t *testing.T) {
	t.Parallel()

	held := time.Now().Add(-3 * time.Hour)
	feed := &stoppedFeed{delivery: brakeDelivery(t, held)}
	sink, _, posts := newSteeringSinkWithFeed(t, feed, testOperator, testSecondOperator)
	for pass := 0; pass < 2; pass++ {
		if err := sink.Once(context.Background()); err != nil {
			t.Fatalf("Once() error = %v", err)
		}
	}
	if len(posts.requests) != 4 {
		t.Fatalf("posts = %d, want one saying to interrupt each of two people once", len(posts.requests))
	}
}

// Nobody to tell is not an error and does not stop reporting. It is said where
// the operator setting the sink up is looking, because the channel cannot say it.
func TestNobodyIsMessagedWhenNobodyHoldsDirectWork(t *testing.T) {
	t.Parallel()

	held := time.Now().Add(-3 * time.Hour)
	sink, _, posts := newSteeringSinkWithFeed(t, &stoppedFeed{delivery: brakeDelivery(t, held)})
	var said []string
	sink.log = func(format string, args ...any) { said = append(said, format) }

	if err := sink.Once(context.Background()); err != nil {
		t.Fatalf("Once() error = %v", err)
	}
	if len(posts.requests) != 0 || len(posts.opened) != 0 {
		t.Fatalf("posts = %#v, want nobody messaged and nothing opened", posts.requests)
	}
	if !saidSomething(said, "nobody was told") {
		t.Fatalf("the sink said %v, want it to say there was nobody to tell", said)
	}
}

// The state every workspace installed from the previous manifest is in: the app
// may post but may not open a direct message. What it must not do is lose the
// alarm, and what it must not do either is stop the channel — so the messages of
// that pass stand and keep their place, the refusal is one only a person can
// clear so the sink says it once and waits it out, and the interrupted saying
// goes as soon as somebody reinstalls the app.
func TestAWorkspaceThatWillNotOpenADirectMessageKeepsTheAlarm(t *testing.T) {
	t.Parallel()

	held := time.Now().Add(-3 * time.Hour)
	feed := &stoppedFeed{
		delivery:  brakeDelivery(t, held),
		alongside: []Delivery{milestone(1, notify.KindRunStarted)},
	}
	sink, _, posts := newSteeringSinkWithFeed(t, feed, testOperator)
	posts.refuseDirects = "missing_scope"

	err := sink.Once(context.Background())
	if err == nil {
		t.Fatal("Once() = nil, want the refusal returned so the alarm is said again rather than dropped")
	}
	// A refusal only a person can clear is said once and waited out rather than
	// repeated every fifteen seconds, which is the delivery loop's own rule and the
	// line the setup document teaches an operator to read.
	if !PermanentError(err) {
		t.Fatalf("Once() = %v, want a refusal the sink waits out rather than retries hard", err)
	}
	// The channel said what it had to say before any of that, and said it there.
	if len(posts.requests) != 2 {
		t.Fatalf("posts = %#v, want the thread this pass opened and the milestone in it", posts.requests)
	}
	for _, post := range posts.requests {
		if post.Channel != "C1" {
			t.Fatalf("post = %#v, want nothing reaching a direct message the workspace refused to open", post)
		}
	}

	// A second pass while it still stands repeats none of that: the cursor was
	// written as each message went, before the direct half failed.
	if err := sink.Once(context.Background()); !PermanentError(err) {
		t.Fatalf("Once() = %v, want the same refusal while it stands", err)
	}
	if len(posts.requests) != 2 {
		t.Fatalf("posts = %#v, want the channel to keep its place across a pass the direct half failed", posts.requests)
	}

	// The operator reinstalls the app, and what was waiting to reach them goes.
	posts.refuseDirects = ""
	if err := sink.Once(context.Background()); err != nil {
		t.Fatalf("Once() error = %v", err)
	}
	if len(posts.opened) != 1 || len(posts.requests) != 4 {
		t.Fatalf("opened %v and posted %#v, want the interrupted saying delivered once it can be", posts.opened, posts.requests)
	}
}

// The noise discipline, which is what keeps the tier worth having. A single
// blocked item has an owner — the development manager's docket — and stays a
// channel warning with its mark on the thread.
func TestASingleBlockedItemReachesNobodyDirectly(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	blocked := harness.run(t, runstate.StatusFailed)
	blocked.Failure = "the checks never passed and the repair budget is spent"
	harness.record(t, blocked)

	cursors := harness.poll(t, harness.start(),
		notify.KindRunStarted, notify.KindChecksPassed, notify.KindBlockerRecorded)
	harness.now = harness.now.Add(6 * time.Hour)
	harness.poll(t, cursors)
}

// Capacity is waited out before anybody is interrupted about it. Below the
// budget there is nothing to decide — the parked runs resume from their own
// records — and past it, carrying on waiting is a decision made by default.
func TestCapacityIsWaitedOutBeforeAnybodyIsTold(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	parked := harness.run(t, runstate.StatusRunning)
	resets := moment.Add(5 * time.Hour)
	parked.UsageLimitResetsAt = &resets
	harness.record(t, parked)

	cursors := harness.poll(t, harness.start(), notify.KindRunStarted, notify.KindRunParked)
	// An hour in, waiting is still the plan and there is nothing to decide.
	harness.now = moment.Add(time.Hour)
	cursors = harness.poll(t, cursors)

	harness.now = moment.Add(3 * time.Hour)
	said := harness.say(t, cursors, notify.KindEscalationRaised)
	if !strings.Contains(said.Body, "parked on provider capacity") {
		t.Fatalf("body %q does not say what stopped", said.Body)
	}
}

// A directive nobody has settled is the one blocked state whose owner is the
// operator themselves: no role can answer it, and nothing else is going to ask.
func TestAnUnsettledDirectiveIsTheOperatorsAndNobodysElse(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	directives := harness.directives(t)
	id, err := directive.NewID()
	if err != nil {
		t.Fatalf("NewID() error = %v", err)
	}
	if err := directives.Record(directive.Directive{
		SchemaVersion: directive.SchemaVersion,
		ID:            id,
		ProductID:     "yoyodyne",
		Kind:          directive.KindAmbiguous,
		ReceivedBy:    domain.RoleProductManager,
		ReceivedAt:    moment,
		Text:          "which of the two publishing behaviours did you mean",
		Unresolved:    "which of the two publishing behaviours was meant",
		Scope:         []string{"yoyodyne-ifd.68.20"},
	}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}

	said := harness.say(t, harness.start(), notify.KindEscalationRaised)
	if !strings.Contains(said.Body, id) || !strings.Contains(said.Body, "which of the two publishing behaviours was meant") {
		t.Fatalf("body %q does not name the directive or what it left unsettled", said.Body)
	}
	// A directive that named one item is about that item, so the ask is addressed
	// there and the decision that answers it is scoped to it.
	if said.Topic != "work-item:yoyodyne-ifd.68.20" {
		t.Fatalf("topic = %q, want the item the directive named", said.Topic)
	}
}

// Releasing cancels the follow-ups, and nothing announces the clearing: the
// channel already carries the release, and a direct message saying an emergency
// is over is one more interruption for something that needs nobody.
func TestReleasingTheBrakeCancelsItsFollowUps(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.hold(t, "the harness held intake after runs kept blocking", moment)

	cursors := harness.poll(t, harness.start(), notify.KindIntakeHeld, notify.KindEscalationRaised)
	if _, _, err := harness.intake.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	cursors = harness.poll(t, cursors, notify.KindIntakeReleased)
	if standing := cursors.Streams[escalationStream].Standing; standing != "" {
		t.Fatalf("the escalation still stands on %q after the brake was released", standing)
	}
	for hour := 0; hour < 4; hour++ {
		harness.now = harness.now.Add(time.Hour)
		cursors = harness.poll(t, cursors)
	}
}

// One machine that has stopped once interrupts somebody once. Two of these
// standing together is one operator woken twice about one thing, and the order
// they are derived in is the order in which clearing one might clear the next.
func TestOnlyOneStoppedStateIsSaidAtATime(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.hold(t, "looking at the queue", moment)
	parked := harness.run(t, runstate.StatusRunning)
	resets := moment.Add(5 * time.Hour)
	parked.UsageLimitResetsAt = &resets
	harness.record(t, parked)
	harness.now = moment.Add(4 * time.Hour)

	cursors := harness.poll(t, harness.start(),
		notify.KindRunStarted, notify.KindRunParked, notify.KindIntakeHeld, notify.KindEscalationRaised)
	if standing := cursors.Streams[escalationStream].Standing; !strings.HasPrefix(standing, intakeMark) {
		t.Fatalf("standing on %q, want the brake, which is what an operator would clear first", standing)
	}
}

// stoppedFeed hands out one direct delivery on every pass, which is what a state
// that is still stopped looks like from the sink's side.
type stoppedFeed struct {
	mutex    sync.Mutex
	delivery Delivery
	// alongside is what the channel had to say in the same pass, ahead of the
	// direct one exactly as a real pass orders them. Each carries a position, so a
	// second pass does not repeat what the first already posted — which is how a
	// test can watch the channel keep its place while the direct half fails.
	alongside []Delivery
}

func (f *stoppedFeed) Poll(_ context.Context, cursors Cursors) (Batch, error) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	batch := Batch{Streams: map[string]struct{}{escalationStream: {}}}
	for _, delivery := range f.alongside {
		batch.Streams[delivery.Stream] = struct{}{}
		if delivery.Cursor.Position <= cursors.Streams[delivery.Stream].Position {
			continue
		}
		batch.Deliveries = append(batch.Deliveries, delivery)
	}
	// The direct one goes last, which is where a real pass puts it.
	batch.Deliveries = append(batch.Deliveries, f.delivery)
	return batch, nil
}

// brakeDelivery is the real derivation of a held intake, so what these tests post
// is what the sink would actually post rather than a hand-written stand-in.
func brakeDelivery(t *testing.T, at time.Time) Delivery {
	t.Helper()
	stopped, found := brakeStoppage(switches{
		intakeHeld: true,
		intake: runstate.IntakeHold{
			HeldAt: at,
			Reason: "the harness held intake after runs kept blocking",
		},
	})
	if !found {
		t.Fatal("a held intake is not a tripped brake, which is the whole of what this tier is for")
	}
	now := at.Add(time.Hour)
	return Delivery{
		Stream:       escalationStream,
		Cursor:       Cursor{Standing: stopped.mark, Said: now},
		Direct:       true,
		Telling:      telling(stopped, now, DefaultFollowUp),
		Notification: notify.FromEscalation(stopped.Escalation, now),
		InThread:     notify.EscalationOptions(stopped.Escalation, now),
	}
}

// directiveDelivery is the real derivation of a directive nobody has settled,
// read from the same record the sink's own answer path writes back to.
func directiveDelivery(t *testing.T, reading *HarnessFeed, now time.Time) Delivery {
	t.Helper()
	stopped, found, err := reading.directiveStoppage()
	if err != nil {
		t.Fatalf("directiveStoppage() error = %v", err)
	}
	if !found {
		t.Fatal("a directive nobody has settled is not a stopped system, which is what this tier is for")
	}
	return Delivery{
		Stream:       escalationStream,
		Cursor:       Cursor{Standing: stopped.mark, Said: now},
		Direct:       true,
		Telling:      telling(stopped, now, DefaultFollowUp),
		Notification: notify.FromEscalation(stopped.Escalation, now),
		InThread:     notify.EscalationOptions(stopped.Escalation, now),
	}
}

// pausingDirective is one directive nobody has settled, in the record the sink
// both reads the state from and writes the answer back to.
func pausingDirective(t *testing.T, directives *runstate.DirectiveStore) string {
	t.Helper()
	id, err := directive.NewID()
	if err != nil {
		t.Fatalf("NewID() error = %v", err)
	}
	if err := directives.Record(directive.Directive{
		SchemaVersion: directive.SchemaVersion,
		ID:            id,
		ProductID:     testProduct,
		Kind:          directive.KindAmbiguous,
		ReceivedBy:    domain.RoleProductManager,
		ReceivedAt:    time.Now().Add(-2 * time.Hour),
		Text:          "which of the two publishing behaviours did you mean",
		Unresolved:    "which of the two publishing behaviours was meant",
		Scope:         []string{testItem},
	}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	return id
}

// answerIn is one person replying in the thread under a direct message this sink
// sent. The conversation is named rather than derived from who is typing, because
// what the identity check is for is exactly the case where those differ.
func answerIn(channel, user, text, ts, threadTS string) socketEnvelope {
	return envelopeFor(map[string]any{
		"type":      "message",
		"user":      user,
		"text":      text,
		"ts":        ts,
		"thread_ts": threadTS,
		"channel":   channel,
	})
}

// directChannel is the conversation the test workspace hands back for one
// member, which Slack names for the pair rather than for any message in it.
func directChannel(member string) string { return "D" + member }

func saidSomething(said []string, wanted string) bool {
	for _, line := range said {
		if strings.Contains(line, wanted) {
			return true
		}
	}
	return false
}

// directives gives the feed a directive record, which is where the third of the
// three stopped states is read from.
func (h *testHarness) directives(t *testing.T) *runstate.DirectiveStore {
	t.Helper()
	store, err := runstate.NewDirectiveStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewDirectiveStore() error = %v", err)
	}
	h.feed.Directives = store
	return store
}
