package action

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/capability"
)

// subject is what the actions in these tests act on. It is a counter rather than
// anything real: what is being tested is the registry, and the only thing the
// registry does with a subject is hand it to the action that was asked for.
type subject struct {
	performed []string
}

func performing(name string) func(context.Context, *subject) error {
	return func(_ context.Context, s *subject) error {
		s.performed = append(s.performed, name)
		return nil
	}
}

func wellFormed(name string) Action[*subject] {
	return Action[*subject]{
		Name:         name,
		Summary:      "does " + name,
		Wraps:        "(*thing)." + name,
		Capabilities: []capability.Capability{capability.RunStateMutate},
		Perform:      performing(name),
	}
}

func TestARegistryHoldsWhatItWasBuiltFrom(t *testing.T) {
	t.Parallel()

	registry, err := New(wellFormed("first"), wellFormed("second"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if names := registry.Names(); !slices.Equal(names, []string{"first", "second"}) {
		t.Errorf("Names() = %v, want declaration order [first second]", names)
	}
	registered, found := registry.Lookup("second")
	if !found {
		t.Fatal(`Lookup("second") found nothing`)
	}
	if registered.Wraps != "(*thing).second" {
		t.Errorf("Lookup(\"second\").Wraps = %q, want (*thing).second", registered.Wraps)
	}
	if _, found := registry.Lookup("third"); found {
		t.Error(`Lookup("third") found something, and nothing is registered under it`)
	}
}

// TestPerformingReachesTheWrappedFunction is the claim the registry makes about
// itself: what comes back out of a lookup is a door onto the action's own
// function, and going through it calls that function with the subject.
func TestPerformingReachesTheWrappedFunction(t *testing.T) {
	t.Parallel()

	registry, err := New(wellFormed("first"), wellFormed("second"))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	acted := &subject{}
	registered, _ := registry.Lookup("second")
	if err := registered.Perform(context.Background(), acted); err != nil {
		t.Fatalf("Perform() error = %v", err)
	}
	if !slices.Equal(acted.performed, []string{"second"}) {
		t.Errorf("performing %q reached %v", "second", acted.performed)
	}
}

func TestADuplicateNameIsRefused(t *testing.T) {
	t.Parallel()

	_, err := New(wellFormed("develop"), wellFormed("check"), wellFormed("develop"))
	if err == nil {
		t.Fatal("New() accepted two actions registered under one name")
	}
	if !strings.Contains(err.Error(), `"develop" is registered more than once`) {
		t.Errorf("New() error = %v, and it does not name the duplicate", err)
	}
}

func TestAnUnknownCapabilityIsRefused(t *testing.T) {
	t.Parallel()

	unknown := wellFormed("promote")
	unknown.Capabilities = []capability.Capability{capability.TargetBranchMutate, "target-branch.rewrite"}
	_, err := New(wellFormed("check"), unknown)
	if err == nil {
		t.Fatal("New() accepted an action requiring a capability nothing declares")
	}
	if !strings.Contains(err.Error(), `"target-branch.rewrite"`) {
		t.Errorf("New() error = %v, and it does not name the capability it refused", err)
	}
	// The capability that is declared must not be reported alongside it: a
	// refusal that names both teaches nobody which one was wrong.
	if strings.Contains(err.Error(), string(capability.TargetBranchMutate)) {
		t.Errorf("New() error = %v, and it names a capability that is declared", err)
	}
}

// TestARegistryRefusedIsEmpty is what makes the refusals worth having. A
// registry that came back half-built from a table with a duplicate in it would
// be one some caller used.
func TestARegistryRefusedIsEmpty(t *testing.T) {
	t.Parallel()

	registry, err := New(wellFormed("develop"), wellFormed("develop"))
	if err == nil {
		t.Fatal("New() accepted a duplicate")
	}
	if names := registry.Names(); len(names) != 0 {
		t.Errorf("a refused registry carries %v", names)
	}
	if _, found := registry.Lookup("develop"); found {
		t.Error("a refused registry still answers lookups")
	}
}

func TestAnActionMissingWhatMakesItOneIsRefused(t *testing.T) {
	t.Parallel()

	nameless := wellFormed("nameless")
	nameless.Name = ""

	uncapable := wellFormed("uncapable")
	uncapable.Capabilities = nil

	doorless := wellFormed("doorless")
	doorless.Perform = nil

	wrapless := wellFormed("wrapless")
	wrapless.Wraps = ""

	for _, test := range []struct {
		name      string
		candidate Action[*subject]
		says      string
	}{
		{name: "no name", candidate: nameless, says: "has no name"},
		{name: "no capabilities", candidate: uncapable, says: "declares no capabilities"},
		{name: "nothing to perform", candidate: doorless, says: "has nothing to perform"},
		{name: "wraps nothing", candidate: wrapless, says: "names no function it wraps"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := New(test.candidate)
			if err == nil {
				t.Fatalf("New() accepted an action with %s", test.name)
			}
			if !strings.Contains(err.Error(), test.says) {
				t.Errorf("New() error = %v, want it to say %q", err, test.says)
			}
		})
	}
}

// TestEveryRefusalIsReported is why the problems are joined rather than
// returned one at a time: a table with three mistakes in it is worth seeing
// whole, instead of being fixed and re-run three times.
func TestEveryRefusalIsReported(t *testing.T) {
	t.Parallel()

	unknown := wellFormed("check")
	unknown.Capabilities = []capability.Capability{"checks.pretend"}

	_, err := New(wellFormed("develop"), unknown, wellFormed("develop"))
	if err == nil {
		t.Fatal("New() accepted a table with two defects in it")
	}
	joined, isJoined := err.(interface{ Unwrap() []error })
	if !isJoined {
		t.Fatalf("New() error = %v, want the refusals joined", err)
	}
	if problems := joined.Unwrap(); len(problems) != 2 {
		t.Errorf("New() reported %d problems, want both: %v", len(problems), errors.Join(problems...))
	}
}

func TestAnEmptyRegistryIsBuildable(t *testing.T) {
	t.Parallel()

	registry, err := New[*subject]()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if names := registry.Names(); len(names) != 0 {
		t.Errorf("Names() = %v, want nothing", names)
	}
	if actions := registry.Actions(); len(actions) != 0 {
		t.Errorf("Actions() = %v, want nothing", actions)
	}
}
