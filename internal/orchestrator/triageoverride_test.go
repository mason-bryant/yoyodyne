package orchestrator

import (
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/triage"
)

var overrideDecidedAt = time.Date(2026, 8, 19, 9, 0, 0, 0, time.UTC)

// crossedItem is one item's triage record with the operator's decisions on it, as
// the docket and the re-run both read it.
func crossedItem(rounds int, overrides ...runstate.TriageOverride) *recordedDecisions {
	return &recordedDecisions{
		counters: map[string]runstate.TriageCounters{docketedItem: {
			ReviewRounds: rounds,
			Overrides:    overrides,
		}},
	}
}

func crossing(budget string, ceiling int, by string) runstate.TriageOverride {
	return runstate.TriageOverride{
		Budget:    budget,
		Cap:       ceiling,
		DecidedBy: by,
		DecidedAt: overrideDecidedAt,
		Reason:    "REBUILD: the rounds went on a base that had moved",
	}
}

// A docket entry is where the development manager meets the cap, so it is where
// the operator's crossing of it has to be visible. Both halves are the point: the
// ceiling the guards will actually refuse against, and the account of why it is
// larger than the project configured — a budget with room the configuration does
// not explain, and nothing saying where the room came from, is a number a
// development manager has to decide whether to trust.
func TestADocketEntryReportsTheCrossedCeilingAndWhoCrossedIt(t *testing.T) {
	t.Parallel()

	recorded := crossedItem(5, crossing(runstate.TriageReviewRoundBudget, 8, "mason"))
	built, err := docketerDeciding([]runstate.State{stoppedState()}, &memoryDocket{}, recorded, recorded).Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(built.Entries) != 1 {
		t.Fatalf("build = %#v, want the one stoppage docketed", built)
	}
	entry := built.Entries[0]
	// The configured cap is 4 and the item has spent 5. Without the override this
	// entry reads as an item nothing may be granted or re-run.
	if entry.Counters.ReviewRoundsCap != 8 {
		t.Fatalf("review round cap = %d, want the ceiling the operator recorded", entry.Counters.ReviewRoundsCap)
	}
	if entry.Counters.Exhausted() {
		t.Fatalf("counters = %#v, want an item with room again rather than one past its cap", entry.Counters)
	}
	if len(entry.Overrides) != 1 || entry.Overrides[0].DecidedBy != "mason" || entry.Overrides[0].Cap != 8 {
		t.Fatalf("overrides = %#v, want the operator's decision carried onto the entry", entry.Overrides)
	}
	rendered := entry.Render()
	for _, want := range []string{
		"5 of 8 review round(s) used",
		"Operator override: raised the review round cap to 8, decided by mason at 2026-08-19T09:00:00Z",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("the entry does not say %q:\n%s", want, rendered)
		}
	}
}

// A cleared budget is a number nothing counted comes near, so the entry says what
// it is rather than printing it: a development manager reading a ceiling of
// 9223372036854775807 has to decode a line before they can act on it.
func TestADocketEntryRendersAClearedCapAsOneRatherThanAsANumber(t *testing.T) {
	t.Parallel()

	cleared := runstate.TriageOverride{
		Budget:    runstate.TriageRerunBudget,
		Cleared:   true,
		DecidedBy: "mason",
		DecidedAt: overrideDecidedAt,
		Reason:    "driven by hand until it lands",
	}
	recorded := crossedItem(3, cleared)
	built, err := docketerDeciding([]runstate.State{stoppedState()}, &memoryDocket{}, recorded, recorded).Build()
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	entry := built.Entries[0]
	if entry.Counters.RerunsCap != runstate.TriageCapCleared {
		t.Fatalf("re-run cap = %d, want it cleared", entry.Counters.RerunsCap)
	}
	rendered := entry.Render()
	for _, want := range []string{
		"0 of no cap re-run(s)",
		"Operator override: cleared the re-run cap, decided by mason",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("the entry does not say %q:\n%s", want, rendered)
		}
	}
	// The budgets nobody crossed are still the configured ones. Clearing one is
	// clearing one.
	if entry.Counters.ReviewRoundsCap != 4 || entry.Counters.RepairGrantsCap != 1 {
		t.Fatalf("counters = %#v, want every other ceiling left where the project put it", entry.Counters)
	}
}

// The re-run's recorded reason is where the operator's crossing survives the
// conversation it was decided in, so it names the override — and it stays inside
// the bound the selection record enforces while doing it.
//
// That bound is the half that can fail in production. The prefix is no longer a
// fixed sentence: it grows by a clause per crossed cap, each carrying a name an
// operator chose, and a reason past the bound is not reported as an error where it
// lands — the selection fails validation and is dropped, so the run records no
// reason at all. That is the unaccounted work
// `selected-work-passes-intake-and-records-why` exists to prevent, arriving on the
// one path whose whole purpose is carrying an operator's own decision out.
func TestARerunReasonNamesTheCrossedCapsAndStaysInsideTheSelectionBound(t *testing.T) {
	t.Parallel()

	// The worst case the record permits: both budgets a re-run is refused against
	// crossed, each by a name at the full length an override may carry, and an
	// argument long enough to fill whatever is left on its own.
	counters := runstate.TriageCounters{
		Reruns: 1,
		Overrides: []runstate.TriageOverride{
			crossing(runstate.TriageRerunBudget, 2, strings.Repeat("m", runstate.MaxTriageOverrideByBytes)),
			crossing(runstate.TriageReviewRoundBudget, 8, strings.Repeat("b", runstate.MaxTriageOverrideByBytes)),
		},
	}
	entry := triage.Entry{WorkItemID: docketedItem, RunID: docketedRunID}
	// The decision itself carries an argument long enough to fill whatever the
	// prefix leaves, at the full length the record permits one.
	decision := triageDecided(runstate.TriageDecisionRerun, docketedRunID)
	decision.Reason = strings.Repeat("a ", runstate.MaxTriageDecisionReasonBytes/2)
	decision.DecidedAt = overrideDecidedAt
	reason := rerunReason(entry, counters, 0, decision)

	if len(reason) > runstate.MaxSelectionReasonBytes {
		t.Fatalf("reason is %d bytes, which exceeds the %d the selection record accepts",
			len(reason), runstate.MaxSelectionReasonBytes)
	}
	// Asked of the record itself rather than only of the length, because that is
	// what actually happens to an over-length reason: it is dropped rather than
	// refused, and the run goes on with nothing accounting for it.
	selection, stated := runstate.Selection{
		By:     runstate.SelectedByDevelopmentManager,
		Reason: reason,
		At:     overrideDecidedAt,
	}.Stamped(overrideDecidedAt)
	if !stated {
		t.Fatal("Stamped() dropped the selection, so the re-run would start recording no reason at all")
	}
	if err := selection.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	for _, want := range []string{
		docketedItem, docketedRunID,
		"recorded operator override of the re-run cap",
		"and of the review round cap",
		overrideDecidedAt.Format(time.RFC3339),
	} {
		if !strings.Contains(reason, want) {
			t.Fatalf("reason does not say %q: %q", want, reason)
		}
	}
}

// An item nobody has crossed anything for records the reason it always did. The
// clause is not a blank the reader has to interpret: most items have no override,
// and a re-run of one says nothing about overrides at all.
func TestARerunReasonSaysNothingAboutOverridesOnAnItemThatHasNone(t *testing.T) {
	t.Parallel()

	entry := triage.Entry{WorkItemID: docketedItem, RunID: docketedRunID}
	reason := rerunReason(entry, runstate.TriageCounters{Reruns: 1}, 0, triageDecided(runstate.TriageDecisionRerun, docketedRunID))
	if strings.Contains(reason, "override") {
		t.Fatalf("reason mentions an override on an item that has none: %q", reason)
	}
	if !strings.Contains(reason, rerunReasoning) {
		t.Fatalf("reason = %q, want the recorded reasoning carried whole", reason)
	}
}
