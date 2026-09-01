package gitworktree

import (
	"testing"

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
