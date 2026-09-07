---
id: provider-adapters-and-endpoints
kind: design
title: Provider adapters and execution endpoints
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-09-07T19:30:00Z
      reason: promoted from the operator's multi-provider scoping question (yoyodyne-ifd.337) under the goals clause admitting multiple providers behind one adapter contract
---

# Provider adapters and execution endpoints

**What this is for.** The goals now require multiple providers behind one adapter contract. This design owns what an adapter must supply, which roles a provider may serve, how pooling and failover work across providers rather than within one, and what durable records carry so evidence stays provider-independent. Claude-specific configuration and capacity semantics stay in [claude-execution-and-account-routing](claude-execution-and-account-routing.md), which this generalizes rather than replaces.

**The adapter contract.** Availability, capability declaration, start, resume, normalized events, usage and cost reporting, sandbox and permission mapping, and refusal classification into the governed taxonomy — usage limit with reset, server overload, the recoverable classes, the terminal auth and permission refusals. An adapter that cannot classify refusals cannot be admitted: the wait machinery, the capacity-blocked state, and the retry taxonomy all key on that classification. A provider speaking a protocol a compiled adapter already reads enters as configuration under the plugin contract; a different protocol is a harness change.

**Role eligibility.** Derived from declared capabilities, never from a provider name, and refused at configuration load. The reviewer requires a demonstrable no-tools posture; the developer requires worktree-scoped write under a sandbox. A provider whose declaration is unverified is ineligible for every role. Claude Code serves all roles; Codex serves the developer only, because its sandbox cannot express no-tools.

**Endpoints and pools.** The unit is the execution endpoint — provider, adapter version, account alias, model. Pools are pools of endpoints, with round-robin among active endpoints, a reserved fallback, affinity, health, and per-alias attribution as already contracted. Failover is posture-preserving: an invocation moves only to an endpoint holding the same role posture, per the fallback clause that never weakens checks, authority, or review independence. Crossing providers rebuilds from the durable record rather than resuming a session, at the cost of context reconstruction paid only when crossing.

**Records.** Every invocation records endpoint identity in full, the configuration revision, the normalized event schema version, the refusal classification where one occurred, and provider-reported cost naming its provider. Review-independence evidence is expressed as endpoint-plus-invocation identity and never as provider session semantics, so a cross-provider review remains verifiable.
