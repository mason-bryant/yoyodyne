package execution

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// EventSchemaVersion is what a event is written at now, and
// MinReadableEventSchemaVersion the oldest an event log may be read at. A run's
// log is append-only and never rewritten, so a repository that has been running
// holds logs at every version it has ever written; reading is a range for that
// reason, and writing is one number because there is only one shape to write.
const (
	EventSchemaVersion            = 2
	MinReadableEventSchemaVersion = 1
)

// TerminalRoleSchemaVersion is the first version whose terminals name the role
// that made the invocation. It is what lets a reader of a mixed history tell a
// terminal that failed to say whose it was from one recorded before there was
// anything to say: below this version the absence means the former did not exist
// yet, and at or above it the absence is the omission itself. A fixed number is
// what makes that decidable rather than a guess about when a log was written,
// and it is separate from EventSchemaVersion so that the next version to be
// added does not silently move it.
const TerminalRoleSchemaVersion = 2

type EventType string

const (
	EventRunStarted EventType = "run.started"
	// One provider invocation's terminal, whichever way it ended, carrying what
	// the provider said it cost. A log can hold several of them — the developer's
	// attempts and the reviewer's invocations share one — so from
	// TerminalRoleSchemaVersion each must name the role it was made as, under the
	// payload key "role". A backend that omits it leaves money nothing can
	// attribute: the phase split in internal/runstate places such a terminal
	// nowhere rather than guessing, so the cost stays in the run's total and out
	// of every phase. Where a terminal sits relative to the others is not a
	// substitute — that is a fact about the order the harness happened to do
	// things in, and anything reading it as a phase is guessing.
	EventRunCompleted     EventType = "run.completed"
	EventRunFailed        EventType = "run.failed"
	EventProcessOutput    EventType = "process.output"
	EventAgentMessage     EventType = "agent.message"
	EventCommandStarted   EventType = "command.started"
	EventCommandCompleted EventType = "command.completed"
	EventFileChanged      EventType = "file.changed"
	EventReviewStarted    EventType = "review.started"
	EventReviewCompleted  EventType = "review.completed"
	// A reviewer that answered with a field the verdict schema does not define
	// has drifted from the contract it was given. The verdict is decoded without
	// the extra fields rather than refused, and what the reviewer invented is
	// recorded here instead: it costs the run nothing, and it is what a prompt
	// regression is diagnosed from afterwards.
	EventReviewDrift EventType = "review.drift"
	// A proposal and the operator's decision on it are separate events, because
	// what was proposed is evidence whether or not it was ever created.
	EventProposalRecorded EventType = "proposal.recorded"
	EventProposalApproved EventType = "proposal.approved"
	EventProposalRejected EventType = "proposal.rejected"
	// A proposal the harness admitted itself, on the strength of the operator's
	// approval of the goal it serves. It is its own event rather than an approval
	// with a different reason: nobody approved this item, and a record that read
	// as though somebody had would be the one claim this arrangement cannot make.
	EventProposalAdmitted EventType = "proposal.admitted"
	EventProposalCreated  EventType = "proposal.created"
	// A concern is work the product manager judged against the goals and put to
	// the operator as a question instead of proposing. What it raised and what
	// it was told are separate events for the same reason a proposal and its
	// decision are: the concern is evidence whether or not anybody answered it.
	EventConcernRaised   EventType = "concern.raised"
	EventConcernAnswered EventType = "concern.answered"
	// A tracker action the product manager takes is recorded as what was asked
	// for and what came of it, separately, so an action that failed is never
	// readable as one that was carried out.
	EventTrackerActionRequested EventType = "tracker.action.requested"
	EventTrackerActionApplied   EventType = "tracker.action.applied"
	EventTrackerActionFailed    EventType = "tracker.action.failed"
	// A whole block of tracker actions the harness would not read, which is
	// recorded even though no action in it was requested: the three events above
	// are written per action and a refused block never reaches them, so without
	// this the only trace of a dozen lost admissions was a line on whichever
	// terminal happened to be watching. It carries the role that asked, how many
	// actions the block asked for where the harness could count them, and the
	// refusal in the words the role is given back.
	EventTrackerBlockRefused EventType = "tracker.block.refused"
	// What an agent reports while its work continues is recorded in that
	// invocation's own log as well as in the collected pile: the run or
	// conversation says a report was made, and the pile says what it was. A
	// block the harness could not read is recorded too, because the work it
	// accompanied is unaffected by it and the report would otherwise leave no
	// trace at all.
	EventReportRecorded   EventType = "report.recorded"
	EventReportUnreadable EventType = "report.unreadable"
	// Work a conversation steers is recorded in that conversation's own log, so
	// what the operator asked the harness to do is evidence beside what was said
	// to arrive at it. The run these describe keeps its own separate log.
	EventWorkStarted  EventType = "work.started"
	EventWorkFinished EventType = "work.finished"
	EventWorkStopped  EventType = "work.stopped"
	EventWorkDirected EventType = "work.directed"
	// The operator's hold on the work the harness chooses for itself passes
	// through a conversation the same way. It lives in the product's own record
	// and is enforced from there, so these say only that the operator placed or
	// lifted it here — which is the thing that would otherwise be missing from an
	// account of a queue that suddenly stopped moving.
	EventIntakeHeld     EventType = "intake.held"
	EventIntakeReleased EventType = "intake.released"
	// A conversation's picture of the repository and the tracker is taken once
	// and can be taken again on the operator's instruction. The refresh is
	// recorded because it changes what the agent is reasoning from, which is
	// otherwise the one thing about a conversation its log would not say.
	EventContextRefreshed EventType = "context.refreshed"
	// A directive the operator gave is recorded for the whole product rather than
	// for this conversation, and enforced from there. These say that it passed
	// through here: what was directed, and what settled it afterwards. Neither is
	// where the directive lives, which is exactly why the conversation's own log
	// has to say that the operator gave one.
	EventDirectiveRecorded EventType = "directive.recorded"
	EventDirectiveResolved EventType = "directive.resolved"
	// A directive the operator took back. It is separate from the two above
	// because it is the opposite fact from a settlement: what was directed was not
	// carried out or answered, it stopped being meant, and a log that recorded the
	// two the same way could not say which of them ended a standing instruction.
	EventDirectiveWithdrawn EventType = "directive.withdrawn"
	// An ask this conversation put to another role is recorded in this
	// conversation's own log as well as in the exchange itself, for the reason a
	// report is recorded in both places: the exchange holds the thread, and the
	// conversation has to say that its own reasoning went and asked somebody. The
	// round and its closing are separate events because a round that produced no
	// answer still happened and still spent the exchange's budget.
	EventExchangeRound  EventType = "exchange.round"
	EventExchangeClosed EventType = "exchange.closed"
)

type Event struct {
	SchemaVersion int             `json:"schema_version"`
	RunID         string          `json:"run_id"`
	Sequence      uint64          `json:"sequence"`
	Timestamp     time.Time       `json:"timestamp"`
	Type          EventType       `json:"type"`
	Source        string          `json:"source"`
	Payload       json.RawMessage `json:"payload,omitempty"`
}

func NewEvent(runID string, sequence uint64, timestamp time.Time, eventType EventType, source string, payload any) (Event, error) {
	event := Event{
		SchemaVersion: EventSchemaVersion,
		RunID:         runID,
		Sequence:      sequence,
		Timestamp:     timestamp.UTC(),
		Type:          eventType,
		Source:        source,
	}
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return Event{}, fmt.Errorf("encode event payload: %w", err)
		}
		event.Payload = encoded
	}
	if err := event.Validate(); err != nil {
		return Event{}, err
	}
	return event, nil
}

func DecodeEvent(data []byte) (Event, error) {
	var event Event
	if err := json.Unmarshal(data, &event); err != nil {
		return Event{}, fmt.Errorf("decode event: %w", err)
	}
	if err := event.Validate(); err != nil {
		return Event{}, err
	}
	return event, nil
}

func (e Event) Validate() error {
	var problems []error
	// Reading accepts every version this harness has ever written, because a run
	// log is appended to and never rewritten: refusing an older one would not
	// upgrade it, it would lose it, and what is in those logs is the only record
	// of what the harness has already done.
	if e.SchemaVersion < MinReadableEventSchemaVersion || e.SchemaVersion > EventSchemaVersion {
		problems = append(problems, fmt.Errorf("schema_version must be between %d and %d", MinReadableEventSchemaVersion, EventSchemaVersion))
	}
	if e.RunID == "" {
		problems = append(problems, errors.New("run_id is required"))
	}
	if e.Sequence == 0 {
		problems = append(problems, errors.New("sequence must be greater than zero"))
	}
	if e.Timestamp.IsZero() {
		problems = append(problems, errors.New("timestamp is required"))
	}
	if e.Type == "" {
		problems = append(problems, errors.New("type is required"))
	}
	if e.Source == "" {
		problems = append(problems, errors.New("source is required"))
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid event: %w", errors.Join(problems...))
	}
	return nil
}

type Sequence struct {
	next uint64
}

func NewSequence(last uint64) *Sequence {
	return &Sequence{next: last + 1}
}

func (s *Sequence) Next() uint64 {
	value := s.next
	s.next++
	return value
}

func (s *Sequence) Last() uint64 {
	return s.next - 1
}
