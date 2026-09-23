package cost

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func TestLedgerRecordsWhatEveryRunOfAnItemCost(t *testing.T) {
	t.Parallel()

	prices := &fakePrices{price: runstate.ItemPrice{
		WorkItemID: "yoyodyne-ifd.2.7",
		Runs: []runstate.RunPrice{
			{RunID: "run-1", CostUSD: 9},
			{RunID: "run-2", CostUSD: 19},
		},
		TotalUSD: 28,
	}}
	tracker := &fakeTracker{}
	recorded, err := Ledger{Prices: prices, Tracker: tracker}.Record(context.Background(), "yoyodyne-ifd.2.7")
	if err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	want := beads.Cost{TotalUSD: 28, Runs: 2}
	if recorded == nil || *recorded != want {
		t.Fatalf("Record() = %#v, want %#v", recorded, want)
	}
	if len(tracker.recorded) != 1 || tracker.recorded[0].id != "yoyodyne-ifd.2.7" || tracker.recorded[0].cost != want {
		t.Fatalf("tracker recorded %#v", tracker.recorded)
	}
}

// A run whose record is gone is carried through as unknown rather than dropped,
// so what the tracker holds says it is a floor rather than a price.
func TestLedgerCarriesUnpricedRunsOntoTheItem(t *testing.T) {
	t.Parallel()

	prices := &fakePrices{price: runstate.ItemPrice{
		WorkItemID: "yoyodyne-ifd.41",
		Runs: []runstate.RunPrice{
			{RunID: "run-1", Unknown: "the run's event log is no longer recorded"},
			{RunID: "run-2", CostUSD: 3.5},
		},
		TotalUSD:    3.5,
		UnknownRuns: 1,
	}}
	tracker := &fakeTracker{}
	recorded, err := Ledger{Prices: prices, Tracker: tracker}.Record(context.Background(), "yoyodyne-ifd.41")
	if err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if recorded.UnknownRuns != 1 || recorded.Complete() || recorded.TotalUSD != 3.5 {
		t.Fatalf("Record() = %#v", recorded)
	}
}

// An item the harness has never run has no price. Recording zero for it would
// put a price tag reading nothing on work nobody has done.
func TestLedgerRecordsNothingForAnItemWithNoRuns(t *testing.T) {
	t.Parallel()

	tracker := &fakeTracker{}
	recorded, err := Ledger{Prices: &fakePrices{}, Tracker: tracker}.Record(context.Background(), "yoyodyne-ifd.99")
	if err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if recorded != nil {
		t.Fatalf("Record() = %#v, want no price", recorded)
	}
	if len(tracker.recorded) != 0 {
		t.Fatalf("an unrun item was still priced: %#v", tracker.recorded)
	}
}

// The price is evidence whether or not the tracker could keep it, so a write
// that failed still returns what the runs cost.
func TestLedgerReportsAPriceItCouldNotRecord(t *testing.T) {
	t.Parallel()

	prices := &fakePrices{price: runstate.ItemPrice{
		WorkItemID: "yoyodyne-ifd.41",
		Runs:       []runstate.RunPrice{{RunID: "run-1", CostUSD: 2}},
		TotalUSD:   2,
	}}
	tracker := &fakeTracker{err: errors.New("bd update failed")}
	recorded, err := Ledger{Prices: prices, Tracker: tracker}.Record(context.Background(), "yoyodyne-ifd.41")
	if err == nil || !strings.Contains(err.Error(), "bd update failed") {
		t.Fatalf("Record() error = %v", err)
	}
	if recorded == nil || recorded.TotalUSD != 2 {
		t.Fatalf("Record() = %#v, want the price beside the failure", recorded)
	}
}

// A backfill that gave up at the first item it could not write would leave the
// ledger half written with nothing saying which half.
func TestLedgerBackfillsEveryItemAndKeepsGoingPastAFailure(t *testing.T) {
	t.Parallel()

	prices := &fakePrices{all: []runstate.ItemPrice{
		{WorkItemID: "yoyodyne-ifd.41", Runs: []runstate.RunPrice{{RunID: "run-1", CostUSD: 1}}, TotalUSD: 1},
		{WorkItemID: "yoyodyne-ifd.42", Runs: []runstate.RunPrice{{RunID: "run-2", CostUSD: 2}}, TotalUSD: 2},
		{WorkItemID: "yoyodyne-ifd.43"},
	}}
	tracker := &fakeTracker{failFor: "yoyodyne-ifd.41"}
	recorded, err := Ledger{Prices: prices, Tracker: tracker}.RecordAll(context.Background())
	if err != nil {
		t.Fatalf("RecordAll() error = %v", err)
	}
	if len(recorded) != 2 {
		t.Fatalf("RecordAll() = %#v, want the two items with runs", recorded)
	}
	if recorded[0].Failure == "" || recorded[1].Failure != "" {
		t.Fatalf("RecordAll() failures = %#v", recorded)
	}
	if len(tracker.recorded) != 2 {
		t.Fatalf("the failure stopped the backfill: %#v", tracker.recorded)
	}
}

// A price already on the item is written over rather than left alone. That is
// what makes the ledger a backfill for a correction and not only for a gap: every
// item priced before yoyodyne-ifd.432.10 carries a total summed from session
// running totals, and an item the tracker already holds a figure for is exactly
// the item that needs the new one.
func TestLedgerWritesACorrectedPriceOverOneTheItemAlreadyCarries(t *testing.T) {
	t.Parallel()

	prices := &fakePrices{price: runstate.ItemPrice{
		WorkItemID: "yoyodyne-ifd.432.10",
		Runs:       []runstate.RunPrice{{RunID: "run-1", CostUSD: 4}},
		TotalUSD:   4,
	}}
	tracker := &fakeTracker{stored: map[string]beads.Cost{
		"yoyodyne-ifd.432.10": {TotalUSD: 36, Runs: 1},
	}}
	recorded, err := Ledger{Prices: prices, Tracker: tracker}.Record(context.Background(), "yoyodyne-ifd.432.10")
	if err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	want := beads.Cost{TotalUSD: 4, Runs: 1}
	if recorded == nil || *recorded != want {
		t.Fatalf("Record() = %#v, want %#v", recorded, want)
	}
	if got := tracker.stored["yoyodyne-ifd.432.10"]; got != want {
		t.Fatalf("the item still carries %#v, want the corrected %#v", got, want)
	}
}

func TestLedgerRefusesToWorkWithoutBothHalves(t *testing.T) {
	t.Parallel()

	if _, err := (Ledger{Tracker: &fakeTracker{}}).Record(context.Background(), "yoyodyne-1"); err == nil {
		t.Fatal("Record() without recorded runs error = nil")
	}
	if _, err := (Ledger{Prices: &fakePrices{}}).RecordAll(context.Background()); err == nil {
		t.Fatal("RecordAll() without a tracker error = nil")
	}
}

type fakePrices struct {
	price runstate.ItemPrice
	all   []runstate.ItemPrice
	err   error
}

func (f *fakePrices) Price(workItemID string) (runstate.ItemPrice, error) {
	if f.err != nil {
		return runstate.ItemPrice{}, f.err
	}
	price := f.price
	price.WorkItemID = workItemID
	return price, nil
}

func (f *fakePrices) Prices() ([]runstate.ItemPrice, error) { return f.all, f.err }

type fakeTracker struct {
	recorded []recordedPrice
	// stored is what each item carries, the way the tracker does: a write
	// replaces whatever was there, so a test can seed a price and see what
	// recording does to it.
	stored  map[string]beads.Cost
	err     error
	failFor string
}

type recordedPrice struct {
	id   string
	cost beads.Cost
}

func (f *fakeTracker) RecordCost(_ context.Context, id string, cost beads.Cost) (beads.WorkItem, error) {
	f.recorded = append(f.recorded, recordedPrice{id: id, cost: cost})
	if f.err != nil {
		return beads.WorkItem{}, f.err
	}
	if f.failFor == id {
		return beads.WorkItem{}, errors.New("bd update failed for " + id)
	}
	if f.stored != nil {
		f.stored[id] = cost
	}
	return beads.WorkItem{ID: id, Cost: &cost}, nil
}
