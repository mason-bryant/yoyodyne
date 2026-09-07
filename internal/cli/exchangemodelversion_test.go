package cli

import (
	"context"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// An exchange is a role speaking, so a round is answered by the same version
// that role's conversation is held on rather than by whatever the family alias
// floats to at the moment somebody asks it something.
func TestAnAnsweringRoundAsksForThePinnedVersion(t *testing.T) {
	t.Parallel()

	provider := &sequencingBackend{results: []backend.RunResult{
		{SessionID: "session-1", FinalText: "It costs a week and you are missing the migration."},
	}}
	voice := exchangeVoice{
		config:      pinnedAnsweringConfig(),
		provider:    provider,
		repository:  t.TempDir(),
		stateRoot:   t.TempDir(),
		usageLimits: newTestExchangeUsageLimits(t),
		productID:   "yoyodyne",
		clock:       exchangeFixedNow,
	}

	spoken, err := voice.Answer(context.Background(), answeringQuestion())
	if err != nil {
		t.Fatalf("Answer() error = %v", err)
	}
	if provider.calls != 1 || provider.requests[0].Model != "opus-architect-20260401" {
		t.Fatalf("invocations = %#v, want one, under the pinned version", provider.requests)
	}
	if spoken.Model != "opus-architect-20260401" {
		t.Fatalf("spoken model = %q, want the version the round actually asked for", spoken.Model)
	}
}

// And a version this provider has not got does not stop the round: the family
// alias answers it, the exchange record names what actually answered, and the
// fallback is written down where every other substitution is.
func TestAnAnsweringRoundFallsBackWhenTheProviderHasNotGotTheVersion(t *testing.T) {
	t.Parallel()

	provider := &sequencingBackend{results: []backend.RunResult{
		{
			IsError:          true,
			StopReason:       "api_error",
			ModelUnavailable: &backend.ModelUnavailable{Detail: "api_error: API Error: 404 model: opus-architect-20260401"},
		},
		{SessionID: "session-1", FinalText: "It costs a week and you are missing the migration."},
	}}
	limits := newTestExchangeUsageLimits(t)
	voice := exchangeVoice{
		config:      pinnedAnsweringConfig(),
		provider:    provider,
		repository:  t.TempDir(),
		stateRoot:   t.TempDir(),
		usageLimits: limits,
		productID:   "yoyodyne",
		clock:       exchangeFixedNow,
	}

	spoken, err := voice.Answer(context.Background(), answeringQuestion())
	if err != nil {
		t.Fatalf("Answer() error = %v, want the round served by the family alias", err)
	}
	if provider.calls != 2 {
		t.Fatalf("invocations = %d, want the refused version and the alias that served", provider.calls)
	}
	if provider.requests[0].Model != "opus-architect-20260401" || provider.requests[1].Model != "opus-architect" {
		t.Fatalf("models asked = %q then %q, want the pin first and the family alias second",
			provider.requests[0].Model, provider.requests[1].Model)
	}
	if spoken.Model != "opus-architect" {
		t.Fatalf("spoken model = %q, want the model that actually answered the round", spoken.Model)
	}

	recorded, err := limits.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 1 {
		t.Fatalf("List() = %#v, want the fallback recorded once", recorded)
	}
	if recorded[0].Model != "opus-architect-20260401" || recorded[0].ServedBy != "opus-architect" {
		t.Fatalf("recorded = %#v, want the pin and the model that served", recorded[0])
	}
	if recorded[0].Reason() != runstate.SubstitutedForAvailability {
		t.Fatalf("recorded reason = %q, want availability rather than capacity", recorded[0].Reason())
	}
}

// pinnedAnsweringConfig is the answering configuration with the architect pinned
// to one version, which is the only difference between the two.
func pinnedAnsweringConfig() config.Config {
	cfg := answeringConfig()
	architect := cfg.Agents["architect"]
	architect.ModelVersion = "opus-architect-20260401"
	cfg.Agents["architect"] = architect
	return cfg
}
