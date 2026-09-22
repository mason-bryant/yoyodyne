# yoyodyne-ifd.389: `make race` ten times beside a second `make race`

The item's second done condition is that `make race` passes ten of ten runs
while a second `make race` runs concurrently on the same tree. This is the
record of that being done, on 2026-09-21 and 22, against the tree this item
landed — every named test converted to wait on a signal it controls, the
process runner's budget armed after `Start`, the Slack sink reading its context
before its first write, the shell suites run from a scratch directory, and the
worktree manager's local Git budget scaled by the machine's load.

## What ran

Two loops over one worktree, started together from one script in the run's
scratch directory. The primary ran `make race` ten times in sequence and
recorded each exit code with the one-, five-, and fifteen-minute load averages
at its start. The neighbour ran `make race` in a loop for as long as the
primary was going and one run past it. Both ran with `GOFLAGS=-count=1`,
because the first attempt without it served nine of its ten runs from Go's test
cache in about a minute each — a cached run executes nothing and evidences
nothing, and `make race` on an unchanged tree is cached by default. The
machine has sixteen cores and was carrying other developer runs and their
provider processes throughout.

## What it reported

| primary run | exit | started | ended | load at start (1, 5, 15 min) |
| --- | --- | --- | --- | --- |
| 1 | 0 | 23:17:42 | 23:27:41 | 18.45, 26.10, 28.17 |
| 2 | 0 | 23:27:41 | 23:37:37 | 59.03, 53.10, 42.01 |
| 3 | 0 | 23:37:37 | 23:45:44 | 31.57, 39.49, 39.70 |
| 4 | 0 | 23:45:44 | 23:52:26 | 14.23, 31.35, 36.83 |
| 5 | 0 | 23:52:26 | 23:59:02 | 27.51, 29.98, 34.39 |
| 6 | 0 | 23:59:02 | 00:05:55 | 15.45, 28.30, 32.76 |
| 7 | 0 | 00:05:55 | 00:12:41 | 21.80, 28.52, 32.13 |
| 8 | 0 | 00:12:41 | 00:19:31 | 22.56, 29.19, 31.87 |
| 9 | 0 | 00:19:31 | 00:26:04 | 23.04, 28.01, 30.50 |
| 10 | 0 | 00:26:04 | 00:32:42 | 13.28, 25.26, 29.23 |

Ten of ten passed. The neighbour's eleven runs over the same span all exited
0 as well, starting at one-minute load averages from 18 to 59. No log of the
twenty-one runs carries a `FAIL` line or a `(cached)` line. The one-minute load
average peaked at 59 on a sixteen-core machine, which is well past the
load-average-near-forty at which the earlier run of this item saw Git killed
at its flat thirty seconds and the tests named in the item reach their bounds.

## What that does and does not show

It shows that the suite no longer fails on load at the concurrency this
product actually runs at: two race suites at once, beside the runs and provider
processes the machine was already carrying. Every test named in the item, and
the two added afterwards, ran twenty-one times under that and reached its
signal every time.

It does not show that no wall-clock bound is left anywhere in the suite. The
rule in [`docs/developing-yoyo.md`](../developing-yoyo.md#a-test-never-bounds-a-wait-in-wall-clock-time)
is what holds that going forward, and the bounds that remain are named there:
the code's own timer where it is the thing under test, and a helper child
bounding its own life. One test outside the item's list still races its own
sleep against the code's timer —
`TestAdoptWaitsOutALeaseNobodyHoldsAnyMore` in `internal/runstate`, which
closes a phantom lock after a fifth of `leaseGrace` and expects `Adopt` to have
been waiting — and did not fail in any of these runs; it is named in the run's
summary as work to admit rather than converted here.
