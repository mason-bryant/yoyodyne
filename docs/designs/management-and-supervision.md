---
id: management-and-supervision
kind: design
title: "Management and supervision: the typed request contract and process residency"
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-08-23T16:33:34Z
      reason: promoted from the operator's management-and-supervision brief under the 2026-08-22 mandate, discharging amendments aab3b51c and 9fafa81a; reconciles yoyodyne-ifd.99 and the yoyodyne-ifd.130 work under one contract, and is the gating input for yoyodyne-ifd.142
    - action: amended
      by: architect
      at: 2026-08-23T18:42:25Z
      reason: the autonomous-management-loop brief promoted as the companion management-loop-protocol design; this contract now points at it as the owner of the protocol vocabulary, keeping one dispatcher and one contract with the detail one level down
    - action: amended
      by: architect
      at: 2026-09-01T19:15:00Z
      reason: approved amendment b52bd247 from yoyodyne-ifd.224 - restart-on-deploy owned in one place, with the takeover bounded to idle boundaries, normal lease reattachment, and pinned instances unaffected
    - action: amended
      by: architect
      at: 2026-09-03T16:45:00Z
      reason: yoyodyne-ifd.130.1 - the supervision tree recorded, one product-manager supervisor, children surviving supervisor death with reattachment by presence and lease, bounded restarts degrading visibly through the standing ladder, and the three operator verbs as the only stop paths; launchd and cutover mechanics deferred to yoyodyne-ifd.142 by name
---

# Management and supervision: the typed request contract and process residency

## What this is for

The management roles need enough autonomy to inspect upcoming work, flag implications, decompose delivery, and explain conflicts — without role collaboration becoming prompt convention or agent-to-agent spawning, and without any coordination surface growing into a second control plane. It serves the autonomy goal directly: the brief-promotion queue sat silent for eighteen hours because conversation-executed work had no scheduler, and automatic wakeups are what close that class of gap. This design is the umbrella contract; yoyodyne-ifd.142 is its first implementation slice.

## The request contract *(reconciling yoyodyne-ifd.99; no second dispatcher)*

Roles communicate through persisted, typed requests. A request carries a stable id, its conversation or topic id, requesting and target roles, authority-relevant intent, durable references, the expected revision of what it refers to, urgency, budget, cycle limit, causation, and reply state. An inbox transition makes it eligible; **the harness acquires a lease and invokes the target role itself** — delivery is retryable and deduplicated, and a request is complete only when durable state records its outcome, never merely because a message was emitted.

The inter-role ask channel is one request type under this contract, keeping the three properties already decided for it: judgment-only, decisionless, durable-and-visible, with its configurable round cap, unresolved-escalation, and per-exchange cost reporting. The ask design and this one share the machinery; there is exactly one dispatcher, and the Slack echo of exchanges remains a notifier consumer.

The request kinds, readiness vocabulary, outcome summaries, execution profiles, and demonstration scenarios are specified in [management-loop-protocol](management-loop-protocol.md), which runs under this contract.

## Wakeups

When relevant work arrives — a request lands in an inbox, a docket entry is created, a promotion queue has items — the harness wakes the responsible role automatically, under lease, within bounded coordination cycles and cost. Wakeups are how conversation-executed work stops depending on the operator noticing silence. Every wakeup passes the same gates any invocation passes: the intake hold where it applies, the spending pause always, and the budgets recorded durably before they are spent.

## Conversation concurrency

Different durable conversations progress independently. One conversation serializes its turns and exposes queueing rather than interleaving transcript mutation — the conversation lease, per agent, single holder, is the primitive, and it is also the reattachment primitive: a returning client attaches by acquiring it. Artifact and backlog mutations use optimistic concurrency across conversations: every mutation checks the current revision and either commits atomically or is rejected as stale with enough current state to refresh and replan.

## Advisory readiness

Readiness records — product, architecture, delivery — are advisory and operator-visible in the first slice, each carrying evidence, the revision it was judged against, and staleness. They become scheduler gates only through a later revision to this design, made after their invalidation, failure, and accuracy behavior is demonstrated — a readiness gate that goes stale silently is a queue that stops for a reason nobody can see.

## Conflict handling

A role resolves what its own authority covers, and requests the role that owns what it does not. The product manager escalates to the operator only for approved intent, priority, material risk, budget, or an authority boundary — in plain language, naming both sides, the impact, the options, and a recommendation. Unrelated work continues; a conflict pauses what it touches, never the line.

## Process residency and the service shape

The long-term shape is **one durable interaction and state service**, with local chat, Slack, and the CLI as conversational clients and the dashboard as a read-only projection — "one pane" means shared state and supervision, not one literal interface. The residency direction: `yoyo chat` is the single entry point; the **product manager supervises the resident subprocesses, the Slack sink included**; today's chat-spawns-subprocesses shape is explicitly interim, on the way to a headless product-manager service — under launchd on macOS, the tested platform — with chat as a thin attach-detach client. The supervising process holds the Slack tokens and **constructs every child's environment by allowlist, never inheritance**, so no provider subprocess ever receives them; that construction is the enforced boundary and is covered by a test. Separately started services are acceptable while they keep clean service boundaries and health/readiness hooks; model sessions are execution details and are never the durable conversation record. Process separation now must not force a new state model later.

A watch session takes up a build installed over it by itself: between runs, never interrupting one. The takeover happens only at a boundary where the process has nothing in flight; the new process reattaches through the normal lease machinery; and pinned workflow instances continue on their pinned definitions and authority — a new binary changes the executor, never in-flight work's contract. This is settled here rather than deferred: a later headless supervisor inherits this behavior rather than re-deciding it.

**The supervision tree.** One supervisor per product: the product-manager service (interim: the chat process). Its children: the Slack reporter, the scheduler/watch loop, the triage dispatcher, the management-loop dispatcher, and — where started — the dashboard command. The supervisor owns start, stop, restart, and the health/readiness of children; each child owns its own domain and none owns workflow truth, per the standing invariants. **Children survive supervisor death**: they are independent processes with recorded presence, and a returning supervisor reattaches through those records and the lease machinery rather than killing and respawning — the same rule a returning chat client already follows. A child that crashes is restarted with backoff; a child that fails repeatedly is not restarted indefinitely — it is left down and reported as **degraded**, reaching the operator through the standing ladder rather than a restart loop. **The escalation ladder is the governed one and the supervisor invents no new verb**: report, then status banners, then a direct message in the degraded class. Operator stopping remains the three existing verbs — hold intake, pause spending, stop everything — and the supervisor is their executor, never a fourth path.

## Recovery

Restart reclaims expired leases without duplicating a completed response; degraded state is explicit rather than silent. A later restricted maintenance mode may take an exclusive lock for diagnostics, validation, stale-lease cleanup, and restarts — it is deferred, and one property of it is decided now: it is never a second path for normal product-manager or backlog mutations.

## The first slice *(yoyodyne-ifd.142)*

Durable inter-role requests and responses; automatic wakeups with leases, retries, and deduplication; bounded coordination cycles and cost; operator-visible advisory readiness; restart recovery and explicit degraded state; stable service interfaces Slack and local chat share. Later, separately: post-merge summaries, synthesized escalations, multi-role consultation, and development-manager model routing under governed execution profiles — the development manager chooses among profiles on measured evidence and never invents provider policy.
