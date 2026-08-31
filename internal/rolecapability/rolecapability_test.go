package rolecapability

import (
	"slices"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/capability"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

func TestTheDefaultRegistryIsBuildable(t *testing.T) {
	t.Parallel()

	registry := mustBuild(t)
	if roles := registry.Roles(); !slices.Equal(roles, domain.Roles()) {
		t.Errorf("Roles() = %v, want %v", roles, domain.Roles())
	}
	for _, role := range domain.Roles() {
		bundle, described := registry.Bundle(role)
		if !described {
			t.Errorf("no bundle describes the %s", role.Title())
			continue
		}
		if len(bundle.Holds) == 0 {
			t.Errorf("the %s's bundle holds nothing", role.Title())
		}
		if bundle.Owns == "" {
			t.Errorf("the %s's bundle says nothing about what the role owns", role.Title())
		}
	}
}

// TestEveryDeclaredCapabilityHasAHolder is the completeness claim, and it is what
// the vocabulary is for: a capability an action can require and no role can
// satisfy is authority that resolves to nobody.
func TestEveryDeclaredCapabilityHasAHolder(t *testing.T) {
	t.Parallel()

	registry := mustBuild(t)
	for _, declared := range capability.All() {
		roles := registry.RolesHolding(declared)
		_, harness := registry.HarnessHolds(declared)
		if len(roles) == 0 && !harness {
			t.Errorf("nothing holds %q", declared)
		}
		if len(roles) > 0 && harness {
			t.Errorf("%q is held by %v and is also recorded as the harness's", declared, roles)
		}
	}
}

// TestThePromotionBelongsToNoRole pins what
// `one-promotion-per-target-branch` requires of any statement of authority: no
// agent performs a promotion, so no role's bundle may confer one.
func TestThePromotionBelongsToNoRole(t *testing.T) {
	t.Parallel()

	registry := mustBuild(t)
	for _, promoting := range []capability.Capability{capability.PromotionLease, capability.TargetBranchMutate} {
		if roles := registry.RolesHolding(promoting); len(roles) > 0 {
			t.Errorf("%q is held by %v; the promotion is the harness's and no agent performs one", promoting, roles)
		}
		held, harness := registry.HarnessHolds(promoting)
		if !harness {
			t.Errorf("%q is recorded neither as a role's nor as the harness's", promoting)
			continue
		}
		if held.Reason == "" {
			t.Errorf("%q is the harness's with no reason recorded", promoting)
		}
	}
}

// TestWhatTellsTheRolesApart holds the bundles to the boundaries the inventory
// already enforces, rather than to a copy of the table beside it. Each claim here
// is one an authorization site makes today by naming a role.
func TestWhatTellsTheRolesApart(t *testing.T) {
	t.Parallel()

	registry := mustBuild(t)
	cases := []struct {
		what  string
		who   capability.Capability
		holds []domain.AgentRole
	}{
		{"admitting work to the backlog", capability.BacklogAdmit, []domain.AgentRole{domain.RoleProductManager}},
		{"what is pulled next", capability.BacklogOrder, []domain.AgentRole{domain.RoleProductManager}},
		{"decomposing admitted work", capability.WorkDecompose, []domain.AgentRole{domain.RoleProductManager, domain.RoleDevelopmentManager}},
		{"triaging work that stopped moving", capability.WorkTriage, []domain.AgentRole{domain.RoleDevelopmentManager}},
		{"the brief and the goals", capability.ArtifactProductMutate, []domain.AgentRole{domain.RoleProductManager}},
		{"the designs and the decision records", capability.ArtifactDesignMutate, []domain.AgentRole{domain.RoleArchitect}},
		{"the architectural invariants", capability.InvariantMutate, []domain.AgentRole{domain.RoleArchitect}},
		{"the verdict a change is gated on", capability.ReviewVerdict, []domain.AgentRole{domain.RoleReviewer}},
		{"writing inside a run's worktree", capability.WorktreeMutate, []domain.AgentRole{domain.RoleDeveloper}},
		{"the inter-role ask channel", capability.ExchangeAsk, []domain.AgentRole{domain.RoleProductManager, domain.RoleArchitect, domain.RoleDevelopmentManager}},
		{"reading the tracker", capability.WorkItemRead, domain.Roles()},
	}
	for _, expected := range cases {
		if holding := registry.RolesHolding(expected.who); !slices.Equal(holding, expected.holds) {
			t.Errorf("%s (%q) is held by %v, want %v", expected.what, expected.who, holding, expected.holds)
		}
	}
}

func TestAnUnknownRoleHoldsNothing(t *testing.T) {
	t.Parallel()

	registry := mustBuild(t)
	for _, invented := range []domain.AgentRole{"", "operator", "Developer", "sentinel"} {
		if _, described := registry.Bundle(invented); described {
			t.Errorf("Bundle(%q) found a bundle, and nothing describes that role", invented)
		}
		if registry.Holds(invented, capability.WorkItemRead) {
			t.Errorf("Holds(%q, %q) = true; a role nobody described holds nothing", invented, capability.WorkItemRead)
		}
	}
}

// TestABundleHandsBackACopy is what stops a caller widening a role. A bundle a
// caller can append to is not a statement of authority.
func TestABundleHandsBackACopy(t *testing.T) {
	t.Parallel()

	registry := mustBuild(t)
	first, _ := registry.Bundle(domain.RoleReviewer)
	first.Holds[0] = capability.TargetBranchMutate
	second, _ := registry.Bundle(domain.RoleReviewer)
	if second.Holds[0] == first.Holds[0] {
		t.Error("Bundle() handed back the registry's own slice: editing it changed what the next caller sees")
	}
	if registry.Holds(domain.RoleReviewer, capability.TargetBranchMutate) {
		t.Error("editing a returned bundle gave the reviewer a capability nothing granted it")
	}
}

func TestABundleForARoleTheHarnessDoesNotHaveIsRefused(t *testing.T) {
	t.Parallel()

	refusal := refuse(t, append(bundles(), Bundle{
		Role:  "sentinel",
		Owns:  "watching",
		Holds: []capability.Capability{capability.RepositoryRead},
	}), harnessHeld())
	if !strings.Contains(refusal, "not one of the harness's roles") {
		t.Errorf("New() refused with %q, and not for the role it does not have", refusal)
	}
}

func TestTwoBundlesForOneRoleAreRefused(t *testing.T) {
	t.Parallel()

	doubled := bundles()
	doubled = append(doubled, Bundle{
		Role:  domain.RoleReviewer,
		Owns:  "judging, again",
		Holds: []capability.Capability{capability.TargetBranchMutate},
	})
	refusal := refuse(t, doubled, harnessHeld())
	if !strings.Contains(refusal, "more than one bundle") {
		t.Errorf("New() refused with %q, and not for the second bundle", refusal)
	}
}

func TestABundleHoldingACapabilityNothingDeclaresIsRefused(t *testing.T) {
	t.Parallel()

	invented := bundles()
	invented[0].Holds = append(slices.Clone(invented[0].Holds), "target-branch.delete")
	refusal := refuse(t, invented, harnessHeld())
	if !strings.Contains(refusal, "target-branch.delete") || !strings.Contains(refusal, "not a capability this repository declares") {
		t.Errorf("New() refused with %q, and not for the capability nothing declares", refusal)
	}
}

func TestABundleHoldingOneCapabilityTwiceIsRefused(t *testing.T) {
	t.Parallel()

	repeated := bundles()
	repeated[0].Holds = append(slices.Clone(repeated[0].Holds), capability.WorkItemRead)
	refusal := refuse(t, repeated, harnessHeld())
	if !strings.Contains(refusal, "twice") {
		t.Errorf("New() refused with %q, and not for the repetition", refusal)
	}
}

func TestABundleThatHoldsNothingIsRefused(t *testing.T) {
	t.Parallel()

	emptied := bundles()
	emptied[0].Holds = nil
	refusal := refuse(t, emptied, harnessHeld())
	if !strings.Contains(refusal, "holds nothing") {
		t.Errorf("New() refused with %q, and not for the empty bundle", refusal)
	}
}

func TestARoleWithNoBundleIsRefused(t *testing.T) {
	t.Parallel()

	described := bundles()
	missing := described[len(described)-1].Role
	refusal := refuse(t, described[:len(described)-1], harnessHeld())
	if !strings.Contains(refusal, "no bundle describes the "+missing.Title()) {
		t.Errorf("New() refused with %q, and not for the role nothing describes", refusal)
	}
}

func TestACapabilityNothingHoldsIsRefused(t *testing.T) {
	t.Parallel()

	orphaned := bundles()
	for index := range orphaned {
		orphaned[index].Holds = slices.DeleteFunc(slices.Clone(orphaned[index].Holds), func(held capability.Capability) bool {
			return held == capability.ReviewVerdict
		})
	}
	refusal := refuse(t, orphaned, harnessHeld())
	if !strings.Contains(refusal, "nothing holds \"review.verdict\"") {
		t.Errorf("New() refused with %q, and not for the capability with no holder", refusal)
	}
}

func TestACapabilityHeldByBothARoleAndTheHarnessIsRefused(t *testing.T) {
	t.Parallel()

	refusal := refuse(t, bundles(), append(harnessHeld(), Held{
		Capability: capability.ReviewVerdict,
		Reason:     "invented for this test",
	}))
	if !strings.Contains(refusal, "two answers to one question") {
		t.Errorf("New() refused with %q, and not for the capability claimed twice", refusal)
	}
}

func TestAHarnessCapabilityWithNoReasonIsRefused(t *testing.T) {
	t.Parallel()

	unexplained := harnessHeld()
	unexplained[0].Reason = ""
	refusal := refuse(t, bundles(), unexplained)
	if !strings.Contains(refusal, "with no reason") {
		t.Errorf("New() refused with %q, and not for the unexplained capability", refusal)
	}
}

func TestAHarnessCapabilityNothingDeclaresIsRefused(t *testing.T) {
	t.Parallel()

	refusal := refuse(t, bundles(), append(harnessHeld(), Held{
		Capability: "target-branch.delete",
		Reason:     "invented for this test",
	}))
	if !strings.Contains(refusal, "target-branch.delete") {
		t.Errorf("New() refused with %q, and not for the capability nothing declares", refusal)
	}
}

func mustBuild(t *testing.T) Registry {
	t.Helper()

	registry, err := Default()
	if err != nil {
		t.Fatalf("Default() error = %v", err)
	}
	return registry
}

// refuse builds a registry that is expected not to build, and hands back what it
// refused with.
func refuse(t *testing.T, described []Bundle, harness []Held) string {
	t.Helper()

	if _, err := New(described, harness); err != nil {
		return err.Error()
	}
	t.Fatal("New() built a registry it should have refused")
	return ""
}
