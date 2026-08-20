package runstate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

func TestConversationStoreRoundTripsAcrossProcesses(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newConversationStore(t, root)
	if _, err := store.Load(ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager}); !errors.Is(err, ErrNoConversation) {
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
	// What the agent is reasoning from travels with the record, because the
	// process that briefed it is usually not the one that resumes it: a resumed
	// conversation that cannot say how old its picture is describes a repository
	// as it was and sounds exactly as certain about it.
	conversation.ContextGatheredAt = conversation.StartedAt
	conversation.ContextCommit = "a1a1a1a1a1a1"
	// So does the work item it last ran, for the same reason: "what did that
	// change" is a question about the run the operator last watched, and the
	// process that started it is often not the one they come back to.
	conversation.LastRunWorkItemID = "yoyodyne-ifd.39"
	// And so do the proposed changes the conversation has already put to the role
	// that owns the documents: a pending proposal stays pending until somebody
	// decides it, so a record that did not survive the process would deliver the
	// whole undecided queue again on every restart.
	conversation.DeliveredAmendmentIDs = []string{"amendment-0123456789abcdef0123456789abcdef"}
	conversation.LastSequence = 4
	conversation.UpdatedAt = conversation.StartedAt.Add(time.Minute)
	if err := store.Save(conversation); err != nil {
		t.Fatalf("Save() update error = %v", err)
	}

	// A second store over the same root is what a restarted process sees.
	loaded, err := newConversationStore(t, root).Load(ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !reflect.DeepEqual(loaded, conversation) {
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
	held, err := newConversationStore(t, root).Hold(ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager})
	if err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	if _, err := newConversationStore(t, root).Hold(ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager}); err == nil ||
		!strings.Contains(err.Error(), "already held") {
		t.Fatalf("second Hold() error = %v", err)
	}
	if err := held.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	// A released conversation is immediately available again.
	regained, err := newConversationStore(t, root).Hold(ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager})
	if err != nil {
		t.Fatalf("Hold() after release error = %v", err)
	}
	if err := regained.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
}

// A claim on a conversation can be put down and taken up again, which is what
// lets the operator's console stop being the reason nothing else can reach the
// agent. While it is down the conversation belongs to whoever takes it.
func TestAConversationPutDownIsReachableAndCanBeTakenUpAgain(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	identity := ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager}
	hold, err := newConversationStore(t, root).Claim(identity)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if !hold.Held() {
		t.Fatal("a fresh claim does not have the conversation")
	}
	// A claim is exclusive exactly as the plain lease is, for as long as it is
	// held: this is the first thing that would be wrong if it had stopped
	// locking at all.
	if _, err := newConversationStore(t, root).Hold(identity); err == nil || !errors.Is(err, ErrConversationHeld) {
		t.Fatalf("Hold() against a claimed conversation error = %v, want ErrConversationHeld", err)
	}

	if err := hold.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if hold.Held() {
		t.Fatal("a released claim still reports the conversation as held")
	}
	// The other process — the harness relaying, or the operator's assistant —
	// gets the conversation the moment the console is not mid-turn.
	other, err := newConversationStore(t, root).Hold(identity)
	if err != nil {
		t.Fatalf("Hold() against a released claim error = %v", err)
	}
	if err := other.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}

	if err := hold.Retake(context.Background()); err != nil {
		t.Fatalf("Retake() error = %v", err)
	}
	if !hold.Held() {
		t.Fatal("a retaken claim does not have the conversation")
	}
	// Taking up a conversation this process already has changes nothing rather
	// than deadlocking against itself.
	if err := hold.Retake(context.Background()); err != nil {
		t.Fatalf("Retake() while held error = %v", err)
	}
	if _, err := newConversationStore(t, root).Hold(identity); !errors.Is(err, ErrConversationHeld) {
		t.Fatalf("Hold() after Retake() error = %v, want ErrConversationHeld", err)
	}
	if err := hold.Release(); err != nil {
		t.Fatalf("final Release() error = %v", err)
	}
}

// Taking a conversation back waits for whoever has it rather than refusing.
// Refusing is right for the first claim, where there is something else for the
// caller to do; here the operator has already typed, and the other process's
// turn ends on its own.
func TestTakingAConversationBackWaitsForWhoeverHasIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	identity := ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager}
	hold, err := newConversationStore(t, root).Claim(identity)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if err := hold.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	other, err := newConversationStore(t, root).Hold(identity)
	if err != nil {
		t.Fatalf("Hold() error = %v", err)
	}

	retaken := make(chan error, 1)
	go func() { retaken <- hold.Retake(context.Background()) }()
	select {
	case err := <-retaken:
		t.Fatalf("Retake() returned %v while another process held the conversation", err)
	case <-time.After(50 * time.Millisecond):
	}

	if err := other.Release(); err != nil {
		t.Fatalf("other Release() error = %v", err)
	}
	select {
	case err := <-retaken:
		if err != nil {
			t.Fatalf("Retake() error = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Retake() did not return after the other process released the conversation")
	}
	if err := hold.Release(); err != nil {
		t.Fatalf("final Release() error = %v", err)
	}
}

// A wait that cannot end is not one an operator has to serve: a cancelled
// context gives the conversation back to the caller as a failure rather than
// leaving them at a prompt that never answers.
func TestTakingAConversationBackGivesUpWhenItsContextIsDone(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	identity := ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager}
	hold, err := newConversationStore(t, root).Claim(identity)
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if err := hold.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	other, err := newConversationStore(t, root).Hold(identity)
	if err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	defer other.Release()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := hold.Retake(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Retake() error = %v, want a deadline", err)
	}
	if hold.Held() {
		t.Fatal("a Retake() that gave up still reports the conversation as held")
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
	if _, err := store.Load(ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager}); err == nil {
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
			// A picture may predate the conversation that carries it, because it
			// is assembled before the record exists. What it may not do is claim
			// to have been taken later than the record was written, which would
			// make every comparison against it read as fresher than it is.
			name:   "a picture from the future",
			mutate: func(c *Conversation) { c.ContextGatheredAt = c.UpdatedAt.Add(time.Second) },
			want:   "context_gathered_at cannot be after updated_at",
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

// Two agents filling one role hold two conversations, with two provider
// sessions and two leases. A store that keyed on the role alone would have each
// of them resuming the other's session under its own persona and model.
func TestConversationsAreKeptPerAgentRatherThanPerRole(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newConversationStore(t, root)
	house := ConversationIdentity{Agent: "house-architect", Role: domain.RoleArchitect}
	visiting := ConversationIdentity{Agent: "visiting-architect", Role: domain.RoleArchitect}

	recorded := testConversation(t)
	recorded.Agent = house.Agent
	recorded.Role = house.Role
	recorded.ProviderSessionID = "session-house"
	recorded.ProviderModel = "opus"
	recorded.Turns = 1
	if err := store.Save(recorded); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loaded, err := store.Load(house)
	if err != nil || loaded.ProviderSessionID != "session-house" {
		t.Fatalf("Load(house) = %q, err = %v", loaded.ProviderSessionID, err)
	}
	if _, err := store.Load(visiting); !errors.Is(err, ErrNoConversation) {
		t.Fatalf("Load(visiting) error = %v, want ErrNoConversation", err)
	}

	// The lease is per agent for the same reason: one architect talking must not
	// stop the other, because they are not sharing anything to serialize.
	held, err := store.Hold(house)
	if err != nil {
		t.Fatalf("Hold(house) error = %v", err)
	}
	defer held.Release()
	if _, err := store.Hold(house); !errors.Is(err, ErrConversationHeld) {
		t.Fatalf("Hold(house) twice error = %v, want ErrConversationHeld", err)
	}
	sibling, err := store.Hold(visiting)
	if err != nil {
		t.Fatalf("Hold(visiting) error = %v, want it free while its sibling is held", err)
	}
	defer sibling.Release()

	// A record whose agent disagrees with the file it is in is somebody's edit
	// rather than a conversation, and resuming it would put one agent's session
	// behind another's persona. It is written by hand here because nothing the
	// store does can produce it.
	misfiled, err := os.ReadFile(filepath.Join(store.Root(), "house-architect.json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	rewritten := strings.Replace(string(misfiled), `"agent": "house-architect"`, `"agent": "visiting-architect"`, 1)
	if rewritten == string(misfiled) {
		t.Fatalf("the record does not name its agent, so this test proves nothing:\n%s", misfiled)
	}
	if err := os.WriteFile(filepath.Join(store.Root(), "house-architect.json"), []byte(rewritten), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := store.Load(house); err == nil || !strings.Contains(err.Error(), "belongs to agent") {
		t.Fatalf("Load(house) error = %v, want a refusal naming the agent", err)
	}
}

// A conversation recorded before the agent was part of the identity keeps
// loading, under the agent named for its role — the only agent that could have
// written it — and acquires the agent the next time it is saved.
func TestAConversationRecordedWithoutAnAgentStillLoads(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newConversationStore(t, root)
	legacy := testConversation(t)
	legacy.ProviderSessionID = "session-1"
	legacy.ProviderModel = "opus"
	legacy.Turns = 1
	if legacy.Agent != "" {
		t.Fatal("the fixture already names an agent; this test proves nothing")
	}
	if err := store.Save(legacy); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(store.Root(), "product-manager.json")); err != nil {
		t.Fatalf("the record is not where the role-keyed layout put it: %v", err)
	}

	identity := ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager}
	loaded, err := store.Load(identity)
	if err != nil || loaded.ProviderSessionID != "session-1" {
		t.Fatalf("Load() = %q, err = %v", loaded.ProviderSessionID, err)
	}
	loaded.Agent = identity.Agent
	if err := store.Save(loaded); err != nil {
		t.Fatalf("Save() stamped error = %v", err)
	}
	stamped, err := store.Load(identity)
	if err != nil || stamped.Agent != "product-manager" {
		t.Fatalf("Load() agent = %q, err = %v", stamped.Agent, err)
	}
}

// Something has to be able to ask what conversations this product has held
// without knowing which agents were configured for it. The reporting sink is the
// first thing that does, and what it lists is what it will read logs from.
func TestConversationStoreListsEveryRecordedConversation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newConversationStore(t, root)
	if recorded, err := store.Recorded(); err != nil || len(recorded) != 0 {
		t.Fatalf("Recorded() on an untouched product = %v, %v", recorded, err)
	}

	manager := testConversation(t)
	manager.Agent = "product-manager"
	if err := store.Save(manager); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	developmentManager := testConversation(t)
	developmentManager.Agent = "development-manager"
	developmentManager.Role = domain.RoleDevelopmentManager
	if err := store.Save(developmentManager); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	// The leases and the event logs live in the same directory, and only a file
	// named for an agent holds a conversation.
	lease, err := store.Hold(ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager})
	if err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	defer lease.Release()
	event, err := execution.NewEvent(manager.ConversationID, 1, manager.StartedAt, execution.EventAgentMessage, "harness.chat", nil)
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}
	if err := store.AppendEvent(event); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}

	recorded, err := store.Recorded()
	if err != nil {
		t.Fatalf("Recorded() error = %v", err)
	}
	if len(recorded) != 2 {
		t.Fatalf("Recorded() = %#v, want the two conversations", recorded)
	}
	found := map[string]domain.AgentRole{}
	for _, conversation := range recorded {
		found[conversation.ConversationID] = conversation.Role
	}
	if found[manager.ConversationID] != domain.RoleProductManager || found[developmentManager.ConversationID] != domain.RoleDevelopmentManager {
		t.Fatalf("Recorded() = %#v, want each conversation under the role that holds it", found)
	}
}

func TestConversationStoreRefusesToListARecordItCannotRead(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newConversationStore(t, root)
	if err := store.Save(testConversation(t)); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(store.Root(), "product-manager.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := store.Recorded(); err == nil || !strings.Contains(err.Error(), "product-manager.json") {
		t.Fatalf("Recorded() error = %v, want the file it could not read named", err)
	}
}

// The undecided proposals a conversation carries are the largest thing in its
// record, so a conversation that filled the bound must still be one that saves.
// The failure this guards is total: a record that cannot be written is a
// conversation that cannot take another turn.
func TestAConversationFullOfUndecidedProposalsStillSaves(t *testing.T) {
	t.Parallel()

	store, err := NewConversationStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewConversationStore() error = %v", err)
	}
	now := time.Now().UTC()
	conversation := Conversation{
		SchemaVersion:  ConversationSchemaVersion,
		ConversationID: "chat-0123456789abcdef0123456789abcdef",
		ProductID:      "yoyodyne",
		RepositoryID:   "yoyodyne",
		Agent:          "product-manager",
		Role:           "product-manager",
		Backend:        domain.BackendClaudeCode,
		StartedAt:      now,
		UpdatedAt:      now,
		// And the rest of what the record carries at its own bounds, because the
		// state file holds all of it at once or none of it.
		PendingTrackerResults: strings.Repeat("r", MaxPendingTrackerResultBytes),
	}
	for i := 0; i < MaxPendingProposals; i++ {
		conversation.PendingProposals = append(conversation.PendingProposals, PendingProposal{
			ID:          fmt.Sprintf("%d.1", i+1),
			Turn:        i + 1,
			Title:       strings.Repeat("t", 200),
			Description: strings.Repeat("d", 8<<10),
			Rationale:   strings.Repeat("w", 8<<10),
			Goal:        strings.Repeat("g", 400),
		})
	}
	if err := store.Save(conversation); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	loaded, err := store.Load(ConversationIdentity{Agent: "product-manager", Role: "product-manager"})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(loaded.PendingProposals) != MaxPendingProposals {
		t.Fatalf("loaded %d proposal(s), want %d", len(loaded.PendingProposals), MaxPendingProposals)
	}
}
