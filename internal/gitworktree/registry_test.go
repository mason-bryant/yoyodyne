package gitworktree

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// askingRunner counts the Git commands that ask one of the two questions the
// manager keeps an answer to, so a test can say how often Git was actually
// asked rather than what the manager answered.
type askingRunner struct {
	delegate execution.ProcessRunner

	mu       sync.Mutex
	common   int
	listings int
}

func (r *askingRunner) Run(ctx context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	r.mu.Lock()
	switch {
	case containsArguments(command.Args, "rev-parse", "--git-common-dir"):
		r.common++
	case containsArguments(command.Args, "worktree", "list"):
		r.listings++
	}
	r.mu.Unlock()
	return r.delegate.Run(ctx, command, observer)
}

func (r *askingRunner) asked() (int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.common, r.listings
}

func newAskingManager(t *testing.T) (*Manager, *askingRunner, string) {
	t.Helper()
	repository := newRepository(t)
	runner := &askingRunner{delegate: execution.OSProcessRunner{}}
	manager, err := New(Options{
		Runner:         runner,
		RepositoryRoot: repository,
		WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
		Timeout:        testGitBudget,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return manager, runner, repository
}

// The common directory was asked of Git once per lease, and every command that
// walks the registrations takes one, which made it the single most repeated Git
// command the harness ran. It depends on nothing a manager changes, so a manager
// asks once however many leases it takes.
func TestManagerAsksForTheCommonDirectoryOnce(t *testing.T) {
	t.Parallel()

	manager, runner, _ := newAskingManager(t)
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:      testRunID,
		WorkItemID: "yoyodyne-common-once",
		BaseRef:    "HEAD",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	for range 3 {
		if _, err := manager.Inspect(context.Background(), worktree); err != nil {
			t.Fatalf("Inspect() error = %v", err)
		}
	}
	if _, err := manager.PruneRegistrations(context.Background()); err != nil {
		t.Fatalf("PruneRegistrations() error = %v", err)
	}
	if common, _ := runner.asked(); common != 1 {
		t.Fatalf("Git was asked for the common directory %d times, want once", common)
	}
}

// A kept common directory that is no longer there is a repository replaced
// under the manager, and is asked again rather than handed to the lease.
func TestManagerAsksAgainForACommonDirectoryThatIsGone(t *testing.T) {
	t.Parallel()

	manager, runner, _ := newAskingManager(t)
	manager.commonDirectory = filepath.Join(t.TempDir(), "gone")
	directory, err := manager.commonGitDirectory(context.Background())
	if err != nil {
		t.Fatalf("commonGitDirectory() error = %v", err)
	}
	if want := filepath.Join(manager.repositoryRoot, ".git"); directory != want {
		t.Fatalf("commonGitDirectory() = %s, want %s", directory, want)
	}
	if common, _ := runner.asked(); common != 1 {
		t.Fatalf("Git was asked for the common directory %d times, want once", common)
	}
}

// Under a held lease the listing is asked of Git once and answered from the
// lease after that, and it is never answered across a change the holder made:
// an add, a removal, and a prune each drop it, so the next listing asks Git
// again and sees the change. Letting the lease go drops it for good, because
// from then on another harness may be changing the registrations.
func TestListingKeptUnderALeaseIsNeverStaleAcrossTheHoldersOwnChanges(t *testing.T) {
	t.Parallel()

	manager, runner, repository := newAskingManager(t)
	if err := os.MkdirAll(manager.worktreeRoot, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	held, lease, err := manager.leaseRegistry(context.Background())
	if err != nil {
		t.Fatalf("leaseRegistry() error = %v", err)
	}
	defer func() { _ = lease.release() }()

	listings := func() int {
		t.Helper()
		_, listings := runner.asked()
		return listings
	}
	registered := func(path string) bool {
		t.Helper()
		found, _, err := manager.registeredWorktree(held, path)
		if err != nil {
			t.Fatalf("registeredWorktree() error = %v", err)
		}
		return found
	}
	mustRun := func(args ...string) {
		t.Helper()
		result, err := manager.run(held, append([]string{"-C", repository}, args...)...)
		if err != nil || result.Status != execution.ProcessSucceeded {
			t.Fatalf("git %v: err = %v, stderr = %s", args, err, result.Stderr)
		}
	}

	path := filepath.Join(manager.worktreeRoot, "kept")
	before := listings()
	if registered(path) || registered(path) {
		t.Fatalf("%s is registered before anything added it", path)
	}
	if asked := listings() - before; asked != 1 {
		t.Fatalf("two listings under one lease asked Git %d times, want once", asked)
	}

	mustRun("branch", "kept", "HEAD")
	mustRun("worktree", "add", path, "kept")
	before = listings()
	if !registered(path) {
		t.Fatal("the listing after the holder's own add does not show what it added")
	}
	if !registered(path) {
		t.Fatal("the kept listing lost what the holder added")
	}
	if asked := listings() - before; asked != 1 {
		t.Fatalf("listing twice after an add asked Git %d times, want once", asked)
	}

	mustRun("worktree", "remove", path)
	if registered(path) {
		t.Fatal("the listing after the holder's own removal still shows what it removed")
	}

	pruned := filepath.Join(manager.worktreeRoot, "pruned")
	mustRun("branch", "pruned", "HEAD")
	mustRun("worktree", "add", pruned, "pruned")
	if !registered(pruned) {
		t.Fatal("the listing after the holder's own add does not show what it added")
	}
	if err := os.RemoveAll(pruned); err != nil {
		t.Fatalf("RemoveAll() error = %v", err)
	}
	mustRun("worktree", "prune")
	if registered(pruned) {
		t.Fatal("the listing after the holder's own prune still shows what it pruned")
	}

	if err := lease.release(); err != nil {
		t.Fatalf("release() error = %v", err)
	}
	before = listings()
	registered(pruned)
	registered(pruned)
	if asked := listings() - before; asked != 2 {
		t.Fatalf("two listings after the lease was released asked Git %d times, want each of them to ask", asked)
	}
}

// Outside an exclusive lease anybody may be changing the registrations, so
// nothing is kept and every listing asks Git.
func TestListingOutsideALeaseIsNeverKept(t *testing.T) {
	t.Parallel()

	manager, runner, _ := newAskingManager(t)
	for range 2 {
		if _, err := manager.listWorktrees(context.Background()); err != nil {
			t.Fatalf("listWorktrees() error = %v", err)
		}
	}
	if _, listings := runner.asked(); listings != 2 {
		t.Fatalf("two listings outside a lease asked Git %d times, want each of them to ask", listings)
	}
}

// A listing asked for before a change and answered after it describes the
// registrations as they were, so it is used by whoever asked and never kept.
func TestListingReadAcrossAChangeIsNotKept(t *testing.T) {
	t.Parallel()

	state := &registryState{}
	_, generation, ok := state.cachedListing()
	if ok {
		t.Fatal("a new lease answered a listing it never took")
	}
	state.forgetListing()
	state.keepListing(generation, []worktreeEntry{{path: "/stale"}})
	if _, _, ok := state.cachedListing(); ok {
		t.Fatal("a listing read across a change was kept")
	}

	_, generation, _ = state.cachedListing()
	state.keepListing(generation, []worktreeEntry{{path: "/current"}})
	entries, _, ok := state.cachedListing()
	if !ok || len(entries) != 1 || entries[0].path != "/current" {
		t.Fatalf("cachedListing() = %v, %v, want the listing just kept", entries, ok)
	}

	state.release()
	_, generation, _ = state.cachedListing()
	state.keepListing(generation, []worktreeEntry{{path: "/after"}})
	if _, _, ok := state.cachedListing(); ok {
		t.Fatal("a released lease kept a listing")
	}
}

// Which commands drop a kept listing is stated as a table for the same reason
// the lease's own is: it is a judgement about what Git writes, and a command
// wrongly left off it is a listing answered across a change.
func TestGitCommandsThatMayChangeTheRegistrationsDropTheKeptListing(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name    string
		args    []string
		changes bool
	}{
		{name: "the listing", args: []string{"-C", "/repository", "worktree", "list", "--porcelain"}, changes: false},
		{name: "an add", args: []string{"-C", "/repository", "worktree", "add", "/path", "branch"}, changes: true},
		{name: "a removal", args: []string{"-C", "/repository", "worktree", "remove", "--force", "/path"}, changes: true},
		{name: "a prune", args: []string{"-C", "/repository", "worktree", "prune"}, changes: true},
		{name: "a rebase", args: []string{"-C", "/worktree", "rebase", "--onto", "main", "base"}, changes: true},
		{name: "a checkout", args: []string{"-C", "/worktree", "-c", "core.hooksPath=/dev/null", "checkout", "HEAD", "--", "file"}, changes: true},
		{name: "a branch", args: []string{"-C", "/repository", "branch", "feature", "HEAD"}, changes: true},
		{name: "a switch", args: []string{"-C", "/worktree", "switch", "main"}, changes: true},
		{name: "the common directory", args: []string{"-C", "/repository", "rev-parse", "--git-common-dir"}, changes: false},
		{name: "a status", args: []string{"-C", "/worktree", "status", "--porcelain"}, changes: false},
		{name: "nothing at all", args: nil, changes: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if changes := mayChangeRegistrations(testCase.args); changes != testCase.changes {
				t.Fatalf("mayChangeRegistrations(%q) = %v, want %v", testCase.args, changes, testCase.changes)
			}
		})
	}
}
