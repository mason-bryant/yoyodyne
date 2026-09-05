# yoyodyne-ifd.298: the design landed, the store did not

yoyodyne-ifd.298 asks for `yoyo agent memory <name>`: a role's memories printed
as Markdown-rendered text, each one with its revision history and the invocation
that wrote it, degrading readably where a terminal cannot show emphasis, and a
plain sentence where a role has none.

The item names its own precondition — *"this item does not start before 282's
design lands and should ride its implementation wave."* **The design has
landed.** What has not is the implementation it calls for, and nothing in the
tracker is the item that would produce it. This run checked, found the design
and no store behind it, and this is the record.

## The design is there

`docs/designs/configurable-workflows.md` carries a revision dated
2026-09-05T17:40:00Z whose reason begins *"yoyodyne-ifd.282 — agent memory
recorded as the existing agent-context mechanism"*, and the section
`### Agent memory` is what it added. It settles every question this surface
depends on:

- **Memory is the agent-context mechanism**, not a new concept beside it:
  continuity modes, typed stores, append-only revisions, compaction that keeps
  provenance.
- **No fourth store.** Durable conversations stay the interaction record,
  tracker notes stay the work record, and memory is a derived, revisioned store
  under the state root that may reference both and copies neither.
- **The store is db-backed**, with bolt named as an acceptable option and the
  choice left to implementation, like the other runtime-state formats.
- **Each revision records the invocation that produced it** — the audit
  requirement this item's second field renders, stated in the design itself.

So the item's two hardest fields, revision history and provenance, are decided
rather than open. The tracker export this run was given still shows ifd.282
`open`, but that copy was cut before the amendment: the item's `updated_at` in
it is 2026-09-05T09:52:45Z and the design revision is 17:40 the same day.

## The store is not

These are the commands, written so they can be copied and run. They are outside a
table on purpose: a pipe inside a table cell has to be written `\|`, and that
escape is what produced the wrong answer the first time this was checked — see
the note below.

```sh
# 1. does any Go code declare a memory store?
grep -rniE 'MemoryStore|MemoryRecord|RoleMemory|AgentMemory' --include='*.go' internal cmd

# 2. is the mechanism the design defines memory as implemented?
grep -rniE 'continuity|ContextPolicy|ContextStore' --include='*.go' internal cmd

# 3. does the word appear in non-test Go at all, under any spelling?
grep -rn --include='*.go' -i memor internal cmd | grep -v '_test.go'

# 4. is an implementation item admitted? (every tracker item mentioning the word)
grep -i memor .beads/issues.jsonl | python3 -c \
  'import sys,json; [print(json.loads(l)["id"], json.loads(l)["status"]) for l in sys.stdin]'
```

Run in this worktree on 2026-09-06:

1. **No matches.** Nothing declares a memory record or store under any of those
   names.
2. **No matches.** The continuity modes and typed context stores the design
   defines memory *as* are not built either, so the absence is of the mechanism
   and not only of one name for it.
3. **Thirty-one lines, none of them code.** Every one is the phrase "in memory"
   in a comment or in a prompt string — a bound on what a process holds, a
   conversation answering without checking, the scheduler not re-pulling an item.
   The stem search is here because the first two are negatives on names somebody
   chose, and a negative on names is only as good as the guess behind it.
4. **Eleven items, four of them unfinished**: ifd.282 the design, ifd.298 this
   surface, and ifd.187 and ifd.239, which use "memory" for something else
   entirely. None of the eleven is the store.

The one package that might have held it does not. `internal/contextbundle`
exports `Assemble`, which builds one bundle per invocation out of canonical
artifacts and keeps nothing between invocations.

The design's continuity modes, typed stores, and append-only revisions are
written down and not built. There are no memories, so there is nothing for the
verb to render.

## Why no verb was written against it

**A reader with nothing to read cannot satisfy the item.** Two of the three Done
clauses — every memory rendered with history and provenance, and readable
degradation of that rendering — need records that exist. Only the third, the
plain sentence for a role with none, could be met today, and a command that meets
only that one reports a feature as present while the operator still cannot read
anything an agent wrote. The item would close against a surface that can never
show a memory.

**The record shape is designed but not typed.** The design fixes what a memory
is; it does not yet exist as a Go type in a package that owns it. Declaring one
in `internal/cli` so the command had something to render is the surface-local
copy of a governed vocabulary that `surfaces-project-one-read-model` forbids by
name, and it would be thrown away when the owning package declares the real one.

## The shape the surface should take

This is the part worth keeping, so the run that picks the item up after the store
lands does not work it out again. Every piece named here exists now.

**A fourth verb beside the three.** `runAgentCommand` in `internal/cli/agent.go`
dispatches `list`, `show`, and `chat`; `memory` joins them, with its line in
`printAgentUsage` and its entry in the operator's list in `docs/conversation.md`.
The name the item offered is the one that fits: the existing verbs are plain
words, and `memory` reads beside them.

**The design already says this belongs on the CLI.** Its role-definition
paragraph settles the same question for a neighbouring record — *"audit history
is a CLI surface, never an agent one"* — which is the reading this item applies
to the memory store's own audit trail.

**The name is resolved the way the other verbs resolve it.** `resolveAgent`
accepts either a configured agent name or a role name, and refuses a role two
agents fill rather than answering for whichever sorted first. A second resolver
would be a second answer to one question.

**`--json` alongside the text.** `list`, `show`, and `chat --message` all carry
it, and `cli_test.go` holds a table of commands checked for their behaviour when
the configuration is missing, which the new verb belongs in.

**The Markdown rendering already exists, and so does its degradation.**
`console.Theme.Reply` in `internal/console/markdown.go` dresses Markdown for a
terminal under a discipline that answers the item's second clause directly:
nothing is added to the text and nothing taken out, every escape inserted between
characters that were already there, so the same text through an undressed theme
is what was written, byte for byte. A terminal that cannot show emphasis loses
the weighting and no distinction. A second renderer in the CLI would be the
duplication the legibility goal argues against and the read-model invariant
forbids.

**Revisions render oldest-last, with the invocation on each.** The design makes
revisions append-only and compaction provenance-keeping, so the history is a list
rather than a diff, and a compacted memory has to say so rather than appearing to
have been written once.

**The empty case has a house phrasing.** `renderAgent` prints `no conversation
recorded` where an agent has never been spoken to; the memory equivalent is one
sentence in the same register, naming the role, with no heading above an empty
list.

**The store is read through whatever owns it.** `readAgents` loads conversations
through `runstate.NewConversationStore` and asks `readmodel.InFlight` rather than
counting in-flight turns itself. Memories should arrive the same way, with the
command doing nothing but rendering.

**One unreadable record does not fail the listing.** `readAgents` reports a
conversation it could not read against the agent it belongs to and carries on,
because an operator asking what is there is owed the answer even when one record
is unreadable. Memories carrying an audit obligation deserve that more, not less:
a record that will not parse is itself something the operator needs to see.

## The check that proved nothing

The first version of this document recorded its searches as
`grep -rniE "a\|b\|c"`. Under an extended regular expression `\|` is a literal
pipe, so those commands searched for one long string that no file contains and
returned zero matches whatever the repository held. They were written as proof of
absence and proved nothing — and the absence they appeared to establish was
false: the corrected search found the design section in
`docs/designs/configurable-workflows.md` immediately. An independent reviewer
caught the quoting; the wrong conclusion it was holding up came out with it.

The four commands above are the corrected ones, and each was run rather than
composed. Anyone re-establishing this should run them rather than read them.

## What would release this item

1. An implementation item for the agent-memory store is admitted, citing the
   `### Agent memory` section of `configurable-workflows.md`, and lands: the
   db-backed store under the state root, the typed context actions that write it,
   the append-only revisions with the invocation recorded on each, and the size
   cap and redaction the design requires. Nothing in the tracker is that item
   today.
2. ifd.298 is released, and the verb above is written against the records that
   then exist.
