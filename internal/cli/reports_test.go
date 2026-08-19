package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The pile an operator was told about is readable without opening a
// conversation. That is the whole point of the verb: a run that finishes
// overnight says it reported something, and reading it must not cost an
// interactive conversation with a provider behind it.
func TestReportsReadsTheSamePileTheConversationShows(t *testing.T) {
	// Not parallel: the state root the command addresses is set here, and the
	// pile it reads is written under it.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	// Nothing reported yet is an answer, and it says where the pile would be so
	// an operator can tell it from reading the wrong product's store.
	stdout, stderr, code := runCLI(t, "reports", "--config", configPath)
	if code != 0 {
		t.Fatalf("reports code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "nothing has been reported") || !strings.Contains(stdout, "reports.jsonl") {
		t.Fatalf("stdout = %q", stdout)
	}

	store, err := runstate.NewReportStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewReportStore() error = %v", err)
	}
	if err := store.Append(report.Report{
		SchemaVersion: report.SchemaVersion,
		ID:            "report-0123456789abcdef0123456789abcdef",
		Role:          "developer",
		Agent:         "developer",
		RunID:         "run-0123456789abcdef0123456789abcdef",
		WorkItemID:    "yoyodyne-ifd.70",
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		Severity:      report.SeverityCritical,
		Message:       "bd lint could not run in its sandbox, so nothing linted the item",
		RecordedAt:    time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC),
	}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}

	stdout, stderr, code = runCLI(t, "reports", "--config", configPath)
	if code != 0 {
		t.Fatalf("reports code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{
		"reports (1 collected)",
		"critical",
		"from the developer on yoyodyne-ifd.70",
		"bd lint could not run",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want it to contain %q", stdout, want)
		}
	}

	// A script reads the same pile, with the attribution that makes it
	// triageable rather than the rendering that makes it readable.
	stdout, stderr, code = runCLI(t, "reports", "--config", configPath, "--json")
	if code != 0 {
		t.Fatalf("reports --json code = %d, stderr = %q", code, stderr)
	}
	var decoded reportsOutput
	if err := json.Unmarshal([]byte(stdout), &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v over %q", err, stdout)
	}
	if len(decoded.Reports) != 1 {
		t.Fatalf("reports = %#v", decoded.Reports)
	}
	collected := decoded.Reports[0]
	if collected.Role != "developer" || collected.WorkItemID != "yoyodyne-ifd.70" || collected.Severity != report.SeverityCritical {
		t.Fatalf("collected = %#v", collected)
	}
}

// The verb reads the pile without building the repository-facing components, so
// that the two still address one store is asserted rather than assumed. A
// reports verb pointed at a different root would answer "nothing has been
// reported" against a pile that is not empty, which is the one failure this
// verb cannot afford: it is the surface an operator checks precisely when they
// were told there was something to read.
func TestReportsReadsTheSameStoreTheRunComponentsAppendTo(t *testing.T) {
	// Not parallel: the state root both paths resolve is set here.
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())
	configPath := writeConfig(t, validConfig)

	parts, err := buildComponents(configPath)
	if err != nil {
		t.Fatalf("buildComponents() error = %v", err)
	}
	store, err := reportStore(configPath)
	if err != nil {
		t.Fatalf("reportStore() error = %v", err)
	}
	if store.Path() != parts.reports.Path() {
		t.Fatalf("reports read %s, but runs append to %s", store.Path(), parts.reports.Path())
	}
}

func TestReportsRefusesArgumentsItCannotHonor(t *testing.T) {
	t.Parallel()

	_, stderr, code := runCLI(t, "reports", "yoyodyne-ifd.70")
	if code != 2 {
		t.Fatalf("code = %d, want 2; stderr = %q", code, stderr)
	}
	if !strings.Contains(stderr, "does not accept positional arguments") {
		t.Fatalf("stderr = %q", stderr)
	}
}
