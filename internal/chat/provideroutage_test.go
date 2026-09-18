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

func newTestOutages(t *testing.T) *runstate.ProviderOutageStore {
	t.Helper()
	store, err := runstate.NewProviderOutageStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewProviderOutageStore() error = %v", err)
	}
	return store
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
	if !strings.Contains(standing.Waiting, "product manager") || !strings.Contains(standing.Waiting, session.Evidence().ConversationID) {
		t.Fatalf("waiting = %q, want the conversation that was stopped", standing.Waiting)
	}

	if _, err := session.Send(context.Background(), "and now?"); err != nil {
		t.Fatalf("Send() after the login error = %v", err)
	}
	if _, away, err := outages.Standing(); err != nil || away {
		t.Fatalf("Standing() after a served turn = %t, %v, want the outage cleared", away, err)
	}
}
