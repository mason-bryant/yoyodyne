package slack

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// The thing the operator asked for: an improvement nobody would otherwise hear
// about arrives, once, as a message they are sent rather than one they have to
// go and look for.
func TestEachNewlyAvailableImprovementIsSaidToTheOperatorsOnce(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	offers := harness.offering(
		improved("agents.developer.persona.text", "a0c1", "9f3e"),
		improved("execution.max_concurrent_developers", "1", "2"),
	)

	said := harness.improvements(t, harness.start())
	if len(said) != 2 {
		t.Fatalf("said %d improvements, want one message per newly-available value", len(said))
	}
	for _, delivery := range said {
		if !delivery.Direct {
			t.Fatalf("%q was said in the channel alone; the operator asked to be sent each one",
				delivery.Notification.Event.Detail.Setting)
		}
		if severity := delivery.Notification.Event.Severity; severity != report.SeverityNote {
			t.Fatalf("severity = %q, want a note: nothing is degraded and nobody is waiting", severity)
		}
	}

	// And never again, whatever else moves. The second pass is a whole heartbeat
	// later, which is exactly when a surface that dedups by nothing would repeat
	// itself.
	cursors := harness.start()
	cursors.Streams[improvementStream] = said[len(said)-1].Cursor
	for hour := 0; hour < 5; hour++ {
		harness.now = harness.now.Add(time.Hour)
		cursors = harness.poll(t, cursors)
	}
	if offers.asked < 2 {
		t.Fatalf("the comparison was made %d times; the silence should be a reading that found nothing new", offers.asked)
	}
}

// The dedup is a file rather than a process's memory, which is the whole of what
// the ruling's advisory-once class asks for: a sink restarted overnight must not
// re-announce what it said before it died.
func TestAnImprovementAlreadySaidIsSilentAfterARestart(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.offering(improved("agents.reviewer.model", "sonnet", "opus"))

	said := harness.improvements(t, harness.start())
	if len(said) != 1 {
		t.Fatalf("said %d improvements, want the one that is available", len(said))
	}
	mark := said[0].Cursor
	if len(mark.Delivered) != 1 || !strings.HasPrefix(mark.Delivered[0], improvementMark) {
		t.Fatalf("cursor delivered %v, want having said it recorded durably", mark.Delivered)
	}

	// A restart reads the cursors back off disk and knows nothing else. What
	// stands between it and saying everything again is exactly that file.
	restarted := newTestHarness(t, time.Time{})
	restarted.offering(improved("agents.reviewer.model", "sonnet", "opus"))
	restarted.now = harness.now.Add(3 * time.Hour)
	cursors := restarted.start()
	cursors.Streams[improvementStream] = mark
	restarted.poll(t, cursors)
}

// One setting improved twice is two improvements. A mark that held the key alone
// would swallow the second one for the life of the project, which is the failure
// that makes "once per improvement" different from "once per key".
func TestATemplateThatImprovesOneSettingAgainSaysSoAgain(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	offers := harness.offering(improved("agents.reviewer.model", "sonnet", "opus"))

	said := harness.improvements(t, harness.start())
	cursors := harness.start()
	cursors.Streams[improvementStream] = said[0].Cursor

	offers.values = []config.Value{improved("agents.reviewer.model", "sonnet", "opus-5")}
	harness.now = harness.now.Add(time.Hour)
	again := harness.improvements(t, cursors)
	if len(again) != 1 {
		t.Fatalf("said %d improvements, want the second offer on the same setting", len(again))
	}
	if body := harness.rendered(t, again[0]); !strings.Contains(body, "opus-5") {
		t.Fatalf("body %q does not carry what the template supplies now", body)
	}
	// And the mark for the offer that is over went with it, so the cursor is a
	// record of what stands rather than of everything ever said.
	if delivered := again[0].Cursor.Delivered; len(delivered) != 1 {
		t.Fatalf("cursor delivered %v, want the superseded offer forgotten", delivered)
	}
}

// What the project changed is the project's. The only class this speaks is the
// one the comparison calls available, and it is the comparison that decides
// which that is rather than anything here.
func TestOnlyValuesTheProjectNeverEditedAreOffered(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.offeringDrift(config.Drift{
		Known:  true,
		Bundle: "yoyodyne",
		Values: []config.Value{
			{Key: "agents.developer.model", Class: config.ClassYours, Baseline: "sonnet", Yours: "opus", Bundle: "sonnet"},
			{Key: "execution.poll", Class: config.ClassConflicting, Baseline: "15s", Yours: "5s", Bundle: "30s"},
			{Key: "agents.reviewer.model", Class: config.ClassUnchanged, Baseline: "sonnet", Yours: "opus", Bundle: "opus"},
		},
	})
	harness.poll(t, harness.start())
}

// Reading the comparison means reading the project's configuration and the
// baseline beside it, so it is asked once a heartbeat rather than once a poll.
// A sink polling every fifteen seconds on a fact nobody is waiting for is cost
// bought for nothing.
func TestTheComparisonIsMadeOnceAHeartbeatRatherThanOnceAPoll(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	offers := harness.offering()

	cursors := harness.poll(t, harness.start())
	for minute := 0; minute < 10; minute++ {
		harness.now = harness.now.Add(15 * time.Second)
		cursors = harness.poll(t, cursors)
	}
	if offers.asked != 1 {
		t.Fatalf("the comparison was made %d times in a quarter of an hour, want once", offers.asked)
	}
	harness.now = harness.now.Add(time.Hour)
	harness.poll(t, cursors)
	if offers.asked != 2 {
		t.Fatalf("the comparison was made %d times, want it asked again a heartbeat later", offers.asked)
	}
}

// A comparison that cannot be made is not a project that is current. It must not
// guess in either direction, so it says so where the sink says everything else
// about itself and asks again at the next interval rather than at the next poll.
func TestAComparisonThatCannotBeMadeIsSaidInTheSinksOwnLog(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	var logged []string
	harness.feed.Log = func(format string, args ...any) { logged = append(logged, format) }
	harness.feed.Improvements = brokenImprovements{}

	cursors := harness.poll(t, harness.start())
	if len(logged) != 1 {
		t.Fatalf("logged %v, want the refusal said once", logged)
	}
	harness.now = harness.now.Add(15 * time.Second)
	harness.poll(t, cursors)
	if len(logged) != 1 {
		t.Fatalf("logged %v, want it asked again at the next interval rather than the next poll", logged)
	}
}

// A feed assembled without a way to read the comparison says everything else and
// never mentions the template, which is every sink built before this existed.
func TestAFeedWithNoComparisonSaysNothingAboutTheTemplate(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	cursors := harness.poll(t, harness.start())
	if _, carried := cursors.Streams[improvementStream]; carried {
		t.Fatal("a cursor was kept for a comparison this sink has no way to make")
	}
}

// What the message says is what somebody deciding about it needs: which setting,
// what it was, what it is, and that nothing whatever is waiting on them.
func TestTheMessageNamesTheSettingAndBothValues(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.offering(improved("agents.developer.model", "sonnet", "opus"))

	said := harness.improvements(t, harness.start())
	body := harness.rendered(t, said[0])
	for _, fact := range []string{"agents.developer.model", "sonnet", "opus", "yoyodyne"} {
		if !strings.Contains(body, fact) {
			t.Fatalf("body %q does not carry %q", body, fact)
		}
	}
	if !strings.Contains(body, "nobody's") {
		t.Fatalf("body %q does not say the move is nobody's; an offer nobody has to take must not read as one somebody does", body)
	}
}

// improved is one value the template moved that the project never edited, which
// is the only class any of this speaks.
func improved(key, was, now string) config.Value {
	return config.Value{Key: key, Class: config.ClassAvailable, Baseline: was, Yours: was, Bundle: now}
}

// offering gives the feed a comparison that offers these values and nothing
// else, and returns it so a test can move it or count how often it was asked.
func (h *testHarness) offering(values ...config.Value) *offeredImprovements {
	offers := &offeredImprovements{values: values}
	h.feed.Improvements = offers
	return offers
}

// offeringDrift gives the feed one whole comparison, for the tests that are
// about which class is spoken rather than about what is said.
func (h *testHarness) offeringDrift(drift config.Drift) {
	h.feed.Improvements = &offeredImprovements{drift: drift}
}

// improvements makes one pass and returns what it said about the template.
func (h *testHarness) improvements(t *testing.T, cursors Cursors) []Delivery {
	t.Helper()
	batch, err := h.feed.Poll(context.Background(), cursors)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	var said []Delivery
	for _, delivery := range batch.Deliveries {
		if delivery.Stream == improvementStream && !delivery.Silent() {
			said = append(said, delivery)
		}
	}
	if len(said) == 0 {
		t.Fatal("nothing was said about what the template has improved")
	}
	return said
}

// rendered is one delivery as the channel would have it, which is where a test
// reads what a persona actually says rather than only which kind was selected.
func (h *testHarness) rendered(t *testing.T, delivery Delivery) string {
	t.Helper()
	message, err := notify.Render(delivery.Notification.Topic, delivery.Notification.Speaker, delivery.Notification.Event)
	if err != nil {
		t.Fatalf("a selected notification could not be said: %v", err)
	}
	return message.Body
}

// offeredImprovements is a comparison a test states outright, counting how often
// it was asked so the cost rule can be checked as well as the messages.
type offeredImprovements struct {
	values []config.Value
	drift  config.Drift
	asked  int
}

func (o *offeredImprovements) Offered(context.Context) (config.Drift, error) {
	o.asked++
	if o.drift.Known {
		return o.drift, nil
	}
	return config.Drift{Known: true, Bundle: "yoyodyne", Values: o.values}, nil
}

type brokenImprovements struct{}

func (brokenImprovements) Offered(context.Context) (config.Drift, error) {
	return config.Drift{}, errors.New("the baseline beside this configuration could not be read")
}
