# yoyodyne-ifd.367: how a conversation-executed item reached a developer run, and why design-only items closed by hand

The finding, as raised by the developer on run-f9e67240 (yoyodyne-ifd.330):
330's deliverable — the side-conversation design — merged as PR #465 on
2026-09-07, yet the item stayed selectable and was handed to a developer run
with nothing it could do, and 330 "carried executor conversation:architect from
admission". Two questions were asked: how an item marked for a role's
conversation reached a developer run at all, and why design-only items close
by hand after their design lands. This is the answer to both, read from the
records, and the mechanism the change lands for each.

Everything below is read from the product manager's conversation record
(`conversations/chat-91253e0e070c17b0663651cc48602122.events.jsonl` under the
product's state directory), the tracker's export as the harness copied it into
this run's worktree (`.beads/issues.jsonl`, 596 items), and the designs'
revision logs at the base commit, on 2026-09-20.

## 330 never carried the marker

The premise the item was admitted on is wrong, and the record says so in one
line. The product manager's creation of 330, applied at turn 468 on
2026-09-07T04:32:47Z, carried these fields and no others:

```text
action, title, description, goal, parent, priority, reason
```

No `executor`. The harness's own record of the applied action reads
`work_item_executor: ""`, and 330's metadata in the export today holds
`yoyodyne_parked`, the cost keys, and the goal witness — every key a later
write added — and no `yoyodyne_executor` at all. Compare 282, admitted at turn
411 two days earlier for the same kind of work: its creation carried
`executor: conversation:architect`, its metadata holds the key today, and it was
never selected. The marker held wherever it was written. It was not lost, it did
not fail, and the architect's run did not discharge it. It was left off.

So the first question has a plain answer: the item that reached a developer
run was, to every reader in the harness, a developer item. Its title said "The
architect designs side conversations with merge-back", its description said
"design routed to the architect as directed", and its done-means said "the
design is recorded in the governed documents". A person reads all three as the
architect's work. Nothing in the harness read any of them — the manual said in
as many words that "nothing infers it — no reading of an item tells a
conversation from a diff" — and the done-condition gate that landed with
yoyodyne-ifd.396 on 2026-09-20 reads only a document named by path or by id,
which 330's clause does neither. The tracker called the item ready, and the
scheduler chose it.

The developer on run-f9e67240 read the same three sentences and reported the
marker from them. That is the second thing this diagnosis corrects: an item's
executor is a metadata key, not what its prose says, and the export carries the
key where one exists.

## Design-only items closed by hand because nothing read the landing

A developer item closes when its change merges: the reconciler reads the forge
and closes it with the run's landing claim. A conversation-executed item had no
landing anything read. The architect's work lands as a revision in a document
the architect owns — 330's is the `2026-09-07T05:30:00Z` amendment of
`docs/designs/management-and-supervision.md`, reason "yoyodyne-ifd.330 - side
conversations designed …", committed by the operator as `52be6d6` — and the
only thing that carried that back to the tracker was the product manager,
closing on evidence: 282 at turn 440, 330 at turn 512, each a turn spent
reading a revision log the harness could have read.

The gap is live. Running the landing sweep below against this worktree's
designs and the open architect items in the export, five items stand open with
a revision opening with their identifier already in a design:

| Item | Revision that lands it | Document |
|---|---|---|
| yoyodyne-ifd.209.21 | 2026-09-05T17:40:00Z, "yoyodyne-ifd.209.21 - recurring tasks recorded in the reserved trigger seat …" | configurable-workflows |
| yoyodyne-ifd.294 | 2026-09-07T00:30:00Z, "yoyodyne-ifd.294 - the config.lock baseline ratified as a contract …" | portable-agent-configuration |
| yoyodyne-ifd.306 | 2026-09-07T00:30:00Z, "yoyodyne-ifd.306 - conversation account failover designed …" | claude-execution-and-account-routing |
| yoyodyne-ifd.313 | 2026-09-07T00:30:00Z, "yoyodyne-ifd.313 - the operator-approved communication rule recorded as binding …" | slack-reporting-design |
| yoyodyne-ifd.348 | 2026-09-07T20:00:00Z, "yoyodyne-ifd.348 - the operator's direction that the management roles act …" | configurable-workflows, v1-harness-design |

330's own landing is the same shape — 2026-09-07T05:30:00Z, "yoyodyne-ifd.330
- side conversations designed …", in management-and-supervision — and was
closed by hand at turn 512, six days after it.

And one that is mentioned and not landed: yoyodyne-ifd.280 appears in the
`2026-09-05T17:40:00Z` revision of v1-harness-design as "published under
yoyodyne-ifd.280 after three reviewer reports" — the backfill that is half of
280's deliverable, with the rule that is the other half not in it. A close on
"the revision mentions the item" would have closed 280 wrongly; a close on "the
revision opens with the item" does not.

## The mechanism, in two halves

**The 330 shape is refused unmarked, at admission and at selection.** The
done-condition gate from yoyodyne-ifd.396 now takes the item's executor as part
of the question, and reads two shapes with no document named: a done-means
clause saying a design or a ruling is recorded, published, promoted, or
ratified, and a title whose subject is the architect acting ("The architect
designs …", "The architect rules …"). Either on an item naming no executor is
refused — the creation or update in the product manager's conversation, and the
run before it claims — with the marker named as the fix. Measured over the 596
items in the export, the two readings fire on 27: 24 already carrying the
marker, 330, and two admitted before the marker existed (yoyodyne-ifd.68.1,
closed, and .68.25, open) whose clause a run indeed cannot meet. "decision" and
"rule" are not read, because triage records a decision on an item and a gate
enforces a rule, and both fired on developer items when tried. A grant under an
artifact home turns the reading off: the grant is somebody's decision that a run
writes there.

The same gate stops refusing what it had been refusing wrongly. A marked item
whose done-means names a document its role owns — "the ruling is recorded on the
slack-reporting design", with `executor: conversation:architect` — is that
role's work stated correctly and is admitted; the 396 gate refused it as a
condition no run could meet, which was true and beside the point, and its
instruction would have had the product manager take out the one clause that
says what the item is for. A marked item naming another role's document is
still refused, as that role's.

**A marked item closes on the pull that finds its landing.** The watch pass
reads, on every pull, the documents each marked role owns, and closes an entry
whose identifier opens the reason of a revision in one of them made by that
role. The close reason names the document, the revision's timestamp, the
convention, and the revision's own words, so a close the product manager
disagrees with is one she can read and reopen with a note. The development
manager owns no document, so its items are never read for one; an unmarked item
is a developer run, whose landing is its merge. Run against this worktree and
the export's open items, the first pull closes 209.21, 294, 306, 313, and 348,
and leaves 280 open.

## What it does not cover

- An item whose done-means is worded outside the two readings — "Done means
  implementation items can cite it", "the governed documents are amended
  through the architect" — and carries no executor is still a developer item to
  the harness. The readings are narrow on purpose; the fix for such an item is
  the marker, in the product manager's conversation.
- A landing recorded without the identifier first — a revision that names the
  item in its second sentence — closes nothing, by design. The convention is
  now in the artifacts manual and the product manager's contract.
- The pass reads the primary checkout's working tree, as every other reader of
  the artifact homes does. A revision on a branch nobody has merged is not a
  landing yet.
