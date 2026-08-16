package runstate

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"yoyodyne/internal/domain"
	"yoyodyne/internal/execution"
)

func TestConversationStoreRoundTripsAcrossProcesses(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newConversationStore(t, root)
	if _, err := store.Load(domain.RoleProductManager); !errors.Is(err, ErrNoConversation) {
		t.Fatalf("Load() error = %v, want ErrNoConversation", err)
	}

	conversation := testConversation(t)
	if err := store.Save(conversation); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	conversation.ProviderSessionID = "session-1"
	conversation.ProviderModel = "opus"
	conversation.ProviderResolvedModel = "claude-opus-5-20260514"
	conversation.Turns = 1
	conversation.PendingTrackerResults = "- t1.1: closed yoyodyne-2\n"
	conversation.LastSequence = 4
	conversation.UpdatedAt = conversation.StartedAt.Add(time.Minute)
	if err := store.Save(conversation); err != nil {
		t.Fatalf("Save() update error = %v", err)
	}

	// A second store over the same root is what a restarted process sees.
	loaded, err := newConversationStore(t, root).Load(domain.RoleProductManager)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded != conversation {
		t.Fatalf("Load() = %#v, want %#v", loaded, conversation)
	}
}

func TestConversationStoreAppendsAndReadsEvents(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newConversationStore(t, root)
	conversation := testConversation(t)
	if err := store.Save(conversation); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	for sequence := uint64(1); sequence <= 2; sequence++ {
		event, err := execution.NewEvent(conversation.ConversationID, sequence, time.Now().UTC(), execution.EventAgentMessage, "provider.claude-code", nil)
		if err != nil {
			t.Fatalf("NewEvent() error = %v", err)
		}
		if err := store.AppendEvent(event); err != nil {
			t.Fatalf("AppendEvent() error = %v", err)
		}
	}
	events, err := newConversationStore(t, root).LoadEvents(conversation.ConversationID)
	if err != nil {
		t.Fatalf("LoadEvents() error = %v", err)
	}
	if len(events) != 2 || events[1].Sequence != 2 {
		t.Fatalf("LoadEvents() = %#v", events)
	}

	// An event that names something other than a conversation never reaches a
	// path.
	stray, err := execution.NewEvent("run-0123456789abcdef0123456789abcdef", 1, time.Now().UTC(), execution.EventAgentMessage, "provider.claude-code", nil)
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}
	if err := store.AppendEvent(stray); err == nil || !strings.Contains(err.Error(), "conversation id is invalid") {
		t.Fatalf("AppendEvent() stray error = %v", err)
	}
}

func TestConversationStoreHoldsOneConversationAtATime(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	held, err := newConversationStore(t, root).Hold(domain.RoleProductManager)
	if err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	if _, err := newConversationStore(t, root).Hold(domain.RoleProductManager); err == nil ||
		!strings.Contains(err.Error(), "already held") {
		t.Fatalf("second Hold() error = %v", err)
	}
	if err := held.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	// A released conversation is immediately available again.
	regained, err := newConversationStore(t, root).Hold(domain.RoleProductManager)
	if err != nil {
		t.Fatalf("Hold() after release error = %v", err)
	}
	if err := regained.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
}

func TestConversationStoreRefusesForeignAndMalformedRecords(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newConversationStore(t, root)
	foreign := testConversation(t)
	foreign.ProductID = "other"
	if err := store.Save(foreign); err == nil || !strings.Contains(err.Error(), "does not match store product") {
		t.Fatalf("Save() foreign product error = %v", err)
	}

	conversation := testConversation(t)
	if err := store.Save(conversation); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	path := filepath.Join(store.Root(), string(domain.RoleProductManager)+".json")
	if err := os.WriteFile(path, []byte(`{"schema_version":1,"conversation_id":"chat-1"}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := store.Load(domain.RoleProductManager); err == nil {
		t.Fatal("Load() malformed error = nil")
	}
}

func TestConversationValidateRejectsIncoherentRecords(t *testing.T) {
	t.Parallel()

	valid := testConversation(t)
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	for _, test := range []struct {
		name   string
		mutate func(*Conversation)
		want   string
	}{
		{name: "schema", mutate: func(c *Conversation) { c.SchemaVersion = 2 }, want: "schema_version"},
		{name: "id", mutate: func(c *Conversation) { c.ConversationID = "chat-nope" }, want: "conversation_id is invalid"},
		{name: "role", mutate: func(c *Conversation) { c.Role = "../escape" }, want: "role"},
		{name: "backend", mutate: func(c *Conversation) { c.Backend = "carrier-pigeon" }, want: "backend is invalid"},
		{name: "turns", mutate: func(c *Conversation) { c.Turns = -1 }, want: "turns cannot be negative"},
		{
			name:   "unauditable turn",
			mutate: func(c *Conversation) { c.Turns = 1 },
			want:   "requires the requested model selector",
		},
		{
			name:   "backwards clock",
			mutate: func(c *Conversation) { c.UpdatedAt = c.StartedAt.Add(-time.Second) },
			want:   "updated_at cannot be before started_at",
		},
		{
			// What waits inside the record has to keep the record loadable, so an
			// unbounded account of pending results is refused where it is written
			// rather than discovered when the next process cannot read it.
			name:   "unbounded pending results",
			mutate: func(c *Conversation) { c.PendingTrackerResults = strings.Repeat("x", MaxPendingTrackerResultBytes+1) },
			want:   "pending tracker results are",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			conversation := testConversation(t)
			test.mutate(&conversation)
			if err := conversation.Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want it to contain %q", err, test.want)
			}
		})
	}
}

func TestNewConversationIDIsUniqueAndWellFormed(t *testing.T) {
	t.Parallel()

	first, err := NewConversationID()
	if err != nil {
		t.Fatalf("NewConversationID() error = %v", err)
	}
	second, err := NewConversationID()
	if err != nil {
		t.Fatalf("NewConversationID() error = %v", err)
	}
	if first == second || !conversationIDPattern.MatchString(first) {
		t.Fatalf("conversation ids = %q, %q", first, second)
	}
}

func newConversationStore(t *testing.T, root string) *ConversationStore {
	t.Helper()

	store, err := NewConversationStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewConversationStore() error = %v", err)
	}
	return store
}

func testConversation(t *testing.T) Conversation {
	t.Helper()

	id, err := NewConversationID()
	if err != nil {
		t.Fatalf("NewConversationID() error = %v", err)
	}
	started := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	return Conversation{
		SchemaVersion:  ConversationSchemaVersion,
		ConversationID: id,
		ProductID:      "yoyodyne",
		RepositoryID:   "yoyodyne",
		Role:           domain.RoleProductManager,
		Backend:        domain.BackendClaudeCode,
		StartedAt:      started,
		UpdatedAt:      started,
	}
}
