# Yoyodyne configuration

**A Yoyodyne project owns its configuration outright.** `yoyo init` writes a
complete `.yoyodyne/config.yaml` — every agent, backend, model selector,
provider account, instance count, and persona reference stated in the file — and copies the
personas themselves into `.yoyodyne/personas/`. Nothing is inherited at load
time, so what the file says is what runs, and an edit to it is an edit to the
harness's behavior with nothing in between.

The executable still contains a versioned, read-only bundle of agent definitions
and personas. It is the **template `init` generates from**, not a layer
underneath your project. A project therefore never needs access to the Yoyodyne
source checkout, and nobody reading its configuration has to be told where a
value came from.

**What owning your defaults costs.** A later Yoyodyne that improves a persona or
corrects a model selector does not reach a project that already has its own
copy. There is no mechanism that reconciles the two: re-run `yoyo init` in a
scratch directory, diff it against yours, and merge what you want. That is a
deliberate trade for a tool whose operator reads and edits the file often — the
effect of an edit is obvious, which matters more here than shared improvement.
Inheritance is still supported for projects that would rather have the other
half of that trade; see
[Extending a built-in bundle](configuration/setup.md#extending-a-built-in-bundle).

## The guides

The reference is split by what you are trying to configure. This page is the
index and stays at this path, so a link anyone has already written still opens
something that knows where the answer went.

- [**Writing a project configuration**](configuration/setup.md) — what `init`
  writes, the layout of `.yoyodyne/`, discovery, precedence, how the layers merge
  and what fails closed, extending a built-in bundle, and `yoyo config`.
- [**Artifact homes, identity, and ownership**](configuration/artifacts.md) —
  `product.specifications`, artifact identity and metadata, approving a document,
  who may change one, and the protected paths a developer's change is refused.
- [**Admission, attribution, and staleness**](configuration/goals.md) — what
  reaches the work queue, traceability and orphans, the goal a work item is
  attributed to, what a change upstream leaves stale, and architectural
  invariants.
- [**Checks, scheduling, and what a run may spend**](configuration/runs.md) —
  the checks that gate integration, how long one may take, scheduling and
  watching for ready work, and the bound on one role asking another.
- [**Publishing, branches, and promotion**](configuration/publishing.md) —
  pull requests, publishing without automatic integration, which branch is
  authoritative, and losing a race for the target branch.
- [**Triage thresholds and provider waits**](configuration/recovery.md) —
  waiting out a provider that refuses, relaunching a run the provider killed,
  and the thresholds that escalate a stalled run or an item given enough.
- [**Agents, operators, and reporting**](configuration/agents.md) — provider
  accounts, the operators this project recognizes, personas, research sources,
  and reporting to Slack.

## Checks

Moved to [`configuration/runs.md`](configuration/runs.md#checks). This heading
stays so the link in every generated `.yoyodyne/config.yaml` keeps resolving.

## Product specifications

Moved to
[`configuration/artifacts.md`](configuration/artifacts.md#product-specifications).
This heading stays so the links written into designs, goals, and tracked work
items keep resolving.
