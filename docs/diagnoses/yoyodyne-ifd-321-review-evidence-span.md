# yoyodyne-ifd.321: the reviewers were shown the branch and told it was a worktree

`yoyodyne-ifd.321` was admitted on nine review filings across
`yoyodyne-ifd.121.5` and `yoyodyne-ifd.274`, each saying its verdict covered less
than the item asked for and naming committed work it believed the evidence had
left out, and on the reading that the harness hands a continued run's reviewer
only the uncommitted worktree delta.

**That does not reproduce. Every one of those reviews was handed the complete
change against its branch's base, committed work included.** What none of them
was handed was a sentence saying so. This is the record of how that was
established, read from the runs' own durable state and from the commits the
filings name.

## What the assembly collects

`UnifiedChanges` runs `git diff <base commit>` inside the worktree, which spans
committed and uncommitted work alike, and has since publishing was put in front
of the checks. That was already established by
[yoyodyne-ifd.236](yoyodyne-ifd-236-review-evidence-over-committed-work.md) and
is held by `TestManagerUnifiedChangesSeesWhatAnAttemptAlreadyCommitted`. A base
that moves — a replay onto a moved target — moves with the branch, because the
replay rewrites the branch onto that base.

## What the two runs' records say

| Run | Item | Reviews | Patch bytes | Truncated |
|---|---|---|---|---|
| `run-79127d5f39656cae5cabbfc778888207` | 121.5, first attempt | 4 | 168,873 → 189,537 | no |
| `run-626e851a51cd74a0315ba5a7b99e641f` | 121.5, second attempt | 5 | 38,866 → 42,557 | no |
| `run-8f7168f44de8346122afc2549a6a7d58` | 274 | 2 | 5,709 | no |

No review in either item was handed a truncated patch, and none was handed an
empty one.

The commits the item names as invisible are all reachable, and all of them are
pre-replay copies of commits the reviews did see:

| Commit named | Parent | Effect | What it actually is |
|---|---|---|---|
| `97d9aa4` | `8672f4d` | 10 files, +434 −30 | 121.5's change, before the first replay |
| `11c45d2` | `64a3cfb` | 10 files, +434 −30 | the same change, before the second |
| `f01549e` | `f5fa080` | 10 files, +434 −30 | the same change, on the branch that landed |
| `4f956f2` | `3ea5430` | 2 files, +85 | 274's change, before its replay |
| `6c05450` | `5dfd67c` | 2 files, +85 | the same change, on the branch that landed |

The 434 insertions the item reads as an unshown reduction commit are the change
the reviewer was shown: 38,866 bytes of patch is that diff. `git diff f5fa080
dd0bcf7` — the whole of the branch that closed 121.5 — is +496 −30 across ten
files, and touches README.md by ten lines. There was never a reduction commit on
that branch to omit.

## What the reviewers actually did

Each verdict states its own limit, and each limit is an inference rather than an
observation:

- 121.5: *"the patch put in front of me was the repair delta only — the branch's
  reduction commit and the internal/contextbundle widening it carries were not in
  the diff"*, and *"My evidence was bounded to the uncommitted worktree diff"*.
- 274: *"the gate this test pins was landed earlier on the branch (commit
  6c05450, named for this item) and is not in the diff"*. `6c05450` is that run's
  own head commit, and its 85 insertions are the patch the reviewer had just
  read.

The work each reviewer wanted was real and was genuinely absent, but it was
absent because it was **already in the base commit**, not because it was on the
branch below it. 121.5's README was 837 lines at `f5fa080` before its run
started; 274's gate was committed by `yoyodyne-ifd.229` in `e58dd7d` on
2026-08-31, four days before the run that pinned it with a test. No diff measured
against a base can show work that is inside that base, and no assembly change
will make one.

## What was actually wrong

The evidence was complete and unlabelled. It arrived under the heading `# Actual
worktree changes`, with a status, a diff stat, and a patch, and with nothing
saying what the patch was measured against or what it spanned. A reviewer that
knows the harness commits every attempt reads "worktree changes" as the
uncommitted tail of a branch it cannot see — which is exactly what these did,
each inventing the commit it thought it was missing.

ifd.321 closes that in the evidence rather than in the collection:

- `ChangeDiff` carries `BaseCommit`, `Commits`, and `CommitsOmitted`: what the
  change is measured against and the commits already made for it, oldest first.
  The listing is bounded and the bound never truncates the change — the patch is
  the whole range whichever commits the listing could hold — so an omission is
  reported as an omission in the account rather than in the work.
- The evidence opens the change with what the patch covers: the base commit, the
  commits, and the sentence that no committed work of the change is missing from
  the patch and that anything already in the base is not part of the change. A
  truncated patch says the bound cut something inside those commits rather than
  outside the change, which is 148's loud-truncation direction applied to the
  same question.
- The review contract says it too, where a developer cannot edit it, including
  the half no assembly can fix: work already in the base commit cannot appear in
  the patch and is not this change's to show.
- `TestReviewSaysThePatchSpansTheBranchsCommittedWork`,
  `TestManagerUnifiedChangesNamesEveryCommitAContinuedRunCarries`, and
  `TestPipelineTellsAReviewerWhatThePatchOfAContinuedRunSpans` replay the two
  shapes: a run continuing on a branch its earlier attempts committed to, and the
  end-to-end publishing run where the second review judges a branch that already
  carries the first attempt.
- `review.started` records the base commit and the commit count beside the patch
  size, so the next reconstruction of a hedged verdict can read what that review
  was shown instead of inferring it from a byte count.

## What it leaves alone

Nothing here changes what a reviewer is shown of work that landed before the run
started. An item whose earlier tranche is already in the base is judged against
that base, and a reviewer that wants to say its verdict covers only the increment
is right to say so — what is fixed is that it can now tell that case from a patch
it was handed short.
