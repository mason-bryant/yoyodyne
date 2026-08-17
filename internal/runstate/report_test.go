package runstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"yoyodyne/internal/report"
)

func TestCollectedReportsSurviveTheProcessThatMadeThem(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newTestReportStore(t, root)
	first := testReport("report-0123456789abcdef0123456789abcdef", report.SeverityWarning, "the declared bundle version is inert")
	second := testReport("report-fedcba9876543210fedcba9876543210", report.SeverityNote, "bd lint could not run in the sandbox")
	for _, reported := range []report.Report{first, second} {
		if err := store.Append(reported); err != nil {
			t.Fatalf("Append() error = %v", err)
		}
	}

	// A second process reads what the first wrote, in the order it was written:
	// a report outlives the run that made it, so the pile is what an operator
	// reads long afterwards rather than anything held in memory.
	reloaded, err := newTestReportStore(t, root).List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(reloaded) != 2 {
		t.Fatalf("List() = %#v", reloaded)
	}
	if reloaded[0].ID != first.ID || reloaded[1].ID != second.ID {
		t.Fatalf("reports came back out of order: %s then %s", reloaded[0].ID, reloaded[1].ID)
	}
	if reloaded[0].Message != first.Message || reloaded[0].Severity != first.Severity {
		t.Fatalf("report did not survive intact: %#v", reloaded[0])
	}
	if !reloaded[0].RecordedAt.Equal(first.RecordedAt) {
		t.Fatalf("recorded time = %s, want %s", reloaded[0].RecordedAt, first.RecordedAt)
	}
}

func TestListingReportsBeforeAnybodyReportedIsNotAFailure(t *testing.T) {
	t.Parallel()

	reports, err := newTestReportStore(t, t.TempDir()).List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(reports) != 0 {
		t.Fatalf("List() = %#v, want nothing", reports)
	}
}

func TestReportsAreRefusedWhenTheyCannotBeReadBackOrDoNotBelongHere(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newTestReportStore(t, root)
	// A record that fails its own contract never reaches the log, so anything in
	// the log is something an operator can be shown.
	invalid := testReport("report-0123456789abcdef0123456789abcdef", "urgent", "unknown severity")
	if err := store.Append(invalid); err == nil {
		t.Fatal("Append() accepted a report with an invalid severity")
	}
	// The store is per product, exactly as the run and conversation stores are.
	foreign := testReport("report-0123456789abcdef0123456789abcdef", report.SeverityNote, "from elsewhere")
	foreign.ProductID = "elsewhere"
	if err := store.Append(foreign); err == nil {
		t.Fatal("Append() accepted another product's report")
	}

	// A log line that cannot be decoded fails the read rather than being skipped:
	// a pile that quietly drops what it cannot parse is one nobody can trust to
	// be complete.
	if err := store.Append(testReport("report-0123456789abcdef0123456789abcdef", report.SeverityNote, "good")); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
	if err := os.WriteFile(store.Path(), []byte("{\"schema_version\":1}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := store.List(); err == nil || !strings.Contains(err.Error(), "decode report log") {
		t.Fatalf("List() error = %v, want a decode failure", err)
	}
}

func TestReportStoreKeepsThePileBesideTheRunsRatherThanAmongThem(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newTestReportStore(t, root)
	runs, err := NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	if strings.HasPrefix(store.Path(), runs.Root()+string(filepath.Separator)) {
		t.Fatalf("the report log is inside the run directory: %s", store.Path())
	}
	if filepath.Dir(store.Path()) != filepath.Join(root, "products", "yoyodyne") {
		t.Fatalf("report log path = %s", store.Path())
	}
}

func newTestReportStore(t *testing.T, root string) *ReportStore {
	t.Helper()

	store, err := NewReportStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewReportStore() error = %v", err)
	}
	return store
}

func testReport(id string, severity report.Severity, message string) report.Report {
	return report.Report{
		SchemaVersion: report.SchemaVersion,
		ID:            id,
		Role:          "developer",
		Agent:         "developer",
		RunID:         "run-0123456789abcdef0123456789abcdef",
		WorkItemID:    "yoyodyne-ifd.19",
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		Severity:      severity,
		Message:       message,
		RecordedAt:    time.Date(2026, 8, 17, 9, 30, 0, 0, time.UTC),
	}
}
