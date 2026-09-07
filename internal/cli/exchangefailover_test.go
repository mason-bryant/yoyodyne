package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/exchange"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// An exchange is a role speaking, so a role that can still speak in its own
// conversation but not when another role asks it something would be the same
// stall moved one seam along. The answering round is served by the answering
// agent's permitted alternate exactly as its conversation turn is, and the
// exchange record says which model actually answered.
func TestAnAnsweringRoundIsServedByThePermittedAlternate(t *testing.T) {
	t.Parallel()

	resetsAt := time.Date(2026, 9, 7, 11, 0, 0, 0, time.UTC)
	provider := &sequencingBackend{results: []backend.RunResult{
		{
			IsError:    true,
			StopReason: "usage_limit",
			UsageLimit: &backend.UsageLimit{Kind: "five_hour", ResetsAt: resetsAt},
		},
		{SessionID: "session-1", FinalText: "It costs a week and you are missing the migration."},
	}}
	limits := newTestExchangeUsageLimits(t)
	voice := exchangeVoice{
		config:      failingOverAnsweringConfig(),
		provider:    provider,
		repository:  t.TempDir(),
		stateRoot:   t.TempDir(),
		usageLimits: limits,
		productID:   "yoyodyne",
		clock:       exchangeFixedNow,
	}

	spoken, err := voice.Answer(context.Background(), answeringQuestion())
	if err != nil {
		t.Fatalf("Answer() error = %v, want the round served by the alternate", err)
	}
	if spoken.SessionID != "session-1" {
		t.Fatalf("spoken = %+v, want the answer the alternate gave", spoken)
	}
	if provider.calls != 2 {
		t.Fatalf("invocations = %d, want the refused one and the one that served", provider.calls)
	}
	if provider.requests[0].Model != "opus-architect" || provider.requests[1].Model != "fable-architect" {
		t.Fatalf("models asked = %q then %q, want the configured model first and the alternate second",
			provider.requests[0].Model, provider.requests[1].Model)
	}
	// The exchange record keeps the model that actually asked. Recording the
	// configured selector would leave the thread naming a model that refused it.
	if spoken.Model != "fable-architect" {
		t.Fatalf("spoken model = %q, want the alternate that answered the round", spoken.Model)
	}

	recorded, err := limits.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 1 {
		t.Fatalf("List() = %#v, want the substitution recorded once", recorded)
	}
	if recorded[0].Model != "opus-architect" || recorded[0].ServedBy != "fable-architect" {
		t.Fatalf("recorded = %#v, want which model was refused and which served", recorded[0])
	}
	// It names what would have stopped in the same words the refusal beside it
	// uses, because it is the same thing photographed from the other side.
	if !strings.Contains(recorded[0].Waiting, "answering exchange") {
		t.Fatalf("waiting = %q, want the answering round that would have stopped", recorded[0].Waiting)
	}
}

// The window the substitution opened is read back on the next round, so a second
// question inside it goes straight to the alternate rather than paying another
// refused invocation and announcing the same outage again.
func TestASecondRoundInsideTheWindowGoesStraightToTheAlternate(t *testing.T) {
	t.Parallel()

	limits := newTestExchangeUsageLimits(t)
	if err := limits.Record(runstate.UsageLimitExhaustion{
		SchemaVersion: runstate.UsageLimitSchemaVersion,
		ProductID:     "yoyodyne",
		At:            exchangeFixedNow(),
		Waiting:       "the architect answering an earlier exchange",
		Model:         "opus-architect",
		ServedBy:      "fable-architect",
		ResetsAt:      pointerToExchangeTime(exchangeFixedNow().Add(time.Hour)),
	}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	provider := &sequencingBackend{results: []backend.RunResult{{SessionID: "session-2", FinalText: "Still a week."}}}
	voice := exchangeVoice{
		config:      failingOverAnsweringConfig(),
		provider:    provider,
		repository:  t.TempDir(),
		stateRoot:   t.TempDir(),
		usageLimits: limits,
		productID:   "yoyodyne",
		clock:       exchangeFixedNow,
	}

	spoken, err := voice.Answer(context.Background(), answeringQuestion())
	if err != nil {
		t.Fatalf("Answer() error = %v", err)
	}
	if provider.calls != 1 || provider.requests[0].Model != "fable-architect" {
		t.Fatalf("invocations = %#v, want one, straight to the alternate", provider.requests)
	}
	if spoken.Model != "fable-architect" {
		t.Fatalf("spoken model = %q, want the alternate the standing window points at", spoken.Model)
	}
	if recorded, _ := limits.List(); len(recorded) != 1 {
		t.Fatalf("List() = %#v, want the outage said once rather than again on this round", recorded)
	}
}

// An answering agent that has not enabled failover behaves exactly as it did
// before: one invocation, under the model it named, and a refused round that
// fails with the refusal recorded as the stoppage it was.
func TestAnAnsweringRoundWithoutFailoverIsRefusedAsBefore(t *testing.T) {
	t.Parallel()

	provider := &sequencingBackend{results: []backend.RunResult{{
		IsError:    true,
		StopReason: "usage_limit",
		UsageLimit: &backend.UsageLimit{Kind: "five_hour"},
	}}}
	limits := newTestExchangeUsageLimits(t)
	voice := exchangeVoice{
		config:      answeringConfig(),
		provider:    provider,
		repository:  t.TempDir(),
		stateRoot:   t.TempDir(),
		usageLimits: limits,
		productID:   "yoyodyne",
		clock:       exchangeFixedNow,
	}

	if _, err := voice.Answer(context.Background(), answeringQuestion()); err == nil {
		t.Fatal("Answer() error = nil, want the refused round still failed")
	}
	if provider.calls != 1 {
		t.Fatalf("invocations = %d, want exactly the one the agent was configured for", provider.calls)
	}
	recorded, err := limits.List()
	if err != nil || len(recorded) != 1 {
		t.Fatalf("List() = %#v, error %v, want the refusal recorded as the stoppage it was", recorded, err)
	}
	if recorded[0].Substituted() {
		t.Fatalf("recorded = %#v, want a stoppage rather than a substitution", recorded[0])
	}
}

// failingOverAnsweringConfig is the answering configuration with the architect
// permitted one alternate, which is the only difference between the two.
func failingOverAnsweringConfig() config.Config {
	cfg := answeringConfig()
	architect := cfg.Agents["architect"]
	architect.Failover = config.Failover{Enabled: true, Model: "fable-architect"}
	cfg.Agents["architect"] = architect
	cfg.Execution.UsageLimitUnknownResetPause = config.Duration(30 * time.Minute)
	return cfg
}

func answeringQuestion() exchange.Question {
	return exchange.Question{
		ExchangeID: "exchange-" + strings.Repeat("a", 32),
		Role:       domain.RoleArchitect,
		Asker:      domain.RoleProductManager,
		Round:      1,
		MaxRounds:  10,
		Question:   "what does this goal cost, and what am I missing?",
	}
}

func newTestExchangeUsageLimits(t *testing.T) *runstate.UsageLimitStore {
	t.Helper()

	store, err := runstate.NewUsageLimitStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewUsageLimitStore() error = %v", err)
	}
	return store
}

// sequencingBackend answers each invocation with the next result it was given,
// which is what a failed-over round needs and the single-result fake beside it
// cannot express: the two attempts of one round end differently.
type sequencingBackend struct {
	results  []backend.RunResult
	requests []backend.RunRequest
	calls    int
}

func (s *sequencingBackend) Run(_ context.Context, request backend.RunRequest) (backend.RunResult, error) {
	index := s.calls
	s.requests = append(s.requests, request)
	s.calls++
	if index >= len(s.results) {
		return backend.RunResult{}, errors.New("unexpected answering round")
	}
	return s.results[index], nil
}

func exchangeFixedNow() time.Time { return time.Date(2026, 9, 7, 6, 0, 0, 0, time.UTC) }

func pointerToExchangeTime(at time.Time) *time.Time { return &at }
