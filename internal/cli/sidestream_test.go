package cli

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sidestream"
)

// The input half of judgment-and-reads-only: what the harness actually
// dispatches when a role is asked something beside its main thread. The output
// half is ReadReply refusing any harness block; this is the side a widened tool
// list or a swapped system prompt would quietly break — the answer would still
// be prose, and the role would have had a filesystem while writing it.
func TestASideTurnIsDispatchedWithNoToolsAndTheSideContract(t *testing.T) {
	t.Parallel()

	provider := &capturingBackend{result: backend.RunResult{
		SessionID:     "session-side-2",
		FinalText:     "The hold covers work the harness chose and not work the operator named.",
		CostUSD:       0.25,
		ResolvedModel: "claude-opus-5",
		LastEvent:     4,
	}}
	costs := &recordedSpend{}
	voice := sideVoice{
		config:     answeringConfig(),
		provider:   provider,
		repository: t.TempDir(),
		productID:  "yoyodyne",
		spend:      costs,
	}
	question := testSideQuestion()

	spoken, err := voice.Answer(context.Background(), question)
	if err != nil {
		t.Fatalf("Answer() error = %v", err)
	}
	if spoken.Answer != provider.result.FinalText || spoken.SessionID != "session-side-2" ||
		spoken.CostUSD != 0.25 || spoken.LastEvent != 4 {
		t.Fatalf("spoken = %+v", spoken)
	}

	request := provider.request
	// No tools at all. A side turn has less than a conversation does, not more.
	if request.AllowedTools == nil || len(request.AllowedTools) != 0 {
		t.Fatalf("allowed tools = %#v, want an empty non-nil list", request.AllowedTools)
	}
	// The side thread's prompt rather than the role's ordinary conversation
	// contract: there is no operator here, no block to act through, and no
	// authority the main thread has.
	if request.SystemPrompt != chat.SidePrompt(domain.RoleArchitect, "house architect persona") {
		t.Fatalf("system prompt = %q, want the side thread's", request.SystemPrompt)
	}
	if !strings.Contains(request.SystemPrompt, sidestream.SideThreadContract) {
		t.Fatal("a side turn was dispatched without the contract that says it takes no action")
	}
	// The side stream is the record this invocation belongs to, so its events are
	// named for it and reach the sink the runner handed in — which is the side
	// stream's own log and never the main thread's.
	if request.RunID != question.StreamID || request.Role != domain.RoleArchitect {
		t.Fatalf("request identity = %q/%q", request.RunID, request.Role)
	}
	if request.LastSequence != question.LastSequence || request.EventSink == nil {
		t.Fatalf("request carries sequence %d and sink=%v, want the stream's own log continued",
			request.LastSequence, request.EventSink != nil)
	}
	if request.SessionID != question.SessionID {
		t.Fatalf("session = %q, want the one this thread has been held in", request.SessionID)
	}
	// It answers under the agent configured for the role, with that agent's model
	// and the side turn's bound.
	if request.Model != "opus-architect" || request.Timeout != sideTurnTimeout {
		t.Fatalf("model = %q, timeout = %s", request.Model, request.Timeout)
	}
	for _, wanted := range []string{
		"A question on your side thread",
		"turn 2 of the 8",
		question.StreamID,
		question.Conversation,
		"whether the intake hold covers work an operator named",
		"does the intake hold cover work the operator named?",
	} {
		if !strings.Contains(request.Prompt, wanted) {
			t.Fatalf("prompt is missing %q: %q", wanted, request.Prompt)
		}
	}

	// And what it spent is one line in the cost log, charged to the side stream
	// rather than to the main thread it was held beside: the two are separately
	// answerable for what they cost, and a line naming the conversation would put
	// a side turn's cost on a thread that never took it.
	if len(costs.lines) != 1 {
		t.Fatalf("the turn appended %d cost line(s), want one", len(costs.lines))
	}
	line := costs.lines[0]
	if line.SideStreamID != question.StreamID || line.ConversationID != "" || line.RunID != "" {
		t.Fatalf("the cost line names %#v, want the side stream alone", line)
	}
	if err := line.Validate(); err != nil {
		t.Fatalf("the cost line a side turn writes is refused by the store: %v", err)
	}
}

// recordedSpend is the cost log in memory.
type recordedSpend struct{ lines []runstate.Spend }

func (r *recordedSpend) Append(line runstate.Spend) error {
	r.lines = append(r.lines, line)
	return nil
}

// The voice reports what served the turn back to the runner, which pins it on
// the stream. A side turn has no run and no conversation of its own, so the
// stream is the only place the four things
// `durable-state-is-provider-independent` asks for can be recorded — and they
// are reported whether or not the provider answered, because a turn it failed
// was still answered on somebody's account and still charged for.
func TestTheSideVoiceReportsWhatServedTheTurnEvenWhenItFailed(t *testing.T) {
	t.Parallel()

	// The same project with a second account, and the architect holding its side
	// thread on its own rather than on whichever the machine is signed in to.
	pooled := answeringConfig()
	pooled.Accounts = map[string]config.Account{"personal": {}, "research": {}}
	architect := pooled.Agents["architect"]
	architect.Account = "research"
	pooled.Agents["architect"] = architect

	for _, test := range []struct {
		name     string
		provider *capturingBackend
		wantErr  string
	}{
		{
			name:     "answered",
			provider: &capturingBackend{result: backend.RunResult{FinalText: "Yes.", CostUSD: 0.5, ResolvedModel: "claude-opus-5"}},
		},
		{
			name:     "reported failure",
			provider: &capturingBackend{result: backend.RunResult{IsError: true, CostUSD: 0.5, ResolvedModel: "claude-opus-5"}},
			wantErr:  "reported failure",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			voice := sideVoice{
				config:     pooled,
				provider:   test.provider,
				repository: t.TempDir(),
				productID:  "yoyodyne",
				stateRoot:  t.TempDir(),
			}
			spoken, err := voice.Answer(context.Background(), testSideQuestion())
			switch {
			case test.wantErr == "" && err != nil:
				t.Fatalf("Answer() error = %v", err)
			case test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)):
				t.Fatalf("Answer() error = %v, want %q", err, test.wantErr)
			}
			if spoken.Backend != domain.BackendClaudeCode || spoken.Model != "opus-architect" ||
				spoken.ResolvedModel != "claude-opus-5" || spoken.AccountAlias != "research" ||
				spoken.ConfigRevision != pooled.Revision() {
				t.Fatalf("spoken = %+v, want the invocation pinned to what served it", spoken)
			}
			if spoken.CostUSD != 0.5 {
				t.Fatalf("cost = %v, want what the provider charged whichever way the turn went", spoken.CostUSD)
			}
			if test.provider.request.AccountAlias != "research" {
				t.Fatalf("the turn was answered on %q, want the architect's own account", test.provider.request.AccountAlias)
			}
		})
	}
}

// A role nobody configured is refused rather than answered emptily, and so is
// one configured for a backend that cannot hold a side thread. Both say which
// side stream had nobody to ask, because that is the record whoever reads the
// failure has to go and look at.
func TestASideTurnWithNobodyToAskIsRefused(t *testing.T) {
	t.Parallel()

	bare := config.Config{Product: config.Product{ID: "yoyodyne"}}
	question := testSideQuestion()
	if _, err := (sideVoice{config: bare, productID: "yoyodyne"}).Answer(context.Background(), sidestream.Question{
		StreamID: question.StreamID, Role: domain.RoleArchitect, Conversation: question.Conversation,
	}); err == nil || !strings.Contains(err.Error(), "nobody to hold a side thread") {
		t.Fatalf("Answer() with no agent configured error = %v, want it refused", err)
	}

	elsewhere := answeringConfig()
	architect := elsewhere.Agents["architect"]
	architect.Backend = domain.BackendCodex
	elsewhere.Agents["architect"] = architect
	if _, err := (sideVoice{config: elsewhere, productID: "yoyodyne"}).Answer(context.Background(), question); err == nil ||
		!strings.Contains(err.Error(), "requires a claude-code agent") {
		t.Fatalf("Answer() on another backend error = %v, want it refused", err)
	}
}

// The per-agent knob decides whether there is a side thread at all, and it is
// read here because this is where a side turn becomes a provider invocation. An
// agent left at the default queues — which is what every agent did before the
// knob existed — so the turn is refused before anything is spent, naming the
// mode the project actually configured.
//
// What the knob never decides is what a side thread may do. That is the role's
// own authority narrowed in Go, and `internal/chat` holds the test that no value
// here reaches it.
func TestASideTurnIsRefusedForAnAgentConfiguredToQueue(t *testing.T) {
	t.Parallel()

	queueing := answeringConfig()
	architect := queueing.Agents["architect"]
	architect.Conversations = config.ConversationQueue
	queueing.Agents["architect"] = architect

	provider := &capturingBackend{result: backend.RunResult{FinalText: "answered anyway"}}
	voice := sideVoice{config: queueing, provider: provider, repository: t.TempDir(), productID: "yoyodyne"}
	_, err := voice.Answer(context.Background(), testSideQuestion())
	if err == nil || !strings.Contains(err.Error(), "holds no side threads") {
		t.Fatalf("Answer() for a queueing agent error = %v, want it refused", err)
	}
	if !strings.Contains(err.Error(), string(config.ConversationQueue)) {
		t.Errorf("the refusal = %v, want it to name the mode the project configured", err)
	}
	if provider.calls != 0 {
		t.Fatalf("a refused side turn still asked a provider %d time(s)", provider.calls)
	}

	// An agent that has written nothing at all is the same answer, because an
	// unstated key is the default rather than an absent one.
	architect.Conversations = ""
	queueing.Agents["architect"] = architect
	if _, err := (sideVoice{config: queueing, provider: provider, productID: "yoyodyne"}).Answer(context.Background(), testSideQuestion()); err == nil ||
		!strings.Contains(err.Error(), "holds no side threads") {
		t.Fatalf("Answer() for an agent that configured nothing error = %v, want it refused", err)
	}
}

// An exhausted usage limit met on a side turn is written down where every
// process outside a run writes one. A side turn has no run to park and the
// conversation it is held beside is not its own to fail at somebody's terminal,
// so without this the one refusal that means hours in which nothing happens
// would stop a side thread and leave no trace anywhere — and a side thread is
// exactly the question somebody asked because the main thread was already busy.
func TestASideTurnRefusedForCapacityIsWrittenDown(t *testing.T) {
	t.Parallel()

	limits, err := runstate.NewUsageLimitStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewUsageLimitStore() error = %v", err)
	}
	resets := time.Date(2026, 9, 7, 18, 0, 0, 0, time.UTC)
	voice := sideVoice{
		config:      answeringConfig(),
		provider:    &capturingBackend{result: backend.RunResult{IsError: true, UsageLimit: &backend.UsageLimit{Kind: "five-hour", ResetsAt: resets}}},
		repository:  t.TempDir(),
		productID:   "yoyodyne",
		usageLimits: limits,
		clock:       func() time.Time { return time.Date(2026, 9, 7, 15, 0, 0, 0, time.UTC) },
	}
	question := testSideQuestion()

	if _, err := voice.Answer(context.Background(), question); err == nil {
		t.Fatal("Answer() = nil, want the refused turn to fail")
	}
	recorded, err := limits.List()
	if err != nil || len(recorded) != 1 {
		t.Fatalf("List() = %#v, error %v, want the refusal recorded as the stoppage it was", recorded, err)
	}
	if recorded[0].Kind != "five-hour" || recorded[0].ResetsAt == nil || !recorded[0].ResetsAt.Equal(resets) {
		t.Fatalf("recorded = %#v, want the provider's own limit and reset time", recorded[0])
	}
	// It names the thread that stopped and the conversation that thread is beside,
	// because both are what somebody reading the refusal has to go and look at.
	if !strings.Contains(recorded[0].Waiting, question.StreamID) ||
		!strings.Contains(recorded[0].Waiting, question.Conversation) {
		t.Fatalf("recorded waiting = %q, want the side thread and the main thread it is beside", recorded[0].Waiting)
	}
}

func testSideQuestion() sidestream.Question {
	return sidestream.Question{
		StreamID:     "side-" + strings.Repeat("a", 32),
		Role:         domain.RoleArchitect,
		Agent:        "architect",
		Conversation: "chat-" + strings.Repeat("b", 32),
		Topic:        "whether the intake hold covers work an operator named",
		Turn:         2,
		MaxTurns:     8,
		Question:     "does the intake hold cover work the operator named?",
		SessionID:    "session-side-1",
		LastSequence: 2,
		Events:       func(execution.Event) error { return nil },
	}
}

// Which single messages may be answered on a side thread when the main thread
// turns out to be held: speech to an agent that holds side threads, and nothing
// else. The knob is the whole of the choice, and three shapes never go aside
// whatever it says, because each has to reach the main thread to mean anything.
func TestASingleMessageGoesAsideOnlyForSpeechToAnAgentThatHoldsSideThreads(t *testing.T) {
	t.Parallel()

	cfg := answeringConfig()
	architect := preparedChat{parts: components{config: cfg}, name: "architect"}
	productManager := preparedChat{parts: components{config: cfg}, name: "product-manager"}
	for name, test := range map[string]struct {
		prepared preparedChat
		request  conversationRequest
		want     bool
	}{
		"speech to an agent holding side threads": {architect, conversationRequest{message: "does the hold cover named work?"}, true},
		"speech to an agent that queues":          {productManager, conversationRequest{message: "does the hold cover named work?"}, false},
		"an interactive conversation":             {architect, conversationRequest{}, false},
		"a command":                               {architect, conversationRequest{message: "/status"}, false},
		"a decision":                              {architect, conversationRequest{message: "approve 3.1"}, false},
		"a bare answer":                           {architect, conversationRequest{message: "y"}, false},
		"an answer to a concern":                  {architect, conversationRequest{message: "answer c3.1 yes"}, false},
		"a message replacing the conversation":    {architect, conversationRequest{message: "start over", fresh: true}, false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := test.request.mayGoAside(test.prepared); got != test.want {
				t.Fatalf("mayGoAside() = %v, want %v", got, test.want)
			}
		})
	}
}

// --side-thread names an open thread and travels with a message alone.
func TestTheSideThreadFlagIsHeldToItsShape(t *testing.T) {
	t.Parallel()

	stream := "side-" + strings.Repeat("c", 32)
	for name, test := range map[string]struct {
		stream, message string
		fresh           bool
		wantErr         string
	}{
		"unset":                     {"", "", false, ""},
		"continuing with a message": {stream, "and the other half?", false, ""},
		"not a side thread":         {"chat-" + strings.Repeat("c", 32), "anything", false, "does not name a side thread"},
		"without a message":         {stream, "", false, "requires --message"},
		"with a new conversation":   {stream, "anything", true, "cannot be combined with --new"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			err := sideThreadFlagProblems("chat", test.stream, test.message, test.fresh)
			switch {
			case test.wantErr == "" && err != nil:
				t.Fatalf("sideThreadFlagProblems() error = %v", err)
			case test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)):
				t.Fatalf("sideThreadFlagProblems() error = %v, want %q", err, test.wantErr)
			}
		})
	}
}

// The operator surface, end to end over the real stores: a message put to the
// architect beside its held main thread is answered on a side thread by the
// configured agent under a toolless invocation, the answer says where it came
// from and that what it promised is tentative, and the concluded thread is in the
// architect's memory for its main conversation's next turn — while the main
// thread's lease stays with whoever had it.
func TestAMessageAnsweredOnASideThreadSaysSoAndMergesIntoMemory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	provider := &capturingBackend{result: backend.RunResult{
		SessionID: "session-side-9",
		FinalText: "The design forbids a side thread taking the main thread's lease.\n\n" +
			sidestream.Fence + "\n{\"side\":{\"concluded\":true,\"commitments\":[\"say so in the design's next revision\"]}}\n```\n",
		CostUSD:       0.25,
		ResolvedModel: "claude-opus-5",
	}}
	prepared := testPreparedChat(t, root, provider)
	conversation := "chat-" + strings.Repeat("d", 32)

	// The architect's main thread is held by another process, and stays held.
	main, err := prepared.store.Hold(prepared.identity)
	if err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	defer main.Release()

	var stdout, stderr strings.Builder
	code := askAside(context.Background(), prepared, conversationRequest{message: "may a side thread take the main thread's lease?"},
		conversation, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("askAside() = %d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}

	// The answer, and the surface saying what it is: judgment from a side thread,
	// with the promise marked tentative and the thread concluded into memory.
	out := stdout.String()
	for _, required := range []string{
		"The design forbids a side thread taking the main thread's lease.",
		"Answered on side thread side-",
		"beside the architect's conversation " + conversation,
		"not an action",
		"tentatively committed",
		"- say so in the design's next revision",
		"The thread concluded after 1 of 8 turn(s), and what it worked out is merged into the architect's memory",
	} {
		if !strings.Contains(out, required) {
			t.Errorf("the answer does not say %q:\n%s", required, out)
		}
	}
	if strings.Contains(out, "yoyodyne-side") {
		t.Errorf("the answer carried the thread's block to the operator:\n%s", out)
	}
	if !strings.Contains(stderr.String(), "answered beside that turn on a side thread") {
		t.Errorf("stderr does not say the message went aside:\n%s", stderr.String())
	}

	// The invocation was the side thread's: toolless, under the side contract,
	// named for the stream and not for the conversation.
	request := provider.request
	if request.AllowedTools == nil || len(request.AllowedTools) != 0 {
		t.Fatalf("allowed tools = %#v, want an empty non-nil list", request.AllowedTools)
	}
	if !strings.Contains(request.SystemPrompt, sidestream.SideThreadContract) || request.RunID == conversation || !sidestream.ValidID(request.RunID) {
		t.Fatalf("the turn was dispatched as %q under %q, want a side stream's own toolless invocation", request.RunID, request.SystemPrompt[:40])
	}
	if !strings.Contains(request.Prompt, "may a side thread take the main thread's lease?") || !strings.Contains(request.Prompt, "beside your main conversation "+conversation) {
		t.Fatalf("the side thread was asked:\n%s", request.Prompt)
	}

	// It is in the architect's memory, concluded, citing the main thread it was
	// held beside — which is where that conversation's next turn reads it.
	live, problems, err := prepared.memories.Live("architect")
	if err != nil || len(problems) != 0 || len(live) != 1 {
		t.Fatalf("Live() = %d memories, %v, %v; want the one merge", len(live), problems, err)
	}
	merged := live[0].Current()
	if merged.Memory != request.RunID || merged.Invocation.Kind != runstate.MemoryInvocationSideStream {
		t.Fatalf("the merge is %q by %s, want the side stream's own memory written by its turn", merged.Memory, merged.Invocation.Kind)
	}
	cited := false
	for _, source := range merged.Sources {
		cited = cited || (source.Kind == runstate.MemorySourceConversation && source.ID == conversation)
	}
	if !cited {
		t.Fatalf("the merge cites %v, want the main thread it was held beside", merged.Sources)
	}
	streams, err := runstate.NewSideStreamStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewSideStreamStore() error = %v", err)
	}
	recorded, err := streams.Load(request.RunID)
	if err != nil || recorded.Open() || recorded.Outcome != sidestream.OutcomeConcluded {
		t.Fatalf("the side stream is %+v, %v; want it recorded concluded", recorded, err)
	}

	// And the main thread's lease was never touched: this test still holds it.
	if _, err := prepared.store.Hold(prepared.identity); !errors.Is(err, runstate.ErrConversationHeld) {
		t.Fatalf("the main thread after a side turn = %v, want it still held", err)
	}

	// The operator's pause covers a side turn exactly as it covers a main one, and
	// no turn is taken through it.
	if _, err := prepared.parts.holds.Hold(time.Now()); err != nil {
		t.Fatalf("Hold() the operator's pause error = %v", err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := askAside(context.Background(), prepared, conversationRequest{message: "and while paused?"}, conversation, &stdout, &stderr); code == 0 {
		t.Fatal("askAside() while paused = 0, want the turn refused")
	}
	if !strings.Contains(stderr.String(), "paused") || provider.calls != 1 {
		t.Fatalf("a paused side turn reached the provider %d time(s) and said:\n%s", provider.calls-1, stderr.String())
	}
}

// A thread the role kept open is reported as open, with the command that
// continues it, and continuing it names the stream alone.
func TestAnOpenSideThreadIsContinuedByItsIdentifier(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	provider := &capturingBackend{result: backend.RunResult{SessionID: "session-side-10", FinalText: "I need one more turn to say.", CostUSD: 0.1, ResolvedModel: "claude-opus-5"}}
	prepared := testPreparedChat(t, root, provider)
	conversation := "chat-" + strings.Repeat("e", 32)

	var stdout, stderr strings.Builder
	if code := askAside(context.Background(), prepared, conversationRequest{message: "which lease?", jsonOutput: true}, conversation, &stdout, &stderr); code != 0 {
		t.Fatalf("askAside() = %d\n%s", code, stderr.String())
	}
	var first chatOutput
	if err := json.Unmarshal([]byte(stdout.String()), &first); err != nil {
		t.Fatalf("Unmarshal() error = %v:\n%s", err, stdout.String())
	}
	if first.SideThread == nil || first.SideThread.Outcome != "" || first.SideThread.Turn != 1 || first.SideThread.Tentative || first.Reply != "I need one more turn to say." {
		t.Fatalf("the first answer reports %+v with reply %q, want an open thread after one turn", first.SideThread, first.Reply)
	}

	// Continued by name, on its own session, and concluded this time.
	provider.result = backend.RunResult{SessionID: "session-side-10", FinalText: "The stream's own.\n\n" + sidestream.Fence + "\n{\"side\":{\"concluded\":true}}\n```\n", CostUSD: 0.1, ResolvedModel: "claude-opus-5"}
	stdout.Reset()
	if code := askAside(context.Background(), prepared, conversationRequest{message: "well?", sideThread: first.SideThread.Stream}, "", &stdout, &stderr); code != 0 {
		t.Fatalf("askAside() continuing = %d\n%s", code, stderr.String())
	}
	if provider.request.SessionID != "session-side-10" || provider.request.RunID != first.SideThread.Stream {
		t.Fatalf("the continuation was dispatched as %q in session %q, want the same stream's own session", provider.request.RunID, provider.request.SessionID)
	}
	if !strings.Contains(stdout.String(), "The thread concluded after 2 of 8 turn(s)") {
		t.Fatalf("the continuation does not report the thread concluded:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "side thread cost so far: $0.2000") {
		t.Fatalf("the continuation does not report what the thread cost:\n%s", stdout.String())
	}
}

// A side thread is continued only by the agent that holds it. Another agent's
// command surface naming the stream is refused before a turn is spent, so the
// thread is never served on the wrong account or merged into the wrong memory —
// and the refusal names whose thread it is.
func TestAnotherAgentsSideThreadCannotBeContinuedFromThisAgentsCommand(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	provider := &capturingBackend{result: backend.RunResult{SessionID: "session-side-11", FinalText: "One more turn.", CostUSD: 0.1, ResolvedModel: "claude-opus-5"}}
	architect := testPreparedChat(t, root, provider)
	var stdout, stderr strings.Builder
	if code := askAside(context.Background(), architect, conversationRequest{message: "which lease?", jsonOutput: true},
		"chat-"+strings.Repeat("f", 32), &stdout, &stderr); code != 0 {
		t.Fatalf("askAside() = %d\n%s", code, stderr.String())
	}
	var opened chatOutput
	if err := json.Unmarshal([]byte(stdout.String()), &opened); err != nil || opened.SideThread == nil {
		t.Fatalf("Unmarshal() error = %v:\n%s", err, stdout.String())
	}

	// The product manager, over the same stores, names the architect's thread.
	productManager := architect
	productManager.name = "product-manager"
	productManager.agent = productManager.parts.config.Agents["product-manager"]
	productManager.identity = runstate.ConversationIdentity{Agent: "product-manager", Role: domain.RoleProductManager}
	stdout.Reset()
	stderr.Reset()
	code := askAside(context.Background(), productManager, conversationRequest{message: "and?", sideThread: opened.SideThread.Stream}, "", &stdout, &stderr)
	if code == 0 {
		t.Fatalf("askAside() continuing another agent's thread = 0, want it refused\n%s", stdout.String())
	}
	if !strings.Contains(stderr.String(), "is the architect agent architect's thread and not product-manager's") {
		t.Fatalf("the refusal does not say whose thread it is:\n%s", stderr.String())
	}
	if provider.calls != 1 {
		t.Fatalf("the provider was reached %d time(s), want the one turn that opened the thread", provider.calls)
	}
	streams, err := runstate.NewSideStreamStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewSideStreamStore() error = %v", err)
	}
	recorded, err := streams.Load(opened.SideThread.Stream)
	if err != nil || recorded.Turns != 1 || recorded.Agent != "architect" {
		t.Fatalf("the architect's thread is %+v, %v; want it untouched by the refused continuation", recorded, err)
	}
	if live, _, err := productManager.memories.Live("product-manager"); err != nil || len(live) != 0 {
		t.Fatalf("the product manager's memory holds %d memories after a refused continuation, want none", len(live))
	}
}

// A turn that failed after the role had written prose still hands the prose to
// a --json caller, with the thread's state beside the error: the turn was paid
// for, and a document that carried only the failure would lose it.
func TestAFailedSideTurnKeepsItsProseInTheJSONDocument(t *testing.T) {
	t.Parallel()

	provider := &capturingBackend{result: backend.RunResult{
		SessionID:     "session-side-12",
		FinalText:     "Here is what I would do.\n\n```yoyodyne-tracker\n{}\n```\n",
		CostUSD:       0.1,
		ResolvedModel: "claude-opus-5",
	}}
	prepared := testPreparedChat(t, t.TempDir(), provider)
	var stdout, stderr strings.Builder
	if code := askAside(context.Background(), prepared, conversationRequest{message: "what would you do?", jsonOutput: true},
		"chat-"+strings.Repeat("a", 32), &stdout, &stderr); code != 1 {
		t.Fatalf("askAside() = %d, want the refused block to fail the turn\n%s", code, stderr.String())
	}
	var failed chatOutput
	if err := json.Unmarshal([]byte(stdout.String()), &failed); err != nil {
		t.Fatalf("Unmarshal() error = %v:\n%s", err, stdout.String())
	}
	if failed.Reply != "Here is what I would do." || !strings.Contains(failed.Error, "takes no action") {
		t.Fatalf("the failure document is %+v, want the prose beside the refusal", failed)
	}
	if failed.SideThread == nil || failed.SideThread.Turn != 1 || failed.SideThread.Outcome != "" {
		t.Fatalf("the failure document reports %+v, want the thread still open after its spent turn", failed.SideThread)
	}
}

// The routing itself, over real stores: a message to an agent holding side
// threads finds the main conversation held and goes aside; one that finds it
// held with no record to be beside yet waits for it instead, and says so.
func TestASingleMessageFindingTheMainThreadHeldGoesAside(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	provider := &capturingBackend{result: backend.RunResult{
		SessionID:     "session-side-13",
		FinalText:     "Nothing takes the main thread's lease but the main thread.\n\n" + sidestream.Fence + "\n{\"side\":{\"concluded\":true}}\n```\n",
		CostUSD:       0.25,
		ResolvedModel: "claude-opus-5",
	}}
	prepared := testPreparedChat(t, root, provider)
	conversation, err := runstate.NewConversationID()
	if err != nil {
		t.Fatalf("NewConversationID() error = %v", err)
	}
	if err := prepared.store.Save(runstate.Conversation{
		SchemaVersion:  runstate.ConversationSchemaVersion,
		ConversationID: conversation,
		ProductID:      "yoyodyne",
		RepositoryID:   "yoyodyne",
		Agent:          "architect",
		Role:           domain.RoleArchitect,
		Backend:        domain.BackendClaudeCode,
		StartedAt:      time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC),
		UpdatedAt:      time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	// Another process is mid-turn on the architect's conversation.
	main, err := prepared.store.Hold(prepared.identity)
	if err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	defer main.Release()

	var stdout, stderr strings.Builder
	code := converseWith(context.Background(), prepared, conversationRequest{message: "may a side thread take the lease?", jsonOutput: true},
		strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("converseWith() = %d\n%s", code, stderr.String())
	}
	var answered chatOutput
	if err := json.Unmarshal([]byte(stdout.String()), &answered); err != nil {
		t.Fatalf("Unmarshal() error = %v:\n%s", err, stdout.String())
	}
	if answered.SideThread == nil || answered.SideThread.Conversation != conversation || answered.SideThread.Outcome != sidestream.OutcomeConcluded {
		t.Fatalf("the message was answered as %+v, want a side thread beside the held conversation", answered.SideThread)
	}
	if provider.request.RunID != answered.SideThread.Stream {
		t.Fatalf("the turn was taken as %q, want the side stream's own invocation", provider.request.RunID)
	}
	// The main thread was never taken from whoever holds it.
	if _, err := prepared.store.Hold(prepared.identity); !errors.Is(err, runstate.ErrConversationHeld) {
		t.Fatalf("the main thread after routing = %v, want it still held", err)
	}

	// The same agent, held before its first turn has recorded anything: there is
	// no conversation to be beside, so the message waits for the main thread as it
	// always did and says why. The wait is ended here by the context, the way
	// Ctrl-C ends it at a terminal.
	unrecorded := testPreparedChat(t, t.TempDir(), provider)
	held, err := unrecorded.store.Hold(unrecorded.identity)
	if err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	defer held.Release()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stdout.Reset()
	stderr.Reset()
	if code := converseWith(ctx, unrecorded, conversationRequest{message: "anything?"}, strings.NewReader(""), &stdout, &stderr); code == 0 {
		t.Fatal("converseWith() with no record and a cancelled wait = 0, want the wait ended")
	}
	if !strings.Contains(stderr.String(), "has no record yet to hold a side thread beside, so this message waits for it") {
		t.Fatalf("stderr does not say the message waited:\n%s", stderr.String())
	}
	if provider.calls != 1 {
		t.Fatalf("the provider was reached %d time(s), want only the routed turn above", provider.calls)
	}
}

// testPreparedChat is a conversation with the architect resolved as far as the
// routing needs, over real stores under one root.
func testPreparedChat(t *testing.T, root string, provider chat.Backend) preparedChat {
	t.Helper()

	cfg := answeringConfig()
	holds, err := runstate.NewOperatorHoldStore(root)
	if err != nil {
		t.Fatalf("NewOperatorHoldStore() error = %v", err)
	}
	spendLog, err := runstate.NewSpendStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewSpendStore() error = %v", err)
	}
	usage, err := runstate.NewUsageLimitStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewUsageLimitStore() error = %v", err)
	}
	conversations, err := runstate.NewConversationStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewConversationStore() error = %v", err)
	}
	memories, err := runstate.NewMemoryStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewMemoryStore() error = %v", err)
	}
	return preparedChat{
		parts: components{
			config:      cfg,
			repository:  t.TempDir(),
			stateRoot:   root,
			holds:       holds,
			spend:       spendLog,
			usageLimits: usage,
		},
		name:     "architect",
		agent:    cfg.Agents["architect"],
		provider: provider,
		store:    conversations,
		memories: memories,
		identity: runstate.ConversationIdentity{Agent: "architect", Role: domain.RoleArchitect},
	}
}
