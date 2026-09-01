package chat

import (
	"context"
	"errors"
	"strings"
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

// A turn the cost log will not take still answers. The provider has already
// written the answer and already charged for it, so failing the turn over the
// bookkeeping behind it would cost the operator both; the answer comes back and
// what is missing from the log is named beside it.
//
// This is the one place the harness makes that trade. A run takes the failure,
// because its answer is a change in a worktree the next attempt starts from.
func TestATurnWhoseSpendCannotBeRecordedStillAnswers(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", ResolvedModel: "claude-opus-5", FinalText: "The brief is thin on goals.", CostUSD: 0.0125, CostReported: true},
	}}
	log := &recordingSpendLog{failure: errors.New("the disk is full")}
	options := testOptions(t, provider)
	options.Spend = log
	options.AccountAlias = "default"
	options.ConfigRevision = "cfg-0123456789ab"
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "What is missing from the brief?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if !strings.Contains(reply.Text, "thin on goals") {
		t.Fatalf("the answer was dropped with the record: %q", reply.Text)
	}
	if !strings.Contains(reply.SpendProblem, "the disk is full") {
		t.Fatalf("SpendProblem = %q, want what stopped the line being kept", reply.SpendProblem)
	}
	// The line was produced and offered; what failed is keeping it. A turn that
	// recorded nothing at all would be a different defect and this would not
	// tell the two apart.
	if len(log.lines) != 1 {
		t.Fatalf("offered %d line(s), want the turn's one", len(log.lines))
	}
}

// recordingSpendLog is the cost log as a test reads it back, and one that
// refuses everything when a failure is set.
type recordingSpendLog struct {
	lines   []runstate.Spend
	failure error
}

func (l *recordingSpendLog) Append(line runstate.Spend) error {
	l.lines = append(l.lines, line)
	return l.failure
}

// What a turn cost is handed back to whoever asked for it. The harness takes
// turns nobody is sitting in front of now — a stopped run put to the development
// manager — and a `yoyo work` session given a budget counts what it spent doing
// that, so an accessor that under-reported would be the operator's cap
// disappearing quietly.
func TestATurnReportsWhatItCost(t *testing.T) {
	t.Parallel()

	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: "Noted.", CostUSD: 0.02, CostReported: true},
		{SessionID: "session-1", IsError: true, StopReason: "max_turns", CostUSD: 0.03, CostReported: true},
	}})
	session := openTestSession(t, options)

	if _, err := session.Send(context.Background(), "what is next?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if session.TurnCostUSD() != 0.02 {
		t.Fatalf("TurnCostUSD() = %v, want what the provider charged for the turn", session.TurnCostUSD())
	}

	// A turn that failed was charged for exactly as one that answered, and says
	// so: a caller that counted only successful turns would spend past a bound on
	// the failures.
	if _, err := session.Send(context.Background(), "and now?"); err == nil {
		t.Fatal("Send() error = nil, want the failed turn still failed")
	}
	if session.TurnCostUSD() != 0.03 {
		t.Fatalf("TurnCostUSD() = %v, want what the failed turn cost rather than the turn before it", session.TurnCostUSD())
	}
}
