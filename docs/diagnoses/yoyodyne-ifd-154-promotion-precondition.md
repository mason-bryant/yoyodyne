# Verification: the promotion precondition the team-mode design leans on

Work item: yoyodyne-ifd.154, a bounded verification commission on the ifd.97
pattern, raised from the reviewer's warning on yoyodyne-ifd.82.2's run. Read-only
against the promotion path; the only change made beside this document is test
coverage for what it found. Nothing in the promotion path was altered.

Code read at 2026-08-23, against the tree this document is committed with:
`internal/gitworktree/remote.go`, `internal/gitworktree/manager.go`,
`internal/gitworktree/converge.go`, `internal/orchestrator/publish.go`,
`internal/orchestrator/pipeline.go`, `internal/orchestrator/reconcile.go`, and
`internal/publish/github.go`. Every line number below is from that tree.

## Superseded in part by yoyodyne-ifd.177

This document is a dated reading and is kept as one. The defect its fourth
assertion found — a promotion made onto a target the remote had left, an item
closed as integrated against it, and a local target no fast-forward could
reconcile — was admitted as `yoyodyne-ifd.177` and fixed. **The sections below
titled "What a refusal actually does: it does not replay" and the fourth row of
the verdict table describe the tree as it was on 2026-08-23 and no longer
describe the promotion path.**

What changed, in two halves:

- **Before the promotion.** `activeRun.integrate` now settles where the remote
  target stands before it promotes (`activeRun.settleRemoteTarget`,
  `internal/orchestrator/publish.go`). A remote the local target can be
  fast-forwarded onto is taken on, which makes the promotion refuse its own
  recorded base as `ErrTargetDrift` and sends the run through the replay the
  claim under test described — checks re-run, fresh independent review. A remote
  the local target cannot be brought onto blocks the run with both branch
  positions named.
- **After it.** The check-then-act window this document identifies is *not*
  closed, and cannot be by a check: the remote can still move between the settle
  and the merge request, and against a second machine nothing covers that. What
  changed is only what such a movement now produces.
  `publishIntegration`'s drift is no longer an outstanding publication the run
  finishes on. It is settled — a remote that swept the promotion in is caught up
  onto and leaves an ordinary unfinished publication, and a remote that diverged
  blocks the run, which happens before `finish` closes anything.

So the window remains and the wedge does not: no run closes an item as
integrated against a divergence nothing reconciles, on either side of the
promotion.

## Superseded in part by yoyodyne-ifd.181

The queued-merge gap this document identifies had a second half that ifd.177 did
not reach, and `yoyodyne-ifd.181` closed it: **a run whose merge the forge queued
no longer closes its work item.** The section below titled "What is not enforced:
the merge the forge queues" still describes what the forge is and is not asked to
enforce, which is unchanged — the base of a queued merge is still unpinned, and
the guarantee for that path is still a check before the request plus
after-the-fact detection. What changed is only what the harness records while
that detection is outstanding: the run finishes with the item open and the queued
merge named on it, reconciliation closes the item when the forge's merge is
confirmed, and a merge the forge dropped hands the item back with a blocker
rather than leaving it closed as integrated against a publication that never
happened.

The same work item gave the divergence this document describes a documented,
executable recovery route, which it never had — see the last section.

The rest of this document — where the precondition is enforced, and what
containment and content each prove — is unchanged and still holds.

## The claim under test

[The team-mode coordination draft](../team-mode-coordination.md), in the
promotion section (lines 276-283), argues that cross-machine promotion is already
safe:

> A promotion is already a compare-and-swap against the shared truth: the local
> target is fast-forwarded from the commit the run was written against, and
> before the forge merges, the remote target must contain that commit and carry
> exactly its content. A target another machine moved fails that precondition,
> the promotion **fails closed**, nothing is force-merged and nothing is reset,
> and the run replays onto wherever the target went, runs the checks again, and
> obtains a fresh independent review.

That claim is what lets the draft downgrade its proposed shared turn record to
advisory (lines 307-311) and what the proposed narrowing of
[`one-promotion-per-target-branch`](../decisions/invariants/one-promotion-per-target-branch.md)
rests on (lines 320-321, 505-512). It is four assertions, and they do not all
hold.

## Verdict

| Assertion | Holds? |
|---|---|
| The local target is fast-forwarded from the commit the run was written against | **Yes** |
| Before the forge merges, the remote target must contain that commit and carry exactly its content | **Yes for a merge the harness performs; no for a merge the forge queues** |
| Nothing is force-merged and nothing is reset | **Yes** |
| The promotion fails closed and the run replays onto wherever the target went, with a fresh independent review | **No.** The local promotion has already happened, is not undone, and is never replayed; the work item closes and an outstanding publication is left for a person |

## Where the precondition is enforced

**The check.** `Manager.VerifyRemoteTarget`, `internal/gitworktree/remote.go:160`.
Its two halves are the two the claim names, and each fails closed with
`ErrRemoteTargetDrift` (`remote.go:29`):

- **Containment** — the remote target must carry the commit the promotion was
  made from: `descendsFrom(integration.PreviousTargetCommit, published)`,
  `remote.go:193-200`.
- **Content** — it must carry exactly that commit's tree:
  `sameContent(published, integration.PreviousTargetCommit)`, `remote.go:201-208`,
  where `sameContent` compares resolved trees at `remote.go:294-304`.

A remote target branch that does not exist is drift too (`remote.go:174-176`).
The one early acceptance is a remote at or behind the promoted commit
(`remote.go:180-186`), which is a remote holding nothing this repository lacks.
Anything else is fetched into a scratch ref before it is judged
(`remote.go:190-192`, `remote.go:266-289`), and a branch that moved between being
resolved and being fetched is refused rather than answered about
(`remote.go:280-283`).

Containment alone would not be enough, and that is the crux for the cross-machine
case. The remote target of a repository that publishes is a forge merge commit
this repository does not have and never will, sitting above the base — and so is
another machine's forge merge. Both pass containment. **Only the content check
separates them.**

**The call site on the promotion path.** `activeRun.publishIntegration`,
`internal/orchestrator/publish.go:229`, immediately before
`Publisher.Merge` at `publish.go:233`. A drift error returns at `publish.go:230`
without reaching the merge call.

**The head side of the same guard.** The pull request must carry the commit that
was integrated (`publish.go:221-225`), and the merge request pins it on the forge
call with `--match-head-commit` (`internal/publish/github.go:324-326`).

**The local half.** `Manager.Integrate` refuses unless the local target is still
at the run's recorded base — `previousTarget != worktree.BaseCommit` is
`ErrTargetDrift`, `internal/gitworktree/manager.go:1009-1015` — and moves it only
by compare-and-swap: `update-ref refs/heads/<target> <new> <old>`, or
`merge --ff-only` when the primary checkout is on the branch
(`manager.go:1294-1318`).

**Nothing is force-merged or reset.** The only force in the path is
`--force-with-lease` on the run's own branch, pinned to the commit the harness
published (`remote.go:126-136`). The remote branch deletion is a compare-and-swap
on the published commit (`remote.go:340-356`), and the local catch-up is
fast-forward-only, held for a person when it is not (`converge.go:113-121`,
`converge.go:142-151`).

**After the merge.** `ConfirmRemoteTarget` (`remote.go:224-260`) requires the
remote target to contain the promoted commit unrewritten *and* carry exactly its
content, so a forge that replayed or merged something else is reported rather
than accepted.

## What is pinned by test

- `internal/gitworktree/remote_test.go:340`,
  `TestManagerVerifyRemoteTargetRefusesARemoteThatMoved` — a moved remote target,
  and an absent one, are drift.
- `internal/gitworktree/remote_test.go:285`,
  `TestManagerVerifyRemoteTargetAcceptsTheForgesOwnMergeCommit` — this
  repository's own forge merge is not drift, so the check does not block every
  publication after the first.
- `internal/gitworktree/remote_test.go:390`,
  `TestManagerVerifyRemoteTargetRefusesAnotherMachinesForgeMerge` — added by this
  work item. A second machine's promotion, merged by the forge onto the same
  base, is refused. The test proves containment passes first, so the refusal is
  the content check's alone. This is the cross-machine case the draft's argument
  is about, and it was the shape that was unpinned: the existing drift coverage
  used a plain pushed commit rather than a merge commit.
- `TestPipelineRefusesToMergeIntoARemoteTargetThatMoved` — the promotion path
  consults the check before it asks the forge: with a drifted target the forge is
  never asked to merge at all. *Replaced by ifd.177 with
  `TestPipelineStopsBeforePromotingIntoADivergedRemoteTarget`, which pins the same
  property one step earlier: the forge is never asked, and nothing is promoted.*
- `TestPipelineDoesNotReplayAPromotionTheRemoteTargetRefused` — added by this work
  item. Pinned what the refusal actually did, which is the subject of the next
  section but one. *Retired by ifd.177, which made the refusal replay; the case it
  covered is now
  `TestPipelineReplaysOntoARemoteTargetSomebodyPushedTo`.*
- *Added by ifd.177, for the window it could not close:
  `TestPipelineStopsWhenTheRemoteTargetDivergesAfterThePromotion` and
  `TestPipelineClosesTheItemWhenTheRemoteSweptThePromotionIn`. Between them they
  pin both outcomes a post-promotion movement can have — blocked without closing
  the item, or caught up and closed — and the second asserts that the remote
  actually contains the promoted commit, which is the condition that makes
  closing the item safe rather than a repeat of the defect.*
- *Extended by ifd.181, for the queued path's own early close.
  `TestPipelineQueuesTheMergeAndFinishesWithoutWaitingForIt` now asserts that the
  run leaves the item open;
  `TestReconcileFinishesAQueuedMergeTheForgePerformed` asserts that the confirmed
  merge is what closes it, and with what reason; and
  `TestReconcileReportsAQueuedMergeTheForgeDropped` asserts that a dropped merge
  blocks the item rather than closing it as integrated. Between them the closure
  is pinned to the forge's answer on both of the answers it can give.*

## What is not enforced: the merge the forge queues

The precondition is a check the harness makes before it *asks*. It is not a
condition the forge enforces when it *merges*, and for the queued path those are
not the same moment.

The harness asks for `gh pr merge --auto` first
(`internal/publish/github.go:329-336`), and the code's own reasoning says a queued
merge is the ordinary answer from a protected branch (`publish.go:189-196`). Team
mode requires publishing (coordination draft, lines 170-173), and branch
protection is the reason to publish at all — so queued is the team-mode case
rather than an edge of it. In that path `VerifyRemoteTarget` runs, the merge is
recorded as queued, and the run *ends* (`publish.go:249-258`); the forge performs
the merge minutes or hours later. The merge request pins the head and nothing
pins the base — the forge CLI offers no base equivalent of `--match-head-commit`,
and none is passed.

What remains for that window is detection rather than prevention. When
reconciliation settles the queued merge it calls `ConfirmRemoteTarget`
(`internal/orchestrator/reconcile.go:475-478`), which requires the remote to carry
exactly the promoted commit's content and records an outstanding publication when
it does not. **Detected after the merge reached the shared remote, not prevented.**

The immediate-merge path is also a check-then-act, with a window between the
`ls-remote` in `VerifyRemoteTarget` and the `gh` call. That window is short and
covered by the local promotion lease against processes on the same machine; it is
covered by nothing against a second machine.

## What a refusal actually does: it does not replay

The remote check runs **after** the local promotion. `activeRun.integrate` calls
`Worktrees.Integrate` at `internal/orchestrator/pipeline.go:2722` and only then
`publishIntegration` at `pipeline.go:2749`. By the time the remote target is
looked at, the local target branch has already moved.

Drift found there goes to `recordPublishFailure` (`publish.go:230`, defined at
`publish.go:351-358`), which cannot fail the run — stated in the code at
`publish.go:206-208`. `ErrRemoteTargetDrift` is not one of the errors that
re-prepare a change: `contendedIntegration` covers only `ErrTargetDrift` and
`ErrNotFastForward` (`pipeline.go:1081-1083`), so `prepareIntegrationRetry`
(`pipeline.go:1096-1099`) is never reached. There is no rebase onto where the
target went, no re-run of the checks, and no second review. `integrate` returns
nil, `finish` runs, and the work item is closed
(`pipeline.go:2768-2773`).

So the outcome of losing a cross-machine race is not a wasted replay and a second
review. It is:

- a local target branch carrying a promotion the shared remote does not have,
- a work item closed as integrated,
- an outstanding publication recorded on the run and named on the item
  (`publish.go:432-437`), and
- a local target that cannot be caught up afterwards: `CatchUpTarget` finds a
  remote that does not contain the local branch and holds with *"only a person can
  say which history is right"* (`internal/gitworktree/converge.go:117-121`).

That is a person-shaped outcome per lost race, and the draft's cost analysis
(lines 285-288: *"the loser burns a replay and a second full review … a cost and
not a hazard"*) understates it in the direction that matters to the ruling.

## What this means for the amendment, stated without deciding it

The narrowing asks to keep the local file lock as the same-machine queue and make
the shared turn record advisory, on the grounds that the forge precondition is
what makes a cross-machine race unable to corrupt code. On the evidence above:

1. The precondition exists, is the content check as well as the containment
   check, and refuses another machine's merged promotion. That much of the
   argument is sound.
2. It guarantees what the draft says only for a merge the harness performs while
   it watches. For a queued merge — which team mode's own required configuration
   makes ordinary — the guarantee is a check before the request plus after-the-fact
   detection.
3. The consequence of a refusal is worse than the draft states, which makes the
   turn record's "converts a wasted review into a wait" understate its value as
   well.

None of that decides the amendment. The architect owns
`one-promotion-per-target-branch`, and this document is the evidence its ruling
was waiting on rather than a position on it.

## Work discovered, for the product manager to admit

This list was once one struck-through bullet with two live pieces inside it,
which read as resolved to anybody who did not finish the paragraph. It is split
below so each piece carries its own state, and a struck bullet means that piece
and nothing beside it.

- **The merge base of a queued merge is still not pinned, and still cannot be.**
  Pin or re-check it, or state in the design that the queued path detects after
  the merge rather than failing closed before it. Nothing covers this today. What
  `yoyodyne-ifd.181` changed is only what the queued path *records* while the
  answer is outstanding, below — not what the forge is asked to enforce.
- ~~The queued-merge path closes an item as integrated before the forge performs
  the merge.~~ Fixed by `yoyodyne-ifd.181`. A run whose merge the forge queued
  finishes without closing its item; the closure moves to the settle path and is
  made on the forge's answer. A merge the forge dropped no longer closes the item
  either — it records the outstanding publication and hands the item back with a
  durable blocker, which is where a bounded re-arm of the dropped merge is
  decided.
- ~~No recovery path exists for a repository already wedged: a local target
  diverged from the remote, reported by reconciliation on every sweep and
  resolved by nobody.~~ Given one by `yoyodyne-ifd.181`, as a documented,
  executable route rather than a harness action:
  [Unwedging a target branch that diverged from the forge](../operations.md#unwedging-a-target-branch-that-diverged-from-the-forge)
  preserves the local-only promotions on a branch, puts the target back onto the
  remote's history without racing a promotion, and leaves the unpublished work
  named for whoever republishes it. Every report of the divergence — both
  blockers a run can end on — now names that route. **Which history is right is
  still a person's decision, and the harness still does not make it**; what
  changed is that the person is no longer left to invent the steps.
- ~~No run produces a closed work item behind that divergence.~~ Fixed at the
  source by `yoyodyne-ifd.177`, on either side of the promotion: it is settled or
  replayed before, and blocked without closing the item after.
- **The check-then-act window against a second machine is not closed, and cannot
  be by a check.** The divergence above therefore remains reachable, however
  rarely. This is the one piece of the original bullet that nothing has addressed
  and nothing in this shape can; what the two work items above changed is what it
  costs when it happens, not whether it can.
