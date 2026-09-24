package contextbundle

import (
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/repowrite/readertest"
)

// The product context is held to the same topology matrix as the artifact and
// invariant readers. What it assembles is the evidence a product conversation
// reasons from, so a specification read from outside the repository is intent
// nobody wrote here arriving as the product's own — the same defect the other two
// carry, in the one place a role is least able to check it.
func TestTheProductContextIsConfinedToTheRepository(t *testing.T) {
	t.Parallel()

	readertest.Run(t, readertest.Reader{
		Name:      "the product context",
		Directory: "docs/product",
		File:      "example-brief.md",
		Document:  wellFormed,
		Read: func(t *testing.T, root string) ([]string, error) {
			bundle, err := AssembleProduct(ProductRequest{
				RepositoryRoot:          root,
				SpecificationsDirectory: "docs/product",
			})
			if err != nil {
				return nil, err
			}
			delivered := make([]string, 0, len(bundle.References))
			for _, reference := range bundle.References {
				delivered = append(delivered, reference.Path)
			}
			return delivered, nil
		},
	})
}
