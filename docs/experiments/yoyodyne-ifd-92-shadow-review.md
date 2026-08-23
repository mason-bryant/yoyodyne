# Experiment: does a cheaper reviewer catch what the configured one catches?

Work item: yoyodyne-ifd.92. Records read 2026-08-23 against the harness's own
state at `state/products/yoyodyne/branch-reviews/`.

**Status: the instrument is delivered and the measurement is not.** This document
is the half that says what remains, so that the gap is visible in the repository
rather than only in a run summary nobody reads again.

## What was delivered

`yoyo review --shadow [--model <name>]` makes a review that measures the reviewer
instead of judging the branch, and `yoyo review --compare` reports what the
collected ones amount to: per severity, how many of the baseline reviewer's
findings the shadow also anchored to, how many it missed, and how many it raised
alone, with each side's own cost from its own event log.
[The operator documentation](../work.md#measuring-the-reviewer-against-itself)
describes both.

## What was not, and why

No shadow review was run, so there is no miss rate, no shadow-only rate, and no
Sonnet-side cost. The run that built the instrument had no provider access, and
spending on provider invocations was not something it was asked for. Running the
experiment is the remaining work, and it is the deliverable the work item
actually names.

## The benchmark is ready as recorded

The item names ifd.1.9's six review rounds as a ready-made benchmark with graded
difficulty, and they are ready: all six are recorded **branch** reviews, which is
exactly what `--compare` pairs on, so no baseline has to be bought again.

Branch `ifd19-docs-true`, base commit `78395930d880`, six rounds recorded
2026-08-19, every one of them `opus` / `claude-opus-5`, none of them a shadow:

| Head | Commits | Verdict | Findings | Severity | Recorded cost |
|---|---|---|---|---|---|
| `f654b6420a2a` | 4 | repair | 5 | 2 major, 3 minor | $0.82 |
| `3a01ace9a467` | 6 | repair | 3 | 1 major, 2 minor | $0.87 |
| `5bf53efc2907` | 7 | repair | 3 | 1 major, 2 minor | $0.87 |
| `23fb91e171b6` | 8 | repair | 2 | 1 major, 1 minor | $0.80 |
| `e06954210f87` | 9 | repair | 4 | 2 major, 2 minor | $0.86 |
| `92d383626bc0` | 10 | approve | 2 | 2 minor | $1.00 |
| **total** | | | **19** | 7 major, 12 minor | **$5.22** |

All seven commits are still reachable in this repository, and the branch itself
is still at `92d383626bc0` — the sixth round's head — so that round is
shadow-reviewable as it stands. The other five need a local branch pointed at the
head commit above (`git branch <name> <commit>`), which is the one manual step;
`--base 78395930d880` is the same for all six.

The whole outstanding cost is therefore six Sonnet reviews of evidence that has
already been described once. What each of them costs is not a projection to make
here — it is half of what the experiment measures.

## What the numbers will and will not settle

Two limits are worth knowing before the result is read, both quantified against
the benchmark above rather than asserted:

- **Pairing is by file.** 18 of the 19 baseline findings name one, so one finding
  can never be paired and will count as missed whatever Sonnet saw. Three of the
  six rounds have two findings sharing a file (5 findings over 4 files, 3 over 2,
  4 over 3), and within a file the pairing is positional — so a round can report
  a match where the two reviewers were talking about different things in the same
  file. `--compare` lists every finding under its comparison for this reason.
- **The class split the item cares about is not a severity split.** Local and
  mechanical catches versus accumulated-shape catches is a judgement about a
  finding's content. `--compare` reports per severity and prints the findings;
  reading the 19 baseline findings into the two classes is a step a person takes,
  and the result is only interesting once it is taken.

## The dependency the item names

The item says the cost comparison needs ifd.83's phase costs to price what
tiering would save, and that is still true. What this change makes readable is
what one review invocation cost. What tiering would save across the harness is
the reviewer's share of every run, which needs the developer/reviewer split
within a run — not something the run record carries today. Without it, this
experiment prices the review and not the saving.
