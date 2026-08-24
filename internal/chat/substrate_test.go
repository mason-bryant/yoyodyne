package chat

// The case this exists for, replayed.
//
// On 2026-08-23 a child was carved from a failed run's deferral. It assumed two
// files that existed only on that run's branch — the pull request for it open,
// the item's repair budget spent — and nothing on the child recorded the
// prerequisite, so the tracker reported it ready. These are that case and the
// states around it: the change that landed, the tracker that would not take the
// link, and the records that could not be read.

import (
	"context"
	"errors"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// The parent of the founding case: the item whose run stopped with its change
// on a preserved branch and its pull request still open.
const substrateParent = "yoyodyne-ifd.100"

// carvedChild is the decomposition the founding case produced: a child written
// against files that exist only on the parent's branch.
const carvedChild = `{"action":"create","title":"Carry the write through the document path","description":"Assumes internal/artifact/write.go and internal/chat/document.go.","goal":"` + recordedGoal + `","parent":"` + substrateParent + `","priority":2,"reason":"the reviewer refused this half as out of scope"}`

// What the item is for: a child carved from a run whose change never landed
// waits for that change, and says on itself why.
func TestAChildCarvedFromAnUnlandedChangeWaitsForIt(t *testing.T) {
	t.Parallel()

	tracker := substrateTracker()
	reply := substrateReply(t, tracker, unlandedParent(), trackerReply("Splitting out the refused half.", carvedChild))
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	created := reply.Actions[0].WorkItemID
	// The link is what makes the item unready, and it is the tracker's own
	// dependency graph that then reports it so — which is the whole of the fix:
	// nothing has to read the prose to know the child cannot be pulled.
	if len(tracker.links) != 1 || tracker.links[0] != [2]string{created, substrateParent} {
		t.Fatalf("links = %#v, want %s waiting for %s", tracker.links, created, substrateParent)
	}
	// And the item says why, so a reader finds a missing substrate rather than an
	// unexplained sequencing decision.
	if len(tracker.updates) != 1 || tracker.updates[0].id != created {
		t.Fatalf("updates = %#v", tracker.updates)
	}
	notes := tracker.updates[0].change.AppendNotes
	for _, want := range []string{
		"Held for " + substrateParent + "'s change to land",
		"never reached main",
		"yoyodyne/ifd-100",
		"pull request #174",
		"the preserved branch cherry-picked, the pull request revived, or the substrate rebuilt",
	} {
		if !strings.Contains(notes, want) {
			t.Fatalf("the child's notes are missing %q:\n%s", want, notes)
		}
	}
	rendered := renderTrackerOutcomes(domain.RoleDevelopmentManager, reply.Actions)
	if !strings.Contains(rendered, "it waits for "+substrateParent) {
		t.Fatalf("the operator was not told the child is held:\n%s", rendered)
	}
}

// The other half of the same rule: ordinary decomposition is untouched. A parent
// whose change is on the target branch leaves its children standing on
// something, so nothing is added to them and the development manager's own
// dependency structure is the whole of what they carry.
func TestAChildOfWorkWhoseChangeLandedIsNotHeld(t *testing.T) {
	t.Parallel()

	tracker := substrateTracker()
	reply := substrateReply(t, tracker, &fakeStoppedRuns{}, trackerReply("Breaking the rest out.", carvedChild))
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	if len(tracker.links) != 0 || len(tracker.updates) != 0 || len(tracker.blocked) != 0 {
		t.Fatalf("an ordinary decomposition was gated: links %#v, updates %#v, blocked %#v",
			tracker.links, tracker.updates, tracker.blocked)
	}
	if summary := reply.Actions[0].Summary; strings.Contains(summary, "waits for") {
		t.Fatalf("the operator was told an ungated child was held: %s", summary)
	}
}

// A tracker that will not record the dependency leaves the one state this must
// never produce: a child in the queue, written against files that are not there,
// with nothing holding it back. So the hold lands on the item itself instead.
func TestAChildTheTrackerWillNotLinkIsBlockedOnTheItemInstead(t *testing.T) {
	t.Parallel()

	tracker := substrateTracker()
	tracker.linkErr = errors.New("bd dep add failed: the tracker is locked")
	reply := substrateReply(t, tracker, unlandedParent(), trackerReply("Splitting out the refused half.", carvedChild))
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	created := reply.Actions[0].WorkItemID
	if len(tracker.blocked) != 1 || tracker.blocked[0][0] != created {
		t.Fatalf("blocked = %#v, want %s blocked on the item itself", tracker.blocked, created)
	}
	summary := reply.Actions[0].Summary
	for _, want := range []string{
		"could not be linked to wait for " + substrateParent,
		"it was blocked on the item itself instead",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("the summary is missing %q: %s", want, summary)
		}
	}
}

// Records that cannot be read establish nothing, so the gate says so rather than
// letting the creation read as one it examined and cleared. An unheld child
// nobody was told about is the failure this whole file is about.
func TestADecompositionWhoseSubstrateCannotBeReadSaysSo(t *testing.T) {
	t.Parallel()

	tracker := substrateTracker()
	stoppages := unlandedParent()
	stoppages.unreadable = substrateParent
	reply := substrateReply(t, tracker, stoppages, trackerReply("Splitting out the refused half.", carvedChild))
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	if len(tracker.links) != 0 {
		t.Fatalf("links = %#v, want none from records nothing could be read from", tracker.links)
	}
	if summary := reply.Actions[0].Summary; !strings.Contains(summary, "may be pulled without its substrate") {
		t.Fatalf("the summary did not say the child is unheld: %s", summary)
	}
}

// unlandedParent is the founding case's run records: ifd.100's change, on the
// branch its stopped run preserved, published and unmerged.
func unlandedParent() *fakeStoppedRuns {
	return &fakeStoppedRuns{unlanded: map[string]UnlandedChange{
		substrateParent: {
			RunID:        "run-0123456789abcdef0123456789abcdef",
			Branch:       "yoyodyne/ifd-100",
			TargetBranch: "main",
			PullRequest:  174,
		},
	}}
}

func substrateTracker() *fakeTracker {
	return &fakeTracker{items: map[string]beads.WorkItem{
		substrateParent: {ID: substrateParent, Title: "the item whose run stopped", Status: "open"},
	}}
}

// substrateReply is a development manager decomposing, with the run records
// wired the way the command line wires them for that role and for no other.
func substrateReply(t *testing.T, tracker Tracker, stoppages Stoppages, answer string) Reply {
	t.Helper()

	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: answer},
		{SessionID: "session-1", ResolvedModel: "claude-opus-5-20260514", FinalText: "That is what I filed."},
	}})
	options.Role = domain.RoleDevelopmentManager
	options.Agent = string(domain.RoleDevelopmentManager)
	options.Tracker = tracker
	options.Goals = recordedGoals(recordedGoal)
	options.Stoppages = stoppages
	reply, err := openTestSession(t, options).Send(context.Background(), "Decompose what the run left.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	return reply
}
