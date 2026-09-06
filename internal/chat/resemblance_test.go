package chat

import (
	"context"
	"errors"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
)

// theGoal is the statement every admission in this file names, so the goal gate
// is satisfied and what these tests exercise is the duplicate guard behind it.
const theGoal = "Run development nearly autonomously."

// filedReport is one report in the pile, as a role would have filed it.
func filedReport(id, message string) report.Report {
	return report.Report{
		SchemaVersion: report.SchemaVersion,
		ID:            id,
		Role:          domain.RoleDeveloper,
		RunID:         "run-1",
		WorkItemID:    "yoyodyne-ifd.229",
		ProductID:     "yoyodyne",
		RepositoryID:  "repo",
		Severity:      report.SeverityWarning,
		Message:       message,
		RecordedAt:    fixedClock{}.Now(),
	}
}

// The 274/229 shape, replayed through the door it came through: the product
// manager admitting work from a developer report it has already admitted work
// from, after that work had landed.
func TestWorkIsNotAdmittedTwiceFromOneReport(t *testing.T) {
	t.Parallel()

	const filed = "report-3f2ac1904e6b48d0b5e7c2a10d9f4a77"
	tracker := &fakeTracker{all: []beads.WorkItem{{
		ID:     "yoyodyne-ifd.229",
		Title:  "The refreshed export cannot become a run's own change: the skip-worktree guard is enforced, not assumed",
		Status: "closed",
		Notes:  "Admitted to the backlog by the product manager.\n\nAdmitted from report " + filed + ", filed at \"warning\" by the developer.",
	}}}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Admitting the export guard.",
			`{"action":"create","title":"The tracker export cannot be smuggled into a run's committed change","description":"Refuse the export in a run's diff.","goal":"`+
				theGoal+`","report":"`+filed+`","reason":"the developer reported it"}`)},
		{SessionID: "session-1", FinalText: "It is already done."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	options.Goals = recordedGoals(theGoal)
	options.Reports = &fakeReports{appended: []report.Report{filedReport(filed, "the skip-worktree bit can be flipped")}}
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "The developer reported the export can be smuggled in.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(tracker.created) != 0 {
		t.Fatalf("created work items = %#v, want nothing admitted a second time from one report", tracker.created)
	}
	if len(reply.Actions) != 1 || reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the creation refused", reply.Actions)
	}
	// What is caught reaches whoever admits, naming the item rather than only the
	// fact: acting on the existing item needs its identifier.
	for _, want := range []string{"yoyodyne-ifd.229", "closed", filed} {
		if !strings.Contains(reply.Actions[0].Failure, want) {
			t.Fatalf("failure = %q, want it to name %q", reply.Actions[0].Failure, want)
		}
	}
	// The duplicate is of work that has already landed, so the remedy is not
	// "fold it in": a run made for it could not contain anything.
	if !strings.Contains(reply.Actions[0].Failure, "already done") {
		t.Fatalf("failure = %q, want it to say the matched work is done", reply.Actions[0].Failure)
	}
}

// The same guard, the other way round: a report that has produced no work yet
// admits work, and the item records which report it came from, which is what the
// next admission citing it is checked against.
func TestWorkAdmittedFromAReportRecordsTheReport(t *testing.T) {
	t.Parallel()

	const filed = "report-3f2ac1904e6b48d0b5e7c2a10d9f4a77"
	tracker := &fakeTracker{}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Admitting it.",
			`{"action":"create","title":"The export is refused in a run's diff","description":"Refuse it.","goal":"`+
				theGoal+`","report":"`+filed+`","reason":"the developer reported it"}`)},
		{SessionID: "session-1", FinalText: "It is in the backlog."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	options.Goals = recordedGoals(theGoal)
	options.Reports = &fakeReports{appended: []report.Report{filedReport(filed, "the skip-worktree bit can be flipped")}}
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Admit what the developer reported.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(tracker.created) != 1 {
		t.Fatalf("created work items = %#v, want one admission", tracker.created)
	}
	if !strings.Contains(tracker.created[0].Notes, "Admitted from report "+filed) {
		t.Fatalf("created notes = %q, want the report cited on the item", tracker.created[0].Notes)
	}
	if !strings.Contains(reply.Actions[0].Summary, "admitted from report "+filed) {
		t.Fatalf("summary = %q, want the citation reported", reply.Actions[0].Summary)
	}
}

// One record can genuinely prompt more than one piece of work — an operator's
// directive routinely does, and the contract has always said to name it on the
// item that answers it. So the refusal for a source names the way through as well
// as the match, rather than walling off something the role is told to do.
func TestARefusalForASourceSaysHowASecondPieceOfWorkIsAdmitted(t *testing.T) {
	t.Parallel()

	const filed = "report-3f2ac1904e6b48d0b5e7c2a10d9f4a77"
	tracker := &fakeTracker{all: []beads.WorkItem{{
		ID:     "yoyodyne-ifd.229",
		Title:  "The refreshed export cannot become a run's own change",
		Status: "open",
		Notes:  "Admitted from report " + filed + ".",
	}}}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Admitting the second half.",
			`{"action":"create","title":"The export hold is re-checked on every attempt","description":"d","goal":"`+
				theGoal+`","report":"`+filed+`","reason":"the same report asked for both"}`)},
		{SessionID: "session-1", FinalText: "I will admit it without the citation."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	options.Goals = recordedGoals(theGoal)
	options.Reports = &fakeReports{appended: []report.Report{filedReport(filed, "the skip-worktree bit can be flipped")}}
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Admit the rest of what that report asked for.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(tracker.created) != 0 || reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, created = %#v, want the creation refused", reply.Actions, tracker.created)
	}
	if !strings.Contains(reply.Actions[0].Failure, "without citing "+filed) {
		t.Fatalf("failure = %q, want the way through named", reply.Actions[0].Failure)
	}
}

// A citation nothing checked is a guard that has quietly stopped working, so a
// creation naming a report nobody filed admits nothing.
func TestAdmittingFromAReportNobodyFiledCreatesNothing(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Admitting it.",
			`{"action":"create","title":"Something a report asked for","description":"d","goal":"`+
				theGoal+`","report":"report-00000000000000000000000000000000","reason":"r"}`)},
		{SessionID: "session-1", FinalText: "It was refused."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	options.Goals = recordedGoals(theGoal)
	options.Reports = &fakeReports{}
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Admit it.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(tracker.created) != 0 {
		t.Fatalf("created work items = %#v, want nothing created against a report nobody filed", tracker.created)
	}
	if len(reply.Actions) != 1 || reply.Actions[0].Applied ||
		!strings.Contains(reply.Actions[0].Failure, "no report in the pile is") {
		t.Fatalf("actions = %#v, want the creation refused for the citation", reply.Actions)
	}
}

// The 241.2/241.4 shape, replayed through the door it came through: the
// development manager decomposing one parent a second time into a child the
// parent already has.
func TestOneParentIsNotDecomposedTwiceIntoTheSameChild(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{
		items: map[string]beads.WorkItem{
			"yoyodyne-ifd.241": {ID: "yoyodyne-ifd.241", Title: "Bundle-improvement notices speak unprompted", Status: "open"},
		},
		all: []beads.WorkItem{
			{ID: "yoyodyne-ifd.241", Title: "Bundle-improvement notices speak unprompted, and each new one DMs the operator once", Status: "blocked"},
			{
				ID:     "yoyodyne-ifd.241.2",
				Title:  "Each newly-available bundle improvement DMs the operator once, per the architect's ruling",
				Parent: "yoyodyne-ifd.241",
				Status: "open",
			},
			{
				ID:     "yoyodyne-ifd.209.14",
				Title:  "The reviewer's evidence carries the invariants the work item was given",
				Parent: "yoyodyne-ifd.209",
				Status: "closed",
			},
		},
	}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Breaking it down.",
			`{"action":"create","parent":"yoyodyne-ifd.241","title":"Each newly-available improvement DMs the operator once, deduplicated, per the ruling",`+
				`"description":"d","goal":"`+theGoal+`","reason":"decomposing the parent"}`)},
		{SessionID: "session-1", FinalText: "It is already carved out."},
	}}
	options := testOptions(t, provider)
	options.Role = domain.RoleDevelopmentManager
	options.Tracker = tracker
	options.Goals = recordedGoals(theGoal)
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Decompose 241.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(tracker.created) != 0 {
		t.Fatalf("created work items = %#v, want the second decomposition refused", tracker.created)
	}
	if len(reply.Actions) != 1 || reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the creation refused", reply.Actions)
	}
	for _, want := range []string{"yoyodyne-ifd.241.2", "yoyodyne-ifd.241"} {
		if !strings.Contains(reply.Actions[0].Failure, want) {
			t.Fatalf("failure = %q, want it to name %q", reply.Actions[0].Failure, want)
		}
	}
	// The matched child is open, so the remedy is the item rather than a claim
	// that the work is done.
	if !strings.Contains(reply.Actions[0].Failure, "Act on that item") {
		t.Fatalf("failure = %q, want the open-work remedy", reply.Actions[0].Failure)
	}
	// The refusal is about the work rather than about the role: a decomposition
	// refused as an admission would name an authority this role does not have.
	if !strings.Contains(reply.Actions[0].Failure, "carve out of yoyodyne-ifd.241") {
		t.Fatalf("failure = %q, want the act named as decomposition", reply.Actions[0].Failure)
	}
}

// Losing an admission to a tracker that would not answer is worse than admitting
// a duplicate: the duplicate is caught by whoever reads the queue, and the lost
// admission is caught by nobody. So the work is admitted and the gap is stated.
func TestAnAdmissionSaysWhenTheDuplicateCheckCouldNotRun(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{listErr: errors.New("bd list failed: the store is locked")}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Admitting it.",
			`{"action":"create","title":"Something worth doing","description":"d","goal":"`+theGoal+`","reason":"r"}`)},
		{SessionID: "session-1", FinalText: "It is in the backlog."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	options.Goals = recordedGoals(theGoal)
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Admit it.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(tracker.created) != 1 {
		t.Fatalf("created work items = %#v, want the admission to survive an unreadable tracker", tracker.created)
	}
	if !reply.Actions[0].Applied {
		t.Fatalf("action = %#v, want it applied", reply.Actions[0])
	}
	if !strings.Contains(reply.Actions[0].Summary, "nothing checked whether this is already in it") {
		t.Fatalf("summary = %q, want the unrun check stated rather than passed over", reply.Actions[0].Summary)
	}
}

// The guard reads the whole tracker rather than the open queue, because the
// duplicate that costs a run is a duplicate of work that has already landed.
func TestTheDuplicateCheckReadsClosedWorkToo(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Admitting it.",
			`{"action":"create","title":"Something worth doing","description":"d","goal":"`+theGoal+`","reason":"r"}`)},
		{SessionID: "session-1", FinalText: "Done."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	options.Goals = recordedGoals(theGoal)
	session := openTestSession(t, options)

	if _, err := session.Send(context.Background(), "Admit it."); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	listedEverything := false
	for _, status := range tracker.listed {
		if status == "" {
			listedEverything = true
		}
	}
	if !listedEverything {
		t.Fatalf("listings = %#v, want one that names no status so closed work is read", tracker.listed)
	}
}

// A proposal is never refused for a resemblance. What happens instead is that
// the harness does not admit it on a goal's authority: it goes to the operator
// with the item it looks like named on it.
func TestAProposalThatLooksLikeAdmittedWorkIsPutToTheOperator(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{
		items: map[string]beads.WorkItem{
			"yoyodyne-ifd.241": {ID: "yoyodyne-ifd.241", Title: "Bundle-improvement notices", Status: "open"},
		},
		all: []beads.WorkItem{{
			ID:     "yoyodyne-ifd.241.2",
			Title:  "Each newly-available bundle improvement DMs the operator once, per the architect's ruling",
			Parent: "yoyodyne-ifd.241",
			Status: "open",
		}},
	}
	proposal := `{"items":[{"title":"Each newly-available improvement DMs the operator once, deduplicated, per the ruling",` +
		`"description":"d","rationale":"the notices are still silent","goal":"` + theGoal + `","parent":"yoyodyne-ifd.241"}]}`
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: "I suggest this.\n\n```yoyodyne-proposal\n" + proposal + "\n```"},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	options.Goals = recordedGoals(theGoal)
	// The project admits work that traces to an approved goal, which is the case
	// where a duplicate would otherwise reach the queue with nobody looking.
	options.Admission = Admission{WorkItems: domain.ApprovalAutomatic}
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "What is left on the notices?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Admitted) != 0 || len(tracker.created) != 0 {
		t.Fatalf("admitted = %#v, created = %#v, want the proposal put to the operator instead", reply.Admitted, tracker.created)
	}
	if len(reply.Proposals) != 1 {
		t.Fatalf("proposals = %#v, want one awaiting a decision", reply.Proposals)
	}
	if !strings.Contains(reply.Proposals[0].Asking, "yoyodyne-ifd.241.2") {
		t.Fatalf("asking = %q, want the item it looks like named", reply.Proposals[0].Asking)
	}
	// The operator reads the rendered card rather than the field, so the match has
	// to survive into it.
	if !strings.Contains(reply.Proposals[0].Render(), "yoyodyne-ifd.241.2") {
		t.Fatalf("rendered proposal = %q, want the match in front of the operator", reply.Proposals[0].Render())
	}
}

// A proposal that looks like nothing is admitted exactly as it was before, so
// the guard costs the ordinary case nothing.
func TestAProposalThatLooksLikeNothingIsStillAdmitted(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{}
	proposal := `{"items":[{"title":"Stall detection runs without Slack","description":"d",` +
		`"rationale":"the watchdog is wired to a surface","goal":"` + theGoal + `"}]}`
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: "I suggest this.\n\n```yoyodyne-proposal\n" + proposal + "\n```"},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	options.Goals = recordedGoals(theGoal)
	options.Admission = Admission{WorkItems: domain.ApprovalAutomatic}
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "What about the watchdog?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Admitted) != 1 || len(tracker.created) != 1 {
		t.Fatalf("admitted = %#v, created = %#v, want the ordinary admission unchanged", reply.Admitted, tracker.created)
	}
}
