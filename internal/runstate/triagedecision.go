package runstate

// What the development manager decided about one stoppage, as a record rather
// than as prose.
//
// The counters beside this have always said how much triage spent on an item,
// and that is not the same thing as what it decided. A spent re-run says a
// decision was made; it does not say which stoppage it was about, who recorded
// it, or what the argument was. Those lived in the item's notes, which is prose
// nothing reads, so the harness that carried a decision out had to be handed the
// reasoning again — as words on a command line, attributed to a role that never
// saw them. A run could therefore carry a development-manager attribution nobody
// in that role wrote, which is exactly what
// `selected-work-passes-intake-and-records-why` exists to make impossible.
//
// So the decision is written where the budget it spends is written: on the
// item's own counter record, under the same lock and in the same update, at the
// moment the development manager's conversation records it. The verb that
// carries a decision out reads it from here and takes no words from anybody.
//
// One decision stands per stopped run. A run is decided about as one thing, so a
// later decision about the same run supersedes the earlier one rather than
// standing beside it: what a reader and a carry-out both need is the decision
// that holds now. What that leaves is a record bounded by the runs an item has
// had, which is bounded in turn by every cap the counters enforce.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// The decisions triage may record. They are the development manager's own
// vocabulary, declared here because this is where a decision becomes durable:
// the conversation that records one and the action that carries one out are two
// packages, and a word each of them spelled for itself is a word they could
// spell differently.
const (
	// TriageDecisionRepair hands the item another bounded go at the change it
	// already has.
	TriageDecisionRepair = "repair"
	// TriageDecisionRerun runs the item again from the start.
	TriageDecisionRerun = "rerun"
	// TriageDecisionRescope splits what was out of scope into a child of the item.
	TriageDecisionRescope = "rescope"
	// TriageDecisionRearm repeats an authorized merge request the forge dropped.
	TriageDecisionRearm = "rearm"
	// TriageDecisionWait is the decision that nothing is to be done yet.
	TriageDecisionWait = "wait"
	// TriageDecisionEscalate hands the entry to the operator.
	TriageDecisionEscalate = "escalate"
)

// TriageDecisionVocabulary lists the decisions in the order the development
// manager's contract states them, so a refusal names exactly what was available.
func TriageDecisionVocabulary() []string {
	return []string{
		TriageDecisionRepair, TriageDecisionRerun, TriageDecisionRescope,
		TriageDecisionRearm, TriageDecisionWait, TriageDecisionEscalate,
	}
}

// The bounds one decision is held to. The reasoning is the whole of what makes a
// decision answerable later and is bounded like an override's; the role and the
// conversation are identifiers rather than arguments.
const (
	MaxTriageDecisionReasonBytes       = 4 << 10
	MaxTriageDecisionByBytes           = 256
	MaxTriageDecisionConversationBytes = 256
)

// MaxTriageDecisions bounds how many stoppages one item's record carries
// decisions about. One decision stands per run, so reaching it would take an
// item more runs than every cap here permits between them.
const MaxTriageDecisions = 64

// TriageDecision is one decision the development manager recorded about one
// stopped run.
//
// Every field is required, which is the point of the record: a decision nobody
// is named for, or one that names no stoppage, is exactly the prose this exists
// to replace. Who recorded it and where are what make the attribution on a run
// citable — a re-run says which conversation and which turn decided it, and a
// reader can go and find that turn.
type TriageDecision struct {
	// Decision is the word from the vocabulary above.
	Decision string `json:"decision"`
	// RunID is the stopped run whose docket entry this settles. It is what makes
	// the record one per stoppage rather than one per item: an item decided about
	// twice was decided about twice, and each decision is about its own run.
	RunID string `json:"run_id"`
	// Reason is the development manager's own reasoning, in its own words. It is
	// what a carry-out records as why the run it starts exists, so it is kept
	// verbatim rather than summarized.
	Reason string `json:"reason"`
	// DecidedBy is the role that recorded it. It is written down rather than
	// assumed, because the whole value of the record is that the attribution on a
	// run is read from something the role itself wrote.
	DecidedBy string `json:"decided_by"`
	// Conversation and Turn are where it was recorded, which is what a citation
	// points at: an attribution naming neither is one nobody can go and check.
	Conversation string    `json:"conversation"`
	Turn         int       `json:"turn"`
	DecidedAt    time.Time `json:"decided_at"`
}

// Validate reports every contract violation in the decision at once.
func (d TriageDecision) Validate() error {
	var problems []error
	if err := validTriageDecision(d.Decision); err != nil {
		problems = append(problems, err)
	}
	switch run := strings.TrimSpace(d.RunID); {
	case run == "":
		problems = append(problems, errors.New("a triage decision names the stopped run it is about"))
	case !ValidRunID(run):
		problems = append(problems, fmt.Errorf("%q is not a run identifier; a triage decision names the run its docket entry is about", d.RunID))
	}
	switch reason := strings.TrimSpace(d.Reason); {
	case reason == "":
		problems = append(problems, errors.New("a triage decision records the reasoning it was made on, which is what a carry-out records as why the run it starts exists"))
	case len(reason) > MaxTriageDecisionReasonBytes:
		problems = append(problems, fmt.Errorf("the decision reason is %d bytes, limit is %d", len(reason), MaxTriageDecisionReasonBytes))
	}
	switch by := strings.TrimSpace(d.DecidedBy); {
	case by == "":
		problems = append(problems, errors.New("a triage decision names the role that recorded it; one nobody is named for is what this record exists to replace"))
	case len(by) > MaxTriageDecisionByBytes:
		problems = append(problems, fmt.Errorf("the deciding role is %d bytes, limit is %d", len(by), MaxTriageDecisionByBytes))
	}
	switch conversation := strings.TrimSpace(d.Conversation); {
	case conversation == "":
		problems = append(problems, errors.New("a triage decision names the conversation it was recorded in, which is what an attribution citing it points at"))
	case len(conversation) > MaxTriageDecisionConversationBytes:
		problems = append(problems, fmt.Errorf("the conversation is %d bytes, limit is %d", len(conversation), MaxTriageDecisionConversationBytes))
	}
	if d.Turn < 0 {
		problems = append(problems, fmt.Errorf("turn %d is not a turn", d.Turn))
	}
	if d.DecidedAt.IsZero() {
		problems = append(problems, errors.New("a triage decision records when it was made"))
	}
	return errors.Join(problems...)
}

// Cite names the record itself, for an attribution that has to be checkable
// rather than only plausible. What it points at is the turn the decision was
// recorded on, which is where the reasoning beside it was written.
func (d TriageDecision) Cite() string {
	return fmt.Sprintf("recorded by the %s in conversation %s after turn %d, at %s",
		strings.TrimSpace(d.DecidedBy), strings.TrimSpace(d.Conversation), d.Turn, d.DecidedAt.UTC().Format(time.RFC3339))
}

// Describe says what one decision was, for whoever is reading an item's record.
func (d TriageDecision) Describe() string {
	return fmt.Sprintf("%q on the stopped work of run %s, %s: %s",
		d.Decision, d.RunID, d.Cite(), strings.TrimSpace(d.Reason))
}

// Spends reports a decision that buys another attempt at work that already
// failed once, which is the half of the vocabulary the durable budgets bound.
// The other three buy no attempt and are never refused for budget.
func (d TriageDecision) Spends() bool {
	switch d.Decision {
	case TriageDecisionRepair, TriageDecisionRerun, TriageDecisionRearm:
		return true
	default:
		return false
	}
}

// DecisionOf is the decision standing about one stopped run, and whether there
// is one at all. It is what a carry-out reads: an item with a spent budget and
// no decision naming this stoppage is an item whose decision was about some
// other run of it.
func (c TriageCounters) DecisionOf(runID string) (TriageDecision, bool) {
	run := strings.TrimSpace(runID)
	if run == "" {
		return TriageDecision{}, false
	}
	for index := len(c.Decisions) - 1; index >= 0; index-- {
		if c.Decisions[index].RunID == run {
			return c.Decisions[index], true
		}
	}
	return TriageDecision{}, false
}

// RecordDecision records a triage decision that spends nothing: a re-scope, a
// wait, or an escalation. The three that buy another attempt are recorded by the
// operation that spends their budget, in the same write, so that a decision and
// the spend it authorizes can never be one without the other.
func (s *TriageStore) RecordDecision(ctx context.Context, workItemID string, decision TriageDecision, at time.Time) (TriageCounters, error) {
	if decision.Spends() {
		return TriageCounters{}, fmt.Errorf(
			"a %q decision spends the item's durable budget, so it is recorded by the operation that spends it rather than on its own",
			decision.Decision)
	}
	when := at
	if when.IsZero() {
		when = time.Now()
	}
	prepared, err := prepareTriageDecision(decision, when)
	if err != nil {
		return TriageCounters{}, err
	}
	return s.update(ctx, workItemID, when, func(counters *TriageCounters) error {
		return counters.recordDecision(prepared)
	})
}

// prepareTriageDecision trims what a caller supplied and dates the decision from
// the update's own clock, so the decision and the record that carries it can
// never disagree about when it was made.
func prepareTriageDecision(decision TriageDecision, at time.Time) (TriageDecision, error) {
	decision.Decision = strings.TrimSpace(decision.Decision)
	decision.RunID = strings.TrimSpace(decision.RunID)
	decision.Reason = strings.TrimSpace(decision.Reason)
	decision.DecidedBy = strings.TrimSpace(decision.DecidedBy)
	decision.Conversation = strings.TrimSpace(decision.Conversation)
	decision.DecidedAt = at.UTC()
	if err := decision.Validate(); err != nil {
		return TriageDecision{}, fmt.Errorf("invalid triage decision: %w", err)
	}
	return decision, nil
}

// recordDecision puts one decision on the item's record, in place of whatever
// was standing about the same stoppage. Superseding rather than appending is
// what keeps the record answerable: a development manager who decided a wait and
// then a re-run about one run decided a re-run, and a carry-out that found both
// would have to guess which.
func (c *TriageCounters) recordDecision(decision TriageDecision) error {
	standing := make([]TriageDecision, 0, len(c.Decisions)+1)
	for _, existing := range c.Decisions {
		if existing.RunID != decision.RunID {
			standing = append(standing, existing)
		}
	}
	if len(standing) >= MaxTriageDecisions {
		return fmt.Errorf(
			"%s already carries decisions about %d stoppages, which is the bound: an item that has stopped this many times has something no further decision settles",
			c.WorkItemID, len(standing))
	}
	c.Decisions = append(standing, decision)
	return nil
}

// validTriageDecision refuses a word the vocabulary does not have, and says what
// the words are.
func validTriageDecision(decision string) error {
	switch decision {
	case TriageDecisionRepair, TriageDecisionRerun, TriageDecisionRescope,
		TriageDecisionRearm, TriageDecisionWait, TriageDecisionEscalate:
		return nil
	default:
		return fmt.Errorf("%q is not a triage decision; the decisions are %s",
			decision, strings.Join(TriageDecisionVocabulary(), ", "))
	}
}

// validateTriageDecisions reports every contract violation in one record's
// decisions at once. The one that is about the record rather than about any
// single decision is the stoppage: decisions supersede each other by run, so two
// standing about one run describe a record the only thing that writes them could
// not have written, and a carry-out reading it would have to choose between them.
func validateTriageDecisions(decisions []TriageDecision) []error {
	var problems []error
	if len(decisions) > MaxTriageDecisions {
		problems = append(problems, fmt.Errorf("decisions about %d stoppages are recorded, which exceeds the bound of %d", len(decisions), MaxTriageDecisions))
	}
	decided := map[string]bool{}
	for index, decision := range decisions {
		if err := decision.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("decisions[%d]: %w", index, err))
			continue
		}
		if decided[decision.RunID] {
			problems = append(problems, fmt.Errorf(
				"decisions[%d]: the stoppage of run %s already has a decision standing, and a later one supersedes it rather than standing beside it",
				index, decision.RunID))
		}
		decided[decision.RunID] = true
	}
	return problems
}
