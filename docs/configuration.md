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
corrects a model selector does not change a project that already has its own
copy — nothing is inherited, so nothing arrives. It does say so, and moving a
value stays yours. That report, and inheritance for projects wanting the other
half of the trade, are both under
[Extending a built-in bundle](configuration/setup.md#extending-a-built-in-bundle).

## The guides

The reference is split by what you are trying to configure. This page is the
index and stays at this path, so a link anyone has already written still opens
something that knows where the answer went.

- [**Writing a project configuration**](configuration/setup.md) — what `init`
  writes, the layout of `.yoyodyne/`, discovery, keeping the configuration
  outside the repository, precedence, how the layers merge and what fails
  closed, extending a built-in bundle, and `yoyo config`.
- [**Artifact homes, identity, and ownership**](configuration/artifacts.md) —
  `product.specifications` and what the product manager sees besides them,
  artifact identity and metadata, approving a document, who may change one, the
  protected paths a developer's change is refused, and
  [proposing a change to a document you do not own](configuration/artifacts.md#proposing-a-change-to-a-document-you-do-not-own).
- [**Admission, attribution, and staleness**](configuration/goals.md) — what
  reaches the work queue, traceability and orphans, the goal a work item is
  attributed to, what a change upstream leaves stale, the release-readiness
  workflow, and architectural invariants.
- [**Checks, scheduling, and what a run may spend**](configuration/runs.md) —
  the checks that gate integration and the environment they run in, how long one
  may take, scheduling and watching for ready work, developer slots and models
  chosen by label, and running a work item against the workflow definition.
- [**Publishing, branches, and promotion**](configuration/publishing.md) — pull
  requests, publishing from a fork, publishing without automatic integration,
  which branch is authoritative, a protected target,
  [what publishing needs](configuration/publishing.md#what-publishing-needs),
  and losing a race for the target branch.
- [**Triage thresholds and provider waits**](configuration/recovery.md) —
  waiting out a provider that refuses, serving a turn from an alternate model,
  pinning a model version, relaunching a run the provider killed, waiting out a
  network that dropped, and the thresholds that escalate a stalled run or an
  item given enough.
- [**Agents, operators, and reporting**](configuration/agents.md) — provider
  accounts and pooling them, the operators this project recognizes, reporting to
  Slack, services, recurring tasks, personas, how long one role may ask another,
  how far behind a conversation may fall, queueing a question, research sources,
  and reading the repository from a conversation.

## Where each section went

Every heading this page used to carry is kept below, pointing at where its
content now lives. They are here so that a link written before the split still
lands on the section it named rather than silently on the top of this page —
which is what GitHub does with a fragment it cannot resolve, and is invisible to
the reader it fails.

Some of them can never be removed, because what cites them is something this
repository cannot rewrite. `#checks` and `#provider-accounts` are written into
the `.yoyodyne/config.yaml` that `yoyo init` generates, in a file its owner
edits. `#product-specifications`, `#precedence`, `#merge-and-removal-semantics`,
`#what-fails-closed`, and
`#converting-an-inheriting-configuration-to-an-explicit-one` are cited from
designs, and `#running-a-work-item-against-the-workflow-definition` from this
project's own `.yoyodyne/workflows/delivery.yaml` — none of which a change to
this guide may rewrite.

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

### Keeping the configuration outside the repository

Moved to [`configuration/setup.md`](configuration/setup.md#keeping-the-configuration-outside-the-repository).

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

### The release-readiness workflow

Moved to [`configuration/goals.md`](configuration/goals.md#the-release-readiness-workflow).

### Architectural invariants

Moved to [`configuration/goals.md`](configuration/goals.md#architectural-invariants).

### Checks

Moved to [`configuration/runs.md`](configuration/runs.md#checks).

### What a developer has to have run

Moved to [`configuration/runs.md`](configuration/runs.md#what-a-developer-has-to-have-run).

### What a check leaves running

Moved to [`configuration/runs.md`](configuration/runs.md#what-a-check-leaves-running).

### The environment a check runs in

Moved to [`configuration/runs.md`](configuration/runs.md#the-environment-a-check-runs-in).

### The environment the harness's own Git and forge commands run in

Moved to [`configuration/runs.md`](configuration/runs.md#the-environment-the-harnesss-own-git-and-forge-commands-run-in).

### Which provider authentication is supported

Moved to [`configuration/runs.md`](configuration/runs.md#which-provider-authentication-is-supported).

### What `init` proposes for `checks`

Moved to [`configuration/runs.md`](configuration/runs.md#what-init-proposes-for-checks).

### How long a check may take

Moved to [`configuration/runs.md`](configuration/runs.md#how-long-a-check-may-take).

### Scheduling ready work

Moved to [`configuration/runs.md`](configuration/runs.md#scheduling-ready-work).

### A developer slot that prefers a label

Moved to [`configuration/runs.md`](configuration/runs.md#a-developer-slot-that-prefers-a-label).

### A developer model chosen by the item's label

Moved to [`configuration/runs.md`](configuration/runs.md#a-developer-model-chosen-by-the-items-label).

### Watching instead of draining

Moved to [`configuration/runs.md`](configuration/runs.md#watching-instead-of-draining).

### When a configuration change takes effect

Moved to [`configuration/runs.md`](configuration/runs.md#when-a-configuration-change-takes-effect).

### Why each run says why it was there

Moved to [`configuration/runs.md`](configuration/runs.md#why-each-run-says-why-it-was-there).

### Running a work item against the workflow definition

Moved to [`configuration/runs.md`](configuration/runs.md#running-a-work-item-against-the-workflow-definition).

### The definition is the project's to own

Moved to [`configuration/runs.md`](configuration/runs.md#the-definition-is-the-projects-to-own).

### Publishing through pull requests

Moved to [`configuration/publishing.md`](configuration/publishing.md#publishing-through-pull-requests).

### Publishing from a fork

Moved to [`configuration/publishing.md`](configuration/publishing.md#publishing-from-a-fork).

### Publishing without automatic integration

Moved to [`configuration/publishing.md`](configuration/publishing.md#publishing-without-automatic-integration).

### Which branch is authoritative

Moved to [`configuration/publishing.md`](configuration/publishing.md#which-branch-is-authoritative).

### A protected target lands through its pull request

Moved to [`configuration/publishing.md`](configuration/publishing.md#a-protected-target-lands-through-its-pull-request).

### What publishing needs

Moved to [`configuration/publishing.md`](configuration/publishing.md#what-publishing-needs).

### Waiting out a provider that refuses

Moved to [`configuration/recovery.md`](configuration/recovery.md#waiting-out-a-provider-that-refuses).

### Serving a turn from a permitted alternate model

Moved to [`configuration/recovery.md`](configuration/recovery.md#serving-a-turn-from-a-permitted-alternate-model).

### Pinning an agent to a model version

Moved to [`configuration/recovery.md`](configuration/recovery.md#pinning-an-agent-to-a-model-version).

### Relaunching a run the provider killed

Moved to [`configuration/recovery.md`](configuration/recovery.md#relaunching-a-run-the-provider-killed).

### Waiting out a network that dropped

Moved to [`configuration/recovery.md`](configuration/recovery.md#waiting-out-a-network-that-dropped).

### Losing a race for the target branch

Moved to [`configuration/publishing.md`](configuration/publishing.md#losing-a-race-for-the-target-branch).

### How long one role may ask another

Moved to [`configuration/agents.md`](configuration/agents.md#how-long-one-role-may-ask-another).

### How far behind a conversation's picture may fall

Moved to [`configuration/agents.md`](configuration/agents.md#how-far-behind-a-conversations-picture-may-fall).

### Queueing a question, or holding it on a side thread

Moved to [`configuration/agents.md`](configuration/agents.md#queueing-a-question-or-holding-it-on-a-side-thread).

### Research sources

Moved to [`configuration/agents.md`](configuration/agents.md#research-sources).

### Reading the repository from a conversation

Moved to [`configuration/agents.md`](configuration/agents.md#reading-the-repository-from-a-conversation).

### Triage thresholds

Moved to [`configuration/recovery.md`](configuration/recovery.md#triage-thresholds).

### What one work item has been given

Moved to [`configuration/recovery.md`](configuration/recovery.md#what-one-work-item-has-been-given).

### What spends a round and what does not

Moved to [`configuration/recovery.md`](configuration/recovery.md#what-spends-a-round-and-what-does-not).

### Crossing a cap the operator decides to cross

Moved to [`configuration/recovery.md`](configuration/recovery.md#crossing-a-cap-the-operator-decides-to-cross).

### A crossing the development manager takes himself

Moved to [`configuration/recovery.md`](configuration/recovery.md#a-crossing-the-development-manager-takes-himself).

### Merge and removal semantics

Moved to [`configuration/setup.md`](configuration/setup.md#merge-and-removal-semantics).

### What fails closed

Moved to [`configuration/setup.md`](configuration/setup.md#what-fails-closed).

### Provider accounts

Moved to [`configuration/agents.md`](configuration/agents.md#provider-accounts).

### Pooling work across several accounts

Moved to [`configuration/agents.md`](configuration/agents.md#pooling-work-across-several-accounts).

### Operators

Moved to [`configuration/agents.md`](configuration/agents.md#operators).

### Reporting to Slack

Moved to [`configuration/agents.md`](configuration/agents.md#reporting-to-slack).

### Avatars

Moved to [`configuration/agents.md`](configuration/agents.md#avatars).

### Services

Moved to [`configuration/agents.md`](configuration/agents.md#services).

### The dashboard's entry

Moved to [`configuration/agents.md`](configuration/agents.md#the-dashboards-entry).

### Recurring tasks

Moved to [`configuration/agents.md`](configuration/agents.md#recurring-tasks).

### A task's own model

Moved to [`configuration/agents.md`](configuration/agents.md#a-tasks-own-model).

### Working the report pile on a cadence

Moved to [`configuration/agents.md`](configuration/agents.md#working-the-report-pile-on-a-cadence).

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
