package chat

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// maxSurveyItems bounds how many items of one kind a survey lists. What an
// operator needs from a survey is what to decide about next, not an export of
// the tracker, so the list is cut while the count stays exact.
const maxSurveyItems = 15

// maxSurveyTitleBytes keeps one tracker-supplied title to one line of a survey.
const maxSurveyTitleBytes = 120

// defaultStopGrace bounds how long stopping waits for a cancelled run to give
// up. It is generous because a cancelled run still has to kill its provider and
// check processes, and it is bounded because an operator who asked to stop must
// get an answer either way rather than a conversation that hangs.
const defaultStopGrace = 2 * time.Minute

// Work is the harness's own hand in a conversation: what an operator sees and
// steers development with without leaving it. Every method here runs because
// the operator asked for it in their own words. The product manager cannot
// reach any of it, exactly as it cannot reach the Tracker, so starting,
// redirecting, and stopping work stay the operator's decisions and the harness's
// actions.
type Work interface {
	// Survey reports development as the harness sees it now: the runs it has in
	// flight and the tracker's own view of what is blocked, claimed, available,
	// and done.
	Survey(ctx context.Context) (Survey, error)
	// Run carries one work item through the harness pipeline: worktree,
	// developer, checks, review, and integration under the project's configured
	// policy. It returns what the run reported even when it failed, because a
	// failed run's branch, worktree, and findings are what the operator acts on.
	Run(ctx context.Context, workItemID string) (RunReport, error)
	// Direct records durable operator direction on a work item, where the next
	// attempt at it reads it. It never changes the item's status: redirecting
	// work says what to do differently, it does not decide the work is done.
	Direct(ctx context.Context, workItemID, note string) error
	// Settle settles the runs nothing is working on any more, the way an
	// interrupted process's runs are settled: artifacts preserved, integrated
	// work finished, and anything a person has to decide recorded as a blocker.
	Settle(ctx context.Context) ([]Settlement, error)
}

// WorkItemSummary is one tracked item as a conversation reports it.
type WorkItemSummary struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	Priority int    `json:"priority"`
}

// RunSnapshot is one run the harness has recorded and not finished. It is read
// from durable run state rather than from this process, so a run another
// process is working on is just as visible as one started here.
type RunSnapshot struct {
	RunID      string    `json:"run_id"`
	WorkItemID string    `json:"work_item_id"`
	Status     string    `json:"status"`
	Phase      string    `json:"phase,omitempty"`
	Branch     string    `json:"branch,omitempty"`
	StartedAt  time.Time `json:"started_at"`
	// Detail names anything about the run that its status does not say, such as
	// the usage limit it is waiting out.
	Detail string `json:"detail,omitempty"`
}

// Survey is development as the harness sees it now.
type Survey struct {
	InFlight  []RunSnapshot     `json:"in_flight,omitempty"`
	Claimed   []WorkItemSummary `json:"claimed,omitempty"`
	Blocked   []WorkItemSummary `json:"blocked,omitempty"`
	Available []WorkItemSummary `json:"available,omitempty"`
	Completed []WorkItemSummary `json:"completed,omitempty"`
	// Unavailable names the parts of the survey that could not be read. A part
	// that could not be read is reported as unknown rather than rendered as
	// empty, and it never hides the parts that were read.
	Unavailable []Unread `json:"unavailable,omitempty"`
}

// Unread is one part of a survey that could not be read, named by the group it
// would have filled so the report can say "unknown" exactly where it belongs.
type Unread struct {
	Group  string `json:"group"`
	Reason string `json:"reason"`
}

// RunReport is what one run of the harness pipeline produced, in the terms an
// operator steers by. It is a projection rather than the pipeline's own
// outcome, so a conversation stays independent of how a run is executed.
type RunReport struct {
	RunID        string `json:"run_id,omitempty"`
	WorkItemID   string `json:"work_item_id,omitempty"`
	Status       string `json:"status,omitempty"`
	Branch       string `json:"branch,omitempty"`
	WorktreePath string `json:"worktree_path,omitempty"`
	// Integrated, TargetBranch, and Commit describe a promotion that actually
	// happened. A run that succeeded without integrating says so instead.
	Integrated     bool   `json:"integrated,omitempty"`
	TargetBranch   string `json:"target_branch,omitempty"`
	Commit         string `json:"commit,omitempty"`
	WorkItemClosed bool   `json:"work_item_closed,omitempty"`
	RepairAttempts int    `json:"repair_attempts,omitempty"`
	// Blocked reports that the run recorded a durable blocker on its item, and
	// Paused reports a run that stopped short of finishing and is owed a
	// continuation rather than having failed.
	Blocked            bool       `json:"blocked,omitempty"`
	Paused             bool       `json:"paused,omitempty"`
	UsageLimitKind     string     `json:"usage_limit_kind,omitempty"`
	UsageLimitResetsAt *time.Time `json:"usage_limit_resets_at,omitempty"`
	Failure            string     `json:"failure,omitempty"`
}

// Settlement is what settling did with one run.
type Settlement struct {
	RunID      string `json:"run_id"`
	WorkItemID string `json:"work_item_id"`
	Action     string `json:"action"`
	Detail     string `json:"detail,omitempty"`
	Failure    string `json:"failure,omitempty"`
}

// FinishedRun is a run this conversation started, collected after it ended.
// Err is whatever the harness reported; the report beside it still describes
// what the run left behind, because a failed run is not an empty one.
type FinishedRun struct {
	WorkItemID string
	Report     RunReport
	Err        error
}

// Stopped is what stopping a run achieved. Finished reports whether the run
// actually gave up before the grace ran out: a run still winding down is
// reported as such rather than described as stopped.
type Stopped struct {
	WorkItemID string
	Finished   bool
	// AlreadyFinished reports a run that had reached its own conclusion before
	// the cancellation reached it, whether or not that conclusion integrated
	// anything. Nothing was stopped, and nothing pretends it was.
	AlreadyFinished bool
	Report          RunReport
	RunErr          error
	Settlements     []Settlement
}

// Render describes the survey for an operator. Every group is named even when
// it is empty: "blocked: none" is an answer, and a missing heading is not.
func (s Survey) Render() string {
	var rendered strings.Builder
	if reason, unread := s.unread(inFlightGroup); unread {
		rendered.WriteString(renderUnread(inFlightGroup, reason))
	} else {
		rendered.WriteString(renderRunSnapshots(s.InFlight))
	}
	for _, group := range []struct {
		label string
		items []WorkItemSummary
	}{
		{"claimed", s.Claimed},
		{"blocked", s.Blocked},
		{"available", s.Available},
		{"completed", s.Completed},
	} {
		if reason, unread := s.unread(group.label); unread {
			rendered.WriteString(renderUnread(group.label, reason))
			continue
		}
		rendered.WriteString(renderWorkItemGroup(group.label, group.items))
	}
	// Anything unread that does not belong to a group above is still reported;
	// a part nobody could read is never dropped for not fitting the listing.
	for _, unavailable := range s.Unavailable {
		if !knownSurveyGroup(unavailable.Group) {
			rendered.WriteString(renderUnread(unavailable.Group, unavailable.Reason))
		}
	}
	return rendered.String()
}

// inFlightGroup is the survey group runs fill, named so the report can say the
// runs are unknown in the same place it would have listed them.
const inFlightGroup = "in flight"

// InFlightGroup names that group for whoever assembles a survey, so the name a
// failure is filed under is the name the report renders it at.
func InFlightGroup() string { return inFlightGroup }

func knownSurveyGroup(group string) bool {
	switch group {
	case inFlightGroup, "claimed", "blocked", "available", "completed":
		return true
	default:
		return false
	}
}

func (s Survey) unread(group string) (string, bool) {
	for _, unavailable := range s.Unavailable {
		if unavailable.Group == group {
			return unavailable.Reason, true
		}
	}
	return "", false
}

func renderUnread(group, reason string) string {
	return fmt.Sprintf("%s: could not be read, so treat it as unknown rather than empty: %s\n",
		group, singleLine(reason, maxSurveyTitleBytes*2))
}


func renderRunSnapshots(runs []RunSnapshot) string {
	if len(runs) == 0 {
		return "in flight: none\n"
	}
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "in flight (%d):\n", len(runs))
	for _, run := range runs {
		fmt.Fprintf(&rendered, "  %s on %s [%s]\n", run.RunID, run.WorkItemID, run.state())
		if run.Branch != "" {
			fmt.Fprintf(&rendered, "    branch %s, started %s\n", run.Branch, run.StartedAt.UTC().Format(time.RFC3339))
		} else {
			fmt.Fprintf(&rendered, "    started %s\n", run.StartedAt.UTC().Format(time.RFC3339))
		}
		if run.Detail != "" {
			fmt.Fprintf(&rendered, "    %s\n", singleLine(run.Detail, maxSurveyTitleBytes*2))
		}
	}
	return rendered.String()
}

// state names where a run is: its status, and the phase it reached when the
// record has one, because "running" alone does not say what it is doing.
func (r RunSnapshot) state() string {
	if r.Phase == "" {
		return r.Status
	}
	return r.Status + ", " + r.Phase
}

func renderWorkItemGroup(label string, items []WorkItemSummary) string {
	if len(items) == 0 {
		return label + ": none\n"
	}
	listed := items
	if len(listed) > maxSurveyItems {
		listed = listed[:maxSurveyItems]
	}
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "%s (%d):\n", label, len(items))
	for _, item := range listed {
		fmt.Fprintf(&rendered, "  [%s] p%d %s\n", item.ID, item.Priority, singleLine(item.Title, maxSurveyTitleBytes))
	}
	if len(items) > len(listed) {
		fmt.Fprintf(&rendered, "  %d further %s item(s) are not listed here.\n", len(items)-len(listed), label)
	}
	return rendered.String()
}

// Render describes a finished run to the operator. It reports the artifacts a
// failed run preserved as carefully as it reports a successful promotion:
// either way they are what the operator's next decision is about.
func (r RunReport) Render() string {
	var rendered strings.Builder
	fmt.Fprintf(&rendered, "%s\n", r.Headline())
	if r.RunID != "" {
		fmt.Fprintf(&rendered, "  run: %s\n", r.RunID)
	}
	if r.Branch != "" {
		fmt.Fprintf(&rendered, "  branch: %s\n", r.Branch)
	}
	if r.WorktreePath != "" && !r.Integrated {
		fmt.Fprintf(&rendered, "  worktree: %s\n", r.WorktreePath)
	}
	if r.RepairAttempts > 0 {
		fmt.Fprintf(&rendered, "  repair attempts: %d\n", r.RepairAttempts)
	}
	if r.Failure != "" {
		fmt.Fprintf(&rendered, "  failure: %s\n", singleLine(r.Failure, MaxOperatorMessageBytes))
	}
	return rendered.String()
}

// Headline states in one line what became of a run. It is what the operator
// reads first and what the product manager is told, so it never describes work
// as integrated on the strength of anything but a recorded promotion.
func (r RunReport) Headline() string {
	item := r.WorkItemID
	if item == "" {
		item = "the work item"
	}
	switch {
	case r.Paused:
		limit := r.UsageLimitKind
		if strings.TrimSpace(limit) == "" {
			limit = "provider"
		}
		resets := "an unstated time"
		if r.UsageLimitResetsAt != nil {
			resets = r.UsageLimitResetsAt.UTC().Format(time.RFC3339)
		}
		return fmt.Sprintf("%s is paused for the %s usage limit and is still in flight; it resets at %s, and /work %s after that continues the same run", item, limit, resets, item)
	case r.Integrated:
		closed := ""
		if r.WorkItemClosed {
			closed = " and the item is closed"
		}
		return fmt.Sprintf("%s was integrated into %s at %s%s", item, r.TargetBranch, r.Commit, closed)
	case r.Blocked:
		return fmt.Sprintf("%s stopped and now carries a durable blocker", item)
	case r.Failure != "":
		return fmt.Sprintf("%s did not finish", item)
	case r.Status != "":
		return fmt.Sprintf("%s finished with status %s and nothing integrated", item, r.Status)
	default:
		return item + " finished with nothing integrated"
	}
}

// Render describes what stopping achieved, including the parts of it that did
// not: a run still winding down and a settlement that failed are both things
// the operator has to know about.
func (s Stopped) Render() string {
	var rendered strings.Builder
	if s.WorkItemID == "" {
		// Nothing was stopped, so there is nothing to describe. The reason for
		// that is the caller's error to report, not something to narrate here.
		return ""
	}
	if !s.Finished {
		fmt.Fprintf(&rendered, "%s was asked to stop but has not given up yet; it is left in flight and can be settled once it does.\n", s.WorkItemID)
		return rendered.String()
	}
	if s.AlreadyFinished {
		fmt.Fprintf(&rendered, "%s had already finished before the stop reached it, so nothing was stopped.\n", s.WorkItemID)
		rendered.WriteString(indent(s.Report.Headline()))
		return rendered.String()
	}
	fmt.Fprintf(&rendered, "stopped work on %s.\n", s.WorkItemID)
	rendered.WriteString(indent(s.Report.Headline()))
	if s.Report.Paused {
		// A paused run reported no failure but is not finished either: it is
		// waiting to be continued, and only saying so keeps "stopped" from
		// reading as "over".
		rendered.WriteString(indent("it had paused itself before the stop reached it, so nothing is working on it now and it continues only if you start it again."))
	}
	if s.RunErr != nil {
		rendered.WriteString(indent("the stopped run reported: " + singleLine(s.RunErr.Error(), MaxOperatorMessageBytes)))
	}
	rendered.WriteString(renderSettlements(s.Settlements))
	return rendered.String()
}

func renderSettlements(settlements []Settlement) string {
	if len(settlements) == 0 {
		return indent("nothing was left outstanding to settle.")
	}
	var rendered strings.Builder
	for _, settlement := range settlements {
		fmt.Fprintf(&rendered, "    %s on %s: %s\n", settlement.RunID, settlement.WorkItemID, settlement.Action)
		if settlement.Detail != "" {
			fmt.Fprintf(&rendered, "        %s\n", singleLine(settlement.Detail, maxSurveyTitleBytes*2))
		}
		if settlement.Failure != "" {
			fmt.Fprintf(&rendered, "        could not be settled: %s\n", singleLine(settlement.Failure, maxSurveyTitleBytes*2))
		}
	}
	return rendered.String()
}

// singleLine folds a value into one bounded line, so tracker prose and provider
// failures stay a list entry whatever they contain. It is cut on a rune
// boundary: a line truncated mid-rune is not text.
func singleLine(value string, limit int) string {
	folded := strings.Join(strings.Fields(value), " ")
	if len(folded) <= limit {
		return folded
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(folded[cut]) {
		cut--
	}
	return strings.TrimSpace(folded[:cut]) + "..."
}
