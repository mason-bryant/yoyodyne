package contextbundle

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

func TestAssembleUsesExplicitDeterministicReferences(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "docs", "z.md"), "z content\n")
	writeFile(t, filepath.Join(root, "docs", "a.md"), "a content\n")
	item := beads.WorkItem{
		ID:                 "yoyodyne-1",
		Title:              "Implement feature",
		Status:             "in_progress",
		Description:        "Read docs/z.md and docs/a.md.",
		Design:             "Bounded change",
		AcceptanceCriteria: "Tests pass",
		Notes:              "Preserve prior operator guidance.",
	}
	bundle, err := Assemble(Request{RepositoryRoot: root, WorkItem: item, References: []string{"docs/z.md"}})
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}
	paths := []string{bundle.References[0].Path, bundle.References[1].Path}
	if !reflect.DeepEqual(paths, []string{"docs/a.md", "docs/z.md"}) {
		t.Fatalf("reference paths = %#v", paths)
	}
	if strings.Index(bundle.Text, "## Referenced file: docs/a.md") > strings.Index(bundle.Text, "## Referenced file: docs/z.md") {
		t.Fatalf("bundle is not deterministic: %s", bundle.Text)
	}
	if !strings.Contains(bundle.Text, "## Notes\n\nPreserve prior operator guidance.") {
		t.Fatalf("bundle omitted work-item notes: %s", bundle.Text)
	}
}

// The context both the developer and the reviewer read the work item from says
// what the item waits on, and says it either way. A reviewer judging a change
// against an item that waits on unfinished work has to be able to see that it
// does, and a line that appeared only when there were dependencies would leave
// an item nothing blocks indistinguishable from one whose blockers this context
// happens not to carry.
func TestAssembleStatesWhatTheWorkItemWaitsOn(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	base := beads.WorkItem{ID: "yoyodyne-1", Title: "Task", Status: "in_progress"}

	unblocked, err := Assemble(Request{RepositoryRoot: root, WorkItem: base})
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}
	if !strings.Contains(unblocked.Text, "Depends on: nothing;") {
		t.Fatalf("bundle does not state an unblocked item as unblocked: %s", unblocked.Text)
	}

	// Only the relation that blocks, and only while the work it names is
	// unfinished: a closed blocker is not something anybody is waiting for, and a
	// parent-child link never was.
	waiting := base
	waiting.Dependencies = []beads.Dependency{
		{ID: "yoyodyne-9", Type: "blocks", Status: "open"},
		{ID: "yoyodyne-2", Type: "blocks", Status: "open"},
		{ID: "yoyodyne-7", Type: "blocks", Status: "closed"},
		{ID: "yoyodyne-8", Type: "parent-child", Status: "open"},
	}
	blocked, err := Assemble(Request{RepositoryRoot: root, WorkItem: waiting})
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}
	if !strings.Contains(blocked.Text, "Depends on: yoyodyne-2, yoyodyne-9 (unfinished work this item waits on)") {
		t.Fatalf("bundle does not name the unfinished work the item waits on: %s", blocked.Text)
	}
	for _, unwanted := range []string{"yoyodyne-7", "yoyodyne-8"} {
		if strings.Contains(blocked.Text, unwanted) {
			t.Fatalf("bundle names %s, which is not work this item waits on: %s", unwanted, blocked.Text)
		}
	}
}

func TestAssembleSkipsMissingImplicitMarkdownDeliverables(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	item := beads.WorkItem{
		ID:          "yoyodyne-1",
		Title:       "Create documentation",
		Status:      "open",
		Description: "Create docs/new-guide.md.",
	}
	bundle, err := Assemble(Request{RepositoryRoot: root, WorkItem: item})
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}
	if len(bundle.References) != 0 {
		t.Fatalf("references = %#v, want no missing implicit deliverables", bundle.References)
	}
	// Nor is it stated as omitted: the file is the work, and reporting it as
	// something this context could not carry would read as a document that went
	// missing rather than as one nobody has written yet.
	if strings.Contains(bundle.Text, "docs/new-guide.md (omitted)") {
		t.Fatalf("bundle stated a deliverable as omitted: %s", bundle.Text)
	}
}

// A reference the caller asked for is its own required input rather than
// something a work item accumulated, so it is still refused. Only what an item's
// own text named is stated as left out, because only that text is append-only.
func TestAssembleRejectsMissingAndEscapingReferences(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.md")
	writeFile(t, outside, "outside")
	if err := os.Symlink(outside, filepath.Join(root, "escape.md")); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}
	item := beads.WorkItem{ID: "yoyodyne-1", Title: "Test", Status: "open"}

	for _, test := range []struct {
		name      string
		reference string
		want      string
	}{
		{name: "missing", reference: "missing.md", want: "resolve reference"},
		{name: "parent", reference: "../outside.md", want: "repository-relative"},
		{name: "symlink", reference: "escape.md", want: "outside the repository"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := Assemble(Request{RepositoryRoot: root, WorkItem: item, References: []string{test.reference}, MaxBytes: 4096})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Assemble() error = %v, want %q", err, test.want)
			}
		})
	}
}

// TestAssembleStatesAnUnresolvableItemReference is the case that wedged an item:
// append-only notes quoting a reviewer's citation of a path this repository does
// not hold. The reference is stated as left out, the run's context still
// assembles, and what the item named that does resolve is still carried.
func TestAssembleStatesAnUnresolvableItemReference(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "docs", "guide.md"), "guide content\n")
	outside := filepath.Join(t.TempDir(), "outside.md")
	writeFile(t, outside, "outside content\n")
	if err := os.Symlink(outside, filepath.Join(root, "escape.md")); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}
	if err := os.Symlink(filepath.Join(root, "gone.md"), filepath.Join(root, "broken.md")); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "directory.md"), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	const maxBytes = 8 << 10

	for _, test := range []struct {
		name      string
		reference string
	}{
		{name: "outside the repository", reference: "../README.md"},
		{name: "symlink out of the repository", reference: "escape.md"},
		{name: "symlink to nothing", reference: "broken.md"},
		{name: "not a file", reference: "directory.md"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			item := beads.WorkItem{
				ID:          "yoyodyne-1",
				Title:       "Repair the documentation",
				Status:      "open",
				Description: "Repair what docs/guide.md describes.",
				Notes:       "Triage recorded that a reviewer cited " + test.reference + " here.",
			}
			bundle, err := Assemble(Request{RepositoryRoot: root, WorkItem: item, MaxBytes: maxBytes})
			if err != nil {
				t.Fatalf("Assemble() error = %v, want an assembled context", err)
			}
			for _, want := range []string{
				"## Referenced file: " + test.reference + " (omitted)",
				test.reference + " was named by this work item but does not resolve to a file in this repository",
				"## Referenced file: docs/guide.md",
				"guide content",
			} {
				if !strings.Contains(bundle.Text, want) {
					t.Fatalf("bundle omitted %q: %s", want, bundle.Text)
				}
			}
			if len(bundle.References) != 1 || bundle.References[0].Path != "docs/guide.md" {
				t.Fatalf("references = %#v, want only what resolved", bundle.References)
			}
			if len(bundle.Text) > maxBytes {
				t.Fatalf("bundle is %d bytes, exceeding the %d it was budgeted", len(bundle.Text), maxBytes)
			}
		})
	}
}

// TestAssembleExcerptsAnOversizedReference is the case that failed a run: a work
// item whose notes name a document that has outgrown the context budget. The
// document is excerpted rather than failing the bundle, and what the excerpt
// leaves out is stated where it was left out.
func TestAssembleExcerptsAnOversizedReference(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "docs", "big.md"), sectionedDocument(30, 12, 17, "zarquon telemetry"))
	item := beads.WorkItem{
		ID:          "yoyodyne-1",
		Title:       "Report zarquon telemetry",
		Status:      "open",
		Description: "The zarquon telemetry described here needs reporting.",
		Notes:       "A prior run recorded that docs/big.md covers this.",
	}
	const maxBytes = 8 << 10

	bundle, err := Assemble(Request{RepositoryRoot: root, WorkItem: item, MaxBytes: maxBytes})
	if err != nil {
		t.Fatalf("Assemble() error = %v", err)
	}
	if len(bundle.Text) > maxBytes {
		t.Fatalf("bundle is %d bytes, exceeding the %d it was budgeted", len(bundle.Text), maxBytes)
	}
	for _, want := range []string{
		"## Referenced file: docs/big.md (excerpt)",
		"The whole file is in the\nworktree",
		"## Section 17",
		"zarquon telemetry",
		"section(s) of this file are not included in this excerpt",
	} {
		if !strings.Contains(bundle.Text, want) {
			t.Fatalf("bundle omitted %q: %s", want, bundle.Text)
		}
	}
	if strings.Contains(bundle.Text, "## Section 29") {
		t.Fatalf("bundle carried a section the budget could not hold: %s", bundle.Text)
	}
	if len(bundle.References) != 1 || !bundle.References[0].Excerpted {
		t.Fatalf("references = %#v, want one reference reported as an excerpt", bundle.References)
	}
}

// TestAssembleStatesAnOversizedReferenceItCannotExcerpt covers the other half:
// the budget holds no excerpt worth reading, or the document has no sections to
// choose between. Either way the run carries on and the reader is told what is
// not there.
func TestAssembleStatesAnOversizedReferenceItCannotExcerpt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "docs", "sections.md"), sectionedDocument(20, 60, 3, "unreachable"))
	writeFile(t, filepath.Join(root, "docs", "unbroken.md"), strings.Repeat("prose that no heading ever divides. ", 3000))

	for _, test := range []struct {
		name      string
		reference string
		maxBytes  int
	}{
		{name: "no section fits", reference: "docs/sections.md", maxBytes: 2 << 10},
		{name: "no sections to choose", reference: "docs/unbroken.md", maxBytes: 16 << 10},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			item := beads.WorkItem{
				ID:          "yoyodyne-1",
				Title:       "Work on something",
				Status:      "open",
				Description: "A prior run recorded that " + test.reference + " covers this.",
			}
			bundle, err := Assemble(Request{RepositoryRoot: root, WorkItem: item, MaxBytes: test.maxBytes})
			if err != nil {
				t.Fatalf("Assemble() error = %v", err)
			}
			want := test.reference + " was referenced but exceeds the context budget; consult it in the worktree."
			if !strings.Contains(bundle.Text, want) {
				t.Fatalf("bundle did not state the omission %q: %s", want, bundle.Text)
			}
			if len(bundle.References) != 0 {
				t.Fatalf("references = %#v, want nothing carried", bundle.References)
			}
			if len(bundle.Text) > test.maxBytes {
				t.Fatalf("bundle is %d bytes, exceeding the %d it was budgeted", len(bundle.Text), test.maxBytes)
			}
		})
	}
}

// TestElisionMarkerFitsWhatIsChargedForIt guards the arithmetic the excerpt's
// bound rests on: each kept section is charged one marker before the sections
// are chosen, so a marker that outgrew its charge would let an excerpt overrun
// the budget it was assembled against.
func TestElisionMarkerFitsWhatIsChargedForIt(t *testing.T) {
	t.Parallel()

	if got := len(renderElision(999999)); got > maxElisionBytes {
		t.Fatalf("elision marker is %d bytes, exceeding the %d charged for it", got, maxElisionBytes)
	}
}

// sectionedDocument writes a Markdown document of the given number of sections,
// each padded with the given number of filler sentences, with the given phrase
// carried by exactly one of them.
func sectionedDocument(sections, filler, carrying int, phrase string) string {
	var document strings.Builder
	document.WriteString("# The document\n\nWhat this document is.\n\n")
	for index := 0; index < sections; index++ {
		fmt.Fprintf(&document, "## Section %d\n\n", index)
		if index == carrying {
			document.WriteString(phrase + " is described here.\n\n")
		}
		document.WriteString(strings.Repeat("filler prose about nothing in particular. ", filler) + "\n\n")
	}
	return document.String()
}

func TestExtractMarkdownReferences(t *testing.T) {
	t.Parallel()

	item := beads.WorkItem{
		Description: "docs/z.md and docs/a.md; see https://example.com/design.md for background",
		Design:      "docs/a.md",
		Notes:       "not-a-reference.txt and /tmp/local.md are not repository references",
	}
	got := ExtractMarkdownReferences(item)
	want := []string{"docs/a.md", "docs/z.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ExtractMarkdownReferences() = %#v, want %#v", got, want)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}
