package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/capability"
)

// deliveryCatalog is the catalog the fixtures are validated against: the actions
// the delivery pipeline registers today, by the names it registers them under.
// It is written out here rather than taken from the orchestrator's registry
// because nothing in the delivery path depends on this package yet, and this
// package depending on that one would be the dependency pointing the other way.
func deliveryCatalog(t *testing.T) Catalog {
	t.Helper()

	catalog, err := NewCatalog(
		CatalogEntry{Action: "work-item.claim", Capabilities: []capability.Capability{capability.WorkItemRead, capability.WorkItemMutate}},
		CatalogEntry{Action: "candidate.develop", Capabilities: []capability.Capability{capability.ProviderInvoke, capability.WorktreeMutate}},
		CatalogEntry{Action: "candidate.publish", Capabilities: []capability.Capability{capability.ForgePublish}},
		CatalogEntry{Action: "candidate.check", Capabilities: []capability.Capability{capability.ChecksExecute}},
		CatalogEntry{Action: "candidate.review", Capabilities: []capability.Capability{capability.ProviderInvoke}},
		CatalogEntry{Action: "candidate.integrate", Capabilities: []capability.Capability{capability.PromotionLease, capability.TargetBranchMutate}},
		CatalogEntry{Action: "run.clean-up", Capabilities: []capability.Capability{capability.WorktreeMutate}},
	)
	if err != nil {
		t.Fatalf("NewCatalog() error = %v", err)
	}
	return catalog
}

// loadFixture reads one of the definitions under testdata and validates it,
// failing the test if it does not pass: every fixture there is a definition this
// schema is supposed to be able to express.
func loadFixture(t *testing.T, name string) Validated {
	t.Helper()

	file, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	defer file.Close()

	validated, err := Load(file, deliveryCatalog(t))
	if err != nil {
		t.Fatalf("Load(%s) error = %v", name, err)
	}
	return validated
}

func TestADefinitionDecodesToWhatItSays(t *testing.T) {
	t.Parallel()

	definition := loadFixture(t, "delivery.yaml").Definition()
	if definition.Schema != SchemaVersion {
		t.Errorf("Schema = %d, want %d", definition.Schema, SchemaVersion)
	}
	if definition.ID != "delivery" {
		t.Errorf("ID = %q, want delivery", definition.ID)
	}
	if definition.Initial != "claim" {
		t.Errorf("Initial = %q, want claim", definition.Initial)
	}
	if len(definition.States) != 7 {
		t.Errorf("the definition holds %d states, want the delivery loop's 7", len(definition.States))
	}
	review, declared := definition.States["review"]
	if !declared {
		t.Fatal(`the definition declares no "review" state`)
	}
	if review.Action != "candidate.review" {
		t.Errorf("review.Action = %q, want candidate.review", review.Action)
	}
	if destination := review.On["changes-requested"]; destination != "develop" {
		t.Errorf("a review asking for changes goes to %q, want develop", destination)
	}
	if _, ends := definition.Terminals["delivered"]; !ends {
		t.Error(`the definition declares no "delivered" terminal`)
	}
}

// TestAnUnknownFieldIsRefused is the whole reason the decode is strict. A
// misspelled key that decoded quietly would be a transition nobody wrote.
func TestAnUnknownFieldIsRefused(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		source string
		says   string
	}{
		{
			name: "at the top level",
			source: `
schema: 1
id: delivery
inital: claim
initial: claim
states:
  claim:
    action: work-item.claim
    on: {claimed: delivered}
terminals:
  delivered: {}
`,
			says: "inital",
		},
		{
			name: "inside a state",
			source: `
schema: 1
id: delivery
initial: claim
states:
  claim:
    action: work-item.claim
    unless: {blocked: delivered}
    on: {claimed: delivered}
terminals:
  delivered: {}
`,
			says: "unless",
		},
		{
			name: "inside a terminal",
			source: `
schema: 1
id: delivery
initial: claim
states:
  claim:
    action: work-item.claim
    on: {claimed: delivered}
terminals:
  delivered:
    then: claim
`,
			says: "then",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := Decode(strings.NewReader(test.source))
			if err == nil {
				t.Fatalf("Decode() accepted a definition with an unknown field %q in it", test.says)
			}
			if !strings.Contains(err.Error(), test.says) {
				t.Errorf("Decode() error = %v, and it does not name the field it refused", err)
			}
		})
	}
}

func TestADefinitionThatIsNotOneIsRefused(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		source string
		says   string
	}{
		{name: "empty", source: "", says: "the definition is empty"},
		{name: "two documents", source: "schema: 1\n---\nschema: 1\n", says: "one file is one workflow"},
		{name: "not a mapping", source: "- schema: 1\n", says: "decode workflow definition"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := Decode(strings.NewReader(test.source))
			if err == nil {
				t.Fatalf("Decode() accepted %s", test.name)
			}
			if !strings.Contains(err.Error(), test.says) {
				t.Errorf("Decode() error = %v, want it to say %q", err, test.says)
			}
		})
	}
}

// TestLoadRefusesADecodedDefinitionThatDoesNotValidate is what makes Load the
// door worth using: a file can be perfectly well-formed YAML and still describe
// a sequence nothing could run.
func TestLoadRefusesADecodedDefinitionThatDoesNotValidate(t *testing.T) {
	t.Parallel()

	source := `
schema: 1
id: delivery
initial: claim
states:
  claim:
    action: work-item.claim
    on: {claimed: reviewed}
terminals:
  delivered: {}
`
	_, err := Load(strings.NewReader(source), deliveryCatalog(t))
	if err == nil {
		t.Fatal("Load() accepted a definition whose only transition goes nowhere")
	}
	if !strings.Contains(err.Error(), `"reviewed"`) {
		t.Errorf("Load() error = %v, and it does not name the destination it refused", err)
	}
}
