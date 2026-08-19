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
	Error       string                        `json:"error,omitempty"`
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

	reconciler, err := buildReconciler(*configPath)
	if err != nil {
		return reportReconcileResult(stdout, stderr, *jsonOutput, nil, orchestrator.Convergence{}, err)
	}
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
	return reportReconcileResult(stdout, stderr, *jsonOutput, results, convergence, err)
}

func reportReconcileResult(stdout, stderr io.Writer, jsonOutput bool, results []orchestrator.Reconciliation, convergence orchestrator.Convergence, err error) int {
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
	if jsonOutput {
		output := reconcileOutput{Runs: results, Convergence: convergence}
		if results == nil {
			output.Runs = []orchestrator.Reconciliation{}
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
}

func printReconcileUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo reconcile [options]

Settles every run an interrupted process left outstanding, then converges local
state on the forge: each target branch is caught up onto its remote counterpart,
and the leftover branches of settled runs whose work the target already carries
are removed. Both are fast-forward-or-nothing and safe to repeat.

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --json            emit machine-readable JSON`)
}
