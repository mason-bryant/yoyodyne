package rolecapability

// The five roles' capabilities, written down.
//
// Every line below is read off the authority inventory rather than decided here.
// The conversation authority table says what a role may ask for; the artifact and
// invariant ownership tables say which documents are whose; the contracts say what
// each role is refused; the backend's posture table says which roles reason over
// supplied evidence. This is those tables restated once, in one vocabulary, which
// is the only thing it adds — and the reason it adds nothing else is that
// authority stated twice is authority two places can disagree about.
//
// The capabilities a delivery run needs are attached to the role whose work the
// run carries. That is the on-behalf reading the package comment sets out: the
// developer's bundle holds ForgePublish because a developer's change is pushed and
// opened as a pull request in the course of delivering it, not because a developer
// may push. The one authority no bundle carries is the promotion, which is the
// harness's and is recorded as such below.

import (
	"fmt"
	"sync"

	"github.com/mason-bryant/yoyodyne/internal/capability"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// bundles is what each role holds, in the order the hierarchy runs.
func bundles() []Bundle {
	return []Bundle{
		{
			Role: domain.RoleProductManager,
			Owns: "the brief, the goals, and what is admitted to the backlog and in what order",
			Holds: []capability.Capability{
				capability.WorkItemRead,
				capability.WorkItemMutate,
				capability.RepositoryRead,
				// Every role's work is carried out by provider invocations, so every
				// bundle holds this. What stops a role spending is the runtime envelope —
				// budgets, holds, pauses — which is not a capability and must not become
				// one: a guarantee wrapped around every step is not a step a bundle can
				// decline to confer.
				capability.ProviderInvoke,
				capability.BacklogAdmit,
				capability.BacklogOrder,
				// Admitting work includes creating it wherever it belongs, so the product
				// manager decomposes as well. The development manager holds only this half,
				// which is what the parent requirement enforces today.
				capability.WorkDecompose,
				capability.ArtifactProductMutate,
				capability.ResearchCommission,
				capability.EvaluationRecord,
				capability.ProposalRaise,
				capability.ConcernRaise,
				capability.ExchangeAsk,
				// The three roles that hold a standing conversation carry a memory of
				// their own, and the two that are gated inside a run do not. That split is
				// the agent-memory design's rather than a preference: memory is what tunes
				// judgment across invocations, and it says in as many words that
				// accumulation and independence are incompatible in one identity. So the
				// roles whose work a reviewer gates — the developer and the reviewer
				// itself — remember nothing between invocations, and what a run learned
				// stays in that run's own record where the review can see it.
				capability.AgentContextMutate,
			},
		},
		{
			Role: domain.RoleArchitect,
			Owns: "the designs, the decision records, and the architectural invariants",
			Holds: []capability.Capability{
				// The architect reads the tracker and writes none of it: its conversation
				// authority is read and survey, and the work it would otherwise file is
				// proposed to the product manager instead.
				capability.WorkItemRead,
				capability.RepositoryRead,
				capability.ProviderInvoke,
				capability.ArtifactDesignMutate,
				capability.InvariantMutate,
				capability.ExchangeAsk,
				capability.AgentContextMutate,
			},
		},
		{
			Role: domain.RoleDevelopmentManager,
			Owns: "decomposition, dependency structure, and triage of work that has stopped moving",
			Holds: []capability.Capability{
				capability.WorkItemRead,
				capability.WorkItemMutate,
				capability.RepositoryRead,
				capability.ProviderInvoke,
				capability.WorkDecompose,
				capability.WorkTriage,
				capability.ExchangeAsk,
				capability.AgentContextMutate,
			},
		},
		{
			Role: domain.RoleDeveloper,
			Owns: "no document and no queue; it implements one bounded work item inside a run",
			Holds: []capability.Capability{
				// Read and no more. A developer run reads tracker state and never writes
				// it: the claim at the start of a run and the close at the end are the
				// harness's, made around the invocation rather than by it.
				capability.WorkItemRead,
				capability.RepositoryRead,
				capability.WorktreeMutate,
				// The delivery run pushes this developer's branch and opens or updates its
				// pull request. The developer contract refuses the developer doing either,
				// and both hold at once: the harness performs it, the agent does not.
				capability.ForgePublish,
				capability.ProviderInvoke,
				// Running the project's configured checks is the developer's twice over —
				// the harness runs them over the change, and the developer is required to
				// execute them itself before it submits anything.
				capability.ChecksExecute,
				capability.RunStateMutate,
			},
		},
		{
			Role: domain.RoleReviewer,
			Owns: "no document and no queue; it judges one change inside a run",
			Holds: []capability.Capability{
				capability.WorkItemRead,
				// The reviewer reaches for nothing: it has no tools, and the change, the
				// item, and the evidence are supplied to it. The read is the harness's, on
				// the reviewer's behalf, which is what this capability has always meant.
				capability.RepositoryRead,
				capability.ProviderInvoke,
				capability.RunStateMutate,
				capability.ReviewVerdict,
			},
		},
	}
}

// harnessHeld is every capability no role holds, and why.
//
// Both are the promotion, and both are here rather than in a bundle because the
// inventory binds them to the harness rather than to a role, and because
// `one-promotion-per-target-branch` says outright that no agent performs a
// promotion or touches the lease that admits one. Putting either in a bundle would
// be the first sentence of an argument that some role may.
func harnessHeld() []Held {
	return []Held{
		{
			Capability: capability.PromotionLease,
			Reason:     "the lease that admits one promotion at a time into a target branch is the harness's own; no agent acquires, releases, or otherwise touches it, and a bundle conferring it would be a role that could",
		},
		{
			Capability: capability.TargetBranchMutate,
			Reason:     "moving the branch a run promotes into is the harness's act under that lease, taken on an approved change rather than on any role's authority; the reviewer's verdict gates it and does not perform it",
		},
	}
}

// Default is the registry this repository ships: the five roles as they behave
// today.
//
// It returns an error rather than panicking or building lazily because the
// refusals are the point. A bundle naming a role the harness does not have, or a
// capability nothing declares, is a defect in the table above rather than in one
// run's luck, and it is found the moment anything asks for the registry.
func Default() (Registry, error) {
	registry, err := New(bundles(), harnessHeld())
	if err != nil {
		return Registry{}, fmt.Errorf("build the role-capability registry: %w", err)
	}
	return registry, nil
}

// MustDefault is Default for an authorization site with nowhere to put an error.
//
// A check answers one question — may this role do this — and the caller acts on
// the answer. Threading a construction failure through every one of them would
// put a second, unrelated failure into every refusal, and a check that answers
// "no, because the table would not build" is one nobody can tell from a role
// that was genuinely refused. Several of the sites reading this have no error to
// return at all: which role owns a kind of document is a lookup, not an attempt.
//
// So stopping is the honest answer instead, and it is a safe one here because the
// table is a Go literal. A registry that will not build cannot be caused by
// configuration, by an operator, or by anything an agent writes; it is a defect
// in this binary, `Default`'s own refusals name it, and this package's tests
// reach it before a release does. The registry is built once and kept, so the
// cost is one construction per process rather than one per check.
func MustDefault() Registry { return defaultRegistry() }

var defaultRegistry = sync.OnceValue(func() Registry {
	registry, err := Default()
	if err != nil {
		panic(err)
	}
	return registry
})
