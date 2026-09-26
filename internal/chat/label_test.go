package chat

// The label half of admission: an item admitted under a labelling practice
// carries the label from the write that admits it, and work already in the
// queue acquires or loses one through an action of its own. Both doors matter
// for the reason both executor doors did — the practice that provoked this
// (a "reliability" label on every item admitted under the directive) was being
// followed by hand because the product manager held no action that sets one.

import (
	"context"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// Admission carries the labels to the tracker in the same write, so the item
// never exists unlabelled, and the operator is told the label went on.
func TestAdmissionCarriesTheLabelsToTheTracker(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Admitting the fix under the directive.",
			`{"action":"create","title":"The sweep never clears a preserved-branch stoppage","description":"Fix it.","goal":"`+recordedGoal+`","labels":["reliability","bug"],"reason":"a stall fix under the reliability directive"}`)},
		{SessionID: "session-1", FinalText: "It is in the queue, labelled."},
	}})
	options.Tracker = tracker
	options.Goals = recordedGoals(recordedGoal)
	options.Admission = Admission{WorkItems: domain.ApprovalAutomatic}
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "admit the stall fix")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the creation carried out", reply.Actions)
	}
	if len(tracker.created) != 1 || strings.Join(tracker.created[0].Labels, " ") != "reliability bug" {
		t.Fatalf("created = %#v, want both labels carried to the tracker in the admission", tracker.created)
	}
	if !strings.Contains(reply.Actions[0].Summary, "labels reliability bug") {
		t.Fatalf("summary = %q, want the labels said where the admission is reported", reply.Actions[0].Summary)
	}
}

// An item already in the queue acquires a label through "label", with the reason
// recorded on the item beside the change, and loses one the same way.
func TestAnItemAlreadyInTheQueueCanAcquireAndLoseALabel(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.423": {ID: "yoyodyne-ifd.423", Title: "The stale-state repair never clears a preserved-branch stoppage", Status: "open"},
	}}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Labelling it, and taking the old marker off.",
			`{"action":"label","id":"yoyodyne-ifd.423","add":"reliability","reason":"a stall fix admitted under the reliability directive"}`,
			`{"action":"label","id":"yoyodyne-ifd.423","remove":"triage","reason":"triage settled it"}`)},
		{SessionID: "session-1", FinalText: "Done."},
	}})
	options.Tracker = tracker
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "423 is a reliability item")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 2 || !reply.Actions[0].Applied || !reply.Actions[1].Applied {
		t.Fatalf("actions = %#v, want both label actions carried out", reply.Actions)
	}
	if len(tracker.updates) != 2 {
		t.Fatalf("updates = %#v, want one write per label action", tracker.updates)
	}
	added := tracker.updates[0].change
	if strings.Join(added.AddLabels, " ") != "reliability" || len(added.RemoveLabels) != 0 {
		t.Fatalf("first update = %#v, want exactly the one label added", added)
	}
	// The reason is on the item, not only in the conversation: a label whose
	// reason exists only in a transcript is one nobody can account for later.
	if !strings.Contains(added.AppendNotes, "Labelled reliability by the Lead Product Manager") ||
		!strings.Contains(added.AppendNotes, "Reason: a stall fix admitted under the reliability directive") {
		t.Fatalf("first update notes = %q, want the label and the reason recorded on the item", added.AppendNotes)
	}
	removed := tracker.updates[1].change
	if strings.Join(removed.RemoveLabels, " ") != "triage" || len(removed.AddLabels) != 0 {
		t.Fatalf("second update = %#v, want exactly the one label removed", removed)
	}
	if !strings.Contains(removed.AppendNotes, "Label triage removed by the Lead Product Manager") {
		t.Fatalf("second update notes = %q, want the removal recorded on the item", removed.AppendNotes)
	}
	// The operator reads what changed, on each item, by name.
	if !strings.Contains(reply.Actions[0].Summary, "labelled yoyodyne-ifd.423 reliability") {
		t.Fatalf("summary = %q, want it to say which label went on which item", reply.Actions[0].Summary)
	}
	if !strings.Contains(reply.Actions[1].Summary, "removed the label triage from yoyodyne-ifd.423") {
		t.Fatalf("summary = %q, want it to say which label came off which item", reply.Actions[1].Summary)
	}
}

// The development manager has the action wherever she may update an item: it
// is the same authority, held under the same capability.
func TestTheDevelopmentManagerMayLabelAnItem(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.423": {ID: "yoyodyne-ifd.423", Title: "The stale-state repair", Status: "open"},
	}}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Labelling the stall fix.",
			`{"action":"label","id":"yoyodyne-ifd.423","add":"reliability","reason":"it is a stall fix"}`)},
		{SessionID: "session-1", FinalText: "Labelled."},
	}})
	options.Role = domain.RoleDevelopmentManager
	options.Tracker = tracker
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "label 423")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the development manager's label carried out", reply.Actions)
	}
	if len(tracker.updates) != 1 || strings.Join(tracker.updates[0].change.AddLabels, " ") != "reliability" ||
		!strings.Contains(tracker.updates[0].change.AppendNotes, "by the development manager") {
		t.Fatalf("updates = %#v, want the label applied and attributed to the development manager", tracker.updates)
	}
}

// A survey shows each item's labels beside its executor, because the label is
// what an admission practice is checked against and a listing that hid them
// would have the product manager reading every item to find out which carry it.
func TestASurveyShowsEachItemsLabelsBesideItsExecutor(t *testing.T) {
	t.Parallel()

	rendered := renderOpenQueueEvidence([]beads.WorkItem{
		{ID: "yoyodyne-ifd.419", Title: "Labels at admission", Status: "open", Priority: 1, IssueType: "task",
			Labels: []string{"reliability"}},
		{ID: "yoyodyne-ifd.130", Title: "The headless product manager", Status: "open", Priority: 2, IssueType: "task",
			Executor: domain.ConversationWith(domain.RoleArchitect), Labels: []string{"reliability", "bug"}},
		{ID: "yoyodyne-ifd.4", Title: "Ordinary work", Status: "open", Priority: 3, IssueType: "task"},
	}, recordedGoals(recordedGoal))
	for _, line := range []string{
		"- yoyodyne-ifd.419 [open, p1, task, label reliability] Labels at admission",
		"- yoyodyne-ifd.130 [open, p2, task, executor conversation:architect, labels reliability bug] The headless product manager",
		"- yoyodyne-ifd.4 [open, p3, task] Ordinary work",
	} {
		if !strings.Contains(rendered, line) {
			t.Fatalf("survey = %q, want it to carry %q", rendered, line)
		}
	}
	// A read of one item says the same.
	item := renderWorkItemEvidence(beads.WorkItem{ID: "yoyodyne-ifd.419", Title: "Labels at admission", Status: "open",
		Labels: []string{"reliability", "bug"}}, recordedGoals(recordedGoal))
	if !strings.Contains(item, "labels: reliability bug\n") {
		t.Fatalf("read = %q, want the labels on their own line", item)
	}
}

// Both contracts state the shape, so a role told the action exists is told how to
// write it, and a test keeps the number and names of the examples in step with
// what the harness accepts.
func TestBothContractsDocumentTheLabelShape(t *testing.T) {
	t.Parallel()

	for _, role := range []domain.AgentRole{domain.RoleProductManager, domain.RoleDevelopmentManager} {
		contract := SystemPrompt(role, Admission{}, "")
		for _, required := range []string{
			`"labels":["reliability"]`,
			`{"action":"label","id":"beads-id","add":"reliability"`,
			`{"action":"label","id":"beads-id","remove":"reliability"`,
			`A label is an identifier`,
		} {
			if !strings.Contains(contract, required) {
				t.Fatalf("the %s contract does not state %q", role, required)
			}
		}
	}
}
