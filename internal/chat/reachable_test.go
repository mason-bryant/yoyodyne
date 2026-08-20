package chat

import (
	"context"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// promptHold is this process's claim on the conversation as a test can watch
// it: what was put down, what was taken back, and somewhere to stand in for the
// other process while the console is waiting at the prompt.
type promptHold struct {
	releases int
	retakes  int
	// meanwhile is the other process, run while the conversation is down. It is
	// given which release this is, because the console puts the conversation
	// down before every prompt and a test usually means one of them.
	meanwhile func(release int)
}

func (h *promptHold) Release() error {
	h.releases++
	if h.meanwhile != nil {
		h.meanwhile(h.releases)
	}
	return nil
}

func (h *promptHold) Retake(context.Context) error {
	h.retakes++
	return nil
}

// The operator's console is idle nearly all of its life, and while it is idle
// the conversation belongs to whoever wants it. What the console must not do is
// carry on from the copy it had before it let go: the provider session has
// moved, and a turn sent against the old one would resume a session the agent
// has already left behind.
func TestAConversationTakenAtThePromptIsResumedRatherThanOverwritten(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: "Two goals are missing."},
		{SessionID: "session-relayed", ResolvedModel: "claude-opus-5-20260514", FinalText: "Here they are."},
	}}
	options := testOptions(t, provider)
	store := options.Store.(*runstate.ConversationStore)
	identity := options.identity()
	hold := &promptHold{}
	options.Hold = hold
	// The other process — the harness relaying, or the operator's assistant —
	// takes the conversation while the console waits for the second message,
	// answers something of its own, and leaves the record advanced.
	hold.meanwhile = func(release int) {
		if release != 2 {
			return
		}
		recorded, err := store.Load(identity)
		if err != nil {
			t.Errorf("the other process could not load the conversation: %v", err)
			return
		}
		recorded.ProviderSessionID = "session-relayed"
		recorded.Turns++
		recorded.PendingNotices = append(recorded.PendingNotices, "the harness relayed a triage decision")
		recorded.UpdatedAt = recorded.UpdatedAt.Add(time.Minute)
		if err := store.Save(recorded); err != nil {
			t.Errorf("the other process could not record its turn: %v", err)
		}
	}
	session := openTestSession(t, options)

	var out strings.Builder
	input := strings.NewReader("what is missing?\nlist them\n/exit\n")
	if err := session.Converse(context.Background(), testConsole(input, &out)); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}

	// Put down before every prompt and taken back for every message, rather than
	// held for the whole window.
	if hold.releases < 3 || hold.retakes < 2 {
		t.Fatalf("the conversation was put down %d time(s) and taken back %d", hold.releases, hold.retakes)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("provider took %d turn(s), want 2", len(provider.requests))
	}
	// The turn after the console let go resumes what the other process left,
	// which is the whole point: without re-reading it would have resumed
	// session-1 and overwritten the record of a turn that really happened.
	if provider.requests[1].SessionID != "session-relayed" {
		t.Fatalf("second turn resumed session %q, want session-relayed", provider.requests[1].SessionID)
	}
	// And it is the whole durable half rather than only the session: what the
	// agent has not been told yet is owed to its next turn wherever that turn is
	// taken, and this is that turn.
	if !strings.Contains(provider.requests[1].Prompt, "the harness relayed a triage decision") {
		t.Fatalf("second prompt did not carry what the other process left: %q", provider.requests[1].Prompt)
	}
	final, err := store.Load(identity)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if final.Turns != 3 {
		t.Fatalf("recorded turns = %d, want 3: this conversation's two and the relayed one", final.Turns)
	}
}

// A run started from the conversation reports itself into the record from under
// the prompt, while the operator is still typing. A conversation that had let go
// would be writing to a record somebody else now owns, so it does not let go:
// what it puts down at the prompt is a conversation with nothing of its own in
// flight.
func TestAConversationRunningWorkKeepsItWhileTheOperatorIsAtThePrompt(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: "Running it now."},
	}}
	options := testOptions(t, provider)
	options.Work = &fakeWork{}
	hold := &promptHold{}
	options.Hold = hold
	session := openTestSession(t, options)

	var out strings.Builder
	// The first prompt puts the conversation down; the one after `/work` must
	// not, because the run is this conversation's to report and to stop.
	input := strings.NewReader("/work yoyodyne-1\n/exit\n")
	if err := session.Converse(context.Background(), testConsole(input, &out)); err != nil {
		t.Fatalf("Converse() error = %v", err)
	}
	if hold.releases != 1 {
		t.Fatalf("the conversation was put down %d time(s), want 1: only the prompt before the run", hold.releases)
	}
}

// A conversation that is not put down is not re-read either. A single message
// holds from end to end, so nothing else can have written, and re-reading would
// be work done to discover what this process already knows.
func TestAConversationThatWasNeverPutDownIsNotReRead(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: "Two goals are missing."},
	}}
	options := testOptions(t, provider)
	store := options.Store.(*runstate.ConversationStore)
	session := openTestSession(t, options)
	if _, err := session.Send(context.Background(), "What is missing from the brief?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	// Nothing should have been holding this conversation, so nothing should be
	// adopted from a record written behind it.
	recorded, err := store.Load(options.identity())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	recorded.PendingNotices = []string{"written by nobody"}
	if err := store.Save(recorded); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := session.reload(); err != nil {
		t.Fatalf("reload() error = %v", err)
	}
	if len(session.notices) != 0 {
		t.Fatalf("a conversation with no hold adopted %#v", session.notices)
	}
}

// A different conversation recorded under the same agent is `--new` somewhere
// else. Carrying on here would replace the record of a conversation somebody is
// currently having, so this one ends and both survive.
func TestAConversationReplacedElsewhereEndsRatherThanOverwritingTheNewOne(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: "Two goals are missing."},
	}}
	options := testOptions(t, provider)
	store := options.Store.(*runstate.ConversationStore)
	identity := options.identity()
	hold := &promptHold{}
	options.Hold = hold
	var replacementID string
	hold.meanwhile = func(release int) {
		if release != 2 {
			return
		}
		recorded, err := store.Load(identity)
		if err != nil {
			t.Errorf("the other process could not load the conversation: %v", err)
			return
		}
		replacement, err := runstate.NewConversationID()
		if err != nil {
			t.Errorf("NewConversationID() error = %v", err)
			return
		}
		replacementID = replacement
		recorded.ConversationID = replacement
		recorded.ProviderSessionID = ""
		recorded.ProviderModel = ""
		recorded.Turns = 0
		if err := store.Save(recorded); err != nil {
			t.Errorf("the other process could not start its conversation: %v", err)
		}
	}
	session := openTestSession(t, options)

	var out strings.Builder
	input := strings.NewReader("what is missing?\nlist them\n/exit\n")
	err := session.Converse(context.Background(), testConsole(input, &out))
	if err == nil || !strings.Contains(err.Error(), "another process started a new one") {
		t.Fatalf("Converse() error = %v, want the conversation to end on the replacement", err)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("provider took %d turn(s), want 1: the second was never sent", len(provider.requests))
	}
	final, err := store.Load(identity)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if final.ConversationID != replacementID {
		t.Fatalf("recorded conversation = %s, want the replacement %s", final.ConversationID, replacementID)
	}
}
