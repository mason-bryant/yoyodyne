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

// Publishing is the whole life of a run's branch on a remote: the harness
// commits and pushes each developer attempt, promotes the work locally, checks
// that the remote target may still be merged into and confirms where the
// forge's merge left it, and then removes the published branch on the same
// evidence it removes the local one.
func TestManagerPublishesEachAttemptAndThenObservesTheMerge(t *testing.T) {
	t.Parallel()

	repository, remote := newPublishedRepository(t)
	manager := newRemoteManager(t, repository, filepath.Join(t.TempDir(), "worktrees"), "origin")
	configured, err := manager.RemoteConfigured(context.Background())
	if err != nil || !configured {
		t.Fatalf("RemoteConfigured() = %t, %v", configured, err)
	}
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:        testRunID,
		WorkItemID:   "yoyodyne-publish",
		BaseRef:      "HEAD",
		TargetBranch: "main",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	writeFile(t, worktree.Path, "feature.txt", "first attempt\n")
	first, err := manager.PublishBranch(context.Background(), worktree, "")
	if err != nil {
		t.Fatalf("PublishBranch() first error = %v", err)
	}
	if first.Remote != "origin" || first.Branch != worktree.Branch || first.Commit == worktree.BaseCommit {
		t.Fatalf("first publication = %#v", first)
	}
	if published := gitLine(t, remote, "rev-parse", "refs/heads/"+worktree.Branch); published != first.Commit {
		t.Errorf("remote branch = %q, want the published commit %q", published, first.Commit)
	}
	// The published commit is the harness's, not the developer's: the developer
	// never gets to author history even when its work is what is being pushed.
	if author := gitLine(t, repository, "log", "-1", "--format=%an <%ae>", first.Commit); author != harnessCommitAuthorName+" <"+harnessCommitAuthorEmail+">" {
		t.Errorf("published commit author = %q, want the harness identity", author)
	}

	// The caller carries the commit forward: it is what permits the worktree's
	// HEAD to have moved, and every later inspection accepts that commit alone.
	worktree.HarnessCommit = first.Commit

	// A repair attempt publishes onto the same branch rather than starting over,
	// so the pull request that was opened for it keeps describing this change.
	writeFile(t, worktree.Path, "feature.txt", "second attempt\n")
	second, err := manager.PublishBranch(context.Background(), worktree, "")
	if err != nil {
		t.Fatalf("PublishBranch() second error = %v", err)
	}
	if second.Commit == first.Commit {
		t.Fatalf("second publication reused the first commit %q", second.Commit)
	}
	if published := gitLine(t, remote, "rev-parse", "refs/heads/"+worktree.Branch); published != second.Commit {
		t.Errorf("remote branch = %q, want the republished commit %q", published, second.Commit)
	}
	worktree.HarnessCommit = second.Commit

	// The change is already committed by publishing, so integration promotes the
	// branch tip rather than making a second commit of the same work.
	integration, err := manager.Integrate(context.Background(), worktree, "")
	if err != nil {
		t.Fatalf("Integrate() error = %v", err)
	}
	if integration.SourceCommit != second.Commit || integration.TargetCommit != second.Commit {
		t.Fatalf("integration = %#v, want the published commit %q", integration, second.Commit)
	}
	if integration.PreviousTargetCommit != worktree.BaseCommit {
		t.Errorf("previous target = %q, want the base %q", integration.PreviousTargetCommit, worktree.BaseCommit)
	}

	// The forge is what merges, so what the manager contributes is the check that
	// it may: the remote target must still carry what the promotion was written
	// against, or the merge would reconcile work this run never saw.
	if err := manager.VerifyRemoteTarget(context.Background(), integration); err != nil {
		t.Fatalf("VerifyRemoteTarget() error = %v", err)
	}
	// What a forge merge leaves behind is its own merge commit above the promoted
	// commit — not the promoted commit itself, which no merge method produces.
	mergeCommit := mergeInRemote(t, remote, "main", integration.TargetCommit)
	confirmed, err := manager.ConfirmRemoteTarget(context.Background(), integration)
	if err != nil {
		t.Fatalf("ConfirmRemoteTarget() error = %v", err)
	}
	if confirmed != mergeCommit || confirmed == integration.TargetCommit {
		t.Fatalf("ConfirmRemoteTarget() = %q, want the forge's merge commit %q", confirmed, mergeCommit)
	}
	if err := manager.DeleteRemoteBranch(context.Background(), worktree, integration.SourceCommit); err != nil {
		t.Fatalf("DeleteRemoteBranch() error = %v", err)
	}
	if refs := gitOutput(t, remote, "for-each-ref", "--format=%(refname)", "refs/heads/"+worktree.Branch); strings.TrimSpace(refs) != "" {
		t.Errorf("merged remote branch survived: %q", refs)
	}
	// Deleting a branch a previous attempt already deleted is how a resumed
	// cleanup finishes, so it must report success rather than failing.
	if err := manager.DeleteRemoteBranch(context.Background(), worktree, integration.SourceCommit); err != nil {
		t.Fatalf("DeleteRemoteBranch() repeated error = %v", err)
	}
	if _, err := manager.CleanupIntegrated(context.Background(), CleanupRequest{
		Worktree:     worktree,
		TargetBranch: worktree.TargetBranch,
		SourceCommit: integration.SourceCommit,
	}); err != nil {
		t.Fatalf("CleanupIntegrated() error = %v", err)
	}
}

// A repository with no remote is the case publishing has to degrade for: it is
// an observation about the repository, never a failure.
func TestManagerRemoteConfiguredReportsAnAbsentRemote(t *testing.T) {
	t.Parallel()

	repository := newRepository(t)
	local := newRemoteManager(t, repository, filepath.Join(t.TempDir(), "worktrees"), "origin")
	configured, err := local.RemoteConfigured(context.Background())
	if err != nil {
		t.Fatalf("RemoteConfigured() error = %v", err)
	}
	if configured {
		t.Fatal("RemoteConfigured() = true for a repository with no remote")
	}

	published, _ := newPublishedRepository(t)
	other := newRemoteManager(t, published, filepath.Join(t.TempDir(), "worktrees"), "upstream")
	configured, err = other.RemoteConfigured(context.Background())
	if err != nil {
		t.Fatalf("RemoteConfigured() other error = %v", err)
	}
	if configured {
		t.Fatal("RemoteConfigured() = true for a remote name the repository does not have")
	}
}

func TestManagerPublishBranchRefusesDeveloperCommitsAndEmptyChanges(t *testing.T) {
	t.Parallel()

	repository, _ := newPublishedRepository(t)
	manager := newRemoteManager(t, repository, filepath.Join(t.TempDir(), "worktrees"), "origin")
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:        testRunID,
		WorkItemID:   "yoyodyne-refuse",
		BaseRef:      "HEAD",
		TargetBranch: "main",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// Nothing to publish is a refusal rather than an empty push, for the same
	// reason an empty integration is: it would claim work that does not exist.
	if _, err := manager.PublishBranch(context.Background(), worktree, ""); !errors.Is(err, ErrNoChanges) {
		t.Fatalf("PublishBranch() clean worktree error = %v, want ErrNoChanges", err)
	}

	writeFile(t, worktree.Path, "feature.txt", "developer work\n")
	runGit(t, worktree.Path, "add", ".")
	runGit(t, worktree.Path, "commit", "-m", "agent must not commit")
	if _, err := manager.PublishBranch(context.Background(), worktree, ""); err == nil || !strings.Contains(err.Error(), "Git commits are owned by the harness") {
		t.Fatalf("PublishBranch() developer commit error = %v", err)
	}
	// The same commit must keep integration and review out of reach, so the
	// movement publishing needed cannot become a way for an agent to commit.
	if _, err := manager.Integrate(context.Background(), worktree, ""); err == nil || !strings.Contains(err.Error(), "Git commits are owned by the harness") {
		t.Fatalf("Integrate() developer commit error = %v", err)
	}
	if _, err := manager.UnifiedChanges(context.Background(), worktree, DiffLimits{}); err == nil || !strings.Contains(err.Error(), "Git commits are owned by the harness") {
		t.Fatalf("UnifiedChanges() developer commit error = %v", err)
	}
}

// The harness identity is a constant in this repository and the developer has a
// shell in the worktree, so a commit that imitates it is trivial to make. What
// refuses it is that the permitted commit is a hash durable state named in
// advance, which an agent cannot mint.
func TestManagerRefusesACommitImitatingTheHarnessIdentity(t *testing.T) {
	t.Parallel()

	repository, _ := newPublishedRepository(t)
	manager := newRemoteManager(t, repository, filepath.Join(t.TempDir(), "worktrees"), "origin")
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:        testRunID,
		WorkItemID:   "yoyodyne-imitate",
		BaseRef:      "HEAD",
		TargetBranch: "main",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// Exactly what an agent with a shell can do, identity and all.
	writeFile(t, worktree.Path, "feature.txt", "smuggled\n")
	runGit(t, worktree.Path, "add", ".")
	runGit(t, worktree.Path,
		"-c", "user.name="+harnessCommitAuthorName,
		"-c", "user.email="+harnessCommitAuthorEmail,
		"commit", "-m", "yoyodyne: imitating the harness")
	if author := gitLine(t, worktree.Path, "log", "-1", "--format=%ae %ce"); author != harnessCommitAuthorEmail+" "+harnessCommitAuthorEmail {
		t.Fatalf("the imitated commit does not carry the harness identity: %q", author)
	}

	for name, call := range map[string]func() error{
		"publish": func() error {
			_, err := manager.PublishBranch(context.Background(), worktree, "")
			return err
		},
		"integrate": func() error {
			_, err := manager.Integrate(context.Background(), worktree, "")
			return err
		},
		"review": func() error {
			_, err := manager.UnifiedChanges(context.Background(), worktree, DiffLimits{})
			return err
		},
		"summarize": func() error {
			_, err := manager.SummarizeChanges(context.Background(), worktree)
			return err
		},
	} {
		if err := call(); err == nil || !strings.Contains(err.Error(), "want the commit the harness recorded") {
			t.Errorf("%s accepted an imitated harness commit: err = %v", name, err)
		}
	}

	// A worktree that recorded a real harness commit still refuses a different
	// one, so an agent cannot commit on top of published work either.
	writeFile(t, worktree.Path, "second.txt", "published\n")
	worktree.HarnessCommit = gitLine(t, worktree.Path, "rev-parse", "HEAD")
	runGit(t, worktree.Path, "add", ".")
	runGit(t, worktree.Path,
		"-c", "user.name="+harnessCommitAuthorName,
		"-c", "user.email="+harnessCommitAuthorEmail,
		"commit", "-m", "yoyodyne: one more")
	if _, err := manager.Integrate(context.Background(), worktree, ""); err == nil || !strings.Contains(err.Error(), "want the commit the harness recorded") {
		t.Fatalf("Integrate() commit past the recorded one error = %v", err)
	}
}

// A remote branch that moved away from what the harness published is never
// forced back. Deletion is a compare-and-swap on the published commit for the
// same reason the local one is.
func TestManagerDeleteRemoteBranchRefusesAnUnexpectedCommit(t *testing.T) {
	t.Parallel()

	repository, remote := newPublishedRepository(t)
	manager := newRemoteManager(t, repository, filepath.Join(t.TempDir(), "worktrees"), "origin")
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:        testRunID,
		WorkItemID:   "yoyodyne-drift",
		BaseRef:      "HEAD",
		TargetBranch: "main",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "feature.txt", "published\n")
	publication, err := manager.PublishBranch(context.Background(), worktree, "")
	if err != nil {
		t.Fatalf("PublishBranch() error = %v", err)
	}
	if err := manager.DeleteRemoteBranch(context.Background(), worktree, worktree.BaseCommit); err == nil || !strings.Contains(err.Error(), "want the published commit") {
		t.Fatalf("DeleteRemoteBranch() drifted error = %v", err)
	}
	if published := gitLine(t, remote, "rev-parse", "refs/heads/"+worktree.Branch); published != publication.Commit {
		t.Errorf("remote branch = %q, want the untouched published commit %q", published, publication.Commit)
	}
}

// The remote target a published run leaves behind is the forge's merge commit,
// which this repository does not have and never will: the harness does not
// carry the forge's commits onto the local branch. The run after it has to be
// able to merge anyway, or publication works exactly once.
func TestManagerVerifyRemoteTargetAcceptsTheForgesOwnMergeCommit(t *testing.T) {
	t.Parallel()

	repository, remote := newPublishedRepository(t)
	manager := newRemoteManager(t, repository, filepath.Join(t.TempDir(), "worktrees"), "origin")
	first := publishAndIntegrate(t, manager, "yoyodyne-first", "first.txt", "first\n")
	if err := manager.VerifyRemoteTarget(context.Background(), first); err != nil {
		t.Fatalf("VerifyRemoteTarget() first error = %v", err)
	}
	merged := mergeInRemote(t, remote, "main", first.TargetCommit)
	if _, err := manager.ConfirmRemoteTarget(context.Background(), first); err != nil {
		t.Fatalf("ConfirmRemoteTarget() first error = %v", err)
	}

	// The second run promotes onto the local target, which is still the promoted
	// commit rather than the forge's merge of it.
	second := publishAndIntegrate(t, manager, "yoyodyne-second", "second.txt", "second\n")
	if second.PreviousTargetCommit != first.TargetCommit {
		t.Fatalf("second promotion was made from %q, want the first promotion %q", second.PreviousTargetCommit, first.TargetCommit)
	}
	if err := manager.VerifyRemoteTarget(context.Background(), second); err != nil {
		t.Fatalf("VerifyRemoteTarget() after a forge merge error = %v, want the merge commit %s accepted", err, merged)
	}
	if _, err := manager.ConfirmRemoteTarget(context.Background(), mergeInRemoteAnd(t, remote, second)); err != nil {
		t.Fatalf("ConfirmRemoteTarget() second error = %v", err)
	}
}

// A merge that replayed the promoted commit rather than merging it — what
// GitHub's rebase and squash methods do — leaves the remote carrying the same
// content under a commit no local branch has. Content alone would accept it, so
// what is checked is that the promoted commit itself is there.
func TestManagerConfirmRemoteTargetRefusesARewrittenPromotion(t *testing.T) {
	t.Parallel()

	repository, remote := newPublishedRepository(t)
	manager := newRemoteManager(t, repository, filepath.Join(t.TempDir(), "worktrees"), "origin")
	integration := publishAndIntegrate(t, manager, "yoyodyne-rewritten", "feature.txt", "published\n")

	// The same tree, on the same base, without the promoted commit as a parent.
	replayed := replayInRemote(t, remote, "main", integration.TargetCommit)
	confirmed, err := manager.ConfirmRemoteTarget(context.Background(), integration)
	if !errors.Is(err, ErrRemoteTargetMismatch) {
		t.Fatalf("ConfirmRemoteTarget() rewritten = %q, %v; want ErrRemoteTargetMismatch", confirmed, err)
	}
	if !strings.Contains(err.Error(), replayed) || !strings.Contains(err.Error(), integration.TargetCommit) {
		t.Errorf("mismatch error names neither the remote commit %q nor the promoted one %q: %v", replayed, integration.TargetCommit, err)
	}
}

// A forge asked to merge into a target that moved would reconcile that movement
// itself, which is exactly the conflict nothing in the run ever saw. The remote
// target is therefore checked the way the local one is, and a remote branch
// carrying history this repository does not have is drift rather than something
// to fetch and accept.
func TestManagerVerifyRemoteTargetRefusesARemoteThatMoved(t *testing.T) {
	t.Parallel()

	repository, remote := newPublishedRepository(t)
	manager := newRemoteManager(t, repository, filepath.Join(t.TempDir(), "worktrees"), "origin")
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:        testRunID,
		WorkItemID:   "yoyodyne-remote-drift",
		BaseRef:      "HEAD",
		TargetBranch: "main",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "feature.txt", "published\n")
	publication, err := manager.PublishBranch(context.Background(), worktree, "")
	if err != nil {
		t.Fatalf("PublishBranch() error = %v", err)
	}
	worktree.HarnessCommit = publication.Commit
	integration, err := manager.Integrate(context.Background(), worktree, "")
	if err != nil {
		t.Fatalf("Integrate() error = %v", err)
	}

	// Someone merges their own work into the target while this run is between
	// its promotion and its merge.
	drifted := pushUnrelatedCommit(t, remote, "main")
	err = manager.VerifyRemoteTarget(context.Background(), integration)
	if !errors.Is(err, ErrRemoteTargetDrift) {
		t.Fatalf("VerifyRemoteTarget() drifted error = %v, want ErrRemoteTargetDrift", err)
	}
	if !strings.Contains(err.Error(), drifted) {
		t.Errorf("drift error does not name the remote commit %q: %v", drifted, err)
	}

	// A target branch that is not on the remote at all cannot be merged into
	// either, and saying so is not the same as reporting a missing branch as
	// present.
	runGit(t, remote, "update-ref", "-d", "refs/heads/main")
	if err := manager.VerifyRemoteTarget(context.Background(), integration); !errors.Is(err, ErrRemoteTargetDrift) {
		t.Fatalf("VerifyRemoteTarget() absent error = %v, want ErrRemoteTargetDrift", err)
	}
	// A merge cannot have reached a branch that is not there either, and that is
	// a mismatch rather than drift: it describes a merge that already happened.
	if _, err := manager.ConfirmRemoteTarget(context.Background(), integration); !errors.Is(err, ErrRemoteTargetMismatch) {
		t.Fatalf("ConfirmRemoteTarget() absent error = %v, want ErrRemoteTargetMismatch", err)
	}
}

// pushUnrelatedCommit puts work on a remote branch that the harness's
// repository has never seen, which is what someone else's merge looks like from
// inside a run.
func pushUnrelatedCommit(t *testing.T, remote, branch string) string {
	t.Helper()
	root := t.TempDir()
	clone := filepath.Join(root, "elsewhere")
	runGit(t, root, "clone", remote, clone)
	runGit(t, clone, "config", "user.name", "Someone Else")
	runGit(t, clone, "config", "user.email", "someone@example.invalid")
	writeFile(t, clone, "elsewhere.txt", "someone else's work\n")
	runGit(t, clone, "add", ".")
	runGit(t, clone, "commit", "-m", "someone else's work")
	runGit(t, clone, "push", "origin", "HEAD:refs/heads/"+branch)
	return gitLine(t, clone, "rev-parse", "HEAD")
}

// publishAndIntegrate takes one run all the way to a local promotion, which is
// the state every remote-target question is asked in.
func publishAndIntegrate(t *testing.T, manager *Manager, workItem, file, content string) Integration {
	t.Helper()
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:        testRunID,
		WorkItemID:   workItem,
		BaseRef:      "HEAD",
		TargetBranch: "main",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, file, content)
	publication, err := manager.PublishBranch(context.Background(), worktree, "")
	if err != nil {
		t.Fatalf("PublishBranch() error = %v", err)
	}
	worktree.HarnessCommit = publication.Commit
	integration, err := manager.Integrate(context.Background(), worktree, "")
	if err != nil {
		t.Fatalf("Integrate() error = %v", err)
	}
	if _, err := manager.CleanupIntegrated(context.Background(), CleanupRequest{
		Worktree:     worktree,
		TargetBranch: worktree.TargetBranch,
		SourceCommit: integration.SourceCommit,
	}); err != nil {
		t.Fatalf("CleanupIntegrated() error = %v", err)
	}
	return integration
}

// mergeInRemote writes into the bare remote what a forge merge produces: a
// merge commit whose first parent is the branch, whose second parent is the
// promoted commit, and whose tree is the promoted commit's. No merge method
// leaves the branch at the promoted commit itself, so nothing here pretends one
// does.
func mergeInRemote(t *testing.T, remote, branch, promoted string) string {
	t.Helper()
	return commitInRemote(t, remote, branch, promoted, true)
}

func mergeInRemoteAnd(t *testing.T, remote string, integration Integration) Integration {
	t.Helper()
	mergeInRemote(t, remote, integration.TargetBranch, integration.TargetCommit)
	return integration
}

// replayInRemote is the same merge with the promoted commit left out of its
// parents, which is what a forge that rebases or squashes leaves behind.
func replayInRemote(t *testing.T, remote, branch, promoted string) string {
	t.Helper()
	return commitInRemote(t, remote, branch, promoted, false)
}

func commitInRemote(t *testing.T, remote, branch, promoted string, carry bool) string {
	t.Helper()
	tip := gitLine(t, remote, "rev-parse", "refs/heads/"+branch)
	tree := gitLine(t, remote, "rev-parse", promoted+"^{tree}")
	arguments := []string{
		"-c", "user.name=Forge",
		"-c", "user.email=forge@example.invalid",
		"commit-tree", tree, "-p", tip,
	}
	if carry {
		arguments = append(arguments, "-p", promoted)
	}
	commit := gitLine(t, remote, append(arguments, "-m", "Merge pull request")...)
	runGit(t, remote, "update-ref", "refs/heads/"+branch, commit, tip)
	return commit
}

// newPublishedRepository is a repository with a bare remote that already
// carries its target branch, which is what a project that publishes looks like.
func newPublishedRepository(t *testing.T) (string, string) {
	t.Helper()
	repository := newRepository(t)
	remote := filepath.Join(t.TempDir(), "remote.git")
	if err := os.MkdirAll(remote, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	runGit(t, remote, "init", "--bare", "-b", "main")
	runGit(t, repository, "remote", "add", "origin", remote)
	runGit(t, repository, "push", "origin", "refs/heads/main:refs/heads/main")
	return repository, remote
}

func newRemoteManager(t *testing.T, repository, worktreeRoot, remote string) *Manager {
	t.Helper()
	manager, err := New(Options{
		Runner:         execution.OSProcessRunner{},
		RepositoryRoot: repository,
		WorktreeRoot:   worktreeRoot,
		Remote:         remote,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return manager
}

// Publishing commits the developer's work before the checks and the review run,
// so everything a reviewer is handed has to be relative to the recorded base
// rather than to the working tree. A summary or a diff built from uncommitted
// state would describe a published change as no change at all, and the approval
// that authorizes the merge would be a rubber stamp on an empty patch.
func TestManagerReportsPublishedWorkAgainstTheBase(t *testing.T) {
	t.Parallel()

	repository, _ := newPublishedRepository(t)
	writeFile(t, repository, "doomed.txt", "delete me\n")
	runGit(t, repository, "add", "doomed.txt")
	runGit(t, repository, "commit", "-m", "add doomed file")
	runGit(t, repository, "push", "origin", "refs/heads/main:refs/heads/main")
	manager := newRemoteManager(t, repository, filepath.Join(t.TempDir(), "worktrees"), "origin")
	worktree, err := manager.Create(context.Background(), CreateRequest{
		RunID:        testRunID,
		WorkItemID:   "yoyodyne-visible",
		BaseRef:      "HEAD",
		TargetBranch: "main",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeFile(t, worktree.Path, "feature.txt", "the developer's work\n")
	writeFile(t, worktree.Path, "README.txt", "test\nedited\n")
	if err := os.Remove(filepath.Join(worktree.Path, "doomed.txt")); err != nil {
		t.Fatalf("Remove() error = %v", err)
	}

	// What the reviewer would have been handed before the branch was published.
	beforeSummary, err := manager.SummarizeChanges(context.Background(), worktree)
	if err != nil {
		t.Fatalf("SummarizeChanges() before publishing error = %v", err)
	}
	beforeChanges, err := manager.UnifiedChanges(context.Background(), worktree, DiffLimits{})
	if err != nil {
		t.Fatalf("UnifiedChanges() before publishing error = %v", err)
	}

	publication, err := manager.PublishBranch(context.Background(), worktree, "")
	if err != nil {
		t.Fatalf("PublishBranch() error = %v", err)
	}
	worktree.HarnessCommit = publication.Commit
	// The worktree is clean now: every later report has to come from the base
	// comparison rather than from what is uncommitted.
	if dirty, err := manager.isDirty(context.Background(), worktree.Path); err != nil || dirty {
		t.Fatalf("published worktree dirty = %t, err = %v; want a clean worktree", dirty, err)
	}

	summary, err := manager.SummarizeChanges(context.Background(), worktree)
	if err != nil {
		t.Fatalf("SummarizeChanges() after publishing error = %v", err)
	}
	changes, err := manager.UnifiedChanges(context.Background(), worktree, DiffLimits{})
	if err != nil {
		t.Fatalf("UnifiedChanges() after publishing error = %v", err)
	}
	for _, want := range []string{"feature.txt", "README.txt", "doomed.txt"} {
		if !strings.Contains(summary.Status, want) {
			t.Errorf("published status does not name %s:\n%s", want, summary.Status)
		}
		if !strings.Contains(changes.DiffStat, want) {
			t.Errorf("published diff stat does not name %s:\n%s", want, changes.DiffStat)
		}
		if !strings.Contains(changes.Patch, want) {
			t.Errorf("published patch does not name %s:\n%s", want, changes.Patch)
		}
	}
	// The content itself has to survive, not only the file names.
	if !strings.Contains(changes.Patch, "the developer's work") {
		t.Errorf("published patch does not carry the developer's content:\n%s", changes.Patch)
	}
	if !strings.Contains(changes.Patch, "edited") {
		t.Errorf("published patch does not carry the edit to a tracked file:\n%s", changes.Patch)
	}
	// Publishing changes where the work is stored, not what the change is. An
	// unpublished run reports the same files and the same content: the new file
	// reaches the patch through the untracked path rather than through the diff
	// against the base, which is why the two diff stats legitimately differ.
	for _, want := range []string{"feature.txt", "README.txt", "doomed.txt"} {
		if !strings.Contains(beforeSummary.Status, want) {
			t.Errorf("unpublished status does not name %s:\n%s", want, beforeSummary.Status)
		}
		if !strings.Contains(beforeChanges.Patch, want) {
			t.Errorf("unpublished patch does not name %s:\n%s", want, beforeChanges.Patch)
		}
	}
	if !strings.Contains(beforeChanges.Patch, "the developer's work") {
		t.Errorf("unpublished patch does not carry the developer's content:\n%s", beforeChanges.Patch)
	}
	// Committing the file moves it out of the separately rendered untracked half
	// and into the diff against the base, where no per-file bound can drop it.
	if len(beforeChanges.UntrackedFiles) != 1 || len(changes.UntrackedFiles) != 0 {
		t.Errorf("untracked files = %v unpublished and %v published, want the file to become part of the diff",
			beforeChanges.UntrackedFiles, changes.UntrackedFiles)
	}
	if strings.Contains(beforeSummary.DiffStat, "feature.txt") || !strings.Contains(summary.DiffStat, "feature.txt") {
		t.Errorf("diff stats = %q unpublished and %q published, want publishing to add the new file to it",
			beforeSummary.DiffStat, summary.DiffStat)
	}
}
