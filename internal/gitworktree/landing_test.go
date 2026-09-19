package gitworktree

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A landing checkout is the integrated commit and nothing else: detached at
// it, under the worktree root beside the run worktrees, and gone once removed.
func TestALandingCheckoutIsTheIntegratedCommitDetachedAndIsRemovedWhole(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	worktreeRoot := filepath.Join(t.TempDir(), "worktrees")
	manager := newManager(t, repository, worktreeRoot)
	commit := strings.TrimSpace(gitOutput(t, repository, "rev-parse", "HEAD"))

	path, err := manager.CheckoutCommit(context.Background(), testRunID, commit)
	if err != nil {
		t.Fatalf("CheckoutCommit() error = %v", err)
	}
	// The root is compared as the manager canonicalized it, which on macOS is
	// not the string the test passed in.
	if filepath.Dir(path) != manager.worktreeRoot || filepath.Base(path) != "landing-01234567" {
		t.Fatalf("checkout at %s, want it under the worktree root named for the run", path)
	}
	if head := strings.TrimSpace(gitOutput(t, path, "rev-parse", "HEAD")); head != commit {
		t.Fatalf("checkout HEAD = %s, want the integrated commit %s", head, commit)
	}
	if branch := strings.TrimSpace(gitOutput(t, path, "rev-parse", "--abbrev-ref", "HEAD")); branch != "HEAD" {
		t.Fatalf("checkout is on branch %q, want it detached", branch)
	}
	if content := readFile(t, path, "README.txt"); content != "test\n" {
		t.Fatalf("checkout README = %q, want the commit's content", content)
	}

	// A check that left build products behind is nothing to preserve.
	writeFile(t, path, "build.out", "artifact\n")
	if err := manager.RemoveCheckout(context.Background(), path); err != nil {
		t.Fatalf("RemoveCheckout() error = %v", err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("Lstat() after removal = %v, want the checkout gone", err)
	}
	if listing := gitOutput(t, repository, "worktree", "list"); strings.Contains(listing, "landing-") {
		t.Fatalf("worktree listing still names the landing checkout:\n%s", listing)
	}
}

// A process that died mid-landing leaves its checkout standing, and the next
// landing for the same run takes the path over rather than refusing it.
func TestALandingCheckoutLeftByADeadProcessIsReplaced(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	commit := strings.TrimSpace(gitOutput(t, repository, "rev-parse", "HEAD"))

	first, err := manager.CheckoutCommit(context.Background(), testRunID, commit)
	if err != nil {
		t.Fatalf("first CheckoutCommit() error = %v", err)
	}
	writeFile(t, first, "half-done.out", "left behind\n")
	second, err := manager.CheckoutCommit(context.Background(), testRunID, commit)
	if err != nil {
		t.Fatalf("second CheckoutCommit() error = %v", err)
	}
	if second != first {
		t.Fatalf("second checkout at %s, want the same path %s taken over", second, first)
	}
	if _, err := os.Lstat(filepath.Join(second, "half-done.out")); !os.IsNotExist(err) {
		t.Fatal("the replaced checkout still holds what the dead process left")
	}
	if err := manager.RemoveCheckout(context.Background(), second); err != nil {
		t.Fatalf("RemoveCheckout() error = %v", err)
	}
}

// Removal is handed a path a record carried, and a record can be wrong: a path
// that is not a landing checkout under the root is refused rather than removed.
func TestRemovingAnythingButALandingCheckoutIsRefused(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	worktreeRoot := filepath.Join(t.TempDir(), "worktrees")
	manager := newManager(t, repository, worktreeRoot)
	worktree, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-ifd.401", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	for _, path := range []string{worktree.Path, repository, filepath.Join(t.TempDir(), "landing-elsewhere")} {
		if err := manager.RemoveCheckout(context.Background(), path); err == nil {
			t.Fatalf("RemoveCheckout(%s) = nil, want a refusal", path)
		}
	}
	if _, err := os.Lstat(worktree.Path); err != nil {
		t.Fatalf("the run worktree was touched: %v", err)
	}
}
