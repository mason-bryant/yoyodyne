package runstate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

func TestMemoryStoreKeepsRevisionsAppendOnlyWithTheirProvenance(t *testing.T) {
	t.Parallel()

	store := newMemoryStore(t, t.TempDir())
	first, err := store.Remember(context.Background(), testMemoryRevision())
	if err != nil {
		t.Fatalf("Remember() error = %v", err)
	}
	if first.Sequence != 1 {
		t.Fatalf("first revision is numbered %d, want 1", first.Sequence)
	}

	second := testMemoryRevision()
	second.Text = "the operator reads reports at leisure and asks for the plain word"
	second.Invocation.Turn = 9
	if _, err := store.Remember(context.Background(), second); err != nil {
		t.Fatalf("Remember() error = %v", err)
	}

	memories, problems, err := store.Memories("product-manager")
	if err != nil {
		t.Fatalf("Memories() error = %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("Memories() reported %v", problems)
	}
	if len(memories) != 1 {
		t.Fatalf("Memories() returned %d memories, want 1", len(memories))
	}
	memory := memories[0]
	if len(memory.Revisions) != 2 {
		t.Fatalf("the memory has %d revisions, want 2", len(memory.Revisions))
	}
	// The earlier revision is still there and still says what it said. That is the
	// whole of what append-only buys: an operator asking what an agent used to
	// believe gets an answer rather than the current answer twice.
	if memory.Revisions[0].Text == memory.Revisions[1].Text {
		t.Errorf("the first revision was overwritten by the second")
	}
	if memory.Current().Sequence != 2 {
		t.Errorf("Current() is revision %d, want 2", memory.Current().Sequence)
	}
	// The audit the design requires: every revision says which invocation wrote it,
	// pinned to the backend, the model, the account, and the configuration.
	for _, revision := range memory.Revisions {
		if revision.Invocation.ID != "chat-0123456789abcdef0123456789abcdef" {
			t.Errorf("revision %d names invocation %q", revision.Sequence, revision.Invocation.ID)
		}
		if revision.Invocation.Backend == "" || revision.Invocation.Model == "" {
			t.Errorf("revision %d records no backend or model", revision.Sequence)
		}
		if revision.Invocation.AccountAlias == "" || revision.Invocation.ConfigRevision == "" {
			t.Errorf("revision %d records no account or configuration", revision.Sequence)
		}
	}
	if memory.Revisions[0].Invocation.Turn == memory.Revisions[1].Invocation.Turn {
		t.Errorf("both revisions name turn %d, so the audit cannot tell them apart", memory.Revisions[0].Invocation.Turn)
	}
}

func TestMemoryStoreNumbersRevisionsItself(t *testing.T) {
	t.Parallel()

	store := newMemoryStore(t, t.TempDir())
	numbered := testMemoryRevision()
	numbered.Sequence = 7
	if _, err := store.Remember(context.Background(), numbered); err == nil {
		t.Fatal("Remember() accepted a revision that numbered itself")
	}
}

func TestMemoryStoreRedactsBeforeItPersists(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store, err := NewMemoryStore(root, "yoyodyne", "sk-secret-token")
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	revision := testMemoryRevision()
	revision.Text = "the forge accepts sk-secret-token for this product"
	recorded, err := store.Remember(context.Background(), revision)
	if err != nil {
		t.Fatalf("Remember() error = %v", err)
	}
	if strings.Contains(recorded.Text, "sk-secret-token") {
		t.Errorf("Remember() returned the value unredacted: %q", recorded.Text)
	}
	// What matters is the disk rather than the return: a memory is read back by a
	// later invocation out of the file, and a redaction that only reached the
	// caller would be no redaction at all.
	stored, err := os.ReadFile(filepath.Join(store.Root(), "product-manager.memory.jsonl"))
	if err != nil {
		t.Fatalf("read the memory log: %v", err)
	}
	if strings.Contains(string(stored), "sk-secret-token") {
		t.Errorf("the memory log holds the unredacted value: %s", stored)
	}
}

func TestMemoryStoreRefusesAWriteThatWouldExceedTheLiveBudget(t *testing.T) {
	t.Parallel()

	store := newMemoryStore(t, t.TempDir())
	// One memory per write, each at the per-revision bound, until the live budget
	// refuses one. Everything before the refusal is stored, so what is measured is
	// what the agent would know rather than what it has written over time.
	for index := 0; ; index++ {
		revision := testMemoryRevision()
		revision.Memory = fmt.Sprintf("what-i-learned-%d", index)
		revision.Text = strings.Repeat("a", MaxMemoryTextBytes)
		_, err := store.Remember(context.Background(), revision)
		if err == nil {
			if index > MaxMemoryLiveBytes/MaxMemoryTextBytes {
				t.Fatalf("wrote %d memories of %d bytes with a %d byte budget", index+1, MaxMemoryTextBytes, MaxMemoryLiveBytes)
			}
			continue
		}
		if !errors.Is(err, ErrMemoryBudget) {
			t.Fatalf("Remember() error = %v, want ErrMemoryBudget", err)
		}
		break
	}

	// Retiring one makes room, because the budget is what the agent still knows
	// rather than what the log holds.
	retirement := testMemoryRevision()
	retirement.Memory = "what-i-learned-0"
	retirement.Text = "this stopped being true when the checks changed"
	retirement.Retired = true
	if _, err := store.Remember(context.Background(), retirement); err != nil {
		t.Fatalf("Remember() a retirement error = %v", err)
	}
	fresh := testMemoryRevision()
	fresh.Memory = "what-i-learned-next"
	fresh.Text = strings.Repeat("b", MaxMemoryTextBytes)
	if _, err := store.Remember(context.Background(), fresh); err != nil {
		t.Fatalf("Remember() after a retirement error = %v", err)
	}
}

// TestAFullLogRollsRatherThanRefusing is the property the log size has to have:
// it is where the history is set aside, never a size at which the store stops
// working. A wall here would be one nothing could climb back over, because the way
// out of a full store is to compact or to retire, and both are writes on the path
// the wall would be in.
func TestAFullLogRollsRatherThanRefusing(t *testing.T) {
	t.Parallel()

	store := newMemoryStore(t, t.TempDir())
	// Small enough that a handful of revisions of one memory crosses it, so the
	// roll is reached without writing the four megabytes the harness's own bound is.
	store.rollAt = 3000

	for round := 0; round < 12; round++ {
		revision := testMemoryRevision()
		revision.Text = fmt.Sprintf("what the operator wanted on round %d: %s", round, strings.Repeat("x", 400))
		if _, err := store.Remember(context.Background(), revision); err != nil {
			t.Fatalf("Remember() on round %d error = %v", round, err)
		}
	}

	archives, err := store.archivePaths("product-manager")
	if err != nil {
		t.Fatalf("archivePaths() error = %v", err)
	}
	if len(archives) == 0 {
		t.Fatal("the log grew past its size and nothing was rolled aside")
	}
	if size, err := store.logSize("product-manager", filepath.Join(store.Root(), "product-manager.memory.jsonl")); err != nil {
		t.Fatalf("logSize() error = %v", err)
	} else if size > store.rollAt {
		t.Errorf("the live log is %d bytes and the roll is at %d", size, store.rollAt)
	}

	// The history is whole across the archives and the log: twelve revisions were
	// written and twelve are readable, each one still naming the invocation that
	// produced it, and none of them counted twice because the roll carried it
	// across.
	memories, problems, err := store.Memories("product-manager")
	if err != nil {
		t.Fatalf("Memories() error = %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("Memories() reported %v", problems)
	}
	if len(memories) != 1 {
		t.Fatalf("Memories() returned %d memories, want 1", len(memories))
	}
	revisions := memories[0].Revisions
	if len(revisions) != 12 {
		t.Fatalf("the memory has %d revisions, want the 12 that were written", len(revisions))
	}
	for index, revision := range revisions {
		if revision.Sequence != index+1 {
			t.Fatalf("revision %d is numbered %d; the history is out of order or a number was reused", index, revision.Sequence)
		}
		if revision.Invocation.ID == "" {
			t.Errorf("revision %d lost the invocation that produced it in the roll", revision.Sequence)
		}
	}
	if !strings.Contains(revisions[11].Text, "round 11") {
		t.Errorf("the last revision is %q, want the one written last", revisions[11].Text)
	}
}

// TestARolledStoreStillCompactsAndRetires is the other half of the finding the
// roll answers: the operations that make room have to keep working at the size
// where room runs out, or a full store is a permanently stuck one.
func TestARolledStoreStillCompactsAndRetires(t *testing.T) {
	t.Parallel()

	store := newMemoryStore(t, t.TempDir())
	store.rollAt = 2000
	for round := 0; round < 6; round++ {
		revision := testMemoryRevision()
		revision.Text = fmt.Sprintf("round %d: %s", round, strings.Repeat("y", 300))
		if _, err := store.Remember(context.Background(), revision); err != nil {
			t.Fatalf("Remember() on round %d error = %v", round, err)
		}
	}
	compaction := testMemoryRevision()
	compaction.Text = "all of that, said once"
	compaction.Compacts = []int{1, 2, 3, 4, 5, 6}
	if _, err := store.Remember(context.Background(), compaction); err != nil {
		t.Fatalf("Remember() a compaction into a rolled store error = %v", err)
	}
	retirement := testMemoryRevision()
	retirement.Text = "and it stopped being true"
	retirement.Retired = true
	if _, err := store.Remember(context.Background(), retirement); err != nil {
		t.Fatalf("Remember() a retirement into a rolled store error = %v", err)
	}
	memories, problems, err := store.Memories("product-manager")
	if err != nil {
		t.Fatalf("Memories() error = %v", err)
	}
	if len(problems) != 0 {
		t.Fatalf("Memories() reported %v", problems)
	}
	if got := len(memories[0].Revisions); got != 8 {
		t.Fatalf("the memory has %d revisions, want the 8 that were written", got)
	}
	if !memories[0].Retired() {
		t.Errorf("the memory is not retired after a retirement was recorded")
	}
	// A compaction written across a roll still names what it replaced, which is the
	// provenance the design requires of one.
	if got := memories[0].Revisions[6].Compacts; len(got) != 6 {
		t.Errorf("the compaction names %v, want the six revisions it replaced", got)
	}
}

func TestMemoryStoreRefusesTextBeyondOneRevisionsBound(t *testing.T) {
	t.Parallel()

	store := newMemoryStore(t, t.TempDir())
	revision := testMemoryRevision()
	revision.Text = strings.Repeat("a", MaxMemoryTextBytes+1)
	if _, err := store.Remember(context.Background(), revision); err == nil {
		t.Fatal("Remember() accepted a revision past the per-revision bound")
	}
}

func TestMemoryStoreCompactionNamesWhatItReplaced(t *testing.T) {
	t.Parallel()

	store := newMemoryStore(t, t.TempDir())
	for _, text := range []string{"reports are read at leisure", "the plain word is preferred", "metaphor is cut"} {
		revision := testMemoryRevision()
		revision.Text = text
		if _, err := store.Remember(context.Background(), revision); err != nil {
			t.Fatalf("Remember() error = %v", err)
		}
	}
	compaction := testMemoryRevision()
	compaction.Text = "the operator reads at leisure and wants the plain word, without metaphor"
	compaction.Compacts = []int{1, 2, 3}
	recorded, err := store.Remember(context.Background(), compaction)
	if err != nil {
		t.Fatalf("Remember() a compaction error = %v", err)
	}
	if !recorded.Compacted() {
		t.Errorf("the compaction does not report itself as one")
	}

	memories, _, err := store.Memories("product-manager")
	if err != nil {
		t.Fatalf("Memories() error = %v", err)
	}
	memory := memories[0]
	if !memory.Compacted() {
		t.Errorf("the memory does not say its current revision was compacted")
	}
	// The provenance is the point: the compacted revisions are still readable, and
	// the compaction says which ones it stands for.
	if len(memory.Revisions) != 4 {
		t.Fatalf("the memory has %d revisions, want 4", len(memory.Revisions))
	}
	if got := memory.Current().Compacts; len(got) != 3 {
		t.Errorf("the compaction names %v, want three revisions", got)
	}
}

func TestMemoryStoreRefusesACompactionOfARevisionThatIsNotThere(t *testing.T) {
	t.Parallel()

	store := newMemoryStore(t, t.TempDir())
	if _, err := store.Remember(context.Background(), testMemoryRevision()); err != nil {
		t.Fatalf("Remember() error = %v", err)
	}
	compaction := testMemoryRevision()
	compaction.Compacts = []int{4}
	if _, err := store.Remember(context.Background(), compaction); err == nil {
		t.Fatal("Remember() accepted provenance pointing at a revision that is not there")
	}
}

func TestMemoryStoreSeparatesWhatIsLiveFromWhatIsRecorded(t *testing.T) {
	t.Parallel()

	store := newMemoryStore(t, t.TempDir())
	kept := testMemoryRevision()
	kept.Memory = "how-the-operator-reads"
	if _, err := store.Remember(context.Background(), kept); err != nil {
		t.Fatalf("Remember() error = %v", err)
	}
	subject := testMemoryRevision()
	subject.Memory = "what-this-item-needs"
	subject.Continuity = MemoryContinuitySubject
	subject.Subject = "yoyodyne-ifd.308"
	if _, err := store.Remember(context.Background(), subject); err != nil {
		t.Fatalf("Remember() a subject memory error = %v", err)
	}
	retirement := subject
	retirement.Sequence = 0
	retirement.Text = "the item landed, so this is done with"
	retirement.Retired = true
	if _, err := store.Remember(context.Background(), retirement); err != nil {
		t.Fatalf("Remember() a retirement error = %v", err)
	}

	live, _, err := store.Live("product-manager")
	if err != nil {
		t.Fatalf("Live() error = %v", err)
	}
	if len(live) != 1 || live[0].Name != "how-the-operator-reads" {
		t.Fatalf("Live() returned %v, want only the memory that was not retired", live)
	}
	// The retired memory is out of the live set and still in the record, which is
	// what makes retiring one different from removing it.
	recorded, _, err := store.Memories("product-manager")
	if err != nil {
		t.Fatalf("Memories() error = %v", err)
	}
	if len(recorded) != 2 {
		t.Fatalf("Memories() returned %d memories, want 2", len(recorded))
	}
}

func TestMemoryStoreRefusesAMemoryThatChangesWhatItIsAbout(t *testing.T) {
	t.Parallel()

	store := newMemoryStore(t, t.TempDir())
	if _, err := store.Remember(context.Background(), testMemoryRevision()); err != nil {
		t.Fatalf("Remember() error = %v", err)
	}
	moved := testMemoryRevision()
	moved.Continuity = MemoryContinuitySubject
	moved.Subject = "yoyodyne-ifd.308"
	if _, err := store.Remember(context.Background(), moved); err == nil {
		t.Fatal("Remember() accepted a memory that changed what it is about")
	}
}

func TestMemoryStoreReportsAnUnreadableLineWithoutLosingTheRest(t *testing.T) {
	t.Parallel()

	store := newMemoryStore(t, t.TempDir())
	if _, err := store.Remember(context.Background(), testMemoryRevision()); err != nil {
		t.Fatalf("Remember() error = %v", err)
	}
	path := filepath.Join(store.Root(), "product-manager.memory.jsonl")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("open the memory log: %v", err)
	}
	if _, err := file.WriteString("{not a memory}\n"); err != nil {
		t.Fatalf("write a corrupt line: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close the memory log: %v", err)
	}

	memories, problems, err := store.Memories("product-manager")
	if err != nil {
		t.Fatalf("Memories() error = %v", err)
	}
	if len(memories) != 1 {
		t.Fatalf("Memories() returned %d memories, want the one that reads", len(memories))
	}
	if len(problems) != 1 || problems[0].Line != 2 {
		t.Fatalf("Memories() reported %v, want one problem on line 2", problems)
	}
	// The file is named as well as the line: an agent's history is its log and the
	// archives rolled off it, so a line number alone sends a reader to the wrong
	// file.
	if problems[0].Log != "product-manager.memory.jsonl" {
		t.Errorf("the problem names log %q, want the live log", problems[0].Log)
	}
	// A writer may not carry on over a line it could not read, because the number
	// it is about to assign is worked out from what it could read.
	if _, err := store.Remember(context.Background(), testMemoryRevision()); err == nil {
		t.Fatal("Remember() wrote into a log with an unreadable line")
	}
}

func TestMemoryStoreReportsARevisionThatBelongsToAnotherAgent(t *testing.T) {
	t.Parallel()

	store := newMemoryStore(t, t.TempDir())
	if _, err := store.Remember(context.Background(), testMemoryRevision()); err != nil {
		t.Fatalf("Remember() error = %v", err)
	}
	transplanted := testMemoryRevision()
	transplanted.Agent = "architect"
	transplanted.Role = domain.RoleArchitect
	transplanted.Sequence = 2
	encoded, err := encodeMemoryRevision(transplanted)
	if err != nil {
		t.Fatalf("encodeMemoryRevision() error = %v", err)
	}
	path := filepath.Join(store.Root(), "product-manager.memory.jsonl")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatalf("open the memory log: %v", err)
	}
	if _, err := file.Write(encoded); err != nil {
		t.Fatalf("write the transplanted revision: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close the memory log: %v", err)
	}

	_, problems, err := store.Memories("product-manager")
	if err != nil {
		t.Fatalf("Memories() error = %v", err)
	}
	if len(problems) != 1 || !strings.Contains(problems[0].Problem, "architect") {
		t.Fatalf("Memories() reported %v, want the transplanted revision named", problems)
	}
}

func TestMemoryStoreListsTheAgentsItHolds(t *testing.T) {
	t.Parallel()

	store := newMemoryStore(t, t.TempDir())
	agents, err := store.Agents()
	if err != nil {
		t.Fatalf("Agents() error = %v", err)
	}
	if len(agents) != 0 {
		t.Fatalf("Agents() returned %v before anything was written", agents)
	}
	for _, agent := range []string{"product-manager", "architect"} {
		revision := testMemoryRevision()
		revision.Agent = agent
		if agent == "architect" {
			revision.Role = domain.RoleArchitect
		}
		if _, err := store.Remember(context.Background(), revision); err != nil {
			t.Fatalf("Remember() for %s error = %v", agent, err)
		}
	}
	agents, err = store.Agents()
	if err != nil {
		t.Fatalf("Agents() error = %v", err)
	}
	if len(agents) != 2 || agents[0] != "architect" || agents[1] != "product-manager" {
		t.Fatalf("Agents() returned %v, want both in name order", agents)
	}
}

func TestMemoryStoreRefusesAnotherProductsRevision(t *testing.T) {
	t.Parallel()

	store := newMemoryStore(t, t.TempDir())
	elsewhere := testMemoryRevision()
	elsewhere.ProductID = "beads"
	if _, err := store.Remember(context.Background(), elsewhere); err == nil {
		t.Fatal("Remember() accepted a revision belonging to another product")
	}
}

func TestMemoryStoreRefusesAnAgentNameThatIsAPath(t *testing.T) {
	t.Parallel()

	store := newMemoryStore(t, t.TempDir())
	escaping := testMemoryRevision()
	escaping.Agent = "../../elsewhere"
	if _, err := store.Remember(context.Background(), escaping); err == nil {
		t.Fatal("Remember() accepted an agent name that is a path")
	}
	if _, _, err := store.Memories("../../elsewhere"); err == nil {
		t.Fatal("Memories() accepted an agent name that is a path")
	}
}

// TestAConversationRecordRefusesAMemory is the design's "no fourth store" rule
// held to the record it is most likely to be broken in. Memory is a store of its
// own that may reference a conversation and copies none of it; a memory written
// into the conversation record instead would be a second copy of what an agent
// knows, in a record every turn rewrites in place.
//
// Nothing has to be added to refuse it — the conversation decoder reads nothing it
// does not declare — and it is pinned here because that decoder's strictness is
// what the rule now rests on.
func TestAConversationRecordRefusesAMemory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store, err := NewConversationStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewConversationStore() error = %v", err)
	}
	if err := os.MkdirAll(store.Root(), 0o700); err != nil {
		t.Fatalf("create the conversation directory: %v", err)
	}
	record := `{"schema_version":1,"conversation_id":"chat-0123456789abcdef0123456789abcdef",` +
		`"product_id":"yoyodyne","repository_id":"yoyodyne","agent":"product-manager",` +
		`"role":"product-manager","backend":"claude-code","turns":0,"last_sequence":0,` +
		`"started_at":"2026-09-06T09:00:00Z","updated_at":"2026-09-06T09:00:00Z",` +
		`"memories":[{"memory":"how-the-operator-reads","text":"at leisure"}]}`
	path := filepath.Join(store.Root(), "product-manager.json")
	if err := os.WriteFile(path, []byte(record), 0o600); err != nil {
		t.Fatalf("write the conversation record: %v", err)
	}
	_, err = store.Load(ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager})
	if err == nil {
		t.Fatal("Load() accepted a conversation record carrying memories")
	}
	if !strings.Contains(err.Error(), "memories") {
		t.Errorf("Load() error = %v, want the refused field named", err)
	}
}

func newMemoryStore(t *testing.T, root string) *MemoryStore {
	t.Helper()
	store, err := NewMemoryStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	return store
}

// testMemoryRevision is one revision as an agent would offer it: unnumbered,
// with the invocation that produced it pinned to everything a durable provider
// invocation records.
func testMemoryRevision() MemoryRevision {
	return MemoryRevision{
		SchemaVersion: MemorySchemaVersion,
		ProductID:     "yoyodyne",
		Agent:         "product-manager",
		Role:          domain.RoleProductManager,
		Memory:        "how-the-operator-reads",
		Continuity:    MemoryContinuityAgent,
		Text:          "the operator reads reports at leisure",
		Sources: []MemorySource{
			{Kind: MemorySourceConversation, ID: "chat-0123456789abcdef0123456789abcdef"},
			{Kind: MemorySourceWorkItem, ID: "yoyodyne-ifd.308"},
		},
		Invocation: MemoryInvocation{
			Kind:           MemoryInvocationConversation,
			ID:             "chat-0123456789abcdef0123456789abcdef",
			Turn:           4,
			Backend:        "claude-code",
			Model:          "opus",
			ResolvedModel:  "claude-opus-5-20260514",
			AccountAlias:   "research",
			ConfigRevision: "cfg-0123456789ab",
			Build:          "9870df6a1b2c3d4e5f60718293a4b5c6d7e8f900",
		},
		RecordedAt: time.Date(2026, 9, 6, 9, 0, 0, 0, time.UTC),
	}
}
