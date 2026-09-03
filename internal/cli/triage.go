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

// The third verb here is not one of those and carries nothing out. Both of them
// require a decision recorded against the item's durable triage budget, and the
// caps that bound those budgets refused the recording as well as the carrying
// out -- so an item at the end of its rounds was unrunnable by every recorded
// path, escalation included, and the operator's ruling on one had to be executed
// by admitting fresh work instead. `yoyo triage override` is the recorded path
// that was missing: the operator crosses one of the item's caps, in their own
// name and with their reason, and the guards that refused the decision then
// permit it. It is the operator's hand and nothing else's, which is why it is a
// terminal command rather than a word in any role's vocabulary.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type triageOutput struct {
	Rerun    *orchestrator.RerunResult          `json:"rerun,omitempty"`
	Repair   *orchestrator.RepairContinueResult `json:"repair,omitempty"`
	Override *triageOverrideResult              `json:"override,omitempty"`
	Error    string                             `json:"error,omitempty"`
}

// triageOverrideResult is what an override came to: the decision as it was
// recorded, the item's counters with it on them, and the ceilings the item now
// stands under. The caps are reported beside the record rather than left to be
// worked out from it, because what an operator wants to see is what the guards
// will now permit -- which is the configured caps as every override on the item
// leaves them, not only the one just typed.
type triageOverrideResult struct {
	WorkItemID string                  `json:"work_item_id"`
	Recorded   runstate.TriageOverride `json:"recorded"`
	Caps       runstate.TriageCaps     `json:"caps"`
	Counters   runstate.TriageCounters `json:"counters"`
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
	case "override":
		return overrideTriageCap(ctx, args[1:], stdout, stderr)
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

// overrideTriageCap records the operator's decision to cross one of a work
// item's triage caps.
//
// It carries nothing out and starts nothing, which is deliberate and is what
// keeps the caps meaning what they say. What it changes is what the guards will
// permit next: the development manager can then record the decision their
// escalation was about, and `yoyo triage rerun` or `yoyo triage repair` carries
// that decision out under every condition either of them already asks. An
// operator who wanted a run started still has to say so afterwards, which is one
// more step and the right one -- crossing a cap and spending it are two decisions.
func overrideTriageCap(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("triage override", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	budget := flags.String("budget", runstate.TriageReviewRoundBudget,
		"the budget to cross: "+strings.Join(runstate.TriageOverrideBudgets(), ", "))
	ceiling := flags.Int("cap", -1, "the ceiling to raise the budget to")
	cleared := flags.Bool("clear", false, "lift the budget entirely rather than raising it to a number")
	by := flags.String("by", "", "the operator deciding this")
	reason := flags.String("reason", "", "why this item's cap is being crossed")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 1 {
		fmt.Fprintln(stderr, "triage override requires exactly one work item identifier, the item whose cap is being crossed")
		printTriageUsage(stderr)
		return 2
	}
	// A cleared budget has no number and a raised one has nothing else, so the two
	// are refused together rather than one being quietly preferred: a record
	// carrying both would leave its next reader to guess which the guards obey.
	switch {
	case *cleared && *ceiling >= 0:
		fmt.Fprintln(stderr, "give one of --cap or --clear: a cleared budget states no ceiling, and an override carrying both says two different things about what the guards permit")
		return 2
	case !*cleared && *ceiling < 0:
		fmt.Fprintln(stderr, "triage override requires --cap <n> or --clear: an override that names no new ceiling gives the item no more room and would change nothing")
		return 2
	}

	parts, err := buildComponents(*configPath)
	if err != nil {
		return reportTriageOverride(stdout, stderr, *jsonOutput, triageOverrideResult{}, err)
	}
	workItemID := positional[0]
	caps := orchestrator.TriageCaps(parts.config.Execution, parts.config.Triage)
	// The budget is trimmed here as well as where it is recorded, because it is
	// also what this reads the written override back by: a name the store trimmed
	// and this did not would find nothing and report an override as unrecorded.
	recorded := runstate.TriageOverride{
		Budget:    strings.TrimSpace(*budget),
		Cleared:   *cleared,
		DecidedBy: *by,
		Reason:    *reason,
	}
	if !*cleared {
		recorded.Cap = *ceiling
	}
	counters, err := parts.store.Triage().Override(ctx, workItemID, recorded, time.Now().UTC(), caps)
	if err != nil {
		return reportTriageOverride(stdout, stderr, *jsonOutput, triageOverrideResult{WorkItemID: workItemID}, err)
	}
	// What the item now stands under is read back off the record that was just
	// written rather than assembled from what was typed, so the figures reported
	// and the figures the guards will read are one thing.
	result := triageOverrideResult{
		WorkItemID: workItemID,
		Caps:       caps.Overridden(counters.Overrides),
		Counters:   counters,
	}
	if latest, found := counters.OverrideOf(recorded.Budget); found {
		result.Recorded = latest
	}
	return reportTriageOverride(stdout, stderr, *jsonOutput, result, nil)
}

// reportTriageOverride describes what the override did. A refusal is reported as
// one and says nothing was recorded, because an operator who thinks a cap was
// crossed and finds the next decision refused is back in the deadlock this verb
// exists to end.
func reportTriageOverride(stdout, stderr io.Writer, jsonOutput bool, result triageOverrideResult, err error) int {
	if jsonOutput {
		output := triageOutput{}
		if result.WorkItemID != "" && err == nil {
			output.Override = &result
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
	if err != nil {
		fmt.Fprintf(stderr, "the override was refused and nothing was recorded: %v\n", err)
		if errors.Is(err, runstate.ErrTriageOverrideNotARaise) {
			fmt.Fprintln(stderr, "`yoyo status <beads-id>` says what the item's budgets already stand at, overrides included")
		}
		return 1
	}
	fmt.Fprintf(stdout, "recorded an operator override on %s: %s\n", result.WorkItemID, result.Recorded.Describe())
	printItemTriage(stdout, result.Counters, result.Caps)
	fmt.Fprintln(stdout, "nothing was started, granted, or spent: an override changes what the guards permit, not what has happened")
	fmt.Fprintln(stdout, "the development manager can now record the decision the escalation was about, and `yoyo triage rerun` or `yoyo triage repair` carries it out under every condition it already asks")
	return 0
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
		Start: func(ctx context.Context, workItemID, runID string) (orchestrator.Outcome, error) {
			// The continuation names the run it re-enters, and takes the entry
			// point that can do nothing else: a repair is worth the change one
			// stopped run preserved, and every recorded loss of one was a dispatch
			// that started something fresh in its place. No selection is stamped,
			// because this continues the run that was already reserved for this
			// item and why that run exists was recorded when it was. Why it is
			// going again is on the run and the item already.
			return pipelineFrom(parts).Continue(ctx, workItemID, runID)
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
// anything was claimed, one intake held, one whose fresh run met a pause where it
// would have started, and one whose fresh run then failed are four different
// things for an operator to do something about, so none of them is reported as
// any of the others.
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
		// A pause the fresh run met where it would have started is the third such
		// state: nothing was reserved, the claim was given back, and what lifts the
		// pause is the same thing that lifts it for any other work. So the accounting
		// is reported here and the pause itself exactly as `yoyo run` reports one,
		// rather than in words this command would have to keep in step with those.
		if result.PausedBeforeStarting != nil {
			fmt.Fprint(stdout, result.Render())
			code := reportRunResult(stdout, stderr, false, *result.PausedBeforeStarting, err)
			if result.RecordProblem != "" {
				return 1
			}
			return code
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
	fmt.Fprintln(writer, `Usage: yoyo triage rerun    [options] <run-id>
       yoyo triage repair   [options] <run-id>
       yoyo triage override [options] <beads-id>

"rerun" and "repair" carry out a decision the development manager recorded about
a docketed stoppage. "override" is yours rather than theirs: it crosses one of a
work item's triage caps so that a decision they could not record becomes one they
can.

The first two are opposites. "rerun" starts a fresh run of the item, which
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

"override" crosses one of a work item's caps, and is the only thing that does.
The caps stop machines looping, so they refuse a development manager past them --
but they refused the recording of your answer to the escalation as well, which
left an item at the end of its rounds unrunnable by every recorded path. This is
that answer as a record: it names the budget, what you raised it to or that you
cleared it, who you are, and why, and it is kept on the item's own triage record
where every guard and every reading of the item finds it.

It clears or raises and never lowers -- an override that would give the item no
more room than it already has is refused. Lowering a cap is a judgement about the
project's pace rather than about one item, and triage.review_rounds_cap is where
that is made.

It carries nothing out. Recording it changes what the guards permit and nothing
else: the development manager then records the decision the escalation was about,
which spends the item's budget as it always did, and "rerun" or "repair" carries
that decision out under every condition either already asks. Crossing a cap and
spending it are two decisions and stay two.

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --reason <text>   rerun/repair: the development manager's recorded reasoning
                    (required); override: why the cap is being crossed (required)
  --budget <name>   override: which cap to cross -- "review round" (the default),
                    "repair grant", "re-run", or "merge re-arm"
  --cap <n>         override: the ceiling to raise that budget to
  --clear           override: lift that budget entirely instead
  --by <name>       override: the operator deciding it (required)
  --json            emit machine-readable JSON`)
}
