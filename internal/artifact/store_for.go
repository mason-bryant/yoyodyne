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
		// Which home each kind is filed in, which the list above cannot say: three
		// directories in configuration order do not tell anything which of them a
		// design belongs in. It is read by the write path, where a role has to be
		// told where its own documents go and a document filed in another kind's
		// home has to be refused — "somewhere among the homes" is not an answer
		// either of those can use.
		KindHomes: map[Kind]string{
			KindBrief:         product.Specifications,
			KindGoals:         product.Specifications,
			KindNonGoals:      product.Specifications,
			KindDesign:        product.Designs,
			KindSpecification: product.Designs,
			KindDecision:      product.Decisions,
		},
	}
}
