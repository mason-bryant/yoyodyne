package cli

import (
	"context"
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
