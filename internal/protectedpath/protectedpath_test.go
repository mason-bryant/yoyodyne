package protectedpath

import (
	"slices"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
)

// defaultSet is the set a project that followed the recommended layout gets,
// which is the one nearly every refusal is decided against.
func defaultSet() Set {
	return Protect(config.Config{Product: config.Product{
		Specifications: config.DefaultSpecifications,
		Designs:        config.DefaultDesigns,
		Decisions:      config.DefaultDecisions,
		Invariants:     config.DefaultInvariants,
	}})
}

func TestProtectCoversTheConfigurationAndEveryArtifactHome(t *testing.T) {
	t.Parallel()

	want := []string{".yoyodyne", "docs/decisions", "docs/decisions/invariants", "docs/designs", "docs/product"}
	if got := defaultSet().Directories(); !slices.Equal(got, want) {
		t.Fatalf("Directories() = %v, want %v", got, want)
	}
	// A project that keeps its designs somewhere else has not made them a
	// developer's to rewrite, so the set follows the configuration rather than
	// the recommended layout.
	moved := Protect(config.Config{Product: config.Product{
		Specifications: "intent",
		Designs:        "architecture/designs",
		Decisions:      "architecture/decisions",
		Invariants:     "architecture/invariants",
	}})
	if refused := moved.Refused([]string{"architecture/designs/v1.md"}, nil); len(refused) != 1 {
		t.Fatalf("Refused() on a moved designs home = %v, want the design refused", refused)
	}
	if refused := moved.Refused([]string{"docs/designs/v1.md"}, nil); len(refused) != 0 {
		t.Fatalf("Refused() = %v, want nothing refused where this project keeps no designs", refused)
	}
}

func TestRefusedNamesEveryUngrantedProtectedPathAndNothingElse(t *testing.T) {
	t.Parallel()

	changed := []string{
		"internal/orchestrator/pipeline.go",
		"docs/configuration.md",
		"docs/products/notes.md",
		"docs/product/brief.md",
		".yoyodyne/config.yaml",
		"docs/decisions/invariants/one-promotion-per-target-branch.md",
	}
	want := []string{".yoyodyne/config.yaml", "docs/decisions/invariants/one-promotion-per-target-branch.md", "docs/product/brief.md"}
	if got := defaultSet().Refused(changed, nil); !slices.Equal(got, want) {
		t.Fatalf("Refused() = %v, want %v", got, want)
	}
	// The separator is required, so a sibling directory whose name starts with a
	// protected one is not inside it.
	if got := defaultSet().Refused([]string{"docs/products/notes.md"}, nil); len(got) != 0 {
		t.Fatalf("Refused() = %v, want a sibling directory left alone", got)
	}
}

func TestAGrantAdmitsOneFileWithoutAdmittingItsHome(t *testing.T) {
	t.Parallel()

	changed := []string{"docs/designs/v1-harness-design.md", "docs/designs/v2-harness-design.md"}
	granted := Grants("Protected-path grant: docs/designs/v1-harness-design.md")
	want := []string{"docs/designs/v2-harness-design.md"}
	if got := defaultSet().Refused(changed, granted); !slices.Equal(got, want) {
		t.Fatalf("Refused() = %v, want %v", got, want)
	}
	// A grant naming the home covers what is inside it, which is what an item
	// that genuinely reorganizes one has to be able to say.
	if got := defaultSet().Refused(changed, Grants("Protected-path grant: docs/designs")); len(got) != 0 {
		t.Fatalf("Refused() = %v, want a granted home to admit its contents", got)
	}
}

func TestGrantsAreReadFromEveryTextGivenAndOnlyFromTheMarker(t *testing.T) {
	t.Parallel()

	granted := Grants(
		"Move the design document",
		"Protected paths are .yoyodyne/**, docs/product/**, docs/designs/** and docs/decisions/**.",
		"- **Protected-path grant:** `docs/designs/v1-harness-design.md`, docs/product/goals/v1-goals.md",
		"a sentence mentioning protected-path grant: in passing",
		"protected-path grant: .yoyodyne/personas/",
	)
	want := []string{".yoyodyne/personas", "docs/designs/v1-harness-design.md", "docs/product/goals/v1-goals.md"}
	if !slices.Equal(granted, want) {
		t.Fatalf("Grants() = %v, want %v", granted, want)
	}
}

// A grant is usually written as a sentence, and the punctuation that ends one is
// not part of the path. Without this an item that plainly names the path is
// refused and blocked for it, which is the worst way to learn about a parser.
func TestAGrantSurvivesThePunctuationOfTheSentenceItIsWrittenIn(t *testing.T) {
	t.Parallel()

	for _, line := range []string{
		"Protected-path grant: docs/designs/v1-harness-design.md.",
		"Protected-path grant: `docs/designs/v1-harness-design.md`.",
		"Protected-path grant: **docs/designs/v1-harness-design.md**!",
		"Protected-path grant: (docs/designs/v1-harness-design.md)",
		"Protected-path grant: docs/designs/v1-harness-design.md,",
	} {
		granted := Grants(line)
		if !slices.Equal(granted, []string{"docs/designs/v1-harness-design.md"}) {
			t.Fatalf("Grants(%q) = %v, want the path it names", line, granted)
		}
	}
	// A leading stop is part of the path rather than decoration around it, and
	// the path it belongs to is the one most worth granting explicitly.
	for _, line := range []string{
		"Protected-path grant: .yoyodyne/config.yaml",
		"Protected-path grant: `.yoyodyne/config.yaml`.",
		"Protected-path grant: **.yoyodyne/config.yaml**",
	} {
		granted := Grants(line)
		if !slices.Equal(granted, []string{".yoyodyne/config.yaml"}) {
			t.Fatalf("Grants(%q) = %v, want the dotted path intact", line, granted)
		}
	}
}

// TestProseDescribingTheGateGrantsNothing is the case the marker was chosen for.
// The work item that introduced this gate names every protected path in its own
// description, and an item that discusses the rule must not thereby suspend it.
func TestProseDescribingTheGateGrantsNothing(t *testing.T) {
	t.Parallel()

	description := "Protected paths - .yoyodyne/**, docs/product/**, docs/designs/**, docs/decisions/** - are default-deny for a developer's diff."
	if granted := Grants(description); len(granted) != 0 {
		t.Fatalf("Grants() = %v, want nothing granted by prose about the rule", granted)
	}
	if refused := defaultSet().Refused([]string{"docs/product/brief.md"}, Grants(description)); len(refused) != 1 {
		t.Fatalf("Refused() = %v, want the brief still refused", refused)
	}
}

func TestNoGrantOpensTheWholeRepository(t *testing.T) {
	t.Parallel()

	for _, grant := range []string{".", "/", "", "..", "../..", "  "} {
		granted := Grants("Protected-path grant: " + grant)
		if refused := defaultSet().Refused([]string{"docs/product/brief.md"}, granted); len(refused) != 1 {
			t.Fatalf("grant %q admitted the brief: Grants() = %v, Refused() = %v", grant, granted, refused)
		}
	}
}

func TestPathsAreComparedAsPathsRatherThanAsStrings(t *testing.T) {
	t.Parallel()

	// The same file written three ways is one refusal, and a grant written a
	// fourth way still covers it.
	changed := []string{"docs/product/brief.md", "./docs/product/brief.md", "docs/product/../product/brief.md"}
	if got := defaultSet().Refused(changed, nil); !slices.Equal(got, []string{"docs/product/brief.md"}) {
		t.Fatalf("Refused() = %v, want one refusal", got)
	}
	if got := defaultSet().Refused(changed, Grants("Protected-path grant: /docs/product/brief.md")); len(got) != 0 {
		t.Fatalf("Refused() = %v, want the grant to cover the same file", got)
	}
}

func TestTheGrantInstructionNamesTheMarkerItTellsAnAgentToUse(t *testing.T) {
	t.Parallel()

	// A refusal that does not say how an exception is made is a refusal an agent
	// has to guess its way past, which is the behavior this gate exists to stop.
	if !strings.Contains(GrantInstruction, GrantMarker) {
		t.Fatalf("GrantInstruction does not name the marker:\n%s", GrantInstruction)
	}
}
