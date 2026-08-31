package orchestrator

// The management loop's own pass: read what the roles have asked each other,
// take the lease on each request nobody is carrying, and act on the plan.
//
// This is the harness's hand, and it is the only thing here that invokes
// anything. A role never reaches this: it emits a request, the harness persists
// it, and the harness — under its own lease, its own gates, and its own budgets
// — puts it in front of the target role and writes the answer back. That is the
// harness-is-the-only-role-invoker invariant, and having exactly one place that
// invokes is what makes it checkable.
//
// A pass and a restart are the same pass. Nothing here asks whether the process
// before it died: it reads the records, finds out from the leases what is
// actually being carried right now, and takes each request to the only place it
// can go. A request whose carrier is gone is delivered again; one whose answer
// is already on the record is settled without being asked twice, which is the
// window a crash lands in and the one thing that must not cost twice.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/supervision"
)

// SupervisionStore is the durable home of the management loop's records as this
// pass reaches it, and the leases that say who is carrying what. It is
// satisfied by runstate.SupervisionStore.
type SupervisionStore interface {
	Requests() ([]supervision.Request, error)
	SaveRequest(recorded supervision.Request) error
	Readiness() ([]supervision.Readiness, error)
	// HoldRequest takes the exclusive lease on one request without waiting,
	// reporting whether it got it. A lease it could not take belongs to a live
	// process, and one the operating system dropped when its holder died is one
	// this pass takes and carries on with — which is the whole of how a restart
	// tells an interrupted delivery from one that is running.
	HoldRequest(requestID string) (*runstate.Lease, bool, error)
}

// SupervisionVoice is one invocation of the role a request names. It is an
// interface so that what drives the loop does not depend on which provider
// answers, and so a test can take a whole pass without one.
type SupervisionVoice interface {
	Answer(ctx context.Context, delivery supervision.Delivery, request supervision.Request) (SupervisionSpoken, error)
}

// SupervisionSpoken is what the target role said and what served the invocation
// that produced it. All of it is written to the request's own record: the
// durable-state-is-provider-independent invariant asks that every provider
// invocation be attributable after the session that ran it is gone, and this
// record is the only copy of this one.
type SupervisionSpoken struct {
	Answer         string
	Backend        domain.Backend
	Model          string
	ResolvedModel  string
	AccountAlias   string
	ConfigRevision string
	Build          string
	// SessionID accelerates resuming the same provider session and is never read
	// back as state.
	SessionID string
	CostUSD   float64
}

// SupervisionReports is the collected pile a request escalates into when it
// runs out of attempts. It is the same pile every role's reports land in,
// because an operator reading what the harness noticed should not have to know
// this one came from two agents failing to reach each other.
type SupervisionReports interface {
	Append(reported report.Report) error
}

// SupervisionLoop takes one pass of the management loop.
type SupervisionLoop struct {
	Store SupervisionStore
	// Voice invokes the role a request names. A loop wired without one reclaims
	// and settles and delivers nothing, which is what a reconciliation pass is:
	// recovering from a lost process is a question about recorded evidence, and
	// never a reason to start an invocation nobody asked for.
	Voice SupervisionVoice
	// Reports is where a request that ran out of attempts reaches the operator.
	// It is optional, and a pass wired without one still settles such a request
	// rather than retrying it for ever — the ending is on the record either way,
	// and what is lost is only that somebody is told.
	Reports SupervisionReports
	// Holder names this process on every attempt it opens, so a later pass
	// reading an attempt nobody holds can say who was carrying it.
	Holder string
	// Revisions is what everything the records name is at now. A pass given none
	// was not asked about staleness and judges none.
	Revisions map[string]string
	Bounds    supervision.Bounds
	// ProductID and RepositoryID attribute an escalation to the product it came
	// from, exactly as a report filed inside a run is.
	ProductID    domain.ProductID
	RepositoryID string
	Clock        execution.Clock
}

// SupervisionOutcome is what one request's turn in a pass came to.
type SupervisionOutcome string

const (
	// SupervisionAnswered is a delivery the target role answered.
	SupervisionAnswered SupervisionOutcome = "answered"
	// SupervisionUnanswered is a delivery the provider failed. The attempt is
	// spent and says why; a later pass retries it or reaches the limit.
	SupervisionUnanswered SupervisionOutcome = "unanswered"
	// SupervisionSettled is an ending written for a request that was already
	// over — the answer recorded by a process that died before it could say so,
	// or the attempts spent.
	SupervisionSettled SupervisionOutcome = "settled"
	// SupervisionUndelivered is a delivery this pass could make and did not,
	// because no voice is wired to it.
	SupervisionUndelivered SupervisionOutcome = "undelivered"
)

// SupervisionResult is one request and what became of it.
type SupervisionResult struct {
	RequestID string
	Outcome   SupervisionOutcome
	// Reclaimed marks a delivery that followed an attempt whose holder was gone.
	Reclaimed bool
	Detail    string
}

// SupervisionPass is what one pass read and what it did.
type SupervisionPass struct {
	// Plan is the reading this pass acted on, kept whole so what was queued,
	// what is degraded, and which roles a stale judgment wants woken are all
	// readable beside what was actually done.
	Plan    supervision.Plan
	Results []SupervisionResult
}

// Run takes one pass. It never invokes anything it does not hold the lease for,
// and it releases every lease it took before it returns.
//
// One request it cannot write does not abandon the rest: a pass that stopped at
// the first bad record would be a loop one unreadable request disables. What
// went wrong is joined into the returned error, and the pass still describes
// everything that did happen.
func (l SupervisionLoop) Run(ctx context.Context) (SupervisionPass, error) {
	if l.Store == nil {
		return SupervisionPass{}, errors.New("a supervision pass needs the store the requests live in")
	}
	if strings.TrimSpace(l.Holder) == "" {
		return SupervisionPass{}, errors.New("a supervision pass names the process taking it, so a later one can say who was carrying a request")
	}
	// An escalation is attributed to the product it came from, exactly as every
	// other collected report is. A pass wired to escalate into a pile it cannot
	// attribute would settle the request and then fail to tell anybody, which is
	// the one failure this loop exists to prevent.
	if l.Reports != nil && (l.ProductID == "" || strings.TrimSpace(l.RepositoryID) == "") {
		return SupervisionPass{}, errors.New("a supervision pass that escalates names the product and repository it escalates for")
	}
	requests, err := l.Store.Requests()
	if err != nil {
		return SupervisionPass{}, err
	}
	judgments, err := l.Store.Readiness()
	if err != nil {
		return SupervisionPass{}, err
	}

	// The leases are what say who is carrying what. Taking one is how this pass
	// finds out that nobody else has it, and it is the same lease the delivery
	// then runs under — asking first and taking afterwards would leave a window
	// for a second process to take it in between.
	held := make(map[string]bool)
	mine := make(map[string]*runstate.Lease)
	defer func() {
		for _, lease := range mine {
			lease.Release()
		}
	}()
	var problems []error
	for _, request := range requests {
		if !request.Open() {
			continue
		}
		lease, taken, err := l.Store.HoldRequest(request.ID)
		if err != nil {
			// A lease that cannot be reasoned about is one this pass leaves alone.
			// Treating it as free would be the double delivery the lease prevents.
			problems = append(problems, err)
			held[request.ID] = true
			continue
		}
		if !taken {
			held[request.ID] = true
			continue
		}
		mine[request.ID] = lease
	}

	pass := SupervisionPass{Plan: supervision.Survey(supervision.State{
		Requests:  requests,
		Readiness: judgments,
		Revisions: l.Revisions,
		Held:      held,
		Bounds:    l.Bounds,
	})}

	recorded := make(map[string]supervision.Request, len(requests))
	for _, request := range requests {
		recorded[request.ID] = request
	}

	for _, settlement := range pass.Plan.Settle {
		if mine[settlement.RequestID] == nil {
			continue
		}
		result, err := l.settle(recorded[settlement.RequestID], settlement)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		pass.Results = append(pass.Results, result)
	}
	for _, delivery := range pass.Plan.Deliver {
		if mine[delivery.RequestID] == nil {
			continue
		}
		if l.Voice == nil {
			pass.Results = append(pass.Results, SupervisionResult{
				RequestID: delivery.RequestID,
				Outcome:   SupervisionUndelivered,
				Reclaimed: delivery.Reclaimed,
				Detail:    "no voice is wired to this pass, so nothing was put in front of the " + string(delivery.To),
			})
			continue
		}
		result, err := l.deliver(ctx, recorded[delivery.RequestID], delivery)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		pass.Results = append(pass.Results, result)
	}
	return pass, errors.Join(problems...)
}

// settle writes the ending a request has already reached, and tells the
// operator about the one ending nobody wants.
func (l SupervisionLoop) settle(request supervision.Request, settlement supervision.Settlement) (SupervisionResult, error) {
	at := l.now()
	request.Outcome = settlement.Outcome
	request.SettledAt = &at
	request.UpdatedAt = at
	// An ending is written over an open attempt where the process carrying it
	// died: the attempt was spent and nothing finished it, and leaving it open
	// would make a settled request look like one still in flight.
	if last := len(request.Attempts) - 1; last >= 0 && request.Attempts[last].Open() {
		request.Attempts[last].FinishedAt = &at
		if strings.TrimSpace(request.Attempts[last].Problem) == "" && request.Response == nil {
			request.Attempts[last].Problem = "the process carrying it stopped before it produced anything"
		}
	}
	if err := l.Store.SaveRequest(request); err != nil {
		return SupervisionResult{}, fmt.Errorf("settle request %s: %w", request.ID, err)
	}
	result := SupervisionResult{
		RequestID: request.ID,
		Outcome:   SupervisionSettled,
		Detail:    settlement.Because,
	}
	if settlement.Escalate {
		if err := l.escalate(request, settlement, at); err != nil {
			return result, fmt.Errorf("escalate request %s: %w", request.ID, err)
		}
	}
	return result, nil
}

// deliver puts one request in front of the role it names.
//
// The attempt is written before the invocation and the answer is written the
// moment it comes back, which is what makes a crash between them survivable in
// the only direction that is safe: a process that dies mid-invocation leaves an
// attempt that was spent rather than one nothing counted, and one that dies
// after the answer leaves an answer the next pass settles rather than pays for
// again.
func (l SupervisionLoop) deliver(ctx context.Context, request supervision.Request, delivery supervision.Delivery) (SupervisionResult, error) {
	started := l.now()
	// A reclaimed delivery follows an attempt whose holder is gone, and that
	// attempt is still open on the record. Closing it here is what it is owed:
	// it was spent, nothing finished it, and only the pass that reclaims it
	// knows why. Leaving it open would also make this record one the contract
	// refuses, since only the last attempt may be the one still running.
	if last := len(request.Attempts) - 1; delivery.Reclaimed && last >= 0 && request.Attempts[last].Open() {
		request.Attempts[last].FinishedAt = &started
		if strings.TrimSpace(request.Attempts[last].Problem) == "" {
			request.Attempts[last].Problem = fmt.Sprintf("%s was carrying it and is gone; nothing came back",
				request.Attempts[last].Holder)
		}
	}
	request.Attempts = append(request.Attempts, supervision.Attempt{
		Number:    delivery.Attempt,
		Holder:    l.Holder,
		StartedAt: started,
	})
	request.UpdatedAt = started
	if err := l.Store.SaveRequest(request); err != nil {
		return SupervisionResult{}, fmt.Errorf("open attempt %d of request %s: %w", delivery.Attempt, request.ID, err)
	}

	spoken, err := l.Voice.Answer(ctx, delivery, request)
	finished := l.now()
	last := len(request.Attempts) - 1
	request.Attempts[last].FinishedAt = &finished
	request.UpdatedAt = finished
	if err != nil {
		request.Attempts[last].Problem = singleLineProblem(err.Error())
		if saveErr := l.Store.SaveRequest(request); saveErr != nil {
			return SupervisionResult{}, fmt.Errorf("record the failed attempt on request %s: %w", request.ID, saveErr)
		}
		return SupervisionResult{
			RequestID: request.ID,
			Outcome:   SupervisionUnanswered,
			Reclaimed: delivery.Reclaimed,
			Detail:    singleLineProblem(err.Error()),
		}, nil
	}

	request.Response = &supervision.Response{
		Text:           spoken.Answer,
		At:             finished,
		Attempt:        delivery.Attempt,
		Backend:        spoken.Backend,
		Model:          spoken.Model,
		ResolvedModel:  spoken.ResolvedModel,
		AccountAlias:   spoken.AccountAlias,
		ConfigRevision: spoken.ConfigRevision,
		Build:          spoken.Build,
		SessionID:      spoken.SessionID,
		CostUSD:        spoken.CostUSD,
	}
	// The answer and the ending are written together where nothing interrupts
	// them. Where something does, the answer alone is what the next pass finds,
	// and settling it is exactly what that pass does with it.
	request.Outcome = supervision.OutcomeAnswered
	request.SettledAt = &finished
	if err := l.Store.SaveRequest(request); err != nil {
		return SupervisionResult{}, fmt.Errorf("record the answer to request %s: %w", request.ID, err)
	}
	return SupervisionResult{
		RequestID: request.ID,
		Outcome:   SupervisionAnswered,
		Reclaimed: delivery.Reclaimed,
		Detail:    fmt.Sprintf("the %s answered on attempt %d", request.To, delivery.Attempt),
	}, nil
}

// escalate is how the one ending nobody wants reaches a person. A request that
// ran out of attempts is rare and expensive, and a loop that swallowed it would
// look exactly like one that was working.
func (l SupervisionLoop) escalate(request supervision.Request, settlement supervision.Settlement, at time.Time) error {
	if l.Reports == nil {
		return nil
	}
	message := fmt.Sprintf("%s ended unresolved: %s. The %s asked the %s %q on topic %s, and nothing answered it. Decide it, or say what neither of them could.",
		request.ID, settlement.Because, request.From.Title(), request.To.Title(),
		singleLineProblem(request.Subject), request.Topic)
	collected, err := report.Collect(
		[]report.Entry{{Severity: report.SeverityWarning, Message: message}},
		report.Attribution{
			Role: request.From,
			// The request is the record this leads back to, exactly as a run is for
			// a report filed inside one.
			RunID:        request.ID,
			ProductID:    l.ProductID,
			RepositoryID: l.RepositoryID,
		}, at)
	if err != nil {
		return err
	}
	for _, reported := range collected {
		if err := l.Reports.Append(reported); err != nil {
			return err
		}
	}
	return nil
}

func (l SupervisionLoop) now() time.Time {
	if l.Clock != nil {
		return l.Clock.Now().UTC()
	}
	return time.Now().UTC()
}

// singleLineProblem keeps what went wrong to one line and inside what a record
// may hold, so a provider that failed with a page of output still leaves an
// attempt that says something rather than one that will not save.
func singleLineProblem(text string) string {
	flattened := strings.Join(strings.Fields(text), " ")
	if len(flattened) > supervision.MaxProblemBytes {
		return flattened[:supervision.MaxProblemBytes]
	}
	return flattened
}
