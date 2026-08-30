package runstate

// The operator crossing one work item's triage caps.
//
// The caps beside this exist to stop machines looping, and they do it by
// refusing: a development manager past an item's round budget may escalate or
// re-scope, and may not hand the item back again. That is the whole of what they
// are for and it is right — until the escalation is answered, at which point they
// refuse the answer as well. An item at five rounds of four could have no re-run
// recorded against it, the re-run verb refuses without that record, and the
// escalation entry records neither, so an operator who read the escalation and
// decided the work should be run again had no recorded path to say so. The
// decision was carried out by admitting fresh work in its place, twice, which is
// surgery rather than a path.
//
// So a cap is crossable, by exactly one thing. An override is the operator's own
// decision written down: the budget it lifts, what it lifts it to, who decided
// and when, and why. Nothing an agent does produces one — the development
// manager's decision vocabulary has no word for it, and the actions that carry
// decisions out read overrides rather than write them. Recording one is a
// terminal command and the operator's own hand, for the reason `yoyo release` is
// one: the switch that answers an escalation has to work with no conversation
// open.
//
// It clears or raises, and never lowers. An override that would leave a budget no
// larger than it already stands is refused, so an item's overrides are a
// monotonic account of who gave it more room and why, and the last word on a
// budget is also the largest. Lowering a cap is a judgement about a project's
// pace rather than a decision about one item, and `triage.review_rounds_cap` is
// where that is made.
//
// It lives on the item's own counter record rather than beside it, because the
// guards read the counters under that record's lock and must not have to reach a
// second store — and because an override read from somewhere else could be read
// at a different moment from the counters it crosses.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

// TriageCapCleared is the ceiling a cleared budget stands at. Nothing this
// harness counts comes near it, so every guard reading it permits, and the
// arithmetic the counters do against a cap cannot overflow past it: what is
// subtracted from a cap is a count of rounds an item has actually produced.
const TriageCapCleared = math.MaxInt

// The bounds one override is held to. The reason is the whole of what makes an
// override answerable for later, and a bounded line is enough to say it; the
// deciding party is a name rather than an argument.
const (
	MaxTriageOverrideReasonBytes = 4 << 10
	MaxTriageOverrideByBytes     = 256
)

// MaxTriageOverrides bounds how many overrides one item's record carries. It is
// generous rather than tight: each one is an operator sitting down to answer an
// escalation, so an item that has had this many has a problem no further budget
// is going to settle, and the refusal says so.
const MaxTriageOverrides = 16

// ErrTriageOverrideNotARaise is what an override that would not give an item more
// room unwraps to, so a caller can tell "this changes nothing" from a record that
// could not be written without matching on the words of either.
var ErrTriageOverrideNotARaise = errors.New("a triage override clears or raises a cap and never lowers one")

// TriageOverride is one operator decision to cross one of a work item's caps.
//
// Every field is required, which is deliberate and is the point of the record: an
// override nobody is named for is exactly the thing this exists to be an
// alternative to. Who decided and when are what make it attributable, and the
// reason is what makes it answerable — an operator reading an item's budgets six
// weeks later has to be able to see that a cap was crossed, by whom, and on what
// argument.
type TriageOverride struct {
	// Budget is the budget this crosses, named in the vocabulary a refusal names:
	// what an operator reads is "the review round cap", not "the review round
	// counter".
	Budget string `json:"budget"`
	// Cap is the ceiling this puts in force, and Cleared lifts the budget
	// entirely. They are exclusive: a cleared budget has no number, and a record
	// carrying both would leave a reader to guess which one the guards obey.
	Cap     int  `json:"cap,omitempty"`
	Cleared bool `json:"cleared,omitempty"`
	// DecidedBy names the operator who decided this, in whatever form they gave.
	// The harness does not verify it and does not pretend to: what it records is
	// that somebody put their name to crossing a cap, which is what the next
	// reader needs in order to go and ask them.
	DecidedBy string    `json:"decided_by"`
	DecidedAt time.Time `json:"decided_at"`
	Reason    string    `json:"reason"`
}

// TriageOverrideBudgets are the budgets an operator may cross, in the order a
// refusal lists them, so a command that refuses an unknown one names exactly what
// was available.
func TriageOverrideBudgets() []string {
	return []string{TriageReviewRoundBudget, TriageRepairGrantBudget, TriageRerunBudget, TriageMergeRearmBudget}
}

// Limit is the ceiling this override puts in force.
func (o TriageOverride) Limit() int {
	if o.Cleared {
		return TriageCapCleared
	}
	return o.Cap
}

// Validate reports every contract violation in the override at once.
func (o TriageOverride) Validate() error {
	var problems []error
	if err := validTriageBudget(o.Budget); err != nil {
		problems = append(problems, err)
	}
	if o.Cap < 0 {
		problems = append(problems, fmt.Errorf("an override to a cap of %d is not a cap", o.Cap))
	}
	if o.Cleared && o.Cap != 0 {
		problems = append(problems, fmt.Errorf("an override that clears the %s cap states no number, and this one states %d", o.Budget, o.Cap))
	}
	if strings.TrimSpace(o.DecidedBy) == "" {
		problems = append(problems, errors.New("an override names the operator who decided it; one nobody is named for is what this record exists to replace"))
	}
	if len(o.DecidedBy) > MaxTriageOverrideByBytes {
		problems = append(problems, fmt.Errorf("the deciding operator is %d bytes, limit is %d", len(o.DecidedBy), MaxTriageOverrideByBytes))
	}
	if o.DecidedAt.IsZero() {
		problems = append(problems, errors.New("an override records when it was decided"))
	}
	if strings.TrimSpace(o.Reason) == "" {
		problems = append(problems, errors.New("an override records why the cap was crossed, which is what an operator reading the item afterwards has to be able to see"))
	}
	if len(o.Reason) > MaxTriageOverrideReasonBytes {
		problems = append(problems, fmt.Errorf("the override reason is %d bytes, limit is %d", len(o.Reason), MaxTriageOverrideReasonBytes))
	}
	return errors.Join(problems...)
}

// Describe says what one override did, for whoever is reading an item's budgets.
func (o TriageOverride) Describe() string {
	crossed := fmt.Sprintf("raised the %s cap to %d", o.Budget, o.Cap)
	if o.Cleared {
		crossed = fmt.Sprintf("cleared the %s cap", o.Budget)
	}
	return fmt.Sprintf("%s, decided by %s at %s: %s",
		crossed, strings.TrimSpace(o.DecidedBy), o.DecidedAt.UTC().Format(time.RFC3339), strings.TrimSpace(o.Reason))
}

// Overridden is these caps as one item's recorded overrides leave them. It is
// what every guard refuses against and what every view reports, so the cap that
// refuses a decision and the cap an operator is shown are one number.
//
// The last override for a budget is the one in force. Nothing has to sort them,
// because an override is only ever recorded where it raises what already stands:
// the last word on a budget is also the largest.
func (c TriageCaps) Overridden(overrides []TriageOverride) TriageCaps {
	for _, override := range overrides {
		switch override.Budget {
		case TriageReviewRoundBudget:
			c.ReviewRounds = override.Limit()
		case TriageRepairGrantBudget:
			c.RepairGrants = override.Limit()
		case TriageRerunBudget:
			c.Reruns = override.Limit()
		case TriageMergeRearmBudget:
			c.MergeRearms = override.Limit()
		}
	}
	return c
}

// OverrideOf is the override in force on one budget, and whether there is one.
// The last recorded is the one in force, for the reason Overridden gives.
func (c TriageCounters) OverrideOf(budget string) (TriageOverride, bool) {
	for index := len(c.Overrides) - 1; index >= 0; index-- {
		if c.Overrides[index].Budget == budget {
			return c.Overrides[index], true
		}
	}
	return TriageOverride{}, false
}

// Override records the operator's decision to cross one of a work item's caps,
// and reports what the item's record now says.
//
// caps are the configured ceilings, which is what the override is measured
// against together with whatever this item's earlier overrides already put in
// force. That comparison happens under the item's own lock, beside the counters,
// so two operators recording overrides of one item at the same instant cannot
// both raise a cap against the same standing figure.
//
// Nothing is spent here and nothing is carried out. What this changes is what the
// guards will permit next: the development manager can then record the decision
// their escalation was about, and the verb that carries it out finds the record
// it has always required. That separation is the same one every other decision
// here keeps — recording is a role's, causing work is the harness's hand
// afterwards — and it is what keeps an override from being a way to start a run.
func (s *TriageStore) Override(ctx context.Context, workItemID string, override TriageOverride, at time.Time, caps TriageCaps) (TriageCounters, error) {
	if err := caps.Validate(); err != nil {
		return TriageCounters{}, err
	}
	when := at
	if when.IsZero() {
		when = time.Now()
	}
	override.Budget = strings.TrimSpace(override.Budget)
	override.DecidedBy = strings.TrimSpace(override.DecidedBy)
	override.Reason = strings.TrimSpace(override.Reason)
	// When it was decided is this update's own clock rather than a second one the
	// caller supplies, so the override and the record that carries it can never
	// disagree about when it happened.
	override.DecidedAt = when.UTC()
	if err := override.Validate(); err != nil {
		return TriageCounters{}, fmt.Errorf("invalid triage override: %w", err)
	}
	return s.update(ctx, workItemID, when, func(counters *TriageCounters) error {
		if len(counters.Overrides) >= MaxTriageOverrides {
			return fmt.Errorf(
				"%s already carries %d recorded cap override(s), which is the bound: an item that has been given more room this many times has something no further budget settles",
				counters.WorkItemID, len(counters.Overrides))
		}
		standing, err := triageBudgetCap(caps.Overridden(counters.Overrides), override.Budget)
		if err != nil {
			return err
		}
		if override.Limit() <= standing {
			return TriageOverrideError{
				Budget:     override.Budget,
				WorkItemID: counters.WorkItemID,
				Standing:   standing,
				Asked:      override.Limit(),
			}
		}
		counters.Overrides = append(append([]TriageOverride{}, counters.Overrides...), override)
		return nil
	})
}

// TriageOverrideError refuses an override that would give an item no more room
// than it already has. It names what the budget stands at as well as what was
// asked for, because the two most ordinary causes read identically otherwise: an
// override already recorded that did the same thing, and a configured cap that is
// already larger than the number being typed.
type TriageOverrideError struct {
	Budget     string
	WorkItemID string
	Standing   int
	Asked      int
}

func (e TriageOverrideError) Error() string {
	if e.Standing == TriageCapCleared {
		return fmt.Sprintf("the %s cap for %s is already cleared, so this override would give it no more room; nothing was recorded",
			e.Budget, e.WorkItemID)
	}
	return fmt.Sprintf("the %s cap for %s already stands at %d, so an override to %d would give it no more room; nothing was recorded",
		e.Budget, e.WorkItemID, e.Standing, e.Asked)
}

func (e TriageOverrideError) Unwrap() error { return ErrTriageOverrideNotARaise }

// triageBudgetCap is one budget's ceiling under the caps given, and is what
// refuses a budget name nothing bounds. The names are the ones a refusal prints,
// so an operator answering an escalation types the words the refusal used.
func triageBudgetCap(caps TriageCaps, budget string) (int, error) {
	if err := validTriageBudget(budget); err != nil {
		return 0, err
	}
	switch budget {
	case TriageReviewRoundBudget:
		return caps.ReviewRounds, nil
	case TriageRepairGrantBudget:
		return caps.RepairGrants, nil
	case TriageRerunBudget:
		return caps.Reruns, nil
	default:
		return caps.MergeRearms, nil
	}
}

// validTriageBudget refuses a budget name nothing bounds, and says what the names
// are: an operator answering a refusal types the words the refusal used.
func validTriageBudget(budget string) error {
	switch budget {
	case TriageReviewRoundBudget, TriageRepairGrantBudget, TriageRerunBudget, TriageMergeRearmBudget:
		return nil
	default:
		return fmt.Errorf("%q is not a triage budget; the budgets are %s",
			budget, strings.Join(TriageOverrideBudgets(), ", "))
	}
}

// validateTriageOverrides reports every contract violation in one record's
// overrides at once.
//
// The monotonicity check is the one that is about the record rather than about
// any single override: they are only ever appended where they raise what stands,
// so a budget whose overrides do not strictly increase describes a record the only
// thing that writes them could not have written. It matters because these are the
// figures the guards obey — a hand-edited record that quietly lowered a cap would
// refuse decisions with nothing saying why.
func validateTriageOverrides(overrides []TriageOverride) []error {
	var problems []error
	if len(overrides) > MaxTriageOverrides {
		problems = append(problems, fmt.Errorf("%d cap overrides are recorded, which exceeds the bound of %d", len(overrides), MaxTriageOverrides))
	}
	standing := map[string]int{}
	for index, override := range overrides {
		if err := override.Validate(); err != nil {
			problems = append(problems, fmt.Errorf("overrides[%d]: %w", index, err))
			continue
		}
		if previous, recorded := standing[override.Budget]; recorded && override.Limit() <= previous {
			problems = append(problems, fmt.Errorf(
				"overrides[%d]: the %s cap already stood at %d, and an override recorded after it may only raise one",
				index, override.Budget, previous))
		}
		standing[override.Budget] = override.Limit()
	}
	return problems
}
