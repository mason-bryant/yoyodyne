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
		return reportReconcileResult(stdout, stderr, *jsonOutput, nil, nil, orchestrator.Convergence{}, 0, err)
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
	return reportReconcileResult(stdout, stderr, *jsonOutput, results, publications, convergence, docketed.Added, err)
}

func reportReconcileResult(stdout, stderr io.Writer, jsonOutput bool, results []orchestrator.Reconciliation, publications []orchestrator.PublicationRefresh, convergence orchestrator.Convergence, docketed int, err error) int {
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
	// A publication left where it stands for a reason is not a failure; one the
	// forge could not be asked about, or whose corrected record could not be
	// written, is a record still disagreeing with the forge.
	for _, publication := range publications {
		if publication.Failure != "" {
			failed = true
		}
	}
	if jsonOutput {
		output := reconcileOutput{Runs: results, Publications: publications, Convergence: convergence, Docketed: docketed}
		if results == nil {
			output.Runs = []orchestrator.Reconciliation{}
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
}

func printReconcileUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo reconcile [options]

Settles every run an interrupted process left outstanding, then converges local
state on the forge: each target branch is caught up onto its remote counterpart,
and the leftover branches of settled runs whose work the target already carries
are removed. Both are fast-forward-or-nothing and safe to repeat.

It also re-asks the forge about the pull request of every run that ended without
its publication being settled, and records what the forge now says — merged,
closed, or still open. Nothing is merged or closed for you: the record is
brought onto the truth, so what reads it afterwards reads truth too.

It then builds the triage docket: the runs that ended on a durable blocker and
the approved publications the forge has not merged, put where the development
manager reads them. Docketing is keyed to what stopped, so sweeping twice
dockets nothing twice.

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --json            emit machine-readable JSON`)
}
