package artifact

import (
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/repowrite/readertest"
	"github.com/mason-bryant/yoyodyne/internal/repowrite/writertest"
)

// The artifact writer is held to the same topology matrix as every other
// repository-confined writer, rather than to a set of cases of its own. What it
// files are the documents that say what the product intends, and one written
// outside the repository is intent nobody reviews and nobody finds again.
func TestTheArtifactWriterIsConfinedToTheRepository(t *testing.T) {
	t.Parallel()

	writertest.Run(t, writertest.Writer{
		Name:      "the artifact store",
		Directory: productHome,
		File:      "example-brief.md",
		Write: func(t *testing.T, root string) error {
			store := Store{
				RepositoryRoot: root,
				Homes:          []string{productHome},
			}
			_, err := store.Create(domain.RoleProductManager, Draft{
				ID:        "example-brief",
				Kind:      KindBrief,
				Title:     "Example brief",
				Directory: productHome,
				Body:      "What this product is for.",
				Reason:    "the topology matrix",
			}, moment())
			return err
		},
	})
}

// The artifact reader is held to the same topologies as the writer. A document
// read from outside the repository arrives as the product's own recorded intent,
// named by a path inside the repository, and every role downstream reasons from
// it as though somebody had written and reviewed it here.
func TestTheArtifactReaderIsConfinedToTheRepository(t *testing.T) {
	t.Parallel()

	readertest.Run(t, readertest.Reader{
		Name:      "the artifact store",
		Directory: productHome,
		File:      "example-brief.md",
		Document:  document("example-brief", "brief", "Example brief", nil, "active") + "\nWhat this product is for.\n",
		Read: func(t *testing.T, root string) ([]string, error) {
			set, err := Store{RepositoryRoot: root, Homes: []string{productHome}}.Load()
			if err != nil {
				return nil, err
			}
			delivered := make([]string, 0, len(set.Artifacts))
			for _, recorded := range set.Artifacts {
				delivered = append(delivered, recorded.Path)
			}
			return delivered, nil
		},
	})
}
