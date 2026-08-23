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
	Runs        []orchestrator.Reconciliation `json:"runs"`
	Convergence orchestrator.Convergence      `json:"convergence"`
	// Docketed is how many entries this sweep is what put on the triage docket.
	// It is a count rather than the entries because the docket is read where it
	// is acted on, which is the development manager's conversation; what this
	// command reports is that the sweep found something, not what it found.
	Docketed int    `json:"docketed"`
	Error    string `json:"error,omitempty"`
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
		return reportReconcileResult(stdout, stderr, *jsonOutput, nil, orchestrator.Convergence{}, 0, err)
	}
	reconciler := reconcilerFrom(parts)
	results, err := reconciler.Reconcile(ctx)
	// Convergence is swept even when settling a run failed. The two are
	// independent — one finishes runs, the other finishes branches — and a
	// checkout left behind the forge because some unrelated run could not be
	// settled is exactly the manual seam this exists to close.
	convergence, convergeErr := reconciler.Converge(ctx)
	if err == nil {
		err = convergeErr
	} else if convergeErr != nil {
		err = errors.Join(err, convergeErr)
	}
	// The docket is built for the same reason the convergence sweep runs: this
	// is one of the two moments anything scans. A publication the forge quietly
	// never merged is not an event anybody can be present for, so it is found
	// here or when a development manager opens a conversation, and a sweep that
	// settled runs without looking would leave it for the other one.
	docketed, docketErr := docketerFrom(parts).Build()
	if docketErr != nil {
		err = errors.Join(err, docketErr)
	}
	return reportReconcileResult(stdout, stderr, *jsonOutput, results, convergence, docketed.Added, err)
}

func reportReconcileResult(stdout, stderr io.Writer, jsonOutput bool, results []orchestrator.Reconciliation, convergence orchestrator.Convergence, docketed int, err error) int {
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
	// A publication left open is a false signal on the forge rather than
	// something at risk, but it is exactly the state this sweep exists to end, so
	// a sweep that could not end it reports failure like the rest.
	for _, publication := range convergence.Publications {
		if publication.Failure != "" {
			failed = true
		}
	}
	if jsonOutput {
		output := reconcileOutput{Runs: results, Convergence: convergence, Docketed: docketed}
		if results == nil {
			output.Runs = []orchestrator.Reconciliation{}
		}
		if output.Convergence.Targets == nil {
			output.Convergence.Targets = []gitworktree.Catchup{}
		}
		if output.Convergence.Branches == nil {
			output.Convergence.Branches = []orchestrator.BranchSweep{}
		}
		if output.Convergence.Publications == nil {
			output.Convergence.Publications = []orchestrator.PublicationSweep{}
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
		printConvergence(stdout, stderr, convergence)
	}
	if failed {
		return 1
	}
	return 0
}

// printConvergence reports only what the sweep changed and what it could not
// do. A repository already level with the forge says nothing here, and neither
// does a branch kept for a good reason: both are the status quo, and a line per
// target and per preserved branch on every sweep would bury the ones that
// actually need reading. `--json` carries the whole sweep either way.
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
	for _, branch := range convergence.Branches {
		switch {
		case branch.Failure != "":
			fmt.Fprintf(stderr, "%s not removed: %s\n", branch.Branch, branch.Failure)
		case branch.Removed:
			fmt.Fprintf(stdout, "%s removed: %s is already in %s\n", branch.Branch, branch.Commit, branch.TargetBranch)
		}
	}
	// A pull request somebody else had already closed says nothing here, for the
	// reason a branch already gone does: it is the status quo, and this sweep
	// only reports what it changed and what it could not do.
	for _, publication := range convergence.Publications {
		switch {
		case publication.Failure != "":
			fmt.Fprintf(stderr, "pull request #%d not closed: %s\n", publication.Number, publication.Failure)
		case publication.Closed:
			fmt.Fprintf(stdout, "pull request #%d closed: %s superseded it\n", publication.Number, publication.SupersededBy.Vehicle())
		}
		// The worktree is reported apart from the request, because it is on this
		// machine rather than on the forge and because one can be dealt with while
		// the other is not. A worktree kept says why: it is the reason the branch
		// under it survives the branch sweep too.
		switch {
		case publication.Worktree.Removed:
			fmt.Fprintf(stdout, "released the worktree run %s kept: %s\n", publication.RunID, publication.Worktree.Path)
		case publication.Worktree.Kept != "":
			fmt.Fprintf(stderr, "worktree of run %s kept: %s\n", publication.RunID, publication.Worktree.Kept)
		}
	}
}

func printReconcileUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo reconcile [options]

Settles every run an interrupted process left outstanding, then converges local
state on the forge: each target branch is caught up onto its remote counterpart,
and the leftover branches of settled runs whose work the target already carries
are removed. Both are fast-forward-or-nothing and safe to repeat.

It also retires the runs whose work landed by another vehicle — a relaunch after
a killed run, the loser of a duplicate selection. Each one's pull request is
closed with a comment naming the pull request or commit the work actually landed
by, the branch it published is deleted, and the worktree it kept is released so
nothing holds its branch. A request that merged is never touched, and a worktree
holding uncommitted work is kept with the reason.

It then builds the triage docket: the runs that ended on a durable blocker and
the approved publications the forge has not merged, put where the development
manager reads them. Docketing is keyed to what stopped, so sweeping twice
dockets nothing twice.

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --json            emit machine-readable JSON`)
}
