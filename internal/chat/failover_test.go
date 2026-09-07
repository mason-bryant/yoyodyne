package chat

import (
	"context"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The morning this exists for, replayed at the seam it actually stopped: the
// model the deciders run on has no capacity, the alternate the operator
// permitted takes the turn, and the decision that unblocks the queue is made
// rather than waited for.
func TestATurnRefusedForCapacityIsServedByThePermittedAlternate(t *testing.T) {
	t.Parallel()

	resetsAt := time.Date(2026, 9, 7, 11, 0, 0, 0, time.UTC)
	provider := &fakeBackend{results: []backendapi.RunResult{
		{
			IsError:    true,
			StopReason: "usage_limit",
			UsageLimit: &backendapi.UsageLimit{Kind: "five_hour", ResetsAt: resetsAt},
		},
		{SessionID: "session-1", FinalText: "Repair it once more."},
	}}
	limits := newTestUsageLimits(t)
	options := testOptions(t, provider)
	options.Model = "fable"
	options.FailoverModel = "opus"
	options.UsageLimits = limits
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "what is next?")
	if err != nil {
		t.Fatalf("Send() error = %v, want the turn served by the alternate", err)
	}
	if reply.Text == "" {
		t.Fatal("reply text is empty, want the answer the alternate gave")
	}
	if len(provider.requests) != 2 {
		t.Fatalf("invocations = %d, want the refused one and the one that served", len(provider.requests))
	}
	if provider.requests[0].Model != "fable" || provider.requests[1].Model != "opus" {
		t.Fatalf("models asked = %q then %q, want the configured model first and the alternate second",
			provider.requests[0].Model, provider.requests[1].Model)
	}
	// What the operator is shown says the alternate answered, because the
	// configured selector alone would read as a conversation held on a model that
	// in fact refused every turn of it.
	if evidence := reply.Evidence; evidence.RequestedModel != "fable" || evidence.ServedModel != "opus" {
		t.Fatalf("evidence = %#v, want the configured model and the one that served", evidence)
	}
	if reply.FailoverProblem != "" {
		t.Fatalf("failover problem = %q, want none", reply.FailoverProblem)
	}

	recorded, err := limits.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 1 {
		t.Fatalf("List() = %#v, want the substitution recorded once", recorded)
	}
	if recorded[0].Model != "fable" || recorded[0].ServedBy != "opus" {
		t.Fatalf("recorded = %#v, want which model was refused and which served", recorded[0])
	}
	if recorded[0].ConversationID != session.Evidence().ConversationID {
		t.Fatalf("conversation = %q, want the way back to the record", recorded[0].ConversationID)
	}
}

// An agent that has not enabled failover behaves exactly as it did before: the
// refused turn fails, the refusal is recorded as a stoppage, and nothing asks a
// second model on the operator's money.
func TestAConversationWithoutFailoverStillFailsARefusedTurn(t *testing.T) {
	t.Parallel()

	provider := &fakeBackend{results: []backendapi.RunResult{{
		IsError:    true,
		StopReason: "usage_limit",
		UsageLimit: &backendapi.UsageLimit{Kind: "five_hour"},
	}}}
	limits := newTestUsageLimits(t)
	options := testOptions(t, provider)
	options.Model = "fable"
	options.UsageLimits = limits
	if _, err := openTestSession(t, options).Send(context.Background(), "what is next?"); err == nil {
		t.Fatal("Send() error = nil, want the refused turn still failed")
	}
	if len(provider.requests) != 1 {
		t.Fatalf("invocations = %d, want exactly the one the conversation was configured for", len(provider.requests))
	}
	recorded, err := limits.List()
	if err != nil || len(recorded) != 1 {
		t.Fatalf("List() = %#v, error %v, want the refusal recorded as the stoppage it was", recorded, err)
	}
	if recorded[0].Substituted() {
		t.Fatalf("recorded = %#v, want a stoppage rather than a substitution", recorded[0])
	}
}

// A conversation whose configured model is refused and whose alternate is
// refused too is the ordinary stoppage: the turn fails, and it is recorded once
// rather than twice.
func TestAnAlternateThatIsAlsoRefusedFailsTheTurnOnce(t *testing.T) {
	t.Parallel()

	refusal := backendapi.RunResult{
		IsError:    true,
		StopReason: "usage_limit",
		UsageLimit: &backendapi.UsageLimit{Kind: "five_hour"},
	}
	limits := newTestUsageLimits(t)
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{refusal, refusal}})
	options.Model = "fable"
	options.FailoverModel = "opus"
	options.UsageLimits = limits
	if _, err := openTestSession(t, options).Send(context.Background(), "what is next?"); err == nil {
		t.Fatal("Send() error = nil, want the turn failed once nothing would serve it")
	}
	recorded, err := limits.List()
	if err != nil || len(recorded) != 1 {
		t.Fatalf("List() = %#v, error %v, want the stoppage recorded once", recorded, err)
	}
	if recorded[0].Substituted() {
		t.Fatalf("recorded = %#v, want a stoppage rather than a substitution nothing served", recorded[0])
	}
}

// The record says which model served the turn rather than which one was
// configured, so a conversation resumed by another process is not described as
// being held on a model that refused it.
func TestTheConversationRecordSaysWhichModelServed(t *testing.T) {
	t.Parallel()

	limits := newTestUsageLimits(t)
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{
			IsError:    true,
			StopReason: "usage_limit",
			UsageLimit: &backendapi.UsageLimit{Kind: "five_hour"},
		},
		{SessionID: "session-1", FinalText: "Repair it once more."},
	}})
	options.Model = "fable"
	options.FailoverModel = "opus"
	options.UsageLimits = limits
	session := openTestSession(t, options)
	if _, err := session.Send(context.Background(), "what is next?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	recorded, err := options.Store.Load(runstate.ConversationIdentity{Agent: options.Agent, Role: options.Role})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if recorded.ProviderModel != "opus" {
		t.Fatalf("recorded model = %q, want the model that actually served the turn", recorded.ProviderModel)
	}
}
