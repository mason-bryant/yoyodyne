package amendment

// What becomes of a proposal, and who is entitled to say so.
//
// A decision is the only thing this mechanism produces. It writes no document:
// approving records that the owner's authority came down in favour of the
// change, and the change itself is then made by the owner, in the artifact,
// under a revision that says the owner made it. Keeping those two apart is what
// stops an approval from being a way to paste text into somebody else's
// document, and it is why an approval here is safe to record even when nobody
// gets round to the edit for a week: nothing has been asserted about the
// document that is not true of it.

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// Verdict is what was decided. There are two, and neither of them is "later": a
// proposal nobody has decided is pending, which is the absence of a verdict
// rather than one of them.
type Verdict string

const (
	VerdictApproved Verdict = "approved"
	VerdictDeclined Verdict = "declined"
)

func (v Verdict) Valid() bool {
	return v == VerdictApproved || v == VerdictDeclined
}

// Decider is who actually made the decision, as against whose authority it was
// made under. The two are recorded apart because they come apart in practice:
// the operator may direct any role and decides in the stead of an owner that
// does not run yet, and a record that only named the authority would make those
// look like the same event.
type Decider string

const (
	// DeciderOperator is the person, deciding directly or in the stead of the
	// owning role.
	DeciderOperator Decider = "operator"
	// DeciderOwner is the owning role itself, deciding about its own document.
	DeciderOwner Decider = "owner"
)

func (d Decider) Valid() bool {
	return d == DeciderOperator || d == DeciderOwner
}

// MaxReasonBytes bounds what a decision says about itself. It is the size of a
// paragraph, because a decision explains itself rather than restating the
// proposal.
const MaxReasonBytes = 4 << 10

// Decision is one settled proposal. It names the proposal rather than repeating
// it, so what was decided can never drift from what was proposed.
type Decision struct {
	SchemaVersion int     `json:"schema_version"`
	ProposalID    string  `json:"proposal_id"`
	Verdict       Verdict `json:"verdict"`
	// Authority is the role the decision was taken under, which is the artifact's
	// owner. Decider is who exercised it.
	Authority domain.AgentRole `json:"authority"`
	Decider   Decider          `json:"decider"`
	// Reason is why. It is required on a decline, because a proposal turned down
	// with no reason is one the same argument arrives to make again, and optional
	// on an approval, where what follows is the owner's own revision saying why
	// the document changed.
	Reason    string    `json:"reason,omitempty"`
	DecidedAt time.Time `json:"decided_at"`
}

// Validate reports every contract violation in the decision at once.
func (d Decision) Validate() error {
	var problems []error
	if d.SchemaVersion != SchemaVersion {
		problems = append(problems, fmt.Errorf("schema_version must be %d", SchemaVersion))
	}
	if !idPattern.MatchString(d.ProposalID) {
		problems = append(problems, errors.New("proposal id is invalid"))
	}
	if !d.Verdict.Valid() {
		problems = append(problems, fmt.Errorf("verdict %q must be %q or %q", d.Verdict, VerdictApproved, VerdictDeclined))
	}
	if err := domain.ValidateIdentifier("authority", string(d.Authority)); err != nil {
		problems = append(problems, err)
	}
	if !d.Decider.Valid() {
		problems = append(problems, fmt.Errorf("decider %q must be %q or %q", d.Decider, DeciderOperator, DeciderOwner))
	}
	switch reason := strings.TrimSpace(d.Reason); {
	case reason == "" && d.Verdict == VerdictDeclined:
		problems = append(problems, errors.New("a declined proposal records why it was turned down"))
	case len(reason) > MaxReasonBytes:
		problems = append(problems, fmt.Errorf("reason is %d bytes, limit is %d", len(reason), MaxReasonBytes))
	}
	if d.DecidedAt.IsZero() {
		problems = append(problems, errors.New("decided_at is required"))
	}
	if err := errors.Join(problems...); err != nil {
		return fmt.Errorf("invalid decision on %q: %w", d.ProposalID, err)
	}
	return nil
}

// Decide settles a proposal, under the authority of the role that owns the
// artifact. Nothing else may: a decision taken under any other role's authority
// would be the boundary crossed by the very mechanism that exists to hold it.
func (p Proposal) Decide(verdict Verdict, decider Decider, reason string, now time.Time) (Decision, error) {
	decision := Decision{
		SchemaVersion: SchemaVersion,
		ProposalID:    p.ID,
		Verdict:       verdict,
		Authority:     p.Owner,
		Decider:       decider,
		Reason:        strings.TrimSpace(reason),
		DecidedAt:     now.UTC(),
	}
	if err := decision.Validate(); err != nil {
		return Decision{}, err
	}
	return decision, nil
}

// Render describes one decision for whoever is reading what became of a
// proposal.
func (d Decision) Render() string {
	var rendered strings.Builder
	who := "the operator"
	if d.Decider == DeciderOwner {
		who = "the " + string(d.Authority)
	}
	fmt.Fprintf(&rendered, "  %s by %s under the %s's authority, %s\n",
		d.Verdict, who, d.Authority, d.DecidedAt.UTC().Format(time.RFC3339))
	if reason := strings.TrimSpace(d.Reason); reason != "" {
		for _, line := range indent(reason) {
			rendered.WriteString(line)
		}
	}
	if d.Verdict == VerdictApproved {
		// Said every time an approval is read, because the one way this mechanism
		// could quietly become an edit is somebody believing the document already
		// says what was approved.
		fmt.Fprintf(&rendered, "      nothing was written to the artifact; the %s makes the change\n", d.Authority)
	}
	return rendered.String()
}

// Record is one line of the durable log: a proposal raised, or a decision on
// one. They share a log rather than living in two because the order between
// them is the thing that matters — a decision means nothing without the
// proposal it settles, and a proposal is pending exactly until a decision
// follows it.
type Record struct {
	Proposal *Proposal `json:"proposal,omitempty"`
	Decision *Decision `json:"decision,omitempty"`
}

// Validate refuses a record that is neither or both. A line that carries
// nothing says nothing happened, and one that carries both would be a proposal
// decided in the same breath it was raised, which no path here can produce.
func (r Record) Validate() error {
	switch {
	case r.Proposal == nil && r.Decision == nil:
		return errors.New("a record carries either a proposal or a decision, and this carries neither")
	case r.Proposal != nil && r.Decision != nil:
		return errors.New("a record carries either a proposal or a decision, and this carries both")
	case r.Proposal != nil:
		return r.Proposal.Validate()
	default:
		return r.Decision.Validate()
	}
}

// Proposals returns every proposal in the log, in the order it was raised.
func Proposals(records []Record) []Proposal {
	proposals := make([]Proposal, 0, len(records))
	for _, record := range records {
		if record.Proposal != nil {
			proposals = append(proposals, *record.Proposal)
		}
	}
	return proposals
}

// Find returns one proposal by id.
func Find(records []Record, id string) (Proposal, bool) {
	for _, record := range records {
		if record.Proposal != nil && record.Proposal.ID == id {
			return *record.Proposal, true
		}
	}
	return Proposal{}, false
}

// DecisionOn returns what was decided about a proposal, and whether anything
// was. The first decision is the one that stands: a log is append-only and two
// decisions on one proposal can only be a race or a mistake, and treating the
// later one as an override would let a second answer quietly replace the
// decision somebody already acted on.
func DecisionOn(records []Record, id string) (Decision, bool) {
	for _, record := range records {
		if record.Decision != nil && record.Decision.ProposalID == id {
			return *record.Decision, true
		}
	}
	return Decision{}, false
}

// Pending returns the proposals nobody has decided, oldest first, which is the
// order they were raised in and so the order they have been waiting in.
func Pending(records []Record) []Proposal {
	var pending []Proposal
	for _, proposal := range Proposals(records) {
		if _, decided := DecisionOn(records, proposal.ID); decided {
			continue
		}
		pending = append(pending, proposal)
	}
	return pending
}

// PendingFor returns the proposals awaiting one role's decision, which is what
// that role is shown when it runs and what an operator deciding in its stead
// asks for.
func PendingFor(records []Record, owner domain.AgentRole) []Proposal {
	var pending []Proposal
	for _, proposal := range Pending(records) {
		if proposal.Owner == owner {
			pending = append(pending, proposal)
		}
	}
	return pending
}
