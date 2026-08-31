// Package spend turns one provider invocation into one line in the cost log.
//
// It is a wrapper around a provider rather than a function beside one on
// purpose. What the harness has to guarantee is that every priced invocation
// lands in the log exactly once at the moment its cost is known, and a recording
// step a caller makes after invoking is a step a caller can forget, take twice,
// or skip on the path where the invocation failed -- which is the path where the
// money was spent and nothing came back. Invoking through this makes the
// invocation and the line the same statement.
//
// The classification is decided here for the same reason. A provider that ends
// an invocation without saying what it cost has not said the invocation was
// free, and every caller working that out for itself is every caller getting a
// chance to record a zero.
package spend

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/buildinfo"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// processBuild is the revision this binary was built from, read once because a
// process does not change binary while it lives — and that is exactly the
// problem it answers: a long-lived one goes on making invocations from what it
// was started with while the harness moves on underneath it.
//
// It is taken here rather than passed in with the rest of the attribution for
// the reason the recording itself is not left to callers: a build a call site
// could supply is one a call site could forget, and the line would then say
// which account paid for an invocation without saying which harness made it. A
// binary that carries no revision leaves it empty, which reads as a comparison
// nobody can make.
var processBuild = buildinfo.Commit()

// Provider is the invocation half of a provider backend. It is the narrow view
// every role already takes of one, so a metered provider drops in wherever an
// unmetered one was wired.
type Provider interface {
	Run(ctx context.Context, request backend.RunRequest) (backend.RunResult, error)
}

// Log is where a line is appended. It is satisfied by runstate.SpendStore.
type Log interface {
	Append(line runstate.Spend) error
}

// Attribution is what the harness knows about one invocation and the provider
// does not: which work it served, under whose account, and under which
// configuration. Everything else on a line -- the role, the requested model,
// what it cost -- is on the request or the result and is never asserted here.
type Attribution struct {
	ProductID domain.ProductID
	// Agent is the configured agent filling the role, which is the persona the
	// spend is attributable to where a project configures more than one agent for
	// a role.
	Agent          string
	Phase          runstate.SpendPhase
	AccountAlias   string
	ConfigRevision string
	// Backend is the provider being invoked, taken from configuration so that an
	// invocation which died before returning anything still names what it died
	// on.
	Backend domain.Backend
	// Exactly one of these names what the invocation belongs to, and a run also
	// names the work item it served. The store refuses a line that names none or
	// more than one. A branch review takes the last of them rather than the run
	// identifier: it is not a run, and a line saying it was would be a run id
	// naming no run to whatever later reads these lines back.
	RunID          string
	WorkItemID     string
	ConversationID string
	ExchangeID     string
	BranchReviewID string
}

// Metered is one provider with the cost log wired behind it. Every invocation it
// serves appends exactly one line, whichever way the invocation went: an
// invocation the provider refused, killed, or answered badly spent money exactly
// as one that succeeded did.
type Metered struct {
	Provider    Provider
	Log         Log
	Attribution Attribution
	Clock       execution.Clock
	// RecordFailure, where a caller sets it, is handed a line that could not be
	// made durable instead of the invocation failing with it.
	//
	// It exists for the one caller whose answer is not reproducible from its own
	// record. A run's answer is a change in a worktree the next attempt starts
	// from, so failing the invocation costs an attempt and loses nothing; a
	// conversation turn's answer is prose the provider has already written and
	// already charged for, and failing it throws that away to report that the
	// bookkeeping behind it did not land. The operator would lose the answer as
	// well as the record, which is a worse trade than the one this makes.
	//
	// A caller that leaves it nil takes the failure, which is what everything but
	// the conversation does.
	RecordFailure func(error)
}

// Run makes the invocation and records what it spent.
//
// A line that cannot be made durable is reported, joined to whatever the
// invocation itself reported. That is the same weight the harness already gives
// a run's event log -- an invocation whose events could not be recorded fails
// rather than carrying on unrecorded -- and it is the weight the cost log needs
// for the same reason: a spend nothing wrote down is money the operator is never
// shown, and the silence looks exactly like not having spent it.
//
// The cost of that choice is stated rather than hidden: an invocation the
// provider already served and already charged for comes back as a failure when
// the bookkeeping behind it fails. Two things bound it. The store refuses only a
// line nobody could attribute, and every call site's attribution comes from
// configuration the loader has already validated or from a run's own validated
// state -- each of those sites has a test that the line it produces satisfies
// the durable contract, so the refusal is not a path a working harness takes.
// What is left is a state root that cannot be written, and that is a root whose
// run state and event log are failing in the same breath; the run is lost either
// way, and the alternative is losing the money silently as well.
//
// A caller for which that trade comes out the other way sets RecordFailure and
// is handed the failure instead of it being joined on.
func (m Metered) Run(ctx context.Context, request backend.RunRequest) (backend.RunResult, error) {
	result, err := m.Provider.Run(ctx, request)
	// A provider with nowhere to record what it spends is one nothing is
	// metering. The harness always wires the log; this is what keeps a test
	// harness that does not care about money from having to.
	if m.Log == nil {
		return result, err
	}
	if recordErr := m.Log.Append(m.line(request, result, err)); recordErr != nil {
		recordErr = fmt.Errorf("record what the %s invocation spent: %w", request.Role, recordErr)
		// Either way the failure is reported and never swallowed. What the caller
		// chooses is whether it costs the invocation as well as the record.
		if m.RecordFailure != nil {
			m.RecordFailure(recordErr)
			return result, err
		}
		return result, errors.Join(err, recordErr)
	}
	return result, err
}

// line is what the invocation spent, said once.
func (m Metered) line(request backend.RunRequest, result backend.RunResult, err error) runstate.Spend {
	line := runstate.Spend{
		SchemaVersion:  runstate.SpendSchemaVersion,
		ProductID:      m.Attribution.ProductID,
		At:             m.clock().Now().UTC(),
		Role:           request.Role,
		Agent:          m.Attribution.Agent,
		Phase:          m.Attribution.Phase,
		AccountAlias:   m.Attribution.AccountAlias,
		ConfigRevision: m.Attribution.ConfigRevision,
		RunID:          m.Attribution.RunID,
		WorkItemID:     m.Attribution.WorkItemID,
		ConversationID: m.Attribution.ConversationID,
		ExchangeID:     m.Attribution.ExchangeID,
		BranchReviewID: m.Attribution.BranchReviewID,
		Backend:        m.Attribution.Backend,
		Model:          request.Model,
		ResolvedModel:  result.ResolvedModel,
		SessionID:      result.SessionID,
		Build:          processBuild,
	}
	// The provider names the backend that served the invocation, which is the
	// same one the configuration named. It is preferred where it is there because
	// it is what actually ran, and the configured one is what a result that never
	// arrived is left with.
	if result.Backend != "" {
		line.Backend = result.Backend
	}
	if result.CostReported {
		line.Classification = runstate.SpendKnown
		line.AmountUSD = result.CostUSD
		return line
	}
	line.Classification = runstate.SpendUnknown
	line.Unknown = unknownReason(err)
	return line
}

// unknownReason says why nobody knows what an invocation cost, in as much of the
// provider's own account of it as the line will hold. An invocation that failed
// before the provider reported anything and one that reported everything except
// the cost are different accidents, and which of them happened is what somebody
// reconciling a bill has to know.
func unknownReason(err error) string {
	if err == nil {
		return "the provider ended the invocation without reporting what it cost"
	}
	reason := strings.Join(strings.Fields("the invocation failed before the provider reported what it cost: "+err.Error()), " ")
	if len(reason) <= runstate.MaxSpendUnknownBytes {
		return reason
	}
	// The bound cuts the tail of a message, and never a rune in half: a line the
	// store would refuse for being unreadable is a spend lost to a long error.
	cut := runstate.MaxSpendUnknownBytes
	for cut > 0 && !utf8.RuneStart(reason[cut]) {
		cut--
	}
	return strings.TrimSpace(reason[:cut])
}

func (m Metered) clock() execution.Clock {
	if m.Clock == nil {
		return execution.RealClock{}
	}
	return m.Clock
}
