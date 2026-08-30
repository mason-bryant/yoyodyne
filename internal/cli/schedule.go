package cli

// `yoyo work`: the harness choosing what to run, rather than being told.
//
// `yoyo run <id>` is the operator naming an item, and it stays exactly what it
// was. This is the other entry point the design names — ready work scheduled by
// the harness itself, several items at once where the configuration allows it —
// and everything that separates the two lives in one place: the intake hold
// applies here and not there, and every run this starts records why it was
// chosen.
//
// It drains by default and watches when asked. That way round is deliberate and
// it is temporary: watching is the shape the loop is meant to have, and it waits
// on stopped work having an owner before it becomes what an operator gets
// without asking for it. `--until-drained` states today's default out loud so
// that flipping it is one line here rather than a behaviour change nobody wrote
// down.

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
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
	limit := flags.Int("limit", 0, "stop after starting this many runs (default: no bound on how many)")
	watch := flags.Bool("watch", false, "keep pulling work as it becomes ready, until stopped")
	untilDrained := flags.Bool("until-drained", false, "return once nothing more is ready to pull (the default)")
	budget := flags.Float64("budget", 0, "stop once the runs this session started have cost this many dollars (default: unbounded)")
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
	if *budget < 0 {
		fmt.Fprintln(stderr, "--budget cannot be negative; leave it out for an unbounded session")
		return 2
	}
	// Asking for both is asking for opposite things, and guessing which one was
	// meant is how a session ends up running all night that was meant to return.
	if *watch && *untilDrained {
		fmt.Fprintln(stderr, "--watch and --until-drained ask for opposite things: one stays open, the other returns when the queue is empty")
		return 2
	}

	scheduler := orchestrator.Scheduler{
		Limit:    *limit,
		Watching: *watch,
		Budget:   *budget,
		// The configuration is read here rather than above, once per pull. That
		// is the decision the design question asked for: capacity and priority
		// changes take effect at the next selection, which keeps the answer the
		// one every other command already gives and matches how the backlog is
		// steered. A run already in flight keeps the configuration its own pull
		// read, because a run's parameters are fixed when it is reserved.
		Open: func(context.Context) (orchestrator.Pull, error) { return openPull(*configPath) },
	}
	// A session that stays open says so where somebody who is not at this
	// terminal can read it. Failing to open that log fails the command rather
	// than starting a session nothing can see: an unattended session nobody can
	// tell is alive is the state the whole guard exists to prevent, and it is
	// better refused at the start than discovered in the morning.
	if *watch {
		sessions, err := openWatchSession(*configPath)
		if err != nil {
			fmt.Fprintf(stderr, "work failed: %v\n", err)
			return 1
		}
		scheduler.Sessions = sessions
	}
	schedule, err := scheduler.Schedule(ctx)
	return reportSchedule(stdout, stderr, *jsonOutput, schedule, err)
}

// openWatchSession names one session of watching and gives it somewhere durable
// to say what it is doing. The identifier is made here rather than in the
// scheduler because it is the session's identity rather than the loop's, and
// because a scheduler that generated one would have no way to tell a caller
// which session it had just written under.
func openWatchSession(configPath string) (orchestrator.WatchSessions, error) {
	parts, err := buildComponents(configPath)
	if err != nil {
		return nil, err
	}
	sessionID, err := runstate.NewWatchSessionID()
	if err != nil {
		return nil, err
	}
	return watchSessionLog{store: parts.watch, productID: parts.config.Product.ID, sessionID: sessionID}, nil
}

// watchSessionLog is the scheduler's account of itself, written into the
// product's watch log. It carries the identity the scheduler has no business
// knowing about — which product, which session — and nothing else.
type watchSessionLog struct {
	store     *runstate.WatchStore
	productID domain.ProductID
	sessionID string
}

func (w watchSessionLog) Record(state runstate.WatchState, at time.Time, reason string) error {
	return w.store.Record(runstate.WatchTransition{
		SchemaVersion: runstate.WatchSchemaVersion,
		ProductID:     w.productID,
		SessionID:     w.sessionID,
		State:         state,
		At:            at,
		Reason:        reason,
	})
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
		// Whether the machine can start a run at all is read from the same
		// worktree manager a run checks it with, so a session's account of what
		// stopped the line and a run's own refusal can never disagree.
		Environment:                 parts.worktrees,
		Capacity:                    parts.config.Execution.MaxConcurrentDevelopers,
		Poll:                        parts.config.Execution.WorkPoll.Duration(),
		BlockedRunsBeforeIntakeHold: parts.config.Execution.BlockedRunsBeforeIntakeHold,
		// The brake places the operator's own switch, so it is the same store
		// the hold is read from. Nothing here releases it.
		Brake: parts.intake,
		// What a session has spent is read from the same recorded run evidence
		// `yoyo cost` prices items from, so a bounded session and a ledger can
		// never disagree about what a run cost.
		Spend: parts.store,
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
			// The remedy is named as something runnable from here. `/release` in a
			// conversation lifts the same hold, but somebody reading this at a
			// terminal with no conversation open was being told a remedy they had
			// no way to reach.
			fmt.Fprintln(stdout, "`yoyo release` lets the harness choose work again, as does /release in a conversation, and `yoyo run <id>` runs one item now regardless")
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
It returns once nothing more is ready and every run it started has ended.

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

With --watch it does not return when the queue empties: it waits out
execution.work_poll and reads the queue again, until you stop it with Ctrl-C.
Nothing is cached between readings, so work you admit or reorder is picked up at
the next poll, and an idle session costs one tracker read per interval and no
provider call at all. Holding intake brakes a watching session in place -- it
keeps polling and chooses nothing -- and "yoyo release" resumes it.

A watching session guards itself three ways. It does not start the same item
twice unless the item has changed -- what it says, what it is for, its priority,
its status, what it depends on, its notes -- so a start the harness cannot get
past is not retried every interval, and a blocker you release is picked up
because releasing it changed the item. Runs blocking one after another with
nothing landing between them hold intake at
execution.blocked_runs_before_intake_hold, and it stays held until "yoyo release"
lifts it.
And what the session is doing -- watching, idle, braked, resumed, stopped -- is
recorded where "yoyo status" and the Slack sink read it, because an idle session
and a dead one are otherwise the same silence.

--budget fails closed. A pass with no way to price itself is refused before
anything starts, and a session that meets a run whose recorded evidence will not
price stops and says which run it was rather than counting it as free and
carrying on inside a bound it can no longer hold.

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --limit <n>       stop after starting this many runs (default: no bound)
  --watch           keep pulling work as it becomes ready, until stopped
  --until-drained   return once nothing more is ready to pull (the default)
  --budget <usd>    stop once this session's runs have cost this much (default: unbounded)
  --json            emit machine-readable JSON`)
}
