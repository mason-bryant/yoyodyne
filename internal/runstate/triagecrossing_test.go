package runstate

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// The week the delegation was argued from, replayed as the harness now takes it.
// Every one of those overrides was a development manager refused past a cap, an
// escalation, an operator granting it within minutes, and a decision recorded
// afterwards. The operator step was latency rather than judgement, so the
// crossing is his: the same refusal, the same cap crossed by one, and the same
// decision recorded — with nobody waited on and the argument on the record.
func TestTheDevelopmentManagerCrossesTheCapTheOperatorWasBeingWokenFor(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	ctx := context.Background()
	const item = "yoyodyne-ifd.143"
	spendRounds(t, store, item, 0, 4)

	// Where every one of those ceremonies started: the decision refused, by the
	// round budget, at the cap the project configured.
	var refusal TriageCapError
	if _, err := store.RecordRerun(ctx, item, time.Now(), overrideCaps); !errors.As(err, &refusal) {
		t.Fatalf("RecordRerun() at 4 of 4 rounds error = %v, want a cap refusal", err)
	}
	rounds, refused := refusal.RefusedBy(TriageReviewRoundBudget)
	if !refused {
		t.Fatalf("RecordRerun() was refused by %v, want the review round budget", refusal.Budgets())
	}

	crossed := time.Date(2026, 9, 6, 9, 0, 0, 0, time.UTC)
	crossing, err := store.CrossCap(ctx, item, domain.RoleDevelopmentManager, TriageReviewRoundBudget,
		"the change was right and the ground moved under it; the rounds went on a base that has since landed", crossed, overrideCaps)
	if err != nil {
		t.Fatalf("CrossCap() error = %v", err)
	}
	// One step, which is exactly what the refusal said would permit the decision:
	// an unbounded raise is not delegated and is not what this gives.
	if crossing.Cap != rounds.Permits() || crossing.Cap != overrideCaps.ReviewRounds+1 {
		t.Fatalf("CrossCap() raised the cap to %d, want the %d the refusal said would permit it", crossing.Cap, rounds.Permits())
	}
	if crossing.Number != 1 || crossing.Bound != MaxDelegatedCapCrossings {
		t.Fatalf("CrossCap() = crossing %d of %d, want the first of %d", crossing.Number, crossing.Bound, MaxDelegatedCapCrossings)
	}

	// The reason and the role are on the item's own record, which is what makes
	// the crossing answerable after the channel message has scrolled away.
	recorded, found := crossing.Counters.OverrideOf(TriageReviewRoundBudget)
	if !found {
		t.Fatalf("counters carry no override of the review round budget: %+v", crossing.Counters.Overrides)
	}
	if recorded.CrossedBy != domain.RoleDevelopmentManager || !recorded.Delegated() {
		t.Fatalf("the recorded crossing was crossed by %q, want the development manager", recorded.CrossedBy)
	}
	if !strings.Contains(recorded.Reason, "the ground moved under it") {
		t.Fatalf("the recorded crossing carries the reason %q", recorded.Reason)
	}
	if !recorded.DecidedAt.Equal(crossed) {
		t.Fatalf("the recorded crossing is dated %s, want %s", recorded.DecidedAt, crossed)
	}
	if described := recorded.Describe(); !strings.Contains(described, "on delegated authority") {
		t.Fatalf("the crossing describes itself as %q, which does not say whose authority it was", described)
	}

	// And the decision the ceremony existed for is now recordable, by the same
	// call that was refused a moment ago.
	if _, err := store.RecordRerun(ctx, item, crossed, overrideCaps); err != nil {
		t.Fatalf("RecordRerun() past the crossing error = %v, want the decision the crossing permits", err)
	}
}

// Five is the delegation and the sixth is the escalation. The refusal names the
// operator's command rather than only saying no, because an item that has been
// crossed five times is one where something other than the budget is wrong and
// the next reader has to be a person.
func TestASixthCrossingOfOneItemIsRefusedAndNamesTheOperatorAsThePath(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	ctx := context.Background()
	const item = "yoyodyne-ifd.209.20"
	at := time.Date(2026, 9, 6, 9, 0, 0, 0, time.UTC)

	for crossing := 1; crossing <= MaxDelegatedCapCrossings; crossing++ {
		recorded, err := store.CrossCap(ctx, item, domain.RoleDevelopmentManager, TriageReviewRoundBudget,
			"the findings are narrow and the change is preserved", at, overrideCaps)
		if err != nil {
			t.Fatalf("CrossCap() %d error = %v", crossing, err)
		}
		if recorded.Number != crossing {
			t.Fatalf("CrossCap() %d reported crossing %d", crossing, recorded.Number)
		}
	}

	var spent TriageCrossingError
	_, err := store.CrossCap(ctx, item, domain.RoleDevelopmentManager, TriageReviewRoundBudget,
		"one more would settle it", at, overrideCaps)
	if !errors.As(err, &spent) || !errors.Is(err, ErrTriageCrossingsSpent) {
		t.Fatalf("the sixth CrossCap() error = %v, want the delegated bound refusing it", err)
	}
	if !strings.Contains(err.Error(), "yoyo triage override") {
		t.Fatalf("the sixth CrossCap() error does not name the operator's path:\n%s", err)
	}
	if spent.Crossings != MaxDelegatedCapCrossings || spent.Bound != MaxDelegatedCapCrossings {
		t.Fatalf("the refusal reports %d of %d, want %d of %d", spent.Crossings, spent.Bound, MaxDelegatedCapCrossings, MaxDelegatedCapCrossings)
	}

	// Nothing was recorded, which is what makes a refusal free: the item stands at
	// the five it had, and the cap where the fifth left it.
	counters, err := store.Counters(item)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if counters.DelegatedCrossings() != MaxDelegatedCapCrossings {
		t.Fatalf("the item carries %d crossings after the refusal, want %d", counters.DelegatedCrossings(), MaxDelegatedCapCrossings)
	}

	// And the operator's own hand still crosses it, which is the whole of what the
	// bound hands back to them.
	if _, err := store.Override(ctx, item, TriageOverride{
		Budget:    TriageReviewRoundBudget,
		Cap:       overrideCaps.ReviewRounds + MaxDelegatedCapCrossings + 1,
		DecidedBy: "mason",
		Reason:    "driven by hand until it lands",
	}, at, overrideCaps); err != nil {
		t.Fatalf("Override() past the delegated bound error = %v, want the operator's path still open", err)
	}
}

// A crossing nobody argued for is refused outright rather than recorded weakly.
// The justification is the condition the delegation was granted under: it is what
// lands on the item and what reaches the operator, so a crossing without one is
// the delegation with its condition quietly dropped.
func TestACrossingWithNoJustificationIsRefusedOutright(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	ctx := context.Background()
	const item = "yoyodyne-ifd.272"
	at := time.Date(2026, 9, 6, 9, 0, 0, 0, time.UTC)

	for _, reason := range []string{"", "   ", "\n\t "} {
		_, err := store.CrossCap(ctx, item, domain.RoleDevelopmentManager, TriageRepairGrantBudget, reason, at, overrideCaps)
		if !errors.Is(err, ErrTriageCrossingUnjustified) {
			t.Fatalf("CrossCap() with reason %q error = %v, want it refused for want of a justification", reason, err)
		}
	}
	counters, err := store.Counters(item)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if len(counters.Overrides) != 0 {
		t.Fatalf("a refused crossing recorded %+v", counters.Overrides)
	}
}

// The delegation is one role's and one step's. Anything else asking for it is a
// caller reaching for the bounded path through a door that is not there, and the
// store refuses rather than recording something no reader could attribute.
func TestOnlyTheDevelopmentManagerCrossesACapAndOnlyByOneStep(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	ctx := context.Background()
	const item = "yoyodyne-ifd.279"
	at := time.Date(2026, 9, 6, 9, 0, 0, 0, time.UTC)

	for _, role := range []domain.AgentRole{domain.RoleDeveloper, domain.RoleReviewer, domain.RoleProductManager, ""} {
		if _, err := store.CrossCap(ctx, item, role, TriageRerunBudget, "because", at, overrideCaps); err == nil {
			t.Fatalf("CrossCap() as %q was permitted, want only the development manager", role)
		}
	}
	if _, err := store.CrossCap(ctx, item, domain.RoleDevelopmentManager, "rounds", "because", at, overrideCaps); err == nil {
		t.Fatalf("CrossCap() of a budget nothing bounds was permitted")
	}

	// A delegated crossing arriving through the operator's own verb is refused
	// there too, so the bounded path cannot be taken through the unbounded one.
	if _, err := store.Override(ctx, item, TriageOverride{
		Budget:    TriageRerunBudget,
		Cap:       9,
		DecidedBy: "the development manager",
		Reason:    "because",
		CrossedBy: domain.RoleDevelopmentManager,
	}, at, overrideCaps); err == nil {
		t.Fatalf("Override() carrying a delegated crossing was permitted, want it refused")
	}

	// And a record hand-edited past the bound is refused as it is read, because
	// these are the figures the guards obey.
	crossings := make([]TriageOverride, 0, MaxDelegatedCapCrossings+1)
	for step := 1; step <= MaxDelegatedCapCrossings+1; step++ {
		crossings = append(crossings, TriageOverride{
			Budget:    TriageReviewRoundBudget,
			Cap:       overrideCaps.ReviewRounds + step,
			DecidedBy: domain.RoleDevelopmentManager.Title(),
			DecidedAt: at,
			Reason:    "because",
			CrossedBy: domain.RoleDevelopmentManager,
		})
	}
	problems := errors.Join(validateTriageOverrides(crossings)...)
	if problems == nil || !strings.Contains(problems.Error(), "delegated authority") {
		t.Fatalf("validateTriageOverrides() over %d crossings = %v, want the bound refusing the record", len(crossings), problems)
	}

	// A crossing that names a cleared budget is not a step and is refused as one.
	cleared := TriageOverride{
		Budget:    TriageReviewRoundBudget,
		Cleared:   true,
		DecidedBy: domain.RoleDevelopmentManager.Title(),
		DecidedAt: at,
		Reason:    "because",
		CrossedBy: domain.RoleDevelopmentManager,
	}
	if err := cleared.Validate(); err == nil || !strings.Contains(err.Error(), "never clears one") {
		t.Fatalf("a delegated crossing that clears a budget validated as %v", err)
	}
}
