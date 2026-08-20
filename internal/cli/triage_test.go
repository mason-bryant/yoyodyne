package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// The verb carries out a decision somebody else recorded, so what it needs is
// the stoppage and the reasoning. Neither is guessed at.
func TestTriageRerunRequiresTheStoppageItActsOn(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"triage", "rerun"}, &stdout, &stderr, "test")
	if code != 2 {
		t.Fatalf("Run() code = %d, want 2; stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "exactly one run identifier") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestTriageRefusesACommandItDoesNotHave(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"triage", "rearm", "run-0123456789abcdef0123456789abcdef"}, &stdout, &stderr, "test")
	if code != 2 {
		t.Fatalf("Run() code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unknown triage command") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

// A re-run that was refused before anything was claimed is reported as a
// refusal rather than as a run that failed, and in JSON it carries the refusal
// where a script reads it.
func TestTriageRerunReportsARefusalAsJSON(t *testing.T) {
	t.Parallel()

	path := writeConfig(t, "version: 3\n")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"triage", "rerun", "--config", path, "--reason", "the ground moved", "--json",
		"run-0123456789abcdef0123456789abcdef"}, &stdout, &stderr, "test")
	if code != 1 {
		t.Fatalf("Run() code = %d, want 1; stderr = %q", code, stderr.String())
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal() error = %v; stdout = %q", err, stdout.String())
	}
	if result["error"] == nil || result["rerun"] != nil {
		t.Fatalf("result = %v, want the refusal and no re-run", result)
	}
}

// The usage says the two things that are easy to get wrong about this verb: the
// stoppage is re-run once, and the intake hold applies because the harness is
// the one choosing the work.
func TestTriageUsageSaysWhatBoundsARerun(t *testing.T) {
	t.Parallel()

	var usage bytes.Buffer
	printTriageUsage(&usage)
	for _, want := range []string{"once", "intake hold", "--reason"} {
		if !strings.Contains(usage.String(), want) {
			t.Fatalf("usage does not mention %q:\n%s", want, usage.String())
		}
	}
}
