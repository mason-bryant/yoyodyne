package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArtifactsAreListedAndShownWithTheirIdentity(t *testing.T) {
	t.Parallel()

	configPath := writeConfig(t, validConfig)
	project := filepath.Dir(configPath)
	writeArtifact(t, project, "docs/product/brief.md", `---
id: brief
kind: brief
title: Product brief
status: active
revisions:
    - action: created
      by: product-manager
      at: 2026-08-17T12:00:00Z
      reason: identity added when artifact governance arrived
---

Intent in, software out.
`)
	writeArtifact(t, project, "docs/product/goals/v1-goals.md", `---
id: v1-goals
kind: goals
title: V1 goals
supports:
    - brief
status: active
revisions:
    - action: created
      by: product-manager
      at: 2026-08-17T12:00:00Z
      reason: identity added when artifact governance arrived
---

## Goals

- Something.
`)
	// A document in an artifact home with no identity is not an artifact anything
	// can refer to, so it is named rather than silently governed.
	writeArtifact(t, project, "docs/decisions/undecided.md", "# A decision nobody identified\n")

	stdout, stderr, code := runCLI(t, "artifact", "list", "--config", configPath)
	if code != 0 {
		t.Fatalf("list code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{"brief [brief, active] Product brief", "v1-goals [goals, active] V1 goals", "supports: brief"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("list stdout = %q, want it to contain %q", stdout, want)
		}
	}
	if !strings.Contains(stderr, "not an artifact: docs/decisions/undecided.md") {
		t.Fatalf("list stderr = %q", stderr)
	}

	stdout, stderr, code = runCLI(t, "artifact", "list", "--config", configPath, "--kind", "goals")
	if code != 0 {
		t.Fatalf("list --kind code = %d, stderr = %q", code, stderr)
	}
	if strings.Contains(stdout, "brief [brief") || !strings.Contains(stdout, "v1-goals") {
		t.Fatalf("list --kind stdout = %q", stdout)
	}

	stdout, stderr, code = runCLI(t, "artifact", "show", "--config", configPath, "--json", "v1-goals")
	if code != 0 {
		t.Fatalf("show code = %d, stderr = %q", code, stderr)
	}
	var shown struct {
		Artifacts []struct {
			ID        string   `json:"id"`
			Kind      string   `json:"kind"`
			Status    string   `json:"status"`
			Supports  []string `json:"supports"`
			Path      string   `json:"path"`
			Revisions []struct {
				Action string `json:"action"`
				By     string `json:"by"`
			} `json:"revisions"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal([]byte(stdout), &shown); err != nil {
		t.Fatalf("Unmarshal() error = %v over %q", err, stdout)
	}
	if len(shown.Artifacts) != 1 {
		t.Fatalf("shown = %#v", shown)
	}
	goals := shown.Artifacts[0]
	if goals.Kind != "goals" || goals.Status != "active" || goals.Path != "docs/product/goals/v1-goals.md" {
		t.Fatalf("goals = %#v", goals)
	}
	if len(goals.Supports) != 1 || goals.Supports[0] != "brief" || len(goals.Revisions) != 1 || goals.Revisions[0].By != "product-manager" {
		t.Fatalf("goals = %#v", goals)
	}

	if _, stderr, code = runCLI(t, "artifact", "show", "--config", configPath, "nothing-by-this-name"); code != 1 {
		t.Fatalf("show of an unknown id code = %d, stderr = %q", code, stderr)
	}
}

func writeArtifact(t *testing.T, project, relative, content string) {
	t.Helper()
	path := filepath.Join(project, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}
