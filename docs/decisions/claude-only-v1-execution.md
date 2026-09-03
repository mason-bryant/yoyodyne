---
id: claude-only-v1-execution
kind: decision
title: "V1 executes every role on Claude Code; Codex and the provider plugin are parked"
status: active
revisions:
    - action: created
      by: architect
      at: 2026-08-23T16:39:19Z
      reason: recording the operator's 2026-08-22 decision during the multi-model promotion
---

# V1 executes every role on Claude Code; Codex and the provider plugin are parked

**Status:** Accepted. Refines [claude-code-default-backend](claude-code-default-backend.md) rather than superseding it: the backend boundary and Claude Code's default position stand; what this records is V1 scope.

## Context

The Codex adapter (yoyodyne-ifd.6) was designed and unbuilt; the generic provider plugin (yoyodyne-ifd.32) was tracked and unstarted. Every role runs on Claude Code today, and the near-term risk is an unreliable Claude path, not a missing second provider.

## Decision

V1 runs every role through Claude Code with fixed operator-configured assignments, one configured account, and no automatic per-item cost routing. Codex and the plugin contract are parked at priority 4, preserved, reconsidered only when they compete against remaining priorities.

## Consequences

- The backend boundary stays as the place a second connector attaches; no connector work exists to exercise it.
- Cross-provider fallback is deferred *with* the connector rather than half-built without one.
- The claude-execution-and-account-routing design's pinning and alias discipline is what keeps this narrowing reversible.
