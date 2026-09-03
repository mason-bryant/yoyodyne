package protectedpath

import (
	"slices"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
)

// heldSet is what a run of this project gets: the same artifact homes, plus the
// tracker's export the harness refreshes into every worktree.
func heldSet() Set {
	return Protect(config.Config{Product: config.Product{
		Specifications: config.DefaultSpecifications,
		Designs:        config.DefaultDesigns,
		Decisions:      config.DefaultDecisions,
		Invariants:     config.DefaultInvariants,
	}}, ".beads/issues.jsonl")
}

func TestAHeldExportIsRefusedAndItsNeighboursAreNot(t *testing.T) {
	t.Parallel()

	changed := []string{
		"internal/orchestrator/pipeline.go",
		// The export the harness holds, back in the change because something in
		// the worktree lifted the hold.
		".beads/issues.jsonl",
		// A file beside it that nothing holds: an export is a file rather than a
		// home, so the directory around it is the developer's like any other.
		".beads/interactions.jsonl",
	}
	want := []string{".beads/issues.jsonl"}
	if got := heldSet().Refused(changed, nil); !slices.Equal(got, want) {
		t.Fatalf("Refused() = %v, want %v", got, want)
	}
	// A project the harness holds nothing for refuses nothing here, which is the
	// case for a product that tracks its work outside a repository export.
	if got := defaultSet().Refused(changed, nil); len(got) != 0 {
		t.Fatalf("Refused() = %v, want nothing refused where no export is held", got)
	}
}

func TestAGrantAdmitsAHeldExportTheSameWayItAdmitsADocument(t *testing.T) {
	t.Parallel()

	// A grant is item text authored and reviewed before the run started, which is
	// the one thing a lifted index bit is not — so the way out of this refusal is
	// the way out of every other one.
	granted := Grants("Protected-path grant: .beads/issues.jsonl")
	if got := heldSet().Refused([]string{".beads/issues.jsonl"}, granted); len(got) != 0 {
		t.Fatalf("Refused() = %v, want the granted export admitted", got)
	}
}

func TestHeldExportsAreReportedApartFromTheDocumentsTheyAreRefusedBeside(t *testing.T) {
	t.Parallel()

	set := heldSet()
	if got := set.HeldExports(); !slices.Equal(got, []string{".beads/issues.jsonl"}) {
		t.Fatalf("HeldExports() = %v, want the export this project holds", got)
	}
	// The refusal has to say which of its two rules it caught: a developer told
	// only that it may not change a file the harness put in its worktree has
	// nothing to act on.
	refused := set.Refused([]string{".beads/issues.jsonl", "docs/product/brief.md"}, nil)
	if got := set.HeldExportsAmong(refused); !slices.Equal(got, []string{".beads/issues.jsonl"}) {
		t.Fatalf("HeldExportsAmong() = %v, want only the held export", got)
	}
	if got := set.HeldExportsAmong([]string{"docs/product/brief.md"}); len(got) != 0 {
		t.Fatalf("HeldExportsAmong() = %v, want a protected document reported as no export", got)
	}
	// What the instruction is worth is that it names the mechanism rather than
	// only the path: the file is one the developer never wrote.
	for _, want := range []string{"base commit", "derived", "hold"} {
		if !strings.Contains(ExportInstruction, want) {
			t.Fatalf("ExportInstruction never mentions %q:\n%s", want, ExportInstruction)
		}
	}
}
