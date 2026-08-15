package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
// independent reviewer agent and at least one deterministic check.
func TestRepositoryConfigurationEnforcesAutomaticIntegration(t *testing.T) {
	t.Parallel()

	path := filepath.Join("..", "..", ".yoyodyne.yaml")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := Run([]string{"config", "validate", "--config", path}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
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

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".yoyodyne.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

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
