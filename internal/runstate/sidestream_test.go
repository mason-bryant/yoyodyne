package runstate

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/sidestream"
)

func TestSideStreamStoreRoundTripsAcrossProcesses(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newSideStreamStore(t, root)
	stream := testSideStream(t)
	if _, err := store.Load(stream.ID); !errors.Is(err, ErrNoSideStream) {
		t.Fatalf("Load() error = %v, want ErrNoSideStream", err)
	}
	if err := store.Open(stream, sidestream.DefaultMaxPerAgent); err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	// What served the side thread is written down as it is for any other provider
	// invocation, because that is what durable-state-is-provider-independent asks
	// of one: a record naming a provider session and nothing that outlives it is a
	// thread nobody can reconstruct once the session is gone.
	stream.Backend = domain.BackendClaudeCode
	stream.ProviderSessionID = "session-side-1"
	stream.ProviderModel = "opus"
	stream.ProviderResolvedModel = "claude-opus-5-20260514"
	stream.AccountAlias = "research"
	stream.ConfigRevision = "cfg-0123456789ab"
	stream.Build = "9870df6a1b2c3d4e5f60718293a4b5c6d7e8f900"
	stream.Turns = 2
	stream.CostUSD = 0.42
	stream.LastSequence = 5
	stream.UpdatedAt = stream.OpenedAt.Add(time.Minute)
	if err := store.Save(stream); err != nil {
		t.Fatalf("Save() update error = %v", err)
	}

	// A second store over the same root is what a restarted process sees.
	loaded, err := newSideStreamStore(t, root).Load(stream.ID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !reflect.DeepEqual(loaded, stream) {
		t.Fatalf("Load() = %#v, want %#v", loaded, stream)
	}
	recorded, err := newSideStreamStore(t, root).List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 1 || recorded[0].ID != stream.ID {
		t.Fatalf("List() = %#v, want the one stream that was opened", recorded)
	}
}

// The concurrency claim the design rests on, held where it can be checked: a side
// stream is leased on its own, and the main thread's lease is untouched by it.
// One conversation still serializes its own turns, which is what stops two
// processes interleaving its transcript, and a side thread is not a second holder
// of that.
func TestASideStreamIsLeasedWithoutTouchingTheMainThreadsLease(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	streams := newSideStreamStore(t, root)
	conversations := newConversationStore(t, root)
	identity := ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager}
	stream := testSideStream(t)
	if err := streams.Open(stream, sidestream.DefaultMaxPerAgent); err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	held, taken, err := streams.Hold(stream.ID)
	if err != nil || !taken {
		t.Fatalf("Hold() = %v, %v, want the stream held", taken, err)
	}
	// The main thread is free while its side stream is held, which is the whole
	// point: a question put to an agent mid-turn no longer queues behind whatever
	// its main conversation is doing.
	main, err := conversations.Hold(identity)
	if err != nil {
		t.Fatalf("Hold(main) while a side stream is held error = %v", err)
	}
	if inFlight, err := conversations.InFlight(identity); !inFlight || err != nil {
		t.Fatalf("InFlight(main) = %v, %v, want the main thread's own hold reported", inFlight, err)
	}
	// And a second holder of the side stream is refused, exactly as a second
	// holder of the conversation is: one holder per stream.
	if _, again, err := newSideStreamStore(t, root).Hold(stream.ID); again || err != nil {
		t.Fatalf("second Hold() = %v, %v, want it refused", again, err)
	}
	if err := held.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	// Releasing the side stream leaves the main thread exactly as it was: still
	// held by the process that took it.
	if inFlight, err := conversations.InFlight(identity); !inFlight || err != nil {
		t.Fatalf("InFlight(main) after the side stream was released = %v, %v", inFlight, err)
	}
	if err := main.Release(); err != nil {
		t.Fatalf("Release(main) error = %v", err)
	}
	// A held main thread stops nothing here either, which is the same claim from
	// the other side.
	main, err = conversations.Hold(identity)
	if err != nil {
		t.Fatalf("Hold(main) error = %v", err)
	}
	regained, taken, err := streams.Hold(stream.ID)
	if err != nil || !taken {
		t.Fatalf("Hold() behind a held main thread = %v, %v, want the stream held", taken, err)
	}
	if err := errors.Join(regained.Release(), main.Release()); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
}

// The design forbids the two transcripts interleaving. They cannot, because
// neither store will name the other's log: each validates the identifier before it
// is a path, and the two identifiers have two shapes.
func TestASideStreamsTranscriptCannotJoinItsMainThreads(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	streams := newSideStreamStore(t, root)
	conversations := newConversationStore(t, root)
	conversation := testConversation(t)
	if err := conversations.Save(conversation); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	stream := testSideStream(t)
	stream.Conversation = conversation.ConversationID
	if err := streams.Open(stream, sidestream.DefaultMaxPerAgent); err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	sideTurn := testEvent(t, stream.ID, 1)
	mainTurn := testEvent(t, conversation.ConversationID, 1)
	if err := streams.AppendEvent(sideTurn); err != nil {
		t.Fatalf("AppendEvent(side) error = %v", err)
	}
	if err := conversations.AppendEvent(mainTurn); err != nil {
		t.Fatalf("AppendEvent(main) error = %v", err)
	}

	// Neither store will write the other's event, so an interleaved transcript is
	// a refusal rather than a log holding two threads.
	if err := streams.AppendEvent(mainTurn); err == nil || !strings.Contains(err.Error(), "is invalid") {
		t.Fatalf("AppendEvent(main into the side log) error = %v, want it refused", err)
	}
	if err := conversations.AppendEvent(sideTurn); err == nil || !strings.Contains(err.Error(), "conversation id is invalid") {
		t.Fatalf("AppendEvent(side into the main log) error = %v, want it refused", err)
	}

	sideEvents, err := newSideStreamStore(t, root).LoadEvents(stream.ID)
	if err != nil {
		t.Fatalf("LoadEvents(side) error = %v", err)
	}
	if len(sideEvents) != 1 || sideEvents[0].RunID != stream.ID {
		t.Fatalf("LoadEvents(side) = %#v, want the side thread's own turn alone", sideEvents)
	}
	mainEvents, err := newConversationStore(t, root).LoadEvents(conversation.ConversationID)
	if err != nil {
		t.Fatalf("LoadEvents(main) error = %v", err)
	}
	if len(mainEvents) != 1 || mainEvents[0].RunID != conversation.ConversationID {
		t.Fatalf("LoadEvents(main) = %#v, want the main thread's own turn alone", mainEvents)
	}
	// A stream whose log somehow held another thread's turn is a read that fails
	// rather than a transcript with two conversations in it.
	if err := newSideStreamStore(t, root).appendRaw(stream.ID, mainTurn); err != nil {
		t.Fatalf("appendRaw() error = %v", err)
	}
	if _, err := newSideStreamStore(t, root).LoadEvents(stream.ID); err == nil ||
		!strings.Contains(err.Error(), "event belongs to") {
		t.Fatalf("LoadEvents(side) over a mixed log error = %v, want it refused", err)
	}
}

func TestAnAgentOpensNoMoreSideStreamsThanItIsAllowed(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newSideStreamStore(t, root)
	const bound = 2
	opened := make([]sidestream.Stream, 0, bound)
	for held := 0; held < bound; held++ {
		stream := testSideStream(t)
		if err := store.Open(stream, bound); err != nil {
			t.Fatalf("Open() %d error = %v", held, err)
		}
		opened = append(opened, stream)
	}
	beyond := testSideStream(t)
	err := store.Open(beyond, bound)
	if !errors.Is(err, sidestream.ErrTooManyStreams) {
		t.Fatalf("Open() beyond the bound error = %v, want ErrTooManyStreams", err)
	}

	// The bound is on what an agent is holding rather than on what it has ever
	// held, so a stream that concluded makes room for the next question.
	concluded := opened[0]
	concluded.Outcome = sidestream.OutcomeConcluded
	ended := concluded.OpenedAt.Add(time.Hour)
	concluded.ClosedAt = &ended
	concluded.UpdatedAt = ended
	if err := store.Save(concluded); err != nil {
		t.Fatalf("Save(concluded) error = %v", err)
	}
	if err := store.Open(beyond, bound); err != nil {
		t.Fatalf("Open() after one concluded error = %v", err)
	}

	// Another agent's streams are not this one's: the bound is per agent, as the
	// lease and the record are.
	sibling := testSideStream(t)
	sibling.Agent = "development-manager"
	sibling.Role = domain.RoleDevelopmentManager
	if err := store.Open(sibling, bound); err != nil {
		t.Fatalf("Open(sibling) error = %v", err)
	}
	held, err := store.OpenFor("product-manager")
	if err != nil {
		t.Fatalf("OpenFor() error = %v", err)
	}
	if len(held) != bound {
		t.Fatalf("OpenFor() = %d streams, want %d", len(held), bound)
	}
}

// Counting what an agent holds and adding to it is one decision. Two processes
// opening at once would each count the same open streams and each open, so the
// second of them waits for nothing and is told to come back instead.
func TestTwoProcessesDoNotCountOneAgentsSideStreamsAtOnce(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newSideStreamStore(t, root)
	stream := testSideStream(t)
	elsewhere, taken, err := TryLeasePath(
		filepath.Join(store.Root(), "leases", stream.Agent+".opening.lease"),
		"a test standing in for another process")
	if err != nil || !taken {
		t.Fatalf("TryLeasePath() = %v, %v, want the opening lease held", taken, err)
	}
	if err := store.Open(stream, sidestream.DefaultMaxPerAgent); err == nil ||
		!strings.Contains(err.Error(), "another process is opening") {
		t.Fatalf("Open() while another process is opening error = %v, want it refused", err)
	}
	if err := elsewhere.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if err := store.Open(stream, sidestream.DefaultMaxPerAgent); err != nil {
		t.Fatalf("Open() once the other process is done error = %v", err)
	}
	// The opening lease is over opening and is put down before the stream is
	// carried, so the stream's own lease is still there to take.
	held, taken, err := store.Hold(stream.ID)
	if err != nil || !taken {
		t.Fatalf("Hold() after opening = %v, %v, want the stream held", taken, err)
	}
	if err := held.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
}

func TestOpeningARecordedSideStreamAgainIsRefusedRatherThanReplacingIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newSideStreamStore(t, root)
	stream := testSideStream(t)
	if err := store.Open(stream, sidestream.DefaultMaxPerAgent); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	revised := stream
	revised.Topic = "something else entirely"
	if err := store.Open(revised, sidestream.DefaultMaxPerAgent); err == nil ||
		!strings.Contains(err.Error(), "already recorded") {
		t.Fatalf("Open() over a recorded stream error = %v, want it refused", err)
	}
	loaded, err := store.Load(stream.ID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.Topic != stream.Topic {
		t.Fatalf("Load().Topic = %q, want the topic the stream was opened with", loaded.Topic)
	}
	// A stream that is already over is not one anything opens, and a bound of none
	// is not a configuration a side thread runs under.
	fresh := testSideStream(t)
	fresh.Outcome = sidestream.OutcomeConcluded
	ended := fresh.OpenedAt.Add(time.Hour)
	fresh.ClosedAt = &ended
	if err := store.Open(fresh, sidestream.DefaultMaxPerAgent); err == nil ||
		!strings.Contains(err.Error(), "already on it") {
		t.Fatalf("Open() of a concluded stream error = %v, want it refused", err)
	}
	if err := store.Open(testSideStream(t), 0); err == nil || !strings.Contains(err.Error(), "at least one") {
		t.Fatalf("Open() under a bound of none error = %v, want it refused", err)
	}
}

func TestASideStreamBelongsToTheProductItIsStoredUnder(t *testing.T) {
	t.Parallel()

	store := newSideStreamStore(t, t.TempDir())
	stream := testSideStream(t)
	stream.ProductID = "somebody-else"
	if err := store.Open(stream, sidestream.DefaultMaxPerAgent); err == nil ||
		!strings.Contains(err.Error(), "does not match store product") {
		t.Fatalf("Open() error = %v, want the product refused", err)
	}
	if _, err := store.Load("chat-0123456789abcdef0123456789abcdef"); err == nil ||
		!strings.Contains(err.Error(), "is invalid") {
		t.Fatalf("Load(a conversation id) error = %v, want it refused", err)
	}
}

// appendRaw writes an event into a stream's log without asking whose it is. It
// exists so a test can produce the mixed log a read has to refuse, which nothing
// the store exports will write.
func (s *SideStreamStore) appendRaw(id string, event execution.Event) error {
	path, err := s.eventPath(id)
	if err != nil {
		return err
	}
	encoded, err := encodeEvent(event)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(encoded); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

func newSideStreamStore(t *testing.T, root string) *SideStreamStore {
	t.Helper()

	store, err := NewSideStreamStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewSideStreamStore() error = %v", err)
	}
	return store
}

func testSideStream(t *testing.T) sidestream.Stream {
	t.Helper()

	id, err := sidestream.NewID()
	if err != nil {
		t.Fatalf("NewID() error = %v", err)
	}
	opened := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	return sidestream.Stream{
		SchemaVersion: sidestream.SchemaVersion,
		ID:            id,
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		Agent:         "product-manager",
		Role:          domain.RoleProductManager,
		Conversation:  "chat-0123456789abcdef0123456789abcdef",
		Topic:         "whether the intake hold covers work an operator named",
		MaxTurns:      sidestream.DefaultMaxTurns,
		OpenedAt:      opened,
		UpdatedAt:     opened,
	}
}

func testEvent(t *testing.T, runID string, sequence uint64) execution.Event {
	t.Helper()

	event, err := execution.NewEvent(runID, sequence, time.Date(2026, 9, 7, 12, 30, 0, 0, time.UTC),
		execution.EventAgentMessage, "provider.claude-code", nil)
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}
	return event
}
