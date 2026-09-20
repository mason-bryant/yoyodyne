package chat

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// releaseNotesScriptPath is the notes writer, which reads the admission this
// package writes onto an item's notes to say which persona asked for the item.
const releaseNotesScriptPath = "../../scripts/release-notes.sh"

// TestTheReleaseNotesReadTheAdmissionThisPackageWrites feeds the admission
// notes the real writers produce -- trackerProvenance for the creation line,
// creationVerb for a decomposition's wording, reportNote for an admission from
// a collected report -- through scripts/release-notes.sh, and reads the
// `Requested by` line back off the draft. The script matches those lines with
// expressions of its own, so a change to the wording here that nothing in Go
// exercises would otherwise make the script fall silent: an item the reviewer
// asked for would read as one the operator asked for, which is the one guess
// the notes must never make. This is what makes that drift fail instead.
func TestTheReleaseNotesReadTheAdmissionThisPackageWrites(t *testing.T) {
	t.Parallel()

	for _, tool := range []string{"bash", "git", "python3"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s needs %s, which is not on PATH", releaseNotesScriptPath, tool)
		}
	}
	script, err := os.ReadFile(releaseNotesScriptPath)
	if err != nil {
		t.Fatalf("read the notes writer: %v", err)
	}

	// The development manager decomposing a parent, worded by the same code
	// that words it in a conversation.
	manager := &Session{state: runstate.Conversation{
		ConversationID: "chat-419cedb4",
		Role:           domain.RoleDevelopmentManager,
		Turns:          80,
	}}
	decomposition := manager.trackerProvenance(manager.creationVerb("scratch-ifd.9").note,
		"the epic's first buildable slice, kept its own run so the review stays readable")

	// The product manager admitting an item from a reviewer's report.
	productManager := &Session{state: runstate.Conversation{
		ConversationID: "chat-91253e0e",
		Role:           domain.RoleProductManager,
		Turns:          408,
	}}
	fromReport := productManager.trackerProvenance("Admitted to the backlog", "the pile said so") +
		reportNote(report.Report{
			ID:       "report-6f1a2b3c",
			Severity: report.SeverityWarning,
			Role:     domain.RoleReviewer,
			Message:  "the goals listing prints in tracker order, which is not the order a reader needs",
		})

	// And the ordinary admission, which names no persona: the line has to be
	// absent for it, so a writer that started matching everything is caught too.
	fromOperator := productManager.trackerProvenance("Admitted to the backlog",
		"Operator request: the harness should run until told to stop")

	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "scripts", "release-notes.sh"), script, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(project, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	var export strings.Builder
	for _, item := range []struct {
		id, title, notes string
	}{
		{"scratch-ifd.1", "Watch mode", fromOperator},
		{"scratch-ifd.2", "Format the goals listing", fromReport},
		{"scratch-ifd.4", "Shift-return inserts a newline", decomposition},
	} {
		line, err := json.Marshal(map[string]any{
			"_type":       "issue",
			"id":          item.id,
			"title":       item.title,
			"description": "What the item asked for.",
			"issue_type":  "task",
			"priority":    2,
			"status":      "closed",
			"notes":       item.notes,
		})
		if err != nil {
			t.Fatal(err)
		}
		export.Write(line)
		export.WriteString("\n")
	}
	if err := os.WriteFile(filepath.Join(project, ".beads", "issues.jsonl"), []byte(export.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	git := func(args ...string) {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", project}, args...)...)
		command.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=releasenotes-test", "GIT_AUTHOR_EMAIL=test@example.invalid",
			"GIT_COMMITTER_NAME=releasenotes-test", "GIT_COMMITTER_EMAIL=test@example.invalid")
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	git("init", "-q")
	git("add", "-A")
	git("commit", "-qm", "product: scratch-ifd.1 scratch-ifd.2 scratch-ifd.4 three items land")

	draft := exec.Command("bash", "scripts/release-notes.sh", "v0.1.0", "--print")
	draft.Dir = project
	draft.Env = append(os.Environ(), "VERSION=")
	notes, err := draft.CombinedOutput()
	if err != nil {
		t.Fatalf("release-notes.sh did not draft (%v):\n%s", err, notes)
	}

	for _, want := range []string{
		"  Requested by the reviewer: the goals listing prints in tracker order, which is not the order a reader needs\n",
		"  Requested by the development manager, decomposing `scratch-ifd.9`: the epic's first buildable slice, kept its own run so the review stays readable\n",
	} {
		if !strings.Contains(string(notes), want) {
			t.Errorf("the draft does not carry %q, so the notes writer no longer reads what this package writes:\n%s", want, notes)
		}
	}
	if got := strings.Count(string(notes), "Requested by"); got != 2 {
		t.Errorf("expected exactly two Requested by lines, one per persona-requested item, got %d:\n%s", got, notes)
	}
}
