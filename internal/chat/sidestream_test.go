package chat

import (
	"slices"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/sidestream"
)

// A side thread cannot take an action the design reserves to the main thread.
// The product manager is the case that matters: it is the role that admits work,
// reorders the backlog, proposes, raises concerns, commissions research, and
// records evaluations, so a side thread of that role is where a narrowing that
// did not hold would be worth the most.
func TestASideThreadCannotTakeAnActionReservedToTheMainThread(t *testing.T) {
	t.Parallel()

	for _, role := range ConversationalRoles() {
		authority, known := AuthorityFor(role)
		if !known {
			t.Fatalf("AuthorityFor(%s) is not known; the table this narrows is not the one being read", role)
		}
		aside := authority.OnSideStream()

		for _, action := range trackerActionNames {
			if !aside.MayAct(action) {
				continue
			}
			if !sidestream.Permits(trackerCapabilities[action]) {
				t.Errorf("a %s side thread may ask for %q, which is %q and not a side thread's",
					role, action, trackerCapabilities[action])
			}
			if !authority.MayAct(action) {
				t.Errorf("a %s side thread may ask for %q and the role itself may not; a side thread narrows and never widens",
					role, action)
			}
		}
		if aside.Proposals || aside.Concerns || aside.Research || aside.Evaluations || aside.Asks {
			t.Errorf("a %s side thread may propose=%v, raise a concern=%v, commission research=%v, record an evaluation=%v, or ask=%v; every one of those is an act the main thread ratifies",
				role, aside.Proposals, aside.Concerns, aside.Research, aside.Evaluations, aside.Asks)
		}
		// What is not authority is untouched: a side thread is still this role, so
		// it is still called what the role is called and still sent the role's own
		// contract.
		if aside.Role != authority.Role || aside.Title != authority.Title ||
			aside.Contract != authority.Contract || aside.Owns != authority.Owns {
			t.Errorf("a %s side thread is described as something other than that role", role)
		}
	}
}

// The product manager, spelled out. Everything it may ask for on its own thread
// is named here, and the two lists are what the narrowing has to come to.
func TestTheProductManagerOnASideThreadReadsAndSurveysAndNothingElse(t *testing.T) {
	t.Parallel()

	authority, known := AuthorityFor(domain.RoleProductManager)
	if !known {
		t.Fatal("the product manager has no conversation authority")
	}
	if !authority.MayAct(actionCreate) || !authority.MayAct(actionClose) || !authority.Proposals {
		t.Fatalf("the product manager's own authority = %#v, want the role that admits work", authority)
	}

	aside := authority.OnSideStream()
	want := []string{actionRead, actionSurvey}
	if !slices.Equal(aside.TrackerActions, want) {
		t.Fatalf("OnSideStream().TrackerActions = %v, want %v", aside.TrackerActions, want)
	}
	for _, refused := range []string{
		actionCreate, actionAttribute, actionUpdate, actionReparent, actionReprioritize,
		actionPark, actionUnpark, actionLink, actionUnlink, actionClose, actionRetire,
		actionTriage, actionHandle,
	} {
		if aside.MayAct(refused) {
			t.Errorf("a product manager side thread may ask for %q; admitting, ordering, and closing work belong to the main thread", refused)
		}
	}
}

// The narrowing is applied to a role's own authority rather than to a fixed list,
// so a role that may not read the tracker at all does not gain a read here.
func TestASideThreadNeverWidensARoleThatHoldsLess(t *testing.T) {
	t.Parallel()

	bare := Authority{Role: domain.RoleDeveloper, Title: "developer"}
	aside := bare.OnSideStream()
	if len(aside.TrackerActions) != 0 {
		t.Fatalf("OnSideStream() of a role with no tracker authority = %v, want none", aside.TrackerActions)
	}
	if aside.Proposals || aside.Concerns || aside.Research || aside.Evaluations || aside.Asks {
		t.Fatalf("OnSideStream() of a role holding nothing = %#v, want it holding nothing", aside)
	}
}
