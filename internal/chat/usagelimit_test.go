package chat

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A turn the provider declined for want of capacity leaves a durable trace, so
// the refusal reaches somebody who is not at this terminal. It is the whole of
// what changes: the turn fails exactly as it did before, and nothing here waits.
func TestARefusedTurnRecordsWhatIsWaitingAndUntilWhen(t *testing.T) {
	t.Parallel()

	resetsAt := time.Date(2026, 8, 20, 2, 0, 0, 0, time.UTC)
	limits := newTestUsageLimits(t)
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{{
		IsError:    true,
		StopReason: "usage_limit",
		UsageLimit: &backendapi.UsageLimit{Kind: "five_hour", ResetsAt: resetsAt},
	}}})
	options.UsageLimits = limits
	session := openTestSession(t, options)
	if _, err := session.Send(context.Background(), "what is next?"); err == nil {
		t.Fatal("Send() error = nil, want the refused turn still failed")
	}

	recorded, err := limits.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 1 {
		t.Fatalf("List() = %#v, want the refusal recorded once", recorded)
	}
	refusal := recorded[0]
	// What is waiting has to name the conversation: a refused architect and a
	// refused product manager are the same limit stopping different work.
	if !strings.Contains(refusal.Waiting, "product manager") || !strings.Contains(refusal.Waiting, session.Evidence().ConversationID) {
		t.Fatalf("waiting = %q, want the conversation that was stopped", refusal.Waiting)
	}
	if refusal.Kind != "five_hour" {
		t.Fatalf("kind = %q, want the provider's own name for the limit", refusal.Kind)
	}
	if refusal.ResetsAt == nil || !refusal.ResetsAt.Equal(resetsAt) {
		t.Fatalf("resets at = %v, want when the provider said it lifts", refusal.ResetsAt)
	}
	if refusal.ConversationID != session.Evidence().ConversationID {
		t.Fatalf("conversation = %q, want the way back to the record", refusal.ConversationID)
	}
}

// A limit reported beside an answer the provider still gave stopped nothing, and
// a turn that failed for anything else is not a refusal. Recording either would
// put hours of silence in the channel that nobody is actually waiting through.
func TestOnlyARefusedTurnIsRecordedAsOne(t *testing.T) {
	t.Parallel()

	answered := newTestUsageLimits(t)
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{{
		SessionID:  "session-1",
		FinalText:  "Here it is.",
		UsageLimit: &backendapi.UsageLimit{Kind: "five_hour"},
	}}})
	options.UsageLimits = answered
	if _, err := openTestSession(t, options).Send(context.Background(), "what is next?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if recorded, err := answered.List(); err != nil || len(recorded) != 0 {
		t.Fatalf("List() = %#v, error %v, want an answered turn recorded as no refusal", recorded, err)
	}

	failed := newTestUsageLimits(t)
	other := testOptions(t, &fakeBackend{results: []backendapi.RunResult{{IsError: true, StopReason: "max_turns"}}})
	other.UsageLimits = failed
	if _, err := openTestSession(t, other).Send(context.Background(), "what is next?"); err == nil {
		t.Fatal("Send() error = nil, want the failed turn still failed")
	}
	if recorded, err := failed.List(); err != nil || len(recorded) != 0 {
		t.Fatalf("List() = %#v, error %v, want a failure that is not a refusal recorded as none", recorded, err)
	}
}

// A conversation with nowhere to record a refusal fails the turn exactly as it
// always did. Reporting is observation: nothing about a conversation depends on
// somebody having wired a log to it.
func TestAConversationWithNoRefusalLogStillFailsTheTurn(t *testing.T) {
	t.Parallel()

	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{{
		IsError:    true,
		StopReason: "usage_limit",
		UsageLimit: &backendapi.UsageLimit{Kind: "five_hour"},
	}}})
	if _, err := openTestSession(t, options).Send(context.Background(), "what is next?"); err == nil {
		t.Fatal("Send() error = nil, want the refused turn still failed")
	}
}

// A turn the provider refuses for want of capacity waits and is asked again,
// which is what a run has always done and what a conversation used to do none
// of. The reset is 45 minutes out and the probe interval is thirty, so the turn
// asks again beneath the deadline rather than sleeping to it — the same polling
// discipline, because a quoted reset is a claim that goes stale in both
// directions.
func TestARefusedTurnWaitsOutTheLimitAndIsAskedAgain(t *testing.T) {
	t.Parallel()

	clock := &waitingClock{now: fixedClock{}.Now()}
	limits := newTestUsageLimits(t)
	provider := &fakeBackend{results: []backendapi.RunResult{
		refusedForCapacity(clock.now.Add(45 * time.Minute)),
		{SessionID: "session-1", FinalText: "Two goals, then."},
	}}
	options := waitingOptions(testOptions(t, provider), clock)
	options.UsageLimits = limits
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "what is next?")
	if err != nil {
		t.Fatalf("Send() error = %v, want the turn waited out the limit and completed", err)
	}
	if reply.Text != "Two goals, then." {
		t.Fatalf("reply = %q, want the answer the reissued turn gave", reply.Text)
	}
	if clock.waited() != 30*time.Minute {
		t.Fatalf("waited %s, want the 30m probe beneath the 45m reset", clock.waited())
	}
	if len(provider.requests) != 2 {
		t.Fatalf("invocations = %d, want the refused turn asked again exactly once", len(provider.requests))
	}
	// The reissue is the same question on the same conversation, not a new one:
	// what was refused was one invocation, and nothing about the turn changed
	// while it waited.
	if provider.requests[1].Prompt != provider.requests[0].Prompt {
		t.Fatalf("reissued prompt = %q, want the prompt that was refused", provider.requests[1].Prompt)
	}
	// Waiting does not replace the record. An exhausted limit is every process's
	// problem for as long as it lasts, and somebody who is not at this terminal
	// still has to be able to read that it happened.
	recorded, err := limits.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 1 || recorded[0].Kind != "five_hour" {
		t.Fatalf("recorded = %#v, want the refusal written down once", recorded)
	}
}

// A limit reported without a reset time is unknown rather than unwaitable — the
// overage allowance reports this way while the ordinary rolling window keeps
// resetting on its usual schedule — so a turn waits the configured interval and
// asks again, exactly as a run does.
func TestATurnPollsAUsageLimitThatNamesNoResetTime(t *testing.T) {
	t.Parallel()

	clock := &waitingClock{now: fixedClock{}.Now()}
	provider := &fakeBackend{results: []backendapi.RunResult{
		refusedForCapacity(time.Time{}),
		{SessionID: "session-1", FinalText: "Two goals, then."},
	}}
	session := openTestSession(t, waitingOptions(testOptions(t, provider), clock))

	if _, err := session.Send(context.Background(), "what is next?"); err != nil {
		t.Fatalf("Send() error = %v, want a limit with no reset time waited out rather than failed on", err)
	}
	if clock.waited() != 30*time.Minute {
		t.Fatalf("waited %s, want the configured 30m interval between attempts", clock.waited())
	}
}

// The tracker actions a message already applied were applied by a round that
// finished. A reissued invocation continues the same session that holds them, so
// what is asked again is the one invocation the provider refused — not the work
// the turn had already done.
func TestTrackerActionsAppliedBeforeAWaitAreNotRepeatedAfterIt(t *testing.T) {
	t.Parallel()

	clock := &waitingClock{now: fixedClock{}.Now()}
	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.22": {ID: "yoyodyne-ifd.22", Title: "Make the conversation readable", Status: "open"},
	}}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Closing it.",
			`{"action":"close","id":"yoyodyne-ifd.22","reason":"the work landed"}`)},
		// The continuation that carries the results back is the invocation the
		// provider refuses, which is exactly where the reported loss happened.
		refusedForCapacity(clock.now.Add(45 * time.Minute)),
		{SessionID: "session-1", FinalText: "It is closed."},
	}}
	options := waitingOptions(testOptions(t, provider), clock)
	options.Tracker = tracker
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "is ifd.22 done?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(tracker.closed) != 1 {
		t.Fatalf("closed items = %#v, want the item closed once across the wait", tracker.closed)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("invocations = %d, want only the refused continuation asked again", len(provider.requests))
	}
	if !strings.Contains(reply.Text, "It is closed.") {
		t.Fatalf("reply = %q, want the answer the reissued continuation gave", reply.Text)
	}
}

// A wait the harness will not take fails the turn rather than becoming a guessed
// one, and says what the provider reported: an operator who knows when it lifts
// knows when to say this again. A run parks here instead, because it has durable
// state a later invocation continues from and a conversation has none.
func TestALimitTheHarnessWillNotWaitOutFailsWithTheResetStated(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		resetsAt func(now time.Time) time.Time
		maximum  time.Duration
		want     []string
	}{
		{
			// A limit still refusing work while naming a reset already behind us is
			// not describing a wait, and reissuing into it would spin.
			name:     "a reset that is not in the future",
			resetsAt: func(now time.Time) time.Time { return now.Add(-time.Minute) },
			maximum:  6 * time.Hour,
			want:     []string{"an exhausted five_hour usage limit", "not in the future"},
		},
		{
			// The budget covers the message rather than one wait, so a probe that no
			// longer fits it stops the turn instead of being taken anyway.
			name:     "a wait past what the message may spend",
			resetsAt: func(now time.Time) time.Time { return now.Add(45 * time.Minute) },
			maximum:  10 * time.Minute,
			want:     []string{"an exhausted five_hour usage limit", "past the 10m0s it may spend waiting"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			clock := &waitingClock{now: fixedClock{}.Now()}
			resetsAt := test.resetsAt(clock.now)
			provider := &fakeBackend{results: []backendapi.RunResult{refusedForCapacity(resetsAt)}}
			options := waitingOptions(testOptions(t, provider), clock)
			options.UsageLimitPause.Maximum = test.maximum
			options.UsageLimitPause.InProcess = test.maximum

			_, err := openTestSession(t, options).Send(context.Background(), "what is next?")
			var refused *UsageLimitError
			if !errors.As(err, &refused) {
				t.Fatalf("Send() error = %v, want a refusal the operator can read a reset time off", err)
			}
			for _, required := range append(test.want, resetsAt.UTC().Format(time.RFC3339)) {
				if !strings.Contains(refused.Error(), required) {
					t.Fatalf("refusal = %q, want it to contain %q", refused.Error(), required)
				}
			}
			if clock.waited() != 0 {
				t.Fatalf("waited %s, want a wait the harness refused to be a wait it did not take", clock.waited())
			}
			if len(provider.requests) != 1 {
				t.Fatalf("invocations = %d, want the refused turn not reissued", len(provider.requests))
			}
		})
	}
}

// A turn that is waiting says so on the display. Without it a turn waiting out
// hours of provider silence looks exactly like a hung command, which is the
// thing the activity display exists to tell apart.
func TestAWaitingTurnSaysSoOnTheDisplay(t *testing.T) {
	t.Parallel()

	clock := &waitingClock{now: fixedClock{}.Now()}
	provider := &fakeBackend{results: []backendapi.RunResult{
		refusedForCapacity(clock.now.Add(45 * time.Minute)),
		{SessionID: "session-1", FinalText: "Two goals, then."},
	}}
	session := openTestSession(t, waitingOptions(testOptions(t, provider), clock))

	var out strings.Builder
	if err := session.Converse(context.Background(), testConsole(strings.NewReader("what is next?\n/exit\n"), &out)); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}
	transcript := out.String()
	for _, required := range []string{
		"… waiting out an exhausted five_hour usage limit; asking again at ",
		"Two goals, then.",
	} {
		if !strings.Contains(transcript, required) {
			t.Fatalf("transcript = %q, want it to contain %q", transcript, required)
		}
	}
}

// A wait probes the same closed window at the configured interval and is refused
// again every time. One limit is written down once for as long as one message is
// waiting it out, because each record is reported into Slack as a warning meaning
// hours in which nothing will happen — and a dozen of those reads as a dozen
// separate stoppages rather than one that is still going. A refusal that says
// something new is new information and is written down.
func TestOneLimitIsRecordedOnceHoweverManyProbesAWaitTakes(t *testing.T) {
	t.Parallel()

	clock := &waitingClock{now: fixedClock{}.Now()}
	limits := newTestUsageLimits(t)
	// The window stays closed under three probes and then the provider quotes a
	// reset that has moved, which is the one thing here somebody has not been told.
	moved := clock.now.Add(4 * time.Hour)
	provider := &fakeBackend{results: []backendapi.RunResult{
		refusedForCapacity(clock.now.Add(3 * time.Hour)),
		refusedForCapacity(clock.now.Add(3 * time.Hour)),
		refusedForCapacity(clock.now.Add(3 * time.Hour)),
		refusedForCapacity(moved),
		{SessionID: "session-1", FinalText: "Two goals, then."},
	}}
	options := waitingOptions(testOptions(t, provider), clock)
	options.UsageLimits = limits
	session := openTestSession(t, options)

	if _, err := session.Send(context.Background(), "what is next?"); err != nil {
		t.Fatalf("Send() error = %v, want the turn waited out the limit and completed", err)
	}
	if len(provider.requests) != 5 {
		t.Fatalf("invocations = %d, want each probe to have been a real attempt", len(provider.requests))
	}
	recorded, err := limits.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 2 {
		t.Fatalf("recorded = %#v, want the repeated refusal written down once and the moved reset written down", recorded)
	}
	if recorded[1].ResetsAt == nil || !recorded[1].ResetsAt.Equal(moved.UTC()) {
		t.Fatalf("second record = %#v, want the reset the provider moved to", recorded[1])
	}

	// A later message meets the limit again, and is told about it again: the same
	// window is a fresh stoppage once the operator is waiting on something else.
	provider.results = append(provider.results, refusedForCapacity(moved), backendapi.RunResult{SessionID: "session-1", FinalText: "Still there."})
	if _, err := session.Send(context.Background(), "and after that?"); err != nil {
		t.Fatalf("Send() second error = %v", err)
	}
	if recorded, err = limits.List(); err != nil || len(recorded) != 3 {
		t.Fatalf("recorded = %#v, error %v, want the next message told about the limit it met", recorded, err)
	}
}

// A wait lasts hours, which is exactly long enough for the operator to pause the
// harness while one is happening. Every provider call this conversation makes
// reads that pause first, and a reissue is one — otherwise a wait would be a
// pause the operator could watch themselves spend through.
func TestAWaitDoesNotReissueThroughAPausePlacedWhileItWaited(t *testing.T) {
	t.Parallel()

	clock := &waitingClock{now: fixedClock{}.Now()}
	holds := &fakeHolds{hold: runstate.OperatorHold{SchemaVersion: runstate.OperatorHoldSchemaVersion, HeldAt: testHeldAt}}
	provider := &fakeBackend{results: []backendapi.RunResult{
		refusedForCapacity(clock.now.Add(45 * time.Minute)),
		{SessionID: "session-1", FinalText: "Two goals, then."},
	}}
	options := waitingOptions(testOptions(t, provider), clock)
	options.Holds = holds
	// The operator places the pause during the wait, which is the only moment
	// this covers: a pause placed before the turn refuses it before any provider
	// is asked anything.
	options.Sleep = func(ctx context.Context, duration time.Duration) error {
		holds.held = true
		return clock.sleep(ctx, duration)
	}

	_, err := openTestSession(t, options).Send(context.Background(), "what is next?")
	var held *OperatorHoldError
	if !errors.As(err, &held) {
		t.Fatalf("Send() error = %v, want the reissue refused by the pause placed during the wait", err)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("invocations = %d, want the wait not spent through the operator's pause", len(provider.requests))
	}
}

// refusedForCapacity is the result the provider returns for a turn it declined
// for want of capacity: an errored invocation carrying the limit and, where it
// named one, when it lifts.
func refusedForCapacity(resetsAt time.Time) backendapi.RunResult {
	return backendapi.RunResult{
		IsError:    true,
		StopReason: "usage_limit",
		UsageLimit: &backendapi.UsageLimit{Kind: "five_hour", ResetsAt: resetsAt},
	}
}

// waitingOptions wires a conversation to a hand-driven clock and the bounds a
// run waits under, so a test states exactly how long the harness may wait
// without spending any of it.
func waitingOptions(options Options, clock *waitingClock) Options {
	options.Clock = clock
	options.Sleep = clock.sleep
	options.UsageLimitPause = UsageLimitPause{
		Maximum:   6 * time.Hour,
		InProcess: 6 * time.Hour,
		Probe:     30 * time.Minute,
	}
	return options
}

// waitingClock is a clock the test moves by sleeping on it, so a wait can be
// driven to its end without spending the time. What it records is what the turn
// actually committed to waiting, which is what makes "it waited and asked again"
// checkable rather than assumed.
type waitingClock struct {
	now   time.Time
	slept []time.Duration
}

func (c *waitingClock) Now() time.Time { return c.now }

func (c *waitingClock) sleep(_ context.Context, duration time.Duration) error {
	c.slept = append(c.slept, duration)
	c.now = c.now.Add(duration)
	return nil
}

func (c *waitingClock) waited() time.Duration {
	var total time.Duration
	for _, slice := range c.slept {
		total += slice
	}
	return total
}

func newTestUsageLimits(t *testing.T) *runstate.UsageLimitStore {
	t.Helper()

	store, err := runstate.NewUsageLimitStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewUsageLimitStore() error = %v", err)
	}
	return store
}
