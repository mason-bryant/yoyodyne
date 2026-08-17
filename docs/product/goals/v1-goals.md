# V1 goals

These are the outcomes Yoyodyne's first version is built to reach: what has to
become true for a harness to carry a product brief through goals, designs, work,
reviewed changes, and an integrated codebase. Each one names the goal in
[the product brief](../brief.md) that it supports, which is what
[design invariant 1](../../v1-harness-design.md#design-invariants) requires. That
link is prose, and nothing in the harness checks it yet; artifact governance in
milestone 2 is what will. Until then a reader has to check that it holds.

What v1 deliberately does not do is stated separately in
[the v1 non-goals](v1-non-goals.md).

Eight of these goals were agreed as part of the v1 design and stated in
[the v1 harness design](../../v1-harness-design.md) until they were moved here;
their wording is unchanged from that document, and what has changed is that each
now names its link upstream. Two are new, and both were added when the brief was
written and the backlog was checked against it. The goal on independent review
was added because the brief requires that nothing lands unreviewed by someone
other than its author, and no v1 goal reached that. The goal on cost was added
because tracked work on reporting what the harness spends traced to no goal at
all.

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
- The operator can see what the harness spends on their behalf: provider-reported cost, per work item, per run, and in total.
  *Supports: the operator can see what the system does on their behalf.*
- Support development in any language. Yoyodyne is written in Go, but the projects it manages are not assumed to be: verification is whatever commands the project declares, and no language, build system, or test framework is built into the harness.
  *Supports: it works on other people's projects.*
