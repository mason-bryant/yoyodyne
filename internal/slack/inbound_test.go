package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The operator this project recognizes, and the thread every reply below arrives
// in: one work item, whose thread this sink opened.
const (
	testOperator = "U0OPERATOR"
	testStranger = "U0STRANGER"
	testItem     = "yoyodyne-ifd.68.4"
	testThreadTS = "1750000000.000100"
)

// A plain reply is an operational directive, recorded where every run reads it
// and scoped to the item whose thread it was said in. That is the whole of the
// inbound half: not a new kind of instruction, but the existing record with a
// second way in.
func TestAPlainReplyRecordsAnOperationalDirectiveAgainstTheThreadsItem(t *testing.T) {
	t.Parallel()

	sink, directives, posts := newSteeringSink(t, testOperator)
	sink.steering.handle(context.Background(), reply(testOperator, "prefer the smaller change here", "1750000001.000200"))

	recorded := onlyDirective(t, directives)
	if recorded.Kind != directive.KindOperational || recorded.Pauses() {
		t.Fatalf("recorded = %+v, want an operational directive that pauses nothing", recorded)
	}
	if recorded.Text != "prefer the smaller change here" {
		t.Fatalf("text = %q, want the operator's own words", recorded.Text)
	}
	if len(recorded.Scope) != 1 || recorded.Scope[0] != testItem {
		t.Fatalf("scope = %v, want the item whose thread it was said in", recorded.Scope)
	}
	// A directive recorded here is one the run pipeline finds by asking the same
	// question it asks of every other directive.
	if !recorded.Affects(testItem) {
		t.Fatalf("recorded = %+v, want it to reach the item it was scoped to", recorded)
	}
	if recorded.ReceivedBy != domain.RoleProductManager {
		t.Fatalf("received by %q, want the product manager where the reply named nobody", recorded.ReceivedBy)
	}

	answer := onlyPost(t, posts)
	if answer.ThreadTS != testThreadTS {
		t.Fatalf("answer = %#v, want it in the thread the reply arrived in", answer)
	}
	if !strings.Contains(answer.Text, recorded.ID) {
		t.Fatalf("answer = %q, want it to name the directive it recorded", answer.Text)
	}
	if answer.ReplyBroadcast {
		t.Fatalf("answer = %#v, want a directive that stopped nothing to stay in its thread", answer)
	}
}

// An answer is for the person who typed the reply, so it tags them rather than
// relying on them to come back and find the thread. Their own message wears what
// became of it as well: heard while it is being decided, then settled.
func TestAnAnsweredReplyTagsWhoWroteItAndTheirMessageWearsWhatBecameOfIt(t *testing.T) {
	t.Parallel()

	const replyTS = "1750000001.000200"
	sink, directives, posts := newSteeringSink(t, testOperator)
	sink.steering.handle(context.Background(), reply(testOperator, "prefer the smaller change here", replyTS))

	recorded := onlyDirective(t, directives)
	answer := onlyPost(t, posts)
	if !strings.HasPrefix(answer.Text, "<@"+testOperator+"> ") {
		t.Fatalf("answer = %q, want it to tag the operator who wrote the reply", answer.Text)
	}
	if !strings.Contains(answer.Text, recorded.ID) {
		t.Fatalf("answer = %q, want the tag in front of what was recorded rather than instead of it", answer.Text)
	}

	// Heard first, on the reply itself, and then the disposition — which sweeps
	// the marks that are no longer true rather than the one a record happens to
	// name, so a sink killed mid-change settles.
	wantMarks(t, posts.marks,
		mark{method: "reactions.add", ts: replyTS, name: notify.ReceiptUnderConsideration.Symbol()},
		mark{method: "reactions.remove", ts: replyTS, name: notify.ReceiptUnderConsideration.Symbol()},
		mark{method: "reactions.remove", ts: replyTS, name: notify.ReceiptRefused.Symbol()},
		mark{method: "reactions.add", ts: replyTS, name: notify.ReceiptSettled.Symbol()})
	if worn := posts.wearing[replyTS]; !worn[notify.ReceiptSettled.Symbol()] || len(worn) != 1 {
		t.Fatalf("the reply wears %#v, want the settled mark and nothing else", worn)
	}
}

// A reply that recorded nothing wears a mark of its own. The absence of one
// would be indistinguishable from a reply nobody read, which is the silence this
// whole path exists to end.
func TestARefusedReplyWearsARefusalRatherThanNothing(t *testing.T) {
	t.Parallel()

	const replyTS = "1750000001.000200"
	sink, _, posts := newSteeringSink(t, testOperator)
	sink.steering.handle(context.Background(), reply(testStranger, "do it the other way", replyTS))

	if worn := posts.wearing[replyTS]; !worn[notify.ReceiptRefused.Symbol()] || len(worn) != 1 {
		t.Fatalf("the reply wears %#v, want the refusal mark and nothing else", worn)
	}
	if answer := onlyPost(t, posts); !strings.HasPrefix(answer.Text, "<@"+testStranger+"> ") {
		t.Fatalf("answer = %q, want a refusal addressed to whoever it refuses", answer.Text)
	}
}

// A settlement said in the thread by the reply that made it is not said a second
// time by the delivery pass reading the same record. The two halves post from
// different goroutines and remember what they have said in different places, so
// the connection writes down that it answered.
func TestASettlementMadeFromAThreadIsNotSaidTwice(t *testing.T) {
	t.Parallel()

	sink, directives, _ := newSteeringSink(t, testOperator)
	sink.steering.handle(context.Background(),
		reply(testOperator, "ambiguous: which branch", "1750000001.000200"))
	recorded := onlyDirective(t, directives)

	steers, err := sink.store.LoadSteers()
	if err != nil {
		t.Fatalf("LoadSteers() error = %v", err)
	}
	steer, found := steers.Lookup(recorded.ID)
	if !found || steer.Member != testOperator {
		t.Fatalf("steer for %s = %#v (found %t), want the thread and the member it was said by", recorded.ID, steer, found)
	}
	if steer.Said {
		t.Fatalf("steer = %#v, want an unsettled directive to be one nothing has answered yet", steer)
	}

	sink.steering.handle(context.Background(),
		reply(testOperator, "resolve "+recorded.ID[:16]+" the one already on the target branch", "1750000002.000300"))

	steers, err = sink.store.LoadSteers()
	if err != nil {
		t.Fatalf("LoadSteers() error = %v", err)
	}
	if steer, _ := steers.Lookup(recorded.ID); !steer.Said {
		t.Fatalf("steer = %#v, want the settlement marked as already said in the thread", steer)
	}
}

// Settling a directive from somewhere other than the thread that asked for it
// answers where it was settled, and leaves the thread that asked still owed what
// became of it. Marking it said there would be the sink concluding somebody had
// been told because somebody else was.
func TestASettlementSaidInAnotherThreadStillOwesTheThreadThatAsked(t *testing.T) {
	t.Parallel()

	sink, directives, _ := newSteeringSink(t, testOperator)
	sink.steering.handle(context.Background(),
		reply(testOperator, "ambiguous: which branch", "1750000001.000200"))
	recorded := onlyDirective(t, directives)

	// A second item, with a thread of its own: the operator settles it from
	// wherever they happen to be reading.
	threads, err := sink.store.LoadThreads()
	if err != nil {
		t.Fatalf("LoadThreads() error = %v", err)
	}
	elsewhere, err := notify.WorkItem("yoyodyne-ifd.68.20")
	if err != nil {
		t.Fatalf("address a work item: %v", err)
	}
	threads.Record(elsewhere.Key(), Thread{Channel: "C1", ThreadTS: "1750000003.000400"})
	if err := sink.store.SaveThreads(threads); err != nil {
		t.Fatalf("SaveThreads() error = %v", err)
	}

	sink.steering.handle(context.Background(), envelopeFor(map[string]any{
		"type": "message", "user": testOperator, "channel": "C1",
		"text": "resolve " + recorded.ID[:16] + " the one already on the target branch",
		"ts": "1750000004.000500", "thread_ts": "1750000003.000400",
	}))

	settled, err := directives.Load(recorded.ID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !settled.Resolved() {
		t.Fatalf("settled = %+v, want it settled from whichever thread said so", settled)
	}
	steers, err := sink.store.LoadSteers()
	if err != nil {
		t.Fatalf("LoadSteers() error = %v", err)
	}
	if steer, _ := steers.Lookup(recorded.ID); steer.Said {
		t.Fatalf("steer = %#v, want the thread that asked still owed what became of it", steer)
	}
}

// The pausing kinds are stated rather than inferred, and a stated one pauses the
// work exactly as one recorded at a terminal does. Nothing about arriving through
// a chat workspace makes it a weaker record.
func TestAStatedAmbiguousReplyPausesTheWorkItAffects(t *testing.T) {
	t.Parallel()

	sink, directives, posts := newSteeringSink(t, testOperator)
	sink.steering.handle(context.Background(),
		reply(testOperator, "ambiguous: which of the two branches did you mean", "1750000001.000200"))

	recorded := onlyDirective(t, directives)
	if recorded.Kind != directive.KindAmbiguous || !recorded.Pauses() {
		t.Fatalf("recorded = %+v, want an ambiguous directive that pauses the work", recorded)
	}
	if recorded.Unresolved != "which of the two branches did you mean" {
		t.Fatalf("unresolved = %q, want what the reply said is unresolved", recorded.Unresolved)
	}
	pausing, err := directives.Pausing(testItem)
	if err != nil {
		t.Fatalf("Pausing() error = %v", err)
	}
	if len(pausing) != 1 {
		t.Fatalf("Pausing(%q) = %v, want the run pipeline to find exactly this one", testItem, pausing)
	}

	// Work stopping is what somebody who has opened no threads most needs to see,
	// so the acknowledgment is shown in the channel as well as in the thread.
	answer := onlyPost(t, posts)
	if !answer.ReplyBroadcast {
		t.Fatalf("answer = %#v, want a paused work item said where nobody has to open a thread", answer)
	}
	if !strings.Contains(answer.Text, "which of the two branches did you mean") {
		t.Fatalf("answer = %q, want it to say what the work is waiting on", answer.Text)
	}
}

// An artifact directive names the document it changes and what has to be decided
// about it, and both are required — the same contract the command line holds one
// to, because it is the same record.
func TestAnArtifactReplyNamesTheDocumentAndWhatMustBeDecided(t *testing.T) {
	t.Parallel()

	sink, directives, _ := newSteeringSink(t, testOperator)
	sink.steering.handle(context.Background(),
		reply(testOperator, "artifact: slack-reporting-design the inbound half should refuse product threads", "1750000001.000200"))

	recorded := onlyDirective(t, directives)
	if recorded.Kind != directive.KindArtifact || recorded.Artifact != "slack-reporting-design" {
		t.Fatalf("recorded = %+v, want an artifact directive naming the design", recorded)
	}
	if recorded.Unresolved != "the inbound half should refuse product threads" {
		t.Fatalf("unresolved = %q, want what has to be decided about the document", recorded.Unresolved)
	}
	if !recorded.Pauses() {
		t.Fatalf("recorded = %+v, want work derived from a document being rewritten to wait", recorded)
	}
}

// A pause nobody can name a reason for is a pause nobody can lift, so a stated
// kind that says nothing is refused rather than recorded as something vaguer. The
// refusal says what to type, because the person reading it is in a chat client
// rather than at a terminal.
func TestAStatedKindThatSaysNothingUnresolvedIsRefused(t *testing.T) {
	t.Parallel()

	for _, said := range []string{"ambiguous:", "artifact:", "artifact: slack-reporting-design", "resolve", "resolve directive-0"} {
		sink, directives, posts := newSteeringSink(t, testOperator)
		sink.steering.handle(context.Background(), reply(testOperator, said, "1750000001.000200"))

		recorded, err := directives.List()
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		if len(recorded) != 0 {
			t.Fatalf("%q recorded %+v, want nothing recorded from a reply that stated no reason", said, recorded)
		}
		answer := onlyPost(t, posts)
		if !strings.Contains(answer.Text, "Nothing was recorded") {
			t.Fatalf("%q was answered %q, want a refusal saying nothing was recorded", said, answer.Text)
		}
	}
}

// Authority defaults closed, and a reply from somebody who does not hold it is
// answered rather than dropped: a channel that silently ignores some people looks
// broken rather than closed.
func TestAReplyFromSomebodyWithoutDirectWorkRecordsNothingAndSaysSo(t *testing.T) {
	t.Parallel()

	sink, directives, posts := newSteeringSink(t, testOperator)
	sink.steering.handle(context.Background(),
		reply(testStranger, "ambiguous: stop what you are doing", "1750000001.000200"))

	recorded, err := directives.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 0 {
		t.Fatalf("recorded %+v, want nothing recorded for somebody the project granted nothing", recorded)
	}
	answer := onlyPost(t, posts)
	if !strings.Contains(answer.Text, "direct-work") {
		t.Fatalf("answer = %q, want a refusal naming the grant it is missing", answer.Text)
	}
}

// A product that has named nobody is one no reply steers, which is what every
// workspace gets until somebody adds themselves. Shipping the inbound half
// changes nothing for them.
func TestAProjectThatGrantedNobodyIsSteeredByNobody(t *testing.T) {
	t.Parallel()

	sink, directives, _ := newSteeringSink(t)
	sink.steering.handle(context.Background(), reply(testOperator, "do it the other way", "1750000001.000200"))

	recorded, err := directives.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 0 {
		t.Fatalf("recorded %+v, want an empty allow-list to act on nothing", recorded)
	}
	if !strings.Contains(sink.steer(), "not acted on") {
		t.Fatalf("steer() = %q, want a sink that says replies are acknowledged and not acted on", sink.steer())
	}
}

// The receiving role is what the reply addressed, and it is attribution rather
// than routing: the record still reaches every run of the item it is scoped to.
func TestAMentionedPersonaIsRecordedAsTheReceiver(t *testing.T) {
	t.Parallel()

	sink, directives, _ := newSteeringSink(t, testOperator)
	sink.steering.handle(context.Background(), reply(testOperator, "@reviewer judge this against the criteria only", "1750000001.000200"))

	recorded := onlyDirective(t, directives)
	if recorded.ReceivedBy != domain.RoleReviewer {
		t.Fatalf("received by %q, want the persona the reply addressed", recorded.ReceivedBy)
	}
	if recorded.Text != "judge this against the criteria only" {
		t.Fatalf("text = %q, want the words with the address taken off", recorded.Text)
	}
	if !recorded.Affects(testItem) {
		t.Fatalf("recorded = %+v, want a directive that reaches the work whoever it names", recorded)
	}
}

// Settling one from a thread is the same act as settling it at a terminal, down
// to the unique-prefix rule, and it is what lifts the pause the reply before it
// placed.
func TestAResolveReplySettlesTheDirectiveAndLiftsThePause(t *testing.T) {
	t.Parallel()

	sink, directives, posts := newSteeringSink(t, testOperator)
	sink.steering.handle(context.Background(),
		reply(testOperator, "ambiguous: which branch", "1750000001.000200"))
	recorded := onlyDirective(t, directives)

	posts.requests = nil
	sink.steering.handle(context.Background(),
		reply(testOperator, "resolve "+recorded.ID[:16]+" the one already on the target branch", "1750000002.000300"))

	settled, err := directives.Load(recorded.ID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !settled.Resolved() || settled.Resolution != "the one already on the target branch" {
		t.Fatalf("settled = %+v, want it resolved with what the reply said", settled)
	}
	pausing, err := directives.Pausing(testItem)
	if err != nil {
		t.Fatalf("Pausing() error = %v", err)
	}
	if len(pausing) != 0 {
		t.Fatalf("Pausing(%q) = %v, want the pause lifted", testItem, pausing)
	}
	if answer := onlyPost(t, posts); !strings.Contains(answer.Text, recorded.ID) {
		t.Fatalf("answer = %q, want it to name what was settled", answer.Text)
	}
}

// The sink's own messages arrive back on the same connection it posts through.
// Reading one as an instruction would be the harness directing itself, and an
// acknowledgment answered by an acknowledgment is a loop, so anything that is not
// a person typing is not read at all.
func TestNothingTheSinkOrAnotherAppSaidIsReadAsADirective(t *testing.T) {
	t.Parallel()

	for name, event := range map[string]map[string]any{
		"this sink's own post": {
			"type": "message", "bot_id": "B0YOYODYNE", "user": testOperator,
			"text": "ambiguous: anything",
			"ts":   "1750000001.000200", "thread_ts": testThreadTS, "channel": "C1",
		},
		"an edited message": {
			"type": "message", "subtype": "message_changed", "user": testOperator,
			"text": "ambiguous: anything", "ts": "1750000001.000200", "thread_ts": testThreadTS, "channel": "C1",
		},
		"a message in another channel": {
			"type": "message", "user": testOperator, "text": "ambiguous: anything",
			"ts": "1750000001.000200", "thread_ts": testThreadTS, "channel": "C2",
		},
		"a message in no thread at all": {
			"type": "message", "user": testOperator, "text": "ambiguous: anything",
			"ts": "1750000001.000200", "channel": "C1",
		},
		"a thread's own opening message": {
			"type": "message", "user": testOperator, "text": "ambiguous: anything",
			"ts": testThreadTS, "thread_ts": testThreadTS, "channel": "C1",
		},
	} {
		sink, directives, posts := newSteeringSink(t, testOperator)
		sink.steering.handle(context.Background(), envelopeFor(event))

		recorded, err := directives.List()
		if err != nil {
			t.Fatalf("List() error = %v", err)
		}
		if len(recorded) != 0 {
			t.Fatalf("%s recorded %+v, want it read past", name, recorded)
		}
		if len(posts.requests) != 0 {
			t.Fatalf("%s was answered %#v, want nothing said about it", name, posts.requests)
		}
	}
}

// A reply in a thread this sink never opened is somebody's own conversation in
// the channel. Answering it would make the app talk over people, and there is no
// topic to scope anything to.
func TestAReplyInAThreadTheSinkNeverOpenedIsLeftAlone(t *testing.T) {
	t.Parallel()

	sink, directives, posts := newSteeringSink(t, testOperator)
	sink.steering.handle(context.Background(),
		envelopeFor(map[string]any{
			"type": "message", "user": testOperator, "text": "ambiguous: anything",
			"ts": "1750000001.000200", "thread_ts": "1749999999.000000", "channel": "C1",
		}))

	recorded, err := directives.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 0 || len(posts.requests) != 0 {
		t.Fatalf("recorded %+v and said %#v, want a thread that is not this sink's left alone", recorded, posts.requests)
	}
}

// A directive from a thread is scoped to the thread's work item, so a thread that
// is about something else has nothing to scope one to. Recording it unscoped
// would reach every item in the product from a reply about none of them.
func TestAReplyInAThreadThatIsNotAWorkItemsIsRefused(t *testing.T) {
	t.Parallel()

	sink, directives, posts := newSteeringSink(t, testOperator)
	threads, err := sink.store.LoadThreads()
	if err != nil {
		t.Fatalf("LoadThreads() error = %v", err)
	}
	threads.Record("exchange:exchange-7f3a", Thread{Channel: "C1", ThreadTS: "1750000003.000400"})
	if err := sink.store.SaveThreads(threads); err != nil {
		t.Fatalf("SaveThreads() error = %v", err)
	}

	sink.steering.handle(context.Background(),
		envelopeFor(map[string]any{
			"type": "message", "user": testOperator, "text": "do it the other way",
			"ts": "1750000004.000500", "thread_ts": "1750000003.000400", "channel": "C1",
		}))

	recorded, err := directives.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 0 {
		t.Fatalf("recorded %+v, want nothing scoped to an item the thread is not about", recorded)
	}
	if answer := onlyPost(t, posts); !strings.Contains(answer.Text, "not a work item") {
		t.Fatalf("answer = %q, want a refusal saying what the thread is not", answer.Text)
	}
}

// Slack redelivers an envelope whose acknowledgment did not reach it. That is
// Slack repeating itself rather than an operator saying something twice, and two
// records for one instruction is two things somebody has to resolve.
func TestARedeliveredReplyIsActedOnOnce(t *testing.T) {
	t.Parallel()

	sink, directives, posts := newSteeringSink(t, testOperator)
	redelivered := reply(testOperator, "ambiguous: which branch", "1750000001.000200")
	sink.steering.handle(context.Background(), redelivered)
	sink.steering.handle(context.Background(), redelivered)

	recorded, err := directives.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 1 {
		t.Fatalf("recorded %d directives, want one instruction to be one record", len(recorded))
	}
	if len(posts.requests) != 1 {
		t.Fatalf("answers = %#v, want one acknowledgment", posts.requests)
	}
}

// Both of a sink's goroutines post: the delivery loop says what the records say
// happened, and the connection answers a reply. This drives them together,
// because the hazard is not in either one — it is in the state they share, and a
// -race run over tests that only ever use one of them at a time proves nothing
// about it. What it holds to is what an operator would notice: every reply is
// recorded and answered, every milestone is posted, and the thread the replies
// arrive in is still the thread the map says it is.
func TestRepliesAreAnsweredWhileTheDeliveryLoopIsPosting(t *testing.T) {
	t.Parallel()

	const rounds = 8
	sink, directives, posts := newSteeringSinkWithFeed(t, &growingFeed{}, testOperator)

	failures := make(chan error, rounds)
	var running sync.WaitGroup
	running.Add(2)
	go func() {
		defer running.Done()
		// Each pass opens a thread for a topic nothing has been said about yet,
		// which is the one thing that writes the thread map.
		for round := 0; round < rounds; round++ {
			if err := sink.pass(context.Background()); err != nil {
				failures <- err
				return
			}
		}
	}()
	go func() {
		defer running.Done()
		for round := 0; round < rounds; round++ {
			sink.steering.handle(context.Background(), reply(testOperator,
				fmt.Sprintf("round %d: prefer the smaller change", round),
				fmt.Sprintf("17500000%02d.000200", round)))
		}
	}()
	running.Wait()
	close(failures)
	for err := range failures {
		t.Fatalf("pass() error = %v", err)
	}

	recorded, err := directives.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != rounds {
		t.Fatalf("recorded %d directives, want every reply written down", len(recorded))
	}

	posts.mutex.Lock()
	defer posts.mutex.Unlock()
	answers := 0
	for _, post := range posts.requests {
		if post.ThreadTS == testThreadTS {
			answers++
		}
	}
	if answers != rounds {
		t.Fatalf("answers in the item's thread = %d, want one for every reply", answers)
	}

	// The map the delivery pass was writing all along still says what it said: an
	// acknowledgment that had written its own copy back would have dropped the
	// threads the pass opened beside it.
	threads, err := sink.store.LoadThreads()
	if err != nil {
		t.Fatalf("LoadThreads() error = %v", err)
	}
	topic, err := notify.WorkItem(testItem)
	if err != nil {
		t.Fatalf("address a work item: %v", err)
	}
	if thread, found := threads.Lookup("C1", topic.Key()); !found || thread.ThreadTS != testThreadTS {
		t.Fatalf("thread for %s = %#v (found %t), want the one the replies arrived in", testItem, thread, found)
	}
	if len(threads.Threads) != rounds+1 {
		t.Fatalf("threads = %d, want the item's and one per pass", len(threads.Threads))
	}
}

// An acknowledgment is not given the capability to open a thread, so a topic
// whose thread has gone from the map it was handed is refused rather than opened
// a second time. That is what keeps the connection's goroutine off the map the
// delivery pass owns.
func TestAnAcknowledgmentNeverOpensAThread(t *testing.T) {
	t.Parallel()

	sink, _, posts := newSteeringSink(t, testOperator)
	topic, err := notify.WorkItem(testItem)
	if err != nil {
		t.Fatalf("address a work item: %v", err)
	}
	sink.steering.answer(context.Background(), ThreadMap{Threads: map[string]Thread{}}, testOperator,
		refused(topic, time.Now(), "a reason"))

	if len(posts.requests) != 0 {
		t.Fatalf("posts = %#v, want nothing said and no thread opened", posts.requests)
	}
	threads, err := sink.store.LoadThreads()
	if err != nil {
		t.Fatalf("LoadThreads() error = %v", err)
	}
	if thread, found := threads.Lookup("C1", topic.Key()); !found || thread.ThreadTS != testThreadTS {
		t.Fatalf("thread for %s = %#v (found %t), want the map left exactly as it was", testItem, thread, found)
	}
}

// growingFeed hands out one new topic's milestone every pass, so every pass opens
// a thread and writes the thread map. An ordinary feed goes quiet after its first
// pass, which is exactly the contention this has to keep up.
type growingFeed struct {
	mutex sync.Mutex
	polls int
}

func (f *growingFeed) Poll(context.Context, Cursors) (Batch, error) {
	f.mutex.Lock()
	defer f.mutex.Unlock()
	f.polls++
	delivery := milestone(1, notify.KindRunStarted)
	delivery.Stream = fmt.Sprintf("run:run-%d", f.polls)
	delivery.Notification.Topic = notify.Topic{
		Kind: notify.TopicWorkItem,
		ID:   fmt.Sprintf("yoyodyne-ifd.68.4.%d", f.polls),
	}
	return Batch{
		Streams:    map[string]struct{}{delivery.Stream: {}},
		Deliveries: []Delivery{delivery},
	}, nil
}

// newSteeringSink is a sink with the inbound half wired to a real directive
// store, and one work item's thread already open — which is the state every reply
// below arrives into, because a reply is correlated through the thread the sink
// opened for a topic.
func newSteeringSink(t *testing.T, operators ...string) (*Sink, *runstate.DirectiveStore, *recordedPosts) {
	t.Helper()
	return newSteeringSinkWithFeed(t, &fixedFeed{}, operators...)
}

func newSteeringSinkWithFeed(t *testing.T, feed Feed, operators ...string) (*Sink, *runstate.DirectiveStore, *recordedPosts) {
	t.Helper()
	root := t.TempDir()
	store, err := NewStore(root, testProduct)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	// The same store `yoyo directive` writes and every run reads, rather than a
	// stand-in: what this half is for is a reply landing in exactly that record.
	directives, err := runstate.NewDirectiveStore(root, testProduct)
	if err != nil {
		t.Fatalf("NewDirectiveStore() error = %v", err)
	}
	posts := &recordedPosts{}
	sink, err := New(Options{
		Channel:    "C1",
		Store:      store,
		API:        newTestAPI(t, posts.handle),
		Feed:       feed,
		Directives: directives,
		Operators:  operators,
		Log:        func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	sink.pace.sleep = func(context.Context, time.Duration) error { return nil }

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
	return sink, directives, posts
}

// reply is one person typing in the work item's thread.
func reply(user, text, ts string) socketEnvelope {
	return envelopeFor(map[string]any{
		"type":      "message",
		"user":      user,
		"text":      text,
		"ts":        ts,
		"thread_ts": testThreadTS,
		"channel":   "C1",
	})
}

func envelopeFor(event map[string]any) socketEnvelope {
	payload, err := json.Marshal(map[string]any{"type": "event_callback", "event": event})
	if err != nil {
		panic(err)
	}
	return socketEnvelope{Type: socketEventsAPI, EnvelopeID: "envelope-1", Payload: payload}
}

func onlyDirective(t *testing.T, directives *runstate.DirectiveStore) directive.Directive {
	t.Helper()
	recorded, err := directives.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 1 {
		t.Fatalf("recorded = %+v, want exactly one directive", recorded)
	}
	return recorded[0]
}

func onlyPost(t *testing.T, posts *recordedPosts) postRequest {
	t.Helper()
	if len(posts.requests) != 1 {
		t.Fatalf("posts = %#v, want exactly one acknowledgment", posts.requests)
	}
	return posts.requests[0]
}
