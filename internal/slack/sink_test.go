package slack

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

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
// record carried — the sink never asks the tracker anything.
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

// A record that carried no title heads its thread with the identifier alone,
// which is exactly what every thread was before titles were carried: a header
// with a dangling separator would read as a name somebody failed to write.
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

// The product a sink reports on is what its speakers are named with, so a sink
// assembled without one would post names that say a role spoke without saying
// whose. It is refused at assembly rather than discovered in a channel.
func TestASinkWithoutAProductIsRefusedAtAssembly(t *testing.T) {
	t.Parallel()

	store, err := NewStore(t.TempDir(), testProduct)
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	api, err := NewAPI("xoxb-test", "xapp-test")
	if err != nil {
		t.Fatalf("NewAPI() error = %v", err)
	}
	if _, err := New(Options{Channel: "C1", Store: store, API: api, Feed: &fixedFeed{}}); err == nil {
		t.Fatal("New() without a product = nil, want a refusal")
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

// fixedFeed is a feed with a fixed answer, so the sink's own behavior is what a
// test exercises rather than the reading of durable records. Every delivery
// carries a position, which is how it knows what a cursor has already covered.
type fixedFeed struct {
	deliveries []Delivery
}

func (f *fixedFeed) Poll(_ context.Context, cursors Cursors) (Batch, error) {
	batch := Batch{Streams: map[string]struct{}{}}
	for _, delivery := range f.deliveries {
		batch.Streams[delivery.Stream] = struct{}{}
		if delivery.Cursor.Position <= cursors.Streams[delivery.Stream].Position {
			continue
		}
		batch.Deliveries = append(batch.Deliveries, delivery)
	}
	return batch, nil
}

// recordedPosts is the workspace: what it was asked to post, and what it
// refused.
type recordedPosts struct {
	requests   []postRequest
	timestamps []string
	// allow, when set, is how many posts this workspace accepts before it
	// starts refusing — which is how a test puts an outage exactly where it
	// matters. It has to refuse every attempt rather than one, because a
	// transient refusal is retried inside the call.
	allow int
	count int
}

func (r *recordedPosts) handle(writer http.ResponseWriter, request *http.Request) {
	// A running sink asks the workspace who it is before it posts anything, and
	// that call carries no body at all.
	if request.Body == nil {
		writeJSON(writer, map[string]any{"ok": true, "team": "test", "user": "yoyodyne"})
		return
	}
	body, _ := io.ReadAll(request.Body)
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

func newTestSink(t *testing.T, root string, feed Feed, posts *recordedPosts) *Sink {
	t.Helper()
	return newTestSinkAt(t, root, feed, posts, time.Time{})
}

// newTestSinkAt is a sink that says the time is whatever a test needs it to be,
// which is the only way to watch a watermark not move.
func newTestSinkAt(t *testing.T, root string, feed Feed, posts *recordedPosts, now time.Time) *Sink {
	t.Helper()
	store, err := NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	options := Options{
		Channel: "C1",
		Product: testProduct,
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

func workItemTopic(t *testing.T, id string) notify.Topic {
	t.Helper()
	topic, err := notify.WorkItem(id)
	if err != nil {
		t.Fatalf("WorkItem(%q) error = %v", id, err)
	}
	return topic
}
