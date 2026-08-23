package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

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
