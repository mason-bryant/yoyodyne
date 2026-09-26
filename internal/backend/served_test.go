package backend

import (
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// Only a result the provider genuinely served is evidence a window is open:
// every other ending, including an answer with a refusal reported beside it,
// is not.
func TestOnlyAGenuinelyServedResultIsServedCleanly(t *testing.T) {
	t.Parallel()

	succeeded := execution.ProcessResult{Status: execution.ProcessSucceeded}
	for _, test := range []struct {
		name   string
		result RunResult
		served bool
	}{
		{"served", RunResult{FinalText: "done", Process: succeeded}, true},
		{"ended in error with nothing classified", RunResult{IsError: true, StopReason: "api_error", Process: succeeded}, false},
		{"process failed", RunResult{Process: execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1}}, false},
		{"process stalled", RunResult{Process: execution.ProcessResult{Status: execution.ProcessStalled}}, false},
		{"no process recorded", RunResult{FinalText: "done"}, false},
		{"limit beside an answer", RunResult{Process: succeeded, UsageLimit: &UsageLimit{Kind: "seven_day"}}, false},
		{"overload beside an answer", RunResult{Process: succeeded, ServerOverload: &ServerOverload{}}, false},
		{"outage beside an answer", RunResult{Process: succeeded, ProviderOutage: &ProviderOutage{}}, false},
	} {
		if got := test.result.ServedCleanly(); got != test.served {
			t.Errorf("%s: ServedCleanly() = %v, want %v", test.name, got, test.served)
		}
	}
}
