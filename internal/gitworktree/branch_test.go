package gitworktree

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// accumulate commits a sequence of edits onto a branch grown from main, which
// is the shape a branch review reads: several commits, made one after another,
// that only add up to something when they are read together.
func accumulate(t *testing.T, repository, branch string, edits ...[2]string) {
	t.Helper()
	runGit(t, repository, "checkout", "-b", branch)
	for _, edit := range edits {
		writeFile(t, repository, edit[0], edit[1])
		runGit(t, repository, "add", edit[0])
		runGit(t, repository, "commit", "-m", "add "+edit[0])
	}
	runGit(t, repository, "checkout", "main")
}

func TestBranchChangesDescribesEveryCommitAndTheWholeRange(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	base := gitLine(t, repository, "rev-parse", "HEAD")
	accumulate(t, repository, "accumulated",
		[2]string{"first.txt", "one\n"},
		[2]string{"second.txt", "two\n"},
		[2]string{"third.txt", "three\n"},
	)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))

	change, err := manager.BranchChanges(context.Background(), BranchRequest{Branch: "accumulated", BaseRef: "main"}, DiffLimits{})
	if err != nil {
		t.Fatalf("BranchChanges() error = %v", err)
	}
	if change.BaseCommit != base {
		t.Errorf("base commit = %q, want %q", change.BaseCommit, base)
	}
	if change.HeadCommit != gitLine(t, repository, "rev-parse", "refs/heads/accumulated") {
		t.Errorf("head commit = %q", change.HeadCommit)
	}
	// Oldest first, and all of them: a cross-commit finding is read out of the
	// order the commits were made in.
	if len(change.Commits) != 3 {
		t.Fatalf("commits = %#v", change.Commits)
	}
	for index, want := range []string{"add first.txt", "add second.txt", "add third.txt"} {
		if change.Commits[index].Subject != want {
			t.Errorf("commit %d subject = %q, want %q", index, change.Commits[index].Subject, want)
		}
	}
	if change.CommitsOmitted != 0 || change.Changes.Truncated {
		t.Errorf("complete change reported as bounded: %#v", change)
	}
	// One patch over the whole range, not one per commit.
	for _, want := range []string{"first.txt", "second.txt", "third.txt"} {
		if !strings.Contains(change.Changes.Patch, "b/"+want) {
			t.Errorf("patch is missing %q", want)
		}
		if !strings.Contains(change.Changes.Status, want) {
			t.Errorf("status is missing %q", want)
		}
	}
	if !strings.Contains(change.Changes.DiffStat, "3 files changed") {
		t.Errorf("diff stat = %q", change.Changes.DiffStat)
	}
}

func TestBranchChangesBoundsALargeAccumulatedChange(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	accumulate(t, repository, "accumulated",
		[2]string{"first.txt", strings.Repeat("one\n", 400)},
		[2]string{"second.txt", strings.Repeat("two\n", 400)},
	)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))

	// The bound holds over the whole multi-commit patch exactly as it does over
	// one worktree's, and what it cut is reported rather than passed off as the
	// complete change.
	change, err := manager.BranchChanges(context.Background(), BranchRequest{Branch: "accumulated", BaseRef: "main"}, DiffLimits{MaxTotalBytes: 200})
	if err != nil {
		t.Fatalf("BranchChanges() error = %v", err)
	}
	if !change.Changes.Truncated {
		t.Fatalf("a clamped patch was not reported as truncated: %#v", change.Changes)
	}
	if len(change.Changes.Patch) > 200 {
		t.Fatalf("patch is %d bytes, want no more than 200", len(change.Changes.Patch))
	}
	if !strings.HasSuffix(change.Changes.Patch, "\n") {
		t.Fatalf("clamped patch does not end on a whole line: %q", change.Changes.Patch)
	}
}

func TestBranchChangesBoundsTheDescribedHistory(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	accumulate(t, repository, "accumulated",
		[2]string{"first.txt", "one\n"},
		[2]string{"second.txt", "two\n"},
		[2]string{"third.txt", "three\n"},
	)
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))

	change, err := manager.BranchChanges(context.Background(), BranchRequest{Branch: "accumulated", BaseRef: "main"}, DiffLimits{MaxCommits: 2})
	if err != nil {
		t.Fatalf("BranchChanges() error = %v", err)
	}
	// The most recent commits are kept, the older one is counted, and a history
	// the reviewer cannot see makes the change incomplete in the same way an
	// unshown patch does.
	if len(change.Commits) != 2 || change.Commits[0].Subject != "add second.txt" || change.Commits[1].Subject != "add third.txt" {
		t.Fatalf("described commits = %#v", change.Commits)
	}
	if change.CommitsOmitted != 1 {
		t.Errorf("omitted commits = %d, want 1", change.CommitsOmitted)
	}
	if !change.Changes.Truncated {
		t.Error("a partly described history was not reported as truncated")
	}
}

func TestBranchChangesRefusesWhatIsNotAnAccumulatedChange(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	accumulate(t, repository, "accumulated", [2]string{"first.txt", "one\n"})
	// main moves away from the branch, so it is no longer the commit the branch
	// was grown from.
	writeFile(t, repository, "unrelated.txt", "elsewhere\n")
	runGit(t, repository, "add", "unrelated.txt")
	runGit(t, repository, "commit", "-m", "move main on")
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))

	if _, err := manager.BranchChanges(context.Background(), BranchRequest{Branch: "accumulated", BaseRef: "main"}, DiffLimits{}); err == nil ||
		!strings.Contains(err.Error(), "is not an ancestor of branch") {
		t.Fatalf("BranchChanges() over a moved base error = %v", err)
	}
	// A branch that is its own base accumulated nothing, which is a refusal
	// rather than an empty review.
	if _, err := manager.BranchChanges(context.Background(), BranchRequest{Branch: "accumulated", BaseRef: "accumulated"}, DiffLimits{}); !errors.Is(err, ErrNoAccumulatedChange) {
		t.Fatalf("BranchChanges() over an empty range error = %v", err)
	}
	for _, request := range []BranchRequest{
		{Branch: "", BaseRef: "main"},
		{Branch: "refs/heads/accumulated", BaseRef: "main"},
		{Branch: "HEAD", BaseRef: "main"},
		{Branch: "accumulated", BaseRef: ""},
		{Branch: "accumulated", BaseRef: "main..accumulated"},
	} {
		if _, err := manager.BranchChanges(context.Background(), request, DiffLimits{}); err == nil ||
			!strings.Contains(err.Error(), "invalid branch change request") {
			t.Errorf("BranchChanges(%#v) error = %v, want a rejected request", request, err)
		}
	}
}

func TestBranchChangesDescribesMergedWork(t *testing.T) {
	t.Parallel()

	// A branch that accumulated its work through merges is the ordinary case
	// once publishing is on, and its merge commits are part of what it carries.
	repository := newRepository(t)
	accumulate(t, repository, "one", [2]string{"first.txt", "one\n"})
	accumulate(t, repository, "two", [2]string{"second.txt", "two\n"})
	base := gitLine(t, repository, "rev-parse", "HEAD")
	runGit(t, repository, "checkout", "-b", "accumulated")
	for _, branch := range []string{"one", "two"} {
		runGit(t, repository, "merge", "--no-ff", "-m", fmt.Sprintf("merge %s", branch), branch)
	}
	runGit(t, repository, "checkout", "main")
	manager := newManager(t, repository, filepath.Join(t.TempDir(), "worktrees"))

	change, err := manager.BranchChanges(context.Background(), BranchRequest{Branch: "accumulated", BaseRef: base}, DiffLimits{})
	if err != nil {
		t.Fatalf("BranchChanges() error = %v", err)
	}
	// Two merges and the two commits they brought in, none of them dropped, and
	// the count beside them agreeing with the list.
	if len(change.Commits) != 4 || change.CommitsOmitted != 0 {
		t.Fatalf("described commits = %#v (omitted %d)", change.Commits, change.CommitsOmitted)
	}
	if !strings.Contains(change.Changes.Patch, "b/first.txt") || !strings.Contains(change.Changes.Patch, "b/second.txt") {
		t.Errorf("patch does not carry the merged work: %q", change.Changes.Patch)
	}
}
