package slack

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The overnight this exists for: intake held at two minutes past midnight, a
// watch session that reached its budget and stopped, and ten hours in which the
// channel could not be told from a broken one. Both of those said themselves
// once, as they happened. What was missing is the hour after that.
func TestAHeldLineWithReadyWorkSaysSoAgainWhileItStands(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(3)
	harness.watched(t, runstate.WatchStopped, "the session spent the budget it was given", moment)
	harness.hold(t, "the harness held intake after runs kept blocking", moment)

	// The first sighting arms the clock and says nothing: the hold said itself
	// when it was placed, and repeating it a poll later would be the sink saying
	// what the channel already has.
	cursors := harness.poll(t, harness.start(), notify.KindWatchStopped, notify.KindIntakeHeld)
	harness.now = harness.now.Add(59 * time.Minute)
	cursors = harness.poll(t, cursors)

	harness.now = harness.now.Add(2 * time.Minute)
	cursors = harness.poll(t, cursors, notify.KindLineWaiting)
	// And again the hour after that, for as long as it stands.
	harness.now = harness.now.Add(time.Hour)
	harness.poll(t, cursors, notify.KindLineWaiting)
}

// What it says is what somebody woken by it has to act on: which state, how long
// it has stood, and how much work is waiting behind it.
func TestTheHeartbeatNamesTheStateItsAgeAndWhatIsWaiting(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(4)
	harness.watched(t, runstate.WatchStopped, "the session spent the budget it was given", moment)
	held := harness.now
	harness.hold(t, "reordering the backlog first", held)

	cursors := harness.poll(t, harness.start(), notify.KindWatchStopped, notify.KindIntakeHeld)
	harness.now = held.Add(10 * time.Hour)
	said := harness.say(t, cursors, notify.KindLineWaiting)
	for _, fact := range []string{"intake is held", "reordering the backlog first", "10 hours", "4 items"} {
		if !strings.Contains(said.Body, fact) {
			t.Fatalf("body %q does not carry %q", said.Body, fact)
		}
	}
}

// A line nobody is holding, with a session choosing work, is not waiting on
// anybody. The heartbeat stops the moment the state clears, and it says nothing
// about the clearing: what cleared it said so itself.
func TestTheHeartbeatStopsWhenTheStateClears(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(2)
	harness.watched(t, runstate.WatchWatching, "watching the backlog until stopped", moment)
	harness.hold(t, "looking at something first", moment)

	cursors := harness.poll(t, harness.start(), notify.KindWatchStarted, notify.KindIntakeHeld)
	harness.now = harness.now.Add(time.Hour)
	cursors = harness.poll(t, cursors, notify.KindLineWaiting)

	if _, _, err := harness.intake.Release(); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	cursors = harness.poll(t, cursors, notify.KindIntakeReleased)
	if standing := cursors.Streams[heartbeatStream].Standing; standing != "" {
		t.Fatalf("the heartbeat is still standing on %q after the state cleared", standing)
	}
	harness.now = harness.now.Add(4 * time.Hour)
	harness.poll(t, cursors)
}

// The other half of the rule, and the half that makes the channel readable: an
// idle line with nothing ready is not waiting on anybody, so it stays exactly as
// silent as it was.
func TestAnIdleLineWithNothingReadyStaysSilent(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(0)
	harness.watched(t, runstate.WatchIdle, "the backlog is empty", moment)

	cursors := harness.poll(t, harness.start(), notify.KindWatchIdle)
	for hour := 0; hour < 5; hour++ {
		harness.now = harness.now.Add(time.Hour)
		cursors = harness.poll(t, cursors)
	}
}

// A product nobody has ever watched has no line that stopped. An operator who
// runs items by name has a queue by choice, and an hourly message about it is
// the nagging this is written not to be.
func TestAProductNobodyHasWatchedIsNotAStalledLine(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(6)

	cursors := harness.poll(t, harness.start())
	harness.now = harness.now.Add(3 * time.Hour)
	harness.poll(t, cursors)
}

// Work visibly moving is not a stalled line whatever else is true, and a message
// saying nothing is being chosen while a run posts its way through a review is
// false in the way that teaches people to stop reading.
func TestALineWithARunInFlightIsNotWaiting(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(2)
	harness.watched(t, runstate.WatchStopped, "the session spent the budget it was given", moment)
	harness.record(t, harness.run(t, runstate.StatusRunning))

	cursors := harness.poll(t, harness.start(), notify.KindRunStarted, notify.KindWatchStopped)
	harness.now = harness.now.Add(2 * time.Hour)
	harness.poll(t, cursors)
}

// A tracker that will not answer leaves the sink unable to tell a line waiting on
// somebody from an honestly quiet one. It must not guess in either direction: it
// says so where it says everything else about itself, and asks again at the next
// interval rather than at the next poll.
func TestATrackerThatCannotBeReadIsSaidRatherThanGuessedAt(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	var said []string
	harness.feed.Log = func(format string, args ...any) { said = append(said, format) }
	harness.feed.Backlog = brokenBacklog{}
	harness.watched(t, runstate.WatchStopped, "the session spent the budget it was given", moment)

	cursors := harness.poll(t, harness.start(), notify.KindWatchStopped)
	harness.now = harness.now.Add(time.Hour)
	cursors = harness.poll(t, cursors)
	if len(said) != 1 {
		t.Fatalf("the sink said %v about a tracker it could not read, want it said once", said)
	}
	// The clock is reset rather than left where it was, so the next attempt is at
	// the next interval rather than fifteen seconds later.
	harness.poll(t, cursors)
	if len(said) != 1 {
		t.Fatalf("the sink said %v, want the retry held to the heartbeat interval", said)
	}
}

// A different state is a different thing to do something about, so it is armed
// afresh rather than inheriting a clock that has already run out — the sink says
// the state that stands now, once it has stood for an interval, rather than the
// moment it replaces one that had.
func TestANewStateIsArmedRatherThanInheritingTheLastOnesClock(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(1)
	harness.watched(t, runstate.WatchStopped, "the session spent the budget it was given", moment)

	cursors := harness.poll(t, harness.start(), notify.KindWatchStopped)
	harness.now = harness.now.Add(2 * time.Hour)
	cursors = harness.poll(t, cursors, notify.KindLineWaiting)

	// The operator holds intake, which is now what has stopped the line.
	harness.hold(t, "looking at the queue", harness.now)
	cursors = harness.poll(t, cursors, notify.KindIntakeHeld)
	harness.now = harness.now.Add(30 * time.Minute)
	cursors = harness.poll(t, cursors)
	harness.now = harness.now.Add(31 * time.Minute)
	harness.poll(t, cursors, notify.KindLineWaiting)
}

// One log holds every session a product has had, and nothing stops two running
// at once. A session ending while another carries on watching is the last line of
// the log saying "stopped" over a line that is still being pulled from, so the
// sessions are read one at a time rather than off the end of the log.
func TestASessionEndingBesideOneStillWatchingIsNotAStoppedLine(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(3)
	harness.watchedAs(t, "watch-0123456789abcdef0123456789abcdef", runstate.WatchWatching, "watching the backlog until stopped", moment)
	harness.watchedAs(t, "watch-fedcba9876543210fedcba9876543210", runstate.WatchWatching, "watching the backlog until stopped", moment.Add(time.Minute))
	harness.watchedAs(t, "watch-fedcba9876543210fedcba9876543210", runstate.WatchStopped, "the session spent the budget it was given", moment.Add(2*time.Minute))

	cursors := harness.poll(t, harness.start(),
		notify.KindWatchStarted, notify.KindWatchStarted, notify.KindWatchStopped)
	harness.now = harness.now.Add(3 * time.Hour)
	cursors = harness.poll(t, cursors)

	// Once the session that was still watching ends too, nobody is choosing.
	harness.watchedAs(t, "watch-0123456789abcdef0123456789abcdef", runstate.WatchStopped, "the scheduler was cancelled", harness.now)
	cursors = harness.poll(t, cursors, notify.KindWatchStopped)
	harness.now = harness.now.Add(time.Hour)
	harness.poll(t, cursors, notify.KindLineWaiting)
}

// The night this exists for, replayed. The repair-handback fix merged the day
// before, the watch session's binary predated it, and three granted repair rounds
// executed against clean worktrees and delivered empty diffs. Every one of those
// rounds was a run in flight, so the waiting heartbeat above was correctly silent
// through the whole of it: the session's age is what nothing was saying.
//
// It is said the first time it is seen rather than armed silently, because unlike
// a hold there is no record that said it as it happened — and being told after
// the first round has been spent is being told too late.
func TestAStaleResidentSaysSoWhileTheRoundsAreBeingSpent(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(2)
	harness.deployed(3)
	harness.watchedBuild(t, runstate.WatchWatching, "watching the backlog until stopped", moment, staleResidentBuild)
	harness.record(t, harness.run(t, runstate.StatusRunning))

	cursors := harness.poll(t, harness.start(),
		notify.KindRunStarted, notify.KindWatchStarted, notify.KindResidentStale)
	// The repository is asked at the cadence rather than at every poll, so a
	// fifteen-second pass a moment later spawns nothing and says nothing.
	harness.now = harness.now.Add(30 * time.Minute)
	cursors = harness.poll(t, cursors)
	// And again the hour after that, for as long as the session runs that binary.
	harness.now = harness.now.Add(31 * time.Minute)
	harness.poll(t, cursors, notify.KindResidentStale)
}

// What it says is what somebody has to act on: how far behind the session is,
// which build it is on, and that a restart is the thing that fixes it.
func TestTheResidentLineNamesTheCountAndTheWayOutOfIt(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(1)
	harness.deployed(7)
	harness.watchedBuild(t, runstate.WatchWatching, "watching the backlog until stopped", moment, staleResidentBuild)

	cursors := harness.poll(t, harness.start(), notify.KindWatchStarted, notify.KindResidentStale)
	harness.now = harness.now.Add(time.Hour)
	said := harness.say(t, cursors, notify.KindResidentStale)
	for _, fact := range []string{"7 harness changes", staleResidentBuild[:12], "Restarting it"} {
		if !strings.Contains(said.Body, fact) {
			t.Fatalf("body %q does not carry %q", said.Body, fact)
		}
	}
}

// A session running what is deployed is the ordinary state, and silence has to
// keep meaning nothing to do. The clock is still reset, so the repository is read
// once an hour rather than every fifteen seconds on a machine that is behaving.
func TestASessionRunningWhatIsDeployedStaysSilent(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(3)
	harness.deployed(0)
	harness.watchedBuild(t, runstate.WatchWatching, "watching the backlog until stopped", moment, staleResidentBuild)

	cursors := harness.poll(t, harness.start(), notify.KindWatchStarted)
	for hour := 0; hour < 4; hour++ {
		harness.now = harness.now.Add(time.Hour)
		cursors = harness.poll(t, cursors)
	}
}

// Far enough past the threshold the session has missed too much for what it is
// doing to be trusted to be about the work, so the operators are told where they
// will see it rather than in a channel nobody is reading at three in the morning.
// Once, because the hourly line is already carrying the state and a second
// channel repeating it is a channel somebody mutes.
func TestCrossingTheThresholdReachesTheOperatorsOnce(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(1)
	harness.deployed(4)
	harness.feed.StaleBuildThreshold = 4
	harness.watchedBuild(t, runstate.WatchWatching, "watching the backlog until stopped", moment, staleResidentBuild)

	crossed := harness.resident(t, harness.start())
	if !crossed.Direct {
		t.Fatal("the threshold was crossed and nothing was said to the operators directly")
	}
	if severity := crossed.Notification.Event.Severity; severity != report.SeverityWarning {
		t.Fatalf("severity = %q, want a degraded harness said louder than a note", severity)
	}
	cursors := harness.start()
	cursors.Streams[residentStream] = crossed.Cursor

	harness.now = harness.now.Add(time.Hour)
	again := harness.resident(t, cursors)
	if again.Direct {
		t.Fatal("the operators were told a second time about the same build")
	}
	if again.Notification.Event.Kind != notify.KindResidentStale {
		t.Fatalf("said %q, want the channel to keep carrying the state", again.Notification.Event.Kind)
	}
}

// Below the threshold this is worth reading beside the heartbeat and not worth
// interrupting somebody for.
func TestAResidentShortOfTheThresholdIsSaidInTheChannelAlone(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(1)
	harness.deployed(3)
	harness.feed.StaleBuildThreshold = 4
	harness.watchedBuild(t, runstate.WatchWatching, "watching the backlog until stopped", moment, staleResidentBuild)

	said := harness.resident(t, harness.start())
	if said.Direct {
		t.Fatal("the operators were interrupted for a session that is three changes behind")
	}
	if severity := said.Notification.Event.Severity; severity != report.SeverityNote {
		t.Fatalf("severity = %q, want a note", severity)
	}
}

// A session restarted onto a binary that is still behind is a different build,
// and being told about the last one is not being told about this one. The
// escalation is remembered per build so it re-arms rather than being swallowed by
// a mark about a binary nobody is running any more.
func TestARestartOntoAStillStaleBuildIsEscalatedAfresh(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(1)
	harness.deployed(9)
	harness.feed.StaleBuildThreshold = 2
	harness.watchedBuild(t, runstate.WatchWatching, "watching the backlog until stopped", moment, staleResidentBuild)

	cursors := harness.start()
	cursors.Streams[residentStream] = harness.resident(t, cursors).Cursor

	// The operator restarts, and what comes back is newer and still not current.
	harness.watchedAs(t, "watch-0123456789abcdef0123456789abcdef", runstate.WatchStopped, "the operator stopped it", harness.now)
	harness.now = harness.now.Add(time.Minute)
	harness.watchedBuildAs(t, "watch-fedcba9876543210fedcba9876543210", runstate.WatchWatching,
		"watching the backlog until stopped", harness.now, newerResidentBuild)

	restarted := harness.resident(t, cursors)
	if !restarted.Direct {
		t.Fatal("a restart onto a binary that is still behind said nothing to the operators")
	}
}

// A session that has stopped is not a resident, and one whose binary recorded no
// revision is a comparison nobody can make. Neither is worth an hourly message:
// the first is already said where sessions are said, and the second is a fact
// about how somebody built the binary rather than news about this product.
func TestASessionWithNothingToCompareIsNotReportedAsStale(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(2)
	harness.deployed(40)
	harness.watched(t, runstate.WatchWatching, "watching the backlog until stopped", moment)

	cursors := harness.poll(t, harness.start(), notify.KindWatchStarted)
	harness.now = harness.now.Add(2 * time.Hour)
	cursors = harness.poll(t, cursors)

	// A session that did record one, and then ended.
	harness.watchedBuildAs(t, "watch-fedcba9876543210fedcba9876543210", runstate.WatchStopped,
		"the session spent the budget it was given", harness.now, staleResidentBuild)
	cursors = harness.poll(t, cursors, notify.KindWatchStopped)
	harness.now = harness.now.Add(2 * time.Hour)
	harness.poll(t, cursors)
}

// The runs in flight are the second record that says which harness is
// dispatching work, and they are read when the watch log does not say. That is
// the shape the field cases had: the session choosing work stamped nothing at
// all — watch.jsonl only began carrying a build on 2026-08-30 — while every run
// it reserved was made by that same binary and now says so. Without this the
// stale resident is silent exactly where it is spending rounds.
func TestARunInFlightNamesTheBuildWhenTheSessionDoesNot(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(2)
	harness.deployed(31)
	// A session that is alive and says nothing about its binary, which is every
	// session recorded before the stamping existed.
	harness.watched(t, runstate.WatchWatching, "watching the backlog until stopped", moment)
	dispatched := harness.run(t, runstate.StatusRunning)
	dispatched.Build = staleResidentBuild
	harness.record(t, dispatched)

	harness.poll(t, harness.start(),
		notify.KindRunStarted, notify.KindWatchStarted, notify.KindResidentStale)
}

// A live session that says which binary it is is believed over the runs beside
// it, even where a run was reserved later by a different one. That is the
// precedence rather than a contest of which record is newer: the session is the
// resident, and a run reserved by another binary is an operator's `yoyo run` or a
// triage carry-out — a process that is already ending, whose build is not the one
// that will go on choosing work.
func TestALiveSessionsOwnStampIsBelievedOverTheRunsBesideIt(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(2)
	harness.feed.Deployments = perBuildDeployments{currentResidentBuild: 0, staleResidentBuild: 31}
	// The session is on what is deployed and said so ten minutes ago; a run
	// started since was reserved by a binary thirty-one changes behind.
	harness.watchedBuild(t, runstate.WatchWatching, "watching the backlog until stopped", moment, currentResidentBuild)
	dispatched := harness.run(t, runstate.StatusRunning)
	dispatched.Build = staleResidentBuild
	dispatched.StartedAt = moment.Add(10 * time.Minute)
	dispatched.UpdatedAt = dispatched.StartedAt
	harness.record(t, dispatched)

	// The resident is current, so nothing is said about a stale one however
	// recently the run beside it started.
	cursors := harness.poll(t, harness.start(), notify.KindRunStarted, notify.KindWatchStarted)
	harness.now = harness.now.Add(2 * time.Hour)
	harness.poll(t, cursors)
}

// A run that has ended says which build made it and is not evidence about what is
// running now. The record is still the answer to "which build did this" long
// afterwards; what it stops being is an account of the resident.
func TestAFinishedRunIsNotReadAsTheResident(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(2)
	harness.deployed(31)
	harness.watched(t, runstate.WatchWatching, "watching the backlog until stopped", moment)
	ended := harness.run(t, runstate.StatusSucceeded)
	ended.Build = staleResidentBuild
	harness.record(t, ended)

	cursors := harness.poll(t, harness.start(),
		notify.KindRunStarted, notify.KindChecksPassed, notify.KindWatchStarted)
	harness.now = harness.now.Add(2 * time.Hour)
	harness.poll(t, cursors)
}

// The build revision is the yoyodyne binary's and the repository is the
// product's, and those are one history only where the product is the harness's
// own source. Everywhere else the repository has never held that revision, there
// is no count to say, and nothing is wrong — so the channel hears nothing at all
// and the sink's log says why once per build rather than every hour for the life
// of the process.
func TestABuildFromAnotherRepositoryIsSilentRatherThanCountedOrNagged(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(2)
	var said []string
	harness.feed.Log = func(format string, args ...any) { said = append(said, format) }
	harness.feed.Deployments = unrelatedDeployments{}
	harness.watchedBuild(t, runstate.WatchWatching, "watching the backlog until stopped", moment, staleResidentBuild)

	cursors := harness.poll(t, harness.start(), notify.KindWatchStarted)
	if len(said) != 1 {
		t.Fatalf("the sink said %v about a build from another repository, want it said once", said)
	}
	for hour := 0; hour < 4; hour++ {
		harness.now = harness.now.Add(time.Hour)
		cursors = harness.poll(t, cursors)
	}
	if len(said) != 1 {
		t.Fatalf("the sink said %v, want an installation that is behaving told about once", said)
	}
}

// A repository that cannot be read leaves the sink unable to tell a session
// running what is deployed from one running a binary from before the fix. It must
// not guess in either direction, so it is said once where the sink says everything
// about itself, and asked again at the next interval rather than at the next poll.
func TestARepositoryThatCannotBeReadIsSaidRatherThanGuessedAt(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(1)
	var said []string
	harness.feed.Log = func(format string, args ...any) { said = append(said, format) }
	harness.feed.Deployments = brokenDeployments{}
	harness.watchedBuild(t, runstate.WatchWatching, "watching the backlog until stopped", moment, staleResidentBuild)

	cursors := harness.poll(t, harness.start(), notify.KindWatchStarted)
	if len(said) != 1 {
		t.Fatalf("the sink said %v about a repository it could not read, want it said once", said)
	}
	harness.poll(t, cursors)
	if len(said) != 1 {
		t.Fatalf("the sink said %v, want the retry held to the heartbeat interval", said)
	}
}

// The builds a session can be running: one the harness has moved past, the newer
// one a restart puts it on that is still not current, and the one that is
// actually deployed.
const (
	staleResidentBuild   = "4c1f2b3a9d8e7f6a5b4c3d2e1f0099887766554433221100aabbccddeeff0011"
	newerResidentBuild   = "aa11bb22cc33dd44ee55ff6677889900112233445566778899aabbccddeeff00"
	currentResidentBuild = "ff00ee11dd22cc33bb44aa5566778899001122334455667788990011223344ff"
)

// ready gives the feed a tracker that reports this many items ready to pull, and
// the hourly cadence the sink ships with.
func (h *testHarness) ready(count int) {
	h.feed.Backlog = countedBacklog{count: count}
}

// deployed gives the feed a repository that has taken on this many changes since
// whatever build it is asked about.
func (h *testHarness) deployed(behind int) {
	h.feed.Deployments = countedDeployments{behind: behind}
}

// watchedBuild records a transition of a session that says which binary it is
// running, which is what every session started by a build that stamped a revision
// records.
func (h *testHarness) watchedBuild(t *testing.T, state runstate.WatchState, reason string, at time.Time, build string) {
	t.Helper()
	h.watchedBuildAs(t, "watch-0123456789abcdef0123456789abcdef", state, reason, at, build)
}

func (h *testHarness) watchedBuildAs(t *testing.T, sessionID string, state runstate.WatchState, reason string, at time.Time, build string) {
	t.Helper()
	if err := h.watch.Record(runstate.WatchTransition{
		SchemaVersion: runstate.WatchSchemaVersion,
		ProductID:     "yoyodyne",
		SessionID:     sessionID,
		State:         state,
		At:            at,
		Reason:        reason,
		Build:         build,
	}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
}

// resident makes one pass and returns the delivery the resident stream produced,
// which is where a test reads the parts of it a rendered message does not carry:
// whether the operators were told directly, and the cursor that records it.
func (h *testHarness) resident(t *testing.T, cursors Cursors) Delivery {
	t.Helper()
	batch, err := h.feed.Poll(context.Background(), cursors)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	for _, delivery := range batch.Deliveries {
		if delivery.Stream == residentStream && !delivery.Silent() {
			return delivery
		}
	}
	t.Fatal("nothing was said about the session's build")
	return Delivery{}
}

func (h *testHarness) hold(t *testing.T, reason string, at time.Time) {
	t.Helper()
	if _, err := h.intake.Hold(reason, at); err != nil {
		t.Fatalf("Hold() error = %v", err)
	}
}

// say makes one pass, checks it said exactly what was expected, and returns the
// message as the channel would have it — which is where a test reads what a
// persona actually says rather than only which kind was selected.
func (h *testHarness) say(t *testing.T, cursors Cursors, want notify.Kind) notify.Message {
	t.Helper()
	batch, err := h.feed.Poll(context.Background(), cursors)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	for _, delivery := range batch.Deliveries {
		if delivery.Silent() {
			continue
		}
		if delivery.Notification.Event.Kind != want {
			t.Fatalf("said %q, want %q", delivery.Notification.Event.Kind, want)
		}
		message, err := notify.Render(delivery.Notification.Topic, delivery.Notification.Speaker, delivery.Notification.Event)
		if err != nil {
			t.Fatalf("a selected notification could not be said: %v", err)
		}
		return message
	}
	t.Fatalf("nothing was said, want %q", want)
	return notify.Message{}
}

type countedBacklog struct {
	count int
}

func (b countedBacklog) Ready(context.Context) (int, error) { return b.count, nil }

type brokenBacklog struct{}

func (brokenBacklog) Ready(context.Context) (int, error) {
	return 0, errors.New("bd: executable file not found in $PATH")
}

type countedDeployments struct {
	behind int
}

func (d countedDeployments) Behind(context.Context, string) (int, error) { return d.behind, nil }

// perBuildDeployments answers differently for each build, which is what a real
// repository does and what separating two candidate residents needs: a session on
// what is deployed and a run reserved by something older are one reading only if
// the repository is asked about each of them.
type perBuildDeployments map[string]int

func (d perBuildDeployments) Behind(_ context.Context, build string) (int, error) {
	return d[build], nil
}

type brokenDeployments struct{}

func (brokenDeployments) Behind(context.Context, string) (int, error) {
	return 0, errors.New("git rev-list failed: bad revision")
}

// unrelatedDeployments is the ordinary answer for every product that is not the
// harness's own source: the repository this sink is pointed at has never held the
// revision the session's binary was built from.
type unrelatedDeployments struct{}

func (unrelatedDeployments) Behind(context.Context, string) (int, error) {
	return 0, fmt.Errorf("%w: not in /some-other-product", ErrUnrelatedBuild)
}

// standingTracker is a tracker with an empty admitted queue, which is what the
// four lines need to be readable at all: a nil tracker would make the queue's
// line report a wiring gap rather than an empty backlog.
type standingTracker struct{}

func (standingTracker) List(context.Context, string) ([]beads.WorkItem, error) { return nil, nil }
func (standingTracker) Ready(context.Context) ([]beads.WorkItem, error)        { return nil, nil }

// The heartbeat says the same four lines the terminal prints. Before this it
// said that choosing had stopped and nothing whatever about what the machine was
// doing instead, which is exactly what somebody woken by it at three in the
// morning then has to go and reconstruct.
func TestTheHeartbeatCarriesTheFourLines(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(2)
	harness.watched(t, runstate.WatchStopped, "the session spent the budget it was given", moment)
	held := harness.now
	harness.hold(t, "reordering the backlog first", held)
	harness.feed.Standing = &readmodel.Sources{
		Runs:          harness.runs,
		Conversations: harness.chats,
		Tracker:       standingTracker{},
		Directives:    harness.directives,
		Amendments:    harness.amend,
		OperatorHolds: harness.holds,
		IntakeHolds:   harness.intake,
		Sessions:      harness.watch,
		Capacity:      1,
		Now:           func() time.Time { return harness.now },
	}

	cursors := harness.poll(t, harness.start(), notify.KindWatchStopped, notify.KindIntakeHeld)
	harness.now = held.Add(2 * time.Hour)
	said := harness.say(t, cursors, notify.KindLineWaiting)
	for _, line := range []string{
		"Running: nothing",
		"Working: nothing",
		"Not startable: nothing, of no admitted items",
		"Needs a human (1):",
	} {
		if !strings.Contains(said.Body, line) {
			t.Fatalf("body %q does not carry %q", said.Body, line)
		}
	}
}

// A sink assembled without a way to read the four lines says so, rather than
// leaving them out. A message that simply lacked them would be indistinguishable
// from a harness with nothing in any of them.
func TestAHeartbeatWithNoStandingSaysSoRatherThanOmittingIt(t *testing.T) {
	t.Parallel()

	harness := newTestHarness(t, time.Time{})
	harness.ready(2)
	harness.watched(t, runstate.WatchStopped, "the session spent the budget it was given", moment)
	held := harness.now
	harness.hold(t, "reordering the backlog first", held)

	cursors := harness.poll(t, harness.start(), notify.KindWatchStopped, notify.KindIntakeHeld)
	harness.now = held.Add(2 * time.Hour)
	said := harness.say(t, cursors, notify.KindLineWaiting)
	if !strings.Contains(said.Body, "where the harness stands could not be read here") {
		t.Fatalf("body %q does not say the four lines were unavailable", said.Body)
	}
}
