package slack

import (
	"context"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

var moment = time.Date(2026, 8, 19, 10, 0, 0, 0, time.UTC)

// What is worth saying is the notifier's to decide, and the feed's whole job is
// to hand it the two readings it compares and to remember which crossings have
// been said. Read the same record twice and the second reading says nothing: a
// thread is a narrative rather than an event log scrolling sideways.
func TestARunsCrossingsAreSaidOnceHoweverOftenTheRecordIsRead(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	state := harness.run(t, runstate.StatusRunning)
	harness.record(t, state)

	cursors := harness.poll(t, harness.start(), notify.KindRunStarted)
	harness.poll(t, cursors)

	// The record moves on, and only what it crossed since is said.
	state.Phase = runstate.PhaseReviewing
	state.UpdatedAt = moment.Add(time.Minute)
	harness.save(t, state)
	harness.poll(t, cursors, notify.KindChecksPassed)
}

// A check that fails, is repaired, and fails differently has crossed the same
// kind twice with two different things to say. A cursor that could not tell
// those apart would swallow the second, which is the repair loop going quiet at
// exactly the point somebody is watching it.
func TestTheSameKindCrossedTwiceDifferentlyIsSaidTwice(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	state := harness.run(t, runstate.StatusRunning)
	state.Phase = runstate.PhaseChecking
	state.CheckFailure = &runstate.CheckFailure{Command: "go test ./...", ExitCode: 1}
	harness.record(t, state)

	cursors := harness.poll(t, harness.start(), notify.KindRunStarted, notify.KindChecksFailed)

	state.CheckFailure = &runstate.CheckFailure{Command: "go vet ./...", ExitCode: 2}
	state.UpdatedAt = moment.Add(time.Minute)
	harness.save(t, state)
	harness.poll(t, cursors, notify.KindChecksFailed)
}

// The reading a crossing was said against advances only once the whole of it has
// been posted. A sink killed halfway therefore repeats what it had already said
// rather than losing what it had not: the durable record is authoritative, and a
// repetition is the right side of that trade.
func TestACrossingInterruptedHalfwayRepeatsRatherThanLosesTheRest(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	state := harness.run(t, runstate.StatusRunning)
	state.Phase = runstate.PhaseChecking
	state.CheckFailure = &runstate.CheckFailure{Command: "go test ./...", ExitCode: 1}
	harness.record(t, state)

	batch, err := harness.feed.Poll(context.Background(), harness.start())
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if len(batch.Deliveries) != 2 {
		t.Fatalf("deliveries = %d, want the run started and its checks failing", len(batch.Deliveries))
	}
	// Only the first was posted before the process died.
	interrupted := Cursors{Streams: map[string]Cursor{
		batch.Deliveries[0].Stream: batch.Deliveries[0].Cursor,
	}}
	harness.poll(t, interrupted, notify.KindChecksFailed)
}

// A sink started today does not want a month of finished work arriving at once.
// A run that was already over before it started is read past without a word, and
// its cursor closes so it is not carried for as long as the product exists.
func TestARunThatWasOverBeforeTheSinkStartedIsReadPastSilently(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, moment.Add(time.Hour))
	state := harness.run(t, runstate.StatusSucceeded)
	harness.record(t, state)

	batch, err := harness.feed.Poll(context.Background(), harness.start())
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if len(batch.Deliveries) != 1 || !batch.Deliveries[0].Silent() {
		t.Fatalf("deliveries = %#v, want one silent advance and nothing said", batch.Deliveries)
	}
	if !batch.Deliveries[0].Cursor.Closed {
		t.Fatalf("cursor = %#v, want history closed rather than carried", batch.Deliveries[0].Cursor)
	}
}

// A run that is over and owes nothing has nothing left to cross, so the reading
// it was compared against is dropped. Keeping it would make the sink's own
// record grow with the product's whole history.
func TestARunThatIsOverAndOwesNothingStopsBeingCarried(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	state := harness.run(t, runstate.StatusRunning)
	harness.record(t, state)
	cursors := harness.poll(t, harness.start(), notify.KindRunStarted)
	if cursors.Streams[runStream(state.RunID)].Reported == nil {
		t.Fatal("a run still in flight must keep the reading it is compared against")
	}

	completed := moment.Add(time.Minute)
	state.Status = runstate.StatusSucceeded
	state.Phase = runstate.PhaseComplete
	state.CompletedAt = &completed
	state.UpdatedAt = completed
	harness.save(t, state)

	// The checks are behind it now, so that is said; the pass after says nothing
	// and closes the run.
	cursors = harness.poll(t, cursors, notify.KindChecksPassed)
	cursors = harness.poll(t, cursors)
	closed := cursors.Streams[runStream(state.RunID)]
	if !closed.Closed || closed.Reported != nil {
		t.Fatalf("cursor = %#v, want a settled run closed and its reading dropped", closed)
	}
}

// A report is the agent's own words and a proposal is its argument about a
// document it does not own. Both are logs, so both advance by position, and both
// are said once.
func TestReportsAndProposalsAreSaidOnceInTheOrderTheyWereRecorded(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.file(t, "report-0123456789abcdef0123456789abcde0", report.SeverityWarning, moment)
	harness.propose(t, "amendment-0123456789abcdef0123456789abcde0", moment)

	cursors := harness.poll(t, harness.start(),
		notify.KindReportFiled, notify.KindProposalRaised)
	harness.poll(t, cursors)

	harness.file(t, "report-0123456789abcdef0123456789abcde1", report.SeverityCritical, moment.Add(time.Minute))
	harness.poll(t, cursors, notify.KindReportFiled)
}

// What a product recorded before this sink started is history. It is read past
// in one silent advance rather than rescanned on every pass for as long as the
// process runs.
func TestRecordsOlderThanTheSinkAreReadPastInOneAdvance(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, moment.Add(time.Hour))
	harness.file(t, "report-0123456789abcdef0123456789abcde0", report.SeverityNote, moment)
	harness.file(t, "report-0123456789abcdef0123456789abcde1", report.SeverityNote, moment.Add(time.Minute))

	batch, err := harness.feed.Poll(context.Background(), harness.start())
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	var reports []Delivery
	for _, delivery := range batch.Deliveries {
		if delivery.Stream == reportStream {
			reports = append(reports, delivery)
		}
	}
	if len(reports) != 1 || !reports[0].Silent() {
		t.Fatalf("deliveries = %#v, want one silent advance past both", reports)
	}
	if reports[0].Cursor.Position != 2 {
		t.Fatalf("cursor = %#v, want the log read past rather than rescanned", reports[0].Cursor)
	}
}

// An outage delays messages rather than losing them, and the record filed while
// the sink was down is exactly the one that would be lost. The stream it arrives
// on has never advanced — the normal state of a product that has not needed a
// report for weeks — so "has this cursor moved" is no answer to "has this sink
// ever run". Only the watermark answers that, and it does not move.
func TestARecordFiledWhileTheSinkWasDownIsStillPosted(t *testing.T) {
	t.Parallel()

	// Somebody turned reporting on, nothing was filed, and the sink stopped with
	// its report cursor still at zero.
	harness := newTestHarness(t, moment)
	cursors := harness.poll(t, harness.start())
	if cursors.Streams[reportStream].Position != 0 {
		t.Fatalf("cursor = %#v, want a log nothing has been filed on left where it was", cursors.Streams[reportStream])
	}

	// An hour of downtime, and a critical filed in the middle of it.
	harness.file(t, "report-0123456789abcdef0123456789abcde0", report.SeverityCritical, moment.Add(time.Hour))

	// The restart reads the same watermark it wrote, so the report is news.
	harness.poll(t, cursors, notify.KindReportFiled)
}

// The same thing for a run: one that both started and finished while the sink
// was down has no cursor at all, so nothing but the watermark distinguishes it
// from work that was over before reporting was ever turned on.
func TestARunThatRanEntirelyWhileTheSinkWasDownIsStillReported(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, moment)
	cursors := harness.poll(t, harness.start())

	completed := moment.Add(time.Hour)
	state := harness.run(t, runstate.StatusSucceeded)
	state.StartedAt = moment.Add(30 * time.Minute)
	state.UpdatedAt = completed
	state.CompletedAt = &completed
	harness.record(t, state)

	harness.poll(t, cursors, notify.KindRunStarted, notify.KindChecksPassed)
}

// One record nobody can address must not hold up every record behind it for as
// long as the process runs. It is said once in the sink's own log and read past,
// because a channel that goes silent over one malformed line is worse than one
// missing that line.
func TestARecordThatCannotBeAddressedIsReadPastRatherThanRetriedForever(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	var logged []string
	harness.feed.Log = func(format string, _ ...any) { logged = append(logged, format) }
	// A work item identifier with a separator in it names no thread: the key it
	// would make could not be read back into the topic it came from.
	harness.fileOn(t, "report-0123456789abcdef0123456789abcde0", "not: an item", moment)
	harness.file(t, "report-0123456789abcdef0123456789abcde1", report.SeverityNote, moment.Add(time.Minute))

	cursors := harness.poll(t, harness.start(), notify.KindReportFiled)
	if len(logged) != 1 {
		t.Fatalf("logged %v, want the record nobody can address said once", logged)
	}
	if cursors.Streams[reportStream].Position != 2 {
		t.Fatalf("cursor = %#v, want the log read past both", cursors.Streams[reportStream])
	}
	harness.poll(t, cursors)
	if len(logged) != 1 {
		t.Fatalf("logged %v, want it said once rather than on every pass", logged)
	}
}

// The operator's two switches are the awkward pair: a hold is a record, and what
// lifts it is only its absence. Both halves are said, because a queue that goes
// quiet is indistinguishable from a broken one until something says which.
// What a watch session is doing is carried like every other log: each
// transition once, in the order it happened, and nothing said twice however
// often the sink reads. A session going quiet is the whole reason this stream
// exists, so an idle one has to reach the channel.
func TestWhatAWatchSessionIsDoingIsSaidOnceEach(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.watched(t, runstate.WatchWatching, "watching the backlog until stopped", moment)
	harness.watched(t, runstate.WatchIdle, "the backlog is empty", moment.Add(time.Minute))
	cursors := harness.poll(t, harness.start(), notify.KindWatchStarted, notify.KindWatchIdle)
	// Read again with nothing new: a session that is still idle is still the
	// same fact, and a channel that repeated it every minute would be one nobody
	// reads.
	cursors = harness.poll(t, cursors)

	harness.watched(t, runstate.WatchBraked, "the operator is holding intake", moment.Add(2*time.Minute))
	harness.poll(t, cursors, notify.KindWatchBraked)
}

// A session that ran before anybody pointed a channel at this product is
// history: the watermark is read past in one silent advance rather than a
// night's worth of idling arriving at once.
func TestAWatchSessionFromBeforeTheWatermarkIsReadPast(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, moment)
	harness.watched(t, runstate.WatchWatching, "watching the backlog until stopped", moment.Add(-time.Hour))
	harness.watched(t, runstate.WatchStopped, "the scheduler was cancelled", moment.Add(-time.Minute))
	cursors := harness.poll(t, harness.start())
	if cursors.Streams[watchStream].Position != 2 {
		t.Fatalf("cursor = %#v, want what was read past advanced rather than re-read every pass", cursors.Streams[watchStream])
	}
	// What happens after the watermark is news, whatever came before it.
	harness.watched(t, runstate.WatchWatching, "watching the backlog until stopped", moment.Add(time.Hour))
	harness.poll(t, cursors, notify.KindWatchStarted)
}

func TestAHoldIsSaidWhenItIsPlacedAndAgainWhenItIsLifted(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	if _, err := harness.intake.Hold("reordering the backlog first", moment); err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	cursors := harness.poll(t, harness.start(), notify.KindIntakeHeld)
	// Held twice is the same hold, and it is said once.
	cursors = harness.poll(t, cursors)

	if _, _, err := harness.intake.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	cursors = harness.poll(t, cursors, notify.KindIntakeReleased)
	// The pair has been said in full and is forgotten, so the product's cursor
	// does not grow a line for every afternoon somebody was away.
	if len(cursors.Streams[productStream].Delivered) != 0 {
		t.Fatalf("cursor = %#v, want a said pair forgotten", cursors.Streams[productStream])
	}
	harness.poll(t, cursors)
}

// The wider hold is the same shape and is said the same way, and the two must
// not be confused for each other: one stops choosing work and the other stops
// everything.
func TestTheOperatorHoldIsSaidSeparatelyFromTheIntakeHold(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	if _, err := harness.holds.Hold(moment); err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
	cursors := harness.poll(t, harness.start(), notify.KindHoldPlaced)

	if _, _, err := harness.holds.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	harness.poll(t, cursors, notify.KindHoldLifted)
}

// testHarness is a product's durable records and a feed reading them, so what a
// test exercises is the reading rather than a stand-in for it.
type testHarness struct {
	// since is the product's watermark, which rides on the cursors rather than on
	// the feed: it is one durable moment for the product rather than one per
	// process, which is what makes downtime a gap the sink reads across.
	since   time.Time
	feed    *HarnessFeed
	runs    *runstate.Store
	chats   *runstate.ConversationStore
	reports *runstate.ReportStore
	amend   *runstate.AmendmentStore
	intake  *runstate.IntakeHoldStore
	holds   *runstate.OperatorHoldStore
	watch   *runstate.WatchStore
}

func newTestHarness(t *testing.T, since time.Time) *testHarness {
	t.Helper()
	root := t.TempDir()
	runs, err := runstate.NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	chats, err := runstate.NewConversationStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewConversationStore() error = %v", err)
	}
	reports, err := runstate.NewReportStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewReportStore() error = %v", err)
	}
	amend, err := runstate.NewAmendmentStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewAmendmentStore() error = %v", err)
	}
	intake, err := runstate.NewIntakeHoldStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewIntakeHoldStore() error = %v", err)
	}
	holds, err := runstate.NewOperatorHoldStore(root)
	if err != nil {
		t.Fatalf("NewOperatorHoldStore() error = %v", err)
	}
	watch, err := runstate.NewWatchStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewWatchStore() error = %v", err)
	}
	return &testHarness{
		since: since,
		feed: &HarnessFeed{
			Runs:          runs,
			Conversations: chats,
			Reports:       reports,
			Proposals:     amend,
			Intake:        intake,
			Holds:         holds,
			Watch:         watch,
			Now:           func() time.Time { return moment.Add(time.Hour) },
		},
		runs:    runs,
		chats:   chats,
		reports: reports,
		amend:   amend,
		intake:  intake,
		holds:   holds,
		watch:   watch,
	}
}

// poll makes one pass, checks it said exactly what was expected, and returns the
// cursors as they stand once every delivery has been taken — which is what the
// sink writes as it posts.
func (h *testHarness) poll(t *testing.T, cursors Cursors, want ...notify.Kind) Cursors {
	t.Helper()
	batch, err := h.feed.Poll(context.Background(), cursors)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	advanced := Cursors{SchemaVersion: CursorsSchemaVersion, Since: cursors.Since, Streams: map[string]Cursor{}}
	for stream, cursor := range cursors.Streams {
		advanced.Streams[stream] = cursor
	}
	var said []notify.Kind
	for _, delivery := range batch.Deliveries {
		advanced.Streams[delivery.Stream] = delivery.Cursor
		if delivery.Silent() {
			continue
		}
		if _, err := notify.Render(delivery.Notification.Topic, delivery.Notification.Speaker, delivery.Notification.Event); err != nil {
			t.Fatalf("a selected notification could not be said: %v", err)
		}
		said = append(said, delivery.Notification.Event.Kind)
	}
	if len(said) != len(want) {
		t.Fatalf("said %v, want %v", said, want)
	}
	for index, kind := range want {
		if said[index] != kind {
			t.Fatalf("said %v, want %v", said, want)
		}
	}
	return advanced
}

// start is the cursors a sink has on the first pass it ever makes over this
// product: nothing read, and the watermark already taken.
func (h *testHarness) start() Cursors {
	return Cursors{SchemaVersion: CursorsSchemaVersion, Since: h.since, Streams: map[string]Cursor{}}
}

func (h *testHarness) run(t *testing.T, status runstate.Status) runstate.State {
	t.Helper()
	runID, err := runstate.NewRunID()
	if err != nil {
		t.Fatalf("NewRunID() error = %v", err)
	}
	state := runstate.State{
		SchemaVersion: runstate.StateSchemaVersion,
		RunID:         runID,
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		WorkItemID:    "yoyodyne-ifd.68.3",
		Backend:       domain.BackendClaudeCode,
		Status:        status,
		Phase:         runstate.PhaseDeveloping,
		StartedAt:     moment,
		UpdatedAt:     moment,
		Selection: &runstate.Selection{
			By:     runstate.SelectedByDevelopmentManager,
			Reason: "the only ready child of the reporting epic",
			At:     moment,
		},
	}
	if status.Terminal() {
		completed := moment
		state.CompletedAt = &completed
		state.Phase = runstate.PhaseComplete
	}
	return state
}

func (h *testHarness) record(t *testing.T, state runstate.State) {
	t.Helper()
	if err := h.runs.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
}

func (h *testHarness) save(t *testing.T, state runstate.State) {
	t.Helper()
	if err := h.runs.Save(state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
}

func (h *testHarness) file(t *testing.T, id string, severity report.Severity, at time.Time) {
	t.Helper()
	h.fileAs(t, id, "yoyodyne-ifd.68.3", severity, at)
}

// fileOn files a report against an item named in a way no thread can be keyed
// by, which is the record the sink has to read past rather than wedge on.
func (h *testHarness) fileOn(t *testing.T, id, workItemID string, at time.Time) {
	t.Helper()
	h.fileAs(t, id, workItemID, report.SeverityNote, at)
}

func (h *testHarness) fileAs(t *testing.T, id, workItemID string, severity report.Severity, at time.Time) {
	t.Helper()
	if err := h.reports.Append(report.Report{
		SchemaVersion: report.SchemaVersion,
		ID:            id,
		Role:          domain.RoleDeveloper,
		Agent:         "developer",
		RunID:         "run-0123456789abcdef0123456789abcdef",
		WorkItemID:    workItemID,
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		Severity:      severity,
		Message:       "the preserved branch holds work worth cherry-picking",
		RecordedAt:    at,
	}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
}

func (h *testHarness) watched(t *testing.T, state runstate.WatchState, reason string, at time.Time) {
	t.Helper()
	if err := h.watch.Record(runstate.WatchTransition{
		SchemaVersion: runstate.WatchSchemaVersion,
		ProductID:     "yoyodyne",
		SessionID:     "watch-0123456789abcdef0123456789abcdef",
		State:         state,
		At:            at,
		Reason:        reason,
	}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
}

func (h *testHarness) propose(t *testing.T, id string, at time.Time) {
	t.Helper()
	if err := h.amend.Append(amendment.Proposal{
		SchemaVersion: amendment.SchemaVersion,
		ID:            id,
		Role:          domain.RoleDeveloper,
		Agent:         "developer",
		RunID:         "run-0123456789abcdef0123456789abcdef",
		WorkItemID:    "yoyodyne-ifd.68.3",
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		Artifact:      "slack-reporting-design",
		Kind:          artifact.KindDesign,
		Owner:         domain.RoleArchitect,
		Change:        "say which persona opens a topic's thread",
		Why:           "opening a thread is nobody's account of anything",
		RaisedAt:      at,
	}); err != nil {
		t.Fatalf("Append() error = %v", err)
	}
}

// A conversation is the second producer, and it arrives the way the design said
// one would: the feed reads its log by position and the notifier decides what
// any of it means. Most of the log is the turn itself, and what is said is the
// few records where the queue actually moved.
func TestAConversationSaysWhatItDidToTheBacklogAndNothingElse(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	conversation := harness.converse(t, domain.RoleProductManager)
	harness.chatted(t, conversation, 1, execution.EventAgentMessage, map[string]any{"text": "what was said in the turn"})
	harness.chatted(t, conversation, 2, execution.EventTrackerActionApplied, map[string]any{
		"action_id": "t1.1",
		"turn":      1,
		"action": map[string]any{
			"action":      "create",
			"title":       "Conversation milestones reach Slack",
			"description": "the item's own words",
			"goal":        "Work the harness runs on its own is visible while it runs",
			"reason":      "the backlog moves invisibly today",
		},
		"work_item_id": "yoyodyne-ifd.114",
		"summary":      "admitted yoyodyne-ifd.114 to the backlog",
	})
	harness.chatted(t, conversation, 3, execution.EventProcessOutput, map[string]any{"provider_subtype": "api_retry"})

	cursors := harness.poll(t, harness.start(), notify.KindItemAdmitted)
	// The position moved past the turn as well as past the milestone, so the log
	// is not read from its beginning again on the next pass.
	if position := cursors.Streams[conversationStream(conversation.ConversationID)].Position; position != 3 {
		t.Fatalf("position = %d, want the whole log read", position)
	}
	harness.poll(t, cursors)
}

// What a conversation did before somebody pointed a sink at this product is
// history nobody turned reporting on to read, exactly as a finished run is.
func TestWhatAConversationDidBeforeTheWatermarkIsReadPastSilently(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, moment.Add(time.Hour))
	conversation := harness.converse(t, domain.RoleProductManager)
	harness.chatted(t, conversation, 1, execution.EventTrackerActionApplied, map[string]any{
		"action_id": "t1.1",
		"turn":      1,
		"action": map[string]any{
			"action":   "reprioritize",
			"id":       "yoyodyne-ifd.99",
			"priority": 1,
			"reason":   "it waits on the epic above it",
		},
		"work_item_id": "yoyodyne-ifd.99",
		"summary":      "set yoyodyne-ifd.99 to priority 1",
	})

	batch, err := harness.feed.Poll(context.Background(), harness.start())
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	for _, delivery := range batch.Deliveries {
		if !delivery.Silent() {
			t.Fatalf("said %s about work that predates reporting", delivery.Notification.Event.Kind)
		}
	}
}

// converse records a conversation for one role, which is what makes its log
// discoverable and tells the notifier whose account the milestones in it are.
func (h *testHarness) converse(t *testing.T, role domain.AgentRole) runstate.Conversation {
	t.Helper()
	id, err := runstate.NewConversationID()
	if err != nil {
		t.Fatalf("NewConversationID() error = %v", err)
	}
	conversation := runstate.Conversation{
		SchemaVersion:  runstate.ConversationSchemaVersion,
		ConversationID: id,
		ProductID:      "yoyodyne",
		RepositoryID:   "yoyodyne",
		Agent:          string(role),
		Role:           role,
		Backend:        domain.BackendClaudeCode,
		StartedAt:      moment,
		UpdatedAt:      moment,
	}
	if err := h.chats.Save(conversation); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	return conversation
}

func (h *testHarness) chatted(t *testing.T, conversation runstate.Conversation, sequence uint64, eventType execution.EventType, payload any) {
	t.Helper()
	event, err := execution.NewEvent(conversation.ConversationID, sequence, moment, eventType, "harness.chat", payload)
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}
	if err := h.chats.AppendEvent(event); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
}
