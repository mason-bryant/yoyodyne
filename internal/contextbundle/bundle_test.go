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
}

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
