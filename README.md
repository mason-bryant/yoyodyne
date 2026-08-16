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

On success, the JSON result reports the run ID, branch, worktree, base commit, change summary, checks, and agent summary. The bootstrap harness preserves the worktree for external review; it does not yet commit, integrate, or close the item automatically.

## Configuring a project

Yoyodyne carries its own agent defaults, so a project repository stores only its own settings and any deliberate deviation from them. A complete configuration can be as small as this:

```yaml
# .yoyodyne/config.yaml
version: 1
extends: builtin:v1

product:
  id: example
  repository: .
```

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
