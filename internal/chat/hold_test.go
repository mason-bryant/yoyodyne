package chat

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

var testHeldAt = time.Date(2026, 8, 18, 18, 15, 0, 0, time.UTC)

// A turn is a provider invocation, so the operator's pause covers it. It is
// refused with the reason stated rather than held: there is a person in front of
// this conversation, and the one thing worse than not answering them is not
// telling them why.
func TestAHeldConversationRefusesTheTurnAndSaysWhy(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: "answered"},
	}}
	options := testOptions(t, provider)
	holds := &fakeHolds{hold: runstate.OperatorHold{SchemaVersion: runstate.OperatorHoldSchemaVersion, HeldAt: testHeldAt}, held: true}
	options.Holds = holds
	session := openTestSession(t, options)

	_, err := session.Send(context.Background(), "What is missing from the brief?")
	var refused *OperatorHoldError
	if !errors.As(err, &refused) {
		t.Fatalf("Send() error = %v, want a turn refused by the operator's hold", err)
	}
	if !strings.Contains(refused.Error(), testHeldAt.Format(time.RFC3339)) || !strings.Contains(refused.Error(), "yoyo resume") {
		t.Fatalf("refusal = %q, want it to say when the pause was placed and what lifts it", refused)
	}
	if len(provider.requests) != 0 {
		t.Fatalf("a held conversation spent a turn anyway: %#v", provider.requests)
	}

	// Lifting the pause is all it takes: the message that was refused is the one
	// the next turn answers, and nothing about the conversation was lost.
	holds.held = false
	reply, err := session.Send(context.Background(), "What is missing from the brief?")
	if err != nil {
		t.Fatalf("Send() after the pause was lifted error = %v", err)
	}
	if reply.Text != "answered" || len(provider.requests) != 1 {
		t.Fatalf("reply = %#v over %d request(s), want the refused turn taken once", reply, len(provider.requests))
	}
}

// A refused turn does not end the conversation. Nothing went wrong: the
// operator paused their own harness, and they are one command from carrying on.
func TestAHeldTurnLeavesTheConversationOpen(t *testing.T) {
	t.Parallel()

	options := testOptions(t, &fakeBackend{})
	options.Holds = &fakeHolds{hold: runstate.OperatorHold{SchemaVersion: runstate.OperatorHoldSchemaVersion, HeldAt: testHeldAt}, held: true}
	session := openTestSession(t, options)

	var out strings.Builder
	if err := session.Converse(context.Background(), testConsole(strings.NewReader("what is missing?\nand what else?\n/exit\n"), &out)); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}
	transcript := out.String()
	if refusals := strings.Count(transcript, "all harness activity is paused"); refusals != 2 {
		t.Fatalf("transcript = %q, want both messages refused with the conversation still open", transcript)
	}
}

// A paused harness leads the status report. Every group in it describes a queue
// that is not moving, and a system somebody paused and forgot looks exactly like
// a system that died.
func TestStatusLeadsWithThePauseWhileItIsHeld(t *testing.T) {
	t.Parallel()

	options := testOptions(t, &fakeBackend{})
	options.Work = &fakeWork{survey: Survey{}}
	options.Holds = &fakeHolds{hold: runstate.OperatorHold{SchemaVersion: runstate.OperatorHoldSchemaVersion, HeldAt: testHeldAt}, held: true}
	session := openTestSession(t, options)

	var out strings.Builder
	if err := session.Converse(context.Background(), testConsole(strings.NewReader("/status\n/exit\n"), &out)); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}
	transcript := out.String()
	banner := strings.Index(transcript, "PAUSED: all harness activity is paused, since "+testHeldAt.Format(time.RFC3339))
	if banner < 0 {
		t.Fatalf("transcript = %q, want a paused banner naming when the pause was placed", transcript)
	}
	if survey := strings.Index(transcript, "in flight"); survey >= 0 && survey < banner {
		t.Fatalf("transcript = %q, want the pause before the work it is holding up", transcript)
	}
	if !strings.Contains(transcript, "yoyo resume") {
		t.Fatalf("transcript = %q, want the banner to say what lifts the pause", transcript)
	}
}

// "I could not find out whether we are paused" and "we are not paused" lead to
// opposite conclusions about the same silence, so the status report says which
// of them it is rather than passing over the difference.
func TestStatusSaysWhenThePauseCannotBeRead(t *testing.T) {
	t.Parallel()

	options := testOptions(t, &fakeBackend{})
	options.Work = &fakeWork{survey: Survey{}}
	options.Holds = &fakeHolds{err: errors.New("the operator hold is unreadable")}
	session := openTestSession(t, options)

	var out strings.Builder
	if err := session.Converse(context.Background(), testConsole(strings.NewReader("/status\n/exit\n"), &out)); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}
	if !strings.Contains(out.String(), "COULD NOT READ THE PAUSE") {
		t.Fatalf("transcript = %q, want a status report that says the pause could not be read", out.String())
	}
}

// A conversation wired without a hold to read is one nothing can pause, which is
// what every conversation was before the switch existed.
func TestAConversationWithNoHoldToReadIsNeverPaused(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{results: []backendapi.RunResult{{SessionID: "session-1", FinalText: "answered"}}}
	session := openTestSession(t, testOptions(t, provider))
	if _, err := session.Send(context.Background(), "What is missing from the brief?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("requests = %d, want the turn taken", len(provider.requests))
	}
}

// A run the operator parked is not a run a provider refused, and the headline
// is where a conversation says which. Describing a hold as an exhausted limit
// would send the operator looking at their account for something they did
// themselves.
func TestRunReportHeadlineTellsAHoldFromAProviderRefusal(t *testing.T) {
	t.Parallel()

	report := RunReport{WorkItemID: "yoyodyne-9", Paused: true, PauseCause: runstate.PauseOperatorHold, OperatorHeldSince: &testHeldAt}
	headline := report.Headline()
	for _, want := range []string{"you paused all harness activity", testHeldAt.Format(time.RFC3339), "yoyo resume", "/work yoyodyne-9"} {
		if !strings.Contains(headline, want) {
			t.Fatalf("Headline() = %q, want it to say %q", headline, want)
		}
	}
	for _, unwanted := range []string{"usage limit", "overload", "asks again"} {
		if strings.Contains(headline, unwanted) {
			t.Fatalf("Headline() = %q, want it not to describe a provider refusal (%q)", headline, unwanted)
		}
	}
}

type fakeHolds struct {
	hold runstate.OperatorHold
	held bool
	err  error
}

func (f *fakeHolds) Held() (runstate.OperatorHold, bool, error) {
	if f.err != nil {
		return runstate.OperatorHold{}, false, f.err
	}
	return f.hold, f.held, nil
}
