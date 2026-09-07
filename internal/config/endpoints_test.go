package config

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// pooledConfig is the smallest pooled configuration: two accounts whose stable
// order is one then two, and the agents that would be served from them.
func pooledConfig(t *testing.T, extra string) Config {
	t.Helper()
	return mustDecodeConfig(t, `version: 1
product:
  id: yoyodyne
  repository: .
approvals:
  brief: human
  goals: human
  designs: automatic
  integration: human
accounts:
  one: {}
  two: {}
agents:
  developer:
    role: developer
    backend: claude-code
    model: opus
  reviewer:
    role: reviewer
    backend: claude-code
    model: sonnet
`+extra)
}

func builtInRegistry(t *testing.T) *backend.Registry {
	t.Helper()
	registry, err := backend.NewRegistry(nil)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	return registry
}

// The pool answers with an endpoint rather than with an account, so what a
// caller is handed says the provider, the adapter that reaches it, the account,
// and the model together — and the account endpoint beside it still says where
// on this machine that alias authenticates.
func TestThePoolAnswersWithAnEndpointAndTheAccountItAuthenticatesUnder(t *testing.T) {
	t.Parallel()

	cfg := pooledConfig(t, "")
	choice, err := cfg.ChooseEndpoint(builtInRegistry(t), "/state", "developer", backend.Endpoint{}, nil)
	if err != nil {
		t.Fatalf("ChooseEndpoint() error = %v", err)
	}
	want := backend.Endpoint{
		Provider:       domain.BackendClaudeCode,
		AdapterVersion: backend.ClaudeCodeAdapterVersion,
		AccountAlias:   "one",
		Model:          "opus",
	}
	if !choice.Endpoint.Same(want) {
		t.Fatalf("ChooseEndpoint() = %s, want %s", choice.Endpoint, want)
	}
	if dir := filepath.Join("/state", "accounts", "one"); choice.Account.Directory != dir {
		t.Fatalf("the chosen account authenticates in %q, want %q", choice.Account.Directory, dir)
	}
}

// The cursor is an endpoint and not an account, which is what keying the pool on
// endpoints buys: a turn served on some other endpoint family does not move this
// one's rotation, so two agents on different models do not read each other's
// position as their own.
func TestTheCursorIsAnEndpointRatherThanAnAccount(t *testing.T) {
	t.Parallel()

	cfg := pooledConfig(t, "")
	registry := builtInRegistry(t)
	served, err := cfg.ChooseEndpoint(registry, "/state", "developer", backend.Endpoint{}, nil)
	if err != nil {
		t.Fatalf("ChooseEndpoint() error = %v", err)
	}

	next, err := cfg.ChooseEndpoint(registry, "/state", "developer", served.Endpoint, nil)
	if err != nil {
		t.Fatalf("ChooseEndpoint() error = %v", err)
	}
	if next.Endpoint.AccountAlias != "two" {
		t.Fatalf("after a turn on %q the pool chose %q, want the next account", served.Endpoint, next.Endpoint)
	}

	// The reviewer asks a different model, so the developer's cursor is not its
	// cursor: its own rotation starts at the top rather than where somebody else's
	// turn left off.
	elsewhere, err := cfg.ChooseEndpoint(registry, "/state", "reviewer", served.Endpoint, nil)
	if err != nil {
		t.Fatalf("ChooseEndpoint() error = %v", err)
	}
	if elsewhere.Endpoint.AccountAlias != "one" {
		t.Fatalf("the reviewer's rotation started at %q, want the top of its own pool", elsewhere.Endpoint)
	}
}

// A budget is the operator's limit on an account, so it stays keyed on the
// account whichever endpoint is asking — and a pool with nothing left to spend
// refuses in the same words it always did.
func TestTheEndpointPoolHonoursTheAccountBudgets(t *testing.T) {
	t.Parallel()

	cfg := pooledConfig(t, "")
	spent := map[string]float64{"one": 12}
	budget := 10.0
	cfg.Accounts["one"] = Account{WeeklyBudgetUSD: &budget}

	registry := builtInRegistry(t)
	choice, err := cfg.ChooseEndpoint(registry, "/state", "developer", backend.Endpoint{}, spent)
	if err != nil {
		t.Fatalf("ChooseEndpoint() error = %v", err)
	}
	if choice.Endpoint.AccountAlias != "two" {
		t.Fatalf("the pool chose %s, want the account that has not spent its budget", choice.Endpoint)
	}

	cfg.Accounts["two"] = Account{WeeklyBudgetUSD: &budget}
	spent["two"] = 12
	_, err = cfg.ChooseEndpoint(registry, "/state", "developer", backend.Endpoint{}, spent)
	if err == nil || !strings.Contains(err.Error(), "spent its weekly budget") {
		t.Fatalf("ChooseEndpoint() = %v, want the refusal naming the budgets", err)
	}
}

// An endpoint the asking role may not be served on is refused before any
// rotation, with the reason named. The answer does not vary by account, so a
// pool that passed over each endpoint in turn would report a budget where the
// answer is a posture.
func TestThePoolRefusesAnEndpointTheRoleMayNotBeServedOn(t *testing.T) {
	t.Parallel()

	cfg := pooledConfig(t, "")
	// Codex holds the developer's posture and not the reviewer's, which is the
	// discriminator capability validation already knows.
	reviewer := cfg.Agents["reviewer"]
	reviewer.Backend = domain.BackendCodex
	cfg.Agents["reviewer"] = reviewer

	_, err := cfg.ChooseEndpoint(builtInRegistry(t), "/state", "reviewer", backend.Endpoint{}, nil)
	if err == nil {
		t.Fatal("ChooseEndpoint() served a reviewer on a provider that cannot hold the read-only posture")
	}
	if !strings.Contains(err.Error(), `cannot hold the "read-only" tool posture`) {
		t.Fatalf("ChooseEndpoint() = %v, want the posture named", err)
	}

	// An agent nothing configured has no endpoint either, which is refused rather
	// than answered with somebody else's.
	if _, err := cfg.ChooseEndpoint(builtInRegistry(t), "/state", "nobody", backend.Endpoint{}, nil); err == nil {
		t.Fatal("ChooseEndpoint() answered for an agent this configuration does not name")
	}
}

// An agent's own invocations are made on a stable endpoint rather than a
// rotating one, for the reason its account is stable: a conversation lasts for
// weeks, and an agent moved between accounts each turn would have no provider
// session left to resume.
func TestAnAgentsOwnEndpointIsTheAccountItIsAssignedTo(t *testing.T) {
	t.Parallel()

	cfg := pooledConfig(t, "")
	reviewer := cfg.Agents["reviewer"]
	reviewer.Account = "two"
	cfg.Agents["reviewer"] = reviewer

	choice, err := cfg.AgentEndpoint(builtInRegistry(t), "/state", "reviewer")
	if err != nil {
		t.Fatalf("AgentEndpoint() error = %v", err)
	}
	if choice.Endpoint.AccountAlias != "two" || choice.Endpoint.Model != "sonnet" {
		t.Fatalf("AgentEndpoint() = %s, want the endpoint the agent is assigned to", choice.Endpoint)
	}
	if choice.Account.Alias != "two" {
		t.Fatalf("the endpoint authenticates as %q, want the agent's own account", choice.Account.Alias)
	}
}
