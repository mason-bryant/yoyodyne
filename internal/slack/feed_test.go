package slack

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/directive"
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

// A status is a reading rather than a crossing, and it is the reading of the
// item's latest run: a second attempt is what is happening to the item now, and
// what the first one did is in the thread rather than on it.
func TestAnItemsStatusIsReadFromItsLatestRun(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	failed := harness.run(t, runstate.StatusFailed)
	failed.Failure = "the repair budget was spent"
	harness.record(t, failed)

	batch, err := harness.feed.Poll(context.Background(), harness.start())
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if got := batch.Statuses["work-item:yoyodyne-ifd.68.3"]; got != notify.StatusBlocked {
		t.Fatalf("status = %q, want a run that stopped and stayed stopped read as blocked", got)
	}

	retried := harness.run(t, runstate.StatusRunning)
	retried.Phase = runstate.PhaseReviewing
	retried.StartedAt = moment.Add(time.Hour)
	retried.UpdatedAt = moment.Add(time.Hour)
	harness.record(t, retried)

	batch, err = harness.feed.Poll(context.Background(), harness.start())
	if err != nil {
		t.Fatalf("second Poll() error = %v", err)
	}
	if got := batch.Statuses["work-item:yoyodyne-ifd.68.3"]; got != notify.StatusInReview {
		t.Fatalf("status = %q, want the latest attempt rather than the one before it", got)
	}
}

// A run that was over before the sink was ever pointed at this product is
// history the channel was never told about, so it marks nothing: a thread opened
// today by something else must not acquire a status from a run nobody here has
// said a word about.
func TestARunThatWasOverBeforeTheSinkStartedMarksNothing(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, moment.Add(time.Hour))
	harness.record(t, harness.run(t, runstate.StatusSucceeded))

	batch, err := harness.feed.Poll(context.Background(), harness.start())
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if len(batch.Statuses) != 0 {
		t.Fatalf("statuses = %#v, want history to mark nothing", batch.Statuses)
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

// A provider refusing something that is not a run reaches the channel from the
// log the process that met it wrote, at the weight an exhausted limit deserves.
// Nothing else in the record says it happened: the conversation failed at
// somebody's terminal and there is no run to have parked.
func TestAProviderRefusalOutsideARunReachesTheChannelAtWarning(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, moment)
	reset := moment.Add(3 * time.Hour)
	harness.refused(t, "the product manager conversation chat-91253e0e", &reset, moment.Add(time.Minute))
	cursors := harness.poll(t, harness.start(), notify.KindUsageLimitExhausted)
	// Read again with nothing new: one refusal is one thing to say, however
	// often the log is read.
	cursors = harness.poll(t, cursors)

	batch, err := harness.feed.Poll(context.Background(), harness.start())
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	said := batch.Deliveries[0].Notification
	if said.Event.Severity != report.SeverityWarning {
		t.Fatalf("a refusal is said at %q, want %q", said.Event.Severity, report.SeverityWarning)
	}
	message, err := notify.Render(said.Topic, said.Speaker, said.Event)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{"the product manager conversation chat-91253e0e", reset.UTC().Format(time.RFC3339)} {
		if !strings.Contains(message.Body, want) {
			t.Fatalf("a refusal reads as %q, which does not say %q", message.Body, want)
		}
	}
	// A second refusal is a second thing to say: an operator who released
	// capacity and ran into it again has learned something.
	harness.refused(t, "the independent review review-4d1f of main", nil, moment.Add(2*time.Minute))
	harness.poll(t, cursors, notify.KindUsageLimitExhausted)
}

// A refusal from before anybody pointed a channel at this product is history,
// and is read past in one silent advance like every other log's.
func TestARefusalFromBeforeTheWatermarkIsReadPast(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, moment)
	harness.refused(t, "the product manager conversation chat-91253e0e", nil, moment.Add(-time.Hour))
	cursors := harness.poll(t, harness.start())
	if cursors.Streams[usageLimitStream].Position != 1 {
		t.Fatalf("cursor = %#v, want what was read past advanced rather than re-read every pass", cursors.Streams[usageLimitStream])
	}
	harness.refused(t, "the product manager conversation chat-91253e0e", nil, moment.Add(time.Hour))
	harness.poll(t, cursors, notify.KindUsageLimitExhausted)
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

// Somebody who steers from a thread is told what was recorded and then hears
// nothing more, because what becomes of a directive is settled at a terminal
// they are not at. So the record is read for it, and it is said where they asked
// and addressed to them — once, however often the record is read afterwards —
// carrying the message that asked, whose mark stops saying the directive is open
// at the same moment.
func TestWhatBecomesOfADirectiveSaidInAThreadIsSaidInThatThread(t *testing.T) {
	t.Parallel()

	const member = "U0OPERATOR"
	const askTS = "1750000001.000200"
	harness := newTestHarness(t, time.Time{})
	recorded := harness.directive(t, "yoyodyne-ifd.68.3", member, askTS)

	// Nothing has become of it yet. The thread already carries what was recorded,
	// and a directive nobody has settled is not news a second time.
	cursors := harness.poll(t, harness.start())

	if _, err := harness.directives.Resolve(recorded.ID, "the second one, and the design says so", moment); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	batch, err := harness.feed.Poll(context.Background(), cursors)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	said := 0
	for _, delivery := range batch.Deliveries {
		if delivery.Stream != directiveStream {
			continue
		}
		said++
		cursors.Streams[delivery.Stream] = delivery.Cursor
		if delivery.Notification.Event.Kind != notify.KindDirectiveResolved {
			t.Fatalf("said %q, want what became of the directive", delivery.Notification.Event.Kind)
		}
		if delivery.Notification.Topic.Key() != "work-item:yoyodyne-ifd.68.3" {
			t.Fatalf("said in %q, want the thread the directive was asked for in", delivery.Notification.Topic.Key())
		}
		if delivery.Mention != member {
			t.Fatalf("mention = %q, want the human who asked for it tagged", delivery.Mention)
		}
		if delivery.Reply != askTS {
			t.Fatalf("reply = %q, want the message that asked, so its mark can move to settled", delivery.Reply)
		}
		if delivery.Notification.Event.Text != "the second one, and the design says so" {
			t.Fatalf("said %q, want what settled it", delivery.Notification.Event.Text)
		}
		if delivery.Notification.Event.Refs.DirectiveID != recorded.ID {
			t.Fatalf("refs = %#v, want the directive it is about", delivery.Notification.Event.Refs)
		}
	}
	if said != 1 {
		t.Fatalf("said %d outcomes, want exactly one", said)
	}

	// Read again, and it says nothing: a settlement is said once, like every
	// other crossing.
	harness.poll(t, cursors)
}

// A settlement that happened before this product's reporting began is history,
// exactly as everything else read from a record is. The per-directive marks live
// in the cursors and the steer map does not, so an operator who starts the
// channel over by deleting one and keeping the other must not be answered by
// name for every directive they ever steered and settled: a flood of mentions
// about work that is long over is the same trust erosion as silence.
func TestASettlementFromBeforeTheWatermarkIsNotSaidAgain(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, moment.Add(time.Hour))
	recorded := harness.directive(t, "yoyodyne-ifd.68.3", "U0OPERATOR", "1750000001.000200")
	if _, err := harness.directives.Resolve(recorded.ID, "settled long before the channel existed", moment); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	// The cursors a sink has when it has read nothing at all, which is exactly
	// what deleting them leaves behind.
	harness.poll(t, harness.start())
}

// A settlement the connection already said in the thread is not said a second
// time by the delivery pass. The two halves post from different goroutines, so
// what the connection wrote down is what stops the pass repeating it.
func TestASettlementTheThreadWasAlreadyToldIsNotSaidByThePass(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	recorded := harness.directive(t, "yoyodyne-ifd.68.3", "U0OPERATOR", "1750000001.000200")
	if _, err := harness.directives.Resolve(recorded.ID, "the one already on the target branch", moment); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	steers, err := harness.steers.LoadSteers()
	if err != nil {
		t.Fatalf("LoadSteers() error = %v", err)
	}
	steer, found := steers.Lookup(recorded.ID)
	if !found {
		t.Fatalf("steer for %s is missing, want the one the harness recorded", recorded.ID)
	}
	// What a reply that resolved it in its own thread leaves behind.
	steer.Said = true
	steers.Record(recorded.ID, steer)
	if err := harness.steers.SaveSteers(steers); err != nil {
		t.Fatalf("SaveSteers() error = %v", err)
	}

	harness.poll(t, harness.start())
}

// A directive recorded at a terminal has no thread to answer in and nobody to
// tag, so nothing is said about it here. Reporting on every directive the
// product has would be a channel narrating a record nobody asked it to.
func TestADirectiveNobodySaidInAThreadIsNotAnsweredInOne(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	recorded := harness.directive(t, "yoyodyne-ifd.68.3", "U0OPERATOR", "1750000001.000200")
	// The sink's note of where it came from is what makes it answerable, and a
	// directive typed at a terminal never has one.
	if err := harness.steers.SaveSteers(SteerMap{}); err != nil {
		t.Fatalf("SaveSteers() error = %v", err)
	}
	if _, err := harness.directives.Resolve(recorded.ID, "settled at the terminal it was typed at", moment); err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	harness.poll(t, harness.start())
}

// testHarness is a product's durable records and a feed reading them, so what a
// test exercises is the reading rather than a stand-in for it.
type testHarness struct {
	// since is the product's watermark, which rides on the cursors rather than on
	// the feed: it is one durable moment for the product rather than one per
	// process, which is what makes downtime a gap the sink reads across.
	since time.Time
	// now is when the feed thinks it is. It moves, because what the sink says
	// about a state rather than an event depends on how long that state has stood.
	now     time.Time
	feed    *HarnessFeed
	runs    *runstate.Store
	chats   *runstate.ConversationStore
	reports *runstate.ReportStore
	amend   *runstate.AmendmentStore
	intake  *runstate.IntakeHoldStore
	holds   *runstate.OperatorHoldStore
	watch   *runstate.WatchStore
	limits  *runstate.UsageLimitStore
	// directives is the product's directive record, and steers is the sink's own
	// note of which of those were said into a thread. The outcome half reads both:
	// one says what became of a directive, the other says whose thread to say it
	// in, whom to tag, and which message stops saying it is open.
	directives *runstate.DirectiveStore
	steers     *Store
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
	limits, err := runstate.NewUsageLimitStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewUsageLimitStore() error = %v", err)
	}
	directives, err := runstate.NewDirectiveStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewDirectiveStore() error = %v", err)
	}
	steers, err := NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	harness := &testHarness{
		since:      since,
		now:        moment.Add(time.Hour),
		runs:       runs,
		chats:      chats,
		reports:    reports,
		amend:      amend,
		intake:     intake,
		holds:      holds,
		watch:      watch,
		limits:     limits,
		directives: directives,
		steers:     steers,
	}
	harness.feed = &HarnessFeed{
		Runs:          runs,
		Conversations: chats,
		Reports:       reports,
		Proposals:     amend,
		Intake:        intake,
		Holds:         holds,
		Watch:         watch,
		UsageLimits:   limits,
		Directives:    directives,
		Steers:        steers,
		Now:           func() time.Time { return harness.now },
	}
	return harness
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

// directive records one directive the way a reply in a thread records it: in
// the product's own directive record, with the sink's note of which thread it
// was said in, by whom, and in which message beside it.
func (h *testHarness) directive(t *testing.T, workItemID, member, messageTS string) directive.Directive {
	t.Helper()
	id, err := directive.NewID()
	if err != nil {
		t.Fatalf("NewID() error = %v", err)
	}
	recorded := directive.Directive{
		SchemaVersion: directive.SchemaVersion,
		ID:            id,
		ProductID:     "yoyodyne",
		Kind:          directive.KindAmbiguous,
		ReceivedBy:    domain.RoleProductManager,
		ReceivedAt:    moment,
		Text:          "ambiguous: which of the two branches did you mean",
		Unresolved:    "which of the two branches did you mean",
		Scope:         []string{workItemID},
	}
	if err := h.directives.Record(recorded); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	topic, err := notify.WorkItem(workItemID)
	if err != nil {
		t.Fatalf("address a work item: %v", err)
	}
	steers, err := h.steers.LoadSteers()
	if err != nil {
		t.Fatalf("LoadSteers() error = %v", err)
	}
	steers.Record(id, Steer{Member: member, Topic: topic.Key(), Message: messageTS, RecordedAt: moment})
	if err := h.steers.SaveSteers(steers); err != nil {
		t.Fatalf("SaveSteers() error = %v", err)
	}
	return recorded
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
	h.watchedAs(t, "watch-0123456789abcdef0123456789abcdef", state, reason, at)
}

// watchedAs records a transition of one named session, so a test can put two
// sessions in one log — which is what the product's log actually holds.
func (h *testHarness) watchedAs(t *testing.T, sessionID string, state runstate.WatchState, reason string, at time.Time) {
	t.Helper()
	if err := h.watch.Record(runstate.WatchTransition{
		SchemaVersion: runstate.WatchSchemaVersion,
		ProductID:     "yoyodyne",
		SessionID:     sessionID,
		State:         state,
		At:            at,
		Reason:        reason,
	}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
}

func (h *testHarness) refused(t *testing.T, waiting string, resetsAt *time.Time, at time.Time) {
	t.Helper()
	if err := h.limits.Record(runstate.UsageLimitExhaustion{
		SchemaVersion: runstate.UsageLimitSchemaVersion,
		ProductID:     "yoyodyne",
		At:            at,
		Waiting:       waiting,
		Kind:          "five-hour",
		ResetsAt:      resetsAt,
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
