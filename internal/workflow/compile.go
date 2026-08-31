package workflow

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/action"
	"github.com/mason-bryant/yoyodyne/internal/capability"
)

// Grant is the authority a workflow is compiled under: the capabilities the
// context binding it confers, in the vocabulary `internal/capability` declares.
//
// It is the second half of "configuration selects sequence, code grants
// capability". Validation answers whether a definition selects actions that
// exist at all; a grant answers whether the thing about to run it may perform
// them. The two are apart because one definition is valid wherever it is read and
// is still compiled under whatever authority the caller holds — the same file
// bound to something that may only read is a refusal, and it is a refusal before
// an instance exists rather than at the step that would have promoted something.
//
// The zero Grant confers nothing and a compile against it is refused. That is
// deliberate: a caller that did not say what it holds has not said "everything".
type Grant struct {
	// granted is in the order the vocabulary declares, so what a grant reports
	// about itself reads the same however it was written down.
	granted []capability.Capability
}

// NewGrant builds a grant, refusing a capability the repository does not declare.
//
// The refusal mirrors the ones the action registry and the catalog make, and for
// the same reason: a name nothing declares is authority nobody defined, and a
// grant conferring it would admit a definition on the strength of a string.
func NewGrant(granted ...capability.Capability) (Grant, error) {
	held := make(map[capability.Capability]bool, len(granted))
	var problems []error
	for _, conferred := range granted {
		if !conferred.Known() {
			problems = append(problems, fmt.Errorf("the grant confers %q, which is not a capability this repository declares; a grant confers authority the code defines rather than authority a name asks for", conferred))
			continue
		}
		held[conferred] = true
	}
	if len(problems) > 0 {
		return Grant{}, errors.Join(problems...)
	}
	return Grant{granted: inDeclaredOrder(held)}, nil
}

// Capabilities is what this grant confers, in the order the vocabulary declares
// them.
func (g Grant) Capabilities() []capability.Capability {
	return slices.Clone(g.granted)
}

// confers reports whether performing something that requires this capability is
// within the grant.
func (g Grant) confers(required capability.Capability) bool {
	return slices.Contains(g.granted, required)
}

// Destination is where one outcome leads: the state or terminal it names, and
// whether arriving there ends the instance.
//
// Which of the two it is, is decided here rather than left to whatever executes
// the graph. A definition's transition table is strings, and an executor that
// looked each one up again is an executor that could look it up differently.
type Destination struct {
	// Name is the state or terminal this outcome transitions into.
	Name string
	// Terminal is whether Name is a terminal, in which case the instance ends there
	// instead of performing anything further.
	Terminal bool
}

// Node is one compiled state: the registered action it performs, and where each
// of its outcomes goes.
//
// It holds the action itself rather than the name of one, which is the whole of
// what compiling adds to a state. Every reference a definition made has been
// resolved against what code registered, so whatever executes a graph never looks
// an action up and never meets a name that resolves to nothing.
type Node[S any] struct {
	state    string
	summary  string
	performs action.Action[S]
	next     map[string]Destination
}

// State is the name the definition declared this node under.
func (n Node[S]) State() string { return n.state }

// Summary is what this step does here, as the definition said it.
func (n Node[S]) Summary() string { return n.summary }

// Action is the registered action this node performs. Nothing here performs it:
// compiling resolves the door, and opening it belongs to the Executor.
func (n Node[S]) Action() action.Action[S] { return n.performs }

// Outcomes is every outcome this node handles, in sorted order.
func (n Node[S]) Outcomes() []string { return slices.Sorted(maps.Keys(n.next)) }

// Next is where an outcome leads, and whether this node handles it at all. An
// outcome nothing here maps is not a defect in the definition — an action's
// outcomes are not declared yet, so whether every one of them is handled is a
// question this package cannot ask — and it is the caller's to answer for.
func (n Node[S]) Next(outcome string) (Destination, bool) {
	destination, handled := n.next[outcome]
	return destination, handled
}

// Graph is a compiled workflow: every state resolved to the registered action it
// performs, every transition resolved to a destination that exists, and the
// digest of the definition all of it came from.
//
// Compiling one performs nothing. What a graph is for is that an instance pins
// itself to the digest here and knows that everything the definition asked for
// was resolved once, before it started, rather than discovered a step at a time
// while a work item is already claimed.
//
// It is deterministic for a given definition: the same definition compiled twice,
// or written down two different ways and compiled once each, produces the same
// states, the same nodes, the same transitions, the same capabilities and the
// same digest.
type Graph[S any] struct {
	id      string
	schema  int
	digest  string
	initial string
	nodes   map[string]Node[S]
	// states and terminals are sorted, which is what makes what a graph reports
	// about itself the same on every walk of it.
	states    []string
	terminals []string
	requires  []capability.Capability
}

// ID is the workflow this graph was compiled from, by the name a binding selects
// it under.
func (g Graph[S]) ID() string { return g.id }

// Schema is the format version the definition was written against and read
// under.
func (g Graph[S]) Schema() int { return g.schema }

// Digest is the content address of the definition this graph is pinned to. An
// instance records it, and a later load of a changed file is refused against it.
func (g Graph[S]) Digest() string { return g.digest }

// Initial is the state an instance starts in.
func (g Graph[S]) Initial() string { return g.initial }

// States is every state, in sorted order.
func (g Graph[S]) States() []string { return slices.Clone(g.states) }

// Terminals is every way this workflow ends, in sorted order.
func (g Graph[S]) Terminals() []string { return slices.Clone(g.terminals) }

// Node is the compiled state under a name, and whether the graph holds one.
func (g Graph[S]) Node(state string) (Node[S], bool) {
	node, isAState := g.nodes[state]
	return node, isAState
}

// Capabilities is everything performing this workflow can require: the union of
// what its actions declare, in the order the vocabulary declares them. It is the
// authority a graph actually draws on, which is never more than the grant it was
// compiled under and is often less.
func (g Graph[S]) Capabilities() []capability.Capability { return slices.Clone(g.requires) }

// Compile turns a validated definition into the graph an instance runs, or
// refuses the whole of it.
//
// This is where the two things validation could not do happen. Every action the
// definition selects is resolved against the registry this build actually holds,
// and every capability those actions declare is checked against the grant this
// workflow is compiled under. Both are refusals rather than warnings, and both
// happen here — before an instance exists, before a work item is claimed, and
// before a worktree is made. That ordering is what the loader is for: a
// definition that cannot run is refused while refusing it still costs nothing.
//
// The registry is authoritative about what an action requires even where a
// definition was validated against a catalog built from something else: the
// catalog is a report of a registry, and it is the registry that will be asked to
// perform the work.
func (l Loader[S]) Compile(validated Validated) (Graph[S], error) {
	// A Validated carries a digest of what passed, so one without a digest never
	// passed anything — it is a zero value some caller reached this with.
	if validated.digest == "" {
		return Graph[S]{}, errors.New("compile workflow: nothing was validated; a graph is compiled from a definition that passed validation")
	}
	definition := validated.definition

	// The pin is checked before anything about the definition's contents, because
	// a definition that is not the one the instance pinned is not one this caller
	// has an opinion about: it is the wrong definition, and what it says does not
	// matter.
	if l.Pin != "" && l.Pin != validated.digest {
		return Graph[S]{}, fmt.Errorf("compile workflow %q: this definition digests to %s and the caller is pinned to %s; the definition changed under something already running it", definition.ID, validated.digest, l.Pin)
	}
	// A grant conferring nothing is refused once rather than once per state: the
	// caller has not narrowed a grant down to nothing, it has not said what it
	// holds, and every state below would report that same omission again.
	if len(l.Grant.granted) == 0 {
		return Graph[S]{}, fmt.Errorf("compile workflow %q: the grant it is compiled under confers no capabilities; a workflow is compiled under the authority that will perform it", definition.ID)
	}

	graph := Graph[S]{
		id:        definition.ID,
		schema:    definition.Schema,
		digest:    validated.digest,
		initial:   definition.Initial,
		nodes:     make(map[string]Node[S], len(definition.States)),
		states:    slices.Sorted(maps.Keys(definition.States)),
		terminals: slices.Sorted(maps.Keys(definition.Terminals)),
	}

	// Everything wrong is reported together, for the reason Validate reports its
	// problems together: a definition is written by hand, and answering one
	// question per reload is how somebody gives up on a format. The walk is over
	// sorted names, so one definition refused twice is refused identically.
	required := make(map[capability.Capability]bool, len(capability.All()))
	var problems []error
	for _, name := range graph.states {
		state := definition.States[name]
		registered, isRegistered := l.Registry.Lookup(state.Action)
		if !isRegistered {
			problems = append(problems, fmt.Errorf("the state %q selects the action %q, which this build registers nothing under; a definition validated against one catalog is compiled against the registry that will perform it", name, state.Action))
			continue
		}
		for _, needed := range registered.Capabilities {
			if !l.Grant.confers(needed) {
				problems = append(problems, fmt.Errorf("the state %q performs %q, which requires %q; this workflow is compiled under %s and nothing it says widens that", name, state.Action, needed, grantedList(l.Grant)))
				continue
			}
			required[needed] = true
		}
		graph.nodes[name] = Node[S]{
			state:    name,
			summary:  state.Summary,
			performs: registered,
			next:     destinations(definition, state),
		}
	}
	if len(problems) > 0 {
		return Graph[S]{}, errors.Join(problems...)
	}
	graph.requires = inDeclaredOrder(required)
	return graph, nil
}

// destinations resolves one state's transitions. Validation has already refused a
// destination naming neither a state nor a terminal, so what is left is deciding
// which of the two each one is.
func destinations(definition Definition, state State) map[string]Destination {
	resolved := make(map[string]Destination, len(state.On))
	for outcome, name := range state.On {
		_, isATerminal := definition.Terminals[name]
		resolved[outcome] = Destination{Name: name, Terminal: isATerminal}
	}
	return resolved
}

// inDeclaredOrder is a set of capabilities in the order the vocabulary declares
// them, which is what keeps a grant and a graph reporting themselves the same way
// twice.
func inDeclaredOrder(held map[capability.Capability]bool) []capability.Capability {
	ordered := make([]capability.Capability, 0, len(held))
	for _, declared := range capability.All() {
		if held[declared] {
			ordered = append(ordered, declared)
		}
	}
	return ordered
}

// grantedList is what a grant confers, for a refusal to say instead of leaving
// somebody to work out what they were compiled under.
func grantedList(grant Grant) string {
	conferred := make([]string, 0, len(grant.granted))
	for _, held := range grant.granted {
		conferred = append(conferred, held.String())
	}
	return strings.Join(conferred, ", ")
}
