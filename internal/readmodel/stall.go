package readmodel

// Why the harness is choosing nothing, derived once for every surface that says
// it.
//
// This derivation existed twice before it lived here: the standing status worked
// it out from the switches, the capacity, and the watch log, and the channel
// heartbeat worked it out again from the same three records a few packages away.
// Two readings of one machine is a disagreement only the operator can
// adjudicate, and these two already disagreed — a live session sitting idle over
// a queue it would not touch was reported by one as a session that had found
// nothing it could start, and by the other as no session running at all, which
// told the operator to start the session that was already there.
//
// A third copy is expected and is not on this line yet. The work item that
// consolidated these names an escalation path in the Slack sink — the push half
// of the heartbeat, which DMs the operators when the system is stopped — as
// carrying its own answer to the same question; that file was written on the
// branch for yoyodyne-ifd.68.20 and has never been merged here, so there was
// nothing in this tree to route through this. It is named here rather than left
// out because the omission is what would make it look considered: when that work
// lands it projects this, and does not derive the stopped state again. What it
// would otherwise inherit is a disagreement this change has just widened, since
// the wording and the idle-versus-absent answer both moved when they came here.
//
// # The taxonomy is closed
//
// Every state this can be in is a named Reason, and every Reason says whose move
// it is. That is the point of naming them rather than assembling a sentence at
// each call site: a reason nobody named is a reason nobody can act on, and the
// state an operator most needs is exactly the one no author thought to write a
// sentence for. A reading with no Reason at all is not a residual category — it
// is the harness saying it would start the next pullable item, which is a
// different answer and the one that makes a startable item's absence from the
// not-startable line mean something.
//
// What is not here is what each surface does about it. Whether a state is worth
// interrupting somebody for, how often it is repeated, and what else is said
// beside it are the surface's decisions, and the two surfaces here make them
// differently on purpose: a terminal answers when it is asked, and a channel
// speaks unprompted and has to be worth reading.

import (
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Reason is one member of the fixed set of reasons the harness is choosing no
// work. The set is closed: a state outside it cannot be reported, which is what
// makes an unnamed reason impossible rather than unlikely.
//
// The values are the tokens a durable cursor already holds — the sink names the
// state it is standing on by them, so that a different state re-arms its clock
// rather than inheriting the last one's. They are kept as they were found for
// that reason: renaming one would re-arm every state standing at the moment this
// landed, and the hour that costs is an hour of exactly the silence this exists
// to end.
type Reason string

const (
	// ReasonOperatorHold is the switch over everything the harness would spend.
	ReasonOperatorHold Reason = "hold"
	// ReasonIntakeHold is the switch over the work the harness chooses for itself.
	ReasonIntakeHold Reason = "intake"
	// ReasonNoCapacity is a machine with every developer slot taken. It is the one
	// reason here that is the harness working rather than the harness stopped.
	ReasonNoCapacity Reason = "capacity"
	// ReasonProviderWindow is a live session waiting out the provider's usage
	// window. It is distinct from an idle session because an operator does nothing
	// at all about it: the window lifts on the provider's clock, and a surface that
	// reported this as a session finding nothing to start would be sending somebody
	// to look at a queue that is fine.
	ReasonProviderWindow Reason = "provider"
	// ReasonSessionIdle is a live session that is choosing nothing. It is distinct
	// from having no session at all because an operator does an entirely different
	// thing about it, and because telling them to start a session they are already
	// running is worse than telling them nothing.
	ReasonSessionIdle Reason = "idle"
	// ReasonNoWatchSession is a product that was being watched and is not any more.
	ReasonNoWatchSession Reason = "stopped"
	// ReasonUnwatched is a product no session has ever watched. It is not a line
	// that stopped: nothing was choosing work here, so nothing is failing to, and
	// an operator running items by name has a queue by choice.
	ReasonUnwatched Reason = "unwatched"
)

// Reasons is the whole taxonomy, in the order an operator acts on it. A caller
// that has to cover every reason reads it from here rather than repeating the
// list.
func Reasons() []Reason {
	return []Reason{
		ReasonOperatorHold,
		ReasonIntakeHold,
		ReasonNoCapacity,
		ReasonProviderWindow,
		ReasonSessionIdle,
		ReasonNoWatchSession,
		ReasonUnwatched,
	}
}

// Whose is whose move it is, and what settles it. It is half of what a reason is
// for: a surface that says work is held without saying who by has told the
// reader something they can do nothing with.
//
// Every reason answers. One that did not would be a state named and then left
// unattributed, which is the hole the taxonomy exists to close, so the zero
// answer belongs to no reason and a test holds the set to it.
func (r Reason) Whose() string {
	switch r {
	case ReasonOperatorHold:
		return "the operator's — nothing runs until `yoyo resume` lifts it"
	case ReasonIntakeHold:
		return "the operator's — nothing new is chosen until `yoyo release` lifts it"
	case ReasonNoCapacity:
		return "nobody's — a slot frees as a run in flight finishes"
	case ReasonProviderWindow:
		return "nobody's — the harness asks again when the provider's usage window lifts"
	case ReasonSessionIdle:
		return "the operator's — a queue with ready work and an idle session is a stall rather than a rest"
	case ReasonNoWatchSession, ReasonUnwatched:
		return "the operator's — nothing pulls the queue until `yoyo work --watch` starts a session"
	default:
		return ""
	}
}

// Conditions are the records one reading of the stall is derived from. They are
// passed in rather than read here because both callers have already read them
// for something else, and a second reading is a second chance for one pass to
// report a state two ways.
type Conditions struct {
	OperatorHold runstate.OperatorHold
	OperatorHeld bool
	IntakeHold   runstate.IntakeHold
	IntakeHeld   bool
	// Running is how many developer runs are in flight, read against Capacity. A
	// caller that has already decided a run in flight is not a stalled line leaves
	// both at zero and gets the rest of the answer.
	Running  int
	Capacity int
	// Sessions is the watch log, asked for only if the question reaches it: the
	// switches and the machine's own capacity are answered from records the caller
	// already holds, and a caller that would have to spend a read to answer this
	// spends it only when nothing before it has. A caller with the log in hand
	// returns it and never fails.
	Sessions func() ([]runstate.WatchTransition, error)
	// Now is when the reading was taken, which is what says whether a provider's
	// usage window a session recorded is still standing or has already lifted. It
	// defaults to the wall clock, so a caller with no particular moment in mind
	// passes none.
	Now time.Time
}

func (c Conditions) now() time.Time {
	if c.Now.IsZero() {
		return time.Now().UTC()
	}
	return c.Now.UTC()
}

// Stall is why nothing is being chosen: the named reason, what it says, when it
// became true, and what could not be read where nothing could be answered. A
// stall with no reason is the harness saying it would start the next pullable
// item.
type Stall struct {
	Reason Reason `json:"reason,omitempty"`
	// Says is the state as a clause, with no remedy in it, because it is read
	// inside sentences the surfaces write around it.
	Says string `json:"says,omitempty"`
	// Clears is what settles the state, where a command settles it. It is separate
	// from Says so a surface can say the state without the instruction.
	Clears string `json:"clears,omitempty"`
	// Since is when it became this way, which is what makes a standing state worth
	// saying again: the state does not change and its age does.
	Since time.Time `json:"since,omitempty"`
	// Problem is why the question could not be answered. A reading that carries one
	// names no reason: a stall invented over a record nobody could read is the
	// confident emptiness every answer in this package is written to avoid.
	Problem string `json:"problem,omitempty"`
}

// Stopped reports whether anything at all is stopping the choosing.
func (s Stall) Stopped() bool { return s.Reason != "" }

// Refusal is the stall as the one line a status prints against an item nothing
// will pull: what stopped it, and what lifts it.
//
// It says nothing about how long the state has stood, and that is a division of
// labour rather than an omission. Every stall that is waiting on a person is on
// the attention line as well — the switches in their own right, the two session
// states through Waiting below — and that line already carries since-when beside
// whose move it is. A refusal that repeated it would say one timestamp twice in
// one reading of four lines whose whole value is that they are read at a glance.
func (s Stall) Refusal() string {
	if !s.Stopped() {
		return ""
	}
	if s.Clears == "" {
		return s.Says
	}
	return s.Says + "; " + s.Clears
}

// Waiting is the stall as one thing waiting on a person, where it is one. It
// carries since-when, in the shape the attention line's other entries already
// say it: how long something has been waiting on somebody is half of what makes
// it worth acting on.
//
// Two reasons are excluded and neither is an oversight. The switches are already
// on the attention line in their own right — a hold waits on the operator whether
// or not it is currently what stops the choosing — and listing them again from
// here would be one state said twice in one reading. A full machine and a product
// nobody has ever watched wait on nobody: the first is the harness working, and
// the second is an operator who runs items by name getting told they have a
// problem they chose.
func (s Stall) Waiting() (Attention, bool) {
	switch s.Reason {
	case ReasonSessionIdle, ReasonNoWatchSession:
		what := s.Says
		if !s.Since.IsZero() {
			what += ", since " + s.Since.UTC().Format(time.RFC3339)
		}
		return Attention{What: what, Whose: s.Reason.Whose()}, true
	default:
		return Attention{}, false
	}
}

// Mark names the stall durably, so a surface that repeats a standing state can
// say which one it is standing on and re-arm its clock when a different one
// takes over.
func (s Stall) Mark() string {
	if !s.Stopped() {
		return ""
	}
	return string(s.Reason) + ":" + s.Since.UTC().Format(time.RFC3339Nano)
}

// WhyNothingStarts is the one derivation of what has stopped the choosing.
//
// The order is the order an operator acts in: the switch that stops everything,
// then the one that stops the choosing, then the machine being full, then the
// sessions that do the choosing or are not there to do it. It is also the cost
// order — the watch log is read last and only where nothing before it answered.
func WhyNothingStarts(conditions Conditions) Stall {
	switch {
	case conditions.OperatorHeld:
		return Stall{
			Reason: ReasonOperatorHold,
			Says:   "all harness activity is held by the operator",
			Clears: "`yoyo resume` lifts it",
			Since:  conditions.OperatorHold.HeldAt,
		}
	case conditions.IntakeHeld:
		says := "intake is held"
		if reason := singleLine(conditions.IntakeHold.Reason, maxRefusalBytes); reason != "" {
			says += " — " + reason
		}
		return Stall{
			Reason: ReasonIntakeHold,
			Says:   says,
			Clears: "`yoyo release` lifts it",
			Since:  conditions.IntakeHold.HeldAt,
		}
	case conditions.Capacity > 0 && conditions.Running >= conditions.Capacity:
		return Stall{
			Reason: ReasonNoCapacity,
			Says: fmt.Sprintf("every developer slot is taken: %d of %d in flight",
				conditions.Running, conditions.Capacity),
		}
	case conditions.Sessions == nil:
		return Stall{Problem: "nothing was wired to read the sessions that choose work"}
	}
	sessions, err := conditions.Sessions()
	if err != nil {
		return Stall{Problem: fmt.Sprintf("what the harness is choosing work with could not be read: %v", err)}
	}
	return whichSession(sessions, conditions.now())
}

// whichSession is the stall as the watch log has it. A session choosing work
// settles it whatever else is in the log; otherwise a live session polling an
// idle queue is the state, and a log whose every session has ended is nobody
// choosing at all — which is the state the overnight was in and the one nothing
// else says.
//
// A session that recorded itself waiting out the provider's usage window is
// answered ahead of the plain idle one, because the two look identical from
// every other record and mean opposite things: one is a queue nobody is pulling
// and the other is a queue the provider will not let anybody pull yet.
func whichSession(sessions []runstate.WatchTransition, now time.Time) Stall {
	if len(sessions) == 0 {
		return Stall{
			Reason: ReasonUnwatched,
			Says:   "no watch session has ever run on this product, so nothing pulls the queue",
			Clears: "`yoyo work --watch` starts one",
		}
	}
	// Live is newest first, so the first idle session it holds is the latest one.
	live := Live(sessions)
	for _, transition := range live {
		if transition.State != runstate.WatchIdle {
			// Watching, braked, or resumed: a session is alive and either choosing or
			// stopped by a hold, which was read before this.
			return Stall{}
		}
	}
	if len(live) > 0 {
		if window := WaitingOnProvider(sessions); window.Standing(now, 0) {
			return Stall{
				Reason: ReasonProviderWindow,
				Says:   window.Says(),
				Since:  window.Since,
			}
		}
		return Stall{
			Reason: ReasonSessionIdle,
			Says:   "the watch session has found nothing it can start",
			Since:  live[0].At,
		}
	}
	var stopped runstate.WatchTransition
	for _, transition := range sessions {
		if transition.State == runstate.WatchStopped && transition.At.After(stopped.At) {
			stopped = transition
		}
	}
	return Stall{
		Reason: ReasonNoWatchSession,
		Says:   "no watch session is running, so nothing pulls the queue",
		Clears: "`yoyo work --watch` starts one",
		Since:  stopped.At,
	}
}
