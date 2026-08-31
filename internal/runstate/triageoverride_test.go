package runstate

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// overrideCaps are the harness defaults the deadlock was met at: four review
// rounds, one grant, one re-run, two re-arms.
var overrideCaps = TriageCaps{ReviewRounds: 4, RepairGrants: 1, Reruns: 1, MergeRearms: 2}

// spendRounds puts an item past its round cap the only way anything can: by
// producing reviewer verdicts, which are counted whatever a cap says. Each round
// names its own attempt, because a repeat of the attempt at the head of the
// record counts once.
func spendRounds(t *testing.T, store *TriageStore, workItemID string, from, to int) {
	t.Helper()
	for round := from; round < to; round++ {
		if _, err := store.RecordReviewRound(context.Background(), workItemID, fmt.Sprintf("run-a#%d", round), countingProcess, time.Now()); err != nil {
			t.Fatalf("RecordReviewRound() error = %v", err)
		}
	}
}

// The deadlock this exists to end, replayed end to end. An item past its round
// cap could have no re-run recorded against it, which is what the re-run verb
// requires before it will start anything — so the operator's ruling on the
// escalation had no recorded path to take at all. The override is that path: it
// is recorded, it names who decided and when, and the ledger that refused the
// decision then accepts it.
func TestAnOperatorOverrideCrossesTheRoundCapThatDeadlockedTheEscalation(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	ctx := context.Background()
	const item = "yoyodyne-ifd.143"
	spendRounds(t, store, item, 0, 5)

	// Where it stopped: five of four, and the development manager cannot record
	// the decision the operator is about to be asked to rule on.
	var refusal TriageCapError
	if _, err := store.RecordRerun(ctx, item, time.Now(), overrideCaps); !errors.As(err, &refusal) {
		t.Fatalf("RecordRerun() at 5 of 4 rounds error = %v, want a cap refusal", err)
	}
	if refusal.Budget != TriageReviewRoundBudget {
		t.Fatalf("RecordRerun() was refused by the %q budget, want the review round budget", refusal.Budget)
	}

	decided := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	counters, err := store.Override(ctx, item, TriageOverride{
		Budget:    TriageReviewRoundBudget,
		Cap:       8,
		DecidedBy: "mason",
		Reason:    "REBUILD: the rounds were spent against a base that had moved, and the work is worth running again",
	}, decided, overrideCaps)
	if err != nil {
		t.Fatalf("Override() error = %v", err)
	}
	if len(counters.Overrides) != 1 {
		t.Fatalf("Override() recorded %d override(s), want exactly the one decided", len(counters.Overrides))
	}
	recorded := counters.Overrides[0]
	if recorded.DecidedBy != "mason" || !recorded.DecidedAt.Equal(decided) {
		t.Fatalf("Override() recorded %+v, want it attributed to the operator who decided it and when", recorded)
	}
	if recorded.Cleared || recorded.Cap != 8 {
		t.Fatalf("Override() recorded %+v, want the raised ceiling rather than a clearing", recorded)
	}

	// The ledger now accepts the decision the escalation was about, which is the
	// whole of what the re-run verb requires before it starts anything.
	after, err := store.RecordRerun(ctx, item, time.Now(), overrideCaps)
	if err != nil {
		t.Fatalf("RecordRerun() past a recorded override error = %v, want it accepted", err)
	}
	if after.Reruns != 1 {
		t.Fatalf("RecordRerun() left %d re-run(s) recorded, want the one just decided", after.Reruns)
	}
}

// The caps still mean something past an override: what was crossed is one budget
// at one item, and the room it was given runs out again.
func TestAnOverrideRaisesOneBudgetAndLeavesTheRestRefusing(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	ctx := context.Background()
	const item = "yoyodyne-ifd.143"
	spendRounds(t, store, item, 0, 5)
	if _, err := store.Override(ctx, item, TriageOverride{
		Budget:    TriageReviewRoundBudget,
		Cap:       6,
		DecidedBy: "mason",
		Reason:    "one more run of it",
	}, time.Now(), overrideCaps); err != nil {
		t.Fatalf("Override() error = %v", err)
	}

	// The item's own re-run budget is untouched, so the second re-run is refused
	// exactly as it was before anybody crossed anything.
	if _, err := store.RecordRerun(ctx, item, time.Now(), overrideCaps); err != nil {
		t.Fatalf("RecordRerun() error = %v", err)
	}
	var refusal TriageCapError
	if _, err := store.RecordRerun(ctx, item, time.Now(), overrideCaps); !errors.As(err, &refusal) || refusal.Budget != TriageRerunBudget {
		t.Fatalf("second RecordRerun() error = %v, want the re-run budget refusing it", err)
	}

	// And the room the override gave runs out: six rounds spent is six of six.
	spendRounds(t, store, item, 5, 6)
	if _, err := store.GrantRepair(ctx, item, 1, time.Now(), overrideCaps); !errors.Is(err, ErrTriageCapReached) {
		t.Fatalf("GrantRepair() at the raised cap error = %v, want the raised cap refusing it in turn", err)
	}
}

// A cleared budget is the other half of what an operator may decide, and it is
// not a large number they have to guess at: nothing counted comes near it, so the
// budget simply stops refusing.
func TestAClearedBudgetStopsRefusing(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	ctx := context.Background()
	const item = "yoyodyne-ifd.143"
	counters, err := store.Override(ctx, item, TriageOverride{
		Budget:    TriageRerunBudget,
		Cleared:   true,
		DecidedBy: "mason",
		Reason:    "this item is being driven by hand until it lands",
	}, time.Now(), overrideCaps)
	if err != nil {
		t.Fatalf("Override() error = %v", err)
	}
	if got := overrideCaps.Overridden(counters.Overrides).Reruns; got != TriageCapCleared {
		t.Fatalf("Overridden() re-run cap = %d, want it cleared", got)
	}
	for taken := range 3 {
		if _, err := store.RecordRerun(ctx, item, time.Now(), overrideCaps); err != nil {
			t.Fatalf("RecordRerun() %d past a cleared cap error = %v", taken+1, err)
		}
	}
	// Clearing one budget clears one budget. The merge re-arms are still two.
	for taken := range 2 {
		if _, err := store.RecordMergeRearm(ctx, item, time.Now(), overrideCaps); err != nil {
			t.Fatalf("RecordMergeRearm() %d error = %v", taken+1, err)
		}
	}
	if _, err := store.RecordMergeRearm(ctx, item, time.Now(), overrideCaps); !errors.Is(err, ErrTriageCapReached) {
		t.Fatalf("RecordMergeRearm() past its own cap error = %v, want it still refused", err)
	}
}

// An override clears or raises and never lowers, so one that would leave the item
// no better off is refused rather than recorded. Both causes read identically
// from the outside — an override already recorded that did the same thing, and a
// configured cap already larger than the number typed — so the refusal names what
// the budget stands at as well as what was asked for.
func TestAnOverrideThatGivesNoMoreRoomIsRefused(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	ctx := context.Background()
	const item = "yoyodyne-ifd.143"
	raise := func(ceiling int) error {
		_, err := store.Override(ctx, item, TriageOverride{
			Budget:    TriageReviewRoundBudget,
			Cap:       ceiling,
			DecidedBy: "mason",
			Reason:    "more room",
		}, time.Now(), overrideCaps)
		return err
	}
	// Below the configured cap of four: this would lower it, which is a
	// configuration change rather than a decision about one item.
	if err := raise(2); !errors.Is(err, ErrTriageOverrideNotARaise) {
		t.Fatalf("Override() below the configured cap error = %v, want it refused as not a raise", err)
	}
	if err := raise(8); err != nil {
		t.Fatalf("Override() error = %v", err)
	}
	// And the same again, which is the ordinary shape of the mistake: an operator
	// recording the ruling twice.
	err := raise(8)
	if !errors.Is(err, ErrTriageOverrideNotARaise) {
		t.Fatalf("Override() repeating a recorded raise error = %v, want it refused", err)
	}
	if !strings.Contains(err.Error(), "already stands at 8") {
		t.Fatalf("Override() refusal = %q, want it to say what the budget already stands at", err)
	}
	counters, err := store.Counters(item)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if len(counters.Overrides) != 1 {
		t.Fatalf("Counters() carry %d override(s), want only the one that was accepted", len(counters.Overrides))
	}
}

// An override nobody is named for is exactly the thing this record exists to
// replace, so it is refused where it is written rather than recorded and left for
// a later reader to wonder about.
func TestAnUnattributedOrUnnamedOverrideIsRefused(t *testing.T) {
	t.Parallel()

	sound := TriageOverride{
		Budget:    TriageReviewRoundBudget,
		Cap:       8,
		DecidedBy: "mason",
		Reason:    "REBUILD",
	}
	for _, refused := range []struct {
		name   string
		change func(*TriageOverride)
		says   string
	}{
		{
			name:   "with nobody named",
			change: func(o *TriageOverride) { o.DecidedBy = "  " },
			says:   "names the operator",
		},
		{
			name:   "with no reason",
			change: func(o *TriageOverride) { o.Reason = "" },
			says:   "records why",
		},
		{
			name:   "of a budget nothing bounds",
			change: func(o *TriageOverride) { o.Budget = "review rounds" },
			says:   "is not a triage budget",
		},
		{
			name:   "that both clears and raises",
			change: func(o *TriageOverride) { o.Cleared = true },
			says:   "states no number",
		},
	} {
		t.Run(refused.name, func(t *testing.T) {
			t.Parallel()

			store := newTriageStore(t)
			override := sound
			refused.change(&override)
			_, err := store.Override(context.Background(), "yoyodyne-ifd.143", override, time.Now(), overrideCaps)
			if err == nil {
				t.Fatal("Override() recorded an override that is not one")
			}
			if !strings.Contains(err.Error(), refused.says) {
				t.Fatalf("Override() error = %q, want it to say %q", err, refused.says)
			}
			counters, err := store.Counters("yoyodyne-ifd.143")
			if err != nil {
				t.Fatalf("Counters() error = %v", err)
			}
			if len(counters.Overrides) != 0 {
				t.Fatalf("Counters() carry %d override(s) after a refusal, want none recorded", len(counters.Overrides))
			}
		})
	}
}

// The override outlives the terminal it was typed at, for the reason every other
// figure on this record does: the decision it crosses a cap for is carried out by
// a later process, and a cap crossed only in the session that crossed it is a cap
// nobody crossed.
func TestAnOverrideSurvivesTheProcessThatRecordedIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	first, err := NewTriageStore(root, domain.ProductID("yoyodyne"))
	if err != nil {
		t.Fatalf("NewTriageStore() error = %v", err)
	}
	decided := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	if _, err := first.Override(context.Background(), "yoyodyne-ifd.143", TriageOverride{
		Budget:    TriageReviewRoundBudget,
		Cap:       8,
		DecidedBy: "mason",
		Reason:    "REBUILD, ruled on the escalation of 2026-08-30",
	}, decided, overrideCaps); err != nil {
		t.Fatalf("Override() error = %v", err)
	}

	second, err := NewTriageStore(root, domain.ProductID("yoyodyne"))
	if err != nil {
		t.Fatalf("NewTriageStore() error = %v", err)
	}
	counters, err := second.Counters("yoyodyne-ifd.143")
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	override, found := counters.OverrideOf(TriageReviewRoundBudget)
	if !found {
		t.Fatalf("Counters() = %+v, want the recorded override read back", counters)
	}
	if override.Cap != 8 || override.DecidedBy != "mason" || !override.DecidedAt.Equal(decided) {
		t.Fatalf("OverrideOf() = %+v, want the decision as it was recorded", override)
	}
	if got := overrideCaps.Overridden(counters.Overrides).ReviewRounds; got != 8 {
		t.Fatalf("Overridden() review round cap = %d, want the ceiling the operator recorded", got)
	}
}

// Overrides accumulate, and the last word on a budget is the one in force. It is
// also the largest, because nothing else can be recorded — which is what lets
// every reader take the last one without sorting anything.
func TestTheLastOverrideOfABudgetIsTheOneInForce(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	ctx := context.Background()
	const item = "yoyodyne-ifd.143"
	for _, ceiling := range []int{6, 8, 12} {
		if _, err := store.Override(ctx, item, TriageOverride{
			Budget:    TriageReviewRoundBudget,
			Cap:       ceiling,
			DecidedBy: "mason",
			Reason:    "still worth another go",
		}, time.Now(), overrideCaps); err != nil {
			t.Fatalf("Override() to %d error = %v", ceiling, err)
		}
	}
	counters, err := store.Counters(item)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if len(counters.Overrides) != 3 {
		t.Fatalf("Counters() carry %d override(s), want every one recorded rather than the last replacing them", len(counters.Overrides))
	}
	if got := overrideCaps.Overridden(counters.Overrides).ReviewRounds; got != 12 {
		t.Fatalf("Overridden() review round cap = %d, want the last and largest", got)
	}
}

// An override is a raise measured against what stood when it was recorded, and
// what stood is a configured cap that moves afterwards. So the ceiling in force
// is the larger of the two rather than the override: raise
// triage.review_rounds_cap over an item already carrying an override and an
// assignment would drop that one item below every other, which is an override
// lowering a cap — reported by every view as a raise, and the one thing this
// record is not allowed to do.
func TestAConfiguredRaisePastAnOverrideDoesNotLowerThatItemsCap(t *testing.T) {
	t.Parallel()

	store := newTriageStore(t)
	ctx := context.Background()
	const item = "yoyodyne-ifd.143"
	spendRounds(t, store, item, 0, 5)
	if _, err := store.Override(ctx, item, TriageOverride{
		Budget:    TriageReviewRoundBudget,
		Cap:       8,
		DecidedBy: "mason",
		Reason:    "REBUILD",
	}, time.Now(), overrideCaps); err != nil {
		t.Fatalf("Override() error = %v", err)
	}
	counters, err := store.Counters(item)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}

	// The project then decides four rounds was mean and makes it ten. This item
	// gets ten like every other, not the eight somebody once gave it.
	raised := overrideCaps
	raised.ReviewRounds = 10
	if got := raised.Overridden(counters.Overrides).ReviewRounds; got != 10 {
		t.Fatalf("Overridden() review round cap = %d, want the configured ceiling where it now stands above the override", got)
	}
	// And the guards agree, which is the half that matters: the item has spent
	// five, so it has room under ten and would have none under eight-as-a-ceiling
	// only if the override had been allowed to lower anything.
	if _, err := store.GrantRepair(ctx, item, 4, time.Now(), raised); err != nil {
		t.Fatalf("GrantRepair() under the raised configured cap error = %v", err)
	}

	// The override still raises where the configured cap is below it, which is the
	// case it was recorded for.
	if got := overrideCaps.Overridden(counters.Overrides).ReviewRounds; got != 8 {
		t.Fatalf("Overridden() review round cap = %d, want the override where it stands above the configured ceiling", got)
	}
}

// A record whose overrides do not strictly increase could not have been written
// by the only thing that writes them, so it is refused where it is read rather
// than obeyed. These are the figures the guards act on, and a hand-edited record
// that quietly lowered a cap would refuse decisions with nothing saying why.
func TestARecordWhoseOverridesLowerACapIsRefused(t *testing.T) {
	t.Parallel()

	decided := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	counters := TriageCounters{
		SchemaVersion: TriageCountersSchemaVersion,
		ProductID:     domain.ProductID("yoyodyne"),
		WorkItemID:    "yoyodyne-ifd.143",
		UpdatedAt:     decided,
		Overrides: []TriageOverride{
			{Budget: TriageReviewRoundBudget, Cap: 8, DecidedBy: "mason", DecidedAt: decided, Reason: "REBUILD"},
			{Budget: TriageReviewRoundBudget, Cap: 5, DecidedBy: "mason", DecidedAt: decided, Reason: "second thoughts"},
		},
	}
	err := counters.Validate()
	if err == nil {
		t.Fatal("Validate() accepted a record whose overrides lower a cap")
	}
	if !strings.Contains(err.Error(), "may only raise one") {
		t.Fatalf("Validate() error = %q, want it to name the rule that was broken", err)
	}
}
