package cli

// Reading what became of the runs the harness made, and why one of them failed.
//
// The write side of a failure is thorough: a terminal run's record keeps the
// status, the phase it died in, and the reason, with the bookkeeping failures
// beside it in fields of their own, and the same reason reaches the work item's
// notes. Reading it back was the gap — an operator asking "what has been
// failing?" went through the tracker item by item, or read the run JSON out of
// the state directory by hand.
//
// So this reports the recorded runs newest first, which is the order the
// question is asked in, with each recorded reason on a line of its own and named
// for what it is: a publication or a cleanup that could not finish is not a
// failed piece of work, and a listing that ran them together would undo the
// separation the records take care to keep.
//
// It is read-only in the strongest sense. Reading a run is not acting on it, so
// this holds nothing, adopts nothing, and settles nothing — a run another
// process is executing is listed exactly as a finished one is. Settling what a
// run left behind is `yoyo reconcile`, and following one live is
// `bin/yoyo-status`; this is the record afterwards.

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type statusOutput struct {
	Runs []runstate.RunSummary `json:"runs"`
	// Matched and Recorded are what keep a limited listing honest: how many runs
	// the query selected, and how many the harness holds at all.
	Matched  int `json:"matched"`
	Recorded int `json:"recorded"`
	// Triage is what triage has spent on the named item and what the item has
	// cost in review rounds, with the caps those are measured against. It is
	// present only when an item was named, because it is a fact about one piece of
	// work rather than about the listing: a run's record says what became of that
	// run, and this says what the item has been given across all of them.
	Triage     *runstate.TriageCounters `json:"triage,omitempty"`
	TriageCaps *runstate.TriageCaps     `json:"triage_caps,omitempty"`
	Error      string                   `json:"error,omitempty"`
}

// defaultStatusRuns is how many runs are reported when nobody says. It is a
// screenful rather than the whole history, because the question this answers is
// about what has happened lately; --limit 0 reports everything.
const defaultStatusRuns = 20

func reportRunStatus(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("status", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	failedOnly := flags.Bool("failed", false, "only the runs that ended without succeeding")
	limit := flags.Int("limit", defaultStatusRuns, "report at most this many runs, newest first (0 reports all of them)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() > 1 {
		fmt.Fprintln(stderr, "status accepts at most one Beads work item id")
		printStatusUsage(stderr)
		return 2
	}
	if *limit < 0 {
		fmt.Fprintln(stderr, "limit cannot be negative; 0 reports every recorded run")
		return 2
	}

	store, caps, err := recordedRunStore(*configPath)
	if err != nil {
		return reportStatusFailure(stdout, stderr, *jsonOutput, err)
	}
	history, err := store.History(runstate.RunQuery{
		WorkItemID: flags.Arg(0),
		FailedOnly: *failedOnly,
		Limit:      *limit,
	})
	if err != nil {
		return reportStatusFailure(stdout, stderr, *jsonOutput, err)
	}
	// The item's triage record is read only when an item was named, and it is
	// read whatever the listing found: an item whose runs were all cleaned up
	// still has a record of what triage gave it, and that is exactly the reader
	// this answers.
	// An unreadable triage record does not replace the listing: the
	// never-spend-an-unreadable-budget rule is about spending, and this is a
	// read-only answer that still holds the runs it found. The failure is
	// reported beside them instead.
	var counters *runstate.TriageCounters
	var triageFailure string
	if workItemID := flags.Arg(0); workItemID != "" {
		read, err := store.Triage().Counters(workItemID)
		if err != nil {
			triageFailure = fmt.Sprintf("the item's triage record could not be read: %v", err)
		} else {
			counters = &read
		}
	}

	if *jsonOutput {
		output := statusOutput{
			Runs:     history.Runs,
			Matched:  history.Matched,
			Recorded: history.Recorded,
			Triage:   counters,
		}
		if counters != nil {
			recorded := caps
			output.TriageCaps = &recorded
		}
		output.Error = triageFailure
		return writeJSON(stdout, stderr, output)
	}
	printRunHistory(stdout, history, flags.Arg(0), *failedOnly)
	if counters != nil {
		printItemTriage(stdout, *counters, caps)
	}
	if triageFailure != "" {
		fmt.Fprintln(stderr, triageFailure)
	}
	// A failed run is what this exists to report, so reporting one is this
	// command working. An exit status that treated the answer as a failure would
	// make the surface something a script has to guard against reading.
	return 0
}

// recordedRunStore resolves the same product-scoped run records every run
// writes, from the configuration and the state root alone. It deliberately does
// not go through buildComponents, for the reason the reports verb does not:
// reading what became of a run needs no repository, no worktree manager, and no
// process runner, and a verb an operator reaches for when something has gone
// wrong must not refuse to answer because of where their checkout happens to sit
// — inside a harness-managed worktree, for one.
//
// The state root is runstate.SystemDefaultRoot for every command that has one,
// so the product id is the whole of what decides which records these are, and a
// test pins this path to the one buildComponents builds.
func recordedRunStore(configPath string) (*runstate.Store, runstate.TriageCaps, error) {
	resolved, err := loadConfiguration(configPath)
	if err != nil {
		return nil, runstate.TriageCaps{}, err
	}
	stateRoot, err := runstate.SystemDefaultRoot(os.Getenv, os.UserHomeDir)
	if err != nil {
		return nil, runstate.TriageCaps{}, err
	}
	store, err := runstate.NewStore(stateRoot, resolved.Config.Product.ID)
	if err != nil {
		return nil, runstate.TriageCaps{}, err
	}
	// The caps come back with the store because the counters are only legible
	// beside them: "three review rounds" says nothing about whether this item is
	// nearly out of them.
	return store, orchestrator.TriageCaps(resolved.Config.Execution, resolved.Config.Triage), nil
}

// printItemTriage says what triage has spent on the named item and what the
// item has cost in review rounds. It is printed under the runs because it is a
// different kind of fact from any of them: every line above belongs to one run,
// and every figure here spans all of them.
//
// The rounds come first because they are the budget two of the three actions are
// refused against, and the one figure that moves without anybody deciding
// anything. An item nothing has triaged says so rather than printing three
// zeroes, and an item triaged more than once says that in the first clause,
// because that is the thing a reader is looking for: work that came back is work
// where something other than the change may be wrong.
func printItemTriage(writer io.Writer, counters runstate.TriageCounters, caps runstate.TriageCaps) {
	fmt.Fprintf(writer, "triage of %s: %s\n", counters.WorkItemID, describeTriagePasses(counters))
	if counters.ReviewRounds > caps.ReviewRounds {
		fmt.Fprintf(writer, "  review rounds: %d spent across every run of this item — past the cap of %d, so triage may only escalate or re-scope\n",
			counters.ReviewRounds, caps.ReviewRounds)
	} else {
		fmt.Fprintf(writer, "  review rounds: %d spent across every run of this item; triage may hand back repairs while under the cap of %d\n",
			counters.ReviewRounds, caps.ReviewRounds)
	}
	fmt.Fprintf(writer, "  repair grants: %d; re-runs: %d; both are refused once no round remains\n",
		counters.RepairGrants, counters.Reruns)
	fmt.Fprintf(writer, "  merge re-arms: %d of %d permitted\n", counters.MergeRearms, caps.MergeRearms)
	// A grant that was cut is said out loud, because it is the fact that says the
	// item is at the end of what it will be given: the next grant has nothing left
	// to truncate to and is refused outright.
	if counters.TruncatedGrants > 0 {
		fmt.Fprintf(writer, "  %d grant(s) were cut down to the rounds the cap still had room for; %d round(s) were granted in total\n",
			counters.TruncatedGrants, counters.GrantedRounds)
	}
}

// describeTriagePasses says how many times triage has acted on an item, in the
// three cases that read differently: never, once, and again.
func describeTriagePasses(counters runstate.TriageCounters) string {
	switch passes := counters.Passes(); passes {
	case 0:
		return "triage has not acted on it"
	case 1:
		return "triaged once"
	default:
		return fmt.Sprintf("triaged %d times", passes)
	}
}

// printRunHistory reports the selected runs, one block each. The reasons are
// each named for what they are and printed under the run they belong to rather
// than in a column, because a reason is a sentence somebody wrote and a column
// wide enough for one would leave no room for anything else.
func printRunHistory(writer io.Writer, history runstate.RunHistory, workItemID string, failedOnly bool) {
	if history.Recorded == 0 {
		fmt.Fprintln(writer, "the harness has no recorded runs, so there is nothing to report")
		return
	}
	if len(history.Runs) == 0 {
		fmt.Fprintf(writer, "no %s, of the %d run(s) recorded\n", describeRunSelection(workItemID, failedOnly), history.Recorded)
		return
	}
	fmt.Fprintf(writer, "%s, %d of %d shown (%d run(s) recorded):\n",
		describeRunSelection(workItemID, failedOnly), len(history.Runs), history.Matched, history.Recorded)
	reasoned := false
	for _, run := range history.Runs {
		fmt.Fprintf(writer, "%s %s started %s [%s] %s\n",
			run.RunID, run.WorkItemID, run.StartedAt.UTC().Format(time.RFC3339),
			renderRunState(run), renderSummaryCost(run))
		if printRunReasons(writer, run) {
			reasoned = true
		}
		printOutstandingSteps(writer, run)
	}
	if remaining := history.Matched - len(history.Runs); remaining > 0 {
		fmt.Fprintf(writer, "%d further run(s) are not listed here; --limit reports more, and 0 reports all of them\n", remaining)
	}
	if reasoned {
		fmt.Fprintln(writer, "each reason is shown as one line; --json carries what the record holds in full")
	}
}

// printRunReasons prints what one run recorded about how it went, and reports
// whether it recorded anything at all. Each reason is labelled, because the
// record keeps them apart on purpose: only the first says the work failed, and
// the others are things that happened around work that may well have landed —
// including a completion record that took a late write to land, the one class
// whose work-item note is itself unreliable.
// Each is folded and bounded by singleLine, so a reviewer's verdict is one row
// of the listing rather than a page of it; --json carries the whole of it.
func printRunReasons(writer io.Writer, run runstate.RunSummary) bool {
	printed := false
	for _, reason := range []struct {
		label string
		text  string
	}{
		{label: "reason", text: run.Failure},
		{label: "outstanding publication", text: run.PublishFailure},
		{label: "outstanding cleanup", text: run.CleanupFailure},
		{label: "completion recorded late", text: run.CompletionRecordingFailure},
	} {
		if reason.text == "" {
			continue
		}
		fmt.Fprintf(writer, "  %s: %s\n", reason.label, singleLine(reason.text))
		printed = true
	}
	if run.FailingCheck != nil {
		fmt.Fprintf(writer, "  failing check: %s exited %d\n", singleLine(run.FailingCheck.Command), run.FailingCheck.ExitCode)
		printed = true
	}
	// A refused path is said here rather than left to the run's JSON for the
	// reason the failing check is: it is what stopped the run, and the worktree
	// that would have shown it is removed when the run is cleaned up.
	if run.RefusedPaths != nil {
		refused := strings.Join(run.RefusedPaths.Paths, ", ")
		if run.RefusedPaths.Omitted > 0 {
			refused = fmt.Sprintf("%s, and %d more", refused, run.RefusedPaths.Omitted)
		}
		fmt.Fprintf(writer, "  refused paths: %s\n", singleLine(refused))
		printed = true
	}
	return printed
}

// printOutstandingSteps says what a finished run still owes, so a run marked
// outstanding is never marked and then left unexplained — which would be the
// "go and read the run's JSON" case this verb exists to remove.
//
// Nothing here is a recorded reason, and that is why it is derived rather than
// read out of a field: what a finished run owes is decided by the state it is
// in, and there are exactly two things it can be. The last branch is not dead
// code but the honest answer if that ever stops being true: a run the record
// says owes something, whose state does not say what, is reported as owing
// something rather than silently as owing nothing.
func printOutstandingSteps(writer io.Writer, run runstate.RunSummary) {
	if !run.Outstanding || !run.Status.Terminal() {
		return
	}
	printed := false
	if run.Integrated && run.Phase != runstate.PhaseComplete {
		fmt.Fprintln(writer, "  outstanding: its work is promoted, and cleaning up after it is not recorded as finished")
		printed = true
	}
	if run.MergeQueued {
		fmt.Fprintln(writer, "  outstanding: the forge queued the merge of its pull request and nothing has settled it since")
		printed = true
	}
	if !printed {
		fmt.Fprintln(writer, "  outstanding: it still owes a step; `yoyo reconcile` reports which and settles it")
	}
}

// describeRunSelection names what was asked for, so an empty answer says which
// question it is empty about: no failed run and no run at all are different
// answers, and so are the whole record and one item's part of it.
func describeRunSelection(workItemID string, failedOnly bool) string {
	selection := "runs"
	if failedOnly {
		selection = "runs that ended without succeeding"
	}
	if workItemID != "" {
		selection += " of " + workItemID
	}
	return selection
}

// renderRunState says where a run is: the status, the phase it reached, whether
// it promoted anything, and — on a run that is over — whether it still owes
// somebody a step. The last is only said of a terminal run because only there
// does it carry news: a run still in flight owes its own remaining steps by
// definition, and saying so of every one of them would make the word mean
// nothing on the records where it means an interrupted cleanup or a merge the
// forge has not settled.
func renderRunState(run runstate.RunSummary) string {
	state := string(run.Status)
	if run.Phase != "" {
		state += ", " + string(run.Phase)
	}
	if run.Integrated {
		state += ", integrated"
	}
	if run.Outstanding && run.Status.Terminal() {
		state += ", outstanding"
	}
	return state
}

// renderSummaryCost says what a run spent, in the terms the ledger uses: a run
// still going reports what it has spent so far rather than a figure that reads
// as final, and a run whose evidence is gone is stated as unpriceable rather
// than as free.
func renderSummaryCost(run runstate.RunSummary) string {
	if !run.CostKnown() {
		return "cost unknown"
	}
	if !run.Status.Terminal() {
		return fmt.Sprintf("$%.2f so far", run.CostUSD)
	}
	return fmt.Sprintf("$%.2f", run.CostUSD)
}

func reportStatusFailure(stdout, stderr io.Writer, jsonOutput bool, err error) int {
	if jsonOutput {
		if code := writeJSON(stdout, stderr, statusOutput{Runs: []runstate.RunSummary{}, Error: err.Error()}); code != 0 {
			return code
		}
		return 1
	}
	fmt.Fprintf(stderr, "status failed: %v\n", err)
	return 1
}

func printStatusUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo status [options] [<beads-id>]

What became of the runs the harness made, newest first: the work item, the
status it reached, the phase it was in, what it cost, and the reasons its record
kept. Naming an item reports only its runs, and under them what triage has spent
on that item: the review rounds it has cost across every run of it, against the
cap that bounds them, and the repair grants, re-runs, and merge re-arms it has
been given. An item triaged more than once says so there.

Each recorded reason is printed under the run and named for what it is. Only
"reason" says the work itself failed; an outstanding publication, an outstanding
cleanup, a failing check, and a completion recorded late are things recorded
around the work, and a run can carry one of those with its change already
promoted. The last is the class whose work-item note is itself unreliable —
recording that note is part of what was failing — so this listing is its
authoritative home.

Reading a run decides nothing about it, so this holds nothing and settles
nothing, and reporting a failure is not itself a failure: the exit status says
whether the records could be read. Settling what an interrupted run left behind
is `+"`yoyo reconcile`"+`; following a live run is `+"`bin/yoyo-status`"+`.

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --failed          only the runs that ended without succeeding
  --limit <n>       report at most this many runs, newest first (default 20; 0 reports all)
  --json            emit machine-readable JSON`)
}
