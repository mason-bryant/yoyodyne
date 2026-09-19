# yoyodyne-ifd.392: the statuses were moved by `bd update --status=open` in an operator's queue script, not by a merge of the export

Four items had their status moved backwards within three days of 2026-09-18,
none with a note: yoyodyne-ifd.297 and .349, closed on confirmed forge merges on
2026-09-16, read as open again; yoyodyne-ifd.272 and .187, escalated to the
operator, read as released. The development manager's hypothesis was a
line-level merge of `.beads/issues.jsonl` taking the older side for some items.

**The export was never merged.** Nothing in the harness or the repository merges
it: the harness copies the primary checkout's copy into each worktree one way
and holds it out of every change (`internal/gitworktree/exports.go`), the
tracked copy on `main` has not been committed since 2026-09-03 (`git log --
.beads/issues.jsonl`), the primary checkout keeps its copy as an ordinary
uncommitted modification, and no git hook is installed that would import it.
The rewrites came from `bd update <id> --status=open`, run without a note
against the live store by an operator's script outside the repository, ahead of
a triage verb that then refused — correctly — because the decision it was
carrying out had already been carried out. Every one of the rewrites below
matches a firing in that script's own log to the second.

## The writer

`~/.local/yoyodyne/carry-out-queue.sh`, an operator's script, carries out
development-manager triage decisions "one at a time as developer slots free".
Its `fire` function:

```sh
fire() { # $1 item  $2 run
  bd update "yoyodyne-ifd.$1" --status=open >/dev/null 2>&1
  OUT=$(bin/yoyo triage rerun "run-$2" 2>&1 | head -3)
  case "$OUT" in
    *"refused"*|*"Usage"*) ... nohup bin/yoyo triage repair "run-$2" --reason "..." ... ;;
    *) echo "$OUT" >> "$LOG";;
  esac
}
```

The first line moves the item to `open` unconditionally, before the harness is
asked anything, and appends nothing. The comment above the queue says refusals
"cost nothing and explain themselves", which is true of the verb and false of
the line ahead of it: the verb reads its own ledger and refuses a stoppage that
was already re-run, and by then the status has moved.

The queue was written on 2026-09-14/15 for a set of decisions, and carried most
of them out on 2026-09-15 (`queue finished` at 07:46 and a twelve-hour bound
reached at 21:10). It was then started again on 2026-09-18 at 06:50, 07:38,
15:29, 16:21, 16:57, and 17:02 local — the first of them by the operator's
assistant session `69f46a51`, whose transcript shows `(nohup
~/.local/yoyodyne/carry-out-queue.sh >/dev/null 2>&1 &)` at
2026-09-18T13:50:27Z — over the same queue, whose
entries had by then all been carried out and two of them merged. Each restart
walked the list from the top, firing an entry every two minutes.

`~/.local/yoyodyne/yoyodyne-maintenance.sh`, the launchd maintenance job, has the
same line (step 7c, `[ -n "$ITEM" ] && bd update "$ITEM" --status=open`) ahead
of its own repair-then-rerun carry-out of the sweep's `CARRY-OUTS NEEDED` list.
Its carry-outs are written into the sweep reports under
`~/.local/yoyodyne/dm-sweeps/`, and none of the reports from 2026-09-15 on
records one, so it made none of the four rewrites; it is the same mechanism
waiting for the same conditions.

## The rewrites, matched to the firings

Statuses are read from the tracker's export as copied into each developer
worktree the harness cut, which gives a snapshot per cut
(`state/worktrees/yoyodyne/yoyodyne/*/.beads/issues.jsonl`). Firings are from
`~/.local/yoyodyne/carry-out-queue.log`, local time, converted here to UTC. The
export's `updated_at` moved forward on every rewrite and `closed_at` and
`close_reason` were cleared on the two closed items, which is what `bd update
--status=open` does to a closed item; the `notes` field was byte-for-byte
unchanged on every one, which is what the command not carrying a note looks
like.

| item | status before | rewrite (UTC) | queue firing (UTC) | verb's answer |
|---|---|---|---|---|
| 297 | closed 09-15T18:41 (run 89157ed6; PR #509 merged 09-16) | open 09-18T13:52:36, notes unchanged | 13:52:34 firing for 297 | refused: already re-run as run-89157ed6 |
| 349 | closed 09-15T19:29 (run 2ba33b83; PR #510 merged 09-16) | open 09-18T13:57:00, notes unchanged | 13:56:59 firing for 349 | refused: already re-run as run-2ba33b83 |
| 272 | blocked 09-16T00:58 (escalated) | open 09-18T13:59:01, notes unchanged | 13:59:00 firing for 272 | refused: already re-run as run-2f6e6e0a |
| 272 | blocked 09-18T14:00:22 (escalated again by the development manager, turn 491, with its note) | open 09-18T14:47:05, notes unchanged | 14:47:04 firing for 272 (second restart) | refused, as above |
| 187 | blocked 09-18T22:29 (escalated turn 499; handed back for repair turn 504, with its note) | open 09-19T00:12:21, notes unchanged | 00:12:20 firing for 187 (sixth restart) | refused: already re-run at 14:01:02Z |

The same firings moved three more items in the same way and nobody reported
them: yoyodyne-ifd.192 blocked → open at 13:50:32Z (the first entry, four
seconds after the restart), 117.1's `updated_at` bumped at 13:55:00Z with no
other change (it was already open, so the bare `--status=open` changed nothing
but the timestamp), and the 07:38 restart fired on 297 and 349 again at 14:41Z
and 14:45Z while they were still open from the first pass, before the product
manager re-closed both by hand at 21:13Z.

187's firing of 14:01Z on 09-18 was the one time the verb accepted: the
stoppage had not been re-run yet, so `yoyo triage rerun` started run
`04e578ce`, which claimed the item itself. The item did not need to be opened
first for that. The harness's claim clears a stale blocked status on its own and
writes why (`claimPastStaleBlock`, `internal/beads/client.go`, landed 2026-09-07
under yoyodyne-ifd.338), which is a week before the script was written.

Nothing in the harness's own records shows a write at any of those seconds — no
run event, no conversation action, no sweep, no docket entry — which is the
other half of the evidence: a write the harness made would have been recorded,
and this one was made past it.

## Why the hypothesis did not fit

A merge taking the older side would have left `updated_at` at the older side's
value and the notes at the older side's length. Every rewrite moved `updated_at`
forward to the second of the firing and left the notes at the *newer* length,
with the escalation notes of the same day still on the item. That is a write to
the store, not a copy of an older record over a newer one. It is also why the
statuses moved one at a time at two-minute spacing rather than together: a merge
lands at once, and a queue fires on a timer.

Two tests pin what was checked and found not to be the cause, and what was.
`TestAnOlderWorktreeCopyNeverMovesAnItemsStatusBackwards`
(`internal/gitworktree/exports_test.go`) replays the hypothesised shape — 297
closed on its merge in the primary checkout, a worktree cut earlier still
carrying it open, the worktree's change integrated after — and the item is
closed on every path anything reads: the older copy is not promoted, the
target's committed copy is untouched, the primary's copy still says closed, and
the next worktree cut reads it closed.
`TestABareStatusMoveIsRefusedHoweverItIsSpelled`
(`internal/beads/statusguard_test.go`) replays the writer: the script's own line
against yoyodyne-ifd.297, and the ways the same command is otherwise typed, each
refused before it runs.

## What is closed here

`yoyo goals guard` now refuses `bd update <id> --status=...` that carries no
`--append-notes` on the same command, and `bd reopen`, beside the `--notes`
refusal it already makes (`internal/beads/statusguard.go`, wired in
`internal/cli/goals.go`). It decides from the command line alone, as the notes
guard does and for the same reason — a guard that read the item would wait on a
locked store in front of every command an agent runs — so it cannot tell a
backward move from a forward one and asks for a note on every status set there.
`--claim` is not a status set and passes; `bd list --status=` reads and passes;
every status the harness itself sets already carries its account on the same
invocation (`Block`, `Unblock`, `Reopen`, `claimPastStaleBlock`), so the rule
costs the harness nothing.

The rule is recorded in `CLAUDE.md` and `AGENTS.md` beside the notes rule, with
the two lines to type and the one not to.

## What is not closed here, and who closes it

The guard reads the command line of a tool call. A script is a command line the
guard does not see inside: the assistant's session ran
`~/.local/yoyodyne/carry-out-queue.sh`, and the `bd update` was inside it. So
the guard would not have stopped this incident, and it will not stop the next
run of either script. What stops that is the operator, and it is two lines:

- `~/.local/yoyodyne/carry-out-queue.sh`, the `bd update "yoyodyne-ifd.$1"
  --status=open` line in `fire`. Delete it. The re-run verb starts a run that
  claims the item and clears a stale blocked status with a note; the repair
  continuation claims the item the same way. Neither needs the item opened
  first, and a closed item is one neither should be asked about at all.
- `~/.local/yoyodyne/yoyodyne-maintenance.sh`, step 7c, the same line ahead of
  the sweep's carry-outs. Delete it for the same reason.

The queue itself is a list written on 2026-09-15 and restarted six times on
2026-09-18 over entries that were finished; a restart walks it from the top. It
is the operator's to retire, and nothing here does.

## The four items now

Read from the export as copied into this run's worktree at 2026-09-19T05:50Z,
against each item's own record:

- **yoyodyne-ifd.297 — closed.** Correct. Integrated by run 89157ed6, PR #509
  merged 2026-09-16 (`Pull request merged: true` on the item), re-closed by the
  product manager at 2026-09-18T21:13:30Z with the reason on the item.
- **yoyodyne-ifd.349 — closed.** Correct. Integrated by run 2ba33b83, PR #510
  merged 2026-09-16, re-closed at 2026-09-18T21:13:39Z with the reason on the
  item.
- **yoyodyne-ifd.272 — open, and its record says blocked.** The last status the
  harness wrote was `blocked`, at 2026-09-18T14:00:22Z, when the development
  manager escalated it (turn 491; the note is on the item). The queue reopened
  it at 14:47:05Z with no note. The development manager's later decision at
  turn 529 (2026-09-19T03:10Z) handed it back for one bounded repair; that
  decision is recorded in the notes and does not set the status — the repair
  continuation claims the item when it re-enters, and would have cleared a stale
  blocked status with a note as it did. So the status the record supports is
  `blocked` until that continuation claims it, and `open` is the queue's. It is
  not corrected here: a developer run cannot write to the tracker, and the
  correction is a `bd update yoyodyne-ifd.272 --status=blocked
  --append-notes=...` naming this diagnosis, for whoever holds the item's
  decision — or nothing at all, if the repair continuation is about to claim it.
- **yoyodyne-ifd.187 — open, and its record says blocked.** Same shape. The
  harness's last status write was `blocked` (escalated at turn 499, 15:58Z;
  handed back for repair at turn 504, 22:29Z, note on the item, status left
  blocked). The queue reopened it at 2026-09-19T00:12:21Z with no note. The same
  correction applies, with the same alternative.

Neither open status has cost a run yet: no run of 272 or 187 begins after the
rewrites (the run records hold none), and the session choosing work was passing
both over on the harness's own records — "awaiting a decision" and "awaiting
carry-out of a decision" — at 13:50Z, before the first firing. What the open
statuses do cost is exactly what the item names: a blocked status cannot be
relied on while a script outside the repository can clear it without a word.
