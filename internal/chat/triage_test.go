package chat

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The run a docket entry names, in the shape the harness mints them.
const stoppedRun = "run-0123456789abcdef0123456789abcdef"

// A triage decision lands on the work item, and it says which stoppage it
// settled. That is the whole point of recording it: the next reader of a run
// that stopped — a later conversation, a later operator — finds the decision
// beside the evidence rather than the evidence alone, and does not decide it a
// second time.
func TestATriageDecisionIsRecordedOnTheWorkItem(t *testing.T) {
	t.Parallel()

	answer := trackerReply("The refused half is its own item now.",
		`{"action":"triage","id":"yoyodyne-ifd.70","run":"`+stoppedRun+`","decision":"rescope","reason":"the reviewer refused the migration, which was never in this item's criteria"}`)
	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.70": {ID: "yoyodyne-ifd.70", Title: "the item that stopped", Status: "open"},
	}}
	reply := triageReply(t, tracker, nil, answer)

	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	if len(tracker.updates) != 1 || tracker.updates[0].id != "yoyodyne-ifd.70" {
		t.Fatalf("updates = %#v", tracker.updates)
	}
	notes := tracker.updates[0].change.AppendNotes
	for _, want := range []string{
		"Triaged: re-scoped, with what was out of scope split out",
		"on the stopped work of run " + stoppedRun,
		"by the development manager in conversation",
		"the reviewer refused the migration",
	} {
		if !strings.Contains(notes, want) {
			t.Fatalf("the item's triage note is missing %q:\n%s", want, notes)
		}
	}
	// Re-scoping buys no further attempt at the work, so nothing was blocked and
	// nothing was spent.
	if len(tracker.blocked) != 0 {
		t.Fatalf("a re-scope blocked the item: %#v", tracker.blocked)
	}
	rendered := renderTrackerOutcomes(domain.RoleDevelopmentManager, reply.Actions)
	if !strings.Contains(rendered, `triaged yoyodyne-ifd.70 as "rescope"`) {
		t.Fatalf("the operator was not told what was decided:\n%s", rendered)
	}
}

// Escalating is the decision that reaches a person, and it is two things at
// once: the item says durably that it is waiting on somebody, and the report
// reaches the pile the operator reads. Either alone is how stopped work went
// unnoticed before this workflow existed.
func TestAnEscalationBlocksTheItemAndReachesTheOperator(t *testing.T) {
	t.Parallel()

	answer := reportReply(
		trackerReply("This one is upstream of the change.",
			`{"action":"triage","id":"yoyodyne-ifd.1.9","run":"`+stoppedRun+`","decision":"escalate","reason":"the findings dispute the acceptance criteria, and another attempt loses the same argument"}`),
		`{"severity":"warning","message":"yoyodyne-ifd.1.9 has been round triage twice; its criteria are disputed rather than its change."}`,
	)
	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.1.9": {ID: "yoyodyne-ifd.1.9", Title: "the item that keeps coming back", Status: "open"},
	}}
	reports := &fakeReports{}
	reply := triageReplyWithReports(t, tracker, nil, reports, answer)

	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	if len(tracker.blocked) != 1 || tracker.blocked[0][0] != "yoyodyne-ifd.1.9" {
		t.Fatalf("blocked items = %#v", tracker.blocked)
	}
	if !strings.Contains(tracker.blocked[0][1], "Escalated to the operator by triage") ||
		!strings.Contains(tracker.blocked[0][1], "on the stopped work of run "+stoppedRun) {
		t.Fatalf("the blocker does not say what it was:\n%s", tracker.blocked[0][1])
	}
	// An escalation is a blocker rather than a note, because a note leaves the
	// item looking like work still in flight.
	if len(tracker.updates) != 0 {
		t.Fatalf("an escalation was recorded as an ordinary note: %#v", tracker.updates)
	}
	if len(reports.appended) != 1 || reports.appended[0].Severity != report.SeverityWarning {
		t.Fatalf("collected reports = %#v", reports.appended)
	}
}

// An escalation nobody is told about is not an escalation. The harness refuses
// it whole rather than blocking the item and leaving the operator to find it,
// which is exactly the failure this workflow replaces.
func TestAnEscalationWithNoReportIsRefusedAndChangesNothing(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name  string
		reply string
	}{
		{
			name: "no report at all",
			reply: trackerReply("Somebody should look at this.",
				`{"action":"triage","id":"yoyodyne-ifd.90","run":"`+stoppedRun+`","decision":"escalate","reason":"it needs a branch protection changed"}`),
		},
		{
			name: "a report that asks for nothing",
			reply: reportReply(
				trackerReply("Somebody should look at this.",
					`{"action":"triage","id":"yoyodyne-ifd.90","run":"`+stoppedRun+`","decision":"escalate","reason":"it needs a branch protection changed"}`),
				`{"severity":"note","message":"the forge dropped the merge."}`),
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			tracker := &fakeTracker{items: map[string]beads.WorkItem{
				"yoyodyne-ifd.90": {ID: "yoyodyne-ifd.90", Title: "the stuck publication", Status: "open"},
			}}
			options := triageOptions(t, tracker, nil, testCase.reply)
			options.Reports = &fakeReports{}
			_, err := openTestSession(t, options).Send(context.Background(), "Work the docket.")
			escalation := &EscalationError{}
			if !errors.As(err, &escalation) {
				t.Fatalf("Send() error = %v, want an escalation refusal", err)
			}
			if escalation.WorkItemID != "yoyodyne-ifd.90" {
				t.Fatalf("the refusal names %q", escalation.WorkItemID)
			}
			if len(tracker.blocked) != 0 || len(tracker.updates) != 0 {
				t.Fatalf("a refused escalation changed the tracker: blocked %#v, updates %#v", tracker.blocked, tracker.updates)
			}
		})
	}
}

// The three decisions that buy another attempt at work that already failed go
// through the durable budget, and the budget is what stops an item nothing else
// was going to. A grant is cut to what the cap has room for, and the next one is
// refused outright — with the refusal naming the cap, which is the development
// manager's evidence for escalating instead.
func TestARepairGrantSpendsTheDurableBudgetAndIsRefusedOnceItIsGone(t *testing.T) {
	t.Parallel()

	budgets := newTriageBudgetGate(t, runstate.TriageCaps{ReviewRounds: 2, MergeRearms: 1}, 2)
	// One round is already spent by the run that stopped, so a grant of two is
	// cut to the one the cap still has room for.
	if _, err := budgets.store.RecordReviewRound(context.Background(), "yoyodyne-ifd.90", runstate.RoundKey(stoppedRun, 0), budgets.clock.Now()); err != nil {
		t.Fatalf("RecordReviewRound() error = %v", err)
	}
	answer := trackerReply("The findings are actionable.",
		`{"action":"triage","id":"yoyodyne-ifd.90","run":"`+stoppedRun+`","decision":"repair","reason":"every finding names a file and a line, and the branch is preserved"}`)
	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.90": {ID: "yoyodyne-ifd.90", Title: "the item that stopped", Status: "open"},
	}}
	reply := triageReply(t, tracker, budgets, answer)
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	if !strings.Contains(reply.Actions[0].Summary, "the grant was cut from 2 round(s) to the 1") {
		t.Fatalf("the truncation was not reported: %q", reply.Actions[0].Summary)
	}
	counters, err := budgets.store.Counters("yoyodyne-ifd.90")
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if counters.RepairGrants != 1 || counters.TruncatedGrants != 1 || counters.GrantedRounds != 1 {
		t.Fatalf("counters after one grant = %#v", counters)
	}

	// The rounds the grant was cut to are then spent, and the next decision that
	// would buy another has nothing left to buy it with.
	if _, err := budgets.store.RecordReviewRound(context.Background(), "yoyodyne-ifd.90", runstate.RoundKey(stoppedRun, 1), budgets.clock.Now()); err != nil {
		t.Fatalf("RecordReviewRound() error = %v", err)
	}
	spent := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.90": {ID: "yoyodyne-ifd.90", Title: "the item that stopped", Status: "open"},
	}}
	refused := triageReply(t, spent, budgets, trackerReply("One more go.",
		`{"action":"triage","id":"yoyodyne-ifd.90","run":"`+stoppedRun+`","decision":"repair","reason":"the findings still look actionable"}`))
	if len(refused.Actions) != 1 || refused.Actions[0].Applied {
		t.Fatalf("actions = %#v", refused.Actions)
	}
	if !strings.Contains(refused.Actions[0].Failure, "2 of 2 permitted review round(s) are spent") {
		t.Fatalf("the refusal does not name the cap: %q", refused.Actions[0].Failure)
	}
	// A refused decision writes nothing: an item that carried a repair note the
	// cap then contradicted would be worse than one carrying no note at all.
	if len(spent.updates) != 0 {
		t.Fatalf("a refused grant wrote on the item: %#v", spent.updates)
	}
}

// A conversation with no budget wired may still decide anything that spends
// nothing, and may not grant, re-run, or re-arm. An unreadable budget is never
// spent through as though it were empty.
func TestTheDecisionsThatSpendABudgetAreRefusedWithoutOne(t *testing.T) {
	t.Parallel()

	for decision, wantApplied := range map[string]bool{
		"repair":   false,
		"rerun":    false,
		"rearm":    false,
		"rescope":  true,
		"wait":     true,
		"escalate": true,
	} {
		t.Run(decision, func(t *testing.T) {
			t.Parallel()

			answer := trackerReply("Decided.",
				`{"action":"triage","id":"yoyodyne-ifd.90","run":"`+stoppedRun+`","decision":"`+decision+`","reason":"the docket entry says so"}`)
			if decision == "escalate" {
				answer = reportReply(answer, `{"severity":"warning","message":"yoyodyne-ifd.90 needs a repository setting only a person can change."}`)
			}
			tracker := &fakeTracker{items: map[string]beads.WorkItem{
				"yoyodyne-ifd.90": {ID: "yoyodyne-ifd.90", Title: "the item that stopped", Status: "open"},
			}}
			reply := triageReply(t, tracker, nil, answer)
			if len(reply.Actions) != 1 || reply.Actions[0].Applied != wantApplied {
				t.Fatalf("%q applied = %#v, want %v", decision, reply.Actions, wantApplied)
			}
			if wantApplied {
				return
			}
			if !strings.Contains(reply.Actions[0].Failure, "no triage budget is wired to this conversation") {
				t.Fatalf("%q was refused for the wrong reason: %q", decision, reply.Actions[0].Failure)
			}
		})
	}
}

// A decision the harness cannot act on is refused before anything in the block
// runs, exactly as any other malformed action is: a decision outside the
// vocabulary, and a stoppage the decision does not name.
func TestATriageDecisionIsRefusedWhenItNamesNoDecisionOrNoStoppage(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name   string
		action string
		want   string
	}{
		{
			name:   "a decision outside the vocabulary",
			action: `{"action":"triage","id":"yoyodyne-ifd.90","run":"` + stoppedRun + `","decision":"abandon","reason":"why"}`,
			want:   `triage decision "abandon" is not a decision`,
		},
		{
			name:   "no decision at all",
			action: `{"action":"triage","id":"yoyodyne-ifd.90","run":"` + stoppedRun + `","reason":"why"}`,
			want:   `triage requires "decision"`,
		},
		{
			name:   "no run",
			action: `{"action":"triage","id":"yoyodyne-ifd.90","decision":"wait","reason":"why"}`,
			want:   `triage requires "run"`,
		},
		{
			name:   "a run identifier no run could have",
			action: `{"action":"triage","id":"yoyodyne-ifd.90","run":"the last one","decision":"wait","reason":"why"}`,
			want:   `is not a run identifier`,
		},
		{
			name:   "an argument triage has no use for",
			action: `{"action":"triage","id":"yoyodyne-ifd.90","run":"` + stoppedRun + `","decision":"wait","note":"and this","reason":"why"}`,
			want:   `triage does not take "note"`,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			tracker := &fakeTracker{items: map[string]beads.WorkItem{
				"yoyodyne-ifd.90": {ID: "yoyodyne-ifd.90", Title: "the item that stopped", Status: "open"},
			}}
			options := triageOptions(t, tracker, nil, trackerReply("Decided.", testCase.action))
			_, err := openTestSession(t, options).Send(context.Background(), "Work the docket.")
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("Send() error = %v, want it to contain %q", err, testCase.want)
			}
			if len(tracker.updates) != 0 || len(tracker.blocked) != 0 {
				t.Fatalf("a refused block changed the tracker: updates %#v, blocked %#v", tracker.updates, tracker.blocked)
			}
		})
	}
}

// Triage belongs to the role the docket is delivered to. Every other role is
// refused it, so no persona and no conversation can move where stopped work is
// decided.
func TestOnlyTheDevelopmentManagerMayTriage(t *testing.T) {
	t.Parallel()

	for _, role := range ConversationalRoles() {
		if role == domain.RoleDevelopmentManager {
			continue
		}
		tracker := &fakeTracker{items: map[string]beads.WorkItem{
			"yoyodyne-ifd.90": {ID: "yoyodyne-ifd.90", Title: "the item that stopped", Status: "open"},
		}}
		options := triageOptions(t, tracker, nil, trackerReply("Decided.",
			`{"action":"triage","id":"yoyodyne-ifd.90","run":"`+stoppedRun+`","decision":"wait","reason":"the forge still has it"}`))
		options.Role = role
		options.Agent = string(role)
		_, err := openTestSession(t, options).Send(context.Background(), "Work the docket.")
		authority := &AuthorityError{}
		if !errors.As(err, &authority) {
			t.Fatalf("%s was not refused the triage action: %v", role, err)
		}
		if len(tracker.updates) != 0 || len(tracker.blocked) != 0 {
			t.Fatalf("%s changed the tracker: updates %#v, blocked %#v", role, tracker.updates, tracker.blocked)
		}
	}
}

// Closed work has left the backlog: there is nothing to hand back, nothing to
// run again, and an escalation would put a closed item back into a blocked
// state nobody asked for. The refusal says where a note about it belongs
// instead.
func TestTriageIsRefusedOnWorkThatHasClosed(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.90": {ID: "yoyodyne-ifd.90", Title: "the item that stopped", Status: "closed"},
	}}
	reply := triageReply(t, tracker, nil, trackerReply("Handing it back.",
		`{"action":"triage","id":"yoyodyne-ifd.90","run":"`+stoppedRun+`","decision":"rescope","reason":"half of it was out of scope"}`))
	if len(reply.Actions) != 1 || reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	if !strings.Contains(reply.Actions[0].Failure, "is closed, so what becomes of the work that stopped is no longer a decision") {
		t.Fatalf("the refusal reads = %q", reply.Actions[0].Failure)
	}
	if len(tracker.updates) != 0 || len(tracker.blocked) != 0 {
		t.Fatalf("a refused decision changed the tracker: updates %#v, blocked %#v", tracker.updates, tracker.blocked)
	}
}

// triageOptions is a development manager's conversation answering with one
// reply and then closing off, which is the shape every test here needs.
func triageOptions(t *testing.T, tracker Tracker, budgets TriageBudgets, answer string) Options {
	t.Helper()

	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: answer},
		{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: "That is what I decided."},
	}})
	options.Role = domain.RoleDevelopmentManager
	options.Agent = string(domain.RoleDevelopmentManager)
	options.Tracker = tracker
	if budgets != nil {
		options.Triage = budgets
	}
	return options
}

func triageReply(t *testing.T, tracker Tracker, budgets TriageBudgets, answer string) Reply {
	t.Helper()

	return triageReplyWithReports(t, tracker, budgets, &fakeReports{}, answer)
}

func triageReplyWithReports(t *testing.T, tracker Tracker, budgets TriageBudgets, reports Reports, answer string) Reply {
	t.Helper()

	options := triageOptions(t, tracker, budgets, answer)
	options.Reports = reports
	reply, err := openTestSession(t, options).Send(context.Background(), "Work the docket.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	return reply
}

// testTriageBudgets is the real durable budget over a temporary state root,
// wired the way the command line wires it. The gate is what these tests are
// about, so it is the gate itself rather than a stand-in for it.
type triageBudgetGate struct {
	store  *runstate.TriageStore
	caps   runstate.TriageCaps
	rounds int
	clock  fixedClock
}

func newTriageBudgetGate(t *testing.T, caps runstate.TriageCaps, rounds int) *triageBudgetGate {
	t.Helper()

	store, err := runstate.NewTriageStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewTriageStore() error = %v", err)
	}
	return &triageBudgetGate{store: store, caps: caps, rounds: rounds}
}

func (b *triageBudgetGate) GrantRepair(ctx context.Context, workItemID string) (runstate.RepairGrant, error) {
	return b.store.GrantRepair(ctx, workItemID, b.rounds, b.now(), b.caps)
}

func (b *triageBudgetGate) RecordRerun(ctx context.Context, workItemID string) (runstate.TriageCounters, error) {
	return b.store.RecordRerun(ctx, workItemID, b.now(), b.caps)
}

func (b *triageBudgetGate) RecordMergeRearm(ctx context.Context, workItemID string) (runstate.TriageCounters, error) {
	return b.store.RecordMergeRearm(ctx, workItemID, b.now(), b.caps)
}

func (b *triageBudgetGate) now() time.Time { return b.clock.Now() }
