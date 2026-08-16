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

When the provider reports that a usage limit is exhausted, the run pauses instead of failing — for either provider invocation a run makes, the developer attempt or the review. The reset time the provider named is recorded in durable run state before any waiting starts, and nothing is cleaned up: the worktree, the branch, the claimed Beads item, and the developer session are all kept, so the reissued attempt continues the same change rather than starting it over. A review that was declined is simply asked for again once the limit resets, without redeveloping the change or spending a repair attempt. A wait shorter than `execution.usage_limit_in_process_pause` is waited out and the run finishes on its own; a longer one exits with the run still in flight, and running `yoyodyne run` on the same item after the reset time continues it. Either way nothing retries before the deadline, and a restart during the wait honors the recorded deadline rather than asking the provider again. `execution.usage_limit_max_pause` bounds what one run may spend waiting in total rather than each wait separately, so a provider that keeps refusing cannot walk a run past it. A limit the harness cannot wait for — no reset time, a reset that is not in the future, or one that no longer fits the run's remaining budget — stops the run and records a blocker rather than guessing a wait. Transient throttling is not this: the provider CLI retries that itself, and the harness does not duplicate it.

Documentation counts as part of a work item rather than as follow-up: the developer contract makes updating the documents that describe changed behavior part of the assigned work, and the reviewer reports a change that leaves a document asserting something the change has made false. That reconciliation is diff-scoped, and the limit is worth stating plainly — the reviewer is given one change, not the repository, so it catches a contradiction with documentation it can see and misses a claim invalidated in a file the change never touches. Nothing in the harness yet compares the accumulated documentation against reality.

## Recovering interrupted runs

A process that is killed mid-run leaves durable state describing where it got to. `yoyodyne reconcile` settles what it left behind:

```sh
./bin/yoyodyne reconcile --json
```

It compares the recorded run against the repository and Beads, and then finishes the run's own remaining step or hands the item to you. A run whose work reached the target branch is closed and its worktree and branch removed, including when the run died before it could record the promotion. A run stopped anywhere earlier becomes a durable blocker naming the branch and worktree that were preserved. It never invokes a provider: a lost process handle is not a reason to start a second developer for an item. Repeating it is safe — a settled run is no longer outstanding, and cleanup over artifacts that are already gone does nothing. A run another process still holds is left to that process, and a run `yoyodyne run` can continue on its own — one inside its repair loop, or one paused for a provider usage limit — is left exactly as it is for that command to pick up.

## Talking to the product manager

`yoyodyne run` and `yoyodyne reconcile` are administrative and recovery entry points. The intended interface is a conversation with the product manager, and ordinary work no longer needs anything else: you state intent there, approve what it becomes, and run, watch, redirect, and stop the work from inside the same conversation.

```sh
./bin/yoyodyne chat
./bin/yoyodyne chat --message "What is missing from the brief?" --json
```

The product manager reads the repository's own Markdown — `README.md` and everything under `docs/` — plus the open Beads items, and discusses product intent with you. It is advisory: it has no tools at all, so it creates, changes, and approves nothing. Anything it concludes is a recommendation for you to act on.

It can propose Beads work items from what you discussed, and a proposal is still only a recommendation. Each one is shown to you with its reasoning, and the harness creates the item only after you answer `y` or `yes`; any other answer declines it and is kept as the reason it was declined. Nothing you did not approve is created, a proposal you left undecided is named when the conversation ends, and a created item records the conversation, the turn, and the rationale it came from. A proposal the harness cannot read is reported and the conversation carries on; `--message` has nobody to ask, so it reports what was proposed and creates nothing.

### Steering the work from the conversation

A line that begins with a slash is a command the harness carries out for you; everything else is said to the product manager:

```text
/status                  what is in flight, claimed, blocked, available, and done
/work <beads-id>         run one work item now, while you keep talking
/wait                    wait for the run this conversation started and report it
/stop [reason]           stop that run and settle what it left behind
/redirect <id> <what to do differently>
/help                    the list
/exit                    end the conversation, stopping anything it is running
```

`/work` runs exactly what `yoyodyne run` would run — the same worktree, developer, checks, reviewer, and integration policy — in the background, so the conversation stays a conversation. One run at a time. `/status` reads durable run state and the tracker, so a run another process is executing is as visible as one started here, and a run waiting out a provider usage limit is named as waiting rather than reported as progress. A finished run is reported when you next press enter, ask for `/status`, or `/wait` for it.

`/stop` cancels the run, records why on the work item, and then settles what the cancelled run left behind exactly as `yoyodyne reconcile` would: integrated work is finished, and anything else becomes a durable blocker naming the branch and worktree that were preserved. Two cases are exceptions, and both are reported as what they are. A run that does not give up within the stop grace is reported as still in flight rather than described as stopped. A run that reached its own conclusion before the cancellation reached it — integrated under an automatic policy, or finished with its worktree preserved under a `human` one, since a successful run then promotes nothing — is reported as having finished on its own: nothing is recorded on the item and nothing is settled, because nothing was stopped. What separates the two is whether the harness reported a failure, not whether anything was integrated. A run that had paused itself for a usage limit is not one of these: it is owed a continuation rather than finished, so the stop is recorded against it, and the report says the run is preserved and continues only if you start it again. Ending the conversation stops its run the same way, because the process that owns the run is the one that is exiting.

Either way the conversation's own log says what happened rather than only what was asked for: the stop is recorded as a request when you make it, and the run's outcome — what it left behind, or the integration that beat the cancellation — is recorded once it is known.

`/redirect` records your direction in the item's notes, where the developer's context reads it on the next attempt, and stops the run first when the item you are redirecting is the one running. It never changes the item's status: saying what to do differently is not deciding that the work is done or blocked. Start it again with `/work` when you want it retried.

Only you reach any of this. The product manager still has no tools, so nothing it says starts, stops, or redirects anything — a reply that contains `/work` is prose. What it does get is an account of what you had the harness do, carried into its next turn as evidence, so the conversation keeps discussing the product as it now is rather than as it was when the conversation opened.

A conversation is durable. It is recorded outside the repository under the operating system's state directory, so leaving and running `yoyodyne chat` again resumes the same conversation; `--new` starts a fresh one instead. The record keeps the requested model selector, the model the provider reported serving, and the provider session identifier, and the normalized event stream is stored beside it — including what the operator asked the harness to do, which is recorded in the conversation's own log beside the runs' logs.

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
