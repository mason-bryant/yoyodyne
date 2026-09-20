package chat

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/recovery"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// killedWrite is what internal/beads formats for a bd the harness stopped
// waiting on: the status and the exit code a killed process reports, with the
// colon the recovery rule matches on. It is the exact string the 2026-09-06
// write on yoyodyne-ifd.142 failed with, message and all.
const killedWrite = "bd update failed with status timed_out and exit code -1: "

// contendedTracker fails the first few calls of one verb the way a contended
// store does and then answers, which is the shape the recovery rule exists for:
// a failure the next attempt survives. Every other verb answers at once, so the
// waits a test counts are the contended verb's own.
type contendedTracker struct {
	*fakeTracker
	verb     string
	failures int
	failure  error
	calls    int
}

func (c *contendedTracker) contended(verb string) error {
	if verb != c.verb {
		return nil
	}
	c.calls++
	if c.calls <= c.failures {
		return c.failure
	}
	return nil
}

func (c *contendedTracker) List(ctx context.Context, status string) ([]beads.WorkItem, error) {
	if err := c.contended("list"); err != nil {
		return nil, err
	}
	return c.fakeTracker.List(ctx, status)
}

func (c *contendedTracker) Update(ctx context.Context, id string, change beads.WorkItemChange) (beads.WorkItem, error) {
	if err := c.contended("update"); err != nil {
		return beads.WorkItem{}, err
	}
	return c.fakeTracker.Update(ctx, id, change)
}

// recordingSleep drives a wait without spending it and keeps what it was asked
// for, so the backoff is an assertion rather than a claim.
func recordingSleep(sleeps *[]time.Duration) func(context.Context, time.Duration) error {
	return func(_ context.Context, duration time.Duration) error {
		*sleeps = append(*sleeps, duration)
		return nil
	}
}

// The rule itself, on the path that lost yoyodyne-ifd.142's re-run recording: a
// conversation's write that fails the way a killed bd does is waited out on the
// pipeline's backoff and asked again, and the action is reported as applied with
// the waits named, rather than reported as failed on the first reset.
func TestAConversationTrackerWriteThatFailsRecoverablyIsAskedAgainOnTheBackoff(t *testing.T) {
	t.Parallel()

	tracker := &contendedTracker{
		fakeTracker: &fakeTracker{items: map[string]beads.WorkItem{
			"yoyodyne-ifd.142": {ID: "yoyodyne-ifd.142", Title: "the item whose write was contended", Status: "open"},
		}},
		// The read that precedes the write answers; the write itself fails three
		// times and then lands.
		verb:     "update",
		failures: 3,
		failure:  errors.New(killedWrite),
	}
	var sleeps []time.Duration
	root := t.TempDir()
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Noting it.",
			`{"action":"update","id":"yoyodyne-ifd.142","note":"the store was contended","reason":"so the item says so"}`)},
		{SessionID: "session-1", FinalText: "The note is on it."},
	}})
	options.Tracker = tracker
	options.Store = newTestStore(t, root)
	options.Sleep = recordingSleep(&sleeps)
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "note it")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the write applied once the store answered", reply.Actions)
	}
	// Three failures, three waits on the operator's series, and the fourth
	// attempt landed: the store saw four writes and kept one.
	if want := []time.Duration{time.Second, time.Second, 2 * time.Second}; !equalDurations(sleeps, want) {
		t.Fatalf("waits = %v, want the Fibonacci series %v", sleeps, want)
	}
	if tracker.calls != 4 {
		t.Fatalf("writes = %d, want the write asked for four times", tracker.calls)
	}
	// The operator is told the store had to be asked again, in the action's own
	// line, because a store that had to be asked three times is one they should
	// hear about before it has to be asked twenty.
	if !strings.Contains(reply.Actions[0].Summary, "asked 3 further time(s) over 4s first") {
		t.Fatalf("summary = %q, want the waits named on the applied action", reply.Actions[0].Summary)
	}
	// And every wait is on the conversation's record, written before it was
	// taken, with the attempt, the interval, and the failure in bd's own words.
	var retried []map[string]any
	for _, event := range loadTestEvents(t, root, session) {
		if event.Type != execution.EventTrackerRetried {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatalf("decode %s payload: %v", event.Type, err)
		}
		retried = append(retried, payload)
	}
	if len(retried) != 3 {
		t.Fatalf("tracker.retried events = %d, want one per wait", len(retried))
	}
	for i, payload := range retried {
		if payload["attempt"] != float64(i+1) || payload["boundary"] != runstate.RetryTracker ||
			payload["delay_seconds"] != float64(recovery.Interval(i+1)/time.Second) ||
			!strings.Contains(payload["failure"].(string), "timed_out") {
			t.Fatalf("tracker.retried event %d = %#v", i+1, payload)
		}
	}
}

// A read that gates a write is retried the same way, and the write it gates
// goes ahead once the read answers: the duplicate check that failed open on
// 2026-09-18 is asked again rather than skipped.
func TestAConversationTrackerReadThatFailsRecoverablyIsAskedAgain(t *testing.T) {
	t.Parallel()

	tracker := &contendedTracker{
		fakeTracker: &fakeTracker{},
		verb:        "list",
		failures:    2,
		failure:     errors.New("bd list failed with status timed_out and exit code -1: "),
	}
	var sleeps []time.Duration
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Admitting it.",
			`{"action":"create","title":"Something worth doing","description":"d","goal":"`+theGoal+`","reason":"r"}`)},
		{SessionID: "session-1", FinalText: "It is in the backlog."},
	}})
	options.Tracker = tracker
	options.Goals = recordedGoals(theGoal)
	options.Sleep = recordingSleep(&sleeps)
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "Admit it.")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || !reply.Actions[0].Applied || len(tracker.created) != 1 {
		t.Fatalf("actions = %#v, created = %#v, want the admission made once the listing answered", reply.Actions, tracker.created)
	}
	if tracker.calls != 3 || len(sleeps) != 2 {
		t.Fatalf("listings = %d, waits = %v, want the listing asked for again after each wait", tracker.calls, sleeps)
	}
}

// The window is the pipeline's, and spending it is the only thing that produces
// the failure the write would have produced at once before. What is reported
// then is what yoyodyne-ifd.327 made honest — the spend that stands, the write
// nobody can confirm — with the attempts and the time in front of it, which is
// exactly the order the item asked for: reported per 327 only after the retries
// are spent.
func TestATrackerWriteThatSpendsTheWindowIsReportedWithTheAttemptsInFront(t *testing.T) {
	t.Parallel()

	budgets := newTriageBudgetGate(t, runstate.TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 1, MergeRearms: 1}, 2)
	tracker := &fakeTracker{
		items: map[string]beads.WorkItem{
			"yoyodyne-ifd.142": {ID: "yoyodyne-ifd.142", Title: "the item whose re-run was reported as unrecorded", Status: "open"},
		},
		durableErr: errors.New(killedWrite),
	}
	var sleeps []time.Duration
	options := triageOptions(t, tracker, budgets, trackerReply("Its ground moved, so it starts over.",
		`{"action":"triage","id":"yoyodyne-ifd.142","run":"`+stoppedRun+`","decision":"rerun","reason":"the change is right and the branch it was written against has moved under it"}`))
	options.Reports = &fakeReports{}
	options.Sleep = recordingSleep(&sleeps)
	reply := triageSend(t, options)

	if len(reply.Actions) != 1 {
		t.Fatalf("actions = %#v", reply.Actions)
	}
	outcome := reply.Actions[0]
	// The whole window was waited out before anything was reported, on the
	// series the operator asked for, and not a second past it.
	var waited time.Duration
	for i, wait := range sleeps {
		if wait != recovery.Interval(i+1) {
			t.Fatalf("wait %d = %s, want %s", i+1, wait, recovery.Interval(i+1))
		}
		waited += wait
	}
	if waited > recovery.Window || waited+recovery.Interval(len(sleeps)+1) <= recovery.Window {
		t.Fatalf("waited %s over %d waits, want the whole of the %s window and no more", waited, len(sleeps), recovery.Window)
	}
	if len(tracker.updates) != len(sleeps)+1 {
		t.Fatalf("updates = %d, want one write per wait and one more", len(tracker.updates))
	}
	// Then the report is 327's, with the retries in front of it: not applied, the
	// spend named as standing, and the failure saying how long it was waited out.
	if outcome.Applied || !outcome.PartlyLanded() {
		t.Fatalf("outcome = %#v, want a failure with the spend standing behind it", outcome)
	}
	for _, want := range []string{"reaching the tracker kept failing", "did not outlast it", "timed_out"} {
		if !strings.Contains(outcome.Failure, want) {
			t.Fatalf("failure = %q, want it to say %q", outcome.Failure, want)
		}
	}
	rendered := renderTrackerOutcomes(options.Role, reply.Actions)
	if strings.Contains(rendered, "changed nothing") {
		t.Fatalf("a durable spend was reported as having changed nothing:\n%s", rendered)
	}
	for _, want := range []string{
		"did not finish, and part of it stands",
		"the re-run is spent against yoyodyne-ifd.142's durable budget",
		"carries it, so the write landed",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("the account is missing %q:\n%s", want, rendered)
		}
	}
}

// One window for the message, not one per call. The settling read a failed
// write is followed by runs under a context nothing can cancel, so it has to
// find the window the write spent rather than a fresh two hours to wait out
// uninterruptibly — and every later call in the message is in the same store's
// weather.
func TestTheTrackerWindowIsTheMessagesRatherThanEachCalls(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{
		items: map[string]beads.WorkItem{
			"yoyodyne-ifd.142": {ID: "yoyodyne-ifd.142", Title: "the item that keeps coming back", Status: "open"},
		},
		err: errors.New(killedWrite),
	}
	var sleeps []time.Duration
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Two notes.",
			`{"action":"update","id":"yoyodyne-ifd.142","note":"first","reason":"r"}`,
			`{"action":"update","id":"yoyodyne-ifd.142","note":"second","reason":"r"}`)},
		{SessionID: "session-1", FinalText: "Neither landed."},
	}})
	options.Tracker = tracker
	options.Sleep = recordingSleep(&sleeps)
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "note it twice")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 2 || reply.Actions[0].Applied || reply.Actions[1].Applied {
		t.Fatalf("actions = %#v, want both writes failed", reply.Actions)
	}
	var waited time.Duration
	for _, wait := range sleeps {
		waited += wait
	}
	if waited > recovery.Window {
		t.Fatalf("waited %s across the message, want no more than the one %s window", waited, recovery.Window)
	}
	// The second action took no wait of its own and says so: the window was
	// spent, and its failure names the retries that spent it.
	if !strings.Contains(reply.Actions[1].Failure, "did not outlast it") {
		t.Fatalf("second failure = %q, want the spent window named", reply.Actions[1].Failure)
	}
}

// A failure whose class says the next attempt earns the identical one is not
// waited out: a store that refused the write is an answer about the write, and
// the wait would turn it into two hours of the same answer.
func TestATrackerFailureNoLaterAttemptCouldSurviveIsNotRetried(t *testing.T) {
	t.Parallel()

	tracker := &contendedTracker{
		fakeTracker: &fakeTracker{items: map[string]beads.WorkItem{
			"yoyodyne-ifd.142": {ID: "yoyodyne-ifd.142", Title: "the item that keeps coming back", Status: "open"},
		}},
		verb:     "update",
		failures: 100,
		failure:  errors.New("bd update failed with status failed and exit code 1: the item is locked"),
	}
	var sleeps []time.Duration
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Noting it.",
			`{"action":"update","id":"yoyodyne-ifd.142","note":"n","reason":"r"}`)},
		{SessionID: "session-1", FinalText: "It refused."},
	}})
	options.Tracker = tracker
	options.Sleep = recordingSleep(&sleeps)
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "note it")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || reply.Actions[0].Applied {
		t.Fatalf("actions = %#v, want the refusal reported", reply.Actions)
	}
	if len(sleeps) != 0 || tracker.calls != 1 {
		t.Fatalf("waits = %v, writes = %d, want the refusal taken as the answer", sleeps, tracker.calls)
	}
	if strings.Contains(reply.Actions[0].Failure, "retr") {
		t.Fatalf("failure = %q, want no retry claimed where none was taken", reply.Actions[0].Failure)
	}
}

// The operator stopping the turn ends the wait, and the call is left as it
// failed rather than asked again under a context that has already ended.
func TestAStoppedTurnEndsTheTrackerWait(t *testing.T) {
	t.Parallel()

	tracker := &contendedTracker{
		fakeTracker: &fakeTracker{items: map[string]beads.WorkItem{
			"yoyodyne-ifd.142": {ID: "yoyodyne-ifd.142", Title: "the item that keeps coming back", Status: "open"},
		}},
		verb:     "update",
		failures: 100,
		failure:  errors.New(killedWrite),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sleeps := 0
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Noting it.",
			`{"action":"update","id":"yoyodyne-ifd.142","note":"n","reason":"r"}`)},
		{SessionID: "session-1", FinalText: "It did not land."},
	}})
	options.Tracker = tracker
	options.Sleep = func(ctx context.Context, _ time.Duration) error {
		sleeps++
		// The operator stops the turn during the second wait.
		if sleeps == 2 {
			cancel()
		}
		return ctx.Err()
	}
	session := openTestSession(t, options)

	reply, _ := session.Send(ctx, "note it")
	if sleeps != 2 {
		t.Fatalf("waits = %d, want the wait ended where the turn was stopped", sleeps)
	}
	if tracker.calls != 2 {
		t.Fatalf("writes = %d, want no write asked for under a context that had ended", tracker.calls)
	}
	if len(reply.Actions) == 1 && reply.Actions[0].Applied {
		t.Fatalf("action = %#v, want it left as it failed", reply.Actions[0])
	}
}

// A conversation with no tracker is exactly what it was: the nil checks at every
// call site still answer rather than reaching a wrapper around nothing.
func TestAConversationWithoutATrackerIsNotGivenAWrappedNothing(t *testing.T) {
	t.Parallel()

	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: trackerReply("Noting it.",
			`{"action":"update","id":"yoyodyne-ifd.142","note":"n","reason":"r"}`)},
		{SessionID: "session-1", FinalText: "There is no tracker."},
	}})
	options.Tracker = nil
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "note it")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(reply.Actions) != 1 || !strings.Contains(reply.Actions[0].Failure, "no work tracker is configured") {
		t.Fatalf("actions = %#v, want the plain refusal", reply.Actions)
	}
}

func equalDurations(got, want []time.Duration) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
