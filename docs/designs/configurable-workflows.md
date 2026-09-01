---
id: configurable-workflows
kind: design
title: "Configurable workflows: a declarative runtime over trusted actions"
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-08-31T15:56:14Z
      reason: promoted from the operator's configurable-workflows brief (yoyodyne-ifd.210) with the twenty open questions settled; the authority model's reconciliation with the fixed-roles ruling is recorded in the authority-by-capability decision record
    - action: amended
      by: architect
      at: 2026-09-01T19:15:00Z
      reason: approved amendment 8f75545e from yoyodyne-ifd.209.10 - the question the developer had to decide alone is answered in the design; a capability is harness-exercised on the role's behalf, and call-site conversion proceeds on that reading
    - action: amended
      by: architect
      at: 2026-09-01T19:15:00Z
      reason: approved amendment 2268eabc from yoyodyne-ifd.209.15 - settled question 19 now distinguishes agent-judged gate evidence, which needs an assessor contract, from deterministic action outputs, which do not; the release-readiness gate is complete under this reading
---

# Configurable workflows: a declarative runtime over trusted actions

## What this is for

The delivery loop's topology is Go control flow: a project can configure checks, agents, models, and budgets, but cannot read or change the sequence as an artifact. This design makes the workflows Yoyodyne already executes declarative, versioned, validated, resumable, and visible — serving the goals that roles and policies stay configurable without safety invariants becoming optional, that the operator can see what runs on their behalf, and that it works on other people's projects, whose sequences will eventually differ. Behavior parity comes first: the existing delivery loop is represented before anything new is expressible.

## The trust model

Five layers, in descending configurability. A **workflow definition** (project-owned) selects step names, registered actions, schema-permitted parameters, outcome transitions, budgets, and terminals. An **agent profile** (project-owned) binds a logical agent to a role contract, backend, model, persona, context policy, triggers, and workflow participation — narrowing its role, never widening it. A **role definition** (protected, operator-controlled) composes named capability primitives with scopes and separation constraints. **Capabilities and actions** (Go, not configurable) define what reading, mutating, evidencing, publishing, and integrating *mean* and enforce it. The **runtime envelope** (Go, not configurable) wraps every action in leases, holds, directives, cancellation, persistence, redaction, idempotency, and revision binding — guarantees around actions, never optional steps. Configuration selects sequence; it cannot grant capability. That sentence is enforced by the invariant `configuration-never-grants-authority`.

A capability is authority the harness exercises on the role's behalf, never authority the agent wields. A capability in a bundle means: when this role, through a registered action, requests that class of operation, the harness performs it subject to the capability's scope and the runtime separation policy. Roles decide, the harness performs — the same boundary the Git model states — and the authority workstream's call-site conversion proceeds on this reading.

## Definitions and validation

The schema is a state machine, not a DAG: sequential steps, conditional transitions on typed action outcomes, bounded loops, durable waits (`$wait`, a runtime destination no definition can replace), and named terminals. No expressions, no parallel joins, no arbitrary code; action parameters are typed literals and configuration references only. Loading produces a compiled, normalized, digest-pinned definition or a complete failure before any work is claimed. Validation covers the structural set (known schema and actions, every outcome handled, every destination real, no unreachable steps, every cycle crossing a declared budget or trusted wait) and the safety set (capability compatibility, automatic integration requiring checks and an independent reviewer, no project shell outside the configured-check action, bounded size). `yoyo workflow validate` and `show --effective` are the inspection surface; a binding names a project file or a built-in template explicitly, and nothing is silently merged.

## Actions and evidence

Actions are registered in Go with a descriptor — subject types, outcomes, parameter schema, required capabilities, mutation class, evidence inputs and outputs, revision behavior, idempotency — and are deliberately coarse: `candidate.integrate` is one operation that takes the promotion lease, verifies authorization evidence, integrates under existing policy, records, and releases. Security-sensitive internals are never assembled from individually optional pieces. An action returns an outcome; only the definition maps outcomes to destinations.

Evidence is a typed fact bound to a revision — protected paths accepted for candidate X; checks passed for X under check-set digest Y; reviewer R approved X against item revision Z; the target stood at commit W when authorization was evaluated — minted only by the trusted action that performed the operation, verified by later actions for producer, type, subject, revision, configuration digest, and invalidation. Any new candidate revision invalidates all candidate-bound evidence; target movement invalidates the target-position evidence alone and drives replay; a changed check set invalidates check evidence. Static validation catches bad paths first; the runtime proof is the final boundary, per `integration-requires-revision-bound-evidence`.

## The runtime

One durable transition at a time: load the instance and its pinned definition; take the instance lease; re-read holds, directives, cancellation, and subject revision at the boundary; create or recover the step-attempt record and idempotency key; invoke; persist outcome, evidence, cost, and side-effect identifiers; resolve the transition from the *pinned* definition; atomically record the next step before releasing the lease; emit the canonical event the read model consumes. Reconciliation after a death reads the attempt and side-effect evidence and adopts, safely re-executes under the same key, or records an operator-visible ambiguity — it never assumes an unrecorded action did nothing. Instances are pinned to workflow ID, revision, schema version, and content digest before the first action; edits apply to new instances through an explicit reload boundary; deleting a file strands nothing because the pinned definition is durable; V1 performs no automatic migration.

## The authority model

Sequenced as the brief's authority workstream, with its guard binding: the authority inventory and the capability registry land before anything is configurable; the five roles are expressed as shipped default bundles that reproduce current behavior exactly, checked against the inventory; authorization call sites convert from role names to capability-and-scope checks, with separation policy (reviewer independence, no self-approval, no evidence self-minting) as runtime rules a static bundle cannot prove; only then do protected operator-defined bundles load; and the closed role-name type is removed last, with compatibility decoding for durable records. Every invocation pins the role-contract revision and digest that authorized it; **authority changes never apply to an in-flight step attempt** — new invocations only, no exceptions, no migration of authority ever.

Role definitions may live in the repository under `.yoyodyne/roles/` with two hard properties: the protected-path gate refuses any grant naming that directory — an absolute exception to the grant mechanism, with no decided-change override — and no definition is effective until operator-authorized activation records its digest, so the *activated digest* is the authority and a file changed by anyone is inert until a person activates it. Audit history is a CLI surface, never an agent one.

## Profiles, context, triggers, and the Sentinel

Every agent eventually shares one chassis — identity, role contract, backend, model, persona, context policy, triggers, typed outputs, budgets — without authorities becoming interchangeable. Context continuity is `invocation`, `subject`, or `agent`; agent continuity is durable-state reconstruction, never a provider session (`durable-state-is-provider-independent` already binds this), each invocation recording the context revision it read and produced, with compaction keeping provenance. Triggers — workflow, request, event, schedule — decide *when* an agent gets an opportunity to judge, never what it must conclude; a trigger class must be permitted by the validated role contract.

The Sentinel is the first specialist: an observer bundle — read canonical evidence, read-write its own context, publish operator alerts — with no project-state mutation, no gate evidence, no verdicts. Its alerting is model-judged by design: no configured severity, keyword, or corroboration rule gates an alert, and the compiler refuses a definition that routes the `alert` outcome anywhere but the trusted publisher. An alert is a distinct record projected into the report stream; operator feedback is append-only, linked to the alert, and derives a versioned, inspectable, reversible preference profile — a negative example, never a suppression rule. A continuous observer and a gate-holding assessor are different agents even when they share a domain, because accumulation and independence are incompatible in one identity.

## Conversion order and the recorded trade

Delivery first — it is bounded, tested, safety-critical, and durable — behind the brief's parity discipline: freeze the behavioral baseline, wrap existing code as actions unchanged, run the compiled definition against the baseline, keep an opt-in trial alongside the old path, then default for new runs with legacy resume preserved; existing incomplete runs stay resumable throughout. Triage second, preserving the docket's caps and authority. Management requests third: **ifd.142 proceeds bespoke, per the operator's recorded trade, and converts here — its durable records must therefore map onto instances and interrupts without loss, which constrains 142's persistence now.** The specialist substrate and Sentinel fourth. Parallel branches, fan-out, visual authoring, and new gate types wait for demonstrated need.

## The twenty questions, settled

1. The run record remains authoritative for delivery-specific facts (worktree, branch, publication); instances own topology position; step attempts own attempt-level provider evidence. One home per fact, instance referencing run ID.
2. Pinned definitions are stored once, content-addressed by digest; instances carry the digest and source identity.
3. An operation is a configurable action when reordering or gating it is meaningful and evidence gates keep it safe; waits, leases, redaction, prompt assembly, and evidence assembly stay runtime-internal. The brief's example decomposition is the initial registry.
4. Typed literals and configuration references. No expression language.
5. Human integration is a terminal preserved outcome, as the example shows — the operator's integration happens outside the harness, so an interrupt would promise a resumption that never comes, and two policy-selected workflows double maintenance for one branch.
6. New projects get materialized workflow files from `init`; existing projects keep built-in selection until they explicitly eject a copy. Either way the effective definition and digest are inspectable and nothing merges silently.
7. An explicit reload verb through the canonical service (restart also suffices). No file watching in V1.
8. `runstate.Phase` values become compatibility projections through one translation table owned by the read model.
9. The four evidence types above, plus review-independence evidence and publication state; invalidation rules as stated in the evidence section. Integration does not migrate until all are minted and verified by the wrapped actions.
10. Subworkflows, when they arrive, get nested instance identity with a parent reference — auditability over namespace convenience.
11. The rule, not an enumeration: changes not touching an instance's current step, completed path, budget semantics, or evidence requirements are migration-eligible; everything else starts a new instance. V1 migrates nothing automatically.
12. Workflow selection becomes a development-manager recommendation only after delivery, triage, and management conversions are proven and under the management-loop protocol's evidence-gated pattern; static binding until then.
13. The capability vocabulary is derived from the authority inventory (workstream step 1), not invented ahead of it; the scope model is settled now — a capability is a primitive ID plus a typed scope (evidence class, artifact kind, tool permission set, alert channel).
14. Repository residence under `.yoyodyne/roles/` with the grant-refusal and activation-digest properties above; operator-only activation; CLI audit surface.
15. Agent context is an append-only revision log per store under the state root; compaction produces a new revision recording its sources; retention details settle with the implementing milestone.
16. Event subscriptions and workflow bindings are project configuration; schedules are operator-local policy, like account pools; both activate at the same explicit reload boundary, new invocations only.
17. An alert is a distinct record projected into the report stream — reports are write-once and decide nothing, and grafting delivery state and feedback onto the simplest channel we have would complicate it for everything else.
18. Preferences promote product-to-global by an explicit operator command that copies the derived preference and never the evidence; the global profile records provenance by ID reference only.
19. The registered-assessor pairing binds gate evidence produced by an agent's judgment: such evidence requires an assessor contract granting that evidence authority, and the compiler enforces the pairing. Evidence minted by a deterministic registered action needs no assessor contract — the action registry is its authority and its output is typed evidence like any other — so a gate built entirely from deterministic actions is complete as built. An agent output no workflow gates on is advisory.
20. Never. An in-flight step attempt keeps the authority that authorized it; role-definition changes reach new invocations only.
