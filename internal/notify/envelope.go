// Package notify is the vocabulary the harness reports itself in: one envelope
// per reportable event, addressed to a topic, attributed to a speaker, carrying
// either text the harness rendered or text an agent wrote.
//
// It knows nothing about Slack, and nothing about where its envelopes come
// from. That is deliberate and it is the whole point of the split: runs are the
// first producer, conversations and branch reviews are the same shape, and ask
// exchanges arrive later with topics of their own, so a new producer adds kinds
// without the sink, the threading, or the inbound half changing. The version on
// the envelope is what makes that extension legible to a reader afterwards.
//
// The severity vocabulary is the reports one — critical, warning, note — rather
// than a second scale that means nearly the same thing. A report already reaches
// the operator at a severity they know how to read, and a channel that graded
// the same fact differently depending on which producer sent it would teach
// nobody anything.
package notify

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// SchemaVersion is the version of this vocabulary carried on every envelope. It
// is versioned separately from run and conversation state because it describes
// neither: an envelope is a reading of a durable record rather than a record.
const SchemaVersion = 1

// Kind names what happened. The set is the milestones an operator has no
// terminal surface for at night, and it is open by construction: a producer that
// has something new to say adds a kind, and a sink that does not know it still
// posts the body under the speaker's name.
type Kind string

const (
	// KindRunStarted is a run claimed and started, carrying the reason that item
	// was selected. The selection reason is on the message deliberately: the
	// invariant that makes it durable exists so an operator can see why the
	// harness chose what it chose, and a channel that reported the choice without
	// the reason would be the half of that guarantee nobody can act on.
	KindRunStarted Kind = "run.started"
	// KindChecksPassed and KindChecksFailed are the deterministic gate, said once
	// each way.
	KindChecksPassed Kind = "checks.passed"
	KindChecksFailed Kind = "checks.failed"
	// KindReviewVerdict is what the reviewer decided, approve or repair.
	KindReviewVerdict Kind = "review.verdict"
	// KindPromotion is the work reaching its target branch.
	KindPromotion Kind = "promotion"
	// KindPublication, KindMergeQueued, and KindMerged are the three things that
	// can be said about a pull request, and they are separate because a queued
	// merge that never lands and a merge that did are different news.
	KindPublication Kind = "publication"
	KindMergeQueued Kind = "merge.queued"
	KindMerged      Kind = "merge"
	// KindRunParked and KindRunContinued bracket a wait: an exhausted usage
	// limit, an overloaded provider, an operator hold, an unresolved directive.
	// Both are said, because a park nobody saw lift reads as a run that died.
	KindRunParked    Kind = "run.parked"
	KindRunContinued Kind = "run.continued"
	// KindBlocker is work that stopped and needs a person.
	KindBlocker Kind = "blocker"
	// KindRunFinished is a run that ended without a promotion to report — the
	// ordinary shape for a project whose integration is the operator's to
	// approve. It is here because the alternative is a thread that opens with a
	// run starting and never says anything became of it.
	KindRunFinished Kind = "run.finished"
	// KindReport is what an agent noticed while its own work carried on, posted
	// as the agent wrote it and at the severity the agent gave it.
	KindReport Kind = "report"
	// KindProposal is a change proposed to an artifact whose owner has to decide
	// it.
	KindProposal Kind = "proposal"
	// KindExchangeTurn and KindExchangeClosed are the ask exchanges, which arrive
	// with a producer of their own. They are named here rather than later because
	// the vocabulary is one contract: a kind added when its producer lands is a
	// kind the sink was never written against.
	KindExchangeTurn   Kind = "exchange.turn"
	KindExchangeClosed Kind = "exchange.closed"
)

// Topic is the thread key: what a message is about, rather than what produced
// it. One thread per topic is the whole of the addressing rule, so two producers
// with something to say about one work item say it in one place.
type Topic string

// ProductTopic is for what belongs to the whole line rather than to any item —
// a pause placed and lifted, intake held and released. It is deliberately not a
// thread: burying an event about everything inside one item's thread misfiles
// it.
const ProductTopic Topic = "product"

// WorkItemTopic and ExchangeTopic are the two threaded topics. An exchange that
// concerns a work item takes that item's topic instead of its own, because the
// item is what the conversation is about.
func WorkItemTopic(id string) Topic {
	return Topic("work-item:" + strings.TrimSpace(id))
}

func ExchangeTopic(id string) Topic {
	return Topic("exchange:" + strings.TrimSpace(id))
}

// Threaded reports whether this topic gets a thread of its own. Only the
// product topic does not.
func (t Topic) Threaded() bool {
	return t != ProductTopic
}

// Label is the topic as a person reads it, which is what opens its thread.
func (t Topic) Label() string {
	trimmed := strings.TrimSpace(string(t))
	switch {
	case trimmed == string(ProductTopic):
		return "the product"
	case strings.HasPrefix(trimmed, "work-item:"):
		return strings.TrimPrefix(trimmed, "work-item:")
	case strings.HasPrefix(trimmed, "exchange:"):
		return "exchange " + strings.TrimPrefix(trimmed, "exchange:")
	default:
		return trimmed
	}
}

// Speaker is whose account a message is. A role and the configured agent that
// filled it are both recorded for the reason a report records both: a project
// may configure more than one agent for a role, and "which developer said this"
// is a different question from "a developer said this".
//
// The zero Speaker is the harness itself, which is what says the things no
// persona did: a promotion, a merge, a run parking on a provider.
type Speaker struct {
	Role  domain.AgentRole `json:"role,omitempty"`
	Agent string           `json:"agent,omitempty"`
}

// Harness is the speaker for events no role authored.
var Harness = Speaker{}

// IsHarness reports whether this is the harness speaking rather than a persona.
func (s Speaker) IsHarness() bool {
	return strings.TrimSpace(string(s.Role)) == "" && strings.TrimSpace(s.Agent) == ""
}

// Name is what the speaker is called on a message. It names the agent where one
// is configured and the role otherwise, so a project with two developers is
// readable and one with a single developer is not made to read its own
// configuration to follow a thread.
func (s Speaker) Name() string {
	if s.IsHarness() {
		return "harness"
	}
	if agent := strings.TrimSpace(s.Agent); agent != "" {
		return agent
	}
	return string(s.Role)
}

// Refs are the correlation ids a message carries back to the durable record it
// was read from. They are what makes a message in a chat workspace lead to the
// evidence rather than replace it.
type Refs struct {
	Run          string `json:"run,omitempty"`
	Conversation string `json:"conversation,omitempty"`
	Exchange     string `json:"exchange,omitempty"`
	WorkItem     string `json:"work_item,omitempty"`
	Directive    string `json:"directive,omitempty"`
	PullRequest  string `json:"pull_request,omitempty"`
}

// MaxBodyBytes bounds one message body. It is generous for the paragraph a
// report or a verdict summary actually is; a body over it is truncated by the
// sink with a marker naming the record that holds the whole, rather than split
// into a flood of messages to fit.
const MaxBodyBytes = 16 << 10

// Envelope is one reportable event, ready to be posted anywhere.
type Envelope struct {
	Version  int             `json:"version"`
	Kind     Kind            `json:"kind"`
	Topic    Topic           `json:"topic"`
	Speaker  Speaker         `json:"speaker"`
	Severity report.Severity `json:"severity"`
	Body     string          `json:"body"`
	Refs     Refs            `json:"refs,omitempty"`
}

// New builds an envelope at the current schema version. It is the constructor
// producers use so no producer has to remember to stamp the version, which is
// the one field a reader afterwards cannot reconstruct.
func New(kind Kind, topic Topic, speaker Speaker, severity report.Severity, body string, refs Refs) Envelope {
	return Envelope{
		Version:  SchemaVersion,
		Kind:     kind,
		Topic:    topic,
		Speaker:  speaker,
		Severity: severity,
		Body:     body,
		Refs:     refs,
	}
}

// Validate reports every contract violation in the envelope at once. It is
// checked at the sink rather than trusted, because an envelope that cannot be
// posted must not be discovered halfway through posting it.
func (e Envelope) Validate() error {
	var problems []error
	if e.Version != SchemaVersion {
		problems = append(problems, fmt.Errorf("version must be %d", SchemaVersion))
	}
	if strings.TrimSpace(string(e.Kind)) == "" {
		problems = append(problems, errors.New("kind is required"))
	}
	if strings.TrimSpace(string(e.Topic)) == "" {
		problems = append(problems, errors.New("topic is required"))
	}
	if !e.Severity.Valid() {
		problems = append(problems, fmt.Errorf("severity %q must be %q, %q, or %q",
			e.Severity, report.SeverityCritical, report.SeverityWarning, report.SeverityNote))
	}
	switch body := strings.TrimSpace(e.Body); {
	case body == "":
		problems = append(problems, errors.New("body is required"))
	case len(body) > MaxBodyBytes:
		problems = append(problems, fmt.Errorf("body is %d bytes, limit is %d", len(body), MaxBodyBytes))
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid notification: %w", err)
	}
	return nil
}
