---
id: writing-goals
kind: non-goals
title: Writing a goal
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: product-manager
      at: 2026-09-04T00:46:19Z
      reason: yoyodyne-ifd.262 - the qualities of a good goal move out of the goals directory index, which is ungoverned by design, into a governed companion filed beside the goals; the prose is unchanged from the index it left, and neither approved goals document is touched
---

# Writing a goal

What each goal has to be, for whoever is writing one and for whoever is reading
the goals to see whether one holds up. It is filed beside the goals and states
none of its own: [the v1 goals](v1-goals.md) and
[the operations goals](operations-goals.md) state the outcomes the product is
trying to reach, and this states what a statement filed there has to be to be
one of them.

The shape a goals *document* must carry — the `Goals` heading, the frontmatter,
the *Supports* trailer resolved against the brief — is
[the artifact contract](../../designs/artifact-contract.md)'s, which is the
normative home for those rules and is not restated here. This document is the
other half of the same question: the contract says what the harness checks of
the file, and this says what makes the sentence inside it a goal.

It is recorded as `non-goals` because that is the only kind the contract has for
a product manager's document in this directory that states no goals of its own.
The kind undersells it, and it moves when the contract grows one for guidance.

## What a goal looks like

Each goal states one outcome the product is trying to reach, and should be:

- **traceable upstream** — it names how it supports the brief. A goal that
  supports nothing in the brief is an orphan.
- **specific enough to design against** — an architect should be able to turn it
  into a design without first having to ask what was meant. Intent that is still
  ambiguous is a question for the operator, not a goal.
- **an outcome, not an implementation** — what should become true, and why. How
  it gets built belongs to designs and specifications.
