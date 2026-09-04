# Diagnosis: the ifd.121 double-run happened before any guard existed

Work item: `yoyodyne-ifd.273`, admitted after the developer and reviewer on
`yoyodyne-ifd.259`'s run found that bd 1.1.2 populates the `parent` field on
every item — contradicting the cause `yoyodyne-ifd.256` recorded for the
`yoyodyne-ifd.121` / `yoyodyne-ifd.121.2` double-run, which was that "the
parentage the guard keyed on was never populated".

Tree read at 2026-09-04, on the branch this document is committed with. Every
`file:line` and every commit below was read directly from this worktree. The two
runs' durable state was read from the machine's product state directory, which is
outside the worktree and is named where it is used.

## Verdict

| Claim | Holds? |
|---|---|
| The scheduler reads `bd list`/`ready`/`show --json`, not `.beads/issues.jsonl` | **Yes** — confirmed against the code |
| bd 1.1.2's `--json` populates the `parent` field | **Yes** — and on every read path a pull makes |
| bd gained the field between 2026-08-20 and 1.1.2 | **No** — the same 1.1.2 binary was installed 25 days before the incident |
| A version floor is owed | **No** — nothing changed in that window; the behaviour is pinned by a check instead |
| ifd.256's recorded cause ("the parentage the guard keyed on was never populated") | **No** — false in both halves |
| A prior document in `docs/diagnoses` records that cause | **No** — the directory carries none for ifd.121 or ifd.256; see [What docs/diagnoses already held](#what-docsdiagnoses-already-held) |
| The epic-coverage guard existed when the double-run happened | **No** — it landed 12h43m after the second run started |
| The shipped edge-keyed guard would prevent the pull that was actually observed | **Yes** — three separate ways, now pinned by a test |
| A mechanism from that incident is still live | **No** |
| An unrelated mechanism is live in the same code | **Yes** — see [What is live](#what-is-live-and-is-not-this-incident) |

## What actually happened

Both runs are in the product state directory at
`~/Library/Application Support/Yoyodyne/state/products/yoyodyne/runs/`, and each
carries the scheduler's own recorded selection reason:

| Run | Item | Started | Ended | Base commit | Selected by |
|---|---|---|---|---|---|
| `run-44940ec3d71cdbc6e47e39745fedf15f` | `yoyodyne-ifd.121.2` | 2026-08-20T06:05:34Z | 06:47:15Z | `3e08960` | scheduler, "position 1 of 48" |
| `run-9f79dceee7b0761059dcea4589eb502e` | `yoyodyne-ifd.121` | 2026-08-20T06:25:43Z | 07:02:08Z | `3e08960` | scheduler, "position 2 of 48" |

So the child was pulled first and the epic twenty minutes later, at a second
pull, while the child's run was still going — a 21½-minute overlap. Both runs
failed. Neither recorded reason mentions coverage, a container, or a deferral,
because at their base commit there was nothing to mention.

**The base commit carried no guard of any kind.** `3e08960` is
`yoyodyne-ifd.121.1`, committed 2026-08-20T09:02:57+03:00. At that commit:

- `internal/orchestrator/schedule.go` has no `children` map, no `claimedStatus`,
  and no `coveredReason` — the epic-coverage guard is entirely absent. Its one
  use of `item.Parent` is inside `fingerprint`, which hashes an item to notice it
  changed.
- `internal/orchestrator/conflict.go` **does not exist**, so there is no
  same-epic sequencing either.

The only thing standing between the two pulls was `occupied`, which keys on the
exact work-item identifier and so cannot tell a parent from its child.

**The guard landed afterwards, as the fix for this.** `09b32ea`,
`yoyodyne-ifd.123`, "Scheduler selection guard: an epic with open execution
children is not itself pulled as developer work", committed
2026-08-20T22:08:43+03:00 — **12 hours 43 minutes after `run-9f79dcee` started**.
The sibling sequencing followed in `79cf794` (`yoyodyne-ifd.133`) at 2026-08-21
01:57+03:00. Code that did not exist cannot have failed.

## Where the recorded cause came from, and why it is wrong

`yoyodyne-ifd.256` (`cdf4dfb`, 2026-09-03) widened the coverage reading from the
parent field to `WorkItem.DecomposedFrom`, which consults the field and the
`parent-child` edge. **The widening is right and stands.** Its stated reason is
not. ifd.256 touched four comments, and **three of them assert the cause** —
`internal/beads/client.go`, `internal/orchestrator/schedule.go`, and
`internal/orchestrator/schedule_test.go`. The fullest of the three, as ifd.256
wrote it into `client.go`, read:

> This project's own tracker uses only the second — not one of its items carries
> the field […] That is what let the scheduler start yoyodyne-ifd.121 and the
> child carrying its execution as two developer runs of one scope: the guard
> against exactly that was already there, and the parentage it keys on was never
> populated.

Both halves are false:

1. **"The guard was already there"** — it was not, per the timings above. This is
   the load-bearing error: it reads the fix for an incident as the thing that
   failed at it.
2. **"The parentage it keys on was never populated"** — `yoyodyne-ifd.259`
   captured this project's own tracker through bd 1.1.2 and found the field
   populated on both items
   (`internal/beads/testdata/bd-list-parent-child.json`: the child carries
   `"parent": "yoyodyne-ifd.121"`, the epic `"parent": "yoyodyne-ifd"`). ifd.259
   corrected two of the three — `client.go` and `schedule.go` — and left the
   first half of the claim standing in `client.go` and the whole of it in
   `schedule_test.go`. `yoyodyne-ifd.267` then restated it in a comment of its
   own, in `internal/beads/conformance_test.go`. **This change corrects the three
   that still carried it**, which is every statement of the cause left in the
   tree.

**The one comment ifd.256 touched that asserts neither half is `conflict.go`'s**,
which is why it is not corrected here. `inFlight.epics`
(`internal/orchestrator/conflict.go:61-68`) says only:

> The parent read here is the one the tracker states as a field, and
> deliberately not the wider reading `beads.WorkItem.DecomposedFrom` does. A
> tracker that hangs its whole backlog off one root epic states that the wider
> way too, and holding every item back behind whichever child of the root is
> already running is serializing the queue rather than declining one race […]
> Widening it wants a container epic told from a decomposed one first, and that
> question is not answered here.

That names no guard and makes no claim about whether the field is populated: it
is a design rationale for keeping one reading narrow, not an account of the
double-run. Nothing anywhere else in `conflict.go` states the cause either. Its
rationale is wrong for a different reason, which is `yoyodyne-ifd.261`'s scope
and is set out under [What is live](#what-is-live-and-is-not-this-incident)
rather than fixed here.

What is true, and what the widened reading is genuinely for, is narrower: the
tracker's **export**, `.beads/issues.jsonl`, states parentage only as an edge and
carries the `parent` key on no item in it. The scheduler does not read the
export — `Pull.queue` (`internal/orchestrator/schedule.go:1841`) calls
`Tracker.List` and `Tracker.Ready`, which are `bd list --json` and
`bd ready --json` (`internal/beads/client.go:321,345`) — so this never bore on
the incident. It bears on any reader handed a store that states parentage one way
and not the other, which is a real thing for a client to be robust to and not a
thing that has happened here.

## What docs/diagnoses already held

ifd.273 asks for the diagnoses of ifd.121 and ifd.256 to be corrected or
confirmed, so the directory was swept rather than assumed. **Neither item has a
diagnosis.** `docs/diagnoses/` held twelve documents before this one, none named
for either item, and the recorded cause this document disproves never lived in
any of them — it lived in the code comments named above, and nowhere else that a
sweep of this directory or of `docs/` finds.

Three existing documents mention `yoyodyne-ifd.121` in passing. Each was read,
none states or depends on a cause for the double-run, and **all three are
confirmed unchanged**:

- `yoyodyne-ifd-195-repair-dispatch-route.md:27` records
  `run-9f79dcee` — the epic's run above — as a run owed a repair, with
  `run-6ff896ba` dispatched in its place by the **operator** rather than the
  scheduler. That is about who dispatches a repair, and it corroborates the
  timeline here rather than bearing on it: the epic's *second* run that day was a
  hand-dispatched repair of the first, not a third scheduler pull.
- `yoyodyne-ifd-122-goal-attribution-loss.md:213-214` lists `yoyodyne-ifd.121`
  and two of its children among the items whose goal attributions were destroyed
  by `bd update --notes`. Unrelated writer, unrelated failure.
- `yoyodyne-ifd-206-coined-terms-sweep.md:202` cites `ifd.121.6` as the single
  use of one coined term. Unrelated.

So this document is the first diagnosis of the incident, and there is no second
one in the directory for it to contradict.

## Whether bd gained the field after the incident

**It did not.** The bd on this machine is a single binary, `/usr/local/bin/bd`,
reporting `bd version 1.1.2`, with both mtime and birth time
**2026-07-26T21:13:38+03:00** — twenty-five days before the double-run, and never
replaced since. Nothing on `PATH` shadows it. So the version that answered the
scheduler's reads on 2026-08-20 is the version that answers them today, and the
field question cannot distinguish the two dates in either direction.

That makes a version floor moot for this incident, so none is recorded. What is
recorded instead is the behaviour itself, in the live smoke check where the rest
of bd's assumed behaviour is pinned: `TestParentFieldConformance`
(`internal/beads/conformance_test.go`) asserts that `List`, `Ready`, and `Show`
each carry the `parent` field on a decomposed item and leave it empty on a
container. It is there for the direction that is still open — a later bd that
drops the field — because one reading in the harness rests on the field alone and
would go inert silently if it did.

## What the shipped guard does with the pull that was observed

`TestSchedulerLeavesTheEpicWhoseClaimedChildIsTheOneThe121DoubleRunStarted`
(`internal/orchestrator/schedule_test.go`) reconstructs the second pull: the epic
open and pullable, the child claimed with a run in flight, parentage stated only
as an edge, and the epic carrying its own `parent-child` edge pointing up at the
root epic. The epic is deferred and nothing is started. Three independent
readings would each have caught it:

- coverage reads the claimed slice as well as the queue
  (`schedule.go:1866`), so the in-flight child is visible;
- `DecomposedFrom` reads the edge as well as the field
  (`client.go:211`), so edge-only parentage is visible; and
- the field is populated anyway, per the conformance check above.

## What is live, and is not this incident

Reading this settled one thing and turned up another, in the same code and in the
opposite direction.

`inFlight.epics` (`internal/orchestrator/conflict.go:69`) keys sequencing on
`item.Parent` alone. Its comment argues that reading it wider would serialize the
queue, because "a tracker that hangs its whole backlog off one root epic states
that the wider way too". The premise the argument rests on is that the narrow
reading is narrow. It is not: bd populates the field, and in this project's own
tracker **276 of 399 items — 51 of the 75 currently unfinished — hang directly off
the root epic `yoyodyne-ifd`**. So the field reading finds the root epic on most
of the backlog, and the outcome the comment set out to avoid is the outcome it
produces: once any run is in flight on a root child, every other root child is
held back as racing over `yoyodyne-ifd`.

This is not a mechanism from the ifd.121 double-run — it holds work back rather
than starting it twice — and it is `yoyodyne-ifd.261`'s scope, which is open with
a branch that adds a `containers` set to exclude exactly this. It is recorded
here because ifd.261's own justification repeats ifd.256's false claim ("this
project's tracker […] carries the field on no item, so a reading that consulted
only the field […] left this map empty"). The fix is right; the reason given for
it is the inverse of the real one, and the difference matters to whoever reviews
whether `containers` is drawn in the right place.

## Limitations of this reading

- **The tracker store was not read.** A developer run's sandbox refuses it, per
  `CLAUDE.md`. Parentage figures above come from `.beads/issues.jsonl`, the
  harness-supplied export, which states the `parent-child` edges but carries no
  `parent` field; the field's live values come from ifd.259's capture and from
  the conformance check, which builds its own scratch store.
- **The harness binary that made the two pulls was not identified.** The
  `harness_commit` field on both run records holds that run's own last commit
  rather than the harness build, so it says nothing. It does not need to: the
  guard was written 12h43m after the second pull, and no binary can carry code
  that has not been written.
- **Why both runs failed** is not examined here. The item is about why two were
  started, not about what became of them.

## Work discovered, for the product manager to admit

- **`yoyodyne-ifd.261`'s stated rationale is the claim this item disproved.** Its
  change is sound and its `containers` set addresses a real live over-sequencing,
  but a reviewer checking it against its own reasoning is checking it against a
  false premise. Worth a correction to that branch's comments before it lands.
- **Nothing reconciles a recorded cause against the tree at the time.** ifd.256
  recorded a cause naming a guard, and no check compared that against whether the
  guard was in the tree on the date; it took two later items to notice. The dates
  were in Git and in the run state the whole time.
