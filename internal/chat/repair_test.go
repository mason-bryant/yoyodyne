package chat

import (
	"context"
	"errors"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/backlogrepair"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// A status left over from a stoppage that ended is corrected without anybody
// invoking a verb, and the item says afterwards what was changed, what made the
// old state stale, and why — which is what makes a correction made wrongly
// something a reader finds rather than something that happened quietly.
func TestClearingAStaleStatusRecordsWhatItCorrectedAndWhy(t *testing.T) {
	t.Parallel()

	stale := beads.WorkItem{ID: "yoyodyne-ifd.60", Title: "Its blocker landed", Status: "blocked",
		Dependencies: []beads.Dependency{{ID: "yoyodyne-ifd.4", Type: beads.BlocksDependency, Status: "closed"}}}
	tracker := &fakeTracker{
		items:        map[string]beads.WorkItem{stale.ID: stale},
		blockedItems: []beads.WorkItem{stale},
	}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Its blocker closed weeks ago; clearing the status.",
			`{"action":"repair","id":"yoyodyne-ifd.60","state":"status","reason":"nothing unfinished is behind it and the queue is passing it over"}`)},
		{SessionID: "session-1", FinalText: "Cleared, and it is back in the order."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	options.Held = readHolds(nil)
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Tidy the queue.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	if len(tracker.unblocked) != 1 || tracker.unblocked[0][0] != stale.ID {
		t.Fatalf("cleared = %#v, want the stale item's status cleared once", tracker.unblocked)
	}
	note := tracker.unblocked[0][1]
	for _, want := range []string{
		"Blocked status cleared as stale",
		"What made the old state stale:",
		"nothing unfinished blocks it",
		"nothing unfinished is behind it and the queue is passing it over",
	} {
		if !strings.Contains(note, want) {
			t.Errorf("the note recorded on the item is %q, want it to say %q", note, want)
		}
	}
}

// The dependency and the attribution are corrected the same way, each against
// the record that says the old state stopped being true.
func TestTheOtherTwoKindsOfStaleStateAreCorrectedAgainstTheirOwnRecords(t *testing.T) {
	t.Parallel()

	t.Run("a dependency on work that closed", func(t *testing.T) {
		t.Parallel()

		item := beads.WorkItem{ID: "yoyodyne-ifd.61", Title: "Waiting on nothing", Status: "open",
			Dependencies: []beads.Dependency{{ID: "yoyodyne-ifd.5", Type: beads.BlocksDependency, Status: "closed"}}}
		closed := beads.WorkItem{ID: "yoyodyne-ifd.5", Title: "Done", Status: "closed"}
		tracker := &fakeTracker{
			items: map[string]beads.WorkItem{item.ID: item, closed.ID: closed},
			open:  []beads.WorkItem{item},
		}
		provider := &fakeBackend{results: []backendapi.RunResult{
			{SessionID: "session-1", FinalText: trackerReply("The work it waits for is done.",
				`{"action":"repair","id":"yoyodyne-ifd.61","state":"dependency","depends_on":"yoyodyne-ifd.5","reason":"the link is the last thing saying it waits"}`)},
			{SessionID: "session-1", FinalText: "Retired the link."},
		}}
		options := testOptions(t, provider)
		options.Tracker = tracker
		options.Held = readHolds(nil)
		session := openTestSession(t, options)

		reply, err := session.Send(context.Background(), "Tidy the queue.")
		if err != nil {
			t.Fatalf("Send() error = %v", err)
		}
		if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
			t.Fatalf("actions = %#v", reply.Actions)
		}
		if len(tracker.unlinks) != 1 || tracker.unlinks[0] != [2]string{item.ID, closed.ID} {
			t.Fatalf("unlinks = %#v, want the dead link retired", tracker.unlinks)
		}
		if len(tracker.updates) != 1 || !strings.Contains(tracker.updates[0].change.AppendNotes, "which is closed") {
			t.Fatalf("updates = %#v, want the item to record why the link was dead", tracker.updates)
		}
	})

	t.Run("an attribution the goals no longer state", func(t *testing.T) {
		t.Parallel()

		item := beads.WorkItem{ID: "yoyodyne-ifd.62", Title: "Attributed before the goal was reworded", Status: "open",
			Notes: "Goal served: Run development almost without a person."}
		tracker := &fakeTracker{items: map[string]beads.WorkItem{item.ID: item}, open: []beads.WorkItem{item}}
		provider := &fakeBackend{results: []backendapi.RunResult{
			{SessionID: "session-1", FinalText: trackerReply("The goal was reworded; the item still names the old wording.",
				`{"action":"repair","id":"yoyodyne-ifd.62","state":"attribution","goal":"Run development nearly autonomously","reason":"the amendment reworded the goal this has always served"}`)},
			{SessionID: "session-1", FinalText: "Re-attributed."},
		}}
		options := testOptions(t, provider)
		options.Tracker = tracker
		options.Held = readHolds(nil)
		options.Goals = recordedGoals("Run development nearly autonomously")
		session := openTestSession(t, options)

		reply, err := session.Send(context.Background(), "Tidy the queue.")
		if err != nil {
			t.Fatalf("Send() error = %v", err)
		}
		if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
			t.Fatalf("actions = %#v", reply.Actions)
		}
		if len(tracker.updates) != 1 {
			t.Fatalf("updates = %#v, want one re-attribution", tracker.updates)
		}
		notes := tracker.updates[0].change.AppendNotes
		for _, want := range []string{"the goals no longer state it", "Run development nearly autonomously"} {
			if !strings.Contains(notes, want) {
				t.Errorf("the note recorded on the item is %q, want it to say %q", notes, want)
			}
		}
	})
}

// The boundary the whole capability is bounded by: work somebody still has to
// release is reported with its reason and left exactly as it is, however stale
// its state looks.
func TestARepairOfHeldWorkChangesNothing(t *testing.T) {
	t.Parallel()

	stopped := beads.WorkItem{ID: "yoyodyne-ifd.63", Title: "Its change is still on a branch", Status: "blocked"}
	tracker := &fakeTracker{
		items:        map[string]beads.WorkItem{stopped.ID: stopped},
		blockedItems: []beads.WorkItem{stopped},
	}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Nothing unfinished blocks it as far as I can see.",
			`{"action":"repair","id":"yoyodyne-ifd.63","state":"status","reason":"the queue has been passing it over"}`)},
		{SessionID: "session-1", FinalText: "It is held; I left it alone."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	options.Held = readHolds(map[string]string{
		stopped.ID: "run run-9 stopped on it and its change is preserved, so a fresh run would start over on top of work that is still there",
	})
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Tidy the queue.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the repair refused", reply.Actions)
	}
	if !strings.Contains(reply.Actions[0].Failure, "its change is preserved") {
		t.Fatalf("failure = %q, want it to restate what somebody has to release", reply.Actions[0].Failure)
	}
	if len(tracker.unblocked) != 0 || len(tracker.updates) != 0 {
		t.Fatalf("a held item was written to: cleared %#v, updated %#v", tracker.unblocked, tracker.updates)
	}
}

// A conversation with no reading of what is held corrects nothing, rather than
// correcting everything it cannot see a hold on.
func TestARepairWithNothingToReadTheHoldsFromChangesNothing(t *testing.T) {
	t.Parallel()

	stale := beads.WorkItem{ID: "yoyodyne-ifd.64", Title: "Blocked by nothing", Status: "blocked"}
	tracker := &fakeTracker{
		items:        map[string]beads.WorkItem{stale.ID: stale},
		blockedItems: []beads.WorkItem{stale},
	}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Clearing it.",
			`{"action":"repair","id":"yoyodyne-ifd.64","state":"status","reason":"nothing unfinished is behind it"}`)},
		{SessionID: "session-1", FinalText: "It could not be judged."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Tidy the queue.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the repair refused", reply.Actions)
	}
	if len(tracker.unblocked) != 0 {
		t.Fatalf("a status was cleared with nothing to read the holds from: %#v", tracker.unblocked)
	}
}

// The staleness is the harness's judgement over the records as they stand as the
// act runs, not the role's assertion about a listing it read earlier.
func TestARepairTheRecordsDoNotSupportChangesNothing(t *testing.T) {
	t.Parallel()

	waiting := beads.WorkItem{ID: "yoyodyne-ifd.65", Title: "Genuinely waiting", Status: "blocked",
		Dependencies: []beads.Dependency{{ID: "yoyodyne-ifd.66", Type: beads.BlocksDependency, Status: "open"}}}
	blocker := beads.WorkItem{ID: "yoyodyne-ifd.66", Title: "The work it waits for", Status: "open"}
	tracker := &fakeTracker{
		items:        map[string]beads.WorkItem{waiting.ID: waiting, blocker.ID: blocker},
		open:         []beads.WorkItem{blocker},
		blockedItems: []beads.WorkItem{waiting},
	}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("The listing I have says its blocker landed.",
			`{"action":"repair","id":"yoyodyne-ifd.65","state":"status","reason":"the queue is passing it over"}`)},
		{SessionID: "session-1", FinalText: "It is genuinely waiting after all."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	options.Held = readHolds(nil)
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Tidy the queue.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the repair refused", reply.Actions)
	}
	if !strings.Contains(reply.Actions[0].Failure, "yoyodyne-ifd.66") {
		t.Fatalf("failure = %q, want it to name the unfinished work behind the status", reply.Actions[0].Failure)
	}
	if len(tracker.unblocked) != 0 {
		t.Fatalf("a status the records still call right was cleared: %#v", tracker.unblocked)
	}
}

// The pass that corrects this is the pass that takes a survey, so the survey is
// where both lists have to be: what may be corrected, and what is held with the
// reason restated.
func TestASurveyListsTheStaleStateAndWhatIsHeld(t *testing.T) {
	t.Parallel()

	open := beads.WorkItem{ID: "yoyodyne-ifd.67", Title: "A dead link", Status: "open",
		Dependencies: []beads.Dependency{{ID: "yoyodyne-ifd.6", Type: beads.BlocksDependency, Status: "closed"}}}
	held := beads.WorkItem{ID: "yoyodyne-ifd.68", Title: "Its change is on a branch", Status: "blocked"}
	tracker := &fakeTracker{
		items:        map[string]beads.WorkItem{open.ID: open, held.ID: held},
		open:         []beads.WorkItem{open},
		blockedItems: []beads.WorkItem{held},
	}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Taking a survey before I correct anything.", `{"action":"survey"}`)},
		{SessionID: "session-1", FinalText: "One link to retire, one item held."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	options.Held = readHolds(map[string]string{
		held.ID: "run run-11 stopped on it and its change is preserved",
	})
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "What is stale?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	detail := reply.Actions[0].Detail
	for _, want := range []string{
		"State the records have made stale",
		"yoyodyne-ifd.67 [dependency]",
		"Held for a person",
		"yoyodyne-ifd.68 [status]",
		"its change is preserved",
	} {
		if !strings.Contains(detail, want) {
			t.Errorf("the survey says %q, want it to carry %q", detail, want)
		}
	}
}

// A survey read by a role that cannot correct any of this says nothing about it:
// stale state in front of a role with no authority over it is work to route to
// somebody else, which is the relaying this was meant to end.
func TestASurveyByARoleThatMayNotRepairSaysNothingAboutStaleState(t *testing.T) {
	t.Parallel()

	stale := beads.WorkItem{ID: "yoyodyne-ifd.69", Title: "A dead link", Status: "open",
		Dependencies: []beads.Dependency{{ID: "yoyodyne-ifd.7", Type: beads.BlocksDependency, Status: "closed"}}}
	tracker := &fakeTracker{items: map[string]beads.WorkItem{stale.ID: stale}, open: []beads.WorkItem{stale}}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Surveying.", `{"action":"survey"}`)},
		{SessionID: "session-1", FinalText: "One open item."},
	}}
	options := testOptions(t, provider)
	options.Role = domain.RoleDevelopmentManager
	options.Agent = string(domain.RoleDevelopmentManager)
	options.Tracker = tracker
	options.Held = readHolds(nil)
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "What is open?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if strings.Contains(reply.Actions[0].Detail, "State the records have made stale") {
		t.Fatalf("the development manager's survey carries state it cannot correct: %q", reply.Actions[0].Detail)
	}
	if slices := tracker.listed; len(slices) != 1 || slices[0] != openWorkItemStatus {
		t.Fatalf("statuses surveyed = %#v, want the open items alone", tracker.listed)
	}
}

// Correcting backlog state is the product manager's, and a role that does not
// hold the capability is refused before anything is carried out.
func TestOnlyARoleHoldingTheCapabilityMayRepair(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{items: map[string]beads.WorkItem{}}
	provider := &fakeBackend{results: []backendapi.RunResult{{SessionID: "session-1",
		FinalText: trackerReply("Clearing it.",
			`{"action":"repair","id":"yoyodyne-ifd.70","state":"status","reason":"it looks stale"}`)}}}
	options := testOptions(t, provider)
	options.Role = domain.RoleDevelopmentManager
	options.Agent = string(domain.RoleDevelopmentManager)
	options.Tracker = tracker
	options.Held = readHolds(nil)
	session := openTestSession(t, options)

	_, err := session.Send(context.Background(), "Tidy the queue.")
	var refusal *AuthorityError
	if !errors.As(err, &refusal) {
		t.Fatalf("Send() error = %v, want an authority refusal", err)
	}
	if len(tracker.unblocked) != 0 {
		t.Fatalf("a refused repair still wrote to the tracker: %#v", tracker.unblocked)
	}
}

// An argument the named state has no use for is refused rather than ignored: a
// repair that carried the goal for a link it was retiring was misunderstood, and
// correcting the part that parsed would correct something nobody named.
func TestARepairIsRefusedWhereItDoesNotSayWhatItIsCorrecting(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name   string
		action TrackerAction
		says   string
	}{
		{
			name:   "naming no state",
			action: TrackerAction{Action: actionRepair, ID: "yoyodyne-ifd.71", Reason: "why"},
			says:   "repair requires \"state\"",
		},
		{
			name:   "naming a state nothing recognizes",
			action: TrackerAction{Action: actionRepair, ID: "yoyodyne-ifd.71", State: "priority", Reason: "why"},
			says:   "is not a kind of stale state",
		},
		{
			name:   "retiring a link without naming it",
			action: TrackerAction{Action: actionRepair, ID: "yoyodyne-ifd.71", State: backlogrepair.ClassDependency, Reason: "why"},
			says:   "requires \"depends_on\"",
		},
		{
			name: "re-attributing without a goal",
			action: TrackerAction{Action: actionRepair, ID: "yoyodyne-ifd.71",
				State: backlogrepair.ClassAttribution, Reason: "why"},
			says: "goal",
		},
		{
			name: "clearing a status and naming a goal",
			action: TrackerAction{Action: actionRepair, ID: "yoyodyne-ifd.71", State: backlogrepair.ClassStatus,
				Goal: "Run development nearly autonomously", Reason: "why"},
			says: "takes no \"goal\"",
		},
		{
			name:   "correcting a state with no reason",
			action: TrackerAction{Action: actionRepair, ID: "yoyodyne-ifd.71", State: backlogrepair.ClassStatus},
			says:   "reason",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			err := testCase.action.Validate()
			if err == nil {
				t.Fatalf("Validate() accepted %#v", testCase.action)
			}
			if !strings.Contains(err.Error(), testCase.says) {
				t.Fatalf("Validate() refused with %q, want it to say %q", err, testCase.says)
			}
		})
	}
}

// readHolds is what the harness is holding for a person, as a conversation
// reads it. A nil map is a reading that found nothing held, which is not the
// same answer as no reading at all.
func readHolds(reasons map[string]string) HeldWork {
	return fakeHeldWork{holds: backlog.ReadHolds(reasons)}
}

type fakeHeldWork struct {
	holds backlog.Holds
	err   error
}

func (f fakeHeldWork) HeldForAPerson(context.Context) (backlog.Holds, error) {
	if f.err != nil {
		return backlog.Holds{}, f.err
	}
	return f.holds, nil
}
