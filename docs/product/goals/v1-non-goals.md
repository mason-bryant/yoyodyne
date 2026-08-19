---
id: v1-non-goals
kind: non-goals
title: V1 non-goals
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: product-manager
      at: 2026-08-17T00:00:00Z
      reason: identity added with the artifact metadata schema; the prose is unchanged
    - action: amended
      by: product-manager
      at: 2026-08-19T04:19:30Z
      reason: the first non-goal narrowed to the hosted control plane alone - multiple humans against one shared repository became bounded scope under the team-mode epic, and permissions between users stay out of v1 unless its design requires them; drafted by the product manager, approved by the operator
approvals:
    - revision: 1
      by: operator
      at: 2026-08-19T04:19:30Z
      reason: 'Approved by the operator in conversation on 2026-08-18, "Both Approved": the non-goals stop contradicting the team-mode epic the operator admitted (yoyodyne-ifd.82).'
---

# V1 non-goals

These are the things Yoyodyne's first version deliberately does not do. They
bound [the v1 goals](v1-goals.md): a non-goal is a decision about where v1 stops
rather than a gap waiting to be closed, and
[what is deferred beyond v1](../../designs/v1-harness-design.md#deferred-beyond-v1) says
which of them are expected to arrive later. They were agreed as part of the v1
design and stated in [the v1 harness design](../../designs/v1-harness-design.md) until
they were moved here alongside the goals they bound; the wording below is
unchanged from that document.

Amended 2026-08-18 by the operator. The first non-goal originally read "Multiple
human users, permissions between users, or a hosted control plane." Multiple
humans running Yoyodyne against one shared repository is now bounded scope under
the team-mode epic rather than a non-goal; permissions between users stay out of
v1 unless the team-mode design requires them; a hosted control plane remains
out. This is the one exception to the statement above that the wording below is
unchanged from the design document.

## Non-goals

- A hosted control plane.
- Remote agent execution in v1.
- Multiple active products or repositories in one v1 harness instance.
- Complete behavioral parity between Claude Code and Codex.
- Direct model API integration when the local coding-agent CLIs provide the required execution interface.
- A general-purpose chat application independent of software delivery.
- Replacing Git, Beads, or the coding agents' native tool execution.
- Native integration with any language's build system, test runner, or package manager. Language support means running the commands a project declares, not understanding its toolchain.
- Fully unattended operation. The human still approves the brief and goals and answers what the product manager escalates; autonomy is the absence of routine per-change gates, not the absence of a human.
