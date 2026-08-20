package cli

// Carrying out what triage decided.
//
// The development manager decides what becomes of work that stopped and records
// the decision on the work item; the harness is what acts on one. This is the
// first of those actions to exist: a re-run, which is what a correct change
// whose ground moved needs.
//
// The decision is not made here and cannot be. What this takes is the run the
// docket entry names and the reasoning the development manager recorded for
// deciding a re-run of it, and what it does with them is the harness's own
// work — reading the intake hold, proving the stoppage is over, claiming the one
// re-run that stoppage gets, and starting the fresh run with the decision
// recorded as why it exists.

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
	Rerun *orchestrator.RerunResult `json:"rerun,omitempty"`
	Error string                    `json:"error,omitempty"`
}

func runTriage(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printTriageUsage(stdout)
		return 0
	}
	switch args[0] {
	case "rerun":
		return rerunStoppage(ctx, args[1:], stdout, stderr)
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
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintln(stderr, "triage rerun requires exactly one run identifier, the run the docket entry names")
		printTriageUsage(stderr)
		return 2
	}

	rerunner, err := buildRerunner(*configPath)
	if err != nil {
		return reportRerun(stdout, stderr, *jsonOutput, orchestrator.RerunResult{}, err)
	}
	result, err := rerunner.Rerun(ctx, orchestrator.RerunRequest{Run: flags.Arg(0), Reason: *reason})
	return reportRerun(stdout, stderr, *jsonOutput, result, err)
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
		if result.IntakeHeld != nil {
			fmt.Fprint(stdout, result.Render())
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
	fmt.Fprintln(writer, `Usage: yoyo triage rerun [options] <run-id>

Carries out a development manager's recorded re-run decision: starts a fresh run
of the item whose stopped run the docket entry names. The stoppage is re-run
once; the intake hold applies, because the harness is choosing the work.

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --reason <text>   the development manager's recorded reasoning (required)
  --json            emit machine-readable JSON`)
}
