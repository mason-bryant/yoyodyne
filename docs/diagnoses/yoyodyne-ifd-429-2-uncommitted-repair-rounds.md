# yoyodyne-ifd.429.2: the commit was on the accepted path only, so four reissued invocations wrote to a worktree the branch tip never caught up with

Run `run-f3755e3f`, developing `yoyodyne-ifd.425` on 2026-09-22, ended with its
branch tip at `d18d295` — the repair-3 commit — and both of the reviewer's
findings fixed in a worktree HEAD did not carry. Its developer reported, at
critical severity, that its last two repair rounds were never committed and that
the reviewer had filed the identical two findings three times against fixes
already in the worktree.

**The uncommitted rounds are real and the reviewer is not the mechanism.** The
reviewer ran twice, not three times, and both of its verdicts were current when
they were given. What was re-delivered three times was the *developer's* prompt,
carrying the findings out of durable state, because each of those invocations
was reissued rather than accepted — and the commit of an attempt's work sat on
the accepted path alone.

Everything below is read from the product's own state directory:
`runs/run-f3755e3fb4f8148277ed7bcae48b2fc0.json`, its `.events.jsonl`, its
`-delivery.instance.json`, and the branch reflog of the preserved worktree.

## What actually happened

`(*activeRun).develop` loops over developer invocations. Before this change, the
only call to `publishAttempt` — which was also the only thing that committed —
was on the one path where an invocation was accepted and `recordDevelopment`
returned no error. Every other ending returned to the top of the loop, or
returned outright, with whatever the invocation had written still uncommitted:

- a provider nobody can reach, an exhausted usage limit, a transiently
  overloaded server, an invocation that ended without accounting for itself;
- a provider death inside the relaunch budget (`recordRelaunch`, then reissue);
- a provider death past it that the recovery window absorbed
  (`recoverProvider`, then reissue);
- a provider death past both, which returns through
  `blockOnSpentRelaunchBudget`.

`run-f3755e3f`'s repair-4 round met the fifth of those, repeatedly. The
`claude-code` parser records an invocation the provider ends twice as a
`duplicate_terminal_result` anomaly and hands it back as a transient failure —
deliberately, since `yoyodyne-ifd.117.1`: a subagent completion that carries a
terminal's marks is read as the invocation's terminal, so the result already
recorded may be a subagent's rather than the run's, and the answer is a relaunch
in the same worktree and session. The run's event log carries four of those
anomalies (sequences 3017, 3586, 3661, 3796). The run's own record ends on
`the provider ended this run without judging the work after 2 of 2 permitted
relaunch(es): the provider ended this invocation twice, first with "completed"
and again with "completed"`.

So repair-4 was invoked, wrote its fixes, ended on a duplicate terminal, and was
reissued with the same repair prompt — four times, none of them committing. Its
developer read the repeated prompt as the reviewer re-filing, and was right about
the consequence and wrong about the actor.

The branch reflog matches the instance trace exactly, and shows the gap:

```text
d18d2959 @{2026-09-22 11:54:20 -0700}: commit   (repair-3)
0c918934 @{2026-09-22 06:51:13 -0700}: commit   (repair-1)
ef10081b @{2026-09-22 06:18:16 -0700}: commit   (first attempt)
fe638d2e @{2026-09-22 05:35:54 -0700}: branch: created
```

Three commits for five repair attempts. The round at 14:11 UTC committed nothing
because it changed nothing — its developer diagnosed a flaky check and edited no
code, which is correct behaviour — and the four repair-4 invocations committed
nothing because none of them was ever accepted.

## The second half, which no run had to hit to be wrong

A project that does not publish never committed an attempt at all. `publishing`
gated the whole of `publishAttempt`, so a local run's branch tip stood at its
base commit for the entire run and every review of it recorded
`review_head_commit == review_base_commit`. The re-recorded delivery baselines
show it: `Reviewed against: base <commit-1>, tip <commit-1>` on every scenario in
`internal/orchestrator/testdata/baseline/`, now `tip <commit-2>`.

Nothing the checks or the reviewer *judge* was wrong either way — the change
they are shown is measured against the recorded base commit and includes the
working tree — which is why this survived so long. What was wrong is everything
bound to the tip: the commits the reviewer is told the patch spans, the tip the
verdict is recorded against (`review_head_commit`), the branch a publishing run
pushes, and the commit an approval therefore authorizes.

## What changed

`gitworktree.Manager.CommitAttempt` is the local half `PublishBranch` used to do
on its own. `(*activeRun).commitAttempt` calls it immediately after every
developer invocation returns, before anything decides what became of that
invocation, and does so whatever `approvals.publishing` says. A commit that
refuses ends the round there rather than letting it reach the checks or the
reviewer. `publishAttempt` keeps the push and the pull request, and now finds the
work already committed.

Two tests hold it: `TestPipelineReviewsARepairRoundAgainstATipThatCarriesIt`
drives a repair round and asserts each round's review evidence names a distinct
tip commit carrying that round's own change, and
`TestPipelineFailsARoundItCannotCommit` asserts a round whose commit refuses
reaches no reviewer.

## What this does not fix

The duplicate-terminal relaunch is untouched, and `run-f3755e3f` would still have
spent its budget on four invocations of a developer that had already finished its
work. What changes is the cost: the branch tip now carries each of those
invocations, so the round is reviewable where it stops and a handback continues
from the work rather than from the round before it. Whether an invocation whose
first terminal was a clean, non-error `result` should be relaunched at all is a
separate question, and `internal/backend/claudecode/parser.go` is where it lives.
