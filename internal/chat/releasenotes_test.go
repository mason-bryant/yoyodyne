package chat

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// releaseNotesScriptPath is the notes writer, which reads the admission this
// package writes onto a work item's notes back out of the tracker's export.
const releaseNotesScriptPath = "../../scripts/release-notes.sh"

// TestTheReleaseNotesReadTheAdmissionsThisPackageWrites holds the two
// patterns scripts/release-notes.sh matches an admission with against the two
// writers here that produce one. The script says which persona asked for an
// item by reading the note reportNote writes for an admission from a role's
// report and the one creationVerb and trackerProvenance write for a
// decomposition; its own suite matches those patterns only against fixtures
// written to fit them, so a change to either writer's wording would drop the
// line silently and the entry would read as work the operator asked for. Here
// the real writers' output is what has to match, and the captures have to be
// the role and the words the script prints.
func TestTheReleaseNotesReadTheAdmissionsThisPackageWrites(t *testing.T) {
	t.Parallel()

	script, err := os.ReadFile(releaseNotesScriptPath)
	if err != nil {
		t.Fatalf("read %s: %v", releaseNotesScriptPath, err)
	}
	patterns := scriptPatterns(t, string(script), "REPORTED", "DECOMPOSED")

	reported := reportNote(report.Report{
		ID:       "report-6f1a2b3c",
		Severity: report.SeverityWarning,
		Role:     domain.RoleReviewer,
		Message:  "the goals listing prints in tracker order,\nwhich is not the order a reader needs.",
	})
	line := firstLine(t, reported)
	match := patterns["REPORTED"].FindStringSubmatch(line)
	if match == nil {
		t.Fatalf("REPORTED %q does not match the report admission this package writes:\n%s", patterns["REPORTED"], line)
	}
	if match[1] != "reviewer" {
		t.Errorf("REPORTED captured the role as %q, want %q", match[1], "reviewer")
	}
	if want := "the goals listing prints in tracker order, which is not the order a reader needs."; match[2] != want {
		t.Errorf("REPORTED captured the words as %q, want %q", match[2], want)
	}

	session := &Session{state: runstate.Conversation{
		ConversationID: "chat-419cedb4a013b063f477e322a2a60466",
		Role:           domain.RoleDevelopmentManager,
		Turns:          80,
	}}
	if !session.authority().ParentRequired {
		t.Fatal("the development manager is expected to create only under a parent; the decomposition note is what that writes")
	}
	decomposed := session.trackerProvenance(session.creationVerb("yoyodyne-ifd.209").note, "the epic's first buildable slice")
	line = firstLine(t, decomposed)
	match = patterns["DECOMPOSED"].FindStringSubmatch(line)
	if match == nil {
		t.Fatalf("DECOMPOSED %q does not match the decomposition this package writes:\n%s", patterns["DECOMPOSED"], line)
	}
	if match[1] != "yoyodyne-ifd.209" {
		t.Errorf("DECOMPOSED captured the parent as %q, want %q", match[1], "yoyodyne-ifd.209")
	}
	if match[2] != "development manager" {
		t.Errorf("DECOMPOSED captured the role as %q, want %q", match[2], "development manager")
	}
	// The reason the script prints beside the role is the one line this writer
	// puts after the admission, spelled the way the script looks for it.
	if !strings.Contains(decomposed, "\n\nReason: the epic's first buildable slice") {
		t.Errorf("the decomposition carries no Reason: line for the script to read:\n%s", decomposed)
	}

	// The ordinary admission is the one the script prints no line for, so it
	// must match neither: an item the operator asked for through the product
	// manager that read as a decomposition would name a persona that never
	// asked.
	session.state.Role = domain.RoleProductManager
	admitted := firstLine(t, session.trackerProvenance(session.creationVerb("").note, "Operator request: the harness should run until told to stop."))
	for name, pattern := range patterns {
		if pattern.MatchString(admitted) {
			t.Errorf("%s matches the product manager's ordinary admission, which carries no requester:\n%s", name, admitted)
		}
	}
}

// scriptPatterns reads the named Python regular expressions out of the notes
// writer's own text, `NAME = re.compile(r"...")`, and compiles each as Go
// reads it. The two dialects agree on everything these patterns use, and a
// pattern one of them refuses fails here by name rather than passing by
// omission.
func scriptPatterns(t *testing.T, script string, names ...string) map[string]*regexp.Regexp {
	t.Helper()
	found := map[string]*regexp.Regexp{}
	for _, name := range names {
		declaration := regexp.MustCompile(`(?m)^` + name + ` = re\.compile\(r(["'])(.*)(["'])\)$`)
		match := declaration.FindStringSubmatch(script)
		if match == nil || match[1] != match[3] {
			t.Fatalf("%s declares no %s = re.compile(r\"...\") pattern for this test to hold", releaseNotesScriptPath, name)
		}
		compiled, err := regexp.Compile(match[2])
		if err != nil {
			t.Fatalf("%s's %s pattern does not compile as Go reads it: %v", releaseNotesScriptPath, name, err)
		}
		found[name] = compiled
	}
	return found
}

// firstLine is the admission line the script matches: the notes writer reads
// an item's notes one stripped line at a time, so a note's first line is what
// its pattern sees, and the leading blank lines reportNote writes are not.
func firstLine(t *testing.T, note string) string {
	t.Helper()
	for _, line := range strings.Split(note, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	t.Fatalf("the note is blank: %q", note)
	return ""
}
