---
id: claude-code-default-backend
kind: decision
title: Claude Code is the default backend; Codex is an optional alternative
status: active
revisions:
    - action: created
      by: architect
      at: 2026-08-17T00:00:00Z
      reason: identity added with the artifact metadata schema; the record itself is unchanged
---

# Claude Code is the default backend; Codex is an optional alternative

**Status:** Accepted. Recorded by the operator, ratifiable by the architect when
the role runs. This decision was made and acted on during the v1 design; it was
stated in [the v1 goals](../product/goals/v1-goals.md) until it was moved here,
because it is a decision about which tools Yoyodyne runs rather than an outcome
the product is trying to reach.

## Context

Roles need something that can actually execute: read a repository, edit files,
run commands, and report what happened. Local coding-agent CLIs already provide
that execution interface, including their own tool sandboxing and permissioning.

## Decision

Claude Code is the default backend for all roles. Codex is supported as an
optional developer and reviewer backend. Yoyodyne drives these CLIs rather than
calling model APIs directly, and does not reimplement their tool execution.

## Consequences

- Direct model API integration is a non-goal while the local CLIs provide the
  required execution interface.
- Complete behavioral parity between Claude Code and Codex is a non-goal. Roles
  are configured knowing the backends differ.
- Yoyodyne depends on interfaces it does not control, and a backend's changes can
  break it.
- Naming specific vendors here rather than in the goals keeps the product's
  intent independent of which agents happen to run underneath it. Adding,
  replacing, or removing a backend is an architectural decision, not a change to
  what the product is for.
```

---
