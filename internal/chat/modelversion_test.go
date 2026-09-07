package chat

import (
	"context"
	"testing"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// A conversation whose agent pinned a version asks for that version, and the
// evidence says so: the selector the record names is what was actually asked
// for, not the family alias behind it.
func TestAPinnedConversationAsksForTheVersionItNamed(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{results: []backendapi.RunResult{
		{SessionID: "session-1", FinalText: "Repair it once more.", ResolvedModel: "claude-opus-5-20260401"},
	}}
	options := testOptions(t, provider)
	options.Model = "opus"
	options.ModelVersion = "claude-opus-5-20260401"
	options.UsageLimits = newTestUsageLimits(t)

	reply, err := openTestSession(t, options).Send(context.Background(), "what is next?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(provider.requests) != 1 || provider.requests[0].Model != "claude-opus-5-20260401" {
		t.Fatalf("invocations = %#v, want one, under the pinned version", provider.requests)
	}
	// Nothing was substituted, so nothing says one was: a pin the provider has is
	// the ordinary case and reads exactly like an unpinned turn does.
	if evidence := reply.Evidence; evidence.RequestedModel != "claude-opus-5-20260401" || evidence.ServedModel != "" {
		t.Fatalf("evidence = %#v, want the pin as the requested selector and no substitution", evidence)
	}
}

// And the turn this exists for: the provider has not got the pinned version, the
// family alias answers, and both the evidence and the durable record say which
// model was asked for and which one served.
func TestAPinnedVersionTheProviderHasNotGotIsServedByTheFamily(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{results: []backendapi.RunResult{
		{
			IsError:          true,
			StopReason:       "api_error",
			ModelUnavailable: &backendapi.ModelUnavailable{Detail: "api_error: API Error: 404 model: claude-opus-5-20260401"},
		},
		{SessionID: "session-1", FinalText: "Repair it once more."},
	}}
	limits := newTestUsageLimits(t)
	options := testOptions(t, provider)
	options.Model = "opus"
	options.ModelVersion = "claude-opus-5-20260401"
	options.UsageLimits = limits
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "what is next?")
	if err != nil {
		t.Fatalf("Send() error = %v, want the turn served by the family alias", err)
	}
	if reply.Text == "" {
		t.Fatal("reply text is empty, want the answer the family alias gave")
	}
	if len(provider.requests) != 2 {
		t.Fatalf("invocations = %d, want the refused version and the alias that served", len(provider.requests))
	}
	if provider.requests[0].Model != "claude-opus-5-20260401" || provider.requests[1].Model != "opus" {
		t.Fatalf("models asked = %q then %q, want the pin first and the family alias second",
			provider.requests[0].Model, provider.requests[1].Model)
	}
	if evidence := reply.Evidence; evidence.RequestedModel != "claude-opus-5-20260401" || evidence.ServedModel != "opus" {
		t.Fatalf("evidence = %#v, want the pin asked for and the alias that answered", evidence)
	}
	if reply.FailoverProblem != "" {
		t.Fatalf("failover problem = %q, want none", reply.FailoverProblem)
	}

	recorded, err := limits.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 1 {
		t.Fatalf("List() = %#v, want the fallback recorded once", recorded)
	}
	entry := recorded[0]
	if entry.Model != "claude-opus-5-20260401" || entry.ServedBy != "opus" {
		t.Fatalf("recorded = %#v, want the pin and the model that actually served", entry)
	}
	if entry.Reason() != runstate.SubstitutedForAvailability {
		t.Fatalf("recorded reason = %q, want the record to say it was availability rather than capacity", entry.Reason())
	}
	if entry.ConversationID != session.Evidence().ConversationID {
		t.Fatalf("conversation = %q, want the way back to the record", entry.ConversationID)
	}
}

// A conversation whose agent pinned nothing behaves exactly as it did before
// this existed. That is the whole of what makes the pin optional.
func TestAConversationWithNoPinIsUnchanged(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{results: []backendapi.RunResult{{SessionID: "session-1", FinalText: "Repair it once more."}}}
	options := testOptions(t, provider)
	options.Model = "opus"
	options.UsageLimits = newTestUsageLimits(t)

	reply, err := openTestSession(t, options).Send(context.Background(), "what is next?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(provider.requests) != 1 || provider.requests[0].Model != "opus" {
		t.Fatalf("invocations = %#v, want one, under the configured alias", provider.requests)
	}
	if evidence := reply.Evidence; evidence.RequestedModel != "opus" || evidence.ServedModel != "" {
		t.Fatalf("evidence = %#v, want the configured alias and no substitution", evidence)
	}
}

// Each attempt is priced against the model that attempt asked for, so a turn the
// family alias served is not billed to the version that would not serve it.
func TestAFallenBackTurnIsPricedAgainstEachModelItAsked(t *testing.T) {
	t.Parallel()

	log := &collectingSpend{}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{IsError: true, StopReason: "api_error", ModelUnavailable: &backendapi.ModelUnavailable{Detail: "no such model"}},
		{SessionID: "session-1", FinalText: "Repair it once more.", CostUSD: 0.25, CostReported: true},
	}})
	options.Model = "opus"
	options.ModelVersion = "claude-opus-5-20260401"
	options.UsageLimits = newTestUsageLimits(t)
	options.Spend = log
	if _, err := openTestSession(t, options).Send(context.Background(), "what is next?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(log.lines) != 2 {
		t.Fatalf("cost lines = %#v, want one for the refused version and one for the served turn", log.lines)
	}
	if log.lines[0].Model != "claude-opus-5-20260401" || log.lines[1].Model != "opus" {
		t.Fatalf("cost lines asked %q then %q, want each attempt priced against the model it asked for",
			log.lines[0].Model, log.lines[1].Model)
	}
}
