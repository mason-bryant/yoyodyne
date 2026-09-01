package gitworktree

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

const exportPath = ".beads/issues.jsonl"

// The committed export is what a worktree used to get, and the item that forced
// this change was one admitted after it: a run looking for its own dependencies
// found a file that stopped several hundred items short and reported them
// missing rather than unexported.
const (
	committedExport = `{"id":"yoyodyne-ifd.193"}` + "\n"
	currentExport   = `{"id":"yoyodyne-ifd.193"}` + "\n" + `{"id":"yoyodyne-ifd.211"}` + "\n"
	laterExport     = `{"id":"yoyodyne-ifd.193"}` + "\n" + `{"id":"yoyodyne-ifd.211"}` + "\n" + `{"id":"yoyodyne-ifd.223"}` + "\n"
)

func TestCreatedWorktreeCarriesThePrimaryCheckoutsExport(t *testing.T) {
	t.Parallel()

	repository := newExportRepository(t)
	manager := newExportManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	// The store is authoritative outside Git and the primary checkout's copy is
	// kept current from it, so between release cuts it is ahead of every commit.
	writeFile(t, repository, exportPath, currentExport)

	worktree, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-ifd.223", BaseRef: "HEAD", TargetBranch: "main"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if content := readFile(t, worktree.Path, exportPath); content != currentExport {
		t.Fatalf("worktree export = %q, want the primary checkout's current copy %q", content, currentExport)
	}

	// Refreshing it must not put a change in the worktree: everything below is
	// what would otherwise show a derived file to a reviewer and then promote it.
	inspection, err := manager.Inspect(context.Background(), worktree)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if inspection.Dirty {
		t.Fatalf("Inspect() = %#v, want a clean worktree", inspection)
	}
	changed, err := manager.ChangedPaths(context.Background(), worktree)
	if err != nil {
		t.Fatalf("ChangedPaths() error = %v", err)
	}
	if len(changed) != 0 {
		t.Fatalf("ChangedPaths() = %v, want nothing", changed)
	}

	writeFile(t, worktree.Path, "feature.txt", "implemented\n")
	integration, err := manager.Integrate(context.Background(), worktree, "")
	if err != nil {
		t.Fatalf("Integrate() error = %v", err)
	}
	promoted := gitOutput(t, repository, "show", "--name-only", "--format=", integration.SourceCommit)
	if strings.Contains(promoted, exportPath) {
		t.Fatalf("promoted commit touched %s: %s", exportPath, promoted)
	}
	if committed := gitOutput(t, repository, "show", "main:"+exportPath); committed != committedExport {
		t.Fatalf("target's export = %q, want the committed copy %q", committed, committedExport)
	}
}

func TestReplayCrossesATargetThatCommittedANewExport(t *testing.T) {
	t.Parallel()

	repository := newExportRepository(t)
	manager := newExportManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))
	writeFile(t, repository, exportPath, currentExport)

	worktree, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-ifd.223", BaseRef: "main", TargetBranch: "main"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	// A release cut commits the export, which is the one thing that regularly
	// moves the target across a path a worktree is holding out of its change.
	writeFile(t, repository, "released.txt", "cut\n")
	runGit(t, repository, "add", "released.txt", exportPath)
	runGit(t, repository, "commit", "-m", "housekeeping")
	writeFile(t, repository, exportPath, laterExport)

	writeFile(t, worktree.Path, "feature.txt", "implemented\n")
	rebase, err := manager.RebaseOntoTarget(context.Background(), worktree, "")
	if err != nil {
		t.Fatalf("RebaseOntoTarget() error = %v", err)
	}
	if !rebase.Moved() {
		t.Fatalf("RebaseOntoTarget() = %#v, want the change replayed onto the moved target", rebase)
	}
	replayed := worktree
	replayed.BaseCommit = rebase.BaseCommit
	replayed.HarnessCommit = rebase.HeadCommit
	inspection, err := manager.Inspect(context.Background(), replayed)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if inspection.Dirty {
		t.Fatalf("Inspect() = %#v, want a clean worktree after the replay", inspection)
	}
	if content := readFile(t, worktree.Path, exportPath); content != laterExport {
		t.Fatalf("worktree export after the replay = %q, want the primary checkout's copy %q", content, laterExport)
	}
	if committed := gitOutput(t, worktree.Path, "show", "HEAD:"+exportPath); committed != currentExport {
		t.Fatalf("replayed branch's export = %q, want the cut's committed copy %q", committed, currentExport)
	}
	changed, err := manager.ChangedPaths(context.Background(), replayed)
	if err != nil {
		t.Fatalf("ChangedPaths() error = %v", err)
	}
	if len(changed) != 1 || changed[0] != "feature.txt" {
		t.Fatalf("ChangedPaths() = %v, want only the developer's own file", changed)
	}
}

func TestIgnoredExportIsRefreshedWithoutTheIndex(t *testing.T) {
	t.Parallel()

	// A project that syncs its tracker between machines stops committing the
	// export and ignores it instead. Nothing has to hold such a path out of a
	// change, because the project's own rules already do.
	repository := newRepository(t)
	writeFile(t, repository, ".gitignore", exportPath+"\n")
	runGit(t, repository, "add", ".gitignore")
	runGit(t, repository, "commit", "-m", "ignore the export")
	writeFile(t, repository, exportPath, currentExport)
	manager := newExportManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))

	worktree, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-ifd.223", BaseRef: "HEAD", TargetBranch: "main"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if content := readFile(t, worktree.Path, exportPath); content != currentExport {
		t.Fatalf("worktree export = %q, want the primary checkout's current copy %q", content, currentExport)
	}
	inspection, err := manager.Inspect(context.Background(), worktree)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if inspection.Dirty {
		t.Fatalf("Inspect() = %#v, want a clean worktree", inspection)
	}
}

func TestExportNeitherTrackedNorIgnoredIsLeftAlone(t *testing.T) {
	t.Parallel()

	// Copying here would arrive as an untracked file in the developer's change,
	// which is worse to hand a reviewer than an export a few items behind.
	repository := newRepository(t)
	writeFile(t, repository, exportPath, currentExport)
	manager := newExportManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))

	worktree, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-ifd.223", BaseRef: "HEAD", TargetBranch: "main"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	inspection, err := manager.Inspect(context.Background(), worktree)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if inspection.Dirty {
		t.Fatalf("Inspect() = %#v, want a clean worktree", inspection)
	}
	if _, err := os.Stat(filepath.Join(worktree.Path, exportPath)); !os.IsNotExist(err) {
		t.Fatalf("worktree carries %s, which is neither tracked nor ignored there: %v", exportPath, err)
	}
}

func TestAProductThatExportsNothingStillGetsAWorktree(t *testing.T) {
	t.Parallel()

	// The exports to refresh are named once for every product the harness
	// manages, so a product whose tracker writes none of them reaches this on the
	// ordinary worktree-creation path rather than on a branch only a beads
	// project takes. Nothing here has ever written the export, and nothing has
	// created the directory it would live in either — absence is absence wherever
	// along the path it starts.
	repository := newRepository(t)
	manager := newExportManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))

	worktree, err := manager.Create(context.Background(), CreateRequest{RunID: testRunID, WorkItemID: "yoyodyne-ifd.223", BaseRef: "HEAD", TargetBranch: "main"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(worktree.Path, ".beads")); !os.IsNotExist(err) {
		t.Fatalf("worktree carries a .beads directory nobody wrote: %v", err)
	}
	inspection, err := manager.Inspect(context.Background(), worktree)
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if inspection.Dirty {
		t.Fatalf("Inspect() = %#v, want a clean worktree", inspection)
	}
}

func TestNewRefusesACurrentExportThatLeavesTheRepository(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	for _, export := range []string{"../elsewhere.jsonl", "/etc/passwd", ""} {
		_, err := New(Options{
			Runner:         execution.OSProcessRunner{},
			RepositoryRoot: repository,
			WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
			CurrentExports: []string{export},
		})
		if err == nil {
			t.Fatalf("New() with current export %q error = nil, want a refusal", export)
		}
	}
}

// newExportRepository is a repository whose committed export is older than the
// store it comes from, which is what every checkout between release cuts is.
func newExportRepository(t *testing.T) string {
	t.Helper()
	repository := newRepository(t)
	writeFile(t, repository, exportPath, committedExport)
	runGit(t, repository, "add", exportPath)
	runGit(t, repository, "commit", "-m", "export as of the last cut")
	return repository
}

func newExportManager(t *testing.T, repository, worktreeRoot string) *Manager {
	t.Helper()
	manager, err := New(Options{
		Runner:         execution.OSProcessRunner{},
		RepositoryRoot: repository,
		WorktreeRoot:   worktreeRoot,
		// The primary checkout's copy is ahead of its commits by construction, so
		// the same allowance production makes is what lets a run be cut from it.
		AllowedPrimaryChanges: []string{exportPath},
		CurrentExports:        []string{exportPath},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return manager
}
