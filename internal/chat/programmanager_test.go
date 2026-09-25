package chat

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/exchange"
	"github.com/mason-bryant/yoyodyne/internal/invariant"
)

// The program manager's row of the authority table is held to what its design
// excludes, one refusal at a time, rather than to a copy of the row: a
// capability arriving in its bundle that lets any of these through fails here.
func TestTheProgramManagerIsRefusedWhatItsDesignExcludes(t *testing.T) {
	t.Parallel()

	authority, known := AuthorityFor(domain.RoleProgramManager)
	if !known {
		t.Fatal("AuthorityFor(program-manager) reports no authority; the sixth role holds a conversation")
	}
	if authority.Title != "program manager" {
		t.Errorf("the program manager is titled %q, want the name written in full", authority.Title)
	}
	if !slices.Contains(ConversationalRoles(), domain.RoleProgramManager) {
		t.Error("the program manager is not among the roles an operator can address")
	}

	session := &Session{}
	session.state.Role = domain.RoleProgramManager
	triage := func(decision string) TrackerAction {
		action := TrackerAction{Action: actionTriage, ID: "yoyodyne-ifd.1", Run: "run-1", Decision: decision, Reason: "because"}
		if decision == "cross" {
			action.Budget = "review round"
		}
		return action
	}
	for _, refused := range []struct {
		what   string
		action TrackerAction
	}{
		{"close", TrackerAction{Action: actionClose, ID: "yoyodyne-ifd.1", Reason: "done"}},
		{"retire", TrackerAction{Action: actionRetire, ID: "yoyodyne-ifd.1", Reason: "not wanted"}},
		{"a repair of stale state", TrackerAction{Action: actionRepair, ID: "yoyodyne-ifd.1", Reason: "stale"}},
		{"handling a report", TrackerAction{Action: actionHandle, Reason: "read"}},
		{"a brake decision", TrackerAction{Action: actionBrake, Reason: "release"}},
		{"a repair decision", triage("repair")},
		{"a re-run decision", triage("rerun")},
		{"a re-scope decision", triage("rescope")},
		{"a re-arm decision", triage("rearm")},
		{"a wait decision", triage("wait")},
		{"an escalation", triage("escalate")},
		{"a cap crossing", triage("cross")},
	} {
		if authority.MayAct(refused.action.Action) {
			t.Errorf("the program manager may ask for %q (%s)", refused.action.Action, refused.what)
		}
		err := session.authorize(parsedReply{Actions: []TrackerAction{refused.action}})
		var refusal *AuthorityError
		if !errors.As(err, &refusal) {
			t.Errorf("authorize() of %s = %v, want an authority refusal", refused.what, err)
		}
	}

	// Everything upstream of the lane is somebody else's, and none of it is
	// reachable from this conversation: no proposal, concern, research, or
	// evaluation, and no document or invariant of any kind.
	for _, flag := range []struct {
		name string
		held bool
	}{
		{"Proposals", authority.Proposals},
		{"Concerns", authority.Concerns},
		{"Research", authority.Research},
		{"Evaluations", authority.Evaluations},
		{"ParentRequired", authority.ParentRequired},
	} {
		if flag.held {
			t.Errorf("the program manager's %s = true", flag.name)
		}
	}
	for _, kind := range artifact.Kinds() {
		if err := artifact.Authorize(domain.RoleProgramManager, kind); !errors.Is(err, artifact.ErrUnauthorized) {
			t.Errorf("artifact.Authorize(program-manager, %s) = %v, want ErrUnauthorized", kind, err)
		}
	}
	if err := invariant.Authorize(domain.RoleProgramManager); !errors.Is(err, invariant.ErrUnauthorized) {
		t.Errorf("invariant.Authorize(program-manager) = %v, want ErrUnauthorized", err)
	}

	// What it does hold: the reads, the named repository read, a memory, and
	// both ends of the ask channel.
	if err := session.authorize(parsedReply{Actions: []TrackerAction{{Action: actionRead, ID: "yoyodyne-ifd.1"}, {Action: actionSurvey}}}); err != nil {
		t.Errorf("authorize() of a read and a survey = %v, want both permitted", err)
	}
	if !authority.RepositoryReads || !authority.Memory || !authority.Asks || !authority.Answers {
		t.Errorf("the program manager holds RepositoryReads=%t Memory=%t Asks=%t Answers=%t, want all four",
			authority.RepositoryReads, authority.Memory, authority.Asks, authority.Answers)
	}
}

// Every write the program manager holds is scoped to its lane, and no lane is
// enforced yet, so every one of them is refused whole — which is the design's
// exclusion of every action on an item outside the lane, made while no item is
// inside one.
func TestTheProgramManagersLaneWritesAreRefusedUntilALaneIsEnforced(t *testing.T) {
	t.Parallel()

	authority, _ := AuthorityFor(domain.RoleProgramManager)
	want := []string{
		actionCreate, actionAttribute, actionUpdate, actionLabel, actionReparent,
		actionReprioritize, actionPark, actionUnpark, actionLink, actionUnlink,
	}
	if !slices.Equal(authority.LaneActions, want) {
		t.Fatalf("the program manager's lane-scoped actions = %v, want %v", authority.LaneActions, want)
	}
	session := &Session{}
	session.state.Role = domain.RoleProgramManager
	for _, action := range want {
		if !authority.MayAct(action) {
			t.Errorf("the program manager may not ask for %q at all; it holds it inside its lane", action)
		}
		err := session.authorize(parsedReply{Actions: []TrackerAction{{Action: action, ID: "yoyodyne-ifd.1", Reason: "why"}}})
		var refusal *AuthorityError
		if !errors.As(err, &refusal) || !strings.Contains(err.Error(), "lane") {
			t.Errorf("authorize() of %q = %v, want it refused for want of a lane", action, err)
		}
	}
	// No other role holds an action through the lane, so nothing about theirs
	// changed.
	for _, role := range ConversationalRoles() {
		if role == domain.RoleProgramManager {
			continue
		}
		other, _ := AuthorityFor(role)
		if len(other.LaneActions) > 0 {
			t.Errorf("the %s holds lane-scoped actions %v", role.Title(), other.LaneActions)
		}
	}
	// And it is told nothing about admission it cannot make.
	if clause := admissionClause(authority, Admission{}); clause != "" {
		t.Errorf("the program manager is sent an admission clause while its creations are refused: %q", clause)
	}
}

// The program manager is on the ask channel at both ends: it may open an
// exchange, and another role may put one to it.
func TestAnExchangeRunsToAndFromTheProgramManager(t *testing.T) {
	t.Parallel()

	programManager, _ := AuthorityFor(domain.RoleProgramManager)
	developmentManager, _ := AuthorityFor(domain.RoleDevelopmentManager)
	if err := refuseUnauthorizedAsk(programManager, parsedReply{Ask: &exchange.Ask{Role: domain.RoleDevelopmentManager, Question: "is this a pattern?"}}); err != nil {
		t.Errorf("the program manager was refused an ask to the development manager: %v", err)
	}
	if err := refuseUnauthorizedAsk(developmentManager, parsedReply{Ask: &exchange.Ask{Role: domain.RoleProgramManager, Question: "what does your lane say?"}}); err != nil {
		t.Errorf("the development manager was refused an ask to the program manager: %v", err)
	}
	if err := refuseUnauthorizedAsk(programManager, parsedReply{Ask: &exchange.Ask{Role: domain.RoleProgramManager, Question: "what?"}}); err == nil {
		t.Error("the program manager was allowed to ask its own role")
	}
	if !slices.Contains(askableRoleNames(), string(domain.RoleProgramManager)) {
		t.Errorf("askableRoleNames() = %v, missing the program manager", askableRoleNames())
	}
	if !strings.Contains(programManager.Contract, exchange.AskingContract) {
		t.Error("the program manager's contract does not tell it how to ask")
	}
	if prompt := AnsweringPrompt(domain.RoleProgramManager, ""); !strings.Contains(prompt, "program manager") {
		t.Errorf("the answering prompt does not say who is answering: %q", prompt)
	}
}
