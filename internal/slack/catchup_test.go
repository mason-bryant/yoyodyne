package slack

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// Slack does not delay an application that posts faster than it tolerates, it
// hides what overflowed. So the sink posts at the rate the workspace sustains
// rather than as fast as the records can be read — which is the difference
// between a catch-up that arrives late and one that is dropped from view.
func TestPostingIsHeldToWhatSlackKeepsAccepting(t *testing.T) {
	t.Parallel()

	posts := &recordedPosts{}
	sink := newTestSink(t, t.TempDir(), &fixedFeed{deliveries: []Delivery{
		milestone(1, notify.KindRunStarted),
		milestone(2, notify.KindChecksPassed),
		milestone(3, notify.KindReviewApproved),
	}}, posts)

	// A clock that only moves when the pace waits, so what the test sees is the
	// pacing rather than however long a test machine took.
	at := time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC)
	var waits []time.Duration
	sink.pace.now = func() time.Time { return at }
	sink.pace.sleep = func(_ context.Context, wait time.Duration) error {
		waits = append(waits, wait)
		at = at.Add(wait)
		return nil
	}

	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	if len(posts.requests) != 4 {
		t.Fatalf("posts = %d, want the thread opened and all three milestones in it", len(posts.requests))
	}
	// The first message goes immediately — a sink with nothing behind it is not
	// something the workspace is being protected from.
	if len(waits) != 3 {
		t.Fatalf("waits = %v, want one before each message after the first", waits)
	}
	for _, wait := range waits {
		if wait != DefaultPostInterval {
			t.Fatalf("waited %s between messages, want %s", wait, DefaultPostInterval)
		}
	}
}

// Twelve hours of cursors replayed in full is hundreds of messages: the
// workspace suppresses the overflow and nobody scrolls what survives. So a
// backlog that deep is said once per thread, with the durable record named as
// what holds the rest.
func TestADeepBacklogIsDigestedPerThreadRatherThanReplayed(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	items := []string{"yoyodyne-ifd.68.7", "yoyodyne-ifd.68.8", "yoyodyne-ifd.68.9"}
	var deliveries []Delivery
	for step := 0; step < 40; step++ {
		recorded := now.Add(-12 * time.Hour).Add(time.Duration(step) * 15 * time.Minute)
		for _, item := range items {
			deliveries = append(deliveries, aged(item, uint64(step+1), recorded, report.SeverityNote))
		}
	}

	posts := &recordedPosts{}
	root := t.TempDir()
	feed := &fixedFeed{deliveries: deliveries}
	if err := newTestSinkAt(t, root, feed, posts, now).pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}

	// One thread opened and one digest in it, for each of the three items — not
	// the hundred and twenty messages the records hold.
	if len(posts.requests) != 2*len(items) {
		t.Fatalf("posts = %d, want a thread and a digest for each of the %d items", len(posts.requests), len(items))
	}
	for index := 1; index < len(posts.requests); index += 2 {
		digest := posts.requests[index].Text
		if !strings.Contains(digest, "40 events") {
			t.Fatalf("digest = %q, want it to say how many events it stands for", digest)
		}
		if !strings.Contains(digest, "9 hours") {
			t.Fatalf("digest = %q, want it to say the span it covers", digest)
		}
		if !strings.Contains(digest, "record") {
			t.Fatalf("digest = %q, want the durable record named as the full account", digest)
		}
	}

	// A digested delivery is reported rather than skipped, so its cursor advances
	// with everything else: a second pass has nothing left to say, and nothing is
	// digested a second time.
	posts.requests = nil
	if err := newTestSinkAt(t, root, feed, posts, now).pass(context.Background()); err != nil {
		t.Fatalf("second pass() error = %v", err)
	}
	if len(posts.requests) != 0 {
		t.Fatalf("second pass posted %#v, want a backlog that was digested to stay digested", posts.requests)
	}
}

// A digest is for accumulation, not for news. What happened in the last half
// hour is what somebody coming back to the channel is reading for, and a
// critical is the one thing a count must never stand in for — important
// findings standing out is what the surface exists to do.
func TestTheRecentWindowAndAnythingCriticalKeepFullFidelity(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	const item = "yoyodyne-ifd.68.8"
	var deliveries []Delivery
	for step := 0; step < deepBacklog+10; step++ {
		deliveries = append(deliveries, aged(item, uint64(step+1), now.Add(-8*time.Hour), report.SeverityNote))
	}

	blocked := aged(item, uint64(len(deliveries)+1), now.Add(-6*time.Hour), report.SeverityCritical)
	blocked.Notification.Event.Kind = notify.KindBlockerRecorded
	blocked.Notification.Event.Text = "the promotion lease was never released"
	recent := aged(item, uint64(len(deliveries)+2), now.Add(-5*time.Minute), report.SeverityNote)
	recent.Notification.Event.Kind = notify.KindPromoted
	deliveries = append(deliveries, blocked, recent)

	posts := &recordedPosts{}
	sink := newTestSinkAt(t, t.TempDir(), &fixedFeed{deliveries: deliveries}, posts, now)
	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}

	// The thread, one digest for everything old and ordinary, the critical said in
	// full, and the recent milestone said in full.
	if len(posts.requests) != 4 {
		t.Fatalf("posts = %d, want the thread, one digest, the critical, and the recent milestone", len(posts.requests))
	}
	said := strings.Join(textOf(posts.requests), "\n")
	if !strings.Contains(said, "the promotion lease was never released") {
		t.Fatalf("posted %q, want a critical said in its own words rather than counted", said)
	}
	if !strings.Contains(said, "promoted") {
		t.Fatalf("posted %q, want what happened inside the recent window said in full", said)
	}
}

// Below the depth that would flood a channel, nothing is digested at all: a
// digest standing in for a handful of messages says less than the messages did.
func TestABacklogAChannelCanCarryIsPostedInFull(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	const item = "yoyodyne-ifd.68.8"
	var deliveries []Delivery
	for step := 0; step < deepBacklog-1; step++ {
		deliveries = append(deliveries, aged(item, uint64(step+1), now.Add(-8*time.Hour), report.SeverityNote))
	}

	posts := &recordedPosts{}
	sink := newTestSinkAt(t, t.TempDir(), &fixedFeed{deliveries: deliveries}, posts, now)
	if err := sink.pass(context.Background()); err != nil {
		t.Fatalf("pass() error = %v", err)
	}
	if len(posts.requests) != len(deliveries)+1 {
		t.Fatalf("posts = %d, want the thread and every one of the %d messages", len(posts.requests), len(deliveries))
	}
}

// A digest is never quieter than the loudest thing it stands for: a warning
// collapsed into a count that reads as a note is a warning nobody sees.
func TestADigestIsMarkedByTheLoudestThingItStandsFor(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	var deliveries []Delivery
	for step := 0; step < deepBacklog; step++ {
		delivery := aged("yoyodyne-ifd.68.8", uint64(step+1), now.Add(-8*time.Hour), report.SeverityNote)
		if step == 7 {
			delivery.Notification.Event.Severity = report.SeverityWarning
		}
		deliveries = append(deliveries, delivery)
	}

	plan := planCatchUp(deliveries, now)
	digest, found := plan.digest[0]
	if !found {
		t.Fatalf("plan digested %d threads, want the one this backlog is about", len(plan.digest))
	}
	if digest.Event.Severity != report.SeverityWarning {
		t.Fatalf("digest severity = %q, want the loudest of what it collapsed", digest.Event.Severity)
	}
	if digest.Event.Detail.Accumulated != len(deliveries) {
		t.Fatalf("digest stands for %d events, want all %d of them", digest.Event.Detail.Accumulated, len(deliveries))
	}
}

// aged is one delivery about one item, recorded at a given moment: what a
// backlog is made of.
func aged(item string, position uint64, at time.Time, severity report.Severity) Delivery {
	return Delivery{
		Stream: "run:" + item,
		Cursor: Cursor{Position: position},
		Notification: notify.Notification{
			Topic:   notify.Topic{Kind: notify.TopicWorkItem, ID: item},
			Speaker: notify.Harness(),
			Event: notify.Event{
				Kind:     notify.KindChecksPassed,
				At:       at,
				Severity: severity,
				Refs:     notify.Refs{RunID: "run-" + item, WorkItemID: item},
			},
		},
	}
}

func textOf(requests []postRequest) []string {
	said := make([]string, 0, len(requests))
	for _, request := range requests {
		said = append(said, request.Text)
	}
	return said
}
