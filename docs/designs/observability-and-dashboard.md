---
id: observability-and-dashboard
kind: design
title: One observability read model, and the read-only dashboard that projects it
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-08-23T16:29:09Z
      reason: promoted from the operator's shared-observability-and-reduced-dashboard brief under the 2026-08-22 mandate; the gating input for yoyodyne-ifd.139 and yoyodyne-ifd.141, with deviations recorded in the promotion
---

# One observability read model, and the read-only dashboard that projects it

## What this is for

The operator can already see what the harness does — in the conversation, in `yoyo-status`, in `yoyo cost`, in Slack threads — and each surface assembles its answer separately. Every surface added that way is another place a number can disagree with the tracker, and the operator adjudicating between surfaces is the failure this design exists to prevent. It serves the goal that the operator can see what the system does on their behalf, and the cost, attention, and legibility goals beside it: one canonical, server-side derivation of status, throughput, cost, and capacity, and surfaces that only present it. The dashboard delivered here is the first surface built on that model rather than beside it.

## The read model *(the section yoyodyne-ifd.139 builds)*

A shared Go package, independent of HTTP and of any rendering, reading only the durable records that already exist — run state, normalized event logs, the tracker, conversation records, the reports pile, recorded prices — and writing nothing. The service owns derivation and attribution; surfaces own presentation and nothing else. Its queries expose:

- source health and observation time for every answer;
- active invocations, each with role, backend, model, **account alias and configuration revision** as run records now carry them, work-item id and title, phase, and start time;
- queued work and the operator-attention conditions — blockers, unresolved directives, undecided proposals, outstanding publications;
- pipeline stage counts and recent transitions, and a bounded recent-activity feed sufficient to explain the current picture;
- integrated-work totals over explicit daily and weekly windows, counting the same events the CLI counts;
- provider cost with every figure classified: known (provider-reported), unknown, or unattributable, and an `estimated` class reserved for future sources that is never summed silently into known — unknown renders as unknown, never as zero, exactly as the cost surfaces already hold;
- capacity: per configured account alias, the model, active invocation count, state — healthy, waiting, usage-limited, or **capacity-blocked** — the recorded reset time, and recent capacity events. The capacity-blocked state is required by this design and does not exist yet: a run past its wait budget today stops with a generic blocker, preserving everything; the read model's arrival is when that blocker becomes a queryable capacity state.

The model also serves what Slack's governed behavior already needs — per-item status for thread reactions, directive lifecycle marks, item titles for thread openers — so the sink presents these derivations instead of computing them.

**Honesty is a property of the model, not a style of the page.** Every metric identifies its time window; every source carries health; every total names its unknown and unattributable portions. A stale or unreadable source degrades the answer visibly rather than narrowing it silently — the same rule the freshness line and the cost floors already follow.

## The dashboard *(the sections yoyodyne-ifd.141 builds)*

A locally hosted, read-only page with five visually distinct sections: a top status band (active agents, queued work, attention count, throughput, cost); live agent and work cards (role, item title and id, phase, provider and model, elapsed time); a compact pipeline view showing where work accumulates or blocks; daily and weekly throughput with total provider cost, labeled for completeness; and the capacity panel from the model's capacity query, credentials nowhere. Attractiveness is an acceptance requirement — hierarchy, typography, spacing, accessible color, responsive layout, useful empty, loading, and error states, a small amount of purposeful motion — and polish must never imply precision the model did not claim. Simple polling; push infrastructure is deferred unless polling proves inadequate. A degraded source gets a prominent indicator while the page stays usable. Restarting the dashboard changes no workflow state and loses no history, because the history lives in the durable records, not in the page.

The dashboard is a projection, never an engine: it owns no workflow, conversation, provider, or configuration state, and offers no write of any kind. This is the boundary [the v1 non-goals] now record, and this design binds to it.

## Web security *(established here, as the repository's first web-service conventions)*

Bind to loopback; loopback alone is insufficient — a high-entropy session or bearer token is required, presented in a header or cookie and never in a URL or a log; Host and Origin are validated; the content-security policy is restrictive and CDN-free; all user-, model-, repository-, and Slack-supplied text is untrusted and escaped, so work-item text renders as text and cannot execute; missing or invalid authorization fails closed. No secret, credential, or private provider identifier appears in the read model or the page — the account *alias* is exactly what makes that possible.

## Process shape

A standalone dashboard command is the V1 shape. It reads the durable service, exposes health and readiness, and is adoptable by the later supervisor for start, stop, and observation without redesign; it remains useful afterwards for inspection and recovery, and never becomes a second control plane. Browser availability is never a prerequisite for harness operation.

## Deferred, deliberately

Deep drilldowns; rich timelines and custom historical windows; cost analysis beyond the capacity panel; readiness and conflict visualizations; push infrastructure; multi-account pool controls; account-fallback history, which becomes meaningful with multi-account pooling; automatic lifecycle ownership by the supervisor.
