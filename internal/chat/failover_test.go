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

// Each attempt is priced against the model that attempt actually asked for. That
// is what putting the failover outside the cost meter buys, and it is the whole
// of the claim docs/configuration.md makes about what a substitution costs: a
// turn the alternate served must not be billed to the model that refused it.
func TestEachAttemptIsPricedAgainstTheModelItAskedFor(t *testing.T) {
	t.Parallel()

	log := &collectingSpend{}
	options := testOptions(t, &fakeBackend{results: []backendapi.RunResult{
		{
			IsError:    true,
			StopReason: "usage_limit",
			UsageLimit: &backendapi.UsageLimit{Kind: "five_hour"},
		},
		{SessionID: "session-1", FinalText: "Repair it once more.", CostUSD: 0.25, CostReported: true},
	}})
	options.Model = "fable"
	options.FailoverModel = "opus"
	options.UsageLimits = newTestUsageLimits(t)
	options.Spend = log
	if _, err := openTestSession(t, options).Send(context.Background(), "what is next?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	// Two lines rather than one: the refused attempt was charged for exactly as
	// the one that answered was, and each says which model it asked.
	if len(log.lines) != 2 {
		t.Fatalf("cost lines = %#v, want one for the refused attempt and one for the served turn", log.lines)
	}
	if log.lines[0].Model != "fable" {
		t.Fatalf("the refused attempt is priced against %q, want the configured model that refused it", log.lines[0].Model)
	}
	if log.lines[1].Model != "opus" {
		t.Fatalf("the served turn is priced against %q, want the alternate that actually answered it", log.lines[1].Model)
	}
	if reply := log.lines[1]; reply.AmountUSD != 0.25 {
		t.Fatalf("the served turn cost %v, want what the provider reported for the invocation the alternate made", reply.AmountUSD)
	}
}

// collectingSpend is the cost log as an assertion: every line the turn wrote, in
// the order it wrote them.
type collectingSpend struct {
	lines []runstate.Spend
}

func (c *collectingSpend) Append(line runstate.Spend) error {
	c.lines = append(c.lines, line)
	return nil
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

// The substitution check applies to every conversation the harness opens,
// because every one of them resolves the endpoint it is held on. That is what
// Open's own refusals amount to: a provider this project names, an account
// alias that is one, and a model selector are the whole of what an endpoint
// needs, and each is refused where the conversation is opened.
func TestAnOpenedConversationAlwaysResolvesItsEndpoint(t *testing.T) {
	t.Parallel()

	options := testOptions(t, &fakeBackend{})
	options.Model = "fable"
	options.FailoverModel = "opus"
	session := openTestSession(t, options)

	endpoint, resolved := session.options.endpoint()
	if !resolved {
		t.Fatal("an opened conversation could not say which endpoint it is held on")
	}
	if endpoint.Provider != options.Provider || endpoint.AccountAlias != options.AccountAlias || endpoint.Model != "fable" {
		t.Fatalf("endpoint = %s, want the provider, account, and model the conversation was opened on", endpoint)
	}
	policy := session.failoverPolicy()
	if policy.Eligibility == nil || !policy.Endpoint.Same(endpoint) || policy.Role != options.Role {
		t.Fatalf("policy = %#v, want the substitution checked against this conversation's endpoint and role", policy)
	}
	if session.failoverProblem != "" {
		t.Fatalf("failover problem = %q, want none for a conversation whose endpoint resolved", session.failoverProblem)
	}
}

// A conversation that cannot say which endpoint it is on substitutes as it did
// before the check existed, and says so. Refusing the turn instead would trade
// an answer the operator wants for a check that can only fail on the provider,
// and a check quietly not made is the one path where the guarantee silently
// does not hold — so the skip is on the reply rather than in nobody's hands.
func TestAConversationThatCannotResolveItsEndpointSaysTheCheckWasNotMade(t *testing.T) {
	t.Parallel()

	options := testOptions(t, &fakeBackend{})
	options.Model = "fable"
	options.FailoverModel = "opus"
	session := openTestSession(t, options)
	// The one field an opened conversation cannot lose by itself, taken away
	// afterwards: what is under test is what the policy does when the four cannot
	// be assembled, not a conversation the harness could open this way.
	session.options.AccountAlias = ""

	policy := session.failoverPolicy()
	if policy.Eligibility != nil || policy.Endpoint.Provider != "" {
		t.Fatalf("policy = %#v, want no endpoint to check against", policy)
	}
	if policy.Alternate != "opus" {
		t.Fatalf("policy alternate = %q, want the turn still served as it was before the check existed", policy.Alternate)
	}
	if session.failoverProblem == "" {
		t.Fatal("the substitution check was skipped and nothing said so")
	}
}
