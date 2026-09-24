package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sidestream"
)

// One recorded side thread is in both totals an operator reads: `yoyo status
// --spend` prices it as its own kind with the conversation it was opened beside
// on its row, and `yoyo cost` carries it into the ledger total on a row of its
// own with that conversation named under the table.
func TestSpendAndCostTotalsIncludeARecordedSideThread(t *testing.T) {
	// Not parallel: the state root the command addresses is set here.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	today := time.Now()
	chatID := recordStreamConversation(t, stateRoot, today.Add(-time.Hour), []time.Time{today.Add(-time.Hour)})
	sideID := recordSideThread(t, stateRoot, chatID, today.Add(-30*time.Minute), 0.5)

	stdout, stderr, code := runCLI(t, "status", "--spend", "--config", configPath)
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{
		"cost: $1.50",
		"conversations: $1.00 from 1 turn(s)   side threads: $0.50 from 1 invocation(s)",
		"open, beside " + chatID,
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stdout = %q, want it to contain %q", stdout, want)
		}
	}
	if !strings.Contains(ledgerLine(stdout, sideID), "beside "+chatID) {
		t.Fatalf("stdout = %q, want the side thread's row to name its conversation", stdout)
	}
	stdout, _, code = runCLI(t, "status", "--spend", "--kind", "sides", "--config", configPath)
	if code != 0 || !strings.Contains(stdout, "cost: $0.50") || strings.Contains(stdout, chatID+" ") {
		t.Fatalf("side threads only reported code = %d, stdout = %q", code, stdout)
	}

	stdout, stderr, code = runCLI(t, "cost", "--config", configPath)
	if code != 0 {
		t.Fatalf("cost code = %d, stderr = %q", code, stderr)
	}
	if row := ledgerLine(stdout, sideLedgerLabel); !strings.Contains(row, "$0.50") {
		t.Fatalf("cost = %q, want a side thread row at $0.50", stdout)
	}
	if total := ledgerLine(stdout, "TOTAL"); !strings.Contains(total, "$0.50") {
		t.Fatalf("cost = %q, want the side thread in the total", stdout)
	}
	if !strings.Contains(stdout, chatID+"  $0.50 from 1 side conversation(s) over 1 invocation(s)") {
		t.Fatalf("cost = %q, want the side thread's cost named against %s", stdout, chatID)
	}
}

// recordSideThread writes one open side thread beside a conversation, with one
// completed invocation costing what it is given.
func recordSideThread(t *testing.T, stateRoot, conversation string, openedAt time.Time, cost float64) string {
	t.Helper()
	store, err := runstate.NewSideStreamStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewSideStreamStore() error = %v", err)
	}
	id, err := sidestream.NewID()
	if err != nil {
		t.Fatalf("NewID() error = %v", err)
	}
	stream := sidestream.Stream{
		SchemaVersion: sidestream.SchemaVersion,
		ID:            id,
		ProductID:     "yoyodyne",
		Agent:         "product-manager",
		Role:          domain.RoleProductManager,
		Conversation:  conversation,
		Topic:         "whether this is priced",
		MaxTurns:      sidestream.DefaultMaxTurns,
		OpenedAt:      openedAt,
		UpdatedAt:     openedAt,
	}
	if err := store.Open(stream, sidestream.DefaultMaxPerAgent); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := store.AppendEvent(newStreamEvent(t, id, 1, execution.EventRunCompleted, openedAt, streamCost(cost))); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
	return id
}
