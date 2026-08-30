# Yoyodyne configuration

**A Yoyodyne project owns its configuration outright.** `yoyo init` writes a
complete `.yoyodyne/config.yaml` — every agent, backend, model selector,
provider account, instance count, and persona reference stated in the file — and copies the
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
yoyo init --tracker-remote <url>       # sync the tracker somewhere else
yoyo init --force                      # overwrite what is already there
```

`yoyo setup` is the same thing asked rather than typed: it runs `init` for you as
one step of a walk from a binary on PATH to an installation `yoyo doctor` calls
healthy, and it will not touch a configuration that is already there — one that
does not load is handed back with the command to edit it rather than
regenerated. Everything below is what it writes on your behalf.

`init` writes `.yoyodyne/config.yaml` and one Markdown file per persona under
`.yoyodyne/personas/`, then loads what it wrote and fails if the result is not
usable. Without `--product`, the product is named after the directory being
configured; a directory name that is not a valid identifier is refused rather
than mangled, and `--product` names one instead. Nothing is overwritten without
`--force`, and a refusal happens before any file is written, so a project is
never left half-configured.

The one thing `init` derives from the project rather than from the template is
`checks`, which it proposes by reading what the repository already declares about
its toolchain. See [What `init` proposes for `checks`](#what-init-proposes-for-checks)
for what it reads and what it does with an answer it cannot settle. A run with
nothing to verify has no gate to integrate behind, so `yoyo run` refuses one
whatever `init` found; read what it proposed before running work.

`init --json` reports it. `checks` is the list that was written; `detected`
carries every proposal with the artifact it came from, in the three lists the
generated file keeps apart — `checks` written, `candidates` found and not
settled, `alternatives` read and deliberately left out.

### When the repository ignores the configuration

`init` and `yoyo config validate` both ask Git whether the configuration they
just wrote or just read is matched by an ignore rule, and say so when it is.
Nothing fails: the files are there and valid, the exit code is what it would have
been, and the warning goes to standard error.

It is worth saying because nothing else announces it. A project whose
`.yoyodyne` is ignored is configured on the machine that ran `init` and nowhere
else — this checkout keeps reading the configuration off disk while every clone,
every collaborator, and every dev worktree, which check out tracked files only,
get a project with no configuration at all. The warning names the rule in Git's
own `<file>:<line>:<pattern>` form, so the line is findable rather than
searchable for.

A configuration that is already tracked is not ignored however loudly a
`.gitignore` names it — Git applies ignore rules to untracked paths only — so a
project that committed its configuration and later added the rule is left alone.
A rule that is local to the checkout, in `.git/info/exclude` or a
`core.excludesFile`, is reported differently: that is the supported way to keep
tool config out of a repository that is not yours to commit it to, so it is
acknowledged rather than argued with, and what the warning names is
[`init --external`](#keeping-the-configuration-outside-the-repository) for
keeping the configuration outside the repository. Nothing is said where Git
could not be asked — a project that is not a repository, a configuration kept
outside the one it describes, a Git that would not run. An external
configuration is not asked about at all: it is outside the repository by
construction, and there is nothing there for an ignore rule to reach.

`init --json` and `config validate --json` both report it under `ignored`, with
the `path` that was asked about, the `rule` Git answered with, and the `source`
file that rule lives in.

### Where the tracker syncs

`init` also points the tracker at a remote, because a tracker that syncs
nowhere is one backlog per machine, drifting apart with nothing to say so. The
default is the project's own Git remote: Beads moves its data over an ordinary
Git remote under refs of Dolt's own, so the tracker rides beside the code it
tracks — one repository, one permission model, and nothing to stand up.

- It reads the Git remote `origin` and configures the tracker remote of the
  same name to sync there, printing what it configured.
- A tracker that already has an `origin` remote is left exactly as it is, even
  when it points somewhere other than this project's Git remote: that is a
  decision `init` must not undo. A tracker whose remotes are all named
  something else is untouched too, and gets an `origin` beside them.
- `--tracker-remote <url>` names the remote instead, and replaces whatever
  `origin` currently holds — which is what a tracker kept in a repository of its
  own needs. Beads accepts any Git URL.
- A project with no Git remote, or one whose `bd` is not initialized yet, is
  told what to run rather than failing: the configuration is written and valid
  either way, so `init` still exits 0 and `init --json` reports the outcome
  under `tracker` — `configured`, `unchanged`, `skipped`, or `failed`.

A synced tracker is one operator's backlog surviving their machine, and not yet a
team sharing one: two people each running their own harness against one
repository is
[not supported](team-mode-scope.md#what-v1-supports-meanwhile), because the
coordination around the backlog — claims, reports, directives, and the budgets
below — stays on the machine that made it.

Two consequences of the tracker riding your repository are worth knowing before
you adopt the default. Its history counts against the repository's size like any
other history, and grows with the backlog rather than with the code. And a push
writes `refs/dolt/data` and a `__dolt_remote_info__` branch: GitHub carries both
without complaint, but a forge that restricts which refs it accepts, or a team
that reads the branch list closely, is worth checking before you rely on it —
that is the case `--tracker-remote` and a tracker repository of its own exist
for.

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

Committing it is the default rather than a requirement, and a contributor to a
repository they do not own has two supported ways not to: the README's
[Keeping the configuration out of the repository](../README.md#keeping-the-configuration-out-of-the-repository)
covers a `.yoyodyne` listed in `.git/info/exclude` and a configuration kept
outside the repository entirely, which
[`yoyo init --external`](#keeping-the-configuration-outside-the-repository)
writes and discovery finds without anything being passed to it.

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
  transient_relaunches_before_blocking: 2
  worktree_root: auto
  remote: origin
  usage_limit_max_pause: 6h
  usage_limit_in_process_pause: 6h
  usage_limit_unknown_reset_pause: 30m
  server_overload_pause: 90s
  check_timeout: 30m

triage:
  stuck_merge_age: 2h
  review_rounds_cap: 4

approvals:
  brief: human
  goals: human
  designs: automatic
  work_items: human
  integration: human
  publishing: human

checks: []          # yours to write; a run with none is refused

accounts:
  default: {}       # the provider account the agents below run under

agents:
  product-manager:
    role: product-manager
    backend: claude-code
    model: opus
    account: default
    instances: 1
    persona:
      version: v1
      path: personas/product-manager.md
  # ... architect, development-manager, developer, and reviewer, the same shape
```

Five agents — product manager, architect, development manager, developer, and
reviewer — each with a role, a backend, a model selector, the [provider
account](#provider-accounts) it runs under, an instance count, and a persona file
that is in the repository beside the configuration. Change one by
editing it. Remove one by deleting its block. Nothing has to be expressed as a
deviation from something invisible.

`yoyo agent list` reports them as they actually stand, with the durable
conversation each one has, and `yoyo agent chat <name>` addresses one. The
conversation belongs to the agent rather than to the role, so configuring two
agents for one role gives you two conversations with two provider sessions —
naming one of them reaches that one. What a role may do in that conversation is
**not** configurable and is not what the persona says: the harness holds one contract and one authority table per role,
sends the contract ahead of the persona on every turn, and refuses anything
outside the table. A persona specializes how a role works; it cannot widen what
the role is allowed to do. The set of role names is fixed for the same reason —
every posture the harness derives, a reviewer's absent tools included, is derived
from the name — so `role` must be one of `product-manager`, `architect`,
`development-manager`, `developer`, or `reviewer`, and anything else is
[refused when the configuration loads](#what-fails-closed).
[Talking to the other agents](conversation.md#talking-to-the-other-agents) states
the table itself.

`backend` is `claude-code` or `codex` unless your project declares one of its
own. A project running a fork, a proxy, or a variant of a provider yoyo already
speaks can describe it under a top-level `providers:` key and name it here: which
compiled adapter launches it, which executable that adapter runs, which roles it
serves, which tool postures it can hold them to, and how to read what it says
about rate limits, retries, and reset times. A declared provider describes and
decides nothing: whether to wait, how long, and against which budget stay the
harness's, because those are what the `execution.usage_limit_*` settings below
mean. [Provider plugins](provider-plugins.md) is the format and its limits — in
particular that a provider speaking a protocol no compiled adapter speaks needs
an adapter rather than a declaration.

## Discovery

Yoyodyne looks for a configuration in this order:

1. the path given to `--config`, if present;
2. otherwise the configuration `YOYODYNE_CONFIG` names;
3. otherwise `.yoyodyne/config.yaml`, searching from the current directory
   upwards to the filesystem root;
4. otherwise `.yoyodyne.yaml` in the same directories;
5. otherwise this machine's own configuration for the repository the current
   directory is in, under
   [the configurations home](#keeping-the-configuration-outside-the-repository).

Because the search walks upwards, `yoyo run` works from the project root or
from any directory beneath it. When both forms exist in one directory, the
directory form wins, so a half-finished migration cannot silently keep using the
old file.

The project's own configuration is looked for before this machine's, and that
order is the point rather than an implementation detail: a repository that
describes itself is what every collaborator gets, and a file one person wrote on
one machine must not quietly win over it. A project that carries a `.yoyodyne`
behaves exactly as it did before external configurations existed.

`YOYODYNE_CONFIG` is `--config` for a whole shell: export it and every command
run there reads that configuration. It must be an absolute path, and it names
either the configuration file or a directory holding a `config.yaml`. A variable
that is set and names nothing readable is a failure rather than a step that is
skipped — it is an instruction exactly as `--config` is, and falling through to a
different configuration than the one that was named is how a command does the
right thing to the wrong project.

Relative paths inside the configuration — `product.repository` and a non-`auto`
`execution.worktree_root` — resolve against the project directory, which is the
parent of `.yoyodyne`, not the `.yoyodyne` directory itself. `repository: .`
therefore keeps meaning the project root. The artifact directories —
`product.specifications`, `product.invariants`, `product.designs`, and
`product.decisions` — are the exceptions, and deliberately: each names a directory
*inside the repository being worked on*, so all four resolve against
`product.repository` and are refused if they leave it.

That refusal is checked twice. When the file loads it is a check on the text: a
path that is absolute or climbs out with `..` is refused before any work is
claimed. When something writes a document into one of those directories it is
checked again, against the filesystem, immediately before the bytes are written —
because a directory that reads as `docs/decisions` in this file is whatever the
filesystem has put there by the time anything writes to it, and one symlink along
the way puts the document outside the repository without a single `..` appearing
anywhere. So a directory that is a symlink out of the repository, or that sits
below one, is refused at the point of the write and nothing is written. A symlink
that stays inside the repository has not left it and the write follows it — with
one exception: an artifact home that is itself a symlink is refused earlier and
for a different reason, because the harness lists what a home holds without
following links and reports one that is a link as not being a directory. The same
holds of the `.yoyodyne` directory `yoyo init` writes: a project
whose `.yoyodyne` leads out of the project is refused with the project untouched
rather than scaffolded somewhere nothing commits. And of the configurations home
below, which is a declared root like any other: a write that resolves out of it
is refused rather than landing where nothing looks for it.

## Keeping the configuration outside the repository

A contributor to a repository they do not own has the configuration as theirs
rather than the project's, and a pull request adding a tool directory nobody
asked for is a pull request about the tool. `yoyo init --external` writes the
configuration this machine keeps for that repository, and nothing at all into
the repository:

```sh
cd ~/src/theirproject
yoyo init --external
yoyo doctor
```

It writes into `~/.config/yoyodyne/projects/<key>/`, where `<key>` names the
checkout — its directory name, then a digest of where it is — and everything
`init` ordinarily writes into `.yoyodyne/` goes there instead, personas
included. `$XDG_CONFIG_HOME/yoyodyne` is used when that variable is set, and
`YOYODYNE_CONFIG_HOME` overrides both.

Four things are worth knowing about it:

- **Nothing is passed on later commands.** The configuration is keyed by the
  repository, so `yoyo` finds it from the repository root, from any directory
  beneath it, and from a worktree Git added from it — a run's worktree, and a
  check or a hook that shells out to `yoyo` from inside one, resolve to the
  repository they came from rather than being read as projects of their own.
  That is what separates this from moving `.yoyodyne` somewhere by hand and
  passing `--config` on everything thereafter.
- **The key is the checkout, not the project.** Two checkouts of one project on
  one machine are two configurations, because they are two things to configure.
  Moving a checkout leaves its configuration behind under the old key; run
  `init --external` again, or move the directory to the key the refusal names.
- **`product.repository` is written absolute.** An external configuration has no
  project directory above it for a relative path to resolve against. The
  artifact directories are unaffected: `specifications`, `invariants`,
  `designs`, and `decisions` resolve against `product.repository` and go on
  naming directories inside the repository being worked on.
- **It writes no artifact-home indexes.** `init` ordinarily puts a `README.md` at
  the door of each of the five artifact homes, and in a repository you are a
  guest in those are five untracked files in somebody else's `docs/` tree.
  `yoyo doctor` reports each home without an index as a warning rather than a
  problem, so an installation configured this way runs work exactly as one with
  them does.

The project stops describing itself, which in this scenario is the intent:
another clone, another machine, and anybody else working on it get no
configuration at all, and `yoyo` there reports that it found none.

## Precedence

A configuration `init` wrote has one layer: itself. Every configured value comes
from the project file, and nothing is inherited from a bundle. Two values are
still reported as computed rather than written. `product.repository_id` has the
origin `derived:product.id`, because the generated file states the product id
and lets the repository id follow from it, and
`triage.repair_grant_attempts` has the origin
`derived:execution.repair_attempts_before_replan` for the same reason: the
generated file states the repair budget and lets the grant follow it, so raising
one raises the other. Both are values derived from something in the same file,
not something arriving from outside it.

The rest of this section describes what happens when a project uses `extends`,
and what the harness still fills in when a file leaves something out.

Up to three layers produce the effective configuration, later ones winning:

1. **Harness defaults.** Values the harness fills in when nothing else supplies
   them: `product.specifications` (`docs/product`), `product.invariants`
   (`docs/decisions/invariants`), `product.designs` (`docs/designs`),
   `product.decisions` (`docs/decisions`), `execution.max_concurrent_developers` (1),
   `execution.repair_attempts_before_replan` (2),
   `execution.integration_retries_before_reconciliation` (2),
   `execution.transient_relaunches_before_blocking` (2),
   `execution.worktree_root`
   (`auto`), `execution.remote` (`origin`),
   `execution.usage_limit_max_pause` and
   `execution.usage_limit_in_process_pause` (`6h` each),
   `execution.usage_limit_unknown_reset_pause` (`30m`),
   `execution.server_overload_pause` (`90s`),
   `execution.check_timeout` (`30m`),
   `triage.stuck_merge_age` (`2h`),
   `triage.review_rounds_cap` (4),
   `approvals.publishing` (`human`), `approvals.work_items` (`human`), and an
   agent's `instances` (1).
   `triage.repair_grant_attempts` is filled in too, but as a derivation rather
   than a fixed default: it takes the size of the effective
   `execution.repair_attempts_before_replan`, read after every layer has been
   applied, and is floored at 1 for a project that repairs nothing routinely.
   `approvals.publishing` and `approvals.work_items` are the only approvals with
   a harness default, because they are the ones added after configurations
   existed, and a file that mentions neither loads rather than failing over a key
   that did not exist when it was written. The bundle states both at the same
   value the default holds, so extending it inherits neither and upgrading the
   executable moves neither. Both are opt-ins, and an opt-in that arrived by
   inheritance would not be one.

   **`work_items` is the one of the two that changes an existing project's
   behavior**, and it is worth being plain about rather than leaving to be
   discovered. `publishing: human` is exactly what a file written before it got:
   the harness publishes nothing. `work_items: human` is not, because before this
   key existed the product manager could admit work to the backlog **directly**,
   through its `create` action, and you were told afterwards rather than asked.
   That direct admission is now refused at `human`, so a project that upgrades
   and leaves the key alone has a product manager that proposes work instead of
   admitting it. Nothing is lost when it does — the proposal is put to you and
   approving it creates the item — and the trade is deliberate: a `human` setting
   that left this door open would be a gate the product manager could walk around
   by choosing the other one. An operator who wants the old behavior back sets
   `work_items: automatic`, which admits directly again against goals they have
   approved. See [what reaches the queue](#what-reaches-the-queue).
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

- A run integrates only behind [protected paths](#protected-paths-in-a-developers-change),
  deterministic checks, and an independent review.
- A run that cannot finish leaves its work recoverable rather than lost.
```

The directory is walked to any depth, and every `.md` file inside it is a
specification, with one exception: a `README.md` is a directory index rather than
a document stating anything, so neither the shape above nor
[artifact identity](#artifact-identity-and-metadata) is asked of it, and it is not
counted as a specification. It is still read into the context, under a heading of
its own, because what is filed in a directory is worth knowing to whoever is about
to write the first document into it.
Everything else has its prose checked for the introduction-then-goals shape
above, and its identity — the frontmatter naming its id, kind, status, and what it
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
statement about the repository rather than a reason to fail. A directory holding
nothing but the indexes `yoyo init` wrote is that same directory: an index states
no intent, so it is not counted as a specification and the conversation still
says intent is not written down.

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

### What the product manager sees besides them, and what it does not

**The specifications directory, the tracker, and a description of what the
product ships today.** That last part is `README.md`, each of the documents it
links to under its further-reading index, this file, and the help
every command prints — carried in a section of its own, labeled as description
of the implementation as built and never as authority about intent. No source,
no design document, and no way to run a command.

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
[its own identity scheme](#architectural-invariants). A home that does not exist
is not an error — a project that has not written its designs down yet records no
design artifacts.

That exemption is what makes the `README.md` the place these directories explain
themselves in, and `yoyo init` writes one for every home above — the
specifications directory, the `goals` directory under it, the designs, the
decision records, and the invariants. Each states three things and nothing else:
what is filed there, which agent owns it, and whether you may edit one of those
documents by hand. None of that is policy the file invents. A role that is not
the owner proposes an amendment and waits, per the ownership table above; your
own edit is never refused, and what it is is reported — a revision recorded under
a role that does not own the document is an unauthorized revision every load
names, and what a change leaves stale downstream is
[`yoyo stale`](#traceability-references-and-orphans)'s to report. An index that is
already there is left alone, `--force` included, because it is the project's own
prose rather than something `init` generated; `yoyo doctor` reports one that is
missing or has stopped stating the three things, and `yoyo setup` offers to write
it — asking separately, and defaulting to no, before it replaces one somebody
wrote.

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
[what reaches the queue](#what-reaches-the-queue) below. Everywhere else, an
amendment after approval changes what is reported about the document rather than
what is allowed, and what `human` buys you is that the difference is visible — in
the document, in the listings, and in `--json`, where each artifact's `state` is
`approved`, `amended`, or `unapproved`.

### What reaches the queue

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

**The paths a grant does not reach.** A grant is an exception to *this harness's*
refusal, and to nothing else's. Claude Code refuses an agent's writes to its own
settings files above anything yoyodyne permits — the editing tools are denied
there however the run is configured, and the shell sandbox names the file and
cannot be disabled by policy — so an item that grants one of them has admitted
work no run can do, and the run discovers that by spending its repair budget
against it. One item spent three rounds there before this was recorded anywhere.

| Beyond any grant | Refused by |
| --- | --- |
| `.claude/settings.json` | Claude Code |
| `.claude/settings.local.json` | Claude Code |

So the harness refuses a creation, an update, or a proposal whose text grants one
of these, and the refusal names the provider: what is wrong is not the item's
judgement about the path, but that the change is a person's to make by hand
rather than a run's to be given. Only the marker is refused — prose that names
one of these files grants nothing and admits fine, which is what lets an item
*about* this boundary exist at all.

**And once more, where all four fields are read.** Those three doors carry an
item's title and description; a grant is honoured from its design guidance and
acceptance criteria too, and nothing in the harness writes either of those — they
reach an item through the tracker's own command. So the run asks the same
question itself, over all four fields, before the item is claimed: a run that
would start on such an item refuses to start, which is the same refusal one step
later and still before a single repair round is spent. That is also what covers
an item admitted before this gate existed. The developer contract names the same
paths for the case no gate can catch — an item that describes the work without
granting anything, whose developer would otherwise spend attempts looking for a
way in.

The list is short and evidenced rather than a guess at a provider's whole
posture: an entry refuses work at admission, so a path added on suspicion costs
items nobody needed to refuse. It grows the same way it started — something meets
the wall and reports it.

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
that gap: it records, on every work item whose notes state a goal and which
carries no witness, the goal those notes already state. It writes no attribution
and decides nothing — the statement is the item's own, copied to where a careless
writer cannot reach it — and it is worth running once after upgrading, and again
after any bulk import of work attributed elsewhere. It sweeps every status the
tracker holds rather than the queue, because the command that destroys an
attribution reaches a claimed or closed item just as easily, and most of the
losses on record were on items that had already closed.

**The sweep is wider than the audit, and what a witness buys differs across that
line.** `attribution` reads the backlog — `open` and `blocked` — so on those a
witnessed loss is a `lost` state it reports and exits non-zero for. On claimed
and closed items the sweep also reaches, nothing reports the loss: what the
witness buys there is recovery, because the statement is kept where replacing
the notes cannot reach it and a destroyed attribution can be put back from the
record rather than judged again.

`yoyo goals guard` is the same loss stopped rather than reported. Wired as a
`PreToolUse` hook on `Bash`, it reads the command an agent session is about to
run and refuses `bd update <id> --notes`, which replaces an item's notes and
takes the goal recorded in them with it; a replacement carrying a
`Goal served:` line through is allowed, because that one destroys nothing. It
decides from the command line alone and never reads the item, so it checks such a
line is present and not that it is the item's own — a substitution passes it, and
the witness rather than the guard is what holds the words that were replaced. The
harness gives it to every developer run it makes on the Claude Code backend,
which is the backend that passes the hook; any other agent session is covered
only by wiring the same command into that session's own hooks. It is passed to
the provider rather than enforced by the harness, so where the hook does not fire
the command runs as it did before and nothing reports that it did.

```sh
yoyo goals list          # the goals work may be attributed to, and where each is stated
yoyo goals attribution   # what each admitted work item says it is for
yoyo goals witness       # witness the goals already recorded on work items
yoyo goals guard         # refuse a command that would replace notes and destroy a goal
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

### What a change upstream leaves stale

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
identity; a `.md` filed in a subdirectory is reported rather than read. One name
is not read at all: `README.md` is the directory index this home carries like
every other, so it is skipped rather than reported as a malformed constraint, and
`yoyo invariant create readme` is refused because a constraint written there
would be one nobody is held to. `scope`
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

### What `init` proposes for `checks`

A project does not start from the empty list unless it has to. `yoyo init` reads
what the repository already announces about its own toolchain and writes the
commands that follow into `checks`, each under a comment naming the artifact it
was derived from:

| What is there | What is proposed |
| --- | --- |
| a Makefile with a `check` target, or with `test` and no `check` | `make check` / `make test` |
| `go.mod` | `go test ./...`, `go vet ./...` |
| `package.json` with a test script and exactly one lockfile | the lockfile's install, `npm`/`yarn`/`pnpm test`, and `tsc --noEmit` where there is a `tsconfig.json` |
| `pyproject.toml`, `pytest.ini`, `setup.cfg`, or `tox.ini` naming pytest | `python3 -m pytest -q` |
| `pom.xml` | `mvn --batch-mode --quiet verify` |
| a `gradlew` wrapper | `./gradlew --no-daemon check` |

**Nothing is executed.** Detection is by artifact presence and by reading those
artifacts, because running a stranger's build to discover what it is is not a
first impression worth making, and because a command that has to run to be
proposed is one that runs before anybody has reviewed it. This is a convenience
default derived from the project's own files rather than an understanding of
toolchains in the harness: what runs is still only the shell commands this list
declares, judged by their exit codes.

**Whatever is not written into `checks` is written beside it, commented out,
under a heading that says what it wants from you.** There are three, and only the
first asks for anything:

| Heading | What it means | What you owe |
| --- | --- | --- |
| `YOU MUST CHOOSE` | detection could not tell which command is the gate, and `checks` is empty | a choice: a run is refused until there is one |
| `ALSO FOUND, AND NOT DECIDED` | the same, except `checks` was written from something else and works | nothing; the question is open, not blocking |
| `ALSO FOUND, AND NOT NEEDED` | commands detection read and decided against, because what it wrote covers them | nothing |

The distinction is the point. A demand to choose is worth reading only where a
run cannot happen until somebody does; putting it over an already-runnable file
teaches an operator to scroll past it.

Taking any of them is the same gesture: delete the leading `#` and nothing else,
and open the list above with `checks:` if it is still `checks: []`. Each carries
the reason it is where it is.

**A Makefile supersedes the language-native commands**, which is the ordinary way
into the third heading. A project with a `check` target and a `go.mod` gets
`make check`, and `go test ./...` and `go vet ./...` appear under
`ALSO FOUND, AND NOT NEEDED` rather than being added, because two gates running
the same suite is the suite run twice. Nothing about that is undecided, so
nothing about it demands a decision.

**What cannot be settled is not settled**, which is the first two headings. The
cases that reach them today are:

- Python tests with no runner named anywhere. unittest discovery over
  pytest-style tests collects nothing and exits 0, which is a gate that passes
  everything, so neither runner is written.
- A `package.json` with no lockfile beside it, or with more than one, which
  leaves how the project installs unsettled.
- A `package.json` that declares no `test` script at all, or whose only one is
  npm's `exit 1` placeholder: nothing there says how the project is tested.
- A Gradle build script with no `gradlew` wrapper to pin the version a check
  would run under.

Which of the two headings they land under depends only on whether anything else
in the project produced a `checks` list to stand on.

A repository that announces none of this keeps `checks: []` and the commented
per-language examples above, which is what it always did.

### How long a check may take

Each check gets a budget, and a check that exceeds it is killed and ends the run:

```yaml
execution:
  check_timeout: 30m   # the default; per check, not for the list
```

It is the *total* time a check may run rather than the time it may stay quiet: a
suite printing a result every second is spending it just as fast as one that has
gone silent. The `30m` default is deliberately generous, because a check stopped
at this bound is not a check that judged the change — the work may have been
passing the whole way, and killing it costs a run that had nothing wrong with it.

**Concurrency multiplies what a suite takes, so this has to scale with it.**
`max_concurrent_developers: 2` does not give each run its own machine: two suites
contend for the same cores, and each one's wall clock grows accordingly — about
twofold for this repository's own suite, and further under whatever else the
machine is doing, including the provider processes the runs themselves keep busy.
The budget is spent in wall clock, so N concurrent runs need a budget set against
what the suite takes with N of them running, not against what it takes alone.
Either raise `check_timeout` to match, or lower `max_concurrent_developers` so
the suites serialize; leaving both at values chosen independently is how a
passing suite gets killed. This is the failure that produced the setting: a flat
ten minutes, a suite past forty packages with real Git integration tests, and two
concurrent runs — the tests were passing package by package when the bound
stopped them.

Every check reports what it spent against what it was allowed, whether it passed
or not. The completion event carries `elapsed` and `timeout`, and the run's notes
on the work item carry the same pair per check, so a suite growing toward its
ceiling is visible run after run rather than only in the run the ceiling finally
stops. When one does time out, the failure names both numbers and the two
settings that move them.

A budget of `0` is refused rather than read as "unbounded": nothing else bounds a
check, so one that never returns would hold a worktree, a claim, and a run open
indefinitely.

## Scheduling ready work

`yoyo run <id>` is you naming an item. `yoyo work` is the harness choosing:

```yaml
execution:
  max_concurrent_developers: 1   # the default
```

It reads the admitted work in the order you set — highest priority first — takes
the items the tracker itself reports as ready to pull, and starts as many of them
at once as this leaves free. Each run gets a worktree and a branch of its own,
and the command returns once every run it started has ended. `--limit <n>` stops
it after that many runs; without one it drains what is ready, and
[`--watch`](#watching-instead-of-draining) keeps it open instead.

Nothing about running several at once relaxes anything. Capacity is enforced at
the reservation rather than by the scheduler, so two schedulers, or a scheduler
and a `yoyo run` beside it, share one limit rather than getting one each — a run
that loses the race for the last slot is reported as declined, not as a failure.
Integration stays serial: at most one promotion into a given target branch
happens at a time, and a change whose target moved while it was being reviewed is
replayed onto where the target went and promoted by fast-forward, or blocked if
it will not replay. Nothing is ever forced.

Six things keep an item out of a pass, reported at two different grains. Three are
named against the item, because nothing else would report that this item was
passed over. An unresolved directive is named with the directive's own words: it
needs a person. An item whose unfinished children already carry its execution is
named with those children: a decomposed epic and the child doing its work are
both reported as ready, and starting both buys the same change twice — two
developers over one file, the second of them guaranteed a conflict at
integration. A child covers whether it is queued, blocked, or already claimed,
and the container is ordinary work again once its last unfinished child leaves
the backlog. And an item that would race work already in flight is sequenced
behind it rather than started beside it, named with the run it would have raced
and what the two share — the siblings of one epic, or overlapping files. That one
is a wait rather than a refusal: the conflicts are re-read at every pull from
what is actually in flight, so the item is pulled at the first pull where the run
it would have raced has ended, and the slot the hold freed is spent on the next
item down the order that races nothing. An item says which files it will change
by naming them after `conflict-surface:` on a line of its own, in its title,
description, design guidance, or acceptance criteria; an item that declares
nothing has those same fields read for the files it plainly names, and that
inference takes only a path with a separator and an extension on the end, because
a surface invented out of prose would hold unrelated work back. The tracker not
reporting an item as ready, a run for it
already being in flight anywhere, and there being no free slot are facts about
the pass rather than about any one item, so the pass reports them as such — the
stop reason names which of them ended the choosing, and a pass that got as far as
reading the queue prints how many items were admitted, how many the tracker
called ready to pull, and how many slots were taken. Those are counts rather than
a list on purpose: naming every unready item would print a line per backlog entry
on every pass and bury the deferrals worth reading. A pass that stopped before
reading the queue at all — held intake, or every slot already taken — says
nothing about the backlog rather than reporting zeroes it never looked up.

A seventh thing deliberately keeps nothing out: an item whose goal was amended
after it was admitted is pulled exactly as it would have been, and what changed
goes into the run's recorded reason instead. See
[what a change upstream leaves stale](#what-a-change-upstream-leaves-stale) for
why staleness reports rather than decides.

`max_concurrent_developers` cannot exceed the number of developer `instances` you
configured, and the default of `1` is deliberate: raising it is a decision about
your machine, and [how long a check may take](#how-long-a-check-may-take) is the
setting that has to move with it.

### Watching instead of draining

`yoyo work` returns when nothing more is ready. `yoyo work --watch` does not: it
waits out an interval and reads the queue again, until you stop it.

```yaml
execution:
  work_poll: 60s                       # the default
  blocked_runs_before_intake_hold: 3   # the default
```

Nothing else about the pass changes, and nothing needed to. Every pull already
re-reads the configuration, re-reads the intake hold, takes the queue in the
order you set, and records why it chose what it chose — so work you admit is
picked up at the next poll, a reprioritization is honored at the next pull, and
an item whose dependency landed becomes pullable because the tracker says so.
There is no change detection anywhere in it, because nothing between the readings
is cached. A run already in flight is never preempted by any of that.

An idle session costs one local tracker read per `work_poll` and asks no provider
anything, so a queue that is empty overnight spends nothing.

**The intake hold is the remote brake.** Holding intake does not stop a watching
session; it brakes it in place. The session keeps polling, chooses nothing, and
resumes where it was when you release it. `yoyo pause` — the wider switch — parks
the runs too, and lifting it resumes them from their own records.

**Three guards, because the loop no longer ends.**

**A watching session does not start the same item twice unless the item has
changed.** The case that forces this is a run that fails *before it starts* —
unreadable acceptance criteria, a provider that is not authenticated, a context
bundle that will not assemble. Nothing is claimed and nothing is recorded, so the
item is left exactly as ready as it was: a drain tries it once and returns, and a
watch with no memory would retry it every interval forever.

The rule covers every item the session has started, not only the ones that failed
that way, because the other cases that leave an item pullable with nothing
recorded — a run the intake hold or your `yoyo pause` stopped before it claimed
anything — would spin the same way. What lifts it is the item changing: what the
work says, what it is for, its priority, its status, what it depends on, and its
notes. The notes are what make the ordinary recovery work. A run that stops on a
blocker takes the item out of the ready queue and writes the blocker into its
notes, so when you release that item without editing anything else, the session
sees an item it has not tried and pulls it. Nothing the harness writes can clear
the cooldown of an item that stayed pullable, because it only ever appends to the
notes of an item it has claimed, blocked, or closed.

An item this session has already run and that nothing has touched since is
therefore left alone for the life of the session. Restarting the session, or
touching the item, is what asks for another attempt.

`blocked_runs_before_intake_hold` is the failure-storm brake, and it is a
different thing from that cooldown: it is aimed at a broken machine rather than a
broken item. That many runs blocking one after another, with nothing landing
between them, holds intake — the same hold you would place — and it stays held
until you release it, with `yoyo release` or the conversation's `/release`. Any run that lands clears the count, and `0` turns the
brake off entirely, leaving you as the only thing that holds intake.

And the session says what it is doing, because an idle session and a dead one are
otherwise the same silence. Each transition — watching, idle, braked, resumed,
stopped — is recorded once, where `yoyo status` prints it and the Slack sink
posts it. A session idling all night writes one line rather than one a minute.

**`--budget <usd>`** caps what one session spends, from the same recorded run
evidence `yoyo cost` prices items from. It is checked between pulls, never during
a run: the money a running run has spent is already spent, and what stopping it
would lose is the work it bought.

A budget the harness cannot measure is not a smaller budget, it is no budget, so
it fails closed at both ends. A pass given `--budget` with no way to price itself
is refused before anything starts. A session that has started and then meets a
run whose recorded evidence will not price — the run's event log gone, or a
record it cannot read — stops there and says which run it was, rather than
counting it as free and carrying on inside a bound it can no longer hold. The
stop is announced like every other transition, so you find out while it matters
rather than in the morning.

**The default is still the drain**, and `--until-drained` says so explicitly.
That is deliberate: watching is the shape this loop is meant to have, and turning
it on by default is a decision to make once stopped work reliably reaches
somebody, rather than a side effect of the flag existing.

What changes when you watch is what bounds the spend. A drain is bounded by the
queue emptying; a watching session is bounded by what you admit to the queue. The
backlog's order stops being a schedule and becomes the throttle.

### When a configuration change takes effect

**At the next selection.** `yoyo work` re-reads the configuration before every
pull, not once when it starts, so a capacity you raise or a priority you reorder
while it is running is picked up the next time it chooses something. That is the
same answer every other command gives — each one loads the configuration fresh —
and it is what makes reordering the backlog steer the work rather than steering
the work after a restart.

A run already in flight keeps the configuration its own pull read. Its capacity,
its check budget, and its repair budget were fixed when it was reserved, and
changing them under a running developer would mean a run judged by rules it was
never started under.

A watching session is the same answer said again: `work_poll` and
`blocked_runs_before_intake_hold` are re-read at every pull too, so an interval
you shorten or a brake you loosen takes effect at the next wait rather than at
the next restart.

### Why each run says why it was there

Every run `yoyo work` starts records, in durable state, why that item was chosen:
where it sat in the order, how much of the queue was pullable, how much of the
machine was free, and anything upstream of it that had changed since it was
admitted. `yoyo status` and a conversation's survey both read it back.

This is not bookkeeping. Work the harness chose and cannot account for looks
exactly like work happening behind your back, and holding intake — which stops
`yoyo work` choosing anything more while what is running finishes — is worth
having only if the thing that chooses actually consults it. Both halves are
enforced rather than conventional: an item you name yourself is exempt from the
hold, because naming it is you deciding it is the exception.

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

If you cannot push to that remote — you are contributing to somebody else's
repository — name your fork as well:

```yaml
execution:
  remote: upstream     # where pull requests are opened; you need no push access
  push_remote: fork    # where run branches go; the one you can push to
```

With both on, a run works like this:

1. **The developer phase publishes.** When a developer attempt finishes, the
   harness commits its work under its own identity, pushes the run branch to
   `execution.remote` — or to your fork, if you named one; see [Publishing from
   a fork](#publishing-from-a-fork) — and opens a pull request against the
   target branch on `execution.remote`. Each repair attempt pushes
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
   because that is what the forge still has to merge. The work item is the one
   thing the run does **not** settle — it stays open, with the queued merge named
   on it, because closing it as integrated would record a publication that has
   not happened and may not. `yoyo reconcile` settles both afterwards: it asks
   the forge, and either finishes the publication and closes the item (merge
   commit recorded, remote branch deleted, your local target branch caught up
   onto the forge's merge commit) or, if the forge dropped the queued merge
   because something it required went unmet, records an outstanding publication
   and hands the item back to you with a blocker rather than closing it. It never
   merges anything itself: a requirement that stopped the forge is yours to
   satisfy, and re-arming a dropped merge is a bounded triage decision rather
   than something a sweep does.

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

### Publishing from a fork

Everything above assumes you can push to the remote you publish into. When you
cannot — the ordinary situation for a contributor to somebody else's
repository — `execution.push_remote` names the remote your run branches go to
instead, and the pull request is opened across the two repositories:

```yaml
execution:
  remote: upstream
  push_remote: fork
```

Both remotes have to exist in your checkout. Add the fork the way you would add
any remote, and check both with `yoyo doctor`, which names whichever one is
missing:

```bash
git remote add fork git@github.com:you/theirproject.git
yoyo doctor
```

Nothing else about publishing changes, and nothing about it is a second mode.
Run branches are pushed to the fork, the pull request is opened against the
remote you publish into with the head qualified by your account, and every
question about the target branch — whether it may still be merged into, where
the forge's merge left it, catching your local branch up afterwards — is asked
of the repository the work is going into. The merged run branch is deleted from
the fork, because that is the only place it ever was.

Your fork's account is read from the fork remote's URL rather than configured
separately, so there is nothing to keep in step with it. A repository that does
not have the remote you named publishes nothing and says which remote it was
looking for, the same way a repository with no remote at all does.

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

The merge itself does not leave the two at the same commit, and no forge merge
method would: **the merge leaves the remote target at your local target plus one
merge commit**, made by the forge and identical in content. The last step of the
promotion is to catch your local branch up onto it, which is an ordinary
fast-forward onto a commit that already contains the promotion and carries
exactly its content. Nothing is rewritten, reset, or merged, and nothing is
decided: that is the `git pull` you used to run yourself.

A catch-up the harness cannot make cleanly is held rather than forced, and says
why:

- **Uncommitted work in your checkout that the incoming commits would
  overwrite.** The branch is left where it is and the file is named. The
  exception is the work tracker's own exports — `.beads/issues.jsonl` and
  `.beads/interactions.jsonl`, the same two a run is allowed to rewrite in your
  checkout while it works. They are derived from a store that is authoritative
  elsewhere, so their churn is discarded and the catch-up goes through.
- **A remote that has diverged from your local branch** — a history somebody
  rewrote, or work that reached the remote another way. Which of the two is
  right is your answer rather than the harness's, so it is reported and nothing
  moves.

A merge that landed after its run had finished, and any catch-up that was held,
are swept by `yoyo reconcile`, which also removes the leftover local branches of
settled runs whose work the target already carries. Catching a branch up takes
that branch's promotion lease, so it never races a run promoting into it.

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

## Waiting out a provider that refuses

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
  server_overload_pause: 90s
```

`usage_limit_unknown_reset_pause` is the interval between probes: how long a run
sleeps before reissuing the attempt and finding out whether the provider will
serve it now. It applies whether or not a reset time was named, which is the
whole of the polling discipline. A limit reported *without* one is not the same
as having no capacity — an exhausted overage allowance reports this way while
the ordinary rolling window keeps resetting on its usual schedule — so the work
is waitable and simply carries no deadline. A limit reported *with* one is
waitable and carries a deadline that is an upper bound rather than a gate: a
reset time is a claim about the provider, and claims go stale in both directions,
because capacity gets bought mid-wait and a rolling window can free room before
the quoted edge. So a run sleeps this interval or the time left to the deadline,
whichever is shorter, and then asks again; a probe into a window that is still
closed costs one refused request and re-parks on whatever the provider now
reports. Every probe spends the same budget as any other wait, so a provider
that keeps refusing reaches the maximum rather than polling forever.

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
default every probe the harness will take is taken here and the run continues on
its own once the limit resets. Lowering it — say to `1h` — makes the process
sleep probes until it has spent that hour on the run and then exit, with the run
still in flight and its deadline recorded; running `yoyo run` on the same item
continues that same run, and that process gets the whole bound again.

It is also what bounds a run parked on an operator pause — `yoyo pause`, which
holds everything the harness would spend at every provider-call boundary. That
pause has no configuration of its own and no maximum: what lifts it is the
operator rather than a clock, and time spent held is accounted separately from
`usage_limit_max_pause`, because the provider never refused the run. What it
shares is this bound on how long a single process will stay open waiting, and
the durable park behind it: a run held past the bound exits with the park
recorded, and `yoyo run` on the same item continues it once `yoyo resume` has
lifted the pause. See the README for the whole of that behavior.

The bound is on how long one process stays open for a run, so it counts every
probe that process has already slept rather than each probe separately. Applying
it per probe would bound nothing: a probe interval of `30m` fits under a `1h`
bound however many times it is taken, so a six-hour deadline would hold the
process open for the whole six hours in half-hour slices. It spans phases for
the same reason — a process that waited half an hour for the developer and half
an hour for the review has been open for an hour.

Both paths record the deadline in durable run state *before* any waiting begins,
so a process that dies mid-wait loses nothing and a restart serves the same
deadline rather than retrying straight back into the limit. What each probe will
spend is committed before it is spent, for the same reason, and the unspent
remainder of a probe cut short is given back — so the recorded total is what was
actually waited. `yoyo reconcile` leaves a paused run alone for the same reason
it leaves a repair loop alone: it is not an interrupted run, it is a run that is
owed the attempt it was refused.

A reset time that is absent, unreadable, already in the past, or beyond what the
run has left of `usage_limit_max_pause` stops the run with a blocker naming what
refused it. A reset that is not in the future is refused deliberately: a limit
still declining work while claiming it has already reset is not describing a
wait, and honoring it would mean reissuing straight back into the same refusal.

`server_overload_pause` is the same discipline on a different clock, for the
other way a provider refuses without judging the work: its own servers are
transiently unable to serve the attempt. That names no reset time at all and
lifts in seconds rather than hours, so a run waits this interval — `90s` by
default — and reissues, rather than parking for the half-hour probe interval a
usage limit uses. Everything else is shared with the paragraphs above: the
deadline is durable before the wait begins, the reissue continues the same
worktree and developer session, and each wait spends `usage_limit_max_pause`, so
an overload that never lifts walks into that maximum and stops with a blocker
instead of reissuing forever. `yoyo resume` releases one of these waits exactly
as it releases a usage-limit wait.

Ordinary transient throttling still never reaches any of this: the provider CLI
retries that itself, and the harness does not duplicate the wait. What the
harness acts on is the terminal result that CLI ends on once its own retries are
spent — an `api_error` reporting HTTP 529 — because the provider has stopped
retrying by then and something has to. An overload is the only terminal
`api_error` that becomes a wait; the rest are covered by
[the relaunch budget](#relaunching-a-run-the-provider-killed) below.

`yoyo resume <beads-id>` is the one thing that overrides a recorded deadline,
and it overrides nothing else. It moves the next probe to now, for when the
reset has stopped being true — you raised the account's capacity, say — because
the deadline is a claim about the provider and you are the one who can change
what it is a claim about. It never stops the run: a process asleep on the wait
acts on the release within seconds, and the run keeps its claim, its branch, its
worktree, and its developer session. If the provider still refuses, the run
records the new report and waits again, so a premature release costs one refused
request. See the README for the whole of that behavior.

## Relaunching a run the provider killed

Not every way a provider ends an invocation is a refusal it names in advance.
Sometimes it dies: the API answers with an error its own retry ladder did not
outlast, or the connection carrying the response goes away before the reply is
finished — `API Error: Connection closed mid-response`, which quotes no HTTP
status because nothing answered. The work was never judged and nothing is wrong
with the change. Rather than failing, the run relaunches itself:

```yaml
execution:
  transient_relaunches_before_blocking: 2
```

The dead invocation is reissued in the same worktree and the same developer
session, so an attempt that died mid-response continues the change it had already
started rather than deriving it again. No wait is attached, because there is no
condition to wait out: a dropped connection is already gone, and the provider's
own retries are spent before the harness sees the terminal.

A stream that ends one invocation twice spends the same budget. Two terminal
results where there was only ever one ending judge nothing about the work, and
neither can be told apart from the other — a subagent's completion carrying a
terminal's marks is read as the invocation's, so the run's own ending arrives
looking like the duplicate — so the invocation is asked again rather than
published. Both endings stay in the run's event log. A stream the harness
genuinely cannot read still fails the run.

One budget covers both provider invocations a run makes. A review the provider
killed is asked for again on the same count, without redeveloping the change,
because what the budget bounds is how much of the provider's weather a single run
absorbs rather than how often either role is asked. Nothing is handed back to the
developer, so a relaunch spends no repair attempt.

Relaunches are counted in durable run state before each one begins, so a process
that dies mid-relaunch resumes against the budget it had rather than a fresh one.
A run that spends the budget stops and records a blocker on the work item naming
the provider's own last message. Setting the bound to `0` restores the earlier
behavior: the first provider death ends the run.

What else that blocker says depends on what the run was carrying, because a
provider dies during a repair attempt as readily as during the first one. A run
nothing had judged yet says plainly that no check failed and no reviewer asked
for repair. A run killed inside its repair loop names the repair attempts it had
spent, the failing check, and the findings it was answering, and says the
provider stopped it rather than that verdict — the evidence is unresolved rather
than dismissed.

A refusal that would stand is never relaunched. A terminal `api_error` quoting a
4xx status — a malformed request, a key that is not permitted, a limit the
provider is enforcing — earns the identical answer on the next attempt, so it
fails the run as it always did; so does a 529, which is a wait rather than a
relaunch, and so does any terminal the API did not report at all. The invocation
ended twice is the one thing outside the API's own errors that still relaunches,
because it is not a verdict on anything.

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

## How long one role may ask another

Roles can put a question to each other through the harness — the product manager
asking the architect what a goal costs before it orders the backlog, the
architect asking the product manager whether a trade-off is one a user would
accept before it settles a design. Every exchange is recorded where you can read
it with `yoyo exchange`, both halves are toolless so an ask moves opinion and
never evidence, and no authority moves through one. What is configurable is how
long a single exchange may go on:

```yaml
exchange:
  max_rounds: 10             # the hard limit on rounds in one exchange thread
```

**It is a hard limit and it is durable with the exchange.** The number is copied
onto an exchange as it opens rather than read afresh each round, so a process
dying part way through, a second process picking the thread up, and an edit to
this setting all leave a thread already in flight bounded by what it started
with. A cap a crash could reset is not a cap.

**Reaching it is not a silent cutoff.** The exchange closes as
`unresolved-after-rounds`, and it is escalated to you as a report at warning
severity naming the two roles, the question, the rounds, and what the exchange
cost — so it reaches [the pile you read](reporting.md#what-agents-report-and-where-it-reaches-you)
rather than ending in a record nobody opens. The failure this bounds is two
judgement models deferring to each other politely for ever, which is rare,
expensive, and invisible without the number.

**Zero is refused**, unlike the triage caps above. An exchange allowed no round
at all is a channel that is off, and turning the channel off is a matter of
nobody using it rather than of configuring a limit nothing can be spent against.
One is the floor.

One further bound is the harness's rather than yours: a single thing you say to a
conversation sets off at most as many rounds of asking as one exchange is
allowed, however many exchanges it spreads them over. That bounds a reply
opening thread after thread, which is a different question from how long one
thread may run.

## Research sources

The product manager can have the harness find something out for it, so an idea
you bring it is evaluated against evidence rather than against what a model
remembers. **The capability is off until you name a source**, and a project that
names none has a product manager that says it could not check rather than
answering from memory as though it had.

```yaml
research:
  max_queries_per_turn: 4    # how many questions one reply may set off
  timeout: 60s               # how long one source has to answer
  sources:
    - name: web              # what the role names, and what every record cites
      command: my-search     # run with the question on standard input
      describes: public web search, no login
```

**A source is a command you wrote.** The harness runs it with the question on
standard input and reads its standard output as the evidence — nothing else is
passed, and the question is never part of a command line the shell parses. That
is the same arrangement `checks` uses, and for the same reason: what the harness
may run is a thing you write down in the file you write everything else in, so
what it can reach is exactly what you named. There is no built-in provider and no
default source, deliberately. A conversational role reaching the network is
something you turn on, not something you acquire by extending a bundle or
upgrading the executable.

**The role still has no network.** It names a question and one of these sources;
it does not choose what runs, where the command reaches, or how often. Only the
question leaves your machine, redacted with the same values every other
provider-facing path is redacted with and bounded at 512 bytes — generous for a
sentence somebody would type into a search box, and far too small to carry a
document out inside one. Which sources exist is delivered to the role with each
turn rather than written into its contract, so a source you add or remove is in
force on the next thing you say.

**What comes back is untrusted.** It is delivered framed as evidence about the
world and never as instruction, exactly as your repository documents and your
work items already are, and it is bounded at 4KB per answer with any cut
declared. A source that fails, times out, or answers with nothing produces a
finding that says so rather than silence — a role that gets silence for an answer
concludes there was nothing to find, which is the one conclusion it must never
draw from a source that broke. Every question and what it returned is printed to
you as it happens.

**The bounds are yours and the protocol has its own.** `max_queries_per_turn`
narrows how many questions one reply may ask and cannot widen it past four, which
is what the block itself permits; `timeout` is per question. Both take a harness
default when you leave them out, so naming a source is enough to have the
capability rather than something you configure twice. Zero is a choice for each —
it takes the default — and a negative number is refused. One further bound is the
harness's rather than yours: one thing you say sets off at most two rounds of
gathering, so a message cannot spend itself searching its way around a question.

What the product manager does with the evidence is an evaluation, which is
advice and nothing else: recording one admits no work, changes no document, and
approves nothing. That path, and how to read the evaluations back, is described
in [the conversation guide](conversation.md#bringing-it-an-idea-rather-than-a-work-item).

## Triage thresholds

Triage is what looks at work that has stopped moving. Its numbers are
configuration rather than constants, because each one is a judgement about a
project's pace — how long a merge may take before nobody merging it is news, how
many times one change may go round with a reviewer before going round again is
the problem:

```yaml
triage:
  stuck_merge_age: 2h        # how long an approved publication may sit unmerged
  review_rounds_cap: 4       # total review rounds one item may accumulate
  repair_grant_attempts: 2   # what a grant is worth, when triage grants one
```

**These are read by the triage docket**, which is where work that has stopped
moving is collected and delivered to the development manager: `stuck_merge_age`
decides when an unmerged publication is docketed, and the item's budgets —
rounds spent, repair grants, re-runs, merge re-arms — are carried on every
entry so a decision about one is made against what the item is allowed to
spend rather than against the evidence alone. Deciding what becomes
of a docketed item is the development manager's; the caps are what refuse a
decision that would spend more than the item is allowed.

**The docket reads the record the guards enforce, not a count of its own.** Every
figure on an entry — the rounds spent, each decision recorded, and each cap
beside it — comes from the [per-item counters](#what-one-work-item-has-been-given)
a decision spends, and the re-runs already carried out come from the per-stoppage
re-run records under `<state root>/products/<product id>/reruns/`. It
is read as the docket is read rather than written into the entry: the entry is
recorded once as the work stops and every decision about it is made afterwards,
so an entry frozen at docket time could only ever show every decision as absent.
That was the defect — a re-run decided and durably recorded, a resubmission
refused as one of one re-runs spent, and a docket showing nothing decided, which
nearly had one authorized recovery spent twice. An item whose record cannot be
read says so on its entries instead of rendering as an item nobody has decided
anything about.

The docket is built when something scans: `yoyo reconcile`, and the moment a
development manager conversation opens. There is no scheduled process behind it,
so `stuck_merge_age` is a floor rather than a promise — a publication becomes
docketable at that age and is docketed the next time one of those happens.

**All three are read.** The docket above consumes `stuck_merge_age` — an approved
publication older than it is docketed at the next scan — and `review_rounds_cap`
bounds the [per-item counters](#what-one-work-item-has-been-given) below, which
every run writes to and `yoyo status <id>` reports. The development manager's
triage decisions spend them: a decision of `repair` takes a grant of
`repair_grant_attempts` rounds truncated to what the cap has room for, and
`rerun` and `rearm` each spend a budget of their own. Every one of the three has
a budget nothing else spends, and is refused once that budget is gone; the two
that buy review rounds — a repair grant and a re-run — are bounded again by the
round cap, which they share with each other and with every run of the item. The
[table below](#what-one-work-item-has-been-given) says which bound refuses
which.

Recording a decision and carrying it out are two steps, and two of the six
decisions have an action for the second. They are the two opposite answers to a
run that stopped: `yoyo triage rerun` starts the item over, and `yoyo triage
repair` continues the run that stopped on the change it already has.

`yoyo triage rerun <run-id> --reason "<the recorded decision>"` starts a fresh
run of the item whose stopped run the docket entry names. It is refused unless that run is terminally recorded with
its blocker standing — read from the run's own record rather than from the
docket entry — and one docketed stoppage is re-run once, whatever the item's
budget still says. It is also refused unless a decision of the development
manager's is there to carry out: the decision spends the item's re-run budget as
it is made, and each one authorizes exactly one re-run, so what has already been
claimed for the item is read back against what was decided. An item whose budget
carries no re-run is one nobody decided this about; an item whose decisions have
all been carried out is refused too, because that counter is a total nothing
clears and reading it alone would let a second stoppage of an already re-run item
start on the strength of a decision that was about the first. A second stoppage
needs a second decision, which past the once-per-item cap is an escalation rather
than a larger budget. It is refused, finally, unless the work item itself is one
a run may start on — open, with nothing it depends on outstanding — which for a
docketed stoppage ordinarily means somebody has put it back, because the run
stopping blocked it. The intake hold applies too, because the harness is the one
choosing the work; a re-run under a hold starts nothing and claims nothing, so the
stoppage keeps its re-run for after the hold is lifted. The fresh run records
the development manager as having chosen it and the reasoning the harness was
given as why, which is what the `selected-work-passes-intake-and-records-why`
invariant requires of anything the harness chooses for itself.

**Every one of those refusals is made before the stoppage's re-run is claimed**,
and the claim is what spends it. A condition asked after the claim would spend
the budget on refusing to use it: the item's status is the one that showed this,
where a re-run of a blocked item was refused for the status and the next attempt
was then refused by the once-only guard, for a run that had never happened. So a
refused re-run leaves the stoppage its re-run and says what would make it stop
refusing, and asking again once that is true carries out the same decision. The
opposite order holds past the claim, deliberately: the claim is taken before the
run is started, so a process that dies between the two has spent a re-run nobody
took rather than taken one nobody recorded.

**A full harness is a state rather than a refusal.** `execution.max_concurrent_developers`
is read before the claim, from the same runs in flight the reservation counts, and
every slot being taken neither refuses the carry-out nor fails it: nothing is
claimed, the decision stands until it is carried out or the development manager
withdraws it, and asking again once a slot frees carries out the same one. The
item is meanwhile the open work the scheduler pulls from, which is the other way
it reaches a developer, and the two agree because this claimed nothing. The last
slot can also go between that reading and the reservation; a claim taken for a
run the reservation then refused for capacity is **given back**, because that run
provably never started — a reservation refused for capacity creates no run
record, claims no work item and runs no agent. It is the one thing that gives a
claim back, and a claim carrying a run is refused rather than removed. A
withdrawal the harness could not write is reported rather than swallowed: the
stoppage has then spent its re-run on a run that never started, which is a thing
to go and correct.

The re-run is recorded beside the counters, one file per docketed stoppage at
`<state root>/products/<product id>/reruns/`, and it carries what the stopped
run preserved. Its branch and worktree are **kept** while the fresh run has not
integrated — that is what a development manager's guidance points at when it says
what to cherry-pick — and **retired** explicitly once it has. Anything that could
not be retired stays kept with the reason recorded: a worktree holding
uncommitted work and a branch whose work nothing promoted are both left exactly
where they are, because nothing else records what they hold. Nothing automated
deletes the record, for the reason nothing deletes a counter file — save the one
withdrawal above, which removes a claim whose run was refused a developer slot
and so never existed.

A retirement is written onto the stopped run itself as well, under that run's own
lease, because its record is what `yoyo status` and the docket read to say
whether its branch and worktree are still there. A stopped run promoted nothing,
so the removal names the run that superseded it — `artifacts_retired_by` on the
run's state — which is the second way a recorded removal is earned beside a
promotion of the run's own. The third is `worktree_swept_at`, written by the
[convergence sweep](operations.md#recovering-interrupted-runs) when it retires
an old stoppage's checkout to keep a machine's worktree registrations bounded;
it earns the checkout alone, because that sweep never touches a branch. Where
that checkout held uncommitted work, `preserved_work_ref` names the ref it was
recorded on first — the run's record is the only place that connects the ref to
the item it belonged to. A
retirement the harness could not write onto that run is reported rather than
swallowed: the artifacts are gone and its record still says otherwise, which is
a thing to go and correct.

`yoyo triage repair <run-id> --reason "<the recorded decision>"` is the other
half of the same pair, and it starts nothing over. It re-enters the stopped run's
own repair loop: the same branch, the same worktree, the same developer session,
and the reviewer's findings handed back exactly as they were written.

**What it may hand the run is the grant the development manager already
recorded**, and it spends nothing of its own. Deciding `repair` is what takes the
item's grant — `repair_grant_attempts` rounds, truncated there to what the round
cap had room for — so this reads that record for how many attempts it is worth
and hands the run exactly that. An item nobody granted a repair is one nobody
decided this about, and it is refused; so is an item whose grant the harness has
already carried out, which it counts from the continuations the item's runs
record. Past the once-per-item cap a second is an escalation rather than a larger
budget, and an item with no rounds left never gets a grant to carry out at all.

Four more things refuse it. The stopped run has to be really over, terminal with
its blocker standing, read from the run's own record rather than from the docket
entry. The run has to have recorded a repair input — a run whose provider kept
refusing, or whose replay conflicted, never had a failure returned to its
developer, so there is no repair loop to re-enter. The preserved worktree has
to be as the harness left it: what a continued developer is handed back is
whatever is in that worktree, so a HEAD that moved — an operator mid-surgery, an
agent that committed — is a person's to decide about, and the refusal leaves the
item blocked and says so. And that worktree has to still hold the change: a
checkout the harness would call its own and that holds nothing passes the gate
above and fails this one, and a developer handed the reviewer's findings and an
empty directory delivers an empty repair or reinvents the change from them, with
nothing in the run's record afterwards to tell either from a repair that went
well. The intake hold applies for the reason it applies to a
re-run: this spends on a provider, and the development manager naming the item is
not the operator naming it.

**The resumed run asks the same question again**, and blocks rather than
spending where the answer has changed. That is the enforcement rather than a
second opinion: it binds every route into a run that continues a change — this
action, an interrupted process a later invocation picks up, and whatever
re-entry is built next — because a worktree that lost its change looks exactly
like a valid one and is caught by nothing else. It is asked of every resume whose
worktree is supposed to hold a change already, which is two different cases: a
run picked up inside its repair loop, which carries a failure returned about a
change it made, and a run picked up at the checks or at the review, which has
completed a developer attempt and has nothing else for those steps to judge. Only
the run owed its first attempt is exempt, because an empty worktree is what that
attempt starts from. The blocker it records names the run's branch as where the
preserved work is, and says plainly that nothing was developed, checked, or
reviewed, because the empty diff behind it is not a verdict on the change.

**A fresh run is refused where a repair is owed**, which is the same loss
arriving by the opposite route: nothing is handed back at all, and a run starts
clean on an item whose last run stopped with its change preserved. The fresh
worktree is a perfectly valid one off the target branch, so nothing downstream
notices; what makes it visible is the stopped run's own record — terminal, its
blocker standing, a failure returned to its developer, and a branch that still
carries the change. So `yoyo run` reads that record before it reserves anything,
and refuses, naming both the repair that would continue the change and the re-run
that would deliberately start over. The one fresh run of such an item that is
right is the re-run, and it says so in the record before it starts: triage claims
it against the stoppage, and a claim naming that run is what lets the fresh run
through. Nothing is reserved, claimed, or created by the refusal, so carrying out
either decision afterwards costs the stoppage nothing.

**A repair supersedes the blocker rather than needing somebody to remember to.**
The run that stopped blocked its item and recorded the blocker on its own state,
which `yoyo status`, `yoyo reconcile`, and the docket all read as the fact that
it has stopped. So re-entry clears both at the moment it happens: the item is put
back with the decision recorded on it first, and the run's blocker is cleared onto
the continuation that supersedes it, which keeps the words it was recorded in and
the grant that bought the attempt. The order is the item first, because a run
recorded as running behind an item that still says it is blocked is the one
half-finished state nothing else here would notice, and every refusal is asked
before either write, so a refused re-entry leaves the grant exactly where it was.

The continuations are recorded on the run itself, under `repair_continuations` in
its state file, and they are what the continued run's repair loop adds to
`execution.repair_attempts_before_replan` to know what it may spend. They are
also how the harness knows what a grant has already bought: summed across an
item's runs, they are what a second re-entry is refused against.

The other four decisions still carry themselves out no further than the record:
nothing in the harness repeats a merge request the forge dropped, and a re-scope,
a wait, and an escalation ask for no action at all. The budget is spent when the
decision is recorded, which is the same order every counter here is written in —
an attempt nobody took rather than one nobody counted — so a decision nobody acts
on has still cost the item its budget.

`stuck_merge_age` is how long an approved publication may sit unmerged before it
is docketed. It is an age rather than a deadline because what makes a
publication stuck is that nothing has happened to it, and nothing happening
offers no event to hang a deadline on. It must be positive: an age of no time at
all dockets every publication the instant it is made, which is a docket of
everything and a triage of nothing.

`review_rounds_cap` bounds the review rounds one work item may accumulate in
total — across repairs, across runs — past which triage may no longer hand it
back for another repair. Past the cap triage still has both of its other
actions: escalate the item, or re-scope it. `0` is a choice somebody can mean and
is accepted as one: an item that reaches triage at all is never repaired again.
The one thing that crosses it for a single item is
[an operator's recorded override](#crossing-a-cap-the-operator-decides-to-cross),
which is what makes an escalation answerable rather than only sayable.

`repair_grant_attempts` is how many repair attempts triage hands an item when it
decides the work is worth another go. Leave it out and it follows
`execution.repair_attempts_before_replan`, tracking that budget rather than
copying it: raise the budget and the grant rises with it. It may not be zero,
because a grant of nothing leaves the item exactly where granting nothing would
have. A project that configured no routine repair attempts at all still gets a
derived grant of 1 rather than a configuration that fails to load — the grant is
triage's deliberate exception to that budget, not another helping of it.

### What one work item has been given

The thresholds above bound something, and what they bound is a durable record
per work item: the repair grants triage has given it, the re-runs it has caused,
the merge re-arms it has made, and the **review rounds** the item has cost across
every run of it. It lives beside the runs under the state directory, one file per
item, and it outlives them — a run is settled and its worktree and branch are
removed, and what the item has been given is still there.

That it is not on a run is the whole point. Every budget a run spends starts
again at zero in the next run, so an item handed back, run again, and handed back
again is an item nothing was bounding. `yoyo status <id>` reports it under that
item's runs, in text and in `--json`:

```text
triage of yoyodyne-ifd.90: triage has spent 2 passes on it
  review rounds: 3 spent across every run of this item, under the cap of 4
```

At or past the cap — 4 of 4 exactly included, because a grant needs a round and
none remains — the same line reads: `review rounds: 6 spent across every run of
this item — at or past the cap of 4, so no decision that buys a round remains`.
What may still happen is what the budget lines beside it say: waiting,
re-scoping, and escalating spend nothing, and a merge re-arm spends only its
own budget, whatever the rounds say.

```text
  repair grants: 1 of 1 permitted; re-runs: 0 of 1; each is refused by its own budget or once no round remains
  merge re-arms: 1 of 2 permitted
  1 grant(s) were cut down to the rounds the cap still had room for; 1 round(s) were granted in total
  waiting, re-scoping, and escalating spend nothing and stay available; a re-arm spends only its own budget, whatever the rounds say
```

**The first line counts what has been spent, not how many times triage looked.**
Three of the development manager's six decisions spend a budget here — a repair
grant, a re-run, a merge re-arm — and `wait`, `rescope`, and `escalate` cost
nothing and reach no counter, so an item that was escalated reads `triage has
spent nothing on it`. Whether stopped work has been decided, and what was
decided, is recorded on the work item itself.

**This is also what the docket reports.** Every entry for an item carries these
counters and these caps, read as the docket is read, so what `yoyo status` says
about an item, what a docket entry says about it, and what refuses the next
decision about it are one record rather than three counts of it.

**A round is a reviewer verdict a developer attempt produced**, counted across
every run of the item. A re-review no developer attempt produced is not one, so a
promotion that [loses its race](#losing-a-race-for-the-target-branch) and gets a
fresh verdict on the replayed change is not charged for it — counting that would
charge an item for losing a race it did not cause. A review re-asked for after an
interrupted process is the same case and is counted once for the same reason.
Rounds are recorded whatever a cap says, because a round is something that
happened rather than something being asked for.

**Each counter is written before the action it counts takes effect**, so a
process that dies between the two has recorded a grant it did not give rather
than given one it did not record — an unspent attempt rather than a duplicated
one. Concurrent updates are serialized per item, so no increment is lost, and a
record that cannot be read is a refusal rather than an empty budget: an
unreadable budget read as empty is every cap in it stopping to mean anything.
Recovery from one is a decision, not a repair: the record is one JSON file per
item at `<state root>/products/<product id>/triage/`, named by a slugged
rendering of the item id with a digest suffix (so a listing reads which item
each file belongs to, and two ids that render alike still get their own files
— match on the slug). Read it and fix what is malformed if the history is
worth keeping — or delete it, which resets every budget the item had spent,
and is therefore a deliberate re-budgeting to record on the work item in the
same breath, not a cleanup. Nothing automated deletes one, for exactly the
reason nothing reads one as empty.

Which threshold refuses which action:

| Action | Refused by |
| --- | --- |
| another repair grant | one per item, and `triage.review_rounds_cap`, truncated to the rounds it still has room for — one precondition among several: the decision recorded here spends the budget, and `yoyo triage repair` re-enters the stopped run's repair loop on it, which is a claim the harness makes rather than the operator, so `selected-work-passes-intake-and-records-why` also requires the intake hold consulted before the run is continued and the reasoning recorded in the run's durable state. That action is bounded again by what the grant has already bought, read back from the continuations the item's runs record, and it refuses a preserved worktree that is not as the harness left it |
| another whole run of the item | one per item, and `triage.review_rounds_cap`, refused outright once none remain — one precondition among several: the invariant `selected-work-passes-intake-and-records-why` also requires the intake hold consulted before the claim and the selection reason recorded in the run's durable state. The decision recorded here spends the budget; `yoyo triage rerun` starts the run and carries both, is bounded again by one re-run per docketed stoppage, and reads this counter back — against the re-runs already claimed for the item — as the proof that a decision is there to carry out |
| re-arming a merge the forge dropped | `execution.integration_retries_before_reconciliation` — one precondition among several: a re-arm is an integration retry against the target branch, so `one-promotion-per-target-branch` binds the re-arm action (unbuilt today), which must repeat only the identical already-authorized forge request under the harness's own lease. The decision recorded here spends the per-item integration-retry budget as shipped; the design's once-per-publication counter arrives with the re-arm action, and performing the re-arm is that action's |

The first two buy review rounds, so the round cap bounds them, and a grant is
**truncated** rather than refused where some rounds remain: at the defaults an
item that has been through its repair budget once has spent three rounds of
four, so the configured grant of two attempts is cut to the one round that is
left, and the truncation is recorded. An untruncated grant would promise a round
nothing would let it take, and one that overshot the cap would make the cap
decorative.

The room it is cut to counts what a grant already recorded has promised, not
only the rounds the item has produced. A grant is spendable from the moment it
is written, so two taken before either is carried out would otherwise be cut
against the same room and promise between them more than the cap has, with
neither one overshooting it.

**The round cap is not their only bound, and could not be.** Each of those two
is also once per item, which is not configured because it is the workflow rather
than a judgement about pace: triage takes its own decisions about one item once,
and a second is an escalation rather than a bigger budget. The rounds cannot
stand in for it — they bound what an item costs, and an item whose runs stop
before any reviewer verdict, on a provider that kept refusing or a replay that
conflicts, costs no rounds at all. With only the round cap, that item could be
handed back and re-run without bound while every counter read zero.

A merge re-arm buys no round at all, which is exactly why it needs a bound of
its own: an action that costs nothing to take is the one that can be taken
forever. It follows the integration retries a single run is already permitted,
which is the same judgement about the same thing one level up.

#### Crossing a cap the operator decides to cross

**A cap refuses the answer to an escalation as readily as it refuses a machine,
and one thing crosses it: the operator, in a record.** Every cap above stops
triage handing an item back again, which is what they are for — but they stopped
the recording as well as the carrying out. A development manager past the round
cap could not record a re-run of the item; `yoyo triage rerun` refuses without
that record; and escalating recorded neither. So a cap-exhausted item was
unrunnable by every path the harness keeps a record of, and an operator who read
the escalation and ruled that the work should be run again had that ruling
carried out by admitting fresh work in its place.

```bash
yoyo triage override --budget "review round" --cap 8 \
  --by "mason" --reason "REBUILD: the rounds went on a base that had moved" yoyodyne-ifd.143
yoyo triage override --budget "re-run" --clear --by "mason" --reason "driven by hand until it lands" yoyodyne-ifd.143
```

`--budget` names the budget in the words a refusal uses — `review round`,
`repair grant`, `re-run`, or `merge re-arm` — and `--cap` raises it while
`--clear` lifts it entirely. `--by` and `--reason` are required, because an
override nobody is named for is the thing this record exists to replace; the
override is kept on the item's own triage record, and `yoyo status <beads-id>`
and every docket entry for the item report it beside the budgets it changed.

**It clears or raises and never lowers.** An override that would leave a budget no
larger than it already stands is refused and nothing is recorded, so an item's
overrides are a monotonic account of who gave it more room and why. Lowering a cap
is a judgement about the project's pace rather than a decision about one item, and
`review_rounds_cap` above is where that is made.

That holds after the configuration moves, too. The ceiling in force for an item is
the larger of the configured cap and its override, not the override — so raising
`review_rounds_cap` from 4 to 10 over an item carrying an override to 8 gives that
item 10 like every other, rather than pinning it to the 8 somebody once gave it. An
override only ever adds room.

**It is yours and nobody else's.** No role has a word for it: the development
manager's triage vocabulary cannot produce one, and the actions that carry
decisions out read overrides rather than write them. It is a terminal command for
the reason `yoyo release` is one — the switch that answers an escalation has to
work with no conversation open.

**It carries nothing out.** Recording an override changes what the guards will
permit and nothing else. The development manager then records the decision the
escalation was about, which spends the item's budget exactly as it always did, and
`yoyo triage rerun` or `yoyo triage repair` carries that decision out under every
condition either already asks — the intake hold, the stoppage being over, the item
being one a run may start on, a free developer slot. Crossing a cap and spending
it are two decisions and stay two.

**These budgets are per machine.** Two collaborators running their own harnesses
against one repository each hold a full set for the same item, so a cap of one is
a cap of two across the pair. That is a recorded limit rather than a design, and
[`docs/team-mode-scope.md`](team-mode-scope.md#a-recorded-gap-per-item-budgets-are-per-machine)
states it where the team-mode design will need it.

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
- a `triage.stuck_merge_age` that is not a duration, or that is zero or
  negative — unlike the usage-limit pauses, "no time at all" is not a choice
  anybody can mean here;
- a negative `triage.review_rounds_cap`, or a `triage.repair_grant_attempts`
  below 1 — a cap of `0` is a choice and is accepted, a grant of `0` is not;
- an `execution.remote` that is empty or is not a plain remote name, since it
  reaches a `git push` command line; and an `execution.push_remote` that is set
  and is not one, for the same reason — leaving it out is what says your run
  branches go to `execution.remote`;
- a `product.specifications` that is empty, absolute, or climbs out of the
  repository, since it decides what the product manager reads; and the same of
  `product.invariants`, `product.designs`, and `product.decisions`, since they
  decide which documents the harness treats as canonical artifacts and which
  paths a developer's change may not touch. This is the check on the text; the
  same four are checked again against the filesystem when something writes into
  them, which is where a symlink out of the repository is caught, and which is a
  refusal at the point of the write rather than at load;
- a persona path that is absolute, traverses upward, is not Markdown, is missing,
  is empty, or resolves through a symlink to somewhere outside `.yoyodyne`;
- a `role` that is not one of the harness's five, which is how a typo in an
  agents block is caught: the message names what was written and lists what could
  have been meant. Adding a role is a change to the harness, not to this file;
- a role and backend combination the backend does not support, such as an
  architect on the Codex backend — and the same refusal for a provider your
  project declared itself, including one asked to hold a tool posture it never
  claimed, such as a developer on a provider that declared only `read-only`;
- a `providers:` entry that names no adapter or one this build ships none for,
  serves no role, holds no tool posture, names a role or posture the harness does
  not have, reads nothing its provider says, or tries to replace a backend this
  build ships;
- any effective configuration that fails validation, even when every individual
  layer looked reasonable — for example `max_concurrent_developers` above the
  configured developer instances, or automatic integration with no checks;
- `slack.enabled` with no `slack.channel`, a channel that is not a channel id
  or name, or an entry under `slack.avatars` keyed by something that is not a
  role or `harness` or valued as something that is neither an emoji shortcode
  nor an https image URL — all checked whether or not reporting is switched on,
  so a typo is found now rather than on the day somebody turns it on;
- an `operators` entry that binds no namespace at all, binds one that is not an
  address, a forge account, or a Slack member id, names a grant the harness does
  not have, or binds an identifier a second human already bound — and two humans
  holding `own-intent`, since intent has one owner;
- an `accounts` alias that is not an identifier, a description longer than 200
  bytes, an agent whose `account` names an alias the mapping does not declare, a
  `pool` that is neither `active` nor `reserved`, a negative
  `weekly_budget_usd`, or a mapping whose every account is reserved — a pool
  with an empty active half is one every run falls out of.

## Provider accounts

`accounts` is the provider accounts this project runs its agents under, keyed by
the alias each one is known by here. One is the ordinary case:

```yaml
accounts:
  default:
    description: the Claude subscription this machine is signed in to

agents:
  developer:
    role: developer
    backend: claude-code
    model: opus
    account: default
```

The whole mapping is optional. A project that names none runs under the alias
`default`, every agent is assigned to it, and nothing about a single-account
project has to be written down for its runs to say what they ran under.

**An entry names an account and never a credential.** There is deliberately no
key here that holds a secret or a path: authentication is the provider's own and
lives on this machine, and the alias is what everything else refers to. Every run
record and every surface that reports one names the account it ran under — `yoyo
status` says it, and so does the message that opens a run's Slack thread.

**A project with one account authenticates where this machine already is**,
whatever that account is called. Nothing about a single-account project changes
because pooling exists: `claude` reads the home it always read, and the alias is
still only a name for the record.

**Under a pool, where an alias authenticates follows from the alias.** `default`
stays the machine's own home. Every other alias has a provider home of its own,
at `<state root>/accounts/<alias>`, which the harness sets `CLAUDE_CONFIG_DIR` to
when it invokes under that account. That is one rule, and the harness,
`yoyo doctor`, and `bin/yoyo-account` all read it the same way. It is a rule
rather than a setting because this file is versioned with the repository, and a
directory belonging to one machine has no business in it.

The consequence worth knowing is at the moment you declare the second account,
not before it. A project whose single account was aliased `work` was
authenticating in this machine's home; adding a second account gives `work` a
home of its own, which nobody has signed in to yet. `yoyo doctor` reports it as
`account:work` with the exact login to run, and `bin/yoyo-account` is the other
way to settle it. Aliasing the account you are already signed in as `default`
avoids the step entirely.

### Pooling work across several accounts

A second entry pools the work:

```yaml
accounts:
  default:
    description: the account this machine was signed in with
  second:
    description: the other subscription
    pool: active
    weekly_budget_usd: 100
  spare:
    pool: reserved
```

- **`pool`** is `active` or `reserved`, and defaults to `active`. The active
  accounts are round-robined, one account per run; a reserved one is served from
  only when no active account can be. A mapping whose every account is reserved
  is refused, because a pool with an empty active half is one every run falls out
  of.
- **`weekly_budget_usd`** is optional. It stands an account down once the runs
  that named it have cost that much over the seven days behind now, read from
  what those runs actually cost rather than from a price table. Leaving it out is
  unbudgeted on purpose: spend on that account until the provider's own limit
  stops us.

  Writing `weekly_budget_usd: 0` is not the same as leaving it out, and means
  what it reads as — nothing may be spent on this account — so it is how an
  account is stood down while it stays in the mapping and keeps its login. A
  negative budget is refused, because it says nothing the zero does not say more
  plainly.

**The rotation's cursor is the run records.** Each run already writes down the
account it spent, so the pool takes the first active alias after the one the last
run recorded. Nothing else is kept, which is why the rotation survives a crash, a
second process, and a machine that was off for a week.

The cursor is read when a run starts and written when that run's record is
reserved, and those are not one step. Two runs starting in the same moment can
therefore read the same cursor and be served by the same account, and the
rotation resumes from whichever of them recorded last. It is a rotation over
time rather than a strict alternation, so with several developers running
concurrently expect the split across a day to be even and any individual pair of
simultaneous starts not to be. Nothing is lost when it happens: both runs are
attributed to the account they actually spent, and a budget that account has
already exhausted still excludes it, because that is read at the same moment.

**A run is affined to the account it started on.** The account is chosen once,
before the work item is claimed, and recorded on the run. Every invocation that
run goes on to make — each repair attempt, the review of the change, and anything
a later process resumes — reads the alias back off the record. A run that moved
between accounts mid-flight would leave half its spend on one subscription and
half on another with nothing saying so.

**Conversations sit still while runs rotate.** A conversation belongs to its
agent and lasts for weeks, so it is held under the account that agent's entry
names, or under the first active account where it names none. An agent that moved
between accounts each turn would have no provider session left to resume.

**A per-agent `account` governs that agent's own invocations, not the runs it
serves.** Under a pool the split is by what the invocation belongs to rather than
by which role makes it: a conversation, an exchange round, and a branch review
belong to their agent and are made under the account that agent's entry names, or
under the first active account where it names none. A run belongs to the work
item, so it is served by the rotation whichever agent's entry the developer and
reviewer invocations came from.

That is deliberate rather than an omission. `yoyo init` writes `account: default`
onto every agent it generates, so honouring the per-agent entry for runs would
mean that adding a second account to a project the harness scaffolded rotated
nothing at all — pooling would read as configured and do nothing, which is the
one failure that looks exactly like success. Taking an account out of the
rotation is what `pool: reserved` is for, and standing one down is what
`weekly_budget_usd` is for.

**A pool with nothing left to spend refuses before it claims anything.** When
every account is over its weekly budget, the run is refused at the point the
account would have been chosen — before a work item is claimed and before a
worktree is cut — and the refusal names what each account has spent against what
it was budgeted.

**Setting the second account up** is [in the
README](../README.md#running-several-claude-accounts), and `bin/yoyo-account`
asks the questions and runs the login. `yoyo doctor` then reports each configured
alias by name — `account:second` — saying whether it is authenticated and which
half of the pool it is in.

## Operators

`operators` is the humans this project recognizes. Each entry binds one person's
identifier namespaces and says what that person may do:

```yaml
operators:
  mason:
    git_email: mason@example.com
    forge_account: mason-bryant
    slack_member_id: U0123456789
    grants:
      - own-intent
      - direct-work
  jordan:
    git_email: jordan@example.com
    forge_account: jordan-q
    grants:
      - direct-work
```

The whole mapping is optional, and a project that names nobody recognizes
nobody — which is every project until it names somebody, and is closed rather
than open. `yoyo init` writes an example of it commented out, beside the
[`slack`](#reporting-to-slack) block, so a generated file shows what an entry
looks like without recognizing anybody.

It is **top level rather than under any one surface**, because a human is known
by more than one. An act carries an identifier and never a person: a commit
carries an address, a push carries a forge account, a thread reply carries a
member id. Binding all three to one entry is what lets an authority check
resolve whichever namespace the act arrived through to the same person and then
ask what that person may do. Filing the whole thing under `slack` would have
made the Slack id the identity and the other two an afterthought.

**No new identity machinery, deliberately.** Git and Dolt authorship are the
assertion — the address on a commit is what the author says about themselves —
and the forge's push authentication is the proof, at the one boundary that is
shared. This mapping adds the join between namespaces that otherwise have
nothing to do with each other; it does not add a login.

Each key is a short name for a person, in the same shape as an agent name
(`mason`, `jordan-q`). Every field under it is optional except that at least one
namespace has to be bound: a human bound to nothing is authority attached to
nobody, since no act can arrive carrying an identifier that reaches them.

- `git_email` — the address their commits and tracker writes are authored with.
- `forge_account` — their account on the remote the project publishes to.
- `slack_member_id` — their member id in the reporting workspace, from their
  profile → "Copy member ID". It is identity rather than a secret, which is why
  it is checked in here with the rest.

Addresses and forge accounts are matched without regard to case, because they
are case-insensitive where they live; a member id is matched exactly, because it
is an opaque id the workspace issued rather than something a person types. One
identifier may be bound by one human: an identifier that resolves to two people
resolves to neither, so it is refused when the configuration loads.

`grants` is what the human may do, whichever namespace they arrive through, and
it defaults to empty. Recognizing somebody and authorizing them are two
decisions, so an entry with no grants records who a person is without giving
them anything — which is also how you take authority back without forgetting the
person.

| grant | what it is |
| --- | --- |
| `own-intent` | stating and approving what the product is for: the brief, the goals, and the non-goals. **At most one human may hold it** — several people amending goals concurrently is conflict machinery nobody has designed. |
| `direct-work` | steering work already in flight: the directives that reach a run, and the thread replies the Slack sink acts on. |

The grants are checked where the act arrives rather than where it is recorded,
which is what makes them worth stating: the point of attaching authority to a
person is that `by: operator` becomes a proven human rather than whoever ran the
command.

## Reporting to Slack

`yoyo slack` reports what the harness is doing into a Slack channel: one thread
per work item, one message per milestone, and every report an agent filed at the
severity it was filed under. The project says where to report and what each
speaker looks like; nothing else about reporting is configurable here.

```yaml
slack:
  enabled: true
  channel: C0123456789   # a channel id, or a #name
```

The whole block is optional, and a project that omits it reports nothing — which
is every project until it opts in. `channel` takes a channel id or a name;
an id is worth preferring because renaming the channel does not break it.

**`yoyo init` writes it commented out**, together with the
[`operators`](#operators) example beside it, so the generated file shows the
shape and says the capability exists rather than leaving both to be found in
[`docs/slack/setup.md`](slack/setup.md). Deleting the leading `# ` from each
line is the whole of turning it on; `yoyo setup` does the same edit for you, and
replaces the commented example rather than writing a second block under it.

### Avatars

Each speaker posts under its own name and picture, and the picture is the
project's to choose:

```yaml
slack:
  enabled: true
  channel: C0123456789
  avatars:
    harness: ":gear:"
    developer: ":ship-it:"                              # a custom emoji works
    reviewer: https://example.com/faces/reviewer.png
```

Keys are roles — `product-manager`, `architect`, `development-manager`,
`developer`, `reviewer` — or `harness` for what no persona did. A value is
either an **emoji shortcode**, including a custom emoji this workspace added
itself, or the **https URL of an image** Slack fetches. Both shapes need the
`chat:write.customize` scope the [app manifest](slack/manifest.yaml) already
declares, so neither costs a reinstall.

The mapping is optional and so is every entry in it. A speaker with no entry
keeps the avatar the harness ships, so naming one persona's picture does not
blank the rest. An avatar that is neither shape is refused when the
configuration loads, whether or not reporting is switched on — Slack accepts an
unknown shortcode or an unreachable image without complaint and quietly shows
the app's own icon, so nothing downstream would ever say so.

Entries **merge across layers** rather than replacing each other, the way agents
do: a project that extends a bundle and changes the developer's picture keeps
every other one it inherited.

**Only the picture is configurable.** The name a message appears under, and
whose account it is, are not here and are not meant to be — who speaks is a
claim about who did the work, and a project that could rewrite it could
attribute a promotion to a developer. The avatar carries none of that:
everything it distinguishes is already distinguished by the name beside it and
the voice below it, so a reader whose client renders no picture loses nothing.

**Every name says which product it speaks for**, from
[`product.id`](#layout): `Development Manager (yoyodyne)`,
`Yoyodyne (yoyodyne)`, and a project that configured a second agent for a role
reads `Developer (opus) (yoyodyne)` — the product is last on every name, in the
same shape, for every speaker including the harness. It is applied by the voice
layer from the id the configuration already carries, never authored per message
and not configurable beside the avatars, because it is a fact about which
harness is talking rather than a claim about who did the work. An operator with
two products in development is running two harnesses, and where both are read in
one channel this is the only thing a message carries that tells them apart.

**Who may steer the harness from a thread is not configured here.** The
allow-list is derived from [`operators`](#operators): the humans granted
`direct-work` who have bound a `slack_member_id`, and nobody else. It is a
derivation rather than a second list because a list maintained beside those
grants is a list that disagrees with them — silently, and about authority. A
human granted `direct-work` who has bound no member id simply is not on it: they
hold the authority, and Slack is not a boundary they can reach it through.

A reply from somebody on that list is recorded as a directive against the item
whose thread it was said in, and reaches the work exactly as one typed at a
terminal does. A reply from anybody else is answered in the thread saying it was
not acted on — visibly, because a channel that silently ignores some people looks
broken rather than closed. What a reply may say is in
[`docs/slack/setup.md`](slack/setup.md#steering-the-work-from-a-thread).

An earlier shape put this list under `slack` as `slack.operators`. It is gone,
and a file that still carries it is refused when the configuration loads, with a
message naming the entry to write instead.

**The credentials are not here and must never be.** The sink reads
`SLACK_BOT_TOKEN` and `SLACK_APP_TOKEN` from its own process environment and
from nowhere else: never from this file, never from a work item, never from a
prompt. That is what keeps the boundary structural rather than behavioral — one
separate process posts, so no run process, and therefore no agent's subprocess
tree, has a Slack token in its environment at all. Exporting them in a shell
profile every process inherits would undo exactly that, so they are read from a
store only the sink's own launch looks at, under names that carry the product —
`yoyo-slack-bot.<product id>` and `yoyo-slack-app.<product id>`. The product is
in the name because a machine running more than one harness has more than one
pair, and a sink launched from a shell holding the wrong one connects,
authenticates, and posts this project's work into another project's channel.
`yoyo doctor` asks whether this project's pair is stored, and whether the sink
that is running was launched with it; [`docs/slack/setup.md`](slack/setup.md#5-store-the-two-tokens-under-this-projects-names)
has the launcher.

Reporting is an observation and never a gate: a workspace that is down, slow, or
misconfigured changes nothing about any run. [`docs/slack/setup.md`](slack/setup.md)
takes a workspace from nothing to live reporting, and the app manifest it asks
for is checked in beside it.

## Personas

A persona is a Markdown file describing how an agent works. Personas specialize
behavior; they never grant it. The harness invariants — agent authority,
worktree sandboxing, the protected paths a developer's change may not touch, the
review verdict contract, integration preconditions, and cleanup — are enforced in
Go and are not configurable, so a persona cannot weaken them:

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
- `path` is relative to the directory the configuration file is in, and must name
  a Markdown file inside it. Absolute paths, `..` traversal, and symlinks that
  escape the directory are rejected. For the ordinary project that is the
  `.yoyodyne` directory, and for a configuration
  [kept outside the repository](#keeping-the-configuration-outside-the-repository)
  it is the directory `init --external` wrote, which is what lets that directory
  be moved as one. A `.yoyodyne.yaml` uses the `.yoyodyne` directory beside it,
  which is where migrating it would put the personas; and a `config.yaml` placed
  by hand somewhere of its own falls back to a `.yoyodyne` directory beside it
  when the persona is not there, so an arrangement that predates this goes on
  loading.
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
system than either is still wanted, and neither shape is it.
[Portable agent configuration](portable-agent-configuration.md) is the draft
that answers what a project should own versus inherit, how the two shapes
convert into each other, and how a bundle improvement reaches a project that
materialized its defaults. It is a draft the architect has not ratified, so
nothing in it is implemented and none of the commands it names exist.

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

`config show` prints the layers it applied, the revision of the configuration in
force, the effective configuration as YAML, and, with `--origins`, one line per
value. Persona bodies are reported as a source and a byte count rather than
inlined, so the output stays readable.

The revision — `cfg-` and a digest, printed by `config validate` as well — is
what a run record names when it says which configuration set it up. It is derived
from the effective values rather than declared, so nobody has to remember to bump
it: two configurations whose effective values agree share a revision however
differently their files are written, a bundle upgrade that moves a default moves
the revision with it, and a changed persona moves it too, because a persona is
what every prompt is written against.

Origins use these values:

| Origin | Meaning |
| --- | --- |
| `harness-default` | No layer supplied the value; the harness filled it in. |
| `builtin:v1` | Inherited from the built-in bundle, by a project that uses `extends`. |
| a file path | Supplied by that project configuration file. |
| `derived:product.id` | Computed from another configured value. |
| `derived:accounts` | An agent's `account` no layer stated, which follows the single account the mapping declares. |

An unexpected effective value is therefore a two-command diagnosis: `--effective`
says what the value is, and `--origins` says which layer is responsible for it.

In a project `init` wrote, the answer is the project file for every configured
value, and `derived:product.id` for `product.repository_id` alone — the one
value the generated file computes rather than states. Nothing reports
`builtin:v1`, and nothing reports `harness-default`, because the generated file
writes down every value the harness would otherwise have filled in. So an origin
that is neither the project file nor that one derivation means the
configuration is inheriting something, which is worth looking at.
