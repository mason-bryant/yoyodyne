# Diagnosis: ifd.209.14's conversion has no subject in the tree

Work item: `yoyodyne-ifd.209.14`, "Convert the coordination slice to the workflow
runtime at the management-requests conversion". It is the epic-owned anchor the
ifd.209 trade decision requires: ifd.142's coordination slice proceeds bespoke,
and this item is the record that the conversion is owed.

Tree read at 2026-09-04, on the branch this document is committed with. Every
`file:line` below is from that tree and was read directly; every command quoted
was run in this worktree.

**This run delivers no conversion, and that is the finding rather than an
omission.** The item names three deliverables — re-express the coordination
slice's workflow on the declarative runtime, preserve its behavior against the
evidence that pins it, and retire the bespoke code on the same landing. All three
take the same subject, and that subject is not in this repository. The anchor
stays open.

## Verdict

| Precondition the item names | Met? |
|---|---|
| The declarative runtime exists | **Yes** — ifd.209.1 through 209.8 are closed; `internal/workflow` is present and `.yoyodyne/workflows/delivery.yaml` is the default path |
| The management-conversion design has landed | **No** — no such document exists, and the conversion before it in the order has not happened either |
| The coordination slice is in the tree, to be converted | **No** — it is on an unmerged branch |
| The bespoke code is in the tree, to be retired | **No** — same branch; there is nothing here to retire |
| Evidence exists pinning the slice's behavior | **No** — it never landed, so nothing in this tree exercises it |
| The runtime could carry a management workflow today | **No** — no durable wait, and no management actions registered |

## The coordination slice is not here

```
$ git ls-tree -r --name-only HEAD | grep -i supervision
docs/designs/management-and-supervision.md
```

The design, and nothing else. On ifd.142's own branch the implementation is all
there:

```
$ git ls-tree -r --name-only origin/yoyodyne/yoyodyne-ifd-142/da758368 | grep -i supervision
docs/designs/management-and-supervision.md
internal/orchestrator/supervision.go
internal/orchestrator/supervision_test.go
internal/runstate/supervision.go
internal/runstate/supervision_test.go
internal/supervision/readiness.go
internal/supervision/readiness_test.go
internal/supervision/request.go
internal/supervision/request_test.go
internal/supervision/survey.go
internal/supervision/survey_test.go
```

and `git merge-base --is-ancestor origin/yoyodyne/yoyodyne-ifd-142/da758368 origin/main`
reports that branch is **not** an ancestor of `main`. It is PR #302: ifd.142's
tracker record carries `Review decision: approve` and `Pull request merged: false`,
and the item's status is `blocked`.

So the workflow this item exists to convert has never been executed by anything
in this tree. There is no behavior here to preserve, no evidence pinning it, and
no bespoke implementation to retire.

## ifd.142's recorded blocker no longer holds

Worth separating from the above, because it is the thing that can actually be
acted on. ifd.142's notes record the stop as a divergence:

> main on origin is at 1550799d258bb987409f53ea4ed693bbf75f16ed, which does not
> contain the local main at 0377ea504440df81d3c0bd5dc6d438e33349e7ef; only a
> person can say which history is right

`git merge-base --is-ancestor` now reports **both** commits as ancestors of
`origin/main`. The divergence has been reconciled since, by whatever route. The
item is nonetheless still `blocked` and its approved branch is still unmerged,
so the recovery in [operations](../operations.md) appears to have been carried
out without the item being taken off the blocker it recorded.

## The design precondition is unmet, and not narrowly

[The configurable-workflows design](../designs/configurable-workflows.md) fixes
the conversion order, and management requests are third:

> Delivery first … Triage second, preserving the docket's caps and authority.
> Management requests third: **ifd.142 proceeds bespoke, per the operator's
> recorded trade, and converts here** …

Delivery has converted. Triage has not: `internal/orchestrator/triage.go` is
still bespoke Go, and `.yoyodyne/workflows/` holds `delivery.yaml` alone. No
management-conversion design document exists. This item's own description says it
is "not decomposable further until the runtime exists and the
management-conversion design lands"; the first half is now true and the second is
not.

## What a conversion would need from the runtime

Even with ifd.142's code in the tree, the runtime cannot yet express a
management-requests workflow. Two gaps, both in trusted Go rather than in
configuration, so neither is something a definition could work around:

- **No durable wait.** `internal/workflow/definition.go:55` reserves `$wait` as
  "the durable wait a definition may eventually transition into but never
  define", and `internal/workflow/validate.go:234` refuses a state or terminal
  whose name begins with the reserved prefix. `internal/workflow/execute.go`
  contains no wait path at all. A management-request workflow is durable waits:
  a request is recorded, a role is invoked by the harness, and the instance waits
  for the answer.
- **No management actions.** `internal/orchestrator/actions.go` registers eight
  actions — `work-item.claim` (line 65), `candidate.develop` (82),
  `candidate.publish` (112), `candidate.check` (129), `candidate.review` (143),
  `candidate.integrate` (174), `run.complete` (197), `run.clean-up` (221). The
  registry is delivery-only; nothing expresses a typed inter-role request. Under
  `configuration-never-grants-authority` a workflow definition cannot supply one,
  which is the invariant working as intended.

## Why this run wrote no workflow anyway

Authoring a management workflow from here would be a third implementation of it —
after ifd.142's bespoke one and before the converted one this item anticipates —
written against a design that has not landed, for code no one working in this
tree can read, needing two runtime features that do not exist. It would satisfy
neither of the item's two acceptance conditions, because there is nothing to
preserve behavior against and nothing to retire, and it would diverge from PR
\#302 the moment that lands. The item was created so the conversion is not
forgotten; closing it against a speculative rewrite would lose exactly what it
was protecting.

## Limitations of this reading

- **The tracker was read, not queried.** `bd` cannot run in a developer run, so
  every tracker fact above comes from `.beads/issues.jsonl` in this worktree —
  the harness's copy, current as of when this run was cut. An item admitted or
  changed since is not in it.
- **ifd.142's branch was inspected, not built.** Its files were listed with
  `git ls-tree`; nothing from it was checked out, compiled, or run. Whether it
  still builds against today's `main` is not established here.
- **Why the divergence resolved is not established.** Both commits are ancestors
  of `origin/main` now; by what act, and whether ifd.142's blocker was reviewed
  at the time, is not readable from the tree.

## Work discovered, for the product manager to admit

- **Take ifd.142 off a blocker that no longer holds.** Its recorded divergence is
  reconciled, its branch is reviewer-approved and unmerged, and it is the direct
  prerequisite for this item. Until it lands, ifd.209.14 cannot start.
- **Implement the durable wait `$wait` in the workflow runtime.** Reserved today
  and unimplemented; a prerequisite for any management conversion, and probably
  for the triage conversion's docket waits too.
- **Register the management-request actions**, so the inter-role request
  vocabulary exists in trusted Go before a definition tries to name it.
- **The triage conversion**, which the design sequences ahead of this one and for
  which no tracker item was found in this run's copy of the export.
- **Nothing marks an anchor item as not-yet-activatable.** ifd.209.14 states its
  own activation conditions in prose and was pulled into a developer run twice
  regardless, each time spending a run to rediscover that its subject is absent.
  Whatever selects work has no way to read "not decomposable further until X" as
  "not ready", and other second-pass anchors will be pulled the same way.
