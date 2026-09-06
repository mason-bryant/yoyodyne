// Package agentcontext is the door an agent's own memory is written through.
//
// The `### Agent memory` section of `docs/designs/configurable-workflows.md`
// (the revision dated 2026-09-05T17:40:00Z) allows agent-authored memory to be
// written only through typed context actions the role contract owns. This is
// those actions: three registered operations over one subject, each declaring
// the authority performing it requires, wrapping the store in
// `internal/runstate` and adding nothing to it.
//
// Three actions rather than one write call, because remembering, compacting, and
// retiring are three different things to find in a durable record afterwards even
// though the store records all three the same way. The registry is what makes
// them selectable by a workflow definition later without that definition being
// able to widen what any of them may do — configuration selects sequence and code
// grants capability, which is `configuration-never-grants-authority` in the place
// it is easiest to break.
//
// What is deliberately not here is a read. What an agent knows is assembled into
// its own prompt by the runtime, which the design keeps runtime-internal along
// with the rest of prompt assembly; what an operator reads is the audit history,
// which the design settles as a CLI surface and never an agent one.
package agentcontext

import (
	"context"
	"errors"
	"fmt"

	"github.com/mason-bryant/yoyodyne/internal/action"
	"github.com/mason-bryant/yoyodyne/internal/capability"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/rolecapability"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// ErrUnauthorized is what every refused memory write returns, so a caller can
// tell refused authority from a malformed revision or a spent budget.
var ErrUnauthorized = errors.New("the role may not write its own memory")

// Authorize refuses a memory write by a role whose bundle does not hold
// `agent-context.mutate`.
//
// It asks the role-capability registry rather than naming roles, so the answer
// here and the answer anywhere else about the same authority are one statement.
// The registry is the shipped default: a bundle is trusted data in Go, and
// nothing a project configures reaches it.
func Authorize(role domain.AgentRole) error {
	if rolecapability.MustDefault().Holds(role, capability.AgentContextMutate) {
		return nil
	}
	return fmt.Errorf("%w: the %s does not hold %s", ErrUnauthorized, role.Title(), capability.AgentContextMutate)
}

// Write is what a context action acts on: the store to write to, the revision to
// write, and — once it has been performed — what was actually stored.
//
// The recorded revision is on the subject rather than returned from Perform
// because a registered action returns an outcome and not a value. A caller that
// needs to know what its agent will read back later reads Recorded, which is what
// reached the disk: redacted, numbered, and stamped.
type Write struct {
	Store *runstate.MemoryStore
	// Revision is what the agent asked to record. Its sequence is left unset: the
	// store numbers a revision, so nothing outside it decides where one falls.
	Revision runstate.MemoryRevision
	// Recorded is what was stored, and is the zero revision until an action has
	// been performed.
	Recorded runstate.MemoryRevision
}

// Remember records something the agent has learned.
func (w *Write) Remember(ctx context.Context) error {
	return w.perform(ctx, "remember", func(revision runstate.MemoryRevision) error {
		if revision.Compacted() {
			return errors.New("this revision compacts earlier ones, which is agent-context.compact")
		}
		if revision.Retired {
			return errors.New("this revision retires the memory, which is agent-context.retire")
		}
		return nil
	})
}

// Compact folds earlier revisions of one memory into a shorter one, naming what
// it replaced. The naming is the design's requirement and it is checked here as
// well as in the store, so an action selected as a compaction cannot quietly be
// an ordinary write.
func (w *Write) Compact(ctx context.Context) error {
	return w.perform(ctx, "compact", func(revision runstate.MemoryRevision) error {
		if !revision.Compacted() {
			return errors.New("a compaction records the revisions it replaces")
		}
		return nil
	})
}

// Retire takes a memory out of the live set, leaving its history readable. The
// retiring revision still says what is being retired, because a history that goes
// blank tells the next reader nothing.
func (w *Write) Retire(ctx context.Context) error {
	return w.perform(ctx, "retire", func(revision runstate.MemoryRevision) error {
		if !revision.Retired {
			return errors.New("a retirement marks the memory retired")
		}
		return nil
	})
}

// perform is what all three share: the authority, the shape the operation
// promises, and the one write.
//
// The authority is checked before the shape, so a role that may not write its own
// memory is told that rather than being told its revision was malformed — a
// refusal that names the wrong reason is one somebody works around.
func (w *Write) perform(ctx context.Context, operation string, shaped func(runstate.MemoryRevision) error) error {
	if w == nil || w.Store == nil {
		return fmt.Errorf("agent-context.%s has no memory store to write to", operation)
	}
	if err := Authorize(w.Revision.Role); err != nil {
		return err
	}
	if err := shaped(w.Revision); err != nil {
		return fmt.Errorf("agent-context.%s: %w", operation, err)
	}
	recorded, err := w.Store.Remember(ctx, w.Revision)
	if err != nil {
		return fmt.Errorf("agent-context.%s: %w", operation, err)
	}
	w.Recorded = recorded
	return nil
}

// Registry is the three context actions, closed the moment it is built.
func Registry() (action.Registry[*Write], error) {
	return action.New(registered()...)
}

func registered() []action.Action[*Write] {
	return []action.Action[*Write]{
		{
			Name:         "agent-context.remember",
			Summary:      "record something the agent has learned, as a new revision of one of its memories",
			Wraps:        "(*Write).Remember",
			Capabilities: []capability.Capability{capability.AgentContextMutate},
			Perform:      func(ctx context.Context, write *Write) error { return write.Remember(ctx) },
		},
		{
			Name:         "agent-context.compact",
			Summary:      "fold earlier revisions of one memory into a shorter one that names what it replaced",
			Wraps:        "(*Write).Compact",
			Capabilities: []capability.Capability{capability.AgentContextMutate},
			Perform:      func(ctx context.Context, write *Write) error { return write.Compact(ctx) },
		},
		{
			Name:         "agent-context.retire",
			Summary:      "take a memory out of the live set, leaving its history readable",
			Wraps:        "(*Write).Retire",
			Capabilities: []capability.Capability{capability.AgentContextMutate},
			Perform:      func(ctx context.Context, write *Write) error { return write.Retire(ctx) },
		},
	}
}
