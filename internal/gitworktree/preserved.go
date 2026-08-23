package gitworktree

// Retiring what a stopped run preserved.
//
// A run that stops leaves its branch and its worktree where they are, because
// what they hold is the evidence somebody decides against. That is the right
// default and it has no end: once the work has landed by some other route, the
// two are orphans nobody knows to look for, and the harness has never had a way
// to say so.
//
// This is that way, and it keeps every rule the cleanup beside it keeps. Nothing
// here proves an integration, so nothing here deletes anything an integration
// would have justified deleting: a worktree holding uncommitted work is kept, a
// directory the harness did not register is never touched, and a branch is
// deleted only where `RemoveMergedBranch` would delete it — its work already
// contained in the target. What is left is exactly the removal that can lose
// nothing: an empty registration whose commits, if it has any, survive on a
// branch this refused to delete.
//
// Every refusal is a kept artifact with a reason rather than a failure. A caller
// retiring what a stoppage left behind is finishing something else, and a
// worktree that has to be looked at by hand is a fact to record rather than a
// reason to fail the work that reached here.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// WorktreeRemoval is what one attempt to retire a preserved worktree found.
// Kept is why it was left where it is, and is empty both when the worktree was
// removed and when there was nothing there to remove — Removed is what tells
// those two apart.
type WorktreeRemoval struct {
	Path string `json:"path"`
	// Registered reports that Git was still managing this checkout when the
	// attempt began. It is what tells a worktree this retired from one that was
	// already gone, which Removed cannot: both report Removed, because what a
	// caller records either way is that nobody will find it. A sweep that runs
	// on every pass needs the difference, or it reports the same long-gone
	// worktree forever.
	Registered bool   `json:"registered"`
	Removed    bool   `json:"removed"`
	Kept       string `json:"kept,omitempty"`
}

// Retirement is what became of both of the artifacts a stopped run preserved.
type Retirement struct {
	Worktree WorktreeRemoval `json:"worktree"`
	Branch   Removal         `json:"branch"`
}

// Retired reports both artifacts gone, which is the only state that leaves
// nothing for anybody to find.
func (r Retirement) Retired() bool {
	return r.Worktree.Removed && r.Branch.Removed
}

// Kept says what survived and why, in one line, or nothing when nothing did.
func (r Retirement) Kept() string {
	var kept []string
	if r.Worktree.Kept != "" {
		kept = append(kept, r.Worktree.Kept)
	}
	if r.Branch.Kept != "" {
		kept = append(kept, r.Branch.Kept)
	}
	return strings.Join(kept, "; ")
}

// RetirePreserved removes what a stopped run left behind, as far as it can be
// removed without losing anything.
//
// The worktree goes first because the branch cannot: a branch a checkout still
// holds is refused, and that checkout is the worktree this is retiring. A
// worktree removed above a branch that is then kept is deliberate rather than a
// half-finished retirement — the directory held no uncommitted work, or it would
// have been kept too, and every commit it carried is on the branch that survived.
func (m *Manager) RetirePreserved(ctx context.Context, worktree Worktree, targetBranch string) (Retirement, error) {
	retirement := Retirement{Branch: Removal{Branch: worktree.Branch}}
	removed, err := m.RemovePreservedWorktree(ctx, worktree)
	retirement.Worktree = removed
	if err != nil {
		return retirement, err
	}
	branch, err := m.RemoveMergedBranch(ctx, worktree.Branch, targetBranch)
	retirement.Branch = branch
	return retirement, err
}

// RemovePreservedWorktree unregisters a worktree the harness created and nobody
// is using any more. It removes only what it can prove is its own and only what
// holds nothing: a path outside what this manager owns is an error, and anything
// else in doubt — a directory Git does not manage, a registration on some other
// branch, uncommitted work — is kept with the reason recorded.
//
// The branch is deliberately untouched. Removing a registration loses nothing
// while the commits are still on a branch, and deciding whether that branch may
// go is `RemoveMergedBranch`'s question and needs the target to answer.
func (m *Manager) RemovePreservedWorktree(ctx context.Context, worktree Worktree) (WorktreeRemoval, error) {
	path, err := m.ownedPath(worktree)
	if err != nil {
		return WorktreeRemoval{}, err
	}
	removal := WorktreeRemoval{Path: path}
	registered, branch, err := m.registeredWorktree(ctx, path)
	if err != nil {
		return removal, err
	}
	removal.Registered = registered
	info, statErr := os.Lstat(path)
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return removal, fmt.Errorf("inspect worktree path: %w", statErr)
	}
	present := statErr == nil

	if !registered {
		if present {
			removal.Kept = fmt.Sprintf("%s exists and is not a registered worktree, so it has to be inspected by hand", path)
			return removal, nil
		}
		// Nothing is registered and nothing is on disk, so there is nothing to
		// retire. That is a worktree already gone rather than one this removed,
		// and Removed says so either way: what a caller records is that nobody
		// will find it.
		removal.Removed = true
		return removal, nil
	}
	if branch != worktree.Branch {
		removal.Kept = fmt.Sprintf("%s is registered on %s rather than on the recorded branch %s", path, branch, worktree.Branch)
		return removal, nil
	}
	if present {
		if info.Mode()&os.ModeSymlink != 0 {
			removal.Kept = fmt.Sprintf("%s is a symlink rather than a worktree", path)
			return removal, nil
		}
		dirty, err := m.isDirty(ctx, path)
		if err != nil {
			return removal, err
		}
		if dirty {
			// Uncommitted work is the one thing no record of it survives, so it is
			// the one thing this never removes.
			removal.Kept = fmt.Sprintf("%s holds uncommitted work, which nothing else records", path)
			return removal, nil
		}
	}
	// A removal unregisters an entry in the same unguarded pieces an add writes
	// one, so it queues on the same lease the creation does.
	lease, err := m.leaseRegistry(ctx)
	if err != nil {
		return removal, err
	}
	defer func() { _ = lease.release() }()

	removed, err := m.run(ctx, "-C", m.repositoryRoot, "worktree", "remove", path)
	if err != nil {
		return removal, err
	}
	if removed.Status != execution.ProcessSucceeded {
		return removal, fmt.Errorf("remove preserved worktree failed with exit code %d: %s", removed.ExitCode, strings.TrimSpace(removed.Stderr))
	}
	// As the integrated cleanup does, a removal that succeeded stays reported as
	// one even where the confirmation cannot run: the artifact is gone whether or
	// not this could ask again, and only observing it still registered clears the
	// flag.
	stillRegistered, _, err := m.registeredWorktree(ctx, path)
	if err != nil {
		removal.Removed = true
		return removal, fmt.Errorf("verify removal of preserved worktree %s: %w", path, err)
	}
	if stillRegistered {
		return removal, fmt.Errorf("preserved worktree %s is still registered after removal", path)
	}
	removal.Removed = true
	return removal, nil
}

// Prune is what one repository-wide prune removed: the registrations whose
// checkout was no longer on disk, named by the path they used to point at.
type Prune struct {
	Pruned []string `json:"pruned,omitempty"`
}

// PruneRegistrations removes every registration in this repository whose
// checkout is already gone, whichever run or person left it behind.
//
// It is the one removal here that is not derived from a recorded worktree, and
// it is safe to be: Git prunes a registration only where the directory it points
// at does not exist, so there is nothing on disk left for it to lose. That is
// also why it covers what the recorded sweeps cannot — a checkout somebody
// deleted by hand, one belonging to a run record that is itself gone, one from
// a product this harness no longer holds. Every one of those is invisible to a
// sweep driven from run state and still costs every later command the deny path
// its registration puts in the sandbox profile.
//
// The lease is what makes a prune safe to ask for at all. Git judges a
// registration stale by whether its gitdir file is there, which is exactly what
// a `git worktree add` has not written yet while it is filling the entry in, so
// an unguarded prune deletes a registration out from under a run that is being
// created beside it — the race maintenanceOptions exists to keep Git from
// starting on its own. Taking the lease puts this prune in the same queue as
// every creation and removal, which is the one place it cannot reach that
// window.
//
// What was pruned is derived from the bookkeeping before and after rather than
// from what Git printed, so it does not depend on the wording of a message.
func (m *Manager) PruneRegistrations(ctx context.Context) (Prune, error) {
	lease, err := m.leaseRegistry(ctx)
	if err != nil {
		return Prune{}, err
	}
	// Releasing is this process letting the next write in, and the operating
	// system does it anyway when the process exits, so a close that failed says
	// nothing about the registrations below.
	defer func() { _ = lease.release() }()

	before, err := m.listWorktrees(ctx)
	if err != nil {
		return Prune{}, err
	}
	pruned, err := m.run(ctx, "-C", m.repositoryRoot, "worktree", "prune")
	if err != nil {
		return Prune{}, err
	}
	if pruned.Status != execution.ProcessSucceeded {
		return Prune{}, fmt.Errorf("prune stale worktree registrations failed with exit code %d: %s", pruned.ExitCode, strings.TrimSpace(pruned.Stderr))
	}
	after, err := m.listWorktrees(ctx)
	if err != nil {
		return Prune{}, fmt.Errorf("read the worktree registrations after pruning: %w", err)
	}
	remaining := make(map[string]struct{}, len(after))
	for _, entry := range after {
		remaining[entry.path] = struct{}{}
	}
	var gone []string
	for _, entry := range before {
		if _, still := remaining[entry.path]; !still {
			gone = append(gone, entry.path)
		}
	}
	return Prune{Pruned: gone}, nil
}
