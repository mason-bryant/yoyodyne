---
name: yoyo-setup
description: Use when setting yoyo up in a repository for the first time, or when `yoyo doctor` says the installation cannot run work. Walks a blank or broken installation to a passing `yoyo doctor`, acting on the structured findings `yoyo setup --json` and `yoyo doctor --json` return rather than on their prose.
---

# Setting up and repairing a yoyo installation

*This document is a prompt, and everything below the rule is addressed to an
agent rather than to you.* Give it to your own coding session in your own
repository — paste it, point the session at this file, or install it as a skill
and then ask for yoyo to be set up:

```sh
mkdir -p ~/.claude/skills/yoyo-setup
curl -fsSL https://raw.githubusercontent.com/mason-bryant/yoyodyne/main/skills/yoyo-setup/SKILL.md \
  -o ~/.claude/skills/yoyo-setup/SKILL.md
```

It covers both directions of the same walk: a project with nothing yet, and an
installation that used to work and stopped. Part of
[yoyo's documentation](../../README.md#further-reading).

---

## What you are converging on

`yoyo doctor` is the one thing that decides whether work can run on this
machine, and reaching a `yoyo doctor` that passes is the whole of this task.
You are not diagnosing anything yourself: the two commands below carry every
finding together with the command that fixes it, so what you contribute is
sequencing, consent, and the commands that are yours to run.

Change nothing else about the operator's repository while you are here. Nothing
in this task requires writing code.

## Before you touch anything

1. **Be in the right directory.** Everything below runs from the operator's own
   project — a Git repository with at least one commit. Confirm which directory
   that is before the first command.
2. **Check the binary.** `yoyo version` prints a bare version. If the command is
   not found, offer one of the two installs and wait for an answer:
   `go install github.com/mason-bryant/yoyodyne/cmd/yoyo@latest` with Go 1.24 or
   newer, followed by `export PATH="$PATH:$(go env GOPATH)/bin"` in their shell
   profile; or a per-platform binary from
   <https://github.com/mason-bryant/yoyodyne/releases>.
3. **Say what you are about to change.** Everything up to and including
   `yoyo setup --json` is read-only against their repository: it asks nothing,
   waits for no input, and writes nothing there. Name the first command that is
   not, and get an answer before running it.

## The two reports you act on

Both print JSON to stdout, and **both exit 1 when something would stop work
running**. That exit status is the report rather than a failure of the command:
read the JSON either way, and do not retry a command because it exited 1. Exit 2
is the other thing entirely — the command could not do what it was asked, it
said why on stderr, and there is no report on stdout to read. Never key on the
human-readable output — the prose is written for a person and both commands have
a `--json` that carries the same findings structurally.

**`yoyo setup --json`** is the walk from a binary on `PATH` to a working
installation, as a report. On its own it asks nothing and changes nothing: it
says what is already true and what is still to be done. Adding `--yes` carries
the walk out. It carries:

| Field | What it is |
|---|---|
| `schema_version` | the version of this report's shape |
| `product` | the product id the configuration names, once there is one |
| `config` | the configuration file that was read, once there is one |
| `steps` | each step of the walk, in the order it was taken |
| `diagnosis` | `yoyo doctor` run at the end, unabridged, in the shape below |

Each entry in `steps` has a stable `step` name, a `status`, a `summary`, an
optional `detail`, and a `remedy`. The status is one of `already` (true before
setup touched anything), `done` (setup did it), `skipped` (declined, or never
offered), or `handed-off` (setup cannot do this itself). Every step that leaves
somebody something to do or to decide carries the `remedy` that does it.

**`yoyo doctor --json`** is the diagnosis on its own, and the same object
`diagnosis` above carries:

| Field | What it is |
|---|---|
| `schema_version` | the version of this report's shape |
| `product` | the product id the configuration names, once there is one |
| `config` | the configuration file that was read, once there is one |
| `status` | the worst of the findings: `ok`, `warning`, or `problem` |
| `findings` | every check that was made, in the order to fix them in |

Each entry in `findings` has a `check`, a `status`, a `summary`, an optional
`detail`, and a `remedy`. `check` is a stable name — `tracker`, `checks`,
`forge`, `provider:claude-code` — so key on it and on `status`, never on the
wording of a `summary`. **Every finding whose status is not `ok` carries a
`remedy`, and a remedy is a command.**

This document is written against `schema_version: 1`, which both reports carry.
If you read any other number, stop: this document and the installed `yoyo`
disagree about the shape of the report. Say so, and offer to read plain
`yoyo doctor` output together with the operator instead of acting on it.

## The loop

1. **`yoyo setup --json`**, from the project directory. Tell the operator what
   is already true, what setup would do, and what it says it cannot do itself.
2. **`yoyo setup --yes --json`**, once they agree. It carries out every step it
   can and stops short of the ones it cannot: a `handed-off` or `skipped` step
   afterwards is one whose `remedy` is now the thing to act on. Two of them are
   ordinary rather than a failure: Slack reporting is declined unless the
   operator names a channel, since an installation reports nothing and runs work
   exactly the same; and storing a Slack token is always left to a terminal they
   are watching, because the keychain asks for the token itself.
3. **`yoyo doctor --json`**. If `status` is `ok` you are finished. Otherwise take
   the findings **in the order they are given** — the tools, then the project,
   then what the project turns on, so the first problem is usually why the ones
   under it are problems too — and act on the first one whose `status` is
   `problem`: show its `summary`, its `detail`, and its `remedy`; run the remedy
   if it is yours to run; then run `yoyo doctor --json` again and repeat this
   step from the top of the new report.
4. **Stop** when no finding has `status: problem`; when a remedy leaves its
   finding unchanged twice, since running it a third time will not help; or when
   the next remedy is one only the operator can run. Say plainly which of the
   three ended the loop, and what is still outstanding.

**A warning is not a gate.** `status: warning` is something worth knowing about
an installation that already works — a `yoyo` on `PATH` that has drifted from
the one running, or reporting that nobody started. Name it with its remedy,
offer to act on it, and neither insist nor loop on it. The installation is
usable with warnings standing.

## What you may run, and what you hand back

**The `remedy` field is your only source of commands.** Never invent one, never
substitute one you think is equivalent, and never run a command against a
finding it did not come from. If a remedy is wrong for this machine, that is
worth telling the operator rather than working around.

Run yourself, after showing the command: an ordinary non-interactive remedy that
names a program and its arguments — `bd init`, `yoyo init --directory …`,
`git -C … init`, `mkdir -p …`, `go install …`.

Hand back to the operator, and wait for them:

- **A remedy with a placeholder in angle brackets** — `<url>`, `<path>`. Only
  they know the value, and guessing one configures the wrong thing quietly.
- **A remedy that opens their editor**, `${EDITOR:-vi} …`, except for the one
  case below.
- **A remedy that authenticates** — `claude auth login`, `gh auth login`,
  anything that ends in a login. These reach for a browser or a prompt on a
  terminal you are not.
- **A remedy that touches a credential store**, such as `security …` on macOS.
  **Never read, print, echo, or store a credential**, and never move one into a
  file or an environment variable to make a command run non-interactively.
  Nothing in these reports contains a secret, and nothing you do here should
  produce one.

A remedy may end in a comment naming where to run it — `bd init   # in
/Users/you/calc`. Run it in that directory; the comment is not part of the
command.

**The one editor remedy you can help with** is the `checks` finding, whose
remedy opens `.yoyodyne/config.yaml` because the project's deterministic checks
are missing or name a program this machine cannot run. Reading the repository
and proposing the checks it actually declares — its `Makefile` target, its test
runner, its linter — is worth more than pasting a path. Propose the edit, make
it only once the operator agrees to it, and then re-run `yoyo doctor --json`.

## One report, and what it asks of you

```json
{
  "schema_version": 1,
  "product": "calc",
  "config": "/Users/you/calc/.yoyodyne/config.yaml",
  "status": "problem",
  "findings": [
    {
      "check": "path",
      "status": "ok",
      "summary": "`yoyo` is on PATH",
      "detail": "/Users/you/go/bin/yoyo"
    },
    {
      "check": "tracker",
      "status": "problem",
      "summary": "bd is installed but could not read this project's issues",
      "detail": "no beads database here (exit 1)",
      "remedy": "bd init   # in /Users/you/calc"
    },
    {
      "check": "provider:claude-code",
      "status": "problem",
      "summary": "claude is installed but not authenticated, so every agent invocation would be refused",
      "remedy": "claude auth login"
    },
    {
      "check": "binary",
      "status": "warning",
      "summary": "the `yoyo` on PATH is a different build from the one running this check",
      "detail": "on PATH: v0.1.0; running: v0.2.0",
      "remedy": "go install github.com/mason-bryant/yoyodyne/cmd/yoyo@latest"
    }
  ]
}
```

Run `bd init` in `/Users/you/calc` yourself, then `yoyo doctor --json` again.
Hand `claude auth login` to the operator and wait, because it authenticates.
Name the drifted build with its remedy and let them decide, because it is a
warning. Do nothing about the `ok` finding at all.

## When doctor passes

Say so plainly, and say what is next rather than leaving them at a green
report: everything under `.yoyodyne/` is machine-independent and belongs in
version control, so it is committed along with the rest of their adoption, and
`yoyo chat` is where the brief, the goals, and the work are established. The
README's [third getting-started
step](../../README.md#3-yoyo-chat--establish-the-brief-and-the-goals) is what
they read next.
