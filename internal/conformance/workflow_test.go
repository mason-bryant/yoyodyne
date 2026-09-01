package conformance

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/capability"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func TestTheShippedDefinitionCompilesAndSelectsEveryRegisteredCheck(t *testing.T) {
	t.Parallel()
	definition, err := Compile("")
	if err != nil {
		t.Fatalf("the definition this build ships does not compile: %v", err)
	}
	if definition.Graph.ID() != WorkflowID || definition.Source != BuiltinDefinitionSource {
		t.Fatalf("compiled %q from %q", definition.Graph.ID(), definition.Source)
	}

	// Every registered check is sequenced, and every sequenced state performs a
	// registered check. A check nobody selects is a gate somebody believes is
	// running, which is the expensive half of this pair.
	selected := map[string]bool{}
	for _, state := range definition.Graph.States() {
		node, _ := definition.Graph.Node(state)
		selected[node.Action().Name] = true
	}
	for _, registeredAction := range registered() {
		if !selected[registeredAction.Name] {
			t.Fatalf("%s is registered and the shipped definition never selects it", registeredAction.Name)
		}
	}
	if len(selected) != len(registered()) {
		t.Fatalf("the definition selects %d distinct actions and %d are registered", len(selected), len(registered()))
	}
}

func TestTheGateHoldsNoAuthorityThatWritesAnything(t *testing.T) {
	t.Parallel()
	grant, err := Grant()
	if err != nil {
		t.Fatalf("Grant() error = %v", err)
	}
	// The whole of what the gate may do, named rather than described: a check
	// that acquired anything else would have to be given it here, in code, and
	// no definition can add to this list.
	want := []capability.Capability{capability.WorkItemRead, capability.RepositoryRead}
	if got := grant.Capabilities(); !slices.Equal(got, want) {
		t.Fatalf("the release-readiness gate is compiled under %v, want %v", got, want)
	}
	for _, registeredAction := range registered() {
		for _, required := range registeredAction.Capabilities {
			if !slices.Contains(want, required) {
				t.Fatalf("%s requires %q, which a read-only gate does not hold", registeredAction.Name, required)
			}
		}
	}
}

func TestADefinitionSelectingSomethingElseIsRefusedBeforeAnythingRuns(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name       string
		definition string
		refusal    string
	}{
		{
			name: "an action nothing registers",
			definition: `schema: 1
id: release-readiness
initial: promote
states:
  promote:
    action: candidate.integrate
    on:
      integrated: ready
terminals:
  ready: {}
  mismatch: {}
`,
			refusal: "candidate.integrate",
		},
		{
			name: "another workflow entirely",
			definition: `schema: 1
id: delivery
initial: artifacts
states:
  artifacts:
    action: conformance.artifacts
    on:
      conforms: ready
      diverges: mismatch
terminals:
  ready: {}
  mismatch: {}
`,
			refusal: `declares the workflow "delivery"`,
		},
		{
			name: "a sequence with nowhere to refuse from",
			definition: `schema: 1
id: release-readiness
initial: artifacts
states:
  artifacts:
    action: conformance.artifacts
    on:
      conforms: ready
      diverges: ready
terminals:
  ready: {}
`,
			refusal: `declares no "mismatch" terminal`,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "release-readiness.yaml")
			if err := os.WriteFile(path, []byte(testCase.definition), 0o600); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}
			_, err := Compile(path)
			if err == nil {
				t.Fatal("the definition was compiled rather than refused")
			}
			if !strings.Contains(err.Error(), testCase.refusal) {
				t.Fatalf("refusal = %q, want it to name %q", err, testCase.refusal)
			}
		})
	}
}

func TestAProjectsOwnCopyOfTheDefinitionIsWhatRuns(t *testing.T) {
	t.Parallel()
	// The same sequence with the two tracker-reading checks taken out: a project
	// that cuts releases without a tracker to read. What it proves is that the
	// file decides the sequence — nothing in Go names these states.
	path := filepath.Join(t.TempDir(), "release-readiness.yaml")
	if err := os.WriteFile(path, []byte(`schema: 1
id: release-readiness
initial: artifacts
states:
  artifacts:
    action: conformance.artifacts
    on:
      conforms: references
      diverges: mismatch
  references:
    action: conformance.references
    on:
      conforms: ready
      diverges: mismatch
terminals:
  ready: {}
  mismatch: {}
`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	definition, err := Compile(path)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if definition.Source != path {
		t.Fatalf("the result names %q as the definition it ran", definition.Source)
	}

	repository := fixture(t)
	result := assessWith(t, definition, Gather(repository, product(), nil, "the tracker was never asked"))
	if !result.Conforms {
		t.Fatalf("the project's own sequence refused: %v", result.Mismatches())
	}
	if steps := steps(result.Findings); !slices.Equal(steps, []string{StepArtifacts, StepReferences}) {
		t.Fatalf("the project's own sequence walked %v", steps)
	}
	// The digest is the definition's rather than this build's, which is what an
	// instance pins itself to.
	shipped, err := Compile("")
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if result.Digest == shipped.Graph.Digest() {
		t.Fatal("a different definition digested the same as the shipped one")
	}
}

func TestEveryTransitionIsRecordedAsItIsCrossed(t *testing.T) {
	t.Parallel()
	repository := fixture(t)
	instances := store(t)
	definition, err := Compile("")
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	result, err := Assess(context.Background(), definition, instances,
		Gather(repository, product(), admitted("serving the chain"), ""), time.Now)
	if err != nil {
		t.Fatalf("Assess() error = %v", err)
	}

	instance, err := instances.LoadWorkflowInstance(result.Instance)
	if err != nil {
		t.Fatalf("the walk left no durable record: %v", err)
	}
	if instance.Digest != definition.Graph.Digest() || instance.WorkflowID != WorkflowID {
		t.Fatalf("the instance is pinned to %s of %q", instance.Digest, instance.WorkflowID)
	}
	if !instance.Terminal || instance.State != TerminalReady {
		t.Fatalf("the instance stands in %q (terminal %t)", instance.State, instance.Terminal)
	}
	// The path is the states the definition orders, ending in the terminal it
	// arrived at. Reading it back is reading exactly what ran.
	want := []string{StepArtifacts, StepReferences, StepInvariants, StepGoals, StepStaleness, TerminalReady}
	if got := instance.Path(); !slices.Equal(got, want) {
		t.Fatalf("the instance walked %v, want %v", got, want)
	}
}

func TestTheShippedDefinitionIsTheOneEmbedded(t *testing.T) {
	t.Parallel()
	// The embedded bytes and the file beside this test are one thing, so a file
	// edited without the build being remade is caught here rather than by a
	// release running a sequence nobody is looking at.
	onDisk, err := os.ReadFile("release-readiness.yaml")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !bytes.Equal(onDisk, BuiltinDefinition) {
		t.Fatal("the embedded definition and the file it is embedded from differ")
	}
}

func TestAnInstanceIdentifierIsOneTheStoreAccepts(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 9, 1, 14, 30, 12, 123456789, time.UTC)
	identifier := InstanceID(at)
	if err := domain.ValidateIdentifier("workflow instance id", identifier); err != nil {
		t.Fatalf("InstanceID() produced %q, which the store refuses: %v", identifier, err)
	}
	if identifier == InstanceID(at.Add(time.Nanosecond)) {
		t.Fatal("two assessments a nanosecond apart share an identifier, so the second would be refused")
	}
}

// run walks the shipped sequence over a repository and reports the result. It is
// the whole gate rather than the checks alone, so what it exercises is the
// definition ordering them.
func run(t *testing.T, repository string, admitted ...beads.WorkItem) Result {
	t.Helper()
	definition, err := Compile("")
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	return assessWith(t, definition, Gather(repository, product(), admitted, ""))
}

func assessWith(t *testing.T, definition Definition, sources Sources) Result {
	t.Helper()
	result, err := Assess(context.Background(), definition, store(t), sources, time.Now)
	if err != nil {
		t.Fatalf("Assess() error = %v", err)
	}
	return result
}

func store(t *testing.T) *runstate.Store {
	t.Helper()
	instances, err := runstate.NewStore(t.TempDir(), domain.ProductID("fixture"))
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	return instances
}
