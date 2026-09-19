# yoyodyne-ifd.402: neither run lost its pull request; one merge was queued and settled, the other was an escalation that merges nothing by design

The item was admitted on this evidence: two runs "ended succeeded/complete with
no pull request on their record and no merge requested of the forge" —
run-7ae62396 on yoyodyne-ifd.391 (PR 540) and run-c07d6849 on
yoyodyne-ifd.141.3 (PR 544) — with the tracker timeouts of the same day named
as the first suspect for a write lost between the developer phase and the run's
completion.

**Both records carry their pull request, and the harness wrote it there in the
developer phase, where it always has.** For 391 the harness also asked the
forge to merge; the forge queued the merge, performed it three seconds before
the run completed, and the next reconcile settled it. For 141.3 the reviewer
escalated on the granted repair round, and an escalation integrates nothing and
asks the forge for nothing — that is the verb, not a lost write. What the day's
observer met on 141.3 was real and is not on the run record at all: the
escalation docket entry, which is what the development manager decides from,
named the run's branch, worktree, target, and base commit and not the open pull
request beside them. The tracker timeouts cost neither run anything: the
`Pull request:` line reached both items.

## run-7ae62396 on yoyodyne-ifd.391

Read from `products/yoyodyne/runs/run-7ae623962d8cba0045e5f3161674b57b.json`,
its event stream, the item's notes in the tracker export, and `git log` on
`origin/main`. All times UTC.

| when | what |
|---|---|
| 07:35:02 | the reviewer's fourth round approves the implementation (`review.completed`, decision `approve`) |
| 07:35:07 | `b383a40 Merge pull request #540` lands on `origin/main` — the forge's merge, on the request the harness had just asked it to merge |
| 07:35:10 | the run completes: `status: succeeded`, `phase: complete`, `pull_request: {number: 540, merge_method: merge, ...}`, `integration` recorded |
| 07:35 | the item's notes gain, from the run's own completion: `Pull request: #540`, `Merge queued: the forge merges this request once the base branch's requirements are met; yoyo reconcile settles the run when it does`, `Merge method: merge` |
| 07:45:19 | reconcile settles the queued merge: the item's notes gain `Yoyodyne settled the merge this run left queued with the forge ... Remote target commit: b383a4069f9ecf4f5a4e36b2e1e5f01e1f9d7547`; the record's `updated_at` moves to this moment, with `merge_commit` recorded and the branch and worktree removed |

The `merge_method` on the record is written by exactly one thing, the run's own
`publishIntegration` (`internal/orchestrator/publish.go`), after the forge has
answered the merge request; nothing that runs afterwards writes one. So the
record itself says the request was made. The forge queued it — `main` is
protected and requires checks, which is the ordinary answer — and the run
finished with the merge queued and the closure deferred, which is the shape
`TestPipelineQueuesTheMergeAndFinishesWithoutWaitingForIt` has held since
yoyodyne-ifd.301. The forge performed the merge while the run was still writing
its notes, and the settle ten minutes later is what `docs/operations.md` says
settles a queued merge. "PR 540 later merged anyway" is the queued merge
landing, three seconds before the run that queued it ended.

## run-c07d6849 on yoyodyne-ifd.141.3

Read from `run-c07d6849e1f55fd7b982600b8f97a303.json`, its event stream, the
escalation record `escalations/escalation-run-c07d6849...-d0ef9fc3471f1838.json`,
`docket.jsonl`, the item's notes, and `git log`.

| when | what |
|---|---|
| 12:54:17–12:55:42 | the granted repair round's review: the reviewer escalates — the review bound cut ~880 lines of `internal/readmodel` out of the patch for the second round running, so the criteria cannot be confirmed and a rescope is asked for |
| 12:55:46 | the run completes: `status: succeeded`, `phase: complete`, `review_decision: escalate`, no `integration`, `pull_request: {number: 544, state: OPEN, merged: false}` — the request the developer phase opened between the first attempt ending at 11:34 and the first review starting at 11:40, and every repair attempt updated |
| 12:55:46 | the item's notes gain, from the run's completion: `Pull request: #544 https://github.com/mason-bryant/yoyodyne/pull/544`, `Pull request merged: false`, and the escalation account: "nothing was integrated and the item is parked with the escalation in front of the development manager" |
| 12:55:46 | the escalation is docketed (`escalation:run-c07d6849...`); its `artifacts` are `branch`, `worktree_path`, `target_branch`, `base_commit` — **no pull request** |
| 12:59:10 | the development manager decides `rescope`: carve the read-model derivation out as 141.4 to land and be reviewed first; 141.3 waits on it |
| 13:17:02 | `8c10fa5 Merge pull request #544` lands on `origin/main`: the operator armed the merge by hand, taking the whole branch, the unreviewed 880 lines included |
| 13:18:10 | the product manager admits this item on the account that the record held no request |
| 13:19:48 | reconcile's publication refresh records what the forge now says — `state: MERGED, merged: true` — onto the run; that refresh only ever rewrites a record that already carries a `pull_request` (`unsettledPublication` in `internal/orchestrator/publication.go`), so the record held #544 before the hand merge |

The harness did not arm the merge because nothing authorized one. An
escalation approves nothing and integrates nothing; `publishIntegration` runs
only on a promotion, and there was none. "Auto-merge NOT armed by the harness,
publishFailure empty" is the correct record of a run that promoted nothing:
there was no publication to be outstanding. The consequence the product
manager then recorded on 141.4 is the one this shape actually carries — the
hand merge put code on `main` no reviewer had seen.

What was missing was on the docket, not the run: the entry the development
manager decided from said which branch and worktree the run left and not that
a pull request for that branch stood open on the forge. The chat surface
(`yoyo work`, `internal/cli/work.go`) and the run's own summary both name the
request from `state.PullRequest`; the docket's `Artifacts` did not carry it.

## The tracker timeouts

At 10:01Z the product manager's conversation had two `bd` writes time out and
land anyway (`tracker.action.failed` at sequences 10868 and 10870, then the
survey at 10:02 showing both applied). Neither run's `Pull request:` line was
lost to that: both items carry the line the run's completion wrote, and the
run record — a file under the state root written by temp-file-and-rename
(`internal/runstate/store.go`) — is not written through the tracker at all. A
`bd` that times out cannot take a field off a run record.

## Where a request is written, and why it cannot be lost between the phases

`publishAttempt` (`internal/orchestrator/publish.go`) assigns the request to
the outcome and the durable state in one statement and saves the state before
it returns; a save the store refuses fails the run there, so a run cannot reach
its checks with a request the record does not hold. `republishRebase` and
`publishIntegration` rewrite both halves the same way and save. Every other
save between the developer phase and completion — the event sink, the phase
markers, the review verdict — saves the same in-memory record, and the event
sink runs on the invocation's own goroutine before the invocation returns, so
nothing saves a copy taken before the request was assigned. The one path that
loads the record from disk and writes over it, `recordEndingAfterRefusedSave`,
runs only for a terminal save the store refused, writes the ending onto what is
on disk, and the request is already on disk by then.

## What this item changed anyway

The shape the item describes has not occurred, and the code now refuses it in
three places rather than trusting the argument above:

- `publishIntegration` no longer returns quietly over a promotion with no
  request on the record. A publishing run in that state records a
  `Publication outstanding` line naming the branch and saying nothing was asked
  of the forge, which holds the item out of the pull and puts the account on
  it.
- `complete` reads the record back from the store and refuses to complete a
  run whose outcome names a request the record does not hold, or holds as a
  different number; the run is recorded failed with the reason.
- `yoyo reconcile` gains a recovery sweep ahead of the publication refresh:
  a terminal, promoted run whose record says it published and holds no request
  is looked up on the forge by its branch, and the request the forge holds is
  written onto the record — number, URL, state, whether a merge is queued, and
  no merge method, because the run never asked for one. From there the docket,
  the heartbeat, the refresh, and the finishing sweep read it as any other
  publication.
- Every docket entry about a run that published — stopped, escalated, or a
  death — names the pull request beside the branch, so the development manager
  reads the open request where the run's other artifacts are.

What it does not do is arm a merge from reconcile. `ReconcilePullRequests` is
"can only ask, never merge" by contract, and `yoyo triage rearm` repeats a
request the run made rather than making one — a recovered request has no
method to repeat. A merge the run should have asked for and did not is made by
hand or by a first-arm verb still to be admitted; the summary of this run names
that as work for the product manager to decide.

## Tests

`internal/orchestrator/lostpublication_test.go` replays both shapes —
`TestAQueuedMergeIsOnTheRecordTheRunCompletesWith` for 391 and
`TestAnEscalatedRunRecordsThePullRequestItLeftOnTheForge` for 141.3, the
latter asserting the docket entry now names the request — and holds the three
refusals: the completion refusal, the run-side account, and the reconcile
recovery, including a forge that holds no request for the branch.
