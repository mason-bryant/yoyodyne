package cli

// Carrying out what triage decided.
//
// The development manager decides what becomes of work that stopped and records
// the decision on the work item; the harness is what acts on one. Two of those
// actions exist, and they are the two opposite answers to a run that stopped: a
// re-run, which is what a correct change whose ground moved needs, and a repair,
// which continues the run that stopped on the change it already has.
//
// The decision is not made here and cannot be. What each takes is the run the
// docket entry names and the reasoning the development manager recorded, and
// what it does with them is the harness's own work — reading the intake hold,
// proving the stoppage is over, and then either claiming the one re-run that
// stoppage gets and starting a fresh run, or spending the item's repair grant,
// superseding the blocker, and continuing the run that stopped.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type triageOutput struct {
	Rerun  *orchestrator.RerunResult          `json:"rerun,omitempty"`
	Repair *orchestrator.RepairContinueResult `json:"repair,omitempty"`
	Error  string                             `json:"error,omitempty"`
}

func runTriage(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printTriageUsage(stdout)
		return 0
	}
	switch args[0] {
	case "rerun":
		return rerunStoppage(ctx, args[1:], stdout, stderr)
	case "repair":
		return repairStoppage(ctx, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown triage command %q\n\n", args[0])
		printTriageUsage(stderr)
		return 2
	}
}

func rerunStoppage(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("triage rerun", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	reason := flags.String("reason", "", "the development manager's recorded reasoning for deciding a re-run")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 1 {
		fmt.Fprintln(stderr, "triage rerun requires exactly one run identifier, the run the docket entry names")
		printTriageUsage(stderr)
		return 2
	}

	rerunner, err := buildRerunner(*configPath)
	if err != nil {
		return reportRerun(stdout, stderr, *jsonOutput, orchestrator.RerunResult{}, err)
	}
	result, err := rerunner.Rerun(ctx, orchestrator.RerunRequest{Run: positional[0], Reason: *reason})
	return reportRerun(stdout, stderr, *jsonOutput, result, err)
}

func repairStoppage(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("triage repair", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	reason := flags.String("reason", "", "the development manager's recorded reasoning for deciding a repair")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 1 {
		fmt.Fprintln(stderr, "triage repair requires exactly one run identifier, the run the docket entry names")
		printTriageUsage(stderr)
		return 2
	}

	continuer, err := buildRepairContinuer(*configPath)
	if err != nil {
		return reportRepair(stdout, stderr, *jsonOutput, orchestrator.RepairContinueResult{}, err)
	}
	result, err := continuer.Continue(ctx, orchestrator.RepairContinueRequest{Run: positional[0], Reason: *reason})
	return reportRepair(stdout, stderr, *jsonOutput, result, err)
}

// buildRepairContinuer wires the repair-continue action over the same parts the
// re-run beside it acts on, so the docket it reads, the runs it proves the
// stoppage from, and the pipeline it continues are the ones the rest of the
// harness uses.
func buildRepairContinuer(configPath string) (orchestrator.RepairContinuer, error) {
	parts, err := buildComponents(configPath)
	if err != nil {
		return orchestrator.RepairContinuer{}, err
	}
	return orchestrator.RepairContinuer{
		Docket: parts.docket,
		Runs:   parts.store,
		Intake: parts.intake,
		// The same per-item counters the development manager's decision spends and
		// `yoyo status` reports, which is what says a grant was made and how much
		// of it the round cap left. What proves the decision and what an operator
		// reads about it can never be two different records.
		Decisions: parts.store.Triage(),
		// The budget the grant is added to, which is the operator's number rather
		// than this action's.
		ConfiguredAttempts: parts.config.Execution.RepairAttemptsBeforeReplan,
		// The item the stopped run blocked, and the worktree it stopped in. Both
		// are read before anything is spent: the item because a blocked one is not
		// one the pipeline resumes, and the worktree because what a continued
		// developer is handed back is whatever is in it.
		Items:     parts.tracker(),
		Worktrees: parts.worktrees,
		// The same limit the reservation enforces, read before the grant so a full
		// harness leaves the decision standing rather than spending the item's
		// grant on a run there is no room to continue.
		Capacity: parts.config.Execution.MaxConcurrentDevelopers,
		Start: func(ctx context.Context, workItemID string) (orchestrator.Outcome, error) {
			// No selection is stamped: this continues the run that was already
			// reserved for this item, and why that run exists was recorded when it
			// was. Why it is going again is on the run and the item already.
			return pipelineFrom(parts).Run(ctx, workItemID)
		},
	}, nil
}

// reportRepair describes what the action did. A refusal before anything was
// spent, an intake hold, a full harness, and a continuation whose run then
// failed are four different things for an operator to do something about.
func reportRepair(stdout, stderr io.Writer, jsonOutput bool, result orchestrator.RepairContinueResult, err error) int {
	if jsonOutput {
		output := triageOutput{}
		if result.WorkItemID != "" || result.RunID != "" {
			output.Repair = &result
		}
		if err != nil {
			output.Error = err.Error()
		}
		if code := writeJSON(stdout, stderr, output); code != 0 {
			return code
		}
		if err != nil {
			return 1
		}
		return 0
	}
	if !result.Continued {
		// A hold and a full harness are states the carry-out is waiting on rather
		// than failures: nothing was spent, and the decision stands to be carried
		// out by asking again.
		if result.IntakeHeld != nil || result.CapacityFull != nil {
			fmt.Fprint(stdout, result.Render())
			return 0
		}
		fmt.Fprintf(stderr, "the repair was refused and nothing was continued: %v\n", err)
		if errors.Is(err, orchestrator.ErrWorktreeNotAsLeft) {
			fmt.Fprintln(stderr, "nothing was spent and the item is still blocked: say what became of that worktree before the run is continued")
		}
		// The twin refusal, and the one an operator can most easily act on: the
		// worktree is the harness's own and empty, so the change is on the branch
		// the run recorded if it survived at all.
		if errors.Is(err, orchestrator.ErrPreservedChangeMissing) {
			fmt.Fprintln(stderr, "nothing was spent and the item is still blocked: the run's branch is where the preserved change is, so put that worktree back on the change before the run is continued")
		}
		return 1
	}
	fmt.Fprint(stdout, result.Render())
	// The continued run reports itself exactly as `yoyo run` reports one, because
	// it is the same run: what it integrated and what its agents reported are the
	// same facts however the run was picked up again.
	return reportRunResult(stdout, stderr, false, result.Outcome, err)
}

// buildRerunner wires the re-run action over the same parts every other command
// acts on, so the docket it reads, the runs it proves the stoppage from, and the
// pipeline it starts are the ones the rest of the harness uses.
func buildRerunner(configPath string) (orchestrator.Rerunner, error) {
	parts, err := buildComponents(configPath)
	if err != nil {
		return orchestrator.Rerunner{}, err
	}
	return orchestrator.Rerunner{
		Docket: parts.docket,
		Runs:   parts.store,
		Intake: parts.intake,
		Reruns: parts.store.Reruns(),
		// The same per-item counters the development manager's decision spends and
		// `yoyo status` reports, so what proves the decision was made and what an
		// operator reads about it can never be two different records.
		Decisions: parts.store.Triage(),
		// The item itself, read before the stoppage's one re-run is claimed. It is
		// the same tracker the fresh run starts on, so what refuses here is exactly
		// what would otherwise have refused past the claim.
		Items: parts.tracker(),
		// The same limit the reservation enforces, read before the claim so a full
		// harness leaves the decision standing rather than spending the stoppage's
		// re-run on a run that would find no slot.
		Capacity: parts.config.Execution.MaxConcurrentDevelopers,
		// What the stopped run preserved is retired through the same manager that
		// created it, which is what keeps the removal inside the ownership rules
		// every other removal here is held to.
		Preserved: parts.worktrees,
		Start: func(ctx context.Context, workItemID string, selection runstate.Selection) (orchestrator.Outcome, error) {
			// The pipeline is a value, so the run this starts carries its own
			// selection: the development manager's decision, which is also what
			// makes the intake hold apply to it.
			pipeline := pipelineFrom(parts)
			pipeline.Selection = selection
			return pipeline.Run(ctx, workItemID)
		},
	}, nil
}

// reportRerun describes what the action did. A re-run that was refused before
// anything was claimed, one intake held, and one whose fresh run then failed are
// three different things for an operator to do something about, so none of them
// is reported as any of the others.
func reportRerun(stdout, stderr io.Writer, jsonOutput bool, result orchestrator.RerunResult, err error) int {
	if jsonOutput {
		output := triageOutput{}
		if result.WorkItemID != "" || result.PriorRunID != "" {
			output.Rerun = &result
		}
		if err != nil {
			output.Error = err.Error()
		}
		if code := writeJSON(stdout, stderr, output); code != 0 {
			return code
		}
		if err != nil {
			return 1
		}
		return 0
	}
	if !result.Started {
		// A hold and a full harness are both states the carry-out is waiting on
		// rather than failures: nothing was claimed, and the decision stands to be
		// carried out by asking again. A record that could not be written while
		// waiting is a failure, because the stoppage has then paid for a wait that
		// was meant to cost it nothing.
		if result.IntakeHeld != nil || result.CapacityFull != nil {
			fmt.Fprint(stdout, result.Render())
			if result.RecordProblem != "" {
				return 1
			}
			return 0
		}
		// Nothing was started and intake is not held, so the action refused, and
		// a refusal always says why: it is the only way out of that path.
		fmt.Fprintf(stderr, "the re-run was refused and nothing was started: %v\n", err)
		if errors.Is(err, runstate.ErrRerunTaken) {
			fmt.Fprintln(stderr, "triage acts on one docketed stoppage once; a second is an escalation rather than a larger budget")
		}
		return 1
	}
	fmt.Fprint(stdout, result.Render())
	// The fresh run reports itself exactly as `yoyo run` reports one, because it
	// is the same run: what it integrated, what it preserved, and what its agents
	// reported are the same facts however the run was started.
	return reportRunResult(stdout, stderr, false, result.Outcome, err)
}

func printTriageUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo triage rerun  [options] <run-id>
       yoyo triage repair [options] <run-id>

Both carry out a decision the development manager recorded about a docketed
stoppage, and they are opposites. "rerun" starts a fresh run of the item, which
is what a correct change whose ground moved needs. "repair" continues the run
that stopped -- same branch, same worktree, same developer session, the
reviewer's findings handed back exactly as they were written -- under a grant of
further repair attempts sized by triage.repair_grant_attempts.

Everything that refuses is asked before anything is claimed or granted, so a
refusal costs nothing and asking again once it no longer applies carries out the
same decision.

A re-run is claimed once per docketed stoppage, and the item has to be one a run
may start on, which for a run that stopped on a blocker means putting the item
back first. A repair needs no such reopening: it supersedes the blocker itself,
on the item and on the run's own record. It is refused once the item's repair
grant is spent or the review-round cap has no room left, and refused to a person
if the preserved worktree is not as the harness left it, or holds none of the
change it is a repair of -- what is in that worktree is what a continued
developer would be handed back, and an empty one buys an empty repair.

The intake hold applies to both, because the harness is the one spending here.

A harness with no free developer is not a refusal at all: nothing is claimed or
granted, the decision stands, and asking again once a slot frees carries out the
same one.

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --reason <text>   the development manager's recorded reasoning (required)
  --json            emit machine-readable JSON`)
}
