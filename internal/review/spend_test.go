package review

import (
	"context"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/spend"
)

// A review's one invocation lands in the cost log, charged to the review rather
// than to the change it judged. An item reviewed four times is where that
// distinction is the whole answer.
func TestAReviewRecordsWhatItsOneInvocationSpent(t *testing.T) {
	t.Parallel()

	provider := &pricedBackend{cost: 0.96}
	log := &recordingSpendLog{}
	request := newRequest(nil)
	// The caller supplies what the harness knows and the reviewer does not, and
	// deliberately names the wrong phase here: the invocation about to be made is
	// a review, and nothing that asks for one gets to say it was anything else.
	request.Spend = reviewSpendAttribution(runstate.SpendPhaseDevelopment)

	reviewer := Reviewer{Backend: provider, Clock: reviewClock{}, Model: testReviewModel, Spend: log}
	if _, err := reviewer.Review(context.Background(), request); err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if len(log.lines) != 1 {
		t.Fatalf("recorded %d line(s), want one for the review's one invocation: %#v", len(log.lines), log.lines)
	}

	line := log.lines[0]
	if line.Phase != runstate.SpendPhaseReview {
		t.Errorf("phase = %q, want the review the invocation actually was", line.Phase)
	}
	if line.Role != domain.RoleReviewer || line.Model != testReviewModel {
		t.Errorf("line = %#v, want the reviewer's own role and selector", line)
	}
	if line.RunID != reviewRunID || line.WorkItemID != "yoyodyne-task" {
		t.Errorf("line = %#v, want the run and the item it judged", line)
	}
	if !line.Known() || line.AmountUSD != 0.96 {
		t.Errorf("line = %#v, want the provider's own figure", line)
	}
	if err := line.Validate(); err != nil {
		t.Errorf("recorded line does not satisfy the durable contract: %v", err)
	}
}

// A review the provider answered badly spent exactly what one it answered well
// would have, so it records a line too — and the amount is classified unknown
// rather than written down as nothing, because this provider never said.
//
// That is the case a recording step taken after a successful verdict would miss
// entirely, which is why the invocation and the line are one statement.
func TestAReviewTheProviderFailedStillRecordsWhatItSpent(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{isError: true, stopReason: "api_error"}
	log := &recordingSpendLog{}
	request := newRequest(nil)
	request.Spend = reviewSpendAttribution(runstate.SpendPhaseReview)

	reviewer := Reviewer{Backend: provider, Clock: reviewClock{}, Model: testReviewModel, Spend: log}
	if _, err := reviewer.Review(context.Background(), request); err == nil {
		t.Fatal("Review() read a provider failure as a verdict")
	}
	if len(log.lines) != 1 {
		t.Fatalf("recorded %d line(s), want one for the invocation that failed: %#v", len(log.lines), log.lines)
	}
	line := log.lines[0]
	if line.Known() || line.AmountUSD != 0 {
		t.Errorf("line = %#v, want an amount nobody knows", line)
	}
	if line.Unknown == "" {
		t.Error("the line does not say why nobody knows what the review cost")
	}
	if err := line.Validate(); err != nil {
		t.Errorf("recorded line does not satisfy the durable contract: %v", err)
	}
}

// reviewSpendAttribution is what a caller hands the reviewer about the
// invocation it is about to make. The phase is a parameter so a test can hand
// over the wrong one and watch the reviewer overrule it.
func reviewSpendAttribution(phase runstate.SpendPhase) spend.Attribution {
	return spend.Attribution{
		ProductID:      "yoyodyne",
		Agent:          "reviewer",
		Phase:          phase,
		AccountAlias:   "default",
		ConfigRevision: "cfg-0123456789ab",
		Backend:        domain.BackendClaudeCode,
		RunID:          reviewRunID,
		WorkItemID:     "yoyodyne-task",
	}
}

// pricedBackend is a provider that approves and reports what the invocation
// cost, which is what a real one does on its terminal.
type pricedBackend struct {
	cost float64
}

func (p *pricedBackend) Run(_ context.Context, _ backendapi.RunRequest) (backendapi.RunResult, error) {
	return backendapi.RunResult{
		Backend:       domain.BackendClaudeCode,
		SessionID:     "review-session",
		ResolvedModel: "claude-opus-5",
		FinalText:     `{"decision":"approve","summary":"fine"}`,
		CostUSD:       p.cost,
		CostReported:  true,
		Process:       execution.ProcessResult{Status: execution.ProcessSucceeded},
	}, nil
}

// recordingSpendLog is the cost log as a test reads it back.
type recordingSpendLog struct {
	lines []runstate.Spend
}

func (l *recordingSpendLog) Append(line runstate.Spend) error {
	l.lines = append(l.lines, line)
	return nil
}
