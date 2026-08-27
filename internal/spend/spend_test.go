package spend

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func TestEveryInvocationLandsInTheLogExactlyOnce(t *testing.T) {
	t.Parallel()

	log := &recordingLog{}
	metered := testMetered(log, func(backend.RunRequest) (backend.RunResult, error) {
		return backend.RunResult{
			Backend:       "claude-code",
			SessionID:     "session-1",
			ResolvedModel: "claude-opus-4-1-20250805",
			CostUSD:       4.25,
			CostReported:  true,
		}, nil
	})
	result, err := metered.Run(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.CostUSD != 4.25 {
		t.Fatalf("the invocation's own result did not come back: %#v", result)
	}
	if len(log.lines) != 1 {
		t.Fatalf("recorded %d line(s), want exactly one", len(log.lines))
	}

	line := log.lines[0]
	if !line.Known() || line.AmountUSD != 4.25 {
		t.Fatalf("the provider's own figure was not recorded: %#v", line)
	}
	// The role and the requested model come off the request and the resolved model
	// off the result, so no caller gets to assert either.
	if line.Role != "developer" || line.Model != "opus" || line.ResolvedModel != "claude-opus-4-1-20250805" {
		t.Fatalf("what served the invocation was not recorded: %#v", line)
	}
	if line.Phase != runstate.SpendPhaseDevelopment || line.RunID != "run-0123456789abcdef0123456789abcdef" {
		t.Fatalf("the attribution was not carried: %#v", line)
	}
	if line.AccountAlias != "default" || line.ConfigRevision != "cfg-0123456789ab" {
		t.Fatalf("the account and the configuration were not carried: %#v", line)
	}
	if err := line.Validate(); err != nil {
		t.Fatalf("the recorded line does not satisfy the durable contract: %v", err)
	}
}

func TestAnInvocationTheProviderDidNotPriceIsRecordedAsUnknown(t *testing.T) {
	t.Parallel()

	// The provider answered and said nothing about the cost. A zero here would be
	// added up as an invocation that was free, which is the opposite of the truth.
	answered := &recordingLog{}
	metered := testMetered(answered, func(backend.RunRequest) (backend.RunResult, error) {
		return backend.RunResult{Backend: "claude-code", IsError: true, StopReason: "api_error"}, nil
	})
	if _, err := metered.Run(context.Background(), testRequest()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(answered.lines) != 1 || answered.lines[0].Known() || answered.lines[0].AmountUSD != 0 {
		t.Fatalf("recorded = %#v, want one unknown spend", answered.lines)
	}
	if !strings.Contains(answered.lines[0].Unknown, "without reporting what it cost") {
		t.Fatalf("the line does not say why nobody knows: %q", answered.lines[0].Unknown)
	}

	// And the invocation that died before the provider reported anything: still a
	// line, and it names what killed it, because that is what somebody
	// reconciling a bill has to know.
	died := &recordingLog{}
	metered = testMetered(died, func(backend.RunRequest) (backend.RunResult, error) {
		return backend.RunResult{}, errors.New("Claude Code stream ended without a result event")
	})
	if _, err := metered.Run(context.Background(), testRequest()); err == nil {
		t.Fatal("Run() hid the invocation's own failure")
	}
	if len(died.lines) != 1 || died.lines[0].Known() {
		t.Fatalf("recorded = %#v, want one unknown spend", died.lines)
	}
	if !strings.Contains(died.lines[0].Unknown, "stream ended without a result event") {
		t.Fatalf("the line does not carry what killed the invocation: %q", died.lines[0].Unknown)
	}
	// A result that never arrived names no backend, so the configured one is what
	// the line is pinned to rather than nothing at all.
	if died.lines[0].Backend != "claude-code" {
		t.Fatalf("backend = %q, want the configured one", died.lines[0].Backend)
	}
}

// A line the log will not take is reported rather than swallowed, which is a
// deliberate trade: an invocation the provider already served and charged for
// comes back as a failure. It is the same weight the harness gives an event it
// could not record, and the alternative is an operator's cost log quietly
// missing lines nothing says are missing.
func TestALineThatCannotBeMadeDurableIsReportedRatherThanLost(t *testing.T) {
	t.Parallel()

	log := &recordingLog{failure: errors.New("the disk is full")}
	metered := testMetered(log, func(backend.RunRequest) (backend.RunResult, error) {
		return backend.RunResult{Backend: "claude-code", CostUSD: 1, CostReported: true}, nil
	})
	result, err := metered.Run(context.Background(), testRequest())
	if err == nil || !strings.Contains(err.Error(), "the disk is full") {
		t.Fatalf("Run() error = %v, want the failure to record", err)
	}
	// The invocation's own result still comes back: the money was spent and what
	// the provider said about it is not lost because the log could not take it.
	if result.CostUSD != 1 {
		t.Fatalf("the result was dropped with the record: %#v", result)
	}

	// The invocation's own failure is not replaced by the failure to record it.
	// They are two things wrong, and a caller deciding what to do about the run
	// needs the first.
	metered = testMetered(log, func(backend.RunRequest) (backend.RunResult, error) {
		return backend.RunResult{Backend: "claude-code"}, errors.New("developer backend failed")
	})
	_, err = metered.Run(context.Background(), testRequest())
	if err == nil || !strings.Contains(err.Error(), "developer backend failed") || !strings.Contains(err.Error(), "the disk is full") {
		t.Fatalf("Run() error = %v, want both failures", err)
	}
}

func TestAProviderWithNowhereToRecordSpendsExactlyAsItWould(t *testing.T) {
	t.Parallel()

	invoked := 0
	metered := testMetered(nil, func(backend.RunRequest) (backend.RunResult, error) {
		invoked++
		return backend.RunResult{Backend: "claude-code", CostUSD: 2, CostReported: true}, nil
	})
	result, err := metered.Run(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if invoked != 1 || result.CostUSD != 2 {
		t.Fatalf("the invocation did not happen unchanged: invoked %d, result %#v", invoked, result)
	}
}

func testMetered(log Log, run func(backend.RunRequest) (backend.RunResult, error)) Metered {
	return Metered{
		Provider: providerFunc(run),
		Log:      log,
		Attribution: Attribution{
			ProductID:      "yoyodyne",
			Agent:          "developer",
			Phase:          runstate.SpendPhaseDevelopment,
			AccountAlias:   "default",
			ConfigRevision: "cfg-0123456789ab",
			Backend:        "claude-code",
			RunID:          "run-0123456789abcdef0123456789abcdef",
			WorkItemID:     "yoyodyne-ifd.182",
		},
		Clock: fixedClock{},
	}
}

func testRequest() backend.RunRequest {
	return backend.RunRequest{
		RunID: "run-0123456789abcdef0123456789abcdef",
		Role:  "developer",
		Model: "opus",
	}
}

type providerFunc func(backend.RunRequest) (backend.RunResult, error)

func (f providerFunc) Run(_ context.Context, request backend.RunRequest) (backend.RunResult, error) {
	return f(request)
}

// recordingLog is a cost log that keeps what it was given, and one that refuses
// everything when a failure is set.
type recordingLog struct {
	lines   []runstate.Spend
	failure error
}

func (l *recordingLog) Append(line runstate.Spend) error {
	l.lines = append(l.lines, line)
	return l.failure
}

type fixedClock struct{}

func (fixedClock) Now() time.Time { return time.Date(2026, 8, 23, 9, 30, 0, 0, time.UTC) }
