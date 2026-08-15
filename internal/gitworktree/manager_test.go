package gitworktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"yoyodyne/internal/execution"
)

const testRunID = "run-0123456789abcdef0123456789abcdef"

func TestManagerCreateInspectPreserveAndCleanup(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	worktreeRoot := filepath.Join(t.TempDir(), "worktrees")
	manager := newManager(t, repository, worktreeRoot)
	request := CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-1.2", BaseRef: "HEAD"}

	worktree, err := manager.Create(context.Background(), request)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if worktree.Branch != "yoyodyne/yoyodyne-1-2/01234567" {
		t.Fatalf("branch = %q", worktree.Branch)
	}
	if !commitPattern.MatchString(worktree.BaseCommit) {
		t.Fatalf("base commit = %q", worktree.BaseCommit)
	}
	inspection, err := manager.Inspect(context.Background(), worktree)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if !inspection.Registered || inspection.Dirty || inspection.Branch != worktree.Branch {
		t.Fatalf("Inspect() = %#v", inspection)
	}

	dirtyPath := filepath.Join(worktree.Path, "failure.txt")
	if err := os.WriteFile(dirtyPath, []byte("preserve me"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := manager.CleanupIntegrated(context.Background(), worktree, "main"); err == nil || !strings.Contains(err.Error(), "dirty") {
		t.Fatalf("CleanupIntegrated() dirty error = %v", err)
	}
	if _, err := os.Stat(dirtyPath); err != nil {
		t.Fatalf("dirty worktree was not preserved: %v", err)
	}
	if err := os.Remove(dirtyPath); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	if err := manager.CleanupIntegrated(context.Background(), worktree, "main"); err != nil {
		t.Fatalf("CleanupIntegrated() error = %v", err)
	}
	if _, err := os.Stat(worktree.Path); !os.IsNotExist(err) {
		t.Fatalf("worktree still exists: %v", err)
	}
}

func TestManagerRejectsReuseAndDirtyPrimaryRepository(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	request := CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-1", BaseRef: "HEAD"}
	if _, err := manager.Create(context.Background(), request); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := manager.Create(context.Background(), request); err == nil || (!strings.Contains(err.Error(), "already exists") && !strings.Contains(err.Error(), "uncommitted")) {
		t.Fatalf("second Create() error = %v", err)
	}

	repository = newRepository(t)
	manager = newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	if err := os.WriteFile(filepath.Join(repository, "dirty.txt"), []byte("dirty"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := manager.Create(context.Background(), request); err == nil || !strings.Contains(err.Error(), "uncommitted") {
		t.Fatalf("Create() dirty error = %v", err)
	}
}

func TestManagerAllowsOnlyConfiguredPrimaryControlPlaneChanges(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager, err := New(Options{
		Runner:                execution.OSProcessRunner{},
		RepositoryRoot:        repository,
		WorktreeRoot:          filepath.Join(t.TempDir(), "worktrees"),
		AllowedPrimaryChanges: []string{".beads/interactions.jsonl", ".beads/issues.jsonl"},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(repository, ".beads"), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(repository, ".beads", "interactions.jsonl"), []byte("base control state\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() base control state error = %v", err)
	}
	runGit(t, repository, "add", ".beads/interactions.jsonl")
	runGit(t, repository, "commit", "-m", "add control state")
	for _, name := range []string{"interactions.jsonl", "issues.jsonl"} {
		if err := os.WriteFile(filepath.Join(repository, ".beads", name), []byte("control state\n"), 0o600); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", name, err)
		}
	}
	if err := manager.ValidateReady(context.Background()); err != nil {
		t.Fatalf("ValidateReady() allowed changes error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(repository, "product.go"), []byte("product change\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() product error = %v", err)
	}
	if err := manager.ValidateReady(context.Background()); err == nil || !strings.Contains(err.Error(), "product.go") {
		t.Fatalf("ValidateReady() product error = %v", err)
	}
}

func TestManagerSummarizeChangesIncludesTrackedAndUntrackedFiles(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:      testRunID,
		WorkItemID: "yoyodyne-task",
		BaseRef:    "HEAD",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktree.Path, "README.txt"), []byte("changed\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() tracked error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktree.Path, "new.txt"), []byte("new\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() untracked error = %v", err)
	}

	summary, err := manager.SummarizeChanges(context.Background(), worktree)
	if err != nil {
		t.Fatalf("SummarizeChanges() error = %v", err)
	}
	if !strings.Contains(summary.Status, "M README.txt") || !strings.Contains(summary.Status, "?? new.txt") {
		t.Fatalf("status = %q", summary.Status)
	}
	if !strings.Contains(summary.DiffStat, "README.txt") {
		t.Fatalf("diff stat = %q", summary.DiffStat)
	}

	runGit(t, worktree.Path, "add", ".")
	runGit(t, worktree.Path, "commit", "-m", "agent must not commit")
	if _, err := manager.SummarizeChanges(context.Background(), worktree); err == nil || !strings.Contains(err.Error(), "Git commits are owned by the harness") {
		t.Fatalf("SummarizeChanges() committed HEAD error = %v", err)
	}
}

func TestManagerRejectsUnsafeRootsAndTamperedOwnership(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	runner := execution.OSProcessRunner{}
	for _, root := range []string{string(filepath.Separator), filepath.Join(repository, "worktrees"), filepath.Dir(repository)} {
		if _, err := New(Options{Runner: runner, RepositoryRoot: repository, WorktreeRoot: root}); err == nil {
			t.Errorf("New() root %q error = nil", root)
		}
	}

	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	request := CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-1", BaseRef: "HEAD"}
	worktree, err := manager.Create(context.Background(), request)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	tampered := worktree
	tampered.Path = repository
	if _, err := manager.Inspect(context.Background(), tampered); err == nil || !strings.Contains(err.Error(), "owned path") {
		t.Fatalf("Inspect() tampered error = %v", err)
	}
	tampered = worktree
	tampered.Branch = "main"
	if _, err := manager.Inspect(context.Background(), tampered); err == nil || !strings.Contains(err.Error(), "owned branch") {
		t.Fatalf("Inspect() branch error = %v", err)
	}
}

func newManager(t *testing.T, repository, worktreeRoot string) *Manager {
	t.Helper()
	manager, err := New(Options{
		Runner:         execution.OSProcessRunner{},
		RepositoryRoot: repository,
		WorktreeRoot:   worktreeRoot,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return manager
}

func newRepository(t *testing.T) string {
	t.Helper()
	repository := filepath.Join(t.TempDir(), "repository")
	if err := os.MkdirAll(repository, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	runGit(t, repository, "init", "-b", "main")
	runGit(t, repository, "config", "user.name", "Yoyodyne Test")
	runGit(t, repository, "config", "user.email", "yoyodyne@example.invalid")
	if err := os.WriteFile(filepath.Join(repository, "README.txt"), []byte("test\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	runGit(t, repository, "add", "README.txt")
	runGit(t, repository, "commit", "-m", "initial")
	return repository
}

func runGit(t *testing.T, repository string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repository}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v error = %v: %s", args, err, output)
	}
}
