package chat

// The operator's hold on harness activity, as a conversation meets it.
//
// A turn is a provider invocation like any other, so it spends like one, and a
// pause that covered runs but not conversations would be a pause the operator
// could watch themselves spend through. What differs from a run is what can be
// done about it. A run parks: it has durable state, a claimed item, and a
// worktree waiting for it, so it waits and carries on unaided. A conversation
// has a person sitting in front of it, and holding their prompt for an hour
// would be worse than telling them — so a held turn is refused with the reason
// stated, and the conversation itself stays open. Nothing about it is lost:
// saying the same thing again once the hold is lifted takes the turn that was
// refused.

import (
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// OperatorHolds is the operator's switch over provider spending, as a
// conversation reads it. It is satisfied by runstate.OperatorHoldStore.
type OperatorHolds interface {
	Held() (runstate.OperatorHold, bool, error)
}

// OperatorHoldError reports a turn refused because the operator paused all
// harness activity. It is its own type so the conversation can tell it from the
// failures that end a conversation: nothing is wrong, and the operator is one
// command away from carrying on.
type OperatorHoldError struct {
	Hold runstate.OperatorHold
}

func (e *OperatorHoldError) Error() string {
	return fmt.Sprintf("all harness activity is paused, since %s, so this turn was not sent to the provider; `yoyo resume` lifts it and saying this again takes the turn",
		e.Hold.HeldAt.Format(time.RFC3339))
}

// heldByOperator reports the hold a turn must not be taken through. A
// conversation wired without a hold to read is one nothing can pause, which is
// what every conversation was before the switch existed; a hold that cannot be
// read is not that, and refuses the turn rather than being spent through.
func (s *Session) heldByOperator() (runstate.OperatorHold, bool, error) {
	if s.options.Holds == nil {
		return runstate.OperatorHold{}, false, nil
	}
	hold, held, err := s.options.Holds.Held()
	if err != nil {
		return runstate.OperatorHold{}, false, fmt.Errorf("read whether the operator has paused harness activity: %w", err)
	}
	return hold, held, nil
}

// operatorHoldBanner is what a status report leads with while the harness is
// paused. It is deliberately the loudest thing on the page: a system somebody
// paused and forgot looks exactly like a system that died, and the difference
// between those two is one command.
//
// A hold that cannot be read is reported rather than passed over. "I could not
// find out whether we are paused" is a different fact from "we are not", and
// only one of them means the quiet is expected.
func (s *Session) operatorHoldBanner() string {
	hold, held, err := s.heldByOperator()
	if err != nil {
		return "COULD NOT READ THE PAUSE: " + err.Error() + "\n\n"
	}
	if !held {
		return ""
	}
	return strings.Join([]string{
		"PAUSED: all harness activity is paused, since " + hold.HeldAt.Format(time.RFC3339) + ".",
		"Nothing reaches the provider until `yoyo resume` lifts it: turns here are refused, and runs park at their next provider call keeping everything they have.",
		"", "",
	}, "\n")
}
