package chat

// Compacting a provider session before it outgrows the request it is sent in.
//
// Every turn but the first resumes a provider session, and a resumed turn sends
// the whole of it: everything the session has been handed and everything it has
// said, again, with the new prompt on the end. So a session grows by every turn
// it takes, and the provider's request has a ceiling — 32 MB on the API these
// conversations are served from. The provider's own compaction triggers on its
// token threshold rather than on that ceiling, and compacting sends the whole
// conversation too, so by the time it tried on chat-419cedb4a013b063f477e322a2a60466
// the session was about 34 MB and could not be sent even to be made smaller.
//
// So the harness keeps its own measure of each session, in bytes, and compacts
// while the session still fits. The measure is what the harness itself put into
// the session and got back out of it — every prompt it sent and every reply it
// was given — which is less than what the provider's request carries: the
// provider adds its own framing, the JSON the request is encoded in escapes
// the text, and a reply's reasoning is kept in the session without ever reaching
// the harness. That is why the budget is a quarter of the ceiling rather than
// most of it.
//
// A compaction is the rebuild a crossing makes, applied to the provider that is
// already holding the conversation: the turn is sent with no session to resume,
// and what the session was carrying comes from the harness's own record instead
// — the picture the conversation is working from and the most recent of what has
// been said, bounded by the rebuild's own budget. Nothing is sent to the old
// session to shrink it, so a compaction never needs the session to fit, and the
// provider's answer starts a new session the measure starts again from.

import (
	"errors"
	"fmt"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// SessionBudgetBytes is the size past which a provider session is compacted
// before its next turn: a quarter of the 32 MB the provider accepts in one
// request, because what the harness measures is only the part of a session it
// wrote and read. See the comment at the top of this file.
const SessionBudgetBytes = 8 << 20

// ErrCompactionFailed marks the failure of a turn that was not sent because the
// session it would have resumed had to be compacted first and could not be. It
// is not a refusal the provider made and nothing about waiting will change it,
// so a caller that gives a declined turn back to be asked again must not give
// this one back: the next turn meets the same session, compacts again, and says
// again why it could not.
var ErrCompactionFailed = errors.New("the provider session could not be compacted, so the turn was not sent")

// Why a session is compacted, as the event recording the compaction says it.
const (
	// compactionOverBudget is a session whose next turn would take it past the
	// budget.
	compactionOverBudget = "over_budget"
	// compactionUnmeasured is a session recorded before the harness measured
	// sessions, whose size nobody knows. It is compacted once, on its next turn,
	// rather than assumed small: the conversation this was built for was one of
	// them, and it was already past the ceiling.
	compactionUnmeasured = "unmeasured"
)

// compaction is the decision to compact the session a turn would resume.
type compaction struct {
	reason string
	// sessionBytes is what the session measured before this turn, and turnBytes
	// what this turn's request would have carried beside it: the system prompt,
	// which is sent with every request rather than kept in the session, and the
	// turn's own prompt.
	sessionBytes int
	turnBytes    int
	budget       int
}

// sessionBudget is the budget this conversation compacts on.
func (o Options) sessionBudget() int {
	if o.SessionBudgetBytes > 0 {
		return o.SessionBudgetBytes
	}
	return SessionBudgetBytes
}

// compactionDue decides whether the turn about to be taken would take the session
// it resumes past the budget, and so has to be sent without it. It is nil where
// there is no session to resume, because a turn with none starts a session
// rather than growing one, and nil where the session has room.
func (s *Session) compactionDue(systemPrompt, prompt string) *compaction {
	if s.resumableSession() == "" && s.alternateSession() == "" {
		return nil
	}
	due := &compaction{
		sessionBytes: s.state.ProviderSessionBytes,
		turnBytes:    len(systemPrompt) + len(prompt),
		budget:       s.options.sessionBudget(),
	}
	switch {
	case s.state.ProviderSessionBytes == 0:
		due.reason = compactionUnmeasured
	case due.sessionBytes+due.turnBytes > due.budget:
		due.reason = compactionOverBudget
	default:
		return nil
	}
	return due
}

// compact prepares the turn's prompt to be sent with no session and the
// conversation rebuilt from its record in front of it, and records that it did.
// From here until the turn ends, the session the record holds is not offered to
// either endpoint, so neither resumes what was just compacted away.
//
// A rebuild that cannot be made is recorded as a failed compaction and ends the
// turn before the provider is asked: sending the turn on the old session anyway
// is sending the request that would not fit, and sending it with nothing in front
// of it is the role answering with none of the conversation it is in.
func (s *Session) compact(systemPrompt, prompt string, due compaction) (string, error) {
	payload := map[string]any{
		"reason":        due.reason,
		"session_id":    s.state.ProviderSessionID,
		"session_bytes": due.sessionBytes,
		"turn_bytes":    due.turnBytes,
		"budget_bytes":  due.budget,
	}
	s.compacting = true
	rebuilt, err := s.rebuiltPrompt(systemPrompt, prompt)
	if err != nil {
		s.compacting = false
		payload["error"] = singleLine(err.Error(), maxTrackerFailureBytes)
		return prompt, errors.Join(
			fmt.Errorf("%w: %w", ErrCompactionFailed, err),
			s.emit(execution.EventSessionCompactionFailed, payload),
		)
	}
	payload["rebuilt_bytes"] = len(rebuilt) - len(prompt)
	if err := s.emit(execution.EventSessionCompacted, payload); err != nil {
		s.compacting = false
		return prompt, err
	}
	return rebuilt, nil
}

// measureSession brings the session measure up to date after a turn the provider
// served: what this turn sent and got back is added to the session it resumed,
// or is the whole of a session it started.
//
// resumed is read before the record moves on to the endpoint that served the
// turn, because what it asks is whether that endpoint was handed a session of
// its own to continue — which is the session the record held, on the provider
// the record said held it.
func (s *Session) measureSession(resumed bool, prompt, reply string) {
	added := len(prompt) + len(reply)
	if resumed {
		s.state.ProviderSessionBytes += added
	} else {
		s.state.ProviderSessionBytes = added
	}
	s.state.ProviderSessionBudgetBytes = s.options.sessionBudget()
}

// resumedOn reports whether the turn served on this endpoint continued a session
// the record already held for it, rather than starting one.
func (s *Session) resumedOn(serving backend.Endpoint) bool {
	if s.compacting {
		return false
	}
	if serving.Provider == "" || serving.Provider == s.options.Provider {
		return s.resumableSession() != ""
	}
	return s.alternateSession() != ""
}
