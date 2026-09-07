package chat

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/agentcontext"
	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sidestream"
)

// A side conversation and then a main turn, end to end: the side thread runs, it
// concludes, its substance merges into the agent's memory, and the main thread's
// next turn reads it there.
//
// What it is asserted to be is a memory rather than replayed dialogue. The side
// thread's transcript stays in the side stream's own log — nothing from it
// reaches the main thread's prompt or the main thread's log — and what the main
// thread is given is one revision, saying where it came from and marking what the
// side thread promised as still tentative.
func TestASideConversationReachesTheMainThreadAsMemoryAndNotAsDialogue(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: "Noted; I will confirm that myself."},
	}}
	options := testOptions(t, provider)
	options.Store = newTestStore(t, root)
	memories, err := runstate.NewMemoryStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	options.Memories = memories
	session := openTestSession(t, options)

	// The side thread runs beside this conversation: its own record, its own
	// transcript, its own identifier.
	streams, err := runstate.NewSideStreamStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewSideStreamStore() error = %v", err)
	}
	stream := testSideStream(t, session.state.ConversationID)
	if err := streams.Open(stream, sidestream.DefaultMaxPerAgent); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := streams.AppendEvent(sideEvent(t, stream.ID, 1)); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}

	// It concludes, and concluding is what merges it.
	concluded := time.Date(2026, 9, 7, 12, 20, 0, 0, time.UTC)
	stream.Turns = 3
	stream.LastSequence = 1
	stream.Outcome = sidestream.OutcomeConcluded
	stream.UpdatedAt = concluded
	stream.ClosedAt = &concluded
	if err := streams.Save(stream); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	recorded, err := agentcontext.Conclusion{
		Stream:      stream,
		Substance:   "The intake hold names work the harness chooses for itself, so an item the operator named is exempt from it.",
		Commitments: []string{"tell the operator the hold does not cover work they named"},
	}.Merge(context.Background(), memories)
	if err != nil {
		t.Fatalf("Merge() error = %v", err)
	}

	// The main thread's next turn.
	if _, err := session.Send(context.Background(), "What did you find out about the hold?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("invocations = %d, want the one turn", len(provider.requests))
	}
	prompt := provider.requests[0].Prompt

	// It arrives as memory, with the thread it came from named and the promise
	// still a promise.
	for _, required := range []string{
		"# Side conversations held beside this one",
		"merged into your memory when it concluded",
		"tentative until you ratify or adjust it here",
		stream.ID,
		"an item the operator named is exempt from it",
		"tell the operator the hold does not cover work they named",
	} {
		if !strings.Contains(prompt, required) {
			t.Errorf("the turn does not carry %q:\n%s", required, prompt)
		}
	}
	if !strings.Contains(prompt, recorded.Text) {
		t.Errorf("the turn does not carry the revision that was stored:\n%s", prompt)
	}

	// And it arrives as an account rather than as the side thread's dialogue. The
	// two logs stay apart: the side stream keeps its own events, and nothing of
	// them is in the main thread's log or in its prompt.
	if strings.Contains(prompt, "provider.side-stream") {
		t.Errorf("the side thread's transcript was replayed into the main thread:\n%s", prompt)
	}
	sideEvents, err := streams.LoadEvents(stream.ID)
	if err != nil {
		t.Fatalf("LoadEvents() error = %v", err)
	}
	if len(sideEvents) != 1 {
		t.Fatalf("the side stream holds %d events, want the one it recorded", len(sideEvents))
	}
	mainEvents, err := options.Store.(*runstate.ConversationStore).LoadEvents(session.state.ConversationID)
	if err != nil {
		t.Fatalf("LoadEvents() error = %v", err)
	}
	for _, event := range mainEvents {
		if event.RunID == stream.ID {
			t.Errorf("the main thread's log holds the side stream's event %d", event.Sequence)
		}
	}
}

// One agent may hold two conversations, and a merge belongs to the thread it was
// opened beside. A conversation that showed another one's side threads would be
// handing an agent context from a thread it is not the continuation of.
func TestAMergeReachesOnlyTheThreadItWasOpenedBeside(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	memories, err := runstate.NewMemoryStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	stream := testSideStream(t, "chat-ffffffffffffffffffffffffffffffff")
	concluded := time.Date(2026, 9, 7, 12, 20, 0, 0, time.UTC)
	stream.Turns = 2
	stream.Outcome = sidestream.OutcomeConcluded
	stream.UpdatedAt = concluded
	stream.ClosedAt = &concluded
	if _, err := (agentcontext.Conclusion{
		Stream:    stream,
		Substance: "something another conversation's side thread worked out",
	}).Merge(context.Background(), memories); err != nil {
		t.Fatalf("Merge() error = %v", err)
	}

	provider := &fakeBackend{results: []backendapi.RunResult{{SessionID: "session-1", FinalText: "Noted."}}}
	options := testOptions(t, provider)
	options.Store = newTestStore(t, root)
	options.Memories = memories
	session := openTestSession(t, options)
	if _, err := session.Send(context.Background(), "Anything from beside this thread?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if strings.Contains(provider.requests[0].Prompt, "# Side conversations held beside this one") {
		t.Errorf("another conversation's side thread reached this one:\n%s", provider.requests[0].Prompt)
	}
}

// A memory store that will not answer says so rather than being read as an agent
// that held no side threads. The two lead to opposite conclusions, and only one
// of them is a reason to carry on as though nothing had been found out.
func TestAMemoryReadThatFailedIsSaidRatherThanReadAsNothing(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{results: []backendapi.RunResult{{SessionID: "session-1", FinalText: "Noted."}}}
	options := testOptions(t, provider)
	options.Memories = refusingMemories{}
	session := openTestSession(t, options)
	if _, err := session.Send(context.Background(), "Anything from beside this thread?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	prompt := provider.requests[0].Prompt
	for _, required := range []string{"could not be read", "Do not read that as there having been none"} {
		if !strings.Contains(prompt, required) {
			t.Errorf("the turn does not say the record was unreadable, only %q missing:\n%s", required, prompt)
		}
	}
}

// A conversation with no memory store wired to it carries nothing at all. The
// role cannot open a side thread, so "you held none" is a sentence it can do
// nothing with, and it is every conversation until an agent is configured for
// side threads.
func TestAConversationWithoutAMemoryStoreCarriesNoSideConversationBlock(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{results: []backendapi.RunResult{{SessionID: "session-1", FinalText: "Noted."}}}
	options := testOptions(t, provider)
	session := openTestSession(t, options)
	if _, err := session.Send(context.Background(), "Anything from beside this thread?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if strings.Contains(provider.requests[0].Prompt, "Side conversations held beside this one") {
		t.Errorf("a conversation with no memory store spoke about side conversations:\n%s", provider.requests[0].Prompt)
	}
}

// refusingMemories is a store that is wired and will not answer, which is a
// different thing from one that is not wired.
type refusingMemories struct{}

func (refusingMemories) Live(string) ([]runstate.Memory, []runstate.MemoryProblem, error) {
	return nil, nil, errors.New("the memory directory is unreadable")
}

func testSideStream(t *testing.T, conversation string) sidestream.Stream {
	t.Helper()

	id, err := sidestream.NewID()
	if err != nil {
		t.Fatalf("NewID() error = %v", err)
	}
	opened := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	return sidestream.Stream{
		SchemaVersion:         sidestream.SchemaVersion,
		ID:                    id,
		ProductID:             "yoyodyne",
		RepositoryID:          "yoyodyne",
		Agent:                 string(domain.RoleProductManager),
		Role:                  domain.RoleProductManager,
		Conversation:          conversation,
		Topic:                 "whether the intake hold covers work an operator named",
		Backend:               domain.BackendClaudeCode,
		ProviderSessionID:     "session-side",
		ProviderModel:         "opus",
		ProviderResolvedModel: "claude-opus-5-20260514",
		MaxTurns:              sidestream.DefaultMaxTurns,
		OpenedAt:              opened,
		UpdatedAt:             opened,
	}
}

func sideEvent(t *testing.T, streamID string, sequence uint64) execution.Event {
	t.Helper()

	event, err := execution.NewEvent(streamID, sequence, time.Date(2026, 9, 7, 12, 10, 0, 0, time.UTC),
		execution.EventAgentMessage, "provider.side-stream", nil)
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}
	return event
}
