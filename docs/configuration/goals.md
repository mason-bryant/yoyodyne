# Configuring admission, attribution, and staleness

What reaches the work queue and on whose approval, how work is traced back to
the brief, how a goal claims the work attributed to it, what a change upstream
leaves stale, and the invariants a change is held to.

[The configuration index](../configuration.md) lists the other guides.

## What reaches the queue

**`approvals.work_items` decides whether you are asked about every work item.**
It is `human` until you say otherwise: every item is put to you before it is
admitted to the queue. Set it to `automatic` to move that approval up to your
goals, which is what it exists for — work that traces to a goal you approved is
then admitted without a further prompt, and you are told afterwards what went in.

**It is opted in to rather than inherited**, for the reason `integration` and
`publishing` are: this is the setting that lets work reach the queue with no
person in the loop, and autonomy is something you turn on once you have the gates
to justify it rather than something a repository acquires by extending a bundle
or by upgrading the executable. The bundle states `human` at the same value the
harness default holds, so `automatic` never arrives on its own.

**Upgrading does move one thing, and it moves toward asking you.** Before this
key existed the product manager could admit work to the backlog directly, and you
were told afterwards rather than asked; `human` refuses that direct admission, so
a project that upgrades and leaves the key alone has a product manager that
proposes work instead of admitting it. That is the whole of the change, it is in
the direction of more consent rather than less, and the work is not lost — the
proposal is put to you, and approving it creates the item. Set `work_items` to
`automatic` to have it admit directly again, against goals you approved.

**Approval moved up a level; it did not disappear.** Three things still stop and
ask, and they are exactly what the product manager escalates rather than
proposes: work it can attach to no goal, work it says would cut against one, and
work that fits the goals and that it judges to be against what the product is
for. A change to the goals themselves is yours and reaches the queue through
nothing at all — the product manager argues for one in prose and cannot make one.

**Nothing is admitted without asking until a goal is actually approved.** The
attribution has to resolve to a goal a document in force states, and that
document has to be approved as it now stands. A goals document nobody approved,
one amended since you approved it, and a repository with no goals to check
against all put the work to you instead, with the reason on the proposal. So
turning it on gets you a second ramp for free: a project that has opted in still
asks about everything until its first `yoyo artifact approve`. It is also why
`work_items: automatic` requires `approvals.goals` to be `human`: admitting work
rests on the goal it serves having been approved, and a project approving no
goals has nothing for it to rest on, so the combination is refused rather than
left to be discovered as a queue that never fills. That refusal only ever names a
key you wrote, because `automatic` is never inherited.

**Both ways work reaches the queue are governed by it.** The product manager can
admit work to the backlog directly as well as propose it, and `human` refuses the
direct admission with a pointer at the proposal it should have made instead — a
setting that governed proposals while work arrived through the other door would
say one thing and do another. Decomposition is not admission: a role that may
only create underneath work you already admitted is building structure under a
decision that was made, and it is unaffected by either setting.

**`approvals.work_item_exemptions` narrows the per-item gate without lifting it.**
It is a list of classes of work this project admits without asking, whatever
`work_items` says, and it is empty until you write one:

```yaml
approvals:
  work_items: human
  work_item_exemptions:
    - diagnosis
```

There is one class. `diagnosis` is work that only looks: it reads what is already
there, says up front what it will read and stops there, and produces findings
rather than a change. It exists because "ask me about every work item" turns out
to be coarser than most operators who set it mean — being asked before something
reads the repository and writes down what it found is not what the gate was put
up for, and with no way to say so the policy stays a sentence nothing enforces.

**The class is the agent's claim about its own work, and the exemption is yours.**
A proposal or a `create` may carry `class: diagnosis`, and it means nothing at all
in a project that has not exempted that class — the product manager is told about
a class only where you have exempted it, precisely so it is never invited to claim
one that would change nothing. What keeps the claim honest is that the exempted
class is work that changes nothing: an item claiming to be diagnosis and then
doing something else is an item whose description says what it does, under a goal
that had to resolve, in a queue you read.

**An exemption moves who is asked and never whether the work is for anything.**
Work admitted under one names a goal that *resolves* — one an in-force goals
document actually states — and nothing weaker. Anything short of that is put to
you exactly as it would be for work claiming no class: a goal the documents do
not state is `unresolved` and refused, and a goal nothing could check against is
`uncheckable` and asked about, because an attribution nobody could check is not
one you agreed to. What an exemption does not require is that the goal be
*approved*, which is what makes it usable by the projects that keep the human
gate. The [attribution table](#goals-and-the-work-attributed-to-them) is the same
table for exempted work as for everything else.

**What it narrows is the per-item gate and only that.** Under `work_items:
human` the exemption stands the per-item question down, and the goal has only to
resolve. Under `work_items: automatic` there is no per-item question left to
narrow: the approved goal is the whole of what admits work there, so an exempt
class clears exactly the gate every other item clears, and a goal nobody
approved — or one amended since — puts the work to you as it would anything
else. An exemption that reached past the per-item question would be a carve-out
admitting work under a goal nobody approved, which is the opposite of the
narrowing it is.

**What was admitted without asking is reported where a decision would have been.**
Each item is named with the goal it traces to and with what actually admitted it
— the approved goal, or the class you exempted — in the conversation and in
`yoyo chat --message ... --json` under `admitted`, where they are the `goal` and
`basis` fields. The two are not the same answer: under an exemption the item
still names a goal, and the goal is not what let it through. The item's own notes
record the same basis, and never that you approved the item. The conversation's
event log records an admission as its own event, so work nobody was asked about
is never readable as work somebody approved.

**Approving writes nothing but the approval.** The prose, the title, what the
document supports, and its status are untouched, so an approval can never become
a way to edit a document by another name — the document itself stays the owning
role's to change. Approval is recorded as yours rather than as a role's, because
every one of these documents is drafted by the role that owns it, and an
approval a role could record would be that role approving its own document.
Refused, rather than recorded: an approval with no reason saying how you gave it,
a second approval of a revision already approved, and approving a document that
has been superseded or retired.

## Traceability: references and orphans

Identity makes a relationship expressible; it does not make it true. So the
chain is validated across the whole set every time the artifacts are loaded, and
what it finds is **reported, never refused** — the opposite of how a document
with no usable identity is handled, and for the same reason a malformed
specification is still read. A broken relationship is a thing to correct, not a
reason to lose a document somebody wrote. What each document's revision log says
about who changed it is reported in the same place and for the same reason, so
one listing says everything that is wrong with the documents that loaded.

| Reported as | What it is |
| --- | --- |
| `dangling-reference` | A `supports` entry naming an id no artifact answers to. Both ends are named: the file the reference is written in, and the id it names. If that id belongs to a file that is in an artifact home and was refused, the report says so and names it, rather than reading as a document nobody wrote. |
| `orphan` | An artifact that nothing connects back to the brief. Following `supports` upstream from it — through as many artifacts as the chain runs — arrives at no `brief`. |
| `unauthorized-revision` | A revision recorded under a role that does not [own the document](artifacts.md#who-may-change-an-artifact). Reported once per document, naming which entries crossed, because opening the file and deciding is one job however many there are. |

Two kinds are never orphans. The **brief** is the root, so nothing is upstream
of it. A **decision** record says how the product is built rather than what it
is for: it is taken in service of the goals without being a statement of intent
downstream of them. Everything else — goals, non-goals, designs, and
specifications — has to trace to the brief.

Only references that resolve are followed, so a reference that names nothing is
reported once as the broken name it is rather than guessed at. Nothing is
followed twice, so two artifacts that support each other are reported as
reaching nothing rather than sending the check round in a circle. A repository
with no `brief` recorded at all is told that, once per document, instead of
being told that each of its documents is separately unconnected.

An artifact that is `superseded` or `retired` still answers to its id and still
holds its place in the chain. The record of what was intended is what makes a
later change traceable, so a design that traces through a goal since replaced is
not an orphan.

```sh
yoyo artifact list   # broken relationships go to stderr beside the listing
yoyo artifact show v1-goals   # and what is wrong with one document, for that one
```

`--kind` narrows the listing and not the reporting: the chain runs between
kinds, and a listing narrowed to the goals would otherwise hide the design that
names one of them and resolves to nothing.

## Goals, and the work attributed to them

The chain's last link runs from a work item to a goal, and a work item is in the
tracker rather than in an artifact home. So the goals themselves are read out of
the goals artifacts: **every statement under a goals document's `Goals` heading
is a goal work can be attributed to**, and an attribution resolves by naming one
of them in the words that document states it in.

Only a `goals` artifact is read this way. A brief or a design with a `Goals`
heading of its own states no goals work may be attributed to — the goals are the
product manager's document, and reading intent out of anything with the right
heading is how a design comes to authorize its own work.

The `Goals` heading is the heading whose **whole text** is `Goals`, at any
level. A title that merely opens with the word — `# Goals for V1` — is a title,
and a document with no such heading states no goals and is reported as stating
none. The exactness is load-bearing rather than pedantic: a title read as the
section opens the goals at the document's top level, and nothing written below
it can then end them by level, so everything in the file becomes something work
may be admitted under.

Each goal is one top-level list entry under that heading, and its statement is
**that entry's opening paragraph, rejoined onto one line**. Markdown is normally
hard-wrapped, so a goal written across several lines is the ordinary case: the
lines that continue it are joined with a single space, and the goal is recorded
whole rather than as its first line. The statement ends at the first thing that
is not more of the same sentence — a blank line, an unindented line, a nested
list entry, or the emphasized `*Supports: ...*` trailer naming what the goal
serves upstream. A trailer is recognised by the emphasis it **opens** with
rather than by where that emphasis closes, so a trailer hard-wrapped across
lines ends the statement exactly as a one-line trailer does; a line that opens
with an emphasized phrase and then carries on in plain text is the rest of a
wrapped sentence, and continues the statement. Everything after the statement
ends describes the goal rather than being part of it or being another, and a
heading below the
`Goals` heading divides the goals rather than ending them. The section ends at
the next heading at the same level or above, **or at any heading stating what
the product will not do** — a `Non-goals` heading ends it wherever it is
written, including nested inside it, so a document that files its non-goals
under its goals rather than beside them is read as ending the goals there rather
than as stating more of them. Attributing work to a non-goal is worse than
attributing it to nothing, so that bound does not depend on how the document was
nested.

**A wrapped goal is recorded whole and reported anyway.** Rejoining is what
closed the silent truncation that recorded only a goal's first line, so nothing
is refused over a wrap and work naming the whole statement still resolves. What
`yoyo goals list` says on stderr about one is that the rejoining is a reading of
the file rather than something the file states: the words an attribution has to
match exist only once the wrap is put back together, and an indent, or a wrapped
line that reads as the `Supports:` trailer, changes the recorded goal without
changing a word of it. A goal written on one physical line cannot be changed that
way, which is why the convention is worth holding rather than merely tolerating
the wrap. Only a goal in a document still in force is reported, for the same
reason a broken link upstream is only reported for one: a goal in a superseded
document is not one work can name.

| Reported as | What it is | What it means for the work |
| --- | --- | --- |
| `attributed` | Names a goal an in-force goals artifact states. | The chain holds. |
| `unresolved` | Names something no in-force goals artifact states. | A claim that is wrong. Admission is refused, and an item already carrying one is reported for correction. |
| `unattributed` | Names no goal at all, and the tracker witnesses none was ever written. | Work admitted before this check existed. Grandfathered: reported, never refused, and nothing stops it running. |
| `lost` | Names no goal, on an item the tracker witnesses one was written onto. | A record that was destroyed rather than never made. Reported and failed. Where the witness kept the words, they are quoted and putting them back is a restoration rather than a fresh judgement; where it kept only that a goal was written, the words have to be recovered from outside the tracker. |
| `uncheckable` | The repository records no goal in force, or the goals could not be read. | Nothing was checked, and it is said so rather than reported either way. Admitting work without asking is refused here, because an attribution nobody could check is not one the operator agreed to; the work is proposed instead and they decide, which is how a repository with no goals yet files the work of writing them. |

Wording is compared with case, surrounding and repeated whitespace, and trailing
sentence punctuation folded. Nothing else is guessed at: a paraphrase is
`unresolved` with the goals documents named, because deciding it was near enough
is the inference a resolved attribution exists to replace.

An attribution is written on the item as a `Goal served:` line — by the creation
that admitted the work, or by an `attribute` action afterwards, appended to what
the item already records rather than replacing it. The newest such line is the
item's current claim, so the goal an item was admitted under is never rewritten
and the record of how it came to be attributed survives.

Every write that puts a goal into an item's notes also records that goal in the
tracker's own metadata for the item, under `yoyodyne_goal_recorded`. It exists
because the notes are what gets destroyed: `yoyo` only ever appends to them, but
anything else with the tracker's command line can replace them wholesale, and it
has. Six items lost the goal they were created under that way and read
afterwards exactly like work admitted before the check existed, which is the one
state nothing fails on. The witness is outside the reach of the write that does
the damage, so it survives to say both that an attribution was destroyed and
which one.

The notes stay the record. What an item serves is resolved from them and only
from them, and the copy in the metadata is never read as an answer — an
attribution the notes lost and the metadata answered for would report as intact
while the item stayed empty, which is the same silence arrived at from the other
side. The copy says what to put back, and putting it back is a `Goal served:`
line written onto the item like any other. A goal longer than a goals document
may state is witnessed without its words rather than stored cut in half.

**The witness covers a goal only from the moment it is written.** An attribution
made before this existed carries none, so replacing its notes reads as work
nobody ever attributed and does not fail the audit. `yoyo goals witness` closes
that gap over a backlog: it records, on every admitted item whose notes state a
goal and which carries no witness, the goal those notes already state. It writes
no attribution and decides nothing — the statement is the item's own, copied to
where a careless writer cannot reach it — and it is worth running once after
upgrading, and again after any bulk import of work attributed elsewhere.

```sh
yoyo goals list          # the goals work may be attributed to, and where each is stated
yoyo goals attribution   # what each admitted work item says it is for
yoyo goals witness       # witness the goals already recorded on admitted work
```

`attribution` exits non-zero for an item whose attribution is `unresolved` or
`lost`, and zero for one with none. That asymmetry is the decision, not an
oversight: an item admitted before goals were checked is somebody's to attribute,
and a rule that failed every one of them would stop a backlog to close a gap that
has cost nothing yet. An item that lost the goal it recorded fails for the
opposite reason — it passed the check, and what is wrong is that the record of it
was written over. Attributing one is a judgement about what the work is for, so it
is the product manager's to make in conversation and there is no command here that
makes it.

That leaves a pass still owed. When the check arrived, Yoyodyne's own backlog
carried one attribution across seventeen open items, and **the rest are still to
be attributed**: until they are, `yoyo goals attribution` reports most of the
queue as naming no goal, and that is the queue's real state rather than a
reporting artefact. Grandfathering is what keeps the work running while the pass
is outstanding; it is not a substitute for making it. The pass is made by the
product manager in conversation, working from `yoyo goals attribution` and using
the `attribute` action on each item — which appends, so nothing already recorded
is lost.

## What a change upstream leaves stale

A goal can be amended while the designs that serve it and the work admitted
under its old wording carry on unchanged. The amendment is somebody exercising
authority over their own document; the silence after it is the problem, and
`yoyo stale` ends it.

```sh
yoyo stale          # what a change upstream left unanswered downstream
yoyo stale --json   # machine-readable
```

| Reported as | What it is |
| --- | --- |
| a document | An artifact something upstream of it — through `supports`, as far as the chain runs — changed after the artifact itself was last revised. |
| open work | An admitted item whose goals document, or anything upstream of it, changed after the item was admitted. |

A change is an `amended`, `superseded`, or `retired` revision. A `created` one is
not: a document that did not exist cannot be what anybody was working from. Each
report names the document that changed, when, the role whose authority it
happened under, and the reason it recorded — a rewording and a reversal of
intent are the same event without the reason.

Two things are never reported. An artifact that is itself `superseded` or
`retired` stated what was intended and stopped, so it is not asked to answer for
what happened upstream afterwards. An item naming no goal, or one the goals do
not state, has no reference to follow at all; that is a gap in the chain, and it
is [reported where attributions are](#goals-and-the-work-attributed-to-them)
rather than restated here. The counts say how many admitted items were judged
and how many were not, so what this could not answer for is never silence.

**Nothing is stored.** Staleness is a comparison over records that already
exist — each artifact's revision log, and the tracker's record of when an item
was admitted — rather than a mark somebody writes. So a document edited by hand
counts exactly as one amended through the harness, a process that dies between
the amendment and anything else leaves nothing unmarked, and there is no second
account of staleness that can disagree with the documents. What it costs is
where it clears: an artifact stops being reported once its owner records a
revision later than the change, which is the durable record that somebody looked
at it, and a work item carries only its admission time, so a stale item stays
reported until it is closed. The tracker's own modification time is deliberately
not used for this — it moves when the harness records what a run cost, and
staleness that vanished because a price was written would be a signal nobody
could trust.

**Stale is not cancelled.** Nothing is stopped, closed, blocked, or reordered,
and the command exits zero whatever it finds. A change to a goal's wording is
frequently not a change to what the work should do, and failing a build over an
edit would teach an operator not to edit. What happens to stale work is the
operator's decision or the owning role's; this surfaces the condition.

A tracker that cannot be read costs the work half of the report rather than all
of it: the documents still report, and the report says the queue was not read
instead of rendering it as one nothing has moved under.

## Architectural invariants

The architect's durable constraints live in a second configured directory:

```yaml
product:
  id: example
  repository: .
  invariants: docs/decisions/invariants   # the default; nothing to write down if you use it
```

An **invariant** is a cross-cutting constraint that outlives the work item that
established it — the kind a later change breaks while its own work looks
correct. One Markdown file per constraint, named by its id, with the metadata in
frontmatter and the constraint itself in two required sections:

```markdown
---
id: one-writer-per-item
title: One process at a time acts on an in-flight work item
status: active
established_by:
    - yoyodyne-ifd.2.7
scope:
    - internal/runstate
revisions:
    - action: created
      by: architect
      at: 2026-08-17T12:00:00Z
      reason: extracted from the decision that added the reservation
---

## Must hold

Every entry into an in-flight run takes the run's exclusive lease first.

## Why

The lease is the only thing keeping two processes off one in-flight item.
```

Only files directly in this directory are read, because the file name is the
identity; a `.md` filed in a subdirectory is reported rather than read. `scope`
is optional: an invariant without one is repository-wide and reaches every work
item, and a scoped one is delivered when the work item's prose — or, for the
reviewer, the change itself — names a path it constrains. A missing directory is
not an error; the project simply records no invariants.

Writing these by hand works, and `yoyo invariant create|amend|retire` is the
supported path: it validates the constraint, records who changed it and why, and
refuses every role but the architect. Retirement sets `status: retired` and
records the reason. The file stays and stops being delivered, because an
invariant that vanished leaves whoever read it last month with no way to find out
it was lifted.

A file in this directory that cannot be read as an invariant is **reported and
not delivered**, which is the opposite of how a malformed specification is
handled and deliberately so: half a constraint is not one a developer can be held
to. `yoyo invariant list` names it on stderr, the gap is stated in the prompts
the harness builds, and it is recorded on the work item, so a set that is missing
something never looks complete.
