package contextbundle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
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
	writeProductFile(t, root, "README.md", "# Yoyodyne\n\nWhat the product ships, described to whoever uses it.\n")
	writeProductFile(t, root, "docs/designs/v1-harness-design.md", "# Design\n\nArchitecture, not intent.\n")
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
	// What is outside the specifications directory and is still not evidence: the
	// design document and the source say how the product is built rather than
	// what it is for or what it ships, and nothing that is not Markdown is read
	// at all.
	for _, excluded := range []string{"Architecture, not intent.", "implementation notes", "not markdown"} {
		if strings.Contains(bundle.Text, excluded) {
			t.Fatalf("product context read outside what it is given (%q):\n%s", excluded, bundle.Text)
		}
	}
	// The specifications are what the references are, whatever else the context
	// carries: a repository with a README and no specification has no recorded
	// intent, and a caller counting references is asking about intent.
	if len(bundle.References) != 2 {
		t.Fatalf("references = %d, want the 2 specifications alone", len(bundle.References))
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
		// Artifact identity metadata is not part of the document a person reads,
		// so the structure contract is checked over what follows it.
		{name: "identity frontmatter then the document", content: "---\nid: brief\nkind: brief\n---\n\n# Brief\n\nWhat this is.\n\n## Goals\n\n- An outcome.\n"},
		{name: "identity frontmatter is not an introduction", content: "---\nid: brief\nkind: brief\n---\n\n# Goals\n\n- An outcome.\n", problem: true},
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

// A repository that has written its intent down is told so plainly, so a
// product manager reading a substantive brief and goals has no reason to open by
// asking for either.
func TestAssembleProductSaysWhatIntentIsRecorded(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "docs/product/brief.md", "---\nid: brief\nkind: brief\n---\n\n# Product brief\n\n"+strings.Repeat("What this is, who it is for, and what finished means. ", 12)+"\n\n## Goals\n\n- An outcome.\n")
	writeProductFile(t, root, "docs/product/goals/v1-goals.md", "---\nid: v1-goals\nkind: goals\n---\n\n# V1 goals\n\n"+strings.Repeat("The outcomes the first version reaches. ", 12)+"\n\n## Goals\n\n- An outcome.\n")

	bundle, err := AssembleProduct(ProductRequest{RepositoryRoot: root, SpecificationsDirectory: "docs/product"})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	for _, required := range []string{
		"## Recorded product intent",
		"- Brief: docs/product/brief.md, about ",
		"- Goals: docs/product/goals/v1-goals.md, about ",
	} {
		if !strings.Contains(bundle.Text, required) {
			t.Fatalf("recorded intent is missing %q:\n%s", required, bundle.Text)
		}
	}
	// Neither document is thin, so nothing here reads as a repository waiting to
	// be asked what it is for.
	for _, absent := range []string{"none recorded", "little more than a placeholder"} {
		if strings.Contains(bundle.Text, absent) {
			t.Fatalf("recorded intent says %q about substantive documents:\n%s", absent, bundle.Text)
		}
	}
}

// The blind repository: nothing written down, and the emptiness said in so many
// words rather than left to be inferred from a context with no brief in it.
func TestAssembleProductSaysWhenIntentIsNotRecorded(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	bundle, err := AssembleProduct(ProductRequest{RepositoryRoot: root, SpecificationsDirectory: "docs/product"})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	for _, required := range []string{
		"- Brief: none recorded.",
		"- Goals: none recorded.",
		"the operator's to state",
	} {
		if !strings.Contains(bundle.Text, required) {
			t.Fatalf("an unwritten product is not reported: missing %q:\n%s", required, bundle.Text)
		}
	}

	// A specification that is neither of the two documents leaves both missing:
	// what the product will not do is not what it is for.
	writeProductFile(t, root, "docs/product/goals/v1-non-goals.md", "---\nid: v1-non-goals\nkind: non-goals\n---\n\n# V1 non-goals\n\nWhat the first version does not do.\n\n## Goals\n\n- Nothing here.\n")
	neither, err := AssembleProduct(ProductRequest{RepositoryRoot: root, SpecificationsDirectory: "docs/product"})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	if !strings.Contains(neither.Text, "- Goals: none recorded.") {
		t.Fatalf("a non-goals document is counted as the goals:\n%s", neither.Text)
	}
}

// Goals stated inside the brief are goals. The structure contract already asks
// every specification to state them under a `Goals` heading, so a project that
// wrote them there has written them, and asking for goals that are on disk would
// be a question with its answer already in the context.
func TestAssembleProductCountsGoalsStatedInsideTheBrief(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "docs/product/brief.md", "# Product brief\n\n"+strings.Repeat("What this is and who it is for. ", 10)+"\n\n## Goals\n\n"+strings.Repeat("- An outcome the product reaches. ", 10)+"\n")

	stated, err := AssembleProduct(ProductRequest{RepositoryRoot: root, SpecificationsDirectory: "docs/product"})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	if !strings.Contains(stated.Text, "- Goals: the `Goals` section of docs/product/brief.md, about ") {
		t.Fatalf("goals stated inside the brief are not counted:\n%s", stated.Text)
	}

	// A goals document is where goals live once there is one, and the brief's own
	// section is not named beside it: that would report one intent twice.
	writeProductFile(t, root, "docs/product/goals/v1-goals.md", "# V1 goals\n\n"+strings.Repeat("The outcomes the first version reaches. ", 10)+"\n\n## Goals\n\n- An outcome.\n")
	both, err := AssembleProduct(ProductRequest{RepositoryRoot: root, SpecificationsDirectory: "docs/product"})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	if !strings.Contains(both.Text, "- Goals: docs/product/goals/v1-goals.md, about ") {
		t.Fatalf("the goals document is not what the goals are read from:\n%s", both.Text)
	}
	if strings.Contains(both.Text, "the `Goals` section of") {
		t.Fatalf("the brief's own section is named beside a goals document:\n%s", both.Text)
	}

	// A heading with nothing under it states no goals, which is the case the
	// structure contract reports as an empty goals section.
	empty := t.TempDir()
	writeProductFile(t, empty, "docs/product/brief.md", "# Product brief\n\nWhat this is and who it is for.\n\n## Goals\n\n## Something else\n\nProse.\n")
	heading, err := AssembleProduct(ProductRequest{RepositoryRoot: empty, SpecificationsDirectory: "docs/product"})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	if !strings.Contains(heading.Text, "- Goals: none recorded.") {
		t.Fatalf("an empty goals heading is counted as goals:\n%s", heading.Text)
	}
}

// A document that exists and says almost nothing is reported as what it is,
// with the count beside it: whether a short document is enough is a judgment,
// and one made from a verdict alone is not one.
func TestAssembleProductSaysWhenIntentIsAPlaceholder(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "docs/product/brief.md", "# Product brief\n\nTODO.\n\n## Goals\n\n- TODO.\n")

	bundle, err := AssembleProduct(ProductRequest{RepositoryRoot: root, SpecificationsDirectory: "docs/product"})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	if !strings.Contains(bundle.Text, "- Brief: docs/product/brief.md, about 3 words and little more than a placeholder.") {
		t.Fatalf("a placeholder brief is not reported as one:\n%s", bundle.Text)
	}
}

// The recorded-intent section is charged to the context as a fixed allowance
// before any specification is read, not as what it goes on to render, so what it
// renders has to fit inside that allowance however the repository files its
// intent. A section that outgrew the reserve would push the assembled context
// past the byte budget it was assembled to, with nothing to catch it.
//
// This renders the section at its largest, and every part of "largest" is what
// the bounds actually permit rather than a number chosen to pass: each entry is
// a placeholder, which is the longest form an entry takes, and states its goals
// inside the brief, which is the longest way one is named; every path is longer
// than the fold allows; and the tail counts documents that were not named.
func TestRecordedIntentFitsWhatIsReservedForIt(t *testing.T) {
	t.Parallel()

	// The allowance is sized against the fold, so what the fold actually returns
	// is pinned here rather than assumed.
	longPath := strings.Repeat("nested/", 40) + "v1-goals.md"
	folded := singleLine(longPath, maxIntentPathBytes)
	if len(folded) > maxIntentPathBytes+len("...") {
		t.Fatalf("singleLine folds %d bytes to %d, want at most %d", len(longPath), len(folded), maxIntentPathBytes+len("..."))
	}

	// Below stubProseWords, so every entry carries the placeholder clause, and at
	// the widest count that stays below it.
	widest := intentDocument{path: longPath, words: stubProseWords - 1, inline: true}
	// More documents than are named, so the tail that says how many were left out
	// is rendered at more digits than a repository could reach: every included
	// specification costs the context far more than one byte, so the default
	// budget cannot hold this many of them.
	total := maxRecordedIntentDocuments + defaultMaxProductBytes
	worst := recordedIntent{brief: make([]intentDocument, total), goals: make([]intentDocument, total)}
	for index := range maxRecordedIntentDocuments {
		worst.brief[index] = widest
		worst.goals[index] = widest
	}

	// The configured directory is added to the allowance rather than counted
	// against it, so it is left out of what is measured here.
	rendered := len(renderRecordedIntent("", worst))
	if rendered+recordedIntentHeadroom > maxRecordedIntentBytes {
		t.Fatalf("recorded intent renders %d bytes with %d bytes of headroom required, allowance is %d",
			rendered, recordedIntentHeadroom, maxRecordedIntentBytes)
	}
}

// What a placeholder entry costs is the largest an entry gets, which is what the
// worst case above is built from. A word count large enough to need more digits
// than a placeholder's does not overtake it, because the clause a placeholder
// adds is worth far more than those digits.
func TestPlaceholderEntriesAreTheLongestOnesRendered(t *testing.T) {
	t.Parallel()

	path := "docs/product/goals/v1-goals.md"
	placeholder := renderIntentDocuments([]intentDocument{{path: path, words: stubProseWords - 1, inline: true}})
	counted := renderIntentDocuments([]intentDocument{{path: path, words: defaultMaxProductBytes, inline: true}})
	if len(placeholder) <= len(counted) {
		t.Fatalf("a placeholder entry renders %d bytes and a counted one %d; the worst case is built from the wrong one",
			len(placeholder), len(counted))
	}
}

func TestIntentKind(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		path    string
		content string
		kind    string
	}{
		{name: "frontmatter brief", path: "docs/product/anything.md", content: "---\nid: anything\nkind: brief\n---\n\n# Brief\n", kind: kindBrief},
		{name: "frontmatter goals", path: "docs/product/anything.md", content: "---\nid: anything\nkind: goals\n---\n\n# Goals\n", kind: kindGoals},
		// A document's own word on what it is beats where it is filed.
		{name: "frontmatter non-goals in a goals directory", path: "docs/product/goals/v1-non-goals.md", content: "---\nid: v1-non-goals\nkind: non-goals\n---\n\n# Non-goals\n", kind: ""},
		{name: "frontmatter design", path: "docs/product/brief.md", content: "---\nid: brief\nkind: design\n---\n\n# A design\n", kind: ""},
		// A key inside the metadata is not the document's own kind.
		{name: "nested kind key", path: "docs/product/notes.md", content: "---\nid: notes\nrevisions:\n    - kind: brief\n---\n\n# Notes\n", kind: ""},
		// Nothing has been asked of the operator about identity yet, so a document
		// with no frontmatter is read by what it is called.
		{name: "named brief", path: "docs/product/brief.md", content: "# Brief\n", kind: kindBrief},
		{name: "named goals", path: "docs/product/v1-goals.md", content: "# Goals\n", kind: kindGoals},
		{name: "filed under goals", path: "docs/product/goals/v1.md", content: "# V1\n", kind: kindGoals},
		{name: "named non-goals", path: "docs/product/goals/v1-non-goals.md", content: "# Non-goals\n", kind: ""},
		{name: "a directory index states no intent", path: "docs/product/goals/README.md", content: "# Goals directory\n", kind: ""},
		{name: "an ordinary specification", path: "docs/product/runs.md", content: wellFormed, kind: ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if kind := intentKind(testCase.path, testCase.content); kind != testCase.kind {
				t.Fatalf("intentKind(%q) = %q, want %q", testCase.path, kind, testCase.kind)
			}
		})
	}
}

func TestProseWords(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		content string
		words   int
	}{
		{name: "prose", content: "One two three four.\n", words: 4},
		// Identity metadata, a title, and a heading for every unanswered question
		// are not a written document.
		{name: "frontmatter and headings only", content: "---\nid: brief\nkind: brief\n---\n\n# Product brief\n\n## Goals\n", words: 0},
		{name: "a fenced block is not prose", content: "# Brief\n\n```\nyoyo run --now\n```\n\nTwo words.\n", words: 2},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if words := proseWords(testCase.content); words != testCase.words {
				t.Fatalf("proseWords() = %d, want %d", words, testCase.words)
			}
		})
	}
}

// What the product ships is carried so the role deciding what to build next can
// name the surfaces that already exist. It is labeled as description, because
// the same documentation read as authority is what let a stale README sentence
// reach the operator as current product fact on 2026-08-16.
func TestAssembleProductDescribesWhatIsShipped(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "docs/product/brief.md", wellFormed)
	writeProductFile(t, root, "README.md", "# Yoyodyne\n\n`bin/yoyo-status` follows a run as it happens.\n")
	writeProductFile(t, root, "docs/configuration.md", "# Configuration\n\nA project owns its configuration outright.\n")

	bundle, err := AssembleProduct(ProductRequest{
		RepositoryRoot:          root,
		SpecificationsDirectory: "docs/product",
		CommandHelp:             "Usage: yoyo <command>\n\n  cost  price work items from the runs made for them\n",
	})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	for _, required := range []string{
		"## What the product ships today",
		"### Shipped documentation: README.md",
		"`bin/yoyo-status` follows a run as it happens.",
		"### Shipped documentation: docs/configuration.md",
		"A project owns its configuration outright.",
		"### Command help",
		"cost  price work items from the runs made for them",
	} {
		if !strings.Contains(bundle.Text, required) {
			t.Fatalf("the shipped surface is missing %q:\n%s", required, bundle.Text)
		}
	}
	// The label is as much the point as the content: what it is, what it is not,
	// and what to do when it disagrees with the specifications.
	for _, required := range []string{
		"It describes the implementation as built.",
		"It is not authority about intent",
		"the specifications above remain\nthe only statement of that",
		"report the conflict",
	} {
		if !strings.Contains(bundle.Text, required) {
			t.Fatalf("the shipped surface is not labeled as description (%q):\n%s", required, bundle.Text)
		}
	}
	// Intent is stated before what was built from it, so the section that decides
	// nothing cannot be read first and become the frame for the one that does.
	if strings.Index(bundle.Text, "## Specification: docs/product/brief.md") > strings.Index(bundle.Text, "## What the product ships today") {
		t.Fatalf("the shipped surface is rendered before the specifications:\n%s", bundle.Text)
	}
}

// A project ships whatever documentation it wrote. A repository holding none of
// what is looked for is told so, rather than getting a section that quietly
// carries less than it says it does.
func TestAssembleProductStatesWhenNothingDescribesWhatIsShipped(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "docs/product/brief.md", wellFormed)

	bundle, err := AssembleProduct(ProductRequest{RepositoryRoot: root, SpecificationsDirectory: "docs/product"})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	if !strings.Contains(bundle.Text, "This repository holds none of the operator-facing documentation looked for here") {
		t.Fatalf("missing documentation is not stated:\n%s", bundle.Text)
	}
	// One document present and the other missing is neither of those cases: what
	// is there is carried, and nothing claims the rest was found.
	writeProductFile(t, root, "README.md", "# Yoyodyne\n\nWhat it does.\n")
	partial, err := AssembleProduct(ProductRequest{RepositoryRoot: root, SpecificationsDirectory: "docs/product"})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	if !strings.Contains(partial.Text, "### Shipped documentation: README.md") {
		t.Fatalf("the documentation that is there was not read:\n%s", partial.Text)
	}
	if strings.Contains(partial.Text, "docs/configuration.md") {
		t.Fatalf("documentation that is not there is named as read:\n%s", partial.Text)
	}
}

// Intent wins the budget over description, by construction rather than by the
// order the sections happen to be written in: the specifications are read first
// and the documentation takes what is left, so a repository too large for both
// keeps the half that is authoritative.
func TestAssembleProductSpendsTheBudgetOnIntentFirst(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "docs/product/brief.md", wellFormed)
	writeProductFile(t, root, "README.md", "# Yoyodyne\n\n"+strings.Repeat("described ", 512))

	bundle, err := AssembleProduct(ProductRequest{RepositoryRoot: root, SpecificationsDirectory: "docs/product", MaxBytes: 6144})
	if err != nil {
		t.Fatalf("AssembleProduct() error = %v", err)
	}
	if !strings.Contains(bundle.Text, "# Bounded runs") {
		t.Fatalf("a specification was dropped for documentation:\n%s", bundle.Text)
	}
	if !strings.Contains(bundle.Text, "This documentation did not fit and is not included above:\n\n- README.md") {
		t.Fatalf("documentation that did not fit is not named:\n%s", bundle.Text)
	}
	if bundle.Bytes > 6144 {
		t.Fatalf("bundle is %d bytes, over the %d it was assembled to", bundle.Bytes, 6144)
	}
}

// The note is written after the budget has been spent, so what it can cost is
// charged before it. Every path it can name is one of a fixed set, so the
// reserve is exact rather than an allowance -- but only while it stays that way.
func TestShippedDocumentationNoteFitsWhatIsReservedForIt(t *testing.T) {
	t.Parallel()

	reserved := longestShippedDocumentationNote()
	for _, note := range []string{
		noShippedDocumentation,
		renderShippedDocumentationNote("", shippedDocumentation),
		renderShippedDocumentationNote("### Shipped documentation: README.md", shippedDocumentation[1:]),
		renderShippedDocumentationNote("### Shipped documentation: README.md", nil),
	} {
		if len(note) > reserved {
			t.Fatalf("a note renders %d bytes against a reserve of %d:\n%s", len(note), reserved, note)
		}
	}
}

// Help is compiled into the product rather than read from it, so a caller that
// supplies far too much of it is a mistake in the caller. It is cut rather than
// carried, and cut at a line, so what survives is still help.
func TestCommandHelpIsBounded(t *testing.T) {
	t.Parallel()

	help := strings.Repeat("  run   run one Beads work item in an isolated worktree\n", 4096)
	bounded := boundedCommandHelp(help)
	if len(bounded) > maxCommandHelpBytes+len("\n[the rest of the command help is not included here]") {
		t.Fatalf("bounded help is %d bytes, over the %d bound", len(bounded), maxCommandHelpBytes)
	}
	if !strings.Contains(bounded, "[the rest of the command help is not included here]") {
		t.Fatal("help was cut without saying so")
	}
	if strings.HasSuffix(strings.TrimSuffix(bounded, "\n[the rest of the command help is not included here]"), "worktre") {
		t.Fatal("help was cut mid-line")
	}
	if unbounded := "Usage: yoyo <command>\n"; boundedCommandHelp(unbounded) != strings.TrimSpace(unbounded) {
		t.Fatalf("help inside the bound was changed: %q", boundedCommandHelp(unbounded))
	}
}

func TestAssembleProductNamesWhatItCouldNotInclude(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProductFile(t, root, "docs/product/brief.md", wellFormed)
	writeProductFile(t, root, "docs/product/huge.md", strings.Repeat("x", 4096))

	// The budget has to clear what is reserved before any specification is read —
	// the header, the tracker state, and the recorded-intent allowance — and leave
	// room for one specification but not the other.
	bundle, err := AssembleProduct(ProductRequest{RepositoryRoot: root, SpecificationsDirectory: "docs/product", MaxBytes: 5120})
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
