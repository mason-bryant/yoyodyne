package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// hierarchyConfig configures the whole default role hierarchy, which is what an
// operator addressing "any configured agent" actually has in front of them.
const hierarchyConfig = `version: 1
product:
  id: yoyodyne
  repository: .
approvals:
  brief: human
  goals: human
  designs: automatic
  integration: human
checks:
  - go test ./...
agents:
  product-manager:
    role: product-manager
    backend: claude-code
    model: opus
  architect:
    role: architect
    backend: claude-code
    model: opus
  development-manager:
    role: development-manager
    backend: claude-code
    model: opus
  developer:
    role: developer
    backend: claude-code
    model: opus
  reviewer:
    role: reviewer
    backend: claude-code
    model: opus
`

// The operator can see who is configured and what each one decides, without a
// provider being started for any of it: this reads durable state, and an agent
// nobody has spoken to says so rather than being missing from the list.
func TestAgentListReportsEveryConfiguredAgentAndItsDurableState(t *testing.T) {
	// Not parallel: the state root the command reads is set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, hierarchyConfig)

	stdout, stderr, code := runCLI(t, "agent", "list", "--config", configPath)
	if code != 0 {
		t.Fatalf("agent list code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{
		"5 configured agent(s) for yoyodyne",
		"architect (architect) claude-code, model opus",
		"owns the designs, the decision records, and the architectural invariants",
		"development-manager (development-manager)",
		"owns decomposition, dependency structure, and assignment of admitted work",
		"no conversation recorded",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want it to contain %q", stdout, want)
		}
	}

	// A conversation recorded by an earlier process is what the agent's identity
	// is: the process that held it is gone, and the operator is still told what
	// the architect has been told and when.
	store, err := runstate.NewConversationStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewConversationStore() error = %v", err)
	}
	spokenAt := time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC)
	if err := store.Save(runstate.Conversation{
		SchemaVersion:         runstate.ConversationSchemaVersion,
		ConversationID:        "chat-0123456789abcdef0123456789abcdef",
		ProductID:             "yoyodyne",
		RepositoryID:          "yoyodyne",
		Role:                  domain.RoleArchitect,
		Backend:               domain.BackendClaudeCode,
		ProviderSessionID:     "session-architect",
		ProviderModel:         "opus",
		ProviderResolvedModel: "claude-opus-5-20260514",
		Turns:                 3,
		StartedAt:             spokenAt.Add(-time.Hour),
		UpdatedAt:             spokenAt,
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	stdout, stderr, code = runCLI(t, "agent", "list", "--config", configPath)
	if code != 0 {
		t.Fatalf("agent list code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{
		"conversation chat-0123456789abcdef0123456789abcdef: 3 turn(s), last spoken to 2026-08-18T09:00:00Z",
		"last answered by claude-opus-5-20260514 (asked for opus)",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want it to contain %q", stdout, want)
		}
	}

	// The same state, for anything that is not a terminal.
	stdout, stderr, code = runCLI(t, "agent", "list", "--config", configPath, "--json")
	if code != 0 {
		t.Fatalf("agent list --json code = %d, stderr = %q", code, stderr)
	}
	var decoded struct {
		Product string        `json:"product"`
		Agents  []agentReport `json:"agents"`
	}
	if err := json.Unmarshal([]byte(stdout), &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v over %q", err, stdout)
	}
	if decoded.Product != "yoyodyne" || len(decoded.Agents) != 5 {
		t.Fatalf("decoded = %#v", decoded)
	}
	byName := map[string]agentReport{}
	for _, agent := range decoded.Agents {
		byName[agent.Name] = agent
		if !agent.Addressable {
			t.Fatalf("%s is reported as unaddressable", agent.Name)
		}
	}
	architect := byName["architect"]
	if architect.Conversation == nil || architect.Conversation.Turns != 3 || !architect.Conversation.Resumable {
		t.Fatalf("architect = %#v", architect)
	}
	if byName["developer"].Conversation != nil {
		t.Fatal("the developer reports a conversation nobody had with it")
	}
}

// One agent in full, including the work its role is executing. A developer that
// is in the middle of a run is the case where "what is this agent doing" has an
// answer that is not a conversation.
func TestAgentShowReportsOneAgentAndTheWorkItsRoleIsExecuting(t *testing.T) {
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, hierarchyConfig)

	store, err := runstate.NewStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	started := time.Date(2026, 8, 18, 10, 0, 0, 0, time.UTC)
	if err := store.Create(runstate.State{
		SchemaVersion: runstate.StateSchemaVersion,
		RunID:         "run-0123456789abcdef0123456789abcdef",
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		WorkItemID:    "yoyodyne-ifd.4",
		Backend:       domain.BackendClaudeCode,
		Status:        runstate.StatusRunning,
		Phase:         runstate.PhaseDeveloping,
		StartedAt:     started,
		UpdatedAt:     started,
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	stdout, stderr, code := runCLI(t, "agent", "show", "--config", configPath, "developer")
	if code != 0 {
		t.Fatalf("agent show code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{
		"developer (developer) claude-code, model opus",
		"run run-0123456789abcdef0123456789abcdef on yoyodyne-ifd.4: running (developing)",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want it to contain %q", stdout, want)
		}
	}

	// A role that does not execute inside a run is not reported as idle work:
	// the architect has no runs, and saying "no run is in flight" about it would
	// describe a queue it has nothing to do with.
	stdout, stderr, code = runCLI(t, "agent", "show", "--config", configPath, "architect")
	if code != 0 {
		t.Fatalf("agent show architect code = %d, stderr = %q", code, stderr)
	}
	if strings.Contains(stdout, "no run is in flight") || strings.Contains(stdout, "run run-") {
		t.Fatalf("the architect was reported against runs: %q", stdout)
	}

	// An agent nobody configured is refused by name rather than by failing
	// somewhere further in.
	_, stderr, code = runCLI(t, "agent", "show", "--config", configPath, "release-manager")
	if code == 0 || !strings.Contains(stderr, `no agent named "release-manager" is configured`) {
		t.Fatalf("agent show code = %d, stderr = %q", code, stderr)
	}
}

// What the operator types is either an agent's configured name or the role it
// fills, because those are the two things they are likely to remember. A role
// filled by more than one agent is a question rather than a request.
func TestResolveAgentAcceptsANameOrARoleAndRefusesAnAmbiguousOne(t *testing.T) {
	t.Parallel()

	resolved, err := loadConfiguration(writeConfig(t, hierarchyConfig))
	if err != nil {
		t.Fatalf("loadConfiguration() error = %v", err)
	}
	name, role, err := resolveAgent(resolved.Config, "architect")
	if err != nil || name != "architect" || role != domain.RoleArchitect {
		t.Fatalf("resolveAgent(architect) = %q, %q, %v", name, role, err)
	}

	// A project may name its agents whatever it likes; the role still addresses
	// the one filling it.
	renamed := resolved.Config
	renamed.Agents = map[string]config.AgentConfig{
		"house-architect": resolved.Config.Agents["architect"],
	}
	name, role, err = resolveAgent(renamed, "architect")
	if err != nil || name != "house-architect" || role != domain.RoleArchitect {
		t.Fatalf("resolveAgent(role) = %q, %q, %v", name, role, err)
	}

	twoDevelopers := resolved.Config
	twoDevelopers.Agents = map[string]config.AgentConfig{
		"first-developer":  resolved.Config.Agents["developer"],
		"second-developer": resolved.Config.Agents["developer"],
	}
	if _, _, err := resolveAgent(twoDevelopers, "developer"); err == nil {
		t.Fatal("resolveAgent() resolved a name that two agents answer to")
	}
	if _, _, err := resolveAgent(resolved.Config, "  "); err == nil {
		t.Fatal("resolveAgent() accepted an empty name")
	}
}

// Each role is given the documents it answers for, and the product manager is
// given none of them. That asymmetry is the point: intent is what the product
// manager reasons from, and everything downstream of intent needs the documents
// that record how the product is built.
func TestEachRoleIsGivenTheDocumentsItAnswersFor(t *testing.T) {
	t.Parallel()

	product := config.Product{
		Designs:    "docs/designs",
		Invariants: "docs/decisions/invariants",
		Decisions:  "docs/decisions",
	}
	if sets := roleDocumentSets(domain.RoleProductManager, product); len(sets) != 0 {
		t.Fatalf("the product manager was given %#v", sets)
	}
	architect := roleDocumentSets(domain.RoleArchitect, product)
	if len(architect) != 3 {
		t.Fatalf("architect sets = %#v", architect)
	}
	// The invariants come before the decision records they are extracted from,
	// so the nested directory is read under the label that says what it is.
	if architect[0].Directory != "docs/designs" ||
		architect[1].Directory != "docs/decisions/invariants" ||
		architect[2].Directory != "docs/decisions" {
		t.Fatalf("architect sets are out of order: %#v", architect)
	}
	if architect[1].Label != "Architectural invariant" {
		t.Fatalf("the invariants are labelled %q", architect[1].Label)
	}
	for _, role := range []domain.AgentRole{domain.RoleDevelopmentManager, domain.RoleDeveloper, domain.RoleReviewer} {
		sets := roleDocumentSets(role, product)
		if len(sets) != 2 || sets[0].Directory != "docs/designs" || sets[1].Directory != "docs/decisions/invariants" {
			t.Fatalf("%s sets = %#v", role, sets)
		}
	}
}
