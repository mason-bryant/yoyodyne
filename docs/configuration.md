# Yoyodyne configuration

Yoyodyne ships its own agent defaults. The executable contains a versioned,
read-only bundle of agent definitions and personas, so a project repository only
records what is genuinely its own: its identity, its checks, its approval policy,
and any deliberate deviation from the defaults. A project never needs access to
the Yoyodyne source checkout to run Yoyodyne.

## Layout

A project keeps its configuration in a `.yoyodyne` directory at its root:

```text
.yoyodyne/
  config.yaml          # the project configuration
  personas/            # optional persona overrides, Markdown only
    reviewer.md
```

Everything under `.yoyodyne/` is machine-independent and belongs in version
control. Run state, provider event streams, locks, and worktrees live outside the
repository under an operating-system state directory, so nothing there depends on
where the project is checked out.

The smallest configuration that can run work is:

```yaml
version: 1
extends: builtin:v1

product:
  id: example
  repository: .

checks:
  - go test ./...
```

`product` and `checks` are the two things the bundle never supplies, because
both describe the project rather than the harness. Omitting `checks` still
validates — the schema does not require it — but `yoyo run` refuses to
execute a work item with no configured check, so it is part of the smallest
configuration that is actually usable rather than merely valid.

Nothing else has to be written down. The five default agents — product manager,
architect, development manager, developer, and reviewer — come from the bundle
with a role, the Claude Code backend, a model selector, an instance count, and a
versioned persona each.

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
therefore keeps meaning the project root.

## Precedence

Three layers produce the effective configuration, later ones winning:

1. **Harness defaults.** Values the harness fills in when nothing else supplies
   them: `execution.max_concurrent_developers` (1),
   `execution.repair_attempts_before_replan` (2), `execution.worktree_root`
   (`auto`), `execution.remote` (`origin`),
   `execution.usage_limit_max_pause` and
   `execution.usage_limit_in_process_pause` (`6h` each),
   `approvals.publishing` (`human`), and an agent's `instances` (1).
   `approvals.publishing` is the only approval with a harness default, because
   it was added after configurations existed: a file written before it keeps the
   behavior it was written for — the harness publishes nothing — rather than
   failing to load for not mentioning a key that did not exist yet.
2. **The built-in bundle**, named by `extends`. Today the only bundle is
   `builtin:v1`. It supplies `execution`, `approvals`, and the five default
   agents. It deliberately supplies no `product` and no `checks`, because those
   describe the project rather than the harness.
3. **The project configuration**, which overlays whatever it names.

A configuration with no `extends` key is a complete standalone file: it inherits
nothing but the harness defaults, and must declare everything it needs. This is
the pre-directory shape, and it still loads unchanged.

`version` is the one field a project never inherits. It must be declared even
when `extends` names a bundle that declares its own, because a version taken
from the bundle would let a file written against a different schema load as
whatever the bundle happened to say — which is what the version exists to
prevent.

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
   merge and the harness performs it. Nothing about the gate changes — the same
   passing checks, the same independent-reviewer evidence, and the same
   fast-forward rule that gate integration also gate the merge.
3. **The merge is confirmed, then the branch is cleaned up** on both sides,
   locally and on the remote, on the same compare-and-swap evidence. The
   confirmation waits briefly and boundedly, because a forge notices its commits
   reaching the base shortly after the push rather than during it.

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
own push then refuses rather than force-resolves.

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
| `automatic` | `automatic` | Pull request opened, merged on approval, branch removed locally and on the remote. |
| `automatic` | `human` | Pull request opened and left for you. Nothing merged, nothing cleaned up. |

### Which branch is authoritative

**The local target branch.** Your work is where that branch says it is.

Merging is not a second promotion performed on the remote. The harness
fast-forwards the local target exactly as it always has, then pushes that same
commit to the remote target branch; the pull request is merged by the arrival of
its own commits on its base. One commit, one fast-forward, one answer. The
remote is the publication of the authoritative branch rather than a second copy
that could disagree with it.

If a promotion cannot be published — the forge is unreachable, or the remote
target moved — the run still succeeds and closes its item, and reports an
*outstanding publication*. The change is integrated where it counts; only its
publication is unfinished, and it is reconciled by hand. Nothing is ever
force-pushed to resolve it.

### What publishing needs

- A remote by the configured name. **Without one the run is purely local**,
  reports `publishing skipped`, and behaves exactly as it did before publishing
  existed. That is a property of the repository, not an error.
- The GitHub CLI, installed and authenticated (`gh auth login`). If a project
  asked to publish and `gh` is missing or logged out, the run **fails before it
  claims anything** — a harness that quietly stopped publishing would look the
  same as one with nothing to publish.
- Permission to push the target branch. Because merging is a push, a protected
  branch that refuses direct pushes will reject it and the run reports an
  outstanding publication. Merging through the forge instead would mean
  accepting a merge commit the harness did not create, which is what the
  fast-forward rule exists to prevent.

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
```

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

## Merge and removal semantics

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

To replace one persona and change nothing else:

```yaml
agents:
  reviewer:
    persona:
      version: house-1
      path: personas/reviewer.md
```

## Portability

A project that extends `builtin:v1` carries no dependency on where Yoyodyne
lives. Upgrading the executable upgrades the defaults and the personas the
project did not override. A project that pins behavior it cares about — a model
identifier, a persona, an approval policy — keeps that pinned across upgrades,
because a project value always wins over the bundle.

New bundle versions are added under new names rather than by changing an existing
one, so `builtin:v1` keeps meaning what it meant when a project adopted it.

## Migrating from `.yoyodyne.yaml`

A `.yoyodyne.yaml` file still loads, so migration is optional and can be done in
one step:

1. `mkdir .yoyodyne`
2. Move the file: `git mv .yoyodyne.yaml .yoyodyne/config.yaml`
3. Add `extends: builtin:v1` near the top.
4. Delete every agent field that now matches the bundle. In practice this is most
   of the `agents` block; keep only genuine deviations.
5. Run `yoyo config show --effective --origins` and confirm the effective
   values still match what the old file produced.

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
| `builtin:v1` | Inherited from the built-in bundle. |
| a file path | Supplied by that project configuration file. |
| `derived:product.id` | Computed from another configured value. |

An unexpected effective value is therefore a two-command diagnosis: `--effective`
says what the value is, and `--origins` says which layer is responsible for it.
