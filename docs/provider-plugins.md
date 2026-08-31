# Provider plugins

Yoyo runs agents through a provider — a coding CLI or a harness that speaks to a
model API. Two are in the vocabulary: Claude Code, which serves every role and is
the one this build ships an adapter for, and Codex, which serves the two roles
inside a run and has no adapter yet.

A project can declare a provider of its own in its configuration, without forking
this repository or rebuilding the binary. **What a declaration supplies is the
dialect and the executable, not a new way of launching a process.** Your provider
runs on a compiled adapter — Claude Code's, today — which starts it and reads its
stream, and your declaration says which executable that adapter runs and how to
read what it says about rate limits, retries, and reset times. So a fork of a
provider yoyo already speaks, a proxy in front of one, or anything that talks the
same protocol and reports its limits differently is reachable from configuration
alone. Something that speaks a different protocol needs an adapter, which is a
change to yoyo.

A declaration that names no adapter, or one this build does not ship, is refused
when the configuration loads. There is deliberately no way to declare a provider
that validates and can never run.

This document is what a provider plugin is, what it may and may not decide, and
how one is written.

## What yoyo needs from a provider

Not very much, and deliberately so. A provider says a great many things while a
run is going; almost all of it is prose, tool calls, and accounting, and yoyo
acts on none of it. What it has to know is which of six things just happened.

| Answer | What it means |
|---|---|
| `served` | The attempt was served, or a limit that was refusing is refusing no longer |
| `retrying` | The provider hit something transient and is retrying by itself |
| `limit-reached` | A usage limit is refusing work and will lift — with, if the provider said so, when |
| `unavailable` | The provider's own servers could not serve the attempt, transiently |
| `interrupted` | The attempt died of something that judged nothing about the work |
| `refused` | A refusal that stands; the same request earns the same answer |

Those six are the contract. Everything yoyo does about a provider refusing work
— parking a run, recording the deadline, probing, blocking when the wait no
longer fits — is driven by them and by nothing provider-specific.

The distinctions are narrower than they look, and each one was learned from a
run that went wrong without it:

- `retrying` is not `unavailable`. A provider retrying by itself has not ended
  the attempt and nothing about the account is exhausted. By the time it reports
  an overload it has usually already spent its own retries.
- `unavailable` is not `limit-reached`. Nothing about the account is exhausted
  and no reset time is ever quoted, so it cannot be folded into the case below:
  an exhausted limit polls on an interval measured in tens of minutes, and an
  overloaded server lifts in seconds.
- `interrupted` is not `refused`. A connection that went away mid-reply says
  nothing about the request. Reading it as a judgement of the work fails a whole
  run on weather.

## Reset times: the two cases the contract owns

A plugin reports the reset time its provider named, and nothing more. What that
time is *worth* is yoyo's answer, not the plugin's, and it is the same answer for
every provider:

- **No reset time is unknown, not fatal.** A provider can refuse work without
  saying when it will stop — the monthly overage allowance does exactly this,
  while the ordinary rolling window keeps resetting on its usual schedule. Yoyo
  waits `execution.usage_limit_unknown_reset_pause` (thirty minutes by default)
  and asks again, under the same budget as any other wait.
- **A reset time that is not in the future is malformed and is not trusted.** A
  limit still refusing work while naming a reset that has already passed is not
  describing a wait; honoring it would reissue straight back into the same
  refusal with nothing bounding the attempts. Yoyo stops the run and records a
  blocker, because a clock skew or a window the provider has not rolled yet is a
  fact for a person.

A reset time your plugin cannot read is the first of these, not an error: the
limit still reaches yoyo as a limit. What yoyo refuses is guessing the wait, not
noticing the refusal.

See [waiting out a provider usage limit](operations.md#waiting-out-a-provider-usage-limit)
for what the waiting itself looks like.

## A plugin describes; it never decides

There is nowhere in a plugin to write a duration, a retry count, a budget, or a
condition on any of them. This is not a convention — the format has no such
field, and a configuration that tries to state one fails to load.

The reason is that whether to wait, how long, and against which budget are what
your `execution.usage_limit_*` settings mean and what a run's safety properties
rest on. A plugin that could decide to keep waiting could spend an account.

## How a plugin is delivered, and what that costs

A plugin is **data in your project's configuration**, and its dialect is a list
of ordered rules. Three deliveries were possible and this is the one chosen, so
here is the trade rather than the assertion.

**Declarative rules in configuration — what ships.** No fork, no vendoring, no
rebuild, and nothing on the other side of the boundary that runs, so there is no
new trust boundary to defend and the "describes but never decides" property is
structural rather than asked for. What it costs is reach: it covers a provider
whose reports differ from a built-in's in spelling — field names, event names,
the unit a reset time arrives in, a limit announced in a sentence — and it rides
on an existing adapter, so it covers nothing about how the process is started.

**Compile-time registration — what the built-ins use, and the only way to add an
adapter.** A dialect written in Go can read a shape no rule can describe. The
built-in Claude Code dialect uses it for exactly that: telling a subagent's
completion apart from the invocation's own terminal, and telling a transient 529
apart from a 4xx that describes the request. Launching a provider is the same
kind of thing — a command line, a permission model, a stream format — and stays
compiled in for the same reason. What it costs is that a user has to fork or
vendor yoyo, which is why the dialect and the executable were pulled out in front
of that boundary.

**A subprocess speaking a documented protocol — considered, not built.** It keeps
users independent the way declarative rules do *and* lets them run arbitrary
code. What it costs is a boundary to defend, and the thing on the other side is
untrusted: a protocol, a timeout, a crash policy, and a resource bound, all
guarding a component that gets a say in whether a run waits. That is a real piece
of engineering and it is not worth it until a plugin exists that declarative
rules cannot express. If you have one, that is the case for building it.

**What a plugin does not do:** it does not launch the provider. Starting the
process, sending the prompt, scoping the tools, and reading the stream are the
compiled adapter's, and a declaration names which one does that for it. So a
declaration reaches a provider that speaks a protocol yoyo already speaks; a
provider that speaks a different one needs an adapter written in Go, which is a
change to yoyo rather than to your configuration.

`yoyo doctor` diagnoses a declared provider as what it actually runs on: it looks
for the executable the declaration named, and reports it installed, missing, or
unauthenticated the same way it reports a built-in. A backend nothing in this
build can launch — Codex today, or a declaration that would not load — is
reported as one this build has no adapter for, with the configuration as the
remedy, because nothing you could install would give this build one.

## Capability validation

A declared provider states which roles it serves and which tool postures it can
hold them to. Both are checked when your configuration loads, before any work is
assigned — the same check a built-in gets, and the reason `codex` is refused for
an `architect` agent.

The two postures are:

- `read-only` — the agent reasons over the evidence it was handed and reaches
  outside it for nothing. It requires a provider that can refuse *every* tool,
  including nominally read-only ones. Every role but the developer needs this.
- `worktree-write` — the agent's work is editing a worktree, and the provider
  must be able to scope writes to it. The developer needs this.

A provider that declares only `read-only` is refused for a developer agent, and
one that declares only `worktree-write` is refused for a reviewer. Declaring a
capability you do not have is how a role meant to have no tools gets a shell, so
declare what is true.

The posture is also what decides the session mode an invocation is made in, and
the invocation the harness asks for carries none: nothing above the adapter names
a mode, so which one a role gets follows from which role it is. That matters most
for what an adapter must *not* choose. A provider with an interactive planning
mode puts that mode's own workflow into the session — do not execute yet, write a
plan, hand the plan back — and a harness-invoked role receives it on top of a role
contract that says the opposite: a reviewer told to plan when its contract wants
one verdict, or a developer told not to edit when the whole run is an edit. An
adapter picks the mode that grants what the posture needs and nothing else, never
the one that instructs.

## Writing one

Providers go under a top-level `providers:` key in your configuration, keyed by
the backend identifier your agents will name. See
[the configuration guide](configuration.md) for where that file lives.

```yaml
providers:
  my-harness:
    # Which compiled adapter launches it and reads its stream. Required, and
    # `claude-code` is the only one this build ships.
    adapter: claude-code
    # The executable that adapter runs. Omit it for the adapter's own.
    binary: my-harness
    roles:
      - developer
      - reviewer
    postures:
      - read-only
      - worktree-write
    capabilities:
      structured_events: true
      session_resumption: true
      structured_output: true
      tool_control: true
      local_auth: true
    dialect:
      rules:
        # Rules are tried in order and the first match wins, so a narrower
        # reading goes in front of a broader one.
        - answer: retrying
          type: retry

        # A limit an overage allowance is already serving is still serving.
        - answer: served
          type: quota
          fields:
            using_overage: "true"

        - answer: limit-reached
          type: quota
          fields:
            state: exceeded
          kind_field: window          # the provider's own name for the limit
          reset_field: resets_at
          reset_format: unix-seconds

        # Any other quota report is capacity.
        - answer: served
          type: quota

        - answer: unavailable
          terminal: true
          failed: true
          match: '(?i)\b503\b'

        - answer: interrupted
          terminal: true
          failed: true
          match: '(?i)connection reset'

        # Anything else that ended badly is a refusal that stands.
        - answer: refused
          terminal: true
          failed: true

agents:
  developers:
    role: developer
    backend: my-harness
    model: my-model
```

### Provider fields

| Field | Meaning |
|---|---|
| `adapter` | Required. The backend whose compiled adapter launches this provider. `claude-code` is the only one this build ships; naming anything else is refused at load. |
| `binary` | The executable that adapter runs. Omit it for the adapter's own. |
| `roles` | Which of the harness's roles this provider serves. |
| `postures` | `read-only`, `worktree-write`, or both. |
| `capabilities` | What the provider can do, stated rather than assumed. |
| `dialect.rules` | How to read what it says, below. |

### Rule fields

| Field | Meaning |
|---|---|
| `answer` | Required. One of the six answers above. |
| `type`, `subtype` | The provider's own names for the event, matched exactly. |
| `terminal`, `failed` | Whether the event ends the invocation, and whether it ended badly. |
| `match` | A regular expression the event's prose must contain. |
| `fields` | Dotted paths into the event payload that must equal the given value. |
| `kind`, `kind_field` | The provider's own name for the limit, stated or read from the payload. `limit-reached` only. |
| `reset_field`, `reset_match` | Where the reset time is: a payload path, or a regular expression over the prose with exactly one capturing group. `limit-reached` only. |
| `reset_format` | `unix-seconds`, `unix-millis`, or `rfc3339`. Required whenever a reset time is read. |

A rule that states no condition at all is refused, because it would answer for
every event the provider emits. So is a rule that reads a reset time without
saying how to read it: a number with no unit is not a time, and guessing the unit
is how a five-hour wait becomes five days.

A field the provider omits is *absent* rather than false, and a rule matching
something absent matches nothing. That is the safe direction — what it costs is a
refusal yoyo keeps failing on, and what the other direction costs is a wait
nobody can justify.

### What is not expressible

A reset time quoted in human local time — `resets 8:30pm (America/Los_Angeles)` —
cannot be read by a declarative rule today, because none of the three formats
covers a wall-clock time plus a zone plus an implied date. A limit announced that
way is still reported as a limit; its reset time is simply unknown, so yoyo polls
on the interval rather than waiting to a deadline. If your provider only ever
states reset times that way, say so — it is the clearest case for a fourth
format.

## Where the contract lives in the code

`internal/backend/contract.go` is the contract itself: the answers, the
observation a dialect returns, and `ReadReset`, which is the single place the
unknown and past-reset cases are decided. `internal/backend/declarative.go` is
the rule format on this page. `internal/backend/registry.go` holds the built-in
descriptions and turns a declaration into one. `internal/backend/claudecode/dialect.go`
is the Claude Code dialect, which is one implementation of the same contract and
gets no special treatment above it — the adapter beside it takes whichever
dialect it is handed, which is how a declared one comes to read a real stream.
`internal/cli/provider.go` is where the backend an agent named is resolved into
the adapter that runs it.
