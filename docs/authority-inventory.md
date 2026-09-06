# The authority inventory

*For someone changing yoyo itself, and for anyone re-expressing what a role may
do. Part of [working on yoyo itself](developing-yoyo.md).*

This is every place the harness enforces role authority, named one row at a
time: where the check lives, which role it binds, and what it refuses. It is the
statement of what is true today rather than a design for what should be true,
and the point of writing it down is that nothing can be re-expressed as
capabilities before what exists has been stated. The capability vocabulary in
`internal/capability` says as much in its own words: the full inventory belongs
to the authority workstream, derived from what the harness actually authorizes
rather than from what looks tidy in advance.

A role here is one of the five the harness has —
product manager, architect, development manager, developer, reviewer — and a
row's **Binds** column names whose authority the check enforces. Some rows bind
*the harness*: a guard that stops the harness itself doing two of something at
once, or doing one of them where no agent can reach it, is authority in the same
sense and belongs in the same list.

## How this stays true

`internal/authority` reads the two tables below and holds them against the code,
under `make test`, so this is a check rather than a document somebody remembers
to update. It fails in four ways:

- a row naming a file that is not there, or a declaration that file no longer
  declares — which is a listed check that moved or was renamed;
- a place in the code that looks like an authorization site and is in neither
  table — which is a new check nobody listed;
- an exemption for something the sweep no longer finds, which is a sentence that
  has stopped being true;
- one declaration in both tables, which is two answers to one question.

What the sweep looks for is deliberately narrow, and it is a floor rather than a
fence. It finds a function that names a role and constructs a refusal, anything
whose declared name carries `authoriz` or `authorit`, and the three boundaries
that bind a role without ever naming one: `protect`, `independen`, and `lease`.
A check that does none of those things is caught by a reviewer and not by this,
which is the same bargain the coined-terms register makes and for the same
reason: no deterministic rule tells an authorization site from an ordinary
refusal, so the mechanical part covers what it can and says so.

The inventory is broader than the sweep. Several rows below — the contracts, the
grant marker, the promotion under lease — are listed and pinned without the
sweep finding them, because listing is what makes them reviewable and pinning is
what stops them moving silently.

## What re-expresses it

[`internal/rolecapability`](../internal/rolecapability/rolecapability.go) is this
document said once more, in capabilities rather than in role names: five bundles,
one per role, built from the vocabulary
[`internal/capability`](../internal/capability/capability.go) declares. It is held
to the inventory by a table that answers every row here with the capability
question that row becomes, or with the reason there is not one. A row added below
and left unanswered fails that check, which is what stops the re-expression
quietly falling behind the thing it re-expresses.

The rows that turn on a role's identity now read their answer out of it rather
than naming a role: which role owns a kind of document
(`artifact.owner`, `artifact.authorize`, `artifact.unauthorized-revisions`, and
the three store rows underneath them), which may change an invariant
(`invariant.authorize` and everything routed through it), what a role may ask for
in a conversation (`conversation.authority-derived`, which the rest of the
conversation rows read through), and whose judgement gates an integration
(`review.policy`). Every other row is answered in that table with the reason a
bundle cannot express it — a contract's prose, a path gate, the separation of two
invocations, a posture, or an authority no role holds at all — and those rows
still name what they name.

Two capabilities belong to no role. The promotion lease and the branch it admits a
move onto are the harness's, as `promotion.lease`, `run.integrate-under-lease`,
and `converge.catch-up-under-lease` below already say and as
[`one-promotion-per-target-branch`](decisions/invariants/one-promotion-per-target-branch.md)
requires: no agent performs a promotion, so no role's bundle confers one.

## What holds the separation rules

Three of the rows below enforce a rule about two things rather than about one
role: the reviewer is never the author, two demonstrably independent provider
invocations gate an integration, and the roles that authorize a promotion cannot
perform one. Each is true today because of where Go control flow puts things —
`review.independent-invocation`, `run.independent-invocations`, and the three
lease rows — and none of that survives a sequence read out of a file.

[`internal/separation`](../internal/separation/separation.go) is those rules
written as named policies over the same vocabulary the bundles are written in,
held against whatever topology a workflow definition chooses. The `separation.*`
rows below are those policies, and `workflow.separation-at-compile` is where a
definition meets them. They do not replace the rows they come from: those bind
the one sequence the harness runs, and these bind every sequence a definition
could ask for.

## The inventory

| Check | Binds | File | Declaration | Refuses |
| --- | --- | --- | --- | --- |
| conversation.authority-table | every role | `internal/chat/role.go` | `authorities` | Anything a role's row does not carry. The table is the whole of what each role may ask for in a conversation, in Go so that a persona cannot widen it. |
| conversation.authority-derived | every role | `internal/chat/role.go` | `buildAuthorities` | Reading the table above off anything but the role-capability registry. Every flag on a row is one capability the role holds, the tracker actions are the operations whose capability it holds, and the parent requirement is decomposition without admission — so what a role may ask for in a conversation and what a role may do are one statement rather than two that can drift. |
| conversation.role-is-known | every role | `internal/chat/role.go` | `AuthorityFor` | A role the table has no row for: it has no contract to send and no statement of what it may ask for. |
| conversation.session-opens | every role | `internal/chat/chat.go` | `(Options).validate` | Opening a conversation at all for a role the table has no row for. |
| conversation.authorize-reply | every role | `internal/chat/role.go` | `(*Session).authorize` | A parsed reply's proposals, concerns, research queries, evaluation, ask, or tracker actions that the role has no authority for. Nothing in the block is carried out. |
| conversation.tracker-action | every role | `internal/chat/role.go` | `(Authority).MayAct` | A tracker action outside the role's own list; one unauthorized action refuses the whole block. |
| conversation.contract | every role | `internal/chat/role.go` | `SystemPrompt` | A configured persona standing in for the contract: the contract is sent verbatim and first on every turn, including a resumed one, and the persona only ever follows it. |
| conversation.contract.product-manager | the product manager | `internal/chat/chat.go` | `productManagerContract` | Anything outside what the product manager owns — the brief, the goals, and what is admitted to the backlog and in what order. |
| conversation.contract.architect | the architect | `internal/chat/role.go` | `architectContract` | Editing any document from the conversation, deciding product intent, and decomposing work; the architect proposes upstream changes instead. |
| conversation.contract.development-manager | the development manager | `internal/chat/role.go` | `developmentManagerContract` | Admitting work, reordering the backlog, closing or retiring an item, and overriding a reviewer's verdict. |
| conversation.contract.developer | the developer | `internal/chat/role.go` | `developerContract` | Describing a conversation as though it implemented, changed, or verified anything: there is no worktree and no run here. |
| conversation.contract.reviewer | the reviewer | `internal/chat/role.go` | `reviewerContract` | Describing a conversation as though it approved, rejected, or gated anything: there is no change here and no verdict. |
| conversation.admission-gate | the product manager | `internal/chat/tracker.go` | `(*Session).admissionRefusal` | A `create` that would admit work where the project asks about every item, or where the goal it names is not one the operator approved. Decomposition underneath an admitted parent is deliberately not admission. |
| conversation.parent-required | the development manager | `internal/chat/role.go` | `(*Session).authorize` | A `create` or `reparent` naming no parent, which is the boundary between decomposing admitted work and admitting new work. |
| conversation.escalation-is-reported | the development manager | `internal/chat/triage.go` | `refuseUnreportedEscalation` | A triage escalation carrying no report at warning severity or above, so an item is never left blocked by a decision nobody was told about. |
| conversation.triage-run-belongs-to-item | the development manager | `internal/chat/triage.go` | `(*Session).refuseTransposedStoppage` | A triage decision whose named run was made for another work item. |
| conversation.crossing-is-justified | the development manager | `internal/chat/triage.go` | `(TrackerAction).crossingProblems` | A `cross` decision carrying no reason, or naming no cap or one nothing bounds, and a `budget` on any other decision. The justification is what reaches the operator, so a crossing without one is refused outright rather than recorded weakly. |
| triage.delegated-crossing | the development manager | `internal/runstate/triageoverride.go` | `(*TriageStore).CrossCap` | A crossing by any role but the development manager, one past `MaxDelegatedCapCrossings` for the item, one that would clear a budget or raise it by more than a step, and one carrying no reason. The refusal past the bound names `yoyo triage override`, which is the operator's again. |
| triage.override-is-the-operators | the operator | `internal/runstate/triageoverride.go` | `(*TriageStore).Override` | An override arriving with a delegated crossing on it, which would take the bounded path through the unbounded one. |
| triage.crossing-record | the development manager | `internal/runstate/triageoverride.go` | `(TriageOverride).Validate` | The same demand made of the durable record, so a crossing hand-edited to name another role, to clear a budget, or to argue nothing is refused as the record is read rather than only as it is written. |
| exchange.ask-authority | every role | `internal/chat/exchange.go` | `refuseUnauthorizedAsk` | An ask from a role not on the channel, an ask a role puts to its own role, and an ask put to a role not on the channel. |
| exchange.asking-contract | the asking role | `internal/exchange/contract.go` | `AskingContract` | An ask that is anything but judgment-only, decisionless, and durably recorded. |
| exchange.answering-contract | the answering role | `internal/exchange/contract.go` | `AnsweringContract` | Any authority in an answer: an answer admits nothing, orders nothing, edits nothing, and resolves nothing. |
| exchange.answer-carries-no-authority | the answering role | `internal/exchange/exchange.go` | `ReadAnswer` | An answer carrying any harness block at all, whole, whether or not the answering role read the contract. |
| exchange.answering-prompt | the answering role | `internal/chat/exchange.go` | `AnsweringPrompt` | The role's own conversation contract standing in here: what is sent is who the role is and the boundary, not blocks and authority that do not exist in an exchange. |
| artifact.owner | the product manager, the architect | `internal/artifact/ownership.go` | `Owner` | A kind of document nobody owns: an unknown kind has no owner rather than a default one. |
| artifact.authorize | every role | `internal/artifact/ownership.go` | `Authorize` | Creating, amending, superseding, or retiring an artifact by any role but the one that owns its kind. |
| artifact.unauthorized-error | every role | `internal/artifact/ownership.go` | `ErrUnauthorized` | What every refused artifact mutation returns, so a caller can tell refused authority from a malformed document. |
| artifact.unauthorized-revisions | every role | `internal/artifact/ownership.go` | `UnauthorizedRevisions` | Reports rather than refuses: a revision log entry recorded under a role that does not own the document. The log is append-only, so this is a named problem instead of a document that can neither load nor be lawfully corrected. |
| artifact.create | every role | `internal/artifact/store.go` | `(Store).Create` | A creation the owner did not make; the authority is checked before anything reaches the filesystem. |
| artifact.amend | every role | `internal/artifact/store.go` | `(Store).Amend` | An amendment the owner did not make. |
| artifact.supersede-or-retire | every role | `internal/artifact/store.go` | `(Store).end` | The half `Supersede` and `Retire` share: ending a document under a role that does not own it. |
| invariant.authorize | every role | `internal/invariant/invariant.go` | `Authorize` | Creating, amending, or retiring an architectural invariant by any role but the architect. |
| invariant.unauthorized-error | every role | `internal/invariant/invariant.go` | `ErrUnauthorized` | What every refused invariant mutation returns. |
| invariant.revision-authorized | the architect | `internal/invariant/invariant.go` | `(Revision).Validate` | A recorded revision naming a role that is not the architect, so the authority a change was made under is checkable afterwards rather than merely intended. |
| invariant.create | every role | `internal/invariant/store.go` | `(Store).Create` | A new invariant from any role but the architect. |
| invariant.amend | every role | `internal/invariant/store.go` | `(Store).Amend` | An amendment from any role but the architect. |
| invariant.retire | every role | `internal/invariant/store.go` | `(Store).Retire` | A retirement from any role but the architect. |
| amendment.decide-under-owner | every role | `internal/amendment/decision.go` | `(Proposal).Decide` | A decision taken under any authority but the owning role's; the authority follows from the document rather than being supplied. |
| amendment.operator-decides | every role | `internal/cli/amendment.go` | `decideAmendment` | An operator naming the authority a change is decided under: it is the artifact's owner and is never asked for, so a design change cannot be decided under the product manager's authority. |
| protectedpath.set | the developer | `internal/protectedpath/protectedpath.go` | `Protect` | Builds the default-deny set: the harness's own project directory, the artifact homes the roles upstream of a developer own, and the derived exports the harness holds out of every run's change. |
| protectedpath.refused | the developer | `internal/protectedpath/protectedpath.go` | `(Set).Refused` | Every path a change touched that sits inside a protected home, or is a held export, and the work item did not grant. |
| protectedpath.grants | the developer | `internal/protectedpath/protectedpath.go` | `Grants` | A grant that ordinary prose could produce: the marker has to open its own line, so a sentence discussing the paths grants nothing. |
| protectedpath.grant-marker | the developer | `internal/protectedpath/protectedpath.go` | `GrantMarker` | The one token a work item admits a protected path with, deliberately unlovely so it cannot be written by accident. |
| protectedpath.grant-instruction | the developer | `internal/protectedpath/protectedpath.go` | `GrantInstruction` | What a refused developer is told: the grant goes in the work item, and nothing the developer writes grants a path. |
| protectedpath.provider-paths | the developer | `internal/protectedpath/provider.go` | `ProviderPaths` | The paths no grant reaches, because the provider refuses an agent's writes to them above anything this harness permits. |
| protectedpath.beyond-grant | the developer | `internal/protectedpath/provider.go` | `BeyondGrant` | Reports which recorded provider paths a set of grants reaches for, a directory grant included. |
| protectedpath.grant-problems | the product manager, the development manager | `internal/protectedpath/provider.go` | `GrantProblems` | A work item granting a path the provider refuses, asked at every door into the queue so an item is not admitted for work no run can do. |
| run.gate-protected-paths | the developer | `internal/orchestrator/pipeline.go` | `(*activeRun).gateProtectedPaths` | A developer's change that touched a protected path the item did not grant, before a check suite is spent on it. |
| run.grant-evidence | the developer | `internal/orchestrator/pipeline.go` | `grantEvidence` | A grant read from an item's notes: only the four fields somebody authored count, because the harness and the agents write into the notes. |
| run.refuse-provider-grant | the developer | `internal/orchestrator/pipeline.go` | `refuseProviderGrant` | Starting on an item whose grant names a path no provider honours, before the item is claimed and before a repair round is spent. |
| run.developer-contract | the developer | `internal/orchestrator/pipeline.go` | `developerContract` | Committing, promoting, editing an upstream artifact, admitting work to the backlog, and amending an invariant: the developer proposes instead. |
| review.independent-invocation | the reviewer | `internal/review/reviewer.go` | `(Reviewer).Review` | A review that is not its own invocation: no session to resume, no tools, and the role and phase set here rather than taken from the caller. |
| review.contract | the reviewer | `internal/review/reviewer.go` | `reviewSystemPrompt` | A persona replacing the reviewer's contract, verdict vocabulary, independence rules, or evidence bounds; it may only specialize what is looked for. |
| review.policy | the reviewer | `internal/orchestrator/pipeline.go` | `(Pipeline).validateReviewPolicy` | Automatic integration with no reviewer, with an agent that does not fill the reviewer role, or with no explicit model. |
| run.independent-invocations | the developer, the reviewer | `internal/orchestrator/pipeline.go` | `validateIndependentInvocations` | Integrating on an approval whose developer and reviewer sessions are missing or are the same session. |
| runstate.independent-invocations | the developer, the reviewer | `internal/runstate/state.go` | `(State).validateIndependentInvocations` | The same demand made of the durable record, so the evidence survives the process that produced it. |
| backend.read-only-role | the product manager, the architect, the development manager, the reviewer | `internal/backend/claudecode/backend.go` | `readOnlyRole` | Names the roles that reason over supplied evidence, which is what the tool refusal below is decided from. |
| backend.supported-role | every role | `internal/backend/claudecode/backend.go` | `supportedRole` | A role this backend cannot assemble an invocation for. |
| backend.no-tools-for-read-only | the product manager, the architect, the development manager, the reviewer | `internal/backend/claudecode/backend.go` | `(Backend).Run` | Tools granted to a role that reasons over bounded supplied evidence, and an unsupported role, before the provider is reached. |
| promotion.lease | the harness | `internal/runstate/promotion.go` | `(*Store).LeasePromotion` | A second concurrent promotion into one target branch. The lease is the harness's own and no agent acquires, releases, or touches it. |
| run.integrate-under-lease | the harness | `internal/orchestrator/pipeline.go` | `(*activeRun).integrate` | Moving a target branch without first holding that branch's promotion lease. |
| converge.catch-up-under-lease | the harness | `internal/orchestrator/converge.go` | `(Reconciler).catchUp` | The reconciler moving a target branch outside the same lease, so a catch-up and a promotion never read one branch and then both move it. |
| capability.known | every role | `internal/capability/capability.go` | `(Capability).Known` | Answers what the two rows below refuse on: the vocabulary an action declares its authority in is a closed list in Go, so a name nothing here declares is not one anything can require. |
| action.registry-closed | every role | `internal/action/action.go` | `New` | An action requiring a capability nothing declares, declaring no capabilities at all, or naming no trusted function it wraps. Configuration selects sequence; code grants capability. |
| workflow.catalog-closed | every role | `internal/workflow/validate.go` | `NewCatalog` | A catalog entry naming a capability the repository does not declare, which would be a claim about authority nothing grants. |
| workflow.compiled-under-grant | every role | `internal/workflow/compile.go` | `Grant` | A definition selecting an action that requires a capability the grant it is compiled under does not confer, and a compile under a grant that confers nothing at all. It is refused before an instance exists and before any work is claimed, so a definition never widens the authority it was bound with. The refusal is made by the loader's `Compile`, whose generic receiver this inventory cannot name, so the row pins the grant it is made against. |
| workflow.performed-under-grant | every role | `internal/workflow/execute.go` | `withinGrant` | A transition whose action requires a capability the grant performing it does not confer. It is the refusal above made again at every state boundary, against the authority held now rather than the authority the graph was compiled under, so an instance that started under a wider grant does not carry it. What it holds the grant against is the registry's own declaration, carried on the compiled node, and never anything the definition said. The instance stays in the state it stood in. |
| separation.policies | the developer, the reviewer | `internal/separation/separation.go` | `Policies` | Nothing, by itself: it is the separation rules stated as named policies over the capability vocabulary, each carrying the rule it is the capability form of. It is listed because the refusals below name one of them, and a policy nothing describes is a refusal nobody can look up. |
| separation.operation | the developer, the reviewer | `internal/separation/separation.go` | `CheckOperation` | One operation that both writes the change and returns the verdict the change is gated on, one that both returns a verdict and promotes, and one that moves a target branch without the lease that admits the move. It is about the combination rather than about who performs it, so it refuses the same combination whoever would have held it. |
| separation.topology | the developer, the reviewer | `internal/separation/separation.go` | `CheckTopology` | A sequence that can reach a step moving the target branch without having crossed, on every path to it and since the last step that writes the change, both a step that runs the project's configured checks and a step that returns a verdict. Every path rather than some path: one route through the review and one around it is a sequence that can promote unjudged work. Since the change was last written rather than anywhere behind it: a step that rewrites the worktree after the verdict has produced a change nobody judged, however many review states came earlier. |
| separation.holders | the harness | `internal/separation/separation.go` | `CheckHolders` | A role holding either half of the promotion. No sequence can answer this — a role's authority is not part of any topology — and `one-promotion-per-target-branch` is what makes it worth asking separately: a bundle conferring the lease or the branch move is a role that could take one whatever the sequence around it says. |
| workflow.separation-at-compile | every role | `internal/workflow/compile.go` | `topologyOf` | A definition whose chosen topology any separation policy refuses, before an instance exists and before a work item is claimed. What the policies are asked about is the resolved graph — the registry's own declaration of what each state requires, never anything the definition said. The refusal is made by the loader's `Compile`, whose generic receiver this inventory cannot name, so the row pins the projection it is made over. |
| rolecapability.role-bundles | every role | `internal/rolecapability/bundles.go` | `bundles` | Nothing, by itself: it is the statement of what each of the five roles holds, derived from this inventory and written in Go so that configuration cannot widen it. It is listed because it is the other half of this document, and a change to it is a change to what the harness says a role may do. |
| rolecapability.bundles-closed | every role | `internal/rolecapability/rolecapability.go` | `New` | A bundle for a role the harness does not have, two bundles for one role, a role no bundle describes, a bundle holding nothing or holding a capability nothing declares, and a declared capability that neither a role nor the harness holds. All of it at construction, from a literal table. |
| rolecapability.harness-held | the harness | `internal/rolecapability/bundles.go` | `harnessHeld` | A role bundle conferring the promotion. The lease and the branch move it admits are recorded as the harness's own, each with the reason no role holds it, and a bundle claiming one is refused where the registry is built. |
| cli.conversation-contract-exists | every role | `internal/cli/agent.go` | `agentConversationRequest` | Addressing an agent whose role the harness holds no conversation contract for. |

## Not an authority check

Everything the sweep finds and this inventory deliberately does not list, with
the reason. It is a table rather than a rule because each line is a judgement
somebody made, and writing the judgement down is the point: something that
arrives and is neither listed nor excused fails the check, so the next person has
to make the same judgement out loud instead of the question never being asked.

| File | Declaration | Why it is not one |
| --- | --- | --- |
| `internal/artifact/ownership.go` | `authority` | Names the capability a kind of document belongs to. It refuses nothing: the lookup that turns it into an owner is `artifact.owner` and the refusal made on it is `artifact.authorize`. |
| `internal/artifact/references.go` | `ProblemUnauthorizedRevision` | The name of the problem kind `UnauthorizedRevisions` reports; the check is that row. |
| `internal/backend/registry.go` | `DescriptorFor` | Validates a provider plugin declaration, the roles it serves included. Which provider serves a role is selection, not what the role may do. |
| `internal/capability/capability.go` | `PromotionLease` | The name an action declares the promotion lease by. The lease itself is `promotion.lease`. |
| `internal/chat/admission.go` | `(*Session).admissionAuthority` | Records what let an item into the queue. It refuses nothing; the refusal is `conversation.admission-gate`. |
| `internal/chat/role.go` | `(*Session).authority` | Reads this session's row out of the table. |
| `internal/chat/role.go` | `Authority` | The shape of a row in `conversation.authority-table`. |
| `internal/chat/role.go` | `AuthorityError` | How a conversation reports a refusal, rather than a refusal of its own. |
| `internal/cli/agent.go` | `resolveAgent` | Resolves a configured agent name to its role. A name nothing configures is a configuration error, not refused authority. |
| `internal/cli/amendment.go` | `listAmendments` | Lists proposals by owning role for an operator to read. |
| `internal/cli/chat.go` | `conversationAgent` | Picks the configured agent for a role and refuses one filling a different role. That is which agent, not what the role may do. |
| `internal/cli/chat.go` | `openChat` | Builds the conversation's components and refuses a backend this build cannot launch. |
| `internal/cli/escalate.go` | `(developmentManagerConversation).Judge` | Opens the development manager's own conversation and puts a stopped run in front of that role. Which role is addressed is whose judgement the stoppage needs, not a decision about what that role may do; what it may decide once it is there is `conversation.authority-table`. |
| `internal/exchange/conduct.go` | `Leases` | Who is carrying one exchange right now, so it takes its rounds one at a time. It decides which process may write, never which role may ask: what a role may put to another is `exchange.ask-authority`. |
| `internal/gitworktree/registry.go` | `(*Manager).leaseRegistry` | A file lock over the worktree registry, held so two processes do not rewrite it at once. |
| `internal/gitworktree/registry.go` | `registryLease` | The handle for the lock above. |
| `internal/notify/conversation.go` | `fromExchange` | Turns a recorded exchange into something the operator is told. |
| `internal/notify/conversation.go` | `fromRefusedTrackerBlock` | Turns a recorded refusal of a whole tracker block into something the operator is told. It reads the role the record names so the message can say whose actions were lost; the refusal itself already happened, in the conversation. |
| `internal/notify/conversation.go` | `fromTrackerAction` | Turns a carried-out tracker action into something the operator is told. |
| `internal/notify/select.go` | `FromRun` | Selects what a finished run reports to the operator. |
| `internal/orchestrator/amendments.go` | `(*activeRun).collectAmendments` | Records the changes a run's role proposed, addressed to the owner. Deciding one is `amendment.decide-under-owner`. |
| `internal/orchestrator/branchreview.go` | `(BranchReviewer).collectReports` | Collects what a branch reviewer reported, which decides nothing. |
| `internal/orchestrator/pipeline.go` | `(*activeRun).attemptReview` | One provider invocation inside the review step. What makes it independent is `review.independent-invocation`. |
| `internal/orchestrator/pipeline.go` | `(*activeRun).recordDevelopment` | Records what a developer invocation produced and cost. |
| `internal/orchestrator/reports.go` | `(*activeRun).collectReports` | Collects what a run's role reported, which decides nothing. |
| `internal/protectedpath/protectedpath.go` | `protect` | The unexported builder behind `protectedpath.set`. |
| `internal/runstate/conversation.go` | `(*ConversationStore).leaseFile` | Names the file a conversation's lease is taken on, so two agents on one role do not hold one conversation. |
| `internal/runstate/lease.go` | `TryLeasePath` | The shared lease primitive that reports rather than waits. The promotion lease waits its turn and is `promotion.lease`. |
| `internal/runstate/rotation.go` | `(*Store).LeaseRotation` | Serializes account rotation, which is capacity rather than authority. |
| `internal/runstate/store.go` | `(*Store).leasePath` | Names the file a run's own lease is taken on. |
| `internal/runstate/store.go` | `(*Store).takeLease` | Takes a run's own lease, so two processes do not act on one run. |
| `internal/runstate/store.go` | `Lease` | The handle every lease here is held through. |
| `internal/runstate/store.go` | `leaseGrace` | How long a run's lease is waited for before it is treated as gone. |
| `internal/runstate/store.go` | `leaseGracePoll` | How often that wait looks again. |
| `internal/runstate/watch.go` | `(*WatchStore).Lease` | Serializes watching sessions against a second copy of one, so two of them do not choose from one queue at once. It decides which process may watch, never which role may do anything. |
| `internal/runstate/watch.go` | `watchLeaseFile` | Names the file that lease is taken on. |
| `internal/separation/separation.go` | `PromotionIsNeverUnleased` | The name one of the separation policies refuses under. The check is `separation.operation`, and the lease it defends is `promotion.lease`. |
| `internal/slack/state.go` | `(*Store).Lease` | Serializes the Slack process against a second copy of itself. |
| `internal/triage/triage.go` | `(Entry).Validate` | Holds a docket entry to its own shape, which for an escalation includes naming one of the two roles that work inside a run. It refuses a record rather than an act: what a role may decide about a docketed entry is `conversation.authority-table`. |
