package notify

// What a conversation did to the backlog, read from the log it wrote while
// doing it.
//
// Runs are the first producer of reportable events; conversations are the
// second, and they arrive the way the design said a producer would — a selection
// function here, and nothing whatever in the sink, the threading, or the
// envelope. The shape is the same as a run's: a durable record written by a
// process that is long gone, read afterwards by something that decides nothing
// about it.
//
// What is worth saying about a conversation is not what was said in it. Most of
// a conversation's log is the turn itself — the provider's messages, the tools
// it asked for, the reply as it was written — and none of that is a milestone.
// What is a milestone is the queue moving: work admitted, work decomposed, a
// goal recorded on an item, an item's place in the order changed, and what the
// operator decided about work an agent proposed. Those are the harness's
// steering wheel turning, and an operator who cannot see them is watching a
// factory whose direction changes silently.

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The tracker operations this reports on. They are named here rather than
// imported so that what is reported stays independent of how a conversation
// carries an action out — the same reason a run's milestones are read by a rule
// stated in this package rather than by the pipeline's own.
//
// The rest of the vocabulary is deliberately absent. Reading an item and
// surveying the queue change nothing; a note, a link, and a reparenting are the
// structure around work rather than the work arriving or moving; and closing an
// item is already said by the run that finished it. A channel that reported
// every tracker call would be the event log this exists not to be.
//
// The exception is work no run carries, and it is the reason two of these are
// named at all. An item marked for a conversation is never selected, so there is
// no run to say any of it: the note the architect writes on it is the only sign
// the work started, and the close is the only sign it finished. For that item the
// rule above inverts — the actions that are silent everywhere else are the whole
// of its narrative, and without them its thread ends on a failed run and says
// nothing for the rest of the item's life.
const (
	trackerRead         = "read"
	trackerSurvey       = "survey"
	trackerCreate       = "create"
	trackerAttribute    = "attribute"
	trackerReprioritize = "reprioritize"
	trackerClose        = "close"
)

// FromConversation says what the event at one position of a conversation's log
// did to the backlog, and says nothing at all — the zero notification — for the
// events that did nothing to it, which is most of them.
//
// It is given the whole log rather than the one event because one milestone
// needs an earlier record to be said in full: the creation of an approved
// proposal names the item but not what the item is for, and the goal is on the
// proposal the operator approved. Reading backwards for it is exact rather than
// a guess, since a creation is only ever recorded after the proposal it came
// from.
func FromConversation(conversation runstate.Conversation, events []execution.Event, index int) (Notification, error) {
	if index < 0 || index >= len(events) {
		return Notification{}, fmt.Errorf("conversation %s has no event at position %d", conversation.ConversationID, index)
	}
	event := events[index]
	switch event.Type {
	case execution.EventTrackerActionApplied:
		// The earlier records are read for the same reason a creation reads them:
		// one milestone is about where in a sequence the action falls rather than
		// about the action itself. A role's first act on work handed to it is that
		// role picking it up, and its second is that role working — and only the log
		// says which this was.
		return fromTrackerAction(conversation, events[:index], event)
	case execution.EventProposalCreated:
		// The approval that precedes this is deliberately not reported beside it.
		// They are one decision, and the creation is the half that names the item
		// the thread belongs to; saying both would say one decision twice, in two
		// threads. What is lost is an approval whose creation then failed, which
		// the operator is told about in the conversation they made it in.
		return fromApprovedWork(conversation, events[:index], event)
	case execution.EventProposalRejected:
		return fromDeclinedWork(conversation, event)
	default:
		return Notification{}, nil
	}
}

// trackerAction is the part of a recorded tracker outcome this reads. It is a
// narrow local shape rather than the conversation's own, because what is read
// here is a durable record that outlives the code that wrote it: a field the
// record does not carry is absent rather than an error, which is what lets an
// event recorded by an older harness still be said.
type trackerAction struct {
	Action struct {
		Action   string  `json:"action"`
		ID       string  `json:"id"`
		Title    string  `json:"title"`
		Goal     string  `json:"goal"`
		Parent   *string `json:"parent"`
		Priority *int    `json:"priority"`
		Executor string  `json:"executor"`
		Reason   string  `json:"reason"`
	} `json:"action"`
	WorkItemID string `json:"work_item_id"`
	// WorkItemTitle is what the tracker called the item as the action ran. It is
	// what names the topic for every action that carries no title of its own —
	// a reordering, an attribution — and a record written before it was carried
	// leaves the topic addressed exactly as it was before there was one.
	WorkItemTitle string `json:"work_item_title"`
	// WorkItemExecutor is what the tracker said carried the item as the action
	// ran, before the action changed anything. A record written before it was
	// carried says nothing, which reads as ordinary work — the same thing an
	// absent marker has always meant.
	WorkItemExecutor string `json:"work_item_executor"`
}

// handsOff is the executor this action gives the item, and is empty for the
// actions that say nothing about what carries the work, which is nearly all of
// them.
func (a trackerAction) handsOff() string { return strings.TrimSpace(a.Action.Executor) }

// carriedBy is what the tracker already said carried the item when the action
// ran, and is empty for ordinary work a developer run carries.
func (a trackerAction) carriedBy() string { return strings.TrimSpace(a.WorkItemExecutor) }

// changesTheItem reports an action that did something to the work rather than
// asked about it. It is what makes a role's first such action the moment it
// picked the work up: reading an item and surveying the queue are what a role
// does before deciding anything, and treating either as starting the work would
// announce a pickup for a conversation that went on to do nothing.
func (a trackerAction) changesTheItem() bool {
	switch a.Action.Action {
	case "", trackerRead, trackerSurvey:
		return false
	default:
		return true
	}
}

// fromTrackerAction says what one applied action did to the queue. Only actions
// the record says were applied reach here, so nothing said is an intention: an
// action that failed is recorded as a failure and is never readable as a change.
func fromTrackerAction(conversation runstate.Conversation, earlier []execution.Event, event execution.Event) (Notification, error) {
	var recorded trackerAction
	if err := json.Unmarshal(event.Payload, &recorded); err != nil {
		return Notification{}, fmt.Errorf("read a tracker action recorded in %s: %w", conversation.ConversationID, err)
	}
	// A priority the record did not carry is stated as absent rather than read as
	// the top of the queue, because zero is the highest priority there is.
	detail := Detail{
		Title:    strings.TrimSpace(recorded.Action.Title),
		Goal:     strings.TrimSpace(recorded.Action.Goal),
		Reason:   strings.TrimSpace(recorded.Action.Reason),
		Priority: -1,
	}
	var kind Kind
	switch {
	// Marking work already in the backlog with an executor is the handoff itself:
	// the item stops being a run's to carry, and from here nothing happens to it
	// until a role opens a conversation about it. A creation that names one is not
	// this — it is an admission that happens to be conversation work, and the
	// admission is where a thread learns what the item is for, so it stays an
	// admission with what carries it named beside it.
	case recorded.Action.Action != trackerCreate && recorded.handsOff() != "":
		kind = KindWorkHandedOff
		detail.Executor = recorded.handsOff()
	// Closing work a conversation carries is the only account there is of that
	// work finishing. It comes before the pickup below because a role that did the
	// work and then closed the item in one turn has done both, and what a reader
	// needs from a single message is the ending.
	case recorded.carriedBy() != "" && recorded.Action.Action == trackerClose:
		kind = KindWorkCarriedOut
		detail.Executor = recorded.carriedBy()
	// The first thing a conversation does to work that was handed to a
	// conversation is that role starting it. It is read from this conversation's
	// own log, so a second role taking the same item up later is a second pickup
	// rather than silence — which is the honest reading, since it is a second role
	// starting on it.
	case recorded.carriedBy() != "" && recorded.changesTheItem() && !actedOn(earlier, recorded.WorkItemID):
		kind = KindWorkPickedUp
		detail.Executor = recorded.carriedBy()
	case recorded.Action.Action == trackerCreate:
		// Admitting work and decomposing it are two acts, and which one this was
		// follows from who did it: the product manager owns what is admitted to the
		// backlog, and every other role that may create at all creates underneath
		// something already admitted. Recording a decomposition as an admission
		// would say a role admitted work it has no authority to admit, which is the
		// one thing an account of the queue must never say.
		kind = KindItemDecomposed
		if conversation.Role == domain.RoleProductManager {
			kind = KindItemAdmitted
		}
		if recorded.Action.Parent != nil {
			detail.Parent = strings.TrimSpace(*recorded.Action.Parent)
		}
		detail.Executor = recorded.handsOff()
	case recorded.Action.Action == trackerAttribute:
		kind = KindItemAttributed
		detail.Executor = recorded.carriedBy()
	case recorded.Action.Action == trackerReprioritize:
		kind = KindItemReprioritized
		if recorded.Action.Priority != nil {
			detail.Priority = *recorded.Action.Priority
		}
		detail.Executor = recorded.carriedBy()
	default:
		return Notification{}, nil
	}
	// An action that names no title of its own is named by the item the record
	// says it acted on. It is the same fallback for the thread's header and for
	// what the message says, because they are the same question: an item's first
	// appearance in the channel is as often a reordering as an admission, and a
	// role picking up work names no title at all — a thread headed by a bare
	// identifier, or a sentence saying the item has no name, would be the record
	// being read less carefully than it was written.
	detail.Title = namedItem(detail.Title, recorded.WorkItemTitle)
	topic, err := topicForItem(recorded.WorkItemID)
	if err != nil {
		return Notification{}, fmt.Errorf("address the %s recorded in %s: %w", kind, conversation.ConversationID, err)
	}
	return Notification{
		// An admission is usually the first thing said about an item, so this is
		// where most threads get the name their header carries.
		Topic: topic.WithTitle(detail.Title),
		// The role's own act, in its own voice: what is admitted, decomposed,
		// attributed, or reordered is a judgment the role made, and the harness
		// only carried it out on the role's behalf.
		Speaker: Persona(conversation.Role, conversation.Agent),
		Event: Event{
			Kind:     kind,
			At:       event.Timestamp,
			Severity: report.SeverityNote,
			Refs: Refs{
				ConversationID: conversation.ConversationID,
				WorkItemID:     strings.TrimSpace(recorded.WorkItemID),
			},
			Detail: detail,
		},
	}, nil
}

// namedItem is what to call the item an action was about: what the action itself
// named, and otherwise what the tracker called the item when the action ran. The
// action comes first because it is the more specific of the two — a creation
// names the item it is admitting, and the reading beside it is of the item as it
// already was.
func namedItem(named, recorded string) string {
	if strings.TrimSpace(named) != "" {
		return named
	}
	return strings.TrimSpace(recorded)
}

// actedOn reports this conversation having already changed one item earlier in
// its own log. It is what makes a pickup the first act rather than every act: a
// role carrying work in conversation writes on the item repeatedly, and a thread
// that said it had been picked up each time would say the one thing that matters
// so often it stopped meaning anything.
//
// A record it cannot read is not an earlier act. Skipping it would be the safe
// direction for a message nobody wants twice, and this is the opposite case: the
// pickup is the message the thread is missing, and losing it to one unreadable
// record is worse than saying it once more than necessary.
func actedOn(earlier []execution.Event, workItemID string) bool {
	wanted := strings.TrimSpace(workItemID)
	if wanted == "" {
		return false
	}
	for _, event := range earlier {
		if event.Type != execution.EventTrackerActionApplied {
			continue
		}
		var recorded trackerAction
		if err := json.Unmarshal(event.Payload, &recorded); err != nil {
			continue
		}
		if strings.TrimSpace(recorded.WorkItemID) == wanted && recorded.changesTheItem() {
			return true
		}
	}
	return false
}

// approvedWork is the part of a created item's record this reads.
type approvedWork struct {
	ProposalID string `json:"proposal_id"`
	WorkItemID string `json:"work_item_id"`
	Title      string `json:"title"`
	Parent     string `json:"parent"`
}

// proposedWork is the part of a proposal's record this reads. It is the shape
// both the proposal as recorded and the operator's refusal of it are written in,
// because a refusal keeps the whole proposal beside the reason.
type proposedWork struct {
	ID       string `json:"id"`
	Proposal struct {
		Title string `json:"title"`
		Goal  string `json:"goal"`
	} `json:"proposal"`
	Reason string `json:"reason"`
}

// fromApprovedWork says that work an agent proposed was approved and is now in
// the backlog. The harness speaks: the judgment was the operator's, and the
// operator is not a persona, so no persona narrates a decision it did not make.
func fromApprovedWork(conversation runstate.Conversation, earlier []execution.Event, event execution.Event) (Notification, error) {
	var created approvedWork
	if err := json.Unmarshal(event.Payload, &created); err != nil {
		return Notification{}, fmt.Errorf("read the work item created in %s: %w", conversation.ConversationID, err)
	}
	topic, err := topicForItem(created.WorkItemID)
	if err != nil {
		return Notification{}, fmt.Errorf("address the work item created in %s: %w", conversation.ConversationID, err)
	}
	return Notification{
		Topic:   topic.WithTitle(created.Title),
		Speaker: Harness(),
		Event: Event{
			Kind:     KindWorkApproved,
			At:       event.Timestamp,
			Severity: report.SeverityNote,
			Refs: Refs{
				ConversationID: conversation.ConversationID,
				WorkItemID:     strings.TrimSpace(created.WorkItemID),
			},
			Detail: Detail{
				Title:    strings.TrimSpace(created.Title),
				Goal:     goalProposed(earlier, created.ProposalID),
				Parent:   strings.TrimSpace(created.Parent),
				Priority: -1,
			},
		},
	}, nil
}

// fromDeclinedWork says that work an agent proposed was turned down. It is
// addressed to the product rather than to any item, because there is no item:
// nothing was created, which is the whole of what a decline means.
func fromDeclinedWork(conversation runstate.Conversation, event execution.Event) (Notification, error) {
	var declined proposedWork
	if err := json.Unmarshal(event.Payload, &declined); err != nil {
		return Notification{}, fmt.Errorf("read the proposal declined in %s: %w", conversation.ConversationID, err)
	}
	return Notification{
		Topic:   Product(),
		Speaker: Harness(),
		Event: Event{
			Kind:     KindWorkDeclined,
			At:       event.Timestamp,
			Severity: report.SeverityNote,
			Refs:     Refs{ConversationID: conversation.ConversationID},
			Detail: Detail{
				Title:    strings.TrimSpace(declined.Proposal.Title),
				Goal:     strings.TrimSpace(declined.Proposal.Goal),
				Reason:   strings.TrimSpace(declined.Reason),
				Priority: -1,
			},
		},
	}, nil
}

// goalProposed is what the proposal an item was created from said the work was
// for. It is read from the record of the proposal itself, which is where the
// goal is: the creation names the item and the proposal names the intent, and an
// admission that could not say what the work serves would be exactly the item
// nobody can later decide to stop doing.
//
// A proposal the log does not hold leaves the goal absent, which the voice
// states as an absence rather than rendering as a blank.
func goalProposed(earlier []execution.Event, proposalID string) string {
	wanted := strings.TrimSpace(proposalID)
	if wanted == "" {
		return ""
	}
	for index := len(earlier) - 1; index >= 0; index-- {
		event := earlier[index]
		switch event.Type {
		case execution.EventProposalApproved, execution.EventProposalRecorded:
		default:
			continue
		}
		var proposed proposedWork
		if err := json.Unmarshal(event.Payload, &proposed); err != nil {
			continue
		}
		if strings.TrimSpace(proposed.ID) == wanted {
			return strings.TrimSpace(proposed.Proposal.Goal)
		}
	}
	return ""
}
