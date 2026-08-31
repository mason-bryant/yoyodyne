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
Go source, and command output.

## The register

| Term | In plain words | Where it is used |
| --- | --- | --- |
| `brake` | the automatic stop after a set number of blocked runs in a row | the scheduler's own messages about why it stopped choosing work; `internal/orchestrator`; the guides under `docs/` |
| `docket` | the list of stopped runs waiting on the development manager | `yoyo reconcile` and `yoyo triage` output, and the product manager's context bundle; `internal/runstate`; [management and supervision](designs/management-and-supervision.md) |
| `handback` | handing the work back to the developer that made it | `internal/orchestrator` and `internal/runstate` only — it names no command output and no document |
| `heartbeat` | how often to repeat | the `yoyo slack --heartbeat` flag, whose own help says it in plain words; [reporting into Slack](slack/setup.md) |
| `minute zero` | before development begins | the [developer-verifies-before-submitting](decisions/invariants/developer-verifies-before-submitting.md) invariant, whose wording only the architect changes |
| `posture` | which tools a role may use — written as *tool posture* | the [harness-is-the-only-role-invoker](decisions/invariants/harness-is-the-only-role-invoker.md) invariant, whose wording only the architect changes; the configuration guide |
| `sink` | the process that posts to Slack | `yoyo slack` and `yoyo doctor` output; `internal/slack`; [the Slack reporting design](designs/slack-reporting-design.md) |
| `steer` | direct the work, or change what is being worked on | `yoyo chat` help and the Slack thread replies; `internal/chat`; [the Slack reporting design](designs/slack-reporting-design.md) |

Two entries are here because the word is still written somewhere no other role
may edit. `minute zero` and `posture` are the sweep's decoration rather than
mechanism names, and both survive only inside the text of an active
architectural invariant. An invariant's wording is the architect's alone, so the
entry is what keeps the word readable until she decides otherwise, and it is
retired when she does.

## Replaced rather than registered

These were decoration: each named nothing a reader can point at, and each had an
ordinary word that said the same thing. They were replaced in the governed
documents rather than given entries, and the check below refuses each of them
coming back without one.

| Term | What replaced it |
| --- | --- |
| `cadence` | how often it repeats |
| `in force` | active, or still applies |
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
