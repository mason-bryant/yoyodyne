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

	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{killedTurn()}})
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

// killedTurn is what internal/backend/claudecode assembles from a stream a
// process-group teardown cut off mid-turn, asserted field by field there in
// TestRunLeavesAKilledTurnCarryingNoAnswer. It is written out here rather than
// simplified, because the whole question this file decides is which of these
// fields prove the turn reached no terminal — and a fixture that dropped the
// session identifier would be a test agreeing with the code about the one thing
// they must not agree about by construction.
func killedTurn() backendapi.RunResult {
	return backendapi.RunResult{
		IsError:    true,
		StopReason: string(execution.ProcessCancelled),
		// Written by the init event, before the role has done anything.
		SessionID: "session-1",
		// And nothing a terminal writes: no FinalText, and CostReported false.
		Process: execution.ProcessResult{Status: execution.ProcessCancelled, ExitCode: -1},
	}
}

// A cancellation that landed after her answer arrived is not a turn that
// produced nothing: whatever became of the process, the provider reached its
// terminal and said what she wrote and what it charged. Reading that as a turn
// nobody took is how one answer gets asked for twice.
func TestACancelledTurnThatAnsweredIsNotAbandoned(t *testing.T) {
	t.Parallel()

	answeredText := killedTurn()
	answeredText.FinalText = "Re-run it."
	pricedTurn := killedTurn()
	pricedTurn.CostReported = true

	for _, result := range []backendapi.RunResult{answeredText, pricedTurn} {
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
