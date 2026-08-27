package runstate

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/exchange"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// An exchange is a provider invocation the harness made and nobody's run, so
// nothing that reads runs finds it. Reading it here is what keeps it out of the
// item prices and in what the harness has spent altogether.
func TestExchangeSpendSumsWhatTheRolesSpentAskingEachOther(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store, err := NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	// A product whose roles have never asked each other anything has no exchange
	// directory at all, which is not a failure to read.
	if spend := store.ExchangeSpend(); spend.Recorded() || spend.CostUSD != 0 || !spend.Known() {
		t.Fatalf("ExchangeSpend() = %#v, want nothing recorded", spend)
	}

	// The exchange store is built the way the harness builds it, from the state
	// root, so this is also the claim that the read model finds the same
	// directory the conductor writes to.
	exchanges := newTestExchangeStore(t, root)
	if err := exchanges.Save(testExchange("a")); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	second := testExchange("b")
	answered := second.UpdatedAt.Add(time.Minute)
	second.Rounds = append(second.Rounds, exchange.Round{
		Number:     2,
		Question:   "and what does it cost to leave it until later?",
		Answer:     "More again, and the difference is the interest.",
		CostUSD:    0.75,
		AskedAt:    second.UpdatedAt,
		AnsweredAt: &answered,
	})
	second.UpdatedAt = answered
	if err := exchanges.Save(second); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	spend := store.ExchangeSpend()
	if !spend.Known() || !spend.Recorded() {
		t.Fatalf("ExchangeSpend() = %#v, want a known recorded figure", spend)
	}
	if spend.Exchanges != 2 || spend.Rounds != 3 {
		t.Fatalf("ExchangeSpend() = %#v, want 2 exchanges over 3 rounds", spend)
	}
	// Every round the provider charged for, whichever thread it belonged to.
	if spend.CostUSD != 1.25 {
		t.Fatalf("ExchangeSpend() total = %v, want 1.25", spend.CostUSD)
	}
	// None of it reaches an item's price, because the record holds nothing to
	// join an item on. The test below is what holds that to the record rather
	// than to this comment.
	prices, err := store.Prices()
	if err != nil {
		t.Fatalf("Prices() error = %v", err)
	}
	if len(prices) != 0 {
		t.Fatalf("Prices() = %#v, want no item priced by an exchange", prices)
	}
}

// The aggregate is product-wide rather than per item because the exchange
// record names no item to be per. That is a fact about the recorded shape, and
// this is where it is checked: the moment an exchange can name a work item — or
// name a run, which names one — the join belongs in ItemPrice beside the runs
// rather than on a row of its own, and the report reading it is short until it
// is written.
//
// It is asserted over the type instead of over an instance so that a field
// added to the record fails here, at the join that would have to change, rather
// than passing silently and leaving the money unattributed.
func TestAnExchangeRecordNamesNoWorkItemToAttributeItsSpendTo(t *testing.T) {
	t.Parallel()

	attributable := func(field reflect.StructField) bool {
		name := strings.ToLower(field.Name + " " + field.Tag.Get("json"))
		return strings.Contains(name, "workitem") || strings.Contains(name, "work_item") ||
			strings.Contains(name, "runid") || strings.Contains(name, "run_id")
	}
	for _, recorded := range []reflect.Type{
		reflect.TypeOf(exchange.Exchange{}),
		// The parties are checked too: what an exchange names about each side is
		// a role, an agent, and the conversation it spoke from, and a conversation
		// is not a work item — the same conversation asks about whatever it is
		// discussing that day.
		reflect.TypeOf(exchange.Party{}),
		reflect.TypeOf(exchange.Round{}),
	} {
		for i := 0; i < recorded.NumField(); i++ {
			if attributable(recorded.Field(i)) {
				t.Fatalf("%s.%s names work an exchange's spend could be attributed to; "+
					"Store.ExchangeSpend sums the product's exchanges into one figure and must now join them onto the item instead",
					recorded.Name(), recorded.Field(i).Name)
			}
		}
	}
}

// An exchange nobody can read is not an exchange that cost nothing — and it is
// not the exchanges beside it either. It is counted and left out, they are
// priced as usual, and the figure that results says it is a floor. A record
// that reduced the whole total to nothing would reintroduce the undercount this
// figure exists to remove, one corrupt file at a time.
func TestExchangeSpendPricesWhatItCanReadAndCountsWhatItCannot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store, err := NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	exchanges := newTestExchangeStore(t, root)
	readable := testExchange("c")
	if err := exchanges.Save(readable); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	broken := filepath.Join(exchanges.Root(), "exchange-"+strings.Repeat("d", 32)+".json")
	if err := os.WriteFile(broken, []byte("{not an exchange"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	spend := store.ExchangeSpend()
	if spend.Known() {
		t.Fatalf("ExchangeSpend() = %#v, want a figure that says it is short", spend)
	}
	if !spend.Recorded() || !spend.Enumerated() {
		t.Fatalf("ExchangeSpend() = %#v, want records that were counted", spend)
	}
	if spend.Unreadable != 1 || spend.Unknown == "" {
		t.Fatalf("ExchangeSpend() = %#v, want one unreadable record and the reason", spend)
	}
	// The readable exchange keeps its price. This is the whole finding: the
	// broken file must cost the total that one thread and nothing else.
	if spend.Exchanges != 1 || spend.Rounds != 1 || spend.CostUSD != readable.CostUSD() {
		t.Fatalf("ExchangeSpend() = %#v, want the readable exchange still priced at %v",
			spend, readable.CostUSD())
	}

	// Exchanges that cannot even be listed are a different answer again: what is
	// missing is unknown in number as well as in amount, so there is not even a
	// floor to state. A file where the directory should be is that, and unlike a
	// mode bit it means the same thing whoever is running the test.
	unlistable, err := NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	blocked := unlistable.exchanges().Root()
	if err := os.MkdirAll(filepath.Dir(blocked), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	blind := unlistable.ExchangeSpend()
	if blind.Known() || blind.Enumerated() {
		t.Fatalf("ExchangeSpend() = %#v, want nothing enumerated", blind)
	}
	if blind.Unknown == "" || blind.Unreadable != 0 {
		t.Fatalf("ExchangeSpend() = %#v, want the reason and no count it cannot have", blind)
	}
}

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

// A price that says only what a piece of work cost invites the question it
// cannot answer: whether the money went on making the change, on judging it, or
// on making it again. The split comes out of the log's own shape -- a review
// announces itself and makes one invocation, and the developer invocations
// between the reviews group into attempts -- and it always adds back up to the
// total it was split from.
func TestStoreSplitsWhatARunSpentByThePhaseItServed(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusSucceeded)
	state.WorkItemID = "yoyodyne-ifd.83"
	state.ProviderSessionID = "session-developer"
	state.ReviewSessionID = "session-reviewer"
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// The change, a review that asked for repair, the repair, and the review that
	// took it: the shape every run of any length has.
	appendCostEvents(t, store, state.RunID, 1, execution.EventRunCompleted, 9.0)
	appendEvent(t, store, state.RunID, 2, execution.EventReviewStarted, nil)
	appendCostEvents(t, store, state.RunID, 3, execution.EventRunCompleted, 2.0)
	appendEvent(t, store, state.RunID, 4, execution.EventReviewCompleted, nil)
	appendCostEvents(t, store, state.RunID, 5, execution.EventRunCompleted, 4.0)
	appendEvent(t, store, state.RunID, 6, execution.EventReviewStarted, nil)
	appendCostEvents(t, store, state.RunID, 7, execution.EventRunCompleted, 1.5)

	price, err := store.Price(state.WorkItemID)
	if err != nil {
		t.Fatalf("Price() error = %v", err)
	}
	phases := price.Runs[0].Phases
	if phases.Development != (PhaseCost{CostUSD: 9.0, Invocations: 1}) {
		t.Fatalf("development = %#v, want the first attempt alone", phases.Development)
	}
	if phases.Review != (PhaseCost{CostUSD: 3.5, Invocations: 2}) {
		t.Fatalf("review = %#v, want both reviewer invocations", phases.Review)
	}
	if phases.Repair != (PhaseCost{CostUSD: 4.0, Invocations: 1}) {
		t.Fatalf("repair = %#v, want the second developer attempt", phases.Repair)
	}
	// The split is a decomposition of the price rather than a second opinion
	// about it, so it has to reconstruct what it decomposed.
	if phases.TotalUSD() != price.Runs[0].CostUSD || phases.Invocations() != price.Runs[0].Invocations {
		t.Fatalf("split = %v across %d, run = %v across %d",
			phases.TotalUSD(), phases.Invocations(), price.Runs[0].CostUSD, price.Runs[0].Invocations)
	}
	if price.Phases.TotalUSD() != price.TotalUSD {
		t.Fatalf("item split = %v, item total = %v", price.Phases.TotalUSD(), price.TotalUSD)
	}
}

// An attempt the provider refused or killed is reissued in the same session, so
// the invocation after it is the same attempt still being made. Counting it as a
// fresh attempt would charge the change to repair for something nobody asked a
// developer to fix.
func TestStoreChargesAReissuedInvocationToTheAttemptItReissues(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusSucceeded)
	state.WorkItemID = "yoyodyne-ifd.83"
	state.ProviderSessionID = "session-developer"
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	// The provider refused the first attempt for want of capacity, the reissue
	// finished it, and only then did a review send it back for repair.
	appendCostEvents(t, store, state.RunID, 1, execution.EventRunFailed, 10.5)
	appendCostEvents(t, store, state.RunID, 2, execution.EventRunCompleted, 8.0)
	appendEvent(t, store, state.RunID, 3, execution.EventReviewStarted, nil)
	appendCostEvents(t, store, state.RunID, 4, execution.EventRunCompleted, 2.0)
	appendCostEvents(t, store, state.RunID, 5, execution.EventRunCompleted, 3.0)

	price, err := store.Price(state.WorkItemID)
	if err != nil {
		t.Fatalf("Price() error = %v", err)
	}
	phases := price.Runs[0].Phases
	if phases.Development != (PhaseCost{CostUSD: 18.5, Invocations: 2}) {
		t.Fatalf("development = %#v, want the refused attempt and its reissue", phases.Development)
	}
	if phases.Repair != (PhaseCost{CostUSD: 3.0, Invocations: 1}) {
		t.Fatalf("repair = %#v, want only the attempt the review asked for", phases.Repair)
	}
	if phases.TotalUSD() != price.TotalUSD {
		t.Fatalf("split = %v, total = %v", phases.TotalUSD(), price.TotalUSD)
	}
}

// A review the provider never answered closes with nothing at all. The bracket
// is closed by the one invocation a review makes rather than by the review
// closing, so an unanswered one cannot swallow whatever the run does next.
func TestStoreClosesAReviewBracketOnTheInvocationItMade(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusFailed)
	state.WorkItemID = "yoyodyne-ifd.83"
	state.ProviderSessionID = "session-developer"
	state.ReviewSessionID = "session-reviewer"
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	appendCostEvents(t, store, state.RunID, 1, execution.EventRunCompleted, 6.0)
	appendEvent(t, store, state.RunID, 2, execution.EventReviewStarted, nil)
	// The reviewer died without a verdict, so nothing closed the review.
	appendCostEvents(t, store, state.RunID, 3, execution.EventRunFailed, 0.5)
	appendCostEvents(t, store, state.RunID, 4, execution.EventRunCompleted, 2.5)

	price, err := store.Price(state.WorkItemID)
	if err != nil {
		t.Fatalf("Price() error = %v", err)
	}
	phases := price.Runs[0].Phases
	if phases.Review != (PhaseCost{CostUSD: 0.5, Invocations: 1}) {
		t.Fatalf("review = %#v, want only the reviewer's own invocation", phases.Review)
	}
	if phases.Repair != (PhaseCost{CostUSD: 2.5, Invocations: 1}) {
		t.Fatalf("repair = %#v, want the developer invocation after the lost review", phases.Repair)
	}
}

// What a run waited comes from its own state rather than from its event log,
// which is why it survives what the money does not: a run nothing can price
// still says how long it was held up, and by whom.
func TestStoreReportsWhatARunWaitedEvenWhenItCannotBePriced(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	priced := testState(t, StatusSucceeded)
	priced.WorkItemID = "yoyodyne-ifd.83"
	priced.ProviderSessionID = "session-developer"
	priced.UsageLimitPausedSeconds = 3600
	priced.OperatorHeldSeconds = 900
	if err := store.Create(priced); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	appendCostEvents(t, store, priced.RunID, 1, execution.EventRunCompleted, 4.0)

	lost := testState(t, StatusFailed)
	lost.RunID = mustRunID(t)
	lost.WorkItemID = priced.WorkItemID
	lost.StartedAt = priced.StartedAt.Add(time.Hour)
	lost.UpdatedAt = lost.StartedAt
	lost.ProviderSessionID = "session-developer-2"
	lost.UsageLimitPausedSeconds = 1800
	if err := store.Create(lost); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	price, err := store.Price(priced.WorkItemID)
	if err != nil {
		t.Fatalf("Price() error = %v", err)
	}
	if price.Runs[0].Phases.Waits != (Waits{UsageLimitSeconds: 3600, OperatorHoldSeconds: 900}) {
		t.Fatalf("priced run waits = %#v", price.Runs[0].Phases.Waits)
	}
	if price.Runs[1].Known() || price.Runs[1].Phases.Waits.UsageLimitSeconds != 1800 {
		t.Fatalf("unpriced run = %#v, want its wait recorded anyway", price.Runs[1])
	}
	if price.Phases.Waits.Total() != 105*time.Minute {
		t.Fatalf("item waits = %v, want every run's wait including the unpriced one", price.Phases.Waits.Total())
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
