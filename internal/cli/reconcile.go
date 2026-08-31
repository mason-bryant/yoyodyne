package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/gitworktree"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
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
	// Supervision is what this sweep made of the requests the roles have put to
	// each other: the ones whose carrier is gone, the answers a dead process
	// recorded and never closed, and the ones that ran out of attempts. It is
	// reported beside the runs because it is the same recovery — a process died
	// holding something — over a different kind of record.
	Supervision []orchestrator.SupervisionResult `json:"supervision"`
	Error       string                           `json:"error,omitempty"`
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
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "reconcile does not accept positional arguments")
		printReconcileUsage(stderr)
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
	// The requests the roles have put to each other are recovered here for the
	// same reason the runs are: a process died holding something, and this is the
	// sweep that finds out. It takes each request's own lease, so one running
	// beside this sweep is left to the process carrying it, and it invokes
	// nothing.
	supervision, supervisionErr := sweepSupervision(ctx, parts)
	err = errors.Join(err, supervisionErr)
	return reportReconcileResult(stdout, stderr, *jsonOutput, reconcileSweep{
		Runs:         results,
		Publications: publications,
		Convergence:  convergence,
		Docketed:     docketed.Added,
		Supervision:  supervision,
	}, err)
}

// sweepSupervision takes one voice-less pass of the management loop. A product
// whose roles have never asked each other anything has nothing here, which is
// not a failure to sweep.
func sweepSupervision(ctx context.Context, parts components) ([]orchestrator.SupervisionResult, error) {
	loop, err := supervisionLoopFrom(parts)
	if err != nil {
		return nil, err
	}
	pass, err := loop.Run(ctx)
	return pass.Results, err
}

// reconcileSweep is everything one sweep found, gathered so the reporting takes
// the sweep rather than a growing list of positional arguments.
type reconcileSweep struct {
	Runs         []orchestrator.Reconciliation
	Publications []orchestrator.PublicationRefresh
	Convergence  orchestrator.Convergence
	Docketed     int
	Supervision  []orchestrator.SupervisionResult
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
	}
	if failed {
		return 1
	}
	return 0
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
		case branch.Failure != "":
			fmt.Fprintf(stderr, "%s not removed: %s\n", branch.Branch, branch.Failure)
		case branch.Removed:
			fmt.Fprintf(stdout, "%s removed: %s is already in %s\n", branch.Branch, branch.Commit, branch.TargetBranch)
		}
	}
}

// printSupervision reports what the sweep did with the requests the roles have
// put to each other, and says nothing where it did nothing — which is nearly
// every sweep, since a request being carried by a live process is the ordinary
// state and a line about it every time is a line nobody reads.
func printSupervision(stdout io.Writer, results []orchestrator.SupervisionResult) {
	for _, result := range results {
		fmt.Fprintf(stdout, "%s: %s\n", result.RequestID, result.Outcome)
		if result.Detail != "" {
			fmt.Fprintf(stdout, "  %s\n", result.Detail)
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

It also recovers the requests the roles have put to each other. Each one is
taken under its own lease, so one a live process is carrying is left alone: an
answer a dead process recorded and never got to close is closed rather than
asked for again, and a request that ran out of attempts is ended and reported
where the rest of what the harness noticed is read. Nothing is put in front of a
role here — recovering from a lost process is never a reason to start an
invocation nobody asked for.

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --json            emit machine-readable JSON`)
}
