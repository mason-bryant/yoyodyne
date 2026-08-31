package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/exchange"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// What two roles said to each other is readable from outside either of their
// conversations, with what it cost beside the rounds it took. That is the whole
// of "durable and visible": an operator who was not there reads the thread.
func TestExchangesAreReadableFromTheCommandLine(t *testing.T) {
	// Not parallel: the state root the command addresses is set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	stdout, _, code := runCLI(t, "exchange", "list", "--config", configPath)
	if code != 0 || !strings.Contains(stdout, "no role has asked another one anything") {
		t.Fatalf("empty listing = %q (code %d)", stdout, code)
	}

	recorded := seedExchange(t, stateRoot)

	stdout, stderr, code := runCLI(t, "exchange", "list", "--config", configPath)
	if code != 0 {
		t.Fatalf("list code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{recorded.ID, "product manager asked architect", "2/10 round(s)", "$0.7500", "unresolved-after-rounds"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("list stdout = %q, want it to mention %q", stdout, want)
		}
	}

	// A prefix names one exchange, as it names one directive.
	stdout, stderr, code = runCLI(t, "exchange", "show", "--config", configPath, recorded.ID[:len("exchange-")+6])
	if code != 0 {
		t.Fatalf("show code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{
		"what does this goal cost, and what am I missing?",
		"More than the ordering assumes.",
		"round 2 of 10",
		"escalated to you",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("show stdout = %q, want it to mention %q", stdout, want)
		}
	}

	stdout, _, code = runCLI(t, "exchange", "show", "--config", configPath, "--json", recorded.ID)
	if code != 0 {
		t.Fatalf("show --json code = %d", code)
	}
	var decoded exchange.Exchange
	if err := json.Unmarshal([]byte(stdout), &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v over %q", err, stdout)
	}
	if decoded.MaxRounds != 10 || len(decoded.Rounds) != 2 || decoded.Outcome != exchange.OutcomeUnresolved {
		t.Fatalf("decoded = %+v", decoded)
	}

	if _, _, code = runCLI(t, "exchange", "show", "--config", configPath, "exchange-"+strings.Repeat("f", 32)); code == 0 {
		t.Fatal("showing an exchange nobody recorded succeeded")
	}
}

// seedExchange writes one exhausted exchange straight into the durable store,
// which is what a conductor that reached its cap would have left behind.
func seedExchange(t *testing.T, stateRoot string) exchange.Exchange {
	t.Helper()

	store, err := runstate.NewExchangeStore(stateRoot, domain.ProductID("yoyodyne"))
	if err != nil {
		t.Fatalf("NewExchangeStore() error = %v", err)
	}
	at := time.Date(2026, 8, 19, 9, 0, 0, 0, time.UTC)
	answered := at.Add(time.Second)
	closed := at.Add(time.Minute)
	recorded := exchange.Exchange{
		SchemaVersion: exchange.SchemaVersion,
		ID:            "exchange-" + strings.Repeat("a", 32),
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		Asker:         exchange.Party{Role: domain.RoleProductManager, Agent: "product-manager", Conversation: "chat-" + strings.Repeat("b", 32)},
		Answerer:      exchange.Party{Role: domain.RoleArchitect, Agent: "architect"},
		Question:      "what does this goal cost, and what am I missing?",
		MaxRounds:     10,
		Rounds: []exchange.Round{
			{Number: 1, Question: "what does this goal cost, and what am I missing?", Answer: "More than the ordering assumes.", CostUSD: 0.25, AskedAt: at, AnsweredAt: &answered},
			{Number: 2, Question: "how much more?", Answer: "It depends what you mean by cost.", CostUSD: 0.5, AskedAt: at, AnsweredAt: &answered},
		},
		Outcome:   exchange.OutcomeUnresolved,
		OpenedAt:  at,
		UpdatedAt: closed,
		ClosedAt:  &closed,
	}
	if err := store.Save(recorded); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if _, err := os.Stat(store.Root()); err != nil {
		t.Fatalf("the exchange directory was not created: %v", err)
	}
	return recorded
}

// The input half of judgment-only: what the harness actually dispatches when one
// role asks another. The output half is enforced by ReadAnswer refusing any
// harness block; this is the other side of it, and it is the side a widened
// tool list or a swapped system prompt would quietly break — the answer would
// still be prose, and the role would have had a filesystem while writing it.
func TestTheAnsweringRoundIsDispatchedWithNoToolsAndNoAuthority(t *testing.T) {
	t.Parallel()

	provider := &capturingBackend{result: backend.RunResult{
		SessionID: "session-2",
		FinalText: "More than the ordering assumes.",
		CostUSD:   0.25,
	}}
	voice := exchangeVoice{
		config:     answeringConfig(),
		provider:   provider,
		repository: t.TempDir(),
		productID:  "yoyodyne",
	}
	question := exchange.Question{
		ExchangeID: "exchange-" + strings.Repeat("a", 32),
		Role:       domain.RoleArchitect,
		Asker:      domain.RoleProductManager,
		Round:      1,
		MaxRounds:  10,
		Question:   "what does this goal cost, and what am I missing?",
		Context:    "I am about to order the backlog with it.",
	}

	spoken, err := voice.Answer(context.Background(), question)
	if err != nil {
		t.Fatalf("Answer() error = %v", err)
	}
	if spoken.Answer != "More than the ordering assumes." || spoken.Agent != "architect" || spoken.SessionID != "session-2" || spoken.CostUSD != 0.25 {
		t.Fatalf("spoken = %+v", spoken)
	}

	request := provider.request
	// No tools at all. An answering round has less than a conversation does, not
	// more; the session mode behind that is the backend's, read off the role.
	if request.AllowedTools == nil || len(request.AllowedTools) != 0 {
		t.Fatalf("allowed tools = %#v, want an empty non-nil list", request.AllowedTools)
	}
	// The answering prompt rather than the role's ordinary conversation contract:
	// there is no operator here, no block to act through, and no authority.
	if request.SystemPrompt != chat.AnsweringPrompt(domain.RoleArchitect, "house architect persona") {
		t.Fatalf("system prompt = %q", request.SystemPrompt)
	}
	if strings.Contains(request.SystemPrompt, "yoyodyne-tracker") {
		t.Fatal("the answering prompt describes a block the role may not use")
	}
	// The exchange is the record this invocation belongs to; it has no run and no
	// conversation of its own.
	if request.RunID != question.ExchangeID || request.Role != domain.RoleArchitect {
		t.Fatalf("request identity = %q/%q", request.RunID, request.Role)
	}
	// It answers under the agent configured for the role, with that agent's model.
	if request.Model != "opus-architect" {
		t.Fatalf("model = %q, want the configured architect's", request.Model)
	}
	if request.Timeout != exchangeAnswerTimeout {
		t.Fatalf("timeout = %s, want the answering bound", request.Timeout)
	}
	for _, wanted := range []string{
		"The product manager is asking you something",
		"round 1 of the 10",
		"what does this goal cost, and what am I missing?",
		"which is what they think rather than evidence",
	} {
		if !strings.Contains(request.Prompt, wanted) {
			t.Fatalf("prompt is missing %q: %q", wanted, request.Prompt)
		}
	}
}

// The voice reports what served the round back to the conductor, which pins it
// on the exchange record. An answering round is the one provider invocation with
// no run and no conversation behind it, so the exchange record is the only place
// the durable-state-is-provider-independent invariant's four things can be
// recorded — and they are reported whether or not the provider answered, because
// a round it failed was still answered on somebody's account and still charged
// for.
func TestTheAnsweringVoiceReportsWhatServedTheRound(t *testing.T) {
	t.Parallel()

	// The same project with a second account, and the architect answering on its
	// own rather than on whichever the machine is signed in to.
	pooled := answeringConfig()
	pooled.Accounts = map[string]config.Account{"personal": {}, "research": {}}
	architect := pooled.Agents["architect"]
	architect.Account = "research"
	pooled.Agents["architect"] = architect

	for _, test := range []struct {
		name      string
		config    config.Config
		provider  *capturingBackend
		wantAlias string
	}{
		{
			name:   "answered",
			config: answeringConfig(),
			provider: &capturingBackend{result: backend.RunResult{
				SessionID:     "session-2",
				ResolvedModel: "claude-opus-5-20260514",
				FinalText:     "More than the ordering assumes.",
			}},
			wantAlias: config.DefaultAccountAlias,
		},
		{
			name:      "unanswered",
			config:    answeringConfig(),
			provider:  &capturingBackend{err: errors.New("the provider went away")},
			wantAlias: config.DefaultAccountAlias,
		},
		{
			// Under a pool there is no configuration-wide account, so the alias is
			// the answering agent's own. This is the case the pinning exists for: two
			// accounts serving one product, and a record that named neither.
			name:      "pooled",
			config:    pooled,
			provider:  &capturingBackend{result: backend.RunResult{SessionID: "session-2", FinalText: "Twice."}},
			wantAlias: "research",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			voice := exchangeVoice{
				config:     test.config,
				provider:   test.provider,
				repository: t.TempDir(),
				stateRoot:  t.TempDir(),
				productID:  "yoyodyne",
			}
			spoken, _ := voice.Answer(context.Background(), exchange.Question{
				ExchangeID: "exchange-" + strings.Repeat("a", 32),
				Role:       domain.RoleArchitect,
				Asker:      domain.RoleProductManager,
				Round:      1,
				MaxRounds:  10,
				Question:   "what does this goal cost, and what am I missing?",
			})
			if spoken.Backend != domain.BackendClaudeCode || spoken.Model != "opus-architect" {
				t.Errorf("spoken = %+v, want the backend and selector the answering agent is configured for", spoken)
			}
			// The alias is the one the round was actually answered under, which is
			// the answering agent's own account rather than a configuration-wide one
			// there may be none of, and the same alias the round's cost line carries.
			if spoken.AccountAlias != test.wantAlias || spoken.ConfigRevision != test.config.Revision() {
				t.Errorf("spoken = %+v, want account %q and the configuration that served the round",
					spoken, test.wantAlias)
			}
		})
	}
}

// There is nobody to ask where the project configured nobody, and nothing here
// answers on a backend a conversation cannot be held over. Both are refused
// rather than producing an empty answer the asker would reason from.
func TestAnAnsweringRoundIsRefusedWhereThereIsNobodyToAsk(t *testing.T) {
	t.Parallel()

	provider := &capturingBackend{}
	unconfigured := exchangeVoice{config: answeringConfig(), provider: provider, productID: "yoyodyne"}
	if _, err := unconfigured.Answer(context.Background(), exchange.Question{
		ExchangeID: "exchange-" + strings.Repeat("a", 32),
		Role:       domain.RoleDevelopmentManager,
		Asker:      domain.RoleProductManager,
		Question:   "what does this cost?",
	}); err == nil {
		t.Fatal("a role nobody is configured for answered")
	}

	elsewhere := answeringConfig()
	elsewhere.Agents["architect"] = config.AgentConfig{
		Role: domain.RoleArchitect, Backend: domain.Backend("codex"), Model: "o1",
	}
	other := exchangeVoice{config: elsewhere, provider: provider, productID: "yoyodyne"}
	if _, err := other.Answer(context.Background(), exchange.Question{
		ExchangeID: "exchange-" + strings.Repeat("a", 32),
		Role:       domain.RoleArchitect,
		Asker:      domain.RoleProductManager,
		Question:   "what does this cost?",
	}); err == nil {
		t.Fatal("an agent on another backend answered")
	}
	if provider.calls != 0 {
		t.Fatalf("a refused round still asked a provider %d time(s)", provider.calls)
	}
}

// answeringConfig is a project with one architect to ask, which is the whole of
// what the voice reads from the configuration.
func answeringConfig() config.Config {
	return config.Config{
		Product: config.Product{ID: "yoyodyne", RepositoryID: "yoyodyne"},
		Agents: map[string]config.AgentConfig{
			"architect": {
				Role:    domain.RoleArchitect,
				Backend: domain.BackendClaudeCode,
				Model:   "opus-architect",
				Persona: config.Persona{Text: "house architect persona"},
			},
			"product-manager": {
				Role:    domain.RoleProductManager,
				Backend: domain.BackendClaudeCode,
				Model:   "opus",
			},
		},
	}
}

// capturingBackend records exactly what the harness asked a provider to run,
// which is what makes the answering round's boundary an assertion rather than a
// claim.
type capturingBackend struct {
	request backend.RunRequest
	result  backend.RunResult
	err     error
	calls   int
}

func (c *capturingBackend) Run(_ context.Context, request backend.RunRequest) (backend.RunResult, error) {
	c.request = request
	c.calls++
	return c.result, c.err
}
