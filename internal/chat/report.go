package chat

// The product manager reports what it notices the same way every other role
// does, and the operator reads the whole collected pile from here. That is the
// point of putting it in this conversation: it is the operator's normal path,
// so a report from a developer three runs ago is somewhere they already are
// rather than behind a tool they have to remember to run.

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// maxRenderedReports bounds how many collected reports one listing shows. The
// most recent are kept rather than the oldest: what an operator needs from the
// pile is what has come in lately, and the count says how much of it they are
// not looking at.
const maxRenderedReports = 20

// Reports is the collected pile: where this conversation records what the
// product manager reported, and where it reads what every role has reported.
// It is satisfied by runstate.ReportStore.
type Reports interface {
	Append(reported report.Report) error
	List() ([]report.Report, error)
}

// ReportError reports a report block the harness could not read. Like
// ProposalError it is not a broken conversation, and unlike either of the
// others it is not even a failed action: the turn happened, the answer is real,
// and nothing about what the product manager did depended on the block. What is
// lost is what it was trying to tell the operator, which is exactly why it is
// said out loud rather than swallowed.
type ReportError struct {
	Err error
}

func (e *ReportError) Error() string {
	return "the product manager reported something the harness cannot read: " + e.Err.Error()
}

func (e *ReportError) Unwrap() error { return e.Err }

// errNoReports reports a conversation with nowhere to collect. Such a
// conversation still discusses the product; it just cannot keep or show what
// anybody reported, and says so rather than showing an empty pile that looks
// like nothing has been reported.
var errNoReports = errors.New("no report collection is wired to this conversation, so nothing can be reported or read back")

// recordReports collects what the product manager reported. It never fails the
// turn: a report is not a blocker for any other role and it is not one here
// either, so what could not be collected is described to the operator and the
// conversation carries on.
func (s *Session) recordReports(entries []report.Entry) ([]report.Report, string) {
	if len(entries) == 0 {
		return nil, ""
	}
	if s.options.Reports == nil {
		return nil, errNoReports.Error()
	}
	collected, err := report.Collect(entries, report.Attribution{
		Role:  s.state.Role,
		Agent: s.options.Agent,
		// A conversation has no run and no assigned work item. Its own identifier
		// is what a report leads back to, exactly as a run identifier is for a
		// role the pipeline executes.
		RunID:        s.state.ConversationID,
		ProductID:    s.options.ProductID,
		RepositoryID: s.options.RepositoryID,
	}, s.options.clock().Now())
	if err != nil {
		return nil, singleLine(err.Error(), maxTrackerFailureBytes)
	}
	recorded := make([]report.Report, 0, len(collected))
	var problems []string
	for _, reported := range collected {
		if err := s.options.Reports.Append(reported); err != nil {
			problems = append(problems, singleLine(err.Error(), maxTrackerFailureBytes))
			continue
		}
		recorded = append(recorded, reported)
		if err := s.emit(execution.EventReportRecorded, reported); err != nil {
			// The report is already collected, so this is a gap in the
			// conversation's own log rather than a lost report, and it is said as
			// that.
			problems = append(problems, singleLine(err.Error(), maxTrackerFailureBytes))
		}
	}
	return recorded, strings.Join(problems, "; ")
}

// noteUnreadableReport records that a report block arrived and could not be
// read. Nothing about the turn changes; without this the report would leave no
// trace anywhere.
func (s *Session) noteUnreadableReport(cause error) string {
	problem := (&ReportError{Err: cause}).Error()
	if err := s.emit(execution.EventReportUnreadable, map[string]any{
		"turn":    s.state.Turns,
		"problem": problem,
	}); err != nil {
		return problem + "; recording that also failed: " + singleLine(err.Error(), maxTrackerFailureBytes)
	}
	return problem
}

// ReadReports returns everything every role has reported, newest last. It is
// read-only: reading the pile is not deciding anything about it, and nothing an
// operator does here retires a report.
func (s *Session) ReadReports() ([]report.Report, error) {
	if s.options.Reports == nil {
		return nil, errNoReports
	}
	reports, err := s.options.Reports.List()
	if err != nil {
		return nil, fmt.Errorf("read the collected reports: %w", err)
	}
	return reports, nil
}

// renderCollectedReports describes the pile for an operator. An empty pile is
// stated rather than printed as nothing at all, because "nobody has reported
// anything" is an answer and a blank space is not.
func renderCollectedReports(reports []report.Report) string {
	if len(reports) == 0 {
		return "reports: nothing has been reported.\n"
	}
	var rendered strings.Builder
	listed := reports
	if len(listed) > maxRenderedReports {
		listed = listed[len(listed)-maxRenderedReports:]
	}
	fmt.Fprintf(&rendered, "reports (%d collected):\n", len(reports))
	if len(reports) > len(listed) {
		fmt.Fprintf(&rendered, "  %d earlier report(s) are not listed here.\n", len(reports)-len(listed))
	}
	for _, reported := range listed {
		rendered.WriteString(reported.Render())
	}
	return rendered.String()
}

// reportFiled tells the operator what the product manager reported while it was
// answering, and what happened to a report that could not be kept. It prints
// nothing when there was nothing to report, which is the ordinary case.
func reportFiled(out io.Writer, reply Reply) {
	if len(reply.Reports) == 0 && reply.ReportProblem == "" {
		return
	}
	if len(reply.Reports) > 0 {
		fmt.Fprintf(out, "The product manager reported %d thing(s) for you:\n", len(reply.Reports))
		for _, reported := range reply.Reports {
			fmt.Fprint(out, reported.Render())
		}
	}
	if reply.ReportProblem != "" {
		fmt.Fprintf(out, "a report was not collected: %s\n", reply.ReportProblem)
	}
	fmt.Fprintln(out)
}
