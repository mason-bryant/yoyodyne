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
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/buildinfo"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/goal"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/redeploy"
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
	//
	// binary is the file this session is executing, resolved for a watch and for
	// nothing else, and left nil where the platform cannot replace a running
	// process.
	var binary *redeploy.Binary
	if *watch {
		sessions, err := openWatchSession(*configPath)
		if err != nil {
			fmt.Fprintf(stderr, "work failed: %v\n", err)
			return 1
		}
		scheduler.Sessions = sessions
		// Which file this process was started from, read before anything runs. A
		// watching session outlives the deploys that land behind it, and this is
		// what lets it take one up between runs rather than dispatching from a
		// build the harness moved past until somebody restarts it.
		//
		// A platform that cannot replace a running process says so here, and the
		// session watches without it. That is a session as capable as every session
		// before this existed, so refusing to watch at all over it would take away
		// more than this adds — but it is said out loud, because a session that
		// will not take up a deploy is exactly what somebody would otherwise assume
		// had happened.
		binary, err = redeploy.Running()
		switch {
		case errors.Is(err, redeploy.ErrUnsupported):
			fmt.Fprintf(stderr, "this session will not take up a build deployed over it, and has to be restarted by hand for one: %v\n", err)
		case err != nil:
			fmt.Fprintf(stderr, "work failed: %v\n", err)
			return 1
		default:
			scheduler.Deployment = binary
		}
	}
	schedule, err := scheduler.Schedule(ctx)
	code := reportSchedule(stdout, stderr, *jsonOutput, schedule, err)
	if !schedule.Redeploying() || binary == nil {
		return code
	}
	// A bounded session arrives here with something left of its bound: what it has
	// spent is checked against the bound at the top of every pull, ahead of the
	// redeploy, so a bound that is gone stops the session as spent rather than
	// restarting it. The guard is here because the cost of that ordering ever
	// changing is a build invoked with a negative budget, which refuses to start
	// at all and takes the line down with it.
	if *budget > 0 && *budget-schedule.SpentUSD <= 0 {
		fmt.Fprintln(stderr, "work stopped to restart into the build deployed over it, and did not: nothing was left of the budget the session was given")
		return code
	}
	// Every run this session started has already ended, and the queue has been
	// left alone since the deploy landed. What is left is to become the build that
	// was deployed: the account of the session that just ended has been printed,
	// and nothing returns from this when it works — the process goes on as the new
	// build, watching the same queue under what is left of the same bounds.
	if err := binary.Take(continued(binary.Args(), *budget, schedule.SpentUSD, *limit, len(schedule.Started))); err != nil {
		fmt.Fprintf(stderr, "work stopped to restart into the build deployed over it, and could not: %v\n", err)
		return 1
	}
	return code
}

// continued is how a session's own command line crosses a restart: the same
// invocation, with the bounds the operator gave it reduced to what is left of
// them.
//
// A bound carried whole would be a bound that starts again at every deploy, and
// on a machine that deploys several times a day that is not a bound at all — a
// session forty-five dollars into a fifty dollar budget would come back with
// fifty. Nothing about a deploy is the operator raising a cap they set, so what
// crosses the restart is the remainder: the budget less what the session spent,
// and the run count less what it started.
//
// Both are strictly positive here, because a session that has reached either
// bound stops on that bound instead of redeploying.
func continued(args []string, budget, spent float64, limit, started int) []string {
	invocation := append([]string(nil), args...)
	if budget > 0 {
		replaceFlagValue(invocation, "budget", remainingBudget(budget-spent))
	}
	if limit > 0 {
		replaceFlagValue(invocation, "limit", strconv.Itoa(limit-started))
	}
	return invocation
}

// remainingBudget writes what is left of a budget as the argument the build this
// session restarts into is given.
//
// It is rounded down to the cent, which is how money is said everywhere else
// here and is the safe direction: rounded up, the restart would hand back a
// fraction of a cent the session had already spent. Under a cent it is written
// exactly instead, because a remainder rounded to "0.00" is how an unbounded
// session is asked for, and a bound that quietly became no bound at all is the
// one mistake this must not make.
func remainingBudget(remaining float64) string {
	if cents := math.Floor(remaining*100) / 100; cents > 0 {
		return strconv.FormatFloat(cents, 'f', 2, 64)
	}
	return strconv.FormatFloat(remaining, 'f', -1, 64)
}

// replaceFlagValue rewrites one flag's value wherever a command line gives it,
// in both spellings the flag package accepts and with either one dash or two. A
// flag the command line does not carry is left absent rather than added: what is
// being rewritten is a value the operator wrote, and a bound nobody asked for is
// not this command's to invent.
func replaceFlagValue(args []string, name, value string) {
	for index := 0; index < len(args); index++ {
		if args[index] == "-"+name || args[index] == "--"+name {
			if index+1 < len(args) {
				args[index+1] = value
				index++
			}
			continue
		}
		for _, prefix := range []string{"-" + name + "=", "--" + name + "="} {
			if strings.HasPrefix(args[index], prefix) {
				args[index] = prefix + value
			}
		}
	}
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
	return watchSessionLog{
		store:     parts.watch,
		productID: parts.config.Product.ID,
		sessionID: sessionID,
		// What this session is actually running, read once as it opens rather than
		// on every transition: a process does not change binary while it lives, and
		// that is exactly the problem — it goes on running what it was started with
		// while the harness moves on underneath it. A binary that recorded no
		// revision leaves this empty, which reads as a comparison nobody can make.
		build: buildinfo.Commit(),
	}, nil
}

// watchSessionLog is the scheduler's account of itself, written into the
// product's watch log. It carries the identity the scheduler has no business
// knowing about — which product, which session — and nothing else.
type watchSessionLog struct {
	store     *runstate.WatchStore
	productID domain.ProductID
	sessionID string
	// build is the revision this process was built from, carried on every
	// transition so a reader who arrives mid-session finds it on the entry the
	// session happened to write last.
	build string
}

func (w watchSessionLog) Record(state runstate.WatchState, at time.Time, reason string) error {
	return w.store.Record(runstate.WatchTransition{
		SchemaVersion: runstate.WatchSchemaVersion,
		ProductID:     w.productID,
		SessionID:     w.sessionID,
		State:         state,
		At:            at,
		Reason:        reason,
		Build:         w.build,
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

A watching session also takes up a build deployed over it. When the yoyo it is
running is written over -- installed, rebuilt -- it stops choosing, waits out
every run it already started, and restarts into what was deployed. A run in
flight is never interrupted for it, so a fix you build reaches the session at the
gap after the run that is going now rather than when somebody remembers to
restart it. The queue is re-read from scratch on the way back in, exactly as it
is at every poll, and the bounds you gave the session cross the restart reduced
to what is left of them -- --budget less what it has spent, --limit less what it
has started -- so a deploy never hands a bounded session its cap back. A session
that has reached either bound stops on it rather than restarting. A platform that
cannot replace a running process says so when the session opens, and that session
watches without this and is restarted by hand for a deploy.

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
