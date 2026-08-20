package surface

// What an item says it will change, and what it does not say. Most of these are
// about the second: a surface read out of prose that nobody meant holds
// unrelated work back, and the whole reason for reading surfaces at all is to
// keep concurrent work concurrent.

import (
	"slices"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
)

func TestDeclaredReadsWhatAnItemNamesAfterTheMarkerHoweverItIsWritten(t *testing.T) {
	t.Parallel()

	declared := Declared(
		"A title that says nothing",
		"conflict-surface: internal/orchestrator/schedule.go\nprose in between\n"+
			"- **Conflict-surface:** `internal/runstate/selection.go`, docs/work.md",
		"a sentence mentioning conflict-surface: in passing",
		"conflict-surface: .beads/",
	)
	want := []string{".beads", "docs/work.md", "internal/orchestrator/schedule.go", "internal/runstate/selection.go"}
	if !slices.Equal(declared, want) {
		t.Fatalf("Declared() = %v, want %v", declared, want)
	}
}

// A declaration written as a sentence ends in the punctuation that closes it,
// and a path carrying a full stop matches nothing at all.
func TestDeclaredTrimsThePunctuationAroundAPath(t *testing.T) {
	t.Parallel()

	for _, line := range []string{
		"conflict-surface: internal/orchestrator/schedule.go.",
		"conflict-surface: `internal/orchestrator/schedule.go`.",
		"conflict-surface: **internal/orchestrator/schedule.go**!",
		"conflict-surface: (internal/orchestrator/schedule.go)",
	} {
		if declared := Declared(line); !slices.Equal(declared, []string{"internal/orchestrator/schedule.go"}) {
			t.Fatalf("Declared(%q) = %v, want the path it names", line, declared)
		}
	}
}

// A surface of "." is every surface, which is not something an author meant to
// say, and a path outside the repository is not a surface of it.
func TestDeclaredRefusesWhatCannotNameAFileInTheRepository(t *testing.T) {
	t.Parallel()

	if declared := Declared("conflict-surface: . .. ../elsewhere/file.go /"); len(declared) != 0 {
		t.Fatalf("Declared() = %v, want nothing that cannot name a file inside the repository", declared)
	}
}

// Inference takes what is unmistakably a file and leaves everything else. The
// rejected cases are the ones that would otherwise appear on nearly every item
// in this repository and hold unrelated work back.
func TestInferredTakesFilesAndNothingThatMerelyContainsASlash(t *testing.T) {
	t.Parallel()

	inferred := Inferred(
		"Put the ordering rationale into internal/orchestrator/schedule.go and update `docs/work.md`.",
		"Read internal/orchestrator for the rest, and the sync notes at "+
			"https://github.com/gastownhall/beads/blob/main/docs/SYNC_CONCEPTS.md.",
		"It applies to the declared and/or inferred surfaces under docs/designs, at refs/dolt/data.",
	)
	want := []string{"docs/work.md", "internal/orchestrator/schedule.go"}
	if !slices.Equal(inferred, want) {
		t.Fatalf("Inferred() = %v, want %v", inferred, want)
	}
}

// A declaration is the whole answer. An author who said which files this touches
// has answered the question, and reading their prose for more would only add
// what they did not mean.
func TestOfPrefersWhatAnItemDeclaresOverWhatItsProseNames(t *testing.T) {
	t.Parallel()

	item := beads.WorkItem{
		ID:          "yoyodyne-ifd.133",
		Title:       "The scheduler orders integration-adjacent work",
		Description: "It grew out of internal/orchestrator/publish.go.\nconflict-surface: internal/orchestrator/schedule.go",
	}
	if surfaces := Of(item); !slices.Equal(surfaces, []string{"internal/orchestrator/schedule.go"}) {
		t.Fatalf("Of() = %v, want the declaration taken as the whole answer", surfaces)
	}
}

// The notes are where the harness appends each run's own record, so a summary
// that happened to name the files it touched must not declare surfaces for the
// item after the fact.
func TestOfIgnoresTheNotesTheHarnessWritesInto(t *testing.T) {
	t.Parallel()

	item := beads.WorkItem{
		ID:    "yoyodyne-ifd.133",
		Title: "Something the author described in words",
		Notes: "conflict-surface: internal/orchestrator/schedule.go\nthe run also touched docs/work.md",
	}
	if surfaces := Of(item); len(surfaces) != 0 {
		t.Fatalf("Of() = %v, want nothing declared by what the harness wrote", surfaces)
	}
}

func TestSharedNamesTheNarrowerOfTwoOverlappingSurfaces(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name        string
		left, right []string
		want        string
	}{
		{
			name:  "the same file",
			left:  []string{"internal/orchestrator/schedule.go"},
			right: []string{"docs/work.md", "internal/orchestrator/schedule.go"},
			want:  "internal/orchestrator/schedule.go",
		},
		{
			name:  "a file inside a declared directory",
			left:  []string{"internal/orchestrator"},
			right: []string{"internal/orchestrator/schedule.go"},
			want:  "internal/orchestrator/schedule.go",
		},
		{
			name:  "a directory inside another",
			left:  []string{"docs/slack/avatars"},
			right: []string{"docs"},
			want:  "docs/slack/avatars",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			shared, ok := Shared(test.left, test.right)
			if !ok || shared != test.want {
				t.Fatalf("Shared() = %q, %v, want %q", shared, ok, test.want)
			}
		})
	}
}

// Neighbours are not overlaps. A prefix that stops mid-segment is a different
// part of the repository, and two files in one package are two files.
func TestSharedReportsNothingForSurfacesThatOnlyLookAlike(t *testing.T) {
	t.Parallel()

	for _, test := range []struct{ left, right []string }{
		{left: []string{"docs/product"}, right: []string{"docs/products"}},
		{left: []string{"internal/orchestrator/schedule.go"}, right: []string{"internal/orchestrator/publish.go"}},
		{left: nil, right: []string{"docs/work.md"}},
	} {
		if shared, ok := Shared(test.left, test.right); ok {
			t.Fatalf("Shared(%v, %v) = %q, want nothing shared", test.left, test.right, shared)
		}
	}
}
