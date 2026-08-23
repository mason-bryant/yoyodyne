package runstate

// What a work item cost is a join nothing else makes. Every run records the
// item it served, and every provider invocation inside a run ends in an event
// carrying the provider's own cost for it, so an item's price is the sum over
// every run made for it: the failed attempt as well as the successful one, the
// repair attempts, and the reviewer's invocation beside the developer's. A
// per-run view of the same evidence shows several numbers where the question
// asked was about one piece of work.
//
// The cost is read from the recorded evidence and never estimated from a price
// table, which drifts the moment a provider changes what it charges. A run whose
// evidence is gone is priced as unknown rather than as nothing: a zero that
// means "no record" would corrupt every total it is added to.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// pricedEvents are the events that carry what one provider invocation cost.
// Both are terminal reports of a single invocation and both carry the cost, so a
// run whose provider ended in an error is priced with the money that error spent
// rather than as though it had been free.
var pricedEvents = []execution.EventType{execution.EventRunCompleted, execution.EventRunFailed}

// spendEvidenceEvents are every event the phase split is read from: the priced
// terminals above, and the review that brackets one of them. A review announcing
// itself costs nothing and is decoded anyway, because it is what says the next
// terminal is the reviewer's rather than the developer's.
var spendEvidenceEvents = append([]execution.EventType{execution.EventReviewStarted}, pricedEvents...)

// PhaseCost is what one part of a run cost and how many provider invocations it
// took. The count travels with the money because the money alone does not say
// whether a phase was one expensive invocation or five cheap ones, and what a
// figure like this is read for -- how often work has to be repaired, how much a
// review round costs -- is as much about how often as about how much.
type PhaseCost struct {
	CostUSD     float64 `json:"cost_usd"`
	Invocations int     `json:"invocations,omitempty"`
}

func (p *PhaseCost) add(cost float64) {
	p.CostUSD += cost
	p.Invocations++
}

func (p *PhaseCost) merge(other PhaseCost) {
	p.CostUSD += other.CostUSD
	p.Invocations += other.Invocations
}

// Waits is time a run spent not working: waiting out a provider that refused it
// for want of capacity or could not serve it at all, and parked at a provider
// call while the operator held activity. It is counted in seconds rather than in
// dollars because a wait spends nothing, and adding it to the money would make a
// run that waited overnight read as expensive when what it was is slow.
//
// It is read from the run's own durable state rather than from its event log,
// which is why it survives what the money does not: a run whose log is gone
// still says how long it was held up.
type Waits struct {
	UsageLimitSeconds   int64 `json:"usage_limit_seconds,omitempty"`
	OperatorHoldSeconds int64 `json:"operator_hold_seconds,omitempty"`
}

// Total is how long the waits held work up altogether.
func (w Waits) Total() time.Duration {
	return time.Duration(w.UsageLimitSeconds+w.OperatorHoldSeconds) * time.Second
}

func (w *Waits) merge(other Waits) {
	w.UsageLimitSeconds += other.UsageLimitSeconds
	w.OperatorHoldSeconds += other.OperatorHoldSeconds
}

// PhaseSpend splits what was spent by the part of the work each invocation
// served. A single total says a piece of work was expensive; this says whether
// it was expensive because the change was hard, because it was reviewed over and
// over, or because it had to be repaired -- which is the difference between a
// number to look at and a number to act on.
//
// Every priced invocation in a run's log lands in exactly one of the three, so
// the money always adds back up to the total beside it. That is what makes this
// a decomposition of the price rather than a second opinion about it.
type PhaseSpend struct {
	// Development is the developer's first attempt at the change, including any
	// invocation reissued into it after the provider refused or killed one: what
	// the attempt cost is what it took to get it made.
	Development PhaseCost `json:"development"`
	// Review is every reviewer invocation the run made, whichever way each
	// verdict went and including the ones that produced no verdict at all.
	Review PhaseCost `json:"review"`
	// Repair is every developer attempt after the first -- the failing check, the
	// refused path, and the reviewer's findings handed back are all repair, since
	// each is the same thing from the money's point of view: the change being made
	// again because it was not right the first time.
	Repair PhaseCost `json:"repair"`
	// Waits is time rather than money, and is here rather than beside this because
	// it answers the same question: where a piece of work went.
	Waits Waits `json:"waits"`
}

// TotalUSD is what the split adds up to, which is the total it was split from.
func (p PhaseSpend) TotalUSD() float64 {
	return p.Development.CostUSD + p.Review.CostUSD + p.Repair.CostUSD
}

// Invocations is how many provider invocations the split covers.
func (p PhaseSpend) Invocations() int {
	return p.Development.Invocations + p.Review.Invocations + p.Repair.Invocations
}

// Merge adds another split into this one, which is what an aggregate across
// several runs or several work items is made of.
func (p *PhaseSpend) Merge(other PhaseSpend) {
	p.Development.merge(other.Development)
	p.Review.merge(other.Review)
	p.Repair.merge(other.Repair)
	p.Waits.merge(other.Waits)
}

// RunPrice is what one recorded run cost, as its own event log reports it.
type RunPrice struct {
	RunID       string     `json:"run_id"`
	WorkItemID  string     `json:"work_item_id"`
	Status      Status     `json:"status"`
	Phase       Phase      `json:"phase,omitempty"`
	StartedAt   time.Time  `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	// Integrated reports a run that promoted its work, which is what separates
	// the attempt that finished a piece of work from the ones that did not.
	Integrated bool `json:"integrated,omitempty"`
	// Invocations counts the provider invocations priced from this run's log,
	// which is the developer's, the reviewer's, and one more per repair attempt.
	Invocations int     `json:"invocations,omitempty"`
	CostUSD     float64 `json:"cost_usd"`
	// Phases splits that cost by the part of the run each invocation served. Its
	// money is zero on a run that could not be priced; its waits are not, because
	// they come from the run's own state rather than from the log that is gone.
	Phases PhaseSpend `json:"phases"`
	// Unknown says why this run could not be priced, and is empty on one that
	// was. A run carrying it is not a run that cost nothing: nothing survives to
	// say what it cost, which is a different fact and is stated as itself.
	Unknown string `json:"unknown,omitempty"`
}

// Known reports a run the recorded evidence could actually price.
func (r RunPrice) Known() bool { return r.Unknown == "" }

// ItemPrice is what every recorded run of one work item cost. The total covers
// the runs that could be priced, so an item with unknown runs is priced at a
// floor rather than at a number that reads as complete.
type ItemPrice struct {
	WorkItemID string     `json:"work_item_id"`
	Runs       []RunPrice `json:"runs,omitempty"`
	TotalUSD   float64    `json:"total_usd"`
	// Phases splits that total across every run made for the item, which is where
	// the split earns its keep: an item that took three attempts is where the
	// money that went on repair rather than on the change itself is visible.
	Phases PhaseSpend `json:"phases"`
	// UnknownRuns counts the runs whose evidence is gone. While it is non-zero
	// the total is a lower bound on what the item cost.
	UnknownRuns int `json:"unknown_runs,omitempty"`
}

// Priced reports a total every run behind it could be priced from, which is
// what separates an exact price from a floor.
func (i ItemPrice) Priced() bool { return i.UnknownRuns == 0 }

// Recorded reports that the harness has at least one run recorded for this
// item. An item with none has no price rather than a price of zero: nothing was
// ever run for it, or the runs that were are no longer recorded, and neither is
// a thing to charge somebody nothing for.
func (i ItemPrice) Recorded() bool { return len(i.Runs) > 0 }

// Price reports what every recorded run of one work item cost. Reading the
// record decides nothing about the runs, so a run another process is executing
// is priced here exactly as a finished one is — with whatever its log holds so
// far.
func (s *Store) Price(workItemID string) (ItemPrice, error) {
	id := strings.TrimSpace(workItemID)
	if id == "" {
		return ItemPrice{}, errors.New("a work item is required to price it")
	}
	states, err := s.scan("recorded", func(state State) bool { return state.WorkItemID == id })
	if err != nil {
		return ItemPrice{}, err
	}
	return s.price(id, states), nil
}

// Prices reports what every work item the harness has ever run cost, one entry
// per item in identifier order. It is what prices the items that were closed
// before anything recorded a price on them: the runs and their event logs are
// still here, so the ledger starts from what already happened rather than from
// today.
func (s *Store) Prices() ([]ItemPrice, error) {
	states, err := s.scan("recorded", func(State) bool { return true })
	if err != nil {
		return nil, err
	}
	byItem := make(map[string][]State)
	for _, state := range states {
		byItem[state.WorkItemID] = append(byItem[state.WorkItemID], state)
	}
	items := make([]string, 0, len(byItem))
	for item := range byItem {
		items = append(items, item)
	}
	sort.Strings(items)
	prices := make([]ItemPrice, 0, len(items))
	for _, item := range items {
		prices = append(prices, s.price(item, byItem[item]))
	}
	return prices, nil
}

// price prices one item's runs, oldest first, so a breakdown reads as the
// history it is: the attempt that was rejected, then the one that was not.
func (s *Store) price(workItemID string, states []State) ItemPrice {
	sort.SliceStable(states, func(first, second int) bool {
		if !states[first].StartedAt.Equal(states[second].StartedAt) {
			return states[first].StartedAt.Before(states[second].StartedAt)
		}
		return states[first].RunID < states[second].RunID
	})
	price := ItemPrice{WorkItemID: workItemID}
	for _, state := range states {
		run := s.priceRun(state)
		price.Runs = append(price.Runs, run)
		// The split is merged before the run is judged priceable, which is what
		// keeps an unpriceable run's waits in the item's total: it spent no money
		// anybody can name and it still held the work up for as long as it did.
		price.Phases.Merge(run.Phases)
		if !run.Known() {
			price.UnknownRuns++
			continue
		}
		price.TotalUSD += run.CostUSD
	}
	return price
}

// priceRun prices one run from its own event log. It never fails: an event log
// that is gone or cannot be read is what an unknown price is, and reporting that
// as an error would lose the runs beside it that can be priced.
func (s *Store) priceRun(state State) RunPrice {
	run := RunPrice{
		RunID:       state.RunID,
		WorkItemID:  state.WorkItemID,
		Status:      state.Status,
		Phase:       state.Phase,
		StartedAt:   state.StartedAt,
		CompletedAt: state.CompletedAt,
		Integrated:  state.Integration != nil,
	}
	// What the run waited is read from its own state rather than from its event
	// log, so it is recorded before anything that can fail: a run whose log is
	// gone still says how long it was held up, because what held it up was never
	// written in the log to begin with.
	waits := Waits{
		UsageLimitSeconds:   state.UsageLimitPausedSeconds,
		OperatorHoldSeconds: state.OperatorHeldSeconds,
	}
	run.Phases.Waits = waits
	path, err := s.eventPath(state.RunID)
	if err != nil {
		run.Unknown = err.Error()
		return run
	}
	spend, err := scanEventSpend(path)
	if err != nil {
		run.Unknown = err.Error()
		return run
	}
	// A run that recorded a provider session made at least one invocation, so a
	// log with none in it is a log that lost them rather than a run that never
	// spent anything. A run with no session recorded never got as far as invoking
	// a provider, and costing nothing is the truth about it.
	if spend.Invocations() == 0 && (strings.TrimSpace(state.ProviderSessionID) != "" || strings.TrimSpace(state.ReviewSessionID) != "") {
		run.Unknown = "the run recorded a provider session but its event log holds no invocation to price"
		return run
	}
	spend.Waits = waits
	run.Phases = spend
	run.CostUSD = spend.TotalUSD()
	run.Invocations = spend.Invocations()
	return run
}

// scanEventCost sums what the provider reported for every invocation in one
// event log, without regard for which part of the run each served. It is what a
// log with no phases in it is priced by -- a branch review is one reviewer and
// nothing else -- and it is the split's own total said the short way.
func scanEventCost(path string) (float64, int, error) {
	spend, err := scanEventSpend(path)
	if err != nil {
		return 0, 0, err
	}
	return spend.TotalUSD(), spend.Invocations(), nil
}

// scanEventSpend splits what the provider reported for every invocation in one
// event log by the part of the run that invocation served. It reads the log a
// line at a time and decodes only the lines that can carry a cost or open a
// review, because an event log is mostly the invocation's own chatter and
// pricing a run must not mean decoding all of it.
//
// The split comes out of the log's own shape rather than out of a phase the
// harness had to remember to write down beside each invocation. A review is
// bracketed: the reviewer announces itself and then makes exactly one
// invocation, so the terminal after a review.started is the reviewer's and no
// other terminal is. Every remaining terminal is a developer invocation, and
// those group into attempts by how each one ended. An attempt that reached a
// terminal of its own is over, so the next developer invocation is the next
// attempt; an attempt the provider refused for want of capacity or killed
// mid-flight is reissued in the same session, so the invocation after it is the
// same attempt still being made and its cost belongs to that attempt. The first
// attempt is the development and every attempt after it is a repair.
func scanEventSpend(path string) (PhaseSpend, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return PhaseSpend{}, errors.New("the event log is no longer recorded, so what it cost cannot be read")
	}
	if err != nil {
		return PhaseSpend{}, fmt.Errorf("open event log to price the run: %w", err)
	}
	defer file.Close()

	var (
		spend PhaseSpend
		// reviewing is set by a review announcing itself and cleared by the single
		// invocation it makes rather than by the review closing, because a review
		// the provider never answered closes with nothing at all -- and a bracket
		// waiting for a close that never comes would charge the developer
		// invocations after it to the reviewer.
		reviewing bool
		// attempt is which developer attempt the log has reached and ended says
		// the last one finished being made. Together they are what puts a reissued
		// invocation on the attempt it is reissuing rather than on a repair nobody
		// asked for.
		attempt int
		ended   bool
	)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), maxEncodedEventBytes)
	for scanner.Scan() {
		line := scanner.Bytes()
		if !carriesSpendEvidence(line) {
			continue
		}
		var priced pricedEvent
		if err := json.Unmarshal(line, &priced); err != nil {
			return PhaseSpend{}, fmt.Errorf("decode event log to price the run: %w", err)
		}
		// The quick filter matches text anywhere in the line, so the decoded type
		// is what decides: an agent quoting an event name is not an invocation and
		// does not open a review either.
		if priced.Type == execution.EventReviewStarted {
			reviewing = true
			continue
		}
		if !priced.priced() {
			continue
		}
		if reviewing {
			spend.Review.add(priced.Payload.TotalCostUSD)
			reviewing = false
			continue
		}
		if ended {
			attempt++
		}
		if attempt == 0 {
			spend.Development.add(priced.Payload.TotalCostUSD)
		} else {
			spend.Repair.add(priced.Payload.TotalCostUSD)
		}
		ended = priced.Type == execution.EventRunCompleted
	}
	if err := scanner.Err(); err != nil {
		return PhaseSpend{}, fmt.Errorf("read event log to price the run: %w", err)
	}
	return spend, nil
}

// pricedEvent is the little of an event pricing needs: which event it is, and
// what the provider said the invocation cost.
type pricedEvent struct {
	Type    execution.EventType `json:"type"`
	Payload struct {
		TotalCostUSD float64 `json:"total_cost_usd"`
	} `json:"payload"`
}

func (p pricedEvent) priced() bool {
	for _, candidate := range pricedEvents {
		if p.Type == candidate {
			return true
		}
	}
	return false
}

// carriesSpendEvidence is the cheap test that decides whether a line is worth
// decoding. It over-matches deliberately: what it must never do is skip a line
// that carries a cost or opens a review, and the decoded type rejects everything
// else.
func carriesSpendEvidence(line []byte) bool {
	for _, candidate := range spendEvidenceEvents {
		if bytes.Contains(line, []byte(`"`+string(candidate)+`"`)) {
			return true
		}
	}
	return false
}
