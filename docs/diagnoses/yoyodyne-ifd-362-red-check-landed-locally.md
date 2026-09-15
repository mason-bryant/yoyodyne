# yoyodyne-ifd.362: how a test that was red on the forge landed locally eight times

On 2026-09-13 the operator's assistant found that every pull request opened
since 2026-09-07 — #487 through #495 — had sat on the forge with the required
`build` check failing, that local `main` had integrated a dozen landings
meanwhile, and that `origin/main` had not moved for six days. The failing test
was yoyodyne-ifd.347's `TestAnInvocationIsMadeUnderTheAccountItWasGiven` in
`internal/backend/codex`, which asserted a `GOCACHE` redirect while handing the
backend a working directory in no repository; PR #497 fixed the test and
auto-merged the held landings.

The question, his: `make test` is a configured check. How did 347 integrate
locally with a test that fails under it — did the check not run on that
branch, or was its verdict not what gated integration? And why did six days of
a forge-wide required-check failure produce no message anywhere?

**The check ran, exited 0, and its verdict is what gated integration — on every
one of the landings.** The test was not red under `make test` as the harness
runs it. It was red only where the test binary's own environment carried no
`GOCACHE`, which is the forge and nowhere the harness ever ran it. Neither of
the two hypotheses holds; what the record shows is a third thing, and it is
below. The silence has a separate cause, also below.

## The check phase ran and passed, every time

Every developer run's event log carries the harness's own check phase as
`command.started` / `command.completed` events from source `harness.checks`,
with the command, its exit code and its verdict. For run-5f8eb416 (ifd.347,
PR #487):

| sequence | at (UTC, 2026-09-07) | command | exit | passed |
| --- | --- | --- | --- | --- |
| 584–585 | 20:11:11 | `make fmtcheck` | 0 | true |
| 586–653 | 20:11:11 → 20:13:45 | `make test` | 0 | true |
| 654–721 | 20:13:45 → 20:16:34 | `make race` | 0 | true |
| 722–724 | 20:16:34 → 20:16:35 | `make vet` | 0 | true |

The workflow instance for the same run records the same thing at the topology
level: `check` entered at 20:11:11 with outcome `produced`, `review` entered at
20:16:35 with outcome `passed`, `integrate` at 20:20:28 on `approved`. The
`make test` output retained in the event log lists every package `ok`, with
`internal/backend/codex` reported `(cached)` — a cached *pass*, from the
developer's own probe minutes earlier in the same worktree.

The same is true of every landing that carried the red test. Reading the check
verdicts off every run record integrated between 347 and the fix:

| run | item | PR | `make test` in the check phase |
| --- | --- | --- | --- |
| run-5f8eb416 | ifd.347 | #487 | exit 0, passed, 2026-09-07 20:13 |
| run-a738aca4 | ifd.350 | #489 | exit 0, passed (twice — one repair round), 2026-09-07 23:12 |
| run-fab4da66 | ifd.351 | #490 | exit 0, passed, 2026-09-07 23:42 |
| run-8094605a | ifd.345 | #491 | exit 0, passed, 2026-09-08 00:24 |
| run-e2519421 | ifd.343 | #492 | exit 0, passed (three rounds), 2026-09-08 01:53 |
| run-ec760358 | ifd.342 | #493 | exit 0, passed, 2026-09-08 02:22 |
| run-53606c51 | ifd.285 | #494 | exit 0, passed, 2026-09-08 02:43 |
| run-7ac372e1 | ifd.356 | #495 | exit 0, passed, 2026-09-13 16:56 |

The item's account says four later landings; the record says seven, so eight
queued merges were held in all. (PR #488 belongs to run-d02b509c, ifd.346,
which failed without promoting anything and so had no queued merge.) None of
the eight carried an exit code other than 0 from `make test` in the round that
reached integration.

The gate itself is also seen refusing in the same window, which rules out a
gate that passes everything: run-d9b807a8 (ifd.336, 2026-09-07 08:06) has
`make test` at exit 2, `passed: false`, followed by a repair round and a green
rerun before review; run-07e4eeaa (ifd.209.29, 2026-09-13 17:59) the same. A
red `make test` is handed back to the developer, exactly as `delivery.yaml`'s
`check` state says.

## Why the test was green here and red there

The test built a Codex backend, ran it with `WorkingDirectory: "/worktree"` — a
path in no repository — and asserted that the invocation's environment carried
`GOCACHE`. The backend's environment is `os.Environ()` plus the provider home,
passed through `execution.WithGoBuildCache`, which redirects `GOCACHE` into the
repository's Git directory *only when the working directory is in a
repository*. For `/worktree` it is not, so the redirect correctly leaves the
environment alone, and the assertion reduces to: **does the test process's own
environment carry `GOCACHE`?**

In every place the harness runs the checks, it does. `internal/checks/runner.go`
runs each check with `execution.WithGoBuildCache(nil, directory)`, which sets
`GOCACHE=<repository .git>/yoyodyne/go-build` in the check's environment so
that `go test` can write its build cache inside a sandbox that grants nothing
under the user's home. `go test` hands that environment to the test binary,
`os.Environ()` in the test contains `GOCACHE`, and `hasEnvironmentName(env,
"GOCACHE")` is true. The developer's own probe gets the same redirect from the
Claude Code backend for the same reason, and passes for the same reason. On
the forge, CI sets no `GOCACHE`, the test binary's environment carries none,
and the assertion fails.

This was reproduced for this diagnosis in the developer's worktree, against
the tree as it stood before the fix (commit 716adfa, exported with
`git archive` into the run's scratch directory):

```
$ go test ./internal/backend/codex -run TestAnInvocationIsMadeUnderTheAccountItWasGiven -count=1
ok      github.com/mason-bryant/yoyodyne/internal/backend/codex 0.436s

$ go test ./internal/backend/codex -run TestAnInvocationIsMadeUnderTheAccountItWasGiven -count=1 -exec 'env -u GOCACHE'
--- FAIL: TestAnInvocationIsMadeUnderTheAccountItWasGiven (0.00s)
    --- FAIL: TestAnInvocationIsMadeUnderTheAccountItWasGiven/the_request's_account (0.00s)
        backend_test.go:462: the invocation carried no build cache the run may write: [...]
FAIL
```

The only difference between the two commands is whether the test binary
inherits `GOCACHE`. That is the whole of the discrepancy: a test whose verdict
depended on its environment, run by a harness whose environment happened to
satisfy it and a forge whose environment did not.

So the answer to the operator's question is neither of the two he offered. The
check ran on every branch, its verdict was exactly what gated integration, and
the verdict was honestly green in the environment the harness ran it in. What
was wrong was the test, and PR #497's fix — hand the backend a real temporary
worktree, so the redirect applies and the assertion tests the redirect rather
than the environment — is the right one. The reviewer that approved 347 was
told the checks had passed, which was true.

## What this changes in the integration path

The check gate was never bypassed, and nothing here says it could have been:
`repairLoop` orders the checks in front of the reviewer and the reviewer in
front of `integrate`, and `runstate.State.Validate` already refuses to store a
promotion beside a recorded failing check. But that ordering is a property of
one caller. The invariant this bears on —
`integration-requires-revision-bound-evidence` — asks for the promotion to
verify typed evidence bound to the exact candidate, whatever route reached it,
and until this change there was no such evidence on the record: a passing check
phase left nothing behind but the absence of a `check_failure`, and
`candidate.integrate` is a registered door a definition can name.

So the check phase now writes `checks_passed` onto the run — the commands that
passed, the attempt they passed over, and the harness commit the worktree stood
at — and `integrate` reads it before it takes the promotion lease or writes a
phase. It refuses, with `ErrIntegrationUnearned`, when the record carries a
standing check failure or protected-path refusal, no passing checks at all,
passing checks for a different attempt or a different commit, or no approving
verdict. Recording a failure clears the evidence; a new attempt invalidates it
by moving the attempt count.
`TestPerformingIntegrateRefusesAChangeTheRecordDoesNotShowPassedItsGate`
drives the registered door through each refusal and through the one record
that passes, and `TestPipelineSkipsReviewAndIntegrationWhenChecksFail` still
holds the ordinary path: a red check reaches neither the reviewer nor the
target branch.

What this does not close, and could not: a check that is green in the harness's
environment and red in the forge's. The harness's `GOCACHE` redirect is what
lets `go test` run inside a sandbox at all, and it is visible to every test
binary. A test that asserts on its own environment will pass here and may fail
there, and the only thing that catches that is the forge's required check —
which is the second half.

## Why six days of a forge-wide required-check failure said nothing

Each of the eight runs finished with its merge queued on the forge: the run
promoted the change onto local `main`, asked the forge to merge the pull
request once the base branch's requirements were met, recorded
`merge_queued: true`, and ended. From then on the run was `Outstanding()` and
every reconcile sweep took it up through `settleQueuedMerge`, which asked the
forge one question — `gh pr list --json number,url,state,mergedAt,autoMergeRequest`
— and got the same answer every time: open, unmerged, auto-merge armed.

That answer is indistinguishable from the answer a request gives in the minute
before the forge merges it. `settleQueuedMerge` therefore returned
`ActionQueued` with "the forge still has the merge of pull request N into main
queued", changed no record, and the next sweep asked again. Nothing crossed on
any run's record, so the Slack sink had nothing to say in any thread; the
heartbeat, which counts `AwaitingForge()` runs, said "N promotions awaiting the
forge" with N climbing from one to eight, which is a count and not a cause.
`yoyo reconcile` printed one "queued" line per run per sweep, which is a
standing list nobody reads. The required check's verdict was on the forge the
whole time, and nothing the harness asked the forge included it.

The records confirm the shape. All eight runs carry `updated_at` stamps on
2026-09-13 between 17:56 and 18:13 — the sweeps that settled them after #497
merged — and nothing between their completion and then: no sweep in the six
days found anything to write.

The forge's own state is now read beside the merge flag. `publish.PullRequest`
carries the checks the forge reports failing on the request's head and its merge
state; a queued merge the forge reports BLOCKED with a named failing check is
held by that check. (UNSTABLE — mergeable despite a non-passing status — is a
check the base does not require, and such a merge goes ahead; it is named and
not called held.) `settleQueuedMerge` writes those checks
onto the run's `pull_request.failing_checks`, once, and clears them the sweep
they stop failing; the notify layer says `merge.held` as a `warning` in the
item's thread and the channel when they appear, naming the check, exactly as it
says a dropped merge; `yoyo reconcile` prints each held merge by name and, once
per check, the forge-wide line — the check and every queued merge it holds —
and carries it in `--json` as `held_merges`. Under this, the six days become
one sweep: the first pass after #487 was queued would have said the forge was
holding it on `build`, and by the third landing the sweep would have said one
check was holding three.

## What this leaves

- A batch of queued merges settled *after* they all landed leaves every one of
  them with a publish failure. Once #497 merged the eight held requests, the
  sweep found each merged and then `ConfirmRemoteTarget` failed on all eight —
  "main on origin is at ac05c63, which carries content the promoted commit does
  not" — because that check requires the remote target's tree to equal the
  promoted commit's, which is true only for the request that merged last.
  All eight runs now carry that failure on their record. This is outside this
  item and is named in the run's summary for admission.
- The heartbeat still counts held merges as "promotions awaiting the forge"
  without saying held; ifd.357 owns the one derivation for status and the
  heartbeat, and the failing checks are on the record for it to read.
