package chat

// The side-thread runner driven against the real durable stores: a main
// conversation held by one process, a side thread asked and answered beside it,
// its conclusion merged through the real memory write, the main thread's next
// turn reading it, and the two records and the two transcripts staying apart.
//
// It lives here rather than in `internal/sidestream` because the separation it
// pins is between that package's stores and the conversation's, and
// `internal/runstate` owns both — which the side stream package cannot import,
// because runstate imports it.

import (
	"context"
	"errors"
	"reflect"
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

// The whole path, end to end: the product manager's main thread is held by
// another process, a question is put to it on a side thread anyway, it answers,
// the concluded thread merges into the agent's memory through the real write, and
// the main thread's next turn reads the merge — all of it against the stores a
// running harness uses, with the main thread's lease never asked for.
func TestASideThreadAnswersWhileTheMainThreadIsHeldAndTheMainThreadReadsIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	conversations := newTestStore(t, root)
	memories, err := runstate.NewMemoryStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	streams, err := runstate.NewSideStreamStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewSideStreamStore() error = %v", err)
	}

	// The main conversation, as a running harness holds it: recorded in the same
	// store, reading the same memory. Its one provider turn is the one taken at the
	// end, after the side thread has concluded.
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-main-1", ResolvedModel: "claude-opus-5", FinalText: "Noted; I will say so in the brief myself."},
	}}
	options := testOptions(t, provider)
	options.Store = conversations
	options.Memories = memories
	session := openTestSession(t, options)
	mainThread := session.state.ConversationID
	identity := runstate.ConversationIdentity{Agent: options.Agent, Role: options.Role}
	before, err := conversations.LoadEvents(mainThread)
	if err != nil {
		t.Fatalf("LoadEvents() error = %v", err)
	}

	// The main thread is held, exactly as it is while a long turn is being taken:
	// nothing else can have it, and today that is what makes every inbound
	// question queue behind whatever it is doing.
	main, err := conversations.Hold(identity)
	if err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	defer main.Release()
	if _, err := conversations.Hold(identity); !errors.Is(err, runstate.ErrConversationHeld) {
		t.Fatalf("a second hold on the main thread error = %v, want it refused; the test is not holding what it thinks it is", err)
	}

	voice := &sideVoiceStub{
		reply: "The hold is read before the item is chosen, so work the operator named is exempt.\n\n" +
			sidestream.Fence + "\n{\"side\":{\"concluded\":true,\"commitments\":[\"say so in the next brief\"]}}\n" + "```" + "\n",
	}
	at := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	runner := sidestream.Runner{
		Store:  streams,
		Leases: streams,
		Voice:  voice,
		// The merge is the real one: the agent-context write over the same memory
		// store the main conversation reads.
		Merge: agentcontext.Merger{Streams: streams, Memory: memories},
		// The side thread's authority is the role's own, narrowed to judgment and
		// reading. It is not passed to the runner at all: it is settled in
		// `internal/sidestream` and pinned by the tests beside this one, and what
		// the runner carries is the record, the lease, and the turn.
		ProductID:    "yoyodyne",
		RepositoryID: "yoyodyne",
		Now: func() time.Time {
			at = at.Add(time.Second)
			return at
		},
	}

	answer, err := runner.Put(context.Background(), sidestream.Ask{
		Agent:        identity.Agent,
		Role:         identity.Role,
		Conversation: mainThread,
		Topic:        "whether the intake hold covers work an operator named",
		Question:     "does the intake hold cover work the operator named?",
	})
	if err != nil {
		t.Fatalf("Put() while the main thread is held error = %v", err)
	}
	if answer.Prose != "The hold is read before the item is chosen, so work the operator named is exempt." {
		t.Fatalf("Put().Prose = %q, want the answer with its block taken out", answer.Prose)
	}
	if !answer.Tentative() {
		t.Fatal("the answer promised something and does not report itself tentative; the main thread has yet to ratify it")
	}

	// The side thread's own record is on disk, concluded, and says what served it.
	recorded, err := streams.Load(answer.Stream.ID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if recorded.Open() || recorded.Outcome != sidestream.OutcomeConcluded {
		t.Fatalf("the side stream is recorded as %#v, want it concluded", recorded)
	}
	if recorded.Conversation != mainThread || recorded.Agent != identity.Agent || recorded.Role != identity.Role {
		t.Fatalf("the side stream records %#v, want it beside the product manager's own main thread", recorded)
	}
	if recorded.Turns != 1 || recorded.LastSequence != 2 || recorded.ProviderSessionID != "session-side-1" {
		t.Fatalf("the side stream recorded %d turn(s) reaching sequence %d in session %q, want one turn, two events, and its own session",
			recorded.Turns, recorded.LastSequence, recorded.ProviderSessionID)
	}

	// The concluded thread is in the agent's memory: one revision, named for the
	// stream, citing the stream and the main thread, written by the side thread's
	// own invocation — and carrying the account and the draft rather than the
	// transcript.
	live, problems, err := memories.Live(identity.Agent)
	if err != nil {
		t.Fatalf("Live() error = %v", err)
	}
	if len(problems) != 0 || len(live) != 1 {
		t.Fatalf("Live() = %d memories with %v, want the one merge", len(live), problems)
	}
	merged := live[0].Current()
	if merged.Memory != recorded.ID || merged.Invocation.Kind != runstate.MemoryInvocationSideStream || merged.Invocation.ID != recorded.ID {
		t.Fatalf("the merge is %q written by %s %s, want the stream's own memory written by its side turns", merged.Memory, merged.Invocation.Kind, merged.Invocation.ID)
	}
	if merged.Invocation.ResolvedModel != "claude-opus-5" || merged.Invocation.AccountAlias != "research" {
		t.Fatalf("the merge is pinned to %+v, want what served the side turn", merged.Invocation)
	}
	if !cites(merged, runstate.MemorySourceConversation, mainThread) || !cites(merged, runstate.MemorySourceSideStream, recorded.ID) {
		t.Fatalf("the merge cites %v, want the main thread and the side stream", merged.Sources)
	}
	if strings.Contains(merged.Text, "yoyodyne-side") {
		t.Fatal("the merge carried the thread's block into the agent's memory; what merges is an account and never a transcript")
	}

	// The two transcripts never met. The turn's events are in the side stream's
	// own log, named for its own identifier, and the main thread's log gained
	// nothing.
	sideEvents, err := streams.LoadEvents(recorded.ID)
	if err != nil {
		t.Fatalf("LoadEvents() error = %v", err)
	}
	if len(sideEvents) != 2 {
		t.Fatalf("the side thread's log holds %d event(s), want the two its turn wrote", len(sideEvents))
	}
	for _, event := range sideEvents {
		if event.RunID != recorded.ID {
			t.Fatalf("a side thread's event names %s, want %s", event.RunID, recorded.ID)
		}
	}
	mainEvents, err := conversations.LoadEvents(mainThread)
	if err != nil {
		t.Fatalf("LoadEvents() on the main thread error = %v", err)
	}
	if !reflect.DeepEqual(mainEvents, before) {
		t.Fatalf("the main thread's log holds %d event(s) after a side turn, want the %d it already had", len(mainEvents), len(before))
	}

	// And the main thread's lease was never asked for: this process still holds
	// it, and nothing the side turn did reached it.
	if _, err := conversations.Hold(identity); !errors.Is(err, runstate.ErrConversationHeld) {
		t.Fatalf("the main thread's lease after a side turn = %v, want it still held by this process", err)
	}

	// The long turn finishes, and the main thread's next turn reads what the side
	// thread concluded — as memory, with the thread named and the promise still a
	// promise, and with none of the side thread's dialogue in it.
	if err := main.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	reply, err := session.Send(context.Background(), "What did you find out about the hold?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if reply.Text != "Noted; I will say so in the brief myself." {
		t.Fatalf("Send().Text = %q", reply.Text)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("invocations = %d, want the one main turn; the side turn was the stub's", len(provider.requests))
	}
	prompt := provider.requests[0].Prompt
	for _, required := range []string{
		"# Side conversations held beside this one",
		"tentative until you ratify or adjust it here",
		recorded.ID,
		"so work the operator named is exempt",
		"- say so in the next brief",
		merged.Text,
	} {
		if !strings.Contains(prompt, required) {
			t.Errorf("the main thread's next turn does not carry %q:\n%s", required, prompt)
		}
	}
	if strings.Contains(prompt, "side-thread") || strings.Contains(prompt, "does the intake hold cover work the operator named?") {
		t.Errorf("the side thread's dialogue was replayed into the main thread:\n%s", prompt)
	}
	if provider.requests[0].RunID != mainThread {
		t.Fatalf("the main turn was taken as %s, want the main thread's own", provider.requests[0].RunID)
	}
}

// Two side threads of one agent run beside each other and beside the main
// thread, each under its own lease. It is the same separation the test above
// pins, taken one step further: what the single-holder rule governs is the main
// conversation, and a side stream is never a second holder of it or of another
// side stream.
func TestTwoSideThreadsOfOneAgentDoNotContend(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	streams, err := runstate.NewSideStreamStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewSideStreamStore() error = %v", err)
	}
	runner := sidestream.Runner{
		Store:  streams,
		Leases: streams,
		// Neither thread concludes, so each stays open and neither reaches a merge.
		Voice:     &sideVoiceStub{reply: "I need another turn."},
		ProductID: "yoyodyne",
	}
	ask := sidestream.Ask{
		Agent:        "product-manager",
		Role:         domain.RoleProductManager,
		Conversation: mainThreadID,
		Topic:        "the first thing",
		Question:     "the first question?",
	}

	first, err := runner.Put(context.Background(), ask)
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	// The first thread's lease is taken and given back around its turn, so the
	// second is refused nothing.
	ask.Topic = "the second thing"
	second, err := runner.Put(context.Background(), ask)
	if err != nil {
		t.Fatalf("Put() for a second side thread error = %v", err)
	}
	if second.Stream.ID == first.Stream.ID {
		t.Fatal("the second question continued the first thread rather than opening its own")
	}
	held, err := streams.OpenFor("product-manager")
	if err != nil {
		t.Fatalf("OpenFor() error = %v", err)
	}
	if len(held) != 2 {
		t.Fatalf("the agent is holding %d side thread(s), want two", len(held))
	}

	// And the bound holds: the default is three per agent, so a fourth is refused
	// as the ordinary answer at a busy moment rather than as a fault.
	runner.MaxPerAgent = 2
	ask.Topic = "the third thing"
	if _, err := runner.Put(context.Background(), ask); !errors.Is(err, sidestream.ErrTooManyStreams) {
		t.Fatalf("Put() past the per-agent bound error = %v, want ErrTooManyStreams", err)
	}
}

// mainThreadID is the conversation a side thread in these tests is held beside.
const mainThreadID = "chat-0123456789abcdef0123456789abcdef"

// sideVoiceStub is one provider invocation on a side thread, writing the events
// a real one writes into the sink the runner hands it — which is the side
// stream's own log and nothing else.
type sideVoiceStub struct {
	reply string
	asked []sidestream.Question
}

func (v *sideVoiceStub) Answer(_ context.Context, question sidestream.Question) (sidestream.Spoken, error) {
	v.asked = append(v.asked, question)
	at := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	for sequence, eventType := range []execution.EventType{execution.EventRunStarted, execution.EventAgentMessage} {
		event, err := execution.NewEvent(question.StreamID, question.LastSequence+uint64(sequence)+1,
			at, eventType, "side-thread", nil)
		if err != nil {
			return sidestream.Spoken{}, err
		}
		if err := question.Events(event); err != nil {
			return sidestream.Spoken{}, err
		}
	}
	return sidestream.Spoken{
		Answer:         v.reply,
		SessionID:      "session-side-1",
		CostUSD:        0.25,
		Backend:        domain.BackendClaudeCode,
		Model:          "opus",
		ResolvedModel:  "claude-opus-5",
		AccountAlias:   "research",
		ConfigRevision: "cfg-0123456789ab",
		Build:          "9870df6a1b2c3d4e5f60718293a4b5c6d7e8f900",
	}, nil
}
