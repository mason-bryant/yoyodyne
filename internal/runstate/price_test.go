package runstate

import (
	"os"
	"testing"
	"time"

	"yoyodyne/internal/execution"
)

// An item's price is what every run made for it cost, which is the whole reason
// it is not answerable per run: the rejected attempt and the successful one are
// both part of what the work cost.
func TestStorePricesEveryRunMadeForAnItem(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	first := testState(t, StatusFailed)
	first.WorkItemID = "yoyodyne-ifd.2.7"
	first.Phase = PhaseReviewing
	first.ProviderSessionID = "session-developer"
	if err := store.Create(first); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// A developer invocation, a repair attempt, and one the provider ended in an
	// error: the failure cost money too, so it is priced rather than ignored.
	appendCostEvents(t, store, first.RunID, 1, execution.EventRunCompleted, 6.5)
	appendCostEvents(t, store, first.RunID, 2, execution.EventRunCompleted, 2.25)
	appendCostEvents(t, store, first.RunID, 3, execution.EventRunFailed, 0.25)

	second := testState(t, StatusSucceeded)
	second.RunID = mustRunID(t)
	second.WorkItemID = first.WorkItemID
	second.StartedAt = first.StartedAt.Add(time.Hour)
	second.UpdatedAt = second.StartedAt
	completedAt := second.StartedAt
	second.CompletedAt = &completedAt
	second.Phase = PhaseComplete
	second.ProviderSessionID = "session-developer-2"
	second.ReviewSessionID = "session-reviewer"
	if err := store.Create(second); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	appendCostEvents(t, store, second.RunID, 1, execution.EventRunCompleted, 19.0)

	// Another item's run must never reach this item's price.
	other := testState(t, StatusSucceeded)
	other.RunID = mustRunID(t)
	other.WorkItemID = "yoyodyne-ifd.41"
	other.ProviderSessionID = "session-other"
	if err := store.Create(other); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	appendCostEvents(t, store, other.RunID, 1, execution.EventRunCompleted, 100)

	price, err := store.Price(first.WorkItemID)
	if err != nil {
		t.Fatalf("Price() error = %v", err)
	}
	if !price.Priced() || !price.Recorded() {
		t.Fatalf("Price() = %#v, want a complete recorded price", price)
	}
	if len(price.Runs) != 2 {
		t.Fatalf("Price() priced %d run(s), want 2", len(price.Runs))
	}
	// Oldest first, so the breakdown reads as the history it is.
	if price.Runs[0].RunID != first.RunID || price.Runs[1].RunID != second.RunID {
		t.Fatalf("Price() run order = %q, %q", price.Runs[0].RunID, price.Runs[1].RunID)
	}
	if price.Runs[0].Invocations != 3 || price.Runs[0].CostUSD != 9.0 {
		t.Fatalf("failed run = %#v, want 3 invocations costing 9", price.Runs[0])
	}
	if price.TotalUSD != 28.0 {
		t.Fatalf("Price() total = %v, want 28", price.TotalUSD)
	}
}

// A run whose evidence is gone is stated as unknown. Pricing it as zero would
// silently understate every total it entered.
func TestStorePricesARunWithNoSurvivingRecordAsUnknown(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	lost := testState(t, StatusSucceeded)
	lost.WorkItemID = "yoyodyne-ifd.41"
	lost.ProviderSessionID = "session-developer"
	if err := store.Create(lost); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	kept := testState(t, StatusSucceeded)
	kept.RunID = mustRunID(t)
	kept.WorkItemID = lost.WorkItemID
	kept.StartedAt = lost.StartedAt.Add(time.Hour)
	kept.UpdatedAt = kept.StartedAt
	completedAt := kept.StartedAt
	kept.CompletedAt = &completedAt
	kept.ProviderSessionID = "session-developer-2"
	if err := store.Create(kept); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	appendCostEvents(t, store, kept.RunID, 1, execution.EventRunCompleted, 3.5)

	price, err := store.Price(lost.WorkItemID)
	if err != nil {
		t.Fatalf("Price() error = %v", err)
	}
	if price.UnknownRuns != 1 || price.Priced() {
		t.Fatalf("Price() = %#v, want one unknown run", price)
	}
	if price.Runs[0].Known() || price.Runs[0].CostUSD != 0 {
		t.Fatalf("lost run = %#v, want unknown and no claimed cost", price.Runs[0])
	}
	// The known run is still priced, and the total is what is known rather than
	// a guess at what is not.
	if price.TotalUSD != 3.5 {
		t.Fatalf("Price() total = %v, want 3.5", price.TotalUSD)
	}
}

// The two ways a log holds no invocation are different facts: a run that never
// invoked a provider really did cost nothing, and a run that recorded a session
// and holds no invocation has lost its evidence.
func TestStoreTellsARunThatSpentNothingFromOneThatLostItsEvidence(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	unspent := testState(t, StatusFailed)
	unspent.WorkItemID = "yoyodyne-ifd.41"
	if err := store.Create(unspent); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	appendEvent(t, store, unspent.RunID, 1, execution.EventRunStarted, nil)

	price, err := store.Price(unspent.WorkItemID)
	if err != nil {
		t.Fatalf("Price() error = %v", err)
	}
	if !price.Priced() || price.TotalUSD != 0 || price.Runs[0].Invocations != 0 {
		t.Fatalf("Price() = %#v, want a run priced at nothing", price)
	}

	emptied := testState(t, StatusFailed)
	emptied.RunID = mustRunID(t)
	emptied.WorkItemID = "yoyodyne-ifd.42"
	emptied.ProviderSessionID = "session-developer"
	if err := store.Create(emptied); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	appendEvent(t, store, emptied.RunID, 1, execution.EventRunStarted, nil)

	price, err = store.Price(emptied.WorkItemID)
	if err != nil {
		t.Fatalf("Price() error = %v", err)
	}
	if price.UnknownRuns != 1 {
		t.Fatalf("Price() = %#v, want the run with a session and no invocation to be unknown", price)
	}
}

// An item nobody has run has no price at all. Reporting zero would put a price
// tag reading nothing on work that was never done.
func TestStorePricesAnItemWithNoRecordedRunsAsUnrecorded(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	price, err := store.Price("yoyodyne-ifd.99")
	if err != nil {
		t.Fatalf("Price() error = %v", err)
	}
	if price.Recorded() || len(price.Runs) != 0 {
		t.Fatalf("Price() = %#v, want nothing recorded", price)
	}
	if _, err := store.Price("  "); err == nil {
		t.Fatal("Price() without a work item error = nil")
	}
}

// The ledger is what prices items closed before anything recorded a price: it
// groups every recorded run by the item it served.
func TestStorePricesEveryItemItHasRun(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	for _, item := range []string{"yoyodyne-ifd.42", "yoyodyne-ifd.41", "yoyodyne-ifd.41"} {
		state := testState(t, StatusSucceeded)
		state.RunID = mustRunID(t)
		state.WorkItemID = item
		state.ProviderSessionID = "session-" + state.RunID
		if err := store.Create(state); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		appendCostEvents(t, store, state.RunID, 1, execution.EventRunCompleted, 1.5)
	}

	prices, err := store.Prices()
	if err != nil {
		t.Fatalf("Prices() error = %v", err)
	}
	if len(prices) != 2 {
		t.Fatalf("Prices() = %#v, want two items", prices)
	}
	if prices[0].WorkItemID != "yoyodyne-ifd.41" || len(prices[0].Runs) != 2 || prices[0].TotalUSD != 3.0 {
		t.Fatalf("Prices()[0] = %#v", prices[0])
	}
	if prices[1].WorkItemID != "yoyodyne-ifd.42" || prices[1].TotalUSD != 1.5 {
		t.Fatalf("Prices()[1] = %#v", prices[1])
	}
}

// An event log that cannot be read prices as unknown rather than failing the
// whole answer: the runs beside it are still priceable, and losing them would
// answer less than the records hold.
func TestStorePricesAnUnreadableEventLogAsUnknown(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusSucceeded)
	state.WorkItemID = "yoyodyne-ifd.41"
	state.ProviderSessionID = "session-developer"
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	path, err := store.eventPath(state.RunID)
	if err != nil {
		t.Fatalf("eventPath() error = %v", err)
	}
	if err := os.WriteFile(path, []byte("{\"type\":\"run.completed\" not json\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	price, err := store.Price(state.WorkItemID)
	if err != nil {
		t.Fatalf("Price() error = %v", err)
	}
	if price.UnknownRuns != 1 || price.Runs[0].Known() {
		t.Fatalf("Price() = %#v, want the unreadable log priced as unknown", price)
	}
}

// The cheap line filter is allowed to over-match; what decides is the decoded
// event type, so an agent quoting an event name never invents an invocation.
func TestStorePricesOnlyRealInvocations(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusSucceeded)
	state.WorkItemID = "yoyodyne-ifd.41"
	state.ProviderSessionID = "session-developer"
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	appendEvent(t, store, state.RunID, 1, execution.EventAgentMessage, map[string]any{
		"text":           `the last "run.completed" event said it was done`,
		"total_cost_usd": 99.0,
	})
	appendCostEvents(t, store, state.RunID, 2, execution.EventRunCompleted, 4.0)

	price, err := store.Price(state.WorkItemID)
	if err != nil {
		t.Fatalf("Price() error = %v", err)
	}
	if price.TotalUSD != 4.0 || price.Runs[0].Invocations != 1 {
		t.Fatalf("Price() = %#v, want only the real invocation priced", price)
	}
}

func appendCostEvents(t *testing.T, store *Store, runID string, sequence uint64, eventType execution.EventType, cost float64) {
	t.Helper()
	appendEvent(t, store, runID, sequence, eventType, map[string]any{
		"session_id":     "session-developer",
		"total_cost_usd": cost,
	})
}

func appendEvent(t *testing.T, store *Store, runID string, sequence uint64, eventType execution.EventType, payload any) {
	t.Helper()
	event, err := execution.NewEvent(runID, sequence, time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC), eventType, "claude-code", payload)
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}
	if err := store.AppendEvent(event); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
}

func mustRunID(t *testing.T) string {
	t.Helper()
	runID, err := NewRunID()
	if err != nil {
		t.Fatalf("NewRunID() error = %v", err)
	}
	return runID
}
