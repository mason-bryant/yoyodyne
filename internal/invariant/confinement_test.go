package invariant

import (
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/repowrite/readertest"
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

// The invariant reader is held to the same topologies as the writer, because a
// constraint read from outside the repository is the worse half of the same
// defect: it is delivered to every developer's context and every reviewer's
// evidence as binding, under a path the repository looks like it holds, and
// nothing downstream can tell it from one somebody committed.
func TestTheInvariantReaderIsConfinedToTheRepository(t *testing.T) {
	t.Parallel()

	document, err := render(built("example-constraint", nil))
	if err != nil {
		t.Fatalf("render() error = %v", err)
	}
	readertest.Run(t, readertest.Reader{
		Name:      "the invariant store",
		Directory: invariantsDirectory,
		File:      "example-constraint.md",
		Document:  document,
		Read: func(t *testing.T, root string) ([]string, error) {
			set, err := Store{RepositoryRoot: root, Directory: invariantsDirectory}.Load()
			if err != nil {
				return nil, err
			}
			var delivered []string
			for _, recorded := range append(append([]Invariant(nil), set.Active...), set.Retired...) {
				delivered = append(delivered, recorded.Path)
			}
			return delivered, nil
		},
	})
}
