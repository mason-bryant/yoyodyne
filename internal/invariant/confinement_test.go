package invariant

import (
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/repowrite/writertest"
)

// The invariant writer is held to the same topology matrix as every other
// repository-confined writer, rather than to a set of cases of its own. A
// constraint written outside the repository is one no developer is ever
// delivered and no reviewer ever sees, while the harness reports it as recorded.
func TestTheInvariantWriterIsConfinedToTheRepository(t *testing.T) {
	t.Parallel()

	writertest.Run(t, writertest.Writer{
		Name:      "the invariant store",
		Directory: invariantsDirectory,
		File:      "example-constraint.md",
		Write: func(t *testing.T, root string) error {
			store := Store{RepositoryRoot: root, Directory: invariantsDirectory}
			_, err := store.Create(domain.RoleArchitect, draft("example-constraint"), moment())
			return err
		},
	})
}
