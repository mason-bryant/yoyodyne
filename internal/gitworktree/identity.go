package gitworktree

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// ContentIdentityPrefix opens every identity ContentIdentity names, so a reader
// can tell one from a commit: both are hexadecimal, and a commit is what every
// other identifier on a run's record is.
const ContentIdentityPrefix = "sha256:"

// hashObjectBatch bounds how many paths one `git hash-object` is handed, so a
// change touching thousands of files does not build a command line the
// operating system refuses.
const hashObjectBatch = 128

// ContentIdentity names the content of a run's change as it stands in the
// worktree: a digest over the recorded base, every path the change touches
// against it, and the blob Git would store for each one as it is on disk now,
// or that it is gone. Two readings agree exactly when the tree holds the same
// change against the same base, and a single byte moved in any file the change
// touches — tracked or untracked — moves the identity. Whether the attempt has
// been committed does not: a file is named by its path and its content, not by
// whether the index has heard of it, so a publishing run, which commits before
// its checks, and one that does not are bound by the same reading.
//
// It exists for evidence that has to name a revision on a project that has made
// no commit to name. A publishing run commits each attempt before its checks
// run, so a passing check phase is bound to that commit; a project that does not
// publish commits nothing until the promotion itself, and until this the only
// thing binding its checks to the change they passed over was the attempt
// count, which says when the checks ran and nothing about what they ran over.
//
// It only reads. `git hash-object` without `-w` computes the id and stores
// nothing, so the worktree, its index, and the object store are exactly as the
// developer left them. The one Git command that would answer this in a line —
// `write-tree` — needs an index to write from, and the run's index carries the
// held-out exports, so it is not asked.
//
// A path that is not a regular file — a symlink, a socket — is named by what it
// is rather than hashed: Git would store a symlink's target, and a target that
// leaves the worktree is exactly the file this must not open.
func (m *Manager) ContentIdentity(ctx context.Context, worktree Worktree) (string, error) {
	path, _, err := m.verifyOwnedHead(ctx, worktree)
	if err != nil {
		return "", err
	}
	entries, err := m.changedEntries(ctx, path, worktree.BaseCommit)
	if err != nil {
		return "", err
	}
	untracked, err := m.untrackedFiles(ctx, path)
	if err != nil {
		return "", err
	}
	for _, relative := range untracked {
		entries = append(entries, contentEntry{path: relative})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })

	// Every entry that still has a file behind it is hashed; a deletion is named
	// as one, and a path that is not a regular file is named by its mode.
	var toHash []int
	for i := range entries {
		if entries[i].status == "D" {
			entries[i].blob = "deleted"
			continue
		}
		info, err := os.Lstat(filepath.Join(path, filepath.FromSlash(entries[i].path)))
		switch {
		case err != nil:
			return "", fmt.Errorf("inspect %s for its content identity: %w", entries[i].path, err)
		case !info.Mode().IsRegular():
			entries[i].blob = "mode:" + info.Mode().Type().String()
		default:
			toHash = append(toHash, i)
		}
	}
	for start := 0; start < len(toHash); start += hashObjectBatch {
		end := min(start+hashObjectBatch, len(toHash))
		args := []string{"-C", path, "hash-object", "--"}
		for _, i := range toHash[start:end] {
			args = append(args, entries[i].path)
		}
		result, err := m.run(ctx, args...)
		if err != nil {
			return "", err
		}
		if result.Status != execution.ProcessSucceeded {
			return "", fmt.Errorf("hash changed worktree files failed with exit code %d: %s", result.ExitCode, strings.TrimSpace(result.Stderr))
		}
		blobs := strings.Fields(result.Stdout)
		if len(blobs) != end-start {
			return "", fmt.Errorf("hash changed worktree files reported %d ids for %d paths", len(blobs), end-start)
		}
		for offset, i := range toHash[start:end] {
			entries[i].blob = blobs[offset]
		}
	}

	digest := sha256.New()
	fmt.Fprintf(digest, "base %s\n", worktree.BaseCommit)
	for _, entry := range entries {
		fmt.Fprintf(digest, "%s\t%s\n", entry.path, entry.blob)
	}
	return ContentIdentityPrefix + hex.EncodeToString(digest.Sum(nil)), nil
}

// contentEntry is one path a change touches: what became of it against the
// base, in Git's one-letter status where the file is tracked, and the blob its
// content is now.
type contentEntry struct {
	status string
	path   string
	blob   string
}

// changedEntries lists the tracked half of a change against the base, with
// rename detection off for the reason ChangedPaths turns it off: a rename is a
// deletion and an addition, and an identity has to name both sides.
func (m *Manager) changedEntries(ctx context.Context, path, baseCommit string) ([]contentEntry, error) {
	result, err := m.run(ctx, "-C", path, "diff", "--name-status", "-z", "--no-renames", "--no-ext-diff", baseCommit, "--")
	if err != nil {
		return nil, err
	}
	if result.Status != execution.ProcessSucceeded {
		return nil, fmt.Errorf("list changed worktree paths failed with exit code %d: %s", result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	// Each entry is a status field and then a path field, both NUL-terminated;
	// with renames off there is never a second path.
	listing := strings.TrimSuffix(strings.TrimSuffix(result.Stdout, "\n"), "\x00")
	if listing == "" {
		return nil, nil
	}
	fields := strings.Split(listing, "\x00")
	if len(fields)%2 != 0 {
		return nil, fmt.Errorf("list changed worktree paths reported %q, which does not pair into statuses and paths", fields)
	}
	entries := make([]contentEntry, 0, len(fields)/2)
	for i := 0; i < len(fields); i += 2 {
		entries = append(entries, contentEntry{status: fields[i], path: fields[i+1]})
	}
	return entries, nil
}
