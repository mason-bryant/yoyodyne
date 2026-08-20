package chat

// What the development manager decides about work that has stopped moving.
//
// The docket is delivered into this role's conversation because deciding is
// this role's, and until now that was the whole of the loop: an entry arrived,
// a decision was reasoned out in prose, and the prose went wherever the
// conversation went. Nothing carried the decision to the item it was about, so
// the next reader of a stopped run — a later conversation, a later operator —
// found the same evidence and none of the reasoning, and decided it again.
//
// So a decision is a recorded act rather than a paragraph. It names the
// stoppage it settles, it lands on the work item where anybody who reads the
// item finds it, and the three decisions that buy another attempt spend the
// item's durable triage budget as they are recorded. That last part is what
// makes the guards real: a repair grant, a re-run, and a re-arm each go through
// the same gate the counters already enforce, so an item nothing was ever going
// to stop is stopped by the cap rather than by whoever happens to be reading.
//
// Escalation is the one decision that reaches the operator, and it is
// deliberately more than prose: a durable blocker on the item, so the item
// itself says it is waiting on a person, and a report at warning severity or
// above, so it reaches the pile the operator reads. A conversation that only
// said "somebody should look at this" is how the four hand surgeries this
// workflow exists to replace were found in the first place — late, and by
// accident.
//
// What the harness does not do is carry the decision out. Nothing here starts a
// run, hands a developer a grant, or asks a forge for anything: causing work is
// the harness's own hand on the operator's instruction, and this is a role
// deciding. The record and the budget are what a later hand acts on — for a
// re-run that hand is `yoyo triage rerun`, which reads the intake hold, proves
// the stoppage is over, and records this decision as why the fresh run exists.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The decisions triage may record. They are the two decision trees the
// development manager works through — one for a run that stopped, one for a
// publication that did not finish — written as a closed vocabulary, because the
// harness acts on three of them and cannot act on prose.
const (
	// decisionRepair hands the item another bounded go at the change it already
	// has, on the branch and worktree the stopped run preserved.
	decisionRepair = "repair"
	// decisionRerun runs the item again from the start, which is what a correct
	// change whose ground moved needs.
	decisionRerun = "rerun"
	// decisionRescope splits what was out of scope into a child of the item. The
	// parent's own criteria are not narrowed here: they are the product
	// manager's, and what this records is the decision plus the argument for
	// narrowing them.
	decisionRescope = "rescope"
	// decisionRearm repeats an authorized merge request the forge dropped for a
	// transient cause.
	decisionRearm = "rearm"
	// decisionWait is the decision that nothing is to be done yet: the forge
	// still has the merge, and waiting is what it needs. It is recorded rather
	// than left unsaid so the next reader knows somebody looked.
	decisionWait = "wait"
	// decisionEscalate hands the entry to the operator, which is the only
	// decision that asks a person for anything.
	decisionEscalate = "escalate"
)

// triageDecisions lists the vocabulary in the order the contract states it, so a
// refusal names exactly what was available.
var triageDecisions = []string{
	decisionRepair, decisionRerun, decisionRescope, decisionRearm, decisionWait, decisionEscalate,
}

// triageVerbs is what each decision records about itself on the work item. The
// item's notes are read by people and by later conversations rather than by
// this package, so what lands there is a sentence rather than the vocabulary
// word that produced it.
var triageVerbs = map[string]string{
	decisionRepair:   "Triaged: handed back for one bounded repair of the change it already has",
	decisionRerun:    "Triaged: to be run again from the start",
	decisionRescope:  "Triaged: re-scoped, with what was out of scope split out",
	decisionRearm:    "Triaged: its dropped merge to be re-armed once",
	decisionWait:     "Triaged: waiting, because the forge still has it",
	decisionEscalate: "Escalated to the operator by triage",
}

// TriageBudgets is what one work item has already been given and may still be
// given. It is the durable per-item record every triage action goes through,
// satisfied by a caller that supplies the configured caps alongside the store:
// what an item may spend is a decision an operator wrote down, and a
// conversation is not the place that invents one.
//
// A conversation without it can still decide anything that spends nothing.
// What it cannot do is grant, re-run, or re-arm, because a budget that cannot be
// read must never be spent through as though it were empty.
type TriageBudgets interface {
	// GrantRepair records a repair grant and reports what it came to, truncated
	// to the review rounds the item's cap still has room for.
	GrantRepair(ctx context.Context, workItemID string) (runstate.RepairGrant, error)
	// RecordRerun records that triage caused this item to be run again.
	RecordRerun(ctx context.Context, workItemID string) (runstate.TriageCounters, error)
	// RecordMergeRearm records that triage re-armed a merge the forge dropped.
	RecordMergeRearm(ctx context.Context, workItemID string) (runstate.TriageCounters, error)
}

// EscalationError reports an escalation that carried no report. It is the
// harness refusing rather than the provider failing, exactly as an authority
// refusal is: nothing in the block was carried out, the item was not blocked,
// and the prose the role wrote is still the operator's to read.
type EscalationError struct {
	WorkItemID string
}

func (e *EscalationError) Error() string {
	return fmt.Sprintf(
		"the development manager escalated %s without reporting it at %q severity or above; nothing was carried out, because an escalation the operator never sees is not an escalation",
		e.WorkItemID, report.SeverityWarning)
}

// triageProblems checks a triage decision as far as one action can be checked:
// it names a decision from the vocabulary, and it names the run whose stoppage
// it settles. Whether that run is on the docket needs the docket rather than the
// action, and is deliberately not asked: the entry the decision is about may
// have been cut from a bounded listing, and refusing a decision for that would
// refuse exactly the oldest stoppages nobody has got to yet.
func (a TrackerAction) triageProblems() []error {
	var problems []error
	switch decision := strings.TrimSpace(a.Decision); {
	case decision == "":
		problems = append(problems, fmt.Errorf("triage requires \"decision\", one of %s", strings.Join(triageDecisions, ", ")))
	case triageVerbs[decision] == "":
		problems = append(problems, fmt.Errorf("triage decision %q is not a decision; the decisions are %s",
			decision, strings.Join(triageDecisions, ", ")))
	}
	switch run := strings.TrimSpace(a.Run); {
	case run == "":
		problems = append(problems, errors.New("triage requires \"run\", the run the docket entry names"))
	case !runstate.ValidRunID(run):
		problems = append(problems, fmt.Errorf("triage run %q is not a run identifier; a docket entry names the run it is about", run))
	}
	return problems
}

// escalates reports a decision that asks a person for something, which is the
// one decision the harness holds to a further condition than the action itself
// carries.
func (a TrackerAction) escalates() bool {
	return a.Action == actionTriage && strings.TrimSpace(a.Decision) == decisionEscalate
}

// refuseUnreportedEscalation refuses an escalation the operator would never see.
// An escalation is a durable blocker and a report, and the report half cannot be
// checked where the action is validated, because it is in a different block of
// the same reply.
//
// The severity floor is warning rather than note for the reason the report
// vocabulary draws that line: a note asks for nothing, and an escalation asks
// for a person.
func refuseUnreportedEscalation(parsed parsedReply) error {
	// One escalating reply may cover several docket entries, and each blocked
	// item needs its own account: a single report satisfying every escalation
	// would leave the rest blocked with nothing reaching the operator about
	// them. So the count of warning-or-above reports must cover the count of
	// escalations, and the refusal names every item that would go unaccounted.
	var escalated []string
	for _, action := range parsed.Actions {
		if action.escalates() {
			escalated = append(escalated, strings.TrimSpace(action.ID))
		}
	}
	if len(escalated) == 0 {
		return nil
	}
	reported := 0
	for _, entry := range parsed.Reports {
		if entry.Severity == report.SeverityWarning || entry.Severity == report.SeverityCritical {
			reported++
		}
	}
	if reported >= len(escalated) {
		return nil
	}
	return &EscalationError{WorkItemID: strings.Join(escalated[reported:], ", ")}
}

// carryOutTriage records one triage decision. The budget is spent before the
// decision is written down, which is the order the durable counters are built
// for: a process that dies between the two has spent an attempt nobody took,
// rather than recorded a decision nothing bounds.
//
// A refusal from the budget is the gate doing its job rather than a failure of
// the conversation: the development manager is told which cap refused it and
// what it has left, which is exactly the evidence for escalating instead.
func (s *Session) carryOutTriage(ctx context.Context, outcome *TrackerOutcome) {
	action := outcome.Action
	id := strings.TrimSpace(action.ID)
	decision := strings.TrimSpace(action.Decision)
	spent, err := s.spendTriageBudget(ctx, id, decision)
	if err != nil {
		outcome.fail(err)
		return
	}
	note := s.trackerProvenance(triageVerbs[decision]+", on the stopped work of run "+strings.TrimSpace(action.Run), action.Reason)
	if decision == decisionEscalate {
		// An escalation is recorded as a blocker rather than as a note, because
		// the item itself has to say it is waiting on a person: a note leaves the
		// item looking like work in flight, which is the state this whole workflow
		// exists to stop somebody discovering by accident.
		if _, err := s.options.Tracker.Block(ctx, id, note); err != nil {
			outcome.fail(err)
			return
		}
		outcome.applied("escalated %s to the operator and blocked it, on the stopped work of run %s",
			id, strings.TrimSpace(action.Run))
		return
	}
	if _, err := s.options.Tracker.Update(ctx, id, beads.WorkItemChange{AppendNotes: note}); err != nil {
		outcome.fail(err)
		return
	}
	outcome.applied("triaged %s as %q, on the stopped work of run %s%s",
		id, decision, strings.TrimSpace(action.Run), spent)
}

// spendTriageBudget spends what the decision costs and says what it came to, or
// says nothing for a decision that costs nothing. Three of the six buy another
// attempt at work that already failed once, and those are the three the durable
// budget bounds; re-scoping, waiting, and escalating buy no attempt at all and
// are never refused for budget.
func (s *Session) spendTriageBudget(ctx context.Context, workItemID, decision string) (string, error) {
	switch decision {
	case decisionRepair, decisionRerun, decisionRearm:
	default:
		return "", nil
	}
	if s.options.Triage == nil {
		return "", errors.New("no triage budget is wired to this conversation, so a repair, a re-run, or a re-arm cannot be bounded and was not recorded")
	}
	switch decision {
	case decisionRepair:
		granted, err := s.options.Triage.GrantRepair(ctx, workItemID)
		if err != nil {
			return "", err
		}
		if granted.Truncated {
			return fmt.Sprintf("; the grant was cut from %d round(s) to the %d the cap still had room for",
				granted.Requested, granted.Rounds), nil
		}
		return fmt.Sprintf("; it is granted %d further review round(s)", granted.Rounds), nil
	case decisionRerun:
		counters, err := s.options.Triage.RecordRerun(ctx, workItemID)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("; %d re-run(s) of it are now recorded", counters.Reruns), nil
	default:
		counters, err := s.options.Triage.RecordMergeRearm(ctx, workItemID)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("; %d merge re-arm(s) of it are now recorded", counters.MergeRearms), nil
	}
}
