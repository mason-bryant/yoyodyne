package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
`
