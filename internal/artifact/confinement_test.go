package artifact

import (
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
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
