# yoyodyne-ifd.399: the empty-diff rule reads the change against the base commit, and the cap kept counting on published runs

yoyodyne-ifd.391 made a verdict on a run with no change present cost nothing:
`recordReviewVerdict` asks `Worktrees.ChangedPaths` whether the worktree holds a
change, and records the verdict as uncharged when it holds none. Its reviewer
could not see whether `ChangedPaths` reads the change against the run's recorded
base commit or against the uncommitted worktree state. Under
`approvals.publishing: automatic` the harness commits each developer attempt
before the checks and the review run, so a listing read from uncommitted state
would report every published attempt as no change, and the review-round cap
would have stopped counting for this repository the moment 391 merged.

**It reads against the base commit.** `ChangedPaths`
(`internal/gitworktree/manager.go`) runs
`git diff --name-only -z --no-renames --no-ext-diff <BaseCommit> --` and appends
the untracked files, so a committed attempt is listed exactly as an uncommitted
one is. Two pipeline tests in `internal/orchestrator/triagecaps_test.go` prove
it through the verdict path rather than at the listing:
`TestPipelineChargesTheRepairVerdictOnAPublishedChange` drives a publishing run
through a repair verdict with `git status --porcelain` captured clean in the
worktree at each reviewer invocation, and asserts the round is charged;
`TestPipelineChargesNoRoundForAVerdictOnNoChangeAgainstTheBase` drives a run
whose developer rewrites a file byte-for-byte to what the base holds, and asserts
nothing is published and nothing is charged. With `ChangedPaths` mutated to diff
against `HEAD` instead of the base commit, the first test fails with
`review rounds = 0, want the repair verdict on a published change charged`,
which is the failure 399 was admitted to rule out.

## The live counters

The item asks for the counters of one run since 391 merged, as evidence either
way. 391 merged as `b383a40` at 2026-09-19T07:35Z. The counters below are read
from the product's triage store
(`<state>/products/yoyodyne/triage/<item>-<digest>.json`) and the run states
beside it, unedited. A developer run reads that store and cannot write the
tracker, so they are recorded here rather than on the item.

**The run that proves it: this item's own first attempt.** The 399 run,
`run-2ba5ff3efe6fb9f685386b08c842777e`, was made by build `d2f8d6a` (main after
the 391 merge), published its first attempt as PR #543 with harness commit
`62bd7fd`, and was sent back by its reviewer. At the moment of that verdict the
worktree's work was committed and its status clean — the reviewer judged the
published change. The counter was charged:

```json
{
  "schema_version": 1,
  "product_id": "yoyodyne",
  "work_item_id": "yoyodyne-ifd.399",
  "review_rounds": 1,
  "last_judged": "run-2ba5ff3efe6fb9f685386b08c842777e#0",
  "last_round": "run-2ba5ff3efe6fb9f685386b08c842777e#0",
  "last_round_charger": "pid-46849-278bae2df9a490dd",
  "updated_at": "2026-09-19T11:16:31.259145Z"
}
```

`review_rounds: 1` on a published attempt under a post-391 build is the cap
counting. Had the presence question been read from uncommitted state, this
record would carry `last_judged` alone and no `review_rounds`.

**The other completed post-391 run, for contrast.** yoyodyne-ifd.400,
`run-227c945e19c8cc50072858826f143833`, build `b383a40`, published as PR #542
with harness commit `0257ee1`: two verdicts, both approvals (the second after a
promotion that lost its race and replayed), no repair. Its counters:

```json
{
  "schema_version": 1,
  "product_id": "yoyodyne",
  "work_item_id": "yoyodyne-ifd.400",
  "last_judged": "run-227c945e19c8cc50072858826f143833#0",
  "updated_at": "2026-09-19T10:54:45.810954Z"
}
```

Uncharged, correctly, by the approval rule — and so consistent with either
reading of the empty-diff rule, which is why it could not settle the question
on its own.

**A run that looks like evidence and is not.** yoyodyne-ifd.389,
`run-4f2bac4db41640f6717807250a583e6d`, started at 2026-09-19T07:35:58Z, fifty
seconds after the merge, was sent back on a published change and charged
(`review_rounds: 1`, `last_round: run-4f2bac4db41640f6717807250a583e6d#0`).
Its `build` is `77a5a1f`, the merge before 391's, so it was charged by the
accounting 391 replaced and says nothing about 391's rule. A run's `build`
field is what says which accounting charged it; its start time does not.

## What to watch

Nothing. The rule is proven by the tests and confirmed by the first post-391
repair verdict on a published change. A later post-391 run sent back on a
published change whose counter file carries `last_judged` and no `review_rounds`
would contradict both, and the two tests above are where to start.
