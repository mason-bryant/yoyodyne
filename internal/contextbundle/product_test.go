package contextbundle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"yoyodyne/internal/beads"
)

// wellFormed is a specification that follows the contract: an introduction
// saying what the thing is and why it exists, then the goals that serve it.
const wellFormed = `# Bounded runs

Yoyodyne runs one bounded work item at a time, because a change nobody can
review is not a change anybody can trust.

## Goals

- A run integrates only behind checks and an independent review.
`

func TestAssembleProductReadsSpecificationsAndTrackerState(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "docs/product/runs.md", wellFormed)
	// The specifications directory is walked to any depth, so a specification
	// nested below it is evidence exactly as one sitting directly under it is.
	writeProductFile(t, root, "docs/product/goals/autonomy.md", "# Autonomy\n\nThe operator states intent and approves it.\n\n## Goals\n\n- Routine work needs no per-change gate.\n")
	// Only Markdown inside the configured directory is product evidence.
	writeProductFile(t, root, "docs/product/diagram.png", "not markdown")
	writeProductFile(t, root, "README.md", "# Yoyodyne\n\nA stale sentence about the product.\n")
	writeProductFile(t, root, "docs/v1-harness-design.md", "# Design\n\nArchitecture, not intent.\n")
	writeProductFile(t, root, "internal/notes.md", "implementation notes")

	bundle, err := AssembleProduct(ProductRequest{
		RepositoryRoot:          root,
		SpecificationsDirectory: "docs/product",
		WorkItems: []beads.WorkItem{
			{ID: "yoyodyne-ifd.20", Title: "Work from a configurable specifications directory", Status: "in_progress", Priority: 1, IssueType: "task"},
		},
	})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	got := []string{}
	for _, reference := range bundle.References {
		got = append(got, reference.Path)
	}
	want := []string{"docs/product/goals/autonomy.md", "docs/product/runs.md"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("specifications = %v, want %v", got, want)
	}
	for _, required := range []string{
		"## Specification: docs/product/runs.md",
		"a change nobody can\nreview is not a change anybody can trust",
		"Routine work needs no per-change gate.",
		"- yoyodyne-ifd.20 [in_progress, p1, task] Work from a configurable specifications directory",
	} {
		if !strings.Contains(bundle.Text, required) {
			t.Fatalf("product context is missing %q:\n%s", required, bundle.Text)
		}
	}
	// The narrowing is the point of the directory: documentation outside it
	// describes how the product is built, not what it is for, and is no longer
	// evidence the product manager can report as current product fact.
	for _, excluded := range []string{"A stale sentence about the product.", "Architecture, not intent.", "implementation notes", "not markdown"} {
		if strings.Contains(bundle.Text, excluded) {
			t.Fatalf("product context read outside the specifications directory (%q):\n%s", excluded, bundle.Text)
		}
	}
	if len(bundle.SpecificationProblems) != 0 {
		t.Fatalf("well-formed specifications reported problems: %v", bundle.SpecificationProblems)
	}
	if bundle.Bytes != len(bundle.Text) {
		t.Fatalf("bundle bytes = %d, text = %d", bundle.Bytes, len(bundle.Text))
	}
}

// The product manager sets the order work is pulled in, so the state it reasons
// over is listed in that order. A queue shown in some other order would have it
// reasoning about a sequence nobody works in.
func TestAssembleProductListsWorkInBacklogOrder(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "docs/product/runs.md", wellFormed)

	bundle, err := AssembleProduct(ProductRequest{
		RepositoryRoot:          root,
		SpecificationsDirectory: "docs/product",
		WorkItems: []beads.WorkItem{
			{ID: "yoyodyne-ifd.26", Title: "See and stop what is pulled", Status: "open", Priority: 3, IssueType: "task"},
			{ID: "yoyodyne-ifd.3", Title: "The scheduler that runs it", Status: "open", Priority: 0, IssueType: "task"},
			{ID: "yoyodyne-ifd.4", Title: "The development manager that pulls", Status: "open", Priority: 1, IssueType: "task"},
		},
	})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}

	positions := make([]int, 0, 3)
	for _, id := range []string{"yoyodyne-ifd.3", "yoyodyne-ifd.4", "yoyodyne-ifd.26"} {
		at := strings.Index(bundle.Text, "- "+id+" ")
		if at < 0 {
			t.Fatalf("product context is missing %s:\n%s", id, bundle.Text)
		}
		positions = append(positions, at)
	}
	if positions[0] > positions[1] || positions[1] > positions[2] {
		t.Fatalf("work items are not in backlog order (positions %v):\n%s", positions, bundle.Text)
	}
	// What the order means is stated, including the part of it nobody decided.
	for _, required := range []string{
		"These are in backlog order: highest priority first",
		"nothing has decided which of those comes first",
	} {
		if !strings.Contains(bundle.Text, required) {
			t.Fatalf("product context does not say %q:\n%s", required, bundle.Text)
		}
	}
}

func TestAssembleProductReadsTheConfiguredDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "specs/runs.md", wellFormed)
	writeProductFile(t, root, "docs/product/other.md", "# Other\n\nElsewhere.\n\n## Goals\n\n- Not this one.\n")

	bundle, err := AssembleProduct(ProductRequest{RepositoryRoot: root, SpecificationsDirectory: "specs"})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	if len(bundle.References) != 1 || bundle.References[0].Path != "specs/runs.md" {
		t.Fatalf("specifications = %v, want specs/runs.md alone", bundle.References)
	}
	if !strings.Contains(bundle.Text, "specifications under specs") {
		t.Fatalf("the configured directory is not named in the context:\n%s", bundle.Text)
	}
}

func TestAssembleProductSurfacesSpecificationsThatIgnoreTheStructure(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "docs/product/no-goals.md", "# Brief\n\nWhat this is and why it exists.\n")
	writeProductFile(t, root, "docs/product/no-introduction.md", "# Goals\n\n- An outcome with nothing saying what it serves.\n")
	writeProductFile(t, root, "docs/product/empty-goals.md", "# Brief\n\nWhat this is.\n\n## Goals\n\n## Something else\n\nProse.\n")
	writeProductFile(t, root, "docs/product/good.md", wellFormed)

	bundle, err := AssembleProduct(ProductRequest{RepositoryRoot: root, SpecificationsDirectory: "docs/product"})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	if len(bundle.References) != 4 {
		t.Fatalf("references = %d, want 4: a specification that ignores the structure is still read", len(bundle.References))
	}
	// The intent survives: refusing to load these would lose what somebody wrote.
	for _, required := range []string{
		"An outcome with nothing saying what it serves.",
		"## Specifications that do not follow the required structure",
		"docs/product/no-goals.md: it states no goals",
		"docs/product/no-introduction.md: it opens with its goals",
		"docs/product/empty-goals.md: its `Goals` section is empty",
	} {
		if !strings.Contains(bundle.Text, required) {
			t.Fatalf("product context is missing %q:\n%s", required, bundle.Text)
		}
	}
	if strings.Contains(bundle.Text, "docs/product/good.md:") {
		t.Fatalf("a well-formed specification was reported as a problem:\n%s", bundle.Text)
	}

	reported := map[string]string{}
	for _, problem := range bundle.SpecificationProblems {
		reported[problem.Path] = problem.Reason
	}
	if len(reported) != 3 {
		t.Fatalf("reported problems = %v, want three", reported)
	}
	for _, path := range []string{"docs/product/no-goals.md", "docs/product/no-introduction.md", "docs/product/empty-goals.md"} {
		if reported[path] == "" {
			t.Fatalf("%s is not reported to the caller: %v", path, reported)
		}
	}
}

func TestSpecificationStructureProblem(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		content string
		problem bool
	}{
		{name: "introduction then goals", content: wellFormed},
		{name: "no title heading", content: "What this is.\n\n## Goals\n\n- An outcome.\n"},
		{name: "singular goal heading", content: "# Brief\n\nWhat this is.\n\n## Goal\n\n- An outcome.\n"},
		{name: "goals with subsections", content: "# Brief\n\nWhat this is.\n\n## Goals\n\n### First\n\nAn outcome.\n"},
		{name: "goals in a lower-cased heading", content: "# Brief\n\nWhat this is.\n\n### goals\n\n- An outcome.\n"},
		{name: "empty file", content: "", problem: true},
		{name: "blank file", content: "\n\n   \n", problem: true},
		{name: "no goals at all", content: "# Brief\n\nWhat this is.\n", problem: true},
		{name: "goals before any introduction", content: "# Goals\n\n- An outcome.\n", problem: true},
		// A fenced block is not an introductory paragraph saying what the thing
		// is, so a specification whose only prose is a code sample still opens
		// with its goals.
		{name: "fenced block is not an introduction", content: "# Brief\n\n```\nyoyo run\n```\n\n## Goals\n\n- An outcome.\n", problem: true},
		// A heading inside a fence is not the goals heading either.
		{name: "goals heading inside a fence", content: "# Brief\n\nWhat this is.\n\n```\n## Goals\n```\n", problem: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			reason := specificationStructureProblem(testCase.content)
			if testCase.problem && reason == "" {
				t.Fatalf("specificationStructureProblem() = %q, want a problem", reason)
			}
			if !testCase.problem && reason != "" {
				t.Fatalf("specificationStructureProblem() = %q, want no problem", reason)
			}
		})
	}
}

func TestAssembleProductStatesWhenThereAreNoSpecifications(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "README.md", "# Yoyodyne\n")

	// A directory that does not exist yet is a project that has written nothing
	// down, not a broken configuration.
	missing, err := AssembleProduct(ProductRequest{RepositoryRoot: root, SpecificationsDirectory: "docs/product"})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	if !strings.Contains(missing.Text, "No specification was found under docs/product.") {
		t.Fatalf("an empty specifications directory is not reported:\n%s", missing.Text)
	}
	if len(missing.References) != 0 {
		t.Fatalf("references = %v, want none", missing.References)
	}

	writeProductFile(t, root, "docs/product/notes.txt", "not markdown")
	empty, err := AssembleProduct(ProductRequest{RepositoryRoot: root, SpecificationsDirectory: "docs/product"})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	if !strings.Contains(empty.Text, "No specification was found under docs/product.") {
		t.Fatalf("a directory with no Markdown is not reported:\n%s", empty.Text)
	}
}

func TestAssembleProductNamesWhatItCouldNotInclude(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "docs/product/brief.md", wellFormed)
	writeProductFile(t, root, "docs/product/huge.md", strings.Repeat("x", 4096))

	bundle, err := AssembleProduct(ProductRequest{RepositoryRoot: root, SpecificationsDirectory: "docs/product", MaxBytes: 3072})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	if !strings.Contains(bundle.Text, "## Specifications omitted for size") || !strings.Contains(bundle.Text, "- docs/product/huge.md") {
		t.Fatalf("omitted specification is not reported:\n%s", bundle.Text)
	}
	if !strings.Contains(bundle.Text, "# Bounded runs") {
		t.Fatalf("a specification that fits was dropped:\n%s", bundle.Text)
	}

	// A budget that cannot even hold the tracker state is a caller error rather
	// than a silently empty conversation.
	if _, err := AssembleProduct(ProductRequest{RepositoryRoot: root, SpecificationsDirectory: "docs/product", MaxBytes: 32}); err == nil {
		t.Fatal("AssembleProduct() tiny budget error = nil")
	}
}

func TestAssembleProductStatesWhenTrackerStateIsMissing(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "docs/product/brief.md", wellFormed)

	unavailable, err := AssembleProduct(ProductRequest{
		RepositoryRoot:          root,
		SpecificationsDirectory: "docs/product",
		WorkItemsUnavailable:    "bd list failed: no database",
	})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	if !strings.Contains(unavailable.Text, "Beads state is unavailable: bd list failed: no database") {
		t.Fatalf("missing tracker state is not reported:\n%s", unavailable.Text)
	}
	if !strings.Contains(unavailable.Text, "Do not assume there is no work in flight") {
		t.Fatalf("missing tracker state reads as an empty tracker:\n%s", unavailable.Text)
	}

	empty, err := AssembleProduct(ProductRequest{RepositoryRoot: root, SpecificationsDirectory: "docs/product"})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	if !strings.Contains(empty.Text, "Beads reported no matching work items.") {
		t.Fatalf("an empty tracker is not reported:\n%s", empty.Text)
	}
}

func TestAssembleProductRefusesSpecificationsOutsideTheRepository(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "docs/product/brief.md", wellFormed)
	for _, directory := range []string{"", "   ", "..", "../elsewhere", "/etc"} {
		if _, err := AssembleProduct(ProductRequest{RepositoryRoot: root, SpecificationsDirectory: directory}); err == nil {
			t.Fatalf("AssembleProduct(%q) error = nil", directory)
		}
	}

	// A path inside the repository that is not a directory is a configuration
	// mistake rather than a conversation with no intent in it.
	if _, err := AssembleProduct(ProductRequest{RepositoryRoot: root, SpecificationsDirectory: "docs/product/brief.md"}); err == nil {
		t.Fatal("AssembleProduct() on a file error = nil")
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
