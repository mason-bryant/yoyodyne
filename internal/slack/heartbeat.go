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
// A promotion the forge has not published is the second thing that makes a quiet
// line worth saying, and it is here for the same reason the line itself is. A
// dropped merge is announced once, as it happens; a reader who was away for that
// message has nothing that would ever tell them, and the change sits published
// nowhere while the item reads as landed. So the count rides with the line, and
// the line is said while there is one — which puts a missed announcement back in
// front of somebody at the next quiet tick, and every one after it until the
// publication is settled.
//
// It costs one tracker read per interval and no provider call, and it is read
// only when something is actually due: a healthy machine polling every fifteen
// seconds asks the tracker nothing at all.

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// DefaultHeartbeat is how often a line that is choosing nothing over ready work
// says so. An hour is the cadence the overnight asks for: ten messages across a
// night nobody was at the machine for is enough for the state to be obvious in a
// morning's scrollback, and few enough that a channel carrying it is still one
// somebody reads.
const DefaultHeartbeat = time.Hour

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

// ErrUnrelatedBuild reports a build the repository has never held: the session's
// binary and the repository being asked come from two different histories.
//
// It is a distinct answer rather than one more failure because it means something
// different and calls for something different. A binary is built from the
// harness's own repository, and the repository a sink is pointed at is the
// product's — the same one the tracker and the worktrees use. Those are one
// history only where the product under management is the harness itself, which is
// how the harness develops itself and is not a constraint anything can enforce on
// somebody else's product. Everywhere else the count is not a number about this
// product's work, and there is nothing to say: an ordinary failure would be said
// again every interval about an installation that is behaving exactly as it
// should.
var ErrUnrelatedBuild = errors.New("the build is not a revision this repository holds")

// Deployments is how far the repository has moved past one build. It is a count
// rather than the changes themselves for the reason the backlog is a count: the
// count is the whole of what a heartbeat says, and which changes they were is a
// question for the repository rather than for a reporting process.
//
// A comparison that cannot be made is an error rather than a zero. A session
// reported as current because the repository would not answer is exactly the
// false all-clear this exists to end, so the caller says so and asks again at the
// next interval. A comparison that is not this repository's to make at all is
// ErrUnrelatedBuild, which is not a failure and is not retried at somebody.
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
func (f *HarnessFeed) heartbeatDeliveries(ctx context.Context, cursor Cursor, held switches, sessions []runstate.WatchTransition, inFlight, awaitingForge int, ready func(context.Context) (int, error), streams map[string]struct{}) ([]Delivery, error) {
	if f.Backlog == nil {
		return nil, nil
	}
	streams[heartbeatStream] = struct{}{}

	now := f.now()
	state := waitingLine(held, sessions, inFlight, now)
	if !state.Stopped() {
		// The state cleared. Nothing is said about that — what cleared it said so
		// itself, as the release, the resumption, or the run it started — and the
		// cursor forgets it so the next state to stand is armed afresh.
		if cursor.Standing != "" {
			return []Delivery{{Stream: heartbeatStream, Cursor: Cursor{}}}, nil
		}
		return nil, nil
	}
	mark := state.Mark()
	armed := Cursor{Standing: mark, Said: now}
	if cursor.Standing != mark {
		return []Delivery{{Stream: heartbeatStream, Cursor: armed}}, nil
	}
	if now.Sub(cursor.Said) < f.heartbeat() {
		return nil, nil
	}

	count, err := ready(ctx)
	if err != nil {
		// A tracker that cannot be read leaves the sink unable to tell a line
		// waiting on somebody from an honestly quiet one, and it must not guess in
		// either direction: inventing ready work would nag, and reporting the line
		// as quiet would be the false silence this exists to end. So it is said
		// where the sink says everything else about itself, and asked again at the
		// next interval rather than at the next poll.
		f.say("what is ready to pull could not be read, so nothing was said about a line that has been stopped since %s: %v",
			state.Since.UTC().Format(time.RFC3339), err)
		return []Delivery{{Stream: heartbeatStream, Cursor: armed}}, nil
	}
	if count == 0 && awaitingForge == 0 {
		// Idle with nothing ready and nothing waiting on the forge is the healthy
		// quiet the operator asked to keep, so it is silent. The clock is still
		// reset, because what a poll costs is a tracker read and there is no reason
		// to spend one every fifteen seconds on a machine that is behaving.
		return []Delivery{{Stream: heartbeatStream, Cursor: armed}}, nil
	}
	// The four lines are read here rather than assembled from what this pass
	// happens to hold, because they are the read model's answer and not the
	// sink's: a channel and a terminal saying one standing two ways is the
	// disagreement only the operator could adjudicate. They are read only when
	// something is actually due, which is the same cost rule the rest of this
	// pass keeps.
	return []Delivery{{
		Stream: heartbeatStream,
		Cursor: armed,
		Notification: notify.FromLine(notify.Line{
			Stopped:     state.Says,
			Since:       state.Since,
			Ready:       count,
			Outstanding: awaitingForge,
			Standing:    f.standing(ctx),
		}, now),
	}}, nil
}

// standing is where the harness stands, in the four lines, or nothing at all
// when this sink was assembled without a way to read them. Nothing is what the
// voice states as an absence: a message that simply lacked the lines would be
// indistinguishable from a harness with nothing in any of them.
//
// It is the brief rendering — the four lines with their queues counted rather
// than listed. Nobody asked for this message: it arrives while a state stands,
// again every hour it goes on standing, and what it owes its reader is the one
// sentence that says the line has stopped and enough of the standing to place it.
// The whole rendering is what `yoyo status` prints for somebody who typed it, and
// what an @mention here is answered with, because both of those are asks.
func (f *HarnessFeed) standing(ctx context.Context) string {
	if f.Standing == nil {
		return ""
	}
	return readmodel.ReadStanding(ctx, *f.Standing).RenderBrief()
}

// standingLines is the same reading with the paused banner left off, for the one
// message whose own first sentence is that banner. Saying it twice in one message
// is repetition rather than emphasis; everything else here takes the reading from
// standing above.
func (f *HarnessFeed) standingLines(ctx context.Context) string {
	if f.Standing == nil {
		return ""
	}
	return readmodel.ReadStanding(ctx, *f.Standing).RenderBriefLines()
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
func (f *HarnessFeed) residentDeliveries(ctx context.Context, cursor Cursor, sessions []runstate.WatchTransition, states []runstate.State, streams map[string]struct{}) ([]Delivery, error) {
	if f.Deployments == nil {
		return nil, nil
	}
	streams[residentStream] = struct{}{}

	build, running := residentBuild(sessions, states)
	if !running {
		// Either nothing is choosing or dispatching work on this product, or what
		// is doing it was started by a binary that recorded no revision. Both are
		// comparisons nobody can make rather than sessions that are current, and
		// neither is worth an hourly message: a stopped session is already said
		// where sessions are said, and a binary that stamped nothing is a fact about
		// how somebody built it rather than news about this product.
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
	switch {
	case errors.Is(err, ErrUnrelatedBuild):
		// The session's binary was built from one repository and this sink is
		// pointed at another, which is every product that is not the harness's own
		// source. There is no count to say and nothing is wrong, so the channel
		// hears nothing at all — and the sink's own log says it once per build,
		// because an operator who expected this line needs to know why it is not
		// there and does not need to be told every hour for the life of the
		// process.
		if !armed.Has(unrelatedMark) {
			armed = armed.With(unrelatedMark)
			f.say("the watch session's build %s is not a revision this product's repository holds, so how old the session is cannot be measured here; that comparison is only this sink's to make where the product is the harness's own source", shortBuild(build))
		}
		return []Delivery{{Stream: residentStream, Cursor: armed}}, nil
	case err != nil:
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

// residentBuild is the build the harness is choosing and dispatching work with,
// taken from the records that say so rather than worked out from anything else.
//
// Two records say it, and they are asked in that order. A live watch session's
// own transitions are the direct answer: that process is the resident, and it
// stamps what it was started with on every transition it writes. Where no live
// session names one, the runs still in flight do — a run's record pins the build
// of the harness that reserved it, so a resident whose own log predates the
// stamping is still visible through the work it is dispatching, and so is a
// dispatcher that is not a watch session at all.
//
// A live session's stamp settles it outright, and the runs are consulted only
// where no live session carries one. That is a precedence rather than a contest
// of which record is newer, and it is the precedence because the two records
// answer slightly different questions. A live watch session is the resident by
// definition; a run reserved by some other binary is usually an operator's `yoyo
// run` or a triage carry-out, which is a process that has already ended or is
// about to, and reporting its build as the resident's would name a stale binary
// that is not the one going on choosing work. So a session that says which binary
// it is is believed over the runs beside it even where a run started later.
//
// Within each source it is the most recent that is taken: the newest live session
// that recorded a build, and the latest-started run still in flight. Two residents
// on one product is a state nothing else here has an answer for either, and the
// honest half of it is that a stale build is still named as stale whichever of
// them is carrying it.
func residentBuild(sessions []runstate.WatchTransition, states []runstate.State) (string, bool) {
	// Live is newest first, so the first session naming a build is the latest one
	// that recorded which binary it is.
	for _, session := range readmodel.Live(sessions) {
		if build := strings.TrimSpace(session.Build); build != "" {
			return build, true
		}
	}
	var dispatched runstate.State
	for _, state := range states {
		if state.Status.Terminal() || strings.TrimSpace(state.Build) == "" {
			continue
		}
		if dispatched.RunID == "" || state.StartedAt.After(dispatched.StartedAt) {
			dispatched = state
		}
	}
	build := strings.TrimSpace(dispatched.Build)
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

// waitingLine is what has stopped the line, as the read model derives it and
// with the three states this surface does not speak about removed.
//
// Nothing here works out what stopped the choosing. That derivation is the read
// model's, because the same question is answered in a terminal by `yoyo status`
// and here in a channel, and a machine that says one thing in one place and
// another in the other is a disagreement only the operator can settle — which
// they had to, when this package's own version told them to start a watch
// session that was already running and sitting idle.
//
// What stays here is what this surface says, which is a different question. A
// run in flight is not a stalled line whatever else is true: work is visibly
// moving, the channel is carrying it, and a message saying nothing is being
// chosen while a run posts its way through a review would be false in the way
// that teaches people to stop reading. When those runs end the state is still
// whatever it was, and the heartbeat picks it up then. And a product nobody has
// ever watched is not a line that stopped either — nothing was choosing work
// here, so nothing is failing to, and an hourly message about a queue somebody
// keeps by choice is the nagging this is written to avoid.
//
// The third is the provider's usage window, and it is left out because this
// surface already says it in its own message rather than because it is not worth
// saying. That message is the one shaped to the operator's acceptance — the
// cause first, in his words, at note severity — and this line's sentence puts
// what stopped the choosing after the fact that choosing stopped. Two messages
// about one silence, one of them wording it the way he asked not to be told, is
// worse than one.
func waitingLine(held switches, sessions []runstate.WatchTransition, inFlight int, now time.Time) readmodel.Stall {
	if inFlight > 0 {
		return readmodel.Stall{}
	}
	// Capacity is left unstated. This surface has already decided that a machine
	// with a run in flight is not a stalled line, which is the whole of what the
	// read model's capacity reason would tell it.
	stall := readmodel.WhyNothingStarts(readmodel.Conditions{
		OperatorHold: held.operator,
		OperatorHeld: held.operatorHeld,
		IntakeHold:   held.intake,
		IntakeHeld:   held.intakeHeld,
		Sessions:     func() ([]runstate.WatchTransition, error) { return sessions, nil },
		// This pass's own moment, so a provider usage window the line reports as
		// standing is one that had not lifted when the rest of the pass was read.
		Now: now,
	})
	if stall.Reason == readmodel.ReasonUnwatched || stall.Reason == readmodel.ReasonProviderWindow {
		return readmodel.Stall{}
	}
	return stall
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
