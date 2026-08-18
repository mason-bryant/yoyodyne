# Yoyodyne configuration

**A Yoyodyne project owns its configuration outright.** `yoyo init` writes a
complete `.yoyodyne/config.yaml` — every agent, backend, model selector,
instance count, and persona reference stated in the file — and copies the
personas themselves into `.yoyodyne/personas/`. Nothing is inherited at load
time, so what the file says is what runs, and an edit to it is an edit to the
harness's behavior with nothing in between.

The executable still contains a versioned, read-only bundle of agent definitions
and personas. It is the **template `init` generates from**, not a layer
underneath your project. A project therefore never needs access to the Yoyodyne
source checkout, and nobody reading its configuration has to be told where a
value came from.

**What owning your defaults costs.** A later Yoyodyne that improves a persona or
corrects a model selector does not reach a project that already has its own
copy. There is no mechanism that reconciles the two: re-run `yoyo init` in a
scratch directory, diff it against yours, and merge what you want. That is a
deliberate trade for a tool whose operator reads and edits the file often — the
effect of an edit is obvious, which matters more here than shared improvement.
Inheritance is still supported for projects that would rather have the other
half of that trade; see [Extending a built-in bundle](#extending-a-built-in-bundle).

## Creating a project configuration

```sh
yoyo init                              # configure the current directory
yoyo init --directory path/to/project  # configure another one
yoyo init --product example            # name the product explicitly
yoyo init --force                      # overwrite what is already there
```

`init` writes `.yoyodyne/config.yaml` and one Markdown file per persona under
`.yoyodyne/personas/`, then loads what it wrote and fails if the result is not
usable. Without `--product`, the product is named after the directory being
configured; a directory name that is not a valid identifier is refused rather
than mangled, and `--product` names one instead. Nothing is overwritten without
`--force`, and a refusal happens before any file is written, so a project is
never left half-configured.

One thing the generated file deliberately leaves empty is `checks`. The harness
cannot guess a project's toolchain, and a run with nothing to verify has no gate
to integrate behind, so `yoyo run` refuses one. Fill the list in — the generated
file carries commented examples for Go, TypeScript, Python, and Java — before
running work.

## Layout

A project keeps its configuration in a `.yoyodyne` directory at its root:

```text
.yoyodyne/
  config.yaml          # the project configuration
  personas/            # one Markdown file per agent persona
    product-manager.md
    architect.md
    development-manager.md
    developer.md
    reviewer.md
```

Everything under `.yoyodyne/` is machine-independent and belongs in version
control. Run state, provider event streams, locks, worktrees, and the reports
agents file while their work carries on live outside the repository under an
operating-system state directory, so nothing there depends on where the project
is checked out.

What `init` writes looks like this, with the explanatory comments trimmed:

```yaml
version: 1

product:
  id: example
  repository: .
  specifications: docs/product
  invariants: docs/decisions/invariants
  designs: docs/designs
  decisions: docs/decisions

execution:
  max_concurrent_developers: 1
  repair_attempts_before_replan: 2
  integration_retries_before_reconciliation: 2
  worktree_root: auto
  remote: origin
  usage_limit_max_pause: 6h
  usage_limit_in_process_pause: 6h
  usage_limit_unknown_reset_pause: 30m

approvals:
  brief: human
  goals: human
  designs: automatic
  integration: human
  publishing: human

checks: []          # yours to write; a run with none is refused

agents:
  product-manager:
    role: product-manager
    backend: claude-code
    model: opus
    instances: 1
    persona:
      version: v1
      path: personas/product-manager.md
  # ... architect, development-manager, developer, and reviewer, the same shape
```

Five agents — product manager, architect, development manager, developer, and
reviewer — each with a role, a backend, a model selector, an instance count, and
a persona file that is in the repository beside the configuration. Change one by
editing it. Remove one by deleting its block. Nothing has to be expressed as a
deviation from something invisible.

## Discovery

Yoyodyne looks for a configuration in this order:

1. the path given to `--config`, if present;
2. otherwise `.yoyodyne/config.yaml`, searching from the current directory
   upwards to the filesystem root;
3. otherwise `.yoyodyne.yaml` in the same directories.

Because the search walks upwards, `yoyo run` works from the project root or
from any directory beneath it. When both forms exist in one directory, the
directory form wins, so a half-finished migration cannot silently keep using the
old file.

Relative paths inside the configuration — `product.repository` and a non-`auto`
`execution.worktree_root` — resolve against the project directory, which is the
parent of `.yoyodyne`, not the `.yoyodyne` directory itself. `repository: .`
therefore keeps meaning the project root. The artifact directories —
`product.specifications`, `product.invariants`, `product.designs`, and
`product.decisions` — are the exceptions, and deliberately: each names a directory
*inside the repository being worked on*, so all four resolve against
`product.repository` and are refused if they leave it.

## Precedence

A configuration `init` wrote has one layer: itself. Every configured value comes
from the project file, and nothing is inherited from a bundle. One value is
still reported as computed rather than written: `product.repository_id` has the
origin `derived:product.id`, because the generated file states the product id
and lets the repository id follow from it. That is a value derived from
something in the same file, not something arriving from outside it.

The rest of this section describes what happens when a project uses `extends`,
and what the harness still fills in when a file leaves something out.

Up to three layers produce the effective configuration, later ones winning:

1. **Harness defaults.** Values the harness fills in when nothing else supplies
   them: `product.specifications` (`docs/product`), `product.invariants`
   (`docs/decisions/invariants`), `product.designs` (`docs/designs`),
   `product.decisions` (`docs/decisions`), `execution.max_concurrent_developers` (1),
   `execution.repair_attempts_before_replan` (2),
   `execution.integration_retries_before_reconciliation` (2),
   `execution.worktree_root`
   (`auto`), `execution.remote` (`origin`),
   `execution.usage_limit_max_pause` and
   `execution.usage_limit_in_process_pause` (`6h` each),
   `approvals.publishing` (`human`), and an agent's `instances` (1).
   `approvals.publishing` is the only approval with a harness default, because
   it was added after configurations existed: a file written before it keeps the
   behavior it was written for — the harness publishes nothing — rather than
   failing to load for not mentioning a key that did not exist yet.
2. **The built-in bundle**, named by `extends`, and present only if a project
   asks for it. Today the only bundle is `builtin:v1`. It supplies `execution`,
   `approvals`, and the five default agents. It deliberately supplies no
   `product` and no `checks`, because those describe the project rather than the
   harness.
3. **The project configuration**, which overlays whatever it names.

A configuration with no `extends` key — which is what `yoyo init` writes — is a
complete standalone file: it inherits nothing but the harness defaults, and must
declare everything it needs.

`version` is the one field a project never inherits. It must be declared even
when `extends` names a bundle that declares its own, because a version taken
from the bundle would let a file written against a different schema load as
whatever the bundle happened to say — which is what the version exists to
prevent.

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

- A run integrates only behind deterministic checks and an independent review.
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
does with that signal is the [persona's](#personas) — the built-in one opens a
project with no brief or goals by asking what the product is for and offering to
draft them, and a project that wants something else replaces that guidance like
any other part of the persona.

### What the product manager does not see

**Only the specifications directory and the tracker.** No `README.md`, no
architecture or operator documentation, no source. This is a deliberate trade
rather than an oversight, and it is worth knowing which half you are getting.

Product intent is what the specifications say. A README, a design document, and
an operator guide describe how the product is built and run; they are owned by
other roles, and they go stale against the code without anybody noticing.
Handing all of them to the role that is authoritative about intent mixes intent
with description and lets a stale description be reported as current product
fact — which is exactly what happened here on 2026-08-16, when a sentence in
`README.md` reached the operator as a statement about the product.

What is given up is real: reading all of `docs/` is what let the product manager
notice a contradiction between documentation and reality, and it can no longer
do that. Reconciling accumulated documentation against the code belongs to a
role that reads the code, and the harness does not have one yet. Point
`specifications` at a wider directory if you would rather have the breadth than
the authority; the confinement rule is the only limit on where it points.

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
[its own identity scheme](#architectural-invariants). A home that does not exist
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
yoyo artifact show v1-goals         # one artifact and its revisions
```

There is no `yoyo artifact create` or `amend`, unlike the invariant commands: an
artifact's content is written by the role that owns it, and its frontmatter is
edited in the same file at the same time. What the harness owns is refusing a
document whose identity is missing, malformed, or claimed by something else, and
[reporting a change recorded by a role that does not own it](#who-may-change-an-artifact).

### Who may change an artifact

Ownership is an authorization boundary rather than a prompt convention, so it is
in code the way the invariants' is, rather than in a persona a configuration can
weaken.

| Kind | Owner | Every other role |
| --- | --- | --- |
| `brief`, `goals`, `non-goals` | Product manager | Asks questions and proposes amendments |
| `design`, `specification`, `decision` | Architect | Identifies risks, asks questions, and proposes amendments |

The development manager appears in neither row, because it owns no repository
document: its decomposition is Beads work rather than Markdown. Nothing here
constrains **you**. The boundary is between agent roles, and the operator directs
any of them.

It holds in two places, and only one of them is live today.

**Writing.** The package that writes an artifact refuses a role that does not own
the kind, on creating, amending, superseding, and retiring one, and records the
role that did in the revision log. That path exists and is enforced, but no
command reaches it yet — there is no `yoyo artifact create`, and the roles that
would use it do not run — so today it constrains nothing that is actually
happening. It is the boundary a role meets when it arrives, rather than a persona
asking it to behave.

**Reading.** A document whose revision log records a change by a role that does
not own it is **reported every time the artifacts are loaded**, as an
`unauthorized-revision` beside the [broken relationships](#traceability-references-and-orphans),
naming the file and which entries crossed. This is the half that bites now: it
catches a hand-edited log wherever it came from.

It reports rather than refuses, deliberately. The revision log is append-only, so
a past entry cannot be made lawful without rewriting history, which is the one
thing the log exists to prevent. Refusing would drop the document out of the set,
report everything that referred to it as naming something nobody wrote, and leave
a file that could neither load nor be corrected. So the document keeps loading,
keeps governing, and stays amendable by its owner, and the entry stays reported
until somebody decides what to do about it.

What is still not enforced anywhere is an agent with an editor in its worktree
changing a document without recording that it did — the same gap the design
records for pushing and merging, where the contract in the prompt and the
reviewer are what stand in the way.

### Traceability: references and orphans

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
| `unauthorized-revision` | A revision recorded under a role that does not [own the document](#who-may-change-an-artifact). Reported once per document, naming which entries crossed, because opening the file and deciding is one job however many there are. |

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

### Goals, and the work attributed to them

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

Each goal is one top-level list entry under that heading. Prose indented under
an entry describes that goal rather than being another, and a heading below the
`Goals` heading divides the goals rather than ending them. The section ends at
the next heading at the same level or above, **or at any heading stating what
the product will not do** — a `Non-goals` heading ends it wherever it is
written, including nested inside it, so a document that files its non-goals
under its goals rather than beside them is read as ending the goals there rather
than as stating more of them. Attributing work to a non-goal is worse than
attributing it to nothing, so that bound does not depend on how the document was
nested.

| Reported as | What it is | What it means for the work |
| --- | --- | --- |
| `attributed` | Names a goal an in-force goals artifact states. | The chain holds. |
| `unresolved` | Names something no in-force goals artifact states. | A claim that is wrong. Admission is refused, and an item already carrying one is reported for correction. |
| `unattributed` | Names no goal at all. | Work admitted before this check existed. Grandfathered: reported, never refused, and nothing stops it running. |
| `uncheckable` | The repository records no goal in force, or the goals could not be read. | Nothing was checked, and it is said so rather than reported either way. |

Wording is compared with case, surrounding and repeated whitespace, and trailing
sentence punctuation folded. Nothing else is guessed at: a paraphrase is
`unresolved` with the goals documents named, because deciding it was near enough
is the inference a resolved attribution exists to replace.

An attribution is written on the item as a `Goal served:` line — by the creation
that admitted the work, or by an `attribute` action afterwards, appended to what
the item already records rather than replacing it. The newest such line is the
item's current claim, so the goal an item was admitted under is never rewritten
and the record of how it came to be attributed survives.

```sh
yoyo goals list          # the goals work may be attributed to, and where each is stated
yoyo goals attribution   # what each admitted work item says it is for
```

`attribution` exits non-zero for an item whose attribution is `unresolved` and
zero for one with none. That asymmetry is the decision, not an oversight: an
item admitted before goals were checked is somebody's to attribute, and a rule
that failed every one of them would stop a backlog to close a gap that has cost
nothing yet. Attributing one is a judgement about what the work is for, so it is
the product manager's to make in conversation and there is no command here that
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

## Checks

Each entry runs through `/bin/sh -c` in the run's worktree, so shell syntax is
available. A check must be non-interactive and must exit non-zero on failure: a
failing check ends the run before any reviewer is asked and before anything can
be integrated. Checks are the project's own — the bundle supplies none — and the
list is replaced wholesale rather than merged.

```yaml
# Go
checks:
  - go test ./...
  - go vet ./...
  - gofmt -l . | (! grep .)

# TypeScript / Node
checks:
  - npm ci
  - npx tsc --noEmit
  - npm test -- --run
  - npx eslint .

# Python
checks:
  - python -m pytest -q
  - python -m ruff check .
  - python -m mypy .

# Java (Maven)
checks:
  - mvn --batch-mode --quiet verify

# Java (Gradle)
checks:
  - ./gradlew --no-daemon check
```

Note the shape of the Go formatting check. `gofmt -l` exits 0 even when it
lists unformatted files, so `gofmt -l .` on its own is not a gate: it reports a
problem and then passes. A check has to turn that output into a non-zero exit,
as above or in a Makefile target. This repository learned it the ordinary way,
by integrating an unformatted file through a green check run.

Prefer the non-interactive, non-daemon, pinned-install form of each tool. A
check that prompts, starts a watcher, or resolves dependencies differently
between runs makes the integration gate nondeterministic.

## Publishing through pull requests

By default Yoyodyne is entirely local: it creates a branch and a worktree, runs
the work, and fast-forwards your target branch. Nothing is pushed, and a
repository with no remote never notices publishing exists.

A project opts in the way it opts in to automatic integration. **Both settings
matter**: publishing opens the pull request, and integration is what merges it.

```yaml
approvals:
  publishing: automatic
  integration: automatic   # required for the harness to merge what it opened

execution:
  remote: origin   # the default; name another remote if yours is not origin
```

With both on, a run works like this:

1. **The developer phase publishes.** When a developer attempt finishes, the
   harness commits its work under its own identity, pushes the run branch, and
   opens a pull request against the target branch. Each repair attempt pushes
   onto the same branch and updates the same pull request, so one change never
   ends up with two places to be reviewed. This happens *before* the checks run:
   a pull request is where work is reviewed, and work that does not pass yet is
   exactly what a reviewer should be able to see.
2. **The reviewer's verdict merges it.** An approving verdict authorizes the
   merge, and the harness asks the forge to perform it — it never pushes your
   target branch. Nothing about the gate changes: the same passing checks, the
   same independent-reviewer evidence, and the same fast-forward rule that gate
   integration also gate the merge, and the remote target is checked again right
   before the call, so a target that moved in the meantime refuses the merge
   rather than having the forge reconcile it.
   The merge is asked for as of *when your branch protection is satisfied*
   rather than as of now, so required checks that are still running are waited
   for by the forge instead of refused seconds after the approval. Administrator
   override is never used to get past them. Waiting that way needs **"Allow
   auto-merge"** enabled in your repository settings, which is off by default;
   when it is off and nothing is holding the pull request back, the harness
   simply merges, so a repository without branch protection needs no setting
   changed at all. Only the combination of the two — something holding the
   request back and no way to queue the merge behind it — cannot be published
   to, and the run says exactly that and names the setting rather than
   reporting a merge that mysteriously fails.
3. **The merge method is a merge commit.** The harness names it rather than
   taking your repository's default, because it is the only method that puts the
   reviewed commit itself on your target branch. A squash replaces it with a
   commit nobody reviewed, and GitHub's rebase always rewrites what it merges —
   new committer, new SHA, even when the request needs no rebasing — so both
   would leave the remote carrying a copy of the work your local branch does not
   have. The method is recorded on the run and on the work item, along with the
   commit the merge produced.
4. **The merge is confirmed, then the branch is cleaned up** on both sides,
   locally and on the remote, on the same compare-and-swap evidence. The
   confirmation waits briefly and boundedly, because a forge's own record of a
   request can lag the merge it just performed. If the forge refuses outright —
   a request that conflicts with its base, a merge method the repository
   forbids — the run reports which requirement was unmet rather than a generic
   failure.
5. **A merge the forge queued ends the run rather than being waited for.** It
   lands minutes later, when your checks pass. The run reports the pull request
   as queued and finishes: your change is already in the local target branch,
   which is the authoritative one, and the run branch stays on the remote
   because that is what the forge still has to merge. `yoyo reconcile` settles
   it afterwards — it asks the forge, and either finishes the publication (merge
   commit recorded, remote branch deleted) or, if the forge dropped the queued
   merge because something it required went unmet, reports an outstanding
   publication on the work item for you. It never merges anything itself: a
   requirement that stopped the forge is yours to satisfy.

`gh` is invoked by the harness and never by a developer or reviewer: no role is
given a credential, a tool, or a request to push or merge. For the reviewer that
is a hard boundary — it runs with no tools at all, so the role whose verdict
authorizes a merge has no way to perform one, and cannot be talked into merging
something the checks would have refused.

For the developer it is not. A developer has a shell in its worktree and runs
under your account, so it could in principle reach a `gh` you have
authenticated; what stands in the way is its backend's sandbox and the harness
contract in its prompt, not a boundary the harness enforces. What does hold is
that your local target branch is authoritative: work an agent pushed by itself
is not integrated by having been pushed, and a pull request merged behind the
harness's back moves the remote away from the local branch, which the harness's
own check of the remote target then refuses rather than force-resolves.

### Publishing without automatic integration

`approvals.publishing: automatic` with `approvals.integration: human` is
supported and does exactly half of the above: the harness pushes and opens the
pull request, and then stops. **It merges nothing.** You get an open pull
request, a run branch that stays on the remote, and a preserved worktree; you
merge, and the harness never touches any of the three afterwards.

That is deliberate rather than a gap. Merging is a promotion, promotion is what
`approvals.integration` governs, and a harness that merged under a `human`
integration policy would be taking the decision that setting reserves for you.

| `publishing` | `integration` | What you get |
| --- | --- | --- |
| `human` | `human` | Local branch and worktree, preserved for you. |
| `human` | `automatic` | Local fast-forward into the target branch, artifacts removed. Nothing pushed. |
| `automatic` | `automatic` | Pull request opened, merged on approval — or queued with the forge until your required checks pass — and the branch removed locally, then on the remote once the merge has happened. |
| `automatic` | `human` | Pull request opened and left for you. Nothing merged, nothing cleaned up. |

### Which branch is authoritative

**The local target branch.** Your work is where that branch says it is.

Merging is not a second promotion performed on the remote. The harness
fast-forwards the local target exactly as it always has, and the forge merges
the pull request carrying exactly that commit. One promotion, one reviewed
commit, the same commit on both sides.

The two branches do not end at the same commit, and no forge merge method would
let them: **the remote target is your local target plus one merge commit per
published run**, made by the forge and identical in content. The harness does
not pull that merge commit back onto your local branch, and never rewrites or
resets it. If you want the two to look the same locally, `git pull` — it is an
ordinary fast-forward onto the merge commit.

Because the forge performs the merge, the harness checks that relationship
rather than assuming it. Before the merge, the remote target must contain the
commit your promotion was made from and carry exactly its content — that is what
tells a target another run already published into from someone else's work.
After the merge, it must contain the promoted commit itself and carry exactly
its content. A forge that rewrote the commit or merged something else is
reported, not reconciled, and the run branch is left on the remote for whoever
decides which history is right.

If a promotion cannot be published — the forge is unreachable, the remote target
moved, or the forge refused the merge — the run still succeeds and closes its
item, and reports an *outstanding publication*. The change is integrated where
it counts; only its publication is unfinished, and it is reconciled by hand.
Nothing is ever force-pushed to resolve it.

### What publishing needs

- A remote by the configured name. **Without one the run is purely local**,
  reports `publishing skipped`, and behaves exactly as it did before publishing
  existed. That is a property of the repository, not an error.
- The GitHub CLI, installed and authenticated (`gh auth login`). If a project
  asked to publish and `gh` is missing or logged out, the run **fails before it
  claims anything** — a harness that quietly stopped publishing would look the
  same as one with nothing to publish.
- Permission to merge the pull request. The target branch itself is never
  pushed, so a branch protected against direct pushes — requiring a pull
  request, a build check, or a review — is merged into normally, provided the
  account `gh` is authenticated as may merge and the request satisfies whatever
  the protection requires. Only the run branch is pushed. If the protection is
  not satisfied, the run reports the unmet requirement as an outstanding
  publication.
- **Merge commits allowed** in the repository's settings, since that is the
  method the harness asks for. A repository that permits only squashing or only
  rebasing refuses the merge, and the run reports that refusal — it does not
  fall back to a method that would replace the reviewed commit with a rewritten
  copy your local branch does not have. A protection rule requiring linear
  history has the same effect.

## Waiting out a provider usage limit

When the provider reports that a usage limit is exhausted, the run pauses rather
than failing: nothing is cleaned up, the Beads item stays claimed, the worktree
and branch survive, and the developer session is kept so the reissued attempt
continues the same change. This covers both provider invocations a run makes — a
developer attempt is reissued, and a review the provider declined is asked for
again without redeveloping the change or spending a repair attempt. Two settings
bound that wait, both written in Go's duration syntax (`6h`, `90m`, `45s`):

```yaml
execution:
  usage_limit_max_pause: 6h
  usage_limit_in_process_pause: 6h
  usage_limit_unknown_reset_pause: 30m
```

`usage_limit_unknown_reset_pause` is how long a run waits before asking again
when the provider reports an exhausted limit but names no reset time. That is
not the same as having no capacity: an exhausted overage allowance reports this
way while the ordinary rolling window keeps resetting on its usual schedule, so
the work is waitable and simply carries no deadline. The wait spends the same
budget as any other, so a provider that keeps refusing reaches the maximum
rather than polling forever.

`usage_limit_max_pause` is the longest a single run will spend waiting **in
total**, across every pause it takes. The budget is per run, not per pause,
because a provider that keeps refusing would otherwise walk a run far past the
configured maximum one individually-acceptable wait at a time. A reset time that
does not fit in what the run has left is treated as no usable reset time: the run
stops and records a blocker naming what it already spent, instead of sleeping on
it. The `6h` default covers the provider's five-hour limit with slack and
deliberately stops short of its seven-day one, because a capacity problem that
would cost days needs a person rather than a timer. Setting it to `0` disables
waiting entirely, so every exhausted limit blocks immediately.

`usage_limit_in_process_pause` is how much of that bound a run will spend
sleeping inside the `yoyodyne` process. It defaults to the same `6h`, so by
default every wait the harness will take is taken here and the run continues on
its own once the limit resets. Lowering it — say to `15m` — makes a longer wait
exit instead, with the run still in flight and its deadline recorded; running
`yoyo run` on the same item after the reset time continues that same run.

Both paths record the deadline in durable run state *before* any waiting begins,
so a process that dies mid-wait loses nothing and a restart honors the same
deadline rather than retrying straight back into the limit. Nothing polls and
nothing retries before the deadline. `yoyo reconcile` leaves a paused run
alone for the same reason it leaves a repair loop alone: it is not an interrupted
run, it is a run that is owed the attempt it was refused.

A reset time that is absent, unreadable, already in the past, or beyond what the
run has left of `usage_limit_max_pause` stops the run with a blocker naming what
refused it. A reset that is not in the future is refused deliberately: a limit
still declining work while claiming it has already reset is not describing a
wait, and honoring it would mean reissuing straight back into the same refusal.
Transient throttling never reaches any of this: the provider CLI retries that
itself, and the harness does not duplicate the wait.

## Losing a race for the target branch

A run promotes its change by fast-forwarding the branch it was written against,
which requires that branch to still be where the run started from. It may not
be: another run can promote into the same branch first, and an operator who
commits to it while a run is working moves it just as effectively. The
promotion fails closed in both cases — nothing is force-merged and nothing is
reset — and the run then re-prepares rather than dying on it:

```yaml
execution:
  integration_retries_before_reconciliation: 2
```

Each retry replays the change onto wherever the target went, runs the
configured checks again, and obtains a **fresh independent review**. The earlier
approval is discarded rather than carried over: it described a diff on the base
the change no longer sits on, and an approval that survived a replay would be
authorizing a promotion nobody judged. Nothing is handed back to the developer,
so a retry spends no repair attempt — the change is not what went wrong.

Retries are counted in durable run state before each one begins, so a process
that dies mid-retry resumes against the budget it had rather than a fresh one. A
run that spends the budget stops and records a blocker on the work item saying
plainly that the checks passed and the reviewer approved, and that what needs
looking at is the target branch. Setting the bound to `0` restores the earlier
behavior: the first refused promotion ends the run.

A replay that **conflicts** is never retried and never resolved automatically.
The replay is abandoned, the branch and worktree are left exactly as they were,
both sides of the conflict survive, and the run stops with a blocker on the
item. Which side of a conflict is right is a decision about the product, not a
Git operation.

A published run's pull request follows the replay: the run branch is replaced on
the remote from exactly the commit the harness published there, so the request
carries the change that would actually be promoted. That is the same
compare-and-swap every other write makes — a remote branch carrying anything
else is refused rather than overwritten — and the refusal stops the run, because
nothing has been promoted yet and there is nothing outstanding to report.

## Merge and removal semantics

These describe how a project that uses `extends` combines with the bundle
beneath it. A configuration `init` wrote has no layer beneath it, so it is read
as written: an agent is present because it is in the file, and absent because it
is not.

- A field a layer does not mention is **inherited** from the layer beneath it.
- A field a layer does mention **replaces** the inherited value. This includes an
  explicit zero, such as `repair_attempts_before_replan: 0`.
- `checks` is replaced as a whole list rather than concatenated. Checks gate
  integration, and a silently merged list is not the gate either layer described.
- `agents` is merged by agent name. An override names only the fields it changes:

  ```yaml
  agents:
    developer:
      model: claude-opus-5-20260514
  ```

  The developer keeps its inherited role, backend, instance count, and persona.
- A `persona` override **replaces the inherited persona completely** and must
  supply both `version` and `path`. Half of one persona and half of another is
  guidance nobody wrote.
- An agent name the bundle does not define creates a new agent, which must then
  supply everything an agent requires: role, backend, and model selector.
- `disabled: true` removes an inherited agent:

  ```yaml
  agents:
    architect:
      disabled: true
  ```

  Removal is explicit, so an agent is never lost by being accidentally omitted.
  Validation still enforces the roles the invoked workflow executes: at least one
  developer agent always, and a reviewer agent whenever `approvals.integration`
  is `automatic`. Disabling either is a validation failure, not a way to skip
  review.

### What fails closed

These are all errors, reported before any work is claimed:

- a missing `version`, or a `version` this executable does not implement;
- an unknown key anywhere in the file, including a misspelled agent field;
- an unknown bundle in `extends`;
- a `disabled: true` entry that also configures fields, or that names an agent no
  layer defined;
- a persona override missing `version` or `path`;
- a usage-limit pause bound that is not a duration, or that is negative — `0`
  is accepted, because "never wait" is a choice somebody can mean;
- an `execution.remote` that is empty or is not a plain remote name, since it
  reaches a `git push` command line;
- a `product.specifications` that is empty, absolute, or climbs out of the
  repository, since it decides what the product manager reads; and the same of
  `product.invariants`, `product.designs`, and `product.decisions`, since they
  decide which documents the harness treats as canonical artifacts;
- a persona path that is absolute, traverses upward, is not Markdown, is missing,
  is empty, or resolves through a symlink to somewhere outside `.yoyodyne`;
- a role and backend combination the backend does not support, such as an
  architect on the Codex backend;
- any effective configuration that fails validation, even when every individual
  layer looked reasonable — for example `max_concurrent_developers` above the
  configured developer instances, or automatic integration with no checks.

## Personas

A persona is a Markdown file describing how an agent works. Personas specialize
behavior; they never grant it. The harness invariants — agent authority,
worktree sandboxing, the review verdict contract, integration preconditions, and
cleanup — are enforced in Go and are not configurable, so a persona cannot
weaken them:

- the developer prompt starts with the harness contract verbatim, and the
  persona follows it as subordinate guidance;
- the reviewer's system prompt starts with the immutable review contract, and
  the persona follows it; the decision vocabulary and the JSON response format
  are not negotiable, and a persona cannot authorize approving a change the
  reviewer cannot see;
- untrusted developer output is never treated as configuration, and configured
  text never replaces harness policy.

Persona rules:

- `version` is a free-form revision label recorded in the effective
  configuration, so a change of guidance is visible in diagnostics.
- `path` is relative to the project `.yoyodyne` directory, and must name a
  Markdown file inside it. Absolute paths, `..` traversal, and symlinks that
  escape the directory are rejected.
- A persona is limited to 32 KiB. It is role guidance, not a document to paste
  into every prompt.

In a project `init` wrote, every persona is already a file in
`.yoyodyne/personas/`: change how the reviewer works by editing
`personas/reviewer.md`, and bump the `version` label beside it in the
configuration so the change is visible in diagnostics.

```yaml
agents:
  reviewer:
    persona:
      version: house-1            # bumped from v1 after editing the file
      path: personas/reviewer.md
```

In a project that uses `extends`, the same block is how one inherited persona is
replaced without changing anything else.

## Extending a built-in bundle

Inheritance is a supported capability, and a project that wants it writes
`extends` instead of the agents:

```yaml
version: 1
extends: builtin:v1

product:
  id: example
  repository: .

checks:
  - go test ./...

agents:
  developer:
    model: claude-opus-5-20260514
```

That file inherits the five agents and their personas from the bundle, overlays
the one field it names, and is subject to the precedence and merge rules above.

**What it buys, and what it costs.** Upgrading the executable upgrades the
defaults and the personas the project did not override — which is exactly what
an explicit configuration gives up. What the project pins, it keeps, because a
project value always wins over the bundle. New bundle versions are added under
new names rather than by changing an existing one, so `builtin:v1` keeps meaning
what it meant when a project adopted it. Neither shape depends on where Yoyodyne
lives: both travel with the repository, and neither needs the Yoyodyne source.

Yoyodyne ships the explicit shape because its operator edits agent properties
often and wants the effect of an edit obvious. A fleet of projects that should
improve together is the case `extends` is for. A more portable configuration
system than either is still wanted, and is not designed yet.

### Converting an inheriting configuration to an explicit one

1. Record what you have now:
   `yoyo config show --effective --origins > before.txt`.
2. Run `yoyo init --force`. This overwrites `.yoyodyne/config.yaml` and the
   personas under `.yoyodyne/personas/`, so commit or stash first.
3. Re-apply what was yours: `checks`, your approval policy, and any agent field
   you had overridden. The generated file states each of them in place, so this
   is editing values rather than re-expressing deviations.
4. Run `yoyo config show --effective --origins` again and diff it against
   `before.txt`. Every origin should now be the project file, and no effective
   value should have moved except the persona sources, which are now paths
   inside your repository.

## Migrating from `.yoyodyne.yaml`

A `.yoyodyne.yaml` file still loads, so migration is optional. The simplest
route is to run `yoyo init` and re-apply what the old file said:

1. Run `yoyo init`, which writes `.yoyodyne/config.yaml` and the personas.
2. Copy your `product`, `checks`, `approvals`, and any agent deviations from
   `.yoyodyne.yaml` into the generated file, editing values in place.
3. Run `yoyo config show --effective --origins` and confirm the effective values
   match what the old file produced.
4. `git rm .yoyodyne.yaml`. While both exist in one directory the directory form
   wins, so a half-finished migration cannot silently keep using the old file.

Personas move to `.yoyodyne/personas/` and are referenced relative to the
`.yoyodyne` directory.

## Inspection

```sh
yoyo config validate                      # validate the discovered configuration
yoyo config show --effective              # the values actually in force
yoyo config show --origins                # where each value came from
yoyo config show --effective --origins    # both
yoyo config show --effective --json       # machine-readable
```

`config show` prints the layers it applied, the effective configuration as YAML,
and, with `--origins`, one line per value. Persona bodies are reported as a
source and a byte count rather than inlined, so the output stays readable.

Origins use these values:

| Origin | Meaning |
| --- | --- |
| `harness-default` | No layer supplied the value; the harness filled it in. |
| `builtin:v1` | Inherited from the built-in bundle, by a project that uses `extends`. |
| a file path | Supplied by that project configuration file. |
| `derived:product.id` | Computed from another configured value. |

An unexpected effective value is therefore a two-command diagnosis: `--effective`
says what the value is, and `--origins` says which layer is responsible for it.

In a project `init` wrote, the answer is the project file for every configured
value, and `derived:product.id` for `product.repository_id` alone — the one
value the generated file computes rather than states. Nothing reports
`builtin:v1`, and nothing reports `harness-default`, because the generated file
writes down every value the harness would otherwise have filled in. So an origin
that is neither the project file nor that one derivation means the
configuration is inheriting something, which is worth looking at.
