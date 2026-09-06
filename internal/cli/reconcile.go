package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/watchdog"
)

type reconcileOutput struct {
	Runs []orchestrator.Reconciliation `json:"runs"`
	// Publications is what the forge now says about the pull requests the harness
	// recorded and nothing settled. It is reported beside the runs rather than
	// folded into them because it is about records of finished work: nothing here
	// settles a run, and a corrected record is a fact about the forge rather than
	// a step somebody was owed.
	Publications []orchestrator.PublicationRefresh `json:"publications"`
	Convergence  orchestrator.Convergence          `json:"convergence"`
	// Docketed is how many entries this sweep is what put on the triage docket.
	// It is a count rather than the entries because the docket is read where it
	// is acted on, which is the development manager's conversation; what this
	// command reports is that the sweep found something, not what it found.
	Docketed int `json:"docketed"`
	// Supervision is the whole of what this sweep made of the exchanges the roles
	// have put to each other. Most of it is what was recovered: a round a dead
	// process asked and never answered, a thread that ran out of rounds. The rest
	// is every exchange the sweep found free and did not take a round on, since it
	// carries no voice — which is the ordinary state of an exchange waiting its
	// turn, and is carried here rather than printed for the same reason `--json`
	// carries the whole of every other sweep.
	Supervision []orchestrator.SupervisionResult `json:"supervision"`
	// Stall is what this sweep made of whether anything is happening at all: the
	// silence it read, and what that opened, closed, or left standing in the
	// product's own stall record.
	Stall *watchdog.Reading `json:"stall,omitempty"`
	// StallProblem is why that reading was not made. It costs the sweep a line and
	// nothing else — every other step it took stands — so it is reported beside
	// the sweep rather than failing it, exactly as a staleness reading is.
	StallProblem string `json:"stall_problem,omitempty"`
	Error        string `json:"error,omitempty"`
}

// reconcileSweep is everything one sweep found, gathered so the reporting takes
// the sweep rather than a growing list of positional arguments.
type reconcileSweep struct {
	Runs         []orchestrator.Reconciliation
	Publications []orchestrator.PublicationRefresh
	Convergence  orchestrator.Convergence
	Docketed     int
	Supervision  []orchestrator.SupervisionResult
	Stall        *watchdog.Reading
	StallProblem string
}

// reconcileRuns settles every run an interrupted process left outstanding and
// then converges local state onto the forge's. It is safe to repeat: a run that
// is already settled is not outstanding, a branch already caught up has nothing
// to catch up to, and the steps it does take are idempotent over artifacts that
// are already gone.
func reconcileRuns(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("reconcile", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	// How long nothing may start, over ready work and with nothing accounting for
	// it, before this sweep records that the harness has stopped. It is a flag
	// because the right number is the operator's judgement about their own machine
	// rather than the harness's, and it is here rather than on the Slack sink
	// because this is the sweep that always runs: reporting is optional, and a
	// threshold set on an optional process is a threshold most products never get.
	//
	// How promptly a stall is noticed is this number and the cadence of whatever
	// runs this sweep, so an unattended pass should run at least as often as the
	// threshold it sets.
	stallAfter := flags.Duration("stall-after", readmodel.DefaultStallThreshold, "how long nothing may start over ready work before this records that the harness has stopped")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "reconcile does not accept positional arguments")
		printReconcileUsage(stderr)
		return 2
	}
	// There is deliberately no way to ask for no watchdog: a threshold of zero
	// would take the default rather than turn anything off, so reading it as a
	// switch would silently do the opposite of what somebody meant by it.
	if *stallAfter <= 0 {
		fmt.Fprintln(stderr, "stall-after must be positive; it is how long nothing may start rather than a switch, and there is no way to ask not to be told")
		return 2
	}

	parts, err := buildComponents(*configPath)
	if err != nil {
		return reportReconcileResult(stdout, stderr, *jsonOutput, reconcileSweep{}, err)
	}
	reconciler := reconcilerFrom(parts)
	results, err := reconciler.Reconcile(ctx)
	// The publications of runs nothing is going to settle are refreshed next, and
	// before the docket is built: a record frozen at its run's death is what the
	// docket, the orphan sweep and every status surface read, so a sweep that
	// docketed first would decide against the very staleness it was about to fix.
	publications, publicationErr := reconciler.RefreshPublications(ctx)
	err = errors.Join(err, publicationErr)
	// Convergence is swept even when settling a run failed. The two are
	// independent — one finishes runs, the other finishes branches — and a
	// checkout left behind the forge because some unrelated run could not be
	// settled is exactly the manual seam this exists to close.
	convergence, convergeErr := reconciler.Converge(ctx)
	err = errors.Join(err, convergeErr)
	// The docket is built for the same reason the convergence sweep runs: this
	// is one of the two moments anything scans. A publication the forge quietly
	// never merged is not an event anybody can be present for, so it is found
	// here or when a development manager opens a conversation, and a sweep that
	// settled runs without looking would leave it for the other one.
	docketed, docketErr := docketerFrom(parts).Build()
	if docketErr != nil {
		err = errors.Join(err, docketErr)
	}
	// The exchanges the roles have put to each other are recovered here for the
	// same reason the runs are: a process died holding something, and this is the
	// sweep that finds out. It takes each exchange's own lease, so one a live
	// process is carrying is left to that process, and it invokes nothing.
	supervision, supervisionErr := sweepSupervision(parts)
	err = errors.Join(err, supervisionErr)
	// Whether anything is happening at all, read last and for two reasons. It is
	// the only step here that says something about the machine rather than about
	// one run, and every step above it moves what it would read: a run whose
	// process died goes on saying it is in flight until the settling above, and a
	// phantom run counted as activity would silence this for exactly the crash it
	// exists to catch.
	//
	// This sweep is where it lives because it is what an unattended pass runs and
	// what an operator runs by hand, neither of which depends on Slack reporting
	// having been turned on. A reading that fails is reported and does not fail the
	// sweep: nothing was recorded, and the next pass decides.
	stall, stallProblem := checkForStall(ctx, parts, *stallAfter)
	return reportReconcileResult(stdout, stderr, *jsonOutput, reconcileSweep{
		Runs:         results,
		Publications: publications,
		Convergence:  convergence,
		Docketed:     docketed.Added,
		Supervision:  supervision,
		Stall:        stall,
		StallProblem: stallProblem,
	}, err)
}

// checkForStall takes one reading of whether this product has gone quiet and
// reconciles it against the product's durable stall record.
//
// The record is where `yoyo status` reads the history back from and where the
// Slack sink reads the one message it says about it, so a product reporting
// nowhere still records its stalls and can be asked about them afterwards — which
// is the whole of what this being here rather than in the sink buys.
func checkForStall(ctx context.Context, parts components, threshold time.Duration) (*watchdog.Reading, string) {
	stalls, err := runstate.NewStallStore(parts.stateRoot, parts.config.Product.ID)
	if err != nil {
		return nil, err.Error()
	}
	reading, err := watchdog.Checker{
		Runs:     parts.store,
		Sessions: parts.watch,
		Holds:    parts.holds,
		Intake:   parts.intake,
		// The tracker's own count of what a developer run could actually be started
		// for, which is the same reading the sink's heartbeat takes: work marked for
		// a conversation and work the product manager parked are ready to the tracker
		// and are not work anything will pull.
		Backlog:   readyBacklog{tracker: parts.tracker()},
		Stalls:    stalls,
		Threshold: threshold,
	}.Check(ctx)
	if err != nil {
		return nil, err.Error()
	}
	return &reading, ""
}

// sweepSupervision takes one voice-less pass of the management loop. A product
// whose roles have never asked each other anything has nothing here, which is
// not a failure to sweep.
func sweepSupervision(parts components) ([]orchestrator.SupervisionResult, error) {
	loop, err := supervisionLoopFrom(parts)
	if err != nil {
		return nil, err
	}
	pass, err := loop.Run()
	return pass.Results, err
}

func reportReconcileResult(stdout, stderr io.Writer, jsonOutput bool, sweep reconcileSweep, err error) int {
	results, publications, convergence, docketed := sweep.Runs, sweep.Publications, sweep.Convergence, sweep.Docketed
	// A run reconciliation could not settle stays outstanding, so the command
	// reports failure rather than folding it into the settled results. A branch
	// the sweep could not remove is the same kind of fact.
	failed := err != nil
	for _, result := range results {
		if result.Failure != "" {
			failed = true
		}
	}
	for _, branch := range convergence.Branches {
		if branch.Failure != "" {
			failed = true
		}
	}
	for _, worktree := range convergence.Worktrees {
		// A record that could not be told its checkout is gone fails the command
		// as squarely as a retirement that could not run: it is the state that
		// sends every later reader to a directory that is not there.
		if worktree.Failure != "" || worktree.RecordProblem != "" {
			failed = true
		}
	}
	if convergence.Registrations.Failure != "" {
		failed = true
	}
	// A publication left where it stands for a reason is not a failure; one the
	// forge could not be asked about, or whose corrected record could not be
	// written, is a record still disagreeing with the forge.
	for _, publication := range publications {
		if publication.Failure != "" {
			failed = true
		}
	}
	if jsonOutput {
		output := reconcileOutput{
			Runs:         results,
			Publications: publications,
			Convergence:  convergence,
			Docketed:     docketed,
			Supervision:  sweep.Supervision,
			Stall:        sweep.Stall,
			StallProblem: sweep.StallProblem,
		}
		if results == nil {
			output.Runs = []orchestrator.Reconciliation{}
		}
		if output.Supervision == nil {
			output.Supervision = []orchestrator.SupervisionResult{}
		}
		if output.Publications == nil {
			output.Publications = []orchestrator.PublicationRefresh{}
		}
		if output.Convergence.Targets == nil {
			output.Convergence.Targets = []gitworktree.Catchup{}
		}
		if output.Convergence.Branches == nil {
			output.Convergence.Branches = []orchestrator.BranchSweep{}
		}
		if output.Convergence.Worktrees == nil {
			output.Convergence.Worktrees = []orchestrator.WorktreeSweep{}
		}
		if output.Convergence.Registrations.Pruned == nil {
			output.Convergence.Registrations.Pruned = []string{}
		}
		if err != nil {
			output.Error = err.Error()
		}
		if code := writeJSON(stdout, stderr, output); code != 0 {
			return code
		}
	} else {
		if err != nil {
			fmt.Fprintf(stderr, "reconcile failed: %v\n", err)
		}
		if len(results) == 0 && err == nil {
			fmt.Fprintln(stdout, "no runs need reconciliation")
		}
		// What was docketed is said only when there is some: a line saying nothing
		// stopped, on every sweep, is a line nobody reads.
		if docketed > 0 {
			fmt.Fprintf(stdout, "%d stopped item(s) added to the triage docket for the development manager\n", docketed)
		}
		for _, result := range results {
			fmt.Fprintf(stdout, "%s (%s): %s\n", result.RunID, result.WorkItemID, result.Action)
			// What the sweep did and what became of the run are two different facts,
			// and the second is the one an operator reads this for: the action says a
			// run was settled, and this says whether their change survived it. Both
			// words are the read model's, so a run described here and the same run in
			// `yoyo status` cannot be described differently.
			if result.Settled() {
				fmt.Fprintf(stdout, "  %s, %s\n", result.Outcome, result.Artifacts().Describe())
			}
			if result.Detail != "" {
				fmt.Fprintf(stdout, "  %s\n", result.Detail)
			}
			if result.Integration != nil {
				fmt.Fprintf(stdout, "  integrated into %s: %s\n", result.Integration.TargetBranch, result.Integration.SourceCommit)
			}
			if result.Action == orchestrator.ActionCompleted {
				fmt.Fprintf(stdout, "  worktree removed: %t, branch removed: %t\n", result.WorktreeRemoved, result.BranchRemoved)
			}
			if result.Catchup != nil && result.Catchup.Advanced {
				fmt.Fprintf(stdout, "  %s caught up to %s\n", result.Catchup.TargetBranch, result.Catchup.RemoteCommit)
			}
			if result.Catchup != nil && result.Catchup.Held != "" {
				fmt.Fprintf(stderr, "  %s not caught up: %s\n", result.Catchup.TargetBranch, result.Catchup.Held)
			}
			if result.Failure != "" {
				fmt.Fprintf(stderr, "  not reconciled: %s\n", result.Failure)
			}
			// A stoppage that was settled and could not be docketed is reported
			// without the run reading as unsettled: what is missing is the
			// delivery to the development manager, not the settlement.
			if result.DocketProblem != "" {
				fmt.Fprintf(stderr, "  not docketed: %s\n", result.DocketProblem)
			}
		}
		printPublications(stdout, stderr, publications)
		printConvergence(stdout, stderr, convergence)
		printSupervision(stdout, sweep.Supervision)
		printStall(stdout, stderr, sweep.Stall, sweep.StallProblem)
	}
	if failed {
		return 1
	}
	return 0
}

// printStall says that this product has stopped doing anything, and says nothing
// at all about one that has not.
//
// A quiet machine with a drained queue, a hold somebody placed, or a run visibly
// working is the ordinary state, and a line about it on every sweep is a line
// nobody reads — which is the same rule the branch and publication sweeps print
// by. What is said is the stall while it stands, whether this sweep opened it or
// found it, because a sweep run by hand after the fact is exactly when somebody
// is asking whether the harness is alive.
//
// The second line is the one to act on: a session whose last word was `stopped`
// wants starting, and one still claiming to be watching wants killing first.
func printStall(stdout, stderr io.Writer, reading *watchdog.Reading, problem string) {
	if problem != "" {
		fmt.Fprintf(stderr, "whether this product has gone quiet was not decided: %s\n", problem)
		return
	}
	if reading == nil || reading.Standing == nil {
		return
	}
	standing := reading.Standing
	fmt.Fprintf(stdout, "nothing has started on this product for %s, with %d item(s) ready\n",
		standing.For().Round(time.Second), standing.Ready)
	if standing.Chooser != "" {
		fmt.Fprintf(stdout, "  %s\n", standing.Chooser)
	}
}

// printSupervision reports what the sweep actually did to an exchange, and
// leaves out every one it merely looked at.
//
// The division is between an outcome that changed a record and one that only
// describes what the sweep found, and it is the same rule the branch and
// publication sweeps above print by. This sweep carries no voice, so most of what
// it comes back with is the second kind: a thread waiting its turn, one another
// process is carrying, one the in-flight bound held back, one whose references
// have moved. Printing those would put a line about an exchange nothing is wrong
// with in front of the two or three that matter, on every sweep — and the noise
// is not hypothetical, since a product with more than a few open threads would
// print one for each of them every time.
//
// `--json` carries the whole pass, which is where to read the staleness and the
// queue.
func printSupervision(stdout io.Writer, results []orchestrator.SupervisionResult) {
	for _, result := range results {
		if !supervisionActed(result.Outcome) {
			continue
		}
		fmt.Fprintf(stdout, "%s: %s\n", result.ExchangeID, result.Outcome)
		if result.Detail != "" {
			fmt.Fprintf(stdout, "  %s\n", result.Detail)
		}
	}
}

// supervisionActed reports an outcome that changed a record, which is what a
// person reading a sweep is owed. Every other outcome is the sweep saying what it
// found, and is carried by `--json` rather than printed.
//
// It is stated as a closed classification rather than a list of what to skip,
// because the failure it guards against has already happened once: an outcome
// added later and left out of a skip list is printed as though the sweep had
// acted on it. TestEverySupervisionOutcomeIsClassified fails on an outcome
// neither side names, so adding one means deciding which it is.
func supervisionActed(outcome orchestrator.SupervisionOutcome) bool {
	switch outcome {
	case orchestrator.SupervisionReclaimed, orchestrator.SupervisionSettled:
		return true
	case orchestrator.SupervisionCarried, orchestrator.SupervisionQueued,
		orchestrator.SupervisionStale, orchestrator.SupervisionUndelivered:
		return false
	default:
		// An outcome nothing classifies is printed rather than swallowed: a sweep
		// that acted and said nothing is the worse of the two failures.
		return true
	}
}

// printPublications reports only the records this sweep corrected and the ones
// it could not ask about, for the reason printConvergence reports only what it
// changed: a publication that already agreed with the forge is the ordinary
// state, and a line per recorded pull request on every sweep would bury the ones
// that were actually wrong. `--json` carries the whole sweep either way.
func printPublications(stdout, stderr io.Writer, publications []orchestrator.PublicationRefresh) {
	for _, publication := range publications {
		switch {
		case publication.Failure != "":
			fmt.Fprintf(stderr, "pull request #%d not refreshed: %s\n", publication.Number, publication.Failure)
		case publication.Updated:
			fmt.Fprintf(stdout, "pull request #%d of %s recorded as %s, was %s\n",
				publication.Number, publication.WorkItemID, publication.State, publication.Recorded)
		}
	}
}

// printConvergence reports what the sweep changed and what it could not do. A
// repository already level with the forge says nothing here, and neither does a
// branch kept for a good reason: both are the status quo, and a line per target
// and per preserved branch on every sweep would bury the ones that actually need
// reading. `--json` carries the whole sweep either way.
//
// A kept checkout is said, unlike a kept branch, because it is no longer a
// category the sweep declines to act on — the work in one is captured and the
// directory retired — so anything still kept is an anomaly: a directory Git is
// not managing, a registration on a branch the run never recorded, a capture
// that could not be written. Each of those is one line that should not be
// appearing at all, rather than a standing list somebody learns to scroll past.
func printConvergence(stdout, stderr io.Writer, convergence orchestrator.Convergence) {
	for _, target := range convergence.Targets {
		switch {
		case target.Advanced:
			fmt.Fprintf(stdout, "%s caught up to %s\n", target.TargetBranch, target.RemoteCommit)
			if len(target.Discarded) > 0 {
				fmt.Fprintf(stdout, "  discarded export churn: %s\n", strings.Join(target.Discarded, ", "))
			}
		case target.Held != "":
			fmt.Fprintf(stderr, "%s not caught up: %s\n", target.TargetBranch, target.Held)
		}
	}
	for _, worktree := range convergence.Worktrees {
		switch {
		// A checkout that is gone whose run was not told so is read first, because
		// it is the only one of these where doing nothing leaves somebody being
		// sent after a directory that does not exist.
		case worktree.RecordProblem != "":
			fmt.Fprintf(stderr, "%s retired but not recorded: %s\n", worktree.Path, worktree.RecordProblem)
		// "not swept cleanly" rather than "not retired", because this covers both
		// a retirement that was refused and one that happened and could not be
		// confirmed. The message says which.
		case worktree.Failure != "":
			fmt.Fprintf(stderr, "%s not swept cleanly: %s\n", worktree.Path, worktree.Failure)
		case worktree.Removed:
			fmt.Fprintf(stdout, "%s retired: run %s is settled\n", worktree.Path, worktree.RunID)
			// Where a half-finished change went is the one thing retiring it raises,
			// so it is said next to the retirement rather than left in `--json`.
			if worktree.PreservedWork != "" {
				fmt.Fprintf(stdout, "  uncommitted work preserved at %s\n", worktree.PreservedWork)
			}
		// The run rather than the path, because the reason already names the
		// checkout and the run is how an operator finds what it was for.
		case worktree.Kept != "":
			fmt.Fprintf(stdout, "%s kept: %s\n", worktree.RunID, worktree.Kept)
		}
		// Read outside the switch because it stands beside a retirement that
		// otherwise went perfectly: the work is on the ref either way, and what is
		// missing is the work item saying so — which is the only place the person
		// who picks that item up would find it.
		if worktree.ItemProblem != "" {
			fmt.Fprintf(stderr, "%s not told where the retired work went: %s\n", worktree.WorkItemID, worktree.ItemProblem)
		}
	}
	// The prune says something only when it removed registrations or could not
	// run. A repository with none to remove is the status quo, and a line about
	// it on every sweep is a line nobody reads.
	if failure := convergence.Registrations.Failure; failure != "" {
		fmt.Fprintf(stderr, "stale worktree registrations not pruned: %s\n", failure)
	}
	if pruned := len(convergence.Registrations.Pruned); pruned > 0 {
		fmt.Fprintf(stdout, "%d stale worktree registration(s) pruned\n", pruned)
	}
	for _, branch := range convergence.Branches {
		switch {
		// A branch that is gone whose run was not told so is read first, for the
		// reason the same case is among the checkouts: it is the only one here where
		// doing nothing leaves every reader of that run being told its change is
		// preserved on a branch that does not exist.
		case branch.RecordProblem != "":
			fmt.Fprintf(stderr, "%s deleted but not recorded: %s\n", branch.Branch, branch.RecordProblem)
		case branch.Failure != "":
			fmt.Fprintf(stderr, "%s not removed: %s\n", branch.Branch, branch.Failure)
		case branch.Removed:
			fmt.Fprintf(stdout, "%s removed: %s is already in %s\n", branch.Branch, branch.Commit, branch.TargetBranch)
		}
	}
}

func printReconcileUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo reconcile [options]

Settles every run an interrupted process left outstanding, then converges local
state on the forge: each target branch is caught up onto its remote counterpart,
and the leftover branches of settled runs whose work the target already carries
are removed. Both are fast-forward-or-nothing and safe to repeat.

It also retires the leftover checkouts, so the worktree registrations a machine
carries are live runs plus a bounded tail rather than growing with the harness's
history until a command in the next worktree cannot spawn. Settled runs past the
most recent few have their checkout unregistered, and registrations whose
checkout is no longer on disk are pruned, whichever run or person left them
behind. No branch is touched by either.

A checkout holding uncommitted work is retired too, and nothing is lost doing it:
the tree is recorded first on refs/yoyodyne/preserved-work/<run-id>, which the
retirement line names and the run's own record keeps. Recover it with
"git worktree add --detach <path> <ref>". A capture that cannot be written
leaves the checkout exactly where it was.

It also re-asks the forge about the pull request of every run that ended without
its publication being settled, and records what the forge now says — merged,
closed, or still open. Nothing is merged or closed for you: the record is
brought onto the truth, so what reads it afterwards reads truth too.

It then builds the triage docket: the runs that ended on a durable blocker and
the approved publications the forge has not merged, put where the development
manager reads them. Docketing is keyed to what stopped, so sweeping twice
dockets nothing twice.

It also recovers the exchanges the roles have put to each other. Each one is
taken under its own lease, so one a live process is carrying is left alone: a
round a dead process asked and never got an answer to is closed saying so, and a
thread that has spent every round it was given is closed as unresolved and the
operator told. Nothing is put in front of a role here — recovering from a lost
process is never a reason to ask a question nobody asked for.

Last, it reads whether anything is happening at all. When nothing has started for
--stall-after, the tracker reports work ready, and no hold, no still-moving run
and no provider usage window accounts for it, that is recorded against the
product as a stall — which `+"`yoyo status`"+` reads back afterwards and the Slack sink,
if one is running, takes to the operators once. It is here because this sweep runs
whether or not reporting was ever turned on: how promptly a stopped harness is
noticed is this threshold and the cadence of whatever runs this sweep, so an
unattended pass should run at least as often as the threshold it sets.

Options:
  --config <path>    configuration file (default: the nearest .yoyodyne/config.yaml)
  --json             emit machine-readable JSON
  --stall-after <d>  how long nothing may start over ready work, with nothing
                     accounting for it, before that is recorded as a stall
                     (default 10m)`)
}
