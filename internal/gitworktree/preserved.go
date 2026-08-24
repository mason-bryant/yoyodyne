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

// Prune is what one pass over the repository's worktree bookkeeping removed:
// the registrations whose checkout is no longer on disk, and how many
// registrations the repository is left carrying.
//
// The count is reported rather than left for a caller to work out, because it is
// the number that matters. Every registration is a path an agent's sandbox
// profile carries on every command it spawns, so bookkeeping that grows with the
// harness's history eventually stops a developer running anything at all — which
// is a failure that looks like a broken machine rather than like a worktree
// nobody removed.
type Prune struct {
	Pruned     []string `json:"pruned,omitempty"`
	Registered int      `json:"registered"`
}

// PruneRegistrations removes the registrations Git judges stale — the ones whose
// checkout directory is gone — and reports what went and what is left.
//
// It is the one removal here that reaches a registration no run record knows
// about: a checkout somebody deleted by hand leaves bookkeeping the run's own
// path-by-name removal can no longer act on, because there is no directory left
// for `git worktree remove` to be pointed at.
//
// It queues on the registry lease for the reason every other write to that
// bookkeeping does, and for one more: a prune reaching a registration another
// run is still filling in deletes it out from under that run, which is exactly
// the loss the lease exists to prevent.
func (m *Manager) PruneRegistrations(ctx context.Context) (Prune, error) {
	lease, err := m.leaseRegistry(ctx)
	if err != nil {
		return Prune{}, err
	}
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
		return Prune{}, err
	}
	// What went is worked out from the two listings rather than from what the
	// prune printed. That output is Git's own to word and to change; the listing
	// is the same bookkeeping every other decision here is made from.
	kept := make(map[string]struct{}, len(after))
	for _, entry := range after {
		kept[entry.path] = struct{}{}
	}
	prune := Prune{Registered: len(after)}
	for _, entry := range before {
		if _, survived := kept[entry.path]; !survived {
			prune.Pruned = append(prune.Pruned, entry.path)
		}
	}
	return prune, nil
}

// WorktreeRemoval is what one attempt to retire a preserved worktree found.
// Kept is why it was left where it is, and is empty both when the worktree was
// removed and when there was nothing there to remove — Removed is what tells
// those two apart.
type WorktreeRemoval struct {
	Path    string `json:"path"`
	Removed bool   `json:"removed"`
	Kept    string `json:"kept,omitempty"`
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
