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

func newTestUsageLimits(t *testing.T) *runstate.UsageLimitStore {
	t.Helper()

	store, err := runstate.NewUsageLimitStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewUsageLimitStore() error = %v", err)
	}
	return store
}

// A turn nobody was asked and a turn answered badly are owed opposite things, so
// the failure says which of the two it was. The harness now takes turns nobody
// is sitting in front of, and a caller that could not tell them apart would
// either give up on a role it never reached or keep paying to hear the same
// answer.
func TestARefusedTurnSaysTheRoleWasNeverAsked(t *testing.T) {
	t.Parallel()

	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{{
		IsError:    true,
		StopReason: "usage_limit",
		UsageLimit: &backendapi.UsageLimit{Kind: "five_hour"},
	}}})
	options.UsageLimits = newTestUsageLimits(t)
	_, err := openTestSession(t, options).Send(context.Background(), "what is next?")
	if !errors.Is(err, ErrProviderCapacity) {
		t.Fatalf("Send() error = %v, want it to say the provider declined the turn", err)
	}
	// And what a person reads is unchanged: the sentinel is joined to the failure
	// rather than replacing it.
	if !strings.Contains(err.Error(), "reported failure") {
		t.Fatalf("Send() error = %q, want the failure itself still in it", err)
	}

	// A turn that failed for anything else is not a refusal, and must not be
	// asked again as though the role had never seen it.
	other := testOptions(t, &fakeBackend{results: []backendapi.RunResult{{IsError: true, StopReason: "max_turns"}}})
	other.UsageLimits = newTestUsageLimits(t)
	_, err = openTestSession(t, other).Send(context.Background(), "what is next?")
	if err == nil || errors.Is(err, ErrProviderCapacity) {
		t.Fatalf("Send() error = %v, want a turn the provider answered badly reported as one", err)
	}
}
