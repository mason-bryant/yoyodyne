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
    - action: amended
      by: architect
      at: 2026-09-26T19:54:03Z
      reason: yoyodyne-ifd.437.7 - 'posture' replaced with 'tool access' in the eligibility and failover rules; nothing about either rule changes (wording from approved amendment 5eca2c89)
    - action: amended
      by: architect
      at: 2026-09-25T04:56:31Z
      reason: 'yoyodyne-ifd.352 - all five roles on Codex scoped: one confinement obstacle shared by the four evidence-only roles, gated on a harness-side probe, with the management roles'' output contract shown to be the harness''s already; the probe, the confinement layer, and the conversation adapter are the three slices'
---

# Provider adapters and execution endpoints

**What this is for.** The goals now require multiple providers behind one adapter contract. This design owns what an adapter must supply, which roles a provider may serve, how pooling and failover work across providers rather than within one, and what durable records carry so evidence stays provider-independent. Claude-specific configuration and capacity semantics stay in [claude-execution-and-account-routing](claude-execution-and-account-routing.md), which this generalizes rather than replaces.

**The adapter contract.** Availability, capability declaration, start, resume, normalized events, usage and cost reporting, sandbox and permission mapping, and refusal classification into the governed taxonomy — usage limit with reset, server overload, the recoverable classes, the terminal auth and permission refusals. An adapter that cannot classify refusals cannot be admitted: the wait machinery, the capacity-blocked state, and the retry taxonomy all key on that classification. A provider speaking a protocol a compiled adapter already reads enters as configuration under the plugin contract; a different protocol is a harness change.

**Role eligibility.** Derived from declared capabilities, never from a provider name, and refused at configuration load. The reviewer requires a provider that can refuse every tool; the developer requires worktree-scoped write under a sandbox. A provider whose declaration is unverified is ineligible for every role. Claude Code serves all roles. Codex serves the developer today, because its read-only sandbox still lets an agent run read-only commands over the whole machine, and that is a tool for every evidence-only role — the product manager, the architect, the development manager, and the reviewer alike — not only for the reviewer whose independence makes it visible first. The way to all five roles on Codex is one obstacle, not two: a harness-side confinement to an evidence directory the harness names, proven by a probe that shows a confined invocation cannot read a canary outside it or reach the network while it can still read its provider home, and declared by the adapter as the `read-only` tool access only when present, so validation admits the four roles by declaration. The management roles' structured output is not a provider property: the harness parses and refuses the blocks it reads on every provider, and what Codex management turns need is the conversation adapter — resume or rebuild, normalized events, per-turn cost, refusal classification — under this contract. A probe that cannot demonstrate the confinement records no as the answer for the four roles, with its output as the reason.

**Endpoints and pools.** The unit is the execution endpoint — provider, adapter version, account alias, model. Pools are pools of endpoints, with round-robin among active endpoints, a reserved fallback, affinity, health, and per-alias attribution as already contracted. Failover only moves an invocation to an endpoint that gives the role the same tool access, per the fallback clause that never weakens checks, authority, or review independence. Crossing providers rebuilds from the durable record rather than resuming a session, at the cost of context reconstruction paid only when crossing.

**Records.** Every invocation records endpoint identity in full, the configuration revision, the normalized event schema version, the refusal classification where one occurred, and provider-reported cost naming its provider. Review-independence evidence is expressed as endpoint-plus-invocation identity and never as provider session semantics, so a cross-provider review remains verifiable.
