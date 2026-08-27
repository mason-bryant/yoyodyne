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
`yoyo review`, `yoyo status`, `yoyo reconcile`, `yoyo pause`, `yoyo resume`, and
`yoyo release`
sit beside that conversation as administrative and recovery entry points — one
named item, one branch judged as a whole, what became of the runs already made
and why one of them failed, settling what a killed process left behind, stopping
everything the harness would spend until you say otherwise, releasing a run
waiting on a refusal the provider no longer makes, and letting the harness choose
work again after intake was held — rather than as the way
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
- **Claude Code is the default backend, and Codex is the optional alternative.**
  Claude Code serves every role. Codex serves the two roles inside a run — the
  developer and the reviewer — and is deliberately thinner: it prices nothing,
  enforces no schema on what an agent finally says, and holds a role to a
  sandbox rather than to a list of tools. Naming it for a management role is
  refused when the configuration loads.
- **One harness against the repository, and as many committers as you like.**
  The git layer already survives teammates contributing the ordinary way: run
  branches are namespaced, the forge is what merges, and a run whose target moved
  underneath it replays and is reviewed again. Two people each running their own
  `yoyo` against one repository is what is not supported yet — `init` shares the
  backlog, but claims, reports, directives, per-item budgets, and the lease that
  keeps two promotions off one branch each stay on the machine that made them.
  [Team mode scope](docs/team-mode-scope.md#what-v1-supports-meanwhile) has the
  boundary and what closing it needs.
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

**Or have step 2 walked for you.** `yoyo setup` is that step and everything
around it as questions — the tracker, the configuration, the checks read from
what your repository already declares, the tracker's sync remote, the index at
the door of each artifact home, and then the
optional offer of [reporting into Slack](docs/reporting.md#reporting-into-slack) — ending with
`yoyo doctor`, which is what decides whether the installation actually works:

```sh
cd path/to/your/project
yoyo setup
```

Everything it does is something you could have typed, and it asks before each
one, so read step 2 below if you would rather know what you are saying yes to.
It changes nothing that is already there — a configuration that does not load is
handed back rather than regenerated, a sync remote the tracker already holds
keeps pointing where it points, and a Slack token already stored is left alone —
and it keeps no record of its own, which is what makes running it again
safe: every step looks at your installation first, says what was already true,
and resumes an interrupted setup where it actually got to rather than where a
record claims. `--yes` answers every question setup asks with the answer it
proposes; the one prompt it cannot answer for you is the keychain's own, which
waits for each Slack token to be typed. `--json` on its own asks nothing and
*changes* nothing: it reports the same steps machine-readably, saying what is
already true and what would still have to be done, so reading the report is
never consent to alter the machine. `yoyo setup --yes --json` is what carries a
walk out with nobody at the terminal — it leaves the keychain step, and only
that step, to a walk somebody is watching.

**Or have your own agent walk it.**
[`skills/yoyo-setup/SKILL.md`](skills/yoyo-setup/SKILL.md) is a prompt rather
than a document to read: paste it into your own coding session, or install it
into `~/.claude/skills/` with the one command at the top of it, and ask for yoyo
to be set up here. It walks the same path — and repairs an installation that
used to work and stopped — acting on the structured findings `yoyo setup --json`
and `yoyo doctor --json` return, which is what keeps it running the commands
those reports carry rather than commands it invented. It asks before each one,
and hands you back anything that needs your editor, a login, or a credential.

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

**It also puts a `README.md` at the door of every artifact home** — the
specifications directory and the goals under it, the designs, the decision
records, and the invariants — saying three things about that directory: what is
filed there, which agent owns it, and whether you may edit one of those documents
by hand. The answers are the ones the harness already enforces rather than a
policy the file invents: a role that is not the owner proposes an amendment, your
own edit is reported rather than refused, and what a change leaves stale
downstream is what `yoyo stale` reports. An index that is already there is left
exactly as it is, `--force` included, because it is your prose rather than
something `init` generated. [`yoyo doctor`](docs/operations.md#checking-the-installation) reports
one that is missing or has stopped answering, and `yoyo setup` offers to write
it, which is how a project configured before these existed gets them.

**It also points the tracker at your Git remote**, so the backlog is shared
rather than one per machine. Beads moves its data over an ordinary Git remote
under refs of Dolt's own, so `init` reads `origin` and configures the tracker to
sync there — one repository, one permission model, nothing to stand up — and
prints what it configured. A shared backlog is not yet a shared harness: two
people each running their own `yoyo` against one repository is
[not supported](docs/team-mode-scope.md#what-v1-supports-meanwhile), and the
tracker syncing is one operator's backlog surviving their machine rather than a
team sharing one. A tracker that already has an `origin` remote is left
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
commit it along with the rest of your adoption — unless the repository is not
yours to add a tool directory to, which
[Keeping the configuration out of the repository](#keeping-the-configuration-out-of-the-repository)
covers.

**If you ignored it, both `init` and `config validate` say so.** A `.yoyodyne`
matched by an ignore rule is a project configured on this machine and nowhere
else: this checkout keeps working from disk while clones, collaborators, and dev
worktrees — which check out tracked files only — get an unconfigured project. The
warning names the rule and does not fail the command. If the repository is not
yours to commit tool config to, that is a real case rather than a mistake: keep
the configuration outside it with `yoyo init --external`, which yoyo finds
without `--config`, and exclude what is already there in `.git/info/exclude`
rather than in a tracked `.gitignore`. See
[When the repository ignores the configuration](docs/configuration.md#when-the-repository-ignores-the-configuration).

**Then check the whole installation, not only the file:**

```sh
yoyo doctor
```

`config validate` is about one document; this is about whether work can actually
run here — the binary on your `PATH`, Git, the tracker, the configuration, the
checks you just settled, the provider that executes every agent, and forge access
if you publish. Everything it finds wrong comes with the command that fixes it,
so a first run that would have failed halfway through is a problem you fix now
instead. [Checking the installation](docs/operations.md#checking-the-installation)
has the whole of what it looks at.

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
README, the documents it links to under [Further reading](#further-reading), the
configuration guide, and the help the commands print. Not the
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
says exactly that rather than inferring what your product must be about. The
`README.md` that `yoyo init` writes there does not change that answer: an index
says what would be filed in a directory and states no intent, so it is carried
under a heading of its own and never counted as a specification. Tell it
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
behind. That is the whole loop, and [the conversation](docs/conversation.md) is
its own document: proposals and batches, steering, directives, and the other
agents.

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

[How work flows once you approve it](docs/work.md) has
the full behavior, and the [configuration guide](docs/configuration.md#publishing-through-pull-requests)
has the table of what each combination produces.

## Configuring a project

A project owns its configuration outright. `yoyo init` writes it:

```sh
./bin/yoyo init                 # configure the current directory
./bin/yoyo init --product example --directory path/to/project
./bin/yoyo init --tracker-remote https://example.com/team/tracker.git
```

That writes a complete `.yoyodyne/config.yaml` — every agent with its role,
backend, model selector, provider account, instance count, and persona
reference, plus the execution, approval, and product settings — and copies the five personas into
`.yoyodyne/personas/`, where they are ordinary Markdown files in your
repository. Nothing is inherited when the file loads, so
`yoyo config show --origins` names the project file for every configured value —
the one exception being `product.repository_id`, which is reported as
`derived:product.id` because the file states the product id and lets the
repository id follow from it. Editing a field is the whole of what changes the
harness's behavior. `init` also points the tracker at a remote so the backlog is
shared rather than per-machine — this project's Git remote by default, or the
URL `--tracker-remote` names; see
[Where the tracker syncs](docs/configuration.md#where-the-tracker-syncs) — and
writes the `README.md` each artifact home gets, which says what is filed there,
who owns it, and whether you may edit one by hand.

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

accounts:
  default: {}                       # the provider account the agents run under

agents:
  developer:
    role: developer
    backend: claude-code
    model: opus
    account: default
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

### Keeping the configuration out of the repository

Committing `.yoyodyne/` is the default because it is what makes a project
describe itself: a colleague clones it and has the same agents, the same
personas, and the same checks. A contributor to a repository they do not own is
in the other situation — the configuration is theirs rather than the project's,
and a pull request that adds a tool directory nobody asked for is a pull request
about the tool. Two mechanisms already cover that, and neither needs anything
new.

**Keep it on disk and out of Git.** Configuration discovery reads the launch
checkout's filesystem and never consults the index, so a `.yoyodyne/` Git has
never heard of loads exactly like a committed one. List it in
`.git/info/exclude`, the per-clone ignore file that is itself never committed:

```sh
printf '.yoyodyne/\n' >> .git/info/exclude
yoyo init
yoyo doctor
```

Excluding it is load-bearing rather than tidiness. A run refuses to start while
the primary checkout holds anything uncommitted that the project did not
declare, untracked files included, and an untracked `.yoyodyne/` is exactly
that — so without the exclude line the first `yoyo run` names the six files
`init` wrote there and stops.

**The exclude line does not cover everything `init` writes.** It also puts a
`README.md` at the door of each of the five artifact homes — `docs/product`,
`docs/product/goals`, `docs/designs`, `docs/decisions`, and
`docs/decisions/invariants` — and in a repository you are a guest in those are
five more untracked paths, in somebody else's `docs/` tree rather than in a tool
directory of your own. They need the same decision `.yoyodyne/` just got, and it
is a decision rather than a default: commit them where an index saying what is
filed in each directory is worth having in the project, and where it is not,
exclude or delete them:

```sh
printf '%s\n' docs/product/README.md docs/product/goals/README.md \
  docs/designs/README.md docs/decisions/README.md \
  docs/decisions/invariants/README.md >> .git/info/exclude
```

Deleting them instead is safe, and it is noticed rather than silent: `yoyo
doctor` reports each home that has no index, as a warning rather than a problem,
so an installation without them runs work exactly as one with them does. The
tracker is worth the same treatment if you are keeping the whole adoption local:
`bd init` writes `.beads/` and a set of agent instruction files, and each of
those is another untracked path a run would refuse over.

**Or keep it outside the repository entirely.** `yoyo init --external` writes
the configuration into this machine's own home for one instead of into the
checkout, and writes nothing into the repository at all:

```sh
cd ~/src/theirproject
yoyo init --external      # writes ~/.config/yoyodyne/projects/<key>/, and nothing here
yoyo doctor
```

Run it from inside the repository: detection reads the project's Makefile
targets, manifests, and lockfiles to propose `checks`, and it needs the project
to read. Nothing is passed on later commands — the configuration is keyed by the
repository, so `yoyo` finds it from the repository root, from any directory
beneath it, and from a worktree Git added from it. `product.repository` is
written as the checkout's own path, and the artifact directories are unaffected:
`specifications`, `designs`, `decisions`, and `invariants` resolve against
`product.repository` and go on naming directories inside the repository being
worked on. The five indexes are not written either, so the paragraph above is a
decision this arrangement does not ask you to make.

`YOYODYNE_CONFIG` names one configuration outright, wherever it is, for a shell
that would rather say so explicitly; `--config` still names one for a single
command.

One thing is true of both, and it is the point rather than a cost: **the project
stops describing itself.** Another clone, another machine, and anybody else
working on it get no configuration at all, and `yoyo` there reports that it found
none. In this scenario that is the intent: the configuration belongs to you and
not to a repository you are a guest in.

See the [configuration guide](docs/configuration.md) for the full layout, the
`init` flags, precedence, merge and removal semantics, persona rules, extending
a bundle, and migration from `.yoyodyne.yaml`.

## Further reading

**Driving the work**

- [The conversation](docs/conversation.md) — proposals and batches, steering,
  directives, the other agents, and what the conversation looks like on a
  terminal.
- [How work flows](docs/work.md) — what happens after you approve an item,
  letting the harness choose, reviewing a branch, and publishing.
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

- [Setting up and repairing an installation with your own agent](skills/yoyo-setup/SKILL.md)
  — the shipped prompt that walks a blank or broken installation to a passing
  `yoyo doctor`, acting on what `yoyo setup --json` and `yoyo doctor --json`
  report.
- [The configuration guide](docs/configuration.md) — the full configuration
  reference: layout, discovery, precedence, checks, publishing, personas,
  inheritance, and inspection.
- [Provider plugins](docs/provider-plugins.md) — declaring a provider of your
  own: the six answers a provider has to give, the rule format for describing one
  in configuration, which compiled adapter runs it, and why a plugin never
  decides how long to wait.
- [The v1 harness design](docs/designs/v1-harness-design.md) — the architecture,
  the artifact and agent models, the Git model and what it does and does not
  enforce, and the self-hosting sequence.
- [Reporting into Slack](docs/slack/setup.md) — an empty workspace to live
  reporting in threads, with the app manifest checked in beside it.
- [Release notes](docs/releases/README.md) — one file per tag, what each section
  is for, and how a cut drafts one from the work that landed.
- [`docs/product/`](docs/product) — the product brief and goals, which are what
  the product manager reads.
- [Working on yoyo itself](docs/developing-yoyo.md) — the checks, the build, what
  a release is, and what a surface may do with emphasis.
