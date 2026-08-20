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
// work — reading the intake hold, proving the stoppage is over, reading that the
// item is one a run may start on, claiming the one re-run that stoppage gets, and
// starting the fresh run with the decision recorded as why it exists.

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
	fmt.Fprintln(writer, `Usage: yoyo triage rerun [options] <run-id>

Carries out a development manager's recorded re-run decision: starts a fresh run
of the item whose stopped run the docket entry names. The stoppage is re-run
once; the intake hold applies, because the harness is choosing the work.

Everything that refuses is asked before the stoppage's one re-run is claimed, so
a refusal costs nothing and asking again once it no longer applies carries out
the same decision. The item has to be one a run may start on, which for a run
that stopped on a blocker means putting the item back first.

A harness with no free developer is not a refusal at all: nothing is claimed, the
decision stands, and asking again once a slot frees carries out the same one. The
item is open work the scheduler pulls from meanwhile, so it can reach a developer
without this being asked again.

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --reason <text>   the development manager's recorded reasoning (required)
  --json            emit machine-readable JSON`)
}
