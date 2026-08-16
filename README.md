# Yoyodyne

Yoyodyne is a local, single-operator harness that runs bounded Beads work items with AI coding agents. The current bootstrap supports one Claude Code developer in an isolated Git worktree, followed by configured checks and durable outcome recording.

## Quick start

Prerequisites:

- Go 1.24 or newer
- Git and Make
- Beads (`bd`) initialized in the repository
- Claude Code installed and authenticated

From an initialized Yoyodyne checkout, verify the tools, run all checks, and build the CLI:

```sh
claude auth status --json
bd where
make check
make build
./bin/yoyodyne config validate
```

Review `.yoyodyne/config.yaml`, commit any intended product changes, and choose an open, unblocked work item:

```sh
git status --short
bd ready
```

Run Yoyodyne from a terminal that can access your Claude Code login:

```sh
work_item_id="replace-with-a-ready-beads-id"
./bin/yoyodyne run --json "$work_item_id"
```

On success, the JSON result reports the run ID, branch, worktree, base commit, change summary, checks, and agent summary.

What happens next depends on `approvals.integration`. This repository sets it to `automatic`, so a run that passes its checks and is approved by the independent reviewer is committed, fast-forwarded into the target branch, closed in Beads, and its worktree and branch removed — the JSON reports the integrated commit and what was cleaned up. The built-in bundle defaults to `human` instead, so a new project preserves the worktree for external integration until it opts in. Either way the harness refuses `automatic` unless deterministic checks and a reviewer agent both exist.

A reviewer verdict of `repair` returns the findings to the same developer, up to `execution.repair_attempts_before_replan` attempts, before the run gives up and records a blocker.

Documentation counts as part of a work item rather than as follow-up: the developer contract makes updating the documents that describe changed behavior part of the assigned work, and the reviewer reports a change that leaves a document asserting something the change has made false. That reconciliation is diff-scoped, and the limit is worth stating plainly — the reviewer is given one change, not the repository, so it catches a contradiction with documentation it can see and misses a claim invalidated in a file the change never touches. Nothing in the harness yet compares the accumulated documentation against reality.

## Recovering interrupted runs

A process that is killed mid-run leaves durable state describing where it got to. `yoyodyne reconcile` settles what it left behind:

```sh
./bin/yoyodyne reconcile --json
```

It compares the recorded run against the repository and Beads, and then finishes the run's own remaining step or hands the item to you. A run whose work reached the target branch is closed and its worktree and branch removed, including when the run died before it could record the promotion. A run stopped anywhere earlier becomes a durable blocker naming the branch and worktree that were preserved. It never invokes a provider: a lost process handle is not a reason to start a second developer for an item. Repeating it is safe — a settled run is no longer outstanding, and cleanup over artifacts that are already gone does nothing. A run another process still holds is left to that process, and a run inside its repair loop is left for `yoyodyne run` to continue.

## Talking to the product manager

`yoyodyne run` is a bootstrap entry point. The intended interface is a conversation with the product manager about what the product should be:

```sh
./bin/yoyodyne chat
./bin/yoyodyne chat --message "What is missing from the brief?" --json
```

The product manager reads the repository's own Markdown — `README.md` and everything under `docs/` — plus the open Beads items, and discusses product intent with you. It is advisory: it has no tools at all, so it creates, changes, and approves nothing. Anything it concludes is a recommendation for you to act on.

A conversation is durable. It is recorded outside the repository under the operating system's state directory, so leaving and running `yoyodyne chat` again resumes the same conversation; `--new` starts a fresh one instead. The record keeps the requested model selector, the model the provider reported serving, and the provider session identifier, and the normalized event stream is stored beside it.

## Configuring a project

Yoyodyne carries its own agent defaults, so a project repository stores only its own settings and any deliberate deviation from them. A configuration can be as small as this:

```yaml
# .yoyodyne/config.yaml
version: 1
extends: builtin:v1

product:
  id: example
  repository: .

checks:
  - go test ./...
```

The bundle supplies the agents but never `product` or `checks`: both describe the project rather than the harness. A file that omits `checks` still validates, but `yoyodyne run` refuses it, because a run with nothing to verify has no gate to integrate behind.

That inherits five default agents — product manager, architect, development manager, developer, and reviewer — each with a role, the Claude Code backend, a model selector, an instance count, and a persona. An override names only what it changes; everything else keeps inheriting:

```yaml
agents:
  developer:
    model: claude-opus-5-20260514
  reviewer:
    persona:
      version: house-1
      path: personas/reviewer.md   # relative to .yoyodyne/
  architect:
    disabled: true                 # remove an inherited agent
```

Yoyodyne finds the nearest `.yoyodyne/config.yaml` from the current directory upwards, so it runs from the project root or anywhere beneath it. To see what a configuration actually resolves to and which layer each value came from:

```sh
./bin/yoyodyne config show --effective --origins
```

See the [configuration guide](docs/configuration.md) for the full layout, precedence, merge and removal semantics, persona rules, portability, and migration from `.yoyodyne.yaml`.

See the [v1 harness design](docs/v1-harness-design.md) for the architecture and self-hosting sequence.
