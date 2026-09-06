---
id: claude-execution-and-account-routing
kind: design
title: "Claude execution: pinned invocations, capacity semantics, and the additive account-pooling contract"
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-08-23T16:39:19Z
      reason: promoted from the operator's multi-model execution and account routing brief under the 2026-08-22 mandate; deviations recorded in the promotion, with the capacity-blocked state carried as deviation-to-implement from the observability promotion
    - action: amended
      by: architect
      at: 2026-09-07T00:30:00Z
      reason: yoyodyne-ifd.306 - conversation account failover designed, turn-granular, durable-record rebuild exercising provider-independence, named-account affinity with return at window reopen, per-turn alias attribution with failover reason, cost paid in context reconstruction only while the window is closed; configuration is a pooled named endpoint plus operator-local enablement
---

# Claude execution: pinned invocations, capacity semantics, and the additive account-pooling contract

## What this is for

V1 executes every role through Claude Code, on one configured account, with fixed operator-configured model assignments — and the discipline that makes that narrow choice safe to widen later: every invocation pinned and recorded, capacity failures visible rather than silently routed around, and durable state that never depends on a provider session. It serves the goals that safety invariants hold whatever the configuration says and that the operator can see what the system does on their behalf. The Codex adapter and the generic provider plugin are parked at priority 4, preserved and off the critical path; the backend boundary remains where a second connector would attach, and no connector work exists merely to exercise it.

## Pinned invocations

Each provider invocation records, and remains pinned for its lifetime to: backend, model, **account alias**, and **configuration revision** — all four carried by every run record, as landed. Model and account choice stays explicit and observable even with one backend of one account, because that is what makes pooling additive: the fields exist before the second account does. Configuration refers to accounts only by operator-chosen alias; credentials stay in provider-native local authentication, and no private account identifier reaches the read model, a page, or a message.

## Capacity semantics

When Claude Code reports explicit usage-limit exhaustion: a wait within the configured threshold keeps the logical run waiting, under the existing polling discipline — the advertised reset is an upper bound, a limit without one polls at the configured interval, and the harness never estimates a wait. Past the threshold, the run is **preserved in the operator-visible capacity-blocked state**: the claim, branch, worktree, developer session, phase, attempt, budgets, reviewer findings, and the capacity-block reason all durable, and **no other provider is silently selected** — capacity is reported, never routed around. The transition records the model and alias, the wait calculation, the configuration revision, and attributable cost. When capacity returns, the run resumes from the native session where it is valid, and otherwise is reconstructed from durable and provider-visible evidence — hidden provider state is never the only copy of anything (the invariant below). The capacity-blocked state is the one the observability read model queries; it is specified here and implemented with that model.

## Configuration reload

Reload is atomic through validation: a valid revision applies to new invocations only, active invocations keep the revision they were pinned to, and an invalid revision changes nothing — the last valid revision stays active and the failure is visible as a warning and an audit event. Today's per-command loading satisfies this trivially; the resident product-manager service satisfies it by watching or reloading through the same validation path. Partial activation does not exist in either shape.

## The configuration split

Project policy is portable and versioned with the repository: roles, execution profiles, allowed providers, thresholds, routing constraints. Machine capacity is local: named account profiles, authentication references, pools, priorities, health. Credential enrollment and removal are always local, CLI-first; Slack and the dashboard display non-secret effective state and activate nothing. A later typed configuration service — preview, validation, audit, rollback, atomic activation — is deferred and named.

## The pooling contract *(the post-v1 fast follow, shaped now)*

Claude accounts become named execution endpoints. A pool round-robins its active accounts — two, in the motivating shape — and reserves a fallback; a run or provider session is **affined to its account** until the account becomes unavailable or policy crosses the wait threshold. Capacity, health, and cost are attributed per account, by alias. The acceptance bar that keeps this additive: a pool of two active accounts and one reserved **changes no run schema** — the alias field the records already carry is the attribution mechanism, and a fast follow that needs a migration has failed this design.

## Fallback, bounded now for later

No automatic account or provider fallback exists in V1. When fallback of any kind arrives, it chooses only among operator-approved backends, models, accounts, and policies; it starts a new provider-native session and reconstructs full durable context; the run records original and fallback backend, model, alias, reason, revision, and cost; and it **never weakens checks, authority, review independence, or acceptance criteria** — a cheaper path through the gates is not a path. Development-manager routing among governed execution profiles is post-v1 and evidence-gated, per the management-and-supervision design: profiles chosen on measured cost, complexity, and outcomes, never invented provider policy.

## Conversation failover

**The pin was an optimization, never the record.** A conversation's account pin exists for provider-session resume; `durable-state-is-provider-independent` already guarantees any account can host any turn by rebuilding from the durable conversation record. Failover is that invariant exercised, not weakened.

**Trigger.** At turn start — and at reissue after a refusal — if the conversation's named account is usage-limited or capacity-blocked, the turn is served by another active pooled account under operator-approved policy. Never mid-generation, and never a different provider: this is account selection inside the pool the operator configured, which the fallback clause already permits — operator-approved accounts, recorded, gates untouched (a conversation produces no gate evidence, so none are in reach).

**The rebuild.** A provider session is account-bound, so a failover turn reconstructs the conversation from the durable record rather than resuming — which is exactly what the record exists to make possible. **Return:** the named account keeps affinity; the conversation returns to it at the first turn after its window reopens, and the failover account accrues none, so a conversation cannot flap between accounts.

**Attribution.** Every turn records the serving alias and configuration revision — the same pinning run records carry — plus, on a failover turn, the named account and the reason. Nothing is silent: the record says which account served every turn, and the cost surfaces price it as they price everything, from the provider's own report.

**The cost story.** A failover turn pays full context reconstruction where a resumed turn pays a session resume — more expensive per turn, paid only while the named window is closed, and that price is the point: the deciders keep thinking through a window instead of stopping with the doers.

**Configuration.** The second account enters as a named endpoint in operator-local machine capacity, per the pooling contract — account profile, auth reference — with conversation failover enabled as operator-local policy naming which pooled accounts may serve conversations. No run schema and no conversation schema changes: the alias fields carry it.

