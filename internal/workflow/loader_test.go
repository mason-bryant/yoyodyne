package workflow

import (
	"strings"
	"testing"
)

// TestADefinitionThatChangedUnderItsPinIsRefused is what a pin is for. An
// instance records the digest it started against, and a later load reads whatever
// the file says now — so the loader refuses the file rather than adopting a
// sequence nobody started.
func TestADefinitionThatChangedUnderItsPinIsRefused(t *testing.T) {
	t.Parallel()

	loader := deliveryLoader(t, &run{})
	pinned, err := loader.LoadFile("testdata/delivery.yaml")
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}
	loader.Pin = pinned.Digest()

	amended, err := loader.LoadFile("testdata/delivery-amended.yaml")
	if err == nil {
		t.Fatal("LoadFile() adopted a definition that is not the one the caller pinned")
	}
	if !strings.Contains(err.Error(), "delivery-amended.yaml") {
		t.Errorf("LoadFile() error = %v, and it does not name the file it refused", err)
	}
	if !strings.Contains(err.Error(), pinned.Digest()) {
		t.Errorf("LoadFile() error = %v, and it does not say what the caller is pinned to", err)
	}
	if amended.Digest() != "" {
		t.Errorf("a refused load carries the digest %q", amended.Digest())
	}

	// The pin is over what a definition says rather than over the bytes it was
	// written in, so the same workflow written down differently passes it.
	rewritten, err := loader.LoadFile("testdata/delivery-rewritten.yaml")
	if err != nil {
		t.Fatalf("LoadFile() refused the same workflow written differently: %v", err)
	}
	if rewritten.Digest() != pinned.Digest() {
		t.Errorf("Digest() = %q, want the pinned %q", rewritten.Digest(), pinned.Digest())
	}
}

// TestAnUnpinnedLoadIsWhereAPinComesFrom: the first load has nothing to compare
// against, and that is not a refusal.
func TestAnUnpinnedLoadIsWhereAPinComesFrom(t *testing.T) {
	t.Parallel()

	graph, err := deliveryLoader(t, &run{}).LoadFile("testdata/delivery-amended.yaml")
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}
	if graph.Digest() == "" {
		t.Error("an unpinned load produced a graph with no digest to pin to")
	}
}

// TestTheLoaderRefusesADefinitionThisBuildCannotRead is the schema violation
// arriving at the loader's own door: a strict decode and the version check behind
// it, before anything about actions or authority is considered.
func TestTheLoaderRefusesADefinitionThisBuildCannotRead(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		source string
		says   string
	}{
		{
			name: "a schema this build does not read",
			source: `
schema: 2
id: delivery
initial: claim
states:
  claim:
    action: work-item.claim
    on: {claimed: delivered}
terminals:
  delivered: {}
`,
			says: "this build reads version 1",
		},
		{
			name: "a key the schema does not describe",
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
			name: "a transition into nothing",
			source: `
schema: 1
id: delivery
initial: claim
states:
  claim:
    action: work-item.claim
    on: {claimed: reviewed}
terminals:
  delivered: {}
`,
			says: `"reviewed"`,
		},
		{
			name: "an action nothing registers",
			source: `
schema: 1
id: delivery
initial: claim
states:
  claim:
    action: candidate.rubber-stamp
    on: {stamped: delivered}
terminals:
  delivered: {}
`,
			says: `"candidate.rubber-stamp"`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			acted := &run{}
			graph, err := deliveryLoader(t, acted).Load(strings.NewReader(test.source))
			if err == nil {
				t.Fatalf("Load() accepted a definition with %s", test.name)
			}
			if !strings.Contains(err.Error(), test.says) {
				t.Errorf("Load() error = %v, want it to say %q", err, test.says)
			}
			// Nothing about a refused definition is adopted, and nothing it named was
			// reached on the way to refusing it.
			if states := graph.States(); len(states) != 0 || graph.ID() != "" {
				t.Errorf("a refused load carries the id %q and the states %v", graph.ID(), states)
			}
			if len(acted.performed) != 0 {
				t.Errorf("refusing a definition performed %v", acted.performed)
			}
		})
	}
}

// TestTheLoaderValidatesAgainstItsOwnRegistry is what keeps the actions a
// definition may select and the actions this build can perform one list: the
// catalog validation uses is the registry's own, so the refusal says what is
// actually registered.
func TestTheLoaderValidatesAgainstItsOwnRegistry(t *testing.T) {
	t.Parallel()

	source := `
schema: 1
id: delivery
initial: claim
states:
  claim:
    action: candidate.rubber-stamp
    on: {stamped: delivered}
terminals:
  delivered: {}
`
	_, err := deliveryLoader(t, &run{}).Load(strings.NewReader(source))
	if err == nil {
		t.Fatal("Load() accepted a definition selecting an action nothing registers")
	}
	if !strings.Contains(err.Error(), "candidate.integrate") {
		t.Errorf("Load() error = %v, and it does not say what this build registers instead", err)
	}
}

// TestAFileThatIsNotThereIsRefusedAsOne. A missing definition is not a defect in
// a definition, and saying so is what stops somebody looking for a schema
// mistake in a file that does not exist.
func TestAFileThatIsNotThereIsRefusedAsOne(t *testing.T) {
	t.Parallel()

	_, err := deliveryLoader(t, &run{}).LoadFile("testdata/no-such-workflow.yaml")
	if err == nil {
		t.Fatal("LoadFile() found a definition that is not there")
	}
	if !strings.Contains(err.Error(), "read workflow definition") {
		t.Errorf("LoadFile() error = %v, want it to say the file could not be read", err)
	}
	if !strings.Contains(err.Error(), "no-such-workflow.yaml") {
		t.Errorf("LoadFile() error = %v, and it does not name the file", err)
	}
}
