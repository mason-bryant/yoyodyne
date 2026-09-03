package chat

// A turn the provider declined for want of capacity, written down where
// somebody who is not at this terminal can read it.
//
// A run that meets an exhausted limit parks, and the park is durable, so the
// sink says it without anybody being present. A conversation has no such record:
// the turn fails, the operator who typed the message is told, and the durable
// account of the product says nothing at all. That is the wrong silence to keep,
// because an exhausted limit is not this conversation's problem — it is every
// process's, for as long as it lasts.
//
// So the refusal is recorded here and nothing else about the turn changes. It
// is not a pause: a conversation is somebody typing, and the harness does not
// put an operator to sleep for six hours. The turn fails exactly as it did
// before, and what is new is that the failure leaves a trace.

import (
	"errors"
	"fmt"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// UsageLimits is where a provider's refusal is collected. It is satisfied by
// runstate.UsageLimitStore.
type UsageLimits interface {
	Record(exhaustion runstate.UsageLimitExhaustion) error
}

// noteUsageLimit records a provider refusal this turn met, and reports only what
// went wrong recording it. A turn that was not refused, and a conversation with
// nowhere to record one, both record nothing and say nothing: the caller is
// already failing the turn on the refusal itself, and this adds a durable trace
// of it rather than another way for the turn to fail.
func (s *Session) noteUsageLimit(result backend.RunResult, err error) error {
	limit := refusedForUsageLimit(result, err)
	if limit == nil || s.options.UsageLimits == nil {
		return nil
	}
	exhaustion := runstate.UsageLimitExhaustion{
		SchemaVersion: runstate.UsageLimitSchemaVersion,
		ProductID:     s.options.ProductID,
		At:            s.options.clock().Now(),
		// Which conversation was stopped is the whole of what an operator needs
		// beside the limit itself: a refused architect and a refused product
		// manager are the same limit stopping different work.
		Waiting:        fmt.Sprintf("the %s conversation %s", RoleTitle(s.state.Role), s.state.ConversationID),
		Kind:           limit.Kind,
		ConversationID: s.state.ConversationID,
	}
	if !limit.ResetsAt.IsZero() {
		resetsAt := limit.ResetsAt.UTC()
		exhaustion.ResetsAt = &resetsAt
	}
	if err := s.options.UsageLimits.Record(exhaustion); err != nil {
		return fmt.Errorf("record the provider's refusal: %w", err)
	}
	return nil
}

// ErrProviderCapacity marks the failure of a turn the provider declined for
// want of capacity, rather than one it answered badly. It is a sentinel joined
// into the error the turn fails with, so what an operator reads is unchanged and
// a caller that is not a person can still tell the two apart.
//
// The distinction is what the caller owes the turn. A turn nobody was asked is
// worth asking again once the limit resets; one the role answered is not, and
// asking again would be the same money spent on the same answer. It exists
// because the harness now takes turns nobody is sitting in front of — a stopped
// run delivered into the development manager's conversation — and a refusal met
// there must not read as her having been asked.
var ErrProviderCapacity = errors.New("the provider declined this turn for want of capacity")

// providerDeclined is that sentinel for a turn the provider refused, and nothing
// for every other ending.
func providerDeclined(result backend.RunResult, err error) error {
	if refusedForUsageLimit(result, err) == nil {
		return nil
	}
	return ErrProviderCapacity
}

// refusedForUsageLimit reports a turn the provider declined for want of
// capacity, rather than one it answered. The limit is only a refusal where the
// invocation actually failed: a provider may report a limit alongside an answer
// it still gave, and an answered turn is not something anybody is waiting on.
func refusedForUsageLimit(result backend.RunResult, err error) *backend.UsageLimit {
	if result.UsageLimit == nil || (err == nil && !result.IsError) {
		return nil
	}
	return result.UsageLimit
}
