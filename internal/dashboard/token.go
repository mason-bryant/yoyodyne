package dashboard

// Where a supplied bearer token lives, named once.
//
// Under the loopback default the token is generated at each start and printed
// once, and nothing here is consulted. A dashboard bound outside loopback is
// the operator's opt-in, and its token has to be supplied — a token printed to
// one terminal is unusable from the other device the opt-in exists for — from
// a store that is not the committed configuration. The two stores are the ones
// the Slack tokens already use, named for the product for the reason theirs
// are: a machine running several harnesses has several dashboards, and a token
// under a generic name is right for at most one of them.
//
// Nothing here reads a token. These are the names a reader and a diagnosis
// agree on, so the diagnosis that asks whether the token is stored and the
// command that will read it look in one place.

import (
	"path/filepath"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// TokenSecret names this product's dashboard token in the macOS keychain, under
// the same account the Slack pair is stored under.
func TokenSecret(productID domain.ProductID) string {
	return "yoyo-dashboard." + string(productID)
}

// TokenFileName is the file a token is kept in under the product's state
// directory, on a machine with no keychain or by an operator's choice.
const TokenFileName = "dashboard.token"

// TokenFile is where the file-sourced token lives: under the state root, in
// the product's own directory beside its runs and conversations, so it is
// outside the repository and belongs to this machine like everything else
// there.
func TokenFile(stateRoot string, productID domain.ProductID) string {
	return filepath.Join(filepath.Clean(stateRoot), "products", string(productID), TokenFileName)
}
