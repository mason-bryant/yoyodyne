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

	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// One thread per topic, held across restarts. The first thing said about a work
// item opens its thread and everything else about it replies into that thread —
// which is what makes a channel readable when three items are in flight at once.
func TestATopicOpensOneThreadAndEverythingElseRepliesIntoIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	posts := &recordedPosts{}
	feed := &fixedFeed{deliveries: []Delivery{
		milestone("run:run-a", "started", notify.KindRunStarted, "yoyodyne-ifd.68.3 started"),
		milestone("run:run-a", "checks.passed#0", notify.KindChecksPassed, "the checks passed"),
	}}
	sink := newTestSink(t, root, feed, posts)

	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("Pass() error = %v", err)
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
		t.Fatalf("second Pass() error = %v", err)
	}
	if len(posts.requests) != 0 {
		t.Fatalf("second pass posted %#v, want a thread that is a narrative rather than a repetition", posts.requests)
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
		milestone("run:run-a", "started", notify.KindRunStarted, "it started"),
		milestone("run:run-a", "promotion", notify.KindPromotion, "it was promoted"),
	}}
	sink := newTestSink(t, root, feed, posts)

	// The thread opens, the first milestone lands, and the second is refused.
	if err := sink.pass(context.Background()); err == nil {
		t.Fatal("Pass() = nil, want the refusal reported so the sink backs off")
	}
	posts.allow = 0
	posts.requests = nil
	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("second Pass() error = %v", err)
	}
	if len(posts.requests) != 1 {
		t.Fatalf("second pass posted %d, want only the message that never landed", len(posts.requests))
	}
	if !strings.Contains(posts.requests[0].Text, "it was promoted") {
		t.Fatalf("second pass posted %q, want the message the first pass could not", posts.requests[0].Text)
	}
}

// What is about the whole line — not any one item — is posted at the top level.
// Burying it in one item's thread would misfile it.
func TestProductLevelNewsIsNotBuriedInAnItemsThread(t *testing.T) {
	t.Parallel()

	posts := &recordedPosts{}
	sink := newTestSink(t, t.TempDir(), &fixedFeed{deliveries: []Delivery{{
		Stream:   reportStream,
		Position: 1,
		Envelope: notify.New(notify.KindReport, notify.ProductTopic, notify.Harness,
			report.SeverityNote, "intake was held", notify.Refs{}),
	}}}, posts)

	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("Pass() error = %v", err)
	}
	if len(posts.requests) != 1 {
		t.Fatalf("posts = %d, want one unthreaded message and no thread opened for it", len(posts.requests))
	}
	if posts.requests[0].ThreadTS != "" {
		t.Fatalf("post = %#v, want product news at the top level of the channel", posts.requests[0])
	}
}

// The words carry the severity and the icon only adds to it, so a client that
// renders no emoji still shows a critical as critical — and an ordinary note
// takes no marker at all, or the marker would stop meaning anything.
func TestSeverityIsCarriedByTheWordsRatherThanTheDecoration(t *testing.T) {
	t.Parallel()

	critical := renderText(notify.New(notify.KindBlocker, notify.ProductTopic, notify.Harness,
		report.SeverityCritical, "it stopped", notify.Refs{}))
	if !strings.Contains(critical, "critical") {
		t.Fatalf("rendered = %q, want the severity said in words", critical)
	}
	warning := renderText(notify.New(notify.KindRunParked, notify.ProductTopic, notify.Harness,
		report.SeverityWarning, "it is waiting", notify.Refs{}))
	if !strings.Contains(warning, "warning") {
		t.Fatalf("rendered = %q, want the severity said in words", warning)
	}
	note := renderText(notify.New(notify.KindRunStarted, notify.ProductTopic, notify.Harness,
		report.SeverityNote, "it started", notify.Refs{}))
	if strings.Contains(note, "note") {
		t.Fatalf("rendered = %q, want an ordinary fact to carry no marker", note)
	}
	// A message leads back to the record it was read from rather than standing
	// in for it.
	referenced := renderText(notify.New(notify.KindRunStarted, notify.ProductTopic, notify.Harness,
		report.SeverityNote, "it started", notify.Refs{Run: "run-a"}))
	if !strings.Contains(referenced, "run run-a") {
		t.Fatalf("rendered = %q, want the durable record named", referenced)
	}
}

// A body too long for Slack is truncated with a marker naming the record that
// holds the whole of it, and never split into a flood of messages to fit.
func TestAnOversizedMessageIsTruncatedAndSaysWhereTheRestIs(t *testing.T) {
	t.Parallel()

	posts := &recordedPosts{}
	sink := newTestSink(t, t.TempDir(), &fixedFeed{deliveries: []Delivery{{
		Stream: "run:run-a",
		Mark:   "report",
		Envelope: notify.New(notify.KindReport, notify.ProductTopic, notify.Harness,
			report.SeverityNote, strings.Repeat("a", maxTextBytes+2048), notify.Refs{Run: "run-a"}),
	}}}, posts)

	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("Pass() error = %v", err)
	}
	if len(posts.requests) != 1 {
		t.Fatalf("posts = %d, want one message rather than a flood of fragments", len(posts.requests))
	}
	text := posts.requests[0].Text
	if len(text) > maxTextBytes {
		t.Fatalf("posted %d bytes, want it inside the limit", len(text))
	}
	if !strings.Contains(text, "run run-a") {
		t.Fatalf("posted %q, want the marker to name the record that holds the whole", text)
	}
}

// A notification that cannot be posted must not hold up every later message on
// the same stream forever, so it is said once in the log and its cursor moves
// past it.
func TestAnUnpostableNotificationIsSkippedRatherThanRepeatedForever(t *testing.T) {
	t.Parallel()

	posts := &recordedPosts{}
	var logged []string
	sink := newTestSink(t, t.TempDir(), &fixedFeed{deliveries: []Delivery{
		{Stream: "run:run-a", Mark: "broken", Envelope: notify.Envelope{Kind: notify.KindRunStarted}},
		milestone("run:run-a", "started", notify.KindRunStarted, "it started"),
	}}, posts)
	sink.log = func(format string, args ...any) { logged = append(logged, format) }

	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("Pass() error = %v", err)
	}
	if len(logged) == 0 {
		t.Fatal("a notification that could not be posted must be said out loud")
	}
	// The thread and the message that followed it still went out.
	if len(posts.requests) != 2 {
		t.Fatalf("posts = %d, want the rest of the stream unaffected", len(posts.requests))
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
	feed := &fixedFeed{deliveries: []Delivery{
		milestone("run:run-a", "started", notify.KindRunStarted, "it started"),
	}}
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
		milestone("run:run-a", "started", notify.KindRunStarted, "it started"),
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
		milestone("run:run-a", "started", notify.KindRunStarted, "it started"),
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
// test exercises rather than the reading of durable records.
type fixedFeed struct {
	deliveries []Delivery
}

func (f *fixedFeed) Poll(_ context.Context, cursors Cursors) (Batch, error) {
	batch := Batch{Streams: map[string]struct{}{}}
	for _, delivery := range f.deliveries {
		batch.Streams[delivery.Stream] = struct{}{}
		cursor := cursors.Streams[delivery.Stream]
		if delivery.Mark != "" && cursor.Has(delivery.Mark) {
			continue
		}
		if delivery.Mark == "" && delivery.Position <= cursor.Position {
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
	store, err := NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	sink, err := New(Options{
		Channel: "C1",
		Store:   store,
		API:     newTestAPI(t, posts.handle),
		Feed:    feed,
		Log:     func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return sink
}

func milestone(stream, mark string, kind notify.Kind, body string) Delivery {
	return Delivery{
		Stream: stream,
		Mark:   mark,
		Envelope: notify.New(kind, notify.WorkItemTopic("yoyodyne-ifd.68.3"), notify.Harness,
			report.SeverityNote, body, notify.Refs{Run: "run-a", WorkItem: "yoyodyne-ifd.68.3"}),
	}
}
