# yoyodyne-ifd.270: `make check` repeated under load

The item's done conditions are that every test it names waits on the condition
it asserts rather than on a length of time, that ten consecutive full `make
check` runs pass under load, that the repetition runs with `GOFLAGS=-count=1`
so a cached pass never stands in for an executed one, and that the run says
whether the sink's live `F_FULLFSYNC` is worth a separate look. This is the
record of the last three, and of the one wall-clock bound the sweep behind the
first had left behind it. That first condition was already met when this run
started, and the section below says how.

## What was already true

Every test the item names had been converted by
[yoyodyne-ifd.389](yoyodyne-ifd-389-race-beside-race.md), whose rule is stated
in [`docs/developing-yoyo.md`](../developing-yoyo.md#a-test-never-bounds-a-wait-in-wall-clock-time):

- `internal/slack`'s `waitFor` is gone. `TestARefusalOnlyAPersonCanClearIsSaidOnceAndThenWaitedOut`
  and `TestReportingSaysSoWhenItStartsWorkingAgain` drive the sink one pass at a
  time through `passStepper`, so what they assert is how many passes there
  were rather than how long they took, and the sixteen-second sink-state wait
  went with the helper.
- `TestStoppingTheSinkStopsIt` reads promptness off what the sink did — a sink
  stopped before it started asks the workspace nothing and records no presence
  — instead of allowing five seconds for it.
- `internal/execution`'s timeout and stall tests hold the runner's own budget
  and idle bound through a seam, and `TestOSProcessRunnerLeavesAChattyProcessAlone`
  reads the claim off the bound's resets rather than off a chatty helper
  outliving two seconds.
- The rebases are bounded by `Manager.localTimeout`, which scales the idle
  thirty seconds by how far the one-minute load average exceeds the cores,
  capped at ten times, per command. `TestManagerIntegrateAdmitsOnePromotionAndReplaysTheLoser`
  and `TestSchedulerRunsSeveralEligibleItemsAtOnceInWorktreesOfTheirOwn` run
  their Git through that manager, so the budget that killed them at a flat
  thirty seconds now moves with the machine.

What was left of the item was therefore its verification, which 389 did for
`make race` and not for `make check`, and its question about `F_FULLFSYNC`.

## What ran

Ten `make check` runs in sequence over one worktree, from a script in the run's
own scratch directory, each recording its exit code and the one-, five-, and
fifteen-minute load averages at its start. A second suite looped beside them
for the first part of the sequence; the rest of it ran against the load the
machine was already carrying, which on this machine is several other developer
runs and their provider processes rather than an idle baseline. Both loops
carry their own deadline, computed when the script starts, so the load stops on
its own whatever becomes of what started it.

Every run was made with `GOFLAGS=-count=1`. `make test` and `make race` on an
unchanged tree are served from Go's test cache, and a cached pass executes
nothing and evidences nothing: 389's first attempt at its own repetition served
nine of ten runs from the cache in about a minute each before it was noticed.
The same flag is why this took the time it did.

## What the first attempt reported, before the fix

The run's own minute-zero probe — one `make check` on the unchanged tree, while
a second suite ran beside it — failed, and not on any bound a test had set:

```
panic: test timed out after 10m0s
	running tests:
		TestAProviderInvocationTheMachineNeverStartedIsRefusedEnvironmentally (0s)
		... sixteen tests, each 0s to 38s
FAIL	github.com/mason-bryant/yoyodyne/internal/orchestrator	600.624s
```

Ten minutes is `go test`'s own default `-timeout`, the bound the rule in
`docs/developing-yoyo.md` relies on to turn a hang into a failure that names
what it waited on. It is also a wall-clock bound, and at a one-minute load
average of 85 on sixteen cores `internal/orchestrator`'s race binary reached it
with hundreds of its 761 tests still queued on the parallel limit — a suite that
was working, failed by the machine. The same package had taken 371 seconds in
the `make test` half of the same probe, so the margin was already thin without
the race detector.

So the bound was sized for the loaded machine rather than left at the figure a
quiet one gets: `TEST_TIMEOUT` in the `Makefile`, twenty minutes, on both `test`
and `race`. That is the only code change this item made; everything below ran
against it.

## What it reported

| run | exit | started | ended | load at start (1, 5, 15 min) |
| --- | --- | --- | --- | --- |
| 1 | 0 | 10:35:02 | 10:55:54 | 11.10, 55.30, 50.68 |
| 2 | 0 | 10:55:54 | 11:17:16 | 42.60, 50.04, 49.37 |
| 3 | 0 | 11:17:16 | 11:29:40 | 58.68, 61.92, 57.18 |
| 4 | 0 | 11:29:40 | 11:43:31 | 22.06, 37.52, 48.77 |
| 5 | 0 | 11:43:31 | 11:59:11 | 38.86, 42.34, 43.26 |
| 6 | 0 | 11:59:11 | 12:11:31 | 26.92, 35.96, 39.75 |
| 7 | 0 | 12:11:31 | 12:19:14 | 19.83, 32.99, 36.87 |
| 8 | 0 | 12:19:14 | 12:31:28 | 21.12, 26.20, 31.47 |
| 9 | 0 | 12:31:28 | 12:38:52 | 25.15, 31.07, 31.85 |
| 10 | 0 | 12:38:52 | 12:45:35 | 14.65, 21.46, 26.56 |

Ten of ten passed. The neighbour's three `make race` runs over the first
thirty-five minutes exited 0 as well. No log of the thirteen suites carries a
`FAIL` line, a `panic:` line, or a `(cached)` line.

The figure that was raised is the one to read these against.
`internal/orchestrator`'s binary is the longest in the suite, and over those
thirteen suites its race half took 614.7s, 711.7s, 281.7s, 534.7s, 538.0s,
298.4s, 253.9s, 404.6s, 219.5s and 205.7s, with the three neighbour runs at
715.2s, 576.3s and 708.7s. Four of those are past the ten minutes that failed
the probe, and the longest is 11m55s — so this sequence did not merely pass
with the bound raised, it needed the raise four times over, and still ran at
two thirds of the new figure at its worst.

## Is `F_FULLFSYNC` on the sink's live path worth a separate look

No — and the measurement rather than the assertion. `Store.write` replaces each
record through a temporary file it `Sync`s before the rename, and on darwin
`os.File.Sync` is `F_FULLFSYNC`, a barrier the device waits on rather than a
write the kernel may still be holding. Measured on this machine's repository
volume at a one-minute load average of 67 on sixteen cores, one record of the
size the sink writes cost 480ms with the sync and 78ms without it: at that load
the barrier is most of a write, and on a quiet machine it is a small fraction of
a much smaller number.

What the barrier lands on is what decides it. An ordinary pass writes nothing at
all — cursors are saved when the watermark is first taken or a stream goes away,
threads when a thread is opened or a status moves — and a pass that does write
does so against a fifteen-second poll interval and, per delivery, a one-second
pace that exists because that is what Slack accepts. Half a second of barrier
behind a second of deliberate pacing is not a cost anybody is waiting on;
reporting is not a gate on anything, and nothing blocks on a pass.

The two writes on a path somebody does wait on are the presence record written
at startup and cleared on the way out, and each is one record. Both are already
behind the context read that `TestStoppingTheSinkStopsIt` asserts, which is what
makes a stop prompt: a sink stopped before it started writes no presence at all.

The test-time store needs no plain-fsync seam either. What made the cost visible
was the sink tests polling at a millisecond against a wall-clock bound — a loop
fsyncing as fast as it could, against a clock that load could win — and those
are gone: no test in `internal/slack` now bounds anything on how long a write
takes, so the fsyncs a test spends are a handful per test rather than thousands.

If reporting ever does look slow on a busy machine, the place to look first is
`SaveCursors` inside the delivery loop, which writes once per delivery, and not
the pass or the store's durability. That durability is what the barrier buys:
the thread map is the difference between reopening one thread and losing every
thread the channel has.

## What this does and does not show

It shows that the whole gate, and not only its race half, survives the
concurrency this machine carries: ten `make check` runs in sequence over three
hours and ten minutes, at one-minute load averages from 14 to 59 on sixteen
cores, with a second suite beside the first three of them, and nothing executed
from the test cache. It also shows that the bound the rule leans on had itself
become a bound on the machine, and by how much: four of thirteen runs of the
longest package went past the figure that had been failing suites.

It does not show that no wall-clock bound is left in the suite. Three that
nothing here converted, none of which failed in these runs:
`TestAdoptWaitsOutALeaseNobodyHoldsAnyMore` in `internal/runstate`, which closes
a phantom lock after a fifth of `leaseGrace` and expects `Adopt` to still be
waiting — named the same way in 389's record and still standing;
`TestAProcessThatStopsIsNotExitedBehindIt` in `internal/shutdown`, which asks a
twenty-millisecond grace not to have fired before the test's next line; and
`TestDashboardPrintsItsURLAndTokenAndStopsWhenAsked` in `internal/cli`, which
allows five seconds for a listener to print its token and ten for the command to
stop. Each is named in this run's summary as work to admit rather than converted
here.

One thing about repeating this is worth knowing in advance. The sequence took
three hours and ten minutes, and a developer run's provider invocation is
bounded at four; this one had already spent half an hour before the loop
started, and finished with the margin it had left rather than with any to
spare. A repetition of this size does not reliably fit inside one run, and
whoever asks for the next one should expect to run the loop beside the harness
rather than inside it.
