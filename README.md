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
like in a single answer — or hand that decision to your goals and watch the work
that serves them go into the queue by itself — and say `/work <id>` when you want
one of them run. The
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
  time. [`yoyo work`](docs/work.md#letting-the-harness-choose-the-work) is the harness
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
never left half-configured. See the
[configuration guide](docs/configuration.md) for what is in the file.

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
README and the documents it links, the configuration guide, and the help the
commands print. Not the source, and not the design document. A specification opens with an introduction
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
approve the work items it proposes, as many as you like in one answer. Once you
trust it, `approvals.work_items: automatic` hands that decision to your goals:
approve them with `yoyo artifact approve v1-goals`, and work that serves them
reaches the queue without asking you. You can also file one by hand if you would
rather have something to run immediately:

```sh
bd create --title="Add a subtract function" \
  --description="calc has add and nothing else. Add subtract(a, b) with a test." \
  --type=feature --priority=2
bd ready
```

When there is an item you want run, `/work <beads-id>` starts it in the
background and the conversation stays a conversation; `/status` says where it got
to, `/diff` says what it changed, and `/stop` ends it and settles what it left
behind. That is the whole loop, and [The conversation](docs/conversation.md) is
where the rest of it is described.

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

[How work flows once you approve it](docs/work.md#how-work-flows-once-you-approve-it) has
the full behavior, and the [configuration guide](docs/configuration.md#publishing-through-pull-requests)
has the table of what each combination produces.

## Further reading

**Driving the work**
- [The conversation](docs/conversation.md) — proposals and batches, steering,
  directives, the other agents, and what the conversation looks like on a terminal.
- [How work flows](docs/work.md) — what happens after you approve an item, letting
  the harness choose, reviewing a branch, and publishing.
- [What comes back to you](docs/reporting.md) — what the work cost, what agents
  report and propose, and reporting into Slack.

**Your written intent**
- [Artifacts, goals, and invariants](docs/artifacts.md) — artifact identity, the
  goal a work item names, what a change upstream leaves stale, and architectural
  invariants.

**When something goes wrong**
- [Operations and recovery](docs/operations.md) — pausing and resuming, provider
  limits and stalls, recovering interrupted runs, and following a run.

**Reference**
- [The configuration guide](docs/configuration.md) — the full configuration
  reference: layout, discovery, precedence, checks, publishing, personas,
  inheritance, and inspection.
- [The v1 harness design](docs/designs/v1-harness-design.md) — the architecture,
  the artifact and agent models, and the Git model.
- [Reporting into Slack](docs/slack/setup.md) — an empty workspace to live
  reporting in threads.
- [`docs/product/`](docs/product) — the brief and goals the product manager reads.
- [Working on yoyo itself](docs/developing-yoyo.md) — the checks, the build, and
  what a release is.
