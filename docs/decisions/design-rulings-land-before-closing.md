---
id: design-rulings-land-before-closing
kind: decision
title: An item that asked a design question does not close before the revision exists
status: active
revisions:
    - action: created
      by: architect
      at: 2026-09-05T17:40:00Z
      reason: recorded for yoyodyne-ifd.280, with two costed instances behind it
---

# An item that asked a design question does not close before the revision exists

**Decision.** A work item whose deliverable is a design ruling closes only when the revision exists in the governed document. The ruling stated in a conversation or a tracker note is a decision *made*, not a decision *delivered*: a developer run cannot open the tracker and a reviewer is shown only the diff, so a ruling that stops there is invisible to both audiences by construction. The closing condition is the landed revision, verified the way any revision is — in the document, in its log.

**Why now.** Two costed instances: yoyodyne-ifd.100.2 closed with its ruling in tracker notes against its own done condition, and approved amendment edbbd603 never reached its document — together roughly five runs and six review rounds spent re-deriving questions settled a week earlier.

**Consequences.** The architect's ruling turn states the revision text; the item stays open until the operator lands it; landing closes it. This binds the closing practice of every conversation-executed design item, including the architect's own — both instances were mine.
