package modelfailover

// Failing over between providers rather than within one.
//
// The rebuild is what these are here for. A substitution that stays on the
// provider keeps the session it was already holding, so nothing about it proves
// anything about durable state; one that leaves the provider has no session to
// keep, and whether the turn carries on at all is decided entirely by what the
// harness wrote down. So these exercise the crossing rather than asserting it:
// which invoker was asked, what it was asked with, and what the record says
// afterwards.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The turn the whole slice exists for: the endpoint has no capacity, the
// alternate is on another provider, and the turn is served there — with no
// session identifier and with the context the caller rebuilt from the durable
// record.
func TestARefusedTurnCrossesToTheAlternateProviderAndRebuildsItsContext(t *testing.T) {
	t.Parallel()

	resetsAt := time.Date(2026, 9, 7, 11, 0, 0, 0, time.UTC)
	refusing := &fakeProvider{results: []backend.RunResult{refused("five_hour", resetsAt)}}
	crossed := &fakeProvider{results: []backend.RunResult{{SessionID: "codex-session-1", FinalText: "decided"}}}
	windows := newTestWindows(t)
	rebuilt := 0
	policy := crossingPolicy(t, windows, func(request backend.RunRequest) (backend.RunRequest, error) {
		rebuilt++
		request.Prompt = "# This conversation, rebuilt from its record\n\n" + request.Prompt
		return request, nil
	})
	policy.AlternateProvider = crossed
	result, served, err := Serve(context.Background(), refusing, backend.RunRequest{
		Model:        "fable",
		SessionID:    "claude-session-1",
		Prompt:       "# Operator message\n\nwhat now?",
		AccountAlias: "house",
	}, policy)
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if result.FinalText != "decided" {
		t.Fatalf("final text = %q, want the answer the other provider gave", result.FinalText)
	}
	if len(refusing.requests) != 1 || len(crossed.requests) != 1 {
		t.Fatalf("invocations = %d then %d, want the refused one and the one the other provider served",
			len(refusing.requests), len(crossed.requests))
	}
	// The crossing is the only thing that could have served this turn, so what it
	// was asked with is the whole of the guarantee.
	asked := crossed.requests[0]
	if asked.SessionID != "" {
		t.Fatalf("session asked for = %q, want none: the provider taking this turn has never seen that session", asked.SessionID)
	}
	if rebuilt != 1 {
		t.Fatalf("rebuilds = %d, want the context assembled from the record exactly once", rebuilt)
	}
	if !strings.HasPrefix(asked.Prompt, "# This conversation, rebuilt from its record") {
		t.Fatalf("prompt asked = %q, want the rebuilt context in front of the turn", asked.Prompt)
	}
	if asked.Model != "gpt-5-codex" || asked.AccountAlias != "codex-house" {
		t.Fatalf("asked model %q on account %q, want the alternate endpoint's own", asked.Model, asked.AccountAlias)
	}
	if asked.AccountConfigDir != "/tmp/codex-home" {
		t.Fatalf("provider home = %q, want the alternate account's own; crossing providers is crossing logins", asked.AccountConfigDir)
	}

	if served.Endpoint.Provider != "codex" || served.RefusedEndpoint.Provider != "claude-code" {
		t.Fatalf("served = %#v, want the endpoint that served and the one that refused", served)
	}
	if !served.CrossedProviders() {
		t.Fatal("the turn does not read as having crossed providers, so nothing would say its context was rebuilt")
	}

	recorded, err := windows.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 1 {
		t.Fatalf("List() = %#v, want the substitution recorded once", recorded)
	}
	substitution := recorded[0]
	if substitution.Provider != "claude-code" || substitution.ServedByProvider != "codex" {
		t.Fatalf("recorded = %#v, want both providers named, since the model selectors alone cannot say a turn crossed", substitution)
	}
	if !substitution.CrossedProviders() {
		t.Fatal("the record does not read as a crossing, so what an operator is told would not say the context was rebuilt")
	}
	if !strings.Contains(substitution.Describe(), "rebuilt its context from the durable record") {
		t.Fatalf("described = %q, want the crossing said in the cause", substitution.Describe())
	}
}

// A crossing onto a provider that cannot hold the role's tool posture is refused
// exactly as a substitution within one provider is, and the turn stays where it
// was. It is the same check and the same refusal: what makes an endpoint
// ineligible is the posture rather than which provider it belongs to.
func TestACrossingOntoAnIneligibleProviderIsRefusedAndTheTurnStays(t *testing.T) {
	t.Parallel()

	refusing := &fakeProvider{results: []backend.RunResult{refused("five_hour", time.Time{})}}
	crossed := &fakeProvider{}
	windows := newTestWindows(t)
	var reported []error
	policy := crossingPolicy(t, windows, func(request backend.RunRequest) (backend.RunRequest, error) {
		return request, nil
	})
	policy.AlternateProvider = crossed
	// The reviewer reasons over bounded evidence with no tools at all, and the
	// alternate provider scopes writes to a worktree and can express nothing
	// narrower than that.
	policy.Role = domain.RoleReviewer
	policy.Eligibility = twoProviderRegistry(t)
	policy.RecordFailure = func(err error) { reported = append(reported, err) }

	_, served, err := Serve(context.Background(), refusing, backend.RunRequest{Model: "fable", SessionID: "claude-session-1"}, policy)
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if len(refusing.requests) != 1 || len(crossed.requests) != 0 {
		t.Fatalf("invocations = %d then %d, want the one attempt on the endpoint the turn was already on",
			len(refusing.requests), len(crossed.requests))
	}
	if served.Substituted() {
		t.Fatalf("served = %#v, want the turn left where it was", served)
	}
	if len(reported) != 1 || !strings.Contains(reported[0].Error(), `cannot hold the "read-only" tool posture`) {
		t.Fatalf("reported = %v, want the posture that could not be held named", reported)
	}
	if recorded, err := windows.List(); err != nil || len(recorded) != 0 {
		t.Fatalf("List() = %#v, error %v, want nothing recorded for a substitution that was refused", recorded, err)
	}
}

// A crossing with no way to rebuild the context is refused before it is
// attempted. Sending the second provider the first one's session identifier and
// none of the conversation would be the conversation quietly starting over,
// which is the one failure the durable-state guarantee exists to prevent.
func TestACrossingWithNoWayToRebuildIsRefusedRatherThanAttempted(t *testing.T) {
	t.Parallel()

	refusing := &fakeProvider{results: []backend.RunResult{refused("five_hour", time.Time{})}}
	crossed := &fakeProvider{}
	var reported []error
	policy := crossingPolicy(t, newTestWindows(t), nil)
	policy.AlternateProvider = crossed
	policy.RecordFailure = func(err error) { reported = append(reported, err) }

	_, served, err := Serve(context.Background(), refusing, backend.RunRequest{Model: "fable", SessionID: "claude-session-1"}, policy)
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if len(crossed.requests) != 0 {
		t.Fatalf("the other provider was asked %d times, want none: nothing could have told it what the conversation is", len(crossed.requests))
	}
	if served.Substituted() {
		t.Fatalf("served = %#v, want the turn left where it was", served)
	}
	if len(reported) != 1 || !strings.Contains(reported[0].Error(), "rebuilds the turn's context from the durable record") {
		t.Fatalf("reported = %v, want the missing rebuild named", reported)
	}
}

// A rebuild that fails leaves the turn on the refusal it already met rather than
// on the rebuild's own failure. Nothing was asked of the second provider and
// nothing was charged, so what the caller is owed is the refusal — and a
// substitution recorded here would claim work carried on that did not.
func TestARebuildThatFailsLeavesTheTurnOnTheRefusalItAlreadyMet(t *testing.T) {
	t.Parallel()

	refusing := &fakeProvider{results: []backend.RunResult{refused("five_hour", time.Time{})}}
	crossed := &fakeProvider{}
	windows := newTestWindows(t)
	var reported []error
	policy := crossingPolicy(t, windows, func(backend.RunRequest) (backend.RunRequest, error) {
		return backend.RunRequest{}, errors.New("the conversation's event log could not be read")
	})
	policy.AlternateProvider = crossed
	policy.RecordFailure = func(err error) { reported = append(reported, err) }

	result, served, err := Serve(context.Background(), refusing, backend.RunRequest{Model: "fable"}, policy)
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if result.UsageLimit == nil {
		t.Fatal("result carried no usage limit, want the refusal the turn actually met handed back")
	}
	if served.Substituted() {
		t.Fatalf("served = %#v, want the turn left on the endpoint that refused it", served)
	}
	if len(crossed.requests) != 0 {
		t.Fatalf("the other provider was asked %d times, want none", len(crossed.requests))
	}
	if len(reported) != 1 || !strings.Contains(reported[0].Error(), "could not be rebuilt from the durable record") {
		t.Fatalf("reported = %v, want the rebuild's failure reported rather than raised as the turn's", reported)
	}
	if recorded, err := windows.List(); err != nil || len(recorded) != 0 {
		t.Fatalf("List() = %#v, error %v, want nothing recorded: no substitution happened", recorded, err)
	}
}

// A window the harness already watched close sends the turn straight across,
// without paying a refused invocation to rediscover it — and the crossing is
// still a crossing there: no session, and the context rebuilt.
func TestAKnownClosedWindowCrossesWithoutARefusedInvocation(t *testing.T) {
	t.Parallel()

	windows := newTestWindows(t)
	recordWindow(t, windows, runstate.UsageLimitExhaustion{
		Model:    "fable",
		ResetsAt: pointerTo(time.Date(2026, 9, 7, 11, 0, 0, 0, time.UTC)),
	})
	refusing := &fakeProvider{}
	crossed := &fakeProvider{results: []backend.RunResult{{SessionID: "codex-session-1", FinalText: "decided"}}}
	rebuilt := 0
	policy := crossingPolicy(t, windows, func(request backend.RunRequest) (backend.RunRequest, error) {
		rebuilt++
		return request, nil
	})
	policy.AlternateProvider = crossed
	_, served, err := Serve(context.Background(), refusing, backend.RunRequest{Model: "fable", SessionID: "claude-session-1"}, policy)
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if len(refusing.requests) != 0 {
		t.Fatalf("the exhausted endpoint was asked %d times, want none", len(refusing.requests))
	}
	if len(crossed.requests) != 1 || crossed.requests[0].SessionID != "" || rebuilt != 1 {
		t.Fatalf("crossing invocations = %#v, rebuilds = %d, want one rebuilt turn with no session", crossed.requests, rebuilt)
	}
	if served.Endpoint.Provider != "codex" {
		t.Fatalf("served = %#v, want the endpoint on the other provider", served)
	}
}

// A rebuild that hands back a session identifier does not get one sent. The
// guarantee a crossing makes is that the second provider is not asked to resume
// somebody else's session, and it is held here rather than in every caller that
// writes a rebuild.
func TestACrossingNeverSendsASessionEvenWhenTheRebuildPutsOneBack(t *testing.T) {
	t.Parallel()

	refusing := &fakeProvider{results: []backend.RunResult{refused("five_hour", time.Time{})}}
	crossed := &fakeProvider{results: []backend.RunResult{{SessionID: "codex-session-1", FinalText: "decided"}}}
	policy := crossingPolicy(t, newTestWindows(t), func(request backend.RunRequest) (backend.RunRequest, error) {
		request.SessionID = "claude-session-1"
		return request, nil
	})
	policy.AlternateProvider = crossed

	if _, _, err := Serve(context.Background(), refusing, backend.RunRequest{Model: "fable"}, policy); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if len(crossed.requests) != 1 || crossed.requests[0].SessionID != "" {
		t.Fatalf("crossing invocations = %#v, want one carrying no session", crossed.requests)
	}
}

// crossingPolicy is a failover from the Claude Code endpoint onto a Codex one,
// with the fields every crossing needs already filled in so a test states only
// the part it is about. The invoker is replaced by callers that need to inspect
// what it was asked; the one here answers the turn.
func crossingPolicy(t *testing.T, windows Windows, rebuild func(backend.RunRequest) (backend.RunRequest, error)) Policy {
	t.Helper()

	return Policy{
		Alternate: "gpt-5-codex",
		AlternateEndpoint: backend.Endpoint{
			Provider:       "codex",
			AdapterVersion: backend.CodexAdapterVersion,
			AccountAlias:   "codex-house",
			Model:          "gpt-5-codex",
		},
		AlternateProvider:         &fakeProvider{results: []backend.RunResult{{SessionID: "codex-session-1", FinalText: "decided"}}},
		AlternateAccountConfigDir: "/tmp/codex-home",
		Rebuild:                   rebuild,
		Windows:                   windows,
		Now:                       fixedNow,
		ProductID:                 "yoyodyne",
		Waiting:                   "the development manager conversation",
		Endpoint: backend.Endpoint{
			Provider:       "claude-code",
			AdapterVersion: backend.ClaudeCodeAdapterVersion,
			AccountAlias:   "house",
			Model:          "fable",
		},
		Role:        domain.RoleDeveloper,
		Eligibility: twoProviderRegistry(t),
	}
}

// twoProviderRegistry is a project naming the two built-in providers, which is
// the arrangement a crossing is configured for: Claude Code serves every role,
// and Codex serves the developer only because its sandbox cannot express no
// tools at all.
func twoProviderRegistry(t *testing.T) *backend.Registry {
	t.Helper()

	registry, err := backend.NewRegistry(nil)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	return registry
}

// The same selector asked of another provider is a real alternate, because an
// endpoint is the provider and the model together and each has a capacity window
// of its own. A guard that compared model names alone would refuse the crossing a
// project is most likely to write: one model family, two subscriptions.
func TestTheSameSelectorOnAnotherProviderIsStillAnAlternate(t *testing.T) {
	t.Parallel()

	refusing := &fakeProvider{results: []backend.RunResult{refused("five_hour", time.Time{})}}
	crossed := &fakeProvider{results: []backend.RunResult{{SessionID: "second-session-1", FinalText: "decided"}}}
	windows := newTestWindows(t)
	policy := crossingPolicy(t, windows, func(request backend.RunRequest) (backend.RunRequest, error) {
		return request, nil
	})
	policy.AlternateProvider = crossed
	policy.Alternate = "fable"
	policy.AlternateEndpoint.Model = "fable"

	_, served, err := Serve(context.Background(), refusing, backend.RunRequest{Model: "fable"}, policy)
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if len(crossed.requests) != 1 {
		t.Fatalf("the other provider was asked %d times, want the turn it served", len(crossed.requests))
	}
	if !served.CrossedProviders() {
		t.Fatalf("served = %#v, want the turn read as having crossed providers", served)
	}
	recorded, err := windows.List()
	if err != nil || len(recorded) != 1 || !recorded[0].CrossedProviders() {
		t.Fatalf("List() = %#v, error %v, want the crossing recorded with both providers named", recorded, err)
	}
}

// And a crossing with nothing to reach the other provider through is refused on
// the same terms. A different provider is a different adapter under a different
// account, so an alternate endpoint with no invoker behind it names a place
// nothing here could make the invocation to.
func TestACrossingWithNoInvokerIsRefusedRatherThanAttempted(t *testing.T) {
	t.Parallel()

	refusing := &fakeProvider{results: []backend.RunResult{refused("five_hour", time.Time{})}}
	var reported []error
	policy := crossingPolicy(t, newTestWindows(t), func(request backend.RunRequest) (backend.RunRequest, error) {
		return request, nil
	})
	policy.AlternateProvider = nil
	policy.RecordFailure = func(err error) { reported = append(reported, err) }

	_, served, err := Serve(context.Background(), refusing, backend.RunRequest{Model: "fable"}, policy)
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if len(refusing.requests) != 1 || served.Substituted() {
		t.Fatalf("invocations = %d, served = %#v, want the turn left where it was", len(refusing.requests), served)
	}
	if len(reported) != 1 || !strings.Contains(reported[0].Error(), "no invoker was supplied for the alternate provider") {
		t.Fatalf("reported = %v, want the missing invoker named", reported)
	}
}

// A window outlasts a turn, so the alternate is asked again while it stands — and
// by then it holds a session of its own. What is sent is that session and never
// the one the refusing endpoint held, whatever the rebuild returns: the session is
// the one thing about the request this package decides for itself, because it is
// the one thing that must never be the other provider's.
func TestACrossingSendsTheAlternatesOwnSessionAndNeverTheRefusedEndpointsOne(t *testing.T) {
	t.Parallel()

	windows := newTestWindows(t)
	recordWindow(t, windows, runstate.UsageLimitExhaustion{
		Model:    "fable",
		ResetsAt: pointerTo(time.Date(2026, 9, 7, 11, 0, 0, 0, time.UTC)),
	})
	crossed := &fakeProvider{results: []backend.RunResult{{SessionID: "codex-session-1", FinalText: "decided"}}}
	policy := crossingPolicy(t, windows, func(request backend.RunRequest) (backend.RunRequest, error) {
		// A rebuild that tried to put the refusing endpoint's session back gets
		// nowhere, which is what makes the guarantee this package's rather than
		// every caller's.
		request.SessionID = "claude-session-1"
		return request, nil
	})
	policy.AlternateProvider = crossed
	policy.AlternateSessionID = "codex-session-1"

	if _, _, err := Serve(context.Background(), &fakeProvider{},
		backend.RunRequest{Model: "fable", SessionID: "claude-session-1"}, policy); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if len(crossed.requests) != 1 || crossed.requests[0].SessionID != "codex-session-1" {
		t.Fatalf("crossing invocations = %#v, want one resuming the alternate's own session", crossed.requests)
	}
}

// ServesElsewhere is what a caller asks before it prepares the turn, so that the
// preparation and the routing cannot disagree about which endpoint will serve it.
// It answers true only for a move this policy would already make unasked — the
// alternate permitted and the window known closed — and false for the refusal
// that has not happened yet, which is not knowable in advance.
func TestServesElsewhereAnswersForTheMoveThePolicyWouldMakeUnasked(t *testing.T) {
	t.Parallel()

	open := newTestWindows(t)
	if policy := crossingPolicy(t, open, passThrough); policy.ServesElsewhere("fable") {
		t.Fatal("a policy with no window recorded says it will move the turn, want the endpoint asked first")
	}

	closed := newTestWindows(t)
	recordWindow(t, closed, runstate.UsageLimitExhaustion{
		Model:    "fable",
		ResetsAt: pointerTo(time.Date(2026, 9, 7, 11, 0, 0, 0, time.UTC)),
	})
	if policy := crossingPolicy(t, closed, passThrough); !policy.ServesElsewhere("fable") {
		t.Fatal("a policy whose window is recorded closed says it will ask the endpoint, want the turn prepared for the alternate")
	}
	// A window that has lifted puts the turn back on the endpoint it was
	// configured for, so the preparation goes back with it.
	lifted := crossingPolicy(t, closed, passThrough)
	lifted.Now = func() time.Time { return time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC) }
	if lifted.ServesElsewhere("fable") {
		t.Fatal("a policy whose window has lifted still says it will move the turn")
	}
	// And an alternate the role may not be served on is not one, so nothing is
	// prepared for it.
	ineligible := crossingPolicy(t, closed, passThrough)
	ineligible.Role = domain.RoleReviewer
	if ineligible.ServesElsewhere("fable") {
		t.Fatal("a policy whose alternate cannot hold the role's posture says it will move the turn")
	}
	// Failover off answers false, which is what every caller that has not
	// configured one asks and gets.
	if (Policy{}).ServesElsewhere("fable") {
		t.Fatal("a policy with no alternate says it will move the turn")
	}
}

func passThrough(request backend.RunRequest) (backend.RunRequest, error) { return request, nil }
