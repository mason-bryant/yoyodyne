package gitworktree

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/execution"
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

// A checkout whose exports could not be refreshed is not handed back: it is
// removed with the error, because the caller reads no path from a failure and
// the sweep only ever removes a landing checkout of a landing the record says
// is still running.
func TestALandingCheckoutWhoseRefreshFailsIsRemovedWithTheError(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	worktreeRoot := filepath.Join(t.TempDir(), "worktrees")
	manager := newExportManager(t, repository, worktreeRoot)
	// The primary checkout's export is unreadable as a file, so refreshing it
	// into the checkout fails after the checkout has been cut.
	if err := os.MkdirAll(filepath.Join(repository, exportPath), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	commit := strings.TrimSpace(gitOutput(t, repository, "rev-parse", "HEAD"))

	path, err := manager.CheckoutCommit(context.Background(), testRunID, commit)
	if err == nil || !strings.Contains(err.Error(), "refresh the current exports in the landing checkout") {
		t.Fatalf("CheckoutCommit() = %q, %v, want the refresh failure", path, err)
	}
	if path != "" {
		t.Fatalf("CheckoutCommit() handed back %q with an error, want no path", path)
	}
	if _, err := os.Lstat(filepath.Join(manager.worktreeRoot, "landing-01234567")); !os.IsNotExist(err) {
		t.Fatalf("Lstat() = %v, want the failed checkout removed", err)
	}
	if listing := gitOutput(t, repository, "worktree", "list"); strings.Contains(listing, "landing-") {
		t.Fatalf("worktree listing still names the failed landing checkout:\n%s", listing)
	}
}

// The landing checks run through the same check runner as the per-run gate, and
// that runner points every check's GOCACHE at the build cache the repository's
// Git directory holds. A landing checkout is a worktree of the primary
// repository, so its checks compile against that one shared cache — the one
// every developer worktree uses — rather than a cold one of their own, even
// where the environment they would have inherited named another.
func TestALandingCheckoutCompilesAgainstTheRepositorysSharedBuildCache(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	commit := strings.TrimSpace(gitOutput(t, repository, "rev-parse", "HEAD"))
	path, err := manager.CheckoutCommit(context.Background(), testRunID, commit)
	if err != nil {
		t.Fatalf("CheckoutCommit() error = %v", err)
	}
	defer manager.RemoveCheckout(context.Background(), path)

	shared := goCacheIn(t, execution.WithGoBuildCache([]string{"PATH=/usr/bin"}, repository))
	landing := goCacheIn(t, execution.WithGoBuildCache([]string{"GOCACHE=/a/cold/cache", "PATH=/usr/bin"}, path))
	resolved, err := filepath.EvalSymlinks(repository)
	if err != nil {
		t.Fatalf("EvalSymlinks() error = %v", err)
	}
	if want := filepath.Join(resolved, ".git", "yoyodyne", "go-build"); canonicalCache(t, landing) != want || canonicalCache(t, shared) != want {
		t.Fatalf("landing GOCACHE = %q and primary GOCACHE = %q, want both the repository's shared cache %q", landing, shared, want)
	}
}

func goCacheIn(t *testing.T, environment []string) string {
	t.Helper()
	var found []string
	for _, entry := range environment {
		if value, ok := strings.CutPrefix(entry, "GOCACHE="); ok {
			found = append(found, value)
		}
	}
	if len(found) != 1 {
		t.Fatalf("environment carries GOCACHE %d times, want once: %v", len(found), environment)
	}
	return found[0]
}

// canonicalCache resolves the part of a cache path that exists, since the cache
// directory itself is created by the Go command rather than by the harness.
func canonicalCache(t *testing.T, cache string) string {
	t.Helper()
	parent, err := filepath.EvalSymlinks(filepath.Dir(filepath.Dir(cache)))
	if err != nil {
		t.Fatalf("EvalSymlinks(%s) error = %v", cache, err)
	}
	return filepath.Join(parent, filepath.Base(filepath.Dir(cache)), filepath.Base(cache))
}
