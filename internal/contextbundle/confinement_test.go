package contextbundle

import (
	"strings"
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

// A role's own documents are read from a directory of their own, so they are held
// to the matrix separately: a design read from outside the repository reaches the
// role it is for as a design somebody here recorded.
func TestTheRoleDocumentsAreConfinedToTheRepository(t *testing.T) {
	t.Parallel()

	// The specifications are kept out of `docs` so that a symlinked `docs` is
	// refused by the read this test is about rather than by the specifications
	// walk that happens to run first.
	readertest.Run(t, readertest.Reader{
		Name:      "the role documents",
		Directory: "docs/design",
		File:      "example-design.md",
		Document:  wellFormed,
		Read: func(t *testing.T, root string) ([]string, error) {
			bundle, err := AssembleProduct(ProductRequest{
				RepositoryRoot:          root,
				SpecificationsDirectory: "specifications",
				RoleDocuments:           []DocumentSet{{Label: "Design", Directory: "docs/design"}},
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

// Shipped documentation is named path by path rather than discovered, so no walk
// stands in front of it: the path is the whole of what is resolved, and a
// symlinked parent anywhere along it must be refused by that resolution alone.
func TestTheShippedDocumentationIsConfinedToTheRepository(t *testing.T) {
	t.Parallel()

	// The specifications are kept out of `docs` for the reason the role documents
	// test gives.
	readertest.Run(t, readertest.Reader{
		Name:      "the shipped documentation",
		Directory: "docs/guide",
		File:      "example-guide.md",
		Document:  wellFormed,
		Read: func(t *testing.T, root string) ([]string, error) {
			bundle, err := AssembleProduct(ProductRequest{
				RepositoryRoot:          root,
				SpecificationsDirectory: "specifications",
				ShippedDocumentation:    []string{"docs/guide/example-guide.md"},
			})
			if err != nil {
				return nil, err
			}
			// Shipped documentation is carried in the text rather than recorded as a
			// reference, so what was delivered is read off the headings it is carried
			// under.
			var delivered []string
			for _, line := range strings.Split(bundle.Text, "\n") {
				if carried, ok := strings.CutPrefix(line, "### Shipped documentation: "); ok {
					delivered = append(delivered, carried)
				}
			}
			return delivered, nil
		},
	})
}
