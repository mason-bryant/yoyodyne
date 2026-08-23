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

## Where each section went

Every heading the guide used to carry is kept below, at this path, pointing at
where its content now lives. They are here so that a link written before the
split still lands on the section it named rather than silently on the top of
this page — which is what GitHub does with a fragment it cannot resolve, and is
invisible to the reader it fails.

Two of them can never be removed whatever else changes. `#checks` is in every
`.yoyodyne/config.yaml` that `yoyo init` has ever generated, in a file its owner
edits. `#product-specifications` is linked from the design document, from the
product goals, and from tracked work items — none of which a change to this
guide may rewrite.

### Creating a project configuration

Moved to [`configuration/setup.md`](configuration/setup.md#creating-a-project-configuration).

### When the repository ignores the configuration

Moved to [`configuration/setup.md`](configuration/setup.md#when-the-repository-ignores-the-configuration).

### Where the tracker syncs

Moved to [`configuration/setup.md`](configuration/setup.md#where-the-tracker-syncs).

### Layout

Moved to [`configuration/setup.md`](configuration/setup.md#layout).

### Discovery

Moved to [`configuration/setup.md`](configuration/setup.md#discovery).

### Precedence

Moved to [`configuration/setup.md`](configuration/setup.md#precedence).

### Product specifications

Moved to [`configuration/artifacts.md`](configuration/artifacts.md#product-specifications).

### What the product manager sees besides them, and what it does not

Moved to [`configuration/artifacts.md`](configuration/artifacts.md#what-the-product-manager-sees-besides-them-and-what-it-does-not).

### Artifact identity and metadata

Moved to [`configuration/artifacts.md`](configuration/artifacts.md#artifact-identity-and-metadata).

### Approving a document

Moved to [`configuration/artifacts.md`](configuration/artifacts.md#approving-a-document).

### What reaches the queue

Moved to [`configuration/goals.md`](configuration/goals.md#what-reaches-the-queue).

### Who may change an artifact

Moved to [`configuration/artifacts.md`](configuration/artifacts.md#who-may-change-an-artifact).

### Protected paths in a developer's change

Moved to [`configuration/artifacts.md`](configuration/artifacts.md#protected-paths-in-a-developers-change).

### Proposing a change to a document you do not own

Moved to [`configuration/artifacts.md`](configuration/artifacts.md#proposing-a-change-to-a-document-you-do-not-own).

### Traceability: references and orphans

Moved to [`configuration/goals.md`](configuration/goals.md#traceability-references-and-orphans).

### Goals, and the work attributed to them

Moved to [`configuration/goals.md`](configuration/goals.md#goals-and-the-work-attributed-to-them).

### What a change upstream leaves stale

Moved to [`configuration/goals.md`](configuration/goals.md#what-a-change-upstream-leaves-stale).

### Architectural invariants

Moved to [`configuration/goals.md`](configuration/goals.md#architectural-invariants).

### Checks

Moved to [`configuration/runs.md`](configuration/runs.md#checks).

### What `init` proposes for `checks`

Moved to [`configuration/runs.md`](configuration/runs.md#what-init-proposes-for-checks).

### How long a check may take

Moved to [`configuration/runs.md`](configuration/runs.md#how-long-a-check-may-take).

### Scheduling ready work

Moved to [`configuration/runs.md`](configuration/runs.md#scheduling-ready-work).

### Watching instead of draining

Moved to [`configuration/runs.md`](configuration/runs.md#watching-instead-of-draining).

### When a configuration change takes effect

Moved to [`configuration/runs.md`](configuration/runs.md#when-a-configuration-change-takes-effect).

### Why each run says why it was there

Moved to [`configuration/runs.md`](configuration/runs.md#why-each-run-says-why-it-was-there).

### Publishing through pull requests

Moved to [`configuration/publishing.md`](configuration/publishing.md#publishing-through-pull-requests).

### Publishing without automatic integration

Moved to [`configuration/publishing.md`](configuration/publishing.md#publishing-without-automatic-integration).

### Which branch is authoritative

Moved to [`configuration/publishing.md`](configuration/publishing.md#which-branch-is-authoritative).

### What publishing needs

Moved to [`configuration/publishing.md`](configuration/publishing.md#what-publishing-needs).

### Waiting out a provider that refuses

Moved to [`configuration/recovery.md`](configuration/recovery.md#waiting-out-a-provider-that-refuses).

### Relaunching a run the provider killed

Moved to [`configuration/recovery.md`](configuration/recovery.md#relaunching-a-run-the-provider-killed).

### Losing a race for the target branch

Moved to [`configuration/publishing.md`](configuration/publishing.md#losing-a-race-for-the-target-branch).

### How long one role may ask another

Moved to [`configuration/runs.md`](configuration/runs.md#how-long-one-role-may-ask-another).

### Research sources

Moved to [`configuration/agents.md`](configuration/agents.md#research-sources).

### Triage thresholds

Moved to [`configuration/recovery.md`](configuration/recovery.md#triage-thresholds).

### What one work item has been given

Moved to [`configuration/recovery.md`](configuration/recovery.md#what-one-work-item-has-been-given).

### Merge and removal semantics

Moved to [`configuration/setup.md`](configuration/setup.md#merge-and-removal-semantics).

### What fails closed

Moved to [`configuration/setup.md`](configuration/setup.md#what-fails-closed).

### Provider accounts

Moved to [`configuration/agents.md`](configuration/agents.md#provider-accounts).

### Operators

Moved to [`configuration/agents.md`](configuration/agents.md#operators).

### Reporting to Slack

Moved to [`configuration/agents.md`](configuration/agents.md#reporting-to-slack).

### Avatars

Moved to [`configuration/agents.md`](configuration/agents.md#avatars).

### Personas

Moved to [`configuration/agents.md`](configuration/agents.md#personas).

### Extending a built-in bundle

Moved to [`configuration/setup.md`](configuration/setup.md#extending-a-built-in-bundle).

### Converting an inheriting configuration to an explicit one

Moved to [`configuration/setup.md`](configuration/setup.md#converting-an-inheriting-configuration-to-an-explicit-one).

### Migrating from `.yoyodyne.yaml`

Moved to [`configuration/setup.md`](configuration/setup.md#migrating-from-yoyodyneyaml).

### Inspection

Moved to [`configuration/setup.md`](configuration/setup.md#inspection).
