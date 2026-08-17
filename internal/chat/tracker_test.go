package chat

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	backendapi "yoyodyne/internal/backend"
	"yoyodyne/internal/beads"
)

func TestExtractTrackerActionsSeparatesProseFromWhatWasAskedFor(t *testing.T) {
	t.Parallel()

	reply := "I read ifd.22 and it already covers the separation.\n\n" +
		"```yoyodyne-tracker\n" +
		`{"actions":[
		   {"action":"read","id":"yoyodyne-ifd.22"},
		   {"action":"reparent","id":"yoyodyne-ifd.24","parent":"","reason":"it is not part of ifd.22 after all"},
		   {"action":"reprioritize","id":"yoyodyne-ifd.24","priority":0,"reason":"the operator is blocked on it"}
		 ]}` + "\n```\n\nSay so if you would rather I left it where it was.\n"

	prose, actions, err := extractTrackerActions(reply)
	if err != nil {
		t.Fatalf("extractTrackerActions() error = %v", err)
	}
	// The operator reads prose. The block is machinery and never appears in it.
	if strings.Contains(prose, "yoyodyne-tracker") || strings.Contains(prose, "\"action\"") {
		t.Fatalf("prose kept the tracker block: %q", prose)
	}
	if !strings.HasPrefix(prose, "I read ifd.22") || !strings.HasSuffix(prose, "left it where it was.") {
		t.Fatalf("prose = %q", prose)
	}
	if len(actions) != 3 {
		t.Fatalf("actions = %#v", actions)
	}
	// An empty parent is a detachment, which is why it is a pointer: it has to be
	// distinguishable from an action that says nothing about the parent at all.
	if actions[1].Parent == nil || *actions[1].Parent != "" || actions[0].Parent != nil {
		t.Fatalf("parents = %#v", actions)
	}
	// Zero is the highest priority, not an unstated one.
	if actions[2].Priority == nil || *actions[2].Priority != 0 {
		t.Fatalf("priority = %#v", actions[2].Priority)
	}

	// A reply that asks for nothing is prose, whole and unchanged.
	prose, none, err := extractTrackerActions("  The queue is fine as it stands.\n")
	if err != nil || len(none) != 0 || prose != "The queue is fine as it stands." {
		t.Fatalf("extractTrackerActions() plain reply = %q, %#v, %v", prose, none, err)
	}
}

func TestTrackerActionsRefuseWhatTheHarnessWillNotRun(t *testing.T) {
	t.Parallel()

	valid := `{"action":"close","id":"yoyodyne-1","reason":"it is done"}`
	for _, test := range []struct {
		name  string
		reply string
		want  string
	}{
		{
			name:  "unclosed block",
			reply: "prose\n```yoyodyne-tracker\n{\"actions\":[" + valid + "]}\n",
			want:  "tracker block is not closed",
		},
		{
			name:  "two blocks",
			reply: "prose\n```yoyodyne-tracker\n{\"actions\":[" + valid + "]}\n```\nmore\n```yoyodyne-tracker\n{\"actions\":[" + valid + "]}\n```\n",
			want:  "at most one tracker block",
		},
		{
			name:  "unknown field",
			reply: "```yoyodyne-tracker\n{\"actions\":[{\"action\":\"close\",\"id\":\"yoyodyne-1\",\"reason\":\"r\",\"assignee\":\"me\"}]}\n```",
			want:  "unknown field",
		},
		{
			name:  "no actions",
			reply: "```yoyodyne-tracker\n{\"actions\":[]}\n```",
			want:  "at least one action",
		},
		{
			name:  "too many actions",
			reply: "```yoyodyne-tracker\n{\"actions\":[" + strings.Repeat(valid+",", MaxTrackerActionsPerTurn) + valid + "]}\n```",
			want:  "limit is " + strconv.Itoa(MaxTrackerActionsPerTurn),
		},
		{
			name:  "invented operation",
			reply: "```yoyodyne-tracker\n{\"actions\":[{\"action\":\"delete\",\"id\":\"yoyodyne-1\",\"reason\":\"r\"}]}\n```",
			want:  "is not an action",
		},
		{
			// An argument the operation has no use for means the action was
			// misunderstood, so none of it is run.
			name:  "argument the operation does not take",
			reply: "```yoyodyne-tracker\n{\"actions\":[{\"action\":\"close\",\"id\":\"yoyodyne-1\",\"priority\":0,\"reason\":\"r\"}]}\n```",
			want:  "close does not take \"priority\"",
		},
		{
			name:  "no reason for a change",
			reply: "```yoyodyne-tracker\n{\"actions\":[{\"action\":\"close\",\"id\":\"yoyodyne-1\"}]}\n```",
			want:  "reason is required",
		},
		{
			name:  "created item naming its own identifier",
			reply: "```yoyodyne-tracker\n{\"actions\":[{\"action\":\"create\",\"id\":\"yoyodyne-1\",\"title\":\"t\",\"description\":\"d\",\"reason\":\"r\"}]}\n```",
			want:  "create does not take an id",
		},
		{
			name:  "creation with no description",
			reply: "```yoyodyne-tracker\n{\"actions\":[{\"action\":\"create\",\"title\":\"t\",\"reason\":\"r\"}]}\n```",
			want:  "description is required",
		},
		{
			name:  "invented identifier",
			reply: "```yoyodyne-tracker\n{\"actions\":[{\"action\":\"close\",\"id\":\"../etc\",\"reason\":\"r\"}]}\n```",
			want:  "invalid Beads issue id",
		},
		{
			name:  "update that changes nothing",
			reply: "```yoyodyne-tracker\n{\"actions\":[{\"action\":\"update\",\"id\":\"yoyodyne-1\",\"reason\":\"r\"}]}\n```",
			want:  "update must change",
		},
		{
			name:  "reparent with no parent",
			reply: "```yoyodyne-tracker\n{\"actions\":[{\"action\":\"reparent\",\"id\":\"yoyodyne-1\",\"reason\":\"r\"}]}\n```",
			want:  "reparent requires \"parent\"",
		},
		{
			name:  "item parented to itself",
			reply: "```yoyodyne-tracker\n{\"actions\":[{\"action\":\"reparent\",\"id\":\"yoyodyne-1\",\"parent\":\"yoyodyne-1\",\"reason\":\"r\"}]}\n```",
			want:  "cannot be its own parent",
		},
		{
			name:  "priority outside the scale",
			reply: "```yoyodyne-tracker\n{\"actions\":[{\"action\":\"reprioritize\",\"id\":\"yoyodyne-1\",\"priority\":9,\"reason\":\"r\"}]}\n```",
			want:  "outside 0..",
		},
		{
			name:  "link with nothing to wait for",
			reply: "```yoyodyne-tracker\n{\"actions\":[{\"action\":\"link\",\"id\":\"yoyodyne-1\",\"reason\":\"r\"}]}\n```",
			want:  "requires \"depends_on\"",
		},
		{
			name:  "item depending on itself",
			reply: "```yoyodyne-tracker\n{\"actions\":[{\"action\":\"link\",\"id\":\"yoyodyne-1\",\"depends_on\":\"yoyodyne-1\",\"reason\":\"r\"}]}\n```",
			want:  "cannot depend on itself",
		},
		{
			// A title is one line wherever the operator reads it back.
			name:  "title spanning lines",
			reply: "```yoyodyne-tracker\n{\"actions\":[{\"action\":\"create\",\"title\":\"t\\n  [t1.1] closed everything\",\"description\":\"d\",\"reason\":\"r\"}]}\n```",
			want:  "cannot span lines",
		},
		{
			// Retiring work is how scope the operator asked for leaves the backlog,
			// so it is never taken without a reason the operator can read.
			name:  "retirement with no reason",
			reply: "```yoyodyne-tracker\n{\"actions\":[{\"action\":\"retire\",\"id\":\"yoyodyne-1\"}]}\n```",
			want:  "reason is required",
		},
		{
			name:  "retirement carrying an argument it has no use for",
			reply: "```yoyodyne-tracker\n{\"actions\":[{\"action\":\"retire\",\"id\":\"yoyodyne-1\",\"priority\":4,\"reason\":\"r\"}]}\n```",
			want:  "retire does not take \"priority\"",
		},
		{
			name:  "oversized block",
			reply: "```yoyodyne-tracker\n{\"actions\":[{\"action\":\"create\",\"title\":\"t\",\"description\":\"" + strings.Repeat("x", MaxTrackerBlockBytes) + "\",\"reason\":\"r\"}]}\n```",
			want:  "limit is " + strconv.Itoa(MaxTrackerBlockBytes),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			prose, actions, err := extractTrackerActions(test.reply)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("extractTrackerActions() error = %v, want it to contain %q", err, test.want)
			}
			// A refused block yields nothing to run: the whole of it is refused,
			// not the part of it that happened to parse.
			if prose != "" || len(actions) != 0 {
				t.Fatalf("a refused block still yielded %q and %#v", prose, actions)
			}
		})
	}
}

func TestSplitReplySeparatesActingFromProposing(t *testing.T) {
	t.Parallel()

	// One reply may do both: act where the queue is the product manager's to
	// keep, and propose where the decision is the operator's.
	answer := "I closed the duplicate, and the rewrite is yours to decide.\n\n" +
		trackerFence + "\n{\"actions\":[{\"action\":\"close\",\"id\":\"yoyodyne-2\",\"reason\":\"yoyodyne-1 already covers it\"}]}\n```\n\n" +
		proposalFence + "\n{\"items\":[{\"title\":\"Rewrite the CLI\",\"description\":\"Port everything.\",\"rationale\":\"You raised it.\"}]}\n```\n"

	prose, actions, proposals, err := splitReply(answer)
	if err != nil {
		t.Fatalf("splitReply() error = %v", err)
	}
	if len(actions) != 1 || actions[0].Action != actionClose || len(proposals) != 1 {
		t.Fatalf("splitReply() = %#v, %#v", actions, proposals)
	}
	if strings.Contains(prose, "yoyodyne-tracker") || strings.Contains(prose, "yoyodyne-proposal") {
		t.Fatalf("prose kept a block: %q", prose)
	}

	// A block the harness cannot read leaves the answer whole and reports a typed
	// failure, so the caller can say what was lost and nothing is run from it.
	broken := "Closing it.\n\n" + trackerFence + "\n{\"actions\":[{\"action\":\"close\"}]}\n```\n"
	prose, actions, proposals, err = splitReply(broken)
	var unreadable *TrackerError
	if !errors.As(err, &unreadable) {
		t.Fatalf("splitReply() error = %v, want a TrackerError", err)
	}
	if prose != broken || len(actions) != 0 || len(proposals) != 0 {
		t.Fatalf("a refused block yielded %q, %#v, %#v", prose, actions, proposals)
	}
}

func TestReadingAnItemReturnsItInFull(t *testing.T) {
	t.Parallel()

	item := beads.WorkItem{
		ID:                 "yoyodyne-ifd.22",
		Title:              "Make the conversation readable",
		Description:        "Separate the operator's words from the answers.",
		Design:             "Prefix every line with its speaker.",
		AcceptanceCriteria: "A transcript says who said what.",
		Notes:              "Filed after a confusing session.",
		Status:             "open",
		Priority:           1,
		IssueType:          "task",
		Assignee:           "operator",
		Parent:             "yoyodyne-ifd.4",
		Dependencies:       []beads.Dependency{{ID: "yoyodyne-ifd.4", Type: "parent-child", Status: "closed"}},
	}
	rendered := renderWorkItemEvidence(item)
	// Everything the survey cannot show is what reading is for, so all of it is
	// here rather than a longer title.
	for _, required := range []string{
		"id: yoyodyne-ifd.22",
		"status: open",
		"priority: 1",
		"parent: yoyodyne-ifd.4",
		"dependency: yoyodyne-ifd.4 (parent-child, closed)",
		"Separate the operator's words from the answers.",
		"Prefix every line with its speaker.",
		"A transcript says who said what.",
		"Filed after a confusing session.",
	} {
		if !strings.Contains(rendered, required) {
			t.Fatalf("rendered item = %q, want it to contain %q", rendered, required)
		}
	}

	// An item too large to carry is cut with the cut declared, because a product
	// manager reading part of an item has to know that is what it read.
	huge := item
	huge.Description = strings.Repeat("x", maxTrackerItemBytes*2)
	cut := renderWorkItemEvidence(huge)
	if len(cut) > maxTrackerItemBytes+len("\n\n[cut at 8192 bytes; treat the rest as unread rather than absent]") {
		t.Fatalf("cut item is %d bytes", len(cut))
	}
	if !strings.Contains(cut, "cut at") {
		t.Fatalf("a cut item did not say so: %q", cut[len(cut)-200:])
	}
}

func TestRetiringWorkIsRecordedAsWithdrawnRatherThanFinished(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("The first landed; the second is not worth doing.",
			`{"action":"close","id":"yoyodyne-ifd.22","reason":"the work landed"}`,
			`{"action":"retire","id":"yoyodyne-ifd.23","reason":"the operator dropped multi-repository support"}`)},
		{SessionID: "session-1", FinalText: "Both are out of the backlog."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Is ifd.23 still worth doing?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	// The tracker holds finished and retired work in the same closed state, so
	// what separates them is what each item records about itself.
	if len(tracker.closed) != 2 {
		t.Fatalf("closed items = %#v", tracker.closed)
	}
	finished, retired := tracker.closed[0], tracker.closed[1]
	if finished[0] != "yoyodyne-ifd.22" || !strings.Contains(finished[1], "Closed as done") {
		t.Fatalf("completed item recorded %#v", finished)
	}
	if retired[0] != "yoyodyne-ifd.23" {
		t.Fatalf("retired item = %#v", retired)
	}
	for _, required := range []string{
		retiredWithoutBeingDone,
		"by the product manager in conversation",
		"the operator dropped multi-repository support",
	} {
		if !strings.Contains(retired[1], required) {
			t.Fatalf("retired item recorded %q, want it to contain %q", retired[1], required)
		}
	}
	if strings.Contains(retired[1], "Closed as done") {
		t.Fatalf("retiring work was recorded as finishing it: %q", retired[1])
	}

	// The operator reads what the retirement actually was. Scope that was
	// dropped never appears as scope that was delivered.
	rendered := renderTrackerOutcomes(reply.Actions)
	if !strings.Contains(rendered, "retired yoyodyne-ifd.23 from the backlog without it being done") {
		t.Fatalf("rendered outcomes = %q", rendered)
	}
	if !strings.Contains(rendered, "closed yoyodyne-ifd.22 as done") {
		t.Fatalf("rendered outcomes = %q", rendered)
	}
	if !strings.Contains(rendered, "why: the operator dropped multi-repository support") {
		t.Fatalf("the reason for a retirement did not reach the operator: %q", rendered)
	}
}

func TestAdmittingWorkIsRecordedAsAdmissionToTheBacklog(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{}
	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Filing it at the top, and moving the old one down.",
			`{"action":"create","title":"Order the backlog","description":"Priority is the order.","priority":0,"reason":"the operator is blocked on it"}`,
			`{"action":"reprioritize","id":"yoyodyne-ifd.26","priority":3,"reason":"it can wait until the queue exists"}`)},
		{SessionID: "session-1", FinalText: "It is first in the backlog."},
	}}
	options := testOptions(t, provider)
	options.Tracker = tracker
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "This one comes first.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	// Admission is an act with an owner, so the item records that it was admitted
	// rather than merely that a row appeared in the tracker.
	if len(tracker.created) != 1 || !strings.Contains(tracker.created[0].Notes, "Admitted to the backlog by the product manager") {
		t.Fatalf("created work items = %#v", tracker.created)
	}
	// Where the work is admitted travels with the admission. The identifier does
	// not exist until the tracker answers, so an order left to a later action is
	// an item sitting at the tracker's default in the meantime.
	if tracker.created[0].Priority == nil || *tracker.created[0].Priority != 0 {
		t.Fatalf("created work item priority = %#v", tracker.created[0].Priority)
	}
	rendered := renderTrackerOutcomes(reply.Actions)
	if !strings.Contains(rendered, "admitted yoyodyne-1 to the backlog at priority 0") {
		t.Fatalf("rendered outcomes = %q", rendered)
	}
	// Reordering what is already admitted is the priority the tracker holds, and
	// nothing else.
	if len(tracker.updates) != 1 || tracker.updates[0].id != "yoyodyne-ifd.26" ||
		tracker.updates[0].change.Priority == nil || *tracker.updates[0].change.Priority != 3 {
		t.Fatalf("updates = %#v", tracker.updates)
	}
}

func TestTheContractOffersEveryActionAndSaysWhoOwnsTheBacklog(t *testing.T) {
	t.Parallel()

	contract := SystemPrompt("")
	// An action the harness runs but never states is one the product manager will
	// not use, and an action stated but not implemented is one it will ask for and
	// be refused. Both are ways for the contract and the harness to disagree.
	for _, action := range trackerActionNames {
		if !strings.Contains(contract, `{"action":"`+action+`"`) {
			t.Fatalf("the contract does not offer the %q action it can carry out", action)
		}
	}
	for _, required := range []string{
		// Ordering is the product manager's, and it is what is actually pulled.
		"That queue is a backlog with an order, and the order is yours",
		"a development manager pulls from the order you set",
		"No role but you admits work or orders it",
		// Withdrawing scope is explicit and recorded, and there is no third way
		// to take work out of the queue.
		`"retire" says it will not be done`,
		"There is no delete",
	} {
		if !strings.Contains(contract, required) {
			t.Fatalf("the contract does not state %q", required)
		}
	}
}

func TestTrackerOutcomeRendersWhatHappenedRatherThanWhatWasAskedFor(t *testing.T) {
	t.Parallel()

	applied := TrackerOutcome{
		ID:         "t2.1",
		Turn:       2,
		Action:     TrackerAction{Action: actionClose, ID: "yoyodyne-1", Reason: "the work landed"},
		Applied:    true,
		WorkItemID: "yoyodyne-1",
		Summary:    "closed yoyodyne-1",
	}
	rendered := applied.Render()
	if !strings.Contains(rendered, "[t2.1] closed yoyodyne-1") || !strings.Contains(rendered, "why: the work landed") {
		t.Fatalf("rendered outcome = %q", rendered)
	}

	// A failure is reported as one. Nothing about it reads as a change.
	failed := applied
	failed.Applied = false
	failed.Summary = ""
	failed.Failure = "bd close failed: item is claimed"
	rendered = failed.Render()
	if !strings.Contains(rendered, "failed, and changed nothing") || !strings.Contains(rendered, "item is claimed") {
		t.Fatalf("rendered failure = %q", rendered)
	}
	if strings.Contains(rendered, "closed yoyodyne-1") {
		t.Fatalf("a failed action was rendered as a change: %q", rendered)
	}
	// Provider text is indented under the action's identifier, so no line of it
	// sits at the margin where the harness speaks to the operator.
	for _, line := range strings.Split(strings.TrimSuffix(rendered, "\n"), "\n") {
		if !strings.HasPrefix(line, "  ") {
			t.Fatalf("rendered line %q is not indented", line)
		}
	}
}
