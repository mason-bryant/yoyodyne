# yoyodyne-ifd.429.3: the second tranche of load-flaky tests

[yoyodyne-ifd.270](yoyodyne-ifd-270-ten-checks-under-load.md) left three tests
waiting on wall-clock time, and four more failures of the same class have been
reported since. This records what each one was, what changed, and the repetition
the item asked for. The rule every change follows is
[the one in `docs/developing-yoyo.md`](../developing-yoyo.md#a-test-never-bounds-a-wait-in-wall-clock-time).

## The three tests 270 left

- `TestAdoptWaitsOutALeaseNobodyHoldsAnyMore` (`internal/runstate`) closed a
  phantom lock after a fifth of the 250ms `leaseGrace` and expected `Adopt`
  still to be waiting. Under load the sleep can outlast the grace. The store now
  has two seams that the harness never sets: `leaseWait`, the grace, and
  `leaseHeld`, which is told each time an attempt finds the lock held. The test
  widens the grace to an hour and lets the lock go the first time it is told.
  What it asserts is that adoption retried, exactly once, and got the run.
- `TestAProcessThatStopsIsNotExitedBehindIt` (`internal/shutdown`) required a
  20ms grace timer not to fire before the test's next line ran. `answering` now
  starts the grace through a `graceStarter`, and `Answering` passes it a real
  timer. The test passes one it controls, then waits for the grace to be
  disarmed, which only the work returning does. It checks that nothing is still
  waiting on the grace afterwards.
- `TestDashboardPrintsItsURLAndTokenAndStopsWhenAsked` (`internal/cli`) polled
  stdout for five seconds and allowed ten for the stop. It now waits on the
  command writing its `token:` line, or on the command returning, and then on
  the command returning after cancellation. Nothing is set beside either wait.
  A developer run's sandbox refuses loopback binds, so there this test takes its
  skip path; its token path was exercised only by the `make check` runs below.

## gitworktree's tests name their own budget

The item counted about 95 call sites and 29 manager constructions, almost none
of them naming a `Timeout`. The count here was 47 `New(Options{...})`
constructions. 42 now name `testGitBudget`, which is ten minutes, and so do the
two tests that run Git beside a manager. `loadScaledGitBudget` is gone. The five
constructions left alone either name a figure of their own or test the
production budget: the default-budget test's two managers, its named-budget and
negative-timeout cases, and `TestANamedBudgetStillGetsTheCheckoutsAllowance`. A
named budget is not load-scaled, and a creation's checkout gets its allowance on
top of it. That lets the two tests about the checkout allowance assert the exact
figure instead of a floor. `load_test.go` says why, and
`docs/developing-yoyo.md` now says so in place of the sentence that had tests
use the scaled budget.

## The beads conformance timeout

`conformanceTimeout` was 90 seconds for each `bd` command. On this machine,
with a load average around 12, `TestParentFieldConformance` took 66 seconds on
its own, and every conformance test starts its own database engine. The bound
is now ten minutes. Like `testGitBudget`, it sits under `TEST_TIMEOUT`, so a
hung `bd` is still reported as the command it was. The tests stay in the
per-run gate. `docs/developing-yoyo.md` makes them the check on a `bd` version
bump, and they skip where `bd` is not installed.

## The scheduler race

`TestSchedulerRunsSeveralEligibleItemsAtOnceInWorktreesOfTheirOwn` failed once
with `Invalid path '.../worktrees/creation-loop-27'`. The registry lease is not
the cause. Every write the manager makes to the registrations (`add`, `remove`,
`prune`) holds the exclusive lease, and every command that walks them
(`worktree`, `rebase`, `checkout`, `switch`, `branch`) holds the shared one.
The test's creation loop runs raw `git worktree add` and `git worktree remove`
outside the lease, on purpose: that is the crossing `runBounded`'s re-run exists
to cover, from a harness on an older binary or Git run by hand.

The re-run only covered the half-written shape: `failed to read
.git/worktrees/<id>/commondir`. Git refuses a walk in two more ways, both when a
file or an entry disappears between Git checking for it and reading it:

- `failed to read '.git/worktrees/<id>/locked': No such file or directory`. An
  add keeps a `locked` marker on its entry while it works and deletes it when it
  finishes. This was reproduced here: two add/remove loops and three walking
  loops ran against one scratch repository for 90 seconds on git 2.50.1. That
  produced two of these, beside seventeen of the known `commondir` one.
- `Invalid path '<common>/worktrees/<id>': No such file or directory`. Git's
  `get_common_dir_noenv` reads the entry's `commondir` and then resolves the
  common directory through the entry with die-on-error. A removal that deletes
  the entry between the two steps produces this message, which is the one the
  scheduler test recorded. It did not recur in the 90 seconds above.

Neither can be left behind as a shape, because the entry is either whole or gone
once the command beside it returns. So `crossedRegistration` now covers both,
and they are re-run as the half-written `commondir` is. Two tests hold this:
`TestACrossedRegistrationIsGitsWordingForAnEntryAndNothingElse` checks the
pattern against Git's wording, and
`TestManagerRunsAnyGitCommandAgainWhenItCrossesARemoval` checks the manager
re-runs a command over each. The test's own creation loop is unchanged. It
crosses every run, and that crossing is what the test judges. After the change,
eight `-race` runs of the scheduler test in a row passed, with the loop crossing
each 52 to 66 times.

## What ran

Three `make check` runs in sequence over this worktree, all with
`GOFLAGS=-count=1`, from a script in the run's scratch directory. A
`GOFLAGS=-count=1 make race` loop ran beside them until the second check ended,
and its fourth run carried on into the third check. Both loops had a deadline
they computed themselves.

| run | exit | started | ended | load at start (1, 5, 15 min) |
| --- | --- | --- | --- | --- |
| check 1 | 0 | 04:56:24 | 05:23:06 | 33.14, 34.04, 26.25 |
| check 2 | 0 | 05:23:06 | 05:43:12 | 59.42, 60.32, 60.49 |
| check 3 | 0 | 05:43:12 | 05:57:29 | 54.21, 51.42, 54.16 |
| neighbour 1 | 0 | 04:56:24 | 05:13:55 | 33.14, 34.04, 26.25 |
| neighbour 2 | 0 | 05:13:55 | 05:27:25 | 88.13, 80.25, 65.02 |
| neighbour 3 | 0 | 05:27:25 | 05:38:54 | 58.91, 57.14, 58.64 |
| neighbour 4 | 0 | 05:38:54 | 05:50:39 | 43.74, 49.67, 55.15 |

None of the seven logs has a `FAIL` line, a `panic:` line, or a `(cached)`
line. `internal/orchestrator` is still the longest binary in the suite. Its race
half took 826s, 646s and 432s in the three checks, and 962s, 786s, 666s and
681s in the neighbour's runs. The longest of these is 16 minutes, inside the
twenty-minute `TEST_TIMEOUT` but by less than 270's record had it. A package
that keeps growing toward that figure is one to split, as that section says.

## What this does and does not show

Three runs is fewer than 270's ten, and deliberately: 270's record notes that a
repetition that size does not fit in one developer run. What they show is that
the whole gate passes with every change above in it, at one-minute loads from
33 to 88 with a second suite beside it. They do not show that the two removal
refusals are all Git has. The pattern is still Git's wording, matched one
refusal at a time, and a Git that fails a walk some other way will fail it
until somebody adds that case.
