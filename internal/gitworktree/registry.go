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
//
// The lease is also what says what an unfinished registration is. A creation
// holds it from before `git worktree add` until the new checkout is verified, so
// a process holding it that finds an entry registered and not filled in is
// looking at an add that is not the harness's own in flight — a killed one, or
// somebody else's — and that is the ground on which such an entry is cleared:
// see settleRegistrations, which runs only under this lease.
//
// Reading that bookkeeping queues on the same lease, in a shared mode every
// other reader may hold at once — see leaseRegistryShared. Git walks the
// registrations from far more commands than the one that describes them: a
// rebase, a checkout, and a branch deletion all check that a branch is not
// checked out somewhere else, and every one of them fails the whole command
// over an entry a creation beside it has not filled in yet. Taking the lease to
// read is what stops them crossing that instant at all, rather than tolerating
// having crossed it.
//
// A holder reads under its own lease rather than queueing behind itself. That
// is what the mark on the context is for: a creation holds the lease through
// the verification that reads what it just wrote, so a reader beneath a writer
// takes nothing and a listing does not wait for the creation it is verifying.
//
// Holding the lease is also what makes a listing worth keeping. While this
// process holds it, no other harness can change the registrations, so the only
// changes a listing can miss are the holder's own: the listing taken under a
// lease is kept on it and answered from there, and dropped whenever the holder
// runs a command that can change the registrations or clears one itself, and
// when the lease is released. Outside a lease nothing is kept, because anybody
// may be changing them. See registryState.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

const (
	// registryQueueWait bounds how long a write, or a read behind a write, waits
	// its turn. A holder is only ever running one `git worktree` command, which
	// is itself bounded by the manager's Git timeout, so an ordinary queue drains
	// in a small multiple of that. What the bound is really for is the holder
	// that never finishes: a wedged Git command must not hold every other run on
	// the repository until its process dies.
	//
	// It bounds a write behind readers too, which is the one direction the lock
	// itself gives no fairness in: a reader holds for one Git command and asks
	// again only when it runs the next one, so the gaps a write gets in through
	// are frequent, and the bound is what stops a write waiting on them forever.
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
// behind itself. That is why the context it returns is the one to carry through
// everything done under the lease — it is marked as holding it, so the reads
// below take nothing.
func (m *Manager) leaseRegistry(ctx context.Context) (context.Context, *registryLease, error) {
	// Resolving where the lock lives is itself a Git command, run as one already
	// holding the lease so that asking for a lease can never come round to
	// asking for one.
	file, err := m.openRegistryLock(holdingRegistry(ctx))
	if err != nil {
		return nil, nil, err
	}
	waitCtx, cancel := context.WithTimeout(ctx, registryQueueWait)
	defer cancel()
	if err := lockRegistryFile(waitCtx, file); err != nil {
		file.Close()
		// A caller whose own context is still live waited out the bound rather
		// than being cancelled, and the two must not read alike: one is a queue
		// nothing is draining, the other is the run being stopped.
		if ctx.Err() == nil {
			return nil, nil, fmt.Errorf("wait to write the worktree registry: another write held the lease for the whole %s wait", registryQueueWait)
		}
		return nil, nil, fmt.Errorf("wait to write the worktree registry: %w", err)
	}
	state := &registryState{}
	return context.WithValue(ctx, registryHold{}, state), &registryLease{file: file, state: state}, nil
}

// leaseRegistryShared admits this process to read the worktree bookkeeping,
// waiting its turn behind whichever write is in flight. Every other reader may
// hold it at the same time, so it costs nothing where nothing is being written,
// which is almost always.
//
// It answers with no lease at all in two cases, and carries on in both. Under a
// lease this process already holds, because a holder reads what it is writing
// and must not queue behind itself. And on a platform with no advisory lock,
// because refusing every registration-walking command there would be a much
// larger refusal than the writes that platform genuinely cannot make safe — the
// re-run in runBounded is what covered those commands before this existed, and
// it still does.
func (m *Manager) leaseRegistryShared(ctx context.Context) (*registryLease, error) {
	if registryHeld(ctx) || !registryLockSupported {
		return nil, nil
	}
	file, err := m.openRegistryLock(holdingRegistry(ctx))
	if err != nil {
		return nil, err
	}
	waitCtx, cancel := context.WithTimeout(ctx, registryQueueWait)
	defer cancel()
	if err := lockRegistryFileShared(waitCtx, file); err != nil {
		file.Close()
		if ctx.Err() == nil {
			return nil, fmt.Errorf("wait to read the worktree registry: a write held the lease for the whole %s wait", registryQueueWait)
		}
		return nil, fmt.Errorf("wait to read the worktree registry: %w", err)
	}
	return &registryLease{file: file}, nil
}

// openRegistryLock opens the file the lease is taken on, creating it where this
// repository has never had one.
func (m *Manager) openRegistryLock(ctx context.Context) (*os.File, error) {
	directory, err := m.commonGitDirectory(ctx)
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(directory, registryLockName), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open the worktree registry lease: %w", err)
	}
	return file, nil
}

// registryHold marks a context as running under a registry lease this process
// holds. It is a type of its own so nothing else can collide with it, and the
// value it carries is the lease's registryState.
type registryHold struct{}

// holdingRegistry marks ctx as running under a lease this process holds. A
// context marked here rather than by leaseRegistry is one about to take the
// lease, and its state keeps nothing: it is released from the start.
func holdingRegistry(ctx context.Context) context.Context {
	if registryHeld(ctx) {
		return ctx
	}
	return context.WithValue(ctx, registryHold{}, &registryState{released: true})
}

// registryHeld says whether ctx is running under a lease this process holds.
func registryHeld(ctx context.Context) bool {
	return heldRegistry(ctx) != nil
}

// heldRegistry is the state of the lease ctx runs under, and nil outside one.
func heldRegistry(ctx context.Context) *registryState {
	state, _ := ctx.Value(registryHold{}).(*registryState)
	return state
}

// registryState is what one held lease knows about the registrations it
// serializes: the listing taken under it, until something could have changed
// it. The holder is the only writer while it holds the lease, so a listing is
// dropped on exactly three things — a command of the holder's that can change
// the registrations (see runBounded), a registration the holder clears itself
// (see settleRegistrations), and the lease being released — and between those
// a second listing would only ask Git for the answer it already gave.
//
// The generation is what keeps a listing read across a change from being kept.
// Every drop moves it on, and a listing is kept only where no drop happened
// between asking for it and getting it, so a listing Git gave while the holder
// was changing the registrations beside it is used once and never answered
// again.
//
// A nil state is outside any lease, and keeps nothing.
type registryState struct {
	mu         sync.Mutex
	released   bool
	generation uint64
	listed     bool
	listing    []worktreeEntry
}

// cachedListing is the listing kept on this lease, where one is. Where none is,
// it says which generation a listing read now would be kept against.
func (s *registryState) cachedListing() ([]worktreeEntry, uint64, bool) {
	if s == nil {
		return nil, 0, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.released || !s.listed {
		return nil, s.generation, false
	}
	return slices.Clone(s.listing), s.generation, true
}

// keepListing keeps a listing Git gave under this lease, read from the
// generation cachedListing named. A lease already released, or one whose
// registrations may have changed since the listing was asked for, keeps
// nothing.
func (s *registryState) keepListing(generation uint64, entries []worktreeEntry) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.released || s.generation != generation {
		return
	}
	s.listing = slices.Clone(entries)
	s.listed = true
}

// forgetListing drops the listing kept on this lease, because the holder is
// about to change, or has just changed, what it described.
func (s *registryState) forgetListing() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.generation++
	s.listing = nil
	s.listed = false
}

// release drops the listing and keeps nothing from then on: once the lease is
// let go, any other harness may change the registrations.
func (s *registryState) release() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.released = true
	s.generation++
	s.listing = nil
	s.listed = false
}

// commonGitDirectory resolves the directory every checkout of this repository
// shares, which is where Git keeps the worktree bookkeeping the lease protects.
// It is asked of Git rather than assumed to be `.git`, because a repository's
// common directory is a file's worth of indirection away in exactly the setups
// this harness creates.
//
// It is asked once per manager rather than once per lease, which is where
// nearly all of these questions came from: every command that walks the
// registrations takes the shared lease, and finding the lease's file asked Git
// again each time. The answer depends only on the repository root, which a
// manager never changes, and on the root's own `.git`, which no creation,
// removal, or prune touches. The one way it can stop being true is somebody
// replacing the repository under the manager, so a kept answer whose directory
// is no longer there is asked again rather than trusted.
func (m *Manager) commonGitDirectory(ctx context.Context) (string, error) {
	m.commonDirectoryMu.Lock()
	kept := m.commonDirectory
	m.commonDirectoryMu.Unlock()
	if kept != "" {
		if info, err := os.Stat(kept); err == nil && info.IsDir() {
			return kept, nil
		}
	}
	directory, err := m.resolveCommonGitDirectory(ctx)
	if err != nil {
		return "", err
	}
	m.commonDirectoryMu.Lock()
	m.commonDirectory = directory
	m.commonDirectoryMu.Unlock()
	return directory, nil
}

// resolveCommonGitDirectory asks Git for the common directory.
func (m *Manager) resolveCommonGitDirectory(ctx context.Context) (string, error) {
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
	// state is what an exclusive lease keeps while it is held, and nil on a
	// shared one, which keeps nothing.
	state *registryState
}

// release drops the lease, and with it whatever the lease kept. Releasing twice
// is a no-op, so a caller can defer it unconditionally.
func (l *registryLease) release() error {
	if l == nil || l.file == nil {
		return nil
	}
	// What was kept is dropped before the lock is, so nothing is answered from it
	// at a moment another harness could be writing.
	l.state.release()
	file := l.file
	l.file = nil
	if err := file.Close(); err != nil {
		return fmt.Errorf("release the worktree registry lease: %w", err)
	}
	return nil
}
