package chat

import (
	"context"
	"errors"
	"strings"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// The twelve abandoned escalations of yoyodyne-ifd.250 all carried this one
// sentence, and nothing in it said the harness had killed its own turn. A turn a
// cancellation ended before an answer existed now says so, so the caller that
// delivers stoppages can stop spending an attempt on the harness's own shutdown.
func TestACancelledTurnSaysItEndedBeforeAnAnswerExisted(t *testing.T) {
	t.Parallel()

	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{{
		IsError:    true,
		StopReason: string(execution.ProcessCancelled),
		Process:    execution.ProcessResult{Status: execution.ProcessCancelled, ExitCode: -1},
	}}})
	options.Role = domain.RoleDevelopmentManager
	options.Agent = string(domain.RoleDevelopmentManager)

	_, err := openTestSession(t, options).Send(context.Background(), "what becomes of this stoppage?")
	if !errors.Is(err, ErrTurnAbandoned) {
		t.Fatalf("Send() error = %v, want the turn reported as cancelled before she answered", err)
	}
	// And what a person reads is the sentence those twelve records carried: the
	// sentinel is joined to the failure rather than replacing it.
	if !strings.Contains(err.Error(), "development manager reported failure: cancelled") {
		t.Fatalf("Send() error = %q, want the failure those records carried still in it", err)
	}
}

// A cancellation that landed after her answer arrived is not a turn that
// produced nothing: whatever became of the process, the provider ended the
// invocation and said what it served, what it cost, and what she wrote. Reading
// that as a turn nobody took is how one answer gets asked for twice.
func TestACancelledTurnThatAnsweredIsNotAbandoned(t *testing.T) {
	t.Parallel()

	answered := []backendapi.RunResult{
		{
			IsError:    true,
			StopReason: string(execution.ProcessCancelled),
			SessionID:  "session-1",
			Process:    execution.ProcessResult{Status: execution.ProcessCancelled, ExitCode: -1},
		},
		{
			IsError:    true,
			StopReason: string(execution.ProcessCancelled),
			FinalText:  "Re-run it.",
			Process:    execution.ProcessResult{Status: execution.ProcessCancelled, ExitCode: -1},
		},
		{
			IsError:      true,
			StopReason:   string(execution.ProcessCancelled),
			CostReported: true,
			Process:      execution.ProcessResult{Status: execution.ProcessCancelled, ExitCode: -1},
		},
	}
	for _, result := range answered {
		_, err := openTestSession(t, testOptions(t, &fakeBackend{results: []backendapi.RunResult{result}})).
			Send(context.Background(), "what becomes of this stoppage?")
		if err == nil || errors.Is(err, ErrTurnAbandoned) {
			t.Fatalf("Send() error = %v, want a cancellation after her answer left as a turn she took", err)
		}
	}

	// And a failure that is not a cancellation at all is untouched by any of this.
	other := &fakeBackend{results: []backendapi.RunResult{{IsError: true, StopReason: "max_turns"}}}
	_, err := openTestSession(t, testOptions(t, other)).Send(context.Background(), "what becomes of this stoppage?")
	if err == nil || errors.Is(err, ErrTurnAbandoned) {
		t.Fatalf("Send() error = %v, want a turn she answered badly reported as one", err)
	}
}
