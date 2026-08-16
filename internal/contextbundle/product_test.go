package contextbundle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"yoyodyne/internal/beads"
)

func TestAssembleProductReadsRepositoryMarkdownAndTrackerState(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "README.md", "# Yoyodyne\n\nA local harness.\n")
	writeProductFile(t, root, "docs/v1-harness-design.md", "# Design\n\nThe product manager owns intent.\n")
	writeProductFile(t, root, "docs/product/brief.md", "# Brief\n\nRun bounded work items.\n")
	// Only Markdown is product evidence, and only from the documented roots.
	writeProductFile(t, root, "docs/diagram.png", "not markdown")
	writeProductFile(t, root, "internal/notes.md", "implementation notes")

	bundle, err := AssembleProduct(ProductRequest{
		RepositoryRoot: root,
		WorkItems: []beads.WorkItem{
			{ID: "yoyodyne-ifd.4.4", Title: "Add the product-manager conversation", Status: "in_progress", Priority: 1, IssueType: "task"},
		},
	})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	got := []string{}
	for _, reference := range bundle.References {
		got = append(got, reference.Path)
	}
	want := []string{"README.md", "docs/product/brief.md", "docs/v1-harness-design.md"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("documents = %v, want %v", got, want)
	}
	for _, required := range []string{
		"A local harness.",
		"The product manager owns intent.",
		"- yoyodyne-ifd.4.4 [in_progress, p1, task] Add the product-manager conversation",
	} {
		if !strings.Contains(bundle.Text, required) {
			t.Fatalf("product context is missing %q:\n%s", required, bundle.Text)
		}
	}
	if strings.Contains(bundle.Text, "implementation notes") || strings.Contains(bundle.Text, "not markdown") {
		t.Fatalf("product context read outside the documented roots:\n%s", bundle.Text)
	}
	if bundle.Bytes != len(bundle.Text) {
		t.Fatalf("bundle bytes = %d, text = %d", bundle.Bytes, len(bundle.Text))
	}
}

func TestAssembleProductNamesWhatItCouldNotInclude(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "README.md", "# Yoyodyne\n")
	writeProductFile(t, root, "docs/huge.md", strings.Repeat("x", 4096))

	bundle, err := AssembleProduct(ProductRequest{RepositoryRoot: root, MaxBytes: 2048})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	if !strings.Contains(bundle.Text, "## Documents omitted for size") || !strings.Contains(bundle.Text, "- docs/huge.md") {
		t.Fatalf("omitted document is not reported:\n%s", bundle.Text)
	}
	if !strings.Contains(bundle.Text, "# Yoyodyne") {
		t.Fatalf("a document that fits was dropped:\n%s", bundle.Text)
	}

	// A budget that cannot even hold the tracker state is a caller error rather
	// than a silently empty conversation.
	if _, err := AssembleProduct(ProductRequest{RepositoryRoot: root, MaxBytes: 32}); err == nil {
		t.Fatal("AssembleProduct() tiny budget error = nil")
	}
}

func TestAssembleProductStatesWhenTrackerStateIsMissing(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "README.md", "# Yoyodyne\n")

	unavailable, err := AssembleProduct(ProductRequest{RepositoryRoot: root, WorkItemsUnavailable: "bd list failed: no database"})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	if !strings.Contains(unavailable.Text, "Beads state is unavailable: bd list failed: no database") {
		t.Fatalf("missing tracker state is not reported:\n%s", unavailable.Text)
	}
	if !strings.Contains(unavailable.Text, "Do not assume there is no work in flight") {
		t.Fatalf("missing tracker state reads as an empty tracker:\n%s", unavailable.Text)
	}

	empty, err := AssembleProduct(ProductRequest{RepositoryRoot: root})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	if !strings.Contains(empty.Text, "Beads reported no matching work items.") {
		t.Fatalf("an empty tracker is not reported:\n%s", empty.Text)
	}
}

func TestAssembleProductRefusesDocumentsOutsideTheRepository(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "README.md", "# Yoyodyne\n")
	for _, document := range []string{"../outside.md", "/etc/hosts.md", "docs/notes.txt"} {
		if _, err := AssembleProduct(ProductRequest{RepositoryRoot: root, Documents: []string{document}}); err == nil {
			t.Fatalf("AssembleProduct(%q) error = nil", document)
		}
	}
}

func writeProductFile(t *testing.T, root, relative, content string) {
	t.Helper()

	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}
