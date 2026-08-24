package gitworktree

// A listing of what a commit holds. It exists for the one question a patch
// cannot answer — is this file in the repository at all — so what it has to get
// right is naming a file the change never touched, and saying so when the bound
// stopped it naming them all.

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
)

func TestFilesAtHeadNamesWhatTheCommitHoldsRatherThanWhatChanged(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	writeFile(t, repository, "docs/design.md", "design\n")
	writeFile(t, repository, "testdata/fixture.bin", "\x00\x01binary\n")
	runGit(t, repository, "add", ".")
	runGit(t, repository, "commit", "-m", "add a document and a binary fixture")
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:        testRunID,
		WorkItemID:   "yoyodyne-listing",
		BaseRef:      "HEAD",
		TargetBranch: "main",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// The change touches one file and creates another. Neither of the committed
	// files below is in its diff, which is exactly why the listing is not the
	// diff: reading absence out of a diff is what twice produced a finding
	// asserting a repository file did not exist.
	writeFile(t, worktree.Path, "feature.txt", "implemented\n")

	listing, err := manager.FilesAtHead(context.Background(), worktree, 0)
	if err != nil {
		t.Fatalf("FilesAtHead() error = %v", err)
	}
	if listing.Commit != worktree.BaseCommit {
		t.Fatalf("listing commit = %q, want the worktree's HEAD %q", listing.Commit, worktree.BaseCommit)
	}
	if listing.Omitted != 0 {
		t.Fatalf("an unbounded listing reported %d omitted path(s)", listing.Omitted)
	}
	for _, want := range []string{"docs/design.md", "testdata/fixture.bin"} {
		if !containsPathEntry(listing.Files, want) {
			t.Fatalf("listing = %#v, want it to name %q", listing.Files, want)
		}
	}
	// A file the developer created is in the patch and is deliberately not here:
	// this describes the committed tree, and saying otherwise would make the
	// listing agree with the diff about the only thing it exists to disagree on.
	if containsPathEntry(listing.Files, "feature.txt") {
		t.Fatalf("listing names an uncommitted file: %#v", listing.Files)
	}
}

// A listing the bound cut can prove a path is present and can prove nothing
// about one it does not name, so what it dropped is counted rather than left to
// be mistaken for the whole repository.
func TestFilesAtHeadCountsWhatTheBoundDropped(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		writeFile(t, repository, name, name+"\n")
	}
	runGit(t, repository, "add", ".")
	runGit(t, repository, "commit", "-m", "several files")
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:        testRunID,
		WorkItemID:   "yoyodyne-bounded",
		BaseRef:      "HEAD",
		TargetBranch: "main",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	whole, err := manager.FilesAtHead(context.Background(), worktree, 0)
	if err != nil {
		t.Fatalf("FilesAtHead() error = %v", err)
	}
	bounded, err := manager.FilesAtHead(context.Background(), worktree, 2)
	if err != nil {
		t.Fatalf("bounded FilesAtHead() error = %v", err)
	}
	if len(bounded.Files) != 2 || bounded.Omitted != len(whole.Files)-2 {
		t.Fatalf("bounded listing = %#v, want 2 of %d named and the rest counted", bounded, len(whole.Files))
	}
	// Sorted, so what the bound keeps is decided by the repository rather than by
	// whatever order Git happened to walk the tree in.
	if !reflect.DeepEqual(bounded.Files, whole.Files[:2]) {
		t.Fatalf("bounded listing = %#v, want the first two of %#v", bounded.Files, whole.Files)
	}
}

func containsPathEntry(files []string, want string) bool {
	for _, file := range files {
		if file == want {
			return true
		}
	}
	return false
}
