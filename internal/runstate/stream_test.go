package runstate

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// The question an operator asks of a live machine is the same for all three
// kinds — is this alive, what is it doing — so the listing answers it for all
// three at once and says which kind each one is. What each is doing comes from a
// different place for each: a run keeps its own status, a conversation's is
// derived from whether the role is still in it, and a review's from whether its
// verdict has been made.
func TestStreamStoreListsAllThreeKindsNewestFirst(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runs, conversations, reviews := streamStores(t, root)
	store := newStreamStore(t, root)

	state := testState(t, StatusRunning)
	if err := runs.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	appendStreamEvent(t, runs, state.RunID, 1, execution.EventRunStarted, streamMoment, nil)

	current := testConversation(t)
	current.Turns = 1
	current.ProviderModel = "opus"
	if err := conversations.Save(current); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	// The role is in this one and its last turn closed, so it is waiting on the
	// operator rather than being answered.
	appendConversationEvent(t, conversations, current.ConversationID, 1, execution.EventRunStarted, streamMoment, nil)
	appendConversationEvent(t, conversations, current.ConversationID, 2, execution.EventRunCompleted, streamMoment.Add(time.Minute), nil)
	// This one the role has since replaced, so nothing will happen in it again.
	replaced := mustConversationID(t)
	appendConversationEvent(t, conversations, replaced, 1, execution.EventRunStarted, streamMoment, nil)

	reviewID := mustBranchReviewID(t)
	appendReviewEvent(t, reviews, reviewID, 1, execution.EventReviewStarted, streamMoment, nil)

	// Newest means the stream something happened in most recently, which is what
	// an operator who named nothing meant, so the order is asserted against logs
	// whose last append is known rather than against whatever the filesystem
	// happened to record microseconds apart.
	touch(t, reviews.Root(), reviewID, streamMoment.Add(4*time.Hour))
	touch(t, conversations.Root(), current.ConversationID, streamMoment.Add(3*time.Hour))
	touch(t, runs.Root(), state.RunID, streamMoment.Add(2*time.Hour))
	touch(t, conversations.Root(), replaced, streamMoment.Add(time.Hour))

	listed, err := store.List(StreamQuery{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	for index, want := range []string{reviewID, current.ConversationID, state.RunID, replaced} {
		if index < len(listed) && listed[index].ID != want {
			t.Fatalf("stream %d is %s, want %s: the listing is not newest first", index, listed[index].ID, want)
		}
	}
	if len(listed) != 4 {
		t.Fatalf("listed %d stream(s), want the run, both conversations, and the review: %+v", len(listed), listed)
	}
	status := map[string]Stream{}
	for _, stream := range listed {
		status[stream.ID] = stream
	}
	for _, want := range []struct {
		id     string
		kind   StreamKind
		status string
	}{
		{state.RunID, StreamRun, string(StatusRunning)},
		{current.ConversationID, StreamConversation, ConversationWaiting},
		{replaced, StreamConversation, ConversationEnded},
		{reviewID, StreamReview, ReviewInProgress},
	} {
		got, found := status[want.id]
		if !found {
			t.Fatalf("%s was not listed", want.id)
		}
		if got.Kind != want.kind || got.Status != want.status {
			t.Fatalf("%s listed as %s/%s, want %s/%s", want.id, got.Kind, got.Status, want.kind, want.status)
		}
	}
	// A run's opening moment is the one its own record keeps; the two kinds that
	// keep no record of their own take it from their first event, which is the
	// same moment.
	if !status[state.RunID].StartedAt.Equal(state.StartedAt) {
		t.Fatalf("the run opened at %v, want its recorded %v", status[state.RunID].StartedAt, state.StartedAt)
	}
	if !status[reviewID].StartedAt.Equal(streamMoment) {
		t.Fatalf("the review opened at %v, want %v", status[reviewID].StartedAt, streamMoment)
	}

	// Narrowing to one kind is what `--kind` asks for, and a query that names an
	// id prefix is how one stream is named without typing all of it.
	onlyRuns, err := store.List(StreamQuery{Kinds: []StreamKind{StreamRun}})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(onlyRuns) != 1 || onlyRuns[0].ID != state.RunID {
		t.Fatalf("runs only listed %+v", onlyRuns)
	}
	found, matched, err := store.Find(StreamQuery{Match: state.RunID[4:12]})
	if err != nil || !matched {
		t.Fatalf("Find() = %v, %v, %v", found, matched, err)
	}
	if found.ID != state.RunID {
		t.Fatalf("an id prefix found %s, want %s", found.ID, state.RunID)
	}
	if _, matched, err := store.Find(StreamQuery{Match: "nothing-is-named-this"}); err != nil || matched {
		t.Fatalf("Find() on an unmatched pattern = %v, %v, want no match and no failure", matched, err)
	}
}

// A conversation whose record will not load is exactly the state an operator is
// reaching for this listing to diagnose, so it must not be the thing that makes
// the listing refuse to answer. Which conversation a role is in is read as the
// one field that says so.
func TestStreamStoreListsPastAnUnloadableConversationRecord(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	_, conversations, _ := streamStores(t, root)
	store := newStreamStore(t, root)

	id := mustConversationID(t)
	appendConversationEvent(t, conversations, id, 1, execution.EventRunStarted, streamMoment, nil)
	// A record from a harness this one does not understand: it names its
	// conversation and nothing else here can be trusted.
	record := filepath.Join(conversations.Root(), "product-manager.json")
	if err := os.WriteFile(record, []byte(`{"schema_version":99,"conversation_id":"`+id+`"}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	listed, err := store.List(StreamQuery{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed) != 1 || listed[0].Status != ConversationAnswering {
		t.Fatalf("listed %+v, want the conversation still reported as being answered", listed)
	}
}

// What an operator budgets against is what today cost, so spend is grouped by
// the local day the money was spent on rather than by the log it was recorded
// in. A conversation open for days spends on each of them and contributes a row
// to each.
func TestStreamStoreSpendsByTheDayTheMoneyWasSpent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	_, conversations, _ := streamStores(t, root)
	store := newStreamStore(t, root)

	// Anchored at local noon so no invocation below can be pushed into a
	// neighbouring day by the timezone the test happens to run in.
	today := time.Date(2026, 8, 23, 12, 0, 0, 0, time.Local)
	id := mustConversationID(t)
	appendConversationEvent(t, conversations, id, 1, execution.EventRunStarted, today.AddDate(0, 0, -14), nil)
	appendConversationEvent(t, conversations, id, 2, execution.EventRunCompleted, today.AddDate(0, 0, -14), invocationPayload(1.5, 10, 20, 30, 40))
	appendConversationEvent(t, conversations, id, 3, execution.EventRunCompleted, today, invocationPayload(2.5, 1, 2, 3, 4))
	// A turn the provider ended in an error cost money like any other, so it is
	// priced rather than left out of the total it belongs in.
	appendConversationEvent(t, conversations, id, 4, execution.EventRunFailed, today, invocationPayload(0.25, 5, 5, 5, 5))

	report, err := store.Spend(SpendQuery{Now: today})
	if err != nil {
		t.Fatalf("Spend() error = %v", err)
	}
	if len(report.Rows) != 2 {
		t.Fatalf("report has %d row(s), want one per day the conversation spent on: %+v", len(report.Rows), report.Rows)
	}
	if report.Rows[0].Day != LocalDay(today.AddDate(0, 0, -14)) || report.Rows[1].Day != LocalDay(today) {
		t.Fatalf("rows are %s then %s, want the older day first", report.Rows[0].Day, report.Rows[1].Day)
	}
	if report.Rows[1].Calls != 2 || report.Rows[1].CostUSD != 2.75 {
		t.Fatalf("today's row = %+v, want both of today's invocations priced", report.Rows[1])
	}
	if report.Rows[1].Usage != (TokenUsage{Input: 6, Output: 7, CacheCreation: 8, CacheRead: 9}) {
		t.Fatalf("today's usage = %+v", report.Rows[1].Usage)
	}
	// The row for the day the work opened says when it opened; a later day says
	// when it next spent, because there is nothing else that column could mean.
	if !report.Rows[0].At.Equal(today.AddDate(0, 0, -14)) || !report.Rows[1].At.Equal(today) {
		t.Fatalf("row moments = %v, %v", report.Rows[0].At, report.Rows[1].At)
	}

	// A window is about when the money was spent, so a stream that opened before
	// it still reports what it spent inside it — and only that.
	windowed, err := store.Spend(SpendQuery{Days: 7, Now: today})
	if err != nil {
		t.Fatalf("Spend() error = %v", err)
	}
	if len(windowed.Rows) != 1 || windowed.Rows[0].Day != LocalDay(today) {
		t.Fatalf("a seven day window reported %+v, want only today's spend", windowed.Rows)
	}
	if windowed.Oldest != LocalDay(today.AddDate(0, 0, -6)) {
		t.Fatalf("window reaches back to %s, want today and the six days before it", windowed.Oldest)
	}
	// Naming a stream prices it whatever day it ran on: the window is for a
	// report that has to choose what to show, and an id has already chosen.
	named, err := store.Spend(SpendQuery{Match: id[5:13], Days: 7, Now: today})
	if err != nil {
		t.Fatalf("Spend() error = %v", err)
	}
	if len(named.Rows) != 1 {
		t.Fatalf("naming a stream inside a window reported %+v", named.Rows)
	}
}

// Something whose moment cannot be read still cost money, so it is reported
// rather than dropped: it has no day to be outside of, so no window excludes it.
func TestStreamStoreReportsUndatedSpendUnderEveryWindow(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	_, conversations, _ := streamStores(t, root)
	store := newStreamStore(t, root)

	id := mustConversationID(t)
	appendConversationEvent(t, conversations, id, 1, execution.EventRunCompleted, streamMoment, invocationPayload(3, 1, 1, 1, 1))
	// An event log line the harness can read enough of to price but not enough
	// to date is what an undated row is.
	log := filepath.Join(conversations.Root(), id+".events.jsonl")
	if err := os.WriteFile(log, []byte(`{"type":"run.completed","payload":{"total_cost_usd":3}}`+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	report, err := store.Spend(SpendQuery{Days: 1, Now: streamMoment.AddDate(0, 0, 400)})
	if err != nil {
		t.Fatalf("Spend() error = %v", err)
	}
	if len(report.Rows) != 1 || report.Rows[0].Day != UndatedDay || report.Rows[0].CostUSD != 3 {
		t.Fatalf("report = %+v, want the undated spend reported anyway", report.Rows)
	}
}

// streamMoment is when the fabricated streams here happened. It is fixed so
// every assertion about ordering and dating is a fact about the code rather than
// about when the test ran.
var streamMoment = time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)

// touch says when a stream's log was last appended to, which is the only thing
// deciding which of two streams is newer.
func touch(t *testing.T, root, id string, at time.Time) {
	t.Helper()
	path := filepath.Join(root, id+".events.jsonl")
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatalf("Chtimes(%s) error = %v", path, err)
	}
}

func newStreamStore(t *testing.T, root string) *StreamStore {
	t.Helper()
	store, err := NewStreamStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStreamStore() error = %v", err)
	}
	return store
}

// streamStores are the three stores that own the streams, all under one root, so
// what the reader sees is what the writers actually wrote.
func streamStores(t *testing.T, root string) (*Store, *ConversationStore, *BranchReviewStore) {
	t.Helper()
	runs, err := NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	reviews, err := NewBranchReviewStore(root, domain.ProductID("yoyodyne"))
	if err != nil {
		t.Fatalf("NewBranchReviewStore() error = %v", err)
	}
	return runs, newConversationStore(t, root), reviews
}

func appendStreamEvent(t *testing.T, store *Store, runID string, sequence uint64, eventType execution.EventType, at time.Time, payload any) {
	t.Helper()
	if err := store.AppendEvent(newStreamEvent(t, runID, sequence, eventType, at, payload)); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
}

func appendConversationEvent(t *testing.T, store *ConversationStore, id string, sequence uint64, eventType execution.EventType, at time.Time, payload any) {
	t.Helper()
	if err := store.AppendEvent(newStreamEvent(t, id, sequence, eventType, at, payload)); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
}

func appendReviewEvent(t *testing.T, store *BranchReviewStore, id string, sequence uint64, eventType execution.EventType, at time.Time, payload any) {
	t.Helper()
	if err := store.AppendEvent(newStreamEvent(t, id, sequence, eventType, at, payload)); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
}

func newStreamEvent(t *testing.T, id string, sequence uint64, eventType execution.EventType, at time.Time, payload any) execution.Event {
	t.Helper()
	if payload == nil {
		payload = map[string]any{"session_id": "session-stream"}
	}
	event, err := execution.NewEvent(id, sequence, at, eventType, "claude-code", payload)
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}
	return event
}

// invocationPayload is what the provider reports when an invocation ends, which
// is the only place the cost and the token counts are ever written down.
func invocationPayload(cost float64, input, output, cacheWrite, cacheRead int64) map[string]any {
	return map[string]any{
		"session_id":     "session-stream",
		"total_cost_usd": cost,
		"usage": map[string]any{
			"input_tokens":                input,
			"output_tokens":               output,
			"cache_creation_input_tokens": cacheWrite,
			"cache_read_input_tokens":     cacheRead,
		},
	}
}

func mustConversationID(t *testing.T) string {
	t.Helper()
	id, err := NewConversationID()
	if err != nil {
		t.Fatalf("NewConversationID() error = %v", err)
	}
	return id
}

func mustBranchReviewID(t *testing.T) string {
	t.Helper()
	id, err := NewBranchReviewID()
	if err != nil {
		t.Fatalf("NewBranchReviewID() error = %v", err)
	}
	return id
}
