package workflow

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/action"
	"github.com/mason-bryant/yoyodyne/internal/capability"
	"github.com/mason-bryant/yoyodyne/internal/humangate"
)

// Catalog is what code says a definition may select: every registered action, by
// name, with the capabilities performing it requires.
//
// It is the closed half of the trust model, handed to validation rather than
// read out of the definition, because the alternative — letting a definition say
// which actions exist — is the alternative the whole design exists to refuse. A
// definition validated against a catalog can name an action the catalog holds and
// nothing else.
//
// The capabilities are carried because this is where they can be checked at all:
// a catalog entry naming a capability the repository does not declare is a claim
// about authority that nothing grants, and it is refused where the catalog is
// built rather than discovered when something tries to perform it.
type Catalog struct {
	byAction map[string][]capability.Capability
}

// CatalogEntry is one registered action as a catalog holds it.
type CatalogEntry struct {
	// Action is the name a definition selects it by.
	Action string
	// Capabilities is what performing it requires, as the action itself declares
	// it. Nothing a definition says adds to this list or takes anything from it.
	Capabilities []capability.Capability
}

// NewCatalog builds a catalog, refusing anything that would make it untrustworthy.
//
// The refusals mirror the ones `internal/action` makes when a registry is built,
// and for the same reason: a catalog is assembled once from what code registered,
// so a defect in it is a defect in the binary rather than in one definition's
// luck. They are reported together, because a catalog with three mistakes in it
// is worth seeing whole.
func NewCatalog(entries ...CatalogEntry) (Catalog, error) {
	catalog := Catalog{byAction: make(map[string][]capability.Capability, len(entries))}
	var problems []error
	for index, entry := range entries {
		if entry.Action == "" {
			problems = append(problems, fmt.Errorf("the catalog entry at position %d names no action", index))
			continue
		}
		if _, taken := catalog.byAction[entry.Action]; taken {
			problems = append(problems, fmt.Errorf("%q is in the catalog more than once; a definition selecting it would not say which one it meant", entry.Action))
			continue
		}
		if len(entry.Capabilities) == 0 {
			problems = append(problems, fmt.Errorf("%q declares no capabilities; every action states what performing it requires", entry.Action))
		}
		for _, required := range entry.Capabilities {
			if !required.Known() {
				problems = append(problems, fmt.Errorf("%q requires %q, which is not a capability this repository declares; a definition selecting it would be selecting authority nothing grants", entry.Action, required))
			}
		}
		catalog.byAction[entry.Action] = slices.Clone(entry.Capabilities)
	}
	if len(problems) > 0 {
		return Catalog{}, errors.Join(problems...)
	}
	return catalog, nil
}

// CatalogFrom is the catalog of an action registry, which is where a real one
// always comes from: the registry is the list of actions Go registered, and this
// is the same list in the form validation reads. Building it here rather than by
// hand is what keeps a definition validated against what the binary can actually
// perform instead of against a second list somebody maintained beside it.
func CatalogFrom[S any](registry action.Registry[S]) (Catalog, error) {
	registered := registry.Actions()
	entries := make([]CatalogEntry, 0, len(registered))
	for _, registeredAction := range registered {
		entries = append(entries, CatalogEntry{Action: registeredAction.Name, Capabilities: registeredAction.Capabilities})
	}
	return NewCatalog(entries...)
}

// Actions is every action this catalog holds, in sorted order.
func (c Catalog) Actions() []string {
	return slices.Sorted(maps.Keys(c.byAction))
}

// Capabilities is what performing an action requires, and whether the catalog
// holds it at all.
func (c Catalog) Capabilities(actionName string) ([]capability.Capability, bool) {
	required, found := c.byAction[actionName]
	return slices.Clone(required), found
}

// Validated is a definition that passed validation, and the digest of what
// passed.
//
// It exists so that "this definition is well formed" is something the type
// system carries rather than something each reader has to remember to have
// checked. Nothing outside this package can construct one with anything in it:
// Validate is the only door, so a Validated in hand has been through the
// refusals below, and its digest is the digest of exactly what came out.
//
// It holds its own copy of the definition and hands out copies, because a
// definition whose maps a caller could still reach is one whose digest could stop
// describing it — and an instance pinned to a digest that no longer matches what
// it is running is the failure this type is here to make impossible.
type Validated struct {
	definition Definition
	digest     string
}

// Definition is what passed validation. It is a copy: changing it changes
// nothing about the validated definition or its digest.
func (v Validated) Definition() Definition {
	return v.definition.clone()
}

// Validate refuses a definition or produces one worth executing.
//
// Everything wrong with a definition is reported together. A definition is
// written by hand, in a project this harness does not own, and answering one
// question at a time across six reloads is how a person gives up on a format —
// so the refusal is the whole of what is wrong rather than the first thing found.
//
// What it does not check yet is the half that needs outcomes: a registered action
// declares its capabilities but not the outcomes it can return, so "every outcome
// the action can produce is handled" and "this state's transitions name outcomes
// that exist" are unanswerable here. They belong with the action descriptor that
// gains outcomes, and until then a transition names an outcome on trust.
func (d Definition) Validate(catalog Catalog) (Validated, error) {
	// The schema version is checked alone. A file written against a later schema
	// is wrong in one way, and reporting its unfamiliar keys as a dozen further
	// mistakes teaches its author nothing about the one thing to fix.
	if d.Schema != SchemaVersion {
		return Validated{}, fmt.Errorf("this definition declares schema version %d; this build reads version %d", d.Schema, SchemaVersion)
	}

	var problems []error
	if strings.TrimSpace(d.ID) == "" {
		problems = append(problems, errors.New("the definition has no id; an instance records the workflow it is running by that name"))
	}
	if len(d.States) == 0 {
		problems = append(problems, errors.New("the definition declares no states; there is nothing for an instance to do"))
	}
	if len(d.Terminals) == 0 {
		problems = append(problems, errors.New("the definition declares no terminals; a workflow that cannot end is one no instance ever leaves"))
	}

	// The order the problems are found in is the order they are reported in, and
	// it is the order somebody would read the file: what this workflow is, what
	// its states are called, where it starts, and then what each state does. Every
	// loop is over sorted names, so one definition refused twice is refused
	// identically.
	for _, name := range slices.Sorted(maps.Keys(d.States)) {
		problems = append(problems, namingProblems("state", name)...)
	}
	for _, name := range slices.Sorted(maps.Keys(d.Terminals)) {
		problems = append(problems, namingProblems("terminal", name)...)
		if _, alsoAState := d.States[name]; alsoAState {
			problems = append(problems, fmt.Errorf("%q is both a state and a terminal; a transition into it would not say which was meant", name))
		}
	}

	if strings.TrimSpace(d.Initial) == "" {
		problems = append(problems, errors.New("the definition names no initial state; an instance would have nowhere to start"))
	} else if _, isAState := d.States[d.Initial]; !isAState {
		if _, isATerminal := d.Terminals[d.Initial]; isATerminal {
			problems = append(problems, fmt.Errorf("the initial state %q is a terminal; a workflow that is over before it begins performs nothing", d.Initial))
		} else {
			problems = append(problems, fmt.Errorf("the initial state %q is not a state this definition declares", d.Initial))
		}
	}

	for _, name := range slices.Sorted(maps.Keys(d.States)) {
		problems = append(problems, d.stateProblems(name, d.States[name], catalog)...)
	}

	if len(problems) > 0 {
		return Validated{}, errors.Join(problems...)
	}
	validated := d.clone()
	return Validated{definition: validated, digest: digestOf(validated)}, nil
}

// stateProblems is everything wrong with one state: the action it selects, and
// where its outcomes lead.
func (d Definition) stateProblems(name string, state State, catalog Catalog) []error {
	var problems []error
	// The second case is the refusal that makes a definition safe to read from a
	// project's own repository: it selects among the actions code registered, and
	// an action nothing registered is authority nobody granted rather than a typo
	// to forgive.
	if strings.TrimSpace(state.Action) == "" {
		problems = append(problems, fmt.Errorf("the state %q selects no action; a state is a step and a step performs something", name))
	} else if _, registered := catalog.Capabilities(state.Action); !registered {
		problems = append(problems, fmt.Errorf("the state %q selects the action %q, which no registered action provides; a definition selects among %s", name, state.Action, registeredList(catalog)))
	}

	// A gate name nothing could record an act against is refused here rather than
	// at the step it would have held, because a gate nobody can discharge is a
	// workflow that stops forever at a state whose name somebody mistyped — and
	// the whole value of a gate is that the person it waits for can find it.
	if state.Gate != "" {
		if problem := humangate.NameProblem(state.Gate); problem != nil {
			problems = append(problems, fmt.Errorf("the state %q is gated on %q: %w", name, state.Gate, problem))
		}
	}

	if len(state.On) == 0 {
		problems = append(problems, fmt.Errorf("the state %q handles no outcomes; an instance that reached it would have nowhere to go", name))
		return problems
	}
	for _, outcome := range slices.Sorted(maps.Keys(state.On)) {
		destination := state.On[outcome]
		if strings.TrimSpace(outcome) == "" {
			problems = append(problems, fmt.Errorf("the state %q maps an outcome with no name", name))
			continue
		}
		if strings.TrimSpace(destination) == "" {
			problems = append(problems, fmt.Errorf("the state %q handles the outcome %q with no destination", name, outcome))
			continue
		}
		_, isAState := d.States[destination]
		_, isATerminal := d.Terminals[destination]
		if !isAState && !isATerminal {
			problems = append(problems, fmt.Errorf("the state %q sends the outcome %q to %q, which is neither a state nor a terminal this definition declares", name, outcome, destination))
		}
	}
	return problems
}

// namingProblems is everything wrong with the name of a state or a terminal.
func namingProblems(kind, name string) []error {
	if strings.TrimSpace(name) == "" {
		return []error{fmt.Errorf("a %s has no name; nothing could transition into it", kind)}
	}
	if strings.HasPrefix(name, ReservedPrefix) {
		return []error{fmt.Errorf("the %s %q begins with %q, which is reserved for the destinations the runtime owns", kind, name, ReservedPrefix)}
	}
	return nil
}

// registeredList is what the catalog holds, for a refusal to say instead of
// leaving somebody to guess what they may have meant.
func registeredList(catalog Catalog) string {
	registered := catalog.Actions()
	if len(registered) == 0 {
		return "no registered actions at all"
	}
	return strings.Join(registered, ", ")
}
