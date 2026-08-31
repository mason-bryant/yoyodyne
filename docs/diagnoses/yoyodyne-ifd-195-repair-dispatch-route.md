# yoyodyne-ifd.195: which dispatcher started the runs that stood in for a repair

The class ifd.178 was filed for is a repair round that reaches a developer as a
fresh worktree off the target branch instead of the change it was decided about.
ifd.178 fixed the landing point — a run refuses to hand a developer a worktree
that lost its change, and `yoyo run` refuses a fresh run of an item whose last
run stopped owing a repair. This is the answer to the question ifd.195 asks:
**what actually dispatched those runs.** It is written from the durable run
records rather than from memory, and every row below is checkable in them.

## Method

For every work item, its recorded runs in start order. A run counts as *owed a
repair* where its own record says so: terminal status, a blocker still standing,
a failure returned to its developer (review findings, a failing check, or refused
paths), and a branch that was never removed — the same four facts
`owedARepair` reads. A later run of the same item, in a different worktree, is a
dispatch that stood in for the repair that run was owed. Who dispatched it is the
`selection.by` the run recorded when it was reserved.

## What the records say

| fresh run started | work item | run owed a repair | fresh run | dispatched by |
| --- | --- | --- | --- | --- |
| 2026-08-20 01:10 | yoyodyne-ifd.113 | 16d9603a | 813384f1 | operator |
| 2026-08-20 08:00 | yoyodyne-ifd.120 | 30f1562a | 0193cafd | operator |
| 2026-08-20 08:19 | yoyodyne-ifd.121 | 9f79dcee | 6ff896ba | operator |
| 2026-08-20 19:17 | yoyodyne-ifd.125.1 | 4d239c1c | 96db9d50 | scheduler — *claimed re-run* |
| 2026-08-20 21:57 | yoyodyne-ifd.121.3 | 15bbe42e | e43d8421 | scheduler |
| 2026-08-23 03:07 | yoyodyne-ifd.68.13 | effbd604 | d9f2ed65 | scheduler — *claimed re-run* |
| 2026-08-23 18:55 | yoyodyne-ifd.150 | b9967f5e | dfaa9fd1 | scheduler |
| 2026-08-23 20:09 | yoyodyne-ifd.156 | f921f10c | 3d0856a5 | scheduler |
| 2026-08-23 20:29 | yoyodyne-ifd.68.19 | e46dad52 | 649134f8 | scheduler |
| 2026-08-24 00:36 | yoyodyne-ifd.177 | 8142bde5 | b64b2c41 | scheduler |
| 2026-08-24 00:36 | yoyodyne-ifd.68.20 | 031981f8 | 68a2632e | scheduler |
| 2026-08-24 01:11 | yoyodyne-ifd.125.5 | da245c48 | 051c5ca1 | scheduler |
| 2026-08-24 01:16 | yoyodyne-ifd.153 | 5035c832 | 81f90d0d | scheduler |
| 2026-08-24 01:43 | yoyodyne-ifd.149 | e0a6b4e0 | 255d5fe2 | scheduler |
| 2026-08-24 02:06 | yoyodyne-ifd.151 | dda73df4 | 562408c7 | scheduler |
| 2026-08-27 17:50 | yoyodyne-ifd.180 | 5a2c061c | e282197f | scheduler |
| 2026-08-27 18:00 | yoyodyne-ifd.182 | 121c58f8 | 1055b344 | scheduler |
| 2026-08-30 05:54 | yoyodyne-ifd.143 | 1fdb5a31 | fe0ad846 | scheduler |
| 2026-08-30 05:54 | yoyodyne-ifd.68.22 | cc986572 | e75bc2ea | scheduler |

Two of the nineteen are not substitutions: the runs of ifd.125.1 and ifd.68.13
were claimed re-runs, recorded in the re-run store before they started, which is
the development manager deciding the ground moved. That leaves **seventeen
substituted dispatches: fourteen by the scheduler pulling the item off the
backlog, three by the operator naming the item with `yoyo run`, and none by
`yoyo triage repair`.**

That confirms the code-path reading ifd.178's developer reported. The repair
carry-out re-enters the stopped run through adoption and never creates a
worktree; the fresh worktrees with new suffixes off current main could only come
from the fresh path, and the runs that took it say in their own records who sent
them. The repair intent was real in every case — the development manager had
granted one, and for ifd.143 the item itself said it was being handed back "for
one bounded repair of the change it already has" — but it was carried by the
work item's prose rather than by the dispatch, and the item being put back to
open is all the scheduler needs to pull it.

## Which code path each dispatcher takes

Naming the dispatcher is only half of it; what matters for the defense is the
call each one makes. There are four call sites that start work on a work item,
and every one of them calls `Pipeline.Run`, which is where ifd.178's
`refuseSubstitutedHandback` sits:

| dispatcher | call site | dispatches |
| --- | --- | --- |
| scheduler (`yoyo watch`, `yoyo schedule`) | `openPull` in `internal/cli/schedule.go`, the `Start` it builds | `pipeline.Run` |
| operator by name (`yoyo run <id>`) | `internal/cli/run.go` | `pipeline.Run` |
| a conversation running an item | `conversationWork.Run` in `internal/cli/work.go` | `pipeline.Run` |
| triage re-run (`yoyo triage rerun`) | `buildRerunner` in `internal/cli/triage.go` | `pipeline.Run` |

There is one `orchestrator.Scheduler` in the harness and one `Pull` behind it,
so there is no lower entry point for the scheduler to reach past the refusal:
`Pull.Start` is a `Starter`, the only `Starter` the command builds is the one
above, and `Reserve` and `Worktrees.Create` are reached from inside `Run` after
the refusal has been asked. `TestTheSchedulersDispatchIsRefusedWhereARepairIsOwed`
drives that whole route — a real `Scheduler`, a real `Pull`, a real pipeline,
and an item whose last run stopped owing a repair — and asserts the pull is
turned away with nothing reserved and no worktree cut. The fifth call site is
the repair carry-out, which ifd.195 moved off `Run` and onto `Pipeline.Continue`.

## What ifd.195 changed

The carry-out now names the run it re-enters and dispatches it to
`Pipeline.Continue`, which adopts that run or refuses. It reserves nothing,
claims nothing, and creates no worktree on any path, so repair intent can no
longer arrive as a fresh run. The refusal in `yoyo run` stays where ifd.178 put
it, as the backstop for the two routes above, which never carried repair intent
in the first place.

## What is still open

Four of the substitutions — ifd.180 and ifd.182 on 2026-08-27, ifd.143 and
ifd.68.22 on 2026-08-30 — were dispatched after ifd.178's refusal reached main
(merged 2026-08-27 09:47). Each of the four owed runs satisfies every condition
that refusal tests.

The explanation that had to be ruled out first is the one inside this
repository: that the scheduler dispatches past the refusal rather than through
it, in which case fourteen of the seventeen losses had no defense at all and
these four need nothing else to explain them. It does not. The table above
traces the scheduler's dispatch to `pipeline.Run`, the refusal is inside that
function ahead of every reservation, and the route is now driven end to end by a
test. So the code on main does refuse this, and the code that dispatched those
four cannot have been it.

What is left is the build. The run records could not say which one the scheduler
process was running: `watch.jsonl` only began stamping a `build` on 2026-08-30
11:15, after all four, and nothing else carried one at all. A resident older than
the fix is the same class `docs/work.md` already names as an environmental
refusal — "the build that dispatched it predated the decision it was carrying
out" — and these four cannot now be settled, because the evidence was never
written.

**They are the last four that could not be.** ifd.213 put the revision the
reserving harness was built from onto every run record, beside the account and
the configuration it already carried, and onto every line of the cost log, every
conversation record, and every round of an inter-role exchange. A dispatch made
after that says in its own record which binary made it, so the same question
asked of a later incident is answered from the records rather than abandoned
here. What is still not automatic is the comparison at dispatch — a run reserved
by a build older than the decision it is carrying out still proceeds, and
`docs/work.md` names that as the half of the environmental cause that remains.

One shape of the loss is out of reach of both refusals: ifd.68.22's repair
target was a run of a *different* work item (ifd.68.20), named in the item's
prose. Neither the dispatch nor the refusal can see an intent recorded only
there.
