package workflow

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/action"
	"github.com/mason-bryant/yoyodyne/internal/capability"
)

// wellFormed is the smallest definition that passes: one state, one action it
// selects, one outcome, and somewhere for that outcome to end. Each test below
// breaks one thing about it, so what the test is about is the line that changed.
func wellFormed() Definition {
	return Definition{
		Schema:  SchemaVersion,
		ID:      "delivery",
		Initial: "claim",
		States: map[string]State{
			"claim": {
				Action: "work-item.claim",
				On:     map[string]string{"claimed": "delivered"},
			},
		},
		Terminals: map[string]Terminal{"delivered": {Summary: "the item was claimed"}},
	}
}

func TestAWellFormedDefinitionValidates(t *testing.T) {
	t.Parallel()

	validated, err := wellFormed().Validate(deliveryCatalog(t))
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if validated.Digest() == "" {
		t.Error("a validated definition carries no digest")
	}
	if validated.Definition().ID != "delivery" {
		t.Errorf("Definition().ID = %q, want delivery", validated.Definition().ID)
	}
}

// TestAnActionNoRegisteredActionProvidesIsRefused is the refusal that makes a
// definition safe to read out of a project's own repository: it selects among
// the actions Go registered, and naming anything else is asking for authority
// nothing granted rather than making a typo.
func TestAnActionNoRegisteredActionProvidesIsRefused(t *testing.T) {
	t.Parallel()

	definition := wellFormed()
	definition.States["claim"] = State{
		Action: "candidate.rubber-stamp",
		On:     map[string]string{"stamped": "delivered"},
	}

	_, err := definition.Validate(deliveryCatalog(t))
	if err == nil {
		t.Fatal("Validate() accepted a definition selecting an action nothing registers")
	}
	if !strings.Contains(err.Error(), `"candidate.rubber-stamp"`) {
		t.Errorf("Validate() error = %v, and it does not name the action it refused", err)
	}
	// The refusal says what may be selected instead, because a person writing a
	// definition by hand has no other way to find out.
	if !strings.Contains(err.Error(), "candidate.review") {
		t.Errorf("Validate() error = %v, and it does not say what is registered", err)
	}
}

// TestATransitionToNowhereIsRefused is the other half of it. A destination that
// names nothing is a definition an instance walks off the end of, and it is
// found here rather than at the transition that would have taken it.
func TestATransitionToNowhereIsRefused(t *testing.T) {
	t.Parallel()

	definition := wellFormed()
	definition.States["claim"] = State{
		Action: "work-item.claim",
		On:     map[string]string{"claimed": "develop", "unavailable": "delivered"},
	}

	_, err := definition.Validate(deliveryCatalog(t))
	if err == nil {
		t.Fatal("Validate() accepted a transition into a state nothing declares")
	}
	if !strings.Contains(err.Error(), `sends the outcome "claimed" to "develop"`) {
		t.Errorf("Validate() error = %v, and it does not name the transition it refused", err)
	}
	// The transition that does resolve must not be reported alongside it.
	if strings.Contains(err.Error(), `"unavailable"`) {
		t.Errorf("Validate() error = %v, and it names a transition that resolves", err)
	}
}

func TestADefinitionThatCouldNotBeRunIsRefused(t *testing.T) {
	t.Parallel()

	noStart := wellFormed()
	noStart.Initial = ""

	startsNowhere := wellFormed()
	startsNowhere.Initial = "develop"

	startsAtTheEnd := wellFormed()
	startsAtTheEnd.Initial = "delivered"

	nameless := wellFormed()
	nameless.ID = ""

	stateless := wellFormed()
	stateless.States = nil

	endless := wellFormed()
	endless.Terminals = nil

	actionless := wellFormed()
	actionless.States["claim"] = State{On: map[string]string{"claimed": "delivered"}}

	deadEnd := wellFormed()
	deadEnd.States["claim"] = State{Action: "work-item.claim"}

	nowhereToGo := wellFormed()
	nowhereToGo.States["claim"] = State{Action: "work-item.claim", On: map[string]string{"claimed": ""}}

	unnamedOutcome := wellFormed()
	unnamedOutcome.States["claim"] = State{Action: "work-item.claim", On: map[string]string{"": "delivered"}}

	bothAtOnce := wellFormed()
	bothAtOnce.Terminals["claim"] = Terminal{Summary: "also a state, somehow"}

	reservedState := wellFormed()
	reservedState.States["$wait"] = State{Action: "work-item.claim", On: map[string]string{"waited": "delivered"}}

	reservedTerminal := wellFormed()
	reservedTerminal.Terminals["$done"] = Terminal{Summary: "a terminal named like a runtime destination"}

	for _, test := range []struct {
		name       string
		definition Definition
		says       string
	}{
		{name: "no initial state", definition: noStart, says: "names no initial state"},
		{name: "initial state is not a state", definition: startsNowhere, says: `initial state "develop" is not a state`},
		{name: "initial state is a terminal", definition: startsAtTheEnd, says: `initial state "delivered" is a terminal`},
		{name: "no id", definition: nameless, says: "has no id"},
		{name: "no states", definition: stateless, says: "declares no states"},
		{name: "no terminals", definition: endless, says: "declares no terminals"},
		{name: "a state selecting no action", definition: actionless, says: `state "claim" selects no action`},
		{name: "a state handling no outcomes", definition: deadEnd, says: `state "claim" handles no outcomes`},
		{name: "an outcome with no destination", definition: nowhereToGo, says: `outcome "claimed" with no destination`},
		{name: "an outcome with no name", definition: unnamedOutcome, says: "maps an outcome with no name"},
		{name: "one name for a state and a terminal", definition: bothAtOnce, says: `"claim" is both a state and a terminal`},
		{name: "a state named like a runtime destination", definition: reservedState, says: `state "$wait" begins with "$"`},
		{name: "a terminal named like a runtime destination", definition: reservedTerminal, says: `terminal "$done" begins with "$"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			validated, err := test.definition.Validate(deliveryCatalog(t))
			if err == nil {
				t.Fatalf("Validate() accepted a definition with %s", test.name)
			}
			if !strings.Contains(err.Error(), test.says) {
				t.Errorf("Validate() error = %v, want it to say %q", err, test.says)
			}
			// A refused definition carries nothing. Anything else is something a
			// caller that ignored the error would go on to run.
			if validated.Digest() != "" {
				t.Errorf("a refused definition carries the digest %q", validated.Digest())
			}
		})
	}
}

// TestALaterSchemaIsRefusedForBeingOne is why the version is checked alone: a
// file written against a schema this build does not read is wrong in one way,
// and reporting its unfamiliar keys as a dozen further mistakes teaches its
// author nothing.
func TestALaterSchemaIsRefusedForBeingOne(t *testing.T) {
	t.Parallel()

	later := wellFormed()
	later.Schema = SchemaVersion + 1
	later.Initial = "nowhere"

	_, err := later.Validate(deliveryCatalog(t))
	if err == nil {
		t.Fatal("Validate() accepted a definition written against a later schema")
	}
	if !strings.Contains(err.Error(), "this build reads version 1") {
		t.Errorf("Validate() error = %v, want it to say which version this build reads", err)
	}
	if strings.Contains(err.Error(), "nowhere") {
		t.Errorf("Validate() error = %v, and it reports a second problem in a file it cannot read", err)
	}

	unversioned := wellFormed()
	unversioned.Schema = 0
	if _, err := unversioned.Validate(deliveryCatalog(t)); err == nil {
		t.Fatal("Validate() accepted a definition that declares no schema version")
	}
}

// TestEveryRefusalIsReported is why the problems are joined rather than returned
// one at a time. A definition is written by hand, and answering one question per
// reload is how somebody gives up on a format.
func TestEveryRefusalIsReported(t *testing.T) {
	t.Parallel()

	definition := wellFormed()
	definition.ID = ""
	definition.States["claim"] = State{
		Action: "candidate.rubber-stamp",
		On:     map[string]string{"stamped": "nowhere"},
	}

	_, err := definition.Validate(deliveryCatalog(t))
	if err == nil {
		t.Fatal("Validate() accepted a definition with three defects in it")
	}
	joined, isJoined := err.(interface{ Unwrap() []error })
	if !isJoined {
		t.Fatalf("Validate() error = %v, want the refusals joined", err)
	}
	if problems := joined.Unwrap(); len(problems) != 3 {
		t.Errorf("Validate() reported %d problems, want all three: %v", len(problems), errors.Join(problems...))
	}
}

func TestACatalogHoldsWhatItWasBuiltFrom(t *testing.T) {
	t.Parallel()

	catalog := deliveryCatalog(t)
	if actions := catalog.Actions(); !slices.Contains(actions, "candidate.integrate") {
		t.Errorf("Actions() = %v, want the integrate action among them", actions)
	}
	required, registered := catalog.Capabilities("candidate.integrate")
	if !registered {
		t.Fatal(`Capabilities("candidate.integrate") found nothing`)
	}
	if !slices.Contains(required, capability.PromotionLease) {
		t.Errorf("integrating requires %v, want the promotion lease among them", required)
	}
	// What comes back is a copy: a caller that changed it would be changing what
	// the catalog says an action requires.
	required[0] = "capability.invented"
	if again, _ := catalog.Capabilities("candidate.integrate"); slices.Contains(again, "capability.invented") {
		t.Errorf("integrating requires %v after a caller changed an earlier answer", again)
	}
	if _, registered := catalog.Capabilities("candidate.rubber-stamp"); registered {
		t.Error(`Capabilities("candidate.rubber-stamp") found something, and nothing registers it`)
	}
}

// TestACatalogEntryNamingNoRegisteredCapabilityIsRefused is the capability
// vocabulary being closed, checked where a catalog is assembled. An entry
// requiring something the repository does not declare is a claim about authority
// nothing grants, and a definition validated against it would be selecting that
// claim.
func TestACatalogEntryNamingNoRegisteredCapabilityIsRefused(t *testing.T) {
	t.Parallel()

	catalog, err := NewCatalog(
		CatalogEntry{Action: "candidate.review", Capabilities: []capability.Capability{capability.ProviderInvoke}},
		CatalogEntry{Action: "candidate.integrate", Capabilities: []capability.Capability{capability.TargetBranchMutate, "target-branch.rewrite"}},
	)
	if err == nil {
		t.Fatal("NewCatalog() accepted an action requiring a capability nothing declares")
	}
	if !strings.Contains(err.Error(), `"target-branch.rewrite"`) {
		t.Errorf("NewCatalog() error = %v, and it does not name the capability it refused", err)
	}
	if strings.Contains(err.Error(), string(capability.ProviderInvoke)) {
		t.Errorf("NewCatalog() error = %v, and it names a capability that is declared", err)
	}
	// A refused catalog is empty, so nothing validates against a half-built one.
	if actions := catalog.Actions(); len(actions) != 0 {
		t.Errorf("a refused catalog holds %v", actions)
	}
}

func TestACatalogEntryThatIsNotOneIsRefused(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		entries []CatalogEntry
		says    string
	}{
		{
			name:    "no action name",
			entries: []CatalogEntry{{Capabilities: []capability.Capability{capability.RunStateMutate}}},
			says:    "names no action",
		},
		{
			name: "registered twice",
			entries: []CatalogEntry{
				{Action: "candidate.check", Capabilities: []capability.Capability{capability.ChecksExecute}},
				{Action: "candidate.check", Capabilities: []capability.Capability{capability.ChecksExecute}},
			},
			says: "is in the catalog more than once",
		},
		{
			name:    "no capabilities",
			entries: []CatalogEntry{{Action: "candidate.check"}},
			says:    "declares no capabilities",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if _, err := NewCatalog(test.entries...); err == nil {
				t.Fatalf("NewCatalog() accepted an entry with %s", test.name)
			} else if !strings.Contains(err.Error(), test.says) {
				t.Errorf("NewCatalog() error = %v, want it to say %q", err, test.says)
			}
		})
	}
}

// TestACatalogIsWhatAnActionRegistryRegistered is the bridge that keeps a
// definition validated against what the binary can actually perform rather than
// against a second list maintained beside it.
func TestACatalogIsWhatAnActionRegistryRegistered(t *testing.T) {
	t.Parallel()

	registry, err := action.New(action.Action[*struct{}]{
		Name:         "candidate.check",
		Summary:      "run the project's configured checks",
		Wraps:        "(*activeRun).verify",
		Capabilities: []capability.Capability{capability.ChecksExecute, capability.RunStateMutate},
		Perform:      func(context.Context, *struct{}) error { return nil },
	})
	if err != nil {
		t.Fatalf("action.New() error = %v", err)
	}
	catalog, err := CatalogFrom(registry)
	if err != nil {
		t.Fatalf("CatalogFrom() error = %v", err)
	}
	if actions := catalog.Actions(); !slices.Equal(actions, []string{"candidate.check"}) {
		t.Errorf("Actions() = %v, want what the registry registered", actions)
	}
	required, registered := catalog.Capabilities("candidate.check")
	if !registered {
		t.Fatal(`Capabilities("candidate.check") found nothing the registry registered`)
	}
	if !slices.Equal(required, []capability.Capability{capability.ChecksExecute, capability.RunStateMutate}) {
		t.Errorf("checking requires %v, want what the action declared", required)
	}
}

// A gate name nothing could record an act against is refused where the
// definition is read, rather than at the step it would have held. A workflow
// stopping forever at a state whose gate name somebody mistyped is a wait
// nobody can end, and the point of a gate is that the person it waits for can
// find it.
func TestAGateNameNobodyCouldRecordIsRefusedAtValidation(t *testing.T) {
	t.Parallel()

	definition := Definition{
		Schema: SchemaVersion, ID: "gated", Initial: "integrate",
		States: map[string]State{
			"integrate": {
				Action: "candidate.integrate",
				Gate:   "Integration Approved!",
				On:     map[string]string{"integrated": "delivered"},
			},
		},
		Terminals: map[string]Terminal{"delivered": {}},
	}
	_, err := definition.Validate(deliveryCatalog(t))
	if err == nil {
		t.Fatal("Validate() accepted a gate nothing could be recorded against")
	}
	for _, want := range []string{"integrate", "not a gate name"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal = %v, want it to mention %q", err, want)
		}
	}

	// A well-formed one is not a problem, and neither is a definition with none.
	definition.States["integrate"] = State{
		Action: "candidate.integrate",
		Gate:   "integration-approved",
		On:     map[string]string{"integrated": "delivered"},
	}
	if _, err := definition.Validate(deliveryCatalog(t)); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}
