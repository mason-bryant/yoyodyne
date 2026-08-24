package chat

import (
	"context"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// Every turn of a conversation lands in the cost log, charged to the
// conversation and to no work item. A conversation that discussed five items is
// not attributable to any one of them, and the record says so by naming the
// conversation instead of guessing.
func TestEveryTurnRecordsWhatItSpentAgainstTheConversation(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", ResolvedModel: "claude-opus-5", FinalText: "The brief is thin on goals.", CostUSD: 0.0125, CostReported: true},
		{SessionID: "session-1", ResolvedModel: "claude-opus-5", FinalText: "Two goals, then.", CostUSD: 0.02, CostReported: true},
	}}
	log := &recordingSpendLog{}
	options := testOptions(t, provider)
	options.Spend = log
	options.AccountAlias = "default"
	options.ConfigRevision = "cfg-0123456789ab"
	session := openTestSession(t, options)

	for _, message := range []string{"What is missing from the brief?", "Name two, then."} {
		if _, err := session.Send(context.Background(), message); err != nil {
			t.Fatalf("Send(%q) error = %v", message, err)
		}
	}
	if len(log.lines) != 2 {
		t.Fatalf("recorded %d line(s), want one per turn: %#v", len(log.lines), log.lines)
	}

	for index, want := range []float64{0.0125, 0.02} {
		line := log.lines[index]
		if line.Phase != runstate.SpendPhaseConversation {
			t.Errorf("lines[%d].Phase = %q, want a conversation turn", index, line.Phase)
		}
		if line.ConversationID != session.state.ConversationID {
			t.Errorf("lines[%d] = %#v, want this conversation", index, line)
		}
		// A turn belongs to no run and serves no assigned work, so it names
		// neither rather than naming whichever item happened to come up.
		if line.RunID != "" || line.WorkItemID != "" {
			t.Errorf("lines[%d] = %#v, want nothing but the conversation", index, line)
		}
		if line.Role != domain.RoleProductManager || line.Agent != string(domain.RoleProductManager) {
			t.Errorf("lines[%d] = %#v, want the role answering and the agent filling it", index, line)
		}
		if line.Backend != domain.BackendClaudeCode || line.Model != options.Model {
			t.Errorf("lines[%d] = %#v, want what served the turn", index, line)
		}
		if !line.Known() || line.AmountUSD != want {
			t.Errorf("lines[%d] = %#v, want the provider's own figure %v", index, line, want)
		}
		if err := line.Validate(); err != nil {
			t.Errorf("lines[%d] does not satisfy the durable contract: %v", index, err)
		}
	}
}

// recordingSpendLog is the cost log as a test reads it back.
type recordingSpendLog struct {
	lines []runstate.Spend
}

func (l *recordingSpendLog) Append(line runstate.Spend) error {
	l.lines = append(l.lines, line)
	return nil
}
