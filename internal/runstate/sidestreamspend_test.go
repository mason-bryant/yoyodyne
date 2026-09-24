package runstate

import (
	"math"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/sidestream"
)

// A side conversation's terminals are in a log of their own, beside the
// conversation it was opened from, and nothing else prices that log. This
// records one conversation turn and one side thread of two turns beside it, and
// holds both spend readers to a total that includes the side thread: the stream
// report `yoyo status --spend` prints, and the side thread figure `yoyo cost`
// adds to its ledger. Each names the conversation the thread was opened beside,
// which is whose the money was.
func TestSpendTotalsIncludeASideStreamAttributedToItsConversation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runs, conversations, _ := streamStores(t, root)
	sides, err := NewSideStreamStore(root, "yoyodyne")
	if err != nil {
		t.Fatalf("NewSideStreamStore() error = %v", err)
	}
	store := newStreamStore(t, root)

	side := testSideStream(t)
	chat := mustConversationID(t)
	side.Conversation = chat
	today := side.OpenedAt
	if err := sides.Open(side, sidestream.DefaultMaxPerAgent); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	appendConversationEvent(t, conversations, chat, 1, execution.EventRunCompleted, today, invocationPayload(1.5, 10, 5, 0, 0))
	// The side thread resumes its own session turn after turn, so its second
	// terminal reports the running total: $0.50 and then $0.75 more. Neither names
	// a role, and both are the product manager's, whose thread the record says it
	// is.
	appendSideEvent(t, sides, side.ID, 1, today.Add(time.Minute), invocationPayload(0.5, 4, 2, 0, 0))
	appendSideEvent(t, sides, side.ID, 2, today.Add(2*time.Minute), invocationPayload(1.25, 6, 3, 0, 0))

	report, err := store.Spend(SpendQuery{Days: 7, Now: today})
	if err != nil {
		t.Fatalf("Spend() error = %v", err)
	}
	if report.Streams != 2 {
		t.Fatalf("report read %d stream(s), want the conversation and the side thread", report.Streams)
	}
	var sideRow *SpendRow
	for index := range report.Rows {
		if report.Rows[index].Kind == StreamSide {
			sideRow = &report.Rows[index]
		}
	}
	if sideRow == nil {
		t.Fatalf("rows = %+v, want one for the side thread", report.Rows)
	}
	if sideRow.StreamID != side.ID || sideRow.Conversation != chat || sideRow.Status != SideStreamOpen || sideRow.Calls != 2 || !near(sideRow.CostUSD, 1.25) {
		t.Fatalf("side row = %+v, want two invocations at $1.25 attributed to %s", *sideRow, chat)
	}
	if len(sideRow.Roles) != 1 || sideRow.Roles[0].Role != domain.RoleProductManager {
		t.Fatalf("side row roles = %+v, want the role the side thread's record names", sideRow.Roles)
	}
	totals := report.Totals()
	if totals.Calls != 3 || !near(totals.CostUSD, 2.75) {
		t.Fatalf("totals = %+v, want the conversation's $1.50 and the side thread's $1.25", totals)
	}
	var sideShare *KindTotal
	for index := range totals.ByKind {
		if totals.ByKind[index].Kind == StreamSide {
			sideShare = &totals.ByKind[index]
		}
	}
	if sideShare == nil || sideShare.Calls != 2 || !near(sideShare.CostUSD, 1.25) {
		t.Fatalf("by kind = %+v, want the side thread's own share", totals.ByKind)
	}
	narrowed, err := store.Spend(SpendQuery{Kinds: []StreamKind{StreamSide}, Now: today})
	if err != nil || narrowed.Streams != 1 || len(narrowed.Rows) != 1 {
		t.Fatalf("side threads only reported %+v, %v", narrowed, err)
	}

	listed, err := store.List(StreamQuery{Kinds: []StreamKind{StreamSide}})
	if err != nil || len(listed) != 1 || listed[0].ID != side.ID || listed[0].Conversation != chat || !listed[0].StartedAt.Equal(side.OpenedAt) {
		t.Fatalf("List() = %+v, %v, want the side thread with its conversation and opening moment", listed, err)
	}

	priced := runs.SideStreamSpend()
	if !priced.Known() || priced.Streams != 1 || priced.Invocations != 2 || !near(priced.CostUSD, 1.25) {
		t.Fatalf("SideStreamSpend() = %+v, want one side thread of two invocations at $1.25", priced)
	}
	if len(priced.Conversations) != 1 || priced.Conversations[0].Conversation != chat || !near(priced.Conversations[0].CostUSD, 1.25) {
		t.Fatalf("SideStreamSpend() conversations = %+v, want it all beside %s", priced.Conversations, chat)
	}
	if priced.Tokens.InputTokens != 10 || priced.Tokens.OutputTokens != 5 {
		t.Fatalf("SideStreamSpend() tokens = %+v, want both invocations' usage", priced.Tokens)
	}
}

// A product whose agents have never held a side thread has nothing to say about
// them, which is a different answer from a figure of nothing.
func TestSideStreamSpendIsAbsentWhereNoSideThreadWasHeld(t *testing.T) {
	t.Parallel()

	runs, _, _ := streamStores(t, t.TempDir())
	if spend := runs.SideStreamSpend(); spend.Recorded() || !spend.Known() {
		t.Fatalf("SideStreamSpend() = %+v, want nothing recorded", spend)
	}
}

func appendSideEvent(t *testing.T, store *SideStreamStore, id string, sequence uint64, at time.Time, payload any) {
	t.Helper()
	if err := store.AppendEvent(newStreamEvent(t, id, sequence, execution.EventRunCompleted, at, payload)); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
}

func near(got, want float64) bool { return math.Abs(got-want) < 1e-9 }
