// Package cost puts a price on work items. The evidence is already recorded and
// already attributed -- every provider invocation ends in an event carrying what
// it cost, and every run records the work item it served -- and this is the join
// nobody was making: the price of a piece of work is what every run made for it
// cost, failed attempts and reviewer invocations included.
//
// The price is written onto the item in the tracker rather than kept beside it,
// so everything that reads the tracker reads the same number. What this package
// will not do is invent one: a run whose evidence is gone is carried through as
// unknown, and an item with no recorded runs is left unpriced rather than
// recorded as free.
//
// The scope is runs and only runs. Conversation turns cost money too and are
// recorded just as durably, but attributing a conversation that discussed five
// items to any one of them is a judgement rather than a join, so it is left out
// and said to be left out rather than guessed at.
package cost

import (
	"context"
	"errors"
	"fmt"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Prices is the recorded run evidence a price is read from. It is satisfied by
// runstate.Store.
type Prices interface {
	Price(workItemID string) (runstate.ItemPrice, error)
	Prices() ([]runstate.ItemPrice, error)
}

// Tracker is where a price is carried. It is satisfied by beads.Client.
type Tracker interface {
	RecordCost(ctx context.Context, id string, cost beads.Cost) (beads.WorkItem, error)
}

// Ledger prices work items from recorded runs and records what it found.
type Ledger struct {
	Prices  Prices
	Tracker Tracker
}

// Recorded is what pricing one work item achieved. Failure names what stopped
// the price reaching the tracker, and is set on an entry that was still priced:
// the price is evidence whether or not anybody could store it.
type Recorded struct {
	WorkItemID string     `json:"work_item_id"`
	Cost       beads.Cost `json:"cost"`
	Failure    string     `json:"failure,omitempty"`
}

// Record prices one work item from its recorded runs and writes that price onto
// the item. An item the harness has never run has no price to record, which is
// reported as no price rather than as a price of nothing.
func (l Ledger) Record(ctx context.Context, workItemID string) (*beads.Cost, error) {
	if err := l.validate(); err != nil {
		return nil, err
	}
	price, err := l.Prices.Price(workItemID)
	if err != nil {
		return nil, fmt.Errorf("price the runs of %s: %w", workItemID, err)
	}
	if !price.Recorded() {
		return nil, nil
	}
	recorded := Of(price)
	if _, err := l.Tracker.RecordCost(ctx, workItemID, recorded); err != nil {
		return &recorded, fmt.Errorf("record what %s cost on the work item: %w", workItemID, err)
	}
	return &recorded, nil
}

// RecordAll prices every work item the harness has ever run and writes each
// price onto its item. It is what starts the ledger from what already happened
// rather than from today: the runs closed items were finished by are still
// recorded, so those items can be priced retroactively.
//
// One item that cannot be written never stops the rest. A backfill that gave up
// at the first failure would leave the ledger half written with no record of
// which half, so every failure is carried on its own entry instead.
func (l Ledger) RecordAll(ctx context.Context) ([]Recorded, error) {
	if err := l.validate(); err != nil {
		return nil, err
	}
	prices, err := l.Prices.Prices()
	if err != nil {
		return nil, fmt.Errorf("price the recorded runs: %w", err)
	}
	recorded := make([]Recorded, 0, len(prices))
	for _, price := range prices {
		if !price.Recorded() {
			continue
		}
		entry := Recorded{WorkItemID: price.WorkItemID, Cost: Of(price)}
		if _, err := l.Tracker.RecordCost(ctx, price.WorkItemID, entry.Cost); err != nil {
			entry.Failure = err.Error()
		}
		recorded = append(recorded, entry)
	}
	return recorded, nil
}

// Of projects the recorded run evidence into the price the tracker carries. The
// per-run breakdown stays in the run record where it was written; what the
// tracker holds is the item's own total and how much of it is a floor.
func Of(price runstate.ItemPrice) beads.Cost {
	return beads.Cost{
		TotalUSD:    price.TotalUSD,
		Runs:        len(price.Runs),
		UnknownRuns: price.UnknownRuns,
	}
}

func (l Ledger) validate() error {
	var problems []error
	if l.Prices == nil {
		problems = append(problems, errors.New("a ledger needs the recorded runs to price"))
	}
	if l.Tracker == nil {
		problems = append(problems, errors.New("a ledger needs the tracker that carries the price"))
	}
	return errors.Join(problems...)
}
