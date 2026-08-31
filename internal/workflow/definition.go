// Package workflow is the declarative form of a sequence the harness executes:
// the states it can be in, the registered action each one performs, and where
// each outcome of that action goes next.
//
// It is a state machine rather than a list of steps, because the sequences this
// harness actually runs branch — a review that asks for changes goes back to the
// developer, a promotion that lost a race goes back to the same place — and a
// list cannot say that. It is deliberately not a language: there are no
// expressions, no conditions of its own, and no way to describe an action. A
// definition selects among actions Go registered and decides what happens after
// each outcome, which is the whole of what it can do. That is the same sentence
// `internal/action` enforces from the other side, and it is why a definition is
// safe to read from a project's own repository.
//
// This package gives a definition a shape, a stable serialized form, a content
// digest an instance can pin itself to, a validation that either produces a
// definition every later reader can trust or refuses the whole of it, a loader
// that compiles what passed into a graph — every state resolved to the action
// code registered, every transition resolved to a destination that exists, and
// the whole of it held against the authority it was compiled under and the digest
// it was pinned to — and an executor that walks such a graph one durable
// transition at a time, recording every state boundary it crosses so a process
// killed at one is resumed exactly there.
//
// Nothing in the delivery path runs on it. The executor is exercised against test
// graphs, because what makes an action with a side effect safe to re-perform
// after a death — the step-attempt record, the idempotency key, and the
// reconciliation the design describes — is a later milestone, and the pipeline
// still runs the sequence Go control flow puts it in.
package workflow

import (
	"errors"
	"fmt"
	"io"

	yaml "go.yaml.in/yaml/v3"
)

// SchemaVersion is the version of this format that this build reads and writes.
//
// It is declared in every definition and checked before anything else, so a file
// written against a later schema is refused for the reason it is actually wrong
// rather than field by field as its unfamiliar keys are met. A definition's
// digest covers it, so an instance pinned to a digest is pinned to the schema its
// definition was read under as well.
const SchemaVersion = 1

// ReservedPrefix marks the destinations the runtime owns. A definition names its
// own states and terminals and cannot name one of these, which is what keeps
// `$wait` — the durable wait a definition may eventually transition into but
// never define — from colliding with something a project wrote.
const ReservedPrefix = "$"

// Definition is a workflow as its file declares it: the decoded form, before
// anything has been checked about it. Validate is what turns one into something
// worth executing; until then it is what a file said and no more.
type Definition struct {
	// Schema is the format version this definition was written against. See
	// SchemaVersion.
	Schema int `yaml:"schema" json:"schema"`
	// ID is the workflow's stable identity — what a binding selects and what an
	// instance records alongside the digest it pinned. It is not the identity of
	// the content: two revisions of one workflow share an ID and differ by digest,
	// which is exactly the pair an instance needs to say what it is running and
	// which version of it.
	ID string `yaml:"id" json:"id"`
	// Summary is what this workflow is for, in the words somebody choosing between
	// two of them needs.
	Summary string `yaml:"summary,omitempty" json:"summary,omitempty"`
	// Initial is the state an instance starts in. It names a state and never a
	// terminal: a workflow that is over before it begins is a definition somebody
	// got wrong rather than a sequence worth starting.
	Initial string `yaml:"initial" json:"initial"`
	// States is every state, by name. A state is a step: one action, and where each
	// of its outcomes goes.
	States map[string]State `yaml:"states" json:"states"`
	// Terminals is every way this workflow can end, by name. They are declared
	// rather than inferred so that ending is something a definition says on purpose,
	// and so a transition into one reads as an ending instead of as a step nobody
	// wrote.
	Terminals map[string]Terminal `yaml:"terminals" json:"terminals"`
}

// State is one step: the action it performs, and where its outcomes lead.
type State struct {
	// Action is the registered action this state performs, by name. It is resolved
	// against the catalog of what Go registered, and a name nothing registers is
	// refused — a definition selects actions and never describes one.
	Action string `yaml:"action" json:"action"`
	// Summary is what this step does here, which is not always what the action
	// does in general.
	Summary string `yaml:"summary,omitempty" json:"summary,omitempty"`
	// On maps each outcome of the action to where the instance goes next: another
	// state, or a terminal. This is the only place a definition decides anything,
	// and it decides by naming a destination rather than by evaluating anything.
	//
	// Whether the outcomes here are the outcomes the action can actually produce is
	// not checkable yet: a registered action declares its capabilities but not its
	// outcomes. Until it does, an outcome is a name this definition maps and the
	// runtime will have to answer for; see the package's own note in Validate.
	On map[string]string `yaml:"on" json:"on"`
}

// Terminal is one way a workflow ends. It carries only what it is for, because
// ending is not a step: nothing is performed, and there is nowhere to go next.
type Terminal struct {
	Summary string `yaml:"summary,omitempty" json:"summary,omitempty"`
}

// Decode reads a definition from its serialized form, refusing anything the
// schema does not describe.
//
// Unknown fields are refused rather than ignored, which is the whole reason to
// decode strictly: a misspelled key that decodes quietly is a transition nobody
// wrote and an instance that goes somewhere its author did not intend. The same
// goes for a second document in one file — one file is one workflow, and a
// second one silently dropped is a definition somebody believes is loaded.
//
// What comes back has been decoded and not validated. Callers that want a
// definition worth executing want Load.
func Decode(reader io.Reader) (Definition, error) {
	decoder := yaml.NewDecoder(reader)
	decoder.KnownFields(true)

	var definition Definition
	if err := decoder.Decode(&definition); err != nil {
		if errors.Is(err, io.EOF) {
			return Definition{}, errors.New("decode workflow definition: the definition is empty")
		}
		return Definition{}, fmt.Errorf("decode workflow definition: %w", err)
	}

	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return Definition{}, errors.New("decode workflow definition: one file is one workflow; this file holds more than one document")
		}
		return Definition{}, fmt.Errorf("decode workflow definition: %w", err)
	}
	return definition, nil
}

// Load decodes a definition and validates it in one step, which is what a
// caller reading a file almost always wants: a definition that is worth
// executing, or the whole of what is wrong with the one it read.
func Load(reader io.Reader, catalog Catalog) (Validated, error) {
	definition, err := Decode(reader)
	if err != nil {
		return Validated{}, err
	}
	return definition.Validate(catalog)
}

// clone is a copy that shares nothing with the original. A Definition holds
// maps, so handing one out or taking one in without this would let a caller
// change a definition after it was validated and after its digest was taken —
// which is the one thing a pinned definition must not permit.
func (d Definition) clone() Definition {
	copied := d
	copied.States = make(map[string]State, len(d.States))
	for name, state := range d.States {
		state.On = clonedTransitions(state.On)
		copied.States[name] = state
	}
	copied.Terminals = make(map[string]Terminal, len(d.Terminals))
	for name, terminal := range d.Terminals {
		copied.Terminals[name] = terminal
	}
	return copied
}

// clonedTransitions copies one transition table. A nil table stays nil rather
// than becoming empty, so a copy and its original serialize identically.
func clonedTransitions(transitions map[string]string) map[string]string {
	if transitions == nil {
		return nil
	}
	copied := make(map[string]string, len(transitions))
	for outcome, destination := range transitions {
		copied[outcome] = destination
	}
	return copied
}
