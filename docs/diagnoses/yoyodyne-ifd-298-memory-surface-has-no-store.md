# yoyodyne-ifd.298: the reading surface, and the store that is not there yet

yoyodyne-ifd.298 asks for `yoyo agent memory <name>`: a role's memories printed
as Markdown-rendered text, each one with its revision history and the invocation
that wrote it, degrading readably where a terminal cannot show emphasis, and a
plain sentence where a role has none.

The item says what it waits on, in its own words: *"this item does not start
before 282's design lands and should ride its implementation wave."* It has not,
and there is no wave. This run checked, found nothing to read, and this is the
record of what was checked and of the shape the surface should take when there
is something behind it.

## What is there

Four checks, run in this worktree at commit `2f014f6` on 2026-09-06.

| what was asked | how | result |
| --- | --- | --- |
| does the design exist? | `ls docs/designs` | eleven documents, none about memory |
| does any governed document describe one? | `grep -rniE "per-role memor\|role memory\|memory store\|memories" docs` | 0 matches |
| does an implementation exist? | `grep -rniE "MemoryStore\|MemoryRecord\|RoleMemory\|AgentMemory" --include="*.go" internal cmd` | 0 matches |
| is anything else waiting on it? | every item in `.beads/issues.jsonl` whose authored text names 282 | one: ifd.298 itself |

yoyodyne-ifd.282 — the item that produces the design — is `open` in the tracker
export, with `yoyodyne_executor: conversation:architect`. Its own Done clause is
*"the design is recorded in the governed documents … and implementation items
can be admitted citing it."* So the store is two items away rather than one: the
architect's design, then an implementation item that nobody has admitted, and
only then a surface with records to print.

## Why the surface was not written ahead of it

**The item's two hardest fields are the store's to define.** "Its revision
history" and "the invocation that produced it" are not renderings of something
the harness already holds — they are records 282 is being asked to decide the
shape of, along with what supersedes what, the size budget, and the redaction
rule. A reader written now would have to invent that shape, which is the
architect's decision taken by a developer, and would have to be rewritten when
the real one lands.

**Inventing it in the command would break `surfaces-project-one-read-model`.**
That invariant says a surface owns no state of its own, reimplements no
derivation the model provides, and does not redeclare a governed vocabulary as a
local type. A memory record declared in `internal/cli` because no package owns
one yet is precisely the surface-local copy it forbids, and the invariant names
that case as the grep-resistant one worth a coverage sweep to rediscover.

**A verb that can only print the empty sentence is worse than no verb.** The
item's third clause — a plain sentence where a role has none — is the only one
that could be satisfied today, and a command that satisfies only that one says a
feature is there when nothing can ever be listed by it. The item would read as
discharged and the operator would still have no way to read what an agent wrote.

## The shape the surface should take

This is the part worth keeping, so the run that picks the item up after the
store lands does not work it out again. Every piece named here exists now.

**A fourth verb beside the three.** `runAgentCommand` in `internal/cli/agent.go`
dispatches `list`, `show`, and `chat`; `memory` joins them, with its line in
`printAgentUsage` and its entry in the operator's list at
`docs/conversation.md`. The name the item offered is the one that fits: the
existing verbs are plain nouns and verbs, and `memory` reads beside them.

**The name is resolved the way the other verbs resolve it.** `resolveAgent`
already accepts either a configured agent name or a role name, and refuses a
role two agents fill rather than answering for whichever sorted first. A second
resolver would be a second answer to one question.

**`--json` alongside the text.** `list`, `show`, and `chat --message` all carry
it, and `cli_test.go` holds a table of commands checked for their behaviour when
the configuration is missing, which the new verb belongs in.

**The Markdown rendering already exists, and so does its degradation.**
`console.Theme.Reply` in `internal/console/markdown.go` dresses Markdown for a
terminal under a discipline that answers the item's second clause directly:
nothing is added to the text and nothing taken out, every escape is inserted
between characters that were already there, so the same text through an
undressed theme is what was written, byte for byte. A terminal that cannot show
emphasis therefore loses the weighting and no distinction. A second renderer in
the CLI would be the duplication the legibility goal argues against and the
read-model invariant forbids.

**The empty case has a house phrasing.** `renderAgent` prints `no conversation
recorded` where an agent has never been spoken to; the memory equivalent is one
sentence in the same register, naming the role, and no heading above an empty
list.

**The store is read through whatever owns it, not opened here.** `readAgents`
loads conversations through `runstate.NewConversationStore` and asks
`readmodel.InFlight` rather than counting in-flight turns itself. Memories should
arrive the same way, from the package the implementation gives them to, with the
command doing nothing but rendering.

**One unreadable record does not fail the listing.** `readAgents` reports a
conversation it could not read against the agent it belongs to and carries on,
because an operator asking what is there is owed the answer even when one record
is unreadable. Memories with an audit obligation deserve the same treatment more,
not less: a record that will not parse is itself something the operator needs to
see.

## What would release this item

1. yoyodyne-ifd.282 lands its design in the governed documents — the store
   choice, the curation model, what supersedes what, and the budget, redaction
   and audit story.
2. An implementation item for the store is admitted citing that design and
   lands. Nothing in the tracker is that item today.
3. ifd.298 is released, and the verb above is written against the records that
   then exist.
