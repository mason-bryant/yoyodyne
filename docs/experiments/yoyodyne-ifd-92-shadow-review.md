# Experiment: does a cheaper reviewer catch what the configured one catches?

Work item: yoyodyne-ifd.92. Records read 2026-08-23 against the harness's own
state at `state/products/yoyodyne/branch-reviews/`.

**Status: the instrument is delivered; the measurement is blocked, not descoped.**
The run that built the instrument could not invoke a provider at all, so the
experiment has not been run and the decision about running it is an operator's
rather than a developer's. This document is what that decision needs: what is
built, what the benchmark is, and exactly what remains.

## What was delivered

`yoyo review --shadow [--model <name>]` makes a review that measures the reviewer
instead of judging the branch, and `yoyo review --compare` reports what the
collected ones amount to: per severity, how many of the baseline reviewer's
findings the shadow also anchored to, how many it missed, and how many it raised
alone, with each side's own cost from its own event log.
[The operator documentation](../work.md#measuring-the-reviewer-against-itself)
describes both.

## What is blocked

No shadow review was run, so there is no miss rate, no false-positive-candidate
rate per class, and no Sonnet-side cost. That is the work item's actual
deliverable and it is outstanding.

Two things stopped it, and neither is a judgement the developer run was free to
make:

- **The provider is unreachable.** The run executes inside a sandbox with an
  empty network allowlist. `claude -p … --model sonnet` returns
  `Failed to authenticate. API Error: 403 Connection blocked by network allowlist`,
  and `https://api.anthropic.com` fails the CONNECT tunnel with a 403. A branch
  review is one provider invocation, so no shadow review can be made from here at
  all — this is not a matter of how much spend was authorized.
- **Five of the six states need a branch created.** `--branch` takes a local
  branch name, and only the sixth round's head is still a branch head. The other
  five need `git branch <name> <commit>`, which the developer contract forbids.

So an operator or a run with provider access and the authority to create those
five local branches is what finishes this. The steps are below.

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

## Running it

From the repository, with a provider reachable:

```sh
# The sixth round needs no branch: ifd19-docs-true is still at that head.
./bin/yoyo review --shadow --model sonnet --base 78395930d880 --branch ifd19-docs-true

# Each earlier round needs a local branch at its recorded head first.
for head in f654b6420a2a 3a01ace9a467 5bf53efc2907 23fb91e171b6 e06954210f87; do
  git branch "shadow-$head" "$head"
  ./bin/yoyo review --shadow --model sonnet --base 78395930d880 --branch "shadow-$head"
done

./bin/yoyo review --compare
```

`--compare` pairs on the base and head commits, not on the branch name, so each
`shadow-<head>` review pairs with the `ifd19-docs-true` verdict recorded on the
same commits, and each comparison names both branches. Nothing has to be
force-moved onto a historical commit to make the pairing work.

Then record below: the per-severity matched/missed/shadow-only counts, each
side's cost, and — the part the counts cannot make — which of the 19 baseline
findings are local, mechanical catches and which only exist in the accumulated
shape of the branch, so the miss rate can be read per class rather than in
aggregate.

## Result

Not yet measured. See "What is blocked" above.

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
