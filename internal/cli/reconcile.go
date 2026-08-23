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
		printConvergence(stdout, stderr, convergence)
	}
	if failed {
		return 1
	}
	return 0
}

// printConvergence reports what the sweep changed, what it could not do, and
// the one thing it deliberately left that goes on costing something. A
// repository already level with the forge says nothing here, and neither does a
// branch kept for a good reason: both are the status quo, and a line per target
// and per preserved branch on every sweep would bury the ones that actually need
// reading. `--json` carries the whole sweep either way.
//
// A kept checkout is the exception, and it is not the same kind of fact as a
// kept branch. A branch costs nothing to leave; a checkout is a registration
// that every command spawned on this machine pays for, and one holding
// uncommitted work is kept however old it gets, so it is the one category the
// sweep cannot bound on its own. Saying so on every pass is what lets an
// operator deal with a handful of directories rather than meet them as the day
// commands stop spawning.
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
			fmt.Fprintf(stdout, "%s retired: run %s is settled and the checkout held nothing\n", worktree.Path, worktree.RunID)
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

func printReconcileUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo reconcile [options]

Settles every run an interrupted process left outstanding, then converges local
state on the forge: each target branch is caught up onto its remote counterpart,
and the leftover branches of settled runs whose work the target already carries
are removed. Both are fast-forward-or-nothing and safe to repeat.

It also retires the leftover checkouts, so the worktree registrations a machine
carries stop growing with the harness's history until a command in the next
worktree cannot spawn. Settled runs past the most recent few have their checkout
unregistered, and registrations whose checkout is no longer on disk are pruned,
whichever run or person left them behind. No branch is touched by either.

One category is left: a checkout holding uncommitted work is always kept, at any
age, because that work is the one thing nothing else records. Those are the
registrations this cannot bound, so each is printed with its reason on every
sweep. Commit what is worth keeping onto the run's branch or delete it, and the
next sweep takes the directory.

It then builds the triage docket: the runs that ended on a durable blocker and
the approved publications the forge has not merged, put where the development
manager reads them. Docketing is keyed to what stopped, so sweeping twice
dockets nothing twice.

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --json            emit machine-readable JSON`)
}
