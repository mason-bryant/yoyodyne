package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

// The run a docket entry names, in the shape the harness mints them, and a
// second one for the entry beside it: two entries is what a decision can be
// transposed between.
const (
	stoppedRun      = "run-0123456789abcdef0123456789abcdef"
	otherStoppedRun = "run-fedcba9876543210fedcba9876543210"
	// countingProcess is who a round these tests seed was charged by. A round is
	// only ever given back to the process that charged it, and nothing here gives
	// one back; what it needs is somebody to have charged it.
	countingProcess = "pid-1-000000000000000a"
)

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

	budgets := newTriageBudgetGate(t, runstate.TriageCaps{ReviewRounds: 2, RepairGrants: 1, Reruns: 1, MergeRearms: 1}, 2)
	// One round is already spent by the run that stopped, so a grant of two is
	// cut to the one the cap still has room for.
	if _, err := budgets.store.RecordReviewRound(context.Background(), "yoyodyne-ifd.90", runstate.RoundKey(stoppedRun, 0), countingProcess, budgets.clock.Now()); err != nil {
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

	// Triage grants one repair per item and a second is a person's decision, so
	// the next one is refused whatever the rounds say.
	spent := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.90": {ID: "yoyodyne-ifd.90", Title: "the item that stopped", Status: "open"},
	}}
	refused := triageReply(t, spent, budgets, trackerReply("One more go.",
		`{"action":"triage","id":"yoyodyne-ifd.90","run":"`+stoppedRun+`","decision":"repair","reason":"the findings still look actionable"}`))
	if len(refused.Actions) != 1 || refused.Actions[0].Applied {
		t.Fatalf("actions = %#v", refused.Actions)
	}
	if !strings.Contains(refused.Actions[0].Failure, "repair grant is refused for yoyodyne-ifd.90: 1 of 1 permitted repair grant(s) are spent") {
		t.Fatalf("the refusal does not name the cap: %q", refused.Actions[0].Failure)
	}
	// A refused decision writes nothing: an item that carried a repair note the
	// cap then contradicted would be worse than one carrying no note at all.
	if len(spent.updates) != 0 {
		t.Fatalf("a refused grant wrote on the item: %#v", spent.updates)
	}
}

// A re-run and a merge re-arm each spend a budget of their own, and neither is
// bounded by the review rounds: a re-run buys a whole fresh run and spends no
// round itself, and a re-arm buys no round at all. Both are exercised against
// the real record rather than a stand-in, because what they promise is that the
// second one is refused — and a budget that only ever counted up would look
// exactly the same from the conversation.
//
// The item here has no reviewer verdict against it at all, which is the case the
// shared round budget cannot bound: with only that budget, an item whose runs
// keep stopping before review could be re-run for ever.
func TestARerunAndAMergeRearmSpendTheirOwnBudgets(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name string
		// decision is what the development manager records, summary is what the
		// operator is told it spent, and refusal is what the budget says when the
		// second one asks.
		decision string
		summary  string
		counted  func(runstate.TriageCounters) int
		refusal  string
	}{
		{
			name:     "a re-run",
			decision: "rerun",
			summary:  "1 re-run(s) of it are now recorded",
			counted:  func(counters runstate.TriageCounters) int { return counters.Reruns },
			refusal:  "re-run is refused for yoyodyne-ifd.68.3: 1 of 1 permitted re-run(s) are spent",
		},
		{
			name:     "a merge re-arm",
			decision: "rearm",
			summary:  "1 merge re-arm(s) of it are now recorded",
			counted:  func(counters runstate.TriageCounters) int { return counters.MergeRearms },
			refusal:  "merge re-arm is refused for yoyodyne-ifd.68.3: 1 of 1 permitted merge re-arm(s) are spent",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			budgets := newTriageBudgetGate(t, runstate.TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 1, MergeRearms: 1}, 2)
			decided := trackerReply("Decided.",
				`{"action":"triage","id":"yoyodyne-ifd.68.3","run":"`+stoppedRun+`","decision":"`+testCase.decision+`","reason":"the change was right and the ground moved under it"}`)
			first := &fakeTracker{items: map[string]beads.WorkItem{
				"yoyodyne-ifd.68.3": {ID: "yoyodyne-ifd.68.3", Title: "the item that stopped", Status: "open"},
			}}
			reply := triageReply(t, first, budgets, decided)
			if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
				t.Fatalf("actions = %#v", reply.Actions)
			}
			if !strings.Contains(reply.Actions[0].Summary, testCase.summary) {
				t.Fatalf("the summary does not say what was spent: %q", reply.Actions[0].Summary)
			}
			if len(first.updates) != 1 {
				t.Fatalf("updates = %#v", first.updates)
			}
			counters, err := budgets.store.Counters("yoyodyne-ifd.68.3")
			if err != nil {
				t.Fatalf("Counters() error = %v", err)
			}
			if testCase.counted(counters) != 1 {
				t.Fatalf("counters after one %s = %#v", testCase.decision, counters)
			}

			// The budget is now gone — with rounds to spare, which is the point —
			// and the same decision asked for again is refused rather than counted
			// a second time.
			second := &fakeTracker{items: map[string]beads.WorkItem{
				"yoyodyne-ifd.68.3": {ID: "yoyodyne-ifd.68.3", Title: "the item that stopped", Status: "open"},
			}}
			refused := triageReply(t, second, budgets, decided)
			if len(refused.Actions) != 1 || refused.Actions[0].Applied {
				t.Fatalf("actions = %#v", refused.Actions)
			}
			if !strings.Contains(refused.Actions[0].Failure, testCase.refusal) {
				t.Fatalf("the refusal does not name the cap: %q", refused.Actions[0].Failure)
			}
			if len(second.updates) != 0 {
				t.Fatalf("a refused decision wrote on the item: %#v", second.updates)
			}
			after, err := budgets.store.Counters("yoyodyne-ifd.68.3")
			if err != nil {
				t.Fatalf("Counters() error = %v", err)
			}
			if testCase.counted(after) != 1 {
				t.Fatalf("a refused decision moved the counter: %#v", after)
			}
		})
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

// A decision names two things that have to agree: the item it lands on, and the
// run whose stoppage it settles. A docket of several entries is where they come
// apart — two entries transposed put each decision's reasoning onto the other
// item, and both then read as decided about a change neither is. The run record
// is what settles it, and a decision whose run was made for somebody else's work
// is refused before anything is spent or written.
func TestATriageDecisionIsRefusedWhenItsRunIsAnotherItemsStoppedWork(t *testing.T) {
	t.Parallel()

	runs := fakeStoppedRuns{items: map[string]string{
		stoppedRun:      "yoyodyne-ifd.90",
		otherStoppedRun: "yoyodyne-ifd.70",
	}}
	for _, testCase := range []struct {
		name    string
		run     string
		applied bool
		want    string
	}{
		{
			name:    "the item's own stopped work",
			run:     stoppedRun,
			applied: true,
		},
		{
			name: "another item's stopped work",
			run:  otherStoppedRun,
			want: "was made for yoyodyne-ifd.70 rather than for yoyodyne-ifd.90",
		},
		{
			name: "a run nothing recorded",
			run:  "run-00000000000000000000000000000000",
			want: "could not be read, so nothing says it is yoyodyne-ifd.90's stopped work",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			budgets := newTriageBudgetGate(t, runstate.TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 1, MergeRearms: 1}, 2)
			tracker := &fakeTracker{items: map[string]beads.WorkItem{
				"yoyodyne-ifd.90": {ID: "yoyodyne-ifd.90", Title: "the item that stopped", Status: "open"},
			}}
			reply := triageReplyAbout(t, tracker, budgets, runs, trackerReply("Decided.",
				`{"action":"triage","id":"yoyodyne-ifd.90","run":"`+testCase.run+`","decision":"repair","reason":"every finding names a file and a line"}`))
			if len(reply.Actions) != 1 || reply.Actions[0].Applied != testCase.applied {
				t.Fatalf("actions = %#v, want applied = %v", reply.Actions, testCase.applied)
			}
			counters, err := budgets.store.Counters("yoyodyne-ifd.90")
			if err != nil {
				t.Fatalf("Counters() error = %v", err)
			}
			if testCase.applied {
				if len(tracker.updates) != 1 || counters.RepairGrants != 1 {
					t.Fatalf("a decision about the item's own run was not carried out: updates %#v, counters %#v",
						tracker.updates, counters)
				}
				return
			}
			if !strings.Contains(reply.Actions[0].Failure, testCase.want) {
				t.Fatalf("the refusal reads = %q", reply.Actions[0].Failure)
			}
			// The check is asked before anything is spent, so a refused decision
			// leaves the item exactly as much budget as it had — and writes no
			// reasoning about somebody else's change onto it.
			if len(tracker.updates) != 0 || len(tracker.blocked) != 0 {
				t.Fatalf("a refused decision changed the tracker: updates %#v, blocked %#v", tracker.updates, tracker.blocked)
			}
			if counters.RepairGrants != 0 {
				t.Fatalf("a refused decision spent the item's budget: %#v", counters)
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

// A cap refusal has to leave the development manager holding something the
// operator can run. Naming the remedy without naming the verb is what sent two
// overrides into a work item's notes — where "record an override against the
// item" pointed, and where no guard reads — and left the resubmitted decision
// meeting the identical refusal.
func TestACapRefusalNamesTheCommandThatCrossesTheCap(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name string
		err  error
		want []string
	}{
		{
			name: "the refusal a cap produces",
			err: runstate.TriageCapError{
				Action:     runstate.TriageRerun,
				Budget:     runstate.TriageReviewRoundBudget,
				WorkItemID: "yoyodyne-ifd.224",
				Spent:      4,
				Cap:        4,
			},
			want: []string{
				"re-run is refused for yoyodyne-ifd.224",
				`yoyo triage override --budget "review round"`,
				"--cap <n>",
				"yoyodyne-ifd.224",
				"nothing written into the item's notes crosses it either",
			},
		},
		{
			// Nothing produces this today, and a refusal that lost the detail must
			// still leave the operator holding a verb rather than a description.
			name: "a cap refusal carrying no budget to name",
			err:  fmt.Errorf("the budget is gone: %w", runstate.ErrTriageCapReached),
			want: []string{
				`yoyo triage override --budget "<budget>"`,
				"nothing written into the item's notes crosses it either",
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			refusal := refusedPastCap(testCase.err)
			if !errors.Is(refusal, runstate.ErrTriageCapReached) {
				t.Fatalf("the refusal stopped being a cap refusal: %v", refusal)
			}
			for _, want := range testCase.want {
				if !strings.Contains(refusal.Error(), want) {
					t.Fatalf("the refusal is missing %q:\n%s", want, refusal)
				}
			}
		})
	}

	// Everything else is left exactly as it was: a tracker that would not answer
	// is not a cap, and dressing it as one sends the operator to a command that
	// changes nothing.
	other := errors.New("the tracker could not be reached")
	if refusedPastCap(other) != other {
		t.Fatalf("a failure that is not a cap refusal was rewritten: %v", refusedPastCap(other))
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
	return triageSend(t, options)
}

// triageReplyAbout is the same conversation with the run records wired, which is
// how the command line wires a development manager's: the decision is checked
// against what the harness recorded about the run it names.
func triageReplyAbout(t *testing.T, tracker Tracker, budgets TriageBudgets, stoppages Stoppages, answer string) Reply {
	t.Helper()

	options := triageOptions(t, tracker, budgets, answer)
	options.Reports = &fakeReports{}
	options.Stoppages = stoppages
	return triageSend(t, options)
}

func triageSend(t *testing.T, options Options) Reply {
	t.Helper()

	reply, err := openTestSession(t, options).Send(context.Background(), "Work the docket.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	return reply
}

// triageReplyClosing is the same conversation with the docket wired, which is
// how the command line wires a development manager's: a decision settles the
// entry it was made about, so the stoppage stops being put to her.
func triageReplyClosing(t *testing.T, tracker Tracker, docket TriageEntries, answer string) Reply {
	t.Helper()

	options := triageOptions(t, tracker, nil, answer)
	options.Reports = &fakeReports{}
	options.Docket = docket
	return triageSend(t, options)
}

// fakeDocket is the durable docket as a decision reaches it: what was closed,
// and what refuses to close.
type fakeDocket struct {
	closed []DocketClosure
	// closes is how many entries each call reports settling, and err is a docket
	// that could not be written — the state where the decision stands and the
	// entry does not.
	closes int
	err    error
}

func (d *fakeDocket) Close(_ context.Context, closure DocketClosure) (int, error) {
	d.closed = append(d.closed, closure)
	if d.err != nil {
		return 0, d.err
	}
	return d.closes, nil
}

// The other half of the docket's lifecycle: an entry is created where work
// stops, and a decision closes it. Without this the docket is rebuilt from the
// same durable records at every scan and the same settled stoppage comes back
// for ever — and a re-scope spends no counter, so nothing else the harness reads
// says she looked at it.
func TestATriageDecisionClosesTheDocketEntryItSettled(t *testing.T) {
	t.Parallel()

	answer := trackerReply("The refused half is its own item now.",
		`{"action":"triage","id":"yoyodyne-ifd.70","run":"`+stoppedRun+`","decision":"rescope","reason":"the reviewer refused the migration"}`)
	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.70": {ID: "yoyodyne-ifd.70", Title: "the item that stopped", Status: "open"},
	}}
	docket := &fakeDocket{closes: 1}
	reply := triageReplyClosing(t, tracker, docket, answer)

	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	if len(docket.closed) != 1 {
		t.Fatalf("closed = %#v, want the entry this decision settled", docket.closed)
	}
	closure := docket.closed[0]
	if closure.RunID != stoppedRun || closure.Decision != "rescope" {
		t.Fatalf("closure = %#v, want the stoppage and the decision it was settled with", closure)
	}
	// A re-scope answers a run that stopped rather than a publication nobody
	// merged, and closing the wrong one would take a live question off the docket.
	if len(closure.Classes) != 1 || closure.Classes[0] != triage.ClassStoppedRun {
		t.Fatalf("classes = %#v, want the stopped run alone", closure.Classes)
	}
	if !strings.Contains(closure.Reason, "refused the migration") || !strings.Contains(closure.DecidedBy, "development manager") {
		t.Fatalf("closure = %#v, want the reasoning and who decided it", closure)
	}
	if rendered := renderTrackerOutcomes(domain.RoleDevelopmentManager, reply.Actions); !strings.Contains(rendered, "docket entry(s) of that run are closed") {
		t.Fatalf("the operator was not told the entry was closed:\n%s", rendered)
	}
}

// A wait is a decision about the publication the forge has not finished, and an
// escalation hands the whole run to the operator. Which entries a decision
// closes is what keeps a live question on the docket while a settled one leaves.
func TestWhichEntriesADecisionClosesFollowsWhatItAnswers(t *testing.T) {
	t.Parallel()

	for _, decided := range []struct {
		decision string
		classes  []triage.Class
	}{
		{decision: "wait", classes: []triage.Class{triage.ClassPublication}},
		{decision: "escalate", classes: []triage.Class{triage.ClassStoppedRun, triage.ClassPublication}},
	} {
		t.Run(decided.decision, func(t *testing.T) {
			t.Parallel()

			answer := trackerReply("Decided.",
				`{"action":"triage","id":"yoyodyne-ifd.70","run":"`+stoppedRun+`","decision":"`+decided.decision+`","reason":"the forge still has it"}`)
			if decided.decision == "escalate" {
				answer = reportReply(answer,
					`{"severity":"warning","message":"yoyodyne-ifd.70 needs a branch protection changed."}`)
			}
			tracker := &fakeTracker{items: map[string]beads.WorkItem{
				"yoyodyne-ifd.70": {ID: "yoyodyne-ifd.70", Title: "the item that stopped", Status: "open"},
			}}
			docket := &fakeDocket{closes: len(decided.classes)}
			reply := triageReplyClosing(t, tracker, docket, answer)

			if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
				t.Fatalf("actions = %#v", reply.Actions)
			}
			if len(docket.closed) != 1 {
				t.Fatalf("closed = %#v, want one closure", docket.closed)
			}
			if fmt.Sprint(docket.closed[0].Classes) != fmt.Sprint(decided.classes) {
				t.Fatalf("classes = %#v, want %#v", docket.closed[0].Classes, decided.classes)
			}
		})
	}
}

// A docket that could not be written leaves the decision recorded and the entry
// standing, which is what every entry did before closing existed. Failing the
// action instead would report a recorded decision as though nothing had
// happened, and invite exactly the second decision this prevents.
func TestADocketEntryThatCouldNotBeClosedIsSaidRatherThanFailingTheDecision(t *testing.T) {
	t.Parallel()

	answer := trackerReply("Decided.",
		`{"action":"triage","id":"yoyodyne-ifd.70","run":"`+stoppedRun+`","decision":"rescope","reason":"the reviewer refused the migration"}`)
	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.70": {ID: "yoyodyne-ifd.70", Title: "the item that stopped", Status: "open"},
	}}
	docket := &fakeDocket{err: errors.New("the docket is unwritable")}
	reply := triageReplyClosing(t, tracker, docket, answer)

	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the decision recorded", reply.Actions)
	}
	if len(tracker.updates) != 1 {
		t.Fatalf("updates = %#v, want the decision on the item", tracker.updates)
	}
	rendered := renderTrackerOutcomes(domain.RoleDevelopmentManager, reply.Actions)
	if !strings.Contains(rendered, "could not be closed and will be put to you again") {
		t.Fatalf("the standing entry was not said out loud:\n%s", rendered)
	}
}

// A conversation with no docket wired records the decision and closes nothing,
// rather than failing a decision it was asked to make.
func TestADecisionWithoutADocketIsStillRecorded(t *testing.T) {
	t.Parallel()

	answer := trackerReply("Decided.",
		`{"action":"triage","id":"yoyodyne-ifd.70","run":"`+stoppedRun+`","decision":"rescope","reason":"the reviewer refused the migration"}`)
	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.70": {ID: "yoyodyne-ifd.70", Title: "the item that stopped", Status: "open"},
	}}
	reply := triageReply(t, tracker, nil, answer)

	if len(reply.Actions) != 1 || !reply.Actions[0].Applied || len(tracker.updates) != 1 {
		t.Fatalf("actions = %#v, updates = %#v", reply.Actions, tracker.updates)
	}
}

// fakeStoppedRuns is the harness's record of which run was made for which work
// item, which is what a decision is checked against, and of where each item's
// own change is, which is what a decomposition under it is gated on.
type fakeStoppedRuns struct {
	items map[string]string
	// unlanded is the change each work item's own runs left off the integration
	// target, keyed by item. An item missing from it has nothing unlanded, which
	// is every item in every test that is not about the substrate gate.
	unlanded map[string]UnlandedChange
	// unreadable is the item whose records will not be read, so a gate that
	// cannot establish anything is a state a test can produce.
	unreadable string
}

func (f fakeStoppedRuns) WorkItemOf(_ context.Context, runID string) (string, error) {
	workItemID, recorded := f.items[runID]
	if !recorded {
		return "", fmt.Errorf("no run %s is recorded", runID)
	}
	return workItemID, nil
}

func (f fakeStoppedRuns) UnlandedChange(_ context.Context, workItemID string) (UnlandedChange, bool, error) {
	if f.unreadable != "" && f.unreadable == workItemID {
		return UnlandedChange{}, false, fmt.Errorf("the run records of %s could not be read", workItemID)
	}
	unlanded, found := f.unlanded[workItemID]
	return unlanded, found, nil
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

// What the harness writes down after delivering a stoppage is what she actually
// recorded, read from the applied actions rather than from the prose of the
// reply.
func TestAReplySaysWhichStoppageItDecided(t *testing.T) {
	t.Parallel()

	reply := Reply{Actions: []TrackerOutcome{
		{
			// A decision about a different stoppage, which is exactly what a
			// conversation working a docket of several entries produces.
			Applied: true,
			Action:  TrackerAction{Action: actionTriage, ID: "yoyodyne-other", Run: otherStoppedRun, Decision: decisionRerun},
		},
		{
			// A refused decision changed nothing, so nothing here may report it as
			// one.
			Applied: false,
			Action:  TrackerAction{Action: actionTriage, ID: "yoyodyne-task", Run: stoppedRun, Decision: decisionRerun},
		},
		{
			Applied: true,
			Action: TrackerAction{
				Action: actionTriage, ID: "yoyodyne-task", Run: stoppedRun, Decision: decisionRepair,
				Reason: "the findings are narrow and the change is preserved",
			},
		},
	}}

	decision, reason, found := reply.TriageDecision(stoppedRun)
	if !found || decision != decisionRepair || !strings.Contains(reason, "the findings are narrow") {
		t.Fatalf("TriageDecision() = %q, %q, %v, want the applied decision about this stoppage", decision, reason, found)
	}
	if _, _, found := reply.TriageDecision("run-9999999999999999999999999999ffff"); found {
		t.Fatalf("TriageDecision() found a decision about a stoppage nothing in the reply names")
	}
	// A turn that answered and decided nothing is an answer rather than a
	// failure, and reports as one.
	if _, _, found := (Reply{}).TriageDecision(stoppedRun); found {
		t.Fatalf("TriageDecision() found a decision in a reply that recorded none")
	}
}
