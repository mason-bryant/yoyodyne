package chat

import (
	"context"
	"errors"
	"math"
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

	// The second turn resumes the session the first opened, so the provider's
	// $0.02 is what the conversation has cost since it began rather than what the
	// turn cost. What is recorded is the $0.0075 it added.
	for index, want := range []float64{0.0125, 0.0075} {
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
			t.Errorf("lines[%d] = %#v, want what the turn itself cost, %v", index, line, want)
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

// ReportedSessionTotal answers from the lines this log has already taken, the
// way the durable store answers from the lines it has already written.
func (l *recordingSpendLog) ReportedSessionTotal(sessionID string) (float64, bool, error) {
	total, found := 0.0, false
	for _, line := range l.lines {
		if line.SessionID == sessionID && line.Known() {
			total, found = line.ReportedTotal(), true
		}
	}
	return total, found, nil
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

// What a turn cost is the same number the cost log holds, and on a conversation
// that is not what the provider reported. Every turn resumes one session, so the
// provider's figure is what the conversation has cost since it opened; counting
// that as the turn's made a budgeted `yoyo work` session read the whole
// conversation against its cap on every pass, and a recurring task record the
// same.
func TestATurnCostsWhatItAddedToTheConversationRatherThanTheWholeConversation(t *testing.T) {
	t.Parallel()

	// $0.02 for the first turn and $0.03 for the second, as a resumed session
	// reports them: 0.02, then 0.05.
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: "Noted.", CostUSD: 0.02, CostReported: true},
		{SessionID: "session-1", FinalText: "Also noted.", CostUSD: 0.05, CostReported: true},
	}})
	log := &recordingSpendLog{}
	options.Spend = log
	options.AccountAlias = "default"
	options.ConfigRevision = "cfg-0123456789ab"
	session := openTestSession(t, options)

	for _, message := range []string{"what is next?", "and then?"} {
		if _, err := session.Send(context.Background(), message); err != nil {
			t.Fatalf("Send(%q) error = %v", message, err)
		}
	}
	// The figures are differences between floats, so they are compared to within
	// a hundredth of a cent rather than exactly: what is being asserted is which
	// number this is, not that subtraction is exact.
	near := func(got, want float64) bool { return math.Abs(got-want) < 1e-9 }
	if !near(session.TurnCostUSD(), 0.03) {
		t.Fatalf("TurnCostUSD() = %v, want the $0.03 this turn added rather than the session's $0.05",
			session.TurnCostUSD())
	}
	// And the conversation's own running figure is the session's final total
	// rather than a total of totals, which is what the operator is shown.
	if !near(session.sessionCostUSD, 0.05) {
		t.Fatalf("sessionCostUSD = %v, want the session's final total of 0.05", session.sessionCostUSD)
	}
	// One number, not two: what is shown and what is recorded come from the line.
	var recorded float64
	for _, line := range log.lines {
		recorded += line.AmountUSD
	}
	if !near(recorded, session.sessionCostUSD) {
		t.Fatalf("the log holds %v and the conversation shows %v", recorded, session.sessionCostUSD)
	}
}
