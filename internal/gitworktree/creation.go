package gitworktree

// Development is parallel, so several runs may be given a worktree at once, and
// on one repository they all write the same bookkeeping: `git worktree add`
// registers the new checkout under the common Git directory's worktrees/ and
// then fills that entry in. Git takes no lock over those two steps, so a second
// command reaching the entry in between reads a half-written one and exits
// rather than creating anything — a run lost to nothing but timing, on a
// repository configured for more than one developer at a time.
//
// The lease below turns concurrent creation into a queue. It is a sibling of the
// run state leases and works the same way: an advisory file lock the operating
// system drops when its holder exits, so a creation whose process died leaves no
// stale lock for anybody to clear and the next run simply takes its turn.
//
// It is taken per repository rather than per manager, because the shared thing
// is the bookkeeping rather than the configuration that points at it: two
// products aimed at one repository have separate worktree roots and one
// worktrees/ directory between them, so the lease lives beside that directory.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

const (
	// creationQueueWait bounds how long a creation waits its turn. A holder is
	// only ever running one `git worktree add`, which is itself bounded by the
	// manager's Git timeout, so an ordinary queue drains in a small multiple of
	// that. What the bound is really for is the holder that never finishes: a
	// wedged Git command must not hold every other run on the repository until
	// its process dies.
	creationQueueWait = 5 * time.Minute
	// creationLockName is the file the lease is taken on. It lives in the common
	// Git directory beside the worktrees/ directory it serializes writes to, and
	// it is never removed: deleting it while another process held it would let a
	// third take a lock on a file nobody else can see.
	creationLockName = "yoyodyne-worktree-creation.lock"
)

// leaseCreation admits this process to create one worktree in this repository,
// waiting its turn behind whichever creation is in flight. The wait is bounded:
// a caller that never reaches the front is told so rather than held forever, and
// a cancelled run stops waiting immediately.
func (m *Manager) leaseCreation(ctx context.Context) (*creationLease, error) {
	directory, err := m.commonGitDirectory(ctx)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(directory, creationLockName)
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open the worktree creation lease: %w", err)
	}
	waitCtx, cancel := context.WithTimeout(ctx, creationQueueWait)
	defer cancel()
	if err := lockCreationFile(waitCtx, file); err != nil {
		file.Close()
		// A caller whose own context is still live waited out the bound rather
		// than being cancelled, and the two must not read alike: one is a queue
		// nothing is draining, the other is the run being stopped.
		if ctx.Err() == nil {
			return nil, fmt.Errorf("wait to create a worktree: another creation held the lease for the whole %s wait", creationQueueWait)
		}
		return nil, fmt.Errorf("wait to create a worktree: %w", err)
	}
	return &creationLease{file: file}, nil
}

// commonGitDirectory resolves the directory every checkout of this repository
// shares, which is where Git keeps the worktree bookkeeping the lease protects.
// It is asked of Git rather than assumed to be `.git`, because a repository's
// common directory is a file's worth of indirection away in exactly the setups
// this harness creates.
func (m *Manager) commonGitDirectory(ctx context.Context) (string, error) {
	result, err := m.run(ctx, "-C", m.repositoryRoot, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", err
	}
	if result.Status != execution.ProcessSucceeded {
		return "", fmt.Errorf("resolve the common Git directory failed with exit code %d: %s", result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	directory := strings.TrimSpace(result.Stdout)
	if directory == "" {
		return "", errors.New("resolved common Git directory is empty")
	}
	// Git answers relative to the directory it ran in, which is the repository
	// root because that is what -C named.
	if !filepath.IsAbs(directory) {
		directory = filepath.Join(m.repositoryRoot, directory)
	}
	return directory, nil
}

// creationLease is one held worktree creation lease.
type creationLease struct {
	file *os.File
}

// release drops the lease. Releasing twice is a no-op, so a caller can defer it
// unconditionally.
func (l *creationLease) release() error {
	if l == nil || l.file == nil {
		return nil
	}
	file := l.file
	l.file = nil
	if err := file.Close(); err != nil {
		return fmt.Errorf("release the worktree creation lease: %w", err)
	}
	return nil
}
