package modelfailover

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// An agent that has pinned no version behaves exactly as it did before this
// existed: the family alias is what is asked for, and nothing here does
// anything. Everything else in this file is about the option, and this is about
// the default costing nothing.
func TestNoPinnedVersionAsksForTheFamilyAliasAsBefore(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{results: []backend.RunResult{{FinalText: "decided"}}}
	windows := newTestWindows(t)
	_, served, err := Serve(context.Background(), provider, backend.RunRequest{Model: "opus"}, Policy{
		Windows:   windows,
		Now:       fixedNow,
		ProductID: "yoyodyne",
		Waiting:   "the architect conversation",
	})
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if served.Model != "opus" || served.Substituted() {
		t.Fatalf("served = %#v, want the configured alias and no substitution", served)
	}
	if len(provider.requests) != 1 || provider.requests[0].Model != "opus" {
		t.Fatalf("invocations = %#v, want one, under the alias", provider.requests)
	}
	if recorded, err := windows.List(); err != nil || len(recorded) != 0 {
		t.Fatalf("List() = %#v, error %v, want nothing written down", recorded, err)
	}
}

// A pin the provider has is honored, which is the whole of the ordinary case:
// the version is what is asked for and nothing is substituted or recorded.
func TestAPinnedVersionIsWhatIsAskedForWhenTheProviderHasIt(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{results: []backend.RunResult{{FinalText: "decided"}}}
	windows := newTestWindows(t)
	_, served, err := Serve(context.Background(), provider, backend.RunRequest{Model: "opus"}, Policy{
		Version:   "claude-opus-5-20260401",
		Windows:   windows,
		Now:       fixedNow,
		ProductID: "yoyodyne",
		Waiting:   "the architect conversation",
	})
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if served.Model != "claude-opus-5-20260401" || served.Substituted() {
		t.Fatalf("served = %#v, want the pinned version and no substitution", served)
	}
	if len(provider.requests) != 1 || provider.requests[0].Model != "claude-opus-5-20260401" {
		t.Fatalf("invocations = %#v, want one, under the pinned version", provider.requests)
	}
	if recorded, err := windows.List(); err != nil || len(recorded) != 0 {
		t.Fatalf("List() = %#v, error %v, want nothing recorded for a turn nothing was done about", recorded, err)
	}
}

// The turn this exists for: the provider has not got the pinned version, the
// family alias serves it, and the record names both. A pin that stopped the
// agent would be the same stall failover exists to prevent.
func TestAPinnedVersionTheProviderHasNotGotFallsBackToTheFamily(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{results: []backend.RunResult{
		missingModel("model: claude-opus-5-20260401"),
		{SessionID: "session-1", FinalText: "decided"},
	}}
	windows := newTestWindows(t)
	result, served, err := Serve(context.Background(), provider, backend.RunRequest{Model: "opus"}, Policy{
		Version:   "claude-opus-5-20260401",
		Windows:   windows,
		Now:       fixedNow,
		ProductID: "yoyodyne",
		Waiting:   "the architect conversation",
	})
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if result.FinalText != "decided" {
		t.Fatalf("final text = %q, want the answer the family alias gave", result.FinalText)
	}
	if served.Model != "opus" || served.Refused != "claude-opus-5-20260401" {
		t.Fatalf("served = %#v, want the alias serving for the pinned version", served)
	}
	if served.Why != runstate.SubstitutedForAvailability {
		t.Fatalf("why = %q, want the substitution to say it was availability rather than capacity", served.Why)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("invocations = %d, want the refused version and the alias that served", len(provider.requests))
	}
	if provider.requests[0].Model != "claude-opus-5-20260401" || provider.requests[1].Model != "opus" {
		t.Fatalf("models asked = %q then %q, want the pin first and the family alias second",
			provider.requests[0].Model, provider.requests[1].Model)
	}

	recorded, err := windows.List()
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
		t.Fatalf("recorded reason = %q, want availability", entry.Reason())
	}
	if entry.ResetsAt != nil || entry.Kind != "" {
		t.Fatalf("recorded = %#v, want no limit and no reset time; a catalogue is not a window", entry)
	}
	// The provider's own words about the model it would not serve travel with the
	// entry, because the category alone does not say which version was missing.
	if !strings.Contains(entry.Waiting, "claude-opus-5-20260401") {
		t.Fatalf("waiting = %q, want the provider's account of the model it would not serve", entry.Waiting)
	}
}

// The turn after a fallback goes straight to the family alias. A version the
// harness has already been told this provider has not got is not worth one
// refused invocation per turn to rediscover.
func TestAVersionAlreadyFoundMissingIsNotAskedForAgain(t *testing.T) {
	t.Parallel()

	windows := newTestWindows(t)
	recordWindow(t, windows, runstate.UsageLimitExhaustion{
		Model:        "claude-opus-5-20260401",
		ServedBy:     "opus",
		Substitution: runstate.SubstitutedForAvailability,
	})
	provider := &fakeProvider{results: []backend.RunResult{{FinalText: "decided"}}}
	_, served, err := Serve(context.Background(), provider, backend.RunRequest{Model: "opus"}, Policy{
		Version:           "claude-opus-5-20260401",
		Windows:           windows,
		Now:               fixedNow,
		UnknownResetPause: 30 * time.Minute,
		ProductID:         "yoyodyne",
		Waiting:           "the architect conversation",
	})
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if served.Model != "opus" || served.Why != runstate.SubstitutedForAvailability {
		t.Fatalf("served = %#v, want the alias without asking for the missing version", served)
	}
	if len(provider.requests) != 1 || provider.requests[0].Model != "opus" {
		t.Fatalf("invocations = %#v, want one, straight to the family alias", provider.requests)
	}
	// Nothing new is written down: the entry that made this skip possible already
	// said it, and a second would announce the same fallback again.
	if recorded, _ := windows.List(); len(recorded) != 1 {
		t.Fatalf("List() = %#v, want the one entry that was already there", recorded)
	}
}

// And the pin is asked for again once that interval has passed. A version that
// was skipped once and then forever would be a floating alias the operator
// believes is a pin.
func TestAMissingVersionIsAskedForAgainOnceTheIntervalHasPassed(t *testing.T) {
	t.Parallel()

	windows := newTestWindows(t)
	recordWindow(t, windows, runstate.UsageLimitExhaustion{
		Model:        "claude-opus-5-20260401",
		ServedBy:     "opus",
		Substitution: runstate.SubstitutedForAvailability,
	})
	provider := &fakeProvider{results: []backend.RunResult{{FinalText: "decided"}}}
	later := func() time.Time { return fixedNow().Add(2 * time.Hour) }
	_, served, err := Serve(context.Background(), provider, backend.RunRequest{Model: "opus"}, Policy{
		Version:           "claude-opus-5-20260401",
		Windows:           windows,
		Now:               later,
		UnknownResetPause: 30 * time.Minute,
		ProductID:         "yoyodyne",
		Waiting:           "the architect conversation",
	})
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if served.Model != "claude-opus-5-20260401" || served.Substituted() {
		t.Fatalf("served = %#v, want the pin asked for again once the interval passed", served)
	}
	if len(provider.requests) != 1 || provider.requests[0].Model != "claude-opus-5-20260401" {
		t.Fatalf("invocations = %#v, want one, under the pinned version", provider.requests)
	}
}

// An availability entry is not a closed window, and failover must not read it as
// one. Two agents can name the same selector — one pinning it, one configured
// for it — and reading one's missing version as the other's exhausted account
// would move a turn nothing had refused.
func TestAMissingVersionIsNotReadAsAClosedCapacityWindow(t *testing.T) {
	t.Parallel()

	windows := newTestWindows(t)
	recordWindow(t, windows, runstate.UsageLimitExhaustion{
		Model:        "claude-opus-5-20260401",
		ServedBy:     "opus",
		Substitution: runstate.SubstitutedForAvailability,
	})
	provider := &fakeProvider{results: []backend.RunResult{{FinalText: "decided"}}}
	_, served, err := Serve(context.Background(), provider, backend.RunRequest{Model: "claude-opus-5-20260401"}, Policy{
		Alternate:         "fable",
		Windows:           windows,
		Now:               fixedNow,
		UnknownResetPause: 30 * time.Minute,
		ProductID:         "yoyodyne",
		Waiting:           "the architect conversation",
	})
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if served.Model != "claude-opus-5-20260401" || served.Substituted() {
		t.Fatalf("served = %#v, want the configured model asked, not the alternate", served)
	}
	if len(provider.requests) != 1 || provider.requests[0].Model != "claude-opus-5-20260401" {
		t.Fatalf("invocations = %#v, want one, under the configured model", provider.requests)
	}
}

// And the other way round: a closed capacity window against a selector is not a
// reason to stop asking for a pin that happens to name it.
func TestAClosedCapacityWindowIsNotReadAsAMissingVersion(t *testing.T) {
	t.Parallel()

	windows := newTestWindows(t)
	recordWindow(t, windows, runstate.UsageLimitExhaustion{
		Model:    "claude-opus-5-20260401",
		ServedBy: "fable",
		ResetsAt: pointerTo(fixedNow().Add(time.Hour)),
	})
	provider := &fakeProvider{results: []backend.RunResult{{FinalText: "decided"}}}
	_, served, err := Serve(context.Background(), provider, backend.RunRequest{Model: "opus"}, Policy{
		Version:   "claude-opus-5-20260401",
		Windows:   windows,
		Now:       fixedNow,
		ProductID: "yoyodyne",
		Waiting:   "the architect conversation",
	})
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if served.Model != "claude-opus-5-20260401" {
		t.Fatalf("served = %#v, want the pin still asked for", served)
	}
}

// A pin the provider has but has no capacity for is failover's refusal, not the
// fallback's: the permitted alternate answers it, exactly as it would have had
// nothing been pinned. Falling back to the family alias would buy nothing — the
// alias floats over the same family, so it is inside the same window — and
// returning the refusal would be an agent losing failover by pinning a version,
// which is the ordinary reason failover exists.
func TestAPinnedVersionWithNoCapacityIsServedByThePermittedAlternate(t *testing.T) {
	t.Parallel()

	resetsAt := fixedNow().Add(time.Hour)
	provider := &fakeProvider{results: []backend.RunResult{
		refused("five_hour", resetsAt),
		{SessionID: "session-1", FinalText: "decided"},
	}}
	windows := newTestWindows(t)
	result, served, err := Serve(context.Background(), provider, backend.RunRequest{Model: "opus"}, Policy{
		Version:   "claude-opus-5-20260401",
		Alternate: "fable",
		Windows:   windows,
		Now:       fixedNow,
		ProductID: "yoyodyne",
		Waiting:   "the architect conversation",
	})
	if err != nil {
		t.Fatalf("Serve() error = %v, want the turn served by the alternate", err)
	}
	if result.FinalText != "decided" {
		t.Fatalf("final text = %q, want the answer the alternate gave", result.FinalText)
	}
	if served.Model != "fable" || served.Refused != "claude-opus-5-20260401" {
		t.Fatalf("served = %#v, want the alternate serving for the pinned version", served)
	}
	if served.Why != runstate.SubstitutedForCapacity {
		t.Fatalf("why = %q, want capacity; the provider had the version and no room for it", served.Why)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("invocations = %d, want the refused version and the alternate that served", len(provider.requests))
	}
	// The family alias is never asked. It floats over the family whose window just
	// closed, so an attempt at it is one more refusal on the operator's money.
	if provider.requests[0].Model != "claude-opus-5-20260401" || provider.requests[1].Model != "fable" {
		t.Fatalf("models asked = %q then %q, want the pin first and the alternate second",
			provider.requests[0].Model, provider.requests[1].Model)
	}

	recorded, err := windows.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 1 {
		t.Fatalf("List() = %#v, want the substitution recorded once", recorded)
	}
	entry := recorded[0]
	if entry.Model != "claude-opus-5-20260401" || entry.ServedBy != "fable" {
		t.Fatalf("recorded = %#v, want the selector that was refused and the one that served", entry)
	}
	if entry.Reason() != runstate.SubstitutedForCapacity {
		t.Fatalf("recorded reason = %q, want capacity", entry.Reason())
	}
	if entry.ResetsAt == nil || !entry.ResetsAt.Equal(resetsAt) {
		t.Fatalf("resets at = %v, want the provider's own reset time", entry.ResetsAt)
	}
}

// And the window that opened over the pinned selector is read back, so the next
// turn goes straight to the alternate rather than paying a refused invocation to
// rediscover a window the harness watched close.
func TestAClosedWindowOverThePinnedVersionSendsTheNextTurnStraightToTheAlternate(t *testing.T) {
	t.Parallel()

	windows := newTestWindows(t)
	recordWindow(t, windows, runstate.UsageLimitExhaustion{
		Model:    "claude-opus-5-20260401",
		ServedBy: "fable",
		ResetsAt: pointerTo(fixedNow().Add(time.Hour)),
	})
	provider := &fakeProvider{results: []backend.RunResult{{FinalText: "decided"}}}
	_, served, err := Serve(context.Background(), provider, backend.RunRequest{Model: "opus"}, Policy{
		Version:   "claude-opus-5-20260401",
		Alternate: "fable",
		Windows:   windows,
		Now:       fixedNow,
		ProductID: "yoyodyne",
		Waiting:   "the architect conversation",
	})
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if served.Model != "fable" || served.Why != runstate.SubstitutedForCapacity {
		t.Fatalf("served = %#v, want the alternate the standing window points at", served)
	}
	if len(provider.requests) != 1 || provider.requests[0].Model != "fable" {
		t.Fatalf("invocations = %#v, want one, straight to the alternate", provider.requests)
	}
}

// A refusal met at the alternate after it had already taken the turn is about
// the alternate, which is a model nobody pinned. The family alias is no answer
// to it, so it is handed back rather than fallen back from.
func TestAMissingModelAtTheAlternateIsNotFallenBackFromAsThePin(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{results: []backend.RunResult{
		refused("five_hour", fixedNow().Add(time.Hour)),
		missingModel("model: fable"),
	}}
	windows := newTestWindows(t)
	_, served, err := Serve(context.Background(), provider, backend.RunRequest{Model: "opus"}, Policy{
		Version:   "claude-opus-5-20260401",
		Alternate: "fable",
		Windows:   windows,
		Now:       fixedNow,
		ProductID: "yoyodyne",
		Waiting:   "the architect conversation",
	})
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("invocations = %d, want the refused version and the alternate, and no third", len(provider.requests))
	}
	if served.Model != "fable" || served.Why != runstate.SubstitutedForCapacity {
		t.Fatalf("served = %#v, want the alternate's own refusal handed back", served)
	}
	// The capacity hop records itself, as it does for any turn the alternate was
	// reached for and did not itself run out of capacity on. What must not be
	// there is an availability entry: nothing said the pinned version was missing,
	// and one written here would stop the next turn asking for it.
	recorded, err := windows.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	for _, entry := range recorded {
		if entry.Reason() == runstate.SubstitutedForAvailability {
			t.Fatalf("recorded = %#v, want the alternate's missing model not written down as the pin's", entry)
		}
	}
}

// The two mechanisms compose in one order where both fire: the pin falls back to
// the family alias because the provider has not got it, and the alias then fails
// over on capacity exactly as it would have had nothing been pinned. Each hop
// records itself, because one entry collapsing both would name a model that
// refused a turn nobody asked it.
func TestAPinnedVersionFallsBackAndThenFailsOverOnCapacity(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{results: []backend.RunResult{
		missingModel("model: claude-opus-5-20260401"),
		refused("five_hour", fixedNow().Add(time.Hour)),
		{FinalText: "decided"},
	}}
	windows := newTestWindows(t)
	_, served, err := Serve(context.Background(), provider, backend.RunRequest{Model: "opus"}, Policy{
		Version:   "claude-opus-5-20260401",
		Alternate: "fable",
		Windows:   windows,
		Now:       fixedNow,
		ProductID: "yoyodyne",
		Waiting:   "the architect conversation",
	})
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if served.Model != "fable" || served.Refused != "opus" || served.Why != runstate.SubstitutedForCapacity {
		t.Fatalf("served = %#v, want the alternate, reported as the hop that decided the model", served)
	}
	asked := []string{provider.requests[0].Model, provider.requests[1].Model, provider.requests[2].Model}
	if asked[0] != "claude-opus-5-20260401" || asked[1] != "opus" || asked[2] != "fable" {
		t.Fatalf("models asked = %v, want the pin, then the family alias, then the alternate", asked)
	}

	recorded, err := windows.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 2 {
		t.Fatalf("List() = %#v, want both hops recorded", recorded)
	}
	var availability, capacity int
	for _, entry := range recorded {
		switch entry.Reason() {
		case runstate.SubstitutedForAvailability:
			availability++
			if entry.Model != "claude-opus-5-20260401" || entry.ServedBy != "opus" {
				t.Fatalf("availability entry = %#v, want the pin falling back to the family alias", entry)
			}
		case runstate.SubstitutedForCapacity:
			capacity++
			if entry.Model != "opus" || entry.ServedBy != "fable" {
				t.Fatalf("capacity entry = %#v, want the alias failing over to the alternate", entry)
			}
		}
	}
	if availability != 1 || capacity != 1 {
		t.Fatalf("recorded = %#v, want one hop of each kind", recorded)
	}
}

// A pinned turn that failed for anything other than the provider lacking the
// model is that failure, under the version that was asked. Reading every failure
// as a reason to try another model would be routing rather than a fallback.
func TestAPinnedTurnThatFailedForAnythingElseIsNotFallenBackFrom(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{results: []backend.RunResult{
		{IsError: true, StopReason: "error", TransientFailure: &backend.TransientFailure{Detail: "connection closed"}},
	}}
	windows := newTestWindows(t)
	_, served, err := Serve(context.Background(), provider, backend.RunRequest{Model: "opus"}, Policy{
		Version:   "claude-opus-5-20260401",
		Windows:   windows,
		Now:       fixedNow,
		ProductID: "yoyodyne",
		Waiting:   "the architect conversation",
	})
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if served.Model != "claude-opus-5-20260401" || served.Substituted() {
		t.Fatalf("served = %#v, want the failure handed back under the version that was asked", served)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("invocations = %d, want exactly one", len(provider.requests))
	}
	if recorded, _ := windows.List(); len(recorded) != 0 {
		t.Fatalf("List() = %#v, want nothing recorded for a turn nothing was substituted for", recorded)
	}
}

// A fallback the family alias could not take either is the failure the caller
// already handles. Recording it would put a turn nothing served in the log as
// one that carried on.
func TestAFallbackThatFailedTooIsNotRecordedAsHavingServed(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{results: []backend.RunResult{
		missingModel("model: claude-opus-5-20260401"),
		refused("five_hour", time.Time{}),
	}}
	windows := newTestWindows(t)
	_, served, err := Serve(context.Background(), provider, backend.RunRequest{Model: "opus"}, Policy{
		Version:   "claude-opus-5-20260401",
		Windows:   windows,
		Now:       fixedNow,
		ProductID: "yoyodyne",
		Waiting:   "the architect conversation",
	})
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if served.Model != "opus" || served.Refused != "claude-opus-5-20260401" {
		t.Fatalf("served = %#v, want the fallback reported even though it did not serve", served)
	}
	if recorded, _ := windows.List(); len(recorded) != 0 {
		t.Fatalf("List() = %#v, want nothing recorded for a turn nothing served", recorded)
	}
}

// The second attempt starts after the events the refused one already emitted, so
// two attempts at one turn write one log whose numbering still says what order
// things happened in.
func TestTheFallbackAttemptDoesNotRenumberThePinnedAttemptsEvents(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{results: []backend.RunResult{
		{IsError: true, StopReason: "api_error", ModelUnavailable: &backend.ModelUnavailable{Detail: "no such model"}, LastEvent: 11},
		{FinalText: "decided", LastEvent: 14},
	}}
	if _, _, err := Serve(context.Background(), provider, backend.RunRequest{Model: "opus", LastSequence: 4}, Policy{
		Version:   "claude-opus-5-20260401",
		Windows:   newTestWindows(t),
		Now:       fixedNow,
		ProductID: "yoyodyne",
		Waiting:   "the architect conversation",
	}); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if provider.requests[1].LastSequence != 11 {
		t.Fatalf("the fallback attempt started at sequence %d, want it after the %d the pinned one reached",
			provider.requests[1].LastSequence, provider.requests[0].LastSequence)
	}
}

// A turn the family alias has already answered is not thrown away because the
// log would not take the line. The answer comes back and the write failure is
// handed to whoever wired one.
func TestAFallbackThatCannotBeRecordedStillServesTheTurn(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{results: []backend.RunResult{
		missingModel("no such model"),
		{FinalText: "decided"},
	}}
	var reported error
	result, served, err := Serve(context.Background(), provider, backend.RunRequest{Model: "opus"}, Policy{
		Version:       "claude-opus-5-20260401",
		Windows:       refusingWindows{},
		Now:           fixedNow,
		ProductID:     "yoyodyne",
		Waiting:       "the architect conversation",
		RecordFailure: func(err error) { reported = err },
	})
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if result.FinalText != "decided" || served.Model != "opus" {
		t.Fatalf("result = %#v, served = %#v, want the turn the alias answered", result, served)
	}
	if reported == nil {
		t.Fatal("the write failure reached nobody, and a substitution nothing recorded is one nothing will say")
	}
}

// A caller with nowhere to report a failed write is handed it joined onto the
// turn rather than losing it.
func TestAFallbackWriteFailureWithNowhereToGoIsJoinedOntoTheTurn(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{results: []backend.RunResult{
		missingModel("no such model"),
		{FinalText: "decided"},
	}}
	_, _, err := Serve(context.Background(), provider, backend.RunRequest{Model: "opus"}, Policy{
		Version:   "claude-opus-5-20260401",
		Windows:   refusingWindows{},
		Now:       fixedNow,
		ProductID: "yoyodyne",
		Waiting:   "the architect conversation",
	})
	if err == nil {
		t.Fatal("Serve() error = nil, want the write failure carried back to a caller with nowhere else to put it")
	}
}

// A pin naming the model already in the request is nothing to fall back from, so
// it is one invocation exactly as an unpinned turn is.
func TestAPinNamingTheRequestsOwnModelChangesNothing(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{results: []backend.RunResult{{FinalText: "decided"}}}
	_, served, err := Serve(context.Background(), provider, backend.RunRequest{Model: "opus"}, Policy{
		Version:   "opus",
		Windows:   newTestWindows(t),
		Now:       fixedNow,
		ProductID: "yoyodyne",
		Waiting:   "the architect conversation",
	})
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if served.Model != "opus" || served.Substituted() {
		t.Fatalf("served = %#v, want one invocation under the model that was already named", served)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("invocations = %d, want exactly one", len(provider.requests))
	}
}

// A pin with a log that cannot be read costs one invocation rather than the
// turn: the version is asked for, and a fallback is still made if it is missing.
func TestAPinWhoseLogCannotBeReadStillServesTheTurn(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{results: []backend.RunResult{
		missingModel("no such model"),
		{FinalText: "decided"},
	}}
	var reported []error
	_, served, err := Serve(context.Background(), provider, backend.RunRequest{Model: "opus"}, Policy{
		Version:       "claude-opus-5-20260401",
		Windows:       refusingWindows{},
		Now:           fixedNow,
		ProductID:     "yoyodyne",
		Waiting:       "the architect conversation",
		RecordFailure: func(err error) { reported = append(reported, err) },
	})
	if err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	if served.Model != "opus" {
		t.Fatalf("served = %#v, want the family alias to have served", served)
	}
	if len(reported) < 2 {
		t.Fatalf("reported = %v, want both the unreadable log and the write that failed", reported)
	}
}

// missingModel is a provider refusing an attempt because it has not got the
// model the request named.
func missingModel(detail string) backend.RunResult {
	return backend.RunResult{
		IsError:          true,
		StopReason:       "api_error",
		ModelUnavailable: &backend.ModelUnavailable{Detail: detail},
	}
}
