package chat

// The product manager checks work against the goals before the operator is
// asked about it, not after. Work that serves a goal is proposed with that goal
// named. Everything else is a concern: work it cannot place under any goal,
// work that would cut against one, and work that fits the goals and that it
// judges to be against what the product is for. None of those is proposed with
// a caveat attached, because a caveat inside a paragraph is a concern raised so
// softly it reads as assent. A concern is a question, the harness puts it to the
// operator, and it waits for the answer.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// concernFence opens the one block a reply may raise concerns in. It is a
// distinct language tag for the same reason the proposal and tracker fences
// are: a concern can never be confused with JSON the conversation happens to be
// discussing.
const concernFence = "```yoyodyne-concern"

// MaxConcernsPerTurn bounds how many concerns one reply may raise. Every one of
// them stops and waits for the operator, so a reply that raises a whole list is
// refused rather than turned into an interrogation nobody finishes.
// maxConcernsPerTurnText is the same bound as the contract states it; a test
// keeps the number the product manager is told equal to the one enforced here.
const (
	MaxConcernsPerTurn     = 5
	maxConcernsPerTurnText = "5"
)

// MaxConcernBlockBytes bounds the untrusted concern payload one turn may carry.
const MaxConcernBlockBytes = 16 << 10

const (
	maxConcernSubjectBytes = 200
	maxConcernTextBytes    = 4 << 10
)

// ConcernKind is which of the cases a concern is. They are kept apart rather
// than flattened into one warning because the operator answers each of them
// differently: incomplete goals, a conflict to settle, and an opinion to
// overrule or accept are three different things to be told.
type ConcernKind string

const (
	// ConcernUnplaceable is work that traces to no goal the product manager can
	// find. It is usually a sign that the goals are incomplete rather than that
	// the operator asked for the wrong thing, which is why it is a question
	// rather than a refusal.
	ConcernUnplaceable ConcernKind = "unplaceable"
	// ConcernConflict is work that would cut against a goal. The conflict is the
	// operator's to settle, so it is put to them instead of being proposed.
	ConcernConflict ConcernKind = "conflict"
	// ConcernJudgement is work that is consistent with the goals as written and
	// that the product manager judges to be against what the product is for.
	// This is the one it can be wrong about, and it says it anyway: an operator
	// can overrule an opinion that was stated and cannot overrule one that was
	// never voiced.
	ConcernJudgement ConcernKind = "judgement"
)

// concernKinds lists the kinds in the order the contract states them, so a
// refusal names exactly what was available.
var concernKinds = []ConcernKind{ConcernUnplaceable, ConcernConflict, ConcernJudgement}

func (k ConcernKind) Valid() bool {
	for _, kind := range concernKinds {
		if k == kind {
			return true
		}
	}
	return false
}

// Headline is what the operator is told this concern is, in the harness's own
// words. Each kind says something different, because a listing that called all
// three "a concern" would hide exactly the distinction the operator answers on.
func (k ConcernKind) Headline() string {
	switch k {
	case ConcernUnplaceable:
		return "work it cannot place: it serves no goal it can find"
	case ConcernConflict:
		return "work it says would cut against a goal"
	case ConcernJudgement:
		return "work that fits the goals, and that it judges to be against the product's intent"
	default:
		return "a concern the harness does not recognize"
	}
}

// Concern is one thing the product manager will not propose until the operator
// answers. Like a proposal it carries no authority and changes nothing; unlike
// a proposal it is not an offer to create anything, so there is nothing here to
// approve.
type Concern struct {
	Kind ConcernKind `json:"kind"`
	// Subject is the work this is about, in one line. It is required because a
	// question about unnamed work is one the operator cannot answer.
	Subject string `json:"subject"`
	// Goal is the goal at issue: the one the work would cut against, or the one
	// it fits on paper while cutting against the product's intent. Work that
	// traces to no goal is exactly the case with no goal to name, so it is
	// required on the other two kinds and refused on that one.
	Goal string `json:"goal,omitempty"`
	// Detail is what the product manager sees, and Question is what it needs
	// decided. Both are required: a concern with no question is an observation,
	// and an observation does not stop anything.
	Detail   string `json:"detail"`
	Question string `json:"question"`
}

// PendingConcern is a raised concern awaiting the operator's answer, together
// with the conversation turn it came from.
type PendingConcern struct {
	// ID identifies the concern within its conversation as c<turn>.<position>,
	// so an operator can name one and the record says which turn raised it.
	ID             string  `json:"id"`
	ConversationID string  `json:"conversation_id"`
	Turn           int     `json:"turn"`
	Concern        Concern `json:"concern"`
}

// concernDocument is the payload shape of the fenced block. The block always
// carries a list, so raising one concern and raising two are the same protocol.
type concernDocument struct {
	Concerns []Concern `json:"concerns"`
}

// ConcernError reports that a turn carried a concern block the harness could not
// read. Like ProposalError it is not a broken conversation: the turn completed
// and the answer is real. What is lost is the question the product manager was
// trying to stop and ask, which is why it is said out loud rather than treated
// as a reply that raised nothing.
type ConcernError struct {
	Err error
}

func (e *ConcernError) Error() string {
	return "the product manager raised a concern the harness cannot read: " + e.Err.Error()
}

func (e *ConcernError) Unwrap() error { return e.Err }

// extractConcerns splits a reply into the prose the operator reads and the
// concerns the turn raised. Concerns come only from the fenced block: prose that
// mentions a worry is not a concern and does not stop anything, which is the
// whole point of having a block for it.
func extractConcerns(reply string) (string, []Concern, error) {
	prose, payload, found, err := splitFencedBlock(reply, concernFence, "concern")
	if err != nil {
		return "", nil, err
	}
	if !found {
		return strings.TrimSpace(reply), nil, nil
	}
	concerns, err := decodeConcerns(payload)
	if err != nil {
		return "", nil, err
	}
	return prose, concerns, nil
}

// decodeConcerns strictly decodes the block payload. Unknown fields, trailing
// content, and oversized input are refused rather than tolerated: what stops the
// operator has to be exactly what the product manager said.
func decodeConcerns(payload string) ([]Concern, error) {
	trimmed := strings.TrimSpace(payload)
	if trimmed == "" {
		return nil, errors.New("decode concerns: the concern block is empty")
	}
	if len(trimmed) > MaxConcernBlockBytes {
		return nil, fmt.Errorf("decode concerns: block is %d bytes, limit is %d", len(trimmed), MaxConcernBlockBytes)
	}
	decoder := json.NewDecoder(bytes.NewReader([]byte(trimmed)))
	decoder.DisallowUnknownFields()
	var document concernDocument
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode concerns: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("decode concerns: unexpected trailing content after the concerns")
	}
	if len(document.Concerns) == 0 {
		return nil, errors.New("decode concerns: a concern block must raise at least one concern")
	}
	if len(document.Concerns) > MaxConcernsPerTurn {
		return nil, fmt.Errorf("decode concerns: %d concerns in one reply, limit is %d", len(document.Concerns), MaxConcernsPerTurn)
	}
	var problems []error
	for i, concern := range document.Concerns {
		if err := concern.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("concerns[%d]: %w", i, err))
		}
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("invalid concerns: %w", errors.Join(problems...))
	}
	return document.Concerns, nil
}

// Validate reports every contract violation in the concern at once.
func (c Concern) Validate() error {
	var problems []error
	if !c.Kind.Valid() {
		problems = append(problems, fmt.Errorf("kind %q is not a concern; the kinds are %s",
			c.Kind, strings.Join(concernKindNames(), ", ")))
	}
	problems = append(problems, boundConcernLine("subject", c.Subject, maxConcernSubjectBytes, true))
	// The goal is what says which case this is, so it is required where there is
	// one and refused where there is not: a concern that names a goal it cannot
	// place is two different answers at once.
	goalRequired := c.Kind == ConcernConflict || c.Kind == ConcernJudgement
	switch {
	case goalRequired:
		problems = append(problems, boundConcernLine("goal", c.Goal, maxConcernSubjectBytes, true))
	case c.Kind == ConcernUnplaceable && strings.TrimSpace(c.Goal) != "":
		problems = append(problems, errors.New("unplaceable does not take \"goal\"; it is the case where there is no goal to name"))
	}
	problems = append(problems, boundConcernText("detail", c.Detail))
	problems = append(problems, boundConcernText("question", c.Question))
	if question := strings.TrimSpace(c.Question); question != "" && !strings.HasSuffix(question, "?") {
		// A question that does not ask anything is the failure this whole block
		// exists to prevent: it reads as a remark the operator scrolls past.
		problems = append(problems, errors.New("question must ask something and end with a question mark"))
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid concern: %w", err)
	}
	return nil
}

func concernKindNames() []string {
	names := make([]string, 0, len(concernKinds))
	for _, kind := range concernKinds {
		names = append(names, string(kind))
	}
	return names
}

// boundConcernLine checks a value the operator reads as one line of a question
// put to them, so nothing in it can dress itself up as more than a line.
func boundConcernLine(field, value string, limit int, required bool) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		if required {
			return fmt.Errorf("%s is required", field)
		}
		return nil
	}
	if len(trimmed) > limit {
		return fmt.Errorf("%s is %d bytes, limit is %d", field, len(trimmed), limit)
	}
	if strings.ContainsAny(trimmed, "\r\n") {
		return fmt.Errorf("%s cannot span lines", field)
	}
	return nil
}

func boundConcernText(field, value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fmt.Errorf("%s is required", field)
	}
	if len(trimmed) > maxConcernTextBytes {
		return fmt.Errorf("%s is %d bytes, limit is %d", field, len(trimmed), maxConcernTextBytes)
	}
	return nil
}

// Render describes one concern for an operator who is about to answer it. The
// kind is the harness's own line and everything under it came from the
// provider, so provider text is indented and never printed at the margin.
func (c PendingConcern) Render() string {
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "[%s] %s\n", c.ID, c.Concern.Kind.Headline())
	rendered.WriteString(indent("about: " + strings.TrimSpace(c.Concern.Subject)))
	if goal := strings.TrimSpace(c.Concern.Goal); goal != "" {
		rendered.WriteString(indent("goal: " + goal))
	}
	rendered.WriteString(indent(c.Concern.Detail))
	rendered.WriteString(indent(strings.TrimSpace(c.Concern.Question)))
	return rendered.String()
}

// answered is what the record keeps about a concern the operator settled: the
// concern itself and what they said.
type answeredConcern struct {
	PendingConcern
	Answer string `json:"answer"`
}
