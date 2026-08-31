# What the delivery pipeline actually guarantees

*For someone changing the harness's execution path, and for whoever comes to
re-express it. Part of [yoyo's documentation](../README.md#further-reading).*

The pipeline in `internal/orchestrator` is the only thing that turns an approved
work item into a promoted change. What it promises has never been written down
in one place: it is spread across five thousand lines of Go, a few hundred
assertions, and [what work flows through](work.md), which describes the path an
operator sees rather than the boundaries the code actually holds.

This document is that enumeration — the delivery paths, the step boundaries, the
events, the durable fields, the side effects, the policy branches, the pause
cases, the retry counters, the reconciliation behavior, and the terminal
outcomes. It is worth having on its own: a specification of the hard-coded
pipeline that a reviewer can read and disagree with. It is worth more as a
baseline, because anything that re-expresses this pipeline — a configurable
executor, a second backend, a rewritten integration step — has to be measured
against what the current one does rather than against what somebody remembers it
doing, and a memory is not something you can diff.

**Nothing here is a decision.** Every line is a description of code that already
exists. Where this document and the code disagree, the code is right and this
document is a defect.

## How this document is kept true

Prose goes stale silently, so the guarantees below are also recorded as
executable traces in `internal/orchestrator/baseline_test.go`, one per delivery
path, under `internal/orchestrator/testdata/baseline/`.

Each trace drives one path end to end against a real Git repository and a fake
provider, and writes down everything the path produced: the outcome the caller
was handed, the durable run record left on disk, the run's event log, every
tracker call and the words the run left on the work item, and each provider
invocation with the prompt it was given. Temporary directories, commit
identifiers, timestamps, the configuration digest, and elapsed times are
replaced by stable placeholders, so what a trace holds is the behavior rather
than the machine it ran on.

The traces are not assertions about the fields somebody thought to check. They
are the whole of what the path produced, which is what makes them useful to a
parity harness: a new executor is compared to the recorded document field by
field, and a field it writes at a different step or does not write at all shows
up whether or not anyone predicted it.

Changing behavior therefore changes a trace, and re-recording is deliberate:

```sh
go test ./internal/orchestrator -run TestDeliveryPipelineBaseline -update-baseline
```

The diff is the review. An intended change reads as the fields it moved; an
unintended one reads as the fields nobody meant to move.

## The entry points

| Entry point | What it is | Covered by a trace |
| --- | --- | --- |
| `Pipeline.Run` on an item with nothing in flight | A fresh run: claim, worktree, develop, gate, promote | yes |
| `Pipeline.Run` on an item with a run in flight | Adopting that run under the same exclusive lease and continuing it from durable state | yes, for the usage-limit pause |
| `Reconciler.Reconcile` | Settling every run no live process owns | yes, for the completed and blocked settlements |
| `yoyo triage repair` (`repaircontinue.go`) | Re-entering a stopped run's repair loop on a granted budget | **no** |
| `yoyo triage rerun` (`rerun.go`) | Starting a fresh run of an item whose last run stopped owing a repair | **no** |
| `BranchReviewer` (`branchreview.go`) | Reviewing a branch outside a run | **no** — not a delivery path |

The three uncovered rows are named rather than omitted. The first two re-enter
the same run machinery through their own preconditions and are part of what a
later executor has to reproduce; a baseline that did not say they were missing
would read as covering them.

## What happens before anything is claimed

`Pipeline.Run` asks a fixed sequence of questions, and the order is itself a
guarantee: each one is cheaper than the one after it, and each refuses before
anything more has been spent.

1. **Wiring and configuration.** A pipeline missing a required collaborator, a
   configuration that does not validate, a developer on a backend with no
   compiled adapter, an agent whose model selector is not pinned, a project with
   no configured checks, and — where integration is automatic — a review policy
   that does not gate it, are each refused here. Nothing is claimed.
2. **The operator's hold on all harness activity.** Read first among the durable
   questions because it is the broadest answer: a held harness starts nothing,
   claims nothing, and does not ask the provider whether it is installed. A run
   already in flight is left in flight rather than resumed into a boundary that
   would park it again.
3. **Publishing.** Whether this run publishes is settled before anything is
   claimed. A project that asked for pull requests and cannot open one fails
   here; a project whose repository has no configured remote degrades to local
   behavior and says so on the outcome as `publish_skipped`.
4. **Provider availability.** Not installed, or not authenticated, refuses.
5. **The work item**, loaded from the tracker.
6. **Unresolved directives that pause this item.** The work stops before it is
   claimed and the outcome names the directive.
7. **Unfinished work this item depends on.** Only a `blocks` dependency counts;
   a parent-child link is not a blocker. The work stops before it is claimed and
   the outcome names the blocking items.
8. **A run already in flight.** Adopting it takes the same exclusive lease a
   fresh reservation takes. It is continued only when its remaining work is
   fully described by durable state — a usage-limit or overload pause, a
   provider the harness stopped on time, a directive pause, a dependency pause,
   an operator hold, or a resumable repair loop under automatic integration —
   and is otherwise refused as `ExistingRunError`.
9. **The intake hold**, asked only here and only for work the harness chose for
   itself. What it holds is the choosing, so a run already under way carries on
   under it and an item the operator named is exempt.
10. **A fresh run substituted for a repair somebody is owed.** An item whose last
    run stopped owing a repair refuses a clean start unless triage claimed the
    re-run.
11. **Item readiness**, the context bundle, the invariants, and the repository's
    readiness for an isolated worktree.
12. **The integration target.** An automatic or publishing run fixes the branch
    it will be promoted into before any work starts, and never infers it
    afterwards.
13. **The run identifier and the provider account.** A pool with nothing left to
    spend refuses here, before a work item has been taken.
14. **Reservation**, which enforces `execution.max_concurrent_developers`.
15. **The claim**, then the item is re-validated against what was claimed and the
    context bundle is re-assembled from it.
16. **The worktree**, cut from the target branch, and the run's state written as
    `running` in the `developing` phase.

## The steps of a run

| Phase | What it does | What it writes |
| --- | --- | --- |
| `developing` | One developer invocation in the run's worktree, resuming the run's session on every attempt after the first | `provider_session_id`, `provider_model`, `provider_resolved_model`, `changes`, `last_sequence` |
| `checking` | The protected-path gate first, then every configured check in order | `path_refusal` or `check_failure` while one is outstanding, and clears the other two when a gate passes |
| `reviewing` | One independent review invocation, its own session, no tools | `review_session_id`, `review_model`, `review_resolved_model`, `review_decision`, `review_summary`, `review_findings`, `review_finding_details`, `review_rounds` |
| `integrating` | Under the target branch's promotion lease: commit, fast-forward the local target, publish and merge where the project publishes | `harness_commit`, `integration`, `pull_request` |
| `completing` | Record the outcome on the item, close it, price it | the tracker's record and closure |
| `cleaning_up` | Remove the worktree and the branch, each recorded separately | `worktree_removed`, `branch_removed` |
| `complete` | Nothing outstanding | `completed_at` |

A run whose integration a person still approves stops after `checking`: it
records the outcome, leaves the change on its branch, and closes nothing.

### The repair loop

Under automatic integration the whole gate is a loop, and every round asks the
directive and dependency questions again before it spends another developer
invocation. Three different failures are repair input for the same developer,
and all three draw on **one** budget, `execution.repair_attempts_before_replan`:

1. **A protected-path refusal**, decided before the checks run. Recording it
   clears any check failure and any findings, because those describe a change
   the gate has already moved past.
2. **A failing check**, decided before the reviewer is asked. Recording it clears
   the findings for the same reason. A check that could not run at all — timed
   out, cancelled, or infrastructure that failed — is not repair input and ends
   the run.
3. **A reviewer's repair verdict**, with its findings recorded in full because
   they are the next attempt's input.

Sharing one budget is what bounds the total developer invocations a run can
make. The attempt is recorded before the developer is invoked, so an interrupted
attempt still counts and a restart cannot buy a fresh budget. A run that spends
the budget blocks the item and preserves the worktree and the branch.

Triage can grant further attempts on a stopped run; the granted attempts add to
the configured budget rather than replacing it, and `repair_attempts` goes on
meaning every attempt this run has handed back.

### What the promotion re-earns

An approval authorizes integration only when it demonstrably came from a second
invocation: missing or reused provider identity fails the run rather than
promoting it.

A promotion that loses its race for the target branch is re-prepared rather than
failed. The change is replayed onto where the target went, the published branch
is replaced from exactly the commit the harness put there, the approval is
discarded, and the whole gate runs again — the checks against the replayed
change and a fresh independent verdict on it. The re-review is not a round the
item spent, because it judged the same developer attempt on moved ground, and
the replay spends no repair attempt. A replay that conflicts is never resolved
by the harness: both sides are left intact and the item is blocked.

## The pause cases

A pause is not a failure. The run stays in flight with its claim, worktree,
branch, and developer session preserved, and the record on disk says what it is
waiting for so a later invocation resumes it rather than starting a second
attempt.

| Pause | Where it is read | What lifts it | On the outcome |
| --- | --- | --- | --- |
| Exhausted provider usage limit | Every developer and reviewer invocation | The deadline the provider named, or `yoyo resume` | `paused`, `pause_cause: usage_limit`, `usage_limit_resets_at`, `usage_limit_kind` |
| Transient provider server overload | The same boundaries | The configured overload interval | `paused`, `pause_cause: server_overload` |
| A provider invocation the harness stopped on time | After an invocation that stalled or exhausted its budget | Nothing; it is runnable immediately | `paused`, `provider_stop` |
| An unresolved user directive | Before the claim, before a resume, and at every round of the gate including the promotion | Somebody settling the directive | `paused`, `paused_by_directive` |
| Unfinished work the item depends on | The same boundaries | That work closing, or the link being removed | `paused`, `paused_by_dependency` |
| The operator's hold on harness activity | Every provider-call boundary | The operator lifting it | `paused`, `paused_by_operator`, `pause_cause: operator_hold` |
| The operator's hold on intake | Only where a run would be started for a reason other than the operator naming the item | The operator lifting it | `paused`, `paused_by_intake`, and **no run at all** |

The directive, the dependency, and both holds can also stop work before there is
a run: nothing is claimed and no worktree exists, so the outcome names the work
item and what stopped it and nothing else.

Two bounds govern waiting. `execution.usage_limit_in_process_pause` is how much
of a wait this process will hold open — a longer one exits with the run still in
flight, to be picked up later — and `execution.usage_limit_max_pause` bounds the
waiting one run may commit to across every pause it takes, recorded in
`usage_limit_paused_seconds` so a restart part-way through cannot buy a fresh
budget. Time an operator hold accounts for is kept apart in
`operator_held_seconds` and is bounded by nothing: a maximum pause that stopped
a held run would be the harness overriding the operator.

## The counters, and what each one bounds

| Counter | Configured by | What it bounds | What it is evidence about |
| --- | --- | --- | --- |
| `repair_attempts` | `execution.repair_attempts_before_replan` | Developer invocations spent on failures of the change | The change |
| `integration_retries` | `execution.integration_retries_before_reconciliation` | Promotions re-prepared after losing the target branch | The target branch moving |
| `transient_relaunches` | `execution.transient_relaunches_before_blocking` | Provider invocations reissued after one died without judging the work; the developer and the reviewer share it | The provider |
| `usage_limit_paused_seconds` | `execution.usage_limit_max_pause` | Total waiting committed across every pause | The provider's capacity |
| `review_rounds` | nothing in the run; `triage.review_rounds_cap` is what triage measures it against | Nothing the run stops on — it counts verdicts obtained for this **work item** | What the work has cost |

The first four are per run and go with it, and each is what stops the run when it
is spent. `review_rounds` is per item, is counted by the run and bounded by
nobody inside it, and survives a run ending — which is why a repair grant is
truncated against it rather than against a run's own budget.

Each counter is recorded **before** the thing it bounds happens, so a process
that dies mid-attempt resumes against the budget it had.

## What a run does outside itself

| Side effect | When |
| --- | --- |
| `Tracker.Claim` | Once, before the worktree is cut |
| `Tracker.RecordOutcome` | On success, on failure, and on every pause of a claimed run |
| `Tracker.Complete` | Only on a promotion that is where it will stay; a merge the forge only queued defers the closure to reconciliation |
| `Tracker.Block` | On every stoppage a person has to decide about, on its own deadline rather than the run's context |
| Git: worktree created, harness commit, local target fast-forwarded, branch and worktree removed | The phases above |
| Forge: branch pushed, pull request opened or updated, merge requested, remote target observed | Only where the project publishes |
| Run record and event log | Every step boundary |
| Spend log, price on the item, reports, amendments, triage docket | Where each is wired; a pipeline without one runs identically and names what it could not keep |

## The events

The run's log carries what the harness itself observed. From a run driven with a
fake provider that is `command.started` and `command.completed` per check, and
`review.started` and `review.completed` per review invocation, plus
`review.drift` where a reviewer answered with a field the verdict schema does not
define. A real provider adapter adds its own stream — `agent.message`,
`process.output`, `file.changed`, and the `run.*` terminals that carry what an
invocation cost — so the recorded traces are the harness's half of the log
rather than the whole of it.

Events are append-only and never rewritten. `last_sequence` on the run record is
where the log had reached, and it is what a resumed invocation continues from.

## Terminal outcomes

| Status | Reached by |
| --- | --- |
| `succeeded` | The gate passed and, where integration is automatic, the promotion landed |
| `failed` | Anything the run could not carry further, including a spent budget |
| `cancelled` | The operator asked the run to stop, or the context was cancelled |
| `timed_out` | The context's deadline passed, or the harness stopped a process on time |

A terminal run is not the only outcome. A paused run is `running` and still in
flight; a held or directive-stopped item may have no run at all.

Three things are reported on a succeeded run rather than turning it into a
failure, because the work is already integrated and the item already closed:
`publish_failure` (an outstanding publication), `cleanup_failure` (an artifact
that survives, or a removal that could not be confirmed — `worktree_removed` and
`branch_removed` tell those apart), and `completion_recording_failure` (the final
record arrived late).

An **environmental** refusal is the one stoppage that leaves the item's budgets
where they were: the environment refused the round rather than the work failing,
so a caller that reported the blocker without it would say the item had spent a
round it never spent.

## Reconciliation

`Reconciler.Reconcile` settles every run no live process owns. It takes each
run's lease and re-reads its state under it, so nothing is decided from a
listing another process has moved on from. **It never starts a developer.**

| Action | When |
| --- | --- |
| `held` | A live process holds the run; it is left alone |
| `resumable` | The run's own pipeline can continue it: a repair loop with durable state, a usage-limit or overload pause, a directive pause, a dependency pause, an operator hold, or a provider stopped on time |
| `queued` | The forge accepted a merge it has not performed; nothing can be decided until it does |
| `completed` | The work is promoted — recorded, or found in the target branch by containment — so the item is closed and the artifacts removed |
| `blocked` | Nothing could finish the run; the item carries a durable blocker and the work is preserved |
| `failed` | The run left nothing behind for anyone to act on |
| `unsettled` | Settlement itself could not finish; the run stays outstanding for the next sweep |

An interruption inside the integration step is the one boundary durable state
cannot describe — the promotion either landed or it did not — and only the
repository can answer. A recorded integration that the repository contradicts is
blocked rather than believed. A second sweep over a settled run does nothing:
re-closing or re-blocking an item is exactly what a sweep must not do.

## The recorded scenarios

| Trace | What it freezes |
| --- | --- |
| `human-approved-change-is-preserved-for-its-approver` | The non-automatic path stops after the checks and promotes, closes, and cleans up nothing |
| `automatic-run-promotes-reviews-closes-and-cleans-up` | The whole happy path, including both cleanup steps |
| `failing-check-is-repaired-and-then-promoted` | A check failure as repair input, and the durable failure cleared once it passes |
| `failing-check-spends-the-repair-budget-and-blocks` | The repair budget, and what a blocked run leaves behind |
| `review-findings-are-repaired-and-then-promoted` | Findings returned to the same developer and re-reviewed by a fresh invocation |
| `review-findings-spend-the-repair-budget-and-block` | The same budget reached through the reviewer |
| `protected-path-refusal-is-repaired-before-any-check-runs` | The gate deciding before the checks, on the shared budget |
| `protected-path-grant-admits-the-change-it-names` | A grant in the item's own text admitting exactly that path |
| `usage-limit-pause-exits-resumable-and-a-later-invocation-finishes-it` | The pause, the durable deadline, and the resume continuing the same run and session |
| `server-overload-pause-reissues-the-same-attempt` | An overload told apart from an exhausted account |
| `transient-provider-death-relaunches-without-charging-the-developer` | The relaunch budget spending no repair attempt |
| `transient-deaths-spend-the-relaunch-budget-and-block` | The relaunch budget's bound |
| `unresolved-directive-pauses-the-work-before-anything-is-claimed` | A pause with no run behind it |
| `unfinished-dependency-pauses-the-work-before-anything-is-claimed` | The same, and that a parent-child link is not a blocker |
| `operator-hold-starts-nothing-at-all` | The hold read before the provider is asked anything |
| `intake-hold-starts-nothing-the-harness-chose` | The narrower hold, on the choosing rather than the work |
| `promotion-is-replayed-when-the-target-branch-moves` | The replay re-earning the whole gate |
| `integration-retries-are-bounded-and-block-the-item` | The retry budget |
| `reconciliation-completes-a-run-interrupted-inside-integration` | Settlement from the repository rather than the record |
| `reconciliation-blocks-a-run-interrupted-while-developing` | Settlement as a blocker, and a second sweep finding nothing |

## What this baseline does not yet cover

Named so that a parity harness measuring against it knows what it is not
measuring:

- The publishing half — pull requests, queued merges, the remote target, and the
  catch-up after a forge merge. It has its own assertions in
  `internal/orchestrator/publish_test.go` and no trace here.
- `yoyo triage repair` and `yoyo triage rerun`, which re-enter a stopped run.
- The environmental classification of a refused round.
- The spend log, the price on the item, collected reports, proposed amendments,
  and the triage docket, none of which are wired in the recorded scenarios.
- A real provider adapter's event stream, which the fake provider does not emit.
