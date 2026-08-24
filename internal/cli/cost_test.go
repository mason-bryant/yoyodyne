package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

func TestCostRefusesMoreThanOneItemAndReportsConfigurationFailureAsJSON(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := Run([]string{"cost", "yoyodyne-ifd.41", "yoyodyne-ifd.42"}, &stdout, &stderr, "test"); code != 2 {
		t.Fatalf("Run() code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "at most one Beads work item id") {
		t.Fatalf("stderr = %q", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	if code := Run([]string{"cost", "--config", missing, "--json"}, &stdout, &stderr, "test"); code != 1 {
		t.Fatalf("Run() code = %d, want 1; stderr = %q", code, stderr.String())
	}
	var result costOutput
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if result.Error == "" || len(result.Prices) != 0 {
		t.Fatalf("result = %#v", result)
	}
}

// The ledger is read down a column, and a run nothing survives to price is
// counted as unpriced rather than added to the total as a zero: every total it
// touched would otherwise read as complete while being short.
func TestCostLedgerCountsUnpricedRunsRatherThanChargingNothingForThem(t *testing.T) {
	t.Parallel()

	started := time.Date(2026, 8, 16, 8, 0, 0, 0, time.UTC)
	prices := []runstate.ItemPrice{
		{
			WorkItemID: "yoyodyne-ifd.2.7",
			Runs: []runstate.RunPrice{
				{RunID: "run-1", Status: runstate.StatusFailed, StartedAt: started, CostUSD: 8.91, Invocations: 3},
				{RunID: "run-2", Status: runstate.StatusSucceeded, StartedAt: started, CostUSD: 19.02, Invocations: 2},
			},
			TotalUSD: 27.93,
			Phases: runstate.PhaseSpend{
				Development: runstate.PhaseCost{CostUSD: 18.02, Invocations: 2},
				Review:      runstate.PhaseCost{CostUSD: 6.10, Invocations: 2},
				Repair:      runstate.PhaseCost{CostUSD: 3.81, Invocations: 1},
				Waits:       runstate.Waits{UsageLimitSeconds: 9540},
			},
		},
		{
			WorkItemID: "yoyodyne-ifd.41",
			Runs: []runstate.RunPrice{
				{RunID: "run-3", Status: runstate.StatusFailed, StartedAt: started, Unknown: "the run's event log is no longer recorded"},
			},
			UnknownRuns: 1,
		},
		// An item nothing ran is left out entirely rather than listed at nothing.
		{WorkItemID: "yoyodyne-ifd.99"},
	}

	var out bytes.Buffer
	printPrices(&out, prices, false)
	rendered := out.String()
	for _, required := range []string{
		"yoyodyne-ifd.2.7",
		"$27.93",
		"TOTAL",
		"≥ $27.93",
		// A row whose own runs went unpriced is marked as a floor like every
		// other figure, rather than reading as an exact price of nothing because
		// the count that says otherwise is in the next column.
		"≥ $0.00",
		// Where the money went is a column of the ledger rather than something
		// only a per-item question answers.
		"develop",
		"$18.02",
		"$6.10",
		"$3.81",
		"2h39m",
		"develop, review and repair account for every priced invocation",
		"counted as unpriced and left out of the total",
		"conversation turns are recorded but not attributed",
	} {
		if !strings.Contains(rendered, required) {
			t.Fatalf("rendered ledger = %q, want it to contain %q", rendered, required)
		}
	}
	if strings.Contains(rendered, "yoyodyne-ifd.99") {
		t.Fatalf("an item with no recorded run was priced: %q", rendered)
	}
}

func TestCostBreaksOneItemDownByRunAndSaysWhenNothingWasRun(t *testing.T) {
	t.Parallel()

	started := time.Date(2026, 8, 16, 8, 0, 0, 0, time.UTC)
	var out bytes.Buffer
	printPrices(&out, []runstate.ItemPrice{{
		WorkItemID: "yoyodyne-ifd.2.7",
		Runs: []runstate.RunPrice{
			{
				// A run the reviewer blocked: the price listing says what became of
				// it in the same words the run listing does.
				RunID: "run-1", Status: runstate.StatusFailed, Outcome: runstate.OutcomeStopped,
				Phase: runstate.PhaseReviewing, StartedAt: started,
				CostUSD: 8.91, Invocations: 3,
				Phases: runstate.PhaseSpend{
					Development: runstate.PhaseCost{CostUSD: 5.00, Invocations: 1},
					Review:      runstate.PhaseCost{CostUSD: 2.00, Invocations: 1},
					Repair:      runstate.PhaseCost{CostUSD: 1.91, Invocations: 1},
					Waits:       runstate.Waits{UsageLimitSeconds: 3600, OperatorHoldSeconds: 900},
				},
			},
			{
				RunID: "run-2", Status: runstate.StatusSucceeded, Outcome: runstate.OutcomeSucceeded,
				Phase: runstate.PhaseComplete, StartedAt: started,
				Integrated: true, CostUSD: 19.02, Invocations: 2,
				Phases: runstate.PhaseSpend{
					Development: runstate.PhaseCost{CostUSD: 13.02, Invocations: 1},
					Review:      runstate.PhaseCost{CostUSD: 4.10, Invocations: 1},
					Repair:      runstate.PhaseCost{CostUSD: 1.90, Invocations: 1},
				},
			},
		},
		TotalUSD: 27.93,
		Phases: runstate.PhaseSpend{
			Development: runstate.PhaseCost{CostUSD: 18.02, Invocations: 2},
			Review:      runstate.PhaseCost{CostUSD: 6.10, Invocations: 2},
			Repair:      runstate.PhaseCost{CostUSD: 3.81, Invocations: 2},
			Waits:       runstate.Waits{UsageLimitSeconds: 3600, OperatorHoldSeconds: 900},
		},
	}}, true)
	rendered := out.String()
	for _, required := range []string{
		"yoyodyne-ifd.2.7: $27.93 across 2 run(s)",
		"development $18.02 from 2 invocation(s), review $6.10 from 2, repair $3.81 from 2",
		"[stopped, reviewing] $8.91 from 3 invocation(s)",
		// Each attempt says where its own money went, because an item's split
		// says which attempts were expensive and not which part of one was.
		"development $5.00 from 1 invocation(s), review $2.00 from 1, repair $1.91 from 1",
		"[succeeded, complete, integrated] $19.02 from 2 invocation(s)",
		// Two waits of different kinds are named apart: one is a provider that
		// would not serve the account and the other is the operator's own doing.
		"waited 1h15m (1h00m for the provider, 15m00s on the operator's hold)",
	} {
		if !strings.Contains(rendered, required) {
			t.Fatalf("rendered breakdown = %q, want it to contain %q", rendered, required)
		}
	}

	out.Reset()
	printPrices(&out, []runstate.ItemPrice{{WorkItemID: "yoyodyne-ifd.99"}}, true)
	if !strings.Contains(out.String(), "no price rather than a price of nothing") {
		t.Fatalf("rendered breakdown = %q", out.String())
	}

	// A run that is still going has not finished spending, so its figure says so
	// rather than reading as what the attempt will have cost.
	out.Reset()
	printPrices(&out, []runstate.ItemPrice{{
		WorkItemID: "yoyodyne-ifd.41",
		Runs: []runstate.RunPrice{{
			RunID: "run-4", Status: runstate.StatusRunning, Outcome: runstate.RunOutcome(runstate.StatusRunning),
			Phase: runstate.PhaseDeveloping, StartedAt: started,
		}},
	}}, true)
	if !strings.Contains(out.String(), "$0.00 so far from 0 invocation(s)") {
		t.Fatalf("rendered breakdown = %q", out.String())
	}

	// A run nothing survives to price has no split to show, and what it waited
	// came from its own record rather than from the log that is gone, so that is
	// reported on its own rather than lost with the money.
	out.Reset()
	printPrices(&out, []runstate.ItemPrice{{
		WorkItemID: "yoyodyne-ifd.41",
		Runs: []runstate.RunPrice{{
			RunID: "run-5", Status: runstate.StatusFailed, Outcome: runstate.OutcomeFailed, StartedAt: started,
			Unknown: "the run's event log is no longer recorded",
			Phases:  runstate.PhaseSpend{Waits: runstate.Waits{UsageLimitSeconds: 1800}},
		}},
		UnknownRuns: 1,
	}}, true)
	rendered = out.String()
	if !strings.Contains(rendered, "waited 30m00s for the provider") {
		t.Fatalf("rendered breakdown = %q, want the unpriceable run's wait", rendered)
	}
	if strings.Contains(rendered, "development $0.00") {
		t.Fatalf("rendered breakdown = %q, want no split claimed for a run nothing could price", rendered)
	}
}

// A backfill that could not reach an item has to say which one, or the ledger
// reads as complete while part of it was never written.
func TestCostNamesThePricesItCouldNotRecord(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	printRecordedPrices(&stdout, &stderr, []recordedCost{
		{WorkItemID: "yoyodyne-ifd.41"},
		{WorkItemID: "yoyodyne-ifd.42", Failure: "bd update failed"},
	})
	if !strings.Contains(stdout.String(), "recorded the price of 1 of 2 work item(s)") {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "yoyodyne-ifd.42 was priced but not recorded: bd update failed") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}
