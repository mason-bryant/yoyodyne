# Terms

Every word this project coined that a reader can still meet, with what it means in ordinary words and where it is met. This is the one list; a coined term that is not here is one nothing defines.

The rule it serves is the legibility goal's, in
[the v1 goals](product/goals/v1-goals.md): *user-facing language chooses the
ordinary, literal word over metaphor, coinage, or term of art* — unless the term
is defined here. Registration is the whole of the exception, and it is a low bar
on purpose. A word that names a real mechanism an operator meets in command
output is worth keeping and cheap to define. A word that only decorates a
sentence is worth replacing, and the ones that were replaced are listed below
rather than registered. What is not acceptable is the third case: a coinage in
front of a reader with no definition anywhere, so somebody meeting it has
nowhere to go.

The inventory this was seeded from is
[the yoyodyne-ifd.206 sweep](diagnoses/yoyodyne-ifd-206-coined-terms-sweep.md),
which measured every term below across the tracker, the governed documents, the
Go source, and command output. Its governed-document figures were a floor rather
than a count: the scan looked for one spelling of each term, so `minute-zero`
went past it, and two documents were written after it ran. The governed homes
were measured again on 2026-08-31 under yoyodyne-ifd.220, tolerant of how a
term's parts are spaced, and
[what that re-run found](diagnoses/yoyodyne-ifd-206-coined-terms-sweep.md#the-yoyodyne-ifd220-re-run)
is what this document is now written against: **25 occurrences of 6 terms** in
the prose the check reads, every one of them a term with a row below.

## The register


| Term          | In plain words                                                                                                                             | Where it is used                                                                                                                                                                                                                         |
| ------------- | ------------------------------------------------------------------------------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `brake`       | the automatic stop after a set number of blocked runs in a row                                                                             | the scheduler's own messages about why it stopped choosing work; `internal/orchestrator`; the guides under `docs/`                                                                                                                       |
| `discharge`   | to be the work an item asked for, so the item closes on it — as against landing evidence, which does not                                   | the developer's contract and the reviewer's; a work item's own notes after a run; `internal/landing` and `internal/orchestrator`; [how work flows](work.md)                                                                              |
| `docket`      | the list of stopped runs, of runs that died before they started, and of items dispatch would not start, waiting on the development manager | `yoyo reconcile` and `yoyo triage` output, and the product manager's context bundle; `internal/runstate`; [management and supervision](designs/management-and-supervision.md)                                                            |
| `handback`    | handing the work back to the developer that made it                                                                                        | `internal/orchestrator` and `internal/runstate` only — it names no command output and no document                                                                                                                                        |
| `heartbeat`   | how often to repeat                                                                                                                        | the `yoyo slack --heartbeat` flag, whose own help says it in plain words; [reporting into Slack](slack/setup.md)                                                                                                                         |
| `minute zero` | before development begins                                                                                                                  | the [developer-verifies-before-submitting](decisions/invariants/developer-verifies-before-submitting.md) invariant, whose wording only the architect changes — written there both spaced and as `minute-zero`, which this one row covers |
| `posture`     | which tools a role may use — written as *tool posture*                                                                                     | the [harness-is-the-only-role-invoker](decisions/invariants/harness-is-the-only-role-invoker.md) invariant, whose wording only the architect changes; the configuration guide                                                            |
| `re-arm`      | repeat the merge request a forge dropped, once per publication — the `yoyo triage rearm` verb and the budget it spends                     | `yoyo triage rearm` and its help; the merge re-arms count in `yoyo status`; the development manager's triage decisions and `yoyo ground`; the guides that say when to type it — [operations](operations.md), [recovery](configuration/recovery.md), [the conversation](conversation.md), and [configuration](configuration.md); `internal/orchestrator` and `internal/runstate` |
| `seat`        | an instance of a specific persona type — a developer seat, the product manager seat — often with persistent memory but not always. A *developer slot* is the harness's word for the capacity one developer seat fills: the seat is what does the work, and the slot is what it takes up while it does | the operator's own conversations, which is where the word came from; [a developer slot that prefers a label](configuration/runs.md#a-developer-slot-that-prefers-a-label), the yoyodyne-ifd.388 mechanism, and the reliability seat yoyodyne-ifd.415 configured under it |
| `sink`        | the process that posts to Slack                                                                                                            | `yoyo slack` and `yoyo doctor` output; `internal/slack`; [the Slack reporting design](designs/slack-reporting-design.md)                                                                                                                 |
| `steer`       | direct the work, or change what is being worked on                                                                                         | `yoyo chat` help and the Slack thread replies; `internal/chat`; [the Slack reporting design](designs/slack-reporting-design.md)                                                                                                          |


Two entries are here because the word is still written somewhere no other role
may edit. `minute zero` and `posture` are the sweep's decoration rather than
mechanism names, and each survives only inside the text of an active
invariant. That wording is the architect's alone — the sweep says so outright —
so the entry is what keeps the word readable until the architect decides
otherwise, and each is retired when it does. `in force` was the third of these
until yoyodyne-ifd.418 retired it: the operator objected to it by name, so it is
now listed below as replaced, with the governed documents that still carry it
named on its row until the architect amends them.

One entry is a command's own name. The sweep replaced `re-arm` in the prose of
the governed documents, but `yoyo triage rearm` is a verb an operator types and
`yoyo status` counts, and a word a command is called cannot be swept out of the
command's help without renaming the command. So it is registered, and a row
permits its term everywhere the check reads — the governed documents included,
in every spelling — not only in the command's output. The guides that tell an
operator when to type the verb use the word too, so the check reads them for
this term as well: take the row out and every guide sentence saying `re-arm`
or `rearm` fails with the command. What keeps it out of a
sentence that could have said *repeat the merge request* is the reviewer, not
the check.

One entry is the operator's word rather than the project's. He introduced
`seat` on 2026-09-19 and wants to keep using it, so its row is what makes it
read the way he means it wherever it is met. The distinction the row draws is
instance against capacity: a seat is the running persona that does the work,
and a developer slot is one unit of `max_concurrent_developers`, the capacity
that seat fills. So the configuration guide, the status line, and the scheduler
say *slot* when they count, fill, free, or configure capacity, and *seat* when
they mean the developer that sits in one — the reliability seat is the developer
that works in the slot configured to prefer the `reliability` label.

## Replaced rather than registered

These were decoration: each named nothing a reader can point at, and each had an
ordinary word that said the same thing. The second column is the wording to
write instead, rather than a claim that every occurrence has been changed: where
one of these was written in the prose of a governed document it was replaced,
and the rest are still in places outside this sweep — mostly the tracker's own
items, which are the product manager's to reword. The check below refuses any
of them coming back into the prose of a governed document, or into the strings
the commands, the conversation, and the notifier print, without an entry.

The third column is the one exception, and it is a narrow one. A governed
document is its owner's alone to reword, so a term retired from everywhere else
can still be written in one while its owner gets to the amendment. The row names
each such document, in backticks and repository-relative, and the check excuses
the term there and nowhere else: not in another document, and never in a
command's or the notifier's strings. The excuse ends with the amendment. Once a
named document no longer carries the term the check refuses the row itself, so
the document comes off the row rather than staying excused for a word it no
longer says.

| Term                | Write instead                                          | Still written in, until its owner amends it                                                                                                    |
| ------------------- | ------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------- |
| `cadence`           | how often it repeats, or its schedule                  |                                                                                                                                                |
| `in force`          | active, or still applies                               | `docs/decisions/invariants/README.md`, `docs/designs/artifact-contract.md`, `docs/designs/portable-agent-configuration.md`                     |
| `one pane of glass` | one window                                             |                                                                                                                                                |
| `seam`              | the boundary, named for what attaches to what          |                                                                                                                                                |
| `sidecar`           | a separate directory outside the repository            |                                                                                                                                                |
| `soak`              | a trial run kept alongside the old path for comparison |                                                                                                                                                |
| `starving`          | stopping                                               |                                                                                                                                                |
| `supersession pile` | the list of superseded pull requests                   |                                                                                                                                                |
| `tranche`           | stage, or part 1 of 4                                  |                                                                                                                                                |
| `wedged`            | stuck, or the condition said outright                  |                                                                                                                                                |
| `whose-move`        | waiting on you — or, of a thing, who it is waiting on  |                                                                                                                                                |




## Adding an entry

Write the row. The register is the authority: a term with a row is permitted and
a term whose row is removed is refused again, and neither is a change to any
code. An entry has to carry all three columns — a row with no definition is the
coinage with the appearance of having been registered, which is worse than no
row, and the check refuses it.

Prefer replacing the word. An entry is for a term that names something real and
would cost more to rename than it costs to define — the mechanism names above
are all of that kind, and each reaches operators through command output that
would have to change with it. A word invented for one sentence does not need an
entry; it needs the ordinary word.

## What the check covers, and what it does not

`internal/terms` runs under `make test` and reads every Markdown file under
`docs/product`, `docs/designs`, and `docs/decisions` for the terms above. One
with no entry here fails, naming the file, the line, and the ordinary wording to
write instead. It also holds this document to its own shape: an entry that
defines nothing, or names no place the term is used, fails the same check.

It reads the strings an operator is shown as well as the documents, since
yoyodyne-ifd.418: every string literal in the Go source of `internal/cli`,
`internal/chat`, `internal/notify`, `internal/slack`, `internal/readmodel`,
`internal/dashboard`, `internal/directive`, and `internal/goal` — the commands
and their help, the conversation, the notifier's lines, the read model every
surface projects, and the refusals `yoyo directive` and `yoyo goals` print —
and the dashboard's own script, style, and page under
`internal/dashboard/assets`, read whole. A string there is held to the same
register as a sentence in a document, with the one difference that nothing
excuses it: a term retired from the documents and still in the help text has
not been retired, which is what this is for. Only string literals are read, and
only outside test files — a comment is written for whoever reads the code, and a
test names the wording it refuses as often as the wording it wants. The
dashboard's script is read comments and all, because nothing cheap tells a
comment from a string in a language the check does not parse.

A term of more than one word is looked for however its parts are spaced —
`minute zero`, `minute-zero`, `minutezero`, and a `minute` a line wrap left with
its `zero` on the next line are the same coinage and all four fail. That cuts
both ways: a row here permits every spelling of its term, so registering
`minute zero` is what makes the invariant's `minute-zero` legal, and no variant
of a registered term is reported as though nothing defined it. The tolerance
applies only where the term is already written in parts — a term written here as
one word is looked for as one word, so `hand back` in a sentence about handing
something back is not reported as `handback`. Nothing is matched across a blank
line or a fenced block, because a term cannot wrap across either.

It reads the guides for a few terms only, since yoyodyne-ifd.360: the README
and every Markdown file under `docs/` outside the homes above, this document,
and the records under `docs/diagnoses`, `docs/experiments`, and
`docs/releases`. A guide is held to the register only for a term the check
marks as used in the guides — today `re-arm` and no other — so a guide that
leans on a row fails once the row is gone, and is not read for any other word.

Three things it deliberately does not read. A document's frontmatter is identity
and revision history, and a revision's recorded reason is what somebody decided
in their own words on a date — rewriting one to change a word falsifies a record
instead of clarifying a sentence. Fenced blocks are code. And the guides for
every other term, the records under `docs/`, the tracker's own
items, and the Go source outside the packages named above are outside it: they
are operator-facing too, but no sweep has been run over them and holding a
document to an inventory nobody took over it would fail on words nobody was
asked about.

What no check can do is recognize a word coined this morning. That is the
reviewer's, and it is written into the reviewer persona as a finding class: a
coined term in operator-facing text with no entry here is a finding, whatever
else the change does. The check is the floor under it, so a term once swept out
cannot quietly come back.