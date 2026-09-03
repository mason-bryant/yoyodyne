package notify

// What an item's thread says about itself without being opened.
//
// A message is a transition said once, which is what makes a thread a
// narrative; it is also what makes a channel of threads unreadable at a glance,
// because the state an item is in right now is somewhere inside a thread rather
// than on it. This is the other half: a status the surface carries on the
// message a thread hangs from, replaced as the record moves, so a scan of the
// channel answers what is working, what is blocked, and what landed without
// anything being opened.
//
// It is a status rather than prose because it changes far more often than
// anything worth a message does. A state that moves back and forth between
// working and in review would be noise as messages and is exactly what a mark on
// the opener is for.
//
// The set is small and fixed on purpose, and it is four words rather than a
// vocabulary that grows: a status set anybody can extend becomes a second
// severity system nobody ratified, and the whole value of a scan is that a
// reader already knows every mark they can see. Nothing here is derived from a
// severity and nothing shares a symbol with one — severity is how much attention
// one message asks for, and this is what the item is doing.

import "github.com/mason-bryant/yoyodyne/internal/runstate"

// Status is what an item is doing, as the durable record says it. The four are
// the whole set.
type Status string

const (
	// StatusWorking is a run in flight on the item: developing, checking,
	// repairing, integrating. It is the ordinary state of live work.
	StatusWorking Status = "working"
	// StatusInReview is a run whose change is with the reviewer. It is separate
	// from working because it is the one stretch of a run where nothing is being
	// written, and a reader deciding where to look wants to know it.
	StatusInReview Status = "in-review"
	// StatusBlocked is a run that stopped: failed, cancelled, or parked waiting on
	// a provider, the operator, or an unresolved directive. It is the one status
	// that means somebody may have to do something, which is what a channel scan
	// is mostly for.
	StatusBlocked Status = "blocked"
	// StatusCompleted is a run that finished and succeeded. It says the harness's
	// attempt on the item landed, which is what the record holds; whether the
	// publication behind it has been merged is said in the thread by the messages
	// that report it.
	StatusCompleted Status = "completed"
)

// Statuses is the whole set, in the order work reaches them. A caller that has
// to cover every status reads it from here rather than repeating the list.
func Statuses() []Status {
	return []Status{StatusWorking, StatusInReview, StatusBlocked, StatusCompleted}
}

// Valid reports whether a name is one of the four. Anything else has no symbol,
// so a surface refuses it rather than marking a thread with something no reader
// has been told the meaning of.
func (s Status) Valid() bool {
	switch s {
	case StatusWorking, StatusInReview, StatusBlocked, StatusCompleted:
		return true
	default:
		return false
	}
}

// Symbol is the emoji shortcode a surface marks a thread's opener with. They are
// stated here beside the words rather than in the surface that posts them, for
// the reason the voice table is where it is: the mark is part of the vocabulary,
// and a second surface must not be able to say the same status a second way.
//
// Every one of them is an emoji Slack ships in every workspace. A custom name
// would be a mark that renders in the workspace it was chosen in and is refused
// in every other, which is a setup step nobody in the setup document is told
// about. None of them repeats a severity's mark: a reader must not have to work
// out whether a symbol is about one message or about the whole item.
func (s Status) Symbol() string {
	switch s {
	case StatusWorking:
		return "hammer_and_wrench"
	case StatusInReview:
		return "eyes"
	case StatusBlocked:
		return "octagonal_sign"
	case StatusCompleted:
		return "white_check_mark"
	default:
		return ""
	}
}

// StatusOfRun reads what an item is doing out of one run's durable state. It is
// a reading rather than a transition, which is what separates it from everything
// selection says: a status is what is true now, so it is derived afresh from the
// record on every pass and never accumulated.
//
// A parked run reads as blocked rather than as a status of its own. It is
// stopped and what restarts it is outside the run — a provider's capacity, the
// operator, a directive nobody has resolved — which is the same thing a reader
// scanning for what needs them is looking for, and the mark comes off the moment
// the run continues. A terminal run that did not succeed is blocked for the same
// reason: cancelled, timed out, or failed, it stopped and stayed stopped.
//
// Which of those a terminal run is comes from the read model's outcome rather
// than from its status, so that the completion mark here means what "succeeded"
// means everywhere else. A run whose promotion a sweep contradicted keeps the
// status it recorded and carries a blocker, and the precedence rule on
// runstate.Ending is what says such a run stopped; reading the status here put a
// completion mark on an item a person had been handed.
func StatusOfRun(state runstate.State) Status {
	switch {
	case state.Status.Terminal():
		if state.Outcome() == runstate.OutcomeSucceeded {
			return StatusCompleted
		}
		return StatusBlocked
	case parked(state):
		return StatusBlocked
	case state.Phase == runstate.PhaseReviewing:
		return StatusInReview
	default:
		return StatusWorking
	}
}
