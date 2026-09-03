// Package separation is the harness's separation rules, said in capabilities
// instead of in the shape of the code that happens to enforce them today.
//
// Three of them, and the harness has always held all three structurally. The
// reviewer is never the author: the review is its own provider invocation, with
// no session to resume and no tools. Two demonstrably independent invocations
// gate an integration: a run that cannot name a distinct developer session and
// reviewer session does not promote. And the roles that authorize a promotion
// cannot perform one: the lease and the branch move belong to the harness, and
// no role's bundle confers either.
//
// Each of those is true because of where Go control flow puts things. None of
// it survives a sequence read out of a file. A definition chooses its own
// topology — which states exist, which action each performs, and where every
// outcome leads — and "the reviewer is never the author" is not a sentence a
// state machine knows how to be wrong about. A definition that put the review
// before the checks, or that reached the promotion straight out of the
// developer, would be a valid state machine selecting registered actions under
// a sufficient grant, and every refusal upstream of here would pass it.
//
// So each rule is written down here as a named policy over the capability
// vocabulary, and held against whatever topology a definition chose. That is the
// whole of what this package is for: the rules stop being properties of one
// hard-coded sequence and become properties of the vocabulary, which every
// sequence is written in.
//
// # What it is stated over, and what it is deliberately not
//
// A policy is stated over operations and capabilities and nothing else. It does
// not know what an action is, what a workflow definition looks like, or which
// role holds what — it takes the name of a thing to be performed and the
// capabilities performing it requires, which is all three of `internal/action`,
// `internal/workflow` and `internal/rolecapability` reduced to what a separation
// rule actually turns on. Keeping it that way is what makes it checkable in all
// three places rather than in whichever one it was written inside.
//
// What it cannot answer is the run-time half. Whether two invocations that a
// topology keeps apart were actually two invocations is evidence a run records
// and the pipeline refuses to integrate without — `run.independent-invocations`
// and `runstate.independent-invocations` in the authority inventory. A topology
// can guarantee that authorship and judgment are two different steps; only the
// record can say that two different invocations performed them. Both are needed
// and neither replaces the other.
package separation

import (
	"errors"
	"fmt"
	"slices"

	"github.com/mason-bryant/yoyodyne/internal/capability"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// The policies, by the names a refusal reports them under. They are constants
// rather than strings written into each message so that a test naming a policy
// and a refusal naming one are naming the same thing.
const (
	// AuthorshipIsNeverJudgment is the reviewer never being the author.
	AuthorshipIsNeverJudgment = "authorship-is-never-judgment"
	// JudgmentNeverPromotes is the roles that authorize a promotion not being
	// able to perform one.
	JudgmentNeverPromotes = "judgment-never-promotes"
	// PromotionIsNeverUnleased is the branch move never happening outside the
	// lease that admits one of them at a time.
	PromotionIsNeverUnleased = "promotion-is-never-unleased"
	// IntegrationFollowsEvidence is integration being impossible without the
	// evidence that earns it, whatever order a definition put its states in.
	IntegrationFollowsEvidence = "integration-follows-evidence"
)

// Policy is one separation rule: the name it refuses under, the rule it is the
// capability form of, and the combination it will not admit.
type Policy struct {
	// Name is what a refusal reports, and what a test names.
	Name string
	// Rule is the separation rule this is the capability form of, in the words
	// the harness has always used for it. It is here because a policy stated only
	// in primitives is one nobody can check against the thing it came from.
	Rule string
	// Refuses is the combination the policy will not admit, in the words somebody
	// reading a refusal needs.
	Refuses string
}

// Policies is every separation policy this repository holds, in the order the
// rules are usually said in.
func Policies() []Policy {
	return []Policy{
		{
			Name:    AuthorshipIsNeverJudgment,
			Rule:    "the reviewer is never the author",
			Refuses: "one operation that both writes the change and returns the verdict the change is gated on",
		},
		{
			Name:    JudgmentNeverPromotes,
			Rule:    "the roles that authorize a promotion cannot perform one",
			Refuses: "one operation that both returns a verdict and promotes, and any role holding either half of the promotion",
		},
		{
			Name:    PromotionIsNeverUnleased,
			Rule:    "at most one promotion per target branch, taken by the harness",
			Refuses: "an operation that moves a target branch without also taking the lease that admits one move at a time",
		},
		{
			Name:    IntegrationFollowsEvidence,
			Rule:    "two demonstrably independent provider invocations gate an integration",
			Refuses: "a sequence that can reach a step moving the target branch without having crossed, since the last step that wrote the change, both a step that ran the project's checks and a step that returned a verdict",
		},
	}
}

// The vocabulary a separation rule turns on, read out of `internal/capability`.
//
// Naming them here rather than matching on the capability constants at each
// site is what makes the policies readable as the rules they are: "authorship"
// and "judgment" are the words the rules are said in, and this is the one place
// they are tied to the primitives that mean them. A capability added to the
// vocabulary that belongs on one of these lists belongs on it here.
var (
	// authorship is every capability that writes the change a review is about. A
	// step holding one of them could have authored what is being judged — which is
	// why it is both what may not be held alongside the verdict and what
	// invalidates evidence collected before it.
	authorship = []capability.Capability{capability.WorktreeMutate}
	// promotion is the two halves of putting an approved change on the branch it
	// was promised to. They are apart in the vocabulary because the lease is what
	// makes the move safe, and they are together here because holding either is
	// performing the promotion.
	promotion = []capability.Capability{capability.TargetBranchMutate, capability.PromotionLease}
)

const (
	// judgment is returning the verdict a change is gated on. There is one of it
	// and the vocabulary names it, which is exactly why the rules can be written
	// over the vocabulary at all.
	judgment = capability.ReviewVerdict
	// checking is executing the project's configured checks — the other half of
	// what integration has to be able to point at.
	checking = capability.ChecksExecute
	// moving is the branch move itself, apart from the lease that admits it.
	moving = capability.TargetBranchMutate
	// leasing is that lease.
	leasing = capability.PromotionLease
)

// Operation is one thing that can be performed: the name it is selected under
// and everything performing it requires.
//
// It is deliberately not an action. This package is upstream of the action
// registry and stays there, because a policy that had to know what an action is
// would be a policy only that registry's actions could be held to — and the
// same rules have to hold over a role's bundle, over a registry's table, and
// over a topology a project wrote.
type Operation struct {
	// Name is what the operation is selected under, for a refusal to name.
	Name string
	// Requires is everything performing it needs, as trusted code declares it.
	// Nothing a definition says adds to this list or takes anything from it.
	Requires []capability.Capability
}

func (o Operation) requires(needed capability.Capability) bool {
	return slices.Contains(o.Requires, needed)
}

// Step is one state of a topology: the name a definition declared it under, the
// operation it performs, and every state it can go to next.
type Step struct {
	// State is the name the definition declared this step under.
	State string
	// Performs is what the step does.
	Performs Operation
	// Next is every state this step can transition into. A destination that is a
	// terminal is left out: a terminal performs nothing, so nothing about it can
	// carry evidence or need any.
	Next []string
}

// Topology is a whole sequence as this package reads one: where an instance
// starts, and what each step performs and leads to.
//
// It is a projection rather than a definition. Everything a definition says that
// a separation rule does not turn on — its summaries, its terminals, its schema,
// its digest — is left out, so that this package answers about sequence and
// authority and can be handed a sequence from anywhere.
type Topology struct {
	// ID is the workflow this came from, for a refusal to name.
	ID string
	// Initial is the state an instance starts in, which is where every path this
	// package reasons about begins.
	Initial string
	// Steps is every state, in whatever order the caller holds them. The checks
	// below sort what they walk, so one topology refused twice is refused
	// identically.
	Steps []Step
}

// CheckOperation holds one operation to every policy that is about a single
// invocation: what one thing may require at once.
//
// `where` is what a refusal calls the place the operation was found — "the state
// %q" from a topology, "the registered action" from a registry — so that one set
// of messages reads correctly from both.
func CheckOperation(where string, operation Operation) error {
	var problems []error
	if operation.requires(judgment) {
		for _, authored := range authorship {
			if !operation.requires(authored) {
				continue
			}
			problems = append(problems, fmt.Errorf("%s: %s performs %q, which requires both %q and %q; one invocation that writes the change and returns the verdict on it is the author judging their own work, whatever sequence a definition puts around it",
				AuthorshipIsNeverJudgment, where, operation.Name, authored, judgment))
		}
		for _, promotes := range promotion {
			if !operation.requires(promotes) {
				continue
			}
			problems = append(problems, fmt.Errorf("%s: %s performs %q, which requires both %q and %q; the verdict gates the promotion and never performs it, so nothing that returns one may also take it",
				JudgmentNeverPromotes, where, operation.Name, judgment, promotes))
		}
	}
	if operation.requires(moving) && !operation.requires(leasing) {
		problems = append(problems, fmt.Errorf("%s: %s performs %q, which requires %q and not %q; a branch moved outside the lease is the race the lease exists to stop, so the two are performed together or not at all",
			PromotionIsNeverUnleased, where, operation.Name, moving, leasing))
	}
	if len(problems) > 0 {
		return errors.Join(problems...)
	}
	return nil
}

// CheckTopology holds a whole sequence to every policy: each of its steps to the
// ones about a single invocation, and the sequence itself to the one that is
// about a path.
//
// Everything wrong is reported together, for the reason validation reports its
// problems together: a definition is written by hand, and answering one question
// per reload is how somebody gives up on a format.
func CheckTopology(topology Topology) error {
	steps := slices.Clone(topology.Steps)
	slices.SortFunc(steps, func(a, b Step) int {
		if a.State < b.State {
			return -1
		}
		if a.State > b.State {
			return 1
		}
		return 0
	})

	var problems []error
	for _, step := range steps {
		if err := CheckOperation(fmt.Sprintf("the state %q", step.State), step.Performs); err != nil {
			problems = append(problems, err)
		}
	}

	// The path policy is asked second because it is the one whose answer is worth
	// less on its own: a topology whose review step also authors is one where
	// crossing the review step proves nothing, and saying only that the evidence
	// was collected would read as an approval of the sequence.
	before := evidenceBefore(topology, steps)
	for _, step := range steps {
		if !step.Performs.requires(moving) {
			continue
		}
		collected := before[step.State]
		if collected&checked == 0 {
			problems = append(problems, fmt.Errorf("%s: the state %q performs %q, which moves the target branch, and %s can reach it with no state requiring %q between the last state that writes the change and this one; integration is impossible without the evidence that earns it, whatever order a definition puts its states in",
				IntegrationFollowsEvidence, step.State, step.Performs.Name, describe(topology), checking))
		}
		if collected&judged == 0 {
			problems = append(problems, fmt.Errorf("%s: the state %q performs %q, which moves the target branch, and %s can reach it with no state requiring %q between the last state that writes the change and this one; a change promoted on nobody's verdict, or on a verdict it was rewritten after, is what no topology may express",
				IntegrationFollowsEvidence, step.State, step.Performs.Name, describe(topology), judgment))
		}
	}
	if len(problems) > 0 {
		return errors.Join(problems...)
	}
	return nil
}

// Holders is what a statement of who holds which capability has to answer for
// the role half of a separation policy to be asked of it. The registry in
// `internal/rolecapability` is the one this repository ships.
//
// It is an interface so that this package stays upstream of that one: the rules
// a vocabulary is held to must not depend on the table that happens to express
// who holds it, or the table becomes the thing deciding what the rules are.
type Holders interface {
	// Roles is every role the statement describes.
	Roles() []domain.AgentRole
	// Holds reports whether a role has a capability.
	Holds(role domain.AgentRole, required capability.Capability) bool
}

// CheckHolders is the half of judgment-never-promotes that no topology can
// answer: whether any role holds the promotion at all.
//
// A topology can only refuse a step that judges and promotes at once. What it
// cannot see is a role that holds the branch move, because a role's authority is
// not part of any sequence. `one-promotion-per-target-branch` is what makes this
// worth checking separately: no agent performs a promotion, so a bundle
// conferring either half is a role that could, whatever the sequence around it.
func CheckHolders(holders Holders) error {
	var problems []error
	for _, role := range holders.Roles() {
		for _, promotes := range promotion {
			if !holders.Holds(role, promotes) {
				continue
			}
			problems = append(problems, fmt.Errorf("%s: the %s holds %q; the promotion belongs to no role, and a bundle conferring either half of it is a role that could take a branch move nothing gated",
				JudgmentNeverPromotes, role.Title(), promotes))
		}
	}
	if len(problems) > 0 {
		return errors.Join(problems...)
	}
	return nil
}

// evidence is what is known about a change by the time something downstream of a
// step runs. There are two kinds and both are needed, which is why they are bits
// of one value rather than two booleans threaded separately.
type evidence uint8

const (
	// checked is that the project's configured checks ran over the change.
	checked evidence = 1 << iota
	// judged is that a verdict was returned on it.
	judged
	// everything is both, which is what an unreachable step starts and stays at:
	// a step nothing can enter carries no claim about any path, because there is
	// no path.
	everything = checked | judged
)

// provides is what performing a step establishes about the change.
func provides(step Step) evidence {
	var established evidence
	if step.Performs.requires(checking) {
		established |= checked
	}
	if step.Performs.requires(judgment) {
		established |= judged
	}
	return established
}

// invalidates reports whether performing a step makes everything collected
// before it stop describing the change.
//
// Writing the change is what does that, and it is the difference between a rule
// about which states a path crossed and a rule about what is true of the thing
// being promoted. A verdict is a judgement on a change, not a token a sequence
// carries: a step that rewrites the worktree after the review has produced a
// change nobody judged, however many review states came earlier. So authorship
// clears, and whatever the authoring step itself establishes is added back
// afterwards — a step that writes and then runs the checks has checked what it
// wrote, and one that writes and then judges it is refused outright by
// `authorship-is-never-judgment` before this is ever asked.
func invalidates(step Step) bool {
	for _, authored := range authorship {
		if step.Performs.requires(authored) {
			return true
		}
	}
	return false
}

// carries is what a step hands to everything downstream of it: nothing if it
// wrote the change, otherwise what reached it, and either way plus what
// performing it established.
func carries(step Step, reaching evidence) evidence {
	if invalidates(step) {
		reaching = 0
	}
	return reaching | provides(step)
}

// evidenceBefore is what still describes the change on *every* path from the
// initial state to each step, by the time an instance is about to perform it.
//
// Every path rather than some path is half the point. A definition that reaches
// the promotion by one route that crossed the review and one that did not is a
// definition that can promote unjudged work, and it is exactly the topology a
// check that looked for one good route would admit.
//
// Still describes rather than was ever collected is the other half. A pure
// has-crossed analysis admits develop, check, review, rework, integrate — a
// second authoring state after the verdict — and promotes a change nobody
// judged. So the transfer function clears at every authoring step, which makes
// the question "since the change was last written, has it been checked and
// judged" rather than "did those states appear somewhere behind this one".
//
// It is a fixpoint over intersections, run to quiescence: each step starts
// claiming everything, and every pass narrows it to what all of its predecessors
// can hand on. The initial state is pinned to nothing however many transitions
// lead back into it, because an instance starts there with nothing collected. A
// step no path reaches keeps its claim of everything, which is sound rather than
// generous: an instance can never stand in it, so no path runs through it.
func evidenceBefore(topology Topology, steps []Step) map[string]evidence {
	incoming := map[string][]Step{}
	for _, step := range steps {
		for _, destination := range step.Next {
			incoming[destination] = append(incoming[destination], step)
		}
	}

	before := make(map[string]evidence, len(steps))
	for _, step := range steps {
		before[step.State] = everything
	}
	before[topology.Initial] = 0

	for narrowed := true; narrowed; {
		narrowed = false
		for _, step := range steps {
			if step.State == topology.Initial {
				continue
			}
			predecessors := incoming[step.State]
			if len(predecessors) == 0 {
				continue
			}
			narrowest := everything
			for _, predecessor := range predecessors {
				narrowest &= carries(predecessor, before[predecessor.State])
			}
			if narrowest != before[step.State] {
				before[step.State] = narrowest
				narrowed = true
			}
		}
	}
	return before
}

// describe is how a refusal names the sequence it refused, so a message read
// without the file in front of somebody still says which workflow it is about.
func describe(topology Topology) string {
	if topology.ID == "" {
		return "the sequence it is part of"
	}
	return fmt.Sprintf("the sequence %q", topology.ID)
}
