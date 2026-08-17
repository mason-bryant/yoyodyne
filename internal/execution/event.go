package execution

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const EventSchemaVersion = 1

type EventType string

const (
	EventRunStarted       EventType = "run.started"
	EventRunCompleted     EventType = "run.completed"
	EventRunFailed        EventType = "run.failed"
	EventProcessOutput    EventType = "process.output"
	EventAgentMessage     EventType = "agent.message"
	EventCommandStarted   EventType = "command.started"
	EventCommandCompleted EventType = "command.completed"
	EventFileChanged      EventType = "file.changed"
	EventReviewStarted    EventType = "review.started"
	EventReviewCompleted  EventType = "review.completed"
	// A proposal and the operator's decision on it are separate events, because
	// what was proposed is evidence whether or not it was ever created.
	EventProposalRecorded EventType = "proposal.recorded"
	EventProposalApproved EventType = "proposal.approved"
	EventProposalRejected EventType = "proposal.rejected"
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
	if e.SchemaVersion != EventSchemaVersion {
		problems = append(problems, fmt.Errorf("schema_version must be %d", EventSchemaVersion))
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
