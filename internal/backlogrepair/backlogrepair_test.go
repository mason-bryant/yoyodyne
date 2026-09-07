package backlogrepair

import (
	"errors"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/directive"
	"github.com/mason-bryant/yoyodyne/internal/goal"
)

// The three things that go stale on their own, each found from the record that
// says so: the dependency graph, the tracker's own listing of a link, and the
// goals document.
func TestSurveyFindsEachKindOfStaleState(t *testing.T) {
	t.Parallel()

	records := Records{
		Admitted: []beads.WorkItem{
			blockedItem("yoyodyne-ifd.10", "Its blocker landed", closedLink("yoyodyne-ifd.4")),
			openItem("yoyodyne-ifd.11", "Waiting on nothing", closedLink("yoyodyne-ifd.5")),
			attributedItem("yoyodyne-ifd.12", "Named a goal that was reworded", "Run development nearly autonomously"),
		},
		Held:  backlog.ReadHolds(nil),
		Goals: recordedGoals("Run development almost without a person"),
	}

	report := Survey(records)
	if len(report.Holds) != 0 {
		t.Fatalf("holds = %#v, want none: nothing here is held for a person", report.Holds)
	}
	found := map[string]Repair{}
	for _, repair := range report.Repairs {
		found[repair.WorkItemID+" "+string(repair.Class)] = repair
	}
	for _, want := range []struct {
		key   string
		stale string
	}{
		{"yoyodyne-ifd.10 status", "nothing unfinished blocks it"},
		{"yoyodyne-ifd.10 dependency", "which is closed"},
		{"yoyodyne-ifd.11 dependency", "which is closed"},
		{"yoyodyne-ifd.12 attribution", "the goals no longer state it"},
	} {
		repair, judged := found[want.key]
		if !judged {
			t.Fatalf("%s was not reported as stale; the report is %#v", want.key, report.Repairs)
		}
		if !strings.Contains(repair.Stale, want.stale) {
			t.Errorf("%s is stale because %q, want it to say %q", want.key, repair.Stale, want.stale)
		}
	}
	// An item that names a goal the goals still state, and one that never named
	// one at all, are both left alone: the first is not stale and the second is a
	// judgement about what the work is for rather than a correction.
	if _, judged := found["yoyodyne-ifd.11 attribution"]; judged {
		t.Error("work that names no goal was reported as an attribution to correct")
	}
}

// A hold is what separates an item whose status is stale from one whose status
// is the last thing anybody knows about a change on a branch. Both look the
// same; only one may be touched.
func TestHeldWorkIsReportedRatherThanRepaired(t *testing.T) {
	t.Parallel()

	stopped := blockedItem("yoyodyne-ifd.20", "Its run stopped and the change is still there")
	paused := blockedItem("yoyodyne-ifd.21", "A directive pauses it")
	records := Records{
		Admitted: []beads.WorkItem{stopped, paused},
		Held: backlog.ReadHolds(map[string]string{
			stopped.ID: "run run-7 stopped on it and its change is preserved",
		}),
		Directives: []directive.Directive{{
			ID:         "directive-3",
			Kind:       directive.KindAmbiguous,
			Scope:      []string{paused.ID},
			Unresolved: "the operator has to say which of the two readings was meant",
		}},
		Goals: recordedGoals("Run development nearly autonomously"),
	}

	report := Survey(records)
	if len(report.Repairs) != 0 {
		t.Fatalf("repairs = %#v, want none: both items are held for a person", report.Repairs)
	}
	if len(report.Holds) != 2 {
		t.Fatalf("holds = %#v, want both items reported", report.Holds)
	}
	for _, held := range report.Holds {
		if strings.TrimSpace(held.Reason) == "" {
			t.Errorf("%s is held with no reason restated", held.WorkItemID)
		}
		if strings.TrimSpace(held.Stale) == "" {
			t.Errorf("%s is held with no account of what looks stale about it", held.WorkItemID)
		}
	}

	// And the act itself refuses, in the same terms, so a pass that reported the
	// hold and then asked anyway changes nothing.
	for _, item := range []beads.WorkItem{stopped, paused} {
		if _, err := Judge(item, ClassStatus, "", records); err == nil {
			t.Fatalf("Judge(%s) corrected the status of an item held for a person", item.ID)
		} else {
			var hold *HoldError
			if !errors.As(err, &hold) {
				t.Fatalf("Judge(%s) refused with %v, which is not a hold", item.ID, err)
			}
		}
	}
}

// The reading itself decides. Holds nobody could read hold everything, because
// a reader that cannot tell a stale status from a stoppage must not clear
// either.
func TestUnreadHoldsHoldEveryItem(t *testing.T) {
	t.Parallel()

	item := blockedItem("yoyodyne-ifd.30", "Blocked by nothing the queue still carries")
	records := Records{Admitted: []beads.WorkItem{item}, Goals: recordedGoals("Run development nearly autonomously")}

	report := Survey(records)
	if len(report.Repairs) != 0 {
		t.Fatalf("repairs = %#v, want none while nothing could say what is held", report.Repairs)
	}
	if len(report.Holds) != 1 || !strings.Contains(report.Holds[0].Reason, "could not be read") {
		t.Fatalf("holds = %#v, want the item held because the reading did not happen", report.Holds)
	}
	if _, err := Judge(item, ClassStatus, "", records); err == nil {
		t.Fatal("Judge() corrected a status with no reading of what is held behind it")
	}
}

// What the records still say is right is refused, and the refusal says what they
// say instead — which is what the caller reasons from next.
func TestJudgeRefusesWhatTheRecordsDoNotCallStale(t *testing.T) {
	t.Parallel()

	goals := recordedGoals("Run development nearly autonomously")
	waiting := blockedItem("yoyodyne-ifd.40", "Genuinely waiting", openLink("yoyodyne-ifd.41"))
	blocker := openItem("yoyodyne-ifd.41", "The work it waits for")
	attributed := attributedItem("yoyodyne-ifd.42", "Named a goal the goals state", "Run development nearly autonomously")
	unattributed := openItem("yoyodyne-ifd.43", "Admitted before goals were checked")
	records := Records{
		Admitted: []beads.WorkItem{waiting, blocker, attributed, unattributed},
		Held:     backlog.ReadHolds(nil),
		Goals:    goals,
	}

	for _, testCase := range []struct {
		name      string
		item      beads.WorkItem
		class     Class
		dependsOn string
		says      string
	}{
		{
			name:  "a status with unfinished work behind it",
			item:  waiting,
			class: ClassStatus,
			says:  "waits on unfinished work: yoyodyne-ifd.41",
		},
		{
			name:      "a link the tracker does not hold as closed",
			item:      waiting,
			class:     ClassDependency,
			dependsOn: blocker.ID,
			says:      "records no dependency the tracker says is finished",
		},
		{
			name:  "an attribution that still resolves",
			item:  attributed,
			class: ClassAttribution,
			says:  "the goal it names is one the goals state",
		},
		{
			name:  "work that never named a goal",
			item:  unattributed,
			class: ClassAttribution,
			says:  "\"attribute\" is what records one",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			_, err := Judge(testCase.item, testCase.class, testCase.dependsOn, records)
			if err == nil {
				t.Fatalf("Judge(%s, %s) corrected state the records still call right", testCase.item.ID, testCase.class)
			}
			if !strings.Contains(err.Error(), testCase.says) {
				t.Fatalf("Judge() refused with %q, want it to say %q", err, testCase.says)
			}
		})
	}
}

// A dependency repair is about one named link, and it is the tracker's word on
// that link that makes it dead. An identifier the queue simply does not carry is
// not evidence of anything.
func TestJudgeNamesTheDeadLinkAndNoOther(t *testing.T) {
	t.Parallel()

	item := blockedItem("yoyodyne-ifd.50", "Two links, one dead",
		closedLink("yoyodyne-ifd.51"), unstatedLink("yoyodyne-ifd.52"))
	records := Records{
		Admitted: []beads.WorkItem{item},
		Held:     backlog.ReadHolds(nil),
		Goals:    recordedGoals("Run development nearly autonomously"),
	}

	repair, err := Judge(item, ClassDependency, "yoyodyne-ifd.51", records)
	if err != nil {
		t.Fatalf("Judge() error = %v", err)
	}
	if repair.DependsOn != "yoyodyne-ifd.51" {
		t.Fatalf("repair names %q, want the link the tracker holds as closed", repair.DependsOn)
	}
	if _, err := Judge(item, ClassDependency, "yoyodyne-ifd.52", records); err == nil {
		t.Fatal("Judge() retired a link on work nothing says is finished")
	}
	// Until the caller reads that item and the tracker says it is closed, which
	// is the one other place the evidence can come from.
	records.Finished = map[string]struct{}{"yoyodyne-ifd.52": {}}
	if _, err := Judge(item, ClassDependency, "yoyodyne-ifd.52", records); err != nil {
		t.Fatalf("Judge() error = %v after the tracker reported the item closed", err)
	}
}

func TestJudgeRefusesAStateItDoesNotRecognize(t *testing.T) {
	t.Parallel()

	item := blockedItem("yoyodyne-ifd.60", "Anything")
	records := Records{Admitted: []beads.WorkItem{item}, Held: backlog.ReadHolds(nil)}
	if _, err := Judge(item, Class("priority"), "", records); err == nil {
		t.Fatal("Judge() accepted a kind of stale state nothing here recognizes")
	}
}

func openItem(id, title string, dependencies ...beads.Dependency) beads.WorkItem {
	return beads.WorkItem{ID: id, Title: title, Status: "open", Dependencies: dependencies}
}

func blockedItem(id, title string, dependencies ...beads.Dependency) beads.WorkItem {
	item := openItem(id, title, dependencies...)
	item.Status = "blocked"
	return item
}

// attributedItem is an item whose notes record a goal in the words some goals
// document stated it in, which is how every attributed item in the tracker
// carries one.
func attributedItem(id, title, named string) beads.WorkItem {
	item := openItem(id, title)
	item.Notes = goal.Note(named)
	item.GoalWitness = goal.Witness{Recorded: true, Statement: named}
	return item
}

func closedLink(id string) beads.Dependency {
	return beads.Dependency{ID: id, Type: beads.BlocksDependency, Status: "closed"}
}

func openLink(id string) beads.Dependency {
	return beads.Dependency{ID: id, Type: beads.BlocksDependency, Status: "open"}
}

// unstatedLink is a listing that records the relation and says nothing about
// what became of the work, which is what a Beads export frequently carries.
func unstatedLink(id string) beads.Dependency {
	return beads.Dependency{ID: id, Type: beads.BlocksDependency}
}

func recordedGoals(statements ...string) goal.Set {
	set := goal.Set{Sources: []string{"v1-goals"}}
	for _, statement := range statements {
		set.Goals = append(set.Goals, goal.Goal{
			Statement:  statement,
			ArtifactID: "v1-goals",
			Path:       "docs/product/goals/v1-goals.md",
			InForce:    true,
			Approval:   artifact.ApprovalApproved,
		})
	}
	return set
}
