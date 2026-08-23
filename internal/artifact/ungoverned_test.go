package artifact

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIdentityOutsideEveryHomeIsReportedAndNothingIsRefused(t *testing.T) {
	t.Parallel()

	// This is the case that cost the repository a design nothing governed: the
	// document reads as an artifact, the store never walks it, and the load comes
	// back clean, so every check downstream passes while nothing can refer to it.
	store := newStore(t)
	write(t, store, productHome+"/brief.md", document("brief", "brief", "Product brief", nil, "active"))
	write(t, store, "docs/v1-harness-design.md", document("v1-harness-design", "design", "V1 harness design", []string{"brief"}, "active"))

	set, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// Nothing is refused over it, and it is not quietly admitted either: the set
	// is exactly what the homes hold.
	if len(set.Artifacts) != 1 || set.Artifacts[0].ID != "brief" {
		t.Fatalf("artifacts = %#v", set.Artifacts)
	}
	if len(set.Problems) != 0 || len(set.ReferenceProblems) != 0 {
		t.Fatalf("problems = %v, reference problems = %v", set.Problems, set.ReferenceProblems)
	}
	if len(set.Ungoverned) != 1 {
		t.Fatalf("ungoverned = %#v", set.Ungoverned)
	}
	reported := set.Ungoverned[0]
	if reported.Kind != UngovernedOutsideHomes || reported.Path != "docs/v1-harness-design.md" || reported.ID != "v1-harness-design" {
		t.Fatalf("reported = %#v", reported)
	}
	// What is reported has to say what to do about it, which is where the homes
	// are and that nothing resolves to the id it claims.
	if !strings.Contains(reported.Reason, productHome) || !strings.Contains(reported.Reason, "nothing reads it") {
		t.Fatalf("reason = %q", reported.Reason)
	}
}

func TestFrontmatterOnADirectoryIndexIsReportedAsInert(t *testing.T) {
	t.Parallel()

	// An index is skipped by name and always will be, so identity written on one
	// is not a document that is nearly governed: it is metadata nothing will ever
	// read, which looks exactly like metadata that works.
	store := newStore(t)
	write(t, store, productHome+"/brief.md", document("brief", "brief", "Product brief", nil, "active"))
	write(t, store, productHome+"/goals/README.md", document("README", "goals", "Goals directory", []string{"brief"}, "active"))

	set, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(set.Artifacts) != 1 || len(set.Problems) != 0 {
		t.Fatalf("set = %#v", set)
	}
	if len(set.Ungoverned) != 1 {
		t.Fatalf("ungoverned = %#v", set.Ungoverned)
	}
	reported := set.Ungoverned[0]
	if reported.Kind != UngovernedIndex || reported.Path != productHome+"/goals/README.md" {
		t.Fatalf("reported = %#v", reported)
	}
	if !strings.Contains(reported.Reason, "inert") {
		t.Fatalf("reason = %q", reported.Reason)
	}
}

func TestAnIndexWithNoIdentityAndOrdinaryFrontmatterAreNotReported(t *testing.T) {
	t.Parallel()

	// The report is of documents written to be governed. An index that says what
	// is filed beside it, and the frontmatter conventions everything else in a
	// repository writes, are neither — reporting them would be a channel nobody
	// reads by the second week.
	store := newStore(t)
	write(t, store, productHome+"/brief.md", document("brief", "brief", "Product brief", nil, "active"))
	write(t, store, productHome+"/goals/README.md", "# Goals directory\n\nWhat is filed here.\n")
	write(t, store, "docs/notes.md", "---\ntitle: A note\nlayout: page\n---\n\n# A note\n")
	// An invariant records an id and no kind, under a scheme of its own, and is
	// read by that scheme rather than this one.
	write(t, store, invariantsHome+"/one-writer-per-item.md", "---\nid: one-writer-per-item\ntitle: One process at a time\nstatus: active\n---\n\n## Must hold\n\nNothing.\n")

	set, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(set.Ungoverned) != 0 {
		t.Fatalf("ungoverned = %#v", set.Ungoverned)
	}
}

func TestTheScanIsBoundedToWhatSitsBesideTheHomes(t *testing.T) {
	t.Parallel()

	// The bound is what keeps every fixture and vendored document in a repository
	// out of the report. A document filed beside the homes is the case worth
	// reporting; one in a test tree three directories away is somebody's fixture.
	store := newStore(t)
	write(t, store, productHome+"/brief.md", document("brief", "brief", "Product brief", nil, "active"))
	write(t, store, "internal/artifact/testdata/design.md", document("design", "design", "A fixture", []string{"brief"}, "active"))
	write(t, store, "docs/beside/design.md", document("design", "design", "Filed beside the homes", []string{"brief"}, "active"))

	set, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(set.Ungoverned) != 1 || set.Ungoverned[0].Path != "docs/beside/design.md" {
		t.Fatalf("ungoverned = %#v", set.Ungoverned)
	}
}

func TestADirectoryTheScanCannotReadIsReportedRatherThanSkipped(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("root reads an unreadable directory, so there is nothing to report")
	}
	// A subtree that was not walked is identity nothing looked at, which is what
	// this whole report exists to stop being silent. It costs the load nothing:
	// every artifact the homes hold is still returned.
	store := newStore(t)
	write(t, store, productHome+"/brief.md", document("brief", "brief", "Product brief", nil, "active"))
	closed := filepath.Join(store.RepositoryRoot, "docs", "closed")
	if err := os.MkdirAll(closed, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.Chmod(closed, 0o000); err != nil {
		t.Fatalf("Chmod() error = %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(closed, 0o755) })

	set, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(set.Artifacts) != 1 {
		t.Fatalf("artifacts = %#v", set.Artifacts)
	}
	if len(set.Ungoverned) != 1 || set.Ungoverned[0].Kind != UngovernedUnread {
		t.Fatalf("ungoverned = %#v", set.Ungoverned)
	}
	if !strings.Contains(set.Ungoverned[0].Reason, "not accounted for") {
		t.Fatalf("reason = %q", set.Ungoverned[0].Reason)
	}
}

func TestScanRootsAreTheParentsOfTheHomesAndAreNotWalkedTwice(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		homes []string
		want  string
	}{
		"the recommended layout": {homes: []string{productHome, designsHome, decisionsHome}, want: "docs"},
		"a home nested in another home's parent": {
			homes: []string{"docs/product", "docs/architecture/designs"},
			want:  "docs",
		},
		// A home at the top of the repository makes the bound the repository,
		// which is what the bound means there rather than a case to special-case.
		"a home at the repository root": {homes: []string{"product", "designs"}, want: "."},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := strings.Join(scanRoots(testCase.homes), ","); got != testCase.want {
				t.Fatalf("scanRoots(%v) = %q, want %q", testCase.homes, got, testCase.want)
			}
		})
	}
}
