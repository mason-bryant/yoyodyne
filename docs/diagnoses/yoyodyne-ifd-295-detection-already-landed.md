# yoyodyne-ifd.295: the detection was already landed, and this is what proves it

A third developer run — `run-877c6c268319fb3a468adcee61ebd274`, 2026-09-06 — was
dispatched for yoyodyne-ifd.295 and found nothing to implement: every requirement
the item states was already true of the tree it was cut from, base commit
`9339bbd`. This is the record of how that was established, written from the
repository and from the run records rather than from memory, so the next run
dispatched for this item can read it instead of deriving it again.

**Two earlier runs landed the work, and both are ancestors of this branch.**

| run | integrated commit | pull request |
| --- | --- | --- |
| `run-1b782eeb91069754d2284aab7c236e1f` | `bb8ec09` | [#424](https://github.com/mason-bryant/yoyodyne/pull/424), merged |
| `run-55443d4c2c8963339523081c164aba2c` | `b206ca1` | [#428](https://github.com/mason-bryant/yoyodyne/pull/428), merged |

The first moved the reading and the record out of the Slack sink into
`internal/watchdog`, wired the two invocation sites, reduced the sink to a reader,
and rewrote the four documents that described the old arrangement. The second was
a bounded follow-up correcting one wording imprecision and two stale comments in
`internal/readmodel/silence.go`.

## What satisfies each of the item's three requirements

| the item asks for | what satisfies it at `9339bbd` | what pins it |
| --- | --- | --- |
| the stall checker invoked from the harness's own loop or the maintenance job | `watchdog.Checker` is the one pass, called from the pulling loop (`Scheduler.Watchdog`, `internal/orchestrator/schedule.go`, wired and gated by `openStallWatch` in `internal/cli/schedule.go`) and from the sweep (`checkForStall`, `internal/cli/reconcile.go`) | `TestAWatchingSessionTakesTheStallReadingOncePerPull`, `TestTheWatchLoopsStallReadingIsGatedAndRecordsTheStallOnce` |
| a product with Slack disabled records stalls and shows them in `yoyo status` | the record is `runstate.StallStore` under the product, written by the checker and read by `recordedStalls`/`printStalls` in `internal/cli/status.go` | `TestASweepOnAProductWithoutSlackRecordsAStallAndStatusReadsItBack` |
| the sink's poll a consumer of the record rather than its producer | `internal/slack/stall.go` reads the record and never reconciles it | `TestTheSinkNeverWritesToTheStallRecord` |

Nothing here duplicates the productized maintenance job (`yoyodyne-ifd.207`): the
two invokers are the harness's own loop and its own sweep, and neither is
scheduled by anything this repository installs.

## The tests, run at `9339bbd`

Run on 2026-09-06 in this run's worktree, `-count=1`, all passing:

```
go test -run 'TestASweepOnAProductWithoutSlackRecordsAStallAndStatusReadsItBack|TestTheWatchLoopsStallReadingIsGatedAndRecordsTheStallOnce|TestTheSinkSaysWhereStallsAreNoticedWhenItStarts' ./internal/cli/
go test -run 'TestALiveSessionStillPollingAndStartingNothingIsAStall|TestTheDeadWindowIsNoticedAndRecordedOnce|TestARunWhoseProcessIsGoneDoesNotSilenceTheCheck' ./internal/watchdog/
go test -run 'TestTheSinkNeverWritesToTheStallRecord' ./internal/slack/
```

`make check` — the four declared checks, `fmtcheck`, `test`, `race`, `vet` —
passes at this commit: 56 packages green under `go test` and again under
`go test -race`, no failures.

## What the mutations establish

A passing test says nothing on its own about whether it would notice the
mechanism going away. Each mutation below removed one part of the mechanism, ran
the named tests, and was reverted; the worktree carries none of them.

| what was removed | what failed | what it said |
| --- | --- | --- |
| `checkForStall` in `internal/cli/reconcile.go` made to return nothing, so the sweep takes no reading | `TestASweepOnAProductWithoutSlackRecordsAStallAndStatusReadsItBack` | *the sweep said nothing about a product that has stopped: no runs need reconciliation* |
| `s.Watchdog(ctx)` removed from the pulling loop in `internal/orchestrator/schedule.go` | `TestAWatchingSessionTakesTheStallReadingOncePerPull` | *the session took no stall reading at all, so a harness that stopped choosing would go unnoticed* |
| `recordedStalls` in `internal/cli/status.go` made to return no events, so the record is written and never read back | `TestASweepOnAProductWithoutSlackRecordsAStallAndStatusReadsItBack` | *status does not carry "nothing has started on this product since"* |
| `readmodel.LastStart` made to count watch transitions as activity, so a session's own polls answer for the harness having started something | `TestALiveSessionStillPollingAndStartingNothingIsAStall` and `TestALiveSessionThatHasNeverStartedAnythingIsAStall` | *want a live session that has stopped starting anything read as a stall* — the silence read `something started within the last 10m0s` instead |

The last one is worth keeping. The loop's half of this item exists to catch a
session that is alive, still writing a transition on every poll, and no longer
starting runs, and whether it can catch that turns entirely on `LastStart`
ignoring those transitions where any run has ever started. That is now a mutation
somebody can repeat rather than a property inferred from reading.

**One mutation produced a negative result, and it is the useful kind.** Removing
`s.Watchdog(ctx)` from the pulling loop does *not* fail anything in
`internal/cli`: `TestTheWatchLoopsStallReadingIsGatedAndRecordsTheStallOnce`
exercises `stallWatch.check` directly, so it holds the gate and the record and
says nothing about the call site. What holds the call site is
`TestAWatchingSessionTakesTheStallReadingOncePerPull` in `internal/orchestrator`.
A change that keeps the gate and drops the call would pass the `internal/cli`
tests, so the orchestrator test is the one that must not be weakened.

## Why the item was still open, and what remains

The item is not open because anything is unbuilt. `run-55443d4c`'s merge was
confirmed by the forge — the item's own record carries `Pull request merged:
true` and the remote target commit — and the settle then failed on its last step:

```
Publication outstanding: delete the merged remote branch: resolve
yoyodyne/yoyodyne-ifd-295/55443d4c on origin failed with exit code 128:
Read from remote host ssh.github.com: Connection reset by peer
```

That left the item open with nothing marking it, and the scheduler pulled it
again as ordinary ready work. This run's own selection record says so:

> the scheduler pulled yoyodyne-ifd.295 from the backlog: position 23 of 59
> admitted item(s) at priority 2, one of the 7 the tracker reports as ready, with
> 3 of 3 developer slot(s) free.

So what remains is not development. It is closing the item, and settling that
publication — the merged remote branches `yoyodyne/yoyodyne-ifd-295/1b782eeb` and
`yoyodyne/yoyodyne-ifd-295/55443d4c` are still to be deleted, by a person or by a
`yoyo reconcile` that reaches the forge.

Two things are worth admitting to the backlog on the strength of this, and are
named here rather than filed, because a developer run does not admit work:
closing a work item on the confirmed merge rather than on the branch deletion
that follows it, and keeping an item that carries an outstanding publication out
of the pull — blocked, parked, or on the triage docket — so it reaches triage
instead of a developer.
