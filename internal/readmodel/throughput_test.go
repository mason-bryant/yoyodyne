package readmodel

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// fakeLedger is the spend as the test wrote it, and remembers what it was asked.
type fakeLedger struct {
	report runstate.SpendReport
	fail   error
	asked  []runstate.SpendQuery
}

func (f *fakeLedger) Spend(query runstate.SpendQuery) (runstate.SpendReport, error) {
	f.asked = append(f.asked, query)
	return f.report, f.fail
}

// noon is a local noon, so a run an hour ago and a run thirty hours ago fall on
// today and yesterday whatever timezone the test runs in.
var noon = time.Date(2026, 9, 19, 12, 0, 0, 0, time.Local)

func at(offset time.Duration) *time.Time {
	moment := noon.Add(offset)
	return &moment
}

func terminal(id string, status runstate.Status, started, completed time.Duration, integrated bool, blocker string) runstate.State {
	state := runstate.State{
		RunID:       id,
		WorkItemID:  "yoyodyne-ifd." + id,
		Status:      status,
		StartedAt:   noon.Add(started),
		CompletedAt: at(completed),
		Blocker:     blocker,
	}
	if integrated {
		state.Integration = &runstate.Integration{TargetBranch: "main", SourceCommit: "abc", TargetCommit: "def", PreviousTargetCommit: "000"}
	}
	return state
}

func window(t *testing.T, reading Throughput, label string) Window {
	t.Helper()
	for _, window := range reading.Windows {
		if window.Label == label {
			return window
		}
	}
	t.Fatalf("no %q window in %+v", label, reading.Windows)
	return Window{}
}

// The two windows count the run history's own outcomes by when each run ended,
// and a run counts as landed exactly where the terminal prints it as succeeded
// with a promotion recorded.
func TestThroughputCountsEachEndingIntoTheWindowItEndedIn(t *testing.T) {
	t.Parallel()
	recorded := []runstate.State{
		// Today: landed, succeeded without promoting, stopped, and one still running.
		terminal("a", runstate.StatusSucceeded, -3*time.Hour, -time.Hour, true, ""),
		terminal("b", runstate.StatusSucceeded, -3*time.Hour, -2*time.Hour, false, ""),
		terminal("c", runstate.StatusFailed, -4*time.Hour, -30*time.Minute, false, "the reviewer asked for repair"),
		{RunID: "d", WorkItemID: "yoyodyne-ifd.d", Status: runstate.StatusRunning, StartedAt: noon.Add(-10 * time.Minute)},
		// Yesterday: landed, cancelled, timed out, failed.
		terminal("e", runstate.StatusSucceeded, -32*time.Hour, -30*time.Hour, true, ""),
		terminal("f", runstate.StatusCancelled, -32*time.Hour, -31*time.Hour, false, ""),
		terminal("g", runstate.StatusTimedOut, -33*time.Hour, -31*time.Hour, false, ""),
		terminal("h", runstate.StatusFailed, -33*time.Hour, -31*time.Hour, false, ""),
		// Eight days ago: outside both windows however it ended.
		terminal("i", runstate.StatusSucceeded, -9*24*time.Hour, -8*24*time.Hour, true, ""),
		// Started yesterday, landed today: an ending counts where it ended, a start
		// where it started.
		terminal("j", runstate.StatusSucceeded, -26*time.Hour, -20*time.Minute, true, ""),
	}
	ledger := &fakeLedger{}
	reading := ReadThroughput(context.Background(), ThroughputSources{
		Runs:   fakeRuns{recorded: recorded},
		Ledger: ledger,
		Now:    func() time.Time { return noon },
	})
	if reading.RunsProblem != "" || reading.SpendProblem != "" {
		t.Fatalf("problems on a readable reading: %q %q", reading.RunsProblem, reading.SpendProblem)
	}
	today := window(t, reading, "today")
	if today.Days != 1 || today.Since != runstate.LocalDay(noon) {
		t.Fatalf("today's window is %+v", today)
	}
	if today.Started != 4 || today.Landed != 2 || today.Succeeded != 1 || today.Stopped != 1 || today.Cancelled+today.TimedOut+today.Failed != 0 {
		t.Fatalf("today counted %+v", today)
	}
	week := window(t, reading, "last 7 days")
	if week.Days != 7 || week.Since != runstate.LocalDay(noon.AddDate(0, 0, -6)) {
		t.Fatalf("the week's window is %+v", week)
	}
	if week.Started != 9 || week.Landed != 3 || week.Succeeded != 1 || week.Stopped != 1 || week.Cancelled != 1 || week.TimedOut != 1 || week.Failed != 1 {
		t.Fatalf("the week counted %+v", week)
	}
}

// The spend is the spend report's own rows, read once over the widest window
// and split by the day each row was spent on, so what the page says today cost
// is what `yoyo status --spend` says, and the event logs are priced once rather
// than once per window.
func TestThroughputSumsTheSpendReportOncePerReading(t *testing.T) {
	t.Parallel()
	today := runstate.LocalDay(noon)
	yesterday := runstate.LocalDay(noon.AddDate(0, 0, -1))
	ledger := &fakeLedger{report: runstate.SpendReport{
		Days:   7,
		Oldest: runstate.LocalDay(noon.AddDate(0, 0, -6)),
		Rows: []runstate.SpendRow{
			{Day: yesterday, StreamID: "run-1", Kind: runstate.StreamRun, Calls: 3, CostUSD: 10},
			{Day: yesterday, StreamID: "chat-1", Kind: runstate.StreamConversation, Calls: 2, CostUSD: 4},
			{Day: today, StreamID: "run-2", Kind: runstate.StreamRun, Calls: 2, CostUSD: 5.5},
			{Day: today, StreamID: "review-1", Kind: runstate.StreamReview, Calls: 1, CostUSD: 1.25},
			{Day: runstate.UndatedDay, StreamID: "exchange-1", Kind: runstate.StreamExchange, Calls: 1, CostUSD: 0.5},
		},
		UnreadableExchanges: []string{"exchange-broken"},
		UnreadableReason:    "unexpected end of JSON input",
	}}
	reading := ReadThroughput(context.Background(), ThroughputSources{
		Runs:   fakeRuns{},
		Ledger: ledger,
		Now:    func() time.Time { return noon },
	})
	if len(ledger.asked) != 1 || ledger.asked[0].Days != 7 || !ledger.asked[0].Now.Equal(noon) {
		t.Fatalf("the ledger was asked %+v", ledger.asked)
	}
	day := window(t, reading, "today")
	if day.CostUSD != 7.25 || day.Invocations != 4 || !day.Floor || day.Unpriced != 1 {
		t.Fatalf("today's spend %+v", day)
	}
	if len(day.Kinds) != 3 || day.Kinds[0].Kind != runstate.StreamRun || day.Kinds[0].CostUSD != 5.5 || day.Kinds[1].Kind != runstate.StreamReview || day.Kinds[2].Kind != runstate.StreamExchange {
		t.Fatalf("today's kinds %+v", day.Kinds)
	}
	week := window(t, reading, "last 7 days")
	if week.CostUSD != 21.25 || week.Invocations != 9 || !week.Floor {
		t.Fatalf("the week's spend %+v", week)
	}
	if len(week.Kinds) != 4 || week.Kinds[1].Kind != runstate.StreamConversation || week.Kinds[1].CostUSD != 4 {
		t.Fatalf("the week's kinds %+v", week.Kinds)
	}
}

// A source that cannot be read costs its own figures and says so; it never
// reports a zero, and it never costs the other source's figures.
func TestThroughputSaysWhichSourceCouldNotBeRead(t *testing.T) {
	t.Parallel()
	landed := []runstate.State{terminal("a", runstate.StatusSucceeded, -3*time.Hour, -time.Hour, true, "")}

	unpriced := ReadThroughput(context.Background(), ThroughputSources{
		Runs:   fakeRuns{recorded: landed},
		Ledger: &fakeLedger{fail: errors.New("open streams: permission denied")},
		Now:    func() time.Time { return noon },
	})
	if unpriced.SpendProblem == "" || !strings.Contains(unpriced.SpendProblem, "permission denied") || unpriced.RunsProblem != "" {
		t.Fatalf("an unreadable ledger reads as %+v", unpriced)
	}
	if window(t, unpriced, "today").Landed != 1 {
		t.Fatalf("the runs were lost with the ledger: %+v", unpriced.Windows)
	}

	uncounted := ReadThroughput(context.Background(), ThroughputSources{
		Runs:   fakeRuns{failRecorded: errors.New("scan recorded: no such directory")},
		Ledger: &fakeLedger{report: runstate.SpendReport{Rows: []runstate.SpendRow{{Day: runstate.LocalDay(noon), Kind: runstate.StreamRun, Calls: 1, CostUSD: 2}}}},
		Now:    func() time.Time { return noon },
	})
	if uncounted.RunsProblem == "" || !strings.Contains(uncounted.RunsProblem, "no such directory") || uncounted.SpendProblem != "" {
		t.Fatalf("unreadable runs read as %+v", uncounted)
	}
	if window(t, uncounted, "today").CostUSD != 2 {
		t.Fatalf("the spend was lost with the runs: %+v", uncounted.Windows)
	}

	unwired := ReadThroughput(context.Background(), ThroughputSources{Now: func() time.Time { return noon }})
	if !strings.Contains(unwired.RunsProblem, "nothing was wired") || !strings.Contains(unwired.SpendProblem, "nothing was wired") {
		t.Fatalf("an unwired reading reads as %+v", unwired)
	}

	// A source the caller could not open is named by the reason it gave, which
	// is what the page's error state has to say: what failed, not that a wire
	// was missing.
	unopened := ReadThroughput(context.Background(), ThroughputSources{
		RunsProblem:   "state root must be an absolute path",
		LedgerProblem: "open streams: permission denied",
		Now:           func() time.Time { return noon },
	})
	if unopened.RunsProblem != "the recorded runs could not be opened: state root must be an absolute path" || unopened.SpendProblem != "the spend could not be opened: open streams: permission denied" {
		t.Fatalf("an unopened reading reads as %+v", unopened)
	}
	if len(unwired.Windows) != 2 || unwired.Windows[0].Kinds == nil {
		t.Fatalf("an unwired reading still carries its windows, with empty rather than absent kinds: %+v", unwired.Windows)
	}
}

// The cost the throughput reports is the spend report's own figure — the one
// derivation `yoyo status --spend` prices from — read over a real state
// directory rather than a fake: for the same fabricated runs and conversation,
// each window's cost and invocation count equal what runstate.StreamStore.Spend
// answers for that window, and the endings equal what the run store records.
// Two surfaces pricing one week their own way is the disagreement this test
// exists to make impossible.
func TestThroughputPricesTheSameRecordsTheSpendReportPrices(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	store, err := runstate.NewStore(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	streams, err := runstate.NewStreamStore(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}
	conversations, err := runstate.NewConversationStore(root, "yoyodyne")
	if err != nil {
		t.Fatal(err)
	}

	// Three runs: succeeded today, stopped yesterday, succeeded eight days ago;
	// each priced by one invocation at its completion. None records a promotion,
	// because the store validates one against a whole review and integration
	// record; which succeeded runs count as landed is pinned above against the
	// fake, and what this pins is the pricing and the windows over real records.
	record := func(started, completed time.Time, status runstate.Status, blocker string, cost float64) {
		t.Helper()
		id, err := runstate.NewRunID()
		if err != nil {
			t.Fatal(err)
		}
		state := runstate.State{
			SchemaVersion: runstate.StateSchemaVersion,
			RunID:         id,
			ProductID:     "yoyodyne",
			RepositoryID:  "yoyodyne",
			WorkItemID:    "yoyodyne-ifd.1",
			Backend:       "claude-code",
			Status:        status,
			StartedAt:     started,
			UpdatedAt:     completed,
			CompletedAt:   &completed,
			Blocker:       blocker,
		}
		if err := store.Create(state); err != nil {
			t.Fatal(err)
		}
		for sequence, event := range []struct {
			kind    execution.EventType
			payload map[string]any
		}{
			{execution.EventRunStarted, map[string]any{"session_id": "session-developer"}},
			{execution.EventRunCompleted, map[string]any{"session_id": "session-developer", "total_cost_usd": cost,
				"usage": map[string]any{"input_tokens": 10, "output_tokens": 20, "cache_creation_input_tokens": 30, "cache_read_input_tokens": 40}}},
		} {
			recorded, err := execution.NewEvent(id, uint64(sequence+1), completed, event.kind, "claude-code", event.payload)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.AppendEvent(recorded); err != nil {
				t.Fatal(err)
			}
		}
	}
	record(noon.Add(-3*time.Hour), noon.Add(-time.Hour), runstate.StatusSucceeded, "", 3.5)
	record(noon.Add(-32*time.Hour), noon.Add(-30*time.Hour), runstate.StatusFailed, "the reviewer asked for repair", 1.25)
	record(noon.Add(-9*24*time.Hour), noon.Add(-8*24*time.Hour), runstate.StatusSucceeded, "", 40)

	// One conversation, opened a fortnight ago, with a turn yesterday and one
	// today: it spends on both days however long ago it opened.
	chatID, err := runstate.NewConversationID()
	if err != nil {
		t.Fatal(err)
	}
	opened := noon.Add(-14 * 24 * time.Hour)
	if err := conversations.Save(runstate.Conversation{
		SchemaVersion: runstate.ConversationSchemaVersion, ConversationID: chatID, ProductID: "yoyodyne", RepositoryID: "yoyodyne",
		Role: "product-manager", Backend: "claude-code", ProviderModel: "opus", Turns: 2, StartedAt: opened, UpdatedAt: noon.Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	for sequence, turn := range []struct {
		at   time.Time
		kind execution.EventType
		cost float64
	}{{opened, execution.EventRunStarted, 0}, {noon.Add(-26 * time.Hour), execution.EventRunCompleted, 2}, {noon.Add(-time.Minute), execution.EventRunCompleted, 0.75}} {
		payload := map[string]any{"session_id": "session-chat"}
		if turn.kind == execution.EventRunCompleted {
			payload["total_cost_usd"] = turn.cost
		}
		event, err := execution.NewEvent(chatID, uint64(sequence+1), turn.at, turn.kind, "claude-code", payload)
		if err != nil {
			t.Fatal(err)
		}
		if err := conversations.AppendEvent(event); err != nil {
			t.Fatal(err)
		}
	}

	reading := ReadThroughput(context.Background(), ThroughputSources{Runs: store, Ledger: streams, Now: func() time.Time { return noon }})
	if reading.RunsProblem != "" || reading.SpendProblem != "" {
		t.Fatalf("problems over a readable state directory: %q %q", reading.RunsProblem, reading.SpendProblem)
	}
	for _, days := range []int{1, 7} {
		report, err := streams.Spend(runstate.SpendQuery{Days: days, Now: noon})
		if err != nil {
			t.Fatal(err)
		}
		var cost float64
		var calls int
		for _, row := range report.Rows {
			cost += row.CostUSD
			calls += row.Calls
		}
		got := window(t, reading, map[int]string{1: "today", 7: "last 7 days"}[days])
		if got.CostUSD != cost || got.Invocations != calls || got.Since != report.Oldest {
			t.Fatalf("%d-day window %+v, but the spend report prices $%.2f from %d calls since %s", days, got, cost, calls, report.Oldest)
		}
	}
	today := window(t, reading, "today")
	week := window(t, reading, "last 7 days")
	if today.CostUSD != 4.25 || today.Invocations != 2 || week.CostUSD != 7.5 || week.Invocations != 4 {
		t.Fatalf("today %+v, week %+v: the fabricated spend is $3.50 and $0.75 today, and $1.25 and $2.00 more yesterday", today, week)
	}
	if today.Succeeded != 1 || today.Stopped != 0 || week.Succeeded != 1 || week.Stopped != 1 || week.Started != 2 || week.Landed != 0 {
		t.Fatalf("today %+v, week %+v: one succeeded today, one stopped yesterday, and the eight-day-old run is outside both", today, week)
	}
}
