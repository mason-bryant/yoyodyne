package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInvariantLifecycleFromTheCommandLine(t *testing.T) {
	t.Parallel()

	configPath := writeConfig(t, validConfig)
	project := filepath.Dir(configPath)

	stdout, stderr, code := runCLI(t, "invariant", "create",
		"--config", configPath,
		"--title", "One process at a time acts on an in-flight work item",
		"--statement", "Every entry into an in-flight run takes the run's exclusive lease first.",
		"--rationale", "The lease is the only thing keeping two developers off one item.",
		"--established-by", "yoyodyne-ifd.2.7",
		"--scope", "internal/runstate, internal/orchestrator",
		"--reason", "extracted from the decision that added the reservation",
		"one-writer-per-item")
	if code != 0 {
		t.Fatalf("create code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "docs/decisions/invariants/one-writer-per-item.md") {
		t.Fatalf("create stdout = %q", stdout)
	}
	// The invariant is a repository document, reviewed with the code rather than
	// runtime state hidden somewhere else.
	recorded := filepath.Join(project, "docs", "decisions", "invariants", "one-writer-per-item.md")
	content, err := os.ReadFile(recorded)
	if err != nil {
		t.Fatalf("read recorded invariant: %v", err)
	}
	for _, want := range []string{"id: one-writer-per-item", "status: active", "action: created", "by: architect", "## Must hold", "## Why"} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("recorded invariant is missing %q:\n%s", want, content)
		}
	}

	if _, stderr, code = runCLI(t, "invariant", "amend", "--config", configPath,
		"--scope", "internal/runstate",
		"--reason", "the orchestrator half moved behind the store",
		"one-writer-per-item"); code != 0 {
		t.Fatalf("amend code = %d, stderr = %q", code, stderr)
	}

	stdout, stderr, code = runCLI(t, "invariant", "list", "--config", configPath)
	if code != 0 {
		t.Fatalf("list code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "one-writer-per-item [active]") || !strings.Contains(stdout, "scope: internal/runstate") {
		t.Fatalf("list stdout = %q", stdout)
	}

	// Retirement is explicit and recorded: the constraint stops being delivered
	// and the decision to stop enforcing it stays readable.
	if _, stderr, code = runCLI(t, "invariant", "retire", "--config", configPath,
		"--reason", "the reservation moved into the store and cannot be bypassed",
		"one-writer-per-item"); code != 0 {
		t.Fatalf("retire code = %d, stderr = %q", code, stderr)
	}
	stdout, _, _ = runCLI(t, "invariant", "list", "--config", configPath)
	if strings.Contains(stdout, "one-writer-per-item") {
		t.Fatalf("a retired invariant is still listed as active: %q", stdout)
	}
	stdout, stderr, code = runCLI(t, "invariant", "show", "--config", configPath, "--json", "one-writer-per-item")
	if code != 0 {
		t.Fatalf("show code = %d, stderr = %q", code, stderr)
	}
	var shown struct {
		Invariants []struct {
			ID        string `json:"id"`
			Status    string `json:"status"`
			Revisions []struct {
				Action string `json:"action"`
				By     string `json:"by"`
				Reason string `json:"reason"`
			} `json:"revisions"`
		} `json:"invariants"`
	}
	if err := json.Unmarshal([]byte(stdout), &shown); err != nil {
		t.Fatalf("Unmarshal() error = %v over %q", err, stdout)
	}
	if len(shown.Invariants) != 1 || shown.Invariants[0].Status != "retired" || len(shown.Invariants[0].Revisions) != 3 {
		t.Fatalf("shown = %#v", shown)
	}
	retirement := shown.Invariants[0].Revisions[2]
	if retirement.Action != "retired" || retirement.By != "architect" || !strings.Contains(retirement.Reason, "cannot be bypassed") {
		t.Fatalf("retirement revision = %#v", retirement)
	}
}

func TestInvariantRefusesWhatCannotBeHeldTo(t *testing.T) {
	t.Parallel()

	configPath := writeConfig(t, validConfig)
	for name, args := range map[string][]string{
		// Every one of these would produce a constraint nobody could be held to or
		// judge later, so it is refused rather than recorded.
		"no rationale": {"invariant", "create", "--config", configPath,
			"--title", "A title", "--statement", "Something must hold.", "--established-by", "yoyodyne-ifd.1", "--reason", "because", "no-rationale"},
		"no origin": {"invariant", "create", "--config", configPath,
			"--title", "A title", "--statement", "Something.", "--rationale", "Reasons.", "--reason", "because", "no-origin"},
		"no reason": {"invariant", "create", "--config", configPath,
			"--title", "A title", "--statement", "Something.", "--rationale", "Reasons.", "--established-by", "yoyodyne-ifd.1", "no-reason"},
		"unusable id": {"invariant", "create", "--config", configPath,
			"--title", "A title", "--statement", "Something.", "--rationale", "Reasons.", "--established-by", "yoyodyne-ifd.1", "--reason", "because", "Not An Id"},
		"amending nothing":  {"invariant", "amend", "--config", configPath, "--reason", "because", "missing"},
		"retiring nothing":  {"invariant", "retire", "--config", configPath, "--reason", "because", "missing"},
		"showing nothing":   {"invariant", "show", "--config", configPath, "missing"},
		"unknown operation": {"invariant", "revoke", "--config", configPath, "missing"},
	} {
		_, stderr, code := runCLI(t, args...)
		if code == 0 {
			t.Fatalf("%s: command succeeded", name)
		}
		if strings.TrimSpace(stderr) == "" {
			t.Fatalf("%s: refused without saying why", name)
		}
	}
}

// The id read out of a listing is typed first and the options after it, which
// is how anybody addresses one invariant. Every flag here has to be read as the
// flag it is rather than as a second thing being named: what proves it is that
// the values arrive in the recorded invariant.
func TestInvariantOptionsAreReadAfterTheId(t *testing.T) {
	t.Parallel()

	configPath := writeConfig(t, validConfig)

	if _, stderr, code := runCLI(t, "invariant", "create", "one-writer-per-item",
		"--config", configPath,
		"--title", "One process at a time acts on an in-flight work item",
		"--statement", "Every entry into an in-flight run takes the run's exclusive lease first.",
		"--rationale", "The lease is the only thing keeping two developers off one item.",
		"--established-by", "yoyodyne-ifd.2.7",
		"--scope", "internal/runstate",
		"--reason", "extracted from the decision that added the reservation"); code != 0 {
		t.Fatalf("create code = %d, stderr = %q", code, stderr)
	}

	if _, stderr, code := runCLI(t, "invariant", "amend", "one-writer-per-item",
		"--config", configPath,
		"--scope", "internal/runstate, internal/orchestrator",
		"--reason", "the orchestrator half came back"); code != 0 {
		t.Fatalf("amend code = %d, stderr = %q", code, stderr)
	}

	if _, stderr, code := runCLI(t, "invariant", "retire", "one-writer-per-item",
		"--config", configPath,
		"--reason", "the reservation moved into the store and cannot be bypassed"); code != 0 {
		t.Fatalf("retire code = %d, stderr = %q", code, stderr)
	}

	stdout, stderr, code := runCLI(t, "invariant", "show", "one-writer-per-item", "--config", configPath, "--json")
	if code != 0 {
		t.Fatalf("show code = %d, stderr = %q", code, stderr)
	}
	var shown struct {
		Invariants []struct {
			ID        string   `json:"id"`
			Status    string   `json:"status"`
			Scope     []string `json:"scope"`
			Revisions []struct {
				Action string `json:"action"`
				Reason string `json:"reason"`
			} `json:"revisions"`
		} `json:"invariants"`
	}
	if err := json.Unmarshal([]byte(stdout), &shown); err != nil {
		t.Fatalf("Unmarshal() error = %v over %q", err, stdout)
	}
	if len(shown.Invariants) != 1 {
		t.Fatalf("shown = %#v, want the one invariant the flags after the id recorded", shown.Invariants)
	}
	recorded := shown.Invariants[0]
	if recorded.ID != "one-writer-per-item" || recorded.Status != "retired" {
		t.Fatalf("recorded = %#v", recorded)
	}
	if len(recorded.Scope) != 2 || recorded.Scope[1] != "internal/orchestrator" {
		t.Fatalf("scope = %#v, want what the amendment after the id said", recorded.Scope)
	}
	if len(recorded.Revisions) != 3 || !strings.Contains(recorded.Revisions[2].Reason, "cannot be bypassed") {
		t.Fatalf("revisions = %#v", recorded.Revisions)
	}
}

func runCLI(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var out, errs bytes.Buffer
	code = Run(args, &out, &errs, "test")
	return out.String(), errs.String(), code
}
