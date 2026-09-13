package chat

// A conversation carrying on when the provider holding it will not take a turn.
//
// These exercise the rebuild rather than asserting it. The second provider has
// never seen this conversation and holds no session for it, so what it is handed
// is entirely what the harness wrote down — and the way to know the durable
// record is sufficient is to read what actually reached the provider, which is
// what these do.

import (
	"context"
	"strings"
	"testing"
	"time"

	backendapi "github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// The turn this slice exists for. The provider the conversation is held on has
// no capacity; the permitted alternate is on another provider; the turn is served
// there, from the conversation's own durable record rather than from a session
// the second provider has never held.
func TestAConversationWhoseProviderHasNoCapacityCarriesOnAcrossProviders(t *testing.T) {
	t.Parallel()

	resetsAt := time.Date(2026, 9, 7, 11, 0, 0, 0, time.UTC)
	held := &speakingBackend{results: []backendapi.RunResult{
		{SessionID: "claude-session-1", FinalText: "Two goals, then."},
		{
			IsError:    true,
			StopReason: "usage_limit",
			UsageLimit: &backendapi.UsageLimit{Kind: "five_hour", ResetsAt: resetsAt},
		},
	}}
	crossed := &speakingBackend{results: []backendapi.RunResult{
		{SessionID: "second-session-1", FinalText: "The second one first."},
	}}
	limits := newTestUsageLimits(t)
	options := crossingOptions(t, held, crossed)
	options.UsageLimits = limits
	session := openTestSession(t, options)

	// One turn on the provider the conversation is held on, so the record has
	// something in it for the crossing to be rebuilt from.
	if _, err := session.Send(context.Background(), "what should we do?"); err != nil {
		t.Fatalf("Send() error = %v on the first turn", err)
	}
	reply, err := session.Send(context.Background(), "and after that?")
	if err != nil {
		t.Fatalf("Send() error = %v, want the turn served on the other provider", err)
	}
	if reply.Text == "" {
		t.Fatal("reply text is empty, want the answer the other provider gave")
	}
	if len(crossed.requests) != 1 {
		t.Fatalf("the other provider was asked %d times, want the one turn it served", len(crossed.requests))
	}

	asked := crossed.requests[0]
	if asked.SessionID != "" {
		t.Fatalf("session asked for = %q, want none: that session belongs to the provider that refused the turn", asked.SessionID)
	}
	if !strings.Contains(asked.Prompt, "rebuilt from its record") {
		t.Fatalf("prompt = %q, want the conversation rebuilt in front of the turn", asked.Prompt)
	}
	// What the record actually holds has to be in it, or the rebuild is a header
	// with nothing under it.
	if !strings.Contains(asked.Prompt, "Two goals, then.") {
		t.Fatalf("prompt = %q, want what this conversation has already said, read back from its event log", asked.Prompt)
	}
	if !strings.Contains(asked.Prompt, testBriefing) {
		t.Fatalf("prompt = %q, want the picture this conversation is working from", asked.Prompt)
	}
	if !strings.Contains(asked.Prompt, "and after that?") {
		t.Fatalf("prompt = %q, want the operator's message the turn is actually answering", asked.Prompt)
	}
	if asked.Model != "second-model" || asked.AccountAlias != "second-account" {
		t.Fatalf("asked model %q on account %q, want the alternate endpoint's own", asked.Model, asked.AccountAlias)
	}
	// The system prompt is the role's contract and comes from the harness rather
	// than from any provider, so it crosses unchanged: a role served elsewhere is
	// held to the same authority it was held to here.
	if asked.SystemPrompt != held.requests[0].SystemPrompt {
		t.Fatal("the system prompt changed when the turn crossed, want the role's contract unchanged by which provider serves it")
	}

	// The record says which endpoint served the turn, so the session identifier on
	// it reads as the second provider's rather than as the first's.
	recorded, err := options.Store.Load(options.identity())
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if recorded.Backend != "second-provider" || recorded.AccountAlias != "second-account" || recorded.ProviderModel != "second-model" {
		t.Fatalf("recorded = %#v, want the endpoint that actually served this turn", recorded)
	}
	if recorded.ProviderSessionID != "second-session-1" {
		t.Fatalf("recorded session = %q, want the one the provider that served the turn reported", recorded.ProviderSessionID)
	}

	substitutions, err := limits.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(substitutions) != 1 {
		t.Fatalf("List() = %#v, want the substitution recorded once", substitutions)
	}
	if !substitutions[0].CrossedProviders() {
		t.Fatalf("recorded = %#v, want the record saying the turn crossed providers", substitutions[0])
	}
	if substitutions[0].Provider != domain.BackendClaudeCode || substitutions[0].ServedByProvider != "second-provider" {
		t.Fatalf("recorded = %#v, want both providers named", substitutions[0])
	}
}

// The turn after a crossing, once the window has lifted, goes back to the
// provider the conversation is configured for — and takes the record with it,
// because the session it left behind there was closed by the crossing and the one
// it holds now belongs to somebody else.
func TestTheTurnAfterACrossingComesBackRebuiltRatherThanResumingTheWrongSession(t *testing.T) {
	t.Parallel()

	held := &speakingBackend{results: []backendapi.RunResult{
		{SessionID: "claude-session-1", FinalText: "Two goals, then."},
		{
			IsError:    true,
			StopReason: "usage_limit",
			UsageLimit: &backendapi.UsageLimit{Kind: "five_hour", ResetsAt: time.Date(2026, 9, 7, 11, 0, 0, 0, time.UTC)},
		},
		{SessionID: "claude-session-2", FinalText: "Back on the first provider."},
	}}
	crossed := &speakingBackend{results: []backendapi.RunResult{
		{SessionID: "second-session-1", FinalText: "The second one first."},
	}}
	options := crossingOptions(t, held, crossed)
	options.UsageLimits = newTestUsageLimits(t)
	// The window the refusal named lifts before the third turn, so affinity puts
	// the conversation back on the provider it is configured for.
	clock := &steppingClock{at: fixedClock{}.Now()}
	options.Clock = clock
	session := openTestSession(t, options)

	if _, err := session.Send(context.Background(), "what should we do?"); err != nil {
		t.Fatalf("Send() error = %v on the first turn", err)
	}
	if _, err := session.Send(context.Background(), "and after that?"); err != nil {
		t.Fatalf("Send() error = %v on the crossing turn", err)
	}
	clock.at = time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	if _, err := session.Send(context.Background(), "is that still right?"); err != nil {
		t.Fatalf("Send() error = %v on the turn after the window lifted", err)
	}

	if len(held.requests) != 3 {
		t.Fatalf("the configured provider was asked %d times, want the first turn, the refused one, and the one after the window lifted", len(held.requests))
	}
	back := held.requests[2]
	if back.SessionID != "" {
		t.Fatalf("session asked for = %q, want none: the session the record held belongs to the provider that served the crossing", back.SessionID)
	}
	if !strings.Contains(back.Prompt, "rebuilt from its record") {
		t.Fatalf("prompt = %q, want the conversation rebuilt for the provider it is coming back to", back.Prompt)
	}
	if !strings.Contains(back.Prompt, "The second one first.") {
		t.Fatalf("prompt = %q, want what was said while the conversation was on the other provider", back.Prompt)
	}
}

// The posture check refuses a crossing onto a provider that cannot hold the
// role's tool posture, and the turn stays where it was. It is the same refusal a
// substitution within one provider gets, and it is what keeps a fallback from
// reaching authority no configuration ever stated.
func TestACrossingOntoAProviderThatCannotHoldTheRolesPostureIsRefused(t *testing.T) {
	t.Parallel()

	held := &speakingBackend{results: []backendapi.RunResult{{
		IsError:    true,
		StopReason: "usage_limit",
		UsageLimit: &backendapi.UsageLimit{Kind: "five_hour"},
	}}}
	crossed := &speakingBackend{}
	limits := newTestUsageLimits(t)
	options := crossingOptions(t, held, crossed)
	options.UsageLimits = limits
	// The product manager reasons over evidence with no tools at all, and the
	// alternate provider scopes writes to a worktree and can express nothing
	// narrower.
	options.Providers = writesOnlySecondProvider(t)
	session := openTestSession(t, options)

	reply, err := session.Send(context.Background(), "what should we do?")
	if err == nil {
		t.Fatal("Send() succeeded, want the refusal handed back with the turn left where it was")
	}
	if len(crossed.requests) != 0 {
		t.Fatalf("the other provider was asked %d times, want none", len(crossed.requests))
	}
	if len(held.requests) != 1 {
		t.Fatalf("the configured provider was asked %d times, want the one attempt", len(held.requests))
	}
	if !strings.Contains(reply.FailoverProblem, `cannot hold the "read-only" tool posture`) {
		t.Fatalf("failover problem = %q, want the posture that could not be held named", reply.FailoverProblem)
	}
	if recorded, err := limits.List(); err != nil || len(recorded) != 1 || recorded[0].Substituted() {
		t.Fatalf("List() = %#v, error %v, want the refusal recorded and no substitution claimed", recorded, err)
	}
}

// crossingOptions is a conversation held on Claude Code whose failover leaves the
// provider, with everything a crossing needs wired: the endpoint, the adapter
// that reaches it, and the registry both are checked against.
func crossingOptions(t *testing.T, held, crossed Backend) Options {
	t.Helper()

	options := testOptions(t, held)
	options.Model = "opus"
	options.FailoverModel = "second-model"
	options.FailoverBackend = crossed
	options.FailoverEndpoint = backendapi.Endpoint{
		Provider:       "second-provider",
		AdapterVersion: backendapi.ClaudeCodeAdapterVersion,
		AccountAlias:   "second-account",
		Model:          "second-model",
	}
	options.Providers = secondProviderRegistry(t, []backendapi.Posture{backendapi.PostureReadOnly, backendapi.PostureWorktreeWrite})
	return options
}

// secondProviderRegistry is a project that declared a provider of its own beside
// the built-ins — the ordinary way a project comes to have two providers to fail
// over between.
func secondProviderRegistry(t *testing.T, postures []backendapi.Posture) *backendapi.Registry {
	t.Helper()

	registry, err := backendapi.NewRegistry(map[domain.Backend]backendapi.ProviderPlugin{
		"second-provider": {
			Adapter: domain.BackendClaudeCode,
			Roles: []domain.AgentRole{
				domain.RoleProductManager, domain.RoleArchitect, domain.RoleDevelopmentManager,
				domain.RoleDeveloper, domain.RoleReviewer,
			},
			Postures: postures,
			Dialect: backendapi.DialectSpec{Rules: []backendapi.DialectRule{
				{Answer: backendapi.AnswerRefused, Terminal: truth(true), Failed: truth(true)},
			}},
		},
	})
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	return registry
}

// writesOnlySecondProvider is the same second provider, able to scope writes to a
// worktree and unable to refuse every tool — which is the shape of a real
// provider rather than an invented one, and the shape a management role's posture
// exists to refuse.
func writesOnlySecondProvider(t *testing.T) *backendapi.Registry {
	t.Helper()

	return secondProviderRegistry(t, []backendapi.Posture{backendapi.PostureWorktreeWrite})
}

func truth(value bool) *bool { return &value }

// speakingBackend is fakeBackend with the reply written into the event it emits,
// so what the conversation records is what the role actually said. The rebuild
// reads the event log, so a fake whose events carried no prose would let a rebuild
// that carries nothing pass.
type speakingBackend struct {
	results  []backendapi.RunResult
	requests []backendapi.RunRequest
}

func (f *speakingBackend) Run(_ context.Context, request backendapi.RunRequest) (backendapi.RunResult, error) {
	index := len(f.requests)
	f.requests = append(f.requests, request)
	sequence := request.LastSequence + 1
	said := ""
	if index < len(f.results) {
		said = f.results[index].FinalText
	}
	if request.EventSink != nil {
		event, err := execution.NewEvent(request.RunID, sequence, fixedClock{}.Now(),
			execution.EventAgentMessage, "provider.test", map[string]any{"text": said})
		if err != nil {
			return backendapi.RunResult{}, err
		}
		if err := request.EventSink(event); err != nil {
			return backendapi.RunResult{}, err
		}
	}
	if index >= len(f.results) {
		return backendapi.RunResult{LastEvent: sequence}, nil
	}
	result := f.results[index]
	result.LastEvent = sequence
	return result, nil
}

// steppingClock is a clock a test moves, which is what reading a window back
// needs: the substitution stands until the provider's own reset time, and the
// turn after it has to be taken on the far side of that moment.
type steppingClock struct{ at time.Time }

func (c *steppingClock) Now() time.Time { return c.at }

// The operator at the terminal is told the turn crossed, and told by which
// provider. The model selectors alone cannot say it: two providers can spell one
// selector, and an operator shown "opus rather than opus" is shown a
// substitution that reads as no substitution at all.
func TestTheOperatorIsToldWhichProviderAnsweredACrossing(t *testing.T) {
	t.Parallel()

	held := &speakingBackend{results: []backendapi.RunResult{{
		IsError:    true,
		StopReason: "usage_limit",
		UsageLimit: &backendapi.UsageLimit{Kind: "five_hour"},
	}}}
	crossed := &speakingBackend{results: []backendapi.RunResult{{SessionID: "second-session-1", FinalText: "Answered here."}}}
	options := crossingOptions(t, held, crossed)
	// The same selector on both providers, which is the case the model alone
	// cannot report.
	options.FailoverModel = "opus"
	options.FailoverEndpoint.Model = "opus"
	options.UsageLimits = newTestUsageLimits(t)

	reply, err := openTestSession(t, options).Send(context.Background(), "what should we do?")
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if evidence := reply.Evidence; evidence.ServedModel != "second-provider's opus" {
		t.Fatalf("evidence = %#v, want the provider that answered named beside the selector", evidence)
	}
}

// Anything recognizably sensitive is redacted on the way out of the harness, and
// a crossing is one more invocation to a provider that has never seen any of it.
// The rebuilt context is assembled after the turn's own prompt was redacted, so
// it has to be redacted itself rather than inheriting a pass it was not part of.
func TestTheRebuiltContextIsRedactedOnTheWayToTheOtherProvider(t *testing.T) {
	t.Parallel()

	held := &speakingBackend{results: []backendapi.RunResult{
		{SessionID: "claude-session-1", FinalText: "Two goals, then."},
		{
			IsError:    true,
			StopReason: "usage_limit",
			UsageLimit: &backendapi.UsageLimit{Kind: "five_hour"},
		},
	}}
	crossed := &speakingBackend{results: []backendapi.RunResult{{SessionID: "second-session-1", FinalText: "The second one first."}}}
	options := crossingOptions(t, held, crossed)
	options.UsageLimits = newTestUsageLimits(t)
	options.Briefing = Briefing{Text: testBriefing + "\nthe token is sk-secret-value\n", GatheredAt: fixedClock{}.Now()}
	options.RedactValues = []string{"sk-secret-value"}
	session := openTestSession(t, options)

	if _, err := session.Send(context.Background(), "what should we do?"); err != nil {
		t.Fatalf("Send() error = %v on the first turn", err)
	}
	if _, err := session.Send(context.Background(), "and after that?"); err != nil {
		t.Fatalf("Send() error = %v on the crossing turn", err)
	}
	if len(crossed.requests) != 1 {
		t.Fatalf("the other provider was asked %d times, want the one turn it served", len(crossed.requests))
	}
	if strings.Contains(crossed.requests[0].Prompt, "sk-secret-value") {
		t.Fatal("the rebuilt context carried a redacted value to the provider the turn crossed to")
	}
}

// A crossing is charged to the account and the provider that actually served it.
// The money left a different subscription, and a line charging it to the account
// whose window closed would attribute the spend to the one place it did not
// happen — which is the account an operator would then go and look at.
func TestACrossingIsChargedToTheSubscriptionThatServedIt(t *testing.T) {
	t.Parallel()

	held := &speakingBackend{results: []backendapi.RunResult{{
		IsError:    true,
		StopReason: "usage_limit",
		UsageLimit: &backendapi.UsageLimit{Kind: "five_hour"},
	}}}
	crossed := &speakingBackend{results: []backendapi.RunResult{
		{SessionID: "second-session-1", FinalText: "Answered here.", CostUSD: 0.25, CostReported: true},
	}}
	log := &collectingSpend{}
	options := crossingOptions(t, held, crossed)
	options.UsageLimits = newTestUsageLimits(t)
	options.Spend = log
	if _, err := openTestSession(t, options).Send(context.Background(), "what should we do?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	// Two lines: the refused attempt on the endpoint the conversation is held on,
	// and the one the crossing served.
	if len(log.lines) != 2 {
		t.Fatalf("cost lines = %#v, want one for the refused attempt and one for the crossing", log.lines)
	}
	if refusedLine := log.lines[0]; refusedLine.AccountAlias != options.AccountAlias || refusedLine.Backend != options.Provider {
		t.Fatalf("the refused attempt is charged to %q on %q, want the conversation's own account and provider",
			refusedLine.AccountAlias, refusedLine.Backend)
	}
	served := log.lines[1]
	if served.AccountAlias != "second-account" || served.Backend != "second-provider" {
		t.Fatalf("the crossing is charged to %q on %q, want the subscription the money actually left",
			served.AccountAlias, served.Backend)
	}
	if served.Model != "second-model" || served.AmountUSD != 0.25 {
		t.Fatalf("the crossing line = %#v, want what the provider that served it reported", served)
	}
}

// A turn whose own prompt already carries the picture does not have it repeated
// by the rebuild. The first turn carries it, and so does one the operator asked
// for a refresh on; handing the provider the same document twice spends context
// to say one thing and reads as two pictures to reconcile.
func TestTheRebuildDoesNotRepeatAPictureTheTurnAlreadyCarries(t *testing.T) {
	t.Parallel()

	held := &speakingBackend{results: []backendapi.RunResult{{
		IsError:    true,
		StopReason: "usage_limit",
		UsageLimit: &backendapi.UsageLimit{Kind: "five_hour"},
	}}}
	crossed := &speakingBackend{results: []backendapi.RunResult{{SessionID: "second-session-1", FinalText: "Answered here."}}}
	options := crossingOptions(t, held, crossed)
	options.UsageLimits = newTestUsageLimits(t)
	// The first turn, which carries the briefing in its own prompt.
	if _, err := openTestSession(t, options).Send(context.Background(), "what should we do?"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if len(crossed.requests) != 1 {
		t.Fatalf("the other provider was asked %d times, want the one turn it served", len(crossed.requests))
	}
	if count := strings.Count(crossed.requests[0].Prompt, testBriefing); count != 1 {
		t.Fatalf("the picture appears %d times in the crossed turn, want exactly once", count)
	}
}

// A window outlasts a turn, so the turn after a crossing goes where the last one
// did. By then that provider holds a session of its own, and resuming it is the
// whole difference between an outage costing one reconstruction and one per turn
// — and between the alternate being told the conversation once and being told it
// again on top of a session that already holds it.
func TestATurnTakenInsideTheSameWindowResumesTheAlternatesOwnSession(t *testing.T) {
	t.Parallel()

	resetsAt := time.Date(2026, 9, 7, 11, 0, 0, 0, time.UTC)
	held := &speakingBackend{results: []backendapi.RunResult{
		{SessionID: "claude-session-1", FinalText: "Two goals, then."},
		{
			IsError:    true,
			StopReason: "usage_limit",
			UsageLimit: &backendapi.UsageLimit{Kind: "five_hour", ResetsAt: resetsAt},
		},
	}}
	crossed := &speakingBackend{results: []backendapi.RunResult{
		{SessionID: "second-session-1", FinalText: "The second one first."},
		{SessionID: "second-session-1", FinalText: "Still the second one."},
		{SessionID: "second-session-1", FinalText: "And still."},
	}}
	options := crossingOptions(t, held, crossed)
	options.UsageLimits = newTestUsageLimits(t)
	session := openTestSession(t, options)

	for _, message := range []string{"what should we do?", "and after that?", "is that still right?", "and now?"} {
		if _, err := session.Send(context.Background(), message); err != nil {
			t.Fatalf("Send(%q) error = %v", message, err)
		}
	}

	// The endpoint whose window closed is asked twice and no more: the first turn,
	// and the one it refused. Every turn after that goes straight across, which is
	// what reading the window back off the log buys.
	if len(held.requests) != 2 {
		t.Fatalf("the configured provider was asked %d times, want the first turn and the refused one", len(held.requests))
	}
	if len(crossed.requests) != 3 {
		t.Fatalf("the alternate was asked %d times, want the crossing and the two turns after it", len(crossed.requests))
	}
	// The crossing itself has no session and carries the reconstruction.
	if crossing := crossed.requests[0]; crossing.SessionID != "" || !strings.Contains(crossing.Prompt, "rebuilt from its record") {
		t.Fatalf("the crossing asked for session %q, want none and the conversation rebuilt in front of the turn", crossing.SessionID)
	}
	// Every turn after it resumes the session the alternate reported, and is not
	// told the conversation a second time.
	for index, later := range crossed.requests[1:] {
		if later.SessionID != "second-session-1" {
			t.Fatalf("turn %d inside the window asked for session %q, want the one the alternate itself reported",
				index+2, later.SessionID)
		}
		if strings.Contains(later.Prompt, "rebuilt from its record") {
			t.Fatalf("turn %d inside the window carries the reconstruction again: %q", index+2, later.Prompt)
		}
	}
}

// And the reconstruction is never sent twice in one prompt, whichever order a
// turn reaches the rebuild in. A turn can be prepared for the endpoint it was
// nominally on and then moved by a refusal nobody could have known about in
// advance; two reconstructions in one prompt is the conversation told to itself
// twice, and on a long one it is a turn refused for its own size.
func TestTheReconstructionIsNeverSentTwiceInOnePrompt(t *testing.T) {
	t.Parallel()

	held := &speakingBackend{results: []backendapi.RunResult{
		{SessionID: "claude-session-1", FinalText: "Two goals, then."},
		{
			IsError:    true,
			StopReason: "usage_limit",
			UsageLimit: &backendapi.UsageLimit{Kind: "five_hour", ResetsAt: time.Date(2026, 9, 7, 11, 0, 0, 0, time.UTC)},
		},
		// The window has lifted, and the endpoint refuses again anyway: the turn is
		// prepared here and then moved across, which is the one ordering that reaches
		// the rebuild twice.
		{
			IsError:    true,
			StopReason: "usage_limit",
			UsageLimit: &backendapi.UsageLimit{Kind: "five_hour"},
		},
	}}
	crossed := &speakingBackend{results: []backendapi.RunResult{
		{SessionID: "second-session-1", FinalText: "The second one first."},
		{SessionID: "second-session-1", FinalText: "Still here."},
	}}
	options := crossingOptions(t, held, crossed)
	options.UsageLimits = newTestUsageLimits(t)
	clock := &steppingClock{at: fixedClock{}.Now()}
	options.Clock = clock
	session := openTestSession(t, options)

	if _, err := session.Send(context.Background(), "what should we do?"); err != nil {
		t.Fatalf("Send() error = %v on the first turn", err)
	}
	if _, err := session.Send(context.Background(), "and after that?"); err != nil {
		t.Fatalf("Send() error = %v on the crossing turn", err)
	}
	clock.at = time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	if _, err := session.Send(context.Background(), "is that still right?"); err != nil {
		t.Fatalf("Send() error = %v on the turn after the window lifted", err)
	}

	for who, requests := range map[string][]backendapi.RunRequest{
		"the configured provider": held.requests,
		"the alternate":           crossed.requests,
	} {
		for index, request := range requests {
			if count := strings.Count(request.Prompt, "rebuilt from its record"); count > 1 {
				t.Fatalf("%s was asked turn %d carrying %d reconstructions, want at most one", who, index+1, count)
			}
		}
	}
}
