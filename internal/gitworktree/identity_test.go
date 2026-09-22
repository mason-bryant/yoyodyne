package gitworktree

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The identity names the change's content and nothing else: it holds still
// while nothing changes, and moves for a byte written into any file the change
// touches — a tracked edit, an untracked file, a deletion — whether or not the
// attempt has been committed. It is what binds a passing check phase to the
// change it passed over on a project that has made no commit to bind it to.
func TestManagerContentIdentityNamesTheChangeAndMovesWhenItDoes(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-identity", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "README.txt", "test\nedited\n")
	writeFile(t, worktree.Path, filepath.Join("sub", "odd name.txt"), "nested\n")
	writeFile(t, worktree.Path, ".gitignore", "ignored.txt\n")
	writeFile(t, worktree.Path, "ignored.txt", "not part of the change\n")

	before := gitOutput(t, worktree.Path, "status", "--porcelain=v1", "--untracked-files=all")
	first, err := manager.ContentIdentity(context.Background(), worktree)
	if err != nil {
		t.Fatalf("ContentIdentity() error = %v", err)
	}
	if !strings.HasPrefix(first, ContentIdentityPrefix) || len(first) != len(ContentIdentityPrefix)+64 {
		t.Fatalf("ContentIdentity() = %q, want a prefixed sha256", first)
	}
	// Reading never alters the change, and reading again names the same thing.
	if after := gitOutput(t, worktree.Path, "status", "--porcelain=v1", "--untracked-files=all"); after != before {
		t.Errorf("worktree status changed during inspection:\nbefore %q\nafter  %q", before, after)
	}
	if again, _ := manager.ContentIdentity(context.Background(), worktree); again != first {
		t.Fatalf("ContentIdentity() read twice = %q then %q", first, again)
	}

	// An edit that keeps every line count — the kind a summary of the change
	// cannot see — moves it.
	writeFile(t, worktree.Path, "README.txt", "test\nEDITED\n")
	edited, _ := manager.ContentIdentity(context.Background(), worktree)
	if edited == first {
		t.Fatal("ContentIdentity() did not move for an edit to a tracked file")
	}
	// So does a byte in an untracked file, and so does a deletion.
	writeFile(t, worktree.Path, filepath.Join("sub", "odd name.txt"), "nested!\n")
	untracked, _ := manager.ContentIdentity(context.Background(), worktree)
	if untracked == edited {
		t.Fatal("ContentIdentity() did not move for an edit to an untracked file")
	}
	if err := os.Remove(filepath.Join(worktree.Path, "README.txt")); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	deleted, _ := manager.ContentIdentity(context.Background(), worktree)
	if deleted == untracked {
		t.Fatal("ContentIdentity() did not move for a deletion")
	}
	// An ignored file is not part of the change, so writing one moves nothing.
	writeFile(t, worktree.Path, "ignored.txt", "still not part of the change\n")
	if ignored, _ := manager.ContentIdentity(context.Background(), worktree); ignored != deleted {
		t.Fatal("ContentIdentity() moved for a file the change does not carry")
	}

	// Committing the attempt, as a publishing run does before its checks,
	// changes nothing about the content and so nothing about the identity: the
	// checks a publishing run passes are bound to the same reading a
	// non-publishing run's are.
	worktree.HarnessCommit = harnessCommit(t, worktree.Path, "yoyodyne: published attempt")
	if committed, err := manager.ContentIdentity(context.Background(), worktree); err != nil || committed != deleted {
		t.Fatalf("ContentIdentity() after the harness commit = %q, %v; want %q", committed, err, deleted)
	}
}

// A worktree the harness does not own answers nothing, for the reason every
// other reading of a worktree refuses: an identity read off a checkout somebody
// else has moved would bind the evidence to a change the harness never saw.
func TestManagerContentIdentityRefusesAWorktreeItDoesNotOwn(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-identity-moved", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "feature.txt", "implemented\n")
	runGit(t, worktree.Path, "add", "--all")
	runGit(t, worktree.Path, "-c", "user.name=Somebody", "-c", "user.email=somebody@example.invalid", "commit", "-m", "not the harness")
	if _, err := manager.ContentIdentity(context.Background(), worktree); err == nil || !strings.Contains(err.Error(), "owned by the harness") {
		t.Fatalf("ContentIdentity() error = %v, want the moved HEAD refused", err)
	}
}
