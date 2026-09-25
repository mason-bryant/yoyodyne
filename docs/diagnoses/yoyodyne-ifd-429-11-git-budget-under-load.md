# yoyodyne-ifd.429.11: the suites' Git and tracker steps under load

Nine developer reports across five runs on 2026-09-24 and 25 named the same
failure: a Git replay, a `git status`, a worktree checkout, or a `bd create` in
`internal/orchestrator`, `internal/gitworktree`, or `internal/beads` killed at
its budget during a full `make test` or `make race`, in a test that passed in
seconds on its own. This is the record of why the fix is a budget of the tests'
own rather than the manager's load-scaled one, and of the suite passing under
load once it was.

## Why the scaled budget was not the fix

The replay, the status read, and the checkout already ran under the manager's
load-scaled budget: none of the managers these tests built named a `Timeout`.
The reports show that budget reached by Git that was working:

| test | step | killed at | one-minute load |
| --- | --- | --- | --- |
| `TestManagerIntegrateAdmitsOnePromotionAndReplaysTheLoser` | replay | 66.8s, the scaled figure | about 36 |
| `TestPipelineMergesIntoATargetThatRefusesDirectPushes` | one-file checkout | 31s | about 50 |
| `TestSchedulerRunsSeveralEligibleItemsAtOnceInWorktreesOfTheirOwn` | replay | the idle 30s | 11 to 13 |
| `TestTheDashboardPageChangeReplayedThroughTheBoundPresentsTheReadModelWhole` | checkout | 30s | not recorded |
| `TestPipelineBlocksWhenTheIntegrationRetryBudgetIsSpent` | `git status` | exit -1 | not recorded |
| `TestPipelineBlocksOnAReplayConflictWithoutResolvingIt` | replay | not recorded | not recorded |
| `TestDecompositionEdgeConformance` | `bd create` | 90s | 12 to 16 |

The third row settles it. At a load of 11 to 13 on sixteen cores the scaling
reads an idle machine and grants the idle figure, and a replay still ran past
it. The one-minute average lags, and it does not measure what a suite's own race
binaries do to a Git command started beside them. So no scaling of it gives these
tests a figure they can rely on. The tests are about what Git and the harness
do, not how long Git took. The only thing their budget has to catch is a
command that has hung, and ten minutes catches that as well as thirty seconds
does, under the Makefile's twenty-minute `TEST_TIMEOUT`.

## What ran

Two loops over one worktree, started together on 2026-09-25 from a script in the
run's scratch directory, on a sixteen-core machine carrying other developer runs
throughout. The primary ran `make race` and `make test` alternately, six runs,
under `GOFLAGS="-count=1 -v"` so nothing was served from the test cache and
every test's own result line was kept. The neighbour ran `make race` under
`GOFLAGS=-count=1` in a loop for as long as the primary was going. Two race
suites side by side held the load near twenty, so from 00:19 sixteen busy loops
that ended themselves after seventy minutes were added beside them, which is
what put runs 3 to 6 above thirty.

## What it reported

| primary run | check | exit | started | ended | load at start (1, 5, 15 min) |
| --- | --- | --- | --- | --- | --- |
| 1 | `make race` | 0 | 00:09:27 | 00:19:09 | 12.50, 14.35, 18.40 |
| 2 | `make test` | 0 | 00:19:09 | 00:27:27 | 20.41, 16.63, 16.79 |
| 3 | `make race` | 0 | 00:27:27 | 00:34:51 | 46.84, 40.48, 29.62 |
| 4 | `make test` | 0 | 00:34:51 | 00:40:27 | 42.53, 43.90, 35.68 |
| 5 | `make race` | 0 | 00:40:27 | 00:47:11 | 41.14, 40.07, 36.24 |
| 6 | `make test` | 0 | 00:47:11 | 00:52:47 | 45.95, 44.24, 39.59 |

The neighbour's six `make race` runs over the same span all exited 0 as well.
They started at one-minute loads from 12.50 to 46.53. Sampled every twenty
seconds from 00:27 onward, the one-minute load stayed between 33.96 and 53.87.
No log of the twelve runs carries a `FAIL` line or a `(cached)` line.

Every named test passed in every primary run. The times are each run's own,
from runs 1 to 6:

| test | runs 1 to 6 |
| --- | --- |
| `TestManagerIntegrateAdmitsOnePromotionAndReplaysTheLoser` | passed in all six (parallel subtests) |
| `TestTheDashboardPageChangeReplayedThroughTheBoundPresentsTheReadModelWhole` | 16.9s, 22.1s, 24.9s, 10.3s, 15.4s, 9.2s |
| `TestSchedulerRunsSeveralEligibleItemsAtOnceInWorktreesOfTheirOwn` | 28.7s, 111.8s, 52.7s, 15.8s, 38.9s, 24.2s |
| `TestPipelineMergesIntoATargetThatRefusesDirectPushes` | 68.4s, 27.8s, 37.2s, 45.0s, 12.3s, 6.2s |
| `TestPipelineBlocksWhenTheIntegrationRetryBudgetIsSpent` | 9.6s, 9.0s, 10.3s, 8.2s, 22.8s, 8.2s |
| `TestPipelineBlocksOnAReplayConflictWithoutResolvingIt` | 13.3s, 12.1s, 13.1s, 11.3s, 15.5s, 10.9s |
| `TestDecompositionEdgeConformance` | 127.7s, 85.3s, 66.8s, 52.2s, 56.5s, 51.4s |
| `TestParentFieldConformance` | 142.4s, 125.9s, 85.7s, 65.7s, 64.8s, 73.4s |

These are whole-test times, not the time of any one Git or `bd` command, so
they do not show a single command running past its old budget. What they show
is how far load stretches these tests. The same test took up to seven times as
long in one run as in another, with the code unchanged, and a budget sized for
the fastest run is one the slowest would have hit.

## What this does not show

Six primary runs and six neighbour runs are fewer than
[yoyodyne-ifd.389](yoyodyne-ifd-389-race-beside-race.md)'s ten and eleven.
Runs 1 and 2 started below a load of thirty. The reports' failures were
intermittent, so twelve passes are evidence rather than proof. In the
same runs, `internal/orchestrator` took 323 to 557 seconds per binary, which is
still under `TEST_TIMEOUT`.
