package agentcontext

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/capability"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func TestOnlyTheRolesThatHoldTheirOwnContextMayWriteIt(t *testing.T) {
	t.Parallel()

	// The split the agent-memory design decides: the three roles that hold a
	// standing conversation keep a memory, and the two whose work a reviewer gates
	// keep none, because accumulation and independence cannot live in one identity.
	remembering := map[domain.AgentRole]bool{
		domain.RoleProductManager:     true,
		domain.RoleArchitect:          true,
		domain.RoleDevelopmentManager: true,
		domain.RoleDeveloper:          false,
		domain.RoleReviewer:           false,
	}
	if len(remembering) != len(domain.Roles()) {
		t.Fatalf("this table names %d roles and the harness has %d", len(remembering), len(domain.Roles()))
	}
	for _, role := range domain.Roles() {
		may, named := remembering[role]
		if !named {
			t.Errorf("%s is not named in this table", role)
			continue
		}
		err := Authorize(role)
		if may && err != nil {
			t.Errorf("Authorize(%s) error = %v, want the role to hold its own context", role, err)
		}
		if !may {
			if !errors.Is(err, ErrUnauthorized) {
				t.Errorf("Authorize(%s) error = %v, want ErrUnauthorized", role, err)
			}
			if err != nil && !strings.Contains(err.Error(), string(capability.AgentContextMutate)) {
				t.Errorf("Authorize(%s) error = %v, want the capability named", role, err)
			}
		}
	}
}

func TestRememberRecordsThroughTheStore(t *testing.T) {
	t.Parallel()

	write := &Write{Store: newStore(t), Revision: testRevision()}
	if err := write.Remember(context.Background()); err != nil {
		t.Fatalf("Remember() error = %v", err)
	}
	if write.Recorded.Sequence != 1 {
		t.Errorf("Recorded is revision %d, want the first", write.Recorded.Sequence)
	}
	memories, problems, err := write.Store.Memories("product-manager")
	if err != nil {
		t.Fatalf("Memories() error = %v", err)
	}
	if len(problems) != 0 || len(memories) != 1 {
		t.Fatalf("Memories() returned %d memories and %v", len(memories), problems)
	}
}

func TestARefusedRoleWritesNothing(t *testing.T) {
	t.Parallel()

	revision := testRevision()
	revision.Agent = "reviewer"
	revision.Role = domain.RoleReviewer
	write := &Write{Store: newStore(t), Revision: revision}
	if err := write.Remember(context.Background()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Remember() error = %v, want ErrUnauthorized", err)
	}
	memories, _, err := write.Store.Memories("reviewer")
	if err != nil {
		t.Fatalf("Memories() error = %v", err)
	}
	if len(memories) != 0 {
		t.Errorf("a refused write left %d memories behind", len(memories))
	}
}

func TestEachActionOnlyPerformsItsOwnOperation(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	if err := (&Write{Store: store, Revision: testRevision()}).Remember(context.Background()); err != nil {
		t.Fatalf("Remember() error = %v", err)
	}

	// A remembering write that quietly compacted or retired would be a history
	// nobody could read back off the action that made it, which is the whole point
	// of three actions over one store.
	compacting := testRevision()
	compacting.Compacts = []int{1}
	if err := (&Write{Store: store, Revision: compacting}).Remember(context.Background()); err == nil {
		t.Error("Remember() performed a compaction")
	}
	retiring := testRevision()
	retiring.Retired = true
	if err := (&Write{Store: store, Revision: retiring}).Remember(context.Background()); err == nil {
		t.Error("Remember() performed a retirement")
	}
	if err := (&Write{Store: store, Revision: testRevision()}).Compact(context.Background()); err == nil {
		t.Error("Compact() recorded a revision that replaces nothing")
	}
	if err := (&Write{Store: store, Revision: testRevision()}).Retire(context.Background()); err == nil {
		t.Error("Retire() recorded a revision that retires nothing")
	}

	if err := (&Write{Store: store, Revision: compacting}).Compact(context.Background()); err != nil {
		t.Errorf("Compact() error = %v", err)
	}
	if err := (&Write{Store: store, Revision: retiring}).Retire(context.Background()); err != nil {
		t.Errorf("Retire() error = %v", err)
	}
}

func TestTheRegistryIsClosedAndDeclaresItsAuthority(t *testing.T) {
	t.Parallel()

	registry, err := Registry()
	if err != nil {
		t.Fatalf("Registry() error = %v", err)
	}
	names := registry.Names()
	want := []string{"agent-context.remember", "agent-context.compact", "agent-context.retire"}
	if len(names) != len(want) {
		t.Fatalf("Registry() holds %v, want %v", names, want)
	}
	for _, name := range want {
		registered, found := registry.Lookup(name)
		if !found {
			t.Fatalf("Registry() has no %s", name)
		}
		if len(registered.Capabilities) != 1 || registered.Capabilities[0] != capability.AgentContextMutate {
			t.Errorf("%s requires %v, want only %s", name, registered.Capabilities, capability.AgentContextMutate)
		}
	}
	if _, found := registry.Lookup("agent-context.read"); found {
		t.Error("Registry() offers an agent a read of its own memory, which the design keeps out of an agent's hands")
	}
}

func TestPerformingThroughTheRegistryReachesTheSameWrite(t *testing.T) {
	t.Parallel()

	registry, err := Registry()
	if err != nil {
		t.Fatalf("Registry() error = %v", err)
	}
	remember, found := registry.Lookup("agent-context.remember")
	if !found {
		t.Fatal("Registry() has no agent-context.remember")
	}
	write := &Write{Store: newStore(t), Revision: testRevision()}
	if err := remember.Perform(context.Background(), write); err != nil {
		t.Fatalf("Perform() error = %v", err)
	}
	if write.Recorded.Sequence != 1 {
		t.Errorf("Recorded is revision %d, want the first", write.Recorded.Sequence)
	}
}

func newStore(t *testing.T) *runstate.MemoryStore {
	t.Helper()
	store, err := runstate.NewMemoryStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	return store
}

func testRevision() runstate.MemoryRevision {
	return runstate.MemoryRevision{
		SchemaVersion: runstate.MemorySchemaVersion,
		ProductID:     "yoyodyne",
		Agent:         "product-manager",
		Role:          domain.RoleProductManager,
		Memory:        "how-the-operator-reads",
		Continuity:    runstate.MemoryContinuityAgent,
		Text:          "the operator reads reports at leisure",
		Invocation: runstate.MemoryInvocation{
			Kind:    runstate.MemoryInvocationConversation,
			ID:      "chat-0123456789abcdef0123456789abcdef",
			Turn:    4,
			Backend: "claude-code",
			Model:   "opus",
		},
	}
}
