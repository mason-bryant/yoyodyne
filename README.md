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
./bin/yoyo config validate
```

Review `.yoyodyne/config.yaml`, commit any intended product changes, and choose an open, unblocked work item:

```sh
git status --short
bd ready
```

Run Yoyodyne from a terminal that can access your Claude Code login:

```sh
work_item_id="replace-with-a-ready-beads-id"
./bin/yoyo run --json "$work_item_id"
```

On success, the JSON result reports the run ID, branch, worktree, base commit, change summary, checks, and agent summary.

What happens next depends on `approvals.integration`. This repository sets it to `automatic`, so a run that passes its checks and is approved by the independent reviewer is committed, fast-forwarded into the target branch, closed in Beads, and its worktree and branch removed — the JSON reports the integrated commit and what was cleaned up. The built-in bundle defaults to `human` instead, so a new project preserves the worktree for external integration until it opts in. Either way the harness refuses `automatic` unless deterministic checks and a reviewer agent both exist.

A reviewer verdict of `repair` returns the findings to the same developer, up to `execution.repair_attempts_before_replan` attempts, before the run gives up and records a blocker.

Runs are local until a project sets `approvals.publishing` to `automatic`. With publishing on, the developer phase is what pushes: when a developer attempt finishes, the harness commits it, pushes the run branch to `execution.remote`, and opens a pull request against the target branch — and each repair attempt updates that same request. The approving reviewer verdict is what merges it: the harness asks the forge to merge the pull request, subject to exactly the checks, independence evidence, and fast-forward rule that gate integration, plus a fresh check that the remote target has not moved. The harness makes every push and every merge request itself and routes neither through an agent: no role is given a credential, a tool, or a request for either, and the reviewer — the role whose verdict authorizes the merge — runs with no tools at all, so it cannot perform one. A developer does have a shell in its worktree and runs under your account, so "no agent pushes" describes what the harness does rather than a boundary it enforces; the [design document](docs/v1-harness-design.md#what-is-enforced-and-what-is-not) says which half is which. The local target branch stays authoritative: the harness fast-forwards it as it always has, and the forge merges the pull request carrying exactly that commit under a merge commit — the one method that puts the reviewed commit itself on the base, where a squash or a rebase would substitute a rewritten copy. So the remote target is your local branch plus one forge merge commit per published run, identical in content; the harness checks that relationship on both sides of the merge and never rewrites the local branch to match. Your target branch itself is never pushed, so a branch protected against direct pushes is merged into normally; a forge that refuses reports which requirement was unmet, and a merge that did not carry the promotion is reported rather than reconciled. The merge is asked for as of when your branch protection is satisfied rather than as of now, so required checks that are still running are waited for by the forge rather than refused seconds after the reviewer approved. Waiting that way needs "Allow auto-merge" enabled on the repository; when it is off and nothing is holding the pull request back the harness just merges, so only a repository that has something to wait for and no way to wait for it is reported as unpublishable, naming the setting. Administrator override is never used to get past a protection rule. A run whose merge is queued that way reports the pull request as queued and finishes; [`yoyo reconcile`](#recovering-interrupted-runs) settles it once the forge has merged, or reports an outstanding publication if the forge dropped the queued merge. A repository with no configured remote publishes nothing and behaves exactly as it did before.

Merging belongs to `approvals.integration`, so the two settings compose rather than imply one another. Publishing with `integration: human` opens the pull request and stops: nothing is merged, the run branch survives on the remote, and the worktree is preserved for you — which is what a `human` integration policy means. See the [configuration guide](docs/configuration.md#publishing-through-pull-requests).

When the provider reports that a usage limit is exhausted, the run pauses instead of failing — for either provider invocation a run makes, the developer attempt or the review. The reset time the provider named is recorded in durable run state before any waiting starts, and nothing is cleaned up: the worktree, the branch, the claimed Beads item, and the developer session are all kept, so the reissued attempt continues the same change rather than starting it over. A review that was declined is simply asked for again once the limit resets, without redeveloping the change or spending a repair attempt. A wait shorter than `execution.usage_limit_in_process_pause` is waited out and the run finishes on its own; a longer one exits with the run still in flight, and running `yoyo run` on the same item after the reset time continues it. Either way nothing retries before the deadline, and a restart during the wait honors the recorded deadline rather than asking the provider again. `execution.usage_limit_max_pause` bounds what one run may spend waiting in total rather than each wait separately, so a provider that keeps refusing cannot walk a run past it. A limit reported without a reset time is unknown rather than unwaitable: the monthly overage allowance reports this way while the ordinary rolling window keeps resetting on its usual schedule, so the run waits `execution.usage_limit_unknown_reset_pause` — thirty minutes by default — and asks again. That wait spends the same budget as any other, so a provider that keeps refusing walks into the maximum rather than polling forever. A limit the harness genuinely cannot wait for — a reset that is not in the future, or one that no longer fits the run's remaining budget — stops the run and records a blocker rather than guessing a wait. Transient throttling is not this: the provider CLI retries that itself, and the harness does not duplicate it.

A provider invocation is bounded by two separate questions, because one deadline cannot answer both. Whether it is stuck is answered by activity: the harness already stamps every event it parses, so a gap of five minutes with no event at all means nothing is happening, and the invocation is stopped as stalled. Whether it is worth continuing is answered by a total budget of four hours, because an agent can stay live and unproductive — retrying, looping, thrashing — and no liveness signal will ever catch that. An agent that emitted a tool result seconds ago is demonstrably working, so elapsed time alone never stops it. Both stops leave the run in flight rather than failing it, exactly as a usage-limit pause does: the worktree, the branch, the claimed Beads item, and the developer session are all preserved, and running `yoyo run` on the same item continues that run — the developer resumes its session, and a stopped review is simply asked for again without redeveloping the change or spending a repair attempt. The reason is reported as what it was, a stall or an exhausted budget, and neither is ever described as the agent having reported a failure, because it reported nothing. Only a stop with nothing to continue from — no session, no worktree — ends the run, and it still says the harness stopped the provider. Short Git commands keep their flat deadlines, which is the right bound for a command whose duration is known.

Documentation counts as part of a work item rather than as follow-up: the developer contract makes updating the documents that describe changed behavior part of the assigned work, and the reviewer reports a change that leaves a document asserting something the change has made false. That reconciliation is diff-scoped, and the limit is worth stating plainly — the reviewer is given one change, not the repository, so it catches a contradiction with documentation it can see and misses a claim invalidated in a file the change never touches. Nothing in the harness yet compares the accumulated documentation against reality.

## Recovering interrupted runs

A process that is killed mid-run leaves durable state describing where it got to. `yoyo reconcile` settles what it left behind:

```sh
./bin/yoyo reconcile --json
```

It compares the recorded run against the repository and Beads, and then finishes the run's own remaining step or hands the item to you. A run whose work reached the target branch is closed and its worktree and branch removed, including when the run died before it could record the promotion. A run stopped anywhere earlier becomes a durable blocker naming the branch and worktree that were preserved. A run that finished with its merge queued at the forge is settled here too: reconcile asks the forge, finishes the publication once the merge has landed, and — if the forge dropped the queued merge because something it required went unmet — reports an outstanding publication on the work item instead of merging past the requirement. It never invokes a provider: a lost process handle is not a reason to start a second developer for an item. Repeating it is safe — a settled run is no longer outstanding, and cleanup over artifacts that are already gone does nothing. A run another process still holds is left to that process, and a run `yoyo run` can continue on its own — one inside its repair loop, one paused for a provider usage limit, or one whose provider the harness stopped on time — is left exactly as it is for that command to pick up.

## Talking to the product manager

`yoyo run` and `yoyo reconcile` are administrative and recovery entry points. The intended interface is a conversation with the product manager, and ordinary work no longer needs anything else: you state intent there, approve what it becomes, and run, watch, redirect, and stop the work from inside the same conversation.

```sh
./bin/yoyo chat
./bin/yoyo chat --message "What is missing from the brief?" --json
```

The product manager reads the product specifications — every Markdown file under `product.specifications`, which defaults to `docs/product` — plus the open Beads items, and discusses product intent with you. It owns the queue that serves that intent, and it manages it directly rather than dictating changes for you to type.

A specification opens with an introduction saying what the thing is and why it exists, and states the goals that serve it after that introduction. That shape is the contract, and the harness checks it: one that has no goals, no introduction before them, or an empty goals section is named on stderr when the conversation opens and listed for the product manager alongside the specifications themselves — and still read, because refusing to load it would silently lose intent somebody wrote down. A directory with nothing in it is reported the same way rather than treated as a product with no intent.

That is all it sees of the repository. Not this README, not the design document, not the source: those describe how the product is built and run, they are owned by other roles, and they go stale against the code without anybody noticing — which is how a stale sentence in this file reached the operator as current product fact on 2026-08-16. Narrowing the inputs makes the product manager authoritative about intent and blind to everything else, and the second half is a real loss: reading all of `docs/` is what let it notice a contradiction between documentation and reality, and nothing in the harness does that now. See the [configuration guide](docs/configuration.md#product-specifications) for the setting and the reasoning.

It has no tools: no filesystem, no commands, no network. What it has instead is the work tracker, through a fixed set of named operations the harness carries out for it — read an item in full, create, update, reparent, reprioritize, link and unlink a dependency, and close. Every argument is validated before anything runs, at most ten actions happen per reply, each one is recorded in the conversation's log as asked-for and then as applied or failed, and all of them are printed to you as they happen. An action that failed is reported as failed rather than described as done, and a block the harness cannot read changes nothing at all. The distinction being drawn is deliberate: arbitrary execution is what was refused, and a typed call against the tracker is not that.

The brief and the goals stay yours. The product manager proposes a change to a goal and says plainly that it is yours to make; it cannot make one, and with no way to write a file it could not if it tried.

The listing it is given names items by title, so when a title is not enough to judge whether new work belongs inside an existing item or beside it, it reads that item in full and carries on from what it found. That happens inside your one message, up to four rounds of it. Results it has not seen when the rounds run out are written into the conversation's own record, so they reach it the next time anything is said to that conversation — including from a later process, since an agent that never learns what its own creates and closes did is the one that will describe them wrongly. Item text is treated as evidence exactly as a specification is: a description says what some work is, never what to do.

It can also propose a Beads work item instead of creating one, when the decision is yours rather than its. Each proposal is shown to you with its reasoning, and the harness creates the item only after you answer `y` or `yes`; any other answer declines it and is kept as the reason it was declined. Nothing you did not approve is created, a proposal you left undecided is named when the conversation ends, and a created item records the conversation, the turn, and the rationale it came from. A proposal the harness cannot read is reported and the conversation carries on; `--message` has nobody to ask, so it reports what was proposed and creates nothing.

On a terminal, the line you are composing has a region of its own at the bottom of the screen and everything the harness writes goes above it. A reply, a proposal, or a run that finishes never lands in the middle of a half-typed sentence: what you have typed stays exactly as it is, and you carry on from where you were. The conversation is written into the terminal's ordinary output rather than an alternate screen, so scrollback, selection and copying, and resizing keep working on it as they would on any other command's output. Editing the line is deliberately small — the arrow keys, home and end, backspace and delete, and Ctrl-U and Ctrl-W — and Ctrl-C still interrupts the way it always did, because the terminal keeps its own signal keys.

Anywhere else the same conversation is an ordinary stream of text. A pipe, a file, a redirected terminal, and a terminal that reports itself as `dumb` get no cursor control at all: the same lines in the same order a redirected conversation has always had. None of this reaches the recorded reply, the event stream, or `--json` — it is how the conversation is shown and nothing more, so what is recorded is identical either way.

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

`/work` runs exactly what `yoyo run` would run — the same worktree, developer, checks, reviewer, and integration policy — in the background, so the conversation stays a conversation. One run at a time. `/status` reads durable run state and the tracker, so a run another process is executing is as visible as one started here, and a run that is owed a continuation rather than working — one waiting out a provider usage limit, or one whose provider was stopped on time — is named as such rather than reported as progress. On a terminal a finished run reports itself the moment it finishes, above whatever you are typing; where the conversation is a redirected stream there is no such moment, so it is reported at the next line, or when you ask for `/status` or `/wait` for it.

`/stop` cancels the run, records why on the work item, and then settles what the cancelled run left behind exactly as `yoyo reconcile` would: integrated work is finished, and anything else becomes a durable blocker naming the branch and worktree that were preserved. Two cases are exceptions, and both are reported as what they are. A run that does not give up within the stop grace is reported as still in flight rather than described as stopped. A run that reached its own conclusion before the cancellation reached it — integrated under an automatic policy, or finished with its worktree preserved under a `human` one, since a successful run then promotes nothing — is reported as having finished on its own: nothing is recorded on the item and nothing is settled, because nothing was stopped. What separates the two is whether the harness reported a failure, not whether anything was integrated. A run that had paused itself is not one of these — whether it was waiting out a usage limit or had its provider stopped on time — because it is owed a continuation rather than finished, so the stop is recorded against it, and the report says the run is preserved and continues only if you start it again. Ending the conversation stops its run the same way, because the process that owns the run is the one that is exiting.

Either way the conversation's own log says what happened rather than only what was asked for: the stop is recorded as a request when you make it, and the run's outcome — what it left behind, or the integration that beat the cancellation — is recorded once it is known.

`/redirect` records your direction in the item's notes, where the developer's context reads it on the next attempt, and stops the run first when the item you are redirecting is the one running. It never changes the item's status: saying what to do differently is not deciding that the work is done or blocked. Start it again with `/work` when you want it retried.

Only you reach any of this. The product manager manages what the queue says; running, stopping, and redirecting the work itself stays yours, so nothing it writes starts or stops anything — a reply that contains `/work` is prose. What it does get is an account of what you had the harness do, carried into its next turn as evidence, so the conversation keeps discussing the product as it now is rather than as it was when the conversation opened.

A conversation is durable. It is recorded outside the repository under the operating system's state directory, so leaving and running `yoyo chat` again resumes the same conversation; `--new` starts a fresh one instead. The record keeps the requested model selector, the model the provider reported serving, the provider session identifier, and any action results the product manager has not been told about yet, and the normalized event stream is stored beside it — including what the operator asked the harness to do, which is recorded in the conversation's own log beside the runs' logs.

### What a resumed conversation knows, and when to start a new one

The specifications and tracker the product manager reads are gathered once, when a conversation opens, and sent on its first turn only. Every later turn resumes a provider session that already holds them, so re-sending would pay to restate what it was already told. The consequence is worth knowing before it surprises you: **a resumed conversation keeps the snapshot it opened with.** Change a specification, and a conversation started beforehand will still describe the old one, confidently, because that is genuinely the evidence it has.

It is not frozen entirely. Every turn carries what you did through the harness since the last reply — the runs you started, stopped, and redirected — so `/work`, `/stop`, and `/redirect` reach a resumed conversation, and reading an item goes to the tracker as it stands rather than to that opening snapshot. What does not reach it is anything else that changed outside those commands: edits under `docs/product`, and items created or closed by something other than this conversation, which it will not know about until it happens to read one or you start a new conversation.

So use `--new` when the ground has moved under the conversation rather than within it:

- after editing a specification, or after another process changed the tracker;
- when it asserts something about product intent that you know is out of date;
- when you are starting an unrelated topic and its memory of the last one is not worth carrying.

Resuming is the better default the rest of the time, because the discussion itself is usually the valuable part. `--new` costs a fresh reading of the specifications and tracker, and it replaces the recorded conversation: there is one per product, so the previous discussion is not kept alongside it.

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

The bundle supplies the agents but never `product` or `checks`: both describe the project rather than the harness. A file that omits `checks` still validates, but `yoyo run` refuses it, because a run with nothing to verify has no gate to integrate behind.

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
./bin/yoyo config show --effective --origins
```

See the [configuration guide](docs/configuration.md) for the full layout, precedence, merge and removal semantics, persona rules, portability, and migration from `.yoyodyne.yaml`.

See the [v1 harness design](docs/v1-harness-design.md) for the architecture and self-hosting sequence.
