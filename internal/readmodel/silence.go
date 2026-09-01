package readmodel

// Nothing happening, which is the one state nothing in the record announces.
//
// Every other reading in this package answers a question somebody asked. The
// four lines say where the harness stands, and the stall beside them says what
// stopped the choosing — both of them derived from records something wrote down
// on purpose. That works exactly as long as the thing that would have written
// one is alive to write it. A watch session that crashes writes no stop, a
// wedged one goes on saying it is watching, and both of them look from every
// existing surface like a machine with nothing to do.
//
// So this derives the absence instead. Not "what does the record say is
// stopping the line" but "how long is it since anything actually started, and is
// there anything at all that accounts for it" — a question answered from the
// runs' own start times rather than from any process's account of itself, which
// is why it survives the death of the process it is about. On 2026-09-01 that
// was seven and a half hours of a dead watch that no surface reported and a
// person eventually noticed.
//
// What is here is only the reading. Recording that it happened is the durable
// stall record's, and deciding it is worth waking somebody for is the surface's,
// exactly as the four lines and the heartbeat divide the same work.

import (
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// DefaultStallThreshold is how long nothing may start, over work the tracker
// calls ready and with nothing accounting for it, before that is a stall rather
// than a gap between runs.
//
// Half an hour is chosen against what it costs to be wrong in each direction. A
// watch session polls a drained queue in seconds and starts what it can
// immediately, so half an hour of ready work and no start is already far outside
// anything healthy; and the message this produces is a direct one, said once,
// which is the kind of thing that has to be right the first time or get muted.
// The window that was actually paid for was seven and a half hours.
const DefaultStallThreshold = 30 * time.Minute

// Activity is what a stall reading is derived from: the last time the harness
// demonstrably started work, and everything that could legitimately account for
// nothing having started since.
//
// Every field is passed in rather than read here, for the reason the stall's
// conditions are: the caller has already read all of it for something else, and
// a second reading is a second chance for one pass to report one machine two
// ways.
type Activity struct {
	// Since is the last moment the harness demonstrably started a developer run.
	// Where it has never started one, it is the earliest moment anything recorded
	// that a session was watching this product — which is the earliest point from
	// which a failure to start anything means something. Zero is a harness nothing
	// has ever observed, and nothing is concluded from it.
	Since time.Time
	// Ready is how much admitted work the tracker itself calls ready. It is the
	// whole of what separates a stall from a drained queue, and it costs a tracker
	// read, which is why Unexplained answers without it.
	Ready int
	// Running is how many developer runs are in flight. A run in flight is not a
	// stalled line whatever else is true: work is visibly moving, and a message
	// saying nothing is happening while a run posts its way through a review is
	// false in the way that teaches people to stop reading. It is the same rule
	// the channel heartbeat already holds.
	Running int
	// OperatorHeld and IntakeHeld are the operator's two switches. Each is a
	// deliberate decision that is already said where decisions are said, so
	// neither is a silence anybody needs waking for.
	OperatorHeld bool
	IntakeHeld   bool
	// Watched says a session has at some point watched this product. A product no
	// session has ever watched is not a line that stopped — nothing was choosing
	// work here, so nothing is failing to, and an operator running items by name
	// has a queue by choice.
	Watched bool
	// Threshold is how long nothing may start before it is a stall. Zero takes
	// DefaultStallThreshold.
	Threshold time.Duration
	// Now is when the reading was taken.
	Now time.Time
}

// Silence is one reading of the harness having gone quiet: whether it has, since
// when, over how much ready work, and — where it has not — what accounts for it.
type Silence struct {
	Stalled bool `json:"stalled,omitempty"`
	// Since is when the harness last started anything, which is what the age of
	// the stall is measured from.
	Since time.Time `json:"since,omitempty"`
	// Ready is how much admitted work waited through it.
	Ready int `json:"ready,omitempty"`
	// Explains is what accounts for nothing having started, on a reading that is
	// not a stall. It is the words a closed stall record keeps, so a stall that
	// cleared says what cleared it rather than only stopping.
	Explains string `json:"explains,omitempty"`
}

// Unexplained is the half of the reading that costs nothing: whether anything
// other than an empty queue accounts for nothing having started.
//
// It is separate so that a caller can spend the tracker read only where the
// answer still turns on it. A healthy machine polling every fifteen seconds asks
// the tracker nothing at all, which is the same cost rule the channel heartbeat
// holds.
func (a Activity) Unexplained() bool {
	return a.explanation() == ""
}

// explanation is what accounts for nothing having started, or nothing at all.
// The order is the order a reader would accept them in: the switches somebody
// placed, then the work that is visibly moving, then a product nobody ever
// watched, then a machine too young to have gone quiet.
func (a Activity) explanation() string {
	switch {
	case a.OperatorHeld:
		return "all harness activity is held by the operator"
	case a.IntakeHeld:
		return "intake is held"
	case a.Running > 0:
		return fmt.Sprintf("%d developer run(s) are in flight", a.Running)
	case !a.Watched:
		return "no watch session has ever run on this product"
	case a.Since.IsZero():
		return "nothing is recorded that this product was ever choosing work"
	case a.Now.Sub(a.Since) < a.threshold():
		return "something started within the last " + a.threshold().String()
	default:
		return ""
	}
}

func (a Activity) threshold() time.Duration {
	if a.Threshold > 0 {
		return a.Threshold
	}
	return DefaultStallThreshold
}

// ReadSilence says whether the harness has gone quiet with work waiting.
//
// A drained queue is answered last rather than first, because it is the one
// answer that costs a tracker read: everything above it is derived from records
// the caller already holds, and Unexplained is what lets a caller skip the read
// entirely.
func ReadSilence(activity Activity) Silence {
	silence := Silence{Since: activity.Since, Ready: activity.Ready}
	if explains := activity.explanation(); explains != "" {
		silence.Explains = explains
		return silence
	}
	if activity.Ready <= 0 {
		silence.Explains = "the tracker reports nothing ready to pull"
		return silence
	}
	silence.Stalled = true
	return silence
}

// LastWord is what the sessions that choose work last said about themselves, as
// the one clause a stall is reported with.
//
// It is the whole of what tells a dead scheduler from a wedged one, and that is
// the first thing an operator woken by a stall needs: a session whose last word
// was "stopped" wants starting, and one still claiming to be watching wants
// killing. Neither is derivable from the stall itself, because a stall is
// precisely the absence of anything being written down.
func LastWord(sessions []runstate.WatchTransition) string {
	if len(sessions) == 0 {
		return "no watch session has ever run on this product"
	}
	// Live is newest first, so the first entry is the latest word from a session
	// that has not stopped.
	if live := Live(sessions); len(live) > 0 {
		latest := live[0]
		state := string(latest.State)
		if latest.Restarting {
			state = "stopped to restart into the build deployed over it"
		}
		return fmt.Sprintf("the session choosing work last recorded %s at %s, and has said nothing since",
			state, latest.At.UTC().Format(time.RFC3339))
	}
	var stopped runstate.WatchTransition
	for _, transition := range sessions {
		if transition.State == runstate.WatchStopped && transition.At.After(stopped.At) {
			stopped = transition
		}
	}
	if stopped.At.IsZero() {
		return "no watch session is running"
	}
	return "no watch session is running; the last one stopped at " +
		stopped.At.UTC().Format(time.RFC3339)
}

// LastStart is the last moment the harness demonstrably started a developer run,
// or — where it never has — the earliest moment anything recorded that a session
// was watching this product.
//
// The fallback is what makes a machine that has never started anything readable
// at all. Without it a product whose scheduler died before its first run would
// have no anchor and would therefore never be reported as stalled, which is the
// case that most looks like the harness working.
func LastStart(runs []runstate.State, sessions []runstate.WatchTransition) time.Time {
	var latest time.Time
	for _, run := range runs {
		if run.StartedAt.After(latest) {
			latest = run.StartedAt
		}
	}
	if !latest.IsZero() {
		return latest
	}
	var earliest time.Time
	for _, transition := range sessions {
		if transition.At.IsZero() {
			continue
		}
		if earliest.IsZero() || transition.At.Before(earliest) {
			earliest = transition.At
		}
	}
	return earliest
}
