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
// What each run's own line says is what became of the work rather than what
// became of the attempt, in a small fixed vocabulary the read model derives.
// "failed" was one word for four different things — a review nobody repaired, a
// target branch the replay could not catch, a provider that kept killing the
// run, and an operator stopping it — and three of those leave the change intact
// and the item back in somebody's hands. An operator read three of those lines
// and asked whether the runs had been discarded, which is the one question this
// listing exists to answer. So the outcome says which of them it was, the line
// beside it says whether anything survives, and the lines under it name the
// branch, the worktree, and the session that do.
//
// It is read-only in the strongest sense. Reading a run is not acting on it, so
// this holds nothing, adopts nothing, and settles nothing — a run another
// process is executing is listed exactly as a finished one is. Settling what a
// run left behind is `yoyo reconcile`, and following one live is
// `bin/yoyo-status`; this is the record afterwards.

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type statusOutput struct {
	// Standing is where the harness stands right now, in the four lines the
	// operator ratified. It comes first because it is the question this verb is
	// actually reached for: the run history says what became of attempts that are
	// over, and until this existed there was nowhere at all that said what is
	// happening. It is absent when one item was named, because the four lines are
	// about the product rather than about any one piece of work.
	Standing *readmodel.Standing   `json:"standing,omitempty"`
	Runs     []runstate.RunSummary `json:"runs"`
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
	Error      string `json:"error,omitempty"`
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
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) > 1 {
		fmt.Fprintln(stderr, "status accepts at most one Beads work item id")
		printStatusUsage(stderr)
		return 2
	}
	// The item is optional, so it is read through argumentAt rather than indexed:
	// `yoyo status` with nothing named reports the whole recent history.
	workItemID := argumentAt(positional, 0)
	if *limit < 0 {
		fmt.Fprintln(stderr, "limit cannot be negative; 0 reports every recorded run")
		return 2
	}

	store, caps, err := recordedRunStore(*configPath)
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

	// Where the harness stands is read only when nothing was named, because the
	// four lines are about the product: an operator asking about one item is
	// asking a different question, and answering both would put a screen of
	// product-wide state in front of the run they came here to read.
	var standing *readmodel.Standing
	if workItemID == "" {
		read := readmodel.ReadStanding(context.Background(), standingSources(*configPath))
		standing = &read
	}

	if *jsonOutput {
		output := statusOutput{
			Standing: standing,
			Runs:     history.Runs,
			Matched:  history.Matched,
			Recorded: history.Recorded,
			Triage:   counters,
			Watch:    watched,
		}
		if counters != nil {
			// The caps as this item's own recorded overrides leave them, which is what
			// the guards refuse against. Reporting the configured pair instead would
			// tell an operator who crossed a cap that they had not.
			recorded := caps.Overridden(counters.Overrides)
			output.TriageCaps = &recorded
		}
		output.TriageError = triageFailure
		output.WatchError = watchFailure
		return writeJSON(stdout, stderr, output)
	}
	// The four lines come first and are separated from the history by a blank
	// line, because they answer opposite questions: everything above is what is
	// true now, and everything below is what became of attempts that are over.
	if standing != nil {
		fmt.Fprint(stdout, standing.Render())
		fmt.Fprintln(stdout)
	}
	printWatch(stdout, watched)
	printRunHistory(stdout, history, workItemID, *failedOnly)
	if counters != nil {
		printItemTriage(stdout, *counters, caps.Overridden(counters.Overrides))
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

// standingSources wires the four lines over the same durable records every run
// writes. It goes through the configuration and the state root alone, for the
// reason the run store does: a verb an operator reaches for when something has
// gone wrong must not refuse to answer because of where their checkout happens
// to sit, and none of these stores needs a worktree or a process runner.
//
// A source that cannot be built is left out rather than failing the answer, and
// the line it belongs to says it could not be read. That is the whole discipline
// of this format: three quarters of an answer with the missing quarter named
// beats no answer, and beats an answer that quietly reports the missing quarter
// as empty.
//
// The tracker is the one source that needs the repository, so it is the one that
// can be missing on a checkout the configuration does not resolve against. It is
// wired as a tracker that reports that failure rather than left nil, so the line
// says what actually went wrong instead of reporting a wiring gap.
func standingSources(configPath string) readmodel.Sources {
	sources := readmodel.Sources{TrackerTimeout: chatTrackerTimeout}
	resolved, err := loadConfiguration(configPath)
	if err != nil {
		sources.Tracker = unreadableTracker{err}
		return sources
	}
	cfg := resolved.Config
	sources.Capacity = cfg.Execution.MaxConcurrentDevelopers
	stateRoot, err := runstate.SystemDefaultRoot(os.Getenv, os.UserHomeDir)
	if err != nil {
		sources.Tracker = unreadableTracker{err}
		return sources
	}
	if store, err := runstate.NewStore(stateRoot, cfg.Product.ID); err == nil {
		sources.Runs = store
	}
	if store, err := runstate.NewConversationStore(stateRoot, cfg.Product.ID); err == nil {
		sources.Conversations = store
	}
	if store, err := runstate.NewDirectiveStore(stateRoot, cfg.Product.ID); err == nil {
		sources.Directives = store
	}
	if store, err := runstate.NewAmendmentStore(stateRoot, cfg.Product.ID); err == nil {
		sources.Amendments = store
	}
	if store, err := runstate.NewOperatorHoldStore(stateRoot); err == nil {
		sources.OperatorHolds = store
	}
	if store, err := runstate.NewIntakeHoldStore(stateRoot, cfg.Product.ID); err == nil {
		sources.IntakeHolds = store
	}
	if store, err := runstate.NewWatchStore(stateRoot, cfg.Product.ID); err == nil {
		sources.Sessions = store
	}
	repository, err := resolvePath(config.ProjectDirectory(resolved.Path), cfg.Product.Repository)
	if err != nil {
		sources.Tracker = unreadableTracker{fmt.Errorf("resolve product repository: %w", err)}
		return sources
	}
	sources.Tracker = beads.Client{Runner: execution.OSProcessRunner{}, Dir: repository}
	return sources
}

// unreadableTracker is the tracker a reading gets when the harness could not be
// resolved far enough to build one. It answers every question with the reason,
// so the queue's line says what went wrong rather than reporting an empty
// backlog assembled from nothing.
type unreadableTracker struct{ err error }

func (t unreadableTracker) List(context.Context, string) ([]beads.WorkItem, error) {
	return nil, t.err
}

func (t unreadableTracker) Ready(context.Context) ([]beads.WorkItem, error) {
	return nil, t.err
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
// The caps it is given are the effective ones — the configured ceilings as this
// item's own recorded overrides leave them — because they are what refuses the
// next decision. An operator who crossed a cap and is then shown the configured
// figure has been told their decision did not take.
func printItemTriage(writer io.Writer, counters runstate.TriageCounters, caps runstate.TriageCaps) {
	fmt.Fprintf(writer, "triage of %s: %s\n", counters.WorkItemID, describeTriagePasses(counters))
	// The rounds line states the rounds and nothing else. Every conclusion it
	// used to assert acquired a second predicate sooner or later — the grant
	// budget refuses repairs the rounds would allow, and a re-arm ignores the
	// rounds entirely — so what may still happen is said by the budget lines
	// below, each beside the numbers that decide it.
	// A cleared cap is its own line rather than a figure in either of the other
	// two: neither of them reads as a sentence with "no cap" substituted into the
	// place a number goes, and this is the line an operator checks first.
	switch {
	case caps.ReviewRounds == runstate.TriageCapCleared:
		fmt.Fprintf(writer, "  review rounds: %d spent across every run of this item, under no cap at all — the operator cleared it\n",
			counters.ReviewRounds)
	case counters.RoundsRemaining(caps.ReviewRounds) == 0:
		fmt.Fprintf(writer, "  review rounds: %d spent across every run of this item — at or past the cap of %d, so no decision that buys a round remains\n",
			counters.ReviewRounds, caps.ReviewRounds)
	default:
		fmt.Fprintf(writer, "  review rounds: %d spent across every run of this item, under the cap of %d\n",
			counters.ReviewRounds, caps.ReviewRounds)
	}
	// Each of these is refused by two budgets rather than one, and the line says
	// both: the rounds above, which bound what the item may cost, and its own,
	// which bounds how often triage may decide the same thing about it. An
	// operator told only about the rounds would read an item refused a second
	// re-run with rounds to spare as a bug.
	fmt.Fprintf(writer, "  repair grants: %d of %s permitted; re-runs: %d of %s; each is refused by its own budget or once no round remains\n",
		counters.RepairGrants, triageCapFigure(caps.RepairGrants), counters.Reruns, triageCapFigure(caps.Reruns))
	fmt.Fprintf(writer, "  merge re-arms: %d of %s permitted\n", counters.MergeRearms, triageCapFigure(caps.MergeRearms))
	// A grant that was cut is said out loud, because it is the fact that says the
	// item is at the end of what it will be given: the next grant has nothing left
	// to truncate to and is refused outright.
	if counters.TruncatedGrants > 0 {
		fmt.Fprintf(writer, "  %d grant(s) were cut down to the rounds the cap still had room for; %d round(s) were granted in total\n",
			counters.TruncatedGrants, counters.GrantedRounds)
	}
	// A cap somebody crossed is said out loud, with who crossed it and why. Every
	// figure above is one of these caps, so an operator reading a budget larger
	// than the project configured and finding no account of it here would have to
	// go looking in the state directory for the reason.
	for _, override := range counters.Overrides {
		fmt.Fprintf(writer, "  operator override: %s\n", override.Describe())
	}
	fmt.Fprintln(writer, "  waiting, re-scoping, and escalating spend nothing and stay available; a re-arm spends only its own budget, whatever the rounds say")
}

// triageCapFigure is one ceiling as a reader reads it. A cleared cap is a number
// no count comes near rather than a number anybody means, so printing it would be
// a line an operator has to decode. Who cleared it is not repeated on every
// budget: the override line beneath these figures says so once.
func triageCapFigure(limit int) string {
	if limit == runstate.TriageCapCleared {
		return "no cap"
	}
	return strconv.Itoa(limit)
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
		printRunArtifacts(writer, run)
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
	// What the run was spent on, what set it up, and which harness dispatched it
	// are printed for every run, and for the reason the selection line is: there
	// is one account today, so a line that appeared only where several existed
	// would be a line nobody was reading on the day the second one arrived. A
	// record that names none of them is a record written before they were carried,
	// and says so.
	//
	// The build is on the same line because it answers the same class of question
	// and is read at the same moment: an operator asking why a run behaved the way
	// it did needs to know whether the code it ran was the code that was merged,
	// and a run that cannot say is a defect nobody can classify.
	fmt.Fprintf(writer, "  ran under %s, configuration %s, harness %s\n",
		recorded(run.AccountAlias, "an account the record does not name"),
		recorded(run.ConfigRevision, "a configuration the record does not name"),
		recorded(shortBuild(run.Build), "a build the record does not name"))
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

// printRunArtifacts names what a run that did not succeed left behind, which is
// the question a listing of those runs is actually read for: whether the work is
// gone. The brackets above answer that in a word; these lines say where it is,
// so an operator who wants to look at the change is not sent to the run's JSON
// for the path.
//
// It is silent on a run that succeeded and on one still in flight. A successful
// run removes its worktree and branch by design, so naming them would report the
// harness working as a loss; a run still going holds everything it has by
// definition, so saying so of every one of them would make "preserved" mean
// nothing on the records where it means preserved work nobody has looked at.
//
// An artifact the harness recorded as removed is named as removed rather than
// left out. Sending somebody to a worktree that is gone and telling them nothing
// was preserved are the same failure in opposite directions, and the record
// distinguishes them. A run whose record names neither says nothing here at all:
// the brackets above already say the record names no artifact, and a line
// repeating it on every run that broke before it made anything is a line every
// reader learns to skip.
func printRunArtifacts(writer io.Writer, run runstate.RunSummary) {
	if !run.Status.Terminal() || run.Outcome == runstate.OutcomeSucceeded {
		return
	}
	if run.Branch != "" {
		if run.BranchRemoved {
			fmt.Fprintf(writer, "  branch already removed: %s\n", run.Branch)
		} else {
			fmt.Fprintf(writer, "  preserved branch: %s\n", run.Branch)
		}
	}
	if run.WorktreePath != "" {
		if run.WorktreeRemoved {
			fmt.Fprintf(writer, "  worktree already removed: %s\n", run.WorktreePath)
		} else {
			fmt.Fprintf(writer, "  preserved worktree: %s\n", run.WorktreePath)
		}
	}
	// The session and the findings are said only where the change survives. A
	// session that continues work nothing holds any more continues nothing, and
	// findings about a change that is gone are a reading list rather than
	// something to act on.
	if !run.Preserved() {
		return
	}
	if run.ProviderSessionID != "" {
		fmt.Fprintf(writer, "  preserved developer session: %s\n", run.ProviderSessionID)
	}
	if run.ReviewFindings > 0 {
		fmt.Fprintf(writer, "  %d review finding(s) recorded against the preserved change\n", run.ReviewFindings)
	}
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

// shortBuild names a revision the way somebody quoting one does. A run's record
// keeps the whole object name, which --json carries; a listing wants the prefix
// a person would type into `git show`.
func shortBuild(build string) string {
	if trimmed := strings.TrimSpace(build); len(trimmed) > 12 {
		return trimmed[:12]
	}
	return build
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

// renderRunState says what became of a run and what remains of it: the outcome,
// the phase it reached, whether it promoted anything, whether its change
// survives, and — on a run that is over — whether it still owes somebody a step.
//
// It leads with the outcome rather than the durable status, because the status
// answers a question nobody is asking here. "failed" is true of a run whose
// reviewer blocked it, of one the target branch outran, of one the provider kept
// killing, and of one that broke before it made anything — and the first three
// leave a branch, a worktree, a session, and an item back in somebody's hands.
// An operator reading that one word has been told the attempt is over and
// nothing about whether the work is.
//
// Preservation is stated on every run that did not succeed, in the three fixed
// phrases the read model renders, and on no others: a successful run removes
// what it made on purpose, and a run still in flight holds everything by
// definition. The phrases are the read model's rather than this file's for the
// reason the outcome word is — the channel says the same three about the same
// run — and the same discipline `recorded` below keeps holds inside them: an
// absence is stated as an absence rather than read as work thrown away. The
// outstanding marker keeps the same rule and for the same reason it always had.
func renderRunState(run runstate.RunSummary) string {
	state := string(run.Outcome)
	if run.Phase != "" {
		state += ", " + string(run.Phase)
	}
	if run.Integrated {
		state += ", integrated"
	}
	if run.Status.Terminal() && run.Outcome != runstate.OutcomeSucceeded {
		state += ", " + run.Artifacts().Describe()
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

Where the harness stands, then what became of the runs it made.

Naming no item prints four lines first, and prints all four every time: what is
Running, with each run's item, phase, elapsed time and spend; what is Working,
which is the persona conversations with a turn in flight; what is Not startable,
which is each admitted item nothing will pull with the refusal that stops it; and
what Needs a human, which is either "nothing" or the list with whose move each
one is. A line with nothing in it says so in words, and a line whose records
could not be read says that instead of saying nothing. Naming an item leaves them
out: they are about the product, and a question about one item is a different
question.

Under them, what became of the runs the harness made, newest first: the work item, the
outcome it reached, the phase it was in, what it cost, and the reasons its record
kept. Naming an item reports only its runs, and under them what triage has spent
on that item: the review rounds it has cost across every run of it, against the
cap that bounds them, and the repair grants, re-runs, and merge re-arms it has
been given. An item triaged more than once says so there.

The outcome in the brackets is one of a small fixed set. "succeeded" landed its
work. "stopped" ended on a durable blocker: the item carries it, a person decides
what happens next, and nothing was discarded — an unrepaired review, a check that
kept failing, refused paths, a replay the target branch outran, and a provider
that would not carry the run all end this way, and the reason under the run says
which. "cancelled" was stopped rather than judged. "timed out" was stopped on
time. "failed" is what is left: a run that ended leaving nobody anything to act
on. A run still going says "pending" or "running" instead.

Beside it, every run that did not succeed says what remains of it: "work
preserved", "work removed" for artifacts the harness recorded removing, or "no
artifacts recorded" where the record names neither. The last states an absence
rather than claiming the run made nothing, and in practice it is a run that broke
before it got a worktree: a run that reached any phase has one. The preserved
branch, worktree, and developer session are named under the run. A successful run
removes what it made on purpose, so it says nothing about preservation; a run
still in flight holds everything it has.

Each recorded reason is printed under the run and named for what it is. Only
"reason" is the run's own account of why it ended; an outstanding publication, an
outstanding cleanup, a failing check, and a completion recorded late are things
recorded around the work, and a run can carry one of those with its change
already promoted. The last is the class whose work-item note is itself unreliable —
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
