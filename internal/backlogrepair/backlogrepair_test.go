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
		Held: backlog.ReadHolds(map[string]backlog.Hold{
			stopped.ID: {Reason: "run run-7 stopped on it and its change is preserved"},
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

// A status is cleared on evidence about every link the item records, and the
// admitted work is the open and the blocked — so a blocker somebody is running
// right now is in neither listing, and its absence from them says nothing. This
// is the same refusal the dependency class makes, applied to the write that
// would otherwise be made on it.
func TestAStatusIsNotClearedOnALinkNothingSettles(t *testing.T) {
	t.Parallel()

	item := blockedItem("yoyodyne-ifd.44", "Waiting on work the listings do not carry", unstatedLink("yoyodyne-ifd.45"))
	records := Records{
		Admitted: []beads.WorkItem{item},
		Held:     backlog.ReadHolds(nil),
		Goals:    recordedGoals("Run development nearly autonomously"),
	}

	if report := Survey(records); len(report.Repairs) != 0 {
		t.Fatalf("repairs = %#v, want none while nothing says what became of the work it waits for", report.Repairs)
	}
	_, err := Judge(item, ClassStatus, "", records)
	if err == nil {
		t.Fatal("Judge() cleared a status on a link nothing read says is finished")
	}
	for _, want := range []string{"nothing read here says what became of", "yoyodyne-ifd.45"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Judge() refused with %q, want it to say %q", err, want)
		}
	}

	// The caller reading that item and finding it closed is what settles it, and
	// is the same evidence a dependency repair rests on.
	records.Finished = map[string]struct{}{"yoyodyne-ifd.45": {}}
	if _, err := Judge(item, ClassStatus, "", records); err != nil {
		t.Fatalf("Judge() error = %v after the tracker reported the blocker closed", err)
	}
}

// A repair has to take, or the next pass reports the same item again and the
// loop this class exists to end is the loop it runs. The attribution repair
// appends the goal's note rather than rewriting anything, so what has to hold is
// that the item in the state that leaves it — its old note still there, the new
// one after it — is no longer stale. The newest attribution line is the current
// claim, so it is; and where the goal carries an identity, what is appended is
// that identity, so the next re-wording leaves it resolved too.
func TestARepairedAttributionIsNoLongerStale(t *testing.T) {
	t.Parallel()

	goals := recordedGoals("Run development nearly autonomously")
	goals.Goals[0].Identity = "run-autonomously"
	orphaned := attributedItem("yoyodyne-ifd.46", "Attributed before the goal was reworded", "Run development almost without a person")
	records := Records{Admitted: []beads.WorkItem{orphaned}, Held: backlog.ReadHolds(nil), Goals: goals}
	if _, err := Judge(orphaned, ClassAttribution, "", records); err != nil {
		t.Fatalf("Judge() error = %v, want the orphaned attribution offered for repair", err)
	}

	// What the repair writes, appended after everything already there — the old
	// note included — and the witness the tracker re-records from that write.
	// The repair is an AppendNotes-only Update carrying the goal's note, and the
	// beads client witnesses the goal named in any such write; the witness written
	// here by hand is the one that update produces, as
	// TestAWrittenGoalIsWitnessedWhereReplacingTheNotesCannotReachIt in
	// internal/beads pins. If the client's note handling changes, that test is
	// where this assumption stops holding.
	repaired := orphaned
	repaired.Notes = orphaned.Notes + "\n\nRe-attributed after the goal it named stopped resolving.\n\n" + goals.NoteFor("Run development nearly autonomously")
	repaired.GoalWitness = goal.Witness{Recorded: true, Statement: "[run-autonomously] Run development nearly autonomously"}
	records.Admitted = []beads.WorkItem{repaired}

	if attribution := goals.AttributionOf(repaired.Notes, repaired.GoalWitness); !attribution.Resolved() || attribution.Identity != "run-autonomously" {
		t.Fatalf("after the repair the item is %#v, want it attributed by the goal's identity", attribution)
	}
	if report := Survey(records); len(report.Repairs) != 0 || len(report.Holds) != 0 {
		t.Fatalf("after the repair the survey still reports %#v / %#v", report.Repairs, report.Holds)
	}
	if _, err := Judge(repaired, ClassAttribution, "", records); err == nil {
		t.Fatal("Judge() offered a repaired attribution for repair again")
	} else if !strings.Contains(err.Error(), "one the goals state") {
		t.Fatalf("Judge() refused with %q, want it to say the goal now resolves", err)
	}

	// The re-wording that orphaned the old note does nothing to the new one,
	// which is the whole of why the repair writes the identity rather than the
	// words.
	goals.Goals[0].Statement = "Run development with almost nobody in the loop"
	records.Goals = goals
	if report := Survey(records); len(report.Repairs) != 0 {
		t.Fatalf("a re-wording orphaned the repaired attribution again: %#v", report.Repairs)
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
