# Team mode scope

Drafted by the product manager; approved by the operator in conversation on
2026-08-19. This document is deliberately plain prose for now: no governed
kind exists yet for a scoping document, and inventing one belongs to the
artifact-contract specification the architect owns (yoyodyne-ifd.87's
delivery), not to this file.

This bounds the team-mode epic: what Yoyodyne promises a team, who counts as an
operator when several humans hold intent, and what is supported before the
distributed design ships. The design child designs against this document. It
serves the goal "A team can run Yoyodyne against one shared repository:
collaborators each run their own harness without losing work, splitting the
tracker, or weakening any safety invariant."

## What team mode promises

- A collaborator with repository access and the readme can run their own yoyo
  against the shared repository with the same guarantees one operator gets
  today: no lost work, no two machines paying for the same item, no safety
  invariant narrowing to machine scope.
- One tracker. Every machine sees the same backlog, attributions, and reports;
  findings and control travel.
- The forge stays the integration authority. No agent pushes or merges,
  whoever's machine it runs on.

## What team mode does not promise

- No hosted control plane (non-goal, amended 2026-08-18).
- No permissions between users unless the identity design requires them.
  Collaborators are trusted peers: protecting work from races is in scope,
  protecting teammates from each other is not.
- No team chat surface. The Slack epic is the shared channel; this epic moves
  coordination state, not conversation.
- One product, one repository per harness instance, unchanged.

## Who counts as an operator

Intent has one owner. The brief, goals, non-goals, and their approvals belong to
one designated human — the product owner — not to every collaborator. Every
collaborator is an operator of their own machine: they start, pause, release,
and stop their own runs. The distinction exists because the coordination design
assumes intent that cannot conflict with itself; several humans amending goals
concurrently is conflict machinery nobody has designed, and peer approval would
multiply the recorded forgeability gap rather than close it.

Operator identity is designed once, jointly for this epic and for the approval
precondition recorded on the goal-level-approval work: the identity design
decides how a human is proven; this document decides what a proven human may do
— own intent (one) or run work (all).

Team-wide pause is a design question, not a first-cut promise; the pause switch
pauses the machine it is on.

## What v1 supports meanwhile

One harness plus ordinary committers is the supported team answer today, and
remains so until the distributed design ships: one person runs Yoyodyne,
teammates contribute through the forge, and the git layer already survives
multiple committers. Two harnesses against one repository is unsupported until
the recorded coordination gaps close, and the readme states that boundary.
