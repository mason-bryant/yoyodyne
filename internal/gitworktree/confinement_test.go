package gitworktree

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/repowrite"
	"github.com/mason-bryant/yoyodyne/internal/repowrite/writertest"
)

// Refreshing a worktree's exports is a repository-confined write like any other,
// and is held to the same topology matrix rather than to cases of its own. Where
// it lands is decided by a path a project names — the tracker's export directory
// — resolved inside a checkout the harness cut but a developer has a shell in, so
// a symlink below it is content rather than an attack, and a copy that followed
// one out would be a file the harness says it put in the worktree and nobody can
// find there.
func TestTheExportRefreshIsConfinedToTheWorktree(t *testing.T) {
	t.Parallel()

	writertest.Run(t, writertest.Writer{
		Name:      "the current-export refresh",
		Directory: ".beads",
		File:      "issues.jsonl",
		Write: func(t *testing.T, root string) error {
			return writeExport(root, exportPath, []byte(currentExport))
		},
	})
}

// Clearing a registration a killed `git worktree add` left behind is the one
// mutation this package makes to the repository's own bookkeeping, and it is
// confined the same way a write is: through the primitive that resolves the
// path against the filesystem immediately before it acts. The topology matrix
// above is about bytes landing somewhere, and this is about a directory being
// taken away somewhere, so the case is stated here rather than driven from it —
// what it asks is the same question in the other direction: is what is outside
// the repository still there.
func TestClearingARegistrationThroughASymlinkOutOfTheGitDirectoryIsRefused(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	common := filepath.Join(base, "common")
	outside := filepath.Join(base, "outside")
	entry := filepath.Join(outside, "yoyodyne-killed-0a1b2c3d")
	if err := os.MkdirAll(entry, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.MkdirAll(common, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(common, worktreeRegistrations)); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	root, err := repowrite.NewRoot(common)
	if err != nil {
		t.Fatalf("NewRoot() error = %v", err)
	}

	if err := clearRegistration(root, "yoyodyne-killed-0a1b2c3d"); err == nil {
		t.Fatal("clearRegistration() removed an entry the repository does not contain")
	}
	if _, err := os.Lstat(entry); err != nil {
		t.Fatalf("Lstat(%s) error = %v, want the entry outside the repository untouched", entry, err)
	}
}
