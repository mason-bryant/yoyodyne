package orchestrator

// The management loop's own pass: read what the roles have asked each other,
// take the lease on each exchange nobody is carrying, and act on the plan.
//
// This is the harness's hand, and it is the only thing here that touches an
// exchange it does not hold. A role never reaches this: it emits an ask, the
// harness records it, and the harness — under its own lease, its own gates, and
// its own budgets — carries it. That is the harness-is-the-only-role-invoker
// invariant, and having exactly one place that acts is what makes it checkable.
//
// A pass and a restart are the same pass. Nothing here asks whether the process
// before it died: it reads the records, finds out from the leases what is
// actually being carried right now, and takes each exchange to the only place it
// can go. A round whose carrier is gone is reclaimed and stays spent; a thread
// that has spent every round it was given is closed and the operator is told.
//
// It carries no voice, and that is deliberate rather than unfinished. Recovering
// from a lost process is a question about recorded evidence, and never a reason
// to put a question in front of a role that nobody asked for. So this pass
// reclaims, exhausts, and delivers nothing; what it says about the deliveries it
// could have made is the plan, which the wakeup half of the loop is what acts
// on.

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/exchange"
)

// SupervisionStore is the durable home of the exchanges as this pass reaches
// them, and the leases that say who is carrying what. It is satisfied by
// runstate.ExchangeStore.
type SupervisionStore interface {
	List() ([]exchange.Exchange, error)
	// Load reads one exchange as it now stands. It is what this pass reads an
	// exchange back with once it holds the lease, so nothing is decided from the
	// listing's copy — see Run.
	Load(id string) (exchange.Exchange, error)
	// Hold takes the exclusive lease on one exchange without waiting, reporting
	// whether it got it. A lease it could not take belongs to a live process, and
	// one the operating system dropped when its holder died is one this pass takes
	// and carries on with — which is the whole of how a restart tells an
	// interrupted round from one that is running.
	Hold(id string) (exchange.Release, bool, error)
}

// SupervisionConductor is what writes an ending onto an exchange. It is
// satisfied by exchange.Conductor, and it is an interface so that what drives
// the loop does not depend on how an escalation reaches the operator.
type SupervisionConductor interface {
	Reclaim(recorded exchange.Exchange, because string) (exchange.Exchange, error)
	Exhaust(recorded exchange.Exchange) (exchange.Exchange, error)
}

// SupervisionOutcome is what one exchange's turn in a pass came to.
type SupervisionOutcome string

const (
	// SupervisionReclaimed is a round that was asked, never came back, and whose
	// carrier is gone. The round is spent and now says why it produced nothing.
	SupervisionReclaimed SupervisionOutcome = "reclaimed"
	// SupervisionSettled is a thread that has spent every round it was opened
	// with, closed as unresolved with the operator told.
	SupervisionSettled SupervisionOutcome = "settled"
	// SupervisionCarried is an exchange a live process holds, which this pass left
	// alone.
	SupervisionCarried SupervisionOutcome = "carried"
	// SupervisionQueued is an exchange the in-flight bound is holding back.
	SupervisionQueued SupervisionOutcome = "queued"
	// SupervisionStale is an open exchange asked against something that has since
	// moved. It is said and nothing else: staleness stops no exchange.
	SupervisionStale SupervisionOutcome = "stale"
	// SupervisionUndelivered is a round this pass could have taken and did not,
	// because it carries no voice.
	SupervisionUndelivered SupervisionOutcome = "undelivered"
)

// SupervisionOutcomes are every outcome a pass can come back with. It is here so
// a surface deciding what to do with one can be held to covering all of them
// rather than to whichever were in the list the day it was written.
func SupervisionOutcomes() []SupervisionOutcome {
	return []SupervisionOutcome{
		SupervisionReclaimed,
		SupervisionSettled,
		SupervisionCarried,
		SupervisionQueued,
		SupervisionStale,
		SupervisionUndelivered,
	}
}

// SupervisionResult is one exchange and what became of it.
type SupervisionResult struct {
	ExchangeID string             `json:"exchange_id"`
	Outcome    SupervisionOutcome `json:"outcome"`
	Detail     string             `json:"detail,omitempty"`
}

// SupervisionPass is what one pass read and what it did.
type SupervisionPass struct {
	// Plan is the reading this pass acted on, kept whole so what was queued, what
	// is carried, and which roles a stale question wants told are all readable
	// beside what was actually done.
	Plan    exchange.Plan
	Results []SupervisionResult
}

// SupervisionLoop takes one pass of the management loop.
type SupervisionLoop struct {
	Store     SupervisionStore
	Conductor SupervisionConductor
	Bounds    exchange.Bounds
	// Revisions is what everything the records name is at now, by reference key.
	// A pass given none was not asked about staleness and judges none.
	Revisions map[string]string
}

// Run takes one pass. It never acts on an exchange it does not hold the lease
// for, it decides from the record as it stands under that lease rather than from
// the listing that found it, it gives back every lease the plan leaves it nothing
// to do with as soon as the plan is made, and it releases the rest before it
// returns.
//
// The re-read is the whole of what makes the lease worth taking. Listing and
// holding are two moments, and a live process can finish its round and release
// between them: the lease is then free, this pass takes it, and the listing's
// copy still shows a round nobody answered. Acting on that copy would reclaim a
// round that had just come back — writing "the process carrying it is gone" over
// the answer and the cost the provider was paid for, which is exactly the
// double-write the lease exists to prevent. So the record is read again once the
// lease is held, and everything from the survey onwards is decided from that
// copy. Conductor.Put loads after it holds for the same reason.
//
// One exchange it cannot write does not abandon the rest: a pass that stopped at
// the first bad record would be a loop one unreadable exchange disables. What
// went wrong is joined into the returned error, and the pass still describes
// everything that did happen.
func (l SupervisionLoop) Run() (SupervisionPass, error) {
	if l.Store == nil {
		return SupervisionPass{}, errors.New("a supervision pass needs the store the exchanges live in")
	}
	exchanges, err := l.Store.List()
	if err != nil {
		return SupervisionPass{}, err
	}

	// The leases are what say who is carrying what. Taking one is how this pass
	// finds out that nobody else has it, and it is the same lease the acting then
	// runs under — asking first and taking afterwards would leave a window for a
	// second process to take it in between.
	carried := make(map[string]bool)
	// A lease that could not be reasoned about is treated as held, so the pass
	// leaves that exchange alone. It is kept apart from the ones somebody really
	// is carrying because the two are different facts, and reporting the first as
	// the second would say a live process is working on something when what
	// actually happened is that nothing could find out.
	unreadable := make(map[string]string)
	mine := make(map[string]exchange.Release)
	defer func() {
		for _, lease := range mine {
			lease.Release()
		}
	}()
	var problems []error
	// held is what this pass reasons from: the exchange as it stands under the
	// lease for every one this pass took, and the listing's own copy for the rest —
	// which are the closed ones and the ones a live process is carrying, neither of
	// which anything here acts on.
	held := make([]exchange.Exchange, 0, len(exchanges))
	for _, recorded := range exchanges {
		if !recorded.Open() {
			held = append(held, recorded)
			continue
		}
		lease, taken, err := l.Store.Hold(recorded.ID)
		if err != nil {
			// A lease that cannot be reasoned about is one this pass leaves alone.
			// Treating it as free would be the double delivery the lease prevents.
			problems = append(problems, err)
			carried[recorded.ID] = true
			unreadable[recorded.ID] = err.Error()
			held = append(held, recorded)
			continue
		}
		if !taken {
			carried[recorded.ID] = true
			held = append(held, recorded)
			continue
		}
		// The lease is held, so what the record says now is what it will still say
		// when this pass acts on it. Read before the hold it is a guess about a file
		// somebody else was writing.
		fresh, err := l.Store.Load(recorded.ID)
		if err != nil {
			// An exchange whose record cannot be read now is left exactly as an
			// unreadable lease leaves one: the lease goes back, and nothing is decided
			// from the stale copy that would otherwise stand in for it.
			lease.Release()
			problems = append(problems, err)
			carried[recorded.ID] = true
			unreadable[recorded.ID] = err.Error()
			held = append(held, recorded)
			continue
		}
		mine[recorded.ID] = lease
		held = append(held, fresh)
	}

	pass := SupervisionPass{Plan: exchange.Survey(exchange.State{
		Exchanges: held,
		Carried:   carried,
		Revisions: l.Revisions,
		Bounds:    l.Bounds,
	})}

	// Every lease this pass has nothing to do with goes back now rather than when
	// the pass returns.
	//
	// Finding out who is carrying what means taking each lease, because asking and
	// then taking would leave a window between the two. But holding them all until
	// the end would make this sweep collide with every live conversation for as
	// long as the pass ran — and a conductor meeting a held exchange refuses the
	// ask rather than waiting, so an operator would see their question fail
	// because a reconcile happened to be walking past. Nothing is queued behind
	// that: the refusal is the whole cost.
	//
	// The window is narrowed here rather than closed, and it is worth being plain
	// about what is left. From the moment the discovery loop takes an exchange's
	// lease until this release, this pass holds every open exchange it could take —
	// so an ask put to one of them in that span is still refused, even though the
	// plan turns out to leave it alone. What bounds it is that the span is the rest
	// of the discovery loop plus one Survey, and both are local reads with no
	// provider and no network in them; it cannot be closed altogether without
	// deciding each exchange before the others have been looked at, and the
	// in-flight bound is a fact about all of them at once. Past this point what
	// stays held is only the exchanges being reclaimed or ended, which are threads
	// whose process is gone by definition, so no live conversation is on one.
	acting := make(map[string]bool, len(pass.Plan.Reclaim)+len(pass.Plan.Exhaust))
	for _, reclamation := range pass.Plan.Reclaim {
		acting[reclamation.ExchangeID] = true
	}
	for _, exhaustion := range pass.Plan.Exhaust {
		acting[exhaustion.ExchangeID] = true
	}
	for id, lease := range mine {
		if acting[id] {
			continue
		}
		lease.Release()
		delete(mine, id)
	}

	// What is acted on is the copy read under the lease, never the listing's.
	recorded := make(map[string]exchange.Exchange, len(held))
	for _, one := range held {
		recorded[one.ID] = one
	}

	for _, id := range pass.Plan.Carried {
		detail := "a live process is holding this one, so nothing here touched it"
		if why, unknown := unreadable[id]; unknown {
			detail = "who is carrying this one could not be found out, so nothing here touched it: " + why
		}
		pass.Results = append(pass.Results, SupervisionResult{
			ExchangeID: id,
			Outcome:    SupervisionCarried,
			Detail:     detail,
		})
	}
	// Reclaiming comes before exhausting, and on the same record: a thread whose
	// last round died on its cap is both, and settling it while the round still
	// reads as unanswered would close a record that contradicts itself.
	unreclaimed := make(map[string]bool)
	for _, reclamation := range pass.Plan.Reclaim {
		if mine[reclamation.ExchangeID] == nil {
			continue
		}
		if l.Conductor == nil {
			problems = append(problems, fmt.Errorf("reclaim %s: no conductor is wired to this pass", reclamation.ExchangeID))
			unreclaimed[reclamation.ExchangeID] = true
			continue
		}
		after, err := l.Conductor.Reclaim(recorded[reclamation.ExchangeID], reclamation.Because)
		if err != nil {
			problems = append(problems, err)
			unreclaimed[reclamation.ExchangeID] = true
			continue
		}
		recorded[reclamation.ExchangeID] = after
		pass.Results = append(pass.Results, SupervisionResult{
			ExchangeID: reclamation.ExchangeID,
			Outcome:    SupervisionReclaimed,
			Detail:     fmt.Sprintf("round %d %s", reclamation.Round, reclamation.Because),
		})
	}
	for _, exhaustion := range pass.Plan.Exhaust {
		if mine[exhaustion.ExchangeID] == nil {
			continue
		}
		// A thread whose reclaim failed is left open rather than closed over a
		// round that still reads as a question somebody is working on. The reclaim
		// is already reported; closing it anyway would turn one problem into a
		// record that contradicts itself.
		if unreclaimed[exhaustion.ExchangeID] {
			continue
		}
		if l.Conductor == nil {
			problems = append(problems, fmt.Errorf("close %s: no conductor is wired to this pass", exhaustion.ExchangeID))
			continue
		}
		if _, err := l.Conductor.Exhaust(recorded[exhaustion.ExchangeID]); err != nil {
			problems = append(problems, err)
			continue
		}
		pass.Results = append(pass.Results, SupervisionResult{
			ExchangeID: exhaustion.ExchangeID,
			Outcome:    SupervisionSettled,
			Detail: fmt.Sprintf("all %d of its permitted rounds are spent and nobody asked again; closed unresolved and the operator told",
				exhaustion.Cap),
		})
	}
	for _, stale := range pass.Plan.Stale {
		pass.Results = append(pass.Results, SupervisionResult{
			ExchangeID: stale.ExchangeID,
			Outcome:    SupervisionStale,
			Detail:     staleDetail(stale),
		})
	}
	for _, queued := range pass.Plan.Queued {
		pass.Results = append(pass.Results, SupervisionResult{
			ExchangeID: queued.ExchangeID,
			Outcome:    SupervisionQueued,
			Detail:     queued.Because,
		})
	}
	for _, delivery := range pass.Plan.Deliver {
		pass.Results = append(pass.Results, SupervisionResult{
			ExchangeID: delivery.ExchangeID,
			Outcome:    SupervisionUndelivered,
			Detail: fmt.Sprintf("no voice is wired to this pass, so round %d was not put in front of the %s",
				delivery.Round, delivery.To),
		})
	}
	return pass, errors.Join(problems...)
}

// staleDetail says what moved under one exchange, and says separately what could
// not be judged. The two are never run together: a reference nothing current is
// known about has not been found unmoved, and a line that read as though it had
// would be the reassurance this exists to refuse.
func staleDetail(stale exchange.Staleness) string {
	var parts []string
	if len(stale.Moved) > 0 {
		moved := make([]string, 0, len(stale.Moved))
		for _, one := range stale.Moved {
			moved = append(moved, fmt.Sprintf("%s was read at %s and is now at %s",
				one.Reference.Key(), one.Reference.Revision, one.Now))
		}
		parts = append(parts, fmt.Sprintf("tell the %s: %s", stale.Tell, strings.Join(moved, "; ")))
	}
	if len(stale.Unjudged) > 0 {
		unjudged := make([]string, 0, len(stale.Unjudged))
		for _, one := range stale.Unjudged {
			unjudged = append(unjudged, one.Key())
		}
		parts = append(parts, fmt.Sprintf("nothing current is known about %s, so they were not judged",
			strings.Join(unjudged, ", ")))
	}
	return strings.Join(parts, "; ")
}
