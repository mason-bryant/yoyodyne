# Sweep: every renderer of a run's status, and what each says

Work item: yoyodyne-ifd.226. **This document is the coverage half of that item.**
Its done condition is "no surface prints one word for four outcomes, verified by
the grep's recorded site list", and this is that list: the greps as run, every
site each returned, and what was done about it. The code half is in the same
change.

Swept at 2026-08-31 against the working tree of
`yoyodyne/yoyodyne-ifd-226/adbd416c`, at the commit the change is written on.

## What is being looked for

`ifd.173` gave the harness a fixed vocabulary for what became of a run, because
the durable status answers a different question from the one a listing is read
for. `failed` is accurate about the attempt and says nothing about the work, so a
run whose change is preserved and whose item is back with a person printed the
same word as one that was cancelled and one that broke with nothing to show for
it. The vocabulary is `runstate.RunOutcome` — succeeded, stopped, cancelled,
timed out, failed — with what remains of the change stated beside it in three
fixed phrases: work preserved, work removed, no artifacts recorded.

That landed on the three surfaces the patch touched: `yoyo status`, `yoyo cost`,
and the chat price breakdown. The reviewer on that run could not check the
others. This sweep is the check.

## Grep 1 — renderers of `RunPrice.Status`

```
grep -rn "RunPrice" --include="*.go" internal cmd | grep -v "_test.go"
```

17 sites, in four files. **No site outside `internal/cli` and `internal/chat`
renders it, and none needed changing.**

| Site | What it is | Disposition |
| --- | --- | --- |
| `internal/runstate/price.go:242,278,285,494–495` | the type itself, its `Known()` predicate, and the store method that builds one | **Producer, not a renderer.** This is the read model. It already carries `Outcome` beside `Status`, set from `State.Outcome()` |
| `internal/cli/cost.go:320,580,593` | `yoyo cost`'s per-attempt line and `renderRunOutcome` | **Already aligned by ifd.173**, and inside `internal/cli`, which the item's scope excludes. `renderRunOutcome` prints `run.Outcome`, never `run.Status` |
| `internal/cli/work.go:436` | projects `runstate.RunPrice` onto `chat.RunPrice` | **Already aligned by ifd.173**: it copies `string(run.Outcome)`, not the status |
| `internal/chat/work.go:150–159,691,708,724,733` | the chat projection and its `outcome()` renderer | **Already aligned by ifd.173**, and inside `internal/chat`, which the item's scope excludes |

`runstate.RunPrice.Status` is read by nothing at all outside the package that
declares it: every consumer reads `Outcome`. That is why this change contains no
`RunPrice` edit — there was no site to fix, rather than a site that was missed.

## Grep 2 — renderers of `RunSummary.Status`

```
grep -rn "RunSummary" --include="*.go" internal cmd | grep -v "_test.go"
```

17 sites, in two files. **No site outside `internal/cli` and `internal/chat`
reads the type at all.**

| Site | What it is | Disposition |
| --- | --- | --- |
| `internal/runstate/summary.go:147,244,247,258,261,281,299,333–334` | the type, its predicates, and the store method that builds one | **Producer, not a renderer.** `Artifacts()` and `Describe()` were added here by this change, so the three preservation phrases have one home |
| `internal/cli/status.go:61,503,582,646,698,719,731` | `yoyo status`'s run listing | **Aligned by this change.** `renderRunState` no longer carries its own copy of the three phrases; it reads `run.Artifacts().Describe()` |

## Grep 3 — everything else that reads a run's durable status

The two greps above are what the item named, and neither found a site outside
`internal/cli` and `internal/chat`. That is a true answer to the question as
asked and a misleading one about the risk, because the surfaces the item was
raised about — the Slack sink, the recovery view — do not hold a `RunSummary` or
a `RunPrice`. They hold a `runstate.State` and classify it themselves. So the
sweep was widened:

```
grep -rn "\.Status\b" --include="*.go" internal cmd \
  | grep -v "_test.go" \
  | grep -vE "^internal/(runstate|cli|chat)/" \
  | grep -E "runstate\.|state\.Status\.|prior\.Status|r\.Status" \
  | grep -v "execution\."
```

The last filter drops `execution.ProcessStatus`, an unrelated type that shares
the field name and accounts for roughly forty of the raw hits.

| Site | What it is | Disposition |
| --- | --- | --- |
| `internal/notify/select.go:639` (`endedBadly`) | what decides a run has ended without succeeding | **Aligned.** It reports only that the run ended; which of the four endings it was is `State.Outcome()`'s to say. Before this change the same predicate also required a failure string and posted every ending as `blocker.recorded` — one word over four outcomes, in the surface the operator reads most |
| `internal/notify/status.go:107–108` (`StatusOfRun`) | the emoji mark on a thread's opening message | **Needs nothing, deliberately.** This is a separate ratified four-word set — working, in-review, blocked, completed — answering what the item is *doing*, not what became of a run. Its own comment says the set is fixed and that anybody extending it "becomes a second severity system nobody ratified". For all four endings, "stopped and stays stopped" is true, and it claims nothing about the work; the message beside it now names the ending. Widening it is the architect's call, not this item's |
| `internal/orchestrator/reconcile.go:137` (`Reconciliation.Status`) | the recovery view's per-run record | **Aligned.** It now carries `Outcome`, `Branch`, and `WorktreePath` beside the status, and `internal/cli/reconcile.go` prints the outcome and what remains under each run the sweep settled into an ending that is not success |
| `internal/orchestrator/reconcile.go:670–672,802,813–815,863` | the sweep deciding and writing a terminal status | **Writer, not a renderer** |
| `internal/slack/feed.go:250,810`, `internal/slack/heartbeat.go:344` | `Terminal()`, used to pick which runs are live and which are settled | **Needs nothing.** Renders no word about a run |
| `internal/slack/feed.go:370` (`itemStatuses`) | folds run states to one `notify.Status` per item | **Needs nothing** — same reasoning as `StatusOfRun`, whose answer it carries |
| `internal/orchestrator/pipeline.go` (24 sites) | assignments, pause predicates, and `failureStatus` / `statusForContext` / `statusForProcess` | **Writers and control flow, not renderers** |
| `internal/orchestrator/triage.go:327,352` | selects the runs that stopped on a durable blocker for the docket | **Needs nothing.** It reads `State.Blocker`, which is the same field `Outcome()` reads to answer "stopped", so the docket and the vocabulary already agree |
| `internal/orchestrator/rerun.go:388–390,501` | refusal messages when a re-run is asked for | **Needs nothing.** These quote the status to say a run is *not* ended — "recorded as `running` rather than ended" — which is a statement about resumability, not a verdict on the work |
| `internal/orchestrator/repaircontinue.go:632` | sets a continued run back to running | **Writer, not a renderer** |

## What the sweep leaves standing

One collapse survives on purpose: `notify.StatusOfRun` still marks a thread
`blocked` for all four endings, for the reason in its row above. If that is the
wrong call, the four-word status set is the thing to change and the architect
owns it; this item did not widen it unilaterally.

One gap is named but not closed, because it is outside this item: the triage
docket and `yoyo status`'s reason block both render "why did this stop" from
`State.Failure`, and `Reconciler.saveTerminalFailure` skips writing that field on
a run that was already terminal. Those surfaces show an empty reason for a
stoppage reconciliation settled late. The Slack lines state the absence rather
than trailing off; the other two were not checked.
