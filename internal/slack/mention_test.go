package slack

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// The four labels the read model prints, which is what a status answer has to
// carry. They are asserted as the labels rather than as whole lines because what
// each line holds is the read model's and is tested there; what is tested here is
// that the answer is that rendering rather than one this surface assembled.
var fourLines = []string{"Running:", "Working:", "Not startable:", "Needs a human:"}

// The defect this exists for: three questions at the top of the channel, three
// silences. Every one of them is answered now — the two that ask where things
// stand with the four lines, and the one that asks for something else with the
// sentence saying what this app cannot do yet. Nothing sits silent.
func TestTheThreeTopLevelQuestionsAreEachAnsweredRatherThanIgnored(t *testing.T) {
	t.Parallel()

	sink, directives, posts := newSteeringSink(t, testOperator)
	for _, said := range []struct {
		text string
		ts   string
	}{
		{text: "<@" + testApp + "> what is running?", ts: "1750000001.000100"},
		{text: "<@" + testApp + "> what's running right now", ts: "1750000002.000100"},
		{text: "<@" + testApp + "> please promote the branch", ts: "1750000003.000100"},
	} {
		sink.steering.handle(context.Background(), topLevel(testOperator, said.text, said.ts))
	}

	if len(posts.requests) != 3 {
		t.Fatalf("posts = %#v, want one answer to each of the three questions", posts.requests)
	}
	for index, answer := range posts.requests[:2] {
		for _, line := range fourLines {
			if !strings.Contains(answer.Text, line) {
				t.Fatalf("answer %d = %q, want the four lines and so the %q line", index, answer.Text, line)
			}
		}
	}
	if !strings.Contains(posts.requests[2].Text, unhandled) {
		t.Fatalf("answer = %q, want the one sentence saying what this app cannot do yet", posts.requests[2].Text)
	}
	// Answering is not steering: the top of the channel has no thread and so no
	// item to scope a directive to, and nothing here writes the record.
	if recorded, err := directives.List(); err != nil {
		t.Fatalf("List() error = %v", err)
	} else if len(recorded) != 0 {
		t.Fatalf("recorded = %+v, want a question at the top of the channel to record nothing", recorded)
	}
}

// The answer hangs from the message that asked and tags whoever asked, so a
// question and its answer read as one exchange and the person who asked is told
// rather than left to come back and look.
func TestAnAnsweredMentionHangsFromTheMessageThatAskedAndTagsThem(t *testing.T) {
	t.Parallel()

	const askedTS = "1750000001.000100"
	sink, _, posts := newSteeringSink(t, testOperator)
	sink.steering.handle(context.Background(), topLevel(testOperator, "<@"+testApp+"> status", askedTS))

	answer := onlyPost(t, posts)
	if answer.ThreadTS != askedTS {
		t.Fatalf("answer = %#v, want it hanging from the message that asked", answer)
	}
	if !strings.HasPrefix(answer.Text, "<@"+testOperator+"> ") {
		t.Fatalf("answer = %q, want it to tag whoever asked", answer.Text)
	}
	if !strings.Contains(answer.Text, standingLead) {
		t.Fatalf("answer = %q, want the line saying what the four lines are", answer.Text)
	}
}

// A message at the top of the channel that says nothing to this app is left
// exactly as alone as it always was. The exception is for messages addressed to
// the app, and a reporting channel that read every conversation in it as an
// instruction would be a participant nobody invited.
func TestAMessageThatDoesNotAddressTheAppIsLeftAlone(t *testing.T) {
	t.Parallel()

	sink, _, posts := newSteeringSink(t, testOperator)
	sink.steering.handle(context.Background(),
		topLevel(testOperator, "what is running, do you think?", "1750000001.000100"))
	sink.steering.handle(context.Background(),
		topLevel(testOperator, "<@U0SOMEBODYELSE> what is running?", "1750000002.000100"))

	if len(posts.requests) != 0 {
		t.Fatalf("posts = %#v, want silence for what was not said to this app", posts.requests)
	}
}

// A reply in a thread this sink never opened is the same case as the top of the
// channel: nothing is recorded from it, and one that addresses this app is still
// answered — in that thread, where it was asked.
func TestAMentionInAThreadThisSinkNeverOpenedIsAnsweredThere(t *testing.T) {
	t.Parallel()

	const foreignThread = "1749000000.000100"
	sink, directives, posts := newSteeringSink(t, testOperator)
	sink.steering.handle(context.Background(), envelopeFor(map[string]any{
		"type":      "message",
		"user":      testOperator,
		"text":      "<@" + testApp + "> where do things stand",
		"ts":        "1750000001.000100",
		"thread_ts": foreignThread,
		"channel":   "C1",
	}))

	answer := onlyPost(t, posts)
	if answer.ThreadTS != foreignThread {
		t.Fatalf("answer = %#v, want it in the thread the question was asked in", answer)
	}
	if !strings.Contains(answer.Text, "Running:") {
		t.Fatalf("answer = %q, want the four lines", answer.Text)
	}
	if recorded, err := directives.List(); err != nil {
		t.Fatalf("List() error = %v", err)
	} else if len(recorded) != 0 {
		t.Fatalf("recorded = %+v, want nothing recorded from a thread this sink did not open", recorded)
	}
}

// A reply in one of this sink's own threads is untouched by any of this: it
// still goes to the inbound half, and the door for messages outside those
// threads is not a second way in. Naming the app in one changes nothing about
// which path reads it.
func TestAReplyInThisSinksOwnThreadStillRecordsADirective(t *testing.T) {
	t.Parallel()

	sink, directives, _ := newSteeringSink(t, testOperator)
	sink.steering.handle(context.Background(),
		reply(testOperator, "<@"+testApp+"> prefer the smaller change here", "1750000001.000200"))

	recorded := onlyDirective(t, directives)
	if !recorded.Affects(testItem) {
		t.Fatalf("recorded = %+v, want a directive against the thread's item", recorded)
	}
}

// The same reply asking rather than telling gets the inbound half's answer to a
// question and not the standing block, because the door outside these threads
// answers one question and the thread it was asked in is not outside them.
func TestAQuestionInThisSinksOwnThreadIsNotAnsweredByTheMentionDoor(t *testing.T) {
	t.Parallel()

	sink, directives, posts := newSteeringSink(t, testOperator)
	sink.steering.handle(context.Background(),
		reply(testOperator, "<@"+testApp+"> what is running?", "1750000001.000200"))

	if recorded, err := directives.List(); err != nil {
		t.Fatalf("List() error = %v", err)
	} else if len(recorded) != 0 {
		t.Fatalf("recorded = %+v, want a question kept out of the directive record", recorded)
	}
	answer := onlyPost(t, posts)
	if strings.Contains(answer.Text, "Running:") {
		t.Fatalf("answer = %q, want the inbound half's answer rather than the four lines", answer.Text)
	}
	if !strings.Contains(answer.Text, "written down as a question") {
		t.Fatalf("answer = %q, want it to say the question was recorded as one", answer.Text)
	}
}

// Slack redelivers an envelope whose acknowledgment did not reach it. A question
// answered twice for one asking is the channel repeating itself.
func TestARedeliveredMentionIsAnsweredOnce(t *testing.T) {
	t.Parallel()

	sink, _, posts := newSteeringSink(t, testOperator)
	envelope := topLevel(testOperator, "<@"+testApp+"> status", "1750000001.000100")
	sink.steering.handle(context.Background(), envelope)
	sink.steering.handle(context.Background(), envelope)

	if len(posts.requests) != 1 {
		t.Fatalf("posts = %#v, want one answer to one question", posts.requests)
	}
}

// A sink assembled without the read model says so. An empty block of four lines
// would be a confident nothing assembled from a source nobody wired, which is
// the one answer this must never give.
func TestASinkWithNoReadModelSaysSoRatherThanAnsweringWithNothing(t *testing.T) {
	t.Parallel()

	sink, _, posts := newSteeringSink(t, testOperator)
	sink.sources = nil
	sink.steering.handle(context.Background(), topLevel(testOperator, "<@"+testApp+"> status", "1750000001.000100"))

	if answer := onlyPost(t, posts); !strings.Contains(answer.Text, unanswerable) {
		t.Fatalf("answer = %q, want a stated absence rather than an empty standing", answer.Text)
	}
}

// The app's own answers arrive back on the same connection, and reading one as a
// question would be the harness asking itself where it stands forever.
func TestTheAppsOwnMessageIsNotReadAsAQuestion(t *testing.T) {
	t.Parallel()

	sink, _, posts := newSteeringSink(t, testOperator)
	sink.steering.handle(context.Background(), envelopeFor(map[string]any{
		"type":    "message",
		"bot_id":  "B0YOYODYNE",
		"text":    "<@" + testApp + "> status",
		"ts":      "1750000001.000100",
		"channel": "C1",
	}))

	if len(posts.requests) != 0 {
		t.Fatalf("posts = %#v, want the app's own message read past", posts.requests)
	}
}

// Which questions get the four lines. The list is stated rather than inferred,
// and the two mistakes it can make are not the same size: a phrasing it misses
// still gets an answer, and one it matches by accident gets something true.
func TestWhichQuestionsAskWhereThingsStand(t *testing.T) {
	t.Parallel()

	for _, asked := range []string{
		"status",
		"status?",
		"what is running",
		"what's running right now",
		"What Are You Doing?",
		"where do things stand this morning",
		"sitrep please",
	} {
		if !asksForStanding(asked) {
			t.Errorf("asksForStanding(%q) = false, want a question about where things stand", asked)
		}
	}
	for _, asked := range []string{
		"promote the branch",
		"hold intake until tomorrow",
		"who wrote this",
		"",
	} {
		if asksForStanding(asked) {
			t.Errorf("asksForStanding(%q) = true, want everything else", asked)
		}
	}
}

// What is read is the words somebody typed rather than the member ids their
// client wrapped the names in.
func TestMentionsAreTakenOutBeforeWhatWasSaidIsRead(t *testing.T) {
	t.Parallel()

	for said, want := range map[string]string{
		"<@" + testApp + "> what is running":       "what is running",
		"hey <@" + testApp + "> status please":     "hey   status please",
		"<@" + testApp + ">":                       "",
		"<@" + testApp + " unclosed what is going": "",
	} {
		if got := withoutMentions(said); got != want {
			t.Errorf("withoutMentions(%q) = %q, want %q", said, got, want)
		}
	}
}

// Nothing an operator sends to the app goes unrecorded, and the record carries
// what they said rather than only that somebody said something. It is written
// before the answer is posted, so a workspace that refuses to carry the answer
// costs the reply and not the account of having been asked.
func TestWhatWasAskedIsWrittenDownEvenWhenTheAnswerCannotBePosted(t *testing.T) {
	t.Parallel()

	sink, _, posts := newSteeringSink(t, testOperator)
	var noted []string
	sink.log = func(format string, args ...any) { noted = append(noted, fmt.Sprintf(format, args...)) }
	// This workspace takes one message and refuses everything after it.
	posts.allow = 1
	sink.steering.handle(context.Background(),
		topLevel(testOperator, "<@"+testApp+"> status", "1750000001.000100"))
	sink.steering.handle(context.Background(),
		topLevel(testOperator, "<@"+testApp+"> hold intake until I say otherwise", "1750000002.000100"))

	if len(posts.requests) != 1 {
		t.Fatalf("posts = %#v, want the second answer refused by the workspace", posts.requests)
	}
	log := strings.Join(noted, "\n")
	for _, said := range []string{"status", "hold intake until I say otherwise"} {
		if !strings.Contains(log, said) {
			t.Fatalf("log =\n%s\nwant it to carry %q, which is what the operator said", log, said)
		}
	}
	if !strings.Contains(log, testOperator) {
		t.Fatalf("log =\n%s\nwant it to name who said it", log)
	}
	if !strings.Contains(log, "could not be posted") {
		t.Fatalf("log =\n%s\nwant the refused answer said as well as the question", log)
	}
}

// Slack writes a mention two ways, and the older labelled form is the same
// member id addressing the same app. Missing it would be silence in exactly the
// place this exists to remove silence from.
func TestTheLabelledMentionFormAddressesTheAppToo(t *testing.T) {
	t.Parallel()

	sink, _, posts := newSteeringSink(t, testOperator)
	sink.steering.handle(context.Background(),
		topLevel(testOperator, "<@"+testApp+"|yoyodyne> status", "1750000001.000100"))

	if answer := onlyPost(t, posts); !strings.Contains(answer.Text, "Running:") {
		t.Fatalf("answer = %q, want the four lines", answer.Text)
	}
}

// Which texts name this app. A member id is a prefix of longer ids, so what
// terminates the mention is the whole of the test: matching without it would
// answer messages addressed to somebody else entirely.
func TestWhichTextsAddressThisApp(t *testing.T) {
	t.Parallel()

	for said, want := range map[string]bool{
		"<@" + testApp + "> status":          true,
		"<@" + testApp + "|yoyodyne> status": true,
		"hey <@" + testApp + "> status":      true,
		"<@U0SOMEBODYELSE> status":           false,
		"<@" + testApp + "EXTRA> status":     false,
		"<@" + testApp:                       false,
		"status":                             false,
	} {
		if got := addresses(said, testApp); got != want {
			t.Errorf("addresses(%q) = %v, want %v", said, got, want)
		}
	}
}

// Answering is not steering, so it is not gated on the allow-list that decides
// whose replies record directives. Somebody without `direct-work` who asks the
// app where things stand is told, because the four lines are already posted to
// this channel by the heartbeat and refusing them would be the app staying
// silent about something already on the screen.
//
// It is pinned by a test so a later tightening of who may steer cannot quietly
// put the silence back.
func TestSomebodyWithoutDirectWorkStillGetsAnAnswer(t *testing.T) {
	t.Parallel()

	sink, directives, posts := newSteeringSink(t, testOperator)
	sink.steering.handle(context.Background(),
		topLevel(testStranger, "<@"+testApp+"> what is running?", "1750000001.000100"))

	answer := onlyPost(t, posts)
	if !strings.Contains(answer.Text, "Running:") {
		t.Fatalf("answer = %q, want the four lines for somebody the project has not granted direct-work", answer.Text)
	}
	if !strings.HasPrefix(answer.Text, "<@"+testStranger+"> ") {
		t.Fatalf("answer = %q, want it addressed to whoever asked", answer.Text)
	}
	if recorded, err := directives.List(); err != nil {
		t.Fatalf("List() error = %v", err)
	} else if len(recorded) != 0 {
		t.Fatalf("recorded = %+v, want an answer to record nothing whoever asked", recorded)
	}
}

// A standing too large for one message is cut with a pointer at where the whole
// of it is printed. Slack refuses an oversized message every time it is tried
// rather than once, so this is the difference between a long answer and none.
func TestAnAnswerTooLargeForOneMessageIsCutWithSomewhereToRead(t *testing.T) {
	t.Parallel()

	bounded := boundAnswer(strings.Repeat("é", maxTextBytes))
	if len(bounded) > maxTextBytes {
		t.Fatalf("bounded to %d bytes, want no more than %d", len(bounded), maxTextBytes)
	}
	if !utf8.ValidString(bounded) {
		t.Fatalf("bounded = %q, want it cut between runes rather than through one", bounded)
	}
	if !strings.Contains(bounded, "yoyo status") {
		t.Fatalf("bounded = %q, want it to say where the whole of it is printed", bounded)
	}
	if short := "the whole thing"; boundAnswer(short) != short {
		t.Fatalf("boundAnswer(%q) = %q, want an answer that fits left alone", short, boundAnswer(short))
	}
}

// topLevel is one person typing at the top of the channel rather than in any
// thread, which is where the app was asked three questions and answered none.
func topLevel(user, text, ts string) socketEnvelope {
	return envelopeFor(map[string]any{
		"type":    "message",
		"user":    user,
		"text":    text,
		"ts":      ts,
		"channel": "C1",
	})
}
