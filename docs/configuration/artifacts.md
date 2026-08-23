# Configuring artifact homes, identity, and ownership

Where the product manager reads intent from, what identifies a document, how an
approval is recorded, who may change which document, and what a developer's
change is refused from touching.

[The configuration index](../configuration.md) lists the other guides.

## Product specifications

The product manager builds its picture of product intent from the specifications
in one configured directory:

```yaml
product:
  id: example
  repository: .
  specifications: docs/product   # the default; nothing to write down if you use it
```

A **specification** is one Markdown file that opens with an introduction saying
what the thing is and why it exists, and states the goals that serve it after
that introduction:

```markdown
# Bounded runs

Yoyodyne runs one bounded work item at a time, because a change nobody can
review is not a change anybody can trust.

## Goals

- A run integrates only behind [protected paths](#protected-paths-in-a-developers-change),
  deterministic checks, and an independent review.
- A run that cannot finish leaves its work recoverable rather than lost.
```

The directory is walked to any depth, and every `.md` file inside it is a
specification. Its prose is checked for the introduction-then-goals shape above,
and its identity — the frontmatter naming its id, kind, status, and what it
supports — is checked separately, by [artifact identity](#artifact-identity-and-metadata).
The two are read by different things and reported differently, so a
specification with a malformed id is still read as intent, and one with no goals
still has an id everything downstream can refer to it by.

That shape is checked rather than merely described, because the goals are what
downstream work is kept consistent with and goals with nothing behind them are
not traceable to anything. A specification that does not follow it — no goals, no
introduction before them, or an empty goals section — is **reported and still
read**. `yoyo chat` names it on stderr when the conversation opens — and in the
conversation itself when `/refresh` reads the specifications again — and it is
listed for the product manager alongside the specifications themselves. Refusing
to load it would silently lose intent somebody wrote down, which is worse than
loading intent in the wrong shape and saying so.

An empty or missing specifications directory is not an error either. The
conversation says that product intent is not written down, which is a true
statement about the repository rather than a reason to fail.

The context also states, in so many words, what the directory records of the two
documents intent is written in: the **brief** saying what the product is and who
it is for, and the **goals** that serve it. Each is named with roughly how much
prose it carries, and one that carries almost none is called a placeholder — a
count beside the verdict, because how much a short document needs to say is a
judgment. A document's kind comes from the `kind:` in its
[frontmatter](#artifact-identity-and-metadata) when it has one, and from what it
is called when it does not, so a brief written by hand before anything was said
about identity still counts as one. Goals stated in the brief's own `Goals`
section count as the goals when there is no goals document — that is where the
shape above already puts them, and a project that wrote them there has written
them; once a goals document exists, that document is what the goals are read
from and the brief's section is not named beside it. What the product manager
does with that signal is the [persona's](agents.md#personas) — the built-in one opens a
project with no brief or goals by asking what the product is for and offering to
draft them, and a project that wants something else replaces that guidance like
any other part of the persona.

### What the product manager sees besides them, and what it does not

**The specifications directory, the tracker, and a description of what the
product ships today.** That last part is `README.md`, the whole configuration
reference — [the index](../configuration.md) and the seven guides beneath it,
this document among them — and the help every command prints. It is carried in a
section of its own, labeled as description of the implementation as built and
never as authority about intent. No source, no design document, and no way to
run a command.

The label is the whole of the arrangement, so it is worth reading twice. The
specifications are the only statement of what the product is for; nothing in the
shipped-surface section revises that, however emphatically it is written. Where
the two disagree, the product manager **reports the conflict** rather than
resolving it silently or repeating either side as settled product fact. That is
what makes documentation safe to hand to the role that is authoritative about
intent: it arrives as an answer to *what exists*, never to *what is wanted*.

**This reverses half of an earlier trade, openly.** Until 2026-08-18 the product
manager saw the specifications and the tracker and nothing else, narrowed on
2026-08-16 after a stale sentence in `README.md` reached the operator as a
statement about the product. What that bought is real and is kept: description
does not arrive labeled as intent, and it never will again while the section
carries its label. What it cost was underestimated. On 2026-08-18 the product
manager did not know `bin/yoyo-status` or `yoyo cost` existed until the operator
described them, drafted a work item that mis-assumed which surfaces existed, and
could not evaluate a formatting question about two real outputs it had never
seen — three failures in one day of the operator's routine interface needing the
operator to stand in as its eyes.

What is still given up is also real. Reading all of `docs/` is what let the
product manager notice a contradiction between documentation and reality, and
what it reads now is narrower than that: the design document and the decision
records are not there, because they say how the product is built and are the
half of `docs/` that made description reachable as intent in the first place.
Reconciling accumulated documentation against the code belongs to a role that
reads the code, and the harness still does not have one. Point `specifications`
at a wider directory if you would rather have the breadth than the authority;
the confinement rule is the only limit on where it points.

The documentation is read **after** the specifications have taken what they need
of the context budget, so a repository too large for both keeps the half that is
authoritative and the section names what did not fit. A repository that holds
none of this documentation is told so rather than getting a section that quietly
carries less than it says it does.

## Artifact identity and metadata

The canonical documents upstream of a work item — the brief, the goals, the
designs and specifications, and the decision records — each carry a stable
identity in frontmatter, so something downstream can refer to one durably and
the relationship can be checked rather than believed. They live in three
configured homes:

```yaml
product:
  id: example
  repository: .
  specifications: docs/product     # the brief and the goals: the defaults, so
  designs: docs/designs            # nothing to write down if you use them
  decisions: docs/decisions
```

Each home is walked to any depth, and every `.md` file inside one is an
artifact. Two names are not: a `README.md`, which is a directory index rather
than intent anything refers to, and everything inside the invariants directory,
which sits inside the decisions home by default and carries
[its own identity scheme](goals.md#architectural-invariants). A home that does not exist
is not an error — a project that has not written its designs down yet records no
design artifacts.

The metadata is the model the invariants already use, deliberately rather than a
second scheme beside it: **the file name is the id**, a frontmatter id that
disagrees with it is refused, the status is stated rather than inferred, and
every change appends to a revision log.

```markdown
---
id: v1-goals
kind: goals
title: V1 goals
supports:
    - brief
status: active
revisions:
    - action: created
      by: product-manager
      at: 2026-08-17T00:00:00Z
      reason: identity added with the artifact metadata schema
---

# V1 goals

The document itself, unchanged by any of the above.
```

| Field | Meaning |
| --- | --- |
| `id` | The stable identity, and the file's own name: `v1-goals` lives in `v1-goals.md`. Lower-case letters, digits, and hyphens. |
| `kind` | `brief`, `goals`, `non-goals`, `design`, `specification`, or `decision`. |
| `title` | One line naming what the document is. |
| `supports` | The artifacts upstream of this one, by id: the goal a design serves, the brief a goal serves. Optional — the brief is the root and supports nothing. |
| `status` | `draft` (written, not yet in force), `active` (what the product currently intends), `superseded` (replaced by a later artifact), or `retired` (stopped applying, not replaced). |
| `revisions` | Append-only: what changed (`created`, `amended`, `superseded`, `retired`), the role it was recorded under, when, and why. At least the creation is required, and the role must be the one that [owns the kind](#who-may-change-an-artifact). |
| `approvals` | Append-only, and optional: [your approval of the document](#approving-a-document), each entry naming the revision it was given for. |

Everything below the frontmatter is the document, and nothing about it is
prescribed here: a brief, a goals document, and a decision record have nothing in
common structurally. A specification's own prose contract is the
[introduction-then-goals shape](#product-specifications), which is checked
separately.

Status and revisions have to agree, the same way an invariant's retirement does.
A `superseded` or `retired` artifact must record the revision that ended it, and
one that is still in force cannot record one — an artifact whose status says it
was replaced while nothing says when or why records no decision at all. Which
artifact superseded it is not part of the schema yet; the revision's reason says
so in prose.

A file in an artifact home that cannot be read as an artifact is **refused and
named**, which is how an invariant that cannot be read is handled and the
opposite of how a malformed specification is: a document with no usable identity
cannot be referred to, and admitting it under a guessed id would be worse than
saying it is not there. That covers a file with no frontmatter, an unknown or
mistyped field, a status or kind the harness does not know, a missing revision
log, and an id that disagrees with the file name. Two files claiming one id
refuse **both** — choosing between them would hand whatever refers to that id a
document nobody decided on — and each refusal names the other file.

```sh
yoyo artifact list                  # the recorded artifacts; what is not one goes to stderr
yoyo artifact list --kind decision  # one kind
yoyo artifact show v1-goals         # one artifact, its revisions, and your approvals
```

There is no `yoyo artifact create` or `amend`, unlike the invariant commands: an
artifact's content is written by the role that owns it, and its frontmatter is
edited in the same file at the same time. What the harness owns is refusing a
document whose identity is missing, malformed, or claimed by something else,
[reporting a change recorded by a role that does not own it](#who-may-change-an-artifact),
and [recording your approval](#approving-a-document).

### Approving a document

Approving the brief and the goals is the one thing the design asks of you
routinely, and until it is written down it lives only in the conversation where
you said it — which leaves an approved goal and a draft one indistinguishable to
everything downstream. `yoyo artifact approve` records it in the document:

```sh
yoyo artifact approve brief --reason "approved in conversation on 2026-08-17"
yoyo artifact approve v1-goals --reason "approved with the adoption goal added"
```

```yaml
approvals:
    - revision: 1
      by: operator
      at: 2026-08-18T09:00:00Z
      reason: approved in conversation, with the adoption goal added
```

`at` is when the approval was **recorded**, which is not always when it was given
— an approval given in conversation is written down afterwards — so say when and
how you gave it in `--reason`, which is the half only you can attest to.

**The approval names the revision it was given for**, which is the index into the
revision log above it. The log is append-only, so that index means one change
forever, and the arithmetic that follows is the point: an approval of the last
revision is a document approved as it stands, and an approval of an earlier one
is a document that has been amended since you saw it. `yoyo artifact list` and
`show` say which:

```
v1-goals [goals, active] V1 goals
  file: docs/product/goals/v1-goals.md
  supports: brief
  approval: approved and amended since — given by the operator 2026-08-18T09:00:00Z,
            for revision 1, and one revision was recorded after it, so the document
            as it now reads is not what was approved
```

**Your `approvals` configuration decides what is asked of you.** `approvals.brief`
and `approvals.goals` are `human` by default and `approvals.designs` is
`automatic`, deliberately rather than by inheritance: the brief and the goals are
what you state and what everything else traces back to, while a design serving an
approved goal is the architect's judgement about how, and approving each one is
the per-change gate autonomy is the absence of. Approving the goals is the one
approval that then carries weight elsewhere, because it is what work is admitted
against. `approvals.goals` covers the
non-goals with the goals, because a bound on intent nobody approved is as much
unapproved intent as a goal is. A decision record is the architect's account of
how something was decided rather than a statement of what the product should do,
and no setting asks you to approve one.

**Recording an approval gates one thing: what reaches the work queue.** An
unapproved document still loads, still governs what is downstream of it, and
stops nothing that reads it. What your approval of the goals decides is whether
work serving them is admitted without asking you — see
[what reaches the queue](goals.md#what-reaches-the-queue). Everywhere else, an
amendment after approval changes what is reported about the document rather than
what is allowed, and what `human` buys you is that the difference is visible — in
the document, in the listings, and in `--json`, where each artifact's `state` is
`approved`, `amended`, or `unapproved`.

### Who may change an artifact

Ownership is an authorization boundary rather than a prompt convention, so it is
in code the way the invariants' is, rather than in a persona a configuration can
weaken.

| Kind | Owner | Every other role |
| --- | --- | --- |
| `brief`, `goals`, `non-goals` | Product manager | Asks questions and [proposes amendments](#proposing-a-change-to-a-document-you-do-not-own) |
| `design`, `specification`, `decision` | Architect | Identifies risks, asks questions, and [proposes amendments](#proposing-a-change-to-a-document-you-do-not-own) |

The development manager appears in neither row, because it owns no repository
document: its decomposition is Beads work rather than Markdown. Nothing here
constrains **you**. The boundary is between agent roles, and the operator directs
any of them.

It holds in two places, and only one of them is live today.

**Writing.** The package that writes an artifact refuses a role that does not own
the kind, on creating, amending, superseding, and retiring one, and records the
role that did in the revision log. That path exists and is enforced, but no
command reaches it yet — there is no `yoyo artifact create`, and the roles that
own documents reach no tools from a conversation — so today it constrains nothing
that is actually happening. It is the boundary a role meets when it arrives, rather than a persona
asking it to behave.

**Reading.** A document whose revision log records a change by a role that does
not own it is **reported every time the artifacts are loaded**, as an
`unauthorized-revision` beside the [broken relationships](goals.md#traceability-references-and-orphans),
naming the file and which entries crossed. This is the half that bites now: it
catches a hand-edited log wherever it came from.

It reports rather than refuses, deliberately. The revision log is append-only, so
a past entry cannot be made lawful without rewriting history, which is the one
thing the log exists to prevent. Refusing would drop the document out of the set,
report everything that referred to it as naming something nobody wrote, and leave
a file that could neither load nor be corrected. So the document keeps loading,
keeps governing, and stays amendable by its owner, and the entry stays reported
until somebody decides what to do about it.

**A third place, and the one that catches an editor.** Both halves above are
about the document — who wrote it, and what its log says. Neither notices a
developer that simply opens the file. That is what the protected-path gate below
is for, and it is why an agent with an editor in its worktree is no longer the
open case it was: the edit is refused before anybody reviews it, whatever the
revision log does or does not say about it.

### Protected paths in a developer's change

The documents above are upstream of every change a developer makes. A developer
that edits one is redefining what its own work is measured against, and reading
the diff does not tell that from a legitimate edit — both are a file that
changed. So these paths are **default-deny for a developer's diff**:

| Protected | Setting it follows |
| --- | --- |
| `.yoyodyne/` | fixed; the configuration directory |
| `docs/product/` | `product.specifications` |
| `docs/designs/` | `product.designs` |
| `docs/decisions/` | `product.decisions` |
| `docs/decisions/invariants/` | `product.invariants` |

The set follows your configuration rather than the default layout: a project
that keeps its designs elsewhere has not thereby made them a developer's to
rewrite.

**How it behaves.** The gate runs in front of the deterministic checks, on every
attempt, over every path the change touches — tracked, untracked, and both sides
of a move. A change that touches one of these paths without a grant is refused
and handed back to the same developer inside the same repair loop a failing check
uses, spending from the same budget, and the refusal names how a grant is made.
No reviewer is asked about it: the class of finding this replaces used to cost an
Opus review cycle to reach, and it costs a string comparison here. A run whose
repair budget is spent still refusing is blocked on the work item, with the
refused paths and the item's grants both named, because which of the two is wrong
is a person's decision.

**Granting a path.** An exception is declared in the work item's text, on a line
beginning with the marker:

```text
Protected-path grant: docs/designs/v1-harness-design.md
```

Several paths on one line are separated by commas or spaces, and several such
lines are read together. A grant naming a file admits that file alone; a grant
naming a directory admits what is inside it. A grant of the repository root is
not a grant, and prose that merely discusses these paths grants nothing — the
marker has to begin the line, which is why it is an unlovely token rather than a
phrase an item could produce by accident.

**Which fields count, and why it is not "whoever wrote it".** A grant is read
from the item's **title, description, design guidance, and acceptance criteria**,
and **not from its notes**. The gate does not ask who typed a grant — it cannot,
because the tracker records no authorship the harness could check. What it relies
on instead is *when*: those four fields exist before the run starts and no part of
the harness writes to them, so a grant in one of them predates the change it
admits. The notes are the opposite — the harness appends each run's own record
there, including the reviewer's summary and findings — so a grant read from the
notes could be an agent's own prose, admitted to the next run of the same item.
That is the case this gate exists to stop, so the notes do not count.

The practical consequence: **a grant written into the notes silently does not
count.** A run refused despite an item that plainly names the path is usually
this. Both the refusal and the blocker name the fields a grant is read from.

**What a grant does not do.** It admits the path; it does not decide what is
written into it. The legitimate use of the exception is recording a change
somebody already decided — an approved amendment, an operator's decision — never
delegating the deciding, so a grant should name that decision, and the reviewer
is instructed to look for it: a granted path whose item names no decided change
behind the grant is a finding at major severity or higher. The gate is a string
comparison and cannot ask this question; the reviewer can, which is why the two
halves sit where they do. A branch review is not asked it at all, because it
reads commits rather than the items their grants live in.

Nothing any agent produces during a run grants a path. A developer that
genuinely needs one says so in its summary and
[proposes the change](#proposing-a-change-to-a-document-you-do-not-own); the
grant goes into the item, which is not a developer's to write. Which role
maintains an item's text is a question about the fixed set of roles rather than
about this gate, and this document does not answer it.

### Proposing a change to a document you do not own

A role that may not edit a document and has no way to say it is wrong has two
moves left, and both are bad: build against intent it believes is wrong, or edit
the document anyway. So there is a third: one block, in the contract, carried by
every role that has it. Today that is the developer, which is the role that meets
the boundary while implementing against a document — a developer that finds the
design contradicts the goal it serves ends its reply with it:

````text
```yoyodyne-amendment
{"proposals":[{"artifact":"v1-design","change":"say which of the two orderings holds","why":"the work item cannot be implemented against both"}]}
```
````

The harness resolves the document to its kind and its kind to its owner, so who
is being asked follows from the document rather than from anything the agent
claims. A proposal naming a document nobody records is refused, because there is
no owner to decide a change to a document that does not exist, and a proposal
from the role that already owns the document is refused too: that role amends
it. **The refusal reaches you and not the agent that wrote it** — it is named on
the run's outcome beside the proposals that were kept, and nothing carries it
back into the agent's next attempt, so a role that misnames a document is not
told and will misname it the same way again. The artifact ids are what
`yoyo artifact list` prints.

**Nothing an unapproved proposal contains reaches the document, and neither does
anything an approved one contains.** A proposal carries what should become true
and why, never replacement prose — the size bound on it is what keeps it an
argument rather than an edit waiting to be pasted. Approving records that the
owner's authority came down in favour of the change; the change itself is then
made by the owner, in the document, in a revision recorded under that role.

Like a report, a proposal costs its run nothing: the run integrates exactly as it
would have, and a proposal the harness cannot read or cannot keep is named on the
outcome rather than failing the attempt it arrived with. It is durable in the
same place and for the same reason — the run that argued the design was wrong is
finished and cleaned up long before anybody decides what to do about it.

```sh
yoyo amendment list                       # what is waiting to be decided
yoyo amendment list --owner architect     # one owner's queue
yoyo amendment show <id>                  # one proposal and what became of it
yoyo amendment approve <id> --reason ...  # record the change as authorized
yoyo amendment decline <id> --reason ...  # turn it down, keeping why
```

**Every decision is yours, whoever owns the document.** An owning role that runs
is shown what has been proposed against its documents and argues for or against
it — proposals against the brief and the goals are carried into the product
manager's conversation, and proposals against the designs, the specifications,
and the decision records are carried into the architect's, each told in so many
words that it cannot decide one and cannot edit anything. Both owners can now be
asked directly: `yoyo agent chat architect` is where the argument about a design
happens. But no agent records a decision, `yoyo amendment` is the only thing that
does, and the record says you exercised the owner's authority rather than that
the owner answered — the same override path `yoyo invariant` documents. A decline
keeps the reason it was turned down with, because a proposal refused silently is
one the same argument arrives to make again.

An owning role recording its own decision is vocabulary the record already has
and nothing produces: what would make it real is a decision the harness carries
out for a role from its own reply, the way it carries out the product manager's
tracker actions. Until something does that, read "under the architect's
authority" on a decision as your judgement standing in for the role, taken after
hearing it rather than instead of hearing it.

The reviewer is deliberately not given this block. What it finds wrong with a
change is a finding, which decides whether the change is repaired; a reviewer
that could also propose amendments would have two ways to say one thing. The
product manager raises what it cannot place under a goal as a concern, which
stops and asks you, for the same reason.

A developer that could not be talked out of its argument makes it again on every
repair attempt, and the second and later copies within one run are dropped: one
disagreement is one proposal, rather than one per attempt for whoever decides to
answer several times over. Two proposals count as the same argument when they
ask for the same change to the same document; restating the reasoning does not
make a new one.

**This is a second proposal path rather than a reuse of the one the conversation
already has**, and that is worth knowing because it was not the first choice. The
product manager's work-item proposals live in the conversation that raised them,
in memory, decided inside a turn. A proposed amendment has to survive the run
that raised it, is addressed to an owning role rather than to you alone, and is
decided from the command line days later — so what carries over is the shape
(propose, never defer an edit, decide explicitly, record the decision) rather
than the code. The cost is two vocabularies for one idea: a proposal in the
conversation is a work item, and a proposal in `yoyo amendment` is a change to a
document. Consolidating them is not done.
