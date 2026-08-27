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

	removal, err := manager.RemovePreservedWorktree(context.Background(), worktree, KeepUncommittedWork)
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
	repeated, err := manager.RemovePreservedWorktree(context.Background(), worktree, KeepUncommittedWork)
	if err != nil || !repeated.Removed {
		t.Fatalf("second RemovePreservedWorktree() = %#v, error = %v", repeated, err)
	}
}

// Uncommitted work is the one thing nothing else records, so a caller that has
// not said what to do about it gets the checkout kept with the reason rather
// than a failure — a caller retiring artifacts is finishing something else.
func TestAPreservedWorktreeHoldingUncommittedWorkIsKept(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree := preservedWorktree(t, manager, "yoyodyne-uncommitted")
	writeFile(t, worktree.Path, "half-done.txt", "the developer got this far\n")

	removal, err := manager.RemovePreservedWorktree(context.Background(), worktree, KeepUncommittedWork)
	if err != nil {
		t.Fatalf("RemovePreservedWorktree() error = %v", err)
	}
	if removal.Removed || removal.PreservedWork != "" {
		t.Fatalf("removal = %#v, want the uncommitted work kept where it is", removal)
	}
	if !strings.Contains(removal.Kept, "uncommitted work") {
		t.Fatalf("kept = %q, want the reason it survived", removal.Kept)
	}
	if _, err := os.Stat(filepath.Join(worktree.Path, "half-done.txt")); err != nil {
		t.Fatalf("the uncommitted work was removed: %v", err)
	}
}

// The other answer, and the one that makes bounding a machine's registrations
// possible at all: the work is moved somewhere durable and the checkout goes.
// "Kept anything dirty" is not a bound — a run that stopped is the run most
// likely to have left a change behind — so what matters is that moving it loses
// nothing, tracked edits and untracked files alike.
func TestAPreservedWorktreeHoldingUncommittedWorkIsCapturedAndRetired(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree := preservedWorktree(t, manager, "yoyodyne-captured")
	// One file the developer changed and one it created: an untracked file alone
	// is enough to make a checkout dirty, and it is the half a naive capture from
	// the index would drop.
	writeFile(t, worktree.Path, "README.txt", "the developer rewrote this\n")
	writeFile(t, worktree.Path, "half-done.txt", "the developer got this far\n")

	removal, err := manager.RemovePreservedWorktree(context.Background(), worktree, CaptureUncommittedWork)
	if err != nil {
		t.Fatalf("RemovePreservedWorktree() error = %v", err)
	}
	if !removal.Removed || removal.Kept != "" {
		t.Fatalf("removal = %#v, want the checkout retired", removal)
	}
	if removal.PreservedWork != PreservedWorkRef(worktree.RunID) {
		t.Fatalf("preserved work = %q, want %q", removal.PreservedWork, PreservedWorkRef(worktree.RunID))
	}
	if _, err := os.Stat(worktree.Path); !os.IsNotExist(err) {
		t.Fatalf("the checkout is still on disk: %v", err)
	}

	// Everything the checkout held is readable from the ref, which is the whole
	// claim that retiring it lost nothing.
	for file, want := range map[string]string{
		"README.txt":    "the developer rewrote this\n",
		"half-done.txt": "the developer got this far\n",
	} {
		got := gitOutput(t, repository, "show", removal.PreservedWork+":"+file)
		if got != want {
			t.Errorf("%s at %s = %q, want %q", file, removal.PreservedWork, got, want)
		}
	}
	// The capture is not a branch, so the branch sweep, `git branch`, and every
	// containment proof the harness makes about run branches all carry on seeing
	// exactly what they saw before.
	if branches := strings.TrimSpace(gitOutput(t, repository, "for-each-ref", "--format=%(refname)", "refs/heads/")); strings.Contains(branches, "preserved-work") {
		t.Errorf("the capture was written as a branch: %q", branches)
	}
	// The run's branch is untouched and still at the base commit: the capture went
	// beside it rather than onto it, so a branch carrying nothing the target lacks
	// stays deletable by the sweep that decides that.
	if commit, err := manager.resolveBranchCommit(context.Background(), worktree.Branch); err != nil || commit != worktree.BaseCommit {
		t.Errorf("branch = %q, error = %v, want it left at the base commit %q", commit, err, worktree.BaseCommit)
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

// A registration whose checkout somebody deleted by hand is the one no sweep
// driven from run state can see, and it costs every later command a deny path
// in its sandbox profile all the same. The prune is what reaches it, and it
// reaches nothing else: a worktree that is still on disk survives it.
func TestPruningRemovesRegistrationsWhoseCheckoutIsGone(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	abandoned := preservedWorktree(t, manager, "yoyodyne-abandoned")
	live, err := manager.Create(context.Background(), CreateRequest{
		RunID:        "run-abcdef0123456789abcdef0123456789",
		WorkItemID:   "yoyodyne-live",
		BaseRef:      "HEAD",
		TargetBranch: "main",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// Somebody removed the directory without telling Git, which is exactly what
	// leaves a registration nothing else knows to clean up.
	if err := os.RemoveAll(abandoned.Path); err != nil {
		t.Fatalf("RemoveAll() error = %v", err)
	}

	prune, err := manager.PruneRegistrations(context.Background())
	if err != nil {
		t.Fatalf("PruneRegistrations() error = %v", err)
	}
	// The manager's worktree root is symlink-resolved at construction, so the
	// path it derived is the same one Git recorded — which is what every other
	// registration lookup here already relies on.
	if len(prune.Pruned) != 1 || prune.Pruned[0] != abandoned.Path {
		t.Fatalf("pruned = %#v, want only %q", prune.Pruned, abandoned.Path)
	}
	registered, _, err := manager.registeredWorktree(context.Background(), abandoned.Path)
	if err != nil || registered {
		t.Fatalf("registered = %t, error = %v, want the stale registration gone", registered, err)
	}
	// The checkout that is still there is untouched: a prune only ever unregisters
	// what is no longer on disk.
	stillRegistered, _, err := manager.registeredWorktree(context.Background(), live.Path)
	if err != nil || !stillRegistered {
		t.Fatalf("registered = %t, error = %v, want the live worktree kept", stillRegistered, err)
	}
	// Pruning again is what every later sweep does, and a repository with nothing
	// stale left reports nothing rather than repeating itself.
	repeated, err := manager.PruneRegistrations(context.Background())
	if err != nil || len(repeated.Pruned) != 0 {
		t.Fatalf("second PruneRegistrations() = %#v, error = %v", repeated, err)
	}
}

// Registered is what tells a checkout the sweep retired from one that was
// already gone, which Removed cannot: both report Removed. A sweep that runs on
// every pass needs the difference or it reports the same long-gone worktree
// forever.
func TestARemovalSaysWhetherThereWasAnythingRegisteredToRemove(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree := preservedWorktree(t, manager, "yoyodyne-registered")

	removal, err := manager.RemovePreservedWorktree(context.Background(), worktree, KeepUncommittedWork)
	if err != nil || !removal.Registered || !removal.Removed {
		t.Fatalf("removal = %#v, error = %v, want a registered worktree retired", removal, err)
	}
	repeated, err := manager.RemovePreservedWorktree(context.Background(), worktree, KeepUncommittedWork)
	if err != nil || repeated.Registered || !repeated.Removed {
		t.Fatalf("second removal = %#v, error = %v, want nothing there to retire", repeated, err)
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

	if _, err := manager.RemovePreservedWorktree(context.Background(), worktree, CaptureUncommittedWork); err == nil {
		t.Fatal("RemovePreservedWorktree() removed a path this manager does not own")
	}
}
