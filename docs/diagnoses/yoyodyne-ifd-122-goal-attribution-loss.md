# Diagnosis: attributions recorded earlier no longer count as naming a goal

Work item: yoyodyne-ifd.122. Diagnosis-class: read-only against the records,
changing nothing. Migration, backfill, and repair are explicitly out of scope
and none was performed; re-attributing over an undiagnosed bug would have
destroyed the evidence this rests on.

Records read at 2026-08-20T05:36Z, against the live tracker export
(`/Users/mbryant/github/yoyodyne/.beads/issues.jsonl`, 178 rows, latest
`updated_at` 2026-08-20T05:22:18Z), the harness's conversation event logs under
`state/products/yoyodyne/conversations/`, and the Claude Code session
transcripts under `~/.claude/projects/`. Commands and outputs are in the
evidence appendix at the end.

## Summary

Records were lost without being counted as lost. Counting did not move to a
structured field: an attribution still lives on the `Goal served:` line in a
work item's notes and is still read from there and nowhere else. Twelve items
had a goal recorded and no longer name one, each destroyed by a
`bd update <id> --notes="…"` run directly from an agent session — a flag that
replaces the notes field wholesale rather than appending to it. The losses are
uncounted because every one of them predates the tracker witness that makes a
loss visible, and the backfill sweep that would cover older attributions has
never been run. All twelve goal statements are recoverable verbatim.

## The mechanism

An attribution is one line in an item's notes, `Goal served: <statement>`
(`internal/goal/goal.go:87`, read back by `goal.NamedIn`). Notes are therefore
the record, and a writer that replaces them takes the attribution with them.
This is the failure the package already documents in its own header — it
happened once before, to six items at once — and it has happened again.

The harness itself never destroys an attribution. `beads.Client.Update` passes
only `--append-notes` (`internal/beads/client.go:322`); `Create` passes
`--notes` on an item that does not yet exist. Every loss below came from
outside those paths: raw `bd update <id> --notes="…"` invocations in Claude
Code sessions rooted at `/Users/mbryant/github/yoyodyne` (one in a worktree
session), each visible in the session transcripts as a `Bash` tool call whose
timestamp matches the item's `updated_at` in the tracker.

The other candidate mechanism does not hold. yoyodyne-ifd.108 did not
restructure where a goal lives on an item: it left the notes as the record and
added `yoyodyne_goal_recorded`, a tracker-metadata *witness* that a goal was
written, precisely so a destroyed attribution could be told from one nobody
ever made. Nothing about the reading path changed, and no note-based
attribution stopped counting because of it.

## Why the "recorded a goal and lost it" counter reads zero

`StateLost` is decided from the witness: an item whose notes state no goal is
`lost` only where the tracker witnesses one was written onto it, and
`unattributed` otherwise (`goal.Set.AttributionOf`,
`internal/goal/goal.go:597`). The witness shipped with ifd.108, merged to main
at 2026-08-19 15:43 -0700; the earliest item carrying one was created
2026-08-20T01:31:58Z. Every loss below predates that.

The gap this leaves is closed by a sweep, `yoyo goals witness`
(`internal/cli/goals.go:192`), which copies the goal an item's notes already
state into the metadata where a careless writer cannot reach it. That sweep has
never been run: 65 items' notes state a goal and 8 carry a witness, and all 8
were witnessed by the post-fix harness as it created or attributed them. So 57
live attributions are still unprotected, and every loss so far decayed into
`unattributed` — the one state that is deliberately reported without failing.

The counter is behaving exactly as designed and is nonetheless reporting
nothing. Both halves of the discrepancy are the same fact seen from two sides.

## Affected items

### Twelve items had a goal recorded and no longer name one

Each is matched to the specific command that destroyed it. Nothing is
unexplained. Times are UTC.

| Item | Goal recorded | Notes replaced by `bd update --notes` |
| --- | --- | --- |
| yoyodyne-ifd.1.1 (closed) | t14.1, 2026-08-18T04:02:30 | 2026-08-18T04:03:41 |
| yoyodyne-ifd.1.6 (closed) | t14.6, 2026-08-18T04:02:34 | 2026-08-18T23:19:09 |
| yoyodyne-ifd.1.9 (closed) | t14.9, 2026-08-18T04:02:37 | 2026-08-18T23:19:09 |
| yoyodyne-ifd.4 (closed) | t25.7, 2026-08-18T10:03:18 | 2026-08-19T03:42:47, 04:42:08, 05:15:56 |
| yoyodyne-ifd.25 (closed) | t25.6, 2026-08-18T10:03:17 | 2026-08-19T00:55:28, 23:57:20, 23:58:44 |
| yoyodyne-ifd.3 (closed) | t25.9, 2026-08-18T10:03:20 | 2026-08-19T19:43:38 |
| **yoyodyne-ifd.45 (open)** | t26.3, 2026-08-18T10:04:08 | 2026-08-19T04:23:53 |
| yoyodyne-ifd.68.3 (closed) | t58.4, 2026-08-18T22:53:08 | 2026-08-19T15:57:17, 15:57:43 |
| yoyodyne-ifd.82.1 (closed) | t70.1 / t76.2, 2026-08-19T04:24:41 | 2026-08-19T15:15:06 |
| **yoyodyne-ifd.82.2 (open)** | t70.2 / t76.3, 2026-08-19T04:24:42 | 2026-08-19T04:50:37, 23:55:19 |
| **yoyodyne-ifd.99 (open)** | t83.1, 2026-08-19T14:37:06 | 2026-08-19T14:40:22, 14:41:42 |
| yoyodyne-ifd.104 (closed) | t89.4, 2026-08-19T15:45:53 | 2026-08-19T16:15:15 |

Every replacement is later than the recording it destroyed, and none of the
replacing commands carried a `Goal served:` line of its own. The three items
the work item named — ifd.45 (t26.3), ifd.99 (t83.1), ifd.82.2 (t76.3) — are
all here, confirmed as destroyed records rather than gaps.

### The rest of the survey never had a goal

Twenty-three open or in-progress items name no goal (the survey's 21 plus two
admitted since). Three are the lost records above. The other twenty have empty
notes, appear in no conversation's `create` or `attribute` action, and were
filed by direct `bd create` outside the product manager's admission path:

yoyodyne-ifd (root epic), ifd.75, ifd.76, ifd.77, ifd.78, ifd.79, ifd.81,
ifd.83, ifd.84, ifd.85, ifd.89, ifd.91, ifd.92, ifd.93, ifd.96, ifd.110,
ifd.111, ifd.112, ifd.115, ifd.120.

These are honestly `unattributed` and correctly grandfathered. Nobody's record
was destroyed on them, and what they need is the product manager's judgement,
not a restoration.

## Recoverability

**All twelve are recoverable.** Each goal statement survives verbatim, with the
reason the product manager gave for it, in the `tracker.action.applied` payload
in `state/products/yoyodyne/conversations/chat-91253e0e070c17b0663651cc48602122.events.jsonl`.
Each recovered statement still appears verbatim in the goals document in force,
`docs/product/goals/v1-goals.md`, so putting one back would resolve rather than
land unresolved. Restoration is reading the record, not judging the work again.

The three open items and the statements to put back:

- **yoyodyne-ifd.45** — "Run development nearly autonomously. The human's
  routine interface is the product manager: they state intent, approve the
  brief and goals, and answer questions the product manager escalates.
  Directing the architect, development manager, developer, or reviewer
  individually is available for inspection, recovery, and override, but is not
  part of the normal loop."
- **yoyodyne-ifd.82.2** — "A team can run Yoyodyne against one shared
  repository: collaborators each run their own harness without losing work,
  splitting the tracker, or weakening any safety invariant."
- **yoyodyne-ifd.99** — "Let configurable agent roles collaborate without
  allowing downstream agents to silently redefine upstream intent."

The nine closed items recover the same way: ifd.1.1, ifd.1.6 and ifd.1.9 to the
traceable-chain goal; ifd.3 to the worktree-isolation goal; ifd.4, ifd.25 and
ifd.68.3 to the near-autonomous-development goal; ifd.82.1 and ifd.104 to the
shared-repository goal.

One partial caveat: the *rest* of each item's pre-overwrite notes — the
provenance block that sat above the goal line — is not in the event log. It is
reconstructible in substance from the create and attribute payloads, and the
Beads Dolt database's own history should hold the literal prior value. That
last part is unverified; see the limitation below.

## Limitation on this diagnosis

The Beads database could not be queried directly. `bd` fails here with
`openat LOCK: operation not permitted`, because this run's sandbox denies
writes under `/Users/mbryant/github/yoyodyne/.beads` and `bd` takes a lock even
to read. The findings rest on the auto-exported `issues.jsonl`, the harness
conversation event logs, and the session transcripts, which agree with each
other. The only thing the database would add is the literal pre-overwrite notes
text, which whoever performs the restoration may want and should retrieve by
running `bd` outside a sandbox.

The transcripts are one channel; a `bd update --notes` typed by a person
directly into a terminal would leave no transcript. This does not weaken the
list above, because every one of the twelve losses is already matched to a
recorded command, but it means the transcripts cannot be used to prove no other
writer exists.

## Evidence

All commands are read-only. Paths are as they were run.

### 1. Which open items name no goal, and which of those ever had one

Cross-references every applied `create`/`attribute` action carrying a goal,
across all conversation logs, against the live export.

```
$ cd state/products/yoyodyne/conversations && python3 - <<'EOF'
import json,glob,datetime,os
export='/Users/mbryant/github/yoyodyne/.beads/issues.jsonl'
recorded={}
for p in sorted(glob.glob('chat-*.events.jsonl')):
    for l in open(p):
        r=json.loads(l)
        if r['type']!='tracker.action.applied': continue
        a=r['payload'].get('action',{})
        if a.get('action') not in ('create','attribute'): continue
        wid=a.get('id') or r['payload'].get('work_item_id')
        if wid and a.get('goal'):
            recorded.setdefault(wid,[]).append((r['timestamp'][:19], a['action'], r['payload'].get('action_id'), a['goal']))
rows=[json.loads(l) for l in open(export)]
def named(n):
    out=None
    for line in (n or '').split('\n'):
        s=line.strip()
        if s.startswith('Goal served:') and s[12:].strip(): out=s[12:].strip()
    return out
openish=[r for r in rows if r['status']!='closed']
nog=[r for r in openish if not named(r.get('notes'))]
lost=[r for r in nog if r['id'] in recorded]
never=[r for r in nog if r['id'] not in recorded]
...
EOF

export mtime 2026-08-20T05:36:04Z | rows 178 | max updated_at 2026-08-20T05:22:18Z
open/in_progress 52 | naming no goal 23 | of those LOST 3 NEVER-ATTRIBUTED 20
lost(open): ['yoyodyne-ifd.99', 'yoyodyne-ifd.45', 'yoyodyne-ifd.82.2']
never(open): ['yoyodyne-ifd', 'yoyodyne-ifd.110', 'yoyodyne-ifd.111', 'yoyodyne-ifd.112',
 'yoyodyne-ifd.115', 'yoyodyne-ifd.120', 'yoyodyne-ifd.75', 'yoyodyne-ifd.76',
 'yoyodyne-ifd.77', 'yoyodyne-ifd.78', 'yoyodyne-ifd.79', 'yoyodyne-ifd.81',
 'yoyodyne-ifd.83', 'yoyodyne-ifd.84', 'yoyodyne-ifd.85', 'yoyodyne-ifd.89',
 'yoyodyne-ifd.91', 'yoyodyne-ifd.92', 'yoyodyne-ifd.93', 'yoyodyne-ifd.96']
lost(all statuses) 12 ['yoyodyne-ifd.1.1', 'yoyodyne-ifd.1.6', 'yoyodyne-ifd.1.9',
 'yoyodyne-ifd.104', 'yoyodyne-ifd.25', 'yoyodyne-ifd.3', 'yoyodyne-ifd.4',
 'yoyodyne-ifd.45', 'yoyodyne-ifd.68.3', 'yoyodyne-ifd.82.1', 'yoyodyne-ifd.82.2',
 'yoyodyne-ifd.99']
items whose notes state a goal: 65 | items carrying a witness: 8
 ['yoyodyne-ifd.122', 'yoyodyne-ifd.119', 'yoyodyne-ifd.121.2', 'yoyodyne-ifd.121.1',
  'yoyodyne-ifd.121', 'yoyodyne-ifd.68.6', 'yoyodyne-ifd.118', 'yoyodyne-ifd.117']
```

The eight witnessed items were created between 2026-08-20T01:31:58Z and
04:43:14Z — all after ifd.108 reached the running binary, which is the whole of
why the loss counter is zero.

### 2. The destroying command, in the session transcript

Searching the transcripts for the note text now sitting on ifd.45 finds the
write that replaced its notes, at 2026-08-19T04:23:53Z — matching ifd.45's
`updated_at` of 2026-08-19T04:23:55Z, and 18 hours after t26.3 recorded its
goal.

```
$ cd ~/.claude/projects/-Users-mbryant-github-yoyodyne
$ grep -l "Fresh evidence 2026-08-18" *.jsonl
05c7acac-b4a3-4630-be58-d6cf91d690c3.jsonl

  line 8210  2026-08-19T04:23:53.369Z  assistant
  {"message": {"role": "assistant", "content": [{"type": "tool_use", "name": "Bash",
    "input": {"command": "bd update yoyodyne-ifd.45 --notes=\"Fresh evidence 2026-08-18:
    a chat --message turn failed outright with 'product manager reported failure:
    api_error' where a run would have waited and retried. ...\""}}]}}
```

### 3. Every lost item matched to its overwrite

Scans all session transcripts for `bd update <id> --notes` (excluding the safe
`--append-notes` form) and reports whether the replacing notes carried a goal
line of their own.

```
$ cd ~/.claude/projects && python3 - <<'EOF'   # regex over Bash tool_use commands
...
EOF

yoyodyne-ifd.1.1    overwritten 2026-08-18T04:03:41.111Z  goal-line-in-new-notes=False
yoyodyne-ifd.1.6    overwritten 2026-08-18T23:19:09.286Z  goal-line-in-new-notes=False
yoyodyne-ifd.1.9    overwritten 2026-08-18T23:19:09.286Z  goal-line-in-new-notes=False
yoyodyne-ifd.1.9    overwritten 2026-08-19T00:27:05.720Z  goal-line-in-new-notes=False
yoyodyne-ifd.3      overwritten 2026-08-19T19:43:38.286Z  goal-line-in-new-notes=False
yoyodyne-ifd.4      overwritten 2026-08-16T04:00:40.565Z  goal-line-in-new-notes=False
yoyodyne-ifd.4      overwritten 2026-08-19T03:42:47.585Z  goal-line-in-new-notes=False
yoyodyne-ifd.4      overwritten 2026-08-19T04:42:08.773Z  goal-line-in-new-notes=False
yoyodyne-ifd.4      overwritten 2026-08-19T05:15:56.316Z  goal-line-in-new-notes=False
yoyodyne-ifd.25     overwritten 2026-08-16T23:16:48.188Z  goal-line-in-new-notes=False
yoyodyne-ifd.25     overwritten 2026-08-19T00:55:28.617Z  goal-line-in-new-notes=False
yoyodyne-ifd.25     overwritten 2026-08-19T23:57:20.465Z  goal-line-in-new-notes=False
yoyodyne-ifd.25     overwritten 2026-08-19T23:58:44.776Z  goal-line-in-new-notes=False
yoyodyne-ifd.45     overwritten 2026-08-19T04:23:53.369Z  goal-line-in-new-notes=False
yoyodyne-ifd.68.3   overwritten 2026-08-19T15:57:17.143Z  goal-line-in-new-notes=False
yoyodyne-ifd.68.3   overwritten 2026-08-19T15:57:43.155Z  goal-line-in-new-notes=False
yoyodyne-ifd.82.1   overwritten 2026-08-19T15:15:06.086Z  goal-line-in-new-notes=False
yoyodyne-ifd.82.2   overwritten 2026-08-19T04:50:37.720Z  goal-line-in-new-notes=False
yoyodyne-ifd.82.2   overwritten 2026-08-19T23:55:19.616Z  goal-line-in-new-notes=False
yoyodyne-ifd.99     overwritten 2026-08-19T14:40:22.188Z  goal-line-in-new-notes=False
yoyodyne-ifd.99     overwritten 2026-08-19T14:41:42.052Z  goal-line-in-new-notes=False
yoyodyne-ifd.104    overwritten 2026-08-19T16:15:15.311Z  goal-line-in-new-notes=False
```

The two entries earlier than their item's attribution (ifd.4 on 08-16, ifd.25
on 08-16) are writes that predate goals being recorded at all; the later ones
are the destroying writes. The same scan finds the safe `--append-notes` form
used 3 times across 2 items, so both spellings were in circulation and which
one an item got was chance.

### 4. The recovered statements still resolve

Each recovered goal, checked against the goals document in force.

```
$ python3 - <<'EOF'   # recovered statement vs docs/product/goals/*.md, case/space folded
...
EOF

yoyodyne-ifd.1.1     [t14.1]  resolves-in-force=['v1-goals.md']
yoyodyne-ifd.1.6     [t14.6]  resolves-in-force=['v1-goals.md']
yoyodyne-ifd.1.9     [t14.9]  resolves-in-force=['v1-goals.md']
yoyodyne-ifd.3       [t25.9]  resolves-in-force=['v1-goals.md']
yoyodyne-ifd.4       [t25.7]  resolves-in-force=['v1-goals.md']
yoyodyne-ifd.25      [t25.6]  resolves-in-force=['v1-goals.md']
yoyodyne-ifd.45      [t26.3]  resolves-in-force=['v1-goals.md']
yoyodyne-ifd.68.3    [t58.4]  resolves-in-force=['v1-goals.md']
yoyodyne-ifd.82.1    [t76.2]  resolves-in-force=['v1-goals.md']
yoyodyne-ifd.82.2    [t76.3]  resolves-in-force=['v1-goals.md']
yoyodyne-ifd.99      [t83.1]  resolves-in-force=['v1-goals.md']
yoyodyne-ifd.104     [t89.4]  resolves-in-force=['v1-goals.md']
```

### 5. An item that never had a goal, for contrast

ifd.83 is one of the twenty. It was filed by a direct `bd create` with no notes,
so nothing was destroyed on it.

```
$ grep 'bd create' transcripts near 2026-08-19T02:13 (ifd.83's created_at)
2026-08-19T02:13  bd create --parent=yoyodyne-ifd --type=feature --priority=2
  --title="Break yoyo cost down by phase: develop, review, repair, waits" --description="..."
```

## Work this diagnosis discovered

Named here for the product manager to admit or decline; none of it was queued
or performed.

1. Restore the three open lost attributions (ifd.45, ifd.82.2, ifd.99) from the
   statements above, and decide whether the nine closed ones are worth
   restoring for the traceability record.
2. Run `yoyo goals witness` once over the backlog, so the 57 unprotected
   attributions become losses the tracker can count.
3. Close the writer rather than only the record: nothing stops an agent running
   `bd update --notes=`, and neither `CLAUDE.md` nor `AGENTS.md` warns that it
   replaces rather than appends.
4. Attribute the twenty never-attributed open items, which is the product
   manager's judgement and not a derivation.
