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
// The vocabulary here is the one the delivery pipeline's own steps need and no
// more. The full inventory belongs to the authority workstream, which derives it
// from what the harness actually authorizes today rather than from what looks
// tidy in advance; these names are expected to be absorbed by that inventory
// rather than to pre-empt it.
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
	// RunStateMutate is writing the run's own durable record. Nearly every step
	// needs it, and it is still named: a step that records nothing about itself is
	// a step reconciliation cannot reason about, so declaring it is what makes the
	// exceptions visible.
	RunStateMutate Capability = "run-state.mutate"
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
