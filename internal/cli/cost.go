package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"yoyodyne/internal/beads"
	"yoyodyne/internal/runstate"
)

type costOutput struct {
	Prices []runstate.ItemPrice `json:"prices,omitempty"`
	// Recorded is what was written onto the tracker, and is absent from a report
	// that only read the records.
	Recorded []recordedCost `json:"recorded,omitempty"`
	Error    string         `json:"error,omitempty"`
}

// recordedCost is one item's price as it was written, or the reason it was not.
type recordedCost struct {
	WorkItemID string     `json:"work_item_id"`
	Cost       beads.Cost `json:"cost"`
	Failure    string     `json:"failure,omitempty"`
}

// costTimeout bounds the tracker writes a backfill makes. Each item is a
// separate bd invocation, so the bound is per item rather than over the lot: a
// tracker that has gone slow should cost the backfill an item, not the ledger.
const costTimeout = 30 * time.Second

// reportCosts prices work items from the runs the harness recorded for them.
// Reading is the default and recording is asked for, because writing a price
// onto every item in a tracker is a change somebody should have to mean.
func reportCosts(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("cost", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	record := flags.Bool("record", false, "write each price onto its work item in the tracker")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() > 1 {
		fmt.Fprintln(stderr, "cost accepts at most one Beads work item id")
		printCostUsage(stderr)
		return 2
	}

	parts, err := buildComponents(*configPath)
	if err != nil {
		return reportCostFailure(stdout, stderr, *jsonOutput, err)
	}
	prices, err := readPrices(parts, flags.Arg(0))
	if err != nil {
		return reportCostFailure(stdout, stderr, *jsonOutput, err)
	}
	output := costOutput{Prices: prices}
	failed := false
	if *record {
		recorded, err := recordPrices(ctx, parts, flags.Arg(0))
		if err != nil {
			return reportCostFailure(stdout, stderr, *jsonOutput, err)
		}
		output.Recorded = recorded
		for _, entry := range recorded {
			failed = failed || entry.Failure != ""
		}
	}

	if *jsonOutput {
		if code := writeJSON(stdout, stderr, output); code != 0 {
			return code
		}
	} else {
		printPrices(stdout, prices, flags.Arg(0) != "")
		printRecordedPrices(stdout, stderr, output.Recorded)
	}
	if failed {
		return 1
	}
	return 0
}

func readPrices(parts components, workItemID string) ([]runstate.ItemPrice, error) {
	if workItemID != "" {
		price, err := parts.store.Price(workItemID)
		if err != nil {
			return nil, err
		}
		return []runstate.ItemPrice{price}, nil
	}
	prices, err := parts.store.Prices()
	if err != nil {
		return nil, err
	}
	return prices, nil
}

// recordPrices writes what was read onto the tracker. An item the harness has
// never run is skipped rather than priced at nothing, so a backfill leaves
// untouched exactly the items it has no evidence about.
func recordPrices(ctx context.Context, parts components, workItemID string) ([]recordedCost, error) {
	ledger := ledgerFrom(parts)
	if workItemID != "" {
		recordCtx, cancel := context.WithTimeout(ctx, costTimeout)
		defer cancel()
		recorded, err := ledger.Record(recordCtx, workItemID)
		if recorded == nil {
			// Nothing was priced, so nothing was written; the failure, if there was
			// one, is the caller's to report.
			return nil, err
		}
		entry := recordedCost{WorkItemID: workItemID, Cost: *recorded}
		if err != nil {
			entry.Failure = err.Error()
		}
		return []recordedCost{entry}, nil
	}
	results, err := ledger.RecordAll(ctx)
	if err != nil {
		return nil, err
	}
	recorded := make([]recordedCost, 0, len(results))
	for _, result := range results {
		recorded = append(recorded, recordedCost{WorkItemID: result.WorkItemID, Cost: result.Cost, Failure: result.Failure})
	}
	return recorded, nil
}

// printPrices reports what was read. Asking about one item breaks its price down
// by the runs it took, because a total for one item invites the question of
// which attempt spent it; asking about everything reports a line per item, which
// is the ledger.
func printPrices(writer io.Writer, prices []runstate.ItemPrice, single bool) {
	if len(prices) == 0 {
		fmt.Fprintln(writer, "the harness has no recorded runs, so there is nothing to price")
		return
	}
	if single {
		printPriceBreakdown(writer, prices[0])
		return
	}
	fmt.Fprintf(writer, "%-38s %6s %9s %12s\n", "item", "runs", "unpriced", "cost")
	total := 0.0
	runs, unpriced := 0, 0
	for _, price := range prices {
		if !price.Recorded() {
			continue
		}
		fmt.Fprintf(writer, "%-38s %6d %9d %12s\n", price.WorkItemID, len(price.Runs), price.UnknownRuns, renderPrice(price))
		total += price.TotalUSD
		runs += len(price.Runs)
		unpriced += price.UnknownRuns
	}
	fmt.Fprintf(writer, "%-38s %6d %9d %12s\n", "TOTAL", runs, unpriced, renderTotal(total, unpriced))
	if unpriced > 0 {
		fmt.Fprintln(writer, "a run with no surviving record is counted as unpriced and left out of the total,")
		fmt.Fprintln(writer, "so every total it touches is a floor rather than a price")
	}
	fmt.Fprintln(writer, "this prices runs; conversation turns are recorded but not attributed to an item")
}

func printPriceBreakdown(writer io.Writer, price runstate.ItemPrice) {
	if !price.Recorded() {
		fmt.Fprintf(writer, "%s: the harness has no recorded run of it, so it has no price rather than a price of nothing\n", price.WorkItemID)
		return
	}
	fmt.Fprintf(writer, "%s: %s across %d run(s)\n", price.WorkItemID, renderTotal(price.TotalUSD, price.UnknownRuns), len(price.Runs))
	for _, run := range price.Runs {
		fmt.Fprintf(writer, "  %s started %s [%s] %s\n", run.RunID, run.StartedAt.UTC().Format(time.RFC3339), renderRunOutcome(run), renderRunPrice(run))
	}
	fmt.Fprintln(writer, "this prices runs; conversation turns are recorded but not attributed to an item")
}

// printRecordedPrices says what reached the tracker. A price that could not be
// written is named rather than swallowed: the ledger is only worth reading if it
// says which items it failed to reach.
func printRecordedPrices(stdout, stderr io.Writer, recorded []recordedCost) {
	if len(recorded) == 0 {
		return
	}
	written := 0
	for _, entry := range recorded {
		if entry.Failure == "" {
			written++
			continue
		}
		fmt.Fprintf(stderr, "%s was priced but not recorded: %s\n", entry.WorkItemID, entry.Failure)
	}
	fmt.Fprintf(stdout, "recorded the price of %d of %d work item(s) on the tracker\n", written, len(recorded))
}

func renderPrice(price runstate.ItemPrice) string {
	return fmt.Sprintf("$%.2f", price.TotalUSD)
}

func renderTotal(total float64, unpriced int) string {
	if unpriced > 0 {
		return fmt.Sprintf("≥ $%.2f", total)
	}
	return fmt.Sprintf("$%.2f", total)
}

func renderRunOutcome(run runstate.RunPrice) string {
	outcome := string(run.Status)
	if run.Phase != "" {
		outcome += ", " + string(run.Phase)
	}
	if run.Integrated {
		outcome += ", integrated"
	}
	return outcome
}

// renderRunPrice says what one attempt cost. A run that is still going reports
// what it has spent so far rather than a figure that reads as final.
func renderRunPrice(run runstate.RunPrice) string {
	if !run.Known() {
		return "unknown: " + run.Unknown
	}
	if !run.Status.Terminal() {
		return fmt.Sprintf("$%.2f so far from %d invocation(s)", run.CostUSD, run.Invocations)
	}
	return fmt.Sprintf("$%.2f from %d invocation(s)", run.CostUSD, run.Invocations)
}

func reportCostFailure(stdout, stderr io.Writer, jsonOutput bool, err error) int {
	if jsonOutput {
		if code := writeJSON(stdout, stderr, costOutput{Error: err.Error()}); code != 0 {
			return code
		}
		return 1
	}
	fmt.Fprintf(stderr, "cost failed: %v\n", err)
	return 1
}

func printCostUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo cost [options] [<beads-id>]

Prices work items from the runs the harness recorded for them: every run made
for an item, the failed attempts and the reviewer's invocations included, at the
cost the provider itself reported. Naming an item breaks its price down by run.

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --record          write each price onto its work item in the tracker
  --json            emit machine-readable JSON`)
}
