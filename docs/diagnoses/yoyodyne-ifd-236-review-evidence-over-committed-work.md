# yoyodyne-ifd.236: the empty patch the reviewer read as lost evidence

A reviewer on run `run-7172270f0c41c127769ae72e01f1f509` — work item
yoyodyne-ifd.68.25, 2026-09-01 — was handed a clean worktree status and an empty
patch while the branch it was reviewing carried three commits naming that item.
It reported the shape it could see: if the developer had committed its work
rather than leaving it uncommitted, then the evidence assembly had captured only
the uncommitted half, and the verdict had judged a missing diff rather than the
change. ifd.236 was admitted on that report, priority 2, pending confirmation it
reproduces.

**It does not reproduce, because the assembly never collected the uncommitted
half.** The patch was empty because the change was empty: the run's third
developer attempt reverted its own work and said so. This is the record of how
that was established, written from the durable run record and the branch rather
than from memory.

## What the assembly actually collects

Every part of a work-item change is measured against the commit the worktree was
cut from, not against the index: `UnifiedChanges`, the status and diff stat
`summarize` renders, and the `ChangedPaths` listing the protected-path gate
decides on all run `git diff <base commit>` inside the worktree, which spans
committed and uncommitted work alike. It has been that way since
`b3183dc` (yoyodyne-ifd.29, 2026-08-16) put publishing in front of the checks and
made every attempt a commit; the comments on `summarize` and `ChangedPaths` say
so, and `TestManagerChangedPathsSeesWhatAnAttemptAlreadyCommitted` held the
listing half of it.

A developer that commits work of its own does not reach the assembly at all:
`verifyOwnedHead` permits exactly the base commit or the harness commit durable
state recorded, so an agent's commit fails the run loudly rather than producing a
short patch.

## What the record says about the run

The run's own events give the patch each review round was assembled from:

| review round | event | patch bytes | verdict |
| --- | --- | --- | --- |
| 1 | seq 767 | 17,028 | repair, 3 findings |
| 2 | seq 1273 | 18,829 | repair, 3 findings |
| 3 | seq 1635 | 0 | repair, 2 findings |

The first two rounds prove the assembly reads committed work: the run publishes,
so by the time each review ran the attempt it judged was already a commit on the
branch, and the patch carried it.

The third round is the reported shape, and the branch explains it. Base
`8d4448d`, three commits, head `38bb0a7`:

| commit | effect |
| --- | --- |
| `0927704` | 9 files, +215 −26 |
| `f929c61` | 3 files, +57 −23 |
| `38bb0a7` | the same 9 files, +26 −249 |

`git diff 8d4448d 38bb0a7` is empty: the third commit undid the first two. The
developer of that attempt reported it in its own final message, recorded at
sequence 1516: *"I reverted the entire change. The worktree is now
byte-identical to the base commit `8d4448d`, and `git diff` against base is
empty. The two commits `f929c61` and `0927704` remain in this branch's history,
so the work is recoverable, but nothing is being submitted as satisfying this
item."*

So the reviewer was shown the change exactly as it stood, and its verdict —
there is nothing here that satisfies the item — was right about the branch and
right about what promotion would have moved.

## What was actually wrong

The evidence was accurate and unreadable. An empty patch has two causes that
look identical to a reviewer holding no tools: a change that came to nothing, and
a change that was never collected. The reviewer had one piece of contrary
evidence — commits on the branch naming the item — and no way to reconcile it, so
it reported the machinery. That report cost this item.

ifd.236 closes the gap in the evidence rather than in the collection:

- `ChangeDiff.CommitsWithoutEffect` names the commits a worktree carries over its
  base when the change against that base is nothing, described by the same
  `describeCommits` a branch review uses. It is filled in that case alone;
  everywhere else the patch is what the commits did.
- The review evidence renders it as a section of its own, saying the empty patch
  is the whole change and is what would be promoted rather than a change that
  failed to be collected.
- `TestManagerUnifiedChangesSeesWhatAnAttemptAlreadyCommitted` fails against a
  patch built from the index, so the shape reported here — an empty patch over
  committed work — cannot return unnoticed.

## The replay

The assembly was run against that preserved worktree as it stands, through
`UnifiedChanges` with the run's own recorded base and harness commit. It reports
a patch of 0 bytes and an empty status — the change really is nothing — and now
names the three commits above the base:

```
patch bytes = 0, status = "", truncated = false
commit without effect: 09277040884574581a6d8d3dfd9a8f31d7f4cf4e yoyodyne: yoyodyne-ifd.68.25 Agents speak in their threads through the sink, …
commit without effect: f929c6160ad9d3641fa432ab2832cf13deefd763 yoyodyne: yoyodyne-ifd.68.25 Agents speak in their threads through the sink, …
commit without effect: 38bb0a77dad2c10ba0b23c1b4320bc31e9c22bf9 yoyodyne: yoyodyne-ifd.68.25 Agents speak in their threads through the sink, …
```

So the evidence a re-review is handed is the same empty change it was handed
before, now carrying the sentence that says which emptiness it is. It is not a
review of the reverted work, and cannot be: that work is not in the branch's
tree, only in two commits the third undid, and reviving it is a decision about
the item rather than a diff to collect.

Re-entering the run to obtain one is a separate matter and is not changed here.
The handback guard refuses a worktree holding no change against the recorded
base, so `yoyo triage repair` of this run is turned away as a preserved change
that went missing — the right refusal for a change somebody lost, and the wrong
description of one a developer undid deliberately and reported undoing. Telling
those two apart is unclaimed work.

## What it leaves alone

Nothing here changes what a run does about an attempt that undoes itself. The
round was reviewed, judged `repair`, and spent the item's last permitted attempt;
whether a change that comes to nothing should reach a reviewer at all, or settle
as its own kind of round before one is asked for, is a question for the backlog
rather than an answer this diagnosis assumes.
