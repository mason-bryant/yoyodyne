package chat

// A conversation whose provider session has grown too long to continue.
//
// These read what actually reached the provider on the attempt after the refusal,
// for the reason the crossing tests do: the fresh session has never seen this
// conversation, so what it is handed is entirely what the harness wrote down.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// The turn this exists for: the session is refused as too long, and the turn is
// served in a fresh session under the same conversation, rebuilt from the record,
// with the replacement and its reason on the record.
func TestASessionRefusedAsTooLongContinuesInAFreshSessionRebuiltFromTheRecord(t *testing.T) {
	t.Parallel()

	provider := &speakingBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: "Two goals, then."},
		{IsError: true, FinalText: "Prompt is too long"},
		{SessionID: "session-2", FinalText: "The second one first."},
		{SessionID: "session-2", FinalText: "Still the second one."},
	}}
	options := testOptions(t, provider)
	session := openTestSession(t, options)
	conversation := session.Evidence().ConversationID

	if _, err := session.Send(context.Background(), "what should we do?"); err != nil {
		t.Fatalf("Send() error = %v on the first turn", err)
	}
	reply, err := session.Send(context.Background(), "and after that?")
	if err != nil {
		t.Fatalf("Send() error = %v, want the turn served in a fresh session", err)
	}
	if !strings.Contains(reply.Text, "The second one first.") {
		t.Fatalf("reply = %q, want the answer the fresh session gave", reply.Text)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("provider asked %d times, want the first turn, the refused attempt, and the fresh one", len(provider.requests))
	}
	if refused := provider.requests[1]; refused.SessionID != "session-1" {
		t.Fatalf("refused attempt resumed %q, want the conversation's session", refused.SessionID)
	}
	fresh := provider.requests[2]
	if fresh.SessionID != "" {
		t.Fatalf("fresh attempt resumed %q, want no session: the one on the record was refused", fresh.SessionID)
	}
	for _, want := range []string{rebuiltContextHeader, sessionSetAside, "Two goals, then.", "what should we do?", testBriefing, "and after that?"} {
		if !strings.Contains(fresh.Prompt, want) {
			t.Fatalf("fresh prompt = %q, want it to carry %q", fresh.Prompt, want)
		}
	}
	// The refused attempt is the thing being replaced, not something said.
	if strings.Contains(fresh.Prompt, "Prompt is too long") {
		t.Fatalf("fresh prompt = %q, want the refused attempt left out of the rebuild", fresh.Prompt)
	}
	if strings.Contains(fresh.Prompt, crossedProviders) {
		t.Fatalf("fresh prompt = %q, want it not to claim another provider was holding the conversation", fresh.Prompt)
	}

	// Same conversation, new session.
	if session.Evidence().ConversationID != conversation {
		t.Fatalf("conversation = %q, want it unchanged from %q", session.Evidence().ConversationID, conversation)
	}
	recorded, err := options.Store.Load(options.identity())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if recorded.ConversationID != conversation || recorded.ProviderSessionID != "session-2" || recorded.Turns != 2 {
		t.Fatalf("recorded = %#v, want the same conversation holding the fresh session after two turns", recorded)
	}

	events, err := options.Store.LoadEvents(conversation)
	if err != nil {
		t.Fatalf("LoadEvents() error = %v", err)
	}
	var replacements []execution.Event
	for _, event := range events {
		if event.Type == execution.EventSessionReplaced {
			replacements = append(replacements, event)
		}
	}
	if len(replacements) != 1 {
		t.Fatalf("recorded %d session replacements, want one", len(replacements))
	}
	var payload struct {
		Replaced string `json:"replaced_session"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal(replacements[0].Payload, &payload); err != nil {
		t.Fatalf("session.replaced payload: %v", err)
	}
	if payload.Replaced != "session-1" || !strings.Contains(payload.Reason, "Prompt is too long") {
		t.Fatalf("session.replaced = %+v, want the session set aside and what the provider said", payload)
	}

	// The turn after it resumes the fresh session as any other would.
	if _, err := session.Send(context.Background(), "is that still right?"); err != nil {
		t.Fatalf("Send() error = %v on the turn after the replacement", err)
	}
	if next := provider.requests[3]; next.SessionID != "session-2" || strings.Contains(next.Prompt, rebuiltContextHeader) {
		t.Fatalf("next turn resumed %q with prompt %q, want the fresh session resumed and nothing rebuilt", next.SessionID, next.Prompt)
	}
}

// A compaction the provider could not make, reported as an invocation error
// rather than a flagged result, is answered the same way.
func TestAFailedCompactionReportedAsAnErrorIsAnsweredWithAFreshSession(t *testing.T) {
	t.Parallel()

	provider := &failingOnceBackend{
		speakingBackend: speakingBackend{results: []backendapi.RunResult{
			{SessionID: "session-1", FinalText: "Two goals, then."},
			{},
			{SessionID: "session-2", FinalText: "Carrying on."},
		}},
		failOn: 1,
		err:    errors.New("claude exited: Error during compaction: Error: Conversation too long."),
	}
	session := openTestSession(t, testOptions(t, provider))
	if _, err := session.Send(context.Background(), "what should we do?"); err != nil {
		t.Fatalf("Send() error = %v on the first turn", err)
	}
	if _, err := session.Send(context.Background(), "and after that?"); err != nil {
		t.Fatalf("Send() error = %v, want the turn served in a fresh session", err)
	}
	if len(provider.requests) != 3 || provider.requests[2].SessionID != "" ||
		!strings.Contains(provider.requests[2].Prompt, "Two goals, then.") {
		t.Fatalf("requests = %d, want a third, sessionless and rebuilt from the record", len(provider.requests))
	}
}

// A fresh session refused the same way ends the turn rather than looping, and
// leaves nothing on the record to resume: the next turn rebuilds again instead of
// going back to the session that was refused.
func TestAFreshSessionRefusedToEndsTheTurnAndTheNextTurnRebuildsAgain(t *testing.T) {
	t.Parallel()

	provider := &speakingBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: "Two goals, then."},
		{IsError: true, FinalText: "Prompt is too long"},
		{IsError: true, FinalText: "Prompt is too long"},
		{SessionID: "session-2", FinalText: "Back again."},
	}}
	options := testOptions(t, provider)
	session := openTestSession(t, options)
	if _, err := session.Send(context.Background(), "what should we do?"); err != nil {
		t.Fatalf("Send() error = %v on the first turn", err)
	}
	if _, err := session.Send(context.Background(), "and after that?"); err == nil {
		t.Fatal("Send() error = nil, want the turn failed when the fresh session was refused too")
	}
	if len(provider.requests) != 3 {
		t.Fatalf("provider asked %d times, want one replacement and no more", len(provider.requests))
	}
	recorded, err := options.Store.Load(options.identity())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if recorded.ProviderSessionID != "" {
		t.Fatalf("recorded session = %q, want none: the refused session was set aside", recorded.ProviderSessionID)
	}
	if _, err := session.Send(context.Background(), "try again?"); err != nil {
		t.Fatalf("Send() error = %v on the turn after", err)
	}
	next := provider.requests[3]
	if next.SessionID != "" || !strings.Contains(next.Prompt, sessionSetAside) || !strings.Contains(next.Prompt, "Two goals, then.") {
		t.Fatalf("next turn resumed %q with prompt %q, want it rebuilt from the record for a fresh session", next.SessionID, next.Prompt)
	}
}

// Other refusals are not read as length, and neither is a reply that talks about
// length: only a provider refusing the session starts a fresh one.
func TestOnlyALengthRefusalIsReadAsOne(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		result backendapi.RunResult
		err    error
		want   bool
	}{
		"flagged prompt too long": {result: backendapi.RunResult{IsError: true, FinalText: "Prompt is too long"}, want: true},
		"request too large": {result: backendapi.RunResult{IsError: true, StopReason: "api_error",
			FinalText: `API Error: 413 {"type":"error","error":{"type":"request_too_large","message":"Request exceeds the maximum size"}}`}, want: true},
		"codex context length":   {err: errors.New("stream error: context_length_exceeded"), want: true},
		"unflagged notice":       {result: backendapi.RunResult{FinalText: "Prompt is too long"}, want: true},
		"overload":               {result: backendapi.RunResult{IsError: true, FinalText: "API Error: 529 overloaded"}},
		"answer about length":    {result: backendapi.RunResult{FinalText: "The prompt is too long for the reviewer; " + strings.Repeat("split it. ", 80)}},
		"successful short reply": {result: backendapi.RunResult{FinalText: "Done."}},
	}
	for name, tc := range cases {
		if got := refusedAsTooLong(tc.result, tc.err) != ""; got != tc.want {
			t.Errorf("%s: refusedAsTooLong() = %v, want %v", name, got, tc.want)
		}
	}
}

// failingOnceBackend is speakingBackend with one attempt failing as an
// invocation error, the way a provider process that died reports.
type failingOnceBackend struct {
	speakingBackend
	failOn int
	err    error
}

func (f *failingOnceBackend) Run(ctx context.Context, request backendapi.RunRequest) (backendapi.RunResult, error) {
	index := len(f.requests)
	result, err := f.speakingBackend.Run(ctx, request)
	if index == f.failOn {
		return backendapi.RunResult{LastEvent: result.LastEvent}, f.err
	}
	return result, err
}
