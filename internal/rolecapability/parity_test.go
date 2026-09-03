package rolecapability_test

// The converted authorization sites, held against the decisions they made before
// they were converted.
//
// Every site below used to compare a role name — `role == owner`,
// `role == domain.RoleArchitect`, a hand-written list of tracker actions per role
// — and now asks the role-capability registry what the role holds. The claim the
// conversion rests on is that not one answer changed, and a claim like that is
// only worth anything written down as the answers themselves.
//
// So the tables here are the pre-conversion decision tables, transcribed from the
// literals the conversion removed rather than generated from the registry. A
// capability moved between bundles by accident fails here: the new answer and the
// old one disagree, and the old one is in this file where a reviewer can read it
// against the diff. Deriving these expectations from the registry instead would
// make the test agree with whatever the registry says, which is the one thing it
// must not do.
//
// It is an external test package because it reads the converted sites, and those
// packages read this one.

import (
	"errors"
	"slices"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/invariant"
)

// artifactOwners is who owned each kind of document before the conversion, read
// off the switch `artifact.Owner` used to be.
var artifactOwners = map[artifact.Kind]domain.AgentRole{
	artifact.KindBrief:         domain.RoleProductManager,
	artifact.KindGoals:         domain.RoleProductManager,
	artifact.KindNonGoals:      domain.RoleProductManager,
	artifact.KindDesign:        domain.RoleArchitect,
	artifact.KindSpecification: domain.RoleArchitect,
	artifact.KindDecision:      domain.RoleArchitect,
}

func TestArtifactOwnershipDecidesWhatItDecidedBeforeTheConversion(t *testing.T) {
	t.Parallel()

	if len(artifact.Kinds()) != len(artifactOwners) {
		t.Fatalf("artifact.Kinds() has %d kinds and this table names %d; a kind that arrived has no recorded owner to be held to",
			len(artifact.Kinds()), len(artifactOwners))
	}
	for _, kind := range artifact.Kinds() {
		want, recorded := artifactOwners[kind]
		if !recorded {
			t.Errorf("%s has no recorded pre-conversion owner", kind)
			continue
		}
		owner, known := artifact.Owner(kind)
		if !known || owner != want {
			t.Errorf("Owner(%s) = %q, %t; want %q, true", kind, owner, known, want)
		}
		for _, role := range domain.Roles() {
			err := artifact.Authorize(role, kind)
			if authorized := err == nil; authorized != (role == want) {
				t.Errorf("Authorize(%s, %s) error = %v; the %s %s own a %s artifact",
					role, kind, err, role.Title(), permission(role == want), kind)
			}
			if err != nil && !errors.Is(err, artifact.ErrUnauthorized) {
				t.Errorf("Authorize(%s, %s) refused with %v, which is not ErrUnauthorized; a caller cannot tell it from a malformed document",
					role, kind, err)
			}
		}
	}
}

// A kind nobody declares has no owner and no authorization, which is the answer
// the switch gave and the one a lookup through the registry has to keep: a
// document nobody can place is not one anybody can be authorized over.
func TestAnUnknownArtifactKindStillHasNoOwner(t *testing.T) {
	t.Parallel()

	if owner, known := artifact.Owner(artifact.Kind("charter")); known {
		t.Errorf("Owner(charter) = %q, true; an unplaceable kind has no owner", owner)
	}
	for _, role := range domain.Roles() {
		err := artifact.Authorize(role, artifact.Kind("charter"))
		if err == nil {
			t.Errorf("Authorize(%s, charter) authorized a kind nothing declares", role)
			continue
		}
		if errors.Is(err, artifact.ErrUnauthorized) {
			t.Errorf("Authorize(%s, charter) refused as unauthorized; an unknown kind is a malformed document rather than a refused role", role)
		}
	}
}

func TestInvariantAuthorityDecidesWhatItDecidedBeforeTheConversion(t *testing.T) {
	t.Parallel()

	for _, role := range domain.Roles() {
		err := invariant.Authorize(role)
		if authorized := err == nil; authorized != (role == domain.RoleArchitect) {
			t.Errorf("Authorize(%s) error = %v; the %s %s create, amend, or retire an invariant",
				role, err, role.Title(), permission(role == domain.RoleArchitect))
		}
		if err != nil && !errors.Is(err, invariant.ErrUnauthorized) {
			t.Errorf("Authorize(%s) refused with %v, which is not ErrUnauthorized", role, err)
		}
	}
	if err := invariant.Authorize(""); err == nil {
		t.Error("Authorize(\"\") authorized a mutation no role was named for")
	}
}

// conversationAuthority is one role's row of the table `internal/chat` used to
// write out by hand.
type conversationAuthority struct {
	title          string
	owns           string
	trackerActions []string
	parentRequired bool
	proposals      bool
	concerns       bool
	research       bool
	evaluations    bool
	asks           bool
}

// conversationAuthorities is that table, transcribed. The action lists are in the
// order the rows wrote them, because a refusal names them in order and the wording
// of a refusal is what the operator reads.
var conversationAuthorities = map[domain.AgentRole]conversationAuthority{
	domain.RoleProductManager: {
		title: "product manager",
		owns:  "the brief, the goals, and what is admitted to the backlog and in what order",
		trackerActions: []string{
			"read", "survey", "create", "attribute", "update",
			"reparent", "reprioritize", "park", "unpark",
			"link", "unlink", "close", "retire", "handle",
		},
		proposals:   true,
		concerns:    true,
		research:    true,
		evaluations: true,
		asks:        true,
	},
	domain.RoleArchitect: {
		title:          "architect",
		owns:           "the designs, the decision records, and the architectural invariants",
		trackerActions: []string{"read", "survey"},
		asks:           true,
	},
	domain.RoleDevelopmentManager: {
		title: "development manager",
		owns:  "decomposition, dependency structure, and triage of work that has stopped moving",
		trackerActions: []string{
			"read", "survey", "create", "update",
			"reparent", "link", "unlink", "triage",
		},
		parentRequired: true,
		asks:           true,
	},
	domain.RoleDeveloper: {
		title:          "developer",
		owns:           "no document and no queue; it implements one bounded work item inside a run",
		trackerActions: []string{"read", "survey"},
	},
	domain.RoleReviewer: {
		title:          "reviewer",
		owns:           "no document and no queue; it judges one change inside a run",
		trackerActions: []string{"read", "survey"},
	},
}

func TestConversationAuthorityDecidesWhatItDecidedBeforeTheConversion(t *testing.T) {
	t.Parallel()

	for _, role := range domain.Roles() {
		want, recorded := conversationAuthorities[role]
		if !recorded {
			t.Errorf("the %s has no recorded pre-conversion conversation authority", role.Title())
			continue
		}
		authority, known := chat.AuthorityFor(role)
		if !known {
			t.Errorf("AuthorityFor(%s) reports no authority; every role the harness has holds a conversation", role)
			continue
		}
		if authority.Title != want.title {
			t.Errorf("the %s is titled %q, want %q", role, authority.Title, want.title)
		}
		if authority.Owns != want.owns {
			t.Errorf("the %s owns %q, want %q", role, authority.Owns, want.owns)
		}
		if !slices.Equal(authority.TrackerActions, want.trackerActions) {
			t.Errorf("the %s may ask for %v, want %v", role, authority.TrackerActions, want.trackerActions)
		}
		// The same list asked one action at a time, because MayAct is what every
		// call site actually calls and a list nothing reads is not a decision.
		for _, action := range trackerActionsInTheContract(t) {
			if may, wanted := authority.MayAct(action), slices.Contains(want.trackerActions, action); may != wanted {
				t.Errorf("MayAct(%s) for the %s = %t, want %t", action, role, may, wanted)
			}
		}
		for _, flag := range []struct {
			name string
			got  bool
			want bool
		}{
			{"ParentRequired", authority.ParentRequired, want.parentRequired},
			{"Proposals", authority.Proposals, want.proposals},
			{"Concerns", authority.Concerns, want.concerns},
			{"Research", authority.Research, want.research},
			{"Evaluations", authority.Evaluations, want.evaluations},
			{"Asks", authority.Asks, want.asks},
		} {
			if flag.got != flag.want {
				t.Errorf("the %s's %s = %t, want %t", role, flag.name, flag.got, flag.want)
			}
		}
		if authority.Contract == "" {
			t.Errorf("the %s carries no contract; a role with nothing to send has no conversation", role)
		}
	}
	for role := range conversationAuthorities {
		if !slices.Contains(domain.Roles(), role) {
			t.Errorf("this table records the %s and the harness has no such role", role)
		}
	}
}

// A role outside the harness's five holds nothing in a conversation, which is the
// answer the map lookup gave and the answer a bundle lookup has to keep: an
// authority invented at the point of use is how authority leaks.
func TestAnUnknownRoleStillHoldsNoConversationAuthority(t *testing.T) {
	t.Parallel()

	for _, unknown := range []domain.AgentRole{"", "release-manager"} {
		if authority, known := chat.AuthorityFor(unknown); known {
			t.Errorf("AuthorityFor(%q) = %+v, true; want no authority at all", unknown, authority)
		}
	}
}

// trackerActionsInTheContract is every action any role's recorded row named. The
// union is what "every capability-gated act" means for the tracker: an action no
// row named is one no role could ask for either way.
func trackerActionsInTheContract(t *testing.T) []string {
	t.Helper()

	var actions []string
	for _, recorded := range conversationAuthorities {
		for _, action := range recorded.trackerActions {
			if !slices.Contains(actions, action) {
				actions = append(actions, action)
			}
		}
	}
	slices.Sort(actions)
	return actions
}

func permission(allowed bool) string {
	if allowed {
		return "may"
	}
	return "may not"
}
