package separation

import (
	"slices"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/capability"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// performing is one operation written the way a table reads best.
func performing(name string, required ...capability.Capability) Operation {
	return Operation{Name: name, Requires: required}
}

// The delivery loop's own operations, as the capabilities the harness declares
// for them. They are written out here rather than imported because this package
// is upstream of the registry that declares them, which is the whole point of
// its being stated over capabilities: the same rules hold over any table.
var (
	claim     = performing("work-item.claim", capability.WorkItemRead, capability.WorkItemMutate, capability.RepositoryRead)
	develop   = performing("candidate.develop", capability.ProviderInvoke, capability.RepositoryRead, capability.WorktreeMutate, capability.ForgePublish, capability.RunStateMutate)
	publish   = performing("candidate.publish", capability.WorktreeMutate, capability.ForgePublish, capability.RunStateMutate)
	check     = performing("candidate.check", capability.RepositoryRead, capability.ChecksExecute, capability.RunStateMutate)
	review    = performing("candidate.review", capability.RepositoryRead, capability.ProviderInvoke, capability.RunStateMutate, capability.ReviewVerdict)
	integrate = performing("candidate.integrate", capability.PromotionLease, capability.TargetBranchMutate, capability.ForgePublish, capability.RunStateMutate)
	cleanUp   = performing("run.clean-up", capability.WorktreeMutate, capability.RunStateMutate)
	// A second authoring operation. The harness reaches its repair round by
	// invoking the developer again rather than by a step of its own, so this is
	// what that round would be if a definition named it — and it is what the
	// evidence rule is about, because a sequence with two authoring steps is where
	// "the change was rewritten after the verdict" first becomes expressible.
	rework = performing("candidate.rework", capability.ProviderInvoke, capability.WorktreeMutate, capability.RunStateMutate)
)

// delivery is the sequence the harness runs today, as a topology.
func delivery() Topology {
	return Topology{
		ID:      "delivery",
		Initial: "claim",
		Steps: []Step{
			{State: "claim", Performs: claim, Next: []string{"develop"}},
			{State: "develop", Performs: develop, Next: []string{"publish"}},
			{State: "publish", Performs: publish, Next: []string{"check"}},
			{State: "check", Performs: check, Next: []string{"review", "develop"}},
			{State: "review", Performs: review, Next: []string{"integrate", "develop"}},
			{State: "integrate", Performs: integrate, Next: []string{"clean-up", "develop"}},
			{State: "clean-up", Performs: cleanUp},
		},
	}
}

func TestEveryPolicyIsNamedAndSaysWhatItRefuses(t *testing.T) {
	t.Parallel()

	policies := Policies()
	if len(policies) == 0 {
		t.Fatal("Policies() is empty; the separation rules are what this package is")
	}
	named := map[string]bool{}
	for _, policy := range policies {
		if strings.TrimSpace(policy.Name) == "" {
			t.Errorf("a policy has no name; a refusal reports one")
		}
		if strings.TrimSpace(policy.Rule) == "" {
			t.Errorf("%q states no rule; a policy stated only in primitives is one nobody can check against what it came from", policy.Name)
		}
		if strings.TrimSpace(policy.Refuses) == "" {
			t.Errorf("%q says nothing about what it refuses", policy.Name)
		}
		if named[policy.Name] {
			t.Errorf("%q is listed twice", policy.Name)
		}
		named[policy.Name] = true
	}
	for _, constant := range []string{AuthorshipIsNeverJudgment, JudgmentNeverPromotes, PromotionIsNeverUnleased, IntegrationFollowsEvidence} {
		if !named[constant] {
			t.Errorf("%q is a policy name nothing lists; a refusal would name a policy Policies() does not describe", constant)
		}
	}
}

// TestTheDeliveryOperationsPassEveryPolicy is the parity claim at the operation
// level: nothing the harness performs today is a combination these rules refuse.
func TestTheDeliveryOperationsPassEveryPolicy(t *testing.T) {
	t.Parallel()

	for _, operation := range []Operation{claim, develop, publish, check, review, integrate, cleanUp} {
		if err := CheckOperation("the registered action", operation); err != nil {
			t.Errorf("CheckOperation(%q) error = %v; the delivery loop is what these policies were written from", operation.Name, err)
		}
	}
}

// TestOneInvocationMayNotAuthorAndJudge is the first rule, made a rule: the
// reviewer is never the author, said so that no sequence can arrange otherwise.
func TestOneInvocationMayNotAuthorAndJudge(t *testing.T) {
	t.Parallel()

	both := performing("candidate.develop-and-review", capability.ProviderInvoke, capability.WorktreeMutate, capability.ReviewVerdict)
	err := CheckOperation("the state \"develop\"", both)
	if err == nil {
		t.Fatal("CheckOperation() admitted one invocation that writes the change and returns the verdict on it")
	}
	for _, named := range []string{AuthorshipIsNeverJudgment, "candidate.develop-and-review", string(capability.WorktreeMutate), string(capability.ReviewVerdict)} {
		if !strings.Contains(err.Error(), named) {
			t.Errorf("CheckOperation() error = %v, and it does not name %q", err, named)
		}
	}
	// Either half alone is ordinary. The rule is about the combination, and a
	// policy that refused authorship would refuse the developer.
	for _, alone := range []Operation{
		performing("candidate.develop", capability.ProviderInvoke, capability.WorktreeMutate),
		performing("candidate.review", capability.ProviderInvoke, capability.ReviewVerdict),
	} {
		if err := CheckOperation("the state", alone); err != nil {
			t.Errorf("CheckOperation(%q) error = %v; each half alone is what the harness already does", alone.Name, err)
		}
	}
}

// TestOneInvocationMayNotJudgeAndPromote is the third rule: the roles that
// authorize a promotion cannot perform one, held at the operation rather than at
// the role.
func TestOneInvocationMayNotJudgeAndPromote(t *testing.T) {
	t.Parallel()

	for _, both := range []Operation{
		performing("candidate.review-and-promote", capability.ReviewVerdict, capability.TargetBranchMutate, capability.PromotionLease),
		performing("candidate.review-and-lease", capability.ReviewVerdict, capability.PromotionLease),
	} {
		err := CheckOperation("the state \"review\"", both)
		if err == nil {
			t.Fatalf("CheckOperation(%q) admitted one invocation that judges and promotes", both.Name)
		}
		if !strings.Contains(err.Error(), JudgmentNeverPromotes) {
			t.Errorf("CheckOperation(%q) error = %v, and it does not name the policy it refused under", both.Name, err)
		}
	}
}

// TestMovingATargetBranchWithoutTheLeaseIsRefused is the invariant
// `one-promotion-per-target-branch` expressed where a definition could otherwise
// reach around it: the branch move and the lease are one operation or neither.
func TestMovingATargetBranchWithoutTheLeaseIsRefused(t *testing.T) {
	t.Parallel()

	err := CheckOperation("the state \"promote\"", performing("candidate.promote", capability.TargetBranchMutate))
	if err == nil {
		t.Fatal("CheckOperation() admitted an operation that moves a target branch outside the lease")
	}
	if !strings.Contains(err.Error(), PromotionIsNeverUnleased) {
		t.Errorf("CheckOperation() error = %v, and it does not name the policy it refused under", err)
	}
	// Taking the lease and not moving anything is not the same defect: a step that
	// only takes the lease is doing half of a promotion the other half completes,
	// and refusing it would be this package deciding how a promotion is assembled.
	if err := CheckOperation("the state", performing("candidate.lease", capability.PromotionLease)); err != nil {
		t.Errorf("CheckOperation() error = %v; taking the lease alone is not the race the lease exists to stop", err)
	}
}

// TestTheDeliveryTopologyPassesEveryPolicy is the parity claim at the sequence
// level: today's loop is admitted unchanged.
func TestTheDeliveryTopologyPassesEveryPolicy(t *testing.T) {
	t.Parallel()

	if err := CheckTopology(delivery()); err != nil {
		t.Errorf("CheckTopology() error = %v; the delivery loop is the sequence these policies were written from", err)
	}
}

// TestIntegrationIsRefusedWithoutEvidenceOnEveryPath is the epic's criterion said
// at this level: a promotion reachable by any route that skipped the checks or
// the verdict is refused, even where another route collected both.
//
// The second case is the one a weaker check would admit. A definition that
// reaches the promotion by one path through the review and one straight out of
// the checks is a definition that can promote unjudged work, and a check asking
// whether the review is reachable at all would call it fine.
func TestIntegrationIsRefusedWithoutEvidenceOnEveryPath(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name     string
		topology Topology
		missing  capability.Capability
	}{
		{
			name: "no verdict anywhere on the way",
			topology: Topology{
				ID:      "unjudged",
				Initial: "develop",
				Steps: []Step{
					{State: "develop", Performs: develop, Next: []string{"check"}},
					{State: "check", Performs: check, Next: []string{"integrate"}},
					{State: "integrate", Performs: integrate},
				},
			},
			missing: capability.ReviewVerdict,
		},
		{
			name: "no checks anywhere on the way",
			topology: Topology{
				ID:      "unchecked",
				Initial: "develop",
				Steps: []Step{
					{State: "develop", Performs: develop, Next: []string{"review"}},
					{State: "review", Performs: review, Next: []string{"integrate"}},
					{State: "integrate", Performs: integrate},
				},
			},
			missing: capability.ChecksExecute,
		},
		{
			name: "one path collects the verdict and another does not",
			topology: Topology{
				ID:      "sometimes-judged",
				Initial: "develop",
				Steps: []Step{
					{State: "develop", Performs: develop, Next: []string{"check"}},
					// The checks lead to the review and, on their other outcome,
					// straight to the promotion.
					{State: "check", Performs: check, Next: []string{"review", "integrate"}},
					{State: "review", Performs: review, Next: []string{"integrate"}},
					{State: "integrate", Performs: integrate},
				},
			},
			missing: capability.ReviewVerdict,
		},
		{
			// The one a has-crossed analysis admits, and the most obvious extra state
			// anybody would add: everything was collected, and then the change was
			// rewritten, so none of it describes what is being promoted.
			name: "the change is rewritten after the verdict",
			topology: Topology{
				ID:      "reworked",
				Initial: "develop",
				Steps: []Step{
					{State: "develop", Performs: develop, Next: []string{"check"}},
					{State: "check", Performs: check, Next: []string{"review"}},
					{State: "review", Performs: review, Next: []string{"rework"}},
					{State: "rework", Performs: rework, Next: []string{"integrate"}},
					{State: "integrate", Performs: integrate},
				},
			},
			missing: capability.ReviewVerdict,
		},
		{
			// The same, reached by one route out of two. The route through the repair
			// is the only one that matters, and it is the one an analysis over some
			// path rather than every path would not look at.
			name: "one route back through a repair and one straight on",
			topology: Topology{
				ID:      "sometimes-reworked",
				Initial: "develop",
				Steps: []Step{
					{State: "develop", Performs: develop, Next: []string{"check"}},
					{State: "check", Performs: check, Next: []string{"review"}},
					{State: "review", Performs: review, Next: []string{"integrate", "rework"}},
					{State: "rework", Performs: rework, Next: []string{"integrate"}},
					{State: "integrate", Performs: integrate},
				},
			},
			missing: capability.ChecksExecute,
		},
		{
			name: "the promotion is the first thing it does",
			topology: Topology{
				ID:      "promote-first",
				Initial: "integrate",
				Steps: []Step{
					{State: "integrate", Performs: integrate, Next: []string{"develop"}},
					{State: "develop", Performs: develop, Next: []string{"check"}},
					{State: "check", Performs: check, Next: []string{"review"}},
					{State: "review", Performs: review, Next: []string{"integrate"}},
				},
			},
			missing: capability.ChecksExecute,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			err := CheckTopology(testCase.topology)
			if err == nil {
				t.Fatal("CheckTopology() admitted a sequence that can promote without the evidence that earns it")
			}
			if !strings.Contains(err.Error(), IntegrationFollowsEvidence) {
				t.Errorf("CheckTopology() error = %v, and it does not name the policy it refused under", err)
			}
			if !strings.Contains(err.Error(), string(testCase.missing)) {
				t.Errorf("CheckTopology() error = %v, and it does not name the missing %q", err, testCase.missing)
			}
			if !strings.Contains(err.Error(), testCase.topology.ID) {
				t.Errorf("CheckTopology() error = %v, and it does not name the sequence it refused", err)
			}
		})
	}
}

// TestALoopBackIntoTheSequenceKeepsItsEvidence is what the delivery loop needs
// and a path check written carelessly would break: a review that asks for changes
// goes back to the developer, so the promotion has predecessors that lead round
// again, and the analysis has to settle rather than conclude from the first pass.
func TestALoopBackIntoTheSequenceKeepsItsEvidence(t *testing.T) {
	t.Parallel()

	looping := Topology{
		ID:      "repairing",
		Initial: "develop",
		Steps: []Step{
			{State: "develop", Performs: develop, Next: []string{"check"}},
			{State: "check", Performs: check, Next: []string{"review", "develop"}},
			{State: "review", Performs: review, Next: []string{"integrate", "develop"}},
			// The promotion goes back to the developer when it lost its race, which
			// is a transition out of the integrating state and into one that has
			// collected nothing.
			{State: "integrate", Performs: integrate, Next: []string{"develop"}},
		},
	}
	if err := CheckTopology(looping); err != nil {
		t.Errorf("CheckTopology() error = %v; a sequence that repairs and tries again still crosses both", err)
	}
}

// TestAStepNoPathReachesIsNotAPath states the one thing the analysis is
// deliberately quiet about, so that it is a decision somebody made rather than a
// hole somebody finds. A state nothing can transition into is a state no
// instance stands in, so no promotion happens through it.
func TestAStepNoPathReachesIsNotAPath(t *testing.T) {
	t.Parallel()

	stranded := Topology{
		ID:      "stranded",
		Initial: "develop",
		Steps: []Step{
			{State: "develop", Performs: develop, Next: []string{"check"}},
			{State: "check", Performs: check, Next: []string{"review"}},
			{State: "review", Performs: review},
			// Nothing leads here.
			{State: "integrate", Performs: integrate},
		},
	}
	if err := CheckTopology(stranded); err != nil {
		t.Errorf("CheckTopology() error = %v; a state no transition reaches is one no instance ever performs", err)
	}
}

// TestASeparationRefusalReportsEverythingWrong is the same bargain validation
// makes: a definition is written by hand, and one answer per reload is how
// somebody gives up on a format.
func TestASeparationRefusalReportsEverythingWrong(t *testing.T) {
	t.Parallel()

	wrong := Topology{
		ID:      "wrong-twice",
		Initial: "develop",
		Steps: []Step{
			{State: "develop", Performs: performing("candidate.develop-and-review", capability.WorktreeMutate, capability.ReviewVerdict), Next: []string{"promote"}},
			{State: "promote", Performs: performing("candidate.promote", capability.TargetBranchMutate)},
		},
	}
	err := CheckTopology(wrong)
	if err == nil {
		t.Fatal("CheckTopology() admitted a sequence three policies refuse")
	}
	for _, policy := range []string{AuthorshipIsNeverJudgment, PromotionIsNeverUnleased, IntegrationFollowsEvidence} {
		if !strings.Contains(err.Error(), policy) {
			t.Errorf("CheckTopology() error = %v, and it does not report %q", err, policy)
		}
	}
}

// holders is a statement of who holds what, for the role half of the policy.
type holders map[domain.AgentRole][]capability.Capability

func (h holders) Roles() []domain.AgentRole {
	roles := make([]domain.AgentRole, 0, len(h))
	for _, role := range domain.Roles() {
		if _, described := h[role]; described {
			roles = append(roles, role)
		}
	}
	return roles
}

func (h holders) Holds(role domain.AgentRole, required capability.Capability) bool {
	return slices.Contains(h[role], required)
}

// TestNoRoleMayHoldThePromotion is the half of the third rule no sequence can
// answer. `one-promotion-per-target-branch` says no agent performs a promotion,
// and a bundle conferring either half is a role that could.
func TestNoRoleMayHoldThePromotion(t *testing.T) {
	t.Parallel()

	if err := CheckHolders(holders{
		domain.RoleReviewer:  {capability.ReviewVerdict, capability.RepositoryRead},
		domain.RoleDeveloper: {capability.WorktreeMutate, capability.ForgePublish},
	}); err != nil {
		t.Errorf("CheckHolders() error = %v; neither role holds either half of the promotion", err)
	}

	for _, held := range []capability.Capability{capability.TargetBranchMutate, capability.PromotionLease} {
		err := CheckHolders(holders{domain.RoleReviewer: {capability.ReviewVerdict, held}})
		if err == nil {
			t.Fatalf("CheckHolders() admitted a role holding %q", held)
		}
		if !strings.Contains(err.Error(), JudgmentNeverPromotes) {
			t.Errorf("CheckHolders() error = %v, and it does not name the policy it refused under", err)
		}
		if !strings.Contains(err.Error(), string(held)) {
			t.Errorf("CheckHolders() error = %v, and it does not name the %q it refused", err, held)
		}
	}
}
