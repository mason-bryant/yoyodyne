package config

import (
	"errors"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

const validBootstrapConfig = `version: 1
product:
  id: yoyodyne
  repository: .
execution:
  max_concurrent_developers: 1
  repair_attempts_before_replan: 2
  worktree_root: auto
approvals:
  brief: human
  goals: human
  designs: automatic
  integration: human
checks:
  - go test ./...
agents:
  developers:
    role: developer
    backend: claude-code
    model: opus
    instances: 1
`

func TestDecodeValidBootstrapConfig(t *testing.T) {
	t.Parallel()

	cfg, err := Decode(strings.NewReader(validBootstrapConfig))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if cfg.Product.RepositoryID != "yoyodyne" {
		t.Fatalf("RepositoryID = %q, want yoyodyne", cfg.Product.RepositoryID)
	}
	if cfg.Agents["developers"].Backend != domain.BackendClaudeCode {
		t.Fatalf("developer backend = %q", cfg.Agents["developers"].Backend)
	}
}

func TestDecodeAppliesDefaults(t *testing.T) {
	t.Parallel()

	input := `version: 1
product:
  id: yoyodyne
  repository: .
approvals:
  brief: human
  goals: human
  designs: automatic
  integration: human
agents:
  developer:
    role: developer
    backend: claude-code
    model: opus
`
	cfg, err := Decode(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if cfg.Execution.MaxConcurrentDevelopers != 1 {
		t.Fatalf("MaxConcurrentDevelopers = %d, want 1", cfg.Execution.MaxConcurrentDevelopers)
	}
	if cfg.Execution.RepairAttemptsBeforeReplan != 2 {
		t.Fatalf("RepairAttemptsBeforeReplan = %d, want 2", cfg.Execution.RepairAttemptsBeforeReplan)
	}
	if cfg.Execution.WorktreeRoot != "auto" {
		t.Fatalf("WorktreeRoot = %q, want auto", cfg.Execution.WorktreeRoot)
	}
	if cfg.Agents["developer"].Instances != 1 {
		t.Fatalf("Instances = %d, want 1", cfg.Agents["developer"].Instances)
	}
}

func TestDecodePreservesExplicitZeroRepairAttempts(t *testing.T) {
	t.Parallel()

	input := strings.Replace(validBootstrapConfig, "repair_attempts_before_replan: 2", "repair_attempts_before_replan: 0", 1)
	cfg, err := Decode(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if cfg.Execution.RepairAttemptsBeforeReplan != 0 {
		t.Fatalf("RepairAttemptsBeforeReplan = %d, want 0", cfg.Execution.RepairAttemptsBeforeReplan)
	}
}

func TestDecodeRejectsExplicitZeroConcurrencyAndInstances(t *testing.T) {
	t.Parallel()

	input := strings.Replace(validBootstrapConfig, "max_concurrent_developers: 1", "max_concurrent_developers: 0", 1)
	input = strings.Replace(input, "instances: 1", "instances: 0", 1)
	_, err := Decode(strings.NewReader(input))
	if err == nil {
		t.Fatal("Decode() error = nil, want validation error")
	}
	for _, expected := range []string{"max_concurrent_developers must be at least 1", "instances must be at least 1"} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("Decode() error %q does not contain %q", err, expected)
		}
	}
}

func TestDecodeRejectsUnknownFields(t *testing.T) {
	t.Parallel()

	input := validBootstrapConfig + "unexpected: true\n"
	_, err := Decode(strings.NewReader(input))
	if err == nil || !strings.Contains(err.Error(), "field unexpected not found") {
		t.Fatalf("Decode() error = %v, want unknown field error", err)
	}
}

func TestDecodeRejectsMultipleDocuments(t *testing.T) {
	t.Parallel()

	_, err := Decode(strings.NewReader(validBootstrapConfig + "---\n{}\n"))
	if err == nil || !strings.Contains(err.Error(), "multiple YAML documents") {
		t.Fatalf("Decode() error = %v, want multiple document error", err)
	}
}

func TestValidateCollectsInvalidInvariants(t *testing.T) {
	t.Parallel()

	input := `version: 2
product:
  id: Invalid_ID
  repository: ""
execution:
  max_concurrent_developers: -1
  repair_attempts_before_replan: -1
approvals:
  brief: maybe
  goals: human
  designs: automatic
  integration: automatic
agents:
  product:
    role: product-manager
    backend: codex
`
	_, err := Decode(strings.NewReader(input))
	if err == nil {
		t.Fatal("Decode() error = nil, want validation error")
	}
	var validationErr ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("Decode() error type = %T, want ValidationError", err)
	}
	for _, expected := range []string{
		"version must be 1",
		"product id",
		"product repository is required",
		"max_concurrent_developers",
		"repair_attempts_before_replan",
		"approval brief",
		"does not support role",
		"at least one developer",
		"automatic integration requires at least one check",
		"automatic integration requires at least one reviewer",
	} {
		if !strings.Contains(err.Error(), expected) {
			t.Errorf("Decode() error %q does not contain %q", err, expected)
		}
	}
}

func TestValidateRequiresAModelSelectorForEveryAgent(t *testing.T) {
	t.Parallel()

	// There is no implicit harness default: an agent that names no model is a
	// run whose evidence could never say what produced it.
	missing := strings.Replace(validBootstrapConfig, "    model: opus\n", "", 1)
	if _, err := Decode(strings.NewReader(missing)); err == nil || !strings.Contains(err.Error(), `agent "developers" model selector is required`) {
		t.Fatalf("Decode() missing model error = %v", err)
	}

	for _, test := range []struct {
		name     string
		selector string
		problem  string
	}{
		{name: "floating family alias", selector: "opus"},
		{name: "pinned identifier", selector: "claude-opus-5-20260514"},
		{name: "blank", selector: `"   "`, problem: "model selector is required"},
		{name: "argument smuggling", selector: `"--dangerously-skip-permissions"`, problem: "must be a single model name"},
		{name: "multiple words", selector: `"opus sonnet"`, problem: "must be a single model name"},
		{name: "oversized", selector: strings.Repeat("m", MaxModelSelectorBytes+1), problem: "limit is"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input := strings.Replace(validBootstrapConfig, "model: opus", "model: "+test.selector, 1)
			cfg, err := Decode(strings.NewReader(input))
			if test.problem != "" {
				if err == nil || !strings.Contains(err.Error(), test.problem) {
					t.Fatalf("Decode() error = %v, want %q", err, test.problem)
				}
				return
			}
			if err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			if cfg.Agents["developers"].Model != test.selector {
				t.Fatalf("developer model = %q, want %q", cfg.Agents["developers"].Model, test.selector)
			}
		})
	}
}

func TestValidateAllowsCodexOnlyForThinRoles(t *testing.T) {
	t.Parallel()

	input := strings.Replace(validBootstrapConfig, "backend: claude-code", "backend: codex", 1)
	if _, err := Decode(strings.NewReader(input)); err != nil {
		t.Fatalf("Decode() developer Codex error = %v", err)
	}

	input = strings.Replace(input, "role: developer", "role: architect", 1)
	_, err := Decode(strings.NewReader(input))
	if err == nil || !strings.Contains(err.Error(), "does not support role") {
		t.Fatalf("Decode() architect Codex error = %v, want capability error", err)
	}
}
