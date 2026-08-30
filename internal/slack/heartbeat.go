package slack

// The one thing the sink says that no record said first: the line is choosing
// nothing, and work is ready to be chosen.
//
// Everything else here is a reading of something somebody wrote down. A run
// crossed a milestone, an agent filed a report, an operator held intake — each is
// an event, each is said once, and saying it once is right, because a thread is a
// narrative rather than an event log scrolling sideways.
//
// That discipline has one hole in it, and an overnight found it. Intake was held
// at two minutes past midnight and said so; the watch session reached its budget
// and said so; and then ten hours of silence followed that nobody could tell from
// a healthy quiet queue or from a sink that had died. Both messages were correct
// and both were hours stale by the time anybody read them, because a transition
// says what happened rather than what is still true.
//
// So this reports a state rather than a transition, and it is the only thing here
// that does. The rule it holds to is the operator's: silence must always mean
// nothing to do, and never waiting-on-you-without-telling-you. A line that is
// held or idle with admitted work ready is waiting on somebody, so it says so
// again while it stands, at an interval that informs rather than nags, and stops
// the moment the state clears. A line that is idle with nothing ready is not
// waiting on anybody, and stays as silent as it always was.
//
// It costs one tracker read per interval and no provider call, and it is read
// only when something is actually due: a healthy machine polling every fifteen
// seconds asks the tracker nothing at all.

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// DefaultHeartbeat is how often a line that is choosing nothing over ready work
// says so. An hour is the cadence the overnight asks for: ten messages across a
// night nobody was at the machine for is enough for the state to be obvious in a
// morning's scrollback, and few enough that a channel carrying it is still one
// somebody reads.
const DefaultHeartbeat = time.Hour

// maxStoppedReasonBytes bounds the part of a heartbeat that came from an
// operator's own words. The reason a hold carries is bounded generously, because
// it is written once and read carefully; this is said again every hour and has to
// stay a line.
const maxStoppedReasonBytes = 200

// DefaultStaleBuildThreshold is how far behind the deployed harness a running
// session may be before its staleness stops being a line beside the heartbeat
// and becomes something the operators are told directly.
//
// It is a count of changes rather than a length of time, because an afternoon
// with nothing merged in it costs nobody anything and an hour with a fix in it
// costs a night. Twenty is several items' worth of landed work: below it the odds
// that one of them is the bug a session is about to burn a granted round against
// are genuinely small, and above it they stop being. The number is the sink's,
// changeable in one place, and deliberately not a configuration key — an operator
// who could raise it would be buying back the silence this exists to end.
const DefaultStaleBuildThreshold = 20

// Backlog is how much admitted work the tracker itself reports as ready. It is a
// count rather than the items because a count is the whole of what a heartbeat
// says, and because the sink has no business holding an opinion about which items
// they are — that is the scheduler's, and it reads the tracker itself.
type Backlog interface {
	Ready(ctx context.Context) (int, error)
}

// Deployments is how far the repository has moved past one build. It is a count
// rather than the changes themselves for the reason the backlog is a count: the
// count is the whole of what a heartbeat says, and which changes they were is a
// question for the repository rather than for a reporting process.
//
// A comparison that cannot be made is an error rather than a zero. A session
// reported as current because the repository would not answer is exactly the
// false all-clear this exists to end, so the caller says so and asks again at the
// next interval.
type Deployments interface {
	Behind(ctx context.Context, build string) (int, error)
}

// switches is the operator's two holds as one reading. They are read once per
// pass and shared, because the same two files answer both what has to be posted
// about the switches themselves and whether the line is stopped by one.
type switches struct {
	intake       runstate.IntakeHold
	intakeHeld   bool
	operator     runstate.OperatorHold
	operatorHeld bool
}

// waiting is a line with nothing being chosen from it, as the sink derived it.
type waiting struct {
	// stopped is what has stopped the choosing, in the words the message will use.
	stopped string
	// since is when it became that way, which is the whole of what makes the state
	// worth saying again: the state does not change and its age does.
	since time.Time
	// mark names this state durably, so a cursor can say which one it is standing
	// on and a different one re-arms rather than inheriting the last one's clock.
	mark string
}

// heartbeatDeliveries says a line that is choosing nothing over ready work, again
// while it stands.
//
// The order of the checks is the cost model. What has stopped the line is derived
// from files this pass has already read or is about to; only once something is
// actually due does the tracker get asked what is ready, so an idle sink polling
// every fifteen seconds spawns no tracker process at all and a stalled one spawns
// one an hour.
//
// A state the sink has just started seeing is armed silently rather than said.
// The record that made it — the hold, the session stopping — said it as it
// happened, and a second message a moment later would be the sink repeating what
// the channel already has. What this adds is the hour after that, and every hour
// after that.
func (f *HarnessFeed) heartbeatDeliveries(ctx context.Context, cursor Cursor, held switches, sessions []runstate.WatchTransition, inFlight int, streams map[string]struct{}) ([]Delivery, error) {
	if f.Backlog == nil {
		return nil, nil
	}
	streams[heartbeatStream] = struct{}{}

	state, stalled := waitingLine(held, sessions, inFlight)
	if !stalled {
		// The state cleared. Nothing is said about that — what cleared it said so
		// itself, as the release, the resumption, or the run it started — and the
		// cursor forgets it so the next state to stand is armed afresh.
		if cursor.Standing != "" {
			return []Delivery{{Stream: heartbeatStream, Cursor: Cursor{}}}, nil
		}
		return nil, nil
	}
	now := f.now()
	armed := Cursor{Standing: state.mark, Said: now}
	if cursor.Standing != state.mark {
		return []Delivery{{Stream: heartbeatStream, Cursor: armed}}, nil
	}
	if now.Sub(cursor.Said) < f.heartbeat() {
		return nil, nil
	}

	ready, err := f.Backlog.Ready(ctx)
	if err != nil {
		// A tracker that cannot be read leaves the sink unable to tell a line
		// waiting on somebody from an honestly quiet one, and it must not guess in
		// either direction: inventing ready work would nag, and reporting the line
		// as quiet would be the false silence this exists to end. So it is said
		// where the sink says everything else about itself, and asked again at the
		// next interval rather than at the next poll.
		f.say("what is ready to pull could not be read, so nothing was said about a line that has been stopped since %s: %v",
			state.since.UTC().Format(time.RFC3339), err)
		return []Delivery{{Stream: heartbeatStream, Cursor: armed}}, nil
	}
	if ready == 0 {
		// Idle with nothing ready is the healthy quiet the operator asked to keep,
		// so it is silent. The clock is still reset, because what a poll costs is a
		// tracker read and there is no reason to spend one every fifteen seconds on
		// a machine that is behaving.
		return []Delivery{{Stream: heartbeatStream, Cursor: armed}}, nil
	}
	return []Delivery{{
		Stream:       heartbeatStream,
		Cursor:       armed,
		Notification: notify.FromLine(notify.Line{Stopped: state.stopped, Since: state.since, Ready: ready}, now),
	}}, nil
}

// residentDeliveries says that the session choosing work is running a binary the
// harness has moved past, again while it stands.
//
// It is beside the waiting line rather than part of it because the two are true
// at opposite times. The line is said only when nothing is being chosen, and a
// stale session is at its most expensive when it is busiest: the overnight that
// asked for this had three granted repair rounds executing against a bug that was
// fixed and merged the day before, which is a line the waiting heartbeat would
// have been silent through from end to end.
//
// It differs from the line in one other way, and deliberately. A state the line
// has just started seeing is armed silently, because the record that made it — the
// hold, the session stopping — already said it as it happened. Nothing anywhere
// says a session is running an old binary, so there is nothing for a first message
// to repeat: this one is said the first time it is seen, which is what puts it in
// front of somebody before the first granted round is spent against a dead bug.
//
// The cost is the same shape as the line's. What is due is decided from the watch
// log this pass has already read, and only once something is actually due does the
// repository get asked anything, so an idle sink polling every fifteen seconds
// spawns no process at all and a stale one spawns one an hour.
func (f *HarnessFeed) residentDeliveries(ctx context.Context, cursor Cursor, sessions []runstate.WatchTransition, streams map[string]struct{}) ([]Delivery, error) {
	if f.Deployments == nil {
		return nil, nil
	}
	streams[residentStream] = struct{}{}

	build, running := residentBuild(sessions)
	if !running {
		// Either nothing is watching this product, or what is watching it was
		// started by a binary that recorded no revision. Both are comparisons
		// nobody can make rather than sessions that are current, and neither is
		// worth an hourly message: a stopped session is already said where sessions
		// are said, and a binary that stamped nothing is a fact about how somebody
		// built it rather than news about this product.
		if cursor.Standing != "" {
			return []Delivery{{Stream: residentStream, Cursor: Cursor{}}}, nil
		}
		return nil, nil
	}
	now := f.now()
	mark := buildMark + build
	if cursor.Standing == mark && now.Sub(cursor.Said) < f.heartbeat() {
		return nil, nil
	}
	// A build the sink has not measured yet keeps nothing from the last one. The
	// escalation is remembered per build, so a session restarted onto a binary
	// that is still behind is told about afresh rather than inheriting a mark
	// saying somebody was already warned about a different one.
	armed := Cursor{Standing: mark, Said: now}
	if cursor.Standing == mark {
		armed.Delivered = cursor.Delivered
	}

	behind, err := f.Deployments.Behind(ctx, build)
	if err != nil {
		// A repository that cannot be read leaves the sink unable to tell a session
		// running what is deployed from one running a binary from before the fix,
		// and it must not guess in either direction. So it is said where the sink
		// says everything else about itself, and asked again at the next interval.
		f.say("how far the session's build %s is behind could not be read, so nothing was said about it: %v", shortBuild(build), err)
		return []Delivery{{Stream: residentStream, Cursor: armed}}, nil
	}
	if behind <= 0 {
		// The session is running what is deployed, which is the ordinary state and
		// is silent. The clock is still reset, because what a poll costs is a
		// repository read and there is no reason to spend one every fifteen seconds
		// on a machine that is behaving.
		return []Delivery{{Stream: residentStream, Cursor: armed}}, nil
	}

	// Below the threshold this is a note said beside the heartbeat: worth reading,
	// and not worth interrupting somebody for. Past it the session has missed
	// enough that what it is doing can no longer be trusted to be about the work,
	// so it is a degraded system — which is said louder, and said to the operators
	// where they will actually see it rather than in a channel nobody is reading at
	// three in the morning. That direct word is said once per build, because the
	// hourly line is already carrying the state and a second channel repeating it
	// is a channel somebody mutes.
	severity := report.SeverityNote
	direct := false
	if behind >= f.staleBuildThreshold() {
		severity = report.SeverityWarning
		if !armed.Has(escalatedMark) {
			armed = armed.With(escalatedMark)
			direct = true
		}
	}
	return []Delivery{{
		Stream:       residentStream,
		Cursor:       armed,
		Direct:       direct,
		Notification: notify.FromResident(notify.Resident{Build: build, Behind: behind}, severity, now),
	}}, nil
}

// residentBuild is the build the session choosing work is running, and it reads
// the log per session for the reason choosing() does: one log holds every session
// a product has had, and its last entry can be a session stopping while another
// carries on watching. Taking that at face value would report the build of a
// process that is not running.
//
// Where two sessions are alive, the one that acted most recently is the one
// reported. That is a reading rather than a resolution — two residents on one
// product is a state nothing else here has an answer for either — and the honest
// half of it is that a stale session is still named as stale whichever of them it
// is.
func residentBuild(sessions []runstate.WatchTransition) (string, bool) {
	last := make(map[string]runstate.WatchTransition, len(sessions))
	for _, transition := range sessions {
		last[transition.SessionID] = transition
	}
	var live runstate.WatchTransition
	for _, transition := range last {
		if transition.State == runstate.WatchStopped {
			continue
		}
		if transition.At.After(live.At) {
			live = transition
		}
	}
	build := strings.TrimSpace(live.Build)
	return build, build != ""
}

// staleBuildThreshold is how far behind a session has to be before the operators
// are told directly.
func (f *HarnessFeed) staleBuildThreshold() int {
	if f.StaleBuildThreshold > 0 {
		return f.StaleBuildThreshold
	}
	return DefaultStaleBuildThreshold
}

// shortBuild names a revision the way somebody quoting one does. It is the
// sink's own log rather than a message, so it is cut here rather than by the
// voice that renders the message.
func shortBuild(build string) string {
	if len(build) <= 12 {
		return build
	}
	return build[:12]
}

// waitingLine derives what has stopped the line, in the order an operator would
// act on it: the switch that stops everything, then the one that stops the
// choosing, then the sessions that do the choosing or are not there to do it.
//
// A run in flight is not a stalled line whatever else is true. Work is visibly
// moving, the channel is carrying it, and a message saying nothing is being
// chosen while a run posts its way through a review would be false in the way
// that teaches people to stop reading. When those runs end the state is still
// whatever it was, and the heartbeat picks it up then.
func waitingLine(held switches, sessions []runstate.WatchTransition, inFlight int) (waiting, bool) {
	if inFlight > 0 {
		return waiting{}, false
	}
	if held.operatorHeld {
		return waiting{
			stopped: "the operator is holding all harness activity",
			since:   held.operator.HeldAt,
			mark:    "hold:" + stamp(held.operator.HeldAt),
		}, true
	}
	if held.intakeHeld {
		stopped := "intake is held"
		if reason := singleLine(held.intake.Reason, maxStoppedReasonBytes); reason != "" {
			stopped += " — " + reason
		}
		return waiting{
			stopped: stopped,
			since:   held.intake.HeldAt,
			mark:    "intake:" + stamp(held.intake.HeldAt),
		}, true
	}
	return choosing(sessions)
}

// choosing derives the line from what the watch sessions are doing, and it reads
// them per session rather than reading the last line of the log. One log holds
// every session a product has had, and nothing stops two running at once: the
// last entry can be one session stopping while another carries on watching, and
// a reading that took it at face value would report a line that is being pulled
// from as one that nobody is pulling from.
//
// A session choosing work settles it whatever else is in the log. Otherwise a
// session polling an idle queue is the state, and if every session there has ever
// been has ended, there is nobody choosing at all — which is the state the
// overnight was in and the one nothing else says.
func choosing(sessions []runstate.WatchTransition) (waiting, bool) {
	// A product nobody has ever watched is not a line that stopped. Nothing was
	// choosing work here, so nothing is failing to; an operator who runs items by
	// name has a queue by choice, and an hourly message about it would be the
	// nagging this is written to avoid.
	if len(sessions) == 0 {
		return waiting{}, false
	}
	last := make(map[string]runstate.WatchTransition, len(sessions))
	for _, transition := range sessions {
		last[transition.SessionID] = transition
	}
	var idle, stopped runstate.WatchTransition
	for _, transition := range last {
		switch transition.State {
		case runstate.WatchIdle:
			if transition.At.After(idle.At) {
				idle = transition
			}
		case runstate.WatchStopped:
			if transition.At.After(stopped.At) {
				stopped = transition
			}
		default:
			// Watching, braked, or resumed: a session is alive and either choosing
			// or stopped by a hold, which was read before this.
			return waiting{}, false
		}
	}
	if !idle.At.IsZero() {
		return waiting{
			stopped: "the watch session has found nothing it can start",
			since:   idle.At,
			mark:    "idle:" + stamp(idle.At),
		}, true
	}
	return waiting{
		stopped: "no watch session is running",
		since:   stopped.At,
		mark:    "stopped:" + stamp(stopped.At),
	}, true
}

// heartbeat is how often a standing state is said again.
func (f *HarnessFeed) heartbeat() time.Duration {
	if f.Heartbeat > 0 {
		return f.Heartbeat
	}
	return DefaultHeartbeat
}

// singleLine folds what somebody wrote into the one line a repeated message can
// carry, and says where it was cut.
func singleLine(text string, limit int) string {
	folded := strings.Join(strings.Fields(text), " ")
	if len(folded) <= limit {
		return folded
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(folded[cut]) {
		cut--
	}
	return strings.TrimRight(folded[:cut], " ") + "…"
}
