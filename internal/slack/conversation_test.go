package slack

// What a message addressed to this app does now that there is a product manager
// behind the door, and what the three ways that can fail say instead of nothing.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The point of the whole item: an operator says something to this app that is
// not a question about the standing, and the product manager answers it — in the
// thread they said it in, addressed to them, in the product manager's own name.
func TestAMessageToTheAppIsAnsweredByTheProductManager(t *testing.T) {
	t.Parallel()

	const askedTS = "1750000001.000100"
	talker := &scriptedConversation{text: "The brief is thin on who this is for."}
	sink, posts := newConversingSink(t, talker)
	say(sink, topLevel(testOperator, "<@"+testApp+"> what is missing from the brief?", askedTS))

	answer := onlyPost(t, posts)
	if !strings.Contains(answer.Text, "The brief is thin on who this is for.") {
		t.Fatalf("answer = %q, want what the product manager said", answer.Text)
	}
	if !strings.HasPrefix(answer.Text, "<@"+testOperator+"> ") {
		t.Fatalf("answer = %q, want it addressed to whoever asked", answer.Text)
	}
	if answer.ThreadTS != askedTS {
		t.Fatalf("answer = %#v, want it hanging from the message that asked", answer)
	}
	// The words the operator typed reach the conversation without Slack's own
	// mention syntax wrapped around them.
	if said := talker.saidTo(); said != "what is missing from the brief?" {
		t.Fatalf("said = %q, want the operator's own words with the mention taken out", said)
	}
	// It is the product manager's answer, so it wears the product manager's name
	// rather than the harness's: a message in a persona's voice that the persona
	// did not say is attribution nobody can check, and so is the reverse.
	wantName := sink.appearance.Identity(notify.Persona(domain.RoleProductManager, "")).Name
	if answer.Username != wantName {
		t.Fatalf("username = %q, want the product manager's own name %q", answer.Username, wantName)
	}
}

// The standing is still the read model's and is still answered without a turn.
// It is the question an operator asks most, the derivation belongs to the read
// model, and spending a provider invocation on something already in this channel
// would be paying for an answer twice.
func TestAskingWhereThingsStandStillSpendsNoTurn(t *testing.T) {
	t.Parallel()

	talker := &scriptedConversation{text: "I would not say this."}
	sink, posts := newConversingSink(t, talker)
	say(sink, topLevel(testOperator, "<@"+testApp+"> what is running?", "1750000001.000100"))

	answer := onlyPost(t, posts)
	for _, line := range fourLines {
		if !strings.Contains(answer.Text, line) {
			t.Fatalf("answer = %q, want the read model's four lines and so the %q line", answer.Text, line)
		}
	}
	if talker.turns() != 0 {
		t.Fatalf("the product manager was asked %d time(s) for something the read model answers", talker.turns())
	}
}

// Talking to the product manager admits work, reorders the queue, and spends the
// operator's money, so it is held to the grant a thread reply is held to. A
// colleague the project recognizes and has granted nothing is told which grant
// they are missing, and nothing they said reaches the conversation.
func TestOnlyDirectWorkReachesTheProductManager(t *testing.T) {
	t.Parallel()

	talker := &scriptedConversation{text: "I would not say this."}
	sink, posts := newConversingSink(t, talker)
	recognize(sink, testColleague)
	say(sink, topLevel(testColleague, "<@"+testApp+"> retire the installer epic", "1750000001.000100"))

	answer := onlyPost(t, posts)
	if !strings.Contains(answer.Text, ungranted) {
		t.Fatalf("answer = %q, want the refusal naming the grant they are missing", answer.Text)
	}
	if talker.turns() != 0 {
		t.Fatalf("somebody without direct-work reached the product manager %d time(s)", talker.turns())
	}
	// And the same person still gets the standing, because answering is not
	// steering and those four lines are already in this channel.
	say(sink, topLevel(testColleague, "<@"+testApp+"> what is running?", "1750000002.000100"))
	if len(posts.requests) != 2 || !strings.Contains(posts.requests[1].Text, "Running:") {
		t.Fatalf("posts = %#v, want the standing still answered for somebody who may not steer", posts.requests)
	}
}

// A sink assembled without the conversation says outright that the work is
// driven from the terminal, which is exactly what this door did before there was
// a product manager behind it. Silence is the one answer it must never give.
func TestASinkWithNoConversationStillSaysSomething(t *testing.T) {
	t.Parallel()

	sink, _, posts := newSteeringSink(t, testOperator)
	say(sink, topLevel(testOperator, "<@"+testApp+"> what should we build next?", "1750000001.000100"))

	if answer := onlyPost(t, posts); !strings.Contains(answer.Text, unhandled) {
		t.Fatalf("answer = %q, want the sentence saying what this app cannot do yet", answer.Text)
	}
}

// The bound the reviewer's warning on ifd.45 asked for. A conversation that
// steers work can wait on an exhausted usage limit for hours, which is a choice
// somebody made at a terminal and is indistinguishable from a dead sink in a
// channel. The wait ends and the thread is told it ended.
func TestAConversationThatDoesNotAnswerInTimeIsSaidRatherThanWaitedOnForever(t *testing.T) {
	t.Parallel()

	talker := &scriptedConversation{block: true}
	sink, posts := newConversingSink(t, talker)
	sink.steering.deadline = 20 * time.Millisecond
	say(sink, topLevel(testOperator, "<@"+testApp+"> what should we build next?", "1750000001.000100"))

	answer := onlyPost(t, posts)
	if !strings.Contains(answer.Text, "I waited") {
		t.Fatalf("answer = %q, want the wait running out said in the thread", answer.Text)
	}
	if !strings.Contains(answer.Text, "yoyo chat") {
		t.Fatalf("answer = %q, want it to name where the same conversation carries on", answer.Text)
	}
	// The deadline is the client's own and is passed to the conversation, so what
	// it is waiting on stops rather than being abandoned still running.
	if !talker.cancelled() {
		t.Fatalf("the turn was left running after the client stopped waiting on it")
	}
}

// A provider with no capacity left is the case the whole bound exists for, and
// what the person who asked is owed is the reason rather than silence. The
// durable record the refusal leaves reaches this channel through the feed as
// well; this is the same fact said to the person waiting on it.
func TestATurnRefusedForCapacityIsSaidToWhoeverAskedFor(t *testing.T) {
	t.Parallel()

	talker := &scriptedConversation{err: errors.New("product manager backend failed: usage limit reached, resets at 18:00")}
	sink, posts := newConversingSink(t, talker)
	say(sink, topLevel(testOperator, "<@"+testApp+"> what should we build next?", "1750000001.000100"))

	answer := onlyPost(t, posts)
	if !strings.Contains(answer.Text, "usage limit reached") {
		t.Fatalf("answer = %q, want the provider's own reason said to whoever asked", answer.Text)
	}
}

// The conversation admits one holder, and the holder is usually the operator's
// own `yoyo chat`. That is a different thing from a failure and has a different
// answer, so it is told apart and said in words.
func TestAConversationHeldElsewhereIsSaidAsHeldRatherThanAsAFailure(t *testing.T) {
	t.Parallel()

	talker := &scriptedConversation{err: fmt.Errorf("the product-manager conversation is %w", runstate.ErrConversationHeld)}
	sink, posts := newConversingSink(t, talker)
	say(sink, topLevel(testOperator, "<@"+testApp+"> what should we build next?", "1750000001.000100"))

	if answer := onlyPost(t, posts); !strings.Contains(answer.Text, conversationHeld) {
		t.Fatalf("answer = %q, want the conversation being held elsewhere said as itself", answer.Text)
	}
}

// One turn at a time, and the second asker told so. The durable conversation
// would refuse the second turn anyway; this refuses it a moment earlier and with
// somebody actually told, rather than a message that vanishes.
func TestASecondMessageWhileATurnIsRunningIsToldSoRatherThanDropped(t *testing.T) {
	t.Parallel()

	talker := &scriptedConversation{
		text:    "one at a time",
		hold:    make(chan struct{}),
		running: make(chan struct{}),
	}
	sink, posts := newConversingSink(t, talker)
	sink.steering.handle(context.Background(),
		topLevel(testOperator, "<@"+testApp+"> what should we build next?", "1750000001.000100"))
	<-talker.running
	sink.steering.handle(context.Background(),
		topLevel(testOperator, "<@"+testApp+"> and what after that?", "1750000002.000100"))
	close(talker.hold)
	sink.steering.settle()

	if len(posts.requests) != 2 {
		t.Fatalf("posts = %#v, want both messages answered", posts.requests)
	}
	if !strings.Contains(posts.requests[0].Text, conversationBusy) {
		t.Fatalf("answer = %q, want the second asker told a turn is already running", posts.requests[0].Text)
	}
	if talker.turns() != 1 {
		t.Fatalf("the conversation took %d turn(s) at once, want one", talker.turns())
	}
}

// Somebody who typed a mention and stopped meant to ask something, so they are
// prompted rather than told no and rather than costing a turn on an empty
// message the conversation would refuse anyway.
func TestAMentionWithNothingSaidIsPromptedRatherThanSentOn(t *testing.T) {
	t.Parallel()

	talker := &scriptedConversation{text: "I would not say this."}
	sink, posts := newConversingSink(t, talker)
	say(sink, topLevel(testOperator, "<@"+testApp+">", "1750000001.000100"))

	if answer := onlyPost(t, posts); !strings.Contains(answer.Text, nothingSaid) {
		t.Fatalf("answer = %q, want a prompt to say something", answer.Text)
	}
	if talker.turns() != 0 {
		t.Fatalf("an empty message was taken to the product manager %d time(s)", talker.turns())
	}
}

// A decision the harness carried out is not the product manager speaking, so it
// is not posted in the product manager's voice. No turn was spent and nothing
// said it.
func TestAHarnessAnswerIsNotPostedAsThoughThePersonaSaidIt(t *testing.T) {
	t.Parallel()

	talker := &scriptedConversation{text: "You decided 1 proposal(s):", harness: true}
	sink, posts := newConversingSink(t, talker)
	say(sink, topLevel(testOperator, "<@"+testApp+"> y", "1750000001.000100"))

	answer := onlyPost(t, posts)
	harnessName := sink.appearance.Identity(notify.Harness()).Name
	if answer.Username != harnessName {
		t.Fatalf("username = %q, want the harness's own name %q for something no persona said", answer.Username, harnessName)
	}
}

// A reply in one of this sink's own threads is untouched by any of this: it is
// still the directive path, and the conversation is not a second way into the
// work record.
func TestTheConversationDoorDoesNotChangeWhatAThreadReplyDoes(t *testing.T) {
	t.Parallel()

	talker := &scriptedConversation{text: "I would not say this."}
	sink, posts := newConversingSink(t, talker)
	sink.steering.handle(context.Background(), reply(testOperator, "do it the other way", "1750000001.000200"))
	sink.steering.settle()

	// This sink has no directive record, so the reply is refused in its thread
	// rather than silently read past: it was read, and somebody is owed the answer.
	if answer := onlyPost(t, posts); !strings.Contains(answer.Text, "without the directive record") {
		t.Fatalf("answer = %q, want a reply that steers nothing said so in its thread", answer.Text)
	}
	if talker.turns() != 0 {
		t.Fatalf("a thread reply reached the product manager %d time(s); it is the directive path", talker.turns())
	}
}

// say drives one envelope through the door and waits for whatever it set off, so
// a test reads the answer rather than racing it.
func say(sink *Sink, envelope socketEnvelope) {
	sink.steering.handle(context.Background(), envelope)
	sink.steering.settle()
}

// newConversingSink is a sink with the product manager behind the door and
// nowhere to record a directive, which is the assembly this file is about: the
// two halves of the inbound side are separate, and either is enough to read what
// arrives.
func newConversingSink(t *testing.T, talker Conversation) (*Sink, *recordedPosts) {
	t.Helper()

	root := t.TempDir()
	store, err := NewStore(root, testProduct)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	posts := &recordedPosts{}
	sink, err := New(Options{
		Channel:      "C1",
		Store:        store,
		API:          newTestAPI(t, posts.handle),
		Feed:         &fixedFeed{},
		Conversation: talker,
		Operators:    []string{testOperator},
		Recognized:   []string{testOperator},
		Contacts:     []string{testContact},
		Standing:     &readmodel.Sources{},
		Log:          func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	sink.pace.sleep = func(context.Context, time.Duration) error { return nil }
	// The work item's thread, so a reply into one of this sink's own threads is
	// the thread reply it is rather than a message in somebody else's thread.
	threads, err := store.LoadThreads()
	if err != nil {
		t.Fatalf("LoadThreads() error = %v", err)
	}
	topic, err := notify.WorkItem(testItem)
	if err != nil {
		t.Fatalf("address a work item: %v", err)
	}
	threads.Record(topic.Key(), Thread{Channel: "C1", ThreadTS: testThreadTS})
	if err := store.SaveThreads(threads); err != nil {
		t.Fatalf("SaveThreads() error = %v", err)
	}
	return sink, posts
}

// scriptedConversation is one answer, or one refusal, or a turn that never
// finishes — which is the whole of what the door has to tell apart.
type scriptedConversation struct {
	text    string
	harness bool
	err     error
	// block is a turn that waits for its context to end, which is what the
	// deadline is tested against.
	block bool
	// hold is a turn that waits to be let go, and running is closed as it starts,
	// so a second message arrives while this one is genuinely still under way
	// rather than after a sleep somebody guessed at.
	hold    chan struct{}
	running chan struct{}

	mu    sync.Mutex
	said  []string
	stops int
}

func (c *scriptedConversation) Say(ctx context.Context, said string) (Answer, error) {
	c.mu.Lock()
	c.said = append(c.said, said)
	c.mu.Unlock()
	if c.running != nil {
		close(c.running)
	}
	switch {
	case c.block:
		<-ctx.Done()
		c.mu.Lock()
		c.stops++
		c.mu.Unlock()
		return Answer{ConversationID: testConversation}, ctx.Err()
	case c.hold != nil:
		<-c.hold
	}
	if c.err != nil {
		return Answer{ConversationID: testConversation}, c.err
	}
	return Answer{Text: c.text, Harness: c.harness, ConversationID: testConversation, Turns: 1}, nil
}

func (c *scriptedConversation) turns() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.said)
}

func (c *scriptedConversation) saidTo() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.said) == 0 {
		return ""
	}
	return c.said[0]
}

func (c *scriptedConversation) cancelled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stops > 0
}

const testConversation = "chat-0123456789abcdef0123456789abcdef"
