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
// Three things make a finding, and two of them are read from the report pile
// here. A report filed at critical severity is one until somebody handles it:
// critical is the severity that means action, in the reporting contract's own
// words. A handling that says the report needs the operator is one until a
// later handling of the same report says otherwise. The third is the brake
// tripping, which is read from the intake hold beside the switches and is named
// on the attention line there, as the held intake it is.

import (
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/report"
)

// OperatorAction is one finding only the operator can act on: what is needed,
// where it is recorded, and since when.
type OperatorAction struct {
	// Key names the finding durably, so a surface that says each one once can
	// remember having said it. A report makes one finding however many times it
	// is handled, so the key is the report's.
	Key string `json:"key"`
	// ReportID is the report the finding came from, and WorkItemID the item that
	// report was about where it was about one.
	ReportID   string `json:"report_id"`
	WorkItemID string `json:"work_item_id,omitempty"`
	// Needs is what the operator has to do, in the words of whoever found it: the
	// handling's reason, or the critical report's own message.
	Needs string `json:"needs"`
	// RecordedIn says where the finding is recorded, so the operator can go and
	// read the whole of it: the report and its handling, or the report alone.
	RecordedIn string `json:"recorded_in"`
	// FoundBy is who found it and how, worded once here.
	FoundBy string    `json:"found_by"`
	Since   time.Time `json:"since"`
}

// operatorActionKey names a finding by the report it came from.
func operatorActionKey(reportID string) string { return "report:" + reportID }

// OperatorActions is every finding that needs the operator, read from the pile
// and what became of it, oldest first. It is the one derivation; nothing else
// decides what needs him.
func OperatorActions(reports []report.Report, handlings []report.Handling) []OperatorAction {
	handled := report.Handled(handlings)
	var actions []OperatorAction
	for _, reported := range report.ByFiling(reports) {
		handling, done := handled[reported.ID]
		switch {
		case done && handling.NeedsOperator:
			actions = append(actions, OperatorAction{
				Key:        operatorActionKey(reported.ID),
				ReportID:   reported.ID,
				WorkItemID: reported.WorkItemID,
				Needs:      strings.Join(strings.Fields(handling.Reason), " "),
				RecordedIn: fmt.Sprintf("the handling of %s recorded in %s, over the %s's report from %s",
					reported.ID, handling.RunID, reported.Role.Title(), reported.RunID),
				FoundBy: fmt.Sprintf("the %s, handling the report", handling.Role.Title()),
				Since:   handling.RecordedAt,
			})
		case !done && reported.Severity == report.SeverityCritical:
			actions = append(actions, OperatorAction{
				Key:        operatorActionKey(reported.ID),
				ReportID:   reported.ID,
				WorkItemID: reported.WorkItemID,
				Needs:      strings.Join(strings.Fields(reported.Message), " "),
				RecordedIn: fmt.Sprintf("%s, the %s's report from %s", reported.ID, reported.Role.Title(), reported.RunID),
				FoundBy:    fmt.Sprintf("the %s, in a critical report", reported.Role.Title()),
				Since:      reported.RecordedAt,
			})
		}
	}
	return actions
}

// operatorActionWhose is whose move a finding is, and what ends it. It is one
// clause for every finding, because every finding here is the same shape: a
// change only a person can make, recorded until somebody records it made.
const operatorActionWhose = "the operator's — only a person can make this change; a later handling of the report records it done"

// Attention is the finding as the attention line names it. It is named — never
// counted into a remainder — because a finding that reached the line only as
// "and 3 things not named here" is the month-long silence this class exists to
// end.
func (a OperatorAction) Attention() Attention {
	what := fmt.Sprintf("%s needs your hand: %s (found by %s; recorded in %s",
		a.ReportID, singleLine(a.Needs, maxRefusalBytes), a.FoundBy, a.RecordedIn)
	if strings.TrimSpace(a.WorkItemID) != "" {
		what += ", about " + a.WorkItemID
	}
	return Attention{What: what + ")", Whose: operatorActionWhose, Named: true}
}

// readOperatorActions is the findings as one reading of the pile has them. A
// pile that could not be read lists none, and the line says so through the
// pile's own problem, which the attention line already carries: a pile that
// cannot be read is not a pile with nothing in it.
func readOperatorActions(reports []report.Report, handlings []report.Handling, problem string) []Attention {
	if problem != "" {
		return nil
	}
	actions := OperatorActions(reports, handlings)
	attention := make([]Attention, 0, len(actions))
	for _, action := range actions {
		attention = append(attention, action.Attention())
	}
	return attention
}
