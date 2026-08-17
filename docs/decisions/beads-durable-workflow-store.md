# Beads is the durable workflow store

**Status:** Accepted. Recorded by the operator, ratifiable by the architect when
the role runs. This decision was made and acted on during the v1 design; it was
stated in [the v1 goals](../product/goals/v1-goals.md) until it was moved here,
because it is a decision about how Yoyodyne is built rather than an outcome the
product is trying to reach.

## Context

Yoyodyne needs somewhere durable to hold workflow state: what work exists, what
depends on what, what is blocked, what directives the operator has given, and
what one role has handed to another. That state has to survive process restarts
and agent turns, it has to be queryable by every role, and no agent may be the
only place it lives.

## Decision

Beads is that store. Workflow, dependencies, blockers, directives, approvals, and
handoff state live in Beads. Yoyodyne does not implement its own tracker and does
not keep authoritative workflow state in memory, in prompts, or in repository
files.

## Consequences

- Repository Markdown owns artifact *content*; Beads owns workflow *state*. The
  division is deliberate, and the two are not alternatives.
- Replacing Beads is a non-goal for v1.
- Agents act on the tracker through bounded, validated operations rather than by
  editing its storage, so every change to workflow state is attributable.
- Yoyodyne inherits Beads' data model. Where the product needs something Beads
  does not express, the answer is a design question, not a reason to fork the
  store.
```

---
