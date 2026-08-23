# Artifacts, goals, and invariants

*For an operator maintaining the brief, the goals, the designs, and the
invariants. Part of [yoyo's documentation](../README.md#further-reading).*

## Artifact identity

The documents upstream of a work item — the brief, the goals, the designs and
specifications under `product.designs`, and the decision records under
`product.decisions` — each carry a stable identity in frontmatter: an id, a
kind, a lifecycle status, what they support upstream, and a revision log. It is
the same model the invariants use rather than a second one beside it. The file
name is the id, an id that disagrees with its file name is refused, and two
files claiming one id refuse both, each naming the other.

```sh
./bin/yoyo artifact list
./bin/yoyo artifact show v1-goals
```

Your approval of one of these documents lives in the same frontmatter, and it is
recorded against the revision it was given for:

```sh
./bin/yoyo artifact approve v1-goals --reason "approved in conversation on 2026-08-17"
```

That is what makes an approved goal and a draft one different things to
everything downstream, instead of two identical documents whose difference lived
in a chat log. Because the approval names a revision and the revision log is
append-only, a document amended after you approved it reads as
approved-and-amended-since rather than as approved — the approval still stands
for what you gave it for, and the document as it now reads is not that. What is
asked of you is your configuration's to say: `approvals.brief` and
`approvals.goals` are `human`, `approvals.designs` is `automatic`, and a decision
record is an account of how something was decided rather than a statement of
intent, so nothing asks you to approve one.

**Recording an approval gates one thing: what reaches the work queue.** An
unapproved document still loads, still governs what is downstream of it, and
stops nothing that reads it, and approving writes nothing but the approval — the
document itself stays the owning role's to change. What your approval of the
goals decides is whether work serving them is admitted without asking you, which
is [`approvals.work_items`](configuration.md#what-reaches-the-queue) to say:
it is `human` until you set it otherwise, and every item is put to you. Set it to
`automatic` and your approval of the goals document is what lets work serving
those goals into the queue — so a goals document nobody approved, and one amended
since you approved it, are documents nothing is admitted under. Everywhere else
an amendment after approval changes what is reported about a document rather than
what is allowed. The
[configuration guide](configuration.md#approving-a-document) has the schema
and what is refused.

A document in one of those directories with no usable identity is named on
stderr rather than governed under a guessed id, so a home you have not given
identity to yet says so. Nothing else changes for it: a specification with no
frontmatter is still read as product intent, because refusing intent somebody
wrote down is worse than reading it and saying its identity is missing.

Who may change one of these documents is in the code rather than in a persona.
The product manager owns the brief and the goals, the architect owns the designs,
specifications, and decision records, and the development manager owns no
document at all. Creating, amending, superseding, and retiring an artifact each
refuse a role that does not own the kind, the way the invariants already do, and
that boundary is what a document written from a conversation goes through: the
role writes it, you approve it, and the write happens under that role's
authority or not at all. What also runs on every load is the other half: a document
whose revision log records a change by a role that does not own it is reported,
naming the file and the entries that crossed. It is reported rather than refused
because the log is append-only, so losing the document would leave one that could
neither load nor be corrected. None of it constrains you: the boundary is between
agent roles, and you direct any of them.

A role that meets that boundary is not left with nothing to say. It proposes the
change instead, the proposal reaches the owner and you, and only a decision on it
is ever recorded — see [what agents propose changing, and who
decides](reporting.md#what-agents-propose-changing-and-who-decides).

The chain that identity makes expressible is then checked, every time the
artifacts are loaded: a `supports` entry naming an id no artifact answers to is
reported with both ends named, and an artifact that nothing connects back to the
brief is reported as an orphan. Neither refuses the document — a broken
relationship is a name to correct, not a reason to lose what somebody wrote. The
brief is the root and a decision record is not downstream of intent, so neither
is asked to support anything. The
[configuration guide](configuration.md#traceability-references-and-orphans)
is the reference for the schema, the fields, and what is reported.

## Writing a document from a conversation

A document the owning role drafted used to reach the repository by hand: fenced
Markdown in a reply, your approval in prose, and then you or an agent of yours
working out the path, writing the frontmatter, and committing it. The drafted
content was rarely the part that went wrong — the transcription was.

So a document is written the way work is proposed. Ask the product manager for
the goals or the architect for a design, and what comes back is prose you read
plus a typed action carrying the document. Nothing is written yet. You are shown
what would happen and the document itself, and asked:

```
document document-4.1 · create v2-goals (goals) in docs/product
  title: What v2 is for
  because: drafted with you in this conversation

  # Goals
  ...

create v2-goals (goals) in docs/product? [y or yes writes it and records your
approval in it; anything else declines, and is kept as the reason]
```

On your `y` the harness performs the write itself: it files the document in the
artifact home, generates the frontmatter the contract requires, records the
revision under the role that wrote it, and records your approval against that
revision. `yoyo artifact show v2-goals` then reads back exactly what any
hand-written document reads back as, because it is one. Anything else declines,
and what you said is kept as the reason.

Nothing about that widens what a role may do. The write goes through the same
ownership boundary every other change to these documents goes through, so the
architect cannot write the goals and the product manager cannot write a design —
each proposes to the other instead. Each kind also has one home and is written
only there: the brief, the goals, and the non-goals go under
`product.specifications`, designs and specifications under `product.designs`,
and decision records under `product.decisions`. A role is told which of those its
own kinds go in rather than being handed the list, so a design filed under the
product manager's home is refused even though that is an artifact home — it is
not the one a design is filed in.

A kind the role does not own, a document filed anywhere but its kind's home, a
revision of a document that belongs to another role, a revision of one that was
superseded or retired, and a block the harness cannot read are all refused before
anything is written, and you are never asked to approve one. A revision naming a
document nothing records is refused the same way, saying that a document which
does not exist yet is created rather than revised — and a creation over an id
something already answers to is refused for the mirror of that reason. A role
that owns no document at all — the development manager, the developer, the
reviewer — cannot write one under any circumstances.

A revision is the same action, carrying the document whole:

```sh
./bin/yoyo chat --message "revise the second goal to name the adoption path"
./bin/yoyo chat --message "approve document-5.1"
```

Deciding it as its own message is what makes this work outside an interactive
conversation: the drafted document is recorded with the conversation, so it
survives the process that wrote it and your approval names it hours later. An
approval sent as a message has to name the document — a bare "yes" decides
nothing, because a message is not an answer to a question you were just asked.
What is left undecided when a conversation ends is named on the way out.

**What this does not do is commit or publish, and it says so every time it
writes one.** The document lands in your working tree with the rest of your
changes; committing it and opening a pull request for it are still yours. Commit
it before you start a run: a run refuses to begin while the primary checkout has
uncommitted changes, and a document you approved and left uncommitted is one of
them.

Whether the harness should go further — commit the document, or open a pull
request for it — is an open question rather than a settled boundary, and it is
the architect's to answer. An amendment asking the design to record the answer is
filed; `yoyo amendment list --owner architect` says where it stands.

## Goals, and what work serves them

Identity ends at the document. The last link of the chain is the goal a work
item names, and that link is closed by reading the goals out of the goals
artifacts themselves: every statement under a goals document's `Goals` heading
is a goal work can be attributed to, and an attribution resolves by naming one
of them in the words that document states it in.

```sh
./bin/yoyo goals list          # the goals work can be attributed to, and where each is stated
./bin/yoyo goals attribution   # what each admitted work item says it is for
./bin/yoyo goals witness       # witness the goals already recorded on admitted work
```

Nothing there writes an attribution: what a piece of work is for is a product
judgement, made by the product manager in the conversation where you can see it,
the same way the documents themselves are. What the harness owns
is resolving the claim. An item that names no goal at all and one that names a
goal your goals do not state are reported apart and treated differently, because
they are not the same thing to do: the first predates the check, is somebody's
to attribute, and never stops the work running; the second is a claim that is
wrong, and it is what `yoyo goals attribution` exits non-zero for.

There is a third way to record no goal, and it is reported apart from both.
`yoyo` only ever appends to an item's notes, but anything else with the tracker's
command line can replace them, and a replacement that does not carry the goal
forward destroys it — which has happened, to six items at once, and read
afterwards exactly like work admitted before the check existed. So every write
that puts a goal into an item's notes also records that goal in the tracker's
metadata, where replacing the notes cannot reach it. An item carrying that
witness and no goal has lost one rather than never had one: it is reported as
`lost`, it exits non-zero, and the words it lost are quoted so putting them back
is a restoration rather than a fresh judgement about what the work is for. The
notes stay the record — what an item serves is resolved from them and never from
the copy, because a loss the report answered out of metadata would read as intact
while the item stayed empty.

The witness covers a goal from the moment it is written and no earlier, so an
attribution made before it existed is protected by nothing. `yoyo goals witness`
sweeps that up: it records, on every admitted item whose notes already state a
goal and which carries no witness, the goal those notes state. It decides
nothing — the statement is the item's own — and it is worth running once over an
existing backlog.

A goals document nobody can read goals out of — one with no `Goals` heading, or
with nothing stated under it — is named on stderr rather than quietly shrinking
the set work may be attributed to, and a repository with no goals in force is
told that nothing was checked rather than having its queue reported as
unattributed.

The link the other way is read too. A goals document's frontmatter says the
document serves the brief; it says nothing about which of the brief's goals any
one entry in it reaches, and that is the link a goal states in an emphasized
`*Supports: ...*` line directly under it — indented with the entry and with no
blank line between, or the trailer is not read as part of the goal. `yoyo
goals list` resolves each one against the
goals the brief itself states — named by the claim each opens with — and prints
it beside the goal. A goal that names nothing upstream, and one naming a brief
goal the brief does not state, are reported on stderr; a brief that states no
goals at all is reported once, naming the brief, rather than against every goal
below it. Nothing is refused over a broken link: the goal is still what the
document states and work naming it still resolves, because what is wrong is the
chain above it rather than the goal.

## What a change upstream leaves stale

Amend a goal and the documents that serve it, and the work admitted under its
old wording, may no longer be right — and until now nothing said so. `yoyo
stale` says it:

```sh
./bin/yoyo stale          # what a change upstream left unanswered downstream
./bin/yoyo stale --json   # machine-readable
```

An artifact is reported when something it traces to upstream — the goal a design
serves, the brief a goal serves, through as many links as the chain runs —
recorded a change after that artifact was itself last revised. An admitted work
item is reported when the goals document stating the goal it serves, or anything
upstream of that, changed after the item was admitted. Each one names what
changed, when, under whose authority, and the reason that change recorded, which
is what tells a rewording apart from a reversal of intent.

Nothing is stored to make this true and nothing has to be marked. The documents'
own revision logs and the tracker's record of when each item was admitted
already say it, so any reading reports the same thing, a document edited by hand
counts exactly as one amended through the harness, and there is no second
account of staleness that can drift from the documents it describes. What it
costs is that it clears only where those records say so: an artifact stops being
reported once its owner records a revision later than the change — the durable
record that somebody looked — and a work item carries only its admission time,
so a stale item stays reported until it is closed.

**Stale is not cancelled.** Nothing is stopped, closed, blocked, or reordered,
and `yoyo stale` exits zero whatever it finds. A change to a goal's wording is
frequently not a change to what the work should do, and a harness that failed a
build or cancelled a queue over an edit would teach you not to edit — which
costs more than the divergence this reports. What to do about each of these is
yours to decide, or the owning role's.

A tracker that cannot be read costs the work half of the report and not the
whole of it: the documents still report, and the queue is stated as unread
rather than rendered as one nothing has moved under.

## Architectural invariants

Some constraints outlive the work item that established them: a contract one
change created that later changes must not break. Those live under
`product.invariants` — `docs/decisions/invariants` by default — as one Markdown
file per constraint, named by its id, carrying what must hold, why, what
established it, and a recorded revision history. They belong to the architect,
and it is worth being precise about which half of that is enforced and which is
not. Recording, amending, and retiring one goes through a single code path that
refuses every role but the architect, so `yoyo invariant` and any future
architect agent are bound by it and no other role has an authorized way to write
one. A developer, though, has a shell in its worktree, exactly as it does for
the [pushes and merges the harness never routes through an agent](designs/v1-harness-design.md#what-is-enforced-and-what-is-not):
what stands in the way of it editing an invariant is its contract, which forbids
it and tells it to propose the amendment instead, and the reviewer, which is
told that a change creating, amending, retiring, or editing one is a finding.
Treat "only the architect changes an invariant" as the authorized path plus a
caught one rather than as something the harness makes impossible.

They exist because a change whose own work is correct can still break something
outside its scope, and they are only worth having if they reach the people doing
the work. The harness selects the ones relevant to a work item and delivers them
into the developer's context and the reviewer's evidence, so nothing depends on
whoever wrote the bead having remembered the constraint. Selection is what keeps
that affordable: an invariant with no declared scope is repository-wide and
reaches every item, and a scoped one is delivered when the evidence names a path
it constrains. The developer's evidence is the work item's own prose; the
reviewer's adds the change itself, so an invariant scoped to code the item never
mentioned still reaches the gate that judges the change that touched it. The
reviewer is told that a change violating a delivered invariant draws a finding
naming it, and that editing one is a finding of its own. The limits are stated
where they apply: the delivered set is never presented as the whole set, an
invariant the harness could not read is named as a gap rather than dropped
silently, and what was delivered is recorded on the work item so an operator can
see afterwards which constraints applied.

The architect can be asked what an invariant should say — `yoyo agent chat
architect` — and it cannot write one. The typed write a conversation has covers
the artifact kinds, and an invariant carries an identity scheme of its own that
no conversation action reaches. So the lifecycle is reachable from the command
line, acting with the architect's authority and recording that it did:

```sh
./bin/yoyo invariant list
./bin/yoyo invariant create \
  --title "One process at a time acts on an in-flight work item" \
  --statement "Every entry into an in-flight run takes the run's exclusive lease first." \
  --rationale "The lease is the only thing keeping two developers off one item." \
  --established-by yoyodyne-ifd.2.7 \
  --scope internal/runstate,internal/orchestrator \
  --reason "extracted from the decision that added the reservation" \
  one-writer-per-item
./bin/yoyo invariant show one-writer-per-item
./bin/yoyo invariant retire --reason "the reservation moved into the store" one-writer-per-item
```

Retirement is explicit and recorded rather than a deletion: the file stays, the
constraint stops being delivered, and the reason it was lifted is readable by
whoever read the invariant last month. A repository with no invariants directory
simply has none, and runs are unaffected.
