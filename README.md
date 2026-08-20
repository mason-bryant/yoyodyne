# yoyo

Writing code was never the hard part.

The work is writing down the ideas, turning them into designs, breaking them
up, assigning the work, the reviewing, the testing... the whole SDLC. For
anything more complex than trivial software, that was always the majority of
the effort.

This is the work still done by hand around every coding agent. You prompt, you
test, you get someone else to review and approve, you check it against the
design documents.

Putting that structure in place and maintaining the engineering discipline to
keep it there is most of the overhead of building a dev organization.

Yoyo is that structure, built to run without you driving each turn: goals in,
merged software out, a conversation to steer it.

The nearest picture is a
[dark factory](<https://en.wikipedia.org/wiki/Lights_out_(manufacturing)>).
The floor is dark: work is developed, checked, reviewed, and integrated without
anybody watching each step. The office is not. You say what the line is for — a
brief and goals you approve once, and amend when your intent changes. What
reaches you after that is the exception, not the routine: a question only an
owner can answer, a gate only you can open. Days of merged work pass between
them. Everything else between intent and merged code belongs to the harness.
Autonomy here is the absence of routine per-change approval rather than the
absence of you, since a system that never had to ask its owner anything would
be one that had stopped taking direction.

**User testimonials.**

> "Genuinely useful, and the strongest evidence is that it runs itself. This repo is developed by the harness it ships: 50 tracked work items, 142 commits in a day, a PM conversation at turn 97 with real cost and session accounting. Most multi-agent frameworks are demos; this one has a lived-in operational history, and it shows in the design choices."
>
> — Cursor

> "Worth noting the pattern of the day: each role's first real working session has produced a bug report about the machinery it runs on — the architect found its own missing voice and the config fail-closed gap, the PM found the empty-store attribution window, now the DM found the attribution drop. The roles are debugging the system that hosts them, through the channels the system provides. Runs 103/104 still going; operations texts still with you."
>
> — Claude

> "Yoyodyne is genuinely useful as an experimental, governed AI-development system"
>
> — Codex

> "The role's first session did exactly what the role exists for — it exercised judgment (rejecting one proposed option, widening another), corrected itself on evidence, refused to overreach ("the priority is not mine," "admitting that item is the product manager's"), and noted its epistemic limits ("I have not read the source"). The hierarchy is working."
>
> — Claude

Three gates hold that up, and each is enforced by the harness rather than left
to an agent's good behavior:

- **Nothing merges unreviewed.** Integration requires passing checks, an
  approving verdict, and two demonstrably separate provider invocations, so no
  change is judged by the agent that wrote it.
- **The reviewer cannot merge, and cannot be talked into one.** It runs with no
  tools at all, everything it is shown is evidence rather than instruction, and
  a persona can specialize how a role works but never grant it authority it does
  not have.
- **The written goals are the only authority work traces to.** Work reaches the
  backlog with a goal named against it in the words your goals document states
  it in, and the brief and the goals stay yours: a role that disagrees with one
  proposes a change rather than making it.

**You drive it from one conversation.** `yoyo chat` opens it: you talk to a
product manager that has read your product's own written intent and the work
already tracked against it, approve as many of the work items it proposes as you
like in a single answer, and say `/work <id>` when you want one of them run. The
run happens in the background while the conversation stays a conversation — an
isolated worktree, the checks your project declared, an independent reviewer,
that reviewer's findings handed back to the developer to repair, a fast-forward
into your target branch, and — [where you have asked for it](#optional-publishing-and-auto-merge)
— a pull request that merges itself once your required checks pass. `yoyo run`,
`yoyo review`, `yoyo status`, `yoyo reconcile`, `yoyo pause`, and `yoyo resume`
sit beside that conversation as administrative and recovery entry points — one
named item, one branch judged as a whole, what became of the runs already made
and why one of them failed, settling what a killed process left behind, stopping
everything the harness would spend until you say otherwise, and releasing a run
waiting on a refusal the provider no longer makes — rather than as the way
work normally happens.

**Quick start.** With [Beads](https://github.com/gastownhall/beads) and
[Claude Code](https://code.claude.com/docs) installed, and Go 1.24 or newer:

```sh
go install github.com/mason-bryant/yoyodyne/cmd/yoyo@latest
yoyo version           # not found? see Install for the one PATH line
cd path/to/your/project
bd init && yoyo init   # then review the checks it proposed in .yoyodyne/config.yaml
yoyo chat
```

[Getting started](#getting-started) is the same three steps with what each one
is for and what it needs; [Install](#install) has the release download and the
from-source routes, for anyone who would rather not have Go.

What exists today is bounded, and the bounds are worth knowing before you start
rather than after:

- **One run at a time by default**, and choosing is a separate verb. In a
  conversation you say `/work <id>` when you want the next thing run, one at a
  time. [`yoyo work`](#letting-the-harness-choose-the-work) is the harness
  choosing for itself: it pulls the ready items from the top of the backlog and
  runs up to
  `execution.max_concurrent_developers` of them at once, which defaults to one.
  The product manager still owns the backlog and its order.
- **The product manager is the agent you drive the work from**, and it is no
  longer the only one you can talk to: `yoyo agent chat <name>` addresses any
  configured agent, each with its own durable conversation and its own authority.
  What none of them do yet is act without you — the pulling above is the harness
  reading the order and the readiness the tracker already holds, not an agent
  deciding anything, and nothing decomposes a design on its own.
- **Claude Code is the backend that runs.** `codex` exists as a name in the
  configuration vocabulary; the adapter behind it is designed and not built, and
  a run refuses a developer configured for anything but `claude-code`.
- **Your project need not be Go**, or any particular language. The harness's
  only contact with your toolchain is the list of shell commands you declare as
  `checks` and the exit codes they return.

## Install

`yoyo` is one binary. Two of the three ways to get it need no checkout of this
repository.

**With Go 1.24 or newer**, which is the shortest path:

```sh
go install github.com/mason-bryant/yoyodyne/cmd/yoyo@latest
```

That writes `yoyo` into `$GOBIN` if you have set one, and otherwise into
`$(go env GOPATH)/bin`, which is `~/go/bin` unless you moved it. `go install`
does not put that directory on your `PATH`, so an install that worked can still
leave `which yoyo` finding nothing; add it once, in the shell you use:

```sh
echo 'export PATH="$PATH:$(go env GOPATH)/bin"' >> ~/.zshrc   # bash: ~/.bashrc
```

Then open a new shell and check what you got:

```sh
yoyo version   # v0.1.0, the release tag it was installed at
```

A tag rather than `dev` is also how you know the binary knows which release it
came from. Replacing `@latest` with a version — `@v1.2.3` — pins that release
rather than following the newest one.

**From a release download**, if you would rather not have Go at all. Each tag on
[the releases page](https://github.com/mason-bryant/yoyodyne/releases) carries a
binary per platform and a `checksums.txt` covering them. Set `tag` to the
version you want from that page, and `platform` to yours:

```sh
tag=<the tag from the releases page>
platform=darwin_arm64   # or darwin_amd64, or linux_amd64
base="https://github.com/mason-bryant/yoyodyne/releases/download/$tag"
curl -fsSLO "$base/yoyo_${tag}_${platform}.tar.gz"
curl -fsSL "$base/checksums.txt" | shasum -a 256 -c --ignore-missing
tar -xzf "yoyo_${tag}_${platform}.tar.gz"
install -m 0755 yoyo /usr/local/bin/yoyo
yoyo version   # the tag you downloaded
```

`/usr/local/bin` is on most `PATH`s already; install somewhere else and the same
`PATH` line above applies, with that directory in place of `$(go env GOPATH)/bin`.

**From source**, which is also how you work on yoyo itself:

```sh
git clone https://github.com/mason-bryant/yoyodyne
cd yoyodyne
make build
```

That writes `./bin/yoyo`, stamped from `git describe`, so `yoyo version` names
the commit a local build came from rather than only saying `dev`.

**What is tested, and what is only built.** `yoyo` is developed and used on
macOS. The `linux_amd64` binary is built by the same workflow as the others and
exercised by CI, and by nothing else; the `darwin_amd64` binary is built and is
not regularly run by anyone. Treat anything other than macOS on Apple silicon as
untested rather than as a platform with the same evidence behind it. There is no
Windows binary, and Windows is not supported.

Whichever way you got it, the rest of this document calls the binary `yoyo` and
assumes it is on your `PATH`. Run it from your own project: it discovers its
configuration by searching upwards from the current directory.

## Getting started

Three steps, in this order:

1. **[Install `yoyo`](#1-install-yoyo)** — one binary, in your `PATH`.
2. **[`yoyo init`](#2-yoyo-init--give-the-project-its-own-configuration)** —
   give your project its own configuration and personas, and its checks
   proposed from what the repository already declares.
3. **[`yoyo chat`](#3-yoyo-chat--establish-the-brief-and-the-goals)** — establish
   the brief and the goals with the product manager, and drive the work from
   there.

Nothing here assumes your project is written in Go, and nothing after step 1
needs a checkout of yoyo: a configured project carries its own configuration
and personas.

**What you need.** Git and a repository with at least one commit;
[Beads](https://github.com/gastownhall/beads) (`bd`), the tracker every role
reads and writes; and [Claude Code](https://code.claude.com/docs), installed and
authenticated, which executes every agent role. Go 1.24 or newer is needed only
if you install with `go install` or build from source; a release download needs
neither Go nor a checkout. Two more are needed **only if you want pull
requests**: a Git remote, and [`gh`](https://cli.github.com) authenticated with
`gh auth login`. Without them everything stays on your machine and nothing is
pushed.

Every step below is executed by [`scripts/walk-adoption.sh`](scripts/walk-adoption.sh),
which walks this section against a throwaway Python project — its own scratch
repository, its own temporary state directory, removed when it exits — and
asserts what each step is documented to do. Run it if you would rather watch the
path work than take this section's word for it. It needs no provider unless you
pass `WALK_PROVIDER=1`, and it names any claim it could not exercise rather than
passing over it.

### 1. Install `yoyo`

```sh
go install github.com/mason-bryant/yoyodyne/cmd/yoyo@latest
yoyo version
```

If `yoyo version` prints a tag you have it; if the command is not found, the
install directory is not on your `PATH`, and [Install](#install) above has the
one line that fixes that, along with the release download and the from-source
paths and which platforms are tested. Then change into your own project, since
everything after this runs there:

```sh
cd path/to/your/project
```

### 2. `yoyo init` — give the project its own configuration

**First initialize the tracker**, which is a required dependency rather than an
optional integration: work items, their status and dependencies, blockers, and
the durable record of what agents did all live there.

```sh
bd init
yoyo init
```

`yoyo init` writes a complete `.yoyodyne/config.yaml` and copies the five
personas into `.yoyodyne/personas/`, naming the product after the directory
unless you pass `--product`. Nothing already there is overwritten without
`--force`, and the refusal happens before any file is written, so a project is
never left half-configured. See [Configuring a project](#configuring-a-project)
for what is in the file.

**It also points the tracker at your Git remote**, so the backlog is shared
rather than one per machine. Beads moves its data over an ordinary Git remote
under refs of Dolt's own, so `init` reads `origin` and configures the tracker to
sync there — one repository, one permission model, nothing to stand up — and
prints what it configured. A tracker that already has an `origin` remote is left
alone, `--tracker-remote <url>` replaces it for the atypical case of a tracker
kept in a repository of its own, and a project with no Git remote is told what
to run once it has one rather than failing over it. Two consequences
worth knowing: the tracker's history counts against your repository's size like
any other history, and what it pushes — `refs/dolt/data` and a
`__dolt_remote_info__` branch — is carried without complaint by GitHub but is
worth checking on a forge that restricts which refs it accepts. See
[Where the tracker syncs](docs/configuration.md#where-the-tracker-syncs).

**Then review the checks it proposed.** Your repository already announces what
it is built with, so `init` reads that and writes the commands that follow from
it into `checks`, each with a comment naming the file it came from — a
Makefile's `check` or `test` target, `go.mod`, a `package.json` with its
lockfile and `tsconfig.json`, a `pyproject.toml` that names pytest, a Maven
`pom.xml`, a Gradle wrapper. **Nothing in your project is executed to find
them**: detection reads files and stops there, so a `yoyo init` in an unfamiliar
repository runs none of its build.

They are proposals rather than a verdict on your toolchain, so read them before
the first run and edit or delete what does not belong:

```yaml
checks:
  # from pyproject.toml
  - python3 -m pytest -q
```

Everything `init` did not write into `checks` is written beside the list,
commented out, under one of three headings. Only the first asks anything of you:

- **`YOU MUST CHOOSE`** — detection found a toolchain and could not tell which
  command is the gate, and `checks` is empty, so a run is refused until you
  settle it.
- **`ALSO FOUND, AND NOT DECIDED`** — the same open question, except `checks` was
  written from something else and already works, so you can leave this alone.
- **`ALSO FOUND, AND NOT NEEDED`** — nothing open at all: commands `init` read
  and deliberately left out, because what it wrote already covers them. A
  `Makefile` with a `check` target beside a `go.mod` gets `make check`, with
  `go test ./...` and `go vet ./...` offered here rather than added, since two
  gates running the same suite is the suite run twice.

The open questions that reach the first two headings today are tests with no
runner named anywhere, a `package.json` with no lockfile beside it or with
several, one that declares no test script at all, and a Gradle build with no
wrapper. Each says which it is and why.

Taking any of them costs a character: delete its leading `#`. A repository that
announces nothing keeps `checks: []` and the commented examples for Go,
TypeScript, Python, and Java that have always been there; the
[configuration guide](docs/configuration.md#checks) has the same examples with
the reasoning.

Each entry runs through `/bin/sh -c` in the run's worktree. A check must be
non-interactive and must exit non-zero on failure — a check that prints a
complaint and exits 0 is not a gate. Prefer the pinned, non-daemon,
non-interactive form of each tool, so the same commit checks the same way twice.

Each check also gets a wall-clock budget, `execution.check_timeout`, thirty
minutes by default. Raise it as your suite grows, and raise it again if you run
several developers at once: concurrent runs share the machine, so each suite's
wall clock grows without its work doing so. See
[How long a check may take](docs/configuration.md#how-long-a-check-may-take).

**Then validate what you wrote:**

```sh
yoyo config validate
```

`config validate` answers whether the file is *loadable*, and an empty `checks`
list still is; it is `yoyo run` that refuses a run with no checks. Everything
under `.yoyodyne/` is machine-independent and belongs in version control, so
commit it along with the rest of your adoption.

### 3. `yoyo chat` — establish the brief and the goals

Commit what you have added first: a run refuses to start while the primary
checkout has uncommitted changes, and says which files they are. The tracker's
own exports — `.beads/issues.jsonl` and `.beads/interactions.jsonl` — are the
exception, since a run writes them itself.

```sh
git add -A && git commit -m "adopt yoyo"
yoyo chat
```

**What the product manager can see.** Product intent is the Markdown under
`product.specifications` — `docs/product` by default — and nothing else in the
repository is read as intent. Beside it, and labeled as description rather than
intent, it is given the documentation of what the product ships today: this
README, the configuration guide, and the help the commands print. Not the
source, and not the design document. A specification opens with an introduction
saying what the thing is and why it exists, and states the goals that serve it
after that introduction:

```markdown
# Calc

A tiny arithmetic library, kept small enough that a change to it is obvious.

## Goals

- Arithmetic is correct for the operations the library claims to support.
- Every operation has a test that would fail if the operation broke.
```

**A repository with none of that written down is the ordinary starting case**,
and it is what the conversation is for. An empty or missing specifications
directory is reported as "product intent is not written down", which is a true
statement about the repository rather than an error, and the product manager
says exactly that rather than inferring what your product must be about. Tell it
what you are building and it will draft the brief and the goals with you.

It cannot save them. The product manager runs with no tools at all — it manages
the Beads backlog through the harness and never touches your filesystem — so the
division is plain: **the product manager drafts, and you put the files on disk.**
Paste what you agreed into `docs/product/`, commit it, and the next conversation
reads it back as the product's written intent. Nothing fails if you never do, but
goals are what work is admitted against, and a product manager with no goals to
name will stop and ask you for one.

**Then drive the work from the same conversation.** Talk about what you want and
approve the work items it proposes, as many as you like in one answer. You can
also file one by hand if you would rather have something to run immediately:

```sh
bd create --title="Add a subtract function" \
  --description="calc has add and nothing else. Add subtract(a, b) with a test." \
  --type=feature --priority=2
bd ready
```

When there is an item you want run, `/work <beads-id>` starts it in the
background and the conversation stays a conversation; `/status` says where it got
to, `/diff` says what it changed, and `/stop` ends it and settles what it left
behind. That is the whole loop, and [The conversation](#the-conversation) is the
rest of this document's subject.

### Optional: publishing and auto-merge

By default yoyo is entirely local. A repository with no remote publishes
nothing and never notices publishing exists. Two settings turn it on, and they
compose rather than imply one another:

```yaml
approvals:
  publishing: automatic   # push the run branch and open a pull request
  integration: automatic  # merge it on an approving verdict
```

- **`publishing: automatic`** needs a remote by the configured name
  (`execution.remote`, `origin` by default) and `gh` installed and
  authenticated. A project that asked to publish with no `gh` fails **before it
  claims anything**, because a harness that quietly stopped publishing would
  look exactly like one with nothing to publish.
- **`integration: automatic`** is what merges. The harness refuses the setting
  unless deterministic checks and a reviewer agent both exist, so it is
  something a project turns on once it has the gates to justify it. With
  `publishing: automatic` and `integration: human` you get the pull request and
  nothing else: nothing is merged, the run branch survives on the remote, and
  the worktree is preserved for you.
- **Branch protection** needs one repository setting if you use it: **"Allow
  auto-merge"**, which is off by default. It is what lets the harness ask the
  forge to merge once your required checks pass rather than demanding a merge
  seconds after the reviewer approved. Without protection nothing is holding the
  request back and the harness simply merges, so a repository with no protection
  needs no setting changed at all. Merge commits must also be permitted, since
  that is the method the harness asks for.

[How work flows once you approve it](#how-work-flows-once-you-approve-it) has
the full behavior, and the [configuration guide](docs/configuration.md#publishing-through-pull-requests)
has the table of what each combination produces.

### Working on yoyo itself

`yoyo` is configured against its own repository, so a checkout of it is a
project like any other. From one, verify the tools, run every check, and open
the conversation:

```sh
claude auth status --json
bd where
make check
make build
./bin/yoyo config validate
./bin/yoyo chat
```

`make check` is `fmtcheck`, `test`, `race`, and `vet`, and it is the gate CI
runs.

`make dist VERSION=<tag>` builds the release archives and their checksums into
`dist/`, and `make dist-verify VERSION=<tag>` does that and then unpacks the
archive for the platform it is running on and asserts the binary reports
`<tag>`. That target is the whole of what a release is: the release workflow
runs it for a pushed tag and publishes what it produced, and CI runs the same
target on every change with a placeholder version, so a tag push reruns a path
that is already exercised rather than executing it for the first time when a
failure would mean a botched or missing release.

## The conversation

The product manager reads the product specifications — every Markdown file under
`product.specifications`, which defaults to `docs/product` — plus the open Beads
items, and discusses product intent with you. It owns the queue that serves that
intent, and it manages it directly rather than dictating changes for you to
type.

```sh
yoyo chat
yoyo chat --message "What is missing from the brief?" --json
```

That queue is a backlog with an order, and the order is the product manager's.
What is admitted to it and what comes before what are product decisions — the
same intent it already owns, expressed as what to do next — while decomposition,
dependencies, and assignment stay a development manager's, which takes work from
the top of that order rather than choosing for itself. A role that disagrees
with the ordering proposes a change to it exactly as it would propose a change
to a goal; none of them reorders it or admits work to it. The order is written
down as Beads priority — 0 first, 4 last — so there is one place it lives rather
than a second copy that could disagree with the tracker, and items left at the
same priority are in no order anybody decided: the product manager says which
comes first by giving it a higher one. Admitting work says where it goes as part
of admitting it, because a new item has no identifier until the tracker answers
and an ordering left for a later step is an item sitting at the tracker's
default in the meantime — including for an item you approved from a proposal,
which is admitted at that default until the product manager places it.
`/backlog` shows it to you in order, with what is holding each unready item back
and what would be pulled next.

Nothing pulls from that order on its own yet, and the difference is worth
stating plainly: the harness runs a work item when you ask it to with `/work`,
and the development manager that would take from the top of the backlog without
being told is not built. What exists today is the ordered queue it will take
from, an owner for that order, and your view of both.

Work leaves the backlog in one of two ways, and both are recorded on the item.
`close` says the work is done. `retire` says it will not be done, and is the
only way admitted work leaves the queue without being done: there is no delete
and no third way, so scope you asked for cannot quietly disappear from the
queue — it is withdrawn in the open, with a reason, and the item afterwards says
it was retired rather than finished.

A specification opens with an introduction saying what the thing is and why it
exists, and states the goals that serve it after that introduction. That shape
is the contract, and the harness checks it: one that has no goals, no
introduction before them, or an empty goals section is named on stderr when the
conversation opens and listed for the product manager alongside the
specifications themselves — and still read, because refusing to load it would
silently lose intent somebody wrote down. A directory with nothing in it is
reported the same way rather than treated as a product with no intent.

The context also says outright what those specifications record of the two
documents intent is written in — the brief saying what the product is and who it
is for, and the goals that serve it — naming each with roughly how much prose it
carries, and calling one that carries almost none a placeholder. That is what
makes the first conversation on a fresh repository the intended opening move
rather than a confusing silence: with no brief and no goals to read, the product
manager says so, offers to draft both from your answers, and starts asking what
only you can answer — what this is, who it is for, what finished looks like. It
asks them one at a time. The opening reply says there are three, says which order
they come in and why that order, and then asks the first, because a paragraph
holding three questions gets one answer and loses the other two. It is a question
and not a gate. Nothing is blocked, nothing is written, later means later, and a
repository whose goals are already written gets no such prompt. The asking is
persona guidance, so a project that wants a different opening
[replaces it](docs/configuration.md#personas) like any other part of the
persona.

One more section sits below those, and it is a different kind of thing: **what
the product ships today**, which is this README, the configuration guide, and
the help every command prints. It is labeled as exactly that — a description of
the implementation as built, never authority about what the product is for — so
that the role deciding what to build next can say which surfaces already exist
without you having to tell it. Where that description and a specification
disagree, the product manager reports the conflict rather than settling it.

Not the source, not the design document, and no way to run a command: those say
how the product is built rather than what it is for or what it ships. The
narrowing this partially undoes is described, with what it bought and what it
cost, in the [configuration guide](docs/configuration.md#what-the-product-manager-sees-besides-them-and-what-it-does-not).

It has no tools: no filesystem, no commands, no network. What it has instead is
the work tracker, through a fixed set of named operations the harness carries
out for it — read an item in full, survey the open queue, create, attribute to a
goal, update, reparent, reprioritize, link and unlink a dependency, close, and
retire. Every
argument is validated before anything runs, at most ten actions happen per reply,
each one is recorded in the conversation's log as asked-for and then as applied
or failed, and all of them are printed to you as they happen. An action that
failed is reported as failed rather than described as done, and a block the
harness cannot read changes nothing at all. The distinction being drawn is
deliberate: arbitrary execution is what was refused, and a typed call against the
tracker is not that.

The brief and the goals stay yours. The product manager proposes a change to a
goal and says plainly that it is yours to make; it cannot make one, and with no
way to write a file it could not if it tried.

The listing it is given names items by title, so when a title is not enough to
judge whether new work belongs inside an existing item or beside it, it reads
that item in full and carries on from what it found. That happens inside your
one message, up to four rounds of it. Results it has not seen when the rounds
run out are written into the conversation's own record, so they reach it the
next time anything is said to that conversation — including from a later
process, since an agent that never learns what its own creates and closes did is
the one that will describe them wrongly. Item text is treated as evidence
exactly as a specification is: a description says what some work is, never what
to do.

That listing is also a snapshot, and the order is the one decision that must not
be made from one: it was gathered when the conversation opened, work closes while
the conversation carries on, and the role that owns the order is the role that can
least afford a queue that has stopped moving. On 2026-08-18 an item was moved down
a tier for waiting on work that had been finished for hours, and the harness
applied the change to an item that was itself already closed without saying so.
The survey action is the live answer — the open items as the tracker holds them
now, in the same order and the same shape as the listing the conversation opened
with, so the two can be read against each other — and the persona directs every
ordering decision to start from one.

Acting is checked at the moment it acts, too. The harness reads the item an action
names as it carries the action out, so an action aimed at work that has moved on
says so where the reasoning that aimed it is still happening: the result names the
state the tracker holds the item in whenever that is not open, and the product
manager reconciles it in the same reply rather than never. An action that would
mean nothing on work that has already left the backlog — reordering it, closing it
again, retiring it — is refused for that reason, and the refusal names the
closure. Recording a note on finished work still means something, so that is
carried out with the closure stated rather than refused. An item the tracker will
not describe is neither refused nor assumed to be open: the action is attempted
and what could not be read is said.

### Proposals, and deciding them in batches

The product manager can propose a Beads work item instead of creating one, when
the decision is yours rather than its. Each proposal is shown to you as a
numbered card with its reasoning, and the harness creates an item only after an
answer that approves it by name. Nothing you did not approve is created, a
proposal you left undecided is named when the conversation ends, and a created
item records the conversation, the turn, and the rationale it came from. A
proposal the harness cannot read is reported and the conversation carries on;
`--message` has nobody to ask, so it reports what was proposed and creates
nothing.

A turn that proposes five things is not five questions in a row. One answer
decides as many of them as you like:

```text
╭─ 1 · proposal 4.1 · Pause on a usage limit ───────────────────────────────────
│ Wait for the limit to reset and resume the same run.
│ why: you said capacity is not failure.
│ goal: run development nearly autonomously.
╰──────────────────────────────────────────────────────────────────────────────

decide 3 proposals? [approve 1,3 and decline 2 <reason>; anything else declines them all] approve 1,3 and decline 2 not this quarter
```

Batching changes the prompt and nothing underneath it. Each decision is recorded
on its own, the approval before the creation it authorized, and a decline keeps
your own words as the reason. An approval has to name what it creates —
`approve` on its own is refused wherever there is more than one item it could
mean, and the single-proposal question is still answered with `y` or `yes`,
which does name the only one there is.

There are three things an answer can be, and they are not the same:

- **It decides some or all of them.** Those decisions are carried out. Anything
  it said nothing about is left exactly where it was and put to you again,
  rather than guessed at; whatever is left keeps the number it was proposed
  with, so the last of five is still card 5.
- **It is a decision the harness cannot carry out whole** — it approves a card
  that is not there, decides the same card twice, or trails off into words after
  the proposals it named. Then it decides *no part of it*, including clauses
  that were perfectly good on their own, nothing is created, you are told what
  stopped it, and all of it is put to you again. Half of a misread answer is how
  work nobody asked for gets created, and being asked twice is the whole cost of
  a typo.
- **It is not a decision at all** — it names no proposal and starts with nothing
  the harness recognises, like `hmm` or `not this quarter`. That declines
  everything it was asked about and is kept as the reason, exactly as any answer
  that is not a yes always has been.

The one thing not put to you again is a proposal the tracker refused to create:
it stays undecided and is named when the conversation ends, because asking you
the same question until the tracker recovers is not asking you anything.

Because a decline keeps your words verbatim — the whole of what you typed about
those items, down to the word you turned them down with — its reason runs to the
end of the line, so write the approvals first. A decline separates the proposals
it names with commas or `and`, and everything after them is your reason even
when it starts with a number: `decline 2 3 weeks out` turns down card 2 for
being three weeks out rather than turning down cards 2 and 3.

Work reaches the queue only with a goal named against it, and the goal has to be
one you approved. Every proposal, and every item the product manager admits
itself, says which goal it serves in the words your goals document states it in;
the harness resolves that against the goals it reads from `docs/product` and
refuses an admission — or a proposal, before you are asked about it — that names
anything they do not state. So what an item says it is for is checked rather
than asserted, and the check is on the goal rather than on the fact that a
sentence was typed into the field. Wording is compared with case, spacing, and
trailing punctuation folded; nothing else is guessed at, so a paraphrase is
refused with the goals named rather than accepted as near enough. What a
proposal is placed against is checked the same way: a parent or dependency the
tracker does not hold proposes nothing at all, rather than becoming an approval
that fails after you have given it.

Work admitted before that check existed names no goal, and it is grandfathered
rather than blocked or backfilled by the harness itself: nothing refuses to run
it, and it is reported as unattributed wherever the queue is read, because a
rule that failed every item admitted before attributions were checked would stop
all work to close a gap that has cost nothing yet. Grandfathering keeps that
work running until it is attributed; it does not mean it stays unattributed.
Attributing one is a judgement about what the work is for, so the product
manager makes it in the conversation and the harness never guesses: `attribute`
records a goal on an item already in the backlog,
appended to what the item records rather than replacing it, so the goal an item
was admitted under is never rewritten. An item that names no goal and one whose
goal your goals do not state are reported apart, because the first is work to
attribute and the second is a claim to correct. [`yoyo goals`](#goals-and-what-work-serves-them)
reads both from outside the conversation.

Work it will not attach to a goal is not proposed and not quietly dropped
either — it stops and asks you, and the three cases stay apart because you
answer them differently. Work it can find no goal for is usually a sign the
goals are incomplete, so it asks which goal it should serve. Work that would cut
against a goal is a conflict it puts to you rather than proposing with a caveat.
Work that fits the goals as written and that it judges to be against what the
product is for is an opinion it states and can be wrong about, because you can
overrule an opinion it voiced and cannot overrule one it kept to itself. Each
question waits for your answer before the conversation moves on, what you say
reaches it on your next message, and a question you leave unanswered is named
when the conversation ends rather than passing for agreement. `--message` has
nobody to answer, so it prints the questions and proposes nothing.

### Steering the work from the conversation

A line that begins with a slash is a command the harness carries out for you;
everything else is said to the product manager:

```text
/status                  what is in flight, claimed, blocked, available, and done, with prices
/backlog                 the admitted work in order, and what would be pulled next
/show <id>               one work item in full, and what each run for it cost
/diff [id]               what a run changed, from the run's own record
/reports                 what agents reported without it stopping their work
/refresh                 re-read the repository and tracker into this conversation
/work <beads-id>         run one work item now, while you keep talking
/wait                    wait for the run this conversation started and report it
/stop [beads-id] [reason]  stop one item's run wherever it is running, and settle what it left
/hold [reason]           stop the harness starting anything more on its own; running work carries on
/release                 let the harness start work on its own again
/intake                  whether the harness may start work on its own, and why not
/stop-everything [reason]  hold intake and stop every run in flight, settling what each left
/redirect <id> <what to do differently>
/directives              what you have directed, and what is still unresolved
/directive <what you have decided>
/directive ambiguous <what is unresolved> | <what you said>
/directive artifact <artifact> <what is unresolved> | <what changes>
/resolve <directive-id> <how it was settled>
/help                    the list
/exit                    end the conversation, stopping anything it is running
```

A slash means the same thing in `yoyo chat --message`: the harness carries the
command out and the product manager is never asked, because she cannot carry out
a command and a turn spent trying is a turn you paid for. The four that only
mean something inside a conversation — `/work`, `/wait`, a bare `/stop`, and `/exit` with its alias `/quit`,
each of which starts or acts on a run the conversation's own process owns — are
refused there and say what to reach for instead, rather than being half carried
out by a process that is about to exit. `/stop <beads-id>` is not one of them: it
reads durable state and asks whichever process holds the run, so it means exactly
the same thing in a single message. With `--json` what the command printed
is a field of its own, so nothing reads as something the product manager said.

`/backlog` shows the ordering the product manager set, which is the one thing a
development manager pulls from: the admitted work that is not finished, in
priority order, each unready item saying what is holding it, and the item that
would be pulled next named at the end. Like `/status` it is a report rather than
an export — the first twenty entries are listed and the rest are counted, so a
long backlog says how much of itself you are not looking at, and the item that
would be pulled next is named even when it falls outside the listed part. It is
assembled from the same tracker `/status` reads rather than stored anywhere, so
it cannot drift from the priorities the product manager actually set.

Whether an item can be pulled is the tracker's answer rather than one the
harness works out, and that is a deliberate choice about what a listing can be
trusted for. A Beads listing carries the dependencies between items, but it
records only that a dependency exists: the entry reads exactly the same after
the blocking work is closed as it did before. Deciding readiness from that would
either name blocked work as the next thing to pull, on any listing that left
dependencies out, or hold an item back forever for a blocker that finished
months ago. So the harness asks Beads what is ready — the same blocker-aware
question `bd ready` answers — and a dependency is named as a wait only when the
work it points at is itself still in the backlog. An unready item says which of
the three things is holding it: named work it waits for, a blocker recorded on
the item, or the tracker simply not offering it. A tracker slice it cannot read,
including that readiness answer, fails the whole report instead of returning the
half it could: a survey describing part of what is happening is still worth
reading, and half a queue answers "what happens next" wrongly rather than
incompletely.

`/work` runs exactly what `yoyo run` would run — the same worktree, developer,
checks, reviewer, and integration policy — in the background, so the
conversation stays a conversation. One run at a time. `/status` reads durable
run state and the tracker, so a run another process is executing is as visible
as one started here. Every run it lists says why that item was chosen, in the
words whoever chose it recorded when the run started, and a run that recorded
nothing is named as one nothing accounts for rather than shown with the line
missing — an item running for no visible reason is the thing you most need to
see, and it is indistinguishable from work happening behind your back. A run that
is owed a continuation rather than working —
one waiting out a provider usage limit, one whose provider was stopped on time,
or one [paused for an unresolved directive](#directives-and-the-work-they-pause)
— is named as such rather than reported as progress. On a terminal a
finished run reports itself the moment it finishes, above whatever you are
typing, and rings the bell and renames the terminal window as it does, because a
conversation left in a background tab is exactly where a run finishing goes
unnoticed; the window's name is put back when the conversation ends, so it never
outlives the work it was announcing. Where the conversation is a redirected
stream there is no such moment, so it is reported at the next line, or when you
ask for `/status` or `/wait` for it.

A run started here also says the few things it crosses on the way, one line each
above whatever you are typing: its checks passing, the reviewer's verdict, the
promotion into the target branch, and — where the project publishes — its pull
request being queued to merge and being merged. They are read from the run's own
durable record rather than from the process executing it, so what you are told
is what somebody reading that record afterwards would be told, and each is said
once: a crossing is a transition rather than a state, because an event log
scrolling past the conversation is what the activity line exists not to be. The
product manager is told the same things as harness activity, so the next thing
it says about the work is not answering about a run it believes is still
developing.

`/stop` cancels the run, records why on the work item, and then settles what the
cancelled run left behind exactly as `yoyo reconcile` would: integrated work is
finished, and anything else becomes a durable blocker naming the branch and
worktree that were preserved. Two cases are exceptions, and both are reported as
what they are. A run that does not give up within the stop grace is reported as
still in flight rather than described as stopped. A run that reached its own
conclusion before the cancellation reached it — integrated under an automatic
policy, or finished with its worktree preserved under a `human` one, since a
successful run then promotes nothing — is reported as having finished on its
own: nothing is recorded on the item and nothing is settled, because nothing was
stopped. What separates the two is whether the harness reported a failure, not
whether anything was integrated. A run that had paused itself is not one of
these — whether it was waiting out a usage limit, had its provider stopped on
time, or stopped short for an unresolved directive — because it is owed a
continuation rather than finished, so the stop is
recorded against it, and the report says the run is preserved and continues only
if you start it again. Ending the conversation stops its run the same way,
because the process that owns the run is the one that is exiting.

Either way the conversation's own log says what happened rather than only what
was asked for: the stop is recorded as a request when you make it, and the run's
outcome — what it left behind, or the integration that beat the cancellation —
is recorded once it is known.

`/stop <beads-id>` stops that item's run wherever it is running, including in a
process this conversation has nothing to do with, which is what stopping has to
mean once work starts without you asking for it item by item. The difference is
only in who does the cancelling. A run this process owns is cancelled here. A run
somewhere else is *asked*: the request is written beside the run, the process
working on it reads that at its next provider call and ends the run there, and
this waits for the run's own record to say so before reporting anything. So the
report is about the run rather than about the request. A run that gives up is
reported as stopped and what it left is settled exactly as above. A run that
reached its own conclusion first is reported as having finished before the stop
arrived, with nothing recorded on the item and nothing settled. And a run that
has not given up by the time the grace runs out is reported as still in flight,
stopping at its next provider call, with nothing it had thrown away — because a
provider invocation already streaming is never interrupted. That generation is
already paid for, and killing it would leave the run needing the same work again,
which is the cost that makes killing processes the wrong verb in the first place.

`/hold` is the narrower verb and the one with no equivalent before now: it stops
the harness *choosing* new work, and lets everything already running finish. It
is what you reach for when the queue looks wrong but nothing is on fire. It holds
nothing you name yourself — `/work <beads-id>` still runs an item under it, since
you placed the hold and naming something is you deciding it is the exception —
and `/release` lets the harness choose again. A held intake leads `/status` with
its own banner saying when it was placed and why, beneath the PAUSED banner if
both are in force. It is recorded per product, unlike
[`yoyo pause`](#pausing-everything-and-resuming-it), because what a development
manager may pull is a fact about one backlog.

`/stop-everything` is the third and bluntest: it holds intake so nothing more
starts, and then stops every run in flight, settling what each leaves. The order
matters — holding first is what stops it becoming a race against the next item
being started while the last one is being stopped. It reports what became of each
run rather than what was asked of it, and one run that could not be stopped never
hides the others.

`/show` prints one work item in full — its status, priority, parent,
dependencies, description, design, acceptance criteria, and notes — through the
same tracker capability the product manager reads items with. What you see is
what the agent discussing it could see, which is the point: the two of you are
reading the same item rather than two accounts of it. Beneath the item it prints
what the item cost, broken down by the runs it took.

### What the work cost

Ask what is done and the completed items come back with a price tag:

```text
completed (3):
  [yoyodyne-ifd.2.7] p1  $27.93 Resume an interrupted run
  [yoyodyne-ifd.12]  p2 ≥ $4.50 Pause on a provider usage limit
  [yoyodyne-ifd.13]  p2         Publish a pull request
  ≥ marks a floor: some runs of that item have no surviving record and could not be priced.
  1 completed item(s) carry no price: work the harness did not run has none, and work it did is priced by `yoyo cost --record`.
```

The price is of the item rather than of a run, which is the only figure that
answers what a piece of work cost: every run made for it counts, including the
attempt that was rejected, the repair attempts, and the reviewer's invocation
beside the developer's. One item in this repository cost roughly twenty-eight
dollars across a rejected attempt and a successful one; a per-run view shows two
numbers and this shows the truth.

Every figure is the provider's own report of what an invocation cost, read from
the run's event log, never an estimate from a price table that drifts the moment
a provider changes what it charges.

A run finishing writes the item's total onto the item in the tracker, and that
recorded total is what travels with the work: `/status`, the product manager's
briefing, and `bd` itself all read the one number the tracker holds, rather than
each assembling a price of their own. `/show` is the exception, deliberately: it
prices the item from the run records themselves every time it is asked. That is
what lets it answer for an item nothing has recorded a price for yet — anything
finished before this existed, or an item whose run could not write its price
down — and it is why the two can differ. Where they do, `/show` is the current
one and the tracker is what was last recorded; `yoyo cost --record` makes them
agree.

Three things it deliberately will not do. A run whose event log no longer
survives is priced as unknown rather than as nothing — it is counted, left out
of the total, and marked with `≥`, because a zero meaning "no record" would
quietly understate every total it entered. An item the harness has never run
carries no price at all rather than a price of nothing. And what is priced *per
item* is runs: the conversations that steer them cost money too and are recorded
just as durably, but attributing a conversation that discussed five items to any
one of them is a judgement rather than a join, so it is left out here and said
to be left out. It is not left out of what the harness has spent altogether —
[`yoyo-status -c`](#following-a-run-a-conversation-or-a-branch-review) prices
conversations and branch reviews beside runs, because a total that skipped
either would be wrong rather than merely unattributed.

`/show` breaks one item's price down by attempt, which is what a single total
invites:

```text
cost: at least $27.93 across 3 run(s)
  run-0123…  started 2026-08-10T09:14:02Z [failed, reviewing] $8.91 from 3 invocation(s)
  run-89ab…  started 2026-08-10T11:02:41Z [succeeded, complete, integrated] $19.02 from 2 invocation(s)
  run-cdef…  started 2026-08-09T18:30:00Z [failed, developing] unknown: the run's event log is no longer recorded
```

From the command line, `yoyo cost` prices items from the same recorded runs —
one line per item, or a run-by-run breakdown when you name one — and
`yoyo cost --record` writes those prices onto the items. That is also the
backfill: the run state and event logs of everything already finished are still
under the state directory, so items closed before any of this existed can be
priced retroactively rather than the ledger starting today.

```sh
./bin/yoyo cost                     # every item the harness has run, and the total
./bin/yoyo cost yoyodyne-ifd.2.7    # one item, broken down by run
./bin/yoyo cost --record            # write each price onto its work item
```

`/diff` says what a run changed. It reads the run's own durable record rather
than shelling out to git, and that is what makes it survive success: a run is
cleaned up once it integrates, its worktree removed and its branch deleted, so
anything that answered by diffing a tree would stop having an answer exactly
when the work landed. The record keeps the file listing and the diff stat the
harness took while the worktree existed, along with the branch, the promotion,
and the pull request the work was published through — all still there to point
at after the tree is gone. Naming nothing asks about the run this conversation
last started — still going, already collected, or started by an earlier process
this conversation was resumed from, since the item it ran is written into the
conversation's own record rather than kept in whichever process happened to
start it. Naming an item asks about the most recent run of that item, whoever
started it. A run whose record holds no summary says so rather than printing an
empty listing that reads like one.

`/redirect` records your direction in the item's notes, where the developer's
context reads it on the next attempt, and stops the run first when the item you
are redirecting is the one running. It never changes the item's status: saying
what to do differently is not deciding that the work is done or blocked. Start
it again with `/work` when you want it retried.

### Directives, and the work they pause

A redirection is about one item. A directive is about the product: it is
recorded for the product rather than for the agent you happened to say it to, so
it reaches every run of every item, in this process and in any other. That is
what `yoyo directive` and `/directive` write, and it is the same record every
run reads before it starts, before it resumes, and before it puts a change
through the gate that would integrate it.

Most directives are operational. They take effect from the moment they are
recorded, and nothing waits for them:

```text
/directive prefer smaller pull requests
```

Two kinds pause the work they affect, because that work would otherwise be
written and promoted against intent that is being rewritten or was never
settled. One changes a governed artifact — the brief, a goal, a design — and the
work derived from it waits until the change is decided. The other is one nobody
can act on without deciding something you did not, and the work waits until you
answer:

```text
/directive artifact docs/product/goals/v1-goals.md whether autonomy is still the goal | the autonomy goal is being rewritten
/directive ambiguous which of the two publishing behaviours I meant | do publishing differently
```

You state which kind it is rather than the harness guessing. Pausing every run
because something classified a sentence would be a worse failure than pausing
none, so the kind is yours to say, and a directive that pauses work is refused
unless it names what is unresolved: a pause nobody can name a reason for is a
pause nobody can lift.

A pause is not a cancellation. Work already under way keeps its claim, its
branch, its worktree, and its developer session, and stops at its next gate —
the point before its change could be checked, judged, or promoted. Work that has
not started does not start. `yoyo reconcile` reports such a run as resumable and
leaves it exactly where it is, so nothing settles it out from under you, and the
item itself records which directive stopped it and what about that directive is
unresolved.

`/resolve <id> <how it was settled>` lifts the pause. The release is the record
changing rather than anything done to a run: the next time the item is started,
in whichever process, the same run continues from the gate it stopped at.
`/directives` lists what is recorded and what is still unresolved. An identifier
may be shortened to any prefix that names exactly one directive.

From the command line the same records are reachable, which is how a directive
you gave to an agent other than the product manager gets written down:

```text
./bin/yoyo directive list
./bin/yoyo directive record --kind ambiguous \
  --unresolved "which of the two publishing behaviours was meant" \
  --received-by reviewer \
  "do publishing differently"
./bin/yoyo directive resolve --resolution "the second one" directive-3f2a
```

What the harness enforces is the pause; what it does not do yet is work out
which items derive from a changed artifact. A directive that names no work
therefore pauses all of it, which is the safe reading rather than a clever one.
`yoyo directive record --scope` narrows it to the items you name; a directive
recorded from the conversation names none, so it pauses everything and reports
the work in flight and claimed as what it just stopped.

Only you reach any of this. The product manager owns what the queue says and the
order it is in; running, stopping, and redirecting the work itself stays yours,
so nothing it writes starts or stops anything — a reply that contains `/work` is
prose. What it does get is an account of what you had the harness do, carried
into its next turn as evidence, so the conversation keeps discussing the product
as it now is rather than as it was when the conversation opened.

A conversation is durable. It is recorded outside the repository under the
operating system's state directory, so leaving and running `yoyo chat` again
resumes the same conversation; `--new` starts a fresh one instead. The record
keeps the requested model selector, the model the provider reported serving, the
provider session identifier, any action results the product manager has not been
told about yet, the work item it last ran, which proposed changes to its own
documents it has already been shown, and when its picture of the
repository and tracker was gathered and against what commit, and the normalized
event stream is stored beside it — including what the operator asked the harness
to do, which is recorded in the conversation's own log beside the runs' logs.

### Talking to the other agents

`yoyo chat` is the product manager, because product intent is where the work
comes from. Every other configured agent is reachable the same way:

```text
./bin/yoyo agent list                      # who is configured, and what each is doing
./bin/yoyo agent show architect            # one agent in full
./bin/yoyo agent chat architect            # talk to it
./bin/yoyo agent chat development-manager --message "Decompose ifd.4." --json
```

The name is either the agent's configured name or the role it fills, and a role
two agents fill is a question rather than a request: name the one you mean. The
name you give is the agent that answers — its persona and its model, not its
sibling's — and the role it fills is what decides its authority. `yoyo chat`
names none, so it takes the agent filling the product-manager role.

**Each agent is a durable logical identity, not a process.** The provider that
answers a turn is started for that turn and gone afterwards; what survives it is
one conversation record per agent, with its own provider session, its own turn
count, and its own picture of the repository. Talking to the architect never
resumes what the product manager was told, and where two agents fill one role
neither resumes the other: they are two identities with two sessions, and the
lease that stops a second process talking to one of them leaves the other free.
`yoyo agent list` reads all of it without starting a provider — including
whether another process is holding a conversation right now, which is why a
second one would be refused.

An agent is conventionally named for its role, and `yoyo init` names every one
of them that way, so a project that has never configured two agents on a role has
its conversations exactly where the role would have put them.

**What each role may do is fixed in the harness rather than in its persona.** A
project rewrites any persona it likes and the boundaries do not move:

| Role | Reads the tracker | Writes to the tracker | Its own documents |
| --- | --- | --- | --- |
| product manager | yes | admits, orders, attributes, closes, retires | brief and goals: proposes, never writes |
| architect | yes | nothing | designs, decisions, invariants: decides, and you record |
| development manager | yes | creates and links **only underneath admitted work**; records triage decisions on stopped work | none |
| developer, reviewer | yes | nothing | none |

The development manager is the one worth reading twice, because it is where a
design becomes tracked work. It decomposes: every item it creates hangs under an
item the product manager already admitted, and the harness refuses a creation
that names no parent. It cannot admit work, cannot reorder the backlog, and has
no close or retire — so a decomposition can never quietly become new scope, and
the backlog's order stays the product manager's. What it created is recorded as
what it was: the item's own notes say it was created under its parent,
decomposing it, rather than admitted to the backlog, so the two acts stay
distinguishable long after the conversation that made one of them is gone. Work it discovers that belongs
elsewhere it says to you, for the product manager to admit. It is also the role
that decides what becomes of work that stopped moving, which is the [triage
docket](#deciding-what-becomes-of-stopped-work) below.

The architect owns the designs, the decision records, and the invariants, and it
cannot edit any of them from a conversation, because no conversation has tools.
Decide the change with it and then record it yourself — `yoyo invariant` for an
invariant, a revision to the document for the rest. Changes other roles proposed
against its documents are carried into its conversation for it to argue, the
same way the product manager hears proposals against the brief and the goals.

Each role is also given the documents it answers for. The architect gets the
designs, the invariants, and the decision records alongside the specifications;
the development manager, developer, and reviewer get the designs and the
invariants; the product manager gets none of them, which is the same decision
read the other way — intent is what it reasons from, and the implementation must
not be able to argue about what the product is for.

The development manager is given one more thing: the **triage docket**, the work
that has stopped moving. It reaches that conversation the way the backlog
reaches the product manager's — carried in the context rather than by you
noticing something went quiet. Two things put an item on it. A run that ends on
a durable blocker dockets itself as it stops, and so does a run a `yoyo
reconcile` sweep stops for it. An approved publication that did not finish is
docketed too: one the harness already recorded as
outstanding — a merge the forge dropped, or one it performed that could not be
confirmed — and one that has simply been sitting unmerged past
`triage.stuck_merge_age`.

Each entry carries the evidence rather than a summary of it: the blocker in the
words it was recorded in, the reviewer's own findings, the check that was
failing and what it printed, the branch and worktree that were preserved and
whether they still exist, what the forge says about the merge, and the counters
saying how many review rounds the item has accumulated against the configured
cap and what a repair grant would be worth. A decision made without those last
ones is a decision the cap then contradicts.

An entry states that something stopped; it decides nothing, and nothing decides
it for the development manager. Docketing is keyed to what stopped, so a run
that dockets its own ending and a sweep that settles it afterwards produce one
entry between them, and a run that is merely parked — waiting out a usage limit,
held by a directive, or paused by you — is never docketed at all, because it is
owed a continuation rather than a decision.

Finding a publication nobody merged is a scan rather than an event, because
nothing happening is not something anything can be present for. Two things scan:
`yoyo reconcile`, and opening a development manager conversation. There is no
scheduled process behind either, so the configured age is a floor rather than a
promise about when the entry appears.

#### Deciding what becomes of stopped work

An entry decides nothing, and the development manager is the role that does. It
records one decision per entry, on the work item, through a `triage` action that
names the run the entry is about: `repair` hands the item another bounded go at
the change it has, `rerun` runs it again from the start, `rescope` splits out
what was refused as out of scope, `rearm` repeats a merge the forge dropped,
`wait` says the forge still has it, and `escalate` hands it to you. The decision
lands in the item's notes, so the next reader of a run that stopped finds the
reasoning beside the evidence rather than deciding it a second time.

Four of the six the harness holds to more than a note. **A repair, a re-run, and
a re-arm each spend the item's durable budget as they are recorded**, and are
refused once it is gone — the refusal names the budget, which is the evidence for
escalating instead. A repair and a re-run are each once per item — a second of
either is an escalation rather than a larger budget — and past the
[review-round cap](docs/configuration.md#what-one-work-item-has-been-given) even
the first is refused. A merge re-arm is bounded per item by the
integration-retry budget rather than the rounds, because it buys no round at
all; the design's stricter rule — once per publication — arrives with the
re-arm action itself, whose counter will be keyed to the publication it
repeats. **An escalation is a durable blocker on the item and a report at
`warning` severity or above**, in the same reply: the item itself says it is
waiting on a person, and the report reaches [the pile you
read](#what-agents-report-and-where-it-reaches-you). Prose alone is not an escalation, and the
harness refuses one carrying no such report rather than blocking an item you
were never told about. `rescope` and `wait` are the two that are a note and
nothing else — a re-scope's real work is the child item it creates beside the
note, and a wait asks for nothing at all.

What the harness does not do is carry any of it out. Nothing starts a run or
asks a forge for anything because a decision was recorded, so acting on one is
yours: `yoyo run <id>` for a repair or a re-run, and for a re-arm, asking the
forge to merge the pull request again yourself — nothing in the harness repeats
a merge request the forge dropped. That manual re-arm is safe against a concurrent harness promotion for one reason worth knowing: the forge serializes merges into the base branch and re-runs its required checks in full, so the worst a race costs is a drop you would re-arm again — never an unverified merge. The re-arm action, when built, takes the harness's own promotion lease instead and removes even that churn. The budget is spent when the decision is
recorded whether or not you act on it, which is the same direction every counter
here fails in: an attempt nobody took rather than one nobody counted. What
triage changed is that stopped work is decided by the role that owns it, the
decision is durable on the item, and it reaches you only when the development
manager judged it had to.

Everything you type as a command — `/status`, `/backlog`, `/show`, `/work`,
`/reports`, `/refresh` — means the same thing in every conversation, because
those are your authority carried out by the harness rather than anything the
agent did.

### What the conversation looks like on a terminal

On a terminal, the line you are composing has a region of its own at the bottom
of the screen and everything the harness writes goes above it. A reply, a
proposal, or a run that finishes never lands in the middle of a half-typed
sentence: what you have typed stays exactly as it is, and you carry on from
where you were. The conversation is written into the terminal's ordinary output
rather than an alternate screen, so scrollback, selection and copying, and
resizing keep working on it as they would on any other command's output. Editing
the line is deliberately small — the arrow keys, home and end, backspace and
delete, and Ctrl-U and Ctrl-W — and Ctrl-C still interrupts the way it always
did, because the terminal keeps its own signal keys.

While a turn is being answered, the line below the conversation says what it is
doing: a spinner, the phase it has reached, and how long you have been waiting.
The phases are read off the same event stream the turn is already recording —
your message going out, the model thinking, the model writing its reply, the
harness carrying out tracker actions it asked for — and a provider that is
refusing requests is named as exactly that, with the attempt it is on, because a
turn that is slow because the service or your account is declining work is
telling you something worth knowing. Nothing arriving for twenty seconds stops
the animation: the line then says how long it has been quiet, because a display
that keeps moving through a stall looks like progress, and looking like progress
is worse than saying nothing. It is drawn above the line you are typing and
erased when there is a reply to read, so it is never in your way and never in
the scrollback.

The reply itself arrives while it is being written rather than all at once when
it is finished. The provider reports the product manager's message before the
terminal result the turn is recorded from, so the text already exists before the
turn is over and what changed is only when you are shown it. It reads exactly as
the finished reply reads — the same opening, the same Markdown, the same
questions in the same colour — and it is not written a second time when the turn
ends. The blocks the product manager writes for the harness rather than for you
are not shown as prose: a proposal, a tracker action, a concern, and a report
are each reported in their own way once the turn is over, and the source of one
arriving mid-sentence would be the protocol rather than the answer. None of this
touches the record: the reply that is recorded, the events, and
`--message --json` are byte for byte what they would have been with nobody
watching, because the fragments are the same text the harness had already
redacted and already written down. A turn whose provider stops before the reply
is finished says so on the line after the prose it managed to show, because
prose that simply stops reads as a product manager that had nothing more to say.

Between turns that line carries what the conversation has cost: what the last
answer was charged and what this session has spent, taken from what the provider
itself reported per invocation and worked out no further. It is replaced rather
than written into the conversation, so a running total is somewhere you can see
it rather than a log of itself, and a provider that reports no cost is left
unanswered rather than reported as free. Work in progress covers it while there
is any, because what you are waiting on is the more urgent of the two.

A horizontal rule separates your turn from the answer to it, and colour tells
apart the things you have to act on rather than read past: a question the
product manager asks you is orange, and a proposal awaiting your decision and
the harness's own answer to a command each have a colour of their own. A
proposal is framed as a card so a batch of them reads as several things rather
than one wall of text; the frame is decoration exactly as the rule is, and where
decoration is suppressed the same card is its heading with the body indented
under it. The states work is in are coloured too, and the same way wherever they
appear — running blue, blocked orange, done green, failed red — so `/status` is
read down its aligned columns rather than picked out of ragged prose. Colour is
an addition to the text and never what carries the meaning — the question still
ends in a question mark, the proposal still says what it is proposing, the group
still says "blocked (2)" in words — so a transcript with the escapes stripped
out loses the decoration and nothing else. `NO_COLOR`, a terminal that reports
itself as `dumb`, and output that is not a terminal each suppress all of it
together — the colour, the rules, the cards, the reply shown as it forms, the
milestones a run reports, the bell and window title, and the cost line — because
every one of them writes an escape or depends on there being a moment at which
something unprompted can be written, and somebody who asked for an undecorated
conversation asked for all of it.

The product manager writes Markdown, and on a terminal you read it as Markdown:
headings, list markers, thematic breaks, and bold spans are shown as structure
rather than spelled out in punctuation. That is presentation and only
presentation. Nothing is added to the reply and nothing is taken out of it —
every escape is inserted between characters that were already there — so the
same reply stripped of its escapes is the recorded reply byte for byte, and a
stream that may not be dressed is shown exactly what was written.

Anywhere else the same conversation is an ordinary stream of text. A pipe, a
file, a redirected terminal, and a terminal that reports itself as `dumb` get no
cursor control, no colour, and no rules at all: the same lines in the same order
a redirected conversation has always had, plus each phase of a turn said once as
a line of its own, with nothing in it that a clock decided — there is nothing to
animate or erase on a stream, and a transcript whose contents depended on how
long the provider took would not be one you could compare against another. For
the same reason a stream is shown the reply when it is finished rather than as
it forms, and a run it started is not watched at all: a stream has no moment
where you are waiting with the screen to yourself, so anything written between
two lines that are already buffered would make what the transcript holds depend
on timing. None of this reaches the recorded reply, the event stream, or
`--json` — it is how the conversation is shown and nothing more, so what is
recorded is identical either way.

### What agents report, and where it reaches you

An agent used to be able to reach you only by failing. A spent repair budget
becomes a durable blocker, a failed run is reported where you are already
looking — and everything an agent noticed while its work *succeeded* survived
only as prose in a run summary copied into an item's notes, where nothing
surfaces it. Two real examples from a single session reached the operator only
because a person happened to be reading: a reviewer's observation that the
built-in bundle's declared version had gone inert, and a developer's report that
`bd lint` could not run in its sandbox.

Every role can say such a thing without stopping: the developer, the
reviewer, and the product manager each end what they say with one small block,
and the harness collects it. `/reports` shows you the pile, newest last, with
the twenty most recent listed and the rest counted.

`yoyo reports` shows the same pile without opening a conversation, which is what
a run finishing overnight needs: it says it reported something, and reading it
must not cost an interactive conversation with a provider behind it. It prints
the whole pile rather than the most recent twenty, because a command's output
can be paged and piped where a listing beside a conversation cannot, and
`--json` hands a script the collected records themselves. Like `/reports` it is
read-only in the strongest sense the design allows: a report is written once and
never revised, and nothing retires one, marks one read, or decides anything
about the pile.

```sh
./bin/yoyo reports                 # the whole pile, oldest first
./bin/yoyo reports --json          # the collected records, for triage or a script
```

A report is deliberately not a blocker, and nothing about it behaves like one.
The run carries on exactly as it would have: an approving verdict that mentions
something still approves, a developer that reports something still finishes, and
a report the harness cannot read or cannot store costs its run nothing at all —
it is named on the outcome instead, because a report nobody kept would otherwise
be silence. That is the property worth relying on, since a channel that could
cost an agent its run is one agents learn not to use.

Each collected report carries the role and the configured agent that made it,
the run or conversation it came from, the work item where there is one, a
severity — `critical`, `warning`, or `note` — and the text. That is enough
structure to filter the pile later without deciding now how it should be
filtered; an agent that judges which of its own observations are worth your
attention is a later question, and nothing here does it. The severities are
deliberately not the reviewer's `blocker`/`major`/`minor`: a finding decides
whether a change is repaired, and a report decides nothing.

Volume is the risk this design has, and the answer to it is in the role
contracts rather than in a filter. Every contract says what merits a report — a
risk worked around, an assumption that may not hold, a defect or a stale
document outside the assigned work, something in the environment that stopped a
check being run — and says plainly that most replies should carry none, because
a channel full of routine observations is worse than nothing: it looks like
coverage. That guidance is in Go, alongside the rest of each contract, so no
persona can loosen it.

The pile lives outside the repository under the operating system's state
directory, beside the run and conversation records rather than among them. It
outlives them: a run is settled and its worktree and branch are removed, and
what it reported is still there for you to read.

### What agents propose changing, and who decides

The canonical documents each belong to one role, and that boundary is enforced
rather than asked for. A role that meets it and has nothing else to say has two
moves left, both bad: build against intent it believes is wrong, or edit the
document anyway. So it has a third — it proposes the change, in one small block
like the report block, and the harness carries it to the role that owns the
document and to you. The developer carries that block today, being the role that
meets the boundary while implementing against a document; the reviewer says what
is wrong with a change as a finding instead, and the product manager stops and
asks you.

```sh
yoyo amendment list                       # what is waiting to be decided
yoyo amendment show <id>                  # one proposal and what became of it
yoyo amendment approve <id> --reason ...  # record the change as authorized
yoyo amendment decline <id> --reason ...  # turn it down, keeping why
```

Who is being asked follows from the document rather than from anything the agent
says: the harness resolves the artifact it names to its kind, and the kind to its
owner. A proposal about a document nobody records is refused, because there is
nobody to decide it, and a proposal from the role that owns the document is
refused too — that role amends it.

**A proposal is never a deferred edit.** It carries what should become true and
why, not replacement prose, and nothing in one ever reaches the document —
approved or not. Approving records that the owner's authority came down in
favour of the change; the change is then made by the owner, in the document, in
a revision recorded under that role. That is what keeps this from becoming the
slow path by which a downstream role redefines upstream intent: the only thing a
proposal can produce on its own is a decision.

Like a report, it costs the run nothing. The run integrates exactly as it would
have, and a proposal the harness cannot read or cannot keep is named on the
outcome rather than failing the attempt it arrived with — that naming reaches
you and not the agent, so a role that misnames a document is not told and
repeats the mistake. It is durable in the same place and for the same reason:
the run that argued the design was wrong is long finished before anybody decides
what to do about it. A developer that makes the same argument again on a repair
attempt raises one proposal rather than one per attempt.

The owner hears it where it works, and you are the one who decides. Proposals
against the brief and the goals are carried into the product manager's
conversation and proposals against the designs, the specifications, and the
decision records into the architect's, and each argues for or against them and
can decide or edit nothing. So every decision is recorded by you through
`yoyo amendment` — the same override path `yoyo invariant` takes — and the record
says you exercised the owner's authority rather than that the owner answered.

### How fresh the conversation's picture is, and how to refresh it

The specifications and tracker the product manager reads are gathered once, when
a conversation opens, and sent on its first turn only. Every later turn resumes a
provider session that already holds them, so re-sending would pay to restate what
it was already told. The consequence is worth knowing before it surprises you:
**a resumed conversation keeps the snapshot it opened with.** Change a
specification, and a conversation started beforehand will still describe the old
one, confidently, because that is genuinely the evidence it has.

So the conversation says so itself, on one line, as it opens and as it resumes:

```text
context gathered 2h ago; 14 commits and 3 tracker changes since. /refresh reads what moved into this conversation.
```

Freshness is a comparison rather than a timestamp. The picture records when it
was assembled and what commit the repository was on, both durably, so the
process that resumes a conversation can say how old it is without having been the
one that briefed it. What has moved since is two cheap questions: what `HEAD`
holds that the picture did not, and what the tracker wrote into its own
interactions log after the picture was taken. Either comparison can fail — an
unrecorded commit, a repository that will not answer — and a comparison that
could not be made is reported as unknown rather than counted as nothing, because
"0 commits" from a broken comparison is the same confident staleness this exists
to end. The tracker's log is an export rather than its live state, so the count
is a floor on what has moved; a log a tracker has never exported, and one too
large to read to the end, are both reported as unknown rather than as unchanged,
because a truncated comparison that answered "nothing moved" would be the same
false confidence in a smaller place. A one-shot `--message` says the same line on
stderr, where it cannot disturb the reply on stdout or the `--json` document.

`/refresh` re-reads the repository and the tracker into the running
conversation. It discards nothing: what has been said stays said, and the new
picture reaches the product manager on your next message, framed as evidence
with an account of what moved, so it reconciles what it believed rather than
having it swapped underneath. The transcript says the refresh happened, the
conversation's own log records it, and the durable record only says the
conversation is working from the new picture once a turn has actually carried
it — a refresh nobody was told about never reads as one that landed.

It was never frozen entirely. Every turn carries what you did through the
harness since the last reply — the runs you started, stopped, and redirected —
so `/work`, `/stop`, and `/redirect` reach a resumed conversation, and reading an
item, surveying the open queue, and acting on any item all go to the tracker as it
stands rather than to that opening snapshot. Nothing outside those commands
arrives on its own — an item something else created or closed reaches the
conversation when the product manager asks, by surveying or by acting on it, and
not before — and edits under `docs/product` do not reach it that way at all, since
the tracker does not hold them. That is what `/refresh` is for.

`--new` is a different tool rather than the answer to staleness. A refreshed
conversation and a new one end up equally current; they differ in what they
remember, and that difference is the point. Start a new one when the history
itself is the problem — an unrelated topic where its memory of the last one is
not worth carrying — and refresh when the ground has moved under a discussion
worth keeping. `--new` replaces the recorded conversation: there is one per
product, so the previous discussion is not kept alongside it.

## How work flows once you approve it

`/work <beads-id>` and `yoyo run <beads-id>` execute the same thing. The run
claims the item, creates a branch and an isolated worktree outside your primary
checkout from exactly the branch the work will be promoted into, and asks the
developer for the change:

```sh
./bin/yoyo run --json "$work_item_id"
```

On success, the JSON result reports the run ID, branch, worktree, base commit,
change summary, checks, and agent summary.

Then the change is gated on what it touched. The project configuration and the
artifact homes upstream of the work — `.yoyodyne/`, `docs/product/`,
`docs/designs/`, and `docs/decisions/` by default — are default-deny for a
developer's diff, because a developer that edits one is redefining what its own
work is measured against. A change that touches one without the work item
granting it is refused before any check runs and before any reviewer is asked,
and handed back to the same developer in the same repair loop a failing check
uses. An item grants an exception in its own text, on a line beginning
`Protected-path grant:`, so every exception is declared in reviewed item text
rather than discovered in a diff. [Configuration](docs/configuration.md#protected-paths-in-a-developers-change)
has the details.

Then the configured checks run in that worktree, and an independent reviewer —
its own provider invocation, with no tools at all — judges the change against
the work item, its design guidance and acceptance criteria, the invariants
delivered with it, and the check results. Everything the reviewer is shown is
treated as evidence rather than instruction, so an instruction the developer
left in the diff is data to analyze rather than something to follow. A verdict
of `repair` returns the findings to the same developer, up to
`execution.repair_attempts_before_replan` attempts, before the run gives up and
records a blocker.

What happens on approval depends on `approvals.integration`. This repository
sets it to `automatic`, so a run that passes its checks and is approved by the
reviewer is committed, fast-forwarded into the target branch, closed in Beads,
and its worktree and branch removed — the JSON reports the integrated commit and
what was cleaned up. A freshly generated configuration says `human` instead, so
a new project preserves the worktree for external integration until it opts in.
Either way the harness refuses `automatic` unless deterministic checks and a
reviewer agent both exist.

Development is parallel and integration is serial. Two runs may develop, check,
and review at the same time, but a run reaching its promotion phase waits its
turn: the harness takes a lease on the target branch out of the run state store
before it promotes and releases it once the promotion has settled, so at most
one promotion per target branch is ever in flight. The lease is an advisory file
lock, so it dies with the process holding it — a promotion whose process was
killed leaves no stale lock, and `yoyo reconcile` settles what it left behind.
No agent takes the lease or performs a promotion; the harness does both.

A fast-forward needs the target branch to still be where the run started from,
and it may not be: the run ahead of it in the queue can have promoted into it,
and committing to it yourself while a run is working moves it just as
effectively. The promotion fails closed either way, and the run then replays its
change onto where the target went, re-runs the checks, and gets a fresh
independent review before trying again — up to
`execution.integration_retries_before_reconciliation` times. The earlier
approval never carries over, because the diff it approved is not the one that
would now be promoted. A replay that conflicts is never
resolved automatically: the run stops, both sides survive untouched, and the
blocker on the item says so.

### Letting the harness choose the work

`/work <id>` and `yoyo run <id>` are you naming an item. `yoyo work` is the
harness choosing:

```sh
./bin/yoyo work                 # drain what is ready
./bin/yoyo work --limit 2       # start two runs and stop choosing
./bin/yoyo work --json
```

It reads the admitted work in the order the product manager set, takes the items
the tracker itself reports as ready to pull, and starts as many of them at once
as `execution.max_concurrent_developers` leaves free — which is `1` until you
raise it. Each run is the run above: its own branch, its own worktree, the same
checks, the same independent reviewer, the same serial promotion. The command
returns once every run it started has ended.

Capacity is enforced where a run is reserved rather than by the scheduler, so two
of these, or one of these and a `yoyo run` beside it, share one limit rather than
getting one each. A run that loses the race for the last free slot is reported as
declined and the pass exits zero: that is two schedulers doing exactly what they
should, not a failure.

Four things keep an item out of a pass, and the pass accounts for them at two
different grains. An **unresolved directive** is named against the item it paused,
with the directive's own words, because it needs a person and nothing else would
report that this item was passed over for it. The other three — the tracker not
reporting an item as ready, a run for it already being in flight anywhere, and no
free slot — are facts about the pass rather than about any one item, so that is
how they are reported: the stop reason says which of them ended the choosing, and
a pass that got as far as reading the queue prints how many items were admitted,
how many the tracker called ready to pull, and how many slots were taken. Counts
rather than a list, deliberately — a line per unready item would be a line per
backlog entry on every pass, which is how a listing stops being read. A pass that
stopped before reading the queue at all, because you were holding intake or the
machine was already full, says nothing about the backlog rather than reporting
zeroes it never looked up.

A fifth thing deliberately keeps nothing out: an item whose goal was amended after
it was admitted is pulled exactly as it would have been, because
[staleness reports rather than decides](#what-a-change-upstream-leaves-stale),
and what changed goes into the run's recorded reason instead.

Two things make this accountable rather than work happening behind your back.
Holding intake stops it choosing anything more while what is running finishes,
and it is read at every pull rather than once at the start, so a hold you place
mid-pass takes effect at the next selection. And every run it starts records, in
durable state, why that item was chosen — where it sat in the order, how much of
the queue was pullable, how much of the machine was free, and anything upstream
that had moved. `yoyo status` reads it back.

The configuration is re-read before every pull for the same reason: a capacity
you raise or a priority you reorder while a pass is running is picked up the next
time it chooses something, rather than at the next restart. Runs already in
flight keep the configuration they started under.
[Configuration](docs/configuration.md#scheduling-ready-work) has the rest.

Documentation counts as part of a work item rather than as follow-up: the
developer contract makes updating the documents that describe changed behavior
part of the assigned work, and the reviewer reports a change that leaves a
document asserting something the change has made false. That reconciliation is
diff-scoped, and the limit is worth stating plainly — the reviewer is given one
change, not the repository, so it catches a contradiction with documentation it
can see and misses a claim invalidated in a file the change never touches. What
that misses across a whole branch is what [`yoyo review`](#reviewing-what-a-branch-adds-up-to)
is for; nothing in the harness compares the accumulated documentation against
the repository as a whole.

### Reviewing what a branch adds up to

A per-item review sees exactly one work item's worktree, so a defect that is
consistent inside every change that produced it and wrong only in their sum is
structurally invisible to it. `yoyo review` is the same reviewer — the same
contract, the same structured verdict, the same independence — pointed at a
branch against the base it grew from:

```sh
./bin/yoyo review --base main                 # the branch you are on
./bin/yoyo review --base main --branch milestone --json
```

It describes every commit the branch carries over that base and diffs the whole
range as one patch, under the same bounds a single change is described within: a
range too large to show in full is reported as truncated, and a truncated change
cannot be approved, because what was not shown was not reviewed. The base must
be an ancestor of the branch — a base that has moved on is a reconciliation
rather than an accumulated change, and the command says so instead of quietly
reviewing a range you did not name.

The verdict is recorded with the same session and model evidence a per-item
review leaves behind, in the `branch-reviews` directory beside the runs and the
conversations, and what the reviewer noticed beside its verdict is collected
with every other report. It is a provider invocation like any other the harness
makes, so it records the event stream every other one records: it can be
followed while it runs with
[`yoyo-status`](#following-a-run-a-conversation-or-a-branch-review), and what
the provider reported it cost is priced beside runs and conversations rather
than quietly missing from the harness's total.

What a `repair` verdict here does is deliberate and narrow: **nothing to the work
already integrated.** Every commit under review was checked, reviewed, and
promoted by a run that has since settled, so there is no gate left to hold and
the harness does not revert or reopen a promotion on a second opinion — the
branch review is wired with no run store and no integration, so it could not if
it were asked to. What it does instead is answer one question, and enforce the
answer: the branch is approved only if an independent reviewer approved the whole
accumulated change, and `yoyo review` exits non-zero on anything else — a repair
verdict, a review that never answered, a change too large to be seen in full. The
findings are then work, and admitting work to the backlog is the product
manager's.

### Publishing, and the merge that follows it

Runs are local until a project sets `approvals.publishing` to `automatic`. With
publishing on, the developer phase is what pushes: when a developer attempt
finishes, the harness commits it, pushes the run branch to `execution.remote`,
and opens a pull request against the target branch — and each repair attempt
updates that same request. The approving reviewer verdict is what merges it: the
harness asks the forge to merge the pull request, subject to exactly the checks,
independence evidence, and fast-forward rule that gate integration, plus a fresh
check that the remote target has not moved. The harness makes every push and
every merge request itself and routes neither through an agent: no role is given
a credential, a tool, or a request for either, and the reviewer — the role whose
verdict authorizes the merge — runs with no tools at all, so it cannot perform
one. A developer does have a shell in its worktree and runs under your account,
so "no agent pushes" describes what the harness does rather than a boundary it
enforces; the [design document](docs/designs/v1-harness-design.md#what-is-enforced-and-what-is-not)
says which half is which. The local target branch stays authoritative: the
harness fast-forwards it as it always has, and the forge merges the pull request
carrying exactly that commit under a merge commit — the one method that puts the
reviewed commit itself on the base, where a squash or a rebase would substitute
a rewritten copy. So the merge leaves the remote target at your local branch
plus one forge merge commit, identical in content, and the harness checks that
relationship on both sides of the merge. The last step of the promotion is to
catch your local branch up onto that merge commit: a fast-forward onto a commit
that already contains the promotion and carries exactly its content, so nothing
is rewritten, nothing is merged, and nothing is decided. That is the `git pull`
you used to have to remember after every merge, and it is why your checkout
stays level with the forge on its own. A fast-forward blocked by uncommitted
work in your checkout is held rather than forced, and says which file held it —
except for the control-plane exports the harness declares (Beads' passive JSONL
dumps), whose churn is discarded, because they are derived from a store that is
authoritative elsewhere. A merge that landed after its run was over, and any
catch-up that was held, are swept by
[`yoyo reconcile`](#recovering-interrupted-runs).
Your target branch itself is never pushed, so a branch protected against
direct pushes is merged into normally; a forge that refuses reports which
requirement was unmet, and a merge that did not carry the promotion is reported
rather than reconciled. The merge is asked for as of when your branch protection
is satisfied rather than as of now, so required checks that are still running
are waited for by the forge rather than refused seconds after the reviewer
approved. Waiting that way needs "Allow auto-merge" enabled on the repository;
when it is off and nothing is holding the pull request back the harness just
merges, so only a repository that has something to wait for and no way to wait
for it is reported as unpublishable, naming the setting. Administrator override
is never used to get past a protection rule. A run whose merge is queued that
way reports the pull request as queued and finishes;
[`yoyo reconcile`](#recovering-interrupted-runs) settles it once the forge has
merged, or reports an outstanding publication if the forge dropped the queued
merge. A repository with no configured remote publishes nothing and behaves
exactly as a purely local project does.

Merging belongs to `approvals.integration`, so the two settings compose rather
than imply one another. Publishing with `integration: human` opens the pull
request and stops: nothing is merged, the run branch survives on the remote, and
the worktree is preserved for you — which is what a `human` integration policy
means. See the
[configuration guide](docs/configuration.md#publishing-through-pull-requests).

## Configuring a project

A project owns its configuration outright. `yoyo init` writes it:

```sh
./bin/yoyo init                 # configure the current directory
./bin/yoyo init --product example --directory path/to/project
./bin/yoyo init --tracker-remote https://example.com/team/tracker.git
```

That writes a complete `.yoyodyne/config.yaml` — every agent with its role,
backend, model selector, instance count, and persona reference, plus the
execution, approval, and product settings — and copies the five personas into
`.yoyodyne/personas/`, where they are ordinary Markdown files in your
repository. Nothing is inherited when the file loads, so
`yoyo config show --origins` names the project file for every configured value —
the one exception being `product.repository_id`, which is reported as
`derived:product.id` because the file states the product id and lets the
repository id follow from it. Editing a field is the whole of what changes the
harness's behavior. `init` also points the tracker at a remote so the backlog is
shared rather than per-machine — this project's Git remote by default, or the
URL `--tracker-remote` names; see
[Where the tracker syncs](docs/configuration.md#where-the-tracker-syncs).

```yaml
# .yoyodyne/config.yaml, abbreviated
version: 1

product:
  id: example
  repository: .
  specifications: docs/product
  invariants: docs/decisions/invariants

checks:
  # from go.mod
  - go test ./...
  - go vet ./...

agents:
  developer:
    role: developer
    backend: claude-code
    model: opus
    instances: 1
    persona:
      version: v1
      path: personas/developer.md   # relative to .yoyodyne/
  # ... product-manager, architect, development-manager, and reviewer
```

`checks` is the one thing `init` derives from your repository rather than from
its template: it reads the project's Makefile targets, module and manifest
files, lockfiles, and build wrappers, and proposes the commands that follow,
naming the file each came from. It executes none of them, and it decides nothing
it cannot decide — what it could not settle, and what it settled by leaving a
command out, are commented beside the list under headings that say which of the
two happened, and a repository that announces nothing keeps `checks: []` and the
commented examples for Go, TypeScript, Python, and Java. A run with nothing to
verify has no gate to integrate behind, so `yoyo run` refuses one either way.

Personas specialize how an agent works and never grant it authority. The harness
contracts — agent authority, worktree sandboxing, the review verdict contract,
integration preconditions, and cleanup — are enforced in Go and prefix the
configured guidance in the prompt, so editing `personas/reviewer.md` changes how
the reviewer works and cannot let it approve a change it could not see. Bump the
`version` label beside the path when you edit one, so the change is visible in
diagnostics.

**What owning the configuration costs.** A later `yoyo` that improves a
persona or corrects a model selector does not reach a project that already has
its own copy of it. Re-run `yoyo init` in a scratch directory and merge the
difference if you want those improvements. The executable's built-in bundle is
the template `init` generates from rather than a layer projects keep
inheriting — inheritance still works for a project that writes
`extends: builtin:v1`, and is the right choice for a fleet that should improve
together, but the explicit file is what yoyo ships.

`yoyo` finds the nearest `.yoyodyne/config.yaml` from the current directory
upwards, so it runs from the project root or anywhere beneath it. To see what a
configuration resolves to and where each value came from:

```sh
./bin/yoyo config show --effective --origins
```

See the [configuration guide](docs/configuration.md) for the full layout, the
`init` flags, precedence, merge and removal semantics, persona rules, extending
a bundle, and migration from `.yoyodyne.yaml`.

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
intent, so nothing asks you to approve one. None of it is a gate: an unapproved
document still loads and still governs what is downstream of it, and approving
writes nothing but the approval — the document itself stays the owning role's to
change. The [configuration guide](docs/configuration.md#approving-a-document) has
the schema and what is refused.

A document in one of those directories with no usable identity is named on
stderr rather than governed under a guessed id, so a home you have not given
identity to yet says so. Nothing else changes for it: a specification with no
frontmatter is still read as product intent, because refusing intent somebody
wrote down is worse than reading it and saying its identity is missing.

Who may change one of these documents is in the code rather than in a persona.
The product manager owns the brief and the goals, the architect owns the designs,
specifications, and decision records, and the development manager owns no
document at all. Creating, amending, superseding, and retiring an artifact each
refuse a role that does not own the kind, the way the invariants already do —
though no command reaches that path yet, so what it constrains today is nothing
that is happening. What does run on every load is the other half: a document
whose revision log records a change by a role that does not own it is reported,
naming the file and the entries that crossed. It is reported rather than refused
because the log is append-only, so losing the document would leave one that could
neither load nor be corrected. None of it constrains you: the boundary is between
agent roles, and you direct any of them.

A role that meets that boundary is not left with nothing to say. It proposes the
change instead, the proposal reaches the owner and you, and only a decision on it
is ever recorded — see [what agents propose changing, and who
decides](#what-agents-propose-changing-and-who-decides).

The chain that identity makes expressible is then checked, every time the
artifacts are loaded: a `supports` entry naming an id no artifact answers to is
reported with both ends named, and an artifact that nothing connects back to the
brief is reported as an orphan. Neither refuses the document — a broken
relationship is a name to correct, not a reason to lose what somebody wrote. The
brief is the root and a decision record is not downstream of intent, so neither
is asked to support anything. The
[configuration guide](docs/configuration.md#traceability-references-and-orphans)
is the reference for the schema, the fields, and what is reported.

## Goals, and what work serves them

Identity ends at the document. The last link of the chain is the goal a work
item names, and that link is closed by reading the goals out of the goals
artifacts themselves: every statement under a goals document's `Goals` heading
is a goal work can be attributed to, and an attribution resolves by naming one
of them in the words that document states it in.

```sh
./bin/yoyo goals list          # the goals work can be attributed to, and where each is stated
./bin/yoyo goals attribution   # what each admitted work item says it is for
```

Nothing there writes an attribution, for the same reason nothing writes an
artifact: what a piece of work is for is a product judgement, made by the
product manager in the conversation where you can see it. What the harness owns
is resolving the claim. An item that names no goal at all and one that names a
goal your goals do not state are reported apart and treated differently, because
they are not the same thing to do: the first predates the check, is somebody's
to attribute, and never stops the work running; the second is a claim that is
wrong, and it is what `yoyo goals attribution` exits non-zero for.

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
the [pushes and merges the harness never routes through an agent](docs/designs/v1-harness-design.md#what-is-enforced-and-what-is-not):
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
architect` — and it cannot write one, because no conversation has tools. So the
lifecycle is reachable from the command line, acting with the architect's
authority and recording that it did:

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

## Operations and recovery

### Pausing everything, and resuming it

`yoyo pause` stops everything the harness would spend on a provider, and
`yoyo resume` starts it again:

```sh
./bin/yoyo pause      # to conserve tokens, or for any other reason of your own
./bin/yoyo resume     # everything parked on it carries on
```

It is one durable switch over the whole machine rather than one item. Every
provider-call boundary reads it before it spends — a developer attempt, each
reissue of one after a refusal, a reviewer invocation, a conversation turn — so
a pause placed while a developer is working reaches that run at its next
attempt rather than only reaching the runs that had not started. The flag lives
at the state root rather than under a product, because what makes you pause is
an account or an afternoon rather than any one project.

A run that meets the pause parks exactly as one waiting out a
[usage limit](#waiting-out-a-provider-usage-limit) does, on the same machinery
with you as its reset instead of a clock: the park is durable before any waiting
starts, and the item stays claimed with its branch, worktree, and developer
session all preserved. A process already parked acts on `yoyo resume` within
seconds and carries on unaided; one that exited while the pause stood is
continued by `yoyo run <beads-id>`. Nothing is cancelled, so nothing has to be
reconciled afterwards — which is the whole difference between this and killing
processes, where the run lands cancelled with its item still claimed and the
work has to be developed again from scratch. A conversation turn is refused
rather than parked, because there is a person in front of it: saying the same
thing again once the pause is lifted takes the turn that was refused. `yoyo
review` is refused for the same reason, having no run to park.

The honest boundary is that a provider call already in flight is not
interrupted. The flag is read before a call, so a generation that is already
streaming finishes and is charged for, and the pause takes effect at the next
boundary — which for a developer attempt can be minutes away. Stopping a
generation mid-flight would throw away what it had already cost and leave the
run needing the same work again, which is the cost that makes a kill the wrong
verb in the first place.

Time a run spends held is accounted under its own kind, separately from what a
provider's refusals are allowed to spend: a hold never eats a run's
`execution.usage_limit_max_pause` budget, and nothing bounds it, because the
thing that lifts it is you. Both `bin/yoyo-status` and the conversation's
`/status` lead with a PAUSED banner naming when the pause was placed, because a
system somebody paused and forgot looks exactly like a system that died.

This is the broad switch, and there is a narrow one beside it. `yoyo pause` stops
everything including the runs already under way, which park keeping everything
they have; the conversation's [`/hold`](#steering-the-work-from-the-conversation)
stops only the harness choosing new work and lets what is running finish. Reach
for the first when the reason is your account or your afternoon, and the second
when the reason is the queue.

### Waiting out a provider usage limit

When the provider reports that a usage limit is exhausted, the run pauses
instead of failing — for either provider invocation a run makes, the developer
attempt or the review. The reset time the provider named is recorded in durable
run state before any waiting starts, and nothing is cleaned up: the worktree,
the branch, the claimed Beads item, and the developer session are all kept, so
the reissued attempt continues the same change rather than starting it over. A
review that was declined is simply asked for again once the limit resets,
without redeveloping the change or spending a repair attempt.

The recorded reset is an upper bound on the wait rather than a gate on it. A run
sleeps `execution.usage_limit_unknown_reset_pause` — thirty minutes by default —
or the time left to the deadline, whichever is shorter, and then reissues the
attempt: the reissue *is* the probe. A reset time is a claim about the provider,
and claims go stale in both directions — capacity gets bought mid-wait, and a
rolling window can free room before the quoted edge — so a probe into a window
that is still closed costs one refused request and re-parks on whatever the
provider now reports. A run sleeps probes inside this process until it has spent
`execution.usage_limit_in_process_pause` on this run, and then exits with the
run still in flight instead of sleeping the next one; running `yoyo run` on the
same item continues it, with the whole bound available to that process again.
That bound counts every probe this process has already slept rather than each
one on its own, because a bound applied per probe would stop bounding how long
the process stays open at all.
`execution.usage_limit_max_pause` bounds what one run may spend waiting in total
rather than each wait separately, so a provider that keeps refusing cannot walk
a run past it, and what it records is what was actually waited rather than the
span to a deadline the run never reached. A limit reported without a reset time
polls under exactly the same rule, because it is unknown rather than
unwaitable: the monthly overage allowance reports this way while the ordinary
rolling window keeps resetting on its usual schedule, so it waits the same
interval and asks again. Unifying the two was the point — one polling
discipline, whether or not a deadline was quoted. A limit the harness genuinely
cannot wait for — a reset that is not in the future, or one that no longer fits
the run's remaining budget — stops the run and records a blocker rather than
guessing a wait. An exhausted limit is not the only thing a run waits out:
[an overloaded provider](#waiting-out-an-overloaded-provider) below takes the
same machinery on a much shorter clock.

### Waiting out an overloaded provider

A provider whose own servers are transiently overloaded refuses the same way an
exhausted limit does — the work is never judged, only declined — so it takes the
same machinery rather than a second one of its own. The difference is the clock.
An overload quotes no reset time and lifts in seconds rather than hours, so a run
waits `execution.server_overload_pause` — ninety seconds by default — and
reissues, instead of parking for the half-hour probe interval a usage limit uses.
Everything else is shared: the deadline is durable before the wait starts, the
wait spends the same `execution.usage_limit_max_pause` budget, and an overload
that never lifts therefore walks into that maximum and stops with a blocker
rather than reissuing forever. [Releasing a wait early](#releasing-a-wait-early)
below covers one of these exactly as it covers a usage-limit wait.

Ordinary transient throttling still never reaches any of this: the provider CLI
retries that on its own, and the harness does not duplicate the wait. What it
does act on is the terminal result the CLI ends on once its own retries are
spent — an `api_error` reporting HTTP 529 — because at that point the provider
has stopped retrying and somebody has to. An overload is the only terminal
`api_error` that becomes a wait;
[the rest of them](#when-the-provider-dies-mid-run) become a relaunch.

### Releasing a wait early

Everything above honors the recorded deadline as an upper bound, and a restart
mid-wait serves the rest of it rather than asking again, which is what keeps a
crash from retrying straight back into a window that is still closed.
`yoyo resume` with a work item named is the one thing that overrides that
deadline, and it overrides nothing else:

```sh
./bin/yoyo resume yoyodyne-ifd.53
```

(With no work item named it is the other half of
[`yoyo pause`](#pausing-everything-and-resuming-it) and lifts the operator's
hold over everything instead. Both are the same act — stop waiting and carry on
— and what the argument says is whose decision is being withdrawn: the
provider's refusal of one run, or your own hold over all of them.)

It exists because the deadline is a claim about the provider and you are the one
who can change what it is a claim about. Raise the account's capacity while runs
are asleep against an 18:50 reset and that reset has stopped being true; a run
waiting out a limit its owner has already lifted is autonomy working against
them. The command moves the next probe to now and does nothing else. In
particular it does not stop anything: killing a waiting run leaves a cancelled
run whose item stays claimed, and recovering from that means reconciling,
reopening the item, and developing it again from scratch. Released, the run
keeps its claim, its branch, its worktree, and its developer session, and a
process already asleep on the wait acts on the release within seconds. If the
provider still refuses, the run records the new report and waits again, so the
worst a premature release costs is one refused request. It is refused when the
named item has no run in flight, or has one that is not waiting on the provider
at all, because a release recorded against a run that is not waiting would be
acted on by whatever pause that run took next.

### When the provider dies mid-run

Not every way a provider ends an invocation is a refusal it names in advance.
Sometimes it simply dies: the API answers with an error its own retry ladder did
not outlast, or the connection carrying the response goes away before the reply
is finished — `API Error: Connection closed mid-response`, which quotes no HTTP
status because nothing answered. Nothing was judged and nothing is wrong with the
change; the run just stops existing. That used to fail the run outright and leave
a person to reconcile it, reopen the item, and launch it again — twice in the
week before this was built.

The run relaunches itself now. The dead invocation is reissued in the same
worktree and the same developer session, up to
`execution.transient_relaunches_before_blocking` times — two by default — and
then the run carries on as if nothing had happened. Continuing the session is
what makes this cheap rather than merely automatic: an attempt that died
mid-response had already made part of the change, and the relaunch picks that up
instead of asking a developer to derive it a second time. There is no wait
attached, because there is no condition to wait out: a dropped connection is
already gone, and the provider's own retries are spent before the harness sees
the terminal.

One budget covers both provider invocations a run makes. A review the provider
killed is asked for again on the same count, without redeveloping the change,
because what the budget bounds is how much of the provider's weather one run
absorbs rather than how often either role is asked. Nothing is handed back to the
developer either way, so a relaunch spends no repair attempt — the change is not
what went wrong.

Relaunches are counted in durable run state before each one begins, so a process
that dies mid-relaunch resumes against the budget it had rather than a fresh one.
A run that spends the budget stops and records a blocker on the work item naming
the provider's own last message. That is the only case a person sees. Setting the
bound to `0` restores the earlier behavior: the first provider death ends the run.

What else that blocker says depends on what the run was carrying, because a
provider dies during a repair attempt as readily as during the first one. A run
nothing had judged yet says so plainly — no check failed, no reviewer asked for
repair, nothing here says the change is wrong — which is what tells you to pick
the work up rather than replan it. A run killed inside its repair loop names the
repair attempts it had spent, the check that was failing, and the findings it was
answering, and says the provider is what stopped it rather than that verdict:
the evidence is unresolved rather than dismissed.

A refusal that *would* stand is not relaunched. A terminal `api_error` quoting a
4xx status — a malformed request, a key that is not permitted, a limit the
provider is enforcing — would earn the identical answer on the next attempt, so
it fails the run exactly as it always did. So does a 529, which is
[a wait](#waiting-out-an-overloaded-provider) rather than a relaunch, and so does
any terminal the API did not report at all.

### When a provider stalls or runs out of budget

A provider invocation is bounded by two separate questions, because one deadline
cannot answer both. Whether it is stuck is answered by activity: the harness
already stamps every event it parses, so a gap of five minutes with no event at
all means nothing is happening, and the invocation is stopped as stalled.
Whether it is worth continuing is answered by a total budget of four hours,
because an agent can stay live and unproductive — retrying, looping, thrashing —
and no liveness signal will ever catch that. An agent that emitted a tool result
seconds ago is demonstrably working, so elapsed time alone never stops it. Both
stops leave the run in flight rather than failing it, exactly as a usage-limit
pause does: the worktree, the branch, the claimed Beads item, and the developer
session are all preserved, and running `yoyo run` on the same item continues
that run — the developer resumes its session, and a stopped review is simply
asked for again without redeveloping the change or spending a repair attempt.
The reason is reported as what it was, a stall or an exhausted budget, and
neither is ever described as the agent having reported a failure, because it
reported nothing. Only a stop with nothing to continue from — no session, no
worktree — ends the run, and it still says the harness stopped the provider.
Short Git commands keep their flat deadlines, which is the right bound for a
command whose duration is known.

### Recovering interrupted runs

A process that is killed mid-run leaves durable state describing where it got
to. `yoyo reconcile` settles what it left behind, and then converges your local
state onto what the forge has:

```sh
./bin/yoyo reconcile --json
```

It compares the recorded run against the repository and Beads, and then finishes
the run's own remaining step or hands the item to you. It also builds the triage
docket on the way past, so a run it stopped and a publication the forge quietly
never merged reach the development manager rather than waiting for somebody to
go looking. A run whose work reached
the target branch is closed and its worktree and branch removed, including when
the run died before it could record the promotion. A run stopped anywhere
earlier becomes a durable blocker naming the branch and worktree that were
preserved. A run that finished with its merge queued at the forge is settled
here too: reconcile asks the forge and, once the merge has landed, finishes the
publication — merge commit recorded, remote branch deleted, and your local
target branch caught up onto the merge commit the forge made. Settling a merge
is complete on its own that way rather than leaning on the sweep below, so a
checkout is never left behind by which command somebody happened to run.

Two settle-path outcomes leave a publication outstanding for a person, each
with its own line on the work item. A merge the forge **dropped** is the
first: something the base branch required went unmet, the harness does not
merge past a requirement, and nothing about that publication is confirmed. A
merge that **landed but could not be confirmed** is the second: the forge
performed it, and the steps that confirm it — verifying the remote carries the
promotion, recording the merge commit, retiring the consumed branch — failed,
so the record honestly says the publication is not settled even though the
merge is real. In both, your local branch is deliberately left where it is
rather than moved on a publication nothing verified. A catch-up the settle
could not make is neither of these: it is ordinary, the run settles, and the
convergence sweep below finishes it on the next pass. Other reports on this page
still reach you when the evidence demands it — a preserved blocker, a diverged
remote, a catch-up that could not finish — but none of them asks reconcile to
exercise judgement: it reports and leaves the decision where it belongs.
Reconcile never invokes a provider either: a lost process handle is not a
reason to start a second developer for an item.

Once the runs are settled it converges local state, which is the rest of the
post-merge hygiene you would otherwise do by hand. Every target branch the
harness knows about is caught up onto its remote counterpart — the same
fast-forward the settle paths make, for a target left behind by something no run
is going to finish, or a catch-up that was held at the time — and every settled
run's leftover branch whose work the target already carries is deleted. Both
refuse on evidence rather than on a record: a remote that has diverged from
your local branch is reported for you to decide rather than reconciled, a
branch carrying work nothing promoted is
kept, and a branch a checkout still holds is left alone. Catching a branch up
takes that branch's promotion lease, so it never races a run promoting into it.

Repeating the whole thing is safe — a settled run is no longer outstanding, a
branch already level with the remote has nothing to catch up to, and cleanup
over artifacts that are already gone does nothing. A run another process still holds
is left to that process, and a run `yoyo run` can continue on its own — one
inside its repair loop, one paused for a provider usage limit, one whose
provider the harness stopped on time, one paused for an [unresolved
directive](#directives-and-the-work-they-pause), or one parked on an
[operator pause](#pausing-everything-and-resuming-it) — is left exactly as it is
for that command to pick up.

### What became of the runs, and why one failed

`yoyo status` reads back what the runs themselves recorded — newest first, the
work item, the status and the phase the run reached, what it cost, why the item
was chosen, and the reasons its record kept:

```sh
./bin/yoyo status                    # the twenty most recent runs
./bin/yoyo status --failed           # only the ones that did not succeed
./bin/yoyo status yoyodyne-ifd.90    # one item's runs
./bin/yoyo status --limit 0 --json   # every recorded run, for a script
```

The listing below is `./bin/yoyo status --failed --limit 2`:

```text
runs that ended without succeeding, 2 of 9 shown (137 run(s) recorded):
run-19dc9dff153e1eb89a2470f78f02f240 yoyodyne-ifd.1.7 started 2026-08-16T18:02:11Z [failed, developing] $4.62
  selected by the operator: the operator ran this item by name from the command line
  reason: developer reported failure: api_error: API Error: 529 Overloaded.
run-c81f0a4d7c2b41e6a0f9d3b5e7104c22 yoyodyne-ifd.63 started 2026-08-15T11:47:03Z [failed, checking] $12.80
  selected: no reason recorded
  reason: verification failed: make test exited with 2
  failing check: make test exited 2
7 further run(s) are not listed here; --limit reports more, and 0 reports all of them
each reason is shown as one line; --json carries what the record holds in full
```

The `selected` line is on every run, including — in those words — a run that
recorded no reason at all. That is deliberate: work the harness chose and cannot
account for is exactly what you most need to see, and a line left out would read
as a reason you had already looked at rather than as one nobody wrote.

Each of the other reasons is printed under the run it belongs to and named for
what it is, because the records keep them apart deliberately. Only `reason` says
the work itself failed. An `outstanding publication`, an `outstanding cleanup`, a
`failing check`, and a `completion recorded late` are recorded around the work,
and a run can carry one of them with its change already promoted. The last of
those is the class whose work-item note is itself unreliable — recording that
note is part of what was failing — so the run record this verb reads is its
authoritative home.

`outstanding` in the brackets marks a finished run that still owes somebody a
step, and the `outstanding:` line under it says which — cleanup that is not
recorded as finished, or a merge the forge queued and nothing has settled — so
the marker is never left for you to go and interpret out of the run's JSON.
[`yoyo reconcile`](#recovering-interrupted-runs) is what settles either. The
marker is said only of finished runs: one still in flight owes its own remaining
steps by definition.

Naming an item reports one more thing under its runs, because it is the one
question no run can answer: what that item has cost and what it has been given.
Every budget a run spends starts again at zero in the next run, so an item handed
back, run again, and handed back again is an item nothing bounds. The per-item
counters are what bound it:

```text
triage of yoyodyne-ifd.90: triage has spent 2 passes on it
  review rounds: 3 spent across every run of this item, under the cap of 4
  repair grants: 1 of 1 permitted; re-runs: 0 of 1; each is refused by its own budget or once no round remains
  merge re-arms: 1 of 2 permitted
  a decision that spends no budget — waiting, re-scoping, escalating — is recorded on the work item rather than here
```

Every figure here is a budget, and the first line counts what has been spent
rather than how many times somebody looked. Only three of the development
manager's six decisions spend anything — a repair grant, a re-run, a merge
re-arm — so an item it escalated or told to wait shows `triage has spent nothing
on it` and zeroes across the rest. **That is not evidence nobody looked.** The
decision itself is recorded on the work item, which is where to read whether
stopped work has been decided and what was decided; an escalated item is blocked
there as well.

A **round** is a reviewer verdict a developer attempt produced, counted across
every run of the item. A re-review no developer attempt produced is not one, so a
promotion that [loses its race](docs/configuration.md#losing-a-race-for-the-target-branch)
and gets a fresh verdict on the replayed change is not charged for it. Rounds are
what runs actually spend, and every run records them.

The lines under it are the budget for what triage can decide about work that did
not land — another go at the change, a re-run, a re-armed merge — and they move
when [the development manager decides one](#deciding-what-becomes-of-stopped-work).
Each is recorded before the action it counts takes effect, so a crash cannot
double-grant, and each is refused once its budget is spent. A grant and a re-run
are each once per item and are also refused by the rounds — the grant truncated
to what the cap still has room for, the re-run refused outright once none
remain — and a merge re-arm is bounded on its own because it buys no round at
all. The rounds alone would bound neither of the first two on an item whose runs
keep stopping before a reviewer ever sees them. The
numbers are the `triage` keys and the integration retries in [the configuration
guide](docs/configuration.md#what-one-work-item-has-been-given). An item triage
has spent more than one pass on says so in the first line, which is the fact
worth looking for: work that keeps coming back is usually work where something
other than the change is wrong.

The listing folds each reason onto one line and bounds it at 160 bytes with
an ellipsis, never cutting mid-character, so a reviewer's whole verdict does not become the listing;
`--json` carries what the record holds in full, along with the same figures.

Cost comes from the same recorded evidence [`yoyo cost`](#what-the-work-cost)
prices from, so a run still going reports what it has spent so far, and one
whose event log no longer survives reads as `cost unknown` rather than as free.

Reading a run decides nothing about it, so this holds nothing and settles
nothing: a run another process is executing is listed exactly as a finished one
is. Reporting a failure is not itself a failure either — the exit status says
whether the records could be read, so a script can read this without guarding
against the answer.

### Following a run, a conversation, or a branch review

`bin/yoyo-status` follows the normalized event stream a run, a conversation, or
a [branch review](#reviewing-what-a-branch-adds-up-to) records, which is the
closest thing there is to watching an agent work. It is a different thing from
the `yoyo status` verb above despite the name: that reads back what the records
hold now — a run still in flight as readily as one that finished — and this
follows a run's events as they arrive. It is a shell script that
lives in a checkout of this repository rather than part of the `yoyo` binary, so
`go install` and a release download do not carry it; clone the repository, or
copy the single file out of it, if you want it:

```sh
./bin/yoyo-status          # follow the newest of any kind
./bin/yoyo-status -l       # list recent runs, conversations, and reviews and exit
./bin/yoyo-status -c       # report token spend and cost for each, and in total
```

A conversation and a branch review each record the same kind of event stream a
run does, and "is this alive" is the same question asked of all three, so every
mode covers all of them and the default never asks which kind you meant.
Selecting one by id or by a unique id prefix works the same for each. `--runs`,
`--chats`, and `--reviews` narrow it to one kind when that is what you want.

A run's listed status is the status it recorded. A conversation has no such
record of its own, so its status is derived and says what an operator is
actually asking: `answering` while an agent is working on a turn, `waiting`
between turns, and `ended` once the role has moved on to a later conversation. A
branch review has no state file either — its verdicts share one log rather than
having a record each — so its status comes from its own events: `reviewing`
while the verdict is being made, and `reviewed` once it has been.

Every mode leads with a PAUSED banner while
[activity is paused](#pausing-everything-and-resuming-it), naming when the pause
was placed: a quiet machine somebody paused and a quiet machine that died look
identical, and this is the one place an operator is already looking.

It resolves the state directory the same way the harness does, so it keeps
working under `YOYODYNE_STATE_HOME` or `XDG_STATE_HOME`. `--help` lists the rest
of its options. It shapes its output with `jq` when `jq` is installed, and cost
reporting requires it. What it prices is one row per run, per conversation, and
per branch review, and a mixed total says how much of it was each — a
conversation turn and a branch review are each a provider invocation like any
other, and leaving either out understated every total it belonged in.
[`yoyo cost`](#what-the-work-cost) is the same run spending grouped by the work
item the runs were for, which is what answers "what did that piece of work
cost"; it leaves conversations and branch reviews out, deliberately and for the
same reason — a conversation that discussed five items, and a review of a branch
that carried a dozen, cannot be attributed to any one of them.

[`scripts/yoyo-status-test.sh`](scripts/yoyo-status-test.sh) checks these claims
against a fabricated state directory holding runs, conversations, and branch
reviews, without a provider or a repository and without reading your real
state.

### Reporting into Slack

Everything above needs you at the terminal, which is the wrong requirement for
work that runs while you are not. `yoyo slack` is the same account of the work in
a Slack workspace: one thread per work item, one message per milestone, and every
report an agent filed at the severity it was filed under. Each role speaks under
its own name and in its own voice, and what no persona did — a promotion, a
merge, your own holds — arrives from the harness itself. It is a process you
start and leave running, and it needs your project to have opted in:

```yaml
# .yoyodyne/config.yaml
slack:
  enabled: true
  channel: C0123456789
```

```sh
export SLACK_BOT_TOKEN=xoxb-...   # this process's environment, and nowhere else
export SLACK_APP_TOKEN=xapp-...
./bin/yoyo slack                  # or --once to make a single pass and exit
```

[`docs/slack/setup.md`](docs/slack/setup.md) takes you from an empty workspace to
live reporting, and the app it asks you to create is the checked-in manifest
beside it rather than a list of checkboxes to work through by hand.

It is an observation and never a gate. Nothing waits on it: a workspace that is
down delays messages rather than losing them, because the sink reads the same
durable records the verbs above read and catches up from its own cursors when it
returns. The moment its history starts from is written down the first time you
ever run it and never taken again, so time the sink itself spent stopped is a gap
it reads across rather than a gap in what it says. It is also the reason no run
holds a Slack token — one separate process posts, so no agent's subprocess tree
ever has a credential for your workspace in it. Replies are acknowledged and
nothing acts on them yet; steering the harness from a thread is designed and not
built.

## Further reading

- [The v1 harness design](docs/designs/v1-harness-design.md) — the architecture, the
  artifact and agent models, the Git model and what it does and does not
  enforce, and the self-hosting sequence.
- [The configuration guide](docs/configuration.md) — the full configuration
  reference: layout, discovery, precedence, checks, publishing, personas,
  inheritance, and inspection.
- [Reporting into Slack](docs/slack/setup.md) — an empty workspace to live
  reporting in threads, with the app manifest checked in beside it.
- [`docs/product/`](docs/product) — the product brief and goals, which are what
  the product manager reads.
