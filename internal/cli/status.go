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
// run left behind is `yoyo reconcile`; this is the record afterwards.
//
// Watching one happen is the same verb under `--follow`, `--list`, and
// `--spend`, which live in statusstream.go: the record afterwards and the
// stream as it arrives are the same question asked at two moments, and an
// operator should not have to install a second thing to ask the other half of
// it.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
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
	// TriageError accompanies a successful listing: a named item whose triage
	// record could not be read reports that here while the runs it found are
	// still returned. It is its own key so error keeps meaning what it always
	// meant — the command failed, and the exit status agrees.
	TriageError string `json:"triage_error,omitempty"`
	// Watch is where the session that chooses work got to, when one has ever run
	// for this product. It is the one fact here that is not about a run: a
	// session choosing nothing has no run to say so with, and its silence and a
	// dead process read identically without it.
	Watch *runstate.WatchTransition `json:"watch,omitempty"`
	// WatchError accompanies a successful listing, for the reason TriageError
	// does: an unreadable watch log costs this answer a line rather than the runs
	// it found.
	WatchError string `json:"watch_error,omitempty"`
	// What the operator has stopped is carried by every mode of this verb, for
	// the reason it is announced by every mode: a paused machine and a dead one
	// look identical to anything reading only the runs.
	statusHolds
	Error string `json:"error,omitempty"`
}

// defaultStatusRuns is how many runs are reported when nobody says. It is a
// screenful rather than the whole history, because the question this answers is
// about what has happened lately; --limit 0 reports everything.
const defaultStatusRuns = 20

func reportRunStatus(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("status", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	failedOnly := flags.Bool("failed", false, "only the runs that ended without succeeding")
	limit := flags.Int("limit", defaultStatusRuns, "report at most this many, newest first (0 reports all of them)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	follow := flags.Bool("follow", false, "follow a run, conversation, or branch review as its events arrive")
	events := flags.Bool("events", false, "print a stream's recent events and exit, without following")
	list := flags.Bool("list", false, "list the recent runs, conversations, and branch reviews")
	spend := flags.Bool("spend", false, "report what was spent, grouped by the local day it was spent on")
	latest := flags.Bool("latest", false, "with --follow, move to a later stream when one starts")
	lines := flags.Int("lines", defaultStreamLines, "replay this many recorded events first (0 replays the whole log)")
	kind := flags.String("kind", "", "narrow to one kind: runs, chats, reviews, or all (default all)")
	includeAll := flags.Bool("all", false, "include the thinking-token events the default leaves out")
	raw := flags.Bool("raw", false, "emit each event exactly as it was recorded")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) > 1 {
		fmt.Fprintln(stderr, "status accepts at most one id")
		printStatusUsage(stderr)
		return 2
	}
	// The argument is optional, so it is read through argumentAt rather than
	// indexed: `yoyo status` with nothing named reports the whole recent history.
	// Which id it is depends on the mode — a work item for the run records, a
	// stream for the live ones — because those are different collections and an
	// argument that meant the same thing in both would name nothing in one.
	named := argumentAt(positional, 0)
	if *limit < 0 {
		fmt.Fprintln(stderr, "limit cannot be negative; 0 reports everything")
		return 2
	}
	if *lines < 0 {
		fmt.Fprintln(stderr, "lines cannot be negative; 0 replays the whole log")
		return 2
	}
	kinds, err := resolveStreamKinds(*kind)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	mode, err := selectedStatusMode(*follow, *events, *list, *spend)
	if err != nil {
		fmt.Fprintln(stderr, err)
		printStatusUsage(stderr)
		return 2
	}
	if *latest && mode != statusFollows {
		fmt.Fprintln(stderr, "--latest moves a follow to a later stream, so it needs --follow")
		return 2
	}
	// An option that belongs to the other half of the verb is refused rather than
	// ignored: an operator who narrowed a listing and was silently given the
	// unnarrowed one reads a true answer as the answer to their question.
	if mode != statusReadsRecords && *failedOnly {
		fmt.Fprintln(stderr, "--failed selects among the recorded runs, so it cannot narrow a stream")
		return 2
	}
	if mode != statusReadsRecords {
		options := streamOptions{
			kinds:  kinds,
			match:  named,
			lines:  *lines,
			limit:  *limit,
			follow: mode == statusFollows,
			latest: *latest,
			raw:    *raw,
			all:    *includeAll,
		}
		if mode == statusPricesStreams {
			days, match, err := spendWindow(named)
			if err != nil {
				fmt.Fprintln(stderr, err)
				return 2
			}
			options.days, options.match = days, match
		}
		return reportStreamStatus(ctx, mode, options, *configPath, *jsonOutput, stdout, stderr)
	}
	if *raw || *includeAll {
		fmt.Fprintln(stderr, "--raw and --all shape a followed event stream, so they need --follow or --events")
		return 2
	}
	if *kind != "" {
		fmt.Fprintln(stderr, "--kind narrows which event streams are read, so it needs --follow, --events, --list, or --spend")
		return 2
	}
	workItemID := named

	store, caps, roots, err := recordedRunStore(*configPath)
	if err != nil {
		return reportStatusFailure(stdout, stderr, *jsonOutput, err)
	}
	history, err := store.History(runstate.RunQuery{
		WorkItemID: workItemID,
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
	if workItemID != "" {
		read, err := store.Triage().Counters(workItemID)
		if err != nil {
			triageFailure = fmt.Sprintf("the item's triage record could not be read: %v", err)
		} else {
			counters = &read
		}
	}

	// Where the session that chooses work got to is read whatever the listing
	// found, and it is read for the whole product rather than for a named item:
	// a session is about the queue, and the question it answers — is anything
	// still choosing work — is the one an operator asks before any question
	// about a particular run.
	watched, watchFailure := latestWatch(*configPath)
	holds := readStatusHolds(roots)

	if *jsonOutput {
		output := statusOutput{
			Runs:        history.Runs,
			Matched:     history.Matched,
			Recorded:    history.Recorded,
			Triage:      counters,
			Watch:       watched,
			statusHolds: holds,
		}
		if counters != nil {
			recorded := caps
			output.TriageCaps = &recorded
		}
		output.TriageError = triageFailure
		output.WatchError = watchFailure
		return writeJSON(stdout, stderr, output)
	}
	announceHolds(stderr, holds)
	printWatch(stdout, watched)
	printRunHistory(stdout, history, workItemID, *failedOnly)
	if counters != nil {
		printItemTriage(stdout, *counters, caps)
	}
	if triageFailure != "" {
		fmt.Fprintln(stderr, triageFailure)
	}
	if watchFailure != "" {
		fmt.Fprintln(stderr, watchFailure)
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
func recordedRunStore(configPath string) (*runstate.Store, runstate.TriageCaps, statusRoots, error) {
	roots, err := statusStateRoots(configPath)
	if err != nil {
		return nil, runstate.TriageCaps{}, statusRoots{}, err
	}
	store, err := runstate.NewStore(roots.stateRoot, roots.productID)
	if err != nil {
		return nil, runstate.TriageCaps{}, statusRoots{}, err
	}
	resolved, err := loadConfiguration(configPath)
	if err != nil {
		return nil, runstate.TriageCaps{}, statusRoots{}, err
	}
	// The caps come back with the store because the counters are only legible
	// beside them: "three review rounds" says nothing about whether this item is
	// nearly out of them.
	return store, orchestrator.TriageCaps(resolved.Config.Execution, resolved.Config.Triage), roots, nil
}

// statusRoots is what every mode of this verb reads from: the product the
// configuration names, and the state root the harness keeps its records under.
// Resolving them once is what makes the live modes agree with the recorded ones
// about which machine's work is being reported.
type statusRoots struct {
	productID domain.ProductID
	stateRoot string
}

func statusStateRoots(configPath string) (statusRoots, error) {
	resolved, err := loadConfiguration(configPath)
	if err != nil {
		return statusRoots{}, err
	}
	stateRoot, err := runstate.SystemDefaultRoot(os.Getenv, os.UserHomeDir)
	if err != nil {
		return statusRoots{}, err
	}
	return statusRoots{productID: resolved.Config.Product.ID, stateRoot: stateRoot}, nil
}

// statusMode is which question of the verb was asked. The record afterwards is
// the default because it is the one that needs no argument and refuses nothing;
// the other three are the live surface this verb absorbed.
type statusMode int

const (
	statusReadsRecords statusMode = iota
	statusFollows
	statusShowsEvents
	statusListsStreams
	statusPricesStreams
)

// selectedStatusMode reads which mode the flags asked for. They are exclusive
// because they are different answers rather than different amounts of one:
// combining them would have to invent a precedence, and an operator who typed
// two of them meant one of them.
func selectedStatusMode(follow, events, list, spend bool) (statusMode, error) {
	selected := statusReadsRecords
	named := 0
	for _, mode := range []struct {
		asked bool
		mode  statusMode
	}{
		{follow, statusFollows},
		{events, statusShowsEvents},
		{list, statusListsStreams},
		{spend, statusPricesStreams},
	} {
		if mode.asked {
			selected = mode.mode
			named++
		}
	}
	if named > 1 {
		return statusReadsRecords, errors.New("--follow, --events, --list, and --spend are different questions; ask one of them")
	}
	return selected, nil
}

// reportStreamStatus answers the three modes that read the event streams rather
// than the run records. They share the holds banner and the state root with the
// recorded listing, and nothing else: what they read is being written right now.
func reportStreamStatus(ctx context.Context, mode statusMode, options streamOptions, configPath string, jsonOutput bool, stdout, stderr io.Writer) int {
	roots, err := statusStateRoots(configPath)
	if err != nil {
		return reportStatusFailure(stdout, stderr, jsonOutput, err)
	}
	store, err := runstate.NewStreamStore(roots.stateRoot, roots.productID)
	if err != nil {
		return reportStatusFailure(stdout, stderr, jsonOutput, err)
	}
	holds := readStatusHolds(roots)
	announceHolds(stderr, holds)
	switch mode {
	case statusListsStreams:
		return listStreams(store, options, holds, jsonOutput, stdout, stderr)
	case statusPricesStreams:
		return reportSpend(store, options, holds, time.Now(), jsonOutput, stdout, stderr)
	default:
		// Following emits the recorded events themselves, which are already the
		// machine-readable form: --raw is what asks for them untouched.
		if jsonOutput {
			fmt.Fprintln(stderr, "a followed stream emits its own recorded events; --raw is what asks for them untouched")
			return 2
		}
		return followStreams(ctx, store, options, stdout, stderr)
	}
}

// spendWindow reads the one argument a spend report takes, which is either the
// number of local days to cover or the stream to price. A purely numeric one is
// the count, because an operator asking for a fortnight would not otherwise have
// a way to say so. An id prefix can be all digits too — ids are hex — so one
// that is has to be given with its `run-`, `chat-`, or `review-` prefix to be
// read as an id rather than as days. Naming a stream prices it whatever day it
// ran on: the window is for a report that has to choose what to show, and an id
// has already chosen.
func spendWindow(named string) (int, string, error) {
	if named == "" {
		return defaultSpendDays, "", nil
	}
	if strings.TrimLeft(named, "0123456789") != "" {
		return 0, named, nil
	}
	days, err := strconv.Atoi(named)
	if err != nil || days <= 0 {
		return 0, "", fmt.Errorf("%q is neither a positive number of days nor a stream id", named)
	}
	return days, "", nil
}

// latestWatch reads where the session that chooses work got to. A product
// nobody has watched has no session rather than an idle one, which is why the
// absence is carried as a nil rather than as a state: never having watched and
// having stopped watching are different answers to the question being asked.
//
// It resolves its own store for the reason the run records do: reading what a
// session said needs no repository and no worktree, and a verb reached for when
// something looks wrong must not refuse over where a checkout happens to sit.
func latestWatch(configPath string) (*runstate.WatchTransition, string) {
	resolved, err := loadConfiguration(configPath)
	if err != nil {
		return nil, fmt.Sprintf("what the harness is watching could not be read: %v", err)
	}
	stateRoot, err := runstate.SystemDefaultRoot(os.Getenv, os.UserHomeDir)
	if err != nil {
		return nil, fmt.Sprintf("what the harness is watching could not be read: %v", err)
	}
	store, err := runstate.NewWatchStore(stateRoot, resolved.Config.Product.ID)
	if err != nil {
		return nil, fmt.Sprintf("what the harness is watching could not be read: %v", err)
	}
	latest, watched, err := store.Latest()
	if err != nil {
		return nil, fmt.Sprintf("what the harness is watching could not be read: %v", err)
	}
	if !watched {
		return nil, ""
	}
	return &latest, ""
}

// printWatch says where the session that chooses work got to, in one line above
// the runs. A product nobody has ever watched says nothing at all rather than
// asserting that nothing is running: this command has never known that, and a
// line claiming it would be the confident emptiness the rest of this file
// avoids.
func printWatch(writer io.Writer, watched *runstate.WatchTransition) {
	if watched == nil {
		return
	}
	fmt.Fprintf(writer, "the session choosing work is %s as of %s",
		watched.State, watched.At.UTC().Format(time.RFC3339))
	if reason := strings.TrimSpace(watched.Reason); reason != "" {
		fmt.Fprintf(writer, ": %s", reason)
	}
	fmt.Fprintln(writer)
}

// printItemTriage says what triage has spent on the named item and what the
// item has cost in review rounds. It is printed under the runs because it is a
// different kind of fact from any of them: every line above belongs to one run,
// and every figure here spans all of them.
//
// The rounds come first because they are the budget two of the three actions are
// refused against, and the one figure that moves without anybody deciding
// anything. An item triage has spent something on more than once says that in
// the first clause, because that is the thing a reader is looking for: work that
// came back is work where something other than the change may be wrong.
//
// Every figure here is a budget, and the last line says so. Three of the
// development manager's six decisions spend one — a repair grant, a re-run, a
// merge re-arm — and the other three cost nothing and reach no counter, so an
// item that was escalated or told to wait shows zeroes. A reader looking for
// whether anybody has looked at stopped work is looking at the item, and the
// line points them there rather than letting these zeroes answer a question they
// were never counting.
func printItemTriage(writer io.Writer, counters runstate.TriageCounters, caps runstate.TriageCaps) {
	fmt.Fprintf(writer, "triage of %s: %s\n", counters.WorkItemID, describeTriagePasses(counters))
	// The rounds line states the rounds and nothing else. Every conclusion it
	// used to assert acquired a second predicate sooner or later — the grant
	// budget refuses repairs the rounds would allow, and a re-arm ignores the
	// rounds entirely — so what may still happen is said by the budget lines
	// below, each beside the numbers that decide it.
	if counters.RoundsRemaining(caps.ReviewRounds) == 0 {
		fmt.Fprintf(writer, "  review rounds: %d spent across every run of this item — at or past the cap of %d, so no decision that buys a round remains\n",
			counters.ReviewRounds, caps.ReviewRounds)
	} else {
		fmt.Fprintf(writer, "  review rounds: %d spent across every run of this item, under the cap of %d\n",
			counters.ReviewRounds, caps.ReviewRounds)
	}
	// Each of these is refused by two budgets rather than one, and the line says
	// both: the rounds above, which bound what the item may cost, and its own,
	// which bounds how often triage may decide the same thing about it. An
	// operator told only about the rounds would read an item refused a second
	// re-run with rounds to spare as a bug.
	fmt.Fprintf(writer, "  repair grants: %d of %d permitted; re-runs: %d of %d; each is refused by its own budget or once no round remains\n",
		counters.RepairGrants, caps.RepairGrants, counters.Reruns, caps.Reruns)
	fmt.Fprintf(writer, "  merge re-arms: %d of %d permitted\n", counters.MergeRearms, caps.MergeRearms)
	// A grant that was cut is said out loud, because it is the fact that says the
	// item is at the end of what it will be given: the next grant has nothing left
	// to truncate to and is refused outright.
	if counters.TruncatedGrants > 0 {
		fmt.Fprintf(writer, "  %d grant(s) were cut down to the rounds the cap still had room for; %d round(s) were granted in total\n",
			counters.TruncatedGrants, counters.GrantedRounds)
	}
	fmt.Fprintln(writer, "  waiting, re-scoping, and escalating spend nothing and stay available; a re-arm spends only its own budget, whatever the rounds say")
}

// describeTriagePasses says how many times triage has spent something on an
// item, in the three cases that read differently: never, once, and again.
//
// It counts spending rather than deciding, and says so, because those stopped
// being the same thing when triage acquired decisions that cost nothing. An item
// the development manager escalated has been triaged and has been given nothing,
// and a line reading "triage has not acted on it" would tell an operator looking
// for evidence somebody looked that nobody had.
func describeTriagePasses(counters runstate.TriageCounters) string {
	switch passes := counters.Passes(); passes {
	case 0:
		return "triage has spent nothing on it"
	case 1:
		return "triage has spent one pass on it"
	default:
		return fmt.Sprintf("triage has spent %d passes on it", passes)
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
	// Why the harness was running this item at all comes first, because it is the
	// only one of these that is about the choice rather than the outcome. It is
	// printed for every run rather than only for the ones that recorded it: a run
	// with no reason is exactly what an operator most needs to see, and omitting
	// the line would make it look like a run whose reason they had already read.
	if run.Selection != nil {
		fmt.Fprintf(writer, "  selected by the %s: %s\n", run.Selection.By, singleLine(run.Selection.Reason))
	} else {
		fmt.Fprintln(writer, "  selected: no reason recorded")
	}
	// What the run was spent on and what set it up are printed for every run, and
	// for the reason the selection line is: there is one account today, so a line
	// that appeared only where several existed would be a line nobody was reading
	// on the day the second one arrived. A record that names neither is a record
	// written before either was carried, and says so.
	fmt.Fprintf(writer, "  ran under %s, configuration %s\n",
		recorded(run.AccountAlias, "an account the record does not name"),
		recorded(run.ConfigRevision, "a configuration the record does not name"))
	printed := true
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

// recorded says what a record holds for one field, or states the absence in
// words. A blank in a listing reads as a bug in the listing rather than as a
// record that was written before the field existed, which is what an absence
// here actually is.
func recorded(value, absence string) string {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		return trimmed
	}
	return absence
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
       yoyo status --follow [options] [<id>]
       yoyo status --events [options] [<id>]
       yoyo status --list [options]
       yoyo status --spend [options] [<id>|<days>]

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
is `+"`yoyo reconcile`"+`.

--follow, --events, --list, and --spend read the event stream a run, a
conversation, and a branch review each record, rather than the run records. It
is the same question asked of all three -- is this alive, what is it doing, and
what did it cost -- so every one of them covers all three and the default never
asks which kind you meant; --kind narrows it when that is what you want. There,
the id names a stream or a unique prefix of one rather than a work item.

--spend groups what was spent by the local-timezone day it was spent on, each
day's group closing with that day's spend and today's coming last: what an
operator budgets against is what today cost, and the day they mean is the one
their own clock is keeping. What counts on a day is each invocation rather than
the log it was recorded in, so a conversation open for a fortnight appears under
every day it spent on. A number asks for a different count of days; naming a
stream prices that one whatever day it ran on. `+"`yoyo cost`"+` is the same run
spending grouped by the work item the runs were for.

Every mode leads with a banner while activity is paused or intake is held: a
machine somebody paused and a machine that died look identical otherwise.

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --failed          only the runs that ended without succeeding
  --limit <n>       report at most this many, newest first (default 20; 0 reports all)
  --json            emit machine-readable JSON

Live options:
  --follow          follow a stream's events as they arrive
  --events          print a stream's recent events and exit, without following
  --list            list the recent runs, conversations, and branch reviews
  --spend           report what was spent, by the local day it was spent on
  --latest          with --follow, move to a later stream when one starts
  --lines <n>       replay this many recorded events first (default 50; 0 the whole log)
  --kind <kind>     runs, chats, reviews, or all (default all)
  --all             include the thinking-token events the default leaves out
  --raw             emit each event exactly as it was recorded`)
}
