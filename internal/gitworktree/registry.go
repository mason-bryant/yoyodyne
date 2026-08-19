package gitworktree

// Development is parallel, so several runs may be given a worktree at once, and
// on one repository they all write the same bookkeeping: `git worktree add`
// registers the new checkout under the common Git directory's worktrees/ and
// then fills that entry in, and `git worktree remove` takes an entry away in the
// same unguarded pieces. Git takes no lock over either, so a second command
// reaching an entry mid-write reads a half-written or half-deleted one and exits
// rather than doing anything — a run lost to nothing but timing, on a repository
// configured for more than one developer at a time.
//
// The lease below turns concurrent writes to that bookkeeping into a queue. It
// is a sibling of the run state leases and works the same way: an advisory file
// lock the operating system drops when its holder exits, so a write whose
// process died leaves no stale lock for anybody to clear and the next run simply
// takes its turn.
//
// It is taken per repository rather than per manager, because the shared thing
// is the bookkeeping rather than the configuration that points at it: two
// products aimed at one repository have separate worktree roots and one
// worktrees/ directory between them, so the lease lives beside that directory.
//
// The one writer it cannot queue is Git itself. Automatic maintenance prunes
// worktrees, and a registration still being created has no gitdir file yet, so a
// prune judges it stale and deletes it out from under the add that is filling it
// in. Nothing here can make that prune take this lock, so the harness never asks
// for the maintenance that starts it: see maintenanceOptions, which every Git
// command the harness runs carries.

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
	// registryQueueWait bounds how long a write waits its turn. A holder is only
	// ever running one `git worktree` command, which is itself bounded by the
	// manager's Git timeout, so an ordinary queue drains in a small multiple of
	// that. What the bound is really for is the holder that never finishes: a
	// wedged Git command must not hold every other run on the repository until
	// its process dies.
	registryQueueWait = 5 * time.Minute
	// registryLockName is the file the lease is taken on. It lives in the common
	// Git directory beside the worktrees/ directory it serializes writes to, and
	// it is never removed: deleting it while another process held it would let a
	// third take a lock on a file nobody else can see. It keeps the name it had
	// when creation was the only write it covered, because this product promotes
	// into the repository it is running in: a harness still on the older binary
	// has to queue behind a removal rather than beside it.
	registryLockName = "yoyodyne-worktree-creation.lock"
)

// leaseRegistry admits this process to write one worktree registration in this
// repository, waiting its turn behind whichever write is in flight. The wait is
// bounded: a caller that never reaches the front is told so rather than held
// forever, and a cancelled run stops waiting immediately.
//
// A holder must not ask for it again before releasing: the lock belongs to the
// open file description rather than to the process, which is what makes two
// writes in one process queue, and is equally what makes a nested one queue
// behind itself.
func (m *Manager) leaseRegistry(ctx context.Context) (*registryLease, error) {
	directory, err := m.commonGitDirectory(ctx)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(directory, registryLockName)
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open the worktree registry lease: %w", err)
	}
	waitCtx, cancel := context.WithTimeout(ctx, registryQueueWait)
	defer cancel()
	if err := lockRegistryFile(waitCtx, file); err != nil {
		file.Close()
		// A caller whose own context is still live waited out the bound rather
		// than being cancelled, and the two must not read alike: one is a queue
		// nothing is draining, the other is the run being stopped.
		if ctx.Err() == nil {
			return nil, fmt.Errorf("wait to write the worktree registry: another write held the lease for the whole %s wait", registryQueueWait)
		}
		return nil, fmt.Errorf("wait to write the worktree registry: %w", err)
	}
	return &registryLease{file: file}, nil
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

// registryLease is one held worktree registry lease.
type registryLease struct {
	file *os.File
}

// release drops the lease. Releasing twice is a no-op, so a caller can defer it
// unconditionally.
func (l *registryLease) release() error {
	if l == nil || l.file == nil {
		return nil
	}
	file := l.file
	l.file = nil
	if err := file.Close(); err != nil {
		return fmt.Errorf("release the worktree registry lease: %w", err)
	}
	return nil
}
