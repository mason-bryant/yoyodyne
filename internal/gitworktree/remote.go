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

// PublishIntegration publishes a promotion that already happened locally. The
// integrated commit is pushed onto the remote target branch, which is what
// merges the run's pull request: the published branch receives exactly the
// fast-forward the harness already made, so it can never contain a commit the
// authoritative local branch does not.
func (m *Manager) PublishIntegration(ctx context.Context, worktree Worktree, integration Integration) error {
	if err := validateTargetBranch(integration.TargetBranch); err != nil {
		return err
	}
	if worktree.TargetBranch != "" && integration.TargetBranch != worktree.TargetBranch {
		return fmt.Errorf("published target %q does not match the worktree's recorded target %q", integration.TargetBranch, worktree.TargetBranch)
	}
	if !commitPattern.MatchString(integration.TargetCommit) {
		return fmt.Errorf("integrated target commit %q is invalid", integration.TargetCommit)
	}
	return m.pushBranch(ctx, integration.TargetBranch, integration.TargetCommit)
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
