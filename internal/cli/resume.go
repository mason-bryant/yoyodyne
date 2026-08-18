package cli

// Releasing a run's usage-limit wait early.
//
// A run paused on an exhausted usage limit waits against a deadline the provider
// named, and every other path honors that deadline strictly: a restart mid-wait
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
	if flags.NArg() != 1 {
		fmt.Fprintln(stderr, "resume requires exactly one Beads work item id")
		printResumeUsage(stderr)
		return 2
	}
	workItemID := flags.Arg(0)

	parts, err := buildComponents(*configPath)
	if err != nil {
		return reportResumeError(stdout, stderr, *jsonOutput, err)
	}
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
	fmt.Fprintf(stdout, "released the %s usage limit wait on %s\n",
		nonEmptyValue(waiting.UsageLimitKind, "provider"), waiting.WorkItemID)
	fmt.Fprintf(stdout, "run: %s\n", waiting.RunID)
	fmt.Fprintf(stdout, "recorded reset: %s\n", waiting.UsageLimitResetsAt.Format(time.RFC3339))
	fmt.Fprintln(stdout, "a process still serving that wait reissues the attempt within seconds; nothing was cancelled")
	fmt.Fprintf(stdout, "if no process is serving it, `yoyo run %s` continues that run now instead of waiting for the reset\n",
		waiting.WorkItemID)
	fmt.Fprintln(stdout, "if the provider still refuses, the run records what it reports and waits again")
	return 0
}

// pausedRunFor finds the run waiting out a usage limit for one work item. It
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
			return runstate.State{}, fmt.Errorf("run %s for %s is in flight but is not waiting on a usage limit; there is nothing to release",
				candidate.RunID, workItemID)
		}
		return candidate, nil
	}
	return runstate.State{}, fmt.Errorf("no run is in flight for %s, so nothing is waiting on a usage limit", workItemID)
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
	fmt.Fprintln(writer, `Usage: yoyo resume [options] <beads-id>

Release a run that is waiting out an exhausted provider usage limit, so it asks
again now rather than at the reset the provider named. Use it when that reset
has stopped being true — you raised the account's capacity, say — because the
deadline is a claim about the provider and you are the one who can change it.

Nothing else bypasses a recorded deadline, and this bypasses nothing else. The
run keeps its claim, its branch, its worktree, and its developer session; a
process already serving the wait is woken rather than stopped. If the provider
still refuses, the run records the new report and waits again, so the worst a
premature release costs is one refused request.

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --json            emit machine-readable JSON`)
}
