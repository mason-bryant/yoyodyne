package chat

// A turn the provider declined for want of capacity: written down where somebody
// who is not at this terminal can read it, and waited out rather than failed on.
//
// A run that meets an exhausted limit records the reset time, waits, and reissues
// the attempt it was refused. A conversation turn used to do none of that: it
// failed, and whatever the turn was about to do never happened. On 2026-08-17 a
// product-manager turn read the tracker seven times, delivered its reply
// preamble, and died on the continuation invocation with a session limit that
// reset an hour later; the decomposition it was in the middle of creating was
// simply lost, and the operator asking "did we stop?" is what surfaced it.
//
// So a turn waits the way a run waits, under the same configured bounds and the
// same polling discipline: sleep the probe interval or the time left to the
// quoted reset, whichever is shorter, and ask again. What differs is what can be
// done with a wait the harness will not take. A run parks — its deadline is
// durable, and a later invocation continues it — and a conversation has no such
// record to be continued from, so it fails with what the provider said stated on
// the way out, which is the one thing the operator needs to decide when to say
// it again.
//
// Nothing a reissued turn does is done twice. Tracker actions this message
// already applied were applied by rounds that finished, and the reissue resumes
// the same provider session that holds them: what is asked again is the single
// invocation the provider refused, with the prompt it was refused with.
//
// The refusal is recorded either way. An exhausted limit is not this
// conversation's problem — it is every process's, for as long as it lasts — so
// it goes into the product's own log rather than only onto the screen of whoever
// typed the message.

import (
	"context"
	"fmt"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// UsageLimits is where a provider's refusal is collected. It is satisfied by
// runstate.UsageLimitStore.
type UsageLimits interface {
	Record(exhaustion runstate.UsageLimitExhaustion) error
}

// UsageLimitPause bounds how long a refused turn waits for the provider to serve
// it again. It is the run's own configuration rather than a second set of
// numbers: an operator who has said how long the harness may wait out a limit has
// said it about every provider invocation they pay for, and a conversation that
// answered to a different bound would be a wait they never configured.
//
// Its zero value waits for nothing, which is what a conversation wired without
// it gets: a refused turn fails exactly as it did before waiting existed.
type UsageLimitPause struct {
	// Maximum is execution.usage_limit_max_pause: the longest one message may
	// spend waiting, across every wait its rounds take rather than each of them
	// separately. Bounding each wait on its own would let a provider that keeps
	// refusing walk one message far past it, one acceptable-looking wait at a
	// time.
	Maximum time.Duration
	// InProcess is execution.usage_limit_in_process_pause: how much of that a run
	// will spend asleep in one process. A conversation cannot spend more than it
	// either, and cannot do what a run does when it reaches it — exit with the
	// deadline durable for a later invocation to continue — so reaching it fails
	// the turn rather than parking it.
	InProcess time.Duration
	// Probe is execution.usage_limit_unknown_reset_pause: the interval between
	// attempts, whether or not the provider named a reset time. A quoted reset
	// bounds the wait rather than gating it, because a reset time is a claim
	// about the provider and claims go stale in both directions.
	Probe time.Duration
}

// bound is the longest one message will spend waiting. It is the smaller of the
// two bounds a run waits under, because the larger of them describes something a
// conversation cannot do: a run that reaches its in-process bound leaves the work
// in flight with its deadline recorded, and there is no such record behind a
// turn to leave anything in.
func (p UsageLimitPause) bound() time.Duration {
	if p.InProcess < p.Maximum {
		return p.InProcess
	}
	return p.Maximum
}

// waits reports a configuration that describes a wait at all. Both halves are
// required, and each missing one means the same thing: no budget to spend, or no
// interval to spend it in, is a conversation that fails a refused turn the way it
// always did rather than one that reissues with nothing between the attempts.
func (p UsageLimitPause) waits() bool { return p.bound() > 0 && p.Probe > 0 }

// UsageLimitError reports a turn the provider refused and whose wait the harness
// will not take. It is its own type for the same reason the operator's hold is:
// nothing about the conversation is broken, and what the operator needs is when
// the provider said it lifts rather than a failure to interpret.
type UsageLimitError struct {
	// Kind is the provider's own name for the exhausted limit, and ResetsAt is
	// when it said the limit lifts. ResetsAt is zero where the provider named no
	// reset time at all, which is a different fact from a reset already behind us.
	Kind     string
	ResetsAt time.Time
	// Reason says why this wait was not one the harness would take.
	Reason string
}

func (e *UsageLimitError) Error() string {
	described := runstate.DescribePause(runstate.PauseUsageLimit, e.Kind)
	if !e.ResetsAt.IsZero() {
		described += ", which it reports resetting at " + e.ResetsAt.UTC().Format(time.RFC3339)
	} else {
		described += ", for which it named no reset time"
	}
	return fmt.Sprintf("this turn was refused by %s, and the harness will not wait it out: %s; nothing was lost, and saying this again takes the turn that was refused",
		described, e.Reason)
}

// noteUsageLimit records a provider refusal this turn met, and reports only what
// went wrong recording it. A conversation with nowhere to record one records
// nothing and says nothing: the caller is already deciding what to do about the
// refusal itself, and this adds a durable trace of it rather than another way for
// the turn to fail.
func (s *Session) noteUsageLimit(limit backend.UsageLimit) error {
	if s.options.UsageLimits == nil {
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

// waitOutUsageLimit waits for a provider that refused this turn, and reports
// nothing where the turn is to be asked again. It is the run's rule with the run's
// numbers: an unusable reset time or a wait that no longer fits what this message
// may spend refuses the wait instead of guessing one, and everything else sleeps
// a probe and reissues.
func (s *Session) waitOutUsageLimit(ctx context.Context, limit backend.UsageLimit) error {
	pause := s.options.UsageLimitPause
	refused := func(reason string) error {
		return &UsageLimitError{Kind: limit.Kind, ResetsAt: limit.ResetsAt, Reason: reason}
	}
	now := s.options.clock().Now()
	// A limit with no reset time is unknown rather than unwaitable. The overage
	// allowance reports this way while the ordinary rolling window keeps resetting
	// on its usual schedule, so the harness asks again rather than being told when.
	deadline := limit.ResetsAt
	if deadline.IsZero() {
		deadline = now.Add(pause.Probe)
	}
	remaining := deadline.Sub(now)
	if remaining <= 0 {
		// A limit still refusing work while naming a reset that has already passed
		// is not describing a wait. Honoring it would mean reissuing straight back
		// into the same refusal with nothing bounding the attempts, and a clock skew
		// or a window the provider has not rolled yet is a fact for a person.
		return refused("the reset it names is not in the future, and the harness does not guess a wait it was not given")
	}
	probe := min(remaining, pause.Probe)
	if s.usageLimitWaited+probe > pause.bound() {
		reason := fmt.Sprintf("waiting %s to ask again would take this message past the %s it may spend waiting", probe, pause.bound())
		if s.usageLimitWaited > 0 {
			reason += fmt.Sprintf(", and it has already waited %s", s.usageLimitWaited)
		}
		return refused(reason)
	}
	// What the wait spends is counted before it is spent, so a wait interrupted
	// part way cannot buy this message a fresh budget by forgetting it.
	s.usageLimitWaited += probe
	// Interactive or not, the wait is the same; where somebody is watching, this is
	// what stops a turn that is waiting out hours of provider silence from looking
	// exactly like a turn that has hung.
	s.activity.doing(describeUsageLimitWait(limit, deadline))
	return s.options.sleep(ctx, probe)
}

// describeUsageLimitWait says what the turn is waiting on and until when, in the
// operator's language rather than the provider's. The deadline it names is the
// moment the turn will ask again, which is what somebody watching wants, and is
// the quoted reset only when that is sooner than the next probe.
func describeUsageLimitWait(limit backend.UsageLimit, until time.Time) string {
	described := runstate.DescribePause(runstate.PauseUsageLimit, limit.Kind)
	return fmt.Sprintf("waiting out %s; asking again at %s", described, until.Local().Format(time.Kitchen))
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
