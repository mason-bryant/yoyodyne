package gitworktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"yoyodyne/internal/execution"
)

// Publication is what publishing a run's branch produced: the commit the
// harness made of the developer's work, and the remote branch it now sits on.
type Publication struct {
	Remote string `json:"remote"`
	Branch string `json:"branch"`
	Commit string `json:"commit"`
}

// ErrRemotePushRejected reports a push the remote refused. It is never resolved
// by forcing: a remote branch that moved is reconciled, exactly as a local
// target that drifted is.
var ErrRemotePushRejected = errors.New("remote rejected the push")

// ErrRemoteTargetDrift reports a remote target branch carrying work the
// promotion was not written against. It is the remote half of ErrTargetDrift:
// the merge must fail rather than have the forge reconcile a change nobody saw.
var ErrRemoteTargetDrift = errors.New("remote integration target moved away from the content the promotion was written against")

// ErrRemoteTargetMismatch reports a merge that did not put the promotion on the
// remote target branch. It is deliberately distinct from drift: drift is a
// reason not to merge, this is what a merge that already happened turned out to
// be.
var ErrRemoteTargetMismatch = errors.New("remote integration target does not carry the promoted commit")

// RemoteConfigured reports whether the repository has the remote publishing
// would push to. A repository without one is not an error and never becomes a
// failed run: publishing is skipped and the run behaves exactly as a local-only
// run does.
func (m *Manager) RemoteConfigured(ctx context.Context) (bool, error) {
	result, err := m.run(ctx, "-C", m.repositoryRoot, "remote", "get-url", m.remote)
	if err != nil {
		return false, err
	}
	if result.Status != execution.ProcessSucceeded {
		return false, nil
	}
	return strings.TrimSpace(result.Stdout) != "", nil
}

// PublishBranch commits whatever the developer left in the worktree as one
// harness-owned commit and pushes the run branch to the configured remote. It
// is what the developer phase causes and the harness performs: nothing here is
// routed through an agent, and the commit carries the harness identity rather
// than the developer's.
//
// The push is an ordinary fast-forward push. A remote branch that somehow moved
// away from what the harness put there is refused rather than forced, because a
// published branch nobody can explain is exactly the thing a person has to look
// at.
func (m *Manager) PublishBranch(ctx context.Context, worktree Worktree, message string) (Publication, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		message = defaultCommitMessage(worktree)
	}
	path, head, err := m.verifyOwnedHead(ctx, worktree)
	if err != nil {
		return Publication{}, err
	}
	dirty, err := m.isDirty(ctx, path)
	if err != nil {
		return Publication{}, err
	}
	if !dirty && head == worktree.BaseCommit {
		return Publication{}, ErrNoChanges
	}
	commit := head
	if dirty {
		commit, err = m.commitWorktree(ctx, path, message)
		if err != nil {
			return Publication{}, err
		}
	}
	if commit == worktree.BaseCommit {
		return Publication{}, ErrNoChanges
	}
	publication := Publication{Remote: m.remote, Branch: worktree.Branch, Commit: commit}
	// The commit is reported even when the push fails, because it exists either
	// way. A caller that could not learn of it would leave the worktree at a HEAD
	// nothing recorded, which is the one state the ownership check has to be able
	// to tell apart from an agent's own commit.
	if err := m.pushBranch(ctx, worktree.Branch, commit); err != nil {
		return publication, err
	}
	return publication, nil
}

// VerifyRemoteTarget checks that the remote target branch can still receive the
// promotion the harness already made locally. The forge is what performs the
// merge, and a forge asked to merge into a branch that moved would reconcile
// that movement itself — resolving a conflict nothing in this run ever saw.
//
// What the remote target may be is decided by what a forge merge leaves behind.
// A merged pull request puts the promoted commit on the target and a merge
// commit above it, so the target of a repository that has published before is a
// commit this repository does not have and never will: the harness does not
// carry the forge's merge commits onto the local branch. Such a target is not
// drift, and telling the two apart is a question about content rather than
// identity — it must carry exactly what the promotion was written against, and
// it must contain the commit that was promoted last.
func (m *Manager) VerifyRemoteTarget(ctx context.Context, integration Integration) error {
	if err := validateTargetBranch(integration.TargetBranch); err != nil {
		return err
	}
	if !commitPattern.MatchString(integration.TargetCommit) {
		return fmt.Errorf("integrated target commit %q is invalid", integration.TargetCommit)
	}
	if !commitPattern.MatchString(integration.PreviousTargetCommit) {
		return fmt.Errorf("previous target commit %q is invalid", integration.PreviousTargetCommit)
	}
	published, exists, err := m.remoteCommit(ctx, integration.TargetBranch)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("%w: %s does not exist on %s", ErrRemoteTargetDrift, integration.TargetBranch, m.remote)
	}
	// The remote is at or behind the promotion: a repository that has never
	// published, or one whose earlier publication the forge merged by
	// fast-forward. Nothing has to be fetched to know that.
	behind, err := m.descendsFrom(ctx, published, integration.TargetCommit)
	if err != nil {
		return err
	}
	if behind {
		return nil
	}
	// Anything else has to be looked at, so it is fetched. A forge merge commit
	// and someone else's work are both commits this repository does not have;
	// only their content tells them apart.
	if err := m.fetchRemoteBranch(ctx, integration.TargetBranch, published); err != nil {
		return fmt.Errorf("%w: %w", ErrRemoteTargetDrift, err)
	}
	carries, err := m.descendsFrom(ctx, integration.PreviousTargetCommit, published)
	if err != nil {
		return err
	}
	if !carries {
		return fmt.Errorf("%w: %s on %s is at %s, which does not contain the commit this promotion was made from (%s)",
			ErrRemoteTargetDrift, integration.TargetBranch, m.remote, published, integration.PreviousTargetCommit)
	}
	same, err := m.sameContent(ctx, published, integration.PreviousTargetCommit)
	if err != nil {
		return err
	}
	if !same {
		return fmt.Errorf("%w: %s on %s is at %s, which carries content the promotion was not written against (%s)",
			ErrRemoteTargetDrift, integration.TargetBranch, m.remote, published, integration.PreviousTargetCommit)
	}
	return nil
}

// ConfirmRemoteTarget reports where a forge's merge left the remote target
// branch, and refuses anything that is not the promotion arriving there. The
// two branches do not end at the same commit — the forge adds a merge commit of
// its own, which is the price of not pushing the target branch — so what is
// checked is the relationship that a merge does guarantee: the promoted commit
// itself is on the remote target, unrewritten, and the branch carries exactly
// its content and nothing besides.
//
// A forge that replayed the commits instead of merging them fails the first
// half, and one that merged something else along the way fails the second. Both
// are reported rather than reconciled: which history is right is a person's
// decision, not a harness's.
func (m *Manager) ConfirmRemoteTarget(ctx context.Context, integration Integration) (string, error) {
	if err := validateTargetBranch(integration.TargetBranch); err != nil {
		return "", err
	}
	if !commitPattern.MatchString(integration.TargetCommit) {
		return "", fmt.Errorf("integrated target commit %q is invalid", integration.TargetCommit)
	}
	published, exists, err := m.remoteCommit(ctx, integration.TargetBranch)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", fmt.Errorf("%w: %s does not exist on %s after the merge", ErrRemoteTargetMismatch, integration.TargetBranch, m.remote)
	}
	if published != integration.TargetCommit {
		if err := m.fetchRemoteBranch(ctx, integration.TargetBranch, published); err != nil {
			return "", fmt.Errorf("%w: %w", ErrRemoteTargetMismatch, err)
		}
	}
	promoted, err := m.descendsFrom(ctx, integration.TargetCommit, published)
	if err != nil {
		return "", err
	}
	if !promoted {
		return "", fmt.Errorf("%w: %s on %s is at %s, which does not contain the promoted commit %s",
			ErrRemoteTargetMismatch, integration.TargetBranch, m.remote, published, integration.TargetCommit)
	}
	same, err := m.sameContent(ctx, published, integration.TargetCommit)
	if err != nil {
		return "", err
	}
	if !same {
		return "", fmt.Errorf("%w: %s on %s is at %s, which carries content the promoted commit %s does not",
			ErrRemoteTargetMismatch, integration.TargetBranch, m.remote, published, integration.TargetCommit)
	}
	return published, nil
}

// fetchRemoteBranch brings one remote branch's tip into this repository so it
// can be inspected, and refuses a branch that moved between being resolved and
// being fetched: an answer about a commit other than the one that was asked
// about is worse than no answer.
func (m *Manager) fetchRemoteBranch(ctx context.Context, branch, commit string) error {
	result, err := m.runRemote(ctx, "-C", m.repositoryRoot,
		"-c", "core.hooksPath="+os.DevNull,
		"fetch", "--no-tags", "--no-write-fetch-head", m.remote, "+refs/heads/"+branch+":refs/"+fetchedRemoteTarget)
	if err != nil {
		return err
	}
	if result.Status != execution.ProcessSucceeded {
		return fmt.Errorf("fetch %s from %s failed with exit code %d: %s", branch, m.remote, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	fetched, err := m.resolveCommit(ctx, "refs/"+fetchedRemoteTarget)
	if err != nil {
		return err
	}
	if fetched != commit {
		return fmt.Errorf("%s on %s is at %s, not the %s that was resolved a moment earlier", branch, m.remote, fetched, commit)
	}
	return nil
}

// fetchedRemoteTarget is the scratch ref a remote target is fetched into. It is
// deliberately not under refs/heads or refs/remotes: nothing about inspecting
// the remote may look like a branch this repository keeps.
const fetchedRemoteTarget = "yoyodyne/fetched-remote-target"

// sameContent reports whether two commits carry an identical tree. It is how a
// forge merge commit is told from work the harness never saw: the commits
// differ by construction, the content must not.
func (m *Manager) sameContent(ctx context.Context, one, other string) (bool, error) {
	oneTree, err := m.resolveCommit(ctx, one+"^{tree}")
	if err != nil {
		return false, err
	}
	otherTree, err := m.resolveCommit(ctx, other+"^{tree}")
	if err != nil {
		return false, err
	}
	return oneTree == otherTree, nil
}

// descendsFrom reports whether one commit is part of another's history. Like
// contains, any answer other than a clean yes is a no: a commit this repository
// has never seen makes the question unanswerable, and an unanswered question
// must not authorize a merge.
func (m *Manager) descendsFrom(ctx context.Context, ancestor, descendant string) (bool, error) {
	result, err := m.run(ctx, "-C", m.repositoryRoot, "merge-base", "--is-ancestor", ancestor, descendant)
	if err != nil {
		return false, err
	}
	return result.Status == execution.ProcessSucceeded, nil
}

// resolveCommit resolves any revision this repository has, which is how a
// fetched remote commit and the trees on either side of a comparison are read.
func (m *Manager) resolveCommit(ctx context.Context, revision string) (string, error) {
	result, err := m.run(ctx, "-C", m.repositoryRoot, "rev-parse", "--verify", "--quiet", revision)
	if err != nil {
		return "", err
	}
	if result.Status != execution.ProcessSucceeded {
		return "", fmt.Errorf("resolve %s failed with exit code %d: %s", revision, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	resolved := strings.TrimSpace(result.Stdout)
	if !commitPattern.MatchString(resolved) {
		return "", fmt.Errorf("resolved object %q for %s is invalid", resolved, revision)
	}
	return resolved, nil
}

// DeleteRemoteBranch removes a merged run branch from the remote. It is a
// compare-and-swap on the exact published commit, like the local deletion: a
// remote branch that carries anything else is left alone for a person. A branch
// a previous attempt already deleted is reported as done rather than as a
// failure.
func (m *Manager) DeleteRemoteBranch(ctx context.Context, worktree Worktree, commit string) error {
	if !commitPattern.MatchString(commit) {
		return fmt.Errorf("published commit %q is invalid", commit)
	}
	if err := validateRef(worktree.Branch); err != nil {
		return err
	}
	published, exists, err := m.remoteCommit(ctx, worktree.Branch)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if published != commit {
		return fmt.Errorf("remote branch %s is at %s, want the published commit %s", worktree.Branch, published, commit)
	}
	result, err := m.runRemote(ctx, "-C", m.repositoryRoot,
		"-c", "core.hooksPath="+os.DevNull,
		"push", m.remote, "--delete", "refs/heads/"+worktree.Branch)
	if err != nil {
		return err
	}
	if result.Status != execution.ProcessSucceeded {
		return fmt.Errorf("delete remote branch %s failed with exit code %d: %s", worktree.Branch, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	return nil
}

// pushBranch advances one remote branch to an exact commit and proves it
// arrived. The refspec is written out in full so the push never depends on
// whatever `push.default` or an upstream configuration happens to say.
func (m *Manager) pushBranch(ctx context.Context, branch, commit string) error {
	if err := validateRef(branch); err != nil {
		return err
	}
	result, err := m.runRemote(ctx, "-C", m.repositoryRoot,
		"-c", "core.hooksPath="+os.DevNull,
		"push", m.remote, commit+":refs/heads/"+branch)
	if err != nil {
		return err
	}
	if result.Status != execution.ProcessSucceeded {
		return fmt.Errorf("%w: push %s to %s on %s failed with exit code %d: %s",
			ErrRemotePushRejected, commit, branch, m.remote, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	published, exists, err := m.remoteCommit(ctx, branch)
	if err != nil {
		return err
	}
	if !exists || published != commit {
		return fmt.Errorf("%w: %s on %s is at %q after the push, want %s", ErrRemotePushRejected, branch, m.remote, published, commit)
	}
	return nil
}

// remoteCommit resolves one branch on the remote, reporting absence as an
// observation rather than a failure.
func (m *Manager) remoteCommit(ctx context.Context, branch string) (string, bool, error) {
	result, err := m.runRemote(ctx, "-C", m.repositoryRoot, "ls-remote", "--exit-code", "--heads", m.remote, "refs/heads/"+branch)
	if err != nil {
		return "", false, err
	}
	switch {
	case result.ExitCode == 2:
		return "", false, nil
	case result.Status != execution.ProcessSucceeded:
		return "", false, fmt.Errorf("resolve %s on %s failed with exit code %d: %s", branch, m.remote, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	fields := strings.Fields(result.Stdout)
	if len(fields) == 0 {
		return "", false, nil
	}
	commit := fields[0]
	if !commitPattern.MatchString(commit) {
		return "", false, fmt.Errorf("remote commit %q for branch %s is invalid", commit, branch)
	}
	return commit, true, nil
}
