# yoyodyne-ifd.275: the preserved work that was never lost, and the report that said it was

A developer on run `run-e83e13a5` — work item yoyodyne-ifd.264, 2026-09-04 —
reported that the failure record of the previous run of the same item
(`run-48216ea91d8aa3aef2530d87e8d614f7`, branch
`yoyodyne/yoyodyne-ifd-264/48216ea9`) said the branch and worktree were preserved
and listed ~885 lines of uncommitted changes across 23 files, and that neither
existed: the worktree directory was gone, no ref matched, and the work was
unrecoverable. ifd.275 was admitted on that report, asking whether preservation
had failed for the death class `process output exceeded 8388608 bytes` or whether
cleanup had removed the artifacts despite the failure record.

**Neither. Preservation ran, it survived cleanup, and nothing was lost.** The 23
files are on `refs/yoyodyne/preserved-work/run-48216ea91d8aa3aef2530d87e8d614f7`
at commit `32bbeece65b7fcebdacf4e051b8801073c7ed811` — 23 files changed, 1551
insertions, 78 deletions — where the convergence sweep put them six hours before
the developer went looking. What failed was the route from what that developer
read to where the work was. This is the record of how that was established.

## What actually happened, in order

From `run-48216ea91d8aa3aef2530d87e8d614f7.json` in the run store, and from the
code each step runs:

1. **2026-09-03T19:51:12Z — the developer backend died.** `failure` on the record
   is `developer backend failed: run Claude Code: process output exceeded
   8388608 bytes`; `status` is `failed` and `phase` is `developing`. The bound is
   `defaultMaxOutputBytes` in `internal/execution/process.go`.
2. **The run wrote its ending onto the work item.** `activeRun.fail` in
   `internal/orchestrator/pipeline.go` records the terminal state and then calls
   `Tracker.RecordOutcome` with `renderFailureNotes`. That note is the one the
   developer read. Before this item it opened with *"Yoyodyne bootstrap run
   failed; branch and worktree are preserved when present"*, named the branch and
   the worktree, and labelled the change summary **`Preserved changes:`** — every
   one of those from the record alone. Nothing looked at the repository.
3. **Nothing cleaned up, because nothing could.** A failed run reaches no
   cleanup: `CleanupIntegrated` runs only after a promotion, and
   `removeIntegratedWorktree` refuses a dirty worktree outright
   (`refusing to remove a dirty worktree`). The checkout and the branch stood
   exactly as the note said, for about six hours.
4. **2026-09-04T01:41:39Z — the convergence sweep retired the checkout.** The run
   was settled and past `settledWorktreeTail`, so `Reconciler.sweepWorktree`
   called `RemovePreservedWorktree(..., CaptureUncommittedWork)`.
   `capturePreservedWork` staged the tree, wrote it with `commit-tree`, pointed
   `refs/yoyodyne/preserved-work/<run id>` at it, **read the ref back and
   compared it to the commit** — and only then removed the directory. The run's
   record carries the result: `worktree_removed: true`,
   `worktree_swept_at: 2026-09-04T01:41:39.789522Z`,
   `preserved_work_ref: refs/yoyodyne/preserved-work/run-48216ea91d8aa3aef2530d87e8d614f7`.
5. **The branch was deleted, legitimately.** The run died in `developing` and
   made no harness commit, so its branch stood at the base commit
   `c3e9083cfe7979174a914a965bb2a26760e6c8cd`, which `main` contains.
   `RemoveMergedBranch` proves containment in the repository before it deletes,
   so the deletion could lose nothing — every commit the branch carried was
   already in the target.

So the death class does preserve. What the report described as destroyed work was
a stale note plus a branch deletion that was correct.

## Why the developer could not find it

Three gaps, and each of them is now closed:

- **The note claimed preservation without checking.** It said "preserved" because
  `worktree_removed` and `branch_removed` were false, which records what *the
  harness removed* rather than what is there. The run now looks at both before it
  writes — `Observe` for the branch ref and the worktree directory — and the note
  says `(checked and there)` or `(checked and NOT there)`, or that the check
  could not be made. Nothing in the note claims preservation from the record any
  more. The removal flags are still read, for the case that runs the other way:
  an artifact this run's own cleanup removed is `(removed by this run's cleanup)`
  and is never looked for, because reporting an integrated-and-cleaned-up branch
  as lost would send a reader after a preserved-work ref that does not exist.
- **The capture never reached the work item.** `recordSweptWorktree` wrote the
  ref onto the run's own state file and stopped there. The item — the thing a
  person picking the work up actually reads — went on carrying only the failure
  note from step 2, naming a directory that no longer existed. The sweep now
  writes the ref onto the item as well, with the
  `git worktree add --detach <path> <ref>` that opens it.
- **The branch deletion was recorded nowhere.** `sweepBranch` deleted the branch
  and wrote nothing, so `Artifacts.Preserved()` — which asks `branch_removed` and
  nothing else — kept answering *yes* for a run with neither artifact left.
  `run-48216ea9` was still listed as `work preserved` while this was being
  diagnosed. The sweep now takes the run's lease and records `branch_removed` and
  `branch_swept_at`, which the durable schema admits as the fourth way a removal
  is earned.

## What this does not change

The uncommitted work of a run that dies is captured by the sweep and not at the
moment of death, so it moves when the checkout is retired rather than when the
run ends. That is deliberate and it is what the tail of held-back checkouts is
for: the recent stoppages keep their directories, so `/continue` and a repair
have something to re-enter. Nothing in that window is at risk — the directory is
there, and the capture is proven to carry the tree before the directory goes.

The one thing worth watching is that this record is only as good as the sweep
running. A checkout retired by anything else — an operator's
`git worktree remove --force --force`, an external prune — loses whatever was
uncommitted in it, and no ref is written. The sweep records that case as a
checkout it found already gone, which corrects the run's record but cannot
recover a tree nobody captured.
