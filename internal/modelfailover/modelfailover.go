package modelfailover

// Serving a role's turn from a permitted alternate while the model it is
// configured for has no capacity.
//
// The shape this exists for was met rather than imagined. On 2026-09-07 at 06:00
// the model the management roles run on closed its capacity window while the
// model the developers and reviewers run on still had one; every item held for a
// development manager decision stopped behind a role that could not take a turn,
// and what unstuck it was a person editing three agents onto the other model. A
// refused turn looks exactly like a role with nothing to say, so nothing in the
// harness was going to notice.
//
// What is here is the substitution and nothing else. Which agents may take it is
// the configuration's — one alternate, named in the agent's own block, off until
// the agent says otherwise — and this package is handed the answer rather than
// deciding it. So an agent that has not enabled failover runs through here
// byte-for-byte as it ran before: one invocation, with the model it named, and
// no record written that would not have been written anyway.
//
// Three things follow from a substitution being a fact about a model rather than
// about a conversation:
//
//   - It is recorded in the product's usage-limit log, where every other refusal
//     outside a run is already recorded, carrying the model that was refused and
//     the alternate that served. A record that named neither could not be read
//     back as a window, and a second log would be a second answer to the question
//     the first one already answers.
//   - The window is read back from that log, so the turn after a substitution
//     goes straight to the alternate instead of paying a refused invocation to
//     rediscover a window the harness already watched close. It is durable
//     because the process that met the refusal is rarely the process that takes
//     the next turn.
//   - Affinity is the named model's. The moment the provider's own reset time
//     passes, the next turn asks the named model again — so a substitution lasts
//     a window rather than becoming a quiet permanent move to another model.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Invoker is the whole of what failover needs of the thing it wraps: something
// that can make the invocation. It is narrower than backend.Backend
// deliberately — the harness puts its provider through the cost meter before an
// invocation reaches here, and a meter is not a backend — and the narrower
// interface is what lets the substitution sit outside the meter, where each
// attempt is priced against the model that attempt actually asked for.
type Invoker interface {
	Run(ctx context.Context, request backend.RunRequest) (backend.RunResult, error)
}

// Windows is the durable account of which models a provider has refused. It is
// satisfied by runstate.UsageLimitStore, which is where every refusal met
// outside a run is already written down.
type Windows interface {
	Record(exhaustion runstate.UsageLimitExhaustion) error
	List() ([]runstate.UsageLimitExhaustion, error)
}

// Policy is what one agent's turn may be served by, and where the substitution
// is written down. Its zero value is failover off, which is what every caller
// that has not been configured for it passes.
type Policy struct {
	// Alternate is the permitted alternate model, from the agent's own
	// configuration. Empty is failover off and is the whole of the switch: this
	// package never reads a configuration and never chooses a model nobody named.
	Alternate string
	// Windows is where a substitution is recorded and where a window still open
	// against the named model is read from. A policy without one still fails over
	// — the turn is what matters — but pays a refused invocation each turn and
	// tells nobody, so the harness always wires it.
	Windows Windows
	// Now is the clock the reset times are compared against. Nil is time.Now.
	Now func() time.Time
	// UnknownResetPause is how long a refusal that named no usable reset time
	// stands before the configured model is asked again. It is the project's
	// `execution.usage_limit_unknown_reset_pause`, which is the same interval a
	// run probes an unknown-reset limit on, so the harness has one polling
	// discipline rather than two that could disagree about the same account.
	//
	// Without it every turn re-asks a model that is still exhausted, substitutes
	// again, and writes another substitution down — so the operator is told once
	// per turn for as long as the outage lasts, which is the channel-muting
	// outcome saying it once per window exists to avoid. Zero restores exactly
	// that, and is why the harness always passes the configured value.
	UnknownResetPause time.Duration
	// ProductID, Waiting, ConversationID and WorkItemID are what the recorded
	// substitution says about itself: whose product it happened on, what would
	// have stopped in words, and what it can be read back to. Waiting is required
	// by the record for the reason it is required of every refusal — an
	// exhaustion nobody can say what it stopped is not worth recording.
	ProductID      domain.ProductID
	Waiting        string
	ConversationID string
	WorkItemID     string
	// RecordFailure is handed whatever went wrong with the log: a substitution
	// that could not be written down, or a set of windows that could not be read.
	// A turn the alternate has already answered is not thrown away because the log
	// would not take the line, which is the same trade the cost meter makes, and a
	// window that could not be read costs one refused invocation rather than the
	// turn.
	//
	// A caller that leaves it nil is handed a failed write joined onto the
	// invocation's own error rather than losing it. A failed read has nowhere to
	// go in that case and is dropped, because the turn it belongs to succeeded;
	// the harness wires this everywhere for exactly that reason.
	RecordFailure func(error)
}

// Enabled reports a policy that may substitute anything.
func (p Policy) Enabled() bool { return strings.TrimSpace(p.Alternate) != "" }

func (p Policy) now() time.Time {
	if p.Now == nil {
		return time.Now()
	}
	return p.Now()
}

// Served is what actually answered, said by the harness rather than inferred
// from the configuration. The record keeps this rather than the configured
// selector, because "which model served this turn" is the question a
// substitution makes worth asking.
type Served struct {
	// Model is the selector this turn was actually asked for under.
	Model string
	// Refused is the named model the alternate stood in for, and empty where the
	// named model served. It is separate from Model because a record that says
	// only what served cannot say that anything was moved.
	Refused string
}

// Substituted reports a turn the alternate served.
func (s Served) Substituted() bool { return strings.TrimSpace(s.Refused) != "" }

// Serve makes one provider invocation, serving it from the permitted alternate
// where the named model will not take it.
//
// The named model is asked unless the log already says its window is open no
// longer, in which case the alternate is asked directly — an invocation refused
// for a window the harness watched close is a round trip that buys nothing. A
// refusal met at the named model is failed over once, and only once: the
// alternate either serves the turn or the caller is handed the refusal it would
// have been handed anyway, because a third model to try after the second is
// routing rather than fallback.
//
// Everything else passes through untouched. A turn that failed for any other
// reason is that failure, with the model that was asked, and nothing about it is
// retried here.
func Serve(ctx context.Context, provider Invoker, request backend.RunRequest, policy Policy) (backend.RunResult, Served, error) {
	named := strings.TrimSpace(request.Model)
	alternate := strings.TrimSpace(policy.Alternate)
	if alternate == "" || alternate == named {
		result, err := provider.Run(ctx, request)
		return result, Served{Model: request.Model}, err
	}

	// A window the provider said has not lifted is taken at its word for as long
	// as it stands, and for no longer: the comparison is against the clock at the
	// moment of the turn, so affinity returns to the named model on the first turn
	// after the reset time passes without anything having to notice it did.
	if closed, err := windowClosed(policy, named); err != nil {
		policy.report(err)
	} else if closed {
		result, err := runWith(ctx, provider, request, alternate)
		return result, Served{Model: alternate, Refused: named}, err
	}

	result, err := provider.Run(ctx, request)
	refused := refusal(result, err)
	if refused == nil {
		return result, Served{Model: request.Model}, err
	}

	// The refused attempt emitted events of its own before it was declined, so the
	// substituted one starts after them rather than numbering over them. Both
	// attempts write to one event log, and a sequence used twice is a log whose
	// numbering no longer says what order anything happened in.
	if result.LastEvent > request.LastSequence {
		request.LastSequence = result.LastEvent
	}
	substituted, substitutedErr := runWith(ctx, provider, request, alternate)
	// The substitution is written down only where it served. A turn the alternate
	// could not take either is the refusal the caller already handles, and
	// recording it here would put the same stoppage in the log twice — once as a
	// substitution that did not happen, and once by whoever fails the turn.
	if refusal(substituted, substitutedErr) != nil {
		return substituted, Served{Model: alternate, Refused: named}, substitutedErr
	}
	if err := record(policy, named, alternate, *refused); err != nil {
		if policy.RecordFailure == nil {
			return substituted, Served{Model: alternate, Refused: named}, errors.Join(substitutedErr, err)
		}
		policy.RecordFailure(err)
	}
	return substituted, Served{Model: alternate, Refused: named}, substitutedErr
}

// runWith makes the same invocation under another model. Everything else about
// the request is the one the caller built: the same prompt, the same session,
// the same account, so what changes is the model and nothing else.
func runWith(ctx context.Context, provider Invoker, request backend.RunRequest, model string) (backend.RunResult, error) {
	request.Model = model
	return provider.Run(ctx, request)
}

// windowClosed reports the named model still inside a window the last refusal
// of it describes: the provider's own reset time where it named a usable one,
// and the configured probe interval where it did not. A limit reported without a
// reset is a wait of unknown length rather than of no length, so the harness
// waits its interval and asks again — which is what makes a substitution said
// once per window rather than once per turn.
//
// The log is the product's rather than one account's, so under a pool a window
// met on one account is read as closed for an agent held on another. That errs
// the harmless way round — the agent is served by its permitted alternate for a
// window it might not have needed to skip — and it is never a stoppage, which is
// the failure this exists to prevent. Keying windows by account would need the
// account on the refusal record, and that is worth doing when a pooled project
// actually runs its management roles on separate subscriptions.
func windowClosed(policy Policy, model string) (bool, error) {
	if policy.Windows == nil || strings.TrimSpace(model) == "" {
		return false, nil
	}
	exhaustions, err := policy.Windows.List()
	if err != nil {
		return false, fmt.Errorf("read which models the provider has refused: %w", err)
	}
	at := policy.now()
	for _, exhaustion := range exhaustions {
		if strings.TrimSpace(exhaustion.Model) != strings.TrimSpace(model) {
			continue
		}
		if exhaustion.WindowClosed(at, policy.UnknownResetPause) {
			return true, nil
		}
	}
	return false, nil
}

// record writes the substitution down where every refusal met outside a run is
// written down.
func record(policy Policy, named, alternate string, refused backend.UsageLimit) error {
	if policy.Windows == nil {
		return nil
	}
	exhaustion := runstate.UsageLimitExhaustion{
		SchemaVersion:  runstate.UsageLimitSchemaVersion,
		ProductID:      policy.ProductID,
		At:             policy.now(),
		Waiting:        policy.Waiting,
		Kind:           refused.Kind,
		ConversationID: policy.ConversationID,
		WorkItemID:     policy.WorkItemID,
		Model:          named,
		ServedBy:       alternate,
	}
	if !refused.ResetsAt.IsZero() {
		resetsAt := refused.ResetsAt.UTC()
		exhaustion.ResetsAt = &resetsAt
	}
	if err := policy.Windows.Record(exhaustion); err != nil {
		return fmt.Errorf("record the turn %s served while %s had no capacity: %w", alternate, named, err)
	}
	return nil
}

func (p Policy) report(err error) {
	if err == nil {
		return
	}
	if p.RecordFailure != nil {
		p.RecordFailure(err)
	}
}

// refusal reports an invocation the provider declined for want of capacity
// rather than one it answered. A limit reported alongside an answer the provider
// still gave is not a refusal: nothing was stopped, so there is nothing to serve
// from anywhere else.
func refusal(result backend.RunResult, err error) *backend.UsageLimit {
	if result.UsageLimit == nil || (err == nil && !result.IsError) {
		return nil
	}
	return result.UsageLimit
}
