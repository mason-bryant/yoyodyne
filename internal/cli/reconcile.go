package cli

import (
	"context"
	"flag"
	"fmt"
	"io"

	"yoyodyne/internal/orchestrator"
)

type reconcileOutput struct {
	Runs  []orchestrator.Reconciliation `json:"runs"`
	Error string                        `json:"error,omitempty"`
}

// reconcileRuns settles every run an interrupted process left outstanding. It
// is safe to repeat: a run that is already settled is not outstanding, and the
// steps it does take are idempotent over artifacts that are already gone.
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
		return reportReconcileResult(stdout, stderr, *jsonOutput, nil, err)
	}
	results, err := reconciler.Reconcile(ctx)
	return reportReconcileResult(stdout, stderr, *jsonOutput, results, err)
}

func reportReconcileResult(stdout, stderr io.Writer, jsonOutput bool, results []orchestrator.Reconciliation, err error) int {
	// A run reconciliation could not settle stays outstanding, so the command
	// reports failure rather than folding it into the settled results.
	failed := err != nil
	for _, result := range results {
		if result.Failure != "" {
			failed = true
		}
	}
	if jsonOutput {
		output := reconcileOutput{Runs: results}
		if results == nil {
			output.Runs = []orchestrator.Reconciliation{}
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
			if result.Failure != "" {
				fmt.Fprintf(stderr, "  not reconciled: %s\n", result.Failure)
			}
		}
	}
	if failed {
		return 1
	}
	return 0
}

func printReconcileUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyodyne reconcile [options]

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --json            emit machine-readable JSON`)
}
