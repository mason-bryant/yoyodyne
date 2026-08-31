package slack

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/notify"
)

const (
	// testColleague is somebody this project recognizes and granted nothing: in
	// the operators mapping, with no direct-work. They are the case that keeps the
	// refusal for a stranger from swallowing the refusal for a missing grant.
	testColleague = "U0COLLEAGUE"
	// testContact is what the mapping files its humans under, which is who a
	// stranger is told to reach out to.
	testContact = "mason-bryant"
)

// The whole of it, in one thread: somebody the project does not recognize is
// told once that this app does not know them and who to ask instead, and
// everything they say after that is written down and answered with nothing.
func TestAStrangerIsToldOncePerThreadWhoToAskInstead(t *testing.T) {
	t.Parallel()

	sink, directives, posts := newSteeringSink(t, testOperator)
	var noted []string
	sink.log = func(format string, args ...any) { noted = append(noted, fmt.Sprintf(format, args...)) }

	sink.steering.handle(context.Background(), reply(testStranger, "can you deploy this for me", "1750000001.000200"))
	sink.steering.handle(context.Background(), reply(testStranger, "hello? anybody there", "1750000002.000300"))
	sink.steering.handle(context.Background(), reply(testStranger, "fine, be that way", "1750000003.000400"))

	answer := onlyPost(t, posts)
	if answer.ThreadTS != testThreadTS {
		t.Fatalf("answer = %#v, want it in the thread they spoke in", answer)
	}
	if !strings.HasPrefix(answer.Text, "<@"+testStranger+"> ") {
		t.Fatalf("answer = %q, want it addressed to whoever it refuses", answer.Text)
	}
	if !strings.Contains(answer.Text, strangerLead) {
		t.Fatalf("answer = %q, want it to say this app does not know them", answer.Text)
	}
	if !strings.Contains(answer.Text, testContact) {
		t.Fatalf("answer = %q, want it to name who to reach out to", answer.Text)
	}
	if recorded, err := directives.List(); err != nil {
		t.Fatalf("List() error = %v", err)
	} else if len(recorded) != 0 {
		t.Fatalf("recorded = %+v, want nothing recorded from somebody this project does not recognize", recorded)
	}

	// The one they were answered wears the refusal; the ones after it wear
	// nothing, because ignoring somebody is not a thing to put a mark on.
	if worn := posts.wearing["1750000001.000200"]; !worn[notify.ReceiptRefused.Symbol()] || len(worn) != 1 {
		t.Fatalf("the message wears %#v, want the refusal mark and nothing else", worn)
	}
	for _, ignored := range []string{"1750000002.000300", "1750000003.000400"} {
		if worn := posts.wearing[ignored]; len(worn) != 0 {
			t.Fatalf("a message said after being told wears %#v, want nothing", worn)
		}
	}

	// Recorded and ignored: what they went on to say is in the sink's own log,
	// which is the whole of what "recorded" buys — an operator can see who tried
	// and what they wanted without the channel filling up with refusals.
	log := strings.Join(noted, "\n")
	for _, said := range []string{"hello? anybody there", "fine, be that way"} {
		if !strings.Contains(log, said) {
			t.Fatalf("log =\n%s\nwant it to carry %q, which is what they said after being told", log, said)
		}
	}

	// Durable, because the rule is one per thread rather than one per process: a
	// sink that restarts overnight must not greet the same person in the morning.
	refusals, err := sink.store.LoadRefusals()
	if err != nil {
		t.Fatalf("LoadRefusals() error = %v", err)
	}
	if !refusals.Has("C1", testThreadTS) {
		t.Fatalf("refusals = %#v, want the thread remembered so it is not said there again", refusals)
	}
}

// A stranger who @-mentions the app gets the same sentence rather than the four
// lines, and gets it once: the thread the answer hangs from is the thread it is
// remembered against, so their next attempt in it is read and not answered.
func TestAStrangerWhoMentionsTheAppIsToldRatherThanAnswered(t *testing.T) {
	t.Parallel()

	const askedTS = "1750000001.000100"
	sink, _, posts := newSteeringSink(t, testOperator)
	sink.steering.handle(context.Background(), topLevel(testStranger, "<@"+testApp+"> what is running?", askedTS))
	sink.steering.handle(context.Background(), envelopeFor(map[string]any{
		"type": "message", "user": testStranger, "text": "<@" + testApp + "> come on, status",
		"ts": "1750000002.000200", "thread_ts": askedTS, "channel": "C1",
	}))

	answer := onlyPost(t, posts)
	if answer.ThreadTS != askedTS {
		t.Fatalf("answer = %#v, want it hanging from the message that asked", answer)
	}
	if !strings.Contains(answer.Text, strangerLead) || !strings.Contains(answer.Text, testContact) {
		t.Fatalf("answer = %q, want the refusal naming who to reach out to", answer.Text)
	}
	if strings.Contains(answer.Text, "Running:") {
		t.Fatalf("answer = %q, want the standing kept from somebody this project does not recognize", answer.Text)
	}
}

// The two refusals are different answers to different questions, and neither
// swallows the other. Somebody the mapping names who was granted nothing may
// steer nothing — and is told that, by the grant they are missing — but they are
// not somebody this app has never heard of, and answering them is not steering.
func TestAKnownColleagueIsToldTheGrantRatherThanThatNobodyKnowsThem(t *testing.T) {
	t.Parallel()

	sink, _, posts := newSteeringSink(t, testOperator)
	recognize(sink, testColleague)
	sink.steering.handle(context.Background(),
		reply(testColleague, "ambiguous: stop what you are doing", "1750000001.000200"))

	refusal := onlyPost(t, posts)
	if !strings.Contains(refusal.Text, "direct-work") {
		t.Fatalf("answer = %q, want the grant they are missing rather than a stranger's refusal", refusal.Text)
	}
	if strings.Contains(refusal.Text, strangerLead) {
		t.Fatalf("answer = %q, want somebody this project recognizes not told it does not know them", refusal.Text)
	}

	posts.requests = nil
	sink.steering.handle(context.Background(),
		topLevel(testColleague, "<@"+testApp+"> what is running?", "1750000002.000300"))
	if answer := onlyPost(t, posts); !strings.Contains(answer.Text, "Running:") {
		t.Fatalf("answer = %q, want the four lines for a colleague who may steer nothing", answer.Text)
	}
}

// A project that recognizes nobody has drawn no boundary, so there is nobody to
// be a stranger to and nobody to name as a contact. It behaves exactly as it did
// before there was a refusal to give, which is what keeps a workspace that has
// not filled the mapping in from being answered by a sentence naming no one.
func TestAProjectThatRecognizesNobodyTellsNobodyItDoesNotKnowThem(t *testing.T) {
	t.Parallel()

	sink, _, posts := newSteeringSink(t)
	sink.steering.handle(context.Background(),
		topLevel(testStranger, "<@"+testApp+"> what is running?", "1750000001.000100"))

	if answer := onlyPost(t, posts); !strings.Contains(answer.Text, "Running:") {
		t.Fatalf("answer = %q, want the four lines where the mapping names nobody to be a stranger to", answer.Text)
	}
}

// Who to reach out to, as somebody would say it. The empty case is a stated
// description rather than a sentence that trails off into nobody.
func TestWhoAStrangerIsToldToReachOutTo(t *testing.T) {
	t.Parallel()

	for _, want := range []struct {
		contacts []string
		named    string
	}{
		{contacts: nil, named: unnamedContacts},
		{contacts: []string{"  "}, named: unnamedContacts},
		{contacts: []string{"mason"}, named: "mason"},
		{contacts: []string{"mason", "alex"}, named: "mason or alex"},
		{contacts: []string{"mason", "alex", "sam"}, named: "mason, alex, or sam"},
	} {
		if got := contactList(want.contacts); got != want.named {
			t.Errorf("contactList(%#v) = %q, want %q", want.contacts, got, want.named)
		}
		if text := refusalText(want.contacts); !strings.Contains(text, want.named) || !strings.HasPrefix(text, strangerLead) {
			t.Errorf("refusalText(%#v) = %q, want the operator's sentence naming %q", want.contacts, text, want.named)
		}
	}
}

// The one record here a person outside this project can cause a line to be
// written to is bounded, and what it forgets is the oldest thread rather than
// whichever one a map was walked to first.
func TestTheRefusalMapForgetsTheOldestThreadRatherThanGrowingForever(t *testing.T) {
	t.Parallel()

	said := time.Date(2026, 8, 30, 9, 0, 0, 0, time.UTC)
	refusals := RefusalMap{}
	for count := 0; count < maxRememberedRefusals+10; count++ {
		refusals.Record("C1", fmt.Sprintf("17500000%03d.000100", count),
			Refusal{Member: testStranger, At: said.Add(time.Duration(count) * time.Minute)})
	}

	if len(refusals.Refused) != maxRememberedRefusals {
		t.Fatalf("refusals = %d, want no more than the %d it is bounded to", len(refusals.Refused), maxRememberedRefusals)
	}
	if refusals.Has("C1", "17500000000.000100") {
		t.Fatalf("refusals = %#v, want the oldest thread forgotten first", refusals)
	}
	if !refusals.Has("C1", fmt.Sprintf("17500000%03d.000100", maxRememberedRefusals+9)) {
		t.Fatalf("refusals = %#v, want the most recent thread kept", refusals)
	}
}

// The refusal is remembered per channel as well as per thread, because a thread
// timestamp means nothing outside the channel it was posted in.
func TestARefusalIsRememberedInTheChannelItWasSaidIn(t *testing.T) {
	t.Parallel()

	refusals := RefusalMap{}
	refusals.Record("C1", testThreadTS, Refusal{Member: testStranger, At: time.Now().UTC()})
	if !refusals.Has("C1", testThreadTS) {
		t.Fatalf("refusals = %#v, want the thread it was said in remembered", refusals)
	}
	if refusals.Has("C2", testThreadTS) {
		t.Fatalf("refusals = %#v, want a timestamp in another channel to be another thread", refusals)
	}
}

// recognize adds humans this project's mapping names without granting them
// anything, which is the wider list the sink reads to tell a colleague from a
// stranger.
func recognize(sink *Sink, members ...string) {
	for _, member := range members {
		sink.steering.recognized[member] = true
	}
}
