package slack

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The state the asks below are about, and the direct message thread the operator
// answers in.
const (
	testStoppedMark = "intake:2026-08-30T02:02:00Z"
	testStopped     = "intake is held — overnight"
	testDMChannel   = "D0OPERATOR"
	testDMThreadTS  = "1750000000.000900"
)

var testOptions = []string{
	"release intake so admitted work can be chosen again",
	"keep intake held; let what is running finish and choose nothing new",
}

// The grammar is one rule and one refusal. A number the ask offered is the
// option it names; everything else is a decision in the operator's own words,
// which is the reading that cannot silently throw away what somebody said.
func TestParseDecisionReadsTheOptionAReplyNamedAndKeepsTheWords(t *testing.T) {
	t.Parallel()

	for _, said := range []struct {
		reply  string
		option int
	}{
		{reply: "1", option: 1},
		{reply: "2", option: 2},
		{reply: "2.", option: 2},
		{reply: "2) keep it held for now", option: 2},
		{reply: "option 1", option: 1},
		{reply: "Option 2 — for the reason you said", option: 2},
		// Anything the list did not anticipate is still a decision, recorded as it
		// was typed rather than forced onto the nearest number.
		{reply: "leave it until the morning and we will look together", option: 0},
		{reply: "neither of those", option: 0},
	} {
		chosen, err := parseDecision(said.reply, len(testOptions))
		if err != nil {
			t.Fatalf("parseDecision(%q) error = %v, want it read as a decision", said.reply, err)
		}
		if chosen.option != said.option {
			t.Fatalf("parseDecision(%q).option = %d, want %d", said.reply, chosen.option, said.option)
		}
		// What the operator typed is kept whole whichever they meant, because it is
		// what gets written into a record somebody reads weeks later.
		if chosen.said != strings.TrimSpace(said.reply) {
			t.Fatalf("parseDecision(%q).said = %q, want the operator's own words", said.reply, chosen.said)
		}
	}
}

// A number that names no option is refused rather than recorded as prose.
// Somebody who typed 5 was answering the list and believes they have; recording
// the digit verbatim would leave them thinking they had decided something.
func TestParseDecisionRefusesANumberThatNamesNoOption(t *testing.T) {
	t.Parallel()

	for _, said := range []string{"5", "0", "-1", "option 9", "option"} {
		chosen, err := parseDecision(said, len(testOptions))
		if err == nil {
			t.Fatalf("parseDecision(%q) = %+v, want a number naming no option refused", said, chosen)
		}
		if !strings.Contains(err.Error(), "own words") {
			t.Fatalf("parseDecision(%q) error = %q, want it to say what can be typed instead", said, err)
		}
	}
	if _, err := parseDecision("   ", len(testOptions)); err == nil {
		t.Fatal("parseDecision(blank) recorded something, want a reply that said nothing refused")
	}
}

// The decision is recorded where every run reads it: one operational directive
// in the same durable record a terminal writes to, unscoped because what was
// asked was about the whole line rather than any one item. The option chosen is
// named in it, with the words the operator typed.
func TestADecisionRecordsTheChosenOptionWhereEveryRunReadsIt(t *testing.T) {
	t.Parallel()

	sink, directives, _ := newDecidingSink(t, testOperator)
	answer := sink.steering.decide(testAsked(), inboundMessage{
		user:     testOperator,
		text:     "2 — let the overnight finish first",
		ts:       "1750000001.000200",
		threadTS: testDMThreadTS,
		channel:  testDMChannel,
	}, time.Now())

	recorded := onlyDirective(t, directives)
	if recorded.Kind != directive.KindOperational || recorded.Pauses() {
		t.Fatalf("recorded = %+v, want an operational directive that pauses nothing", recorded)
	}
	if len(recorded.Scope) != 0 || !recorded.Affects("yoyodyne-ifd.1") {
		t.Fatalf("recorded = %+v, want a decision about the line to reach every item", recorded)
	}
	if recorded.ReceivedBy != domain.RoleProductManager {
		t.Fatalf("received by %q, want the product manager for a decision that named no role", recorded.ReceivedBy)
	}
	// All three, because a record holding any one of them alone is unreadable
	// afterwards: what was asked, which option was taken, and what was said.
	for _, wanted := range []string{testStopped, "option 2", testOptions[1], "let the overnight finish first"} {
		if !strings.Contains(recorded.Text, wanted) {
			t.Fatalf("recorded text = %q, want it to carry %q", recorded.Text, wanted)
		}
	}
	if !strings.Contains(answer, recorded.ID) {
		t.Fatalf("answer = %q, want it to name the directive it recorded", answer)
	}
}

// A decision in the operator's own words is recorded exactly as one that took an
// option, because the options are shortcuts rather than the whole of what may be
// answered.
func TestADecisionInTheOperatorsOwnWordsIsRecordedAsTheyTypedIt(t *testing.T) {
	t.Parallel()

	sink, directives, _ := newDecidingSink(t, testOperator)
	sink.steering.decide(testAsked(), inboundMessage{
		user:     testOperator,
		text:     "neither; I am about to change what is admitted",
		ts:       "1750000001.000200",
		threadTS: testDMThreadTS,
		channel:  testDMChannel,
	}, time.Now())

	recorded := onlyDirective(t, directives)
	if !strings.Contains(recorded.Text, "I am about to change what is admitted") {
		t.Fatalf("recorded text = %q, want the operator's own words", recorded.Text)
	}
	if strings.Contains(recorded.Text, "chose option") {
		t.Fatalf("recorded text = %q, want no option claimed for a reply that named none", recorded.Text)
	}
}

// Authority defaults closed here exactly as it does in a thread, and it is
// checked before the reply is read at all: an ask somebody forwarded is not a
// grant, and the refusal names what is missing rather than going silent.
func TestADecisionFromSomebodyWithoutDirectWorkRecordsNothingAndSaysSo(t *testing.T) {
	t.Parallel()

	sink, directives, _ := newDecidingSink(t, testOperator)
	answer := sink.steering.decide(testAsked(), inboundMessage{
		user:     testStranger,
		text:     "1",
		ts:       "1750000001.000200",
		threadTS: testDMThreadTS,
		channel:  testDMChannel,
	}, time.Now())

	recorded, err := directives.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 0 {
		t.Fatalf("recorded %+v, want nothing recorded for somebody the project granted nothing", recorded)
	}
	if !strings.Contains(answer, "direct-work") {
		t.Fatalf("answer = %q, want a refusal naming the grant it is missing", answer)
	}
}

// The whole of the inbound half: a reply in the thread the ask was made in is
// the decision. Nothing else is — no command, no button — so this drives the
// connection exactly as Slack does and expects the record to move.
func TestAReplyInTheAsksThreadIsTheDecision(t *testing.T) {
	t.Parallel()

	sink, directives, posts := newDecidingSink(t, testOperator)
	sink.steering.handle(context.Background(), envelopeFor(map[string]any{
		"type": "message", "user": testOperator, "text": "1",
		"ts": "1750000001.000200", "thread_ts": testDMThreadTS, "channel": testDMChannel,
	}))

	recorded := onlyDirective(t, directives)
	if !strings.Contains(recorded.Text, testOptions[0]) {
		t.Fatalf("recorded text = %q, want the option the reply chose", recorded.Text)
	}
	// The answer goes back into the thread that asked, addressed to whoever
	// answered: a decision acknowledged at the top of a channel would tell
	// everybody except the person who made it.
	answer := onlyPost(t, posts)
	if answer.Channel != testDMChannel || answer.ThreadTS != testDMThreadTS {
		t.Fatalf("answer = %#v, want it in the direct message thread the ask was made in", answer)
	}
	if !strings.HasPrefix(answer.Text, "<@"+testOperator+"> ") {
		t.Fatalf("answer = %q, want it to tag whoever decided", answer.Text)
	}
}

// A direct message thread this sink asked nothing in is somebody having their
// own conversation with the app. Answering it would be talking over them, and
// there is no ask to read a number against.
func TestAReplyInADirectMessageThreadTheSinkNeverAskedInIsLeftAlone(t *testing.T) {
	t.Parallel()

	sink, directives, posts := newDecidingSink(t, testOperator)
	sink.steering.handle(context.Background(), envelopeFor(map[string]any{
		"type": "message", "user": testOperator, "text": "1",
		"ts": "1750000001.000200", "thread_ts": "1749999999.000000", "channel": "D0SOMEBODYELSE",
	}))

	recorded, err := directives.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 0 || len(posts.requests) != 0 {
		t.Fatalf("recorded %+v and said %#v, want a thread this sink did not ask in left alone", recorded, posts.requests)
	}
}

// A reply in a work item's thread is still a directive against that item. The
// two halves share a connection and must not have become one behavior: the
// decision tier is a second receiver rather than a replacement.
func TestAReplyInTheChannelIsStillSteeredAgainstItsWorkItem(t *testing.T) {
	t.Parallel()

	sink, directives, _ := newDecidingSink(t, testOperator)
	sink.steering.handle(context.Background(), reply(testOperator, "prefer the smaller change here", "1750000001.000200"))

	recorded := onlyDirective(t, directives)
	if len(recorded.Scope) != 1 || recorded.Scope[0] != testItem {
		t.Fatalf("scope = %v, want the item whose thread it was said in", recorded.Scope)
	}
}

// The outbound half: a line that has stopped reaches every operator, as a brief
// top line carrying the ask with the context and the numbered options threaded
// under it.
func TestAStoppedLineIsPutToEveryOperatorAsADirectMessage(t *testing.T) {
	t.Parallel()

	asking := &Ask{
		Mark:    testStoppedMark,
		Stopped: testStopped,
		Since:   time.Date(2026, 8, 30, 2, 2, 0, 0, time.UTC),
		Ready:   7,
		Options: testOptions,
	}
	sink, _, posts := newSteeringSinkWithFeed(t, &fixedFeed{asking: asking}, testOperator, testStranger)
	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}

	// Every operator separately: a decision addressed to a room is one each of
	// them can assume somebody else is making.
	if len(posts.opened) != 2 || posts.opened[0] != testOperator || posts.opened[1] != testStranger {
		t.Fatalf("opened = %v, want a direct message with each operator", posts.opened)
	}
	if len(posts.requests) != 4 {
		t.Fatalf("posts = %#v, want a top line and a threaded ask for each of the two", posts.requests)
	}
	top, threaded := posts.requests[0], posts.requests[1]
	if top.Channel != "D"+testOperator || top.ThreadTS != "" {
		t.Fatalf("top line = %#v, want it opening the direct message with the operator", top)
	}
	// Brief, and carrying the ask: it is what a lock screen shows.
	if !strings.Contains(top.Text, "waiting on you") || !strings.Contains(top.Text, testStopped) {
		t.Fatalf("top line = %q, want it to say what stopped the line and that it waits on them", top.Text)
	}
	if threaded.Channel != top.Channel || threaded.ThreadTS != posts.timestamps[0] {
		t.Fatalf("threaded ask = %#v, want it under the top line rather than beside it", threaded)
	}
	for _, wanted := range []string{"1. " + testOptions[0], "2. " + testOptions[1], "2026-08-30T02:02:00Z", "Ready to pull: 7"} {
		if !strings.Contains(threaded.Text, wanted) {
			t.Fatalf("threaded ask = %q, want it to carry %q", threaded.Text, wanted)
		}
	}
}

// One message per person per state. The channel says it again every hour while
// it stands; a direct message repeated hourly is what gets an app muted, and a
// muted app is silence exactly where this had to be heard.
func TestOneStoppedStateIsPutToAnOperatorOnce(t *testing.T) {
	t.Parallel()

	asking := &Ask{Mark: testStoppedMark, Stopped: testStopped, Ready: 7, Options: testOptions}
	sink, _, posts := newSteeringSinkWithFeed(t, &fixedFeed{asking: asking}, testOperator)
	for round := 0; round < 3; round++ {
		if err := sink.pass(context.Background()); err != nil {
			t.Fatalf("pass() error = %v", err)
		}
	}

	if len(posts.opened) != 1 || len(posts.requests) != 2 {
		t.Fatalf("opened %v and posted %#v, want one operator asked once however many passes", posts.opened, posts.requests)
	}
	// A different state is a different question, so it is asked afresh rather
	// than swallowed by the first.
	asking.Mark = "hold:2026-08-30T09:00:00Z"
	asking.Stopped = "the operator is holding all harness activity"
	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	if len(posts.opened) != 2 {
		t.Fatalf("opened = %v, want a state that replaced the last one asked on its own", posts.opened)
	}
}

// A product that has granted nobody is asked nothing, which is what every
// workspace gets until somebody names themselves. Shipping this changes nothing
// for them.
func TestAProjectThatGrantedNobodyIsAskedNothing(t *testing.T) {
	t.Parallel()

	asking := &Ask{Mark: testStoppedMark, Stopped: testStopped, Ready: 7, Options: testOptions}
	sink, _, posts := newSteeringSinkWithFeed(t, &fixedFeed{asking: asking})
	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	if len(posts.opened) != 0 || len(posts.requests) != 0 {
		t.Fatalf("opened %v and posted %#v, want nobody asked where nobody holds direct-work", posts.opened, posts.requests)
	}
}

// Asking is an observation like everything else this sink does, so a workspace
// that will not carry a direct message costs that message and nothing else: the
// pass finishes, the channel is unaffected, and nothing is recorded as asked.
func TestAWorkspaceThatRefusesADirectMessageDoesNotFailThePass(t *testing.T) {
	t.Parallel()

	asking := &Ask{Mark: testStoppedMark, Stopped: testStopped, Ready: 7, Options: testOptions}
	sink, _, posts := newSteeringSinkWithFeed(t, &fixedFeed{asking: asking}, testOperator)
	posts.refuseDMs = "cannot_dm_bot"
	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}

	asked, err := sink.store.LoadDecisions()
	if err != nil {
		t.Fatalf("LoadDecisions() error = %v", err)
	}
	if asked.AskedOf(testStoppedMark, testOperator) {
		t.Fatal("the operator is recorded as asked, want a refused message to be asked again rather than remembered")
	}
}

// newDecidingSink is a sink that has already asked one operator to decide about
// a stopped line, which is the state every reply above arrives into: a reply is
// correlated through the direct message thread the ask was made in.
func newDecidingSink(t *testing.T, operators ...string) (*Sink, *runstate.DirectiveStore, *recordedPosts) {
	t.Helper()
	sink, directives, posts := newSteeringSink(t, operators...)
	decisions, err := sink.store.LoadDecisions()
	if err != nil {
		t.Fatalf("LoadDecisions() error = %v", err)
	}
	decisions.Record(testAsked())
	if err := sink.store.SaveDecisions(decisions); err != nil {
		t.Fatalf("SaveDecisions() error = %v", err)
	}
	return sink, directives, posts
}

// testAsked is the ask as it was put to the operator and remembered.
func testAsked() Decision {
	return Decision{
		Mark:     testStoppedMark,
		Member:   testOperator,
		Channel:  testDMChannel,
		ThreadTS: testDMThreadTS,
		Stopped:  testStopped,
		Options:  testOptions,
		AskedAt:  time.Date(2026, 8, 30, 3, 2, 0, 0, time.UTC),
	}
}
