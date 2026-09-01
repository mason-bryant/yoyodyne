// Package rolecapability is what each of the harness's five roles may do, said
// in capabilities instead of in the role's name.
//
// The harness asks "is this the architect?" in about sixty places, and the
// authority inventory is the list of them. This package is the other side of that
// list: the same authority stated once, as a bundle of capability primitives per
// role, so that a call site asks what a role may do rather than who it is. The
// vocabulary and the bundles were reviewed before anything read them, deliberately,
// because a vocabulary and a rewiring reviewed together are a vocabulary nobody
// reviewed. The sites that read them now are the rows of the inventory that turn
// on a role's identity: which role owns a kind of document, which may change an
// invariant, what a role may ask for in a conversation, and which role's judgement
// gates an integration. Everything the inventory records as something else — a
// contract's prose, a path gate, the separation of two invocations, a posture — is
// answered there with the reason a bundle cannot express it.
//
// The bundles are trusted data in Go and configuration never writes them. That is
// the design's law in the place it is easiest to break: a workflow definition
// selects which registered actions run and in what order, and a role definition
// says what may be performed at all. If the second were configurable by the same
// file as the first, a project could grant itself the authority its own sequence
// needed. Operator-authored bundles are a later step of the same workstream, and
// they arrive protected, activated by digest, and refused by the protected-path
// gate — not as a table anything in this repository can widen today.
//
// # What holding a capability means, and what it does not
//
// A bundle says what the harness may do on a role's behalf, through trusted code.
// It is not a tool grant and it is not a licence for the agent filling the role to
// do the thing itself. The developer's bundle holds ForgePublish because the run
// carrying a developer's work pushes its branch and opens its pull request; the
// developer contract refuses the developer doing that, and both are true at once
// because the harness performs it and the agent does not. Which tools an agent
// gets is a separate axis the backend decides from the role's posture, and this
// package neither reads nor replaces it.
//
// The promotion is the one authority that belongs to no bundle. The inventory
// binds it to the harness rather than to a role, and `one-promotion-per-target-branch`
// puts it further out of reach still: no agent performs a promotion and no agent
// touches the lease that admits one. So the capabilities the promotion needs are
// recorded here as held by the harness, named and reasoned rather than left
// unaccounted for — and a bundle that tried to confer one is refused where the
// registry is built.
package rolecapability

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/capability"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// Bundle is one role and everything it may do.
type Bundle struct {
	// Role is the role this bundle is for. It is one of the harness's five: a
	// bundle for anything else is refused, because a name outside that set names
	// authority nobody wrote.
	Role domain.AgentRole
	// Owns is the one-line statement of what this role decides, in the words an
	// operator reads. It is here so a bundle can be shown to somebody without
	// their having to infer the role from a list of primitives.
	Owns string
	// Holds is every capability this role has, in the order the vocabulary
	// declares them. Nothing outside this package adds to it and nothing takes
	// anything away.
	Holds []capability.Capability
}

// Held is one capability the harness holds itself, and why no bundle confers it.
//
// The reason is required rather than decorative. A capability no role holds looks
// exactly like one somebody forgot to place, and the difference between the two is
// the whole of what makes this list reviewable.
type Held struct {
	Capability capability.Capability
	Reason     string
}

// Registry is the closed statement of who holds what.
type Registry struct {
	byRole map[domain.AgentRole]Bundle
	// roles is the order the hierarchy runs in, kept so that what a registry
	// reports about itself reads the same twice.
	roles   []domain.AgentRole
	harness []Held
}

// New builds a registry, refusing anything that would make it untrustworthy.
//
// Everything it refuses, it refuses at construction, from a literal table, so a
// defect in that table is a defect in the binary rather than in one run's luck.
// The refusals are reported together, because a table with three mistakes in it is
// worth seeing whole.
//
// The last of them is the one that makes this a registry rather than a list: a
// capability the repository declares and neither a bundle nor the harness accounts
// for is refused. A vocabulary is only worth having if every word in it has a
// holder, and a capability nobody holds is one an action can require and nothing
// can ever satisfy.
func New(bundles []Bundle, harness []Held) (Registry, error) {
	registry := Registry{byRole: make(map[domain.AgentRole]Bundle, len(bundles))}
	var problems []error
	for index, candidate := range bundles {
		if !candidate.Role.Valid() {
			problems = append(problems, fmt.Errorf("the bundle at position %d is for %q, which is not one of the harness's roles; the set is closed and a bundle for a name outside it grants authority nobody wrote", index, candidate.Role))
			continue
		}
		if _, taken := registry.byRole[candidate.Role]; taken {
			problems = append(problems, fmt.Errorf("the %s has more than one bundle; a role that holds two sets of capabilities holds whichever was read last", candidate.Role.Title()))
			continue
		}
		// A role that may do nothing has not been described. Every one of the five
		// does something today, and an empty bundle reads as a deliberate statement
		// that a role is inert rather than as the omission it would be.
		if len(candidate.Holds) == 0 {
			problems = append(problems, fmt.Errorf("the %s's bundle holds nothing; every role states what it may do", candidate.Role.Title()))
		}
		held := map[capability.Capability]bool{}
		for _, capable := range candidate.Holds {
			if !capable.Known() {
				problems = append(problems, fmt.Errorf("the %s holds %q, which is not a capability this repository declares; add it to internal/capability or name one that is there", candidate.Role.Title(), capable))
				continue
			}
			if held[capable] {
				problems = append(problems, fmt.Errorf("the %s holds %q twice; one role holding one capability is one statement", candidate.Role.Title(), capable))
				continue
			}
			held[capable] = true
		}
		registry.byRole[candidate.Role] = Bundle{Role: candidate.Role, Owns: candidate.Owns, Holds: inDeclaredOrder(held)}
		registry.roles = append(registry.roles, candidate.Role)
	}
	// A role with no bundle at all is the same defect as an empty one and is
	// easier to arrive at, since it takes no edit here to happen: adding a role to
	// the harness and nothing to this table would leave it silently holding nothing.
	for _, role := range domain.Roles() {
		if _, described := registry.byRole[role]; !described {
			problems = append(problems, fmt.Errorf("no bundle describes the %s; every role the harness has says what it may do", role.Title()))
		}
	}

	claimed := map[capability.Capability]bool{}
	for _, held := range harness {
		if !held.Capability.Known() {
			problems = append(problems, fmt.Errorf("the harness is recorded as holding %q, which is not a capability this repository declares", held.Capability))
			continue
		}
		if claimed[held.Capability] {
			problems = append(problems, fmt.Errorf("the harness is recorded as holding %q twice", held.Capability))
			continue
		}
		if held.Reason == "" {
			problems = append(problems, fmt.Errorf("%q is recorded as the harness's with no reason; a capability no role holds is indistinguishable from one somebody forgot to place", held.Capability))
		}
		if roles := registry.rolesHolding(held.Capability); len(roles) > 0 {
			problems = append(problems, fmt.Errorf("%q is recorded as the harness's and is also held by %s; that is two answers to one question", held.Capability, titles(roles)))
		}
		claimed[held.Capability] = true
	}
	registry.harness = slices.Clone(harness)

	for _, declared := range capability.All() {
		if claimed[declared] || len(registry.rolesHolding(declared)) > 0 {
			continue
		}
		problems = append(problems, fmt.Errorf("nothing holds %q: no role's bundle carries it and it is not recorded as the harness's own; a capability with no holder is one an action can require and nothing can satisfy", declared))
	}

	if len(problems) > 0 {
		return Registry{}, errors.Join(problems...)
	}
	return registry, nil
}

// Roles is every role this registry describes, in the order the hierarchy runs.
func (r Registry) Roles() []domain.AgentRole {
	return slices.Clone(r.roles)
}

// Bundle is what a role holds, and whether this registry describes the role at
// all. A role it does not describe holds nothing, which is deliberately not the
// same answer as a role that holds an empty bundle: the first is unknown and the
// second cannot be built.
func (r Registry) Bundle(role domain.AgentRole) (Bundle, bool) {
	described, known := r.byRole[role]
	if !known {
		return Bundle{}, false
	}
	return Bundle{Role: described.Role, Owns: described.Owns, Holds: slices.Clone(described.Holds)}, true
}

// Holds reports whether a role has a capability. An unknown role holds nothing:
// the answer to "may this role do it" for a role nobody described is no, and it is
// the only safe answer a lookup can give.
func (r Registry) Holds(role domain.AgentRole, required capability.Capability) bool {
	described, known := r.byRole[role]
	return known && slices.Contains(described.Holds, required)
}

// RolesHolding is every role with a capability, in the order the hierarchy runs.
// It is empty for a capability the harness holds itself and for one nothing
// declares, which are different facts that HarnessHolds tells apart.
func (r Registry) RolesHolding(required capability.Capability) []domain.AgentRole {
	return r.rolesHolding(required)
}

// HarnessHolds reports whether a capability is the harness's own, and why. It is
// the answer to "which role holds this" for the promotion, where the honest answer
// is that no role does.
func (r Registry) HarnessHolds(required capability.Capability) (Held, bool) {
	for _, held := range r.harness {
		if held.Capability == required {
			return held, true
		}
	}
	return Held{}, false
}

// HarnessCapabilities is everything the harness holds itself, in the order it was
// written down.
func (r Registry) HarnessCapabilities() []Held {
	return slices.Clone(r.harness)
}

func (r Registry) rolesHolding(required capability.Capability) []domain.AgentRole {
	var holding []domain.AgentRole
	for _, role := range r.roles {
		if slices.Contains(r.byRole[role].Holds, required) {
			holding = append(holding, role)
		}
	}
	return holding
}

// inDeclaredOrder is a set of capabilities in the order the vocabulary declares
// them, so a bundle reads the same however it was written down.
func inDeclaredOrder(held map[capability.Capability]bool) []capability.Capability {
	ordered := make([]capability.Capability, 0, len(held))
	for _, declared := range capability.All() {
		if held[declared] {
			ordered = append(ordered, declared)
		}
	}
	return ordered
}

// titles names roles the way a refusal has to read: "the architect and the
// reviewer" rather than a slice printed with its brackets.
func titles(roles []domain.AgentRole) string {
	named := make([]string, 0, len(roles))
	for _, role := range roles {
		named = append(named, "the "+role.Title())
	}
	switch len(named) {
	case 0:
		return "nobody"
	case 1:
		return named[0]
	default:
		return fmt.Sprintf("%s and %s", strings.Join(named[:len(named)-1], ", "), named[len(named)-1])
	}
}
