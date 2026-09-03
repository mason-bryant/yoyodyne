// Package capability is the vocabulary an action declares its authority in.
//
// The point of the vocabulary is that it is closed. A workflow definition
// selects which registered actions run and in what order; it never mints one,
// and it never widens what one is permitted to do. That only holds if the set
// of things an action can require is a list in Go rather than a string some
// definition supplies, so a capability nothing here declares is refused where
// the registry is built — before any definition has been read and long before
// anything could be dispatched.
//
// What is written down is a primitive rather than a role. Two steps that need
// the same authority say so identically, which is what will let a later
// authority model answer "what may this role do" by reading the actions it may
// select instead of reading the code inside them.
//
// The vocabulary is in two halves, and the second is why the first is no longer
// the whole of it. The delivery names below are what the pipeline's own steps
// need; the names after them are derived from the authority inventory —
// `docs/authority-inventory.md`, held to the code by `internal/authority` — which
// is the enumeration of what the harness authorizes today. Deriving them from
// the inventory rather than inventing them is the order the design records: a
// name here that answers no check the inventory lists is authority nobody
// enforces.
//
// Which role holds which of these is `internal/rolecapability`, deliberately not
// here. A capability stays a primitive: two roles that need the same authority
// name it identically, and the sentence "the architect may amend a design" is one
// package away rather than folded into the vocabulary the actions declare
// against.
package capability

import "slices"

// Capability is one primitive an action requires in order to perform its work.
type Capability string

const (
	// WorkItemRead and WorkItemMutate are the tracker: reading what an item says,
	// and claiming, annotating, blocking, or closing it. They are apart because
	// almost everything reads the item and only the ends of a run change it.
	WorkItemRead   Capability = "work-item.read"
	WorkItemMutate Capability = "work-item.mutate"
	// RepositoryRead is reading repository content — the context a developer is
	// given, the paths a change touched, the diff a reviewer is shown. It carries
	// no write of any kind, which is what makes it the capability a step that only
	// looks at a change can be held to.
	RepositoryRead Capability = "repository.read"
	// WorktreeMutate is writing inside the run's own isolated worktree and on its
	// own branch: creating it, committing what a developer left, removing it once
	// its work is somewhere else. It never reaches the branch a run promotes into,
	// which is the next capability and deliberately not this one.
	WorktreeMutate Capability = "worktree.mutate"
	// TargetBranchMutate is moving the branch a run promotes into. It is the most
	// consequential thing the harness does and it is named on its own so that an
	// action requiring it is visible as such in the registry, without anybody
	// having to read what the action does.
	TargetBranchMutate Capability = "target-branch.mutate"
	// PromotionLease is taking the lease that admits one promotion at a time into
	// a target branch. It is separate from moving the branch because the lease is
	// what makes the move safe: an action that moves the branch without it is the
	// race the lease exists to stop, and separating them is what makes that
	// legible where the actions are declared.
	PromotionLease Capability = "promotion.lease"
	// ProviderInvoke is spending a provider invocation. It is the largest thing
	// this harness spends and the one every budget, hold, and pause is about.
	ProviderInvoke Capability = "provider.invoke"
	// ChecksExecute is running the project's configured checks. It executes
	// project-supplied commands, which is why it is its own capability rather than
	// part of reading a change: it is the one place a definition can cause
	// arbitrary project code to run.
	ChecksExecute Capability = "checks.execute"
	// ForgePublish is everything that reaches the forge and the remote: pushing a
	// run branch, opening and updating a pull request, asking for a merge, and
	// deleting the branch afterwards.
	ForgePublish Capability = "forge.publish"
	// RunStateMutate is writing the harness's own durable store: the run's record,
	// its event log, and the per-work-item counters kept beside it — the review
	// rounds an item has spent, which outlive any one run and are still the
	// harness's own bookkeeping rather than the tracker's. It is deliberately not
	// WorkItemMutate: nothing under this capability reaches the work item, and a
	// step that writes a counter here has not touched what the tracker says.
	//
	// Nearly every step needs it, and it is still named: a step that records
	// nothing about itself is a step reconciliation cannot reason about, so
	// declaring it is what makes the exceptions visible.
	RunStateMutate Capability = "run-state.mutate"
)

// The names below are the rest of what the harness authorizes, one per authority
// the inventory's rows actually tell roles apart by. They are not the pipeline's:
// no registered action requires one, and what requires them instead is the
// authorization sites the inventory lists, which ask who holds one rather than
// which role they are talking to. What they are for is that a role's authority can
// be stated in capabilities at all — a bundle built only from the delivery names
// above would say nothing about the four roles that never enter a run.
//
// The granularity is the inventory's rather than a tidier one. Where a row tells
// two roles apart, there is a name for what it tells them apart by; where it does
// not, there is not one.
const (
	// BacklogAdmit is putting new work into the backlog and taking it out again —
	// admitting, closing, retiring. It is apart from WorkItemMutate because that is
	// the boundary the harness enforces most often: the development manager may
	// build structure underneath admitted work all day and may not admit any, and
	// an item admitted by anything but the product manager is work nobody chose.
	BacklogAdmit Capability = "backlog.admit"
	// BacklogOrder is what is pulled next: priority, and parking admitted work out
	// of reach without taking it out of the queue. Order is the product manager's
	// in the same breath as admission and is still its own name, because a role
	// that may reorder without admitting is a coherent thing to write down.
	BacklogOrder Capability = "backlog.order"
	// WorkDecompose is creating work underneath something already admitted. It is
	// the development manager's whole tracker authority beyond annotation, and it is
	// deliberately not BacklogAdmit: decomposition underneath an admitted parent is
	// not admission, which is the distinction the parent requirement enforces today.
	WorkDecompose Capability = "work.decompose"
	// WorkTriage is recording what was decided about work that stopped moving. Its
	// subject is a stopped run rather than the item's own fields, which is why it is
	// not WorkItemMutate: a decision nobody can find is the state triage exists to
	// leave behind.
	WorkTriage Capability = "work.triage"
	// ArtifactProductMutate and ArtifactDesignMutate are authorship of the
	// canonical documents, split the way ownership is: the brief, the goals and the
	// non-goals are the product manager's, and the designs, the specifications and
	// the decision records are the architect's.
	//
	// They are two names rather than one because one name would answer the
	// ownership check wrongly. "May this role amend an artifact?" is a question with
	// no true answer — it depends on the kind — and a vocabulary that could only ask
	// it that way would let the architect through on a goals document. The design
	// settles the eventual shape as one capability with a typed artifact-kind scope;
	// until scopes exist, these two are that scope written into the names.
	ArtifactProductMutate Capability = "artifact.product.mutate"
	ArtifactDesignMutate  Capability = "artifact.design.mutate"
	// InvariantMutate is creating, amending, or retiring an architectural
	// invariant. It is the architect's and is not folded into the design documents
	// it is extracted from: an invariant binds work that never reads the design, and
	// the harness refuses a change to one from every other role by name.
	InvariantMutate Capability = "invariant.mutate"
	// ResearchCommission is having the harness gather evidence from outside this
	// machine, and EvaluationRecord is writing down a durable recommendation about
	// an idea. Both are the product manager's today. They stay apart for the reason
	// the conversation authority already keeps them apart: one reaches outside and
	// decides nothing, the other decides nothing and is authority over what the
	// product's own record says it was advised.
	ResearchCommission Capability = "research.commission"
	EvaluationRecord   Capability = "evaluation.record"
	// ProposalRaise is handing the operator work to approve, and ConcernRaise is
	// stopping to put a question to them instead. They belong to the role that
	// decides what is admitted, and they are two names because they are the two
	// flags the conversation authority carries: a role that may propose and may not
	// stop is a different role from one that may do both.
	ProposalRaise Capability = "proposal.raise"
	ConcernRaise  Capability = "concern.raise"
	// ExchangeAsk is being on the inter-role ask channel, at both ends of it. It
	// carries no authority to decide anything — an ask is judgment-only and an
	// answer resolves nothing — and it is still a capability, because whether the
	// harness will carry a question to or from a role at all is a boundary the
	// harness enforces.
	ExchangeAsk Capability = "exchange.ask"
	// ReviewVerdict is returning the judgement a change is gated on. It is the
	// reviewer's alone, and naming it is what lets "no role but the reviewer decides
	// a verdict" and "the development manager may not override one" be the same
	// question asked twice rather than two rules kept in two places.
	ReviewVerdict Capability = "review.verdict"
)

// declared is every capability this repository has, in the order above. It is
// the list Known answers from, so adding a constant without adding it here
// leaves the constant unusable rather than silently half-declared — which the
// package's own test is what catches.
var declared = []Capability{
	WorkItemRead,
	WorkItemMutate,
	RepositoryRead,
	WorktreeMutate,
	TargetBranchMutate,
	PromotionLease,
	ProviderInvoke,
	ChecksExecute,
	ForgePublish,
	RunStateMutate,
	BacklogAdmit,
	BacklogOrder,
	WorkDecompose,
	WorkTriage,
	ArtifactProductMutate,
	ArtifactDesignMutate,
	InvariantMutate,
	ResearchCommission,
	EvaluationRecord,
	ProposalRaise,
	ConcernRaise,
	ExchangeAsk,
	ReviewVerdict,
}

// All is every capability this repository declares, in declaration order.
func All() []Capability {
	return slices.Clone(declared)
}

// Known reports whether this is a capability the repository declares. Anything
// else is refused where a registry is built, which is the whole of what stops a
// name arriving from outside trusted code.
func (c Capability) Known() bool {
	return slices.Contains(declared, c)
}

func (c Capability) String() string {
	return string(c)
}
