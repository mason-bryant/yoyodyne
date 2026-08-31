package runstate

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
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
	// The account the turn was answered on and the configuration in force while it
	// was travel with the backend and the selectors, because those four together
	// are what the durable-state-is-provider-independent invariant asks of a
	// provider invocation — and a conversation turn is one. Under a pool the alias
	// is the only thing that says whose subscription paid for it.
	conversation.AccountAlias = "research"
	conversation.ConfigRevision = "cfg-0123456789ab"
	// And so does the harness that answered it. A conversation an operator leaves
	// open is held by a process that goes on running whatever binary started it,
	// so which one that is has to be in the record rather than inferred from when
	// the conversation began.
	conversation.Build = "9870df6a1b2c3d4e5f60718293a4b5c6d7e8f900"
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

// Observing a conversation must take nothing from it. The four-line status asks
// this of every conversation on every reading and the heartbeat asks hourly, so
// a probe that took the lease and let it go again would be refusing the
// operator their own chat for the instant it held.
func TestConversationInFlightObservesWithoutTakingTheLease(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newConversationStore(t, root)
	identity := ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager}
	for ask := 1; ask <= 3; ask++ {
		if inFlight, err := store.InFlight(identity); inFlight || err != nil {
			t.Fatalf("ask %d of a free conversation = %v, %v", ask, inFlight, err)
		}
	}

	held, err := store.Hold(identity)
	if err != nil {
		t.Fatalf("Hold() after asking error = %v, want the conversation still free", err)
	}
	// A hold this process took is what another process holding it looks like from
	// outside: the stamp is written by whoever owns the lease, and read by
	// anybody.
	if inFlight, err := store.InFlight(identity); !inFlight || err != nil {
		t.Fatalf("a held conversation = %v, %v, want it reported in flight", inFlight, err)
	}
	if err := held.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if inFlight, err := store.InFlight(identity); inFlight || err != nil {
		t.Fatalf("a released conversation = %v, %v, want it free again", inFlight, err)
	}
	// A hold is observable per agent, exactly as it is taken per agent.
	sibling := ConversationIdentity{Agent: "development-manager", Role: domain.RoleDevelopmentManager}
	lease, err := store.Hold(sibling)
	if err != nil {
		t.Fatalf("Hold(sibling) error = %v", err)
	}
	defer lease.Release()
	if inFlight, err := store.InFlight(identity); inFlight || err != nil {
		t.Fatalf("a sibling's hold reported on %s = %v, %v", identity, inFlight, err)
	}
}

// The whole of the defect: a conversation somebody wants to have must be there
// to take, however often something else is asking whether it is in use.
func TestAConversationIsNeverRefusedByAProbe(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newConversationStore(t, root)
	identity := ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager}

	asking := make(chan struct{})
	asked := make(chan error, 1)
	go func() {
		var problem error
		for {
			select {
			case <-asking:
				asked <- problem
				return
			default:
			}
			if _, err := store.InFlight(identity); err != nil && problem == nil {
				problem = err
			}
		}
	}()

	for attempt := 1; attempt <= 200; attempt++ {
		lease, err := store.Hold(identity)
		if err != nil {
			close(asking)
			<-asked
			t.Fatalf("Hold() attempt %d error = %v, want a conversation no probe can refuse", attempt, err)
		}
		if err := lease.Release(); err != nil {
			close(asking)
			<-asked
			t.Fatalf("Release() attempt %d error = %v", attempt, err)
		}
	}
	close(asking)
	if err := <-asked; err != nil {
		t.Fatalf("observing a conversation being held and released error = %v", err)
	}
}

// A holder that exits without releasing leaves its stamp behind, and the
// operating system has already dropped its lock. The probe's answer has to be
// the operating system's, or a killed chat would read as a turn in flight for
// as long as the state directory lasts.
func TestAStampFromADeadHolderIsNotATurnInFlight(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newConversationStore(t, root)
	identity := ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager}
	lease, err := store.Hold(identity)
	if err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	stamp := filepath.Join(store.Root(), "product-manager.holder")
	written, err := os.ReadFile(stamp)
	if err != nil {
		t.Fatalf("the hold left no stamp to observe: %v", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if _, err := os.Stat(stamp); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a released hold left its stamp behind: %v", err)
	}

	// The stamp of a process that is gone, which is what a killed holder leaves.
	// It is put back by hand because nothing the store does can produce it.
	gone := exitedProcess(t)
	stale := strings.Replace(string(written), fmt.Sprintf(`"pid": %d`, os.Getpid()), fmt.Sprintf(`"pid": %d`, gone), 1)
	if stale == string(written) {
		t.Fatalf("the stamp does not name this process, so this test proves nothing:\n%s", written)
	}
	if err := os.WriteFile(stamp, []byte(stale), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if inFlight, err := store.InFlight(identity); inFlight || err != nil {
		t.Fatalf("a stamp from a process that has gone = %v, %v, want the conversation free", inFlight, err)
	}
	// And it is free to take, which is the answer the operating system was
	// already giving.
	regained, err := store.Hold(identity)
	if err != nil {
		t.Fatalf("Hold() after a stale stamp error = %v", err)
	}
	if err := regained.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
}

// A stamp that is there and will not decode is a failure to answer rather than
// an answer: a reader that guessed would be inventing whether somebody is
// mid-turn.
func TestAnUnreadableStampIsAProblemRatherThanAnAnswer(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newConversationStore(t, root)
	identity := ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager}
	if err := os.MkdirAll(store.Root(), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(store.Root(), "product-manager.holder"), []byte("{"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if inFlight, err := store.InFlight(identity); inFlight || err == nil {
		t.Fatalf("an unreadable stamp = %v, %v, want a stated problem", inFlight, err)
	}
	// A name that could never be a path is refused before anything is read, for
	// the same reason.
	if inFlight, err := store.InFlight(ConversationIdentity{Agent: "Not An Agent", Role: domain.RoleProductManager}); inFlight || err == nil {
		t.Fatalf("an unaskable identity = %v, %v, want a stated problem", inFlight, err)
	}
}

// exitedProcess is the identifier of a process that has run and gone, which is
// the closest thing a test has to a holder that was killed.
func exitedProcess(t *testing.T) int {
	t.Helper()

	command := exec.Command(os.Args[0], "-test.run=TestNoSuchTestExistsHere")
	command.Env = append(os.Environ(), "GO_TEST_EXITED_PROCESS=1")
	if err := command.Start(); err != nil {
		t.Fatalf("start a process to let it exit: %v", err)
	}
	pid := command.Process.Pid
	// The wait is what makes it gone rather than merely finishing, and it is also
	// what reaps it: a zombie is still a process the null signal reaches.
	if err := command.Wait(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatalf("wait for the process to exit: %v", err)
		}
	}
	return pid
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
		{name: "backend", mutate: func(c *Conversation) { c.Backend = "carrier pigeon" }, want: "backend is invalid"},
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
			// A conversation may name no account, because one recorded before the
			// harness pinned them names none. What it may not do is name an account
			// nothing could have configured, which reads as evidence about whose
			// subscription answered it and is not.
			name:   "an account nothing configured",
			mutate: func(c *Conversation) { c.AccountAlias = "Someone Else's" },
			want:   "account_alias is not an account alias",
		},
		{
			name:   "a revision of no digest",
			mutate: func(c *Conversation) { c.ConfigRevision = "yesterday's" },
			want:   "config_revision is not a configuration revision",
		},
		{
			// And the same for the build: a conversation may name none, because one
			// recorded before the harness pinned it does and because a binary carrying
			// no revision of its own leaves it empty. What it may not do is name
			// something no repository could resolve, which reads as an answer to
			// "which harness held this" and is not one.
			name:   "a build that is not a revision",
			mutate: func(c *Conversation) { c.Build = "the one from Tuesday" },
			want:   "build is not a revision",
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
