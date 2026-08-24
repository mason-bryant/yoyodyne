package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
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

// costTrackerTimeout bounds one bd command taken to record a price. It is
// carried by the tracker the ledger writes through rather than applied at a call
// site, so every write is bounded the same way whether it is one item or a
// backfill of all of them: a tracker that has gone slow costs a backfill an item
// rather than the whole ledger.
const costTrackerTimeout = 30 * time.Second

// reportCosts prices work items from the runs the harness recorded for them.
// Reading is the default and recording is asked for, because writing a price
// onto every item in a tracker is a change somebody should have to mean.
func reportCosts(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("cost", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	record := flags.Bool("record", false, "write each price onto its work item in the tracker")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) > 1 {
		fmt.Fprintln(stderr, "cost accepts at most one Beads work item id")
		printCostUsage(stderr)
		return 2
	}
	// The item is optional, so it is read through argumentAt rather than indexed:
	// `yoyo cost` with nothing named prices every item the runs cover.
	workItemID := argumentAt(positional, 0)

	parts, err := buildComponents(*configPath)
	if err != nil {
		return reportCostFailure(stdout, stderr, *jsonOutput, err)
	}
	prices, err := readPrices(parts, workItemID)
	if err != nil {
		return reportCostFailure(stdout, stderr, *jsonOutput, err)
	}
	output := costOutput{Prices: prices}
	failed := false
	if *record {
		recorded, err := recordPrices(ctx, parts, workItemID)
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
		printPrices(stdout, prices, workItemID != "")
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
		recorded, err := ledger.Record(ctx, workItemID)
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
	fmt.Fprintf(writer, ledgerRow, "item", "runs", "unpriced", "develop", "review", "repair", "cost", "waited")
	total := 0.0
	runs, unpriced := 0, 0
	var phases runstate.PhaseSpend
	for _, price := range prices {
		if !price.Recorded() {
			continue
		}
		printLedgerRow(writer, price.WorkItemID, len(price.Runs), price.UnknownRuns, price.TotalUSD, price.Phases)
		total += price.TotalUSD
		runs += len(price.Runs)
		unpriced += price.UnknownRuns
		phases.Merge(price.Phases)
	}
	printLedgerRow(writer, "TOTAL", runs, unpriced, total, phases)
	if unpriced > 0 {
		fmt.Fprintln(writer, "a run with no surviving record is counted as unpriced and left out of the total,")
		fmt.Fprintln(writer, "so every total it touches is a floor rather than a price")
	}
	// The split is said to be exhaustive rather than said to add up, because each
	// column is rounded to the cent on its own and three of them can land a penny
	// away from the total they came from. What matters is that nothing is missing
	// from them, which is the claim the rounding cannot make false.
	fmt.Fprintln(writer, "develop, review and repair account for every priced invocation; waited is time rather than money")
	fmt.Fprintln(writer, "this prices runs; conversation turns are recorded but not attributed to an item")
}

// ledgerRow is the shape of every line of the ledger, header and total
// included, so the columns cannot drift apart between them.
const ledgerRow = "%-38s %6s %9s %12s %12s %12s %12s %9s\n"

// printLedgerRow writes one item's line. Each phase carries the same floor
// marker the total does, for the reason the total carries it: a column that read
// as exact because the count saying otherwise is elsewhere on the line is the
// mistake the marker exists to stop.
func printLedgerRow(writer io.Writer, label string, runs, unpriced int, total float64, phases runstate.PhaseSpend) {
	fmt.Fprintf(writer, ledgerRow,
		label,
		strconv.Itoa(runs),
		strconv.Itoa(unpriced),
		renderTotal(phases.Development.CostUSD, unpriced),
		renderTotal(phases.Review.CostUSD, unpriced),
		renderTotal(phases.Repair.CostUSD, unpriced),
		renderTotal(total, unpriced),
		renderWait(phases.Waits.Total()),
	)
}

func printPriceBreakdown(writer io.Writer, price runstate.ItemPrice) {
	if !price.Recorded() {
		fmt.Fprintf(writer, "%s: the harness has no recorded run of it, so it has no price rather than a price of nothing\n", price.WorkItemID)
		return
	}
	fmt.Fprintf(writer, "%s: %s across %d run(s)\n", price.WorkItemID, renderTotal(price.TotalUSD, price.UnknownRuns), len(price.Runs))
	fmt.Fprintf(writer, "  %s\n", renderPhaseSplit(price.Phases, price.UnknownRuns))
	for _, run := range price.Runs {
		fmt.Fprintf(writer, "  %s started %s [%s] %s\n", run.RunID, run.StartedAt.UTC().Format(time.RFC3339), renderRunOutcome(run), renderRunPrice(run))
		// A run nothing survives to price has no split to show, but it still
		// waited for as long as it waited: that came from the run's own record
		// rather than from the log that is gone.
		if run.Known() {
			fmt.Fprintf(writer, "    %s\n", renderPhaseSplit(run.Phases, 0))
		} else if wait := renderWaits(run.Phases.Waits); wait != "" {
			fmt.Fprintf(writer, "    %s\n", wait)
		}
	}
	fmt.Fprintln(writer, "this prices runs; conversation turns are recorded but not attributed to an item")
}

// renderPhaseSplit says where a price went. Every phase is named even when it
// cost nothing, because a review that never happened and a review that was free
// read identically in a line that leaves the empty ones out, and telling those
// two apart is what splitting the price up is for.
func renderPhaseSplit(phases runstate.PhaseSpend, unpriced int) string {
	split := fmt.Sprintf("development %s from %d invocation(s), review %s from %d, repair %s from %d",
		renderTotal(phases.Development.CostUSD, unpriced), phases.Development.Invocations,
		renderTotal(phases.Review.CostUSD, unpriced), phases.Review.Invocations,
		renderTotal(phases.Repair.CostUSD, unpriced), phases.Repair.Invocations)
	if waits := renderWaits(phases.Waits); waits != "" {
		split += "; " + waits
	}
	return split
}

// renderWaits says how long work was held up and by what. The two waits are
// named apart because they are somebody's decision and nobody's respectively: a
// provider that would not serve the account is a thing to spend money on, and an
// operator's hold is a thing the operator already knows about.
func renderWaits(waits runstate.Waits) string {
	provider := time.Duration(waits.UsageLimitSeconds) * time.Second
	operator := time.Duration(waits.OperatorHoldSeconds) * time.Second
	switch {
	case provider > 0 && operator > 0:
		return fmt.Sprintf("waited %s (%s for the provider, %s on the operator's hold)",
			renderWait(waits.Total()), renderWait(provider), renderWait(operator))
	case provider > 0:
		return "waited " + renderWait(provider) + " for the provider"
	case operator > 0:
		return "waited " + renderWait(operator) + " on the operator's hold"
	}
	return ""
}

// renderWait puts a wait in the units somebody reads it in. A run that waited
// four hours and a run that waited four seconds are different problems, and
// neither is helped by six digits of precision.
func renderWait(waited time.Duration) string {
	waited = waited.Round(time.Second)
	if waited <= 0 {
		return ""
	}
	switch {
	case waited >= time.Hour:
		return fmt.Sprintf("%dh%02dm", int(waited/time.Hour), int(waited%time.Hour/time.Minute))
	case waited >= time.Minute:
		return fmt.Sprintf("%dm%02ds", int(waited/time.Minute), int(waited%time.Minute/time.Second))
	default:
		return fmt.Sprintf("%ds", int(waited/time.Second))
	}
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

// renderTotal marks a figure the unpriced runs behind it make a floor rather
// than a price. Every surface that shows money uses it, including each row of
// the ledger: a row whose own runs went unpriced must not read as exact merely
// because the count that says so is in the next column.
func renderTotal(total float64, unpriced int) string {
	if unpriced > 0 {
		return fmt.Sprintf("≥ $%.2f", total)
	}
	return fmt.Sprintf("$%.2f", total)
}

// renderRunOutcome says how one priced attempt went, in the same fixed
// vocabulary the run listing uses. It reads the outcome the record derives
// rather than the durable status, because a reader meeting the same run in both
// places must not be told two different things about it.
func renderRunOutcome(run runstate.RunPrice) string {
	outcome := string(run.Outcome)
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
cost the provider itself reported. Every price is split by where it went --
making the change, reviewing it, and repairing it -- with the time the work
spent waiting on a provider or on the operator beside it. Naming an item breaks
its price down by run.

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --record          write each price onto its work item in the tracker
  --json            emit machine-readable JSON`)
}
