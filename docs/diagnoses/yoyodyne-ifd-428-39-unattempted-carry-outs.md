# yoyodyne-ifd.428.39: the re-runs recorded for 192 and 187 named stoppages already re-run, and the sweep read the old claim as the new decision carried out

On 2026-09-19 the development manager recorded a re-run of yoyodyne-ifd.192 (the
operator-DM tier, at p0) and of yoyodyne-ifd.187. For a week neither fired, and
neither item's triage record nor docket entry said why. The carry-out writes
every refusal onto both, so a decision that neither fired nor refused was one the
pass never handed to an action.

**That is what happened.** Each decision was recorded against the item's
*original* stoppage, and that stoppage's one re-run had already been claimed by
an earlier decision. The sweep that finds outstanding decisions
(`CarryOut.Outstanding` in `internal/orchestrator/carryout.go`) treated any claim
on the stoppage as the decision having been carried out, whenever the claim was
made. So it offered neither decision to the pass. No action ran, so no gate
refused and nothing was written.

## What the records say

All times are UTC and come from the product's state directory: the items' triage
records under `triage/`, the re-run claims under `reruns/`, the docket in
`docket.jsonl` and `docket-closed.jsonl`, and the runs under `runs/`.

### yoyodyne-ifd.192

| When | What |
| --- | --- |
| 2026-08-30 15:23:44 | `stopped_run:run-ce3a113500eacda7a1f23bf088b07eea` docketed (target branch diverged). |
| 2026-09-15 06:08:11 | Re-run decided about run-ce3a…, conversation chat-419cedb4…, turn 456. |
| 2026-09-15 06:10:25 | That re-run claimed (`reruns/stopped-run-run-ce3a…`), starting run-fd2caa2a225c29b8261852e928fd1488. |
| 2026-09-15 14:01:40 | run-fd2c… stops (review still required repair after 2 of 2), docketed as `stopped_run:run-fd2c…`. |
| 2026-09-19 16:17:36 | Development manager crosses the re-run cap (2) and the review-round cap (7). |
| 2026-09-19 16:17:56 | Re-run decided **about run-ce3a…** again, turn 574. The docket entry for run-ce3a… is closed at 16:17:58. |

The record then held `reruns: 2` and one claim. The latest stoppage, run-fd2c…,
had no decision recorded about it.

### yoyodyne-ifd.187

| When | What |
| --- | --- |
| 2026-08-30 07:08:24 | `stopped_run:run-af66cb33c6b35cd4278cd60b34c0208b` docketed. |
| 2026-09-15 06:08:18 | Re-run decided about run-af66…, turn 456. |
| 2026-09-18 14:01:02 | That re-run claimed (`reruns/stopped-run-run-af66…`). The claim records no fresh run, although run-04e578ce4c1ca86a0be4e6481570b85d started for the item one second later. |
| 2026-09-18 22:29:12 | Repair decided about run-04e5…, which had died in developing ("list worktrees failed"). run-04e5… was never docketed as a stopped run. |
| 2026-09-19 16:18:00 | Re-run decided **about run-af66…** again, turn 574. The docket entry is closed at 16:18:01. |
| 2026-09-19 17:01:19 | run-04e5… ends cancelled. |

## Why the pass passed them over

`Outstanding` walked the docket's `stopped_run` entries. For each entry it read
the decision recorded about that entry's run, and for a re-run it asked
`rerunOutstanding`. That function returned *not outstanding* as soon as it found
a claim carrying the entry's key. It never compared when the claim was made with
when the decision was made. For run-ce3a… and run-af66…, the September 15 claims
answered the September 19 decisions, so neither decision became a task. For
run-fd2c… there was no decision to find. The repair on run-04e5… had no
`stopped_run` entry leading to it, so the walk never reached it.

Nothing downstream could make up for this. A decision reaches a gate only when
the pass hands it to `Rerunner` or `RepairContinuer`, so these two were never
refused. Had they been handed over, `Rerunner.decided` would have refused both
immediately with `RerunTakenError` ("the stoppage of run … was already re-run
…; triage re-runs a docketed stoppage once"), and `CarryOut.stopped` would have
written that refusal onto each item.

Both docket entries had also been closed by the September 19 decision. An entry
closed with no carry-out finding newer than the closure is not undecided
(`Entry.Undecided`), so it dropped out of what the development manager is shown.
The silence therefore reached her docket as well as the item's record.

The three causes the item suggested, checked against this:

- **Entries fallen outside the window.** No. The carry-out reads the whole
  docket through `DocketStore.List`, and the window position
  (`docket-window.json`) governs only what her conversation is offered. What
  took the entries off her docket was the closure described above.
- **Items not offered by the pass.** Yes. This is the cause.
- **Decisions recorded in a shape the carry-out does not read.** In part. Each
  decision was well-formed. It named a stoppage whose one re-run was already
  spent, and the carry-out read that as done rather than as something to
  attempt and refuse.

## What changed

- **A re-run decided after the stoppage's claim is attempted.** A claim answers
  a decision only when the claim came after the decision. A later decision is
  offered to the pass while it is the item's latest decision. The re-run action
  then refuses it, and the refusal on the item names the item's latest stoppage
  as where the decision belongs. A decision she has since decided past is not
  offered again, so a stoppage she moved on from is not refused forever.
- **Every docketed item's latest decision is read, whatever run it names.** A
  decision about a run no `stopped_run` entry stands for, such as 187's repair
  on run-04e5…, is now reached. If the action cannot act on it, it is refused
  on the record rather than never visited.
- **A pull attempts every decision it has a developer slot for,** bounded by a
  session's `--limit`. Before, it fired the oldest one only.
- **A decision is never silently unattempted.** A decision still standing one
  poll interval after it was recorded, with nothing attempted since, is written
  onto the item's triage record as unattempted (`TriageCarryOut.Unattempted`),
  saying what kept it back and what clears it. Three things can keep it back: a
  run of the item in flight, the pull's slots going to decisions ahead of it,
  or a record that disagrees with itself about its re-runs. The record joins the
  docket entry, which puts it ahead of the development manager's walk, and
  `yoyo status` counts it on its held-work line beside the refused decisions.

Run read-only against the product's state as it stood on 2026-09-26, the new
sweep offers both decisions: yoyodyne-ifd.192's about run-ce3a… and
yoyodyne-ifd.187's about run-af66…. It holds back nothing else. So the first
pull made by a build carrying this change attempts both, and the re-run action
refuses both on the record.

## What is left for the development manager

For 192, the decision belongs on run-fd2c…, the stoppage the item's last run
made. Recording the re-run there is what fires it.

187 has no such stoppage to decide against. Its last run, run-04e5…, was
cancelled rather than docketed, so there is no `stopped_run` entry for it, and
the re-run action carries out decisions only about docketed stoppages (it
refuses with "no stopped run of … is on the triage docket"). Until a run of 187
stops and is docketed, no triage decision about its latest run can move it. That gap is reported with this
change as work to be admitted. It is not fixed here.
