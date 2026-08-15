package contextbundle

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"yoyodyne/internal/beads"
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
}

func TestAssembleRejectsMissingEscapingAndOversizedReferences(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.md")
	writeFile(t, outside, "outside")
	if err := os.Symlink(outside, filepath.Join(root, "escape.md")); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}
	writeFile(t, filepath.Join(root, "large.md"), strings.Repeat("x", 1024))
	item := beads.WorkItem{ID: "yoyodyne-1", Title: "Test", Status: "open"}

	for _, test := range []struct {
		name      string
		reference string
		maxBytes  int
		want      string
	}{
		{name: "missing", reference: "missing.md", maxBytes: 4096, want: "resolve reference"},
		{name: "parent", reference: "../outside.md", maxBytes: 4096, want: "repository-relative"},
		{name: "symlink", reference: "escape.md", maxBytes: 4096, want: "outside the repository"},
		{name: "oversized", reference: "large.md", maxBytes: 200, want: "exceeds remaining context"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := Assemble(Request{RepositoryRoot: root, WorkItem: item, References: []string{test.reference}, MaxBytes: test.maxBytes})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Assemble() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestExtractMarkdownReferences(t *testing.T) {
	t.Parallel()

	item := beads.WorkItem{
		Description: "docs/z.md and docs/a.md",
		Design:      "docs/a.md",
		Notes:       "not-a-reference.txt",
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
