package chat

// A provider session compacted before it outgrows the request it is sent in.
//
// These read what actually reached the provider, because a compaction is a turn
// sent without the session it would have resumed, and the only way to know that
// happened is to look at the request.

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

func compactingOptions(t *testing.T, root string, provider Backend, budget int) Options {
	t.Helper()

	options := testOptions(t, provider)
	options.Store = newTestStore(t, root)
	options.SessionBudgetBytes = budget
	return options
}

// A session with room is resumed, and what each turn put into it and got back
// out of it is added to its measure, which the record and the evidence both say
// beside the budget.
func TestASessionWithRoomIsResumedAndMeasured(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	provider := &speakingBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: "Two goals, then."},
		{SessionID: "session-1", FinalText: "The second one first."},
	}}
	session := openTestSession(t, compactingOptions(t, root, provider, 0))

	for _, message := range []string{"what should we do?", "and after that?"} {
		if _, err := session.Send(context.Background(), message); err != nil {
			t.Fatalf("Send(%q) error = %v", message, err)
		}
	}
	if len(provider.requests) != 2 || provider.requests[1].SessionID != "session-1" {
		t.Fatalf("requests = %d, second resuming %q; want the session resumed", len(provider.requests), provider.requests[1].SessionID)
	}
	want := 0
	for index, request := range provider.requests {
		want += len(request.Prompt) + len(provider.results[index].FinalText)
	}
	evidence := session.Evidence()
	if evidence.SessionBytes != want || evidence.SessionBudgetBytes != SessionBudgetBytes {
		t.Fatalf("evidence says %d of %d bytes, want %d of %d", evidence.SessionBytes, evidence.SessionBudgetBytes, want, SessionBudgetBytes)
	}
	recorded, err := newTestStore(t, root).Load(session.options.identity())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if recorded.ProviderSessionBytes != want || recorded.ProviderSessionBudgetBytes != SessionBudgetBytes {
		t.Fatalf("recorded %d of %d bytes, want %d of %d", recorded.ProviderSessionBytes, recorded.ProviderSessionBudgetBytes, want, SessionBudgetBytes)
	}
	if counted := countEvents(t, root, session); counted[execution.EventSessionCompacted] != 0 {
		t.Fatalf("session.compacted events = %d, want none for a session with room", counted[execution.EventSessionCompacted])
	}
}

// The case this exists for: a turn that would take the session past its budget
// is sent without it, with the conversation rebuilt from the record in front of
// it, and the new session the provider starts is measured from nothing.
func TestATurnThatWouldPassTheBudgetCompactsTheSessionFirst(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	provider := &speakingBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: "Two goals, then."},
		{SessionID: "session-2", FinalText: "The second one first."},
	}}
	session := openTestSession(t, compactingOptions(t, root, provider, 1))

	if _, err := session.Send(context.Background(), "what should we do?"); err != nil {
		t.Fatalf("Send() error = %v on the first turn", err)
	}
	if _, err := session.Send(context.Background(), "and after that?"); err != nil {
		t.Fatalf("Send() error = %v, want the turn served on a compacted session", err)
	}

	asked := provider.requests[1]
	if asked.SessionID != "" {
		t.Fatalf("session asked for = %q, want none: the session was past its budget", asked.SessionID)
	}
	if !strings.HasPrefix(asked.Prompt, rebuiltContextHeader) || !strings.Contains(asked.Prompt, "compacted it") {
		t.Fatalf("prompt = %q, want the conversation rebuilt in front of the turn and saying it was compacted", asked.Prompt)
	}
	if !strings.Contains(asked.Prompt, "Two goals, then.") || !strings.Contains(asked.Prompt, "and after that?") {
		t.Fatalf("prompt = %q, want what was said and the message being answered", asked.Prompt)
	}
	if strings.Count(asked.Prompt, rebuiltContextHeader) != 1 {
		t.Fatalf("prompt carries the rebuild %d times, want once", strings.Count(asked.Prompt, rebuiltContextHeader))
	}

	var compacted struct {
		Reason       string `json:"reason"`
		SessionID    string `json:"session_id"`
		SessionBytes int    `json:"session_bytes"`
		BudgetBytes  int    `json:"budget_bytes"`
	}
	if err := json.Unmarshal([]byte(onlyEventPayload(t, root, session, execution.EventSessionCompacted)), &compacted); err != nil {
		t.Fatalf("decode session.compacted: %v", err)
	}
	firstTurn := len(provider.requests[0].Prompt) + len("Two goals, then.")
	if compacted.Reason != compactionOverBudget || compacted.SessionID != "session-1" ||
		compacted.SessionBytes != firstTurn || compacted.BudgetBytes != 1 {
		t.Fatalf("session.compacted = %+v, want the first session, its %d bytes, and the budget", compacted, firstTurn)
	}

	evidence := session.Evidence()
	if evidence.SessionID != "session-2" || evidence.SessionBytes != len(asked.Prompt)+len("The second one first.") {
		t.Fatalf("evidence = %+v, want the new session measured from the turn that started it", evidence)
	}
}

// A compaction that cannot be made is recorded as one and the turn is not sent:
// neither on the session that would not fit nor with nothing in front of it. And
// it is not a refusal the provider made, so nothing gives it back to be retried.
func TestAFailedCompactionIsRecordedAndTheTurnIsNotSent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	provider := &speakingBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: "Two goals, then."},
	}}
	session := openTestSession(t, compactingOptions(t, root, provider, 1))
	if _, err := session.Send(context.Background(), "what should we do?"); err != nil {
		t.Fatalf("Send() error = %v on the first turn", err)
	}
	// A line the store cannot read is what makes the record unreadable, and a
	// rebuild from an unreadable record is one that cannot be made.
	corruptEventLog(t, root)

	_, err := session.Send(context.Background(), "and after that?")
	if !errors.Is(err, ErrCompactionFailed) {
		t.Fatalf("Send() error = %v, want a failed compaction", err)
	}
	if errors.Is(err, ErrProviderCapacity) || errors.Is(err, ErrProviderAway) || errors.Is(err, ErrTurnAbandoned) {
		t.Fatalf("Send() error = %v, want nothing a caller gives back to be asked again", err)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("the provider was asked %d times, want only the first turn", len(provider.requests))
	}
	if session.Evidence().SessionID != "session-1" {
		t.Fatalf("session = %q, want the record still naming the session nothing replaced", session.Evidence().SessionID)
	}
	// The event log cannot be read back whole, so the failure is found in its raw
	// lines: that is where a reader of a damaged log would look for it too.
	if !strings.Contains(readEventLog(t, root), string(execution.EventSessionCompactionFailed)) {
		t.Fatal("the event log holds no session.compaction_failed, want the failed compaction recorded")
	}
}

// A session recorded before the harness measured sessions is of unknown size,
// and the conversation this was built for was one of them and already past the
// provider's limit. So the next turn compacts it rather than assuming it small.
func TestAnUnmeasuredSessionIsCompactedOnItsNextTurn(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	first := &speakingBackend{results: []backendapi.RunResult{{SessionID: "session-1", FinalText: "Two goals, then."}}}
	options := compactingOptions(t, root, first, 0)
	if _, err := openTestSession(t, options).Send(context.Background(), "what should we do?"); err != nil {
		t.Fatalf("Send() error = %v on the first turn", err)
	}
	store := newTestStore(t, root)
	recorded, err := store.Load(options.identity())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	recorded.ProviderSessionBytes, recorded.ProviderSessionBudgetBytes = 0, 0
	if err := store.Save(recorded); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	later := &speakingBackend{results: []backendapi.RunResult{{SessionID: "session-2", FinalText: "The second one first."}}}
	resumed := openTestSession(t, compactingOptions(t, root, later, 0))
	if _, err := resumed.Send(context.Background(), "and after that?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if later.requests[0].SessionID != "" {
		t.Fatalf("session asked for = %q, want none: nobody knows how large it is", later.requests[0].SessionID)
	}
	if payload := onlyEventPayload(t, root, resumed, execution.EventSessionCompacted); !strings.Contains(payload, `"reason":"unmeasured"`) {
		t.Fatalf("session.compacted = %s, want it to say the session was unmeasured", payload)
	}
	if resumed.Evidence().SessionBytes == 0 {
		t.Fatal("the new session is unmeasured, want it measured from the turn that started it")
	}
}

func eventLogPath(t *testing.T, root string) string {
	t.Helper()

	var found string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && strings.HasSuffix(entry.Name(), ".events.jsonl") {
			found = path
		}
		return err
	})
	if err != nil || found == "" {
		t.Fatalf("no event log under %s (err %v)", root, err)
	}
	return found
}

func corruptEventLog(t *testing.T, root string) {
	t.Helper()

	file, err := os.OpenFile(eventLogPath(t, root), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open event log: %v", err)
	}
	defer file.Close()
	if _, err := file.WriteString("not an event\n"); err != nil {
		t.Fatalf("write event log: %v", err)
	}
}

func readEventLog(t *testing.T, root string) string {
	t.Helper()

	data, err := os.ReadFile(eventLogPath(t, root))
	if err != nil {
		t.Fatalf("read event log: %v", err)
	}
	return string(data)
}
