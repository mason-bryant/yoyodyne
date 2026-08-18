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
	path, err := s.eventPath(state.RunID)
	if err != nil {
		run.Unknown = err.Error()
		return run
	}
	cost, invocations, err := scanEventCost(path)
	if err != nil {
		run.Unknown = err.Error()
		return run
	}
	// A run that recorded a provider session made at least one invocation, so a
	// log with none in it is a log that lost them rather than a run that never
	// spent anything. A run with no session recorded never got as far as invoking
	// a provider, and costing nothing is the truth about it.
	if invocations == 0 && (strings.TrimSpace(state.ProviderSessionID) != "" || strings.TrimSpace(state.ReviewSessionID) != "") {
		run.Unknown = "the run recorded a provider session but its event log holds no invocation to price"
		return run
	}
	run.CostUSD = cost
	run.Invocations = invocations
	return run
}

// scanEventCost sums what the provider reported for every invocation in one
// event log. It reads the log a line at a time and decodes only the lines that
// can carry a cost, because an event log is mostly the invocation's own chatter
// and pricing a run must not mean decoding all of it.
func scanEventCost(path string) (float64, int, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return 0, 0, errors.New("the run's event log is no longer recorded, so what it cost cannot be read")
	}
	if err != nil {
		return 0, 0, fmt.Errorf("open event log to price the run: %w", err)
	}
	defer file.Close()

	var (
		cost        float64
		invocations int
	)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), maxEncodedEventBytes)
	for scanner.Scan() {
		line := scanner.Bytes()
		if !carriesCost(line) {
			continue
		}
		var priced pricedEvent
		if err := json.Unmarshal(line, &priced); err != nil {
			return 0, 0, fmt.Errorf("decode event log to price the run: %w", err)
		}
		// The quick filter matches text anywhere in the line, so the decoded type
		// is what decides: an agent quoting an event name is not an invocation.
		if !priced.priced() {
			continue
		}
		cost += priced.Payload.TotalCostUSD
		invocations++
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, fmt.Errorf("read event log to price the run: %w", err)
	}
	return cost, invocations, nil
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

// carriesCost is the cheap test that decides whether a line is worth decoding.
// It over-matches deliberately: what it must never do is skip a line that does
// carry a cost, and the decoded type rejects everything else.
func carriesCost(line []byte) bool {
	for _, candidate := range pricedEvents {
		if bytes.Contains(line, []byte(`"`+string(candidate)+`"`)) {
			return true
		}
	}
	return false
}
