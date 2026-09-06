# Terms

Every word this project coined that a reader can still meet, with what it means
in ordinary words and where it is met. This is the one list; a coined term that
is not here is one nothing defines.

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

| Term | In plain words | Where it is used |
| --- | --- | --- |
| `brake` | the automatic stop after a set number of blocked runs in a row | the scheduler's own messages about why it stopped choosing work; `internal/orchestrator`; the guides under `docs/` |
| `discharge` | to be the work an item asked for, so the item closes on it — as against landing evidence, which does not | the developer's contract and the reviewer's; a work item's own notes after a run; `internal/landing` and `internal/orchestrator`; [how work flows](work.md) |
| `docket` | the list of stopped runs waiting on the development manager | `yoyo reconcile` and `yoyo triage` output, and the product manager's context bundle; `internal/runstate`; [management and supervision](designs/management-and-supervision.md) |
| `handback` | handing the work back to the developer that made it | `internal/orchestrator` and `internal/runstate` only — it names no command output and no document |
| `heartbeat` | how often to repeat | the `yoyo slack --heartbeat` flag, whose own help says it in plain words; [reporting into Slack](slack/setup.md) |
| `in force` | active, or still applies | [the prose governing how invariants are amended](decisions/invariants/README.md), which only the architect changes |
| `minute zero` | before development begins | the [developer-verifies-before-submitting](decisions/invariants/developer-verifies-before-submitting.md) invariant, whose wording only the architect changes — written there both spaced and as `minute-zero`, which this one row covers |
| `posture` | which tools a role may use — written as *tool posture* | the [harness-is-the-only-role-invoker](decisions/invariants/harness-is-the-only-role-invoker.md) invariant, whose wording only the architect changes; the configuration guide |
| `sink` | the process that posts to Slack | `yoyo slack` and `yoyo doctor` output; `internal/slack`; [the Slack reporting design](designs/slack-reporting-design.md) |
| `steer` | direct the work, or change what is being worked on | `yoyo chat` help and the Slack thread replies; `internal/chat`; [the Slack reporting design](designs/slack-reporting-design.md) |

Three entries are here because the word is still written somewhere no other role
may edit. `in force`, `minute zero` and `posture` are the sweep's decoration
rather than mechanism names, and each survives only in the invariants home: two
inside the text of an active invariant, and `in force` in the prose that governs
how an invariant is amended. That wording is the architect's alone — the sweep
says so outright — so the entry is what keeps the word readable until the
architect decides otherwise, and each is retired when it does. The operator has
objected to `in force` by name, so these three are the entries most worth losing.

## Replaced rather than registered

These were decoration: each named nothing a reader can point at, and each had an
ordinary word that said the same thing. The second column is the wording to
write instead, rather than a claim that every occurrence has been changed: where
one of these was written in the prose of a governed document it was replaced,
and the rest are still in places outside this sweep — mostly the tracker's own
items, which are the product manager's to reword. One exception the re-run
confirms: `re-arm` is still written once in `designs/v1-harness-design.md`,
inside a recorded amendment reason in the frontmatter, which is the architect's
account of what it decided on a date rather than a sentence to clarify. The
check does not read frontmatter, for that reason. Either way the check below
refuses any of them coming back into the prose of a governed document without an
entry.

| Term | Write instead |
| --- | --- |
| `cadence` | how often it repeats — still in the `yoyo slack` refusal *heartbeat must be positive; it is a cadence rather than a switch*, which is a shipped string rather than a document |
| `one pane of glass` | one window |
| `re-arm` | repeat the merge request |
| `seam` | the boundary, named for what attaches to what |
| `sidecar` | a separate directory outside the repository |
| `soak` | a trial run kept alongside the old path for comparison |
| `starving` | stopping |
| `supersession pile` | the list of superseded pull requests |
| `tranche` | stage, or part 1 of 4 |
| `wedged` | stuck, or the condition said outright |
| `whose-move` | waiting on you |

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

Three things it deliberately does not read. A document's frontmatter is identity
and revision history, and a revision's recorded reason is what somebody decided
in their own words on a date — rewriting one to change a word falsifies a record
instead of clarifying a sentence. Fenced blocks are code. And the guides under
`docs/`, the README, and the tracker's own items are outside it: they are
operator-facing too, but no sweep has been run over them and holding a document
to an inventory nobody took over it would fail on words nobody was asked about.

What no check can do is recognize a word coined this morning. That is the
reviewer's, and it is written into the reviewer persona as a finding class: a
coined term in operator-facing text with no entry here is a finding, whatever
else the change does. The check is the floor under it, so a term once swept out
cannot quietly come back.
