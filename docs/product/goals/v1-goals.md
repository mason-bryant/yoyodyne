# V1 goals

These are the outcomes Yoyodyne's first version is built to reach: what has to
become true for a local, single-operator harness to carry a product brief
through goals, designs, work, reviewed changes, and an integrated codebase.
They were agreed as part of the v1 design and stated in
[the v1 harness design](../../v1-harness-design.md) until they were moved here.
That was product intent living in an architect-owned document; this directory is
the product manager's home for it, and the wording below is unchanged from the
design document. What v1 deliberately does not do is stated separately in
[the v1 non-goals](v1-non-goals.md).

Each of these should name how it supports the brief, and none of them does yet:
the brief is still a stub, so the link upstream is not written down.
[Design invariant 1](../../v1-harness-design.md#design-invariants) is what
requires that link, and artifact governance in milestone 2 is what will check it.

## Goals

- Maintain a traceable chain from the product brief through goals, designs, work, code changes, and verification.
- Let configurable agent roles collaborate without allowing downstream agents to silently redefine upstream intent.
- Make user directives durable, discoverable, and enforceable regardless of which agent received them.
- Isolate implementation tasks in harness-managed Git worktrees and integrate successful work automatically.
- Publish that work as pull requests the harness opens, and has the forge merge, on the roles' behalf, for projects that enable it, without letting any agent push or merge.
- Use Beads as the durable workflow, dependency, blocker, directive, and handoff store.
- Use repository Markdown as the human-readable source of truth for the brief, goals, designs, specifications, decision records, and invariants.
- Support Claude Code as the default backend and Codex as an optional developer/reviewer backend.
- Reach a useful self-hosting threshold before implementing the entire v1 management hierarchy.
- Keep roles, policies, and provider selection configurable without making safety invariants optional.
- Run development nearly autonomously. The human's routine interface is the product manager: they state intent, approve the brief and goals, and answer questions the product manager escalates. Directing the architect, development manager, developer, or reviewer individually is available for inspection, recovery, and override, but is not part of the normal loop.
- Support development in any language. Yoyodyne is written in Go, but the projects it manages are not assumed to be: verification is whatever commands the project declares, and no language, build system, or test framework is built into the harness.
