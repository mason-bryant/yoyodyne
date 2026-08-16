package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"yoyodyne/internal/chat"
	"yoyodyne/internal/config"
	"yoyodyne/internal/domain"
	"yoyodyne/internal/gitworktree"
	"yoyodyne/internal/orchestrator"
)

func TestRunHelp(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(nil, &stdout, &stderr, "test")
	if code != 0 {
		t.Fatalf("Run() code = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "config validate") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunVersionJSON(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"version", "--json"}, &stdout, &stderr, "v0.1.0")
	if code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	var result map[string]string
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if result["version"] != "v0.1.0" {
		t.Fatalf("version = %q", result["version"])
	}
}

func TestRunConfigValidateJSON(t *testing.T) {
	t.Parallel()

	path := writeConfig(t, validConfig)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"config", "validate", "--config", path, "--json"}, &stdout, &stderr, "test")
	if code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if result["status"] != "valid" {
		t.Fatalf("status = %v", result["status"])
	}
	if result["product_id"] != "yoyodyne" {
		t.Fatalf("product_id = %v", result["product_id"])
	}
}

func TestRunConfigValidateInvalidJSON(t *testing.T) {
	t.Parallel()

	path := writeConfig(t, "version: 2\n")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"config", "validate", "--config", path, "--json"}, &stdout, &stderr, "test")
	if code != 1 {
		t.Fatalf("Run() code = %d, want 1; stderr = %q", code, stderr.String())
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if result["status"] != "invalid" {
		t.Fatalf("status = %v", result["status"])
	}
}

func TestRunUnknownCommand(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"unknown"}, &stdout, &stderr, "test")
	if code != 2 {
		t.Fatalf("Run() code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunWorkItemRequiresExactlyOneID(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"run"}, &stdout, &stderr, "test")
	if code != 2 {
		t.Fatalf("Run() code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "exactly one Beads work item id") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

// Chat talks to whichever agent fills the product-manager role, with the
// persona that role resolved to. In this repository that is the built-in
// bundle's agent and its builtin:v1 persona, inherited rather than restated.
func TestChatResolvesTheConfiguredProductManager(t *testing.T) {
	t.Parallel()

	path := filepath.Join("..", "..", config.DirectoryName, config.FileName)
	resolved, err := config.LoadResolved(path)
	if err != nil {
		t.Fatalf("LoadResolved() error = %v", err)
	}
	agent := agentForRole(resolved.Config, domain.RoleProductManager)
	if agent.Role != domain.RoleProductManager {
		t.Fatal("no product-manager agent is configured; chat would have nobody to talk to")
	}
	if agent.Backend != domain.BackendClaudeCode {
		t.Fatalf("product-manager backend = %q, want %q", agent.Backend, domain.BackendClaudeCode)
	}
	if err := config.ValidateModelSelector(agent.Model); err != nil {
		t.Fatalf("product-manager model: %v", err)
	}
	if origin := resolved.Origins["agents.product-manager.persona"]; origin != config.BuiltinV1 {
		t.Fatalf("product-manager persona origin = %q, want the built-in bundle", origin)
	}
	if !strings.Contains(agent.Persona.Text, "You own product intent, not implementation.") {
		t.Fatalf("product-manager persona = %q", agent.Persona.Text)
	}
	// The persona is guidance underneath the contract, never a replacement for
	// it: the contract is what the conversation actually sends first.
	if !strings.HasPrefix(chat.SystemPrompt(agent.Persona.Text), "You are the product manager for this product") {
		t.Fatal("the conversation prompt does not begin with the immutable contract")
	}
}

func TestChatRefusesArgumentsItCannotHonor(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{
			name: "positional message",
			args: []string{"chat", "what is the brief?"},
			want: "does not accept positional arguments",
		},
		{
			// An interactive conversation has no single result, so machine
			// readable output has to name the turn it describes.
			name: "json without a message",
			args: []string{"chat", "--json"},
			want: "requires --message",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := Run(test.args, &stdout, &stderr, "test")
			if code != 2 {
				t.Fatalf("Run() code = %d, want 2; stderr = %q", code, stderr.String())
			}
			if !strings.Contains(stderr.String(), test.want) {
				t.Fatalf("stderr = %q, want it to contain %q", stderr.String(), test.want)
			}
		})
	}
}

func TestChatReportsConfigurationFailureAsJSON(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	code := Run([]string{"chat", "--config", missing, "--json", "--message", "hello"}, &stdout, &stderr, "test")
	if code != 1 {
		t.Fatalf("Run() code = %d, want 1; stderr = %q", code, stderr.String())
	}
	var result chatOutput
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if !strings.Contains(result.Error, "open config") {
		t.Fatalf("error = %q", result.Error)
	}
}

func TestRunWorkItemReportsConfigurationFailureAsJSON(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	code := Run([]string{"run", "--config", missing, "--json", "yoyodyne-task"}, &stdout, &stderr, "test")
	if code != 1 {
		t.Fatalf("Run() code = %d, want 1; stderr = %q", code, stderr.String())
	}
	var result runOutput
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if !strings.Contains(result.Error, "open config") {
		t.Fatalf("error = %q", result.Error)
	}
}

// The repository's own configuration is the one Yoyodyne self-hosts on, so it
// must validate under the automatic-integration policy it now enables: an
// independent reviewer agent and at least one deterministic check. It is also
// the worked example of a portable project configuration, so it inherits its
// agents from the built-in bundle rather than restating them.
func TestRepositoryConfigurationEnforcesAutomaticIntegration(t *testing.T) {
	t.Parallel()

	path := filepath.Join("..", "..", config.DirectoryName, config.FileName)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := Run([]string{"config", "validate", "--config", path}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}

	resolved, err := config.LoadResolved(path)
	if err != nil {
		t.Fatalf("LoadResolved() error = %v", err)
	}
	cfg := resolved.Config
	if cfg.Extends != config.BuiltinV1 {
		t.Fatalf("extends = %q, want %q", cfg.Extends, config.BuiltinV1)
	}
	for _, name := range []string{"developer", "reviewer"} {
		if origin := resolved.Origins["agents."+name+".persona"]; origin != config.BuiltinV1 {
			t.Errorf("agent %q persona origin = %q, want the built-in bundle", name, origin)
		}
		if strings.TrimSpace(cfg.Agents[name].Persona.Text) == "" {
			t.Errorf("agent %q has no effective persona", name)
		}
	}
	if cfg.Approvals.Integration != domain.ApprovalAutomatic {
		t.Fatalf("integration approval = %q, want %q", cfg.Approvals.Integration, domain.ApprovalAutomatic)
	}
	reviewers := 0
	for _, agent := range cfg.Agents {
		if agent.Role == domain.RoleReviewer {
			reviewers += agent.Instances
		}
	}
	if reviewers == 0 || len(cfg.Checks) == 0 {
		t.Fatalf("automatic integration is not gated: reviewers = %d, checks = %d", reviewers, len(cfg.Checks))
	}
	// Every executable agent declares its own selector, and the wiring uses the
	// reviewer's rather than letting the provider choose one.
	for name, agent := range cfg.Agents {
		if err := config.ValidateModelSelector(agent.Model); err != nil {
			t.Fatalf("agent %q model: %v", name, err)
		}
	}
	if got := agentModel(cfg, domain.RoleReviewer); got != cfg.Agents["reviewer"].Model {
		t.Fatalf("wired reviewer model = %q, want %q", got, cfg.Agents["reviewer"].Model)
	}
	if agentModel(cfg, domain.RoleDeveloper) == "" {
		t.Fatal("developer model selector is not configured")
	}
}

func TestReportRunResultIsTruthfulAboutRemovedArtifacts(t *testing.T) {
	t.Parallel()

	integration := &gitworktree.Integration{TargetBranch: "main", SourceCommit: "abc123", TargetCommit: "abc123"}
	for _, test := range []struct {
		name     string
		outcome  orchestrator.Outcome
		err      error
		wantCode int
		want     []string
		reject   []string
	}{
		{
			name:     "integrated and cleaned up",
			outcome:  orchestrator.Outcome{RunID: "run-1", Branch: "b", WorktreePath: "/wt", Integration: integration, WorktreeRemoved: true, BranchRemoved: true},
			wantCode: 0,
			want:     []string{"worktree removed: /wt", "branch removed: b"},
			reject:   []string{"NOT removed", "remaining"},
		},
		{
			name:     "cleanup outstanding after a successful run",
			outcome:  orchestrator.Outcome{RunID: "run-1", Branch: "b", WorktreePath: "/wt", Integration: integration, CleanupFailure: "worktree is busy"},
			wantCode: 0,
			want:     []string{"worktree NOT removed: /wt", "branch NOT removed: b", "cleanup incomplete", "remaining worktree: /wt", "remaining branch: b"},
		},
		{
			// Partial cleanup: the worktree is gone and only the branch is left.
			name:     "partial cleanup leaves only the branch",
			outcome:  orchestrator.Outcome{RunID: "run-1", Branch: "b", WorktreePath: "/wt", Integration: integration, WorktreeRemoved: true, CleanupFailure: "branch is busy"},
			wantCode: 0,
			want:     []string{"worktree removed: /wt", "branch NOT removed: b", "remaining branch: b"},
			reject:   []string{"remaining worktree"},
		},
		{
			// Both removals succeeded and only their confirmation failed, so no
			// artifact may be described as remaining.
			name: "cleanup verification failed with nothing left",
			outcome: orchestrator.Outcome{
				RunID: "run-1", Branch: "b", WorktreePath: "/wt", Integration: integration,
				WorktreeRemoved: true, BranchRemoved: true, CleanupFailure: "verify removal of worktree: runner unavailable",
			},
			wantCode: 0,
			want:     []string{"cleanup could not be confirmed", "nothing is known to remain", "worktree removed: /wt", "branch removed: b"},
			reject:   []string{"cleanup incomplete", "remaining branch", "remaining worktree", "NOT removed"},
		},
		{
			// Cleanup finished and only writing it down failed: nothing may be
			// described as incomplete or remaining.
			name: "completion recording failed after a finished cleanup",
			outcome: orchestrator.Outcome{
				RunID: "run-1", Branch: "b", WorktreePath: "/wt", Integration: integration,
				WorktreeRemoved: true, BranchRemoved: true, CompletionRecordingFailure: "state store is unavailable",
			},
			wantCode: 0,
			want:     []string{"completion recording failed", "cleanup completed", "worktree removed: /wt", "branch removed: b"},
			reject:   []string{"cleanup incomplete", "remaining", "NOT removed"},
		},
		{
			// A failure must never describe deleted artifacts as preserved.
			name:     "failure after the artifacts were removed",
			outcome:  orchestrator.Outcome{RunID: "run-1", Branch: "b", WorktreePath: "/wt", Integration: integration, WorktreeRemoved: true, BranchRemoved: true},
			err:      errors.New("something later failed"),
			wantCode: 1,
			want:     []string{"worktree was already removed: /wt", "branch was already removed: b"},
			reject:   []string{"preserved worktree", "preserved branch"},
		},
		{
			// A failure after a partial cleanup preserves only what survives.
			name:     "failure after a partial cleanup",
			outcome:  orchestrator.Outcome{RunID: "run-1", Branch: "b", WorktreePath: "/wt", Integration: integration, WorktreeRemoved: true},
			err:      errors.New("something later failed"),
			wantCode: 1,
			want:     []string{"preserved branch: b", "worktree was already removed: /wt"},
			reject:   []string{"preserved worktree"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			code := reportRunResult(&stdout, &stderr, false, test.outcome, test.err)
			if code != test.wantCode {
				t.Fatalf("reportRunResult() = %d, want %d", code, test.wantCode)
			}
			combined := stdout.String() + stderr.String()
			for _, want := range test.want {
				if !strings.Contains(combined, want) {
					t.Errorf("output is missing %q: %s", want, combined)
				}
			}
			for _, reject := range test.reject {
				if strings.Contains(combined, reject) {
					t.Errorf("output falsely claims %q: %s", reject, combined)
				}
			}
		})
	}
}

func TestResolvePathRelativeToConfiguration(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	got, err := resolvePath(base, "repository")
	if err != nil {
		t.Fatalf("resolvePath() error = %v", err)
	}
	want := filepath.Join(base, "repository")
	if got != want {
		t.Fatalf("resolvePath() = %q, want %q", got, want)
	}
}

func TestConfigShowExplainsInheritance(t *testing.T) {
	t.Parallel()

	path := writeProjectConfig(t, portableConfig)
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"config", "show", "--config", path, "--effective", "--origins"}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	output := stdout.String()
	for _, want := range []string{
		"# layer: " + config.BuiltinV1,
		"role: architect",
		"model: claude-opus-5-20260514",
		"source: " + config.BuiltinV1 + "/personas/developer.md",
		"agents.developer.model: " + path,
		"agents.developer.role: " + config.BuiltinV1,
		"approvals.brief: " + config.BuiltinV1,
		"execution.worktree_root: " + config.BuiltinV1,
		"product.repository_id: " + config.OriginDerived,
	} {
		if !strings.Contains(output, want) {
			t.Errorf("config show output is missing %q:\n%s", want, output)
		}
	}
	// The persona body belongs in a prompt, not in a diagnostic listing.
	if strings.Contains(output, "You implement one bounded work item") {
		t.Errorf("config show inlined a persona body:\n%s", output)
	}
}

func TestConfigShowJSONReportsEffectiveValuesAndOrigins(t *testing.T) {
	t.Parallel()

	path := writeProjectConfig(t, portableConfig)
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"config", "show", "--config", path, "--effective", "--origins", "--json"}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	var result struct {
		Config    string            `json:"config"`
		Sources   []string          `json:"sources"`
		Effective config.Config     `json:"effective"`
		Origins   map[string]string `json:"origins"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if result.Config != path || len(result.Sources) != 2 || result.Sources[0] != config.BuiltinV1 {
		t.Fatalf("config = %q, sources = %v", result.Config, result.Sources)
	}
	if len(result.Effective.Agents) != 5 {
		t.Fatalf("effective agents = %d, want the five inherited defaults", len(result.Effective.Agents))
	}
	if result.Origins["agents.reviewer.backend"] != config.BuiltinV1 {
		t.Fatalf("reviewer backend origin = %q", result.Origins["agents.reviewer.backend"])
	}
}

// Discovery is what lets Yoyodyne run from anywhere inside a project, so the
// no-flag path is exercised from a nested directory rather than assumed.
func TestConfigValidateDiscoversTheProjectConfiguration(t *testing.T) {
	path := writeProjectConfig(t, portableConfig)
	nested := filepath.Join(filepath.Dir(filepath.Dir(path)), "internal", "nested")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	t.Chdir(nested)

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"config", "validate"}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), path) {
		t.Fatalf("stdout = %q, want the discovered configuration %q", stdout.String(), path)
	}
}

func TestConfigValidateReportsAMissingConfiguration(t *testing.T) {
	t.Chdir(t.TempDir())

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"config", "validate"}, &stdout, &stderr, "test"); code != 1 {
		t.Fatalf("Run() code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "no Yoyodyne configuration found") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func writeProjectConfig(t *testing.T, content string) string {
	t.Helper()
	directory := filepath.Join(t.TempDir(), config.DirectoryName)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	path := filepath.Join(directory, config.FileName)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".yoyodyne.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

// portableConfig is what a project outside the Yoyodyne source tree writes: its
// own identity plus one sparse override, with every agent default inherited.
const portableConfig = `version: 1
extends: builtin:v1
product:
  id: example
  repository: .
agents:
  developer:
    model: claude-opus-5-20260514
`

const validConfig = `version: 1
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
  developer:
    role: developer
    backend: claude-code
    model: opus
`
