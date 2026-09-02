package execution

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// A run writes files its work needs and its change must not carry: the log it
// redirects a check into, a listing it means to diff against a second listing.
// Neither obvious place is one it can use. Inside the worktree is untracked
// content in the change, which every reviewer is then shown; and the machine's
// temporary directory is one directory per user rather than per run -- Claude
// Code sets TMPDIR itself in sandbox mode, so a TMPDIR the harness passed in
// would be replaced before the run's first command -- which means every run
// working beside this one is handed the same directory and an obvious name for a
// log is obvious to all of them.
//
// That second one has already cost a round. On 2026-09-01 two runs five seconds
// apart redirected `make check` into $TMPDIR/probe-check.log; one of them read
// the other's compile error back out of it and reported a broken toolchain while
// its own check was exiting 0.
// `docs/diagnoses/yoyodyne-ifd-238-probe-verdict-crosstalk.md` is the whole of
// it. What stood against that afterwards was prose asking every run to put the
// id of its work item into every scratch name, which holds exactly as far as
// each run's reading of it.
//
// So the harness cuts each run its own directory and names that directory in the
// developer contract. Two runs cannot read each other's scratch output because
// they are never handed the same directory, which is a fact about the path
// rather than about whether a run followed a convention.
//
// Where it goes is decided by the same two constraints as the build cache beside
// it. It is outside the working tree, so nothing written there can enter the
// change or leave the tree dirty; and it is inside the Git directory, which is
// one of the three places a run's sandbox actually grants -- the worktree, the
// repository's Git directory, and the temporary directory. The harness's own
// state tree is none of those: a directory cut there is one the run is refused
// at its first write, which is why this is not where a reader might expect the
// harness to keep it.
//
// The worktree's own administrative directory rather than the repository's is
// the other half. Git creates it for that worktree and removes it with that
// worktree, so a run's scratch goes when its worktree goes and nothing has to
// remember to delete it. Nothing here is a repository-scoped write in the sense
// internal/repowrite exists for: what lands here is never repository content,
// never part of a diff, and never a document a role owns.
const scratchDirectoryName = "scratch"

// ScratchDirectory is where the run named by runID may write while it works in
// workingDirectory, and whether there is anywhere at all: a working directory in
// no repository has no Git directory to hold one.
//
// The run id is in the path even though the directory above it already belongs
// to one worktree. It costs nothing, it says which run a directory belongs to
// wherever somebody finds it, and it is what makes two runs' scratch distinct
// even where they were somehow given one Git directory to share.
func ScratchDirectory(workingDirectory, runID string) (string, bool) {
	id := strings.TrimSpace(runID)
	// The id becomes a path element, so it has to be one: anything that could
	// climb out of the directory below is refused rather than cleaned up, because
	// a run id that is not a plain name is a caller's mistake and not a path.
	if id == "" || id != filepath.Base(id) || id == "." || id == ".." {
		return "", false
	}
	gitDirectory, found := WorktreeGitDirectory(workingDirectory)
	if !found {
		return "", false
	}
	return filepath.Join(gitDirectory, harnessGitSubdirectory, scratchDirectoryName, id), true
}

// PrepareScratchDirectory creates that directory and reports where it is.
//
// It creates, where the build cache beside it only names a path and lets the Go
// command make it. The difference is who is told: a tool asked for a cache makes
// its own, and an agent handed a directory in its contract has to find one there
// -- a contract naming a path that does not exist is a contract inviting the run
// to go and pick somewhere else.
func PrepareScratchDirectory(workingDirectory, runID string) (string, error) {
	directory, found := ScratchDirectory(workingDirectory, runID)
	if !found {
		return "", fmt.Errorf("no Git directory holds a scratch directory for run %q working in %q", runID, workingDirectory)
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", fmt.Errorf("create the scratch directory for run %s: %w", runID, err)
	}
	return directory, nil
}
