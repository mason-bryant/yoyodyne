package runstate

import (
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// The ledger is the price join read for the items that shipped, most recent
// promotion first, with the wall clock beside the money: every run of the item
// counts, the failed attempt included, and elapsed is first claim to promotion
// with the paused hours inside it and stated separately.
func TestStoreListsWhatShippedMostRecentFirstWithItsPriceAndWallClock(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	claimed := time.Date(2026, 8, 17, 20, 0, 0, 0, time.UTC)

	// One item, two attempts: a rejected one that spent money and paused for an
	// hour of the night, then the one that promoted, three hours after the first
	// claim. It was renamed between them, and the title that shipped is the one
	// the ledger reports.
	rejected := testState(t, StatusFailed)
	rejected.WorkItemID = "yoyodyne-ifd.2.7"
	rejected.WorkItemTitle = "Resume a run"
	rejected.StartedAt = claimed
	rejected.UpdatedAt = claimed
	claimMoment := claimed.Add(time.Minute)
	rejected.WorkItemClaimedAt = &claimMoment
	rejected.UsageLimitPausedSeconds = 3600
	rejected.ProviderSessionID = "session-rejected"
	if err := store.Create(rejected); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	appendLegacyCostEvents(t, store, rejected.RunID, 1, execution.EventRunCompleted, 8.0)

	promoted := integratedState(t, PhaseCleaningUp)
	promoted.WorkItemID = rejected.WorkItemID
	promoted.WorkItemTitle = "Resume an interrupted run"
	promoted.StartedAt = claimed.Add(2 * time.Hour)
	promoted.UpdatedAt = promoted.StartedAt
	shipped := claimed.Add(3*time.Hour + time.Minute)
	promoted.CompletedAt = &shipped
	promoted.OperatorHeldSeconds = 600
	if err := store.Create(promoted); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	appendLegacyCostEvents(t, store, promoted.RunID, 1, execution.EventRunCompleted, 20.0)

	// A second item shipped a day later, and is therefore listed first.
	later := integratedState(t, PhaseCleaningUp)
	later.WorkItemID = "yoyodyne-ifd.41"
	later.StartedAt = claimed.Add(24 * time.Hour)
	later.UpdatedAt = later.StartedAt
	laterShipped := later.StartedAt.Add(30 * time.Minute)
	later.CompletedAt = &laterShipped
	if err := store.Create(later); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	appendLegacyCostEvents(t, store, later.RunID, 1, execution.EventRunCompleted, 1.5)

	// An item that succeeded without promoting anything — a diagnosis landed as
	// evidence, say — did not ship, and neither did one that failed.
	unshipped := testState(t, StatusSucceeded)
	unshipped.RunID = mustRunID(t)
	unshipped.WorkItemID = "yoyodyne-ifd.99"
	if err := store.Create(unshipped); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	ledger, err := store.Shipped(0)
	if err != nil {
		t.Fatalf("Shipped() error = %v", err)
	}
	if ledger.Shipped != 2 || len(ledger.Items) != 2 {
		t.Fatalf("Shipped() = %#v, want two shipped items", ledger)
	}
	if ledger.Items[0].WorkItemID != "yoyodyne-ifd.41" || ledger.Items[1].WorkItemID != "yoyodyne-ifd.2.7" {
		t.Fatalf("Shipped() order = %q, %q, want most recent promotion first", ledger.Items[0].WorkItemID, ledger.Items[1].WorkItemID)
	}

	item := ledger.Items[1]
	if item.ShippedRunID != promoted.RunID || !item.ShippedAt.Equal(shipped) {
		t.Fatalf("shipped by %s at %s, want %s at %s", item.ShippedRunID, item.ShippedAt, promoted.RunID, shipped)
	}
	// The claim is the earliest run's recorded claim, not its start.
	if !item.ClaimedAt.Equal(claimMoment) {
		t.Fatalf("ClaimedAt = %s, want the first run's recorded claim %s", item.ClaimedAt, claimMoment)
	}
	took, known := item.Elapsed()
	if !known || took != 3*time.Hour {
		t.Fatalf("Elapsed() = %s, %v; want 3h from the first claim to the promotion", took, known)
	}
	// Paused is every wait of every run: the rejected attempt's hour on the
	// provider and the promoting one's ten minutes on the operator's hold.
	if item.Paused() != time.Hour+10*time.Minute {
		t.Fatalf("Paused() = %s, want 1h10m across both runs", item.Paused())
	}
	if item.Price.TotalUSD != 28.0 || len(item.Price.Runs) != 2 {
		t.Fatalf("Price = %#v, want both runs priced at 28 together", item.Price)
	}
	if item.Title != "Resume an interrupted run" {
		t.Fatalf("Title = %q, want the title the shipping run recorded", item.Title)
	}

	// The limit bounds the listing and not the count of what shipped.
	bounded, err := store.Shipped(1)
	if err != nil {
		t.Fatalf("Shipped(1) error = %v", err)
	}
	if bounded.Shipped != 2 || len(bounded.Items) != 1 || bounded.Items[0].WorkItemID != "yoyodyne-ifd.41" {
		t.Fatalf("Shipped(1) = %#v, want the most recent of two", bounded)
	}
}

// An item whose records predate part of the recording reports what it can and
// states the rest as unknown, never as nothing: a promoting run that has not
// recorded completing has no elapsed time to report, and a run written before
// titles were carried leaves the title empty rather than invented.
func TestStoreSaysWhatItCannotReadAboutAShippedItem(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	started := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	finishing := integratedState(t, PhaseCompleting)
	finishing.Status = StatusRunning
	finishing.CompletedAt = nil
	finishing.WorkItemID = "yoyodyne-ifd.12"
	finishing.StartedAt = started
	finishing.UpdatedAt = started.Add(time.Hour)
	if err := store.Create(finishing); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	appendLegacyCostEvents(t, store, finishing.RunID, 1, execution.EventRunCompleted, 2.0)

	ledger, err := store.Shipped(0)
	if err != nil {
		t.Fatalf("Shipped() error = %v", err)
	}
	if len(ledger.Items) != 1 {
		t.Fatalf("Shipped() = %#v, want the promoted run's item", ledger)
	}
	item := ledger.Items[0]
	if _, known := item.Elapsed(); known || item.ElapsedUnknown == "" || item.ElapsedSeconds != 0 {
		t.Fatalf("item = %#v, want elapsed unknown rather than a figure", item)
	}
	// Placed by its last update, which is the nearest moment the record has.
	if !item.ShippedAt.Equal(finishing.UpdatedAt) {
		t.Fatalf("ShippedAt = %s, want the last update %s", item.ShippedAt, finishing.UpdatedAt)
	}
	// No claim was recorded, so the start stands in for it.
	if !item.ClaimedAt.Equal(started) {
		t.Fatalf("ClaimedAt = %s, want the run's start %s", item.ClaimedAt, started)
	}
	if item.Title != "" {
		t.Fatalf("Title = %q, want none where no run recorded one", item.Title)
	}
	if item.Paused() != 0 {
		t.Fatalf("Paused() = %s, want nothing on a run that recorded no wait", item.Paused())
	}
}
