package readmodel

// What needs the operator's own hand, derived once from the records that say so.
//
// A finding only the operator can act on used to reach him by accident. Six
// developer reports asking for a change a person had to make sat in the pile
// from 2026-08-17 until a sweep reached them on 2026-09-14, and the finding
// that sweep produced landed on the product manager's own checklist, which he
// sees when he asks. He asked why it took a month.
//
// So the class is derived here, from the durable records and nowhere else, and
// every surface reads this rather than deciding for itself what needs him: the
// attention line names each finding, and the channel says each one to him once.
// Two of those surfaces working it out separately is the disagreement one read
// model exists to prevent.
//
// Four things make a finding. Two are read from the report pile: a report
// filed at critical severity is one until somebody handles it — critical is the
// severity that means action, in the reporting contract's own words — and a
// handling that says the report needs the operator is one until a later
// handling of the same report says otherwise. The third is a run stopping on a
// condition only a person can clear, which the harness records as the
// development manager's escalation of that stoppage to the operator: her
// decision is the one typed record that says a stopped run needs a person
// rather than a repair, a re-run, or a wait, and it stands while it is the
// decision on the item's latest stopped run. The fourth is the brake tripping,
// which is read from the intake hold beside the switches and is named on the
// attention line there, as the held intake it is.

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// OperatorAction is one finding only the operator can act on: what is needed,
// where it is recorded, and since when.
type OperatorAction struct {
	// Key names the finding durably, so a surface that says each one once can
	// remember having said it. A report makes one finding however many times it
	// is handled, so the key is the report's; a stoppage makes one however many
	// times it is decided, so the key is the run's.
	Key string `json:"key"`
	// Subject is what the finding is named by where it is listed: the report,
	// or the work item whose run stopped.
	Subject string `json:"subject"`
	// ReportID is the report the finding came from, and RunID the run whose
	// stoppage it came from; each finding has one of the two. WorkItemID is the
	// item either was about, where it was about one.
	ReportID   string `json:"report_id,omitempty"`
	RunID      string `json:"run_id,omitempty"`
	WorkItemID string `json:"work_item_id,omitempty"`
	// Needs is what the operator has to do, in the words of whoever found it: the
	// handling's reason, the critical report's own message, or the development
	// manager's reason for escalating.
	Needs string `json:"needs"`
	// RecordedIn says where the finding is recorded, so the operator can go and
	// read the whole of it.
	RecordedIn string `json:"recorded_in"`
	// FoundBy is who found it and how, and Ends is what ends it, both worded once
	// here: every surface that names the finding says the same thing about who
	// found it and what it is waiting for.
	FoundBy string    `json:"found_by"`
	Ends    string    `json:"ends"`
	Since   time.Time `json:"since"`
}

// operatorActionKey names a finding by the report it came from, and
// escalationKey by the run whose stoppage was escalated.
func operatorActionKey(reportID string) string { return "report:" + reportID }

func escalationKey(runID string) string { return "run:" + runID }

// The endings, one per shape of finding. A report-derived finding ends with a
// handling; an escalated stoppage ends when the development manager decides the
// run again, or the item is run again or leaves the backlog.
const (
	reportFindingEnds     = "a later handling of the report records it done"
	escalationFindingEnds = "a later triage decision on the run, or the item being run again or retired, ends it"
)

// OperatorActions is every finding that needs the operator, read from the pile
// and what became of it, oldest first. It is the one derivation of the two
// report-derived shapes; EscalatedOperatorActions is the third, and readOperatorActions
// puts them together for the attention line.
func OperatorActions(reports []report.Report, handlings []report.Handling) []OperatorAction {
	handled := report.Handled(handlings)
	var actions []OperatorAction
	for _, reported := range report.ByFiling(reports) {
		handling, done := handled[reported.ID]
		switch {
		case done && handling.NeedsOperator:
			actions = append(actions, OperatorAction{
				Key:        operatorActionKey(reported.ID),
				Subject:    reported.ID,
				ReportID:   reported.ID,
				WorkItemID: reported.WorkItemID,
				Needs:      strings.Join(strings.Fields(handling.Reason), " "),
				RecordedIn: fmt.Sprintf("the handling of %s recorded in %s, over the %s's report from %s",
					reported.ID, handling.RunID, reported.Role.Title(), reported.RunID),
				FoundBy: fmt.Sprintf("the %s, handling the report", handling.Role.Title()),
				Ends:    reportFindingEnds,
				Since:   handling.RecordedAt,
			})
		case !done && reported.Severity == report.SeverityCritical:
			actions = append(actions, OperatorAction{
				Key:        operatorActionKey(reported.ID),
				Subject:    reported.ID,
				ReportID:   reported.ID,
				WorkItemID: reported.WorkItemID,
				Needs:      strings.Join(strings.Fields(reported.Message), " "),
				RecordedIn: fmt.Sprintf("%s, the %s's report from %s", reported.ID, reported.Role.Title(), reported.RunID),
				FoundBy:    fmt.Sprintf("the %s, in a critical report", reported.Role.Title()),
				Ends:       reportFindingEnds,
				Since:      reported.RecordedAt,
			})
		}
	}
	return actions
}

// EscalatedOperatorActions is every stopped run the development manager
// escalated to the operator, as findings, oldest first: a run stopping on a
// condition only a person can clear is recorded as her escalation of it, since
// she is the role that judges a stoppage and escalating is the one decision
// that hands it to a person.
//
// A finding stands while the escalation is the decision standing on the item's
// latest run that stopped with a durable blocker. A later decision on that run
// supersedes it, a later run makes it history, and an item that has left the
// backlog — retired, or closed after the operator did what was asked — ends it
// where the caller can say which items are admitted; a caller that cannot
// passes nil and reads every escalation as standing, which is the direction
// that says a finding once rather than never.
//
// A triage record that cannot be read costs that item's finding and is said in
// the problem rather than read as no escalation: a stoppage the operator was
// handed must not vanish from the line because one file would not open. A
// reading wired without decisions at all names none and says nothing, which is
// the answer the held-work derivation gives the same absence: every surface the
// harness builds wires them, and a fixture that does not is asking about
// something else.
func EscalatedOperatorActions(runs []runstate.State, decisions Decisions, admitted func(workItemID string) bool) ([]OperatorAction, string) {
	if decisions == nil {
		return nil, ""
	}
	stopped := latestPerItem(runs, func(run runstate.State) bool {
		return run.WorkItemID != "" && run.Status.Terminal() && strings.TrimSpace(run.Blocker) != ""
	})
	var actions []OperatorAction
	var problems []string
	for workItemID, run := range stopped {
		if admitted != nil && !admitted(workItemID) {
			continue
		}
		counters, err := decisions.Counters(workItemID)
		if err != nil {
			problems = append(problems, fmt.Sprintf("what triage decided about %s could not be read, so an escalation of it cannot be named: %v", workItemID, err))
			continue
		}
		decision, decided := counters.DecisionOf(run.RunID)
		if !decided || decision.Decision != runstate.TriageDecisionEscalate {
			continue
		}
		actions = append(actions, OperatorAction{
			Key:        escalationKey(run.RunID),
			Subject:    workItemID,
			RunID:      run.RunID,
			WorkItemID: workItemID,
			Needs:      strings.Join(strings.Fields(decision.Reason), " "),
			RecordedIn: fmt.Sprintf("the development manager's escalation of run %s, recorded in %s at turn %d, and the blocker on %s",
				run.RunID, decision.Conversation, decision.Turn, workItemID),
			FoundBy: "the development manager, escalating the stopped run to the operator",
			Ends:    escalationFindingEnds,
			Since:   decision.DecidedAt,
		})
	}
	sortOperatorActions(actions)
	return actions, strings.Join(problems, "; ")
}

// sortOperatorActions orders findings oldest first, the key breaking a tie, so
// a reading is the same twice over rather than whatever order a map handed back.
func sortOperatorActions(actions []OperatorAction) {
	sort.SliceStable(actions, func(i, j int) bool {
		if !actions[i].Since.Equal(actions[j].Since) {
			return actions[i].Since.Before(actions[j].Since)
		}
		return actions[i].Key < actions[j].Key
	})
}

// Whose is whose move a finding is and what ends it, in the one clause every
// surface says: the attention line closes on it, and the message that reaches
// the operator carries it as the move that follows.
func (a OperatorAction) Whose() string {
	return "the operator's — only a person can act on this; " + a.Ends
}

// Attention is the finding as the attention line names it. It is named — never
// counted into a remainder — because a finding that reached the line only as
// "and 3 things not named here" is the month-long silence this class exists to
// end.
func (a OperatorAction) Attention() Attention {
	what := fmt.Sprintf("%s needs your hand: %s (found by %s; recorded in %s",
		a.Subject, singleLine(a.Needs, maxRefusalBytes), a.FoundBy, a.RecordedIn)
	if item := strings.TrimSpace(a.WorkItemID); item != "" && item != a.Subject {
		what += ", about " + item
	}
	return Attention{What: what + ")", Whose: a.Whose(), Named: true}
}

// readOperatorActions is every finding as one reading has it: the report-derived
// ones from the pile, and the escalated stoppages from the runs and what triage
// decided about them, restricted to items the queue still admits. A pile that
// could not be read lists none of the first kind, and the line says so through
// the pile's own problem, which the attention line already carries; a triage
// record that could not be read is said here.
func readOperatorActions(reports []report.Report, handlings []report.Handling, pileProblem string, sources Sources, admitted func(string) bool) ([]Attention, string) {
	var actions []OperatorAction
	if pileProblem == "" {
		actions = OperatorActions(reports, handlings)
	}
	var problem string
	if sources.Stoppages == nil || sources.Decisions == nil {
		// Read as the held-work derivation reads the same absence: nothing named,
		// nothing said. Every surface the harness builds wires both.
	} else if runs, err := sources.Stoppages.Recorded(); err != nil {
		problem = fmt.Sprintf("the recorded runs could not be read, so no escalated stoppage can be named: %v", err)
	} else {
		escalated, escalatedProblem := EscalatedOperatorActions(runs, sources.Decisions, admitted)
		actions = append(actions, escalated...)
		problem = escalatedProblem
	}
	attention := make([]Attention, 0, len(actions))
	for _, action := range actions {
		attention = append(attention, action.Attention())
	}
	return attention, problem
}
