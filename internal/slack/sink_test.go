package slack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// testProduct is the product every sink in these tests reports on, and
// testAppearance is how its speakers appear because of it: the shipped name and
// picture, with the product after the name.
const testProduct domain.ProductID = "yoyodyne"

var testAppearance = notify.Appearance{Product: testProduct}

// One thread per topic, held across restarts. The first thing said about a work
// item opens its thread and everything else about it replies into that thread —
// which is what makes a channel readable when three items are in flight at once.
func TestATopicOpensOneThreadAndEverythingElseRepliesIntoIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	posts := &recordedPosts{}
	feed := &fixedFeed{deliveries: []Delivery{
		milestone(1, notify.KindRunStarted),
		milestone(2, notify.KindChecksPassed),
	}}
	sink := newTestSink(t, root, feed, posts)

	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	if len(posts.requests) != 3 {
		t.Fatalf("posts = %d, want the thread opened and both milestones in it", len(posts.requests))
	}
	if posts.requests[0].ThreadTS != "" || !strings.Contains(posts.requests[0].Text, "yoyodyne-ifd.68.3") {
		t.Fatalf("first post = %#v, want the thread opened by naming the topic", posts.requests[0])
	}
	for _, reply := range posts.requests[1:] {
		if reply.ThreadTS != posts.timestamps[0] {
			t.Fatalf("reply = %#v, want it inside the thread the topic opened", reply)
		}
	}

	// A restart is a second sink over the same durable state. It must say
	// nothing further: every one of these transitions has already been said.
	posts.requests = nil
	if err := newTestSink(t, root, feed, posts).pass(context.Background()); err != nil {
		t.Fatalf("second pass() error = %v", err)
	}
	if len(posts.requests) != 0 {
		t.Fatalf("second pass posted %#v, want a thread that is a narrative rather than a repetition", posts.requests)
	}
}

// A thread header is read by people, and an identifier on its own is a name
// they have to go and resolve before they know what the thread is about. So the
// header names the item and then says what it is called, from what the durable
// record carried — which is where a title comes from wherever a record has one,
// and nothing is asked of the tracker.
func TestAThreadHeaderNamesTheItemAndWhatItIsCalled(t *testing.T) {
	t.Parallel()

	posts := &recordedPosts{}
	titled := milestone(1, notify.KindRunStarted)
	titled.Notification.Topic = titled.Notification.Topic.WithTitle(
		"Slack run-started messages speak as the role that actually selected the run")
	sink := newTestSink(t, t.TempDir(), &fixedFeed{deliveries: []Delivery{titled}}, posts)

	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	if len(posts.requests) != 2 {
		t.Fatalf("posts = %d, want the thread and the milestone in it", len(posts.requests))
	}
	want := "*yoyodyne-ifd.68.3 — Slack run-started messages speak as the role that actually selected the run*"
	if posts.requests[0].Text != want {
		t.Fatalf("header = %q, want %q", posts.requests[0].Text, want)
	}
}

// A sink with nothing to ask heads an untitled topic's thread with the
// identifier alone, which is exactly what every thread was before titles were
// carried: a header with a dangling separator would read as a name somebody
// failed to write.
func TestAThreadForAnUntitledTopicIsHeadedByTheIdentifierAlone(t *testing.T) {
	t.Parallel()

	posts := &recordedPosts{}
	sink := newTestSink(t, t.TempDir(), &fixedFeed{deliveries: []Delivery{
		milestone(1, notify.KindRunStarted),
	}}, posts)

	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	if posts.requests[0].Text != "*yoyodyne-ifd.68.3*" {
		t.Fatalf("header = %q, want the identifier alone", posts.requests[0].Text)
	}
}

// An item whose first appearance in the channel is a bookkeeping event — a
// priority changed, a goal recorded — is mentioned by a record that says what
// happened without saying what the item is. Every item admitted before the
// channel existed is one of those, so the tracker is asked rather than the
// thread being opened on a bare identifier.
func TestAThreadWhoseRecordCarriedNoTitleIsNamedFromTheTracker(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	posts := &recordedPosts{}
	titles := &fixedTitles{titles: map[string]string{
		"yoyodyne-ifd.68.3": "Park the Codex adapter until the provider answers",
	}}
	feed := &fixedFeed{deliveries: []Delivery{
		milestone(1, notify.KindItemReprioritized),
		milestone(2, notify.KindRunParked),
	}}
	sink := newTestSinkWithTitles(t, root, feed, posts, titles)

	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	want := "*yoyodyne-ifd.68.3 — Park the Codex adapter until the provider answers*"
	if posts.requests[0].Text != want {
		t.Fatalf("header = %q, want %q", posts.requests[0].Text, want)
	}
	// A thread is opened once and the map that says so is durable, so the tracker
	// is asked once however many messages the thread goes on to carry — and never
	// again after a restart.
	if len(titles.asked) != 1 || titles.asked[0] != "yoyodyne-ifd.68.3" {
		t.Fatalf("asked the tracker %#v, want the one item whose thread was opened", titles.asked)
	}
	feed.deliveries = append(feed.deliveries, milestone(3, notify.KindChecksPassed))
	if err := newTestSinkWithTitles(t, root, feed, posts, titles).pass(context.Background()); err != nil {
		t.Fatalf("second pass() error = %v", err)
	}
	if len(titles.asked) != 1 {
		t.Fatalf("asked the tracker %#v, want a thread that is named once", titles.asked)
	}
}

// A tracker that will not say what an item is called costs the header its title
// and nothing else. Reporting is never a gate, and a thread nobody opened is a
// whole narrative missing rather than a name.
func TestATrackerThatWillNotSayWhatAnItemIsCalledStillOpensTheThread(t *testing.T) {
	t.Parallel()

	posts := &recordedPosts{}
	titles := &fixedTitles{err: errors.New("bd show failed: no work item yoyodyne-ifd.68.3")}
	said := ""
	sink := newTestSinkWithTitles(t, t.TempDir(), &fixedFeed{deliveries: []Delivery{
		milestone(1, notify.KindItemReprioritized),
	}}, posts, titles)
	sink.log = func(format string, args ...any) { said += fmt.Sprintf(format, args...) }

	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	if len(posts.requests) != 2 {
		t.Fatalf("posts = %d, want the thread and the milestone in it", len(posts.requests))
	}
	if posts.requests[0].Text != "*yoyodyne-ifd.68.3*" {
		t.Fatalf("header = %q, want the identifier alone", posts.requests[0].Text)
	}
	if !strings.Contains(said, "would not say what yoyodyne-ifd.68.3 is called") {
		t.Fatalf("the sink's log = %q, want it to say why the header carries no title", said)
	}
}

// Each persona speaks under its own name and face. One sink posts for all of
// them, so a channel where they all arrived as one app would leave the voice as
// the only thing telling the speakers apart.
func TestEachPersonaPostsUnderItsOwnDisplayIdentity(t *testing.T) {
	t.Parallel()

	posts := &recordedPosts{}
	sink := newTestSink(t, t.TempDir(), &fixedFeed{deliveries: []Delivery{{
		Stream: reportStream,
		Cursor: Cursor{Position: 1},
		Notification: notify.Notification{
			Topic:   workItemTopic(t, "yoyodyne-ifd.68.3"),
			Speaker: notify.Persona(domain.RoleDeveloper, ""),
			Event: notify.Event{
				Kind:     notify.KindReportFiled,
				At:       time.Now(),
				Severity: report.SeverityWarning,
				Text:     "the replay conflicted with the merged package",
			},
		},
	}}}, posts)

	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	if len(posts.requests) != 2 {
		t.Fatalf("posts = %d, want the thread and the report in it", len(posts.requests))
	}
	filed := posts.requests[1]
	identity := testAppearance.Identity(notify.Persona(domain.RoleDeveloper, ""))
	if filed.Username != identity.Name || filed.IconEmoji != identity.Avatar {
		t.Fatalf("post = %#v, want it under the developer's own name and face", filed)
	}
	// The words are the notifier's, severity included, and the sink changes none
	// of them.
	if !strings.Contains(filed.Text, "Warning") || !strings.Contains(filed.Text, "the replay conflicted") {
		t.Fatalf("post text = %q, want the rendered message carried through unchanged", filed.Text)
	}
	// The thread is opened by the harness rather than by whichever persona
	// happened to speak first: opening a thread is nobody's account of anything.
	if posts.requests[0].Username != testAppearance.Identity(notify.Harness()).Name {
		t.Fatalf("thread opened by %q, want the harness", posts.requests[0].Username)
	}
}

// A project may choose the picture beside each name, and Slack takes the two
// shapes in two different fields. A shortcode goes in one and an image in the
// other, never both on one post, or the call has said the same thing twice.
func TestAConfiguredAvatarIsPostedInTheFieldItsShapeBelongsIn(t *testing.T) {
	t.Parallel()

	posts := &recordedPosts{}
	sink := newTestSink(t, t.TempDir(), &fixedFeed{deliveries: []Delivery{{
		Stream: reportStream,
		Cursor: Cursor{Position: 1},
		Notification: notify.Notification{
			Topic:   workItemTopic(t, "yoyodyne-ifd.68.6"),
			Speaker: notify.Persona(domain.RoleDeveloper, ""),
			Event: notify.Event{
				Kind:     notify.KindReportFiled,
				At:       time.Now(),
				Severity: report.SeverityNote,
				Text:     "the avatar is the picture and nothing else",
			},
		},
	}}}, posts)
	// The harness gets an image and the developer a shortcode, so one pass
	// exercises both fields — the thread header is the harness's own post.
	sink.appearance.Avatars = notify.Avatars{
		notify.HarnessSpeaker:        "https://example.invalid/faces/harness.png",
		string(domain.RoleDeveloper): ":ship-it:",
	}

	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	if len(posts.requests) != 2 {
		t.Fatalf("posts = %d, want the thread and the report in it", len(posts.requests))
	}
	opened := posts.requests[0]
	if opened.IconURL != "https://example.invalid/faces/harness.png" || opened.IconEmoji != "" {
		t.Errorf("thread opened as %#v, want the configured image in icon_url alone", opened)
	}
	filed := posts.requests[1]
	if filed.IconEmoji != ":ship-it:" || filed.IconURL != "" {
		t.Errorf("report posted as %#v, want the configured shortcode in icon_emoji alone", filed)
	}
	// The picture moved and nothing else did: the name is still the developer's.
	if filed.Username != testAppearance.Identity(notify.Persona(domain.RoleDeveloper, "")).Name {
		t.Errorf("report posted as %q, want it still under the developer's own name", filed.Username)
	}
}

// Every name a sink posts under says which product it is reporting on, the
// thread header the harness opens included. An operator develops more than one
// product, and where two harnesses are read in one channel the name is the only
// thing a message carries that says which of them is talking.
func TestEveryNameASinkPostsUnderCarriesItsProduct(t *testing.T) {
	t.Parallel()

	posts := &recordedPosts{}
	sink := newTestSink(t, t.TempDir(), &fixedFeed{deliveries: []Delivery{{
		Stream: reportStream,
		Cursor: Cursor{Position: 1},
		Notification: notify.Notification{
			Topic:   workItemTopic(t, "yoyodyne-ifd.68.13"),
			Speaker: notify.Persona(domain.RoleDevelopmentManager, ""),
			Event: notify.Event{
				Kind:     notify.KindReportFiled,
				At:       time.Now(),
				Severity: report.SeverityNote,
				Text:     "which harness is talking is the boundary that matters most",
			},
		},
	}}}, posts)

	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	if len(posts.requests) != 2 {
		t.Fatalf("posts = %d, want the thread and the report in it", len(posts.requests))
	}
	if want := "Yoyodyne (yoyodyne)"; posts.requests[0].Username != want {
		t.Errorf("thread opened by %q, want %q", posts.requests[0].Username, want)
	}
	if want := "Development Manager (yoyodyne)"; posts.requests[1].Username != want {
		t.Errorf("report posted by %q, want %q", posts.requests[1].Username, want)
	}
	for _, post := range posts.requests {
		if !strings.HasSuffix(post.Username, " ("+string(testProduct)+")") {
			t.Errorf("post = %#v, want a name saying which product is talking", post)
		}
	}
}

// The product a sink names its speakers for is the one its own state is kept
// under, taken from the store rather than given beside it. A sink cannot
// therefore hold one product's threads and post another product's name, which
// is a channel of misattributed messages nothing would detect.
func TestASinkIsNamedForTheProductItsStoreIsFor(t *testing.T) {
	t.Parallel()

	store, err := NewStore(t.TempDir(), "context-conductor")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	api, err := NewAPI("xoxb-test", "xapp-test")
	if err != nil {
		t.Fatalf("NewAPI() error = %v", err)
	}
	sink, err := New(Options{Channel: "C1", Store: store, API: api, Feed: &fixedFeed{}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if got := sink.appearance.Product; got != "context-conductor" {
		t.Errorf("sink names its speakers for %q, want the product its store is for", got)
	}
	if want := "Yoyodyne (context-conductor)"; sink.appearance.Identity(notify.Harness()).Name != want {
		t.Errorf("harness posts as %q, want %q", sink.appearance.Identity(notify.Harness()).Name, want)
	}
}

// A sink with no store has nowhere to keep its cursors and no product to name
// its speakers for, so it is refused at assembly rather than discovered in a
// channel.
func TestASinkWithoutAStoreIsRefusedAtAssembly(t *testing.T) {
	t.Parallel()

	api, err := NewAPI("xoxb-test", "xapp-test")
	if err != nil {
		t.Fatalf("NewAPI() error = %v", err)
	}
	if _, err := New(Options{Channel: "C1", API: api, Feed: &fixedFeed{}}); err == nil {
		t.Fatal("New() without a store = nil, want a refusal")
	}
}

// A speaker nothing was configured for keeps the avatar the harness ships, so a
// project that named one persona's picture has not blanked the rest.
func TestASpeakerWithNoConfiguredAvatarKeepsTheShippedOne(t *testing.T) {
	t.Parallel()

	posts := &recordedPosts{}
	sink := newTestSink(t, t.TempDir(), &fixedFeed{deliveries: []Delivery{
		milestone(1, notify.KindRunStarted),
	}}, posts)
	sink.appearance.Avatars = notify.Avatars{string(domain.RoleDeveloper): ":ship-it:"}

	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	shipped := notify.Harness().Identity().Avatar
	for _, post := range posts.requests {
		if post.IconEmoji != shipped || post.IconURL != "" {
			t.Errorf("post = %#v, want the harness's shipped avatar %q", post, shipped)
		}
	}
}

// A message is posted and then its cursor advances, so a sink that dies between
// the two repeats a message rather than losing one. The durable record is
// authoritative and this is a view of it, so a repetition is the right side of
// that trade.
func TestAMessageThatCouldNotBePostedIsPostedOnTheNextPass(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	// The thread's own message and the first milestone land; everything after
	// them is refused, which is what a workspace going away looks like.
	posts := &recordedPosts{allow: 2}
	feed := &fixedFeed{deliveries: []Delivery{
		milestone(1, notify.KindRunStarted),
		milestone(2, notify.KindPromoted),
	}}
	sink := newTestSink(t, root, feed, posts)

	if err := sink.pass(context.Background()); err == nil {
		t.Fatal("pass() = nil, want the refusal reported so the sink backs off")
	}
	posts.allow = 0
	posts.requests = nil
	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("second pass() error = %v", err)
	}
	if len(posts.requests) != 1 {
		t.Fatalf("second pass posted %d, want only the message that never landed", len(posts.requests))
	}
	if !strings.Contains(posts.requests[0].Text, "promoted") {
		t.Fatalf("second pass posted %q, want the message the first pass could not", posts.requests[0].Text)
	}
}

// What is about the whole line — not any one item — is posted at the top level.
// Burying it in one item's thread would misfile it.
func TestProductLevelNewsIsNotBuriedInAnItemsThread(t *testing.T) {
	t.Parallel()

	posts := &recordedPosts{}
	sink := newTestSink(t, t.TempDir(), &fixedFeed{deliveries: []Delivery{{
		Stream:       productStream,
		Cursor:       Cursor{Position: 1},
		Notification: notify.IntakeReleased(time.Now()),
	}}}, posts)

	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	if len(posts.requests) != 1 {
		t.Fatalf("posts = %d, want one unthreaded message and no thread opened for it", len(posts.requests))
	}
	if posts.requests[0].ThreadTS != "" {
		t.Fatalf("post = %#v, want product news at the top level of the channel", posts.requests[0])
	}
}

// The main channel view hides thread replies by design, which is right for a
// routine note and wrong for a warning: a run parked out of tokens can sit
// unseen inside a thread while the channel looks quiet. So the severity the
// envelope already carries decides — a note stays where the narrative is, and
// anything asking for attention is also sent to the channel.
func TestRepliesThatAskForAttentionAreAlsoSentToTheChannel(t *testing.T) {
	t.Parallel()

	posts := &recordedPosts{}
	sink := newTestSink(t, t.TempDir(), &fixedFeed{deliveries: []Delivery{
		filedReport(1, report.SeverityNote, "the replay was clean"),
		filedReport(2, report.SeverityWarning, "the replay conflicted with the merged package"),
		filedReport(3, report.SeverityCritical, "the target branch has diverged under the change"),
	}}, posts)

	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	if len(posts.requests) != 4 {
		t.Fatalf("posts = %d, want the thread opened and all three reports in it", len(posts.requests))
	}
	// The message a thread hangs from is already in the channel, so asking for it
	// to be sent there as well is a flag that says nothing.
	if posts.requests[0].ReplyBroadcast {
		t.Fatalf("thread header = %#v, want no broadcast on a message that is not a reply", posts.requests[0])
	}
	for index, want := range []bool{false, true, true} {
		reply := posts.requests[index+1]
		if reply.ThreadTS != posts.timestamps[0] {
			t.Fatalf("reply = %#v, want every severity still inside the topic's thread", reply)
		}
		if reply.ReplyBroadcast != want {
			t.Fatalf("reply %q broadcast = %v, want %v", reply.Text, reply.ReplyBroadcast, want)
		}
	}
}

// Product-level news is already at the top of the channel, so nothing about its
// severity turns it into a reply Slack is asked to send there twice.
func TestProductLevelNewsIsNeverBroadcastBackIntoTheChannel(t *testing.T) {
	t.Parallel()

	posts := &recordedPosts{}
	sink := newTestSink(t, t.TempDir(), &fixedFeed{deliveries: []Delivery{{
		Stream: reportStream,
		Cursor: Cursor{Position: 1},
		Notification: notify.Notification{
			Topic:   notify.Product(),
			Speaker: notify.Persona(domain.RoleDeveloper, ""),
			Event: notify.Event{
				Kind:     notify.KindReportFiled,
				At:       time.Now(),
				Severity: report.SeverityCritical,
				Text:     "the provider refused every account",
			},
		},
	}}}, posts)

	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	if len(posts.requests) != 1 {
		t.Fatalf("posts = %d, want one unthreaded message", len(posts.requests))
	}
	if posts.requests[0].ThreadTS != "" || posts.requests[0].ReplyBroadcast {
		t.Fatalf("post = %#v, want product news posted once at the top level", posts.requests[0])
	}
}

// A message leads back to the durable record it was read from rather than
// standing in for it.
func TestAMessageNamesTheRecordItWasReadFrom(t *testing.T) {
	t.Parallel()

	rendered, err := notify.Render(workItemTopic(t, "yoyodyne-ifd.68.3"), notify.Harness(), notify.Event{
		Kind:     notify.KindPromoted,
		At:       time.Now(),
		Severity: report.SeverityNote,
		Refs:     notify.Refs{RunID: "run-a", WorkItemID: "yoyodyne-ifd.68.3"},
	})
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if text := renderText(rendered); !strings.Contains(text, "run run-a") {
		t.Fatalf("rendered = %q, want the durable record named", text)
	}
}

// A message too long for Slack is cut with a marker naming the record that holds
// the whole of it, and never split into a flood of messages to fit.
func TestAnOversizedMessageIsTruncatedAndSaysWhereTheRestIs(t *testing.T) {
	t.Parallel()

	text := truncate(strings.Repeat("a", maxTextBytes+2048), notify.Refs{RunID: "run-a"})
	if len(text) > maxTextBytes {
		t.Fatalf("rendered %d bytes, want it inside the limit", len(text))
	}
	if !strings.Contains(text, "run-a") {
		t.Fatalf("rendered %q, want the marker to name the record that holds the whole", text)
	}
}

// A record nothing can be said about must not hold up every later message on
// every stream forever, so it is said once in the log and its cursor moves past
// it. A workspace that refused the post is the other case entirely, and the two
// must not be confused: that one is retried.
func TestARecordNothingCanBeSaidAboutIsSkippedRatherThanRepeatedForever(t *testing.T) {
	t.Parallel()

	posts := &recordedPosts{}
	var logged []string
	sink := newTestSink(t, t.TempDir(), &fixedFeed{deliveries: []Delivery{
		// An event with no kind has no voice line in any persona, so nothing
		// could be said about it whatever the workspace does.
		{Stream: "run:run-a", Cursor: Cursor{Position: 1}, Notification: notify.Notification{
			Topic:   workItemTopic(t, "yoyodyne-ifd.68.3"),
			Speaker: notify.Harness(),
			Event:   notify.Event{Kind: "nothing.happened", At: time.Now(), Severity: report.SeverityNote},
		}},
		milestone(2, notify.KindRunStarted),
	}}, posts)
	sink.log = func(format string, args ...any) { logged = append(logged, format) }

	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	if len(logged) == 0 {
		t.Fatal("a notification that could not be said must be said out loud")
	}
	// The thread and the message that followed it still went out.
	if len(posts.requests) != 2 {
		t.Fatalf("posts = %d, want the rest of the stream unaffected", len(posts.requests))
	}
}

// A delivery that carries nothing to say is a cursor advance and no more. It is
// how a run that was over before the sink started stops being carried, and it
// must not put a message nobody asked for into the channel.
func TestASilentDeliveryAdvancesTheCursorWithoutSayingAnything(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	posts := &recordedPosts{}
	sink := newTestSink(t, root, &fixedFeed{deliveries: []Delivery{
		{Stream: "run:run-a", Cursor: Cursor{Closed: true, Position: 1}},
	}}, posts)

	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	if len(posts.requests) != 0 {
		t.Fatalf("posts = %#v, want nothing said about a run nothing was said about", posts.requests)
	}
	cursors, err := sink.store.LoadCursors()
	if err != nil {
		t.Fatalf("LoadCursors() error = %v", err)
	}
	if !cursors.Streams["run:run-a"].Closed {
		t.Fatalf("cursor = %#v, want the run closed so it stops being carried", cursors.Streams["run:run-a"])
	}
}

// The moment this product's reporting begins at is taken once, ever, written
// before anything is read, and never taken again. A sink that took it afresh on
// every start would carry it forward past every outage, and everything filed
// while it was down would then be older than the restart and read past as
// history — which is the one record somebody coming back most needs to see.
func TestTheWatermarkIsTakenOnceAndSurvivesEveryRestart(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	first := newTestSinkAt(t, root, &fixedFeed{}, &recordedPosts{}, moment)
	if err := first.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	cursors, err := first.store.LoadCursors()
	if err != nil {
		t.Fatalf("LoadCursors() error = %v", err)
	}
	if !cursors.Since.Equal(moment) {
		t.Fatalf("watermark = %s, want the moment the sink was first pointed at this product", cursors.Since)
	}

	// A second process, a day later, over the same durable state.
	restarted := newTestSinkAt(t, root, &fixedFeed{}, &recordedPosts{}, moment.Add(24*time.Hour))
	if err := restarted.pass(context.Background()); err != nil {
		t.Fatalf("second pass() error = %v", err)
	}
	cursors, err = restarted.store.LoadCursors()
	if err != nil {
		t.Fatalf("LoadCursors() error = %v", err)
	}
	if !cursors.Since.Equal(moment) {
		t.Fatalf("watermark = %s, want it unmoved by a restart", cursors.Since)
	}
}

// A sink assembled with a missing piece would discover it with a run in flight,
// which is the one moment nobody is watching the reporting process.
func TestASinkWithAMissingPieceIsRefusedAtAssembly(t *testing.T) {
	t.Parallel()

	if _, err := New(Options{}); err == nil {
		t.Fatal("New() = nil, want every missing piece named at once")
	}
	store, err := NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	api, err := NewAPI("xoxb-test", "xapp-test")
	if err != nil {
		t.Fatalf("NewAPI() error = %v", err)
	}
	if _, err := New(Options{Store: store, API: api, Feed: &fixedFeed{}}); err == nil {
		t.Fatal("New() without a channel = nil, want a refusal")
	}
}

// A single pass posts, so it is as capable of doubling a channel as a running
// sink is. It holds the same lease, and the lease is what makes "do not run
// two" a property rather than a warning in a document.
func TestASinglePassHoldsTheSameLeaseARunningSinkDoes(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	posts := &recordedPosts{}
	feed := &fixedFeed{deliveries: []Delivery{milestone(1, notify.KindRunStarted)}}
	running := newTestSink(t, root, feed, posts)
	release, err := running.hold()
	if err != nil {
		t.Fatalf("hold() error = %v", err)
	}

	if err := newTestSink(t, root, feed, posts).Once(context.Background()); err == nil {
		t.Fatal("Once() = nil, want a pass alongside a running sink refused")
	}
	if len(posts.requests) != 0 {
		t.Fatalf("posts = %#v, want nothing posted by the sink that was refused", posts.requests)
	}

	release()
	if err := newTestSink(t, root, feed, posts).Once(context.Background()); err != nil {
		t.Fatalf("Once() after the sink stopped error = %v", err)
	}
	if len(posts.requests) != 2 {
		t.Fatalf("posts = %d, want the thread and the milestone once the lease was free", len(posts.requests))
	}
}

// A refusal only a person can clear — an app nobody invited to the channel — is
// said once and then waited out quietly. Saying it every pass would put the same
// line in the log every few seconds for as long as the process runs, which is
// how a log stops being read; and it must still be retried, because what clears
// it happens in Slack rather than here.
func TestARefusalOnlyAPersonCanClearIsSaidOnceAndThenWaitedOut(t *testing.T) {
	t.Parallel()

	posts := &refusingPosts{code: "not_in_channel"}
	sink := newTestSink(t, t.TempDir(), &fixedFeed{deliveries: []Delivery{
		milestone(1, notify.KindRunStarted),
	}}, &recordedPosts{})
	sink.api = newTestAPI(t, posts.handle)
	// The waits are driven rather than spent: what is being tested is how often
	// the sink speaks, not how long it sleeps between attempts.
	sink.poll = time.Millisecond
	sink.refusal = time.Millisecond

	var mutex sync.Mutex
	var refusals int
	sink.log = func(format string, _ ...any) {
		mutex.Lock()
		defer mutex.Unlock()
		if strings.Contains(format, "keep refusing") {
			refusals++
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		sink.deliver(ctx)
	}()
	// Wait until the workspace has refused several times over, so a sink that
	// said it every pass would have said it several times.
	waitFor(t, func() bool { return posts.attempts() >= 4 })
	cancel()
	<-done

	mutex.Lock()
	defer mutex.Unlock()
	if refusals != 1 {
		t.Fatalf("the refusal was reported %d time(s) over %d attempts, want it said once", refusals, posts.attempts())
	}
}

// The other half of saying it once: reporting has to come back by itself when
// the operator fixes it, and say so, or a channel that went quiet for a reason
// nobody remembers looks like one that broke.
func TestReportingSaysSoWhenItStartsWorkingAgain(t *testing.T) {
	t.Parallel()

	posts := &refusingPosts{code: "not_in_channel"}
	sink := newTestSink(t, t.TempDir(), &fixedFeed{deliveries: []Delivery{
		milestone(1, notify.KindRunStarted),
	}}, &recordedPosts{})
	sink.api = newTestAPI(t, posts.handle)
	sink.poll = time.Millisecond
	sink.refusal = time.Millisecond

	var mutex sync.Mutex
	var recovered int
	sink.log = func(format string, _ ...any) {
		mutex.Lock()
		defer mutex.Unlock()
		if strings.Contains(format, "accepting messages again") {
			recovered++
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		sink.deliver(ctx)
	}()
	waitFor(t, func() bool { return posts.attempts() >= 2 })
	posts.invite() // the operator invites the app to the channel
	waitFor(t, func() bool {
		mutex.Lock()
		defer mutex.Unlock()
		return recovered == 1
	})
	cancel()
	<-done
}

// The process an operator leaves running has to stop when they stop it. A sink
// that kept its terminal until a read deadline passed would look hung at exactly
// the moment somebody had decided to intervene.
func TestStoppingTheSinkStopsIt(t *testing.T) {
	t.Parallel()

	sink := newTestSink(t, t.TempDir(), &fixedFeed{}, &recordedPosts{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() { done <- sink.Run(ctx) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v, want a stopped sink to be a clean exit", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return after its context ended")
	}
}

// The channel's top level is a status board. The message a thread hangs from
// carries what its item is doing now, and the mark that has stopped being true
// comes off as the record moves — so a scan of the channel answers what is
// working, what is with the reviewer, and what landed without a thread being
// opened.
func TestAThreadsOpenerCarriesTheItemsStatusAndTheStaleMarkComesOff(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	posts := &recordedPosts{}
	feed := &fixedFeed{
		deliveries: []Delivery{milestone(1, notify.KindRunStarted)},
		statuses:   map[string]notify.Status{"work-item:yoyodyne-ifd.68.3": notify.StatusWorking},
	}
	sink := newTestSink(t, root, feed, posts)

	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	opener := posts.timestamps[0]
	wantWearing(t, posts, opener, notify.StatusWorking)

	// A status that has not moved is left alone. Most passes are this one, and a
	// sink that re-marked every fifteen seconds would spend a workspace's
	// tolerance saying what it had already said.
	posts.marks = nil
	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("second pass() error = %v", err)
	}
	if len(posts.marks) != 0 {
		t.Fatalf("marks = %#v, want a status that has not moved marked again by nothing", posts.marks)
	}

	// The record moves and the mark moves with it. Every other status in the
	// vocabulary comes off first — three calls, two of which hit nothing — because
	// what is on the message is the question, and only the sweep answers it
	// without trusting a record that a crash could have left behind.
	feed.statuses["work-item:yoyodyne-ifd.68.3"] = notify.StatusInReview
	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("third pass() error = %v", err)
	}
	wantMarks(t, posts.marks,
		mark{method: "reactions.remove", ts: opener, name: notify.StatusWorking.Symbol()},
		mark{method: "reactions.remove", ts: opener, name: notify.StatusBlocked.Symbol()},
		mark{method: "reactions.remove", ts: opener, name: notify.StatusCompleted.Symbol()},
		mark{method: "reactions.add", ts: opener, name: notify.StatusInReview.Symbol()})
	wantWearing(t, posts, opener, notify.StatusInReview)

	// A restart is a second sink over the same durable state: what the opener is
	// already marked with is remembered, so the item moving once more leaves it
	// wearing the new status and nothing else.
	posts.marks = nil
	feed.statuses["work-item:yoyodyne-ifd.68.3"] = notify.StatusCompleted
	if err := newTestSink(t, root, feed, posts).pass(context.Background()); err != nil {
		t.Fatalf("restarted pass() error = %v", err)
	}
	wantWearing(t, posts, opener, notify.StatusCompleted)
}

// The record of which mark is on a thread is written after the workspace has
// taken it, so a sink killed between the two leaves a record naming a status the
// message is not wearing. The mark that is actually there has to come off anyway:
// a removal aimed at what the record named would leave the real one on the opener
// for good, saying "working" under a run that failed hours ago.
func TestAMarkTheRecordDoesNotNameIsStillTakenOff(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	posts := &recordedPosts{}
	feed := &fixedFeed{
		deliveries: []Delivery{milestone(1, notify.KindRunStarted)},
		statuses:   map[string]notify.Status{"work-item:yoyodyne-ifd.68.3": notify.StatusWorking},
	}
	sink := newTestSink(t, root, feed, posts)
	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	opener := posts.timestamps[0]
	wantWearing(t, posts, opener, notify.StatusWorking)

	// The write that would have said so never landed: the opener wears working
	// and the durable record still names the status before it.
	store, err := NewStore(root, testProduct)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	threads, err := store.LoadThreads()
	if err != nil {
		t.Fatalf("LoadThreads() error = %v", err)
	}
	stale := threads.Threads["work-item:yoyodyne-ifd.68.3"]
	stale.Status = notify.StatusInReview
	threads.Record("work-item:yoyodyne-ifd.68.3", stale)
	if err := store.SaveThreads(threads); err != nil {
		t.Fatalf("SaveThreads() error = %v", err)
	}

	feed.statuses["work-item:yoyodyne-ifd.68.3"] = notify.StatusBlocked
	if err := newTestSink(t, root, feed, posts).pass(context.Background()); err != nil {
		t.Fatalf("second pass() error = %v", err)
	}
	wantWearing(t, posts, opener, notify.StatusBlocked)
}

// A sink stopped while a mark is waiting out the pace is not a workspace
// refusing anything. The line it would otherwise log is the one the setup
// document teaches an operator to read as a missing scope, so a shutdown must
// not print it — a diagnosis somebody has to rule out later is worse than
// silence on the way out.
func TestASinkStoppedWhileMarkingSaysNothingAboutARefusal(t *testing.T) {
	t.Parallel()

	posts := &recordedPosts{}
	sink := newTestSink(t, t.TempDir(), &fixedFeed{}, posts)
	var said []string
	sink.log = func(format string, args ...any) { said = append(said, fmt.Sprintf(format, args...)) }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// The pace is the only blocking call in a mark, so it is where a shutdown is
	// actually met: the wait is made due and the sink stopped inside it.
	sink.pace.next = time.Now().Add(time.Hour)
	sink.pace.sleep = func(context.Context, time.Duration) error {
		cancel()
		return context.Canceled
	}

	topic := "work-item:yoyodyne-ifd.68.3"
	threads := ThreadMap{Threads: map[string]Thread{
		topic: {Channel: "C1", ThreadTS: "1755.0001"},
	}}
	sink.mark(ctx, &threads, map[string]notify.Status{topic: notify.StatusWorking})

	if len(said) != 0 {
		t.Fatalf("said %q on the way out, want a shutdown to say nothing about a refusal", said)
	}
	if sink.marking != "" {
		t.Fatalf("marking = %q, want a shutdown not remembered as a standing refusal", sink.marking)
	}
}

// wantWearing checks that a message carries exactly one status and that it is
// this one. Two at once is the failure worth naming: a thread that says both
// working and blocked is worse than one that says neither.
func wantWearing(t *testing.T, posts *recordedPosts, ts string, status notify.Status) {
	t.Helper()
	worn := posts.wearing[ts]
	if len(worn) != 1 || !worn[status.Symbol()] {
		t.Fatalf("the opener wears %v, want %q alone", worn, status.Symbol())
	}
}

// A topic nobody has said anything about has no thread and nothing to mark.
// Opening one to carry a status would put a thread in the channel whose whole
// content is that nothing has happened yet.
func TestATopicWithNoThreadIsNotMarked(t *testing.T) {
	t.Parallel()

	posts := &recordedPosts{}
	feed := &fixedFeed{statuses: map[string]notify.Status{
		"work-item:yoyodyne-ifd.68.3": notify.StatusWorking,
	}}
	if err := newTestSink(t, t.TempDir(), feed, posts).pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	if len(posts.requests) != 0 || len(posts.marks) != 0 {
		t.Fatalf("posted %#v and marked %#v, want an item nothing has been said about left alone", posts.requests, posts.marks)
	}
}

// Marking is not a delivery and it is never a gate. A workspace that refuses the
// reaction — an app installed before the manifest asked for the scope — costs
// the channel its status board and not one message; and because nothing durable
// says the mark went on, it goes on by itself once somebody reinstalls, without
// the item having to move again.
func TestAWorkspaceThatRefusesAMarkStillGetsEveryMessage(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	posts := &recordedPosts{refuseMarks: "missing_scope"}
	feed := &fixedFeed{
		deliveries: []Delivery{milestone(1, notify.KindRunStarted)},
		statuses:   map[string]notify.Status{"work-item:yoyodyne-ifd.68.3": notify.StatusBlocked},
	}
	sink := newTestSink(t, root, feed, posts)

	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v, want a refused mark to cost the pass nothing", err)
	}
	if len(posts.requests) != 2 {
		t.Fatalf("posts = %d, want the thread opened and the milestone in it", len(posts.requests))
	}

	posts.refuseMarks = ""
	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("second pass() error = %v", err)
	}
	wantWearing(t, posts, posts.timestamps[0], notify.StatusBlocked)
}

// What became of a directive somebody asked for in a thread is the one message
// the pass posts for a person rather than for the channel, and both halves of
// that have to survive the pass: the text reaches them by name, and the reply
// they typed stops wearing the thinking face at the moment the outcome is said.
func TestAnOutcomeThePassSaysTagsWhoAskedAndSettlesTheMarkOnTheirReply(t *testing.T) {
	t.Parallel()

	const member = "U0OPERATOR"
	const askTS = "1750000001.000200"
	posts := &recordedPosts{}
	feed := &fixedFeed{deliveries: []Delivery{outcome(1, member, askTS)}}
	if err := newTestSink(t, t.TempDir(), feed, posts).pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}

	if len(posts.requests) != 2 {
		t.Fatalf("posts = %#v, want the thread opened and the outcome said in it", posts.requests)
	}
	said := posts.requests[1]
	if !strings.HasPrefix(said.Text, "<@"+member+"> ") {
		t.Fatalf("said %q, want the outcome to reach the person who asked by name", said.Text)
	}
	if !strings.Contains(said.Text, "the second one, and the design says so") {
		t.Fatalf("said %q, want the tag in front of what became of it rather than instead of it", said.Text)
	}
	if worn := posts.wearing[askTS]; !worn[notify.ReceiptSettled.Symbol()] || len(worn) != 1 {
		t.Fatalf("the reply that asked wears %#v, want the settled mark alone once the outcome was said", worn)
	}
}

// outcome is what the feed hands the sink when the record says a directive
// somebody asked for in a thread has been settled: said in their thread, tagged
// to them, and carrying the reply that asked so its mark can move.
func outcome(position uint64, member, replyTS string) Delivery {
	settled := moment
	recorded := directive.Directive{
		SchemaVersion: directive.SchemaVersion,
		ID:            "directive-" + strings.Repeat("f", 32),
		ProductID:     testProduct,
		Kind:          directive.KindAmbiguous,
		ReceivedBy:    domain.RoleProductManager,
		ReceivedAt:    moment,
		Text:          "ambiguous: which of the two branches did you mean",
		Unresolved:    "which of the two branches did you mean",
		Scope:         []string{"yoyodyne-ifd.68.3"},
		Resolution:    "the second one, and the design says so",
		ResolvedAt:    &settled,
	}
	topic := notify.Topic{Kind: notify.TopicWorkItem, ID: "yoyodyne-ifd.68.3"}
	return Delivery{
		Stream:       directiveStream,
		Cursor:       Cursor{Position: position},
		Mention:      member,
		Reply:        replyTS,
		Notification: acknowledged(topic, notify.KindDirectiveResolved, recorded, settled),
	}
}

func wantMarks(t *testing.T, got []mark, want ...mark) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("marks = %#v, want %#v", got, want)
	}
	for index, marked := range want {
		if got[index] != marked {
			t.Fatalf("marks = %#v, want %#v", got, want)
		}
	}
}

// fixedFeed is a feed with a fixed answer, so the sink's own behavior is what a
// test exercises rather than the reading of durable records. Every delivery
// carries a position, which is how it knows what a cursor has already covered.
type fixedFeed struct {
	deliveries []Delivery
	// statuses is what each topic is doing at the end of this pass, which is a
	// reading rather than a history: a test moves it and polls again exactly as a
	// record moving underneath the sink would.
	statuses map[string]notify.Status
}

func (f *fixedFeed) Poll(_ context.Context, cursors Cursors) (Batch, error) {
	batch := Batch{Streams: map[string]struct{}{}, Statuses: f.statuses}
	for _, delivery := range f.deliveries {
		batch.Streams[delivery.Stream] = struct{}{}
		if delivery.Cursor.Position <= cursors.Streams[delivery.Stream].Position {
			continue
		}
		batch.Deliveries = append(batch.Deliveries, delivery)
	}
	return batch, nil
}

// mark is one reaction the workspace was asked to put on or take off a message:
// which call, which message, and which emoji.
type mark struct {
	method string
	ts     string
	name   string
}

// recordedPosts is the workspace: what it was asked to post, what it was asked
// to mark, and what it refused.
type recordedPosts struct {
	// mutex is here because the real thing is a web service and this stands in for
	// one: a sink posts from its delivery loop and from the connection answering a
	// reply, and a workspace that could not take two calls at once would be the
	// double failing rather than the sink.
	mutex      sync.Mutex
	requests   []postRequest
	timestamps []string
	// marks is every reaction call in the order it was made, which is what says a
	// stale mark came off before the new one went on.
	marks []mark
	// wearing is what each message actually carries once those calls have been
	// applied. It is kept as well as the calls because the two answer different
	// questions: the calls say what the sink did, and this says what somebody
	// scanning the channel would see — which is the only thing that is wrong when
	// a mark is orphaned.
	wearing map[string]map[string]bool
	// refuseMarks, when set, is the error every reaction call is refused with —
	// an app installed before the manifest asked for the scope, which is what
	// every workspace looks like the first time it runs a sink that marks.
	refuseMarks string
	// allow, when set, is how many posts this workspace accepts before it
	// starts refusing — which is how a test puts an outage exactly where it
	// matters. It has to refuse every attempt rather than one, because a
	// transient refusal is retried inside the call.
	allow int
	count int
	// opens is every member a direct-message conversation was asked for, in
	// order, and refuseOpen is the Slack error every one of those is refused
	// with — an app installed before the manifest asked for `im:write`, which is
	// also the configuration the setup document offers to anybody who would
	// rather not be messaged.
	opens       []string
	refuseOpen  string
	directPosts []postRequest
}

func (r *recordedPosts) handle(writer http.ResponseWriter, request *http.Request) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	// A running sink asks the workspace who it is before it posts anything, and
	// that call carries no body at all.
	if request.Body == nil {
		writeJSON(writer, map[string]any{"ok": true, "team": "test", "user": "yoyodyne"})
		return
	}
	body, _ := io.ReadAll(request.Body)
	if method := request.URL.Path[strings.LastIndex(request.URL.Path, "/")+1:]; method == "conversations.open" {
		var opened directRequest
		if err := json.Unmarshal(body, &opened); err != nil {
			writeJSON(writer, map[string]any{"ok": false, "error": "invalid_request"})
			return
		}
		if r.refuseOpen != "" {
			writeJSON(writer, map[string]any{"ok": false, "error": r.refuseOpen})
			return
		}
		r.opens = append(r.opens, opened.Users)
		writeJSON(writer, map[string]any{"ok": true, "channel": map[string]any{"id": "D" + opened.Users}})
		return
	}
	if method := request.URL.Path[strings.LastIndex(request.URL.Path, "/")+1:]; strings.HasPrefix(method, "reactions.") {
		var reaction reactionRequest
		if err := json.Unmarshal(body, &reaction); err != nil {
			writeJSON(writer, map[string]any{"ok": false, "error": "invalid_request"})
			return
		}
		if r.refuseMarks != "" {
			writeJSON(writer, map[string]any{"ok": false, "error": r.refuseMarks})
			return
		}
		r.marks = append(r.marks, mark{method: method, ts: reaction.Timestamp, name: reaction.Name})
		// The workspace answers the way Slack does: a mark that is already there
		// and one that is already off are refusals rather than successes, which is
		// what a sweep over the vocabulary meets three times out of four.
		if r.wearing == nil {
			r.wearing = map[string]map[string]bool{}
		}
		if r.wearing[reaction.Timestamp] == nil {
			r.wearing[reaction.Timestamp] = map[string]bool{}
		}
		worn := r.wearing[reaction.Timestamp]
		if method == "reactions.add" {
			if worn[reaction.Name] {
				writeJSON(writer, map[string]any{"ok": false, "error": "already_reacted"})
				return
			}
			worn[reaction.Name] = true
		} else {
			if !worn[reaction.Name] {
				writeJSON(writer, map[string]any{"ok": false, "error": "no_reaction"})
				return
			}
			delete(worn, reaction.Name)
		}
		writeJSON(writer, map[string]any{"ok": true})
		return
	}
	var decoded postRequest
	if err := json.Unmarshal(body, &decoded); err != nil {
		writeJSON(writer, map[string]any{"ok": false, "error": "invalid_request"})
		return
	}
	r.count++
	if r.allow > 0 && r.count > r.allow {
		writeJSON(writer, map[string]any{"ok": false, "error": "internal_error"})
		return
	}
	r.requests = append(r.requests, decoded)
	if strings.HasPrefix(decoded.Channel, "D") {
		r.directPosts = append(r.directPosts, decoded)
	}
	ts := "1755.000" + strconv.Itoa(r.count)
	r.timestamps = append(r.timestamps, ts)
	writeJSON(writer, map[string]any{"ok": true, "ts": ts})
}

// refusingPosts is a workspace that refuses every post with one named Slack
// error until somebody fixes what it is complaining about.
type refusingPosts struct {
	mutex sync.Mutex
	code  string
	count int
}

func (r *refusingPosts) handle(writer http.ResponseWriter, request *http.Request) {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	if request.Body == nil {
		writeJSON(writer, map[string]any{"ok": true, "team": "test", "user": "yoyodyne"})
		return
	}
	r.count++
	if r.code != "" {
		writeJSON(writer, map[string]any{"ok": false, "error": r.code})
		return
	}
	writeJSON(writer, map[string]any{"ok": true, "ts": "1755.0001"})
}

func (r *refusingPosts) attempts() int {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return r.count
}

// invite is the operator doing the thing the refusal asked them to do.
func (r *refusingPosts) invite() {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	r.code = ""
}

// waitFor spends real time rather than fake time, because what these tests are
// about is a loop running repeatedly. The bound is generous and the condition is
// reached in milliseconds when the code is right.
func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("the sink never reached the state this test is about")
		}
		time.Sleep(time.Millisecond)
	}
}

// fixedTitles is the tracker as the sink sees it: what each item is called, or
// one refusal to say. Every question it was asked is kept, so a sink that asks
// the same thread's name twice is visible rather than merely wasteful.
type fixedTitles struct {
	titles map[string]string
	err    error
	asked  []string
}

func (f *fixedTitles) Title(_ context.Context, workItemID string) (string, error) {
	f.asked = append(f.asked, workItemID)
	if f.err != nil {
		return "", f.err
	}
	return f.titles[workItemID], nil
}

func newTestSink(t *testing.T, root string, feed Feed, posts *recordedPosts) *Sink {
	t.Helper()
	return newTestSinkAt(t, root, feed, posts, time.Time{})
}

// newTestSinkWithTitles is a sink that can ask what an item is called, which is
// every sink the harness builds and none of the ones above: what the rest are
// about is what the records carry.
func newTestSinkWithTitles(t *testing.T, root string, feed Feed, posts *recordedPosts, titles Titles) *Sink {
	t.Helper()
	sink := newTestSink(t, root, feed, posts)
	sink.titles = titles
	return sink
}

// newTestSinkAt is a sink that says the time is whatever a test needs it to be,
// which is the only way to watch a watermark not move.
func newTestSinkAt(t *testing.T, root string, feed Feed, posts *recordedPosts, now time.Time) *Sink {
	t.Helper()
	store, err := NewStore(root, testProduct)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	options := Options{
		Channel: "C1",
		Store:   store,
		API:     newTestAPI(t, posts.handle),
		Feed:    feed,
		Log:     func(string, ...any) {},
	}
	if !now.IsZero() {
		options.Now = func() time.Time { return now }
	}
	sink, err := New(options)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	// The pace is real time in a running sink and none at all here, so a test
	// about what is posted does not spend a second per message proving it. The
	// pace itself is what TestPostingIsHeldToWhatSlackKeepsAccepting is for.
	sink.pace.sleep = func(context.Context, time.Duration) error { return nil }
	return sink
}

func milestone(position uint64, kind notify.Kind) Delivery {
	return Delivery{
		Stream: "run:run-a",
		Cursor: Cursor{Position: position},
		Notification: notify.Notification{
			Topic:   notify.Topic{Kind: notify.TopicWorkItem, ID: "yoyodyne-ifd.68.3"},
			Speaker: notify.Harness(),
			Event: notify.Event{
				Kind:     kind,
				At:       time.Now(),
				Severity: report.SeverityNote,
				Refs:     notify.Refs{RunID: "run-a", WorkItemID: "yoyodyne-ifd.68.3"},
			},
		},
	}
}

// filedReport is one agent's report against a work item, at the severity it was
// filed under. It is what a test needs to watch severity decide anything: the
// milestones above are all notes.
func filedReport(position uint64, severity report.Severity, text string) Delivery {
	return Delivery{
		Stream: reportStream,
		Cursor: Cursor{Position: position},
		Notification: notify.Notification{
			Topic:   notify.Topic{Kind: notify.TopicWorkItem, ID: "yoyodyne-ifd.68.12"},
			Speaker: notify.Persona(domain.RoleDeveloper, ""),
			Event: notify.Event{
				Kind:     notify.KindReportFiled,
				At:       time.Now(),
				Severity: severity,
				Refs:     notify.Refs{RunID: "run-a", WorkItemID: "yoyodyne-ifd.68.12"},
				Text:     text,
			},
		},
	}
}

func workItemTopic(t *testing.T, id string) notify.Topic {
	t.Helper()
	topic, err := notify.WorkItem(id)
	if err != nil {
		t.Fatalf("WorkItem(%q) error = %v", id, err)
	}
	return topic
}
