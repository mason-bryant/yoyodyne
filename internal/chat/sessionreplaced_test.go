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
	"fmt"
	"strings"
	"testing"
	"time"

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

// A conversation whose recent messages are more than a fresh session will take:
// the rebuild at its full bound is refused too, and the turn is served on a
// rebuild half that size, which drops the oldest of what it carried and says so.
// Without the halving every later turn would rebuild the same refused context.
func TestARebuildRefusedAsTooLongIsHalvedOnceAndTheTurnIsServed(t *testing.T) {
	t.Parallel()

	// Between half the bound and the whole of it, so the full rebuild is refused
	// and the halved one is not.
	provider := &lengthLimitedBackend{limit: maxRebuiltContextBytes * 3 / 4}
	options := testOptions(t, provider)
	session := openTestSession(t, options)

	// Each side of each turn is recorded at the bound a message is cut to, so ten
	// turns are more than the rebuild's bound by some way.
	pad := func(label string) string {
		return label + " " + strings.Repeat("x", execution.MaxEventTextBytes)
	}
	const turns = 10
	for turn := 0; turn < turns; turn++ {
		provider.reply = pad(fmt.Sprintf("answer-%d", turn))
		if _, err := session.Send(context.Background(), pad(fmt.Sprintf("question-%d", turn))); err != nil {
			t.Fatalf("Send() error = %v on turn %d", err, turn+1)
		}
	}
	before := len(provider.requests)

	provider.reply, provider.refuses = "Served after all.", "session-1"
	reply, err := session.Send(context.Background(), "and now?")
	if err != nil {
		t.Fatalf("Send() error = %v, want the turn served on a smaller rebuild", err)
	}
	if !strings.Contains(reply.Text, "Served after all.") {
		t.Fatalf("reply = %q, want the answer the halved rebuild was given", reply.Text)
	}
	asked := provider.requests[before:]
	if len(asked) != 3 {
		t.Fatalf("provider asked %d times for the turn, want the refused session, the refused rebuild, and the halved one", len(asked))
	}
	full, halved := asked[1], asked[2]
	if full.SessionID != "" || halved.SessionID != "" {
		t.Fatalf("rebuilt attempts resumed %q and %q, want neither to resume a session", full.SessionID, halved.SessionID)
	}
	if len(full.Prompt) <= provider.limit || len(halved.Prompt) > provider.limit {
		t.Fatalf("rebuilds were %d then %d bytes, want the first over %d and the second within it", len(full.Prompt), len(halved.Prompt), provider.limit)
	}
	for _, want := range []string{rebuiltContextHeader, sessionSetAside, "earlier message(s) are not carried here.", fmt.Sprintf("answer-%d", turns-1), "and now?"} {
		if !strings.Contains(halved.Prompt, want) {
			t.Fatalf("halved prompt is missing %q", want)
		}
	}
	// The oldest go first: what the full rebuild carried that the halved one does
	// not is the start of the conversation, never its end.
	if strings.Contains(halved.Prompt, "question-0 ") || strings.Count(halved.Prompt, rebuiltContextHeader) != 1 {
		t.Fatalf("halved prompt carries the oldest message or the reconstruction twice")
	}
	if !strings.Contains(full.Prompt, "answer-4 ") || strings.Contains(halved.Prompt, "answer-4 ") {
		t.Fatalf("want answer-4 carried by the full rebuild and dropped from the halved one")
	}

	// The turn after resumes the fresh session rather than rebuilding again.
	if _, err := session.Send(context.Background(), "still there?"); err != nil {
		t.Fatalf("Send() error = %v on the turn after", err)
	}
	if next := provider.requests[len(provider.requests)-1]; next.SessionID == "" || strings.Contains(next.Prompt, rebuiltContextHeader) {
		t.Fatalf("next turn resumed %q, want the fresh session resumed and nothing rebuilt", next.SessionID)
	}
}

// A message that alone is more than the rebuild may spend is dropped with
// everything before it, and the account still says so rather than going quiet.
func TestARebuildThatCarriesNoMessageStillSaysWhatWasLeftOut(t *testing.T) {
	t.Parallel()

	event, err := execution.NewEvent("chat-test", 1, fixedClock{}.Now(), execution.EventAgentMessage, "test",
		map[string]any{"text": strings.Repeat("z", 64)})
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}
	if rendered := recordedMessages([]execution.Event{event}, 32); !strings.HasPrefix(rendered, "- 1 earlier message(s) are not carried here.") {
		t.Fatalf("rendered = %q, want the dropped message accounted for", rendered)
	}
}

// lengthLimitedBackend serves every turn, except that it refuses the session
// named refuses as too long once a test names one, and any sessionless prompt
// longer than limit, the way a provider refuses a request past its window.
type lengthLimitedBackend struct {
	limit    int
	refuses  string
	reply    string
	requests []backendapi.RunRequest
	sessions int
}

func (f *lengthLimitedBackend) Run(_ context.Context, request backendapi.RunRequest) (backendapi.RunResult, error) {
	f.requests = append(f.requests, request)
	result := backendapi.RunResult{FinalText: f.reply, SessionID: request.SessionID}
	switch {
	case f.refuses != "" && request.SessionID == f.refuses:
		result = backendapi.RunResult{IsError: true, FinalText: "Prompt is too long"}
	case request.SessionID == "" && len(request.Prompt) > f.limit:
		result = backendapi.RunResult{IsError: true, FinalText: "Prompt is too long"}
	case request.SessionID == "":
		f.sessions++
		result.SessionID = fmt.Sprintf("session-%d", f.sessions)
	}
	sequence := request.LastSequence + 1
	if request.EventSink != nil {
		event, err := execution.NewEvent(request.RunID, sequence, fixedClock{}.Now(),
			execution.EventAgentMessage, "provider.test", map[string]any{"text": result.FinalText})
		if err != nil {
			return backendapi.RunResult{}, err
		}
		if err := request.EventSink(event); err != nil {
			return backendapi.RunResult{}, err
		}
	}
	result.LastEvent = sequence
	return result, nil
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
		"codex context length": {err: errors.New("stream error: context_length_exceeded"), want: true},
		"unflagged notice":     {result: backendapi.RunResult{FinalText: "Prompt is too long"}, want: true},
		"overload":             {result: backendapi.RunResult{IsError: true, FinalText: "API Error: 529 overloaded"}},
		"answer about length":  {result: backendapi.RunResult{FinalText: "The prompt is too long for the reviewer; " + strings.Repeat("split it. ", 80)}},
		// Short served replies that mention the phrases are the role talking about
		// this very feature, and reading one as a refusal would throw away a healthy
		// session and repeat a finished turn.
		"short reply naming the error":           {result: backendapi.RunResult{FinalText: "Done — context_length_exceeded now starts a fresh session."}},
		"short reply naming a failed compaction": {result: backendapi.RunResult{FinalText: "Fixed the compaction failed test."}},
		"short reply quoting the notice":         {result: backendapi.RunResult{FinalText: "It said: Prompt is too long"}},
		"successful short reply":                 {result: backendapi.RunResult{FinalText: "Done."}},
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

// A session the failover's alternate was holding, refused as too long there, is
// recorded against the endpoint that refused it and replaced on that endpoint
// with a fresh session rebuilt from the record.
func TestAnAlternatesSessionRefusedAsTooLongIsRecordedAgainstTheAlternate(t *testing.T) {
	t.Parallel()

	held := &speakingBackend{results: []backendapi.RunResult{
		{SessionID: "claude-session-1", FinalText: "Two goals, then."},
		{
			IsError:    true,
			StopReason: "usage_limit",
			UsageLimit: &backendapi.UsageLimit{Kind: "five_hour", ResetsAt: time.Date(2026, 9, 7, 11, 0, 0, 0, time.UTC)},
		},
	}}
	crossed := &speakingBackend{results: []backendapi.RunResult{
		{SessionID: "second-session-1", FinalText: "The second one first."},
		{IsError: true, FinalText: "Prompt is too long"},
		{SessionID: "second-session-2", FinalText: "Carrying on over here."},
	}}
	options := crossingOptions(t, held, crossed)
	options.UsageLimits = newTestUsageLimits(t)
	session := openTestSession(t, options)

	for _, message := range []string{"what should we do?", "and after that?", "is that still right?"} {
		if _, err := session.Send(context.Background(), message); err != nil {
			t.Fatalf("Send(%q) error = %v", message, err)
		}
	}
	if len(crossed.requests) != 3 {
		t.Fatalf("the alternate was asked %d times, want the crossing, the refused attempt, and the fresh one", len(crossed.requests))
	}
	if refused := crossed.requests[1]; refused.SessionID != "second-session-1" {
		t.Fatalf("refused attempt resumed %q, want the alternate's own session", refused.SessionID)
	}
	fresh := crossed.requests[2]
	if fresh.SessionID != "" || !strings.Contains(fresh.Prompt, sessionSetAside) || !strings.Contains(fresh.Prompt, "The second one first.") {
		t.Fatalf("fresh attempt resumed %q with prompt %q, want no session and the record rebuilt", fresh.SessionID, fresh.Prompt)
	}
	if strings.Count(fresh.Prompt, rebuiltContextHeader) != 1 {
		t.Fatalf("fresh prompt = %q, want the reconstruction exactly once", fresh.Prompt)
	}

	events, err := options.Store.LoadEvents(session.Evidence().ConversationID)
	if err != nil {
		t.Fatalf("LoadEvents() error = %v", err)
	}
	var payload struct {
		Replaced string `json:"replaced_session"`
		Provider string `json:"provider"`
		Model    string `json:"model"`
	}
	found := 0
	for _, event := range events {
		if event.Type != execution.EventSessionReplaced {
			continue
		}
		found++
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatalf("session.replaced payload: %v", err)
		}
	}
	if found != 1 || payload.Replaced != "second-session-1" || payload.Provider != "second-provider" || payload.Model != "second-model" {
		t.Fatalf("session.replaced (%d recorded) = %+v, want the alternate's session on the alternate's endpoint", found, payload)
	}
	recorded, err := options.Store.Load(options.identity())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if recorded.ProviderSessionID != "second-session-2" || recorded.SessionSetAside != "" {
		t.Fatalf("recorded = %#v, want the fresh session held and nothing left set aside", recorded)
	}
}

// A conversation with no session for a reason nobody recorded — an old record
// with no backend on it — is not told its session was refused as too long.
func TestARebuildWithNoRecordedReplacementDoesNotClaimOne(t *testing.T) {
	t.Parallel()

	provider := &speakingBackend{results: []backendapi.RunResult{{SessionID: "session-1", FinalText: "Two goals, then."}}}
	session := openTestSession(t, testOptions(t, provider))
	if _, err := session.Send(context.Background(), "what should we do?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	session.state.ProviderSessionID = ""
	session.state.Backend = ""
	rebuilt, err := session.rebuildForOwnEndpoint(backendapi.RunRequest{Prompt: "and after that?"})
	if err != nil {
		t.Fatalf("rebuildForOwnEndpoint() error = %v", err)
	}
	if strings.Contains(rebuilt.Prompt, sessionSetAside) || strings.Contains(rebuilt.Prompt, crossedProviders) ||
		!strings.Contains(rebuilt.Prompt, noSessionHeld) {
		t.Fatalf("prompt = %q, want it told only that no session is held", rebuilt.Prompt)
	}
}
