package notify

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func conversationWith(role domain.AgentRole) runstate.Conversation {
	return runstate.Conversation{
		SchemaVersion:  runstate.ConversationSchemaVersion,
		ConversationID: "chat-11558d32000000000000000000000000",
		ProductID:      "yoyodyne",
		RepositoryID:   "yoyodyne",
		Agent:          string(role),
		Role:           role,
		Backend:        domain.BackendClaudeCode,
		StartedAt:      moment,
		UpdatedAt:      moment,
	}
}

// recorded is one event of a conversation's log, built the way the conversation
// itself builds one so a test cannot pass against a payload nothing writes.
func recorded(t *testing.T, sequence uint64, eventType execution.EventType, payload any) execution.Event {
	t.Helper()
	event, err := execution.NewEvent("chat-11558d32000000000000000000000000", sequence, moment, eventType, "harness.chat", payload)
	if err != nil {
		t.Fatalf("record a %s event: %v", eventType, err)
	}
	return event
}

// applied is a tracker action the harness carried out, in the shape the
// conversation records one.
func applied(t *testing.T, sequence uint64, workItemID string, action map[string]any) execution.Event {
	t.Helper()
	return recorded(t, sequence, execution.EventTrackerActionApplied, map[string]any{
		"action_id":    "t1.1",
		"turn":         1,
		"action":       action,
		"work_item_id": workItemID,
		"summary":      "the harness's own account of it",
	})
}

// said selects one event of a log and insists the result can actually be said,
// because a milestone nothing has words for reaches nobody.
func said(t *testing.T, conversation runstate.Conversation, events []execution.Event, index int) (Notification, Message) {
	t.Helper()
	notification, err := FromConversation(conversation, events, index)
	if err != nil {
		t.Fatalf("select from conversation event %d: %v", index, err)
	}
	if notification.Silent() {
		t.Fatalf("conversation event %d said nothing", index)
	}
	message, err := Render(notification.Topic, notification.Speaker, notification.Event)
	if err != nil {
		t.Fatalf("a selected %s could not be said: %v", notification.Event.Kind, err)
	}
	return notification, message
}

func TestAdmittedWorkIsSaidByTheRoleThatAdmittedItWithItsGoal(t *testing.T) {
	// The queue growing is the harness's steering wheel turning, and an item that
	// does not say what it is for is the work nobody can later decide to stop.
	conversation := conversationWith(domain.RoleProductManager)
	events := []execution.Event{applied(t, 1, "yoyodyne-ifd.113", map[string]any{
		"action":      "create",
		"title":       "Report what conversations do to the backlog",
		"description": "the item's own words",
		"goal":        "Work the harness runs on its own is visible while it runs",
		"reason":      "the channel says nothing about the queue moving",
	})}
	notification, message := said(t, conversation, events, 0)
	if notification.Event.Kind != KindItemAdmitted {
		t.Fatalf("a creation by the product manager is %s", notification.Event.Kind)
	}
	if notification.Topic.Key() != "work-item:yoyodyne-ifd.113" {
		t.Fatalf("addressed to %q", notification.Topic.Key())
	}
	if notification.Speaker.Role != domain.RoleProductManager {
		t.Fatalf("spoken by %q", notification.Speaker.Key())
	}
	if notification.Event.Refs.ConversationID != conversation.ConversationID {
		t.Fatalf("refs %+v do not lead back to the conversation", notification.Event.Refs)
	}
	if !strings.Contains(message.Body, "Work the harness runs on its own is visible while it runs") {
		t.Fatalf("body %q does not say what the work is for", message.Body)
	}
	if !strings.Contains(message.Body, "Report what conversations do to the backlog") {
		t.Fatalf("body %q does not say what was admitted", message.Body)
	}
}

func TestACreationUnderAParentIsDecompositionRatherThanAdmission(t *testing.T) {
	// The development manager may not admit work at all, so recording what it did
	// as an admission would say it exercised authority it does not have.
	conversation := conversationWith(domain.RoleDevelopmentManager)
	parent := "yoyodyne-ifd.68"
	events := []execution.Event{applied(t, 1, "yoyodyne-ifd.68.5", map[string]any{
		"action":      "create",
		"title":       "The voice work under the reporting epic",
		"description": "the item's own words",
		"goal":        "Work the harness runs on its own is visible while it runs",
		"parent":      parent,
		"reason":      "the epic is too large to run as one item",
	})}
	notification, message := said(t, conversation, events, 0)
	if notification.Event.Kind != KindItemDecomposed {
		t.Fatalf("a creation under a parent is %s", notification.Event.Kind)
	}
	if notification.Speaker.Role != domain.RoleDevelopmentManager {
		t.Fatalf("spoken by %q", notification.Speaker.Key())
	}
	if !strings.Contains(message.Body, parent) {
		t.Fatalf("body %q does not say what was decomposed", message.Body)
	}
}

func TestAnAttributionAndAReprioritizationSayWhatChanged(t *testing.T) {
	conversation := conversationWith(domain.RoleProductManager)
	events := []execution.Event{
		applied(t, 1, "yoyodyne-ifd.99", map[string]any{
			"action": "attribute",
			"id":     "yoyodyne-ifd.99",
			"goal":   "Directives reach the agent regardless of which one received them",
			"reason": "it was admitted before goals were checked",
		}),
		applied(t, 2, "yoyodyne-ifd.99", map[string]any{
			"action":   "reprioritize",
			"id":       "yoyodyne-ifd.99",
			"priority": 0,
			"reason":   "nothing else is worth doing before it",
		}),
	}
	attributed, attribution := said(t, conversation, events, 0)
	if attributed.Event.Kind != KindItemAttributed {
		t.Fatalf("an attribution is %s", attributed.Event.Kind)
	}
	if !strings.Contains(attribution.Body, "Directives reach the agent") {
		t.Fatalf("body %q does not name the goal", attribution.Body)
	}
	reordered, order := said(t, conversation, events, 1)
	if reordered.Event.Kind != KindItemReprioritized {
		t.Fatalf("a reprioritization is %s", reordered.Event.Kind)
	}
	// Zero is the top of this queue rather than an unstated place in it, which is
	// the one value an absent priority would be mistaken for.
	if !strings.Contains(order.Body, "priority 0") {
		t.Fatalf("body %q does not say where in the queue it went", order.Body)
	}
}

func TestAPriorityTheRecordDoesNotStateIsSaidAsAbsent(t *testing.T) {
	conversation := conversationWith(domain.RoleProductManager)
	events := []execution.Event{applied(t, 1, "yoyodyne-ifd.99", map[string]any{
		"action": "reprioritize",
		"id":     "yoyodyne-ifd.99",
		"reason": "recorded before the priority was carried",
	})}
	_, message := said(t, conversation, events, 0)
	if strings.Contains(message.Body, "priority 0") {
		t.Fatalf("body %q reads an absent priority as the top of the queue", message.Body)
	}
	if !strings.Contains(message.Body, "does not state") {
		t.Fatalf("body %q does not state the absence", message.Body)
	}
}

func TestApprovedWorkIsSaidByTheHarnessAndCarriesTheGoalItWasProposedUnder(t *testing.T) {
	// The decision was the operator's, and the operator is not a persona: no
	// persona narrates a decision it did not make. The goal is on the proposal
	// rather than on the creation, which is why the earlier record is read.
	conversation := conversationWith(domain.RoleProductManager)
	proposed := map[string]any{
		"id":              "1.1",
		"conversation_id": conversation.ConversationID,
		"turn":            1,
		"proposal": map[string]any{
			"title":       "Thread conversation milestones into the channel",
			"description": "the proposal's own words",
			"rationale":   "the backlog changing is invisible",
			"goal":        "Work the harness runs on its own is visible while it runs",
		},
	}
	events := []execution.Event{
		recorded(t, 1, execution.EventProposalRecorded, proposed),
		recorded(t, 2, execution.EventProposalApproved, proposed),
		recorded(t, 3, execution.EventProposalCreated, map[string]any{
			"proposal_id":  "1.1",
			"turn":         1,
			"work_item_id": "yoyodyne-ifd.114",
			"title":        "Thread conversation milestones into the channel",
		}),
	}
	// The approval and the creation are one decision, so only the half that names
	// the item is said.
	if notification, err := FromConversation(conversation, events, 1); err != nil || !notification.Silent() {
		t.Fatalf("the approval said %+v (%v), want nothing beside the creation", notification.Event.Kind, err)
	}
	notification, message := said(t, conversation, events, 2)
	if notification.Event.Kind != KindWorkApproved {
		t.Fatalf("an approved proposal is %s", notification.Event.Kind)
	}
	if !notification.Speaker.IsHarness() {
		t.Fatalf("spoken by %q", notification.Speaker.Key())
	}
	if notification.Topic.Key() != "work-item:yoyodyne-ifd.114" {
		t.Fatalf("addressed to %q", notification.Topic.Key())
	}
	if !strings.Contains(message.Body, "Work the harness runs on its own is visible while it runs") {
		t.Fatalf("body %q does not carry the goal it was proposed under", message.Body)
	}
}

func TestDeclinedWorkIsAddressedToTheProductWithTheReasonKept(t *testing.T) {
	// Nothing was created, so there is no item to thread it to; burying it in
	// somebody else's thread would misfile it.
	conversation := conversationWith(domain.RoleProductManager)
	events := []execution.Event{recorded(t, 1, execution.EventProposalRejected, map[string]any{
		"id":              "1.1",
		"conversation_id": conversation.ConversationID,
		"turn":            1,
		"proposal": map[string]any{
			"title":       "Rewrite the CLI in Rust",
			"description": "port everything",
			"rationale":   "it would be faster",
			"goal":        "Support development in any language",
		},
		"reason": "not this quarter",
	})}
	notification, message := said(t, conversation, events, 0)
	if notification.Event.Kind != KindWorkDeclined {
		t.Fatalf("a declined proposal is %s", notification.Event.Kind)
	}
	if notification.Topic.Kind != TopicProduct {
		t.Fatalf("addressed to %q", notification.Topic.Key())
	}
	if !notification.Speaker.IsHarness() {
		t.Fatalf("spoken by %q", notification.Speaker.Key())
	}
	if !strings.Contains(message.Body, "not this quarter") || !strings.Contains(message.Body, "Rewrite the CLI in Rust") {
		t.Fatalf("body %q loses what was declined or why", message.Body)
	}
}

func TestNothingIsSaidAboutWhatDidNotMoveTheBacklog(t *testing.T) {
	// Most of a conversation's log is the turn itself, and a channel that
	// reported every record of it would be the event log this exists not to be.
	conversation := conversationWith(domain.RoleProductManager)
	events := []execution.Event{
		recorded(t, 1, execution.EventAgentMessage, map[string]any{"text": "what the product manager said"}),
		recorded(t, 2, execution.EventTrackerActionRequested, map[string]any{
			"action_id": "t1.1",
			"turn":      1,
			"action":    map[string]any{"action": "create", "title": "asked for, not yet done"},
		}),
		recorded(t, 3, execution.EventTrackerActionFailed, map[string]any{
			"action_id":    "t1.1",
			"turn":         1,
			"action":       map[string]any{"action": "create", "title": "refused by the tracker"},
			"work_item_id": "",
			"failure":      "the tracker refused it",
		}),
		applied(t, 4, "yoyodyne-ifd.113", map[string]any{
			"action": "read",
			"id":     "yoyodyne-ifd.113",
		}),
		applied(t, 5, "yoyodyne-ifd.113", map[string]any{
			"action": "update",
			"id":     "yoyodyne-ifd.113",
			"note":   "what was learned",
			"reason": "worth recording on the item",
		}),
	}
	for index := range events {
		notification, err := FromConversation(conversation, events, index)
		if err != nil {
			t.Fatalf("select from conversation event %d: %v", index, err)
		}
		if !notification.Silent() {
			t.Fatalf("event %d (%s) said %s", index, events[index].Type, notification.Event.Kind)
		}
	}
}

func TestAnEventOutsideTheLogIsRefusedRatherThanInvented(t *testing.T) {
	conversation := conversationWith(domain.RoleProductManager)
	if _, err := FromConversation(conversation, nil, 0); err == nil {
		t.Fatal("selecting from an empty log succeeded")
	}
}

func TestARecordThatCannotBeReadIsReportedRatherThanSaidWrongly(t *testing.T) {
	// A payload nothing can decode is a record nothing true can be said about, and
	// the sink reads past it once it has been told.
	conversation := conversationWith(domain.RoleProductManager)
	event := recorded(t, 1, execution.EventTrackerActionApplied, map[string]any{"action": "create"})
	event.Payload = json.RawMessage(`{"action":"not an action object"}`)
	if _, err := FromConversation(conversation, []execution.Event{event}, 0); err == nil {
		t.Fatal("an unreadable tracker payload was selected without complaint")
	}
}
