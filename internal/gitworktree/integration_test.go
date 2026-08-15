package gitworktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"yoyodyne/internal/execution"
)

func TestManagerIntegratePromotesCheckedWorkAndPermitsCleanup(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	writeFile(t, repository, "doomed.txt", "delete me\n")
	runGit(t, repository, "add", "doomed.txt")
	runGit(t, repository, "commit", "-m", "add doomed file")
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:        testRunID,
		WorkItemID:   "yoyodyne-integrate",
		BaseRef:      "HEAD",
		TargetBranch: "main",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if worktree.TargetBranch != "main" {
		t.Fatalf("recorded target = %q", worktree.TargetBranch)
	}
	writeFile(t, worktree.Path, "README.txt", "test\nedited\n")
	writeFile(t, worktree.Path, filepath.Join("sub", "new.txt"), "brand new\n")
	if err := os.Remove(filepath.Join(worktree.Path, "doomed.txt")); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}

	integration, err := manager.Integrate(context.Background(), worktree, "")
	if err != nil {
		t.Fatalf("Integrate() error = %v", err)
	}
	if integration.PreviousTargetCommit != worktree.BaseCommit {
		t.Errorf("previous target = %q, want base %q", integration.PreviousTargetCommit, worktree.BaseCommit)
	}
	if integration.SourceCommit == worktree.BaseCommit || integration.SourceCommit != integration.TargetCommit {
		t.Errorf("integration commits = %#v", integration)
	}
	if integration.Branch != worktree.Branch || integration.TargetBranch != "main" {
		t.Errorf("integration refs = %#v", integration)
	}
	if head := gitLine(t, repository, "rev-parse", "refs/heads/main"); head != integration.TargetCommit {
		t.Errorf("main = %q, want %q", head, integration.TargetCommit)
	}

	// A fast-forward of the branch the primary checkout is on must leave that
	// checkout consistent with the branch it just moved.
	if content := readFile(t, repository, "README.txt"); content != "test\nedited\n" {
		t.Errorf("primary README.txt = %q", content)
	}
	if content := readFile(t, repository, filepath.Join("sub", "new.txt")); content != "brand new\n" {
		t.Errorf("primary sub/new.txt = %q", content)
	}
	if _, err := os.Stat(filepath.Join(repository, "doomed.txt")); !os.IsNotExist(err) {
		t.Errorf("deleted file survived integration: %v", err)
	}
	if status := gitOutput(t, repository, "status", "--porcelain=v1", "--untracked-files=all"); strings.TrimSpace(status) != "" {
		t.Errorf("primary checkout is dirty after integration: %q", status)
	}
	if author := gitLine(t, repository, "log", "-1", "--format=%an <%ae>", integration.TargetCommit); author != harnessCommitAuthorName+" <"+harnessCommitAuthorEmail+">" {
		t.Errorf("integration commit author = %q, want the harness identity", author)
	}
	if subject := gitLine(t, repository, "log", "-1", "--format=%s", integration.TargetCommit); !strings.Contains(subject, "yoyodyne-integrate") {
		t.Errorf("default commit subject = %q", subject)
	}

	if err := manager.CleanupIntegrated(context.Background(), worktree, worktree.TargetBranch); err != nil {
		t.Fatalf("CleanupIntegrated() after integration error = %v", err)
	}
	if _, err := os.Stat(worktree.Path); !os.IsNotExist(err) {
		t.Errorf("integrated worktree still exists: %v", err)
	}
}

func TestManagerIntegrateAdvancesTargetThatIsNotCheckedOut(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	runGit(t, repository, "branch", "release")
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	mainCommit := gitLine(t, repository, "rev-parse", "refs/heads/main")
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:        testRunID,
		WorkItemID:   "yoyodyne-release",
		BaseRef:      "release",
		TargetBranch: "release",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "release.txt", "released\n")

	integration, err := manager.Integrate(context.Background(), worktree, "yoyodyne: integrate release work")
	if err != nil {
		t.Fatalf("Integrate() error = %v", err)
	}
	if release := gitLine(t, repository, "rev-parse", "refs/heads/release"); release != integration.SourceCommit {
		t.Errorf("release = %q, want %q", release, integration.SourceCommit)
	}
	if head := gitLine(t, repository, "rev-parse", "refs/heads/main"); head != mainCommit {
		t.Errorf("main moved during integration of release: %q", head)
	}
	if subject := gitLine(t, repository, "log", "-1", "--format=%s", integration.SourceCommit); subject != "yoyodyne: integrate release work" {
		t.Errorf("commit subject = %q", subject)
	}
	// The primary checkout is on another branch and must be untouched by an
	// integration that only advanced a ref.
	if _, err := os.Stat(filepath.Join(repository, "release.txt")); !os.IsNotExist(err) {
		t.Errorf("integration wrote into the primary checkout: %v", err)
	}
}

func TestManagerIntegrateRefusesEmptyAndAgentCommittedWork(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	writeFile(t, repository, ".gitignore", "ignored.txt\n")
	runGit(t, repository, "add", ".gitignore")
	runGit(t, repository, "commit", "-m", "ignore build output")
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:        testRunID,
		WorkItemID:   "yoyodyne-empty",
		BaseRef:      "HEAD",
		TargetBranch: "main",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	base := gitLine(t, repository, "rev-parse", "refs/heads/main")

	if _, err := manager.Integrate(context.Background(), worktree, ""); !errors.Is(err, ErrNoChanges) {
		t.Fatalf("Integrate() empty error = %v, want ErrNoChanges", err)
	}
	// An ignored-only worktree has produced nothing integratable either.
	writeFile(t, worktree.Path, "ignored.txt", "not a reviewable change\n")
	if _, err := manager.Integrate(context.Background(), worktree, ""); !errors.Is(err, ErrNoChanges) {
		t.Fatalf("Integrate() ignored-only error = %v, want ErrNoChanges", err)
	}

	writeFile(t, worktree.Path, "new.txt", "new\n")
	runGit(t, worktree.Path, "add", ".")
	runGit(t, worktree.Path, "commit", "-m", "agent must not commit")
	if _, err := manager.Integrate(context.Background(), worktree, ""); err == nil || !strings.Contains(err.Error(), "Git commits are owned by the harness") {
		t.Fatalf("Integrate() developer commit error = %v", err)
	}
	if head := gitLine(t, repository, "rev-parse", "refs/heads/main"); head != base {
		t.Fatalf("main moved during a refused integration: %q", head)
	}
	if _, err := os.Stat(filepath.Join(worktree.Path, "new.txt")); err != nil {
		t.Fatalf("refused worktree was not preserved: %v", err)
	}
}

func TestManagerIntegrateRefusesTargetDriftAndDirtyPrimaryWithoutCommitting(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:        testRunID,
		WorkItemID:   "yoyodyne-drift",
		BaseRef:      "HEAD",
		TargetBranch: "main",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "work.txt", "developer work\n")

	// A dirty primary checkout is refused before anything is committed.
	writeFile(t, repository, "unrelated.txt", "someone else's edit\n")
	if _, err := manager.Integrate(context.Background(), worktree, ""); err == nil || !strings.Contains(err.Error(), "primary checkout is not ready") {
		t.Fatalf("Integrate() dirty primary error = %v", err)
	}
	assertUncommittedWork(t, worktree, "work.txt")

	// The same change is refused once the target has moved on.
	runGit(t, repository, "add", "unrelated.txt")
	runGit(t, repository, "commit", "-m", "concurrent target change")
	drifted := gitLine(t, repository, "rev-parse", "refs/heads/main")
	_, err = manager.Integrate(context.Background(), worktree, "")
	if !errors.Is(err, ErrTargetDrift) {
		t.Fatalf("Integrate() drift error = %v, want ErrTargetDrift", err)
	}
	if !strings.Contains(err.Error(), drifted) || !strings.Contains(err.Error(), worktree.BaseCommit) {
		t.Errorf("drift error does not report both commits: %v", err)
	}
	if head := gitLine(t, repository, "rev-parse", "refs/heads/main"); head != drifted {
		t.Errorf("main = %q, want the drifted commit %q", head, drifted)
	}
	assertUncommittedWork(t, worktree, "work.txt")
}

func TestManagerIntegrateRefusesInvalidOwnershipAndTargets(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:        testRunID,
		WorkItemID:   "yoyodyne-invalid",
		BaseRef:      "HEAD",
		TargetBranch: "main",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "work.txt", "developer work\n")

	tests := []struct {
		name    string
		mutate  func(Worktree) Worktree
		message string
	}{
		{"no target", func(w Worktree) Worktree { w.TargetBranch = ""; return w }, "no recorded integration target"},
		{"qualified ref", func(w Worktree) Worktree { w.TargetBranch = "refs/heads/main"; return w }, "must be a local branch name"},
		{"HEAD", func(w Worktree) Worktree { w.TargetBranch = "HEAD"; return w }, "must be a local branch name"},
		{"traversal", func(w Worktree) Worktree { w.TargetBranch = "../evil"; return w }, "invalid integration target"},
		{"own branch", func(w Worktree) Worktree { w.TargetBranch = w.Branch; return w }, "must differ from the worktree branch"},
		{"missing branch", func(w Worktree) Worktree { w.TargetBranch = "absent"; return w }, "resolve branch absent"},
		{"tampered path", func(w Worktree) Worktree { w.Path = repository; return w }, "owned path"},
		{"tampered branch", func(w Worktree) Worktree { w.Branch = "yoyodyne/other/01234567"; return w }, "owned branch"},
		{"tampered base", func(w Worktree) Worktree { w.BaseCommit = "not-a-commit"; return w }, "base commit is invalid"},
	}
	base := gitLine(t, repository, "rev-parse", "refs/heads/main")
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := manager.Integrate(context.Background(), test.mutate(worktree), "")
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("Integrate() error = %v, want %q", err, test.message)
			}
			if head := gitLine(t, repository, "rev-parse", "refs/heads/main"); head != base {
				t.Fatalf("main moved during a refused integration: %q", head)
			}
		})
	}
	assertUncommittedWork(t, worktree, "work.txt")
}

func TestManagerIntegrateRefusesTargetCheckedOutElsewhere(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	runGit(t, repository, "branch", "release")
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:        testRunID,
		WorkItemID:   "yoyodyne-elsewhere",
		BaseRef:      "release",
		TargetBranch: "release",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "work.txt", "developer work\n")
	foreign := filepath.Join(t.TempDir(), "foreign")
	runGit(t, repository, "worktree", "add", foreign, "release")

	release := gitLine(t, repository, "rev-parse", "refs/heads/release")
	if _, err := manager.Integrate(context.Background(), worktree, ""); err == nil || !strings.Contains(err.Error(), "checked out in another worktree") {
		t.Fatalf("Integrate() foreign checkout error = %v", err)
	}
	if head := gitLine(t, repository, "rev-parse", "refs/heads/release"); head != release {
		t.Errorf("release moved into a foreign checkout: %q", head)
	}
	assertUncommittedWork(t, worktree, "work.txt")
}

func TestManagerIntegratePreservesCommittedWorktreeWhenCommitOrUpdateFails(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	failing := &commandFailureRunner{delegate: execution.OSProcessRunner{}, failOn: "commit"}
	manager, err := New(Options{Runner: failing, RepositoryRoot: repository, WorktreeRoot: filepath.Join(t.TempDir(), "worktrees")})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:        testRunID,
		WorkItemID:   "yoyodyne-failure",
		BaseRef:      "HEAD",
		TargetBranch: "main",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "work.txt", "developer work\n")
	base := gitLine(t, repository, "rev-parse", "refs/heads/main")

	failing.armed = true
	if _, err := manager.Integrate(context.Background(), worktree, ""); err == nil || !strings.Contains(err.Error(), "injected commit failure") {
		t.Fatalf("Integrate() commit failure error = %v", err)
	}
	failing.armed = false
	if head := gitLine(t, repository, "rev-parse", "refs/heads/main"); head != base {
		t.Fatalf("main moved after a failed commit: %q", head)
	}
	if content := readFile(t, worktree.Path, "work.txt"); content != "developer work\n" {
		t.Fatalf("worktree work was lost after a failed commit: %q", content)
	}
	if head := gitLine(t, worktree.Path, "rev-parse", "HEAD"); head != worktree.BaseCommit {
		t.Fatalf("worktree HEAD = %q after a failed commit, want the base", head)
	}
}

func TestManagerIntegrateRefusesNonFastForwardAndKeepsHarnessCommit(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	drifting := &driftingRunner{delegate: execution.OSProcessRunner{}}
	manager, err := New(Options{Runner: drifting, RepositoryRoot: repository, WorktreeRoot: filepath.Join(t.TempDir(), "worktrees")})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:        testRunID,
		WorkItemID:   "yoyodyne-race",
		BaseRef:      "HEAD",
		TargetBranch: "main",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "work.txt", "developer work\n")

	// The target is moved after the drift check but before the update, so only
	// the fast-forward-only update itself can catch it.
	drifting.beforeUpdate = func() {
		writeFile(t, repository, "concurrent.txt", "racing change\n")
		runGit(t, repository, "add", "concurrent.txt")
		runGit(t, repository, "commit", "-m", "racing target change")
	}
	_, err = manager.Integrate(context.Background(), worktree, "")
	if !errors.Is(err, ErrNotFastForward) {
		t.Fatalf("Integrate() race error = %v, want ErrNotFastForward", err)
	}
	racing := gitLine(t, repository, "rev-parse", "refs/heads/main")
	if head := gitLine(t, worktree.Path, "rev-parse", "HEAD"); head == worktree.BaseCommit || head == racing {
		t.Fatalf("harness commit was not preserved on the worktree branch: %q", head)
	}
	if content := readFile(t, repository, "concurrent.txt"); content != "racing change\n" {
		t.Fatalf("racing target change was overwritten: %q", content)
	}
	if status := gitOutput(t, worktree.Path, "status", "--porcelain=v1", "--untracked-files=all"); strings.TrimSpace(status) != "" {
		t.Fatalf("preserved worktree is dirty: %q", status)
	}
}

func TestManagerIntegrateUsesExactCommitAndDisablesAmbientCustomization(t *testing.T) {
	repository := newRepository(t)
	drifting := &driftingRunner{delegate: execution.OSProcessRunner{}}
	manager, err := New(Options{Runner: drifting, RepositoryRoot: repository, WorktreeRoot: filepath.Join(t.TempDir(), "worktrees")})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:        testRunID,
		WorkItemID:   "yoyodyne-exact",
		BaseRef:      "HEAD",
		TargetBranch: "main",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "work.txt", "approved work\n")

	// These normally override user.name/user.email. Harness commits must ignore
	// them and must not execute repository-controlled lifecycle hooks.
	t.Setenv("GIT_AUTHOR_NAME", "Ambient Author")
	t.Setenv("GIT_AUTHOR_EMAIL", "ambient-author@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Ambient Committer")
	t.Setenv("GIT_COMMITTER_EMAIL", "ambient-committer@example.com")
	marker := filepath.Join(t.TempDir(), "hook-ran")
	t.Setenv("YOYODYNE_TEST_HOOK_MARKER", marker)
	for _, name := range []string{"post-commit", "post-merge", "reference-transaction"} {
		hook := filepath.Join(repository, ".git", "hooks", name)
		if err := os.WriteFile(hook, []byte("#!/bin/sh\n: > \"$YOYODYNE_TEST_HOOK_MARKER\"\n"), 0o700); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", name, err)
		}
	}

	var sourceCommit string
	var movedBranchCommit string
	drifting.beforeUpdate = func() {
		sourceCommit = gitLine(t, worktree.Path, "rev-parse", "HEAD")
		movedBranchCommit = gitLine(t, repository, "commit-tree", worktree.BaseCommit+"^{tree}", "-p", worktree.BaseCommit, "-m", "concurrent source ref move")
		runGit(t, repository, "-c", "core.hooksPath="+os.DevNull, "update-ref", "refs/heads/"+worktree.Branch, movedBranchCommit, sourceCommit)
	}

	integration, err := manager.Integrate(context.Background(), worktree, "")
	if err != nil {
		t.Fatalf("Integrate() error = %v", err)
	}
	if integration.SourceCommit != sourceCommit || integration.TargetCommit != sourceCommit {
		t.Fatalf("integration commits = %#v, want exact source %s", integration, sourceCommit)
	}
	if main := gitLine(t, repository, "rev-parse", "refs/heads/main"); main != sourceCommit || main == movedBranchCommit {
		t.Fatalf("main = %q, want exact source %q and not moved branch %q", main, sourceCommit, movedBranchCommit)
	}
	if content := readFile(t, repository, "work.txt"); content != "approved work\n" {
		t.Fatalf("primary work.txt = %q", content)
	}
	if author := gitLine(t, repository, "log", "-1", "--format=%an <%ae>", sourceCommit); author != harnessCommitAuthorName+" <"+harnessCommitAuthorEmail+">" {
		t.Errorf("integration author = %q", author)
	}
	if committer := gitLine(t, repository, "log", "-1", "--format=%cn <%ce>", sourceCommit); committer != harnessCommitAuthorName+" <"+harnessCommitAuthorEmail+">" {
		t.Errorf("integration committer = %q", committer)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("repository hook ran during integration: %v", err)
	}
}

func TestManagerCreateRejectsTargetThatIsNotTheBase(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	runGit(t, repository, "branch", "release")
	writeFile(t, repository, "later.txt", "later\n")
	runGit(t, repository, "add", "later.txt")
	runGit(t, repository, "commit", "-m", "move main past release")
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))

	if _, err := manager.Create(context.Background(), CreateRequest{
		RunID:        testRunID,
		WorkItemID:   "yoyodyne-mismatch",
		BaseRef:      "HEAD",
		TargetBranch: "release",
	}); err == nil || !strings.Contains(err.Error(), "not the base commit") {
		t.Fatalf("Create() mismatched target error = %v", err)
	}
	if _, err := manager.Create(context.Background(), CreateRequest{
		RunID:        testRunID,
		WorkItemID:   "yoyodyne-badtarget",
		BaseRef:      "HEAD",
		TargetBranch: "refs/heads/main",
	}); err == nil || !strings.Contains(err.Error(), "must be a local branch name") {
		t.Fatalf("Create() invalid target error = %v", err)
	}
}

// assertUncommittedWork proves a refused integration left the developer's work
// exactly where it was: uncommitted, in place, and still on the recorded base.
func assertUncommittedWork(t *testing.T, worktree Worktree, relative string) {
	t.Helper()
	if head := gitLine(t, worktree.Path, "rev-parse", "HEAD"); head != worktree.BaseCommit {
		t.Fatalf("worktree HEAD = %q, want the recorded base %q", head, worktree.BaseCommit)
	}
	if _, err := os.Stat(filepath.Join(worktree.Path, relative)); err != nil {
		t.Fatalf("preserved worktree file %s: %v", relative, err)
	}
}

func readFile(t *testing.T, root, relative string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(root, relative))
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", relative, err)
	}
	return string(content)
}

func gitLine(t *testing.T, repository string, args ...string) string {
	t.Helper()
	return strings.TrimSpace(gitOutput(t, repository, args...))
}

// commandFailureRunner injects a failure into one Git subcommand once armed.
type commandFailureRunner struct {
	delegate execution.ProcessRunner
	failOn   string
	armed    bool
}

func (r *commandFailureRunner) Run(ctx context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	if r.armed && hasArgument(command.Args, r.failOn) {
		return execution.ProcessResult{}, errors.New("injected " + r.failOn + " failure")
	}
	return r.delegate.Run(ctx, command, observer)
}

func hasArgument(arguments []string, want string) bool {
	for _, argument := range arguments {
		if argument == want {
			return true
		}
	}
	return false
}

// driftingRunner moves the integration target between the harness commit and
// the fast-forward, exercising the update's own safety rather than the earlier
// drift check.
type driftingRunner struct {
	delegate     execution.ProcessRunner
	beforeUpdate func()
}

func (r *driftingRunner) Run(ctx context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	if r.beforeUpdate != nil && (containsArguments(command.Args, "merge", "--ff-only") || hasArgument(command.Args, "update-ref")) {
		drift := r.beforeUpdate
		r.beforeUpdate = nil
		drift()
	}
	return r.delegate.Run(ctx, command, observer)
}
