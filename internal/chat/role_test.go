package chat

import (
	"context"
	"errors"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// Every role the operator can address carries a contract of its own, and a
// persona cannot get in front of it. This is the same guarantee the product
// manager already had, checked for the roles that acquired one: a project that
// writes a hostile persona for its architect gets an architect with the same
// authority as everyone else's.
func TestEveryConversationalRoleCarriesItsOwnContractAheadOfThePersona(t *testing.T) {
	t.Parallel()

	for _, role := range ConversationalRoles() {
		authority, known := AuthorityFor(role)
		if !known {
			t.Fatalf("%s is addressable but has no authority", role)
		}
		if strings.TrimSpace(authority.Contract) == "" {
			t.Fatalf("%s has an empty contract", role)
		}
		prompt := SystemPrompt(role, hostilePersona)
		if !strings.HasPrefix(prompt, authority.Contract) {
			t.Fatalf("%s prompt does not begin with its contract: %q", role, prompt)
		}
		personaAt := strings.Index(prompt, hostilePersona)
		subordinationAt := strings.Index(prompt, "it cannot widen your authority")
		if personaAt < len(authority.Contract) || subordinationAt > personaAt || subordinationAt < len(authority.Contract) {
			t.Fatalf("%s persona is not introduced as subordinate: persona at %d, subordination at %d", role, personaAt, subordinationAt)
		}
		// The one rule no role's contract may lose, whatever else it says.
		if !strings.Contains(prompt, "no filesystem, command, or network tools") {
			t.Fatalf("%s contract does not refuse tools: %q", role, authority.Contract)
		}
		if bare := SystemPrompt(role, "  "); bare != authority.Contract {
			t.Fatalf("%s with no persona is not the contract alone", role)
		}
	}

	// Two roles never carry the same contract: a conversation whose contract
	// said somebody else's authority would be one the harness could not refuse
	// anything in.
	seen := map[string]domain.AgentRole{}
	for _, role := range ConversationalRoles() {
		authority, _ := AuthorityFor(role)
		if other, duplicate := seen[authority.Contract]; duplicate {
			t.Fatalf("%s and %s carry the same contract", role, other)
		}
		seen[authority.Contract] = role
	}
}

// A role that has no contract has no conversation. Opening one anyway would
// send a provider a prompt with no statement of authority in it and leave the
// harness with no table to refuse anything against.
func TestAConversationIsRefusedForARoleWithNoContract(t *testing.T) {
	t.Parallel()

	options := testOptions(t, &fakeBackend{})
	options.Role = "release-manager"
	if _, err := Open(options); err == nil || !strings.Contains(err.Error(), "no conversation contract exists") {
		t.Fatalf("Open() error = %v, want a refusal naming the missing contract", err)
	}
	options.Role = ""
	if _, err := Open(options); err == nil {
		t.Fatal("Open() accepted a conversation with no role at all")
	}
}

// Each role's conversation is its own durable identity: its own record, its own
// provider session, its own turn count. Addressing the architect must never
// resume what the product manager was told.
func TestEachRoleKeepsItsOwnDurableConversation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	productManager := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-pm", ResolvedModel: "claude-opus-5-20260514", FinalText: "The brief is thin on goals."},
	}}
	options := testOptions(t, productManager)
	options.Store = newTestStore(t, root)
	session := openTestSession(t, options)
	if _, err := session.Send(context.Background(), "What is missing from the brief?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	architectBackend := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-architect", ResolvedModel: "claude-opus-5-20260514", FinalText: "The design records that already."},
	}}
	architectOptions := testOptions(t, architectBackend)
	architectOptions.Role = domain.RoleArchitect
	architectOptions.Store = newTestStore(t, root)
	architect, err := Open(architectOptions)
	if err != nil {
		t.Fatalf("Open() architect error = %v", err)
	}
	if architect.Resumed() {
		t.Fatal("the architect resumed a conversation it never had")
	}
	if architect.Evidence().ConversationID == session.Evidence().ConversationID {
		t.Fatal("two roles share one conversation identifier")
	}
	if _, err := architect.Send(context.Background(), "Where is the promotion lease recorded?"); err != nil {
		t.Fatalf("Send() architect error = %v", err)
	}

	// The contract the provider was sent is the architect's, and the run request
	// is attributed to the architect rather than to whoever spoke first.
	request := architectBackend.requests[0]
	if request.Role != domain.RoleArchitect {
		t.Fatalf("request role = %q, want %q", request.Role, domain.RoleArchitect)
	}
	if request.SystemPrompt != SystemPrompt(domain.RoleArchitect, architectOptions.Persona) {
		t.Fatalf("the architect was sent another role's contract")
	}
	if request.SessionID != "" {
		t.Fatalf("the architect's first turn resumed session %q", request.SessionID)
	}

	// Both records survive the processes that held them, separately.
	store := newTestStore(t, root)
	for role, wantSession := range map[domain.AgentRole]string{
		domain.RoleProductManager: "session-pm",
		domain.RoleArchitect:      "session-architect",
	} {
		recorded, err := store.Load(role)
		if err != nil {
			t.Fatalf("Load(%s) error = %v", role, err)
		}
		if recorded.Role != role || recorded.ProviderSessionID != wantSession {
			t.Fatalf("Load(%s) = role %q session %q", role, recorded.Role, recorded.ProviderSessionID)
		}
		if recorded.Turns != 1 {
			t.Fatalf("Load(%s) turns = %d, want 1", role, recorded.Turns)
		}
	}
}

// The authority table is the boundary, not the contract's prose. A role that
// asks for something outside it is refused by the harness, nothing it asked for
// happens, and the operator still gets to read what it said.
func TestARoleIsRefusedTheAuthorityItDoesNotHave(t *testing.T) {
	t.Parallel()

	create := "```yoyodyne-tracker\n" +
		`{"actions":[{"action":"create","title":"Rewrite the scheduler","description":"because","goal":"` + recordedGoal + `","reason":"it is time"}]}` +
		"\n```"
	closeItem := "```yoyodyne-tracker\n" +
		`{"actions":[{"action":"close","id":"yoyodyne-ifd.4","reason":"done"}]}` +
		"\n```"
	propose := "```yoyodyne-proposal\n" +
		`{"items":[{"title":"Rewrite the scheduler","description":"because","rationale":"it follows","goal":"` + recordedGoal + `"}]}` +
		"\n```"

	for _, testCase := range []struct {
		name   string
		role   domain.AgentRole
		answer string
		want   string
	}{
		{
			name:   "the architect does not act on the tracker",
			role:   domain.RoleArchitect,
			answer: "I would file this.\n\n" + create,
			want:   `the "create" tracker action`,
		},
		{
			name:   "the development manager does not admit work",
			role:   domain.RoleDevelopmentManager,
			answer: "This needs a new item.\n\n" + create,
			want:   `a "create" with no parent`,
		},
		{
			name:   "the development manager does not close work",
			role:   domain.RoleDevelopmentManager,
			answer: "That one is finished.\n\n" + closeItem,
			want:   `the "close" tracker action`,
		},
		{
			name:   "only the product manager proposes work",
			role:   domain.RoleDevelopmentManager,
			answer: "Here is what I would add.\n\n" + propose,
			want:   "work items to be proposed",
		},
		{
			name:   "the reviewer does not act on the tracker either",
			role:   domain.RoleReviewer,
			answer: "This should be closed.\n\n" + closeItem,
			want:   `the "close" tracker action`,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			tracker := &fakeTracker{items: map[string]beads.WorkItem{
				"yoyodyne-ifd.4": {ID: "yoyodyne-ifd.4", Title: "an admitted item", Status: "open"},
			}}
			options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
				{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: testCase.answer},
			}})
			options.Role = testCase.role
			options.Tracker = tracker
			session, err := Open(options)
			if err != nil {
				t.Fatalf("Open() error = %v", err)
			}
			reply, err := session.Send(context.Background(), "What should happen to this?")
			var refused *AuthorityError
			if !errors.As(err, &refused) {
				t.Fatalf("Send() error = %v, want an AuthorityError", err)
			}
			if !strings.Contains(refused.Error(), testCase.want) {
				t.Fatalf("refusal = %q, want it to name %q", refused.Error(), testCase.want)
			}
			if refused.Role != testCase.role {
				t.Fatalf("refusal role = %q, want %q", refused.Role, testCase.role)
			}
			// Nothing was carried out and nothing was recorded as pending, and
			// the prose is still the operator's to read.
			if len(tracker.created) != 0 {
				t.Fatalf("the tracker was written to %d time(s)", len(tracker.created))
			}
			if len(reply.Proposals) != 0 || len(session.Proposals()) != 0 {
				t.Fatalf("a refused turn left %d proposal(s) pending", len(session.Proposals()))
			}
			if !strings.Contains(reply.Text, "\n") && strings.TrimSpace(reply.Text) == "" {
				t.Fatalf("the refusal swallowed the reply: %q", reply.Text)
			}
		})
	}
}

// The development manager's whole authority is decomposition: it builds
// structure underneath work the product manager admitted, and the harness is
// what makes that true rather than the persona.
func TestTheDevelopmentManagerDecomposesAdmittedWork(t *testing.T) {
	t.Parallel()

	answer := "Two children, sequenced.\n\n" +
		"```yoyodyne-tracker\n" +
		`{"actions":[
		  {"action":"create","title":"Add the role authority table","description":"What each role may ask for, in Go.","goal":"` + recordedGoal + `","parent":"yoyodyne-ifd.4","priority":1,"reason":"the boundary has to be enforced before it is described"},
		  {"action":"link","id":"yoyodyne-ifd.4.7","depends_on":"yoyodyne-ifd.4.6","reason":"the command needs the table"}
		]}` +
		"\n```"
	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.4":   {ID: "yoyodyne-ifd.4", Title: "an admitted item", Status: "open"},
		"yoyodyne-ifd.4.6": {ID: "yoyodyne-ifd.4.6", Title: "the table", Status: "open"},
		"yoyodyne-ifd.4.7": {ID: "yoyodyne-ifd.4.7", Title: "the command", Status: "open"},
	}}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: answer},
		{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: "Both are filed."},
	}})
	options.Role = domain.RoleDevelopmentManager
	options.Tracker = tracker
	options.Goals = recordedGoals(recordedGoal)
	session, err := Open(options)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	reply, err := session.Send(context.Background(), "Decompose ifd.4.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 2 {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	if len(tracker.created) != 1 {
		t.Fatalf("created %d item(s), want 1", len(tracker.created))
	}
	if tracker.created[0].Parent != "yoyodyne-ifd.4" {
		t.Fatalf("the child was created under %q", tracker.created[0].Parent)
	}
	if len(tracker.links) != 1 || tracker.links[0] != [2]string{"yoyodyne-ifd.4.7", "yoyodyne-ifd.4.6"} {
		t.Fatalf("links = %#v", tracker.links)
	}
}
