package cli

// `yoyo work`: the harness choosing what to run, rather than being told.
//
// `yoyo run <id>` is the operator naming an item, and it stays exactly what it
// was. This is the other entry point the design names — ready work scheduled by
// the harness itself, several items at once where the configuration allows it —
// and everything that separates the two lives in one place: the intake hold
// applies here and not there, and every run this starts records why it was
// chosen.

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/goal"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/staleness"
)

type scheduleOutput struct {
	Schedule *orchestrator.Schedule `json:"schedule,omitempty"`
	Error    string                 `json:"error,omitempty"`
}

// scheduleWork pulls ready work from the backlog and runs it, up to the
// configured developer capacity, returning once every run it started has ended.
func scheduleWork(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("work", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	limit := flags.Int("limit", 0, "stop after starting this many runs (default: drain what is ready)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "work does not accept positional arguments")
		printWorkUsage(stderr)
		return 2
	}
	if *limit < 0 {
		fmt.Fprintln(stderr, "--limit cannot be negative")
		return 2
	}

	scheduler := orchestrator.Scheduler{
		Limit: *limit,
		// The configuration is read here rather than above, once per pull. That
		// is the decision the design question asked for: capacity and priority
		// changes take effect at the next selection, which keeps the answer the
		// one every other command already gives and matches how the backlog is
		// steered. A run already in flight keeps the configuration its own pull
		// read, because a run's parameters are fixed when it is reserved.
		Open: func(context.Context) (orchestrator.Pull, error) { return openPull(*configPath) },
	}
	schedule, err := scheduler.Schedule(ctx)
	return reportSchedule(stdout, stderr, *jsonOutput, schedule, err)
}

// openPull builds everything one pull acts through from one reading of the
// configuration. The parts are captured by the starter it returns, so each run
// this pull begins uses the configuration this pull read.
func openPull(configPath string) (orchestrator.Pull, error) {
	parts, err := buildComponents(configPath)
	if err != nil {
		return orchestrator.Pull{}, err
	}
	tracker := parts.tracker()
	return orchestrator.Pull{
		Tracker:    tracker,
		Runs:       parts.store,
		Intake:     parts.intake,
		Directives: parts.directives,
		Staleness: repositoryStaleness{
			repository: parts.repository,
			product:    parts.config.Product,
			tracker:    tracker,
		},
		Capacity: parts.config.Execution.MaxConcurrentDevelopers,
		Start: func(ctx context.Context, workItemID string, selection runstate.Selection) (orchestrator.Outcome, error) {
			// The pipeline is a value, so each run gets its own with its own
			// selection on it. Two runs started from one pull therefore record
			// separately why each of them was started.
			pipeline := pipelineFrom(parts)
			pipeline.Selection = selection
			return pipeline.Run(ctx, workItemID)
		},
	}, nil
}

// repositoryStaleness reads what changed upstream of the admitted work, from the
// same documents and the same tracker `yoyo stale` reads. It decides nothing
// here: what it produces goes into the recorded reason a run was chosen, so
// somebody reading what the harness picked can see that an item's goal had moved
// under it.
type repositoryStaleness struct {
	repository string
	product    config.Product
	tracker    beads.Client
}

func (s repositoryStaleness) Stale(ctx context.Context) ([]staleness.WorkItem, error) {
	artifacts, err := artifactStore(s.repository, s.product).Load()
	if err != nil {
		return nil, fmt.Errorf("read the recorded artifacts: %w", err)
	}
	admitted, err := admittedWorkItems(ctx, s.tracker)
	if err != nil {
		return nil, err
	}
	return staleness.Survey(artifacts, goal.Collect(s.repository, artifacts), admitted).WorkItems, nil
}

func reportSchedule(stdout, stderr io.Writer, jsonOutput bool, schedule orchestrator.Schedule, err error) int {
	if jsonOutput {
		output := scheduleOutput{Schedule: &schedule}
		if err != nil {
			output.Error = err.Error()
		}
		if code := writeJSON(stdout, stderr, output); code != 0 {
			return code
		}
	} else {
		fmt.Fprint(stdout, schedule.Render())
		if schedule.IntakeHeld != nil {
			fmt.Fprintln(stdout, "/release in a conversation lets the harness choose work again, and `yoyo run <id>` runs one item now regardless")
		}
		if err != nil {
			fmt.Fprintf(stderr, "scheduling stopped: %v\n", err)
		}
	}
	// A run that failed is reported as a failure, and so is a pass that could not
	// keep pulling. A declined start is neither: the work went to another
	// process, which is what two schedulers sharing one capacity look like when
	// they are working.
	if err != nil || schedule.Failed() {
		return 1
	}
	return 0
}

func printWorkUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo work [options]

Pulls ready work from the backlog and runs it, up to
execution.max_concurrent_developers at once, in a worktree of its own per run.
It returns once every run it started has ended.

Items are taken in the product manager's order -- highest priority first -- and
only where the tracker itself reports them as ready to pull, so dependencies are
the tracker's answer rather than this command's guess. An item an unresolved
directive pauses is named and skipped, and every run records why the harness
chose the item it did.

The configuration is re-read before every pull, so a change to capacity or to
the backlog's priorities takes effect at the next selection. Runs already in
flight keep the configuration they started under.

Holding intake stops it choosing anything more; runs already going carry on to
their own end. An item you name with "yoyo run" is not subject to that hold.

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --limit <n>       stop after starting this many runs (default: drain what is ready)
  --json            emit machine-readable JSON`)
}
