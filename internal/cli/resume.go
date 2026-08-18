package cli

// Releasing a run's wait on the provider early.
//
// A run the provider refused waits against a recorded deadline, and every other
// path honors that deadline strictly: a restart mid-wait
// serves the rest of it rather than asking again, because retrying into a window
// that is still closed is how a crash turns into a refusal loop. This is the one
// verb that overrides it, and it overrides nothing else.
//
// It exists because the deadline is a claim about the provider and the operator
// can change what it is a claim about. When capacity is raised mid-wait, a run
// sleeping out a limit its owner has already lifted is autonomy working against
// them. So this records that the claim went stale and leaves everything else
// alone: the run keeps its claim, its branch, its worktree, and its session, the
// process serving the wait is woken rather than stopped, and if the provider
// still refuses, the run simply re-parks on whatever it reports next.

import (
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type resumeOutput struct {
	Release *runstate.Release `json:"release,omitempty"`
	Error   string            `json:"error,omitempty"`
}

func resumeWorkItem(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("resume", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() > 1 {
		fmt.Fprintln(stderr, "resume takes one Beads work item id, or none at all to lift a pause on everything")
		printResumeUsage(stderr)
		return 2
	}

	parts, err := buildComponents(*configPath)
	if err != nil {
		return reportResumeError(stdout, stderr, *jsonOutput, err)
	}
	// Naming nothing resumes everything, which is the other half of `yoyo pause`.
	// It is the same verb because it is the same act — stop waiting and carry on —
	// and what the argument says is whose decision is being withdrawn: the
	// provider's refusal of one run, or the operator's hold over all of them.
	if flags.NArg() == 0 {
		return resumeHarness(stdout, stderr, *jsonOutput, parts.holds)
	}
	workItemID := flags.Arg(0)
	// The waiting run is found by reading, never by adopting: a run another
	// process is serving must keep serving it, and taking its lease to release its
	// wait would be the harness stopping the very run this exists to keep alive.
	waiting, err := pausedRunFor(parts.store, workItemID)
	if err != nil {
		return reportResumeError(stdout, stderr, *jsonOutput, err)
	}
	released := runstate.Release{
		SchemaVersion: runstate.ReleaseSchemaVersion,
		ProductID:     parts.config.Product.ID,
		RunID:         waiting.RunID,
		WorkItemID:    waiting.WorkItemID,
		ReleasedAt:    time.Now().UTC(),
	}
	if err := parts.store.RecordRelease(released); err != nil {
		return reportResumeError(stdout, stderr, *jsonOutput, err)
	}
	if *jsonOutput {
		return writeJSON(stdout, stderr, resumeOutput{Release: &released})
	}
	fmt.Fprintf(stdout, "released the wait on %s for %s\n",
		waiting.WorkItemID, runstate.DescribePause(waiting.PauseCause, waiting.UsageLimitKind))
	fmt.Fprintf(stdout, "run: %s\n", waiting.RunID)
	fmt.Fprintf(stdout, "recorded deadline: %s\n", waiting.UsageLimitResetsAt.Format(time.RFC3339))
	fmt.Fprintln(stdout, "a process still serving that wait reissues the attempt within seconds; nothing was cancelled")
	fmt.Fprintf(stdout, "if no process is serving it, `yoyo run %s` continues that run now instead of waiting out the rest\n",
		waiting.WorkItemID)
	fmt.Fprintln(stdout, "if the provider still refuses, the run records what it reports and waits again")
	return 0
}

// pausedRunFor finds the run waiting out a provider refusal for one work item. It
// refuses anything else by name rather than releasing it anyway: a release
// recorded against a run that is not waiting would be acted on by whatever pause
// that run takes next, which is a pause nobody has said anything about.
func pausedRunFor(store *runstate.Store, workItemID string) (runstate.State, error) {
	incomplete, err := store.Incomplete()
	if err != nil {
		return runstate.State{}, fmt.Errorf("find the run in flight for %s: %w", workItemID, err)
	}
	for _, candidate := range incomplete {
		if candidate.WorkItemID != workItemID {
			continue
		}
		if candidate.UsageLimitResetsAt == nil {
			// A run parked on the operator's own hold is waiting on them rather than
			// on the provider, and what lifts it is this verb with nothing named. An
			// operator who typed the item is one word from what they meant, so they
			// are told which word rather than only that this is not it.
			if candidate.OperatorHeldSince != nil {
				return runstate.State{}, fmt.Errorf("run %s for %s is parked because all harness activity is paused, not because the provider refused it; `yoyo resume` with no work item lifts that pause",
					candidate.RunID, workItemID)
			}
			return runstate.State{}, fmt.Errorf("run %s for %s is in flight but is not waiting on the provider; there is nothing to release",
				candidate.RunID, workItemID)
		}
		return candidate, nil
	}
	return runstate.State{}, fmt.Errorf("no run is in flight for %s, so nothing is waiting on the provider", workItemID)
}

func reportResumeError(stdout, stderr io.Writer, jsonOutput bool, err error) int {
	if jsonOutput {
		if code := writeJSON(stdout, stderr, resumeOutput{Error: err.Error()}); code != 0 {
			return code
		}
		return 1
	}
	fmt.Fprintln(stderr, err)
	return 1
}

func printResumeUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo resume [options] [beads-id]

With no work item, lift the pause `+"`yoyo pause`"+` placed over all harness
activity. Everything parked on it carries on: a process still holding a parked
run acts within seconds, and a run whose process has exited is continued by
`+"`yoyo run <beads-id>`"+` from exactly where it stopped.

With a work item, release a run that is waiting out a provider refusal — an exhausted usage limit
or an overloaded server — so it asks again now rather than at its recorded
deadline. Use it when that deadline has stopped being true — you raised the
account's capacity, say, or the provider's status page went green — because it is
a claim about the provider and you are the one who can find out otherwise.

Nothing else bypasses a recorded deadline, and this bypasses nothing else. The
run keeps its claim, its branch, its worktree, and its developer session; a
process already serving the wait is woken rather than stopped. If the provider
still refuses, the run records the new report and waits again, so the worst a
premature release costs is one refused request.

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --json            emit machine-readable JSON`)
}
