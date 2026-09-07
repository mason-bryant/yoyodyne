package chat

import (
	"slices"
	"strings"
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

// The prompt a side turn is taken under is the side thread's contract and never
// the role's own. The role's contract describes a thread that admits work,
// raises proposals, and issues directives; sending it here would leave the role
// to work out which half it was under on the one turn where getting that wrong is
// an action nobody ratified.
func TestASideTurnIsTakenUnderTheSideThreadsContract(t *testing.T) {
	t.Parallel()

	for _, role := range ConversationalRoles() {
		authority, known := AuthorityFor(role)
		if !known {
			t.Fatalf("AuthorityFor(%s) is not known", role)
		}
		prompt := SidePrompt(role, "")
		if !strings.Contains(prompt, sidestream.SideThreadContract) {
			t.Errorf("the %s side prompt does not carry the side thread's contract", role)
		}
		if strings.Contains(prompt, authority.Contract) {
			t.Errorf("the %s side prompt carries the role's own contract, which describes a thread that acts", role)
		}
		// It is still that role, said in the role's own words: the narrowing takes
		// authority away and leaves what a role is called and is answerable for.
		if !strings.Contains(prompt, authority.Title) || !strings.Contains(prompt, authority.Owns) {
			t.Errorf("the %s side prompt does not say which role is answering or what it owns", role)
		}
		// And a persona is placed after it and told it widens nothing, exactly as
		// every other prompt here places one.
		withPersona := SidePrompt(role, "  Answer in one paragraph.  ")
		if !strings.HasPrefix(withPersona, prompt) || !strings.HasSuffix(withPersona, "Answer in one paragraph.") {
			t.Errorf("the %s side prompt with a persona = %q, want the persona after the contract", role, withPersona)
		}
	}
}

// A role the harness holds no contract for gets no side thread contract either:
// it is told it has nothing to answer with rather than being sent a prompt with
// no authority statement in it at all.
func TestASideTurnForARoleWithNoContractSaysSo(t *testing.T) {
	t.Parallel()

	prompt := SidePrompt("auditor", "")
	if !strings.Contains(prompt, "holds no contract for this role") {
		t.Fatalf("SidePrompt() for an unknown role = %q, want it to say there is no contract", prompt)
	}
	if strings.Contains(prompt, sidestream.SideThreadContract) {
		t.Fatal("a role the harness knows nothing about was sent a side thread's contract")
	}
}
