# yoyodyne-ifd.336: the note writes landed, and the read-back showed the wrong end of the notes

`yoyodyne-ifd.336` was admitted on two `updated yoyodyne-ifd.283: note`
confirmations that were said to have landed no text, verified by reading the item
back twice and finding only its admission and goal lines, and on the reading that
at least one tracker write path reports success and persists nothing.

**Both of those notes are on the item.** They are at byte 63,026 and byte 63,849
of a notes field 64,661 bytes long, in the tracker's own export
(`.beads/issues.jsonl`, copied into this run's worktree, item updated
`2026-09-07T07:06:08Z`). The write path is not the failure. What lied was the
read-back: the rendering both readers use cut the item at 8 KiB from the front,
so what it showed of a 64 KB notes field was its first few kilobytes — the
admission and the goal lines — and everything written to that item in the last
fortnight was outside the window.

## What the rendering did

`renderWorkItemEvidence` (`internal/chat/tracker.go`) assembles one item —
header, description, design, acceptance criteria, notes, in that order — and ends
with `boundText(rendered, maxTrackerItemBytes)`, which keeps the first 8,192
bytes and appends `[cut at 8192 bytes; treat the rest as unread rather than
absent]`.

Two things follow, and the second is what cost the operator two directions:

- Notes are rendered last, so they are the first thing a cut reaches.
- Notes are only ever appended to, so their end is the recent writing. A cut that
  keeps the front keeps the oldest text and drops the newest.

283's description is 1,710 bytes, so the window left for its notes was about 6 KB
of 64 KB — the admission line, the goal line, and the first few stoppage records.
A reader asking "is the note I just wrote there?" was answered with text from a
fortnight earlier.

Both readers of an item go through that one function: the product manager's own
`read` action (`carryOutTrackerAction`) and the operator's `/show`
(`internal/chat/steer.go`). The declared cut was in the output, and it says to
treat the rest as unread rather than absent — but what a reader wants after a
write is at the end, and a cut marker in the middle does not distinguish "your
note is past this line" from "your note is not here."

## What bd actually does with an appended note

Checked against bd 1.1.2 rather than argued:
`TestAppendedNoteDurabilityConformance` (`internal/beads/conformance_test.go`)
builds an item with 64 KB of accumulated notes in a scratch store, appends the
operator's two paragraphs to it in succession, and reads the item back. Both
appends are durable, both survive the second, and the item's own admission and
goal lines survive both. Nothing here reproduces a dropped write.

## The audit the item asked for

188 of the 472 items in the export — 40% — carry more text than the 8 KiB
rendering could show, and in every one of those the most recent notes were
entirely outside the window. Any note written to any of them and then checked by
reading the item back would have read as absent. That is the population the two
283 reads came out of; it is not evidence that any other write was lost, because
no write is lost.

No item's notes sit at a round boundary that would suggest a store-side cap: the
longest is 283's 64,661 bytes, and the next four are 49,586, 36,183, 35,567, and
33,726.

What this audit cannot see is the confirmation side. A run's durable state records
what each tracker action reported, and that state lives outside a developer run's
worktree, so "which confirmations were issued" was read from the item text itself
rather than from the records of the actions that wrote it.

## What changed

- **The confirmation is earned rather than assumed.** Every path in
  `internal/beads` that appends to an item's notes — `Update`, `RecordOutcome`,
  `Block`, `Reopen` — now checks that the text is in the notes bd answers with,
  and reads the item back separately when it is not. A replaced description is
  checked the same way, because that is where a scope addition belongs and it was
  as unverified as the note. Text the tracker does not
  hold is an error the caller sees. A write that landed and was echoed badly is
  not reported as a failure, and a read-back that cannot run is reported as an
  append nobody could confirm rather than as one that failed: a durable write
  reported as lost is the mirror of this defect, and yoyodyne-ifd.327 is the
  record of what that costs.
- **The read-back shows the end of the notes.** The notes are cut from their
  beginning, keep a guaranteed floor of half the item's budget whatever else the
  item carries, and the cut is declared where it falls.

## What is still open

The paragraph the operator's directions carry belongs in 283's own description,
where a scope addition is read rather than appended to a 64 KB notes field. A
developer run cannot write to the tracker, so that edit is named in this run's
summary for whoever can make it.
