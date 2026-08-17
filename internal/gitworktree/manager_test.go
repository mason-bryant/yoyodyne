package gitworktree

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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
	request := CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-1.2", BaseRef: "HEAD", TargetBranch: "main"}

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

	// Cleanup only ever acts on a worktree that really integrated, so this
	// promotes one first rather than passing the base commit off as an
	// integrated one: the base is in the target by construction and would make
	// the containment proof vacuous.
	writeFile(t, worktree.Path, "feature.txt", "implemented\n")
	integration, err := manager.Integrate(context.Background(), worktree, "")
	if err != nil {
		t.Fatalf("Integrate() error = %v", err)
	}

	dirtyPath := filepath.Join(worktree.Path, "failure.txt")
	if err := os.WriteFile(dirtyPath, []byte("preserve me"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	cleanupRequest := CleanupRequest{Worktree: worktree, TargetBranch: "main", SourceCommit: integration.SourceCommit}
	if _, err := manager.CleanupIntegrated(context.Background(), cleanupRequest); err == nil || !strings.Contains(err.Error(), "dirty") {
		t.Fatalf("CleanupIntegrated() dirty error = %v", err)
	}
	if _, err := os.Stat(dirtyPath); err != nil {
		t.Fatalf("dirty worktree was not preserved: %v", err)
	}
	if err := os.Remove(dirtyPath); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}
	cleanup, err := manager.CleanupIntegrated(context.Background(), cleanupRequest)
	if err != nil {
		t.Fatalf("CleanupIntegrated() error = %v", err)
	}
	if !cleanup.Complete() {
		t.Fatalf("CleanupIntegrated() = %#v, want both artifacts removed", cleanup)
	}
	if _, err := os.Stat(worktree.Path); !os.IsNotExist(err) {
		t.Fatalf("worktree still exists: %v", err)
	}
}

func TestManagerCreatesWorktreeFromResolvedBaseCommit(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	runner := &recordingProcessRunner{delegate: execution.OSProcessRunner{}}
	manager, err := New(Options{
		Runner:         runner,
		RepositoryRoot: repository,
		WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	worktree, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-base", BaseRef: "main"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	var worktreeBase string
	for _, command := range runner.commands {
		for index := 0; index+3 < len(command); index++ {
			if command[index] == "worktree" && command[index+1] == "add" {
				worktreeBase = command[len(command)-1]
			}
		}
	}
	if worktreeBase != worktree.BaseCommit {
		t.Fatalf("git worktree base = %q, want resolved commit %q", worktreeBase, worktree.BaseCommit)
	}
}

func TestManagerReturnsCreatedIdentityWhenPostCreateInspectionFails(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	runner := &postCreateFailureRunner{delegate: execution.OSProcessRunner{}}
	manager, err := New(Options{
		Runner:         runner,
		RepositoryRoot: repository,
		WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	worktree, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-partial", BaseRef: "main"})
	if err == nil || !strings.Contains(err.Error(), "verify created worktree") {
		t.Fatalf("Create() error = %v", err)
	}
	if worktree.Path == "" || worktree.Branch == "" || worktree.BaseCommit == "" {
		t.Fatalf("Create() discarded created identity: %#v", worktree)
	}
	if _, err := os.Stat(worktree.Path); err != nil {
		t.Fatalf("created worktree is not recoverable at %s: %v", worktree.Path, err)
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

func TestManagerUnifiedChangesCoversTrackedAndUntrackedWorkWithoutMutating(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-diff", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "README.txt", "test\nedited\n")
	writeFile(t, worktree.Path, "new.txt", "brand new\n")
	writeFile(t, worktree.Path, filepath.Join("sub", "odd name.txt"), "nested\n")
	writeFile(t, worktree.Path, ".gitignore", "ignored.txt\n")
	writeFile(t, worktree.Path, "ignored.txt", "should not be reviewed\n")

	before := gitOutput(t, worktree.Path, "status", "--porcelain=v1", "--untracked-files=all")
	changes, err := manager.UnifiedChanges(context.Background(), worktree, DiffLimits{})
	if err != nil {
		t.Fatalf("UnifiedChanges() error = %v", err)
	}
	for _, want := range []string{
		"diff --git a/README.txt b/README.txt",
		"+edited",
		"diff --git a/new.txt b/new.txt",
		"+brand new",
		"diff --git a/sub/odd name.txt b/sub/odd name.txt",
		"+nested",
	} {
		if !strings.Contains(changes.Patch, want) {
			t.Errorf("patch is missing %q:\n%s", want, changes.Patch)
		}
	}
	if strings.Contains(changes.Patch, "should not be reviewed") {
		t.Errorf("patch includes an ignored file:\n%s", changes.Patch)
	}
	wantUntracked := []string{".gitignore", "new.txt", "sub/odd name.txt"}
	if !reflect.DeepEqual(changes.UntrackedFiles, wantUntracked) {
		t.Errorf("untracked files = %#v, want %#v", changes.UntrackedFiles, wantUntracked)
	}
	if changes.Truncated || len(changes.OmittedFiles) != 0 {
		t.Errorf("changes = %#v, want an untruncated change", changes)
	}
	if !strings.Contains(changes.Status, "?? new.txt") || !strings.Contains(changes.DiffStat, "README.txt") {
		t.Errorf("changes summary = %#v", changes)
	}

	// Inspecting a change must never stage or otherwise alter what the
	// developer left behind.
	if after := gitOutput(t, worktree.Path, "status", "--porcelain=v1", "--untracked-files=all"); after != before {
		t.Errorf("worktree status changed during inspection:\nbefore %q\nafter  %q", before, after)
	}
	if staged := gitOutput(t, worktree.Path, "diff", "--cached", "--name-only"); staged != "" {
		t.Errorf("inspection staged files: %q", staged)
	}
}

func TestManagerUnifiedChangesEnforcesDiffBounds(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-bounds", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "big.txt", strings.Repeat("a line of new content\n", 200))
	writeFile(t, worktree.Path, "small.txt", "small\n")

	perFile, err := manager.UnifiedChanges(context.Background(), worktree, DiffLimits{MaxFileBytes: 64})
	if err != nil {
		t.Fatalf("UnifiedChanges() per-file error = %v", err)
	}
	if !perFile.Truncated || !reflect.DeepEqual(perFile.OmittedFiles, []string{"big.txt"}) {
		t.Fatalf("per-file bound = %#v", perFile)
	}
	if !reflect.DeepEqual(perFile.UntrackedFiles, []string{"small.txt"}) {
		t.Fatalf("per-file untracked = %#v", perFile.UntrackedFiles)
	}
	if strings.Contains(perFile.Patch, "a line of new content") {
		t.Fatalf("patch included an oversized file:\n%s", perFile.Patch)
	}

	counted, err := manager.UnifiedChanges(context.Background(), worktree, DiffLimits{MaxFiles: 1})
	if err != nil {
		t.Fatalf("UnifiedChanges() file-count error = %v", err)
	}
	if len(counted.UntrackedFiles) != 1 || !counted.Truncated || len(counted.OmittedFiles) != 1 {
		t.Fatalf("file-count bound = %#v", counted)
	}

	// A total bound clamps to whole lines rather than cutting a diff mid-line.
	total, err := manager.UnifiedChanges(context.Background(), worktree, DiffLimits{MaxTotalBytes: 200})
	if err != nil {
		t.Fatalf("UnifiedChanges() total error = %v", err)
	}
	if !total.Truncated {
		t.Fatalf("total bound = %#v", total)
	}
	if len(total.Patch) > 200 {
		t.Fatalf("patch is %d bytes, want at most 200", len(total.Patch))
	}
	if total.Patch != "" && !strings.HasSuffix(total.Patch, "\n") {
		t.Fatalf("clamped patch ends mid-line: %q", total.Patch)
	}

	if _, err := manager.UnifiedChanges(context.Background(), worktree, DiffLimits{MaxFiles: -1}); err == nil {
		t.Fatal("UnifiedChanges() negative limit error = nil")
	}
}

func TestManagerUnifiedChangesMarksBinaryContentIncomplete(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-binary", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "README.txt", "changed\x00binary\n")
	writeFile(t, worktree.Path, "new.bin", "new\x00binary\n")

	changes, err := manager.UnifiedChanges(context.Background(), worktree, DiffLimits{})
	if err != nil {
		t.Fatalf("UnifiedChanges() error = %v", err)
	}
	if !changes.Truncated {
		t.Fatalf("binary changes were reported as complete: %#v", changes)
	}
	if !reflect.DeepEqual(changes.OmittedFiles, []string{"new.bin"}) {
		t.Fatalf("omitted files = %#v, want new.bin", changes.OmittedFiles)
	}
	if !strings.Contains(changes.Patch, "Binary files a/README.txt and b/README.txt differ") {
		t.Fatalf("tracked binary change is not disclosed in patch:\n%s", changes.Patch)
	}
	if strings.Contains(changes.Patch, "new.bin") {
		t.Fatalf("unreviewable untracked binary was included in patch:\n%s", changes.Patch)
	}
}

func TestManagerUnifiedChangesRejectsAgentOwnedCommits(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-commit", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "new.txt", "new\n")
	runGit(t, worktree.Path, "add", ".")
	runGit(t, worktree.Path, "commit", "-m", "agent must not commit")

	if _, err := manager.UnifiedChanges(context.Background(), worktree, DiffLimits{}); err == nil || !strings.Contains(err.Error(), "Git commits are owned by the harness") {
		t.Fatalf("UnifiedChanges() committed HEAD error = %v", err)
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

// A test's own teardown is part of what it asserts: a run that passes every
// check and then fails deleting its temporary directory is still a red required
// check, and one that is usually spurious is one people stop reading. Both
// halves of that are guarded here, because both are a line someone can delete
// without any test noticing.
func TestRepositoryUnderTestLeavesNothingForTempDirToRace(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	// Writing commands otherwise detach "git maintenance run --auto", which
	// outlives them and is still working inside .git when TempDir deletes it.
	for _, setting := range []struct{ name, want string }{
		{"maintenance.auto", "false"},
		{"gc.auto", "0"},
	} {
		value, err := attemptGit(repository, "config", "--get", setting.name)
		if err != nil || strings.TrimSpace(value) != setting.want {
			t.Errorf("%s = %q (%v), want %q", setting.name, strings.TrimSpace(value), err, setting.want)
		}
	}

	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	worktree, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-teardown", BaseRef: "HEAD"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "work.txt", "left behind\n")

	// Running the cleanup here proves it removes a worktree the test never
	// integrated, and leaves the registered one at teardown to prove it is
	// idempotent. TempDir deletes directories and unregisters nothing, so a
	// registration it never sees is the state this has to end in.
	removeLinkedWorktrees(t, repository)
	if registrations := gitOutput(t, repository, "worktree", "list", "--porcelain"); strings.Contains(registrations, worktree.Path) {
		t.Errorf("worktree registration survived cleanup: %q", registrations)
	}
	if _, err := os.Stat(worktree.Path); !os.IsNotExist(err) {
		t.Errorf("worktree directory survived cleanup: %v", err)
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
	disableBackgroundMaintenance(t, repository)
	// Registered after the first TempDir call above and therefore run before
	// TempDir's removal, so the repository is idle by the time Go deletes it.
	t.Cleanup(func() { removeLinkedWorktrees(t, repository) })
	if err := os.WriteFile(filepath.Join(repository, "README.txt"), []byte("test\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	runGit(t, repository, "add", "README.txt")
	runGit(t, repository, "commit", "-m", "initial")
	return repository
}

// disableBackgroundMaintenance stops Git from handing this repository to a
// process that outlives the command which started it. Writing commands
// otherwise start "git maintenance run --auto --detach", which daemonizes into
// its own session: it escapes both the caller's wait and the process group the
// runner kills, and it is still working inside .git when a test's TempDir
// cleanup deletes that directory. A directory Go has already emptied and that
// then gains an entry again is exactly what makes the removal fail. Nothing
// under test needs maintenance, so none is started.
func disableBackgroundMaintenance(t *testing.T, repository string) {
	t.Helper()
	runGit(t, repository, "config", "maintenance.auto", "false")
	runGit(t, repository, "config", "gc.auto", "0")
}

// removeLinkedWorktrees makes a test responsible for the worktrees it
// registered. TempDir deletes directories and unregisters nothing, so a
// registration inside .git outlives the checkout it names and leaves the
// repository holding state no test still describes.
func removeLinkedWorktrees(t *testing.T, repository string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(repository, ".git")); err != nil {
		return
	}
	for _, path := range linkedWorktreePaths(t, repository) {
		// A worktree whose directory a test deleted on purpose cannot be
		// removed, only pruned. One that is still on disk must come off here.
		if output, err := attemptGit(repository, "worktree", "remove", "--force", path); err != nil {
			if _, statErr := os.Stat(path); statErr == nil {
				t.Errorf("cleanup could not remove worktree %s: %v: %s", path, err, output)
			}
		}
	}
	if output, err := attemptGit(repository, "worktree", "prune"); err != nil {
		t.Errorf("cleanup could not prune worktree registrations: %v: %s", err, output)
	}
	if remaining := linkedWorktreePaths(t, repository); len(remaining) > 0 {
		t.Errorf("cleanup left worktree registrations behind: %v", remaining)
	}
}

// linkedWorktreePaths names every worktree registered against repository apart
// from the primary checkout, which is the repository itself.
func linkedWorktreePaths(t *testing.T, repository string) []string {
	t.Helper()
	listing, err := attemptGit(repository, "worktree", "list", "--porcelain")
	if err != nil {
		t.Errorf("cleanup could not list worktrees: %v: %s", err, listing)
		return nil
	}
	var paths []string
	for _, line := range strings.Split(listing, "\n") {
		path, found := strings.CutPrefix(strings.TrimSpace(line), "worktree ")
		if !found || samePath(path, repository) {
			continue
		}
		paths = append(paths, path)
	}
	return paths
}

func runGit(t *testing.T, repository string, args ...string) {
	t.Helper()
	if output, err := attemptGit(repository, args...); err != nil {
		t.Fatalf("git %v error = %v: %s", args, err, output)
	}
}

// attemptGit runs a Git command whose failure is the caller's to interpret,
// rather than a test failure at the point of the call.
func attemptGit(repository string, args ...string) (string, error) {
	command := exec.Command("git", append([]string{"-C", repository}, args...)...)
	output, err := command.CombinedOutput()
	return string(output), err
}

func gitOutput(t *testing.T, repository string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repository}, args...)...)
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git %v error = %v", args, err)
	}
	return string(output)
}

func writeFile(t *testing.T, root, relative, content string) {
	t.Helper()
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", relative, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", relative, err)
	}
}

type recordingProcessRunner struct {
	delegate execution.ProcessRunner
	commands [][]string
	// bounds keeps every command as it was issued, so a test can hold Git to the
	// flat deadline that is exactly right for it.
	bounds []execution.Command
}

func (r *recordingProcessRunner) Run(ctx context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	r.commands = append(r.commands, append([]string(nil), command.Args...))
	r.bounds = append(r.bounds, command)
	return r.delegate.Run(ctx, command, observer)
}

type postCreateFailureRunner struct {
	delegate execution.ProcessRunner
	created  bool
}

func (r *postCreateFailureRunner) Run(ctx context.Context, command execution.Command, observer execution.OutputObserver) (execution.ProcessResult, error) {
	if r.created && containsArguments(command.Args, "worktree", "list") {
		return execution.ProcessResult{}, errors.New("injected post-create inspection failure")
	}
	result, err := r.delegate.Run(ctx, command, observer)
	if err == nil && result.Status == execution.ProcessSucceeded && containsArguments(command.Args, "worktree", "add") {
		r.created = true
	}
	return result, err
}

func containsArguments(arguments []string, first, second string) bool {
	for index := 0; index+1 < len(arguments); index++ {
		if arguments[index] == first && arguments[index+1] == second {
			return true
		}
	}
	return false
}

// A Git command's duration is known and short, so a flat deadline is exactly
// the right bound for it. The activity bound that keeps a working provider
// alive would only mistake a quiet Git command for a stalled one.
func TestManagerBoundsGitCommandsByAFlatDeadlineOnly(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	runner := &recordingProcessRunner{delegate: execution.OSProcessRunner{}}
	manager, err := New(Options{
		Runner:         runner,
		RepositoryRoot: repository,
		WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-bounds", BaseRef: "main"}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if len(runner.bounds) == 0 {
		t.Fatal("no Git commands were recorded")
	}
	for _, command := range runner.bounds {
		if command.Timeout <= 0 {
			t.Fatalf("git %v carries no deadline", command.Args)
		}
		if command.IdleTimeout != 0 {
			t.Fatalf("git %v carries an idle bound of %s", command.Args, command.IdleTimeout)
		}
	}
}
