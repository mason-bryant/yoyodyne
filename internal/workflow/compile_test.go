package workflow

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/action"
	"github.com/mason-bryant/yoyodyne/internal/capability"
)

// run is what the actions in these tests act on. It records what was performed,
// which is how a test can say that compiling performed nothing: the runtime that
// walks a graph is a later milestone, and until it exists every action in every
// compiled graph must stay unperformed.
type run struct {
	performed []string
}

// deliveryRegistry is the delivery pipeline's steps as a registry these tests
// own: the same names and the same capabilities as the catalog the fixtures are
// validated against, over a subject this package can construct. It is written out
// here for the reason deliveryCatalog is — nothing in the delivery path depends
// on this package yet, and depending on that one from here would point the
// dependency the wrong way.
func deliveryRegistry(t *testing.T, acted *run) action.Registry[*run] {
	t.Helper()

	// What was performed is recorded on the run this registry was built with rather
	// than on whatever subject is handed in, because nothing hands a loader a
	// subject at all: a compile that reached through one of these doors has to be
	// visible somewhere the test still holds.
	performing := func(name string) func(context.Context, *run) error {
		return func(context.Context, *run) error {
			acted.performed = append(acted.performed, name)
			return nil
		}
	}
	registered := func(name string, required ...capability.Capability) action.Action[*run] {
		return action.Action[*run]{
			Name:         name,
			Summary:      "performs " + name,
			Wraps:        "(*activeRun)." + name,
			Capabilities: required,
			Perform:      performing(name),
		}
	}
	registry, err := action.New(
		registered("work-item.claim", capability.WorkItemRead, capability.WorkItemMutate),
		registered("candidate.develop", capability.ProviderInvoke, capability.WorktreeMutate),
		registered("candidate.publish", capability.ForgePublish),
		registered("candidate.check", capability.ChecksExecute),
		registered("candidate.review", capability.ProviderInvoke),
		registered("candidate.integrate", capability.PromotionLease, capability.TargetBranchMutate),
		registered("run.clean-up", capability.WorktreeMutate),
	)
	if err != nil {
		t.Fatalf("action.New() error = %v", err)
	}
	return registry
}

// deliveryLoader is a loader over that registry, holding everything the delivery
// definition asks for. A test that is about a refusal narrows one of the three
// things a loader is.
func deliveryLoader(t *testing.T, acted *run) Loader[*run] {
	t.Helper()

	grant, err := NewGrant(capability.All()...)
	if err != nil {
		t.Fatalf("NewGrant() error = %v", err)
	}
	return Loader[*run]{Registry: deliveryRegistry(t, acted), Grant: grant}
}

// describe is everything a graph says about itself, in one string. Two graphs
// that describe identically are the same compiled workflow, which is what the
// determinism test compares — an action holds a function, so a graph is not
// something reflect.DeepEqual can answer about.
func describe[S any](graph Graph[S]) string {
	var described strings.Builder
	fmt.Fprintf(&described, "%s schema=%d %s initial=%s\n", graph.ID(), graph.Schema(), graph.Digest(), graph.Initial())
	for _, state := range graph.States() {
		node, _ := graph.Node(state)
		fmt.Fprintf(&described, "%s performs %s\n", state, node.Action().Name)
		for _, outcome := range node.Outcomes() {
			destination, _ := node.Next(outcome)
			fmt.Fprintf(&described, "\t%s -> %s (terminal %t)\n", outcome, destination.Name, destination.Terminal)
		}
	}
	fmt.Fprintf(&described, "terminals %v requires %v\n", graph.Terminals(), graph.Capabilities())
	return described.String()
}

// TestAValidDefinitionCompilesToRegisteredActions is the criterion this whole
// package exists for: what comes out is a graph whose every node is an action
// this build registered, with nothing left to resolve later.
func TestAValidDefinitionCompilesToRegisteredActions(t *testing.T) {
	t.Parallel()

	acted := &run{}
	graph, err := deliveryLoader(t, acted).LoadFile("testdata/delivery.yaml")
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}
	if graph.ID() != "delivery" {
		t.Errorf("ID() = %q, want delivery", graph.ID())
	}
	if graph.Schema() != SchemaVersion {
		t.Errorf("Schema() = %d, want %d", graph.Schema(), SchemaVersion)
	}
	if graph.Initial() != "claim" {
		t.Errorf("Initial() = %q, want claim", graph.Initial())
	}
	if !strings.HasPrefix(graph.Digest(), DigestPrefix) {
		t.Errorf("Digest() = %q, want a workflow digest", graph.Digest())
	}
	if states := graph.States(); len(states) != 7 {
		t.Errorf("States() = %v, want the delivery loop's 7", states)
	}
	if terminals := graph.Terminals(); !slices.Equal(terminals, []string{"abandoned", "delivered"}) {
		t.Errorf("Terminals() = %v, want abandoned and delivered in sorted order", terminals)
	}

	registry := deliveryRegistry(t, acted)
	for _, state := range graph.States() {
		node, isAState := graph.Node(state)
		if !isAState {
			t.Fatalf("States() reported %q and Node() does not hold it", state)
		}
		if _, registered := registry.Lookup(node.Action().Name); !registered {
			t.Errorf("the node %q performs %q, which nothing registers", state, node.Action().Name)
		}
		if node.Action().Perform == nil {
			t.Errorf("the node %q performs an action with no door onto anything", state)
		}
	}

	// A transition into a state and a transition into a terminal are resolved as
	// what they are, so nothing downstream has to look either up again.
	review, _ := graph.Node("review")
	changes, handled := review.Next("changes-requested")
	if !handled {
		t.Fatal(`the review node does not handle "changes-requested"`)
	}
	if changes.Name != "develop" || changes.Terminal {
		t.Errorf("a review asking for changes goes to %+v, want the develop state", changes)
	}
	cleanUp, _ := graph.Node("clean-up")
	cleaned, handled := cleanUp.Next("cleaned")
	if !handled {
		t.Fatal(`the clean-up node does not handle "cleaned"`)
	}
	if cleaned.Name != "delivered" || !cleaned.Terminal {
		t.Errorf("a cleaned run goes to %+v, want the delivered terminal", cleaned)
	}
	if _, handled := review.Next("rubber-stamped"); handled {
		t.Error("the review node handles an outcome the definition does not map")
	}

	// What the graph requires is the union of what its actions declare, and it is
	// reported in the vocabulary's own order rather than in whichever order the
	// states were walked.
	want := []capability.Capability{
		capability.WorkItemRead,
		capability.WorkItemMutate,
		capability.WorktreeMutate,
		capability.TargetBranchMutate,
		capability.PromotionLease,
		capability.ProviderInvoke,
		capability.ChecksExecute,
		capability.ForgePublish,
	}
	if required := graph.Capabilities(); !slices.Equal(required, want) {
		t.Errorf("Capabilities() = %v, want %v", required, want)
	}
}

// TestCompilingPerformsNothing is the milestone boundary held to the code:
// loading a definition resolves every door and opens none of them.
func TestCompilingPerformsNothing(t *testing.T) {
	t.Parallel()

	acted := &run{}
	if _, err := deliveryLoader(t, acted).LoadFile("testdata/delivery.yaml"); err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}
	if len(acted.performed) != 0 {
		t.Errorf("compiling performed %v", acted.performed)
	}
}

// TestACompiledGraphIsDeterministic is the other half of pinning: a digest is
// worth nothing if the graph compiled from the definition it addresses could
// differ between two compiles of it.
func TestACompiledGraphIsDeterministic(t *testing.T) {
	t.Parallel()

	acted := &run{}
	loader := deliveryLoader(t, acted)
	first, err := loader.LoadFile("testdata/delivery.yaml")
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}
	again, err := loader.LoadFile("testdata/delivery.yaml")
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}
	if describe(first) != describe(again) {
		t.Errorf("one definition compiled twice:\n%s\nand then:\n%s", describe(first), describe(again))
	}

	// The same workflow written down differently compiles to the same graph, for
	// the same reason it digests the same: what a definition says is the sequence,
	// and not the order somebody typed it in.
	rewritten, err := loader.LoadFile("testdata/delivery-rewritten.yaml")
	if err != nil {
		t.Fatalf("LoadFile() error = %v", err)
	}
	if describe(first) != describe(rewritten) {
		t.Errorf("one workflow written two ways compiled to:\n%s\nand:\n%s", describe(first), describe(rewritten))
	}
}

// TestAnActionThisBuildDoesNotRegisterIsRefused is the compiler's own resolution,
// which is not the same refusal validation makes. A definition can pass
// validation against one catalog and still name an action the registry that will
// perform it does not hold.
func TestAnActionThisBuildDoesNotRegisterIsRefused(t *testing.T) {
	t.Parallel()

	catalog, err := NewCatalog(
		CatalogEntry{Action: "work-item.claim", Capabilities: []capability.Capability{capability.WorkItemRead, capability.WorkItemMutate}},
		CatalogEntry{Action: "candidate.rubber-stamp", Capabilities: []capability.Capability{capability.RunStateMutate}},
	)
	if err != nil {
		t.Fatalf("NewCatalog() error = %v", err)
	}
	definition := wellFormed()
	definition.States["stamp"] = State{
		Action: "candidate.rubber-stamp",
		On:     map[string]string{"stamped": "delivered"},
	}
	definition.States["claim"] = State{
		Action: "work-item.claim",
		On:     map[string]string{"claimed": "stamp"},
	}
	validated, err := definition.Validate(catalog)
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	graph, err := deliveryLoader(t, &run{}).Compile(validated)
	if err == nil {
		t.Fatal("Compile() accepted a definition selecting an action this build registers nothing under")
	}
	if !strings.Contains(err.Error(), `"candidate.rubber-stamp"`) {
		t.Errorf("Compile() error = %v, and it does not name the action it refused", err)
	}
	if strings.Contains(err.Error(), `"work-item.claim"`) {
		t.Errorf("Compile() error = %v, and it names an action that resolves", err)
	}
	// A refused compile carries nothing: a graph that came back half-resolved is
	// one a caller ignoring the error would go on to run.
	if states := graph.States(); len(states) != 0 || graph.Digest() != "" {
		t.Errorf("a refused compile carries the states %v and the digest %q", states, graph.Digest())
	}
}

// TestAnActionRequiringMoreThanTheGrantIsRefused is the safety half of the trust
// model: a definition selects sequence, and the authority its actions need has to
// already be conferred on whatever is about to run them.
func TestAnActionRequiringMoreThanTheGrantIsRefused(t *testing.T) {
	t.Parallel()

	// Everything the delivery definition needs except the two capabilities
	// integrating requires: the workflow is otherwise perfectly valid, and this is
	// the same file bound to something that may not promote.
	granted := slices.DeleteFunc(capability.All(), func(held capability.Capability) bool {
		return held == capability.PromotionLease || held == capability.TargetBranchMutate
	})
	grant, err := NewGrant(granted...)
	if err != nil {
		t.Fatalf("NewGrant() error = %v", err)
	}
	loader := deliveryLoader(t, &run{})
	loader.Grant = grant

	_, err = loader.LoadFile("testdata/delivery.yaml")
	if err == nil {
		t.Fatal("LoadFile() compiled a workflow requiring authority its grant does not confer")
	}
	if !strings.Contains(err.Error(), `the state "integrate" performs "candidate.integrate"`) {
		t.Errorf("LoadFile() error = %v, and it does not name the state and action it refused", err)
	}
	// Both missing capabilities are reported rather than the first one found: a
	// caller that narrowed a grant wants the whole of what that cost it.
	for _, missing := range []capability.Capability{capability.PromotionLease, capability.TargetBranchMutate} {
		if !strings.Contains(err.Error(), string(missing)) {
			t.Errorf("LoadFile() error = %v, and it does not name the missing %q", err, missing)
		}
	}
	// The states whose actions the grant does cover are not reported alongside
	// them.
	if strings.Contains(err.Error(), `the state "claim"`) {
		t.Errorf("LoadFile() error = %v, and it names a state the grant covers", err)
	}
}

// TestAGrantThatConfersNothingIsRefused is the fail-closed default. A caller that
// did not say what authority it holds has not said "all of it", and the zero
// Loader is exactly that caller.
func TestAGrantThatConfersNothingIsRefused(t *testing.T) {
	t.Parallel()

	loader := deliveryLoader(t, &run{})
	loader.Grant = Grant{}

	_, err := loader.LoadFile("testdata/delivery.yaml")
	if err == nil {
		t.Fatal("LoadFile() compiled a workflow under a grant conferring nothing")
	}
	if !strings.Contains(err.Error(), "confers no capabilities") {
		t.Errorf("LoadFile() error = %v, want it to say the grant confers nothing", err)
	}
}

// TestAGrantNamingNoDeclaredCapabilityIsRefused keeps the vocabulary closed at
// the last place authority is named. A grant is written by whatever binds a
// workflow, and a name nothing declares is a permission nobody defined.
func TestAGrantNamingNoDeclaredCapabilityIsRefused(t *testing.T) {
	t.Parallel()

	grant, err := NewGrant(capability.RepositoryRead, "target-branch.rewrite")
	if err == nil {
		t.Fatal("NewGrant() accepted a capability nothing declares")
	}
	if !strings.Contains(err.Error(), `"target-branch.rewrite"`) {
		t.Errorf("NewGrant() error = %v, and it does not name the capability it refused", err)
	}
	if conferred := grant.Capabilities(); len(conferred) != 0 {
		t.Errorf("a refused grant confers %v", conferred)
	}
}

// TestAGrantReportsWhatItConfersInOneOrder is what makes a graph's capabilities
// comparable between two builds of the same thing.
func TestAGrantReportsWhatItConfersInOneOrder(t *testing.T) {
	t.Parallel()

	grant, err := NewGrant(capability.ForgePublish, capability.WorkItemRead, capability.ForgePublish)
	if err != nil {
		t.Fatalf("NewGrant() error = %v", err)
	}
	want := []capability.Capability{capability.WorkItemRead, capability.ForgePublish}
	if conferred := grant.Capabilities(); !slices.Equal(conferred, want) {
		t.Errorf("Capabilities() = %v, want %v", conferred, want)
	}
	// What comes back is a copy: a caller that changed it would be changing what
	// the grant confers.
	grant.Capabilities()[0] = "capability.invented"
	if conferred := grant.Capabilities(); !slices.Equal(conferred, want) {
		t.Errorf("Capabilities() = %v after a caller changed an earlier answer", conferred)
	}
}

// TestCompilingSomethingNeverValidatedIsRefused closes the one door into a graph
// that does not go through validation: the zero Validated.
func TestCompilingSomethingNeverValidatedIsRefused(t *testing.T) {
	t.Parallel()

	_, err := deliveryLoader(t, &run{}).Compile(Validated{})
	if err == nil {
		t.Fatal("Compile() accepted a definition that never passed validation")
	}
	if !strings.Contains(err.Error(), "nothing was validated") {
		t.Errorf("Compile() error = %v, want it to say nothing was validated", err)
	}
}
