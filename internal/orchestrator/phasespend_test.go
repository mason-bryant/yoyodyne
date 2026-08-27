package orchestrator

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The phase split is read from the log a real run leaves behind, so what it is
// worth depends on that log rather than on one a test wrote by hand. A run that
// developed, was sent back, and was approved on the second look is the shape
// every longer run is made of, and each of its invocations has to land in the
// part of the work it actually served.
func TestEveryPricedInvocationOfARunAttributesToThePhaseThatSpentIt(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := pricingBackend(0, []float64{12.0, 4.0}, 1.0, repairVerdict, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	price, err := store.Price(tracker.item.ID)
	if err != nil {
		t.Fatalf("Price() error = %v", err)
	}
	phases := price.Runs[0].Phases
	if phases.Development != (runstate.PhaseCost{CostUSD: 12.0, Invocations: 1}) {
		t.Fatalf("development = %#v, want the first developer attempt alone", phases.Development)
	}
	if phases.Review != (runstate.PhaseCost{CostUSD: 2.0, Invocations: 2}) {
		t.Fatalf("review = %#v, want both reviewer invocations", phases.Review)
	}
	if phases.Repair != (runstate.PhaseCost{CostUSD: 4.0, Invocations: 1}) {
		t.Fatalf("repair = %#v, want the attempt the findings bought", phases.Repair)
	}
	// Nothing the run spent may sit outside the three. A figure here is an
	// invocation that reached the log without saying what it was for, which is
	// the pollution this attribution exists to make visible instead of quietly
	// charging to repair.
	if phases.Unattributed != (runstate.PhaseCost{}) {
		t.Fatalf("unattributed = %#v, want every invocation of a real run placed", phases.Unattributed)
	}
	if phases.TotalUSD() != price.Runs[0].CostUSD || phases.Invocations() != price.Runs[0].Invocations {
		t.Fatalf("split = %v across %d, run = %v across %d",
			phases.TotalUSD(), phases.Invocations(), price.Runs[0].CostUSD, price.Runs[0].Invocations)
	}
	if outcome.RepairAttempts != phases.Repair.Invocations {
		t.Fatalf("run made %d repair attempt(s), repair column holds %d", outcome.RepairAttempts, phases.Repair.Invocations)
	}
}

// The split charges a developer invocation to the attempt it belongs to by
// reading how the one before it ended: an attempt that reached a terminal of its
// own is over, so the next developer invocation is a new attempt. That is exact
// only because the develop loop never hands a repair attempt to a developer
// whose previous invocation ended in a failure — it reissues into the same
// attempt instead — and nothing but this states that. A loop that ever did
// otherwise would move the reissued invocation's money into repair on the very
// runs the phase figures are read from, and every synthetic-log test of the
// split would still pass, because the log would be exactly the one the split
// reads correctly.
func TestAFailedDeveloperInvocationIsReissuedIntoItsOwnAttempt(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	// The provider drops the first invocation mid-response, having already spent
	// what it spent, and the run is relaunched into the same attempt. Only the
	// review that follows buys a repair.
	provider := pricingBackend(1, []float64{1.5, 12.0, 4.0}, 1.0, repairVerdict, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline.Config.Execution.TransientRelaunchesBeforeBlocking = 2

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.TransientRelaunches != 1 || outcome.RepairAttempts != 1 {
		t.Fatalf("run made %d relaunch(es) and %d repair attempt(s), want one of each",
			outcome.TransientRelaunches, outcome.RepairAttempts)
	}
	price, err := store.Price(tracker.item.ID)
	if err != nil {
		t.Fatalf("Price() error = %v", err)
	}
	phases := price.Runs[0].Phases
	// The dead invocation and the one that replaced it are one attempt: what the
	// change cost is what it took to get it made.
	if phases.Development != (runstate.PhaseCost{CostUSD: 13.5, Invocations: 2}) {
		t.Fatalf("development = %#v, want the failed invocation and its reissue", phases.Development)
	}
	if phases.Repair != (runstate.PhaseCost{CostUSD: 4.0, Invocations: 1}) {
		t.Fatalf("repair = %#v, want only the attempt the findings bought", phases.Repair)
	}
	// The property said plainly: the run made one repair attempt, so exactly one
	// developer invocation is repair. A loop that answered a failed invocation
	// with a fresh attempt would put two here while the run still reported one.
	if phases.Repair.Invocations != outcome.RepairAttempts {
		t.Fatalf("repair holds %d invocation(s), run made %d repair attempt(s)",
			phases.Repair.Invocations, outcome.RepairAttempts)
	}
	if phases.Unattributed != (runstate.PhaseCost{}) {
		t.Fatalf("unattributed = %#v, want every invocation placed", phases.Unattributed)
	}
}

// pricingBackend serves the developer and the reviewer the way roleBackend does
// and, unlike it, records each invocation the way the provider adapter does: one
// terminal event carrying the role the invocation was made as and the figure the
// provider reported for it. That is what makes an assertion about the phase
// split an assertion about the pipeline rather than about a log a test wrote.
//
// deaths is how many of the developer's first invocations the provider drops
// mid-response, which is the death the harness answers by reissuing into the
// same attempt. developerCosts is what each developer invocation costs in turn,
// the last repeating, and reviewerCost is what each review costs.
func pricingBackend(deaths int, developerCosts []float64, reviewerCost float64, verdicts ...string) *fakeBackend {
	provider := &fakeBackend{developerSession: "developer-session", reviewerSession: "reviewer-session"}
	developed, reviews, died := 0, 0, 0
	provider.run = func(request backend.RunRequest) (backend.RunResult, error) {
		switch request.Role {
		case domain.RoleDeveloper:
			cost := developerCosts[min(developed, len(developerCosts)-1)]
			developed++
			if died < deaths {
				died++
				last, err := emitTerminal(request, domain.RoleDeveloper, cost, true)
				if err != nil {
					return backend.RunResult{}, err
				}
				return backend.RunResult{
					Backend:          domain.BackendClaudeCode,
					SessionID:        provider.developerSession,
					IsError:          true,
					StopReason:       "api_error",
					FinalText:        connectionClosedMessage,
					TransientFailure: &backend.TransientFailure{Detail: "api_error: " + connectionClosedMessage},
					Process:          execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1},
					LastEvent:        last,
				}, nil
			}
			if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600); err != nil {
				return backend.RunResult{}, err
			}
			last, err := emitTerminal(request, domain.RoleDeveloper, cost, false)
			if err != nil {
				return backend.RunResult{}, err
			}
			return backend.RunResult{
				Backend:       domain.BackendClaudeCode,
				SessionID:     provider.developerSession,
				ResolvedModel: developerResolved,
				FinalText:     "implemented the work item",
				Process:       execution.ProcessResult{Status: execution.ProcessSucceeded},
				LastEvent:     last,
			}, nil
		case domain.RoleReviewer:
			verdict := verdicts[len(verdicts)-1]
			if reviews < len(verdicts) {
				verdict = verdicts[reviews]
			}
			reviews++
			last, err := emitTerminal(request, domain.RoleReviewer, reviewerCost, false)
			if err != nil {
				return backend.RunResult{}, err
			}
			return backend.RunResult{
				Backend:       domain.BackendClaudeCode,
				SessionID:     provider.reviewerSession,
				ResolvedModel: reviewerResolved,
				FinalText:     verdict,
				Process:       execution.ProcessResult{Status: execution.ProcessSucceeded},
				LastEvent:     last,
			}, nil
		default:
			return backend.RunResult{}, fmt.Errorf("unexpected role %q", request.Role)
		}
	}
	return provider
}

// emitTerminal records one invocation's terminal into the run's log the way the
// Claude Code adapter does: the role it was made as beside what it cost, so the
// record says whose money this was rather than leaving it to be inferred from
// where the terminal happens to sit.
func emitTerminal(request backend.RunRequest, role domain.AgentRole, cost float64, failed bool) (uint64, error) {
	eventType := execution.EventRunCompleted
	if failed {
		eventType = execution.EventRunFailed
	}
	sequence := execution.NewSequence(request.LastSequence)
	event, err := execution.NewEvent(request.RunID, sequence.Next(), time.Now(), eventType, "claude-code", map[string]any{
		"role":           string(role),
		"is_error":       failed,
		"total_cost_usd": cost,
	})
	if err != nil {
		return 0, err
	}
	if err := request.EventSink(event); err != nil {
		return 0, err
	}
	return sequence.Last(), nil
}
