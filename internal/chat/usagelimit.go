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
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/modelfailover"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/spend"
)

// UsageLimits is where a provider's refusal is collected. It is satisfied by
// runstate.UsageLimitStore.
//
// It is read as well as written because failover reads it: which models the
// provider has refused, and until when, is what says whether the next turn
// should ask the configured model at all. That is the same log rather than a
// second one, because a window is one fact and two records of it would be two
// answers.
type UsageLimits interface {
	Record(exhaustion runstate.UsageLimitExhaustion) error
	List() ([]runstate.UsageLimitExhaustion, error)
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

// failoverPolicy is what this conversation's turn may be served by when the
// model it would ask for will not take it — the pinned version the provider has
// not got, or the configured model whose window is closed — and where either
// substitution is written down. A conversation whose agent has pinned no version
// and enabled no failover produces the zero policy, which is both mechanisms
// off: the turn is one invocation under the configured model, exactly as it was.
//
// The substitution is recorded in the same log a refusal is, for the same
// reason a refusal is recorded there at all — what a provider will and will not
// serve is a fact about the product rather than about this conversation, and the
// process that meets it is rarely the process that takes the next turn.
func (s *Session) failoverPolicy() modelfailover.Policy {
	alternate := strings.TrimSpace(s.options.FailoverModel)
	version := strings.TrimSpace(s.options.ModelVersion)
	if alternate == "" && version == "" {
		return modelfailover.Policy{}
	}
	policy := modelfailover.Policy{
		Alternate:         alternate,
		Version:           version,
		Now:               s.options.clock().Now,
		UnknownResetPause: s.options.UsageLimitUnknownResetPause,
		ProductID:         s.options.ProductID,
		// The same sentence a refusal here writes, because it is the same thing
		// that would have stopped — and what makes this entry the other half of
		// that fact is that something served it anyway.
		Waiting:        fmt.Sprintf("the %s conversation %s", RoleTitle(s.state.Role), s.state.ConversationID),
		ConversationID: s.state.ConversationID,
		RecordFailure: func(err error) {
			s.failoverProblem = appendProblem(s.failoverProblem, singleLine(err.Error(), maxTrackerFailureBytes))
		},
	}
	// A conversation with nowhere to record one still fails over — the turn is
	// what matters — and pays a refused invocation each turn to rediscover the
	// window. The log is only wired where there is one, so a typed nil never
	// reaches the failover as a store it can call.
	if s.options.UsageLimits != nil {
		policy.Windows = s.options.UsageLimits
	}
	// The endpoint this conversation is held on, and the providers it may name,
	// so the substitution is checked against this role's tool posture before the
	// turn is moved.
	//
	// A conversation whose endpoint will not resolve substitutes as it did before
	// the check existed, and says so rather than doing it quietly. Refusing the
	// turn instead would trade an answer the operator wants for a check that
	// today can only fail on the provider — the alternate is another model on the
	// same provider — and a skip nobody is told about is the one path where the
	// guarantee silently does not hold. Every conversation the harness opens
	// resolves its endpoint, which is what TestAnOpenedConversationAlwaysResolves-
	// ItsEndpoint holds, so this is a path a running harness does not take.
	endpoint, resolved := s.options.endpoint()
	if !resolved {
		s.failoverProblem = appendProblem(s.failoverProblem, singleLine(fmt.Sprintf(
			"the substitution check was not made: this conversation's endpoint could not be resolved from provider %q, account %q, and model %q",
			s.options.Provider, s.options.AccountAlias, s.options.Model), maxTrackerFailureBytes))
		return policy
	}
	policy.Endpoint = endpoint
	policy.Role = s.options.Role
	policy.Eligibility = s.options.providers()
	// And where the alternate is served, for an agent whose alternate leaves the
	// provider. Everything a crossing needs travels together — the endpoint, the
	// adapter that reaches it, and the way back to the conversation's own record —
	// because a crossing that had two of the three would be a turn sent somewhere
	// it could not be answered from.
	if alternate := s.options.FailoverEndpoint; alternate.Provider != "" && alternate.Provider != endpoint.Provider {
		policy.AlternateEndpoint = alternate
		policy.AlternateAccountConfigDir = s.options.FailoverAccountConfigDir
		policy.Rebuild = s.rebuildFromRecord
		if s.options.FailoverBackend != nil {
			policy.AlternateProvider = s.meteredFailover()
		}
	}
	return policy
}

// meteredFailover is the alternate provider with the cost log wired behind it,
// charged to the account and the provider that actually served the turn. It is
// built here rather than beside the conversation's own meter because it is only
// ever used by a substitution: an agent that never crosses providers never builds
// one, and one that does gets a line saying what the crossing cost and where.
func (s *Session) meteredFailover() modelfailover.Invoker {
	return spend.Metered{
		Provider:    s.options.FailoverBackend,
		Log:         s.options.Spend,
		Attribution: s.failoverAttribution(),
		Clock:       s.options.Clock,
		// The same trade the conversation's own meter makes, for the same reason: a
		// turn the alternate has already answered is not thrown away because the
		// cost log would not take the line.
		RecordFailure: func(err error) {
			s.spendProblem = appendProblem(s.spendProblem, singleLine(err.Error(), maxTrackerFailureBytes))
		},
	}
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
