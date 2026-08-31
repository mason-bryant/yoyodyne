// Package supervision is the advisory half of the management loop: what the
// harness reads before it puts a request in front of a role, and what it reads
// again after a restart.
//
// Three things are being proved here, and they are the whole of what this
// package is for.
//
// A restart loses nothing and repeats nothing. Every request is a record on
// disk before the role it names is ever invoked, and the answer that comes back
// is written to the same record. So a process that dies mid-delivery leaves a
// request whose attempt nobody is holding, and the next process to read it can
// tell that from a request that was answered: the first is delivered again, the
// second is settled and never delivered twice. Nothing rests on the provider
// session that was open when the process died, which is what the
// durable-state-is-provider-independent invariant asks of every record here.
//
// Coordination is bounded. One topic takes its requests one at a time, so two
// deliveries never interleave against the same durable conversation, and
// different topics run independently of each other. Across the product the
// number of deliveries open at once is capped, and one request may be attempted
// only so many times before it ends as unresolved and the operator is told —
// which is what stops two roles deferring to each other for ever at the
// operator's expense.
//
// Judgments go stale visibly. A readiness record says which revision of which
// document it was judged against, so a judgment made against something that has
// since moved is derivable rather than remembered. What that produces is a
// reason to wake the role that owns the judgment, and nothing else.
//
// # Advisory rather than authoritative
//
// Nothing here invokes a role, and nothing here stops one. Survey reads durable
// records and returns what the harness may do now; the harness is what invokes,
// under its own lease and its own gates, because it is the only thing that may
// — the harness-is-the-only-role-invoker invariant is why this package returns a
// plan rather than taking one. A stale judgment in that plan blocks nothing
// either: it names the role to wake and the work the staleness touches, and what
// happens next is that role's decision or the operator's. Readiness becomes a
// gate on what the scheduler pulls only through a later revision to the
// management-and-supervision design, after this slice has shown how staleness
// actually behaves.
package supervision

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// SchemaVersion is 1 and has never changed. A request is written as it goes:
// before the first attempt is made, again as each attempt opens and closes, and
// once more when the answer or the ending is recorded.
const SchemaVersion = 1

// DefaultCycleLimit is how many times one request may be attempted where
// nothing configures otherwise. Three is enough for a delivery interrupted by a
// crash and retried, and small enough that a request nothing can answer costs a
// bounded amount before it becomes one legible question for the operator.
const DefaultCycleLimit = 3

// The bounds on what one request carries. A request moves a question and the
// references to read it against; anything that needs a document attached to it
// is work rather than a request.
const (
	MaxTopicBytes     = 200
	MaxSubjectBytes   = 4 << 10
	MaxResponseBytes  = 16 << 10
	MaxProblemBytes   = 2 << 10
	MaxHolderBytes    = 200
	MaxReferenceBytes = 200
	// MaxReferences bounds how many things one request or judgment rests on.
	// Every one of them is a revision somebody has to keep current, so a record
	// naming fifty of them is a record that will be stale the moment it is
	// written.
	MaxReferences = 32
)

// Kind is what one role is asking another for. There are three, deliberately:
// the vocabulary is the management-loop-protocol design's, and a fourth arrives
// only for a workflow that demonstrably means something the three do not.
type Kind string

const (
	// KindConsult asks another role for a judgment that is that role's to make.
	KindConsult Kind = "consult"
	// KindClarify asks for information the caller needs to finish a judgment of
	// its own.
	KindClarify Kind = "clarify"
	// KindEscalate asks the product manager to get an operator decision, because
	// what is needed is beyond any role's delegated authority.
	KindEscalate Kind = "escalate"
)

func (k Kind) Valid() bool {
	switch k {
	case KindConsult, KindClarify, KindEscalate:
		return true
	default:
		return false
	}
}

// Kinds are the three, in the order they escalate: a judgment, then the
// information behind one, then the operator.
func Kinds() []Kind { return []Kind{KindConsult, KindClarify, KindEscalate} }

// Outcome is how a request ended. One still being conducted carries none.
type Outcome string

const (
	// OutcomeAnswered is the ordinary ending: the target role replied and the
	// reply is on the record.
	OutcomeAnswered Outcome = "answered"
	// OutcomeUnresolved is the cycle limit being reached with no answer. It is
	// not a silent cutoff: the request ends as this and the product manager
	// carries it to the operator, so a question nothing could answer becomes a
	// rare thing somebody reads rather than a retry that never stops.
	OutcomeUnresolved Outcome = "unresolved-at-limit"
	// OutcomeWithdrawn is the asking role no longer needing it — the work it was
	// asked about was closed, or the question was answered another way.
	OutcomeWithdrawn Outcome = "withdrawn"
)

func (o Outcome) Valid() bool {
	switch o {
	case OutcomeAnswered, OutcomeUnresolved, OutcomeWithdrawn:
		return true
	default:
		return false
	}
}

// Reference is one durable thing a request or a judgment was written against,
// and the revision of it that was read.
//
// The revision is the point. A reference without one says which document was
// consulted and nothing about whether what it said then is what it says now,
// and "whether it still says that" is the only question staleness asks.
type Reference struct {
	// What names the kind of thing referred to — an artifact, a work item, a
	// run. It is free text rather than a closed set, because what a role reads
	// before it answers is not this package's to enumerate.
	What string `json:"what"`
	ID   string `json:"id"`
	// Revision is what that thing was at when it was read. Anything the producer
	// can compare for equality serves: a revision timestamp, a content hash, a
	// tracker's own version.
	Revision string `json:"revision"`
}

// Key names the thing referred to, without the revision. It is how a reference
// is looked up against what is current now.
func (r Reference) Key() string { return r.What + "/" + r.ID }

func (r Reference) validate(what string) error {
	return errors.Join(
		boundedText(what+" what", r.What, MaxReferenceBytes, true),
		boundedText(what+" id", r.ID, MaxReferenceBytes, true),
		boundedText(what+" revision", r.Revision, MaxReferenceBytes, true),
	)
}

// Attempt is one delivery of a request to the role it names: the harness took a
// lease, invoked the role, and either got an answer back or did not.
//
// The attempt is recorded when it opens rather than when it closes, so a
// process that dies mid-delivery leaves an attempt that was spent rather than
// one that was taken and never counted. That is the same direction every other
// durable budget here fails in, and it is what makes the cycle limit hold across
// a crash.
type Attempt struct {
	Number int `json:"number"`
	// Holder names the process that took the lease for this attempt, so a
	// restart reading an open attempt can say who was carrying it.
	Holder    string    `json:"holder"`
	StartedAt time.Time `json:"started_at"`
	// FinishedAt is absent while the attempt is open. An open attempt whose
	// holder is gone is what a restart reclaims.
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	// Problem is why this attempt produced no answer, where it produced none.
	// An attempt that was spent and says nothing about itself is the thing an
	// operator cannot act on.
	Problem string `json:"problem,omitempty"`
}

// Open reports an attempt that was started and never finished.
func (a Attempt) Open() bool { return a.FinishedAt == nil }

// Response is what the target role said, and what served the invocation that
// produced it.
//
// The six fields naming the provider are what the
// durable-state-is-provider-independent invariant asks of every provider
// invocation: which backend, which model was asked for and which was served,
// which account, which configuration, and which harness binary made the call.
// They are here rather than on the request because a request retried across a
// configuration edit is answered by whatever served the attempt that answered
// it.
type Response struct {
	Text string    `json:"text"`
	At   time.Time `json:"at"`
	// Attempt is which delivery produced this, so the answer and the cost of
	// getting it are the same record.
	Attempt        int            `json:"attempt"`
	Backend        domain.Backend `json:"backend,omitempty"`
	Model          string         `json:"model,omitempty"`
	ResolvedModel  string         `json:"resolved_model,omitempty"`
	AccountAlias   string         `json:"account_alias,omitempty"`
	ConfigRevision string         `json:"config_revision,omitempty"`
	Build          string         `json:"build,omitempty"`
	// SessionID is the provider session the invocation ran in, kept only because
	// resuming one is cheaper than starting over. Nothing is read back from it
	// and nothing here is lost when it expires: the answer above is the record.
	SessionID string  `json:"session_id,omitempty"`
	CostUSD   float64 `json:"cost_usd,omitempty"`
}

// Request is one durable typed ask from one role to another.
type Request struct {
	SchemaVersion int              `json:"schema_version"`
	ID            string           `json:"id"`
	ProductID     domain.ProductID `json:"product_id"`
	// Topic is the durable conversation or subject this belongs to, and it is
	// what serializes: requests sharing a topic are delivered one at a time, and
	// requests on different topics proceed independently. It is what a
	// conversation lease is taken on, named on the record so a reader can see
	// which requests are queued behind which.
	Topic string           `json:"topic"`
	Kind  Kind             `json:"kind"`
	From  domain.AgentRole `json:"from"`
	To    domain.AgentRole `json:"to"`
	// Subject is what is being asked, in the asking role's own words.
	Subject string `json:"subject"`
	// Refers are the durable things the answer is to be read against, each at the
	// revision the asking role read.
	Refers []Reference `json:"refers,omitempty"`
	// CycleLimit is how many attempts this request is allowed. It is copied onto
	// the request when it opens rather than read from the configuration each
	// time, so a process death, a configuration edit, or a second process cannot
	// reset what a request already in flight may spend.
	CycleLimit int       `json:"cycle_limit"`
	Attempts   []Attempt `json:"attempts,omitempty"`
	// Response is absent until the target role answers, and is written exactly
	// once. It is what makes a settled request unrepeatable: a restart that finds
	// one settles the request rather than delivering it again.
	Response *Response `json:"response,omitempty"`
	// Outcome is empty while the request is open.
	Outcome   Outcome    `json:"outcome,omitempty"`
	OpenedAt  time.Time  `json:"opened_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	SettledAt *time.Time `json:"settled_at,omitempty"`
}

var requestIDPattern = regexp.MustCompile(`^request-[a-f0-9]{32}$`)

// NewRequestID issues an identifier of the shape a store will name a file
// after.
func NewRequestID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate request id: %w", err)
	}
	return "request-" + hex.EncodeToString(raw), nil
}

// ValidRequestID reports an identifier of the shape this package issues. A
// store names a file after it, so it is checked before anything built from
// outside is used as a path.
func ValidRequestID(id string) bool { return requestIDPattern.MatchString(id) }

// Open reports a request that has not ended.
func (r Request) Open() bool { return r.Outcome == "" }

// Answered reports a request whose answer is on the record, whether or not the
// ending has been written yet. The gap between the two is exactly the window a
// crash can land in, and it is the case a restart must not deliver again.
func (r Request) Answered() bool { return r.Response != nil }

// Spent is how many attempts this request has cost, an attempt still open
// included: it was started, so it was spent.
func (r Request) Spent() int { return len(r.Attempts) }

// CyclesRemaining is how many further attempts this request may take. It is
// never negative: a request at its limit has nothing remaining rather than a
// debt.
func (r Request) CyclesRemaining() int {
	if remaining := r.CycleLimit - len(r.Attempts); remaining > 0 {
		return remaining
	}
	return 0
}

// InFlight is the attempt that was started and never finished, where there is
// one. Only the last attempt can be open, which Validate holds it to.
func (r Request) InFlight() (Attempt, bool) {
	if len(r.Attempts) == 0 {
		return Attempt{}, false
	}
	last := r.Attempts[len(r.Attempts)-1]
	if last.Open() {
		return last, true
	}
	return Attempt{}, false
}

// CostUSD is what getting the answer cost, as the provider reported it.
func (r Request) CostUSD() float64 {
	if r.Response == nil {
		return 0
	}
	return r.Response.CostUSD
}

// Moved reports the references this request was written against that have since
// changed, given the current revision of everything by reference key. A
// reference the caller knows nothing current about is not reported here, because
// silence is not evidence that something held still; Survey names those
// separately as what it could not judge.
func (r Request) Moved(current map[string]string) []Moved {
	return moved(r.Refers, current)
}

// Unknown reports the references this request was written against that the
// caller could say nothing current about.
func (r Request) Unknown(current map[string]string) []Reference {
	return unknown(r.Refers, current)
}

// Validate reports every contract violation in the request at once.
func (r Request) Validate() error {
	var problems []error
	if r.SchemaVersion != SchemaVersion {
		problems = append(problems, fmt.Errorf("request schema version %d is not supported", r.SchemaVersion))
	}
	if !ValidRequestID(r.ID) {
		problems = append(problems, fmt.Errorf("request id %q is invalid", r.ID))
	}
	if err := domain.ValidateIdentifier("product id", string(r.ProductID)); err != nil {
		problems = append(problems, err)
	}
	problems = append(problems,
		boundedText("topic", r.Topic, MaxTopicBytes, true),
		boundedText("subject", r.Subject, MaxSubjectBytes, true),
	)
	if !r.Kind.Valid() {
		problems = append(problems, fmt.Errorf("kind %q is not one of %s", r.Kind, kindNames()))
	}
	if !r.From.Valid() {
		problems = append(problems, fmt.Errorf("requesting role %q is not one of the harness's roles", r.From))
	}
	if !r.To.Valid() {
		problems = append(problems, fmt.Errorf("target role %q is not one of the harness's roles", r.To))
	}
	if r.From == r.To && r.From.Valid() {
		problems = append(problems, errors.New("a request is between two roles; a role does not ask itself"))
	}
	// An escalation is the one kind whose target is fixed. It exists to reach the
	// operator, and the product manager is the only role that carries anything to
	// them — an escalation addressed anywhere else is a request that would go
	// round the person it was raised for.
	if r.Kind == KindEscalate && r.To != domain.RoleProductManager && r.To.Valid() {
		problems = append(problems, fmt.Errorf("an escalation reaches the operator through the product manager, not %q", r.To))
	}
	if len(r.Refers) > MaxReferences {
		problems = append(problems, fmt.Errorf("%d references are recorded, limit is %d", len(r.Refers), MaxReferences))
	}
	for i, reference := range r.Refers {
		problems = append(problems, reference.validate(fmt.Sprintf("refers[%d]", i)))
	}
	if r.CycleLimit < 1 {
		problems = append(problems, fmt.Errorf("cycle limit is %d; a request is allowed at least one attempt", r.CycleLimit))
	}
	if len(r.Attempts) > r.CycleLimit {
		problems = append(problems, fmt.Errorf("%d attempts are recorded against a limit of %d", len(r.Attempts), r.CycleLimit))
	}
	for i, attempt := range r.Attempts {
		if attempt.Number != i+1 {
			problems = append(problems, fmt.Errorf("attempts[%d] is numbered %d", i, attempt.Number))
		}
		problems = append(problems,
			boundedText(fmt.Sprintf("attempts[%d] holder", i), attempt.Holder, MaxHolderBytes, true),
			boundedText(fmt.Sprintf("attempts[%d] problem", i), attempt.Problem, MaxProblemBytes, false),
		)
		if attempt.StartedAt.IsZero() {
			problems = append(problems, fmt.Errorf("attempts[%d] records no moment it was started", i))
		}
		// Only the last attempt can be the one still running. An earlier one left
		// open is a record two processes wrote over each other, and delivering
		// against it would be the duplicate this whole scheme is here to prevent.
		if attempt.Open() && i != len(r.Attempts)-1 {
			problems = append(problems, fmt.Errorf("attempts[%d] is still open behind a later attempt", i))
		}
	}
	problems = append(problems, r.validateResponse())
	if r.Outcome != "" && !r.Outcome.Valid() {
		problems = append(problems, fmt.Errorf("outcome %q is not one a request ends with", r.Outcome))
	}
	if (r.Outcome == "") != (r.SettledAt == nil) {
		problems = append(problems, errors.New("an outcome and the moment it was reached are recorded together"))
	}
	if r.Outcome == OutcomeUnresolved && r.Response != nil {
		problems = append(problems, errors.New("a request that ended unresolved has no answer recorded"))
	}
	if r.OpenedAt.IsZero() {
		problems = append(problems, errors.New("opened at is required"))
	}
	if r.UpdatedAt.IsZero() {
		problems = append(problems, errors.New("updated at is required"))
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid request: %w", err)
	}
	return nil
}

func (r Request) validateResponse() error {
	if r.Response == nil {
		// The one ending that asserts an answer is the one that has to have it.
		if r.Outcome == OutcomeAnswered {
			return errors.New("a request that ended answered records the answer")
		}
		return nil
	}
	var problems []error
	problems = append(problems, boundedText("response text", r.Response.Text, MaxResponseBytes, true))
	if r.Response.At.IsZero() {
		problems = append(problems, errors.New("response records no moment it came back"))
	}
	if r.Response.Attempt < 1 || r.Response.Attempt > len(r.Attempts) {
		problems = append(problems, fmt.Errorf("response names attempt %d, and %d attempts are recorded",
			r.Response.Attempt, len(r.Attempts)))
	}
	if r.Response.CostUSD < 0 {
		problems = append(problems, errors.New("response cost cannot be negative"))
	}
	// What served the invocation is checked for shape rather than for presence,
	// exactly as an exchange round's is: a record naming an account or a
	// configuration nothing could have produced says less than one naming
	// neither, because it reads as evidence.
	if r.Response.Backend != "" && !r.Response.Backend.Valid() {
		problems = append(problems, fmt.Errorf("response backend %q is not a backend identifier", r.Response.Backend))
	}
	if r.Response.AccountAlias != "" && !accountAliasPattern.MatchString(r.Response.AccountAlias) {
		problems = append(problems, fmt.Errorf("response account alias %q is not an account alias", r.Response.AccountAlias))
	}
	if r.Response.ConfigRevision != "" && !configRevisionPattern.MatchString(r.Response.ConfigRevision) {
		problems = append(problems, fmt.Errorf("response config revision %q is not a configuration revision", r.Response.ConfigRevision))
	}
	if r.Response.Build != "" && !buildPattern.MatchString(r.Response.Build) {
		problems = append(problems, fmt.Errorf("response build %q is not a revision", r.Response.Build))
	}
	// A settled request that carries an answer settled because of it. Recording
	// any other ending over an answer already on the record is how an answer
	// somebody paid for gets lost.
	if r.Outcome != "" && r.Outcome != OutcomeAnswered {
		problems = append(problems, fmt.Errorf("a request whose answer is recorded ended %q rather than answered", r.Outcome))
	}
	return errors.Join(problems...)
}

// The shape of what a response records about how it was served. It is stated
// here rather than imported from the configuration package for the reason the
// run record states its own: the durable schema stays independent of the code
// that produces what it stores.
var (
	accountAliasPattern   = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)
	configRevisionPattern = regexp.MustCompile(`^cfg-[a-f0-9]{8,}$`)
	buildPattern          = regexp.MustCompile(`^[a-f0-9]{7,64}$`)
)

func kindNames() string {
	names := make([]string, 0, len(Kinds()))
	for _, kind := range Kinds() {
		names = append(names, string(kind))
	}
	return strings.Join(names, ", ")
}

// boundedText checks one value the record keeps verbatim.
func boundedText(field, value string, limit int, required bool) error {
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
	return nil
}
