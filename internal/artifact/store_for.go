package artifact

import "github.com/mason-bryant/yoyodyne/internal/config"

// StoreFor assembles the artifact store a product configuration describes. It
// is the one place the configuration's homes become a store, shared by the
// commands and by the repository tests, so a home added to the configuration
// cannot be read by one and silently unknown to the other.
func StoreFor(repositoryRoot string, product config.Product) Store {
	return Store{
		RepositoryRoot: repositoryRoot,
		Homes:          []string{product.Specifications, product.Designs, product.Decisions},
		Excluded:       []string{product.Invariants},
	}
}
