package chat

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/goal"
)

const recordedGoal = "Maintain a traceable chain from the product brief through goals, designs, work, code changes, and verification."

func TestAdmittingWorkUnderAGoalTheGoalsDoNotStateIsRefused(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Admitting the plugin marketplace.",
			`{"action":"create","title":"A plugin marketplace","description":"Third-party extensions.","goal":"Grow the ecosystem.","reason":"the operator asked"}`)},
		{SessionID: "session-1", FinalText: "It was refused; no goal says that."},
	}})
	options.Tracker = tracker
	options.Goals = recordedGoals(recordedGoal)

	reply, err := openTestSession(t, options).Send(context.Background(), "admit it")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	// The refusal names what was claimed and where the harness looked, so the
	// answer is to quote the goal rather than to try the same sentence again.
	failure := reply.Actions[0].Failure
	if !strings.Contains(failure, "Grow the ecosystem.") || !strings.Contains(failure, "v1-goals") {
		t.Fatalf("failure = %q", failure)
	}
	// Nothing reached the tracker. An item admitted under a goal nothing states
	// asserts a traceability that is not there, and it is refused before it
	// exists rather than reported afterwards.
	if len(tracker.created) != 0 {
		t.Fatalf("created = %#v", tracker.created)
	}
}

func TestAdmittedWorkRecordsTheGoalItResolvedTo(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Admitting it.",
			`{"action":"create","title":"Resolve a work item's goal","description":"Make the attribution mean something.","goal":"`+recordedGoal+`","reason":"the chain breaks at its last link"}`)},
		{SessionID: "session-1", FinalText: "Admitted."},
	}})
	options.Tracker = tracker
	options.Goals = recordedGoals(recordedGoal)

	reply, err := openTestSession(t, options).Send(context.Background(), "admit it")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	// The goal was checked, so nothing says it was not.
	if strings.Contains(reply.Actions[0].Summary, "nothing checked") {
		t.Fatalf("summary = %q", reply.Actions[0].Summary)
	}
	if len(tracker.created) != 1 {
		t.Fatalf("created = %#v", tracker.created)
	}
	// What the harness writes is what it reads back: the item it just created
	// resolves to the goal it was admitted under.
	if !options.Goals.AttributionOf(tracker.created[0].Notes).Resolved() {
		t.Fatalf("the admitted item does not resolve to a goal: %q", tracker.created[0].Notes)
	}
}

func TestWorkAdmittedBeforeGoalsWereCheckedCanAcquireOne(t *testing.T) {
	t.Parallel()

	legacy := "Admitted to the backlog by the product manager in conversation chat-1, after turn 4.\n\nReason: the operator asked for it."
	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.4": {ID: "yoyodyne-ifd.4", Title: "Add configurable management roles", Status: "open", Notes: legacy},
	}}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("It serves the traceability goal.",
			`{"action":"attribute","id":"yoyodyne-ifd.4","goal":"`+recordedGoal+`","reason":"the roles exist to keep intent traceable"}`)},
		{SessionID: "session-1", FinalText: "Attributed."},
	}})
	options.Tracker = tracker
	options.Goals = recordedGoals(recordedGoal)

	reply, err := openTestSession(t, options).Send(context.Background(), "attribute ifd.4")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	if len(tracker.updates) != 1 {
		t.Fatalf("updates = %#v", tracker.updates)
	}
	change := tracker.updates[0].change
	// Nothing already on the item is rewritten: the goal an item was admitted
	// under cannot be edited, and the attribution is appended to what is there.
	if change.Title != "" || change.Description != "" || change.Priority != nil || change.Parent != nil {
		t.Fatalf("attributing rewrote something: %#v", change)
	}
	if !strings.Contains(change.AppendNotes, "Attributed to a goal by the product manager") {
		t.Fatalf("appended notes carry no provenance: %q", change.AppendNotes)
	}
	// The item, with what was appended, now resolves.
	attribution := options.Goals.AttributionOf(legacy + "\n\n" + change.AppendNotes)
	if !attribution.Resolved() || attribution.Goal.ArtifactID != "v1-goals" {
		t.Fatalf("attribution = %#v", attribution)
	}
}

func TestAttributingWorkToAGoalTheGoalsDoNotStateIsRefused(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{items: map[string]beads.WorkItem{
		"yoyodyne-ifd.4": {ID: "yoyodyne-ifd.4", Title: "Add configurable management roles", Status: "open"},
	}}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Attributing it.",
			`{"action":"attribute","id":"yoyodyne-ifd.4","goal":"Make the harness pleasant to use.","reason":"it feels right"}`)},
		{SessionID: "session-1", FinalText: "It was refused."},
	}})
	options.Tracker = tracker
	options.Goals = recordedGoals(recordedGoal)

	reply, err := openTestSession(t, options).Send(context.Background(), "attribute ifd.4")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	// An attribution nothing supports is exactly as wrong as an admission under
	// one, so it is refused the same way and the item is left saying nothing.
	if len(tracker.updates) != 0 {
		t.Fatalf("updates = %#v", tracker.updates)
	}
}

func TestAdmittingWorkWithNoRecordedGoalsSaysNothingCheckedIt(t *testing.T) {
	t.Parallel()

	// A project that has not written its goals down yet still admits work: the
	// alternative is a harness that refuses to plan the work of writing them.
	// What it does not do is let the attribution read as though it was checked.
	tracker := &fakeTracker{}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Admitting it.",
			`{"action":"create","title":"Write the goals","description":"There are none yet.","goal":"Have goals to work from.","reason":"nothing traces anywhere"}`)},
		{SessionID: "session-1", FinalText: "Admitted."},
	}})
	options.Tracker = tracker

	reply, err := openTestSession(t, options).Send(context.Background(), "admit it")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	if !strings.Contains(reply.Actions[0].Summary, "nothing checked the goal it names") {
		t.Fatalf("summary = %q", reply.Actions[0].Summary)
	}
}

func TestASurveySaysWhichAdmittedWorkNamesNoGoal(t *testing.T) {
	t.Parallel()

	goals := recordedGoals(recordedGoal)
	rendered := renderOpenQueueEvidence([]beads.WorkItem{
		{ID: "yoyodyne-ifd.1", Title: "Attributed work", Status: "open", Notes: goal.Note(recordedGoal)},
		{ID: "yoyodyne-ifd.2", Title: "Legacy work", Status: "open", Notes: "Admitted long ago."},
		{ID: "yoyodyne-ifd.3", Title: "Misattributed work", Status: "open", Notes: goal.Note("Grow the ecosystem.")},
	}, goals)

	// The survey is where the product manager would go to attribute the backlog,
	// so it says which items are waiting for one and which carry a claim that is
	// wrong, without turning the listing into a per-item report.
	for _, want := range []string{
		"1 of 3 name a goal the goals state, 1 name none, and 1 name a goal the goals do not state",
		"yoyodyne-ifd.2",
		"yoyodyne-ifd.3",
		`"attribute" records a goal`,
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("survey = %q, want it to contain %q", rendered, want)
		}
	}

	// A survey of a long queue is cut at its end, so what the queue is for has to
	// survive the cut: an account of its traceability that was cut off would read
	// as a queue with nothing to say about it.
	long := make([]beads.WorkItem, 0, maxTrackerSurveyItems)
	for index := range maxTrackerSurveyItems {
		long = append(long, beads.WorkItem{
			ID:     "yoyodyne-ifd." + strconv.Itoa(index),
			Title:  strings.Repeat("a long title ", 16),
			Status: "open",
		})
	}
	cut := renderOpenQueueEvidence(long, goals)
	if !strings.Contains(cut, "cut at") {
		t.Fatalf("a survey of %d long items was not cut", len(long))
	}
	if !strings.Contains(cut, "0 of 200 name a goal the goals state") {
		t.Fatalf("the cut survey lost what the queue is for: %q", cut)
	}

	// With nothing to check against, nothing is counted: a tally taken against
	// goals nobody could read would report traceability that was never confirmed.
	unchecked := renderOpenQueueEvidence([]beads.WorkItem{{ID: "yoyodyne-ifd.1", Status: "open"}}, goal.Set{})
	if !strings.Contains(unchecked, "What the queue is for was not checked") {
		t.Fatalf("survey = %q", unchecked)
	}
}

func TestReadingAnItemSaysWhatItIsFor(t *testing.T) {
	t.Parallel()

	goals := recordedGoals(recordedGoal)
	attributed := renderWorkItemEvidence(beads.WorkItem{ID: "yoyodyne-ifd.1", Notes: goal.Note(recordedGoal)}, goals)
	if !strings.Contains(attributed, "attribution: it serves a goal v1-goals states") {
		t.Fatalf("rendered item = %q", attributed)
	}
	legacy := renderWorkItemEvidence(beads.WorkItem{ID: "yoyodyne-ifd.2", Notes: "Admitted long ago."}, goals)
	if !strings.Contains(legacy, "attribution: none recorded") {
		t.Fatalf("rendered item = %q", legacy)
	}
	wrong := renderWorkItemEvidence(beads.WorkItem{ID: "yoyodyne-ifd.3", Notes: goal.Note("Grow the ecosystem.")}, goals)
	// Told apart, because they are not the same thing to do about it: one is
	// work to attribute and the other is a claim to correct.
	if !strings.Contains(wrong, "Grow the ecosystem.") || strings.Contains(wrong, "none recorded") {
		t.Fatalf("rendered item = %q", wrong)
	}
}

func TestWorkProposedUnderAGoalTheGoalsDoNotStateIsNotPutToTheOperator(t *testing.T) {
	t.Parallel()

	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: "I suggest this.\n\n" + proposalFence +
			"\n{\"items\":[{\"title\":\"A plugin marketplace\",\"description\":\"Third-party extensions.\",\"rationale\":\"You raised it.\",\"goal\":\"Grow the ecosystem.\"}]}\n```\n"},
	}})
	options.Tracker = &fakeTracker{}
	options.Goals = recordedGoals(recordedGoal)

	reply, err := openTestSession(t, options).Send(context.Background(), "what about a marketplace")
	var unrecorded *ProposalGoalError
	if !errors.As(err, &unrecorded) {
		t.Fatalf("Send() error = %v, want a ProposalGoalError", err)
	}
	// The operator is never asked. An approval that the creation then refuses has
	// already spent the decision it was asking for.
	if len(reply.Proposals) != 0 {
		t.Fatalf("proposals = %#v", reply.Proposals)
	}
	if !strings.Contains(err.Error(), "Grow the ecosystem.") {
		t.Fatalf("error = %v", err)
	}
}

func TestApprovingAProposalWhoseGoalHasSinceGoneRefusesRatherThanCreating(t *testing.T) {
	t.Parallel()

	// The goals are read from the repository rather than from the conversation,
	// so between the proposal and the approval the goal can be reworded or
	// retired. What must not happen is an item admitted under a goal that is no
	// longer there.
	tracker := &fakeTracker{}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: "I suggest this.\n\n" + proposalFence +
			"\n{\"items\":[{\"title\":\"Resolve a work item's goal\",\"description\":\"Make it mean something.\",\"rationale\":\"You raised it.\",\"goal\":\"" + recordedGoal + "\"}]}\n```\n"},
	}})
	options.Tracker = tracker
	options.Goals = recordedGoals(recordedGoal)
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "what next")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Proposals) != 1 {
		t.Fatalf("proposals = %#v", reply.Proposals)
	}
	session.options.Goals = recordedGoals("Something else entirely.")
	if _, err := session.Approve(context.Background(), reply.Proposals[0].ID); err == nil {
		t.Fatalf("Approve() created an item under a goal that is no longer recorded")
	}
	if len(tracker.created) != 0 {
		t.Fatalf("created = %#v", tracker.created)
	}
}

// recordedGoals is a repository whose goals document states what a test names,
// so a test about attribution says only that.
func recordedGoals(statements ...string) goal.Set {
	set := goal.Set{Sources: []string{"v1-goals"}}
	for _, statement := range statements {
		set.Goals = append(set.Goals, goal.Goal{
			Statement:  statement,
			ArtifactID: "v1-goals",
			Path:       "docs/product/goals/v1-goals.md",
			InForce:    true,
		})
	}
	return set
}
