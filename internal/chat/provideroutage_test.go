package chat

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func newTestOutages(t *testing.T) *runstate.ProviderOutageStore {
	t.Helper()
	store, err := runstate.NewProviderOutageStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewProviderOutageStore() error = %v", err)
	}
	return store
}

// A turn refused for a usage limit records the account and model it was refused
// on and records nothing served; the next turn the provider serves records that
// account and model as served, which is what reads the refusal as lifted before
// the reset it quoted.
func TestAServedTurnRecordsTheAccountAndModelItWasServedOn(t *testing.T) {
	t.Parallel()

	resetsAt := time.Date(2026, 9, 27, 3, 0, 0, 0, time.UTC)
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{IsError: true, UsageLimit: &backendapi.UsageLimit{Kind: "seven_day", ResetsAt: resetsAt}},
		{SessionID: "session-1", FinalText: "Here it is.", Process: execution.ProcessResult{Status: execution.ProcessSucceeded}},
	}})
	limits, err := runstate.NewUsageLimitStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewUsageLimitStore() error = %v", err)
	}
	served, err := runstate.NewCapacityServedStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewCapacityServedStore() error = %v", err)
	}
	options.UsageLimits = limits
	options.CapacityServed = served
	session := openTestSession(t, options)

	if _, err := session.Send(context.Background(), "what is next?"); !errors.Is(err, ErrProviderCapacity) {
		t.Fatalf("Send() error = %v, want the turn refused for capacity", err)
	}
	refusals, err := limits.List()
	if err != nil || len(refusals) != 1 {
		t.Fatalf("refusals = %+v, %v; want the one refusal", refusals, err)
	}
	if refusals[0].Model != "opus" || refusals[0].AccountAlias != options.AccountAlias {
		t.Fatalf("refusal = %+v, want it to name model opus and account %q", refusals[0], options.AccountAlias)
	}
	if listed, err := served.List(); err != nil || len(listed) != 0 {
		t.Fatalf("served after a refused turn = %+v, %v; want nothing recorded as served", listed, err)
	}

	if _, err := session.Send(context.Background(), "and now?"); err != nil {
		t.Fatalf("Send() after the refusal error = %v", err)
	}
	listed, err := served.List()
	if err != nil || len(listed) != 1 {
		t.Fatalf("served = %+v, %v; want the served turn recorded", listed, err)
	}
	// The test's clock does not move between the two turns, so the pair is held
	// to the key Lifts matches on rather than to the moment.
	if listed[0].Model != refusals[0].Model || listed[0].AccountAlias != refusals[0].AccountAlias {
		t.Fatalf("served = %+v, want the account and model the refusal named (%q, %q)", listed[0], refusals[0].AccountAlias, refusals[0].Model)
	}
}

// A turn that ended in error with no limit classified on it — a terminal
// api_error the dialect could not name, which may be a limit the provider is
// enforcing — is not a served turn and records nothing, so it lifts no refusal.
func TestATurnEndingInErrorWithNoLimitRecordsNothingServed(t *testing.T) {
	t.Parallel()

	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{{
		IsError:    true,
		StopReason: "api_error",
		FinalText:  "API Error: 429 rate_limit_error",
		Process:    execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1},
	}}})
	served, err := runstate.NewCapacityServedStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewCapacityServedStore() error = %v", err)
	}
	options.CapacityServed = served
	session := openTestSession(t, options)

	_, _ = session.Send(context.Background(), "what is next?")
	if listed, err := served.List(); err != nil || len(listed) != 0 {
		t.Fatalf("served after a turn that ended in error = %+v, %v; want nothing recorded", listed, err)
	}
}

// A turn the provider refused because nobody is logged into it records the
// outage on the product and fails naming the wait, so a caller that is not a
// person — a scheduled firing — can tell the role was never asked; and the first
// turn the provider serves afterwards ends the outage for every surface.
func TestATurnIntoAnExpiredLoginRecordsTheOutageAndAServedTurnClearsIt(t *testing.T) {
	t.Parallel()

	outages := newTestOutages(t)
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{
			IsError:        true,
			StopReason:     "api_error",
			FinalText:      "Not logged in",
			ProviderOutage: &backendapi.ProviderOutage{Cause: domain.ProviderUnauthenticated, Detail: "api_error: Not logged in"},
		},
		{SessionID: "session-1", FinalText: "Here it is."},
	}})
	options.ProviderOutages = outages
	session := openTestSession(t, options)

	_, err := session.Send(context.Background(), "what is next?")
	if !errors.Is(err, ErrProviderAway) {
		t.Fatalf("Send() error = %v, want the turn failed as one the provider turned away", err)
	}
	if !strings.Contains(err.Error(), "the operator must log in") {
		t.Fatalf("Send() error = %v, want the wait named", err)
	}
	standing, away, readErr := outages.Standing()
	if readErr != nil || !away || standing.Cause != domain.ProviderUnauthenticated {
		t.Fatalf("Standing() = %#v, %t, %v, want the login recorded on the product", standing, away, readErr)
	}
	if !strings.Contains(standing.Waiting, "Lead Product Manager") || !strings.Contains(standing.Waiting, session.Evidence().ConversationID) {
		t.Fatalf("waiting = %q, want the conversation that was stopped", standing.Waiting)
	}

	if _, err := session.Send(context.Background(), "and now?"); err != nil {
		t.Fatalf("Send() after the login error = %v", err)
	}
	if _, away, err := outages.Standing(); err != nil || away {
		t.Fatalf("Standing() after a served turn = %t, %v, want the outage cleared", away, err)
	}
}
