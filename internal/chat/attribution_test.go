package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
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
	if !options.Goals.AttributionOf(tracker.created[0].Notes, false).Resolved() {
		t.Fatalf("the admitted item does not resolve to a goal: %q", tracker.created[0].Notes)
	}
}

func TestADecompositionChildKeepsTheGoalItWasCreatedUnder(t *testing.T) {
	t.Parallel()

	// A decomposition is the development manager's only creation, and the goal it
	// names has to survive exactly as an admission's does. The audit reads an
	// item's goal off the notes the tracker holds, so a creation that validated
	// the goal and then wrote it nowhere would orphan every child of every
	// decomposition — silently, because the action itself reported success, and at
	// scale, because decomposition is where most items now come from.
	//
	// Nothing here is a test double standing in for the tracker. The session acts
	// through the real beads client, so the note the chat layer writes has to
	// survive being turned into a bd command line and read back out of bd's
	// answer — which is the boundary an in-package fake cannot speak for.
	bd := &recordingBD{}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("One child, under the admitted item.",
			`{"action":"create","title":"Triage docket","description":"Stopped work reaches the development manager.","goal":"`+recordedGoal+`","parent":"yoyodyne-ifd.102","priority":1,"reason":"nothing routes stopped work today"}`)},
		{SessionID: "session-1", FinalText: "Filed."},
	}})
	options.Role = domain.RoleDevelopmentManager
	options.Agent = string(domain.RoleDevelopmentManager)
	options.Tracker = beads.Client{Runner: bd, Binary: "bd-test", Dir: "/repo"}
	options.Goals = recordedGoals(recordedGoal)

	session, err := Open(options)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	reply, err := session.Send(context.Background(), "decompose ifd.102")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	if !reply.Actions[0].Applied {
		t.Fatalf("the creation was not applied: %s", reply.Actions[0].Failure)
	}

	// The creation reached bd as a decomposition under the admitted parent, and it
	// carried the note. A --notes bd never receives is an attribution that exists
	// only in the harness's own account of what it did.
	create := bd.command("create")
	if create == nil {
		t.Fatalf("no bd create was run: %#v", bd.args)
	}
	if !slices.Contains(create, "--parent=yoyodyne-ifd.102") {
		t.Fatalf("the child was not created under its parent: %#v", create)
	}

	// The item is then read back the way the audit reads it — off a bd listing,
	// through the same client — and asked the same question yoyo goals
	// attribution asks. Nothing between here and there is stubbed: the notes in
	// bd's listing are the ones bd was told to store.
	listed, err := beads.Client{Runner: bd, Binary: "bd-test", Dir: "/repo"}.List(context.Background(), "open")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("listed = %#v", listed)
	}
	attribution := options.Goals.AttributionOf(listed[0].Notes, listed[0].GoalWitnessed)
	if !attribution.Resolved() || attribution.Goal.ArtifactID != "v1-goals" {
		t.Fatalf("the decomposition child does not resolve to a goal: %#v\nnotes:\n%s", attribution, listed[0].Notes)
	}
	// The provenance and the goal are one note, and the goal is the part that has
	// been lost before: an item recording only who created it says nothing about
	// what the work is for.
	if !strings.Contains(listed[0].Notes, "Created under yoyodyne-ifd.102, decomposing it") {
		t.Fatalf("the child lost its provenance:\n%s", listed[0].Notes)
	}
	// And the tracker was told, outside those notes, that a goal was written here.
	// That is what survives somebody replacing them, and it is why the loss would
	// be reported next time rather than read as work nobody has attributed yet.
	if !listed[0].GoalWitnessed {
		t.Fatalf("the creation left no witness that a goal was recorded: %#v", listed[0])
	}
	wiped := beads.WorkItem{Notes: "Constraints from the architect.", GoalWitnessed: listed[0].GoalWitnessed}
	if state := options.Goals.AttributionOf(wiped.Notes, wiped.GoalWitnessed).State; state != goal.StateLost {
		t.Fatalf("replacing the child's notes reads as %q rather than a lost attribution", state)
	}
}

// recordingBD stands in for the bd binary rather than for the tracker: it keeps
// whatever a creation stored and gives it back on a listing, exactly as bd does
// — verified against a real bd, which returns the notes and metadata a create
// was given both in its own answer and in a later list --json. It invents
// nothing. An item listed with no notes is an item created with none, which is
// what makes the round trip through the command line worth asserting on.
type recordingBD struct {
	args  [][]string
	items []map[string]any
}

func (r *recordingBD) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	r.args = append(r.args, append([]string(nil), command.Args...))
	answer := "[]"
	switch {
	case len(command.Args) > 0 && command.Args[0] == "create":
		item := map[string]any{
			"id":         fmt.Sprintf("yoyodyne-ifd.102.%d", len(r.items)+2),
			"status":     "open",
			"priority":   1,
			"issue_type": "task",
		}
		for _, argument := range command.Args {
			for flag, field := range map[string]string{"--title=": "title", "--description=": "description", "--notes=": "notes"} {
				if value, carried := strings.CutPrefix(argument, flag); carried {
					item[field] = value
				}
			}
			// bd takes an item's whole metadata as JSON at creation and stores what
			// it is given, so it is decoded here rather than pattern-matched: what a
			// listing carries afterwards is that object.
			if value, carried := strings.CutPrefix(argument, "--metadata="); carried {
				var metadata map[string]any
				if err := json.Unmarshal([]byte(value), &metadata); err != nil {
					return execution.ProcessResult{}, err
				}
				item["metadata"] = metadata
			}
		}
		r.items = append(r.items, item)
		encoded, err := json.Marshal(item)
		if err != nil {
			return execution.ProcessResult{}, err
		}
		answer = string(encoded)
	case len(command.Args) > 0 && command.Args[0] == "list":
		encoded, err := json.Marshal(r.items)
		if err != nil {
			return execution.ProcessResult{}, err
		}
		answer = string(encoded)
	}
	return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: answer}, nil
}

// command returns the arguments of the one bd invocation with this verb, or nil
// when there was none.
func (r *recordingBD) command(verb string) []string {
	for _, args := range r.args {
		if len(args) > 0 && args[0] == verb {
			return args
		}
	}
	return nil
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
	attribution := options.Goals.AttributionOf(legacy+"\n\n"+change.AppendNotes, false)
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
		// An item whose notes were replaced by something that did not carry its
		// goal forward. It is not legacy work, and the survey must not offer it as
		// something to attribute afresh: the goal it served is in the record of
		// what was written on it.
		{ID: "yoyodyne-ifd.4", Title: "Overwritten work", Status: "open", Notes: "Constraints from the architect.", GoalWitnessed: true},
	}, goals)

	// The survey is where the product manager would go to attribute the backlog,
	// so it says which items are waiting for one, which carry a claim that is
	// wrong, and which lost what they recorded, without turning the listing into a
	// per-item report.
	for _, want := range []string{
		"1 of 4 name a goal the goals state, 1 name none, 1 name a goal the goals do not state, and 1 recorded a goal and lost it",
		"yoyodyne-ifd.2",
		"yoyodyne-ifd.3",
		"a record to put back rather than work to attribute afresh: yoyodyne-ifd.4",
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
	// An item that lost its goal is said even then, because saying so rests on the
	// tracker's own record of what was written rather than on any goals document.
	lost := renderOpenQueueEvidence([]beads.WorkItem{
		{ID: "yoyodyne-ifd.1", Status: "open"},
		{ID: "yoyodyne-ifd.4", Status: "open", Notes: "Constraints from the architect.", GoalWitnessed: true},
	}, goal.Set{})
	if !strings.Contains(lost, "a record to put back: yoyodyne-ifd.4") {
		t.Fatalf("survey = %q", lost)
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
