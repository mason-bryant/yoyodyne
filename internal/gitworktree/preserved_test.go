package gitworktree

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// preservedWorktree is what a stopped run leaves behind: a worktree the harness
// created, with a commit on its branch that nothing has promoted.
func preservedWorktree(t *testing.T, manager *Manager, workItemID string) Worktree {
	t.Helper()
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:        testRunID,
		WorkItemID:   workItemID,
		BaseRef:      "HEAD",
		TargetBranch: "main",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	return worktree
}

// The orphan this exists to prevent: a worktree of a run that stopped, still
// registered long after the work landed by another route. Nothing is lost by
// removing it, and the branch is left for the question only the target can
// answer.
func TestAPreservedWorktreeIsRemovedWithoutTouchingItsBranch(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree := preservedWorktree(t, manager, "yoyodyne-preserved")

	removal, err := manager.RemovePreservedWorktree(context.Background(), worktree)
	if err != nil {
		t.Fatalf("RemovePreservedWorktree() error = %v", err)
	}
	if !removal.Removed || removal.Kept != "" {
		t.Fatalf("removal = %#v, want the worktree removed", removal)
	}
	if _, err := os.Stat(worktree.Path); !os.IsNotExist(err) {
		t.Fatalf("the worktree is still on disk: %v", err)
	}
	registered, _, err := manager.registeredWorktree(context.Background(), worktree.Path)
	if err != nil || registered {
		t.Fatalf("registered = %t, error = %v, want the registration gone", registered, err)
	}
	// The branch is deliberately still there: whether it may go is a question
	// about the target rather than about the directory.
	if _, err := manager.resolveBranchCommit(context.Background(), worktree.Branch); err != nil {
		t.Fatalf("the branch was deleted with the worktree: %v", err)
	}
	// Asking again is not an error: the artifact is gone, which is what a caller
	// records either way.
	repeated, err := manager.RemovePreservedWorktree(context.Background(), worktree)
	if err != nil || !repeated.Removed {
		t.Fatalf("second RemovePreservedWorktree() = %#v, error = %v", repeated, err)
	}
}

// Uncommitted work is the one thing nothing else records, so it is the one
// thing this never removes. It is kept with the reason rather than failing,
// because a caller retiring artifacts is finishing something else.
func TestAPreservedWorktreeHoldingUncommittedWorkIsKept(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree := preservedWorktree(t, manager, "yoyodyne-uncommitted")
	writeFile(t, worktree.Path, "half-done.txt", "the developer got this far\n")

	removal, err := manager.RemovePreservedWorktree(context.Background(), worktree)
	if err != nil {
		t.Fatalf("RemovePreservedWorktree() error = %v", err)
	}
	if removal.Removed {
		t.Fatalf("removal = %#v, want the uncommitted work kept", removal)
	}
	if !strings.Contains(removal.Kept, "uncommitted work") {
		t.Fatalf("kept = %q, want the reason it survived", removal.Kept)
	}
	if _, err := os.Stat(filepath.Join(worktree.Path, "half-done.txt")); err != nil {
		t.Fatalf("the uncommitted work was removed: %v", err)
	}
}

// A retirement removes the registration and keeps a branch whose work nothing
// promoted, which is the ordinary shape after a re-run: the fresh run landed its
// own change, and the stopped run's commits are still only on its branch.
func TestRetiringPreservedArtifactsKeepsABranchNothingPromoted(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree := preservedWorktree(t, manager, "yoyodyne-unpromoted")
	writeFile(t, worktree.Path, "feature.txt", "the stopped run got this far\n")
	runGit(t, worktree.Path, "add", "feature.txt")
	runGit(t, worktree.Path, "commit", "-m", "work nothing promoted")

	retirement, err := manager.RetirePreserved(context.Background(), worktree, "main")
	if err != nil {
		t.Fatalf("RetirePreserved() error = %v", err)
	}
	if retirement.Retired() {
		t.Fatalf("retirement = %#v, want the unpromoted branch kept", retirement)
	}
	if !retirement.Worktree.Removed {
		t.Fatalf("worktree = %#v, want the registration retired", retirement.Worktree)
	}
	if !strings.Contains(retirement.Kept(), "not contained in main") {
		t.Fatalf("kept = %q, want why the branch survived", retirement.Kept())
	}
	// Nothing was lost: the commits are still reachable from the branch.
	if _, err := manager.resolveBranchCommit(context.Background(), worktree.Branch); err != nil {
		t.Fatalf("the kept branch is gone: %v", err)
	}
}

// A branch the target already carries has nothing on it that deleting could
// lose, so a retirement takes both and leaves nothing behind.
func TestRetiringPreservedArtifactsTakesBothWhenTheWorkIsPromoted(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree := preservedWorktree(t, manager, "yoyodyne-promoted")
	writeFile(t, worktree.Path, "feature.txt", "implemented\n")
	if _, err := manager.Integrate(context.Background(), worktree, ""); err != nil {
		t.Fatalf("Integrate() error = %v", err)
	}

	retirement, err := manager.RetirePreserved(context.Background(), worktree, "main")
	if err != nil {
		t.Fatalf("RetirePreserved() error = %v", err)
	}
	if !retirement.Retired() || retirement.Kept() != "" {
		t.Fatalf("retirement = %#v, want both artifacts retired", retirement)
	}
}

// The registration nothing else here can reach. A checkout somebody removed by
// hand leaves bookkeeping no run record can act on — there is no directory left
// to point a removal at — and every one of those entries is a path an agent's
// sandbox profile carries on every command it spawns.
func TestPruningClearsTheRegistrationOfACheckoutThatIsGone(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree := preservedWorktree(t, manager, "yoyodyne-pruned")
	if err := os.RemoveAll(worktree.Path); err != nil {
		t.Fatalf("RemoveAll() error = %v", err)
	}

	prune, err := manager.PruneRegistrations(context.Background())
	if err != nil {
		t.Fatalf("PruneRegistrations() error = %v", err)
	}
	if len(prune.Pruned) != 1 || prune.Pruned[0] != worktree.Path {
		t.Fatalf("pruned = %#v, want the registration of %s", prune.Pruned, worktree.Path)
	}
	if prune.Registered != 1 {
		t.Errorf("registered = %d, want only the primary checkout left", prune.Registered)
	}
	registered, _, err := manager.registeredWorktree(context.Background(), worktree.Path)
	if err != nil || registered {
		t.Fatalf("registered = %t, error = %v, want the registration gone", registered, err)
	}
	// The branch is left exactly as every other removal here leaves it: whether it
	// may go is a question about the target rather than about the bookkeeping.
	if _, err := manager.resolveBranchCommit(context.Background(), worktree.Branch); err != nil {
		t.Fatalf("the branch was deleted with the registration: %v", err)
	}
	// Sweeping again is what every later reconcile does, and a repository with
	// nothing stale has nothing to prune.
	repeated, err := manager.PruneRegistrations(context.Background())
	if err != nil || len(repeated.Pruned) != 0 || repeated.Registered != 1 {
		t.Fatalf("second PruneRegistrations() = %#v, error = %v", repeated, err)
	}
}

// The ownership rule every other removal here is held to: a worktree this
// manager did not create is not this manager's to remove.
func TestRetiringAWorktreeThisManagerDoesNotOwnIsRefused(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree := preservedWorktree(t, manager, "yoyodyne-elsewhere")
	worktree.Path = filepath.Join(t.TempDir(), "somebody-elses-checkout")

	if _, err := manager.RemovePreservedWorktree(context.Background(), worktree); err == nil {
		t.Fatal("RemovePreservedWorktree() removed a path this manager does not own")
	}
}
