package artifact

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// defaultConfiguration is a project that says nothing about where its artifacts
// live: the version, the product's identity, the approvals that have no harness
// default, and the one agent a configuration has to declare. Every artifact
// directory it resolves to is the shipped default rather than something this
// document chose, which is the whole of what the test below is about.
const defaultConfiguration = `version: 1
product:
  id: example
  repository: .
approvals:
  brief: human
  goals: human
  designs: automatic
  integration: human
agents:
  developer:
    role: developer
    backend: claude-code
    model: opus
`

// TestTheDefaultConfigurationExcludesTheInvariantsHome holds the exclusion to
// the shipped defaults rather than to a store a test assembled. Everywhere else
// in this package the exclusion is stated by the test itself, which says the
// store honours what it is told and says nothing about the project that
// configures nothing — and the claim made downstream of this is absolute: an
// invariant carries an identity scheme of its own that an artifact write never
// reaches. A default that stopped excluding the invariants home would leave that
// claim true only of the projects that had written the exclusion down.
func TestTheDefaultConfigurationExcludesTheInvariantsHome(t *testing.T) {
	t.Parallel()

	configured, err := config.Decode(strings.NewReader(defaultConfiguration))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	product := configured.Product
	store := StoreFor(temporaryRepository(t), product)

	// The refusal below is about the exclusion only while the invariants home is
	// inside an artifact home. A default that moved it outside every home would
	// refuse the write for the other reason and the exclusion could rot behind
	// that, so what the defaults are is asserted rather than assumed.
	if !strings.HasPrefix(product.Invariants, product.Decisions+"/") {
		t.Fatalf("the default invariants home %q is not inside the default decisions home %q", product.Invariants, product.Decisions)
	}

	before := snapshot(t, store.RepositoryRoot)
	_, err = store.Create(domain.RoleArchitect, Draft{
		ID:        "filed-as-a-decision",
		Kind:      KindDecision,
		Title:     "A decision filed where the invariants live",
		Directory: product.Invariants,
		Body:      "A document written under the artifacts' identity scheme.",
		Reason:    "because",
	}, moment())
	if err == nil || !strings.Contains(err.Error(), "identity scheme of its own") {
		t.Fatalf("Create() into %q error = %v, want the exclusion to refuse it", product.Invariants, err)
	}
	assertUnchanged(t, before, snapshot(t, store.RepositoryRoot))

	// And the refusal is the exclusion rather than a default-configured store
	// that refuses everything: the decisions home the invariants sit inside takes
	// the same write.
	if _, err := store.Create(domain.RoleArchitect, Draft{
		ID:        "markdown-source-of-truth",
		Kind:      KindDecision,
		Title:     "Markdown is the source of truth",
		Directory: product.Decisions,
		Body:      "A decision record filed where decision records go.",
		Reason:    "because",
	}, moment()); err != nil {
		t.Fatalf("Create() into %q error = %v", product.Decisions, err)
	}

	// Nothing already inside the invariants home is read as an artifact either,
	// so a file that landed there some other way is not governed under two
	// identity schemes at once.
	write(t, store, product.Invariants+"/one-writer-per-item.md",
		document("one-writer-per-item", "decision", "One writer per item", nil, "active")+"\nThe constraint.\n")
	set, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(set.Problems) != 0 {
		t.Fatalf("problems = %v", set.Problems)
	}
	if _, found := set.Find("one-writer-per-item"); found {
		t.Fatal("Load() read a document inside the invariants home as an artifact")
	}
}
