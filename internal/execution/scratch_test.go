package execution

// Where a run's scratch files go.
//
// The whole point of the directory is that no two runs are handed the same one,
// so what is asserted here is that separation rather than the convenience: two
// runs never collide, nothing lands in the working tree the change is read from,
// and a run id that is not a plain name never becomes a path.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAWorktreeScratchesInItsOwnAdministrativeDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	checkout := filepath.Join(root, "checkout")
	administrative := filepath.Join(checkout, ".git", "worktrees", "one")
	makeDirectory(t, administrative)
	writeFile(t, filepath.Join(administrative, "commondir"), "../..\n")

	worktree := filepath.Join(root, "worktree")
	makeDirectory(t, worktree)
	writeFile(t, filepath.Join(worktree, ".git"), "gitdir: "+administrative+"\n")

	directory, found := ScratchDirectory(worktree, "run-one")
	if !found {
		t.Fatal("ScratchDirectory() found nowhere for a worktree")
	}
	if want := filepath.Join(administrative, "yoyodyne", "scratch", "run-one"); directory != want {
		t.Fatalf("ScratchDirectory() = %q, want the worktree's own %q", directory, want)
	}
	// The repository's Git directory is where the build cache goes, deliberately
	// shared by every worktree. Scratch must not land there: a shared directory is
	// the thing this exists to stop.
	if cache, _ := goBuildCache(worktree); strings.HasPrefix(directory, filepath.Dir(cache)+string(filepath.Separator)) {
		t.Fatalf("ScratchDirectory() = %q, which is under the shared repository directory holding %q", directory, cache)
	}
}

// A checkout that is not a worktree has one Git directory and scratches in it,
// which is still per run: the run id is the last element either way.
func TestACheckoutScratchesInItsGitDirectory(t *testing.T) {
	t.Parallel()

	checkout := t.TempDir()
	makeDirectory(t, filepath.Join(checkout, ".git"))

	directory, found := ScratchDirectory(checkout, "run-one")
	if !found {
		t.Fatal("ScratchDirectory() found nowhere for a checkout")
	}
	if want := filepath.Join(checkout, ".git", "yoyodyne", "scratch", "run-one"); directory != want {
		t.Fatalf("ScratchDirectory() = %q, want %q", directory, want)
	}
}

// The collision this exists for: two runs redirecting a check into a name they
// both picked. They cannot, because the directory they are given differs before
// either of them names anything inside it.
func TestTwoRunsAreNeverGivenTheSameDirectory(t *testing.T) {
	t.Parallel()

	checkout := t.TempDir()
	makeDirectory(t, filepath.Join(checkout, ".git"))

	first, _ := ScratchDirectory(checkout, "run-one")
	second, _ := ScratchDirectory(checkout, "run-two")
	if first == second {
		t.Fatalf("both runs were given %q", first)
	}
}

// Nothing a run writes to its scratch directory may reach the change, so the
// directory is outside the working tree the diff is read from.
func TestScratchIsOutsideTheWorkingTree(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	checkout := filepath.Join(root, "checkout")
	administrative := filepath.Join(checkout, ".git", "worktrees", "one")
	makeDirectory(t, administrative)

	worktree := filepath.Join(root, "worktree")
	makeDirectory(t, worktree)
	writeFile(t, filepath.Join(worktree, ".git"), "gitdir: "+administrative+"\n")

	directory, err := PrepareScratchDirectory(worktree, "run-one")
	if err != nil {
		t.Fatalf("PrepareScratchDirectory() error = %v", err)
	}
	if strings.HasPrefix(directory, worktree+string(filepath.Separator)) {
		t.Fatalf("PrepareScratchDirectory() = %q, which is inside the worktree %q", directory, worktree)
	}
	if _, err := os.Stat(directory); err != nil {
		t.Fatalf("stat the prepared directory: %v", err)
	}
}

// A run resumed by a second process is handed the directory the first one was,
// so preparing it again has to be the same answer rather than a failure.
func TestPreparingTheSameRunTwiceIsTheSameDirectory(t *testing.T) {
	t.Parallel()

	checkout := t.TempDir()
	makeDirectory(t, filepath.Join(checkout, ".git"))

	first, err := PrepareScratchDirectory(checkout, "run-one")
	if err != nil {
		t.Fatalf("PrepareScratchDirectory() error = %v", err)
	}
	writeFile(t, filepath.Join(first, "check.log"), "what the first process wrote\n")
	second, err := PrepareScratchDirectory(checkout, "run-one")
	if err != nil {
		t.Fatalf("PrepareScratchDirectory() again error = %v", err)
	}
	if second != first {
		t.Fatalf("PrepareScratchDirectory() = %q, want the same %q", second, first)
	}
	if _, err := os.Stat(filepath.Join(first, "check.log")); err != nil {
		t.Fatalf("preparing again lost what the run had written: %v", err)
	}
}

// The run id becomes a path element, so anything that is not one is refused
// rather than cleaned up: a caller passing a path here has a bug, and joining it
// would put a run's scratch somewhere nobody named.
func TestARunIdThatIsNotAPlainNameIsRefused(t *testing.T) {
	t.Parallel()

	checkout := t.TempDir()
	makeDirectory(t, filepath.Join(checkout, ".git"))

	for _, id := range []string{"", "   ", ".", "..", "../elsewhere", "one/two", string(filepath.Separator) + "absolute"} {
		if directory, found := ScratchDirectory(checkout, id); found {
			t.Errorf("ScratchDirectory(%q) = %q, want a refusal", id, directory)
		}
		if _, err := PrepareScratchDirectory(checkout, id); err == nil {
			t.Errorf("PrepareScratchDirectory(%q) created a directory for an id that is not a name", id)
		}
	}
}

// A working directory in no repository has no Git directory to hold a scratch
// directory, which is a refusal rather than a guess at somewhere writable.
func TestADirectoryInNoRepositoryHasNoScratchDirectory(t *testing.T) {
	t.Parallel()

	for _, directory := range []string{t.TempDir(), "   "} {
		if scratch, found := ScratchDirectory(directory, "run-one"); found {
			t.Errorf("ScratchDirectory(%q) = %q, want a refusal", directory, scratch)
		}
	}
}

// Nothing removes a run's scratch, because Git does: the directory it goes in
// belongs to one worktree and goes when that worktree does. This is the whole of
// why there is no cleanup step anywhere in the harness for it.
func TestGitRemovesAWorktreeScratchWithTheWorktree(t *testing.T) {
	t.Parallel()

	git, err := exec.LookPath("git")
	if err != nil {
		t.Skipf("git is not installed: %v", err)
	}
	root := t.TempDir()
	checkout := filepath.Join(root, "checkout")
	makeDirectory(t, checkout)
	run := func(directory string, args ...string) {
		t.Helper()
		command := exec.Command(git, args...)
		command.Dir = directory
		command.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@invalid",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@invalid")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
		}
	}
	run(checkout, "init", "--quiet", ".")
	run(checkout, "commit", "--quiet", "--allow-empty", "-m", "base")
	worktree := filepath.Join(root, "worktree")
	run(checkout, "worktree", "add", "--quiet", "-b", "scratch-probe", worktree, "HEAD")

	directory, err := PrepareScratchDirectory(worktree, "run-one")
	if err != nil {
		t.Fatalf("PrepareScratchDirectory() error = %v", err)
	}
	writeFile(t, filepath.Join(directory, "check.log"), "what the run wrote\n")

	// Whatever the run left there is not part of the change: the working tree is
	// clean with the log already written.
	status := exec.Command(git, "status", "--porcelain", "--untracked-files=all")
	status.Dir = worktree
	dirty, err := status.CombinedOutput()
	if err != nil {
		t.Fatalf("git status: %v\n%s", err, dirty)
	}
	if strings.TrimSpace(string(dirty)) != "" {
		t.Fatalf("the scratch directory made the worktree dirty:\n%s", dirty)
	}

	run(checkout, "worktree", "remove", worktree)
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatalf("stat the scratch directory after the worktree was removed = %v, want it gone", err)
	}
}
