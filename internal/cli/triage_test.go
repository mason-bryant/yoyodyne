package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
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

// A carry-out that met a full harness is not a failure: nothing was claimed and
// the decision still stands, so what it reports is the state it is waiting on.
// A claim that could not be given back is the exception, because the stoppage
// has then paid for a wait that was meant to cost it nothing.
func TestTriageRerunReportsAFullHarnessAsAWaitRatherThanAFailure(t *testing.T) {
	t.Parallel()

	waiting := orchestrator.RerunResult{
		WorkItemID:   "yoyodyne-ifd.68.13",
		PriorRunID:   "run-0123456789abcdef0123456789abcdef",
		CapacityFull: &runstate.CapacityError{Limit: 2, Active: 2},
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := reportRerun(&stdout, &stderr, false, waiting, nil); code != 0 {
		t.Fatalf("reportRerun() code = %d, want a wait reported as something other than a failure; stderr = %q", code, stderr.String())
	}
	for _, want := range []string{"limit 2", "keeps its one re-run", "decision still stands"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, is missing %q", stdout.String(), want)
		}
	}

	waiting.RecordProblem = "the claim taken for it could not be given back"
	stdout.Reset()
	stderr.Reset()
	if code := reportRerun(&stdout, &stderr, false, waiting, nil); code != 1 {
		t.Fatalf("reportRerun() code = %d, want a spent claim reported as a failure", code)
	}
	if !strings.Contains(stdout.String(), "could not be given back") {
		t.Fatalf("stdout = %q, want the spent claim named", stdout.String())
	}
}

// The usage says the three things that are easy to get wrong about this verb:
// the stoppage is re-run once, the intake hold applies because the harness is
// the one choosing the work, and a harness with no free developer is a wait
// rather than a refusal.
func TestTriageUsageSaysWhatBoundsARerun(t *testing.T) {
	t.Parallel()

	var usage bytes.Buffer
	printTriageUsage(&usage)
	for _, want := range []string{"once", "intake hold", "--reason", "no free developer"} {
		if !strings.Contains(usage.String(), want) {
			t.Fatalf("usage does not mention %q:\n%s", want, usage.String())
		}
	}
}
