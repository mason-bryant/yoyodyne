package slack

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The 2026-09-17 shape: the Claude Code login expired at 18:17 local, every
// dispatch was refused from then on, and nothing told the operator.
var septemberLoginExpired = time.Date(2026, 9, 17, 15, 17, 0, 0, time.UTC)

// loginExpires gives the feed the outage record and the sources the lines are
// read through, and records the first refused dispatch at the moment the login
// expired.
func (h *testHarness) loginExpires(t *testing.T) *runstate.ProviderOutageStore {
	t.Helper()
	outages, err := runstate.NewProviderOutageStore(h.root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewProviderOutageStore() error = %v", err)
	}
	h.feed.Outages = outages
	h.feed.Standing = &readmodel.Sources{
		Runs:            h.runs,
		Conversations:   h.chats,
		Tracker:         standingTracker{},
		Directives:      h.directives,
		Amendments:      h.amend,
		OperatorHolds:   h.holds,
		IntakeHolds:     h.intake,
		Sessions:        h.watch,
		UsageLimits:     h.limits,
		ProviderOutages: outages,
		Capacity:        3,
		Now:             func() time.Time { return h.now },
	}
	h.now = septemberLoginExpired
	if _, err := outages.Notice(runstate.ProviderOutageObservation{
		Cause:        domain.ProviderUnauthenticated,
		Provider:     domain.BackendClaudeCode,
		AccountAlias: "default",
		Detail:       "the claude-code backend is not authenticated for account \"default\"",
		Waiting:      "the dispatch of yoyodyne-ifd.377",
		At:           h.now,
	}); err != nil {
		t.Fatalf("Notice() error = %v", err)
	}
	return outages
}

// provider makes one pass and returns what it said about the provider, posting
// or not.
func (h *testHarness) provider(t *testing.T, cursors Cursors) (Delivery, bool) {
	t.Helper()
	batch, err := h.feed.Poll(context.Background(), cursors)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	for _, delivery := range batch.Deliveries {
		if delivery.Stream == providerStream {
			return delivery, true
		}
	}
	return Delivery{}, false
}

// The September outage replayed produces the message within one poll, tagged to
// the operators, naming the login; it is not said again while it stands; and
// re-authentication produces the one message more that says the line carried
// on. Nothing in any of it prescribes a release.
func TestAnExpiredLoginReachesTheOperatorsOnTheFirstPassAndOnceMoreWhenRenewed(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	outages := harness.loginExpires(t)
	harness.now = septemberLoginExpired.Add(15 * time.Second)

	cursors := harness.poll(t, harness.start(), notify.KindProviderOutage)
	said, found := harness.provider(t, harness.start())
	if !found || !said.Tag {
		t.Fatalf("delivery = %+v, found %v; want the outage tagged to the operators the first time it is seen", said, found)
	}
	if severity := said.Notification.Event.Severity; severity != report.SeverityWarning {
		t.Fatalf("severity = %q, want a warning", severity)
	}
	message, err := notify.Render(said.Notification.Topic, said.Notification.Speaker, said.Notification.Event)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	for _, fact := range []string{
		"The provider is not authenticated; the operator must log in",
		"claude-code, account default",
		"Next: the operator's",
		"log in to the provider",
		"nothing is released or restarted",
	} {
		if !strings.Contains(message.Body, fact) {
			t.Fatalf("body %q does not carry %q", message.Body, fact)
		}
	}
	if strings.Contains(message.Body, "yoyo release") {
		t.Fatalf("body %q prescribes a release, which lifts nothing here", message.Body)
	}
	if strings.Count(message.Body, "The provider is not authenticated") != 1 {
		t.Fatalf("body %q says the banner twice", message.Body)
	}
	if standing := cursors.Streams[providerStream].Standing; standing != "provider:unauthenticated:2026-09-17T15:17:00Z" {
		t.Fatalf("standing = %q, want the outage marked by its cause and when it began", standing)
	}

	// Told once. Three days of it standing is three days of silence on this
	// stream, however many passes are made.
	harness.now = septemberLoginExpired.Add(3 * 24 * time.Hour)
	if _, again := harness.provider(t, cursors); again {
		t.Fatal("the outage was said again while it stood, and it is said once")
	}
	// Nor does the heartbeat nag about it as a stopped line: the wait is named
	// in its own message, and every other line carries it as the banner.
	cursors = harness.poll(t, cursors)

	// The operator logs in. The next pass says the line carried on, once, as a
	// note, and the cursor forgets the outage so the next one is said afresh.
	if _, _, err := outages.Clear(); err != nil {
		t.Fatalf("Clear() error = %v", err)
	}
	harness.now = harness.now.Add(time.Minute)
	restored, found := harness.provider(t, cursors)
	if !found || restored.Notification.Event.Kind != notify.KindProviderRestored {
		t.Fatalf("delivery = %+v, found %v; want the provider answering again said once", restored, found)
	}
	if restored.Tag || restored.Direct {
		t.Fatalf("delivery = %+v, want the restoration said in the channel without interrupting anybody", restored)
	}
	message, err = notify.Render(restored.Notification.Topic, restored.Notification.Speaker, restored.Notification.Event)
	if err != nil {
		t.Fatalf("Render() error = %v", err)
	}
	if !strings.Contains(message.Body, "The provider is answering again after 3 days") || !strings.Contains(message.Body, "not authenticated") {
		t.Fatalf("body %q does not say what was restored and how long it stood", message.Body)
	}
	if restored.Cursor.Standing != "" {
		t.Fatalf("cursor = %+v, want the outage forgotten once it ended", restored.Cursor)
	}
	cursors = harness.poll(t, cursors, notify.KindProviderRestored)
	if _, more := harness.provider(t, cursors); more {
		t.Fatal("the restoration was said twice")
	}
}

// The operators are named by member id in the channel on the one message that
// is theirs to act on, and on nothing else.
func TestTheOutageMessageNamesEveryOperatorByMemberID(t *testing.T) {
	t.Parallel()

	if got := taggedAll([]string{"U1", " U2 ", ""}, "the line has stopped"); got != "<@U1> <@U2> the line has stopped" {
		t.Fatalf("taggedAll() = %q", got)
	}
	if got := taggedAll(nil, "as rendered"); got != "as rendered" {
		t.Fatalf("taggedAll() with nobody = %q, want the text as rendered", got)
	}
}
