---
id: staleness-is-derived-not-marked
kind: decision
title: Staleness is derived, never marked
supports:
    - v1-harness-design
status: active
revisions:
    - action: created
      by: architect
      at: 2026-08-19T00:00:00Z
      reason: recorded while deciding amendment-5c856a72, which found the design promising a stored mark that was never built; the derived form is the better design and this record is what binds later work to it
---

# Staleness is derived, never marked

What a change upstream leaves unanswered downstream is computed, every time it
is asked for, from the artifacts' own revision logs and the tracker's admission
times, and reported by `yoyo stale`. Nothing writes a staleness flag anywhere,
and nothing may.

## Decision

There is no stored mark to set and none to clear. An artifact stops being
reported when its owner records a later revision that answers the change; a
work item stops when it closes. The report stops, closes, blocks and reorders
nothing, and it reads a hand edit exactly as it reads an amendment made
through the harness.

## Why

A stored mark is a second account of the same fact, and two accounts can
disagree. The revision logs are already the truth about what changed and when;
deriving staleness from them means the report cannot drift from the documents
it describes. The tempting conveniences — a "mark reviewed" button, a
bulk-clear after a big amendment — are exactly the operations that would let
the record say "answered" while the documents say otherwise.

This also sets the boundary with directives: a directive that changes an
upstream artifact pauses affected work, because a directive is intent arriving
with authority; an artifact edited directly pauses nothing, because a harness
that paused the queue on every wording edit would teach an operator not to
edit. The derived report is what covers that gap — quietly, and without
stopping anyone.

## What this binds

Anything that later wants to "clear" staleness must do it the only honest way:
by the owning role recording the answering revision, or by the work item
closing. A feature that clears it any other way is building the disagreement
this decision exists to prevent, and should be refused at design time.
