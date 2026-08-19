---
id: v1-goals
kind: goals
title: V1 goals
supports:
    - brief
status: active
revisions:
    - action: created
      by: product-manager
      at: 2026-08-17T00:00:00Z
      reason: identity added with the artifact metadata schema; the prose is unchanged
    - action: amended
      by: product-manager
      at: 2026-08-18T00:00:00Z
      reason: added the legibility goal, drafted by the product manager and approved by the operator verbatim; its statement was rejoined onto one line so the goal is recorded whole
    - action: amended
      by: product-manager
      at: 2026-08-18T00:00:00Z
      reason: added the adoption goal, drafted by the product manager and approved by the operator with one amendment - the readme alone, not the documentation, is what a newcomer needs; the intro now dates each new goal honestly
    - action: amended
      by: product-manager
      at: 2026-08-18T00:00:00Z
      reason: the introduction claimed nothing checks the link to the brief; milestone 2 delivered that checking, and the introduction now says so
approvals:
    - revision: 2
      by: operator
      at: 2026-08-19T00:43:34.381311Z
      reason: 'Approved by the operator in conversation: the v1 goals on 2026-08-17, and the legibility and adoption goals added on 2026-08-18, each approved as the revisions above record. Written down here by yoyodyne-ifd.1.8.'
---

# V1 goals

These are the outcomes Yoyodyne's first version is built to reach: what has to
become true for a harness to carry a product brief through goals, designs, work,
reviewed changes, and an integrated codebase. Each one names the goal in
[the product brief](../brief.md) that it supports, which is what
[design invariant 1](../../designs/v1-harness-design.md#design-invariants) requires. That
link is checked: artifact governance, delivered in milestone 2, resolves
each goal's *Supports* trailer against the brief mechanically, and a test
enforces it. A reader can still trace the link by hand; the harness no longer
depends on them to.

What v1 deliberately does not do is stated separately in
[the v1 non-goals](v1-non-goals.md).

Eight of these goals were agreed as part of the v1 design and stated in
[the v1 harness design](../../designs/v1-harness-design.md) until they were moved here;
their wording is unchanged from that document, and what has changed is that each
now names its link upstream. Four are new. The goal on independent review was
added when the brief was written and the backlog was checked against it, because
the brief requires that nothing lands unreviewed by someone other than its
author, and no v1 goal reached that. The goal on cost was added at the same
time, because tracked work on reporting what the harness spends traced to no
goal at all. The goal on legibility was added when the operator asked that clear
reading be a stated outcome rather than a habit. The goal on adoption was added
because tracked work on the install path traced to no goal that named a
newcomer.

Four entries that were not outcomes have left this list. Beads as the durable
workflow store, repository Markdown as the human-readable source of truth, and
Claude Code and Codex as the supported backends were architectural decisions
recorded here because there was nowhere else to record them; they are decisions,
and they live in the architect's decision records. Reaching a useful self-hosting
threshold before implementing the whole management hierarchy was a delivery
milestone rather than an outcome, it was reached, and it is recorded as such.

## Goals

- Maintain a traceable chain from the product brief through goals, designs, work, code changes, and verification.
  *Supports: every change traces to intent somebody approved.*
- Let configurable agent roles collaborate without allowing downstream agents to silently redefine upstream intent.
  *Supports: intent is only redefined by whoever owns it.*
- Make user directives durable, discoverable, and enforceable regardless of which agent received them.
  *Supports: intent is only redefined by whoever owns it.*
- Isolate implementation tasks in harness-managed Git worktrees and integrate successful work automatically.
  *Supports: intent goes in and merged software comes out.*
- Every change is reviewed against the intent it claims to serve, by a role that did not write it, before it is integrated.
  *Supports: nothing lands unreviewed by someone other than its author.*
- Publish that work as pull requests the harness opens, and has the forge merge, on the roles' behalf, for projects that enable it, without letting any agent push or merge.
  *Supports: safety invariants hold whatever the configuration says.*
- Keep roles, policies, and provider selection configurable without making safety invariants optional.
  *Supports: safety invariants hold whatever the configuration says.*
- Run development nearly autonomously. The human's routine interface is the product manager: they state intent, approve the brief and goals, and answer questions the product manager escalates. Directing the architect, development manager, developer, or reviewer individually is available for inspection, recovery, and override, but is not part of the normal loop.
  *Supports: the human's attention goes only where it is needed.*
- The harness's surfaces read clearly: boundaries between topics and speakers are visible, important findings stand out, and every distinction survives a terminal that cannot render emphasis.
  *Supports: the human's attention goes only where it is needed.*
- The operator can see what the harness spends on their behalf: provider-reported cost, per work item, per run, and in total.
  *Supports: the operator can see what the system does on their behalf.*
- Support development in any language. Yoyodyne is written in Go, but the projects it manages are not assumed to be: verification is whatever commands the project declares, and no language, build system, or test framework is built into the harness.
  *Supports: it works on other people's projects.*
- A newcomer can go from the documented install to a working first run on their own repository using the readme alone.
  *Supports: it works on other people's projects.*
