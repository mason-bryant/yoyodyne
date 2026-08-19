package cli

// Reading what agents reported, without opening a conversation.
//
// A report is how something an agent noticed while its own work succeeded
// reaches the operator, and until this verb existed the only way to read the
// pile was `/reports` inside `yoyo chat`. That fails the operator the channel
// exists for: a run that finishes overnight tells them a report was filed, and
// reading it costs them an interactive conversation with a provider behind it.
// It also puts the pile out of reach of anything scripted, which is the surface
// a channel meant to be triaged eventually needs.
//
// So this reads the same durable store the conversation reads and nothing else.
// It is read-only in the strongest sense the design allows: a report is written
// once and never revised, and nothing here retires one, marks one read, or
// decides anything about the pile.

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/mason-bryant/yoyodyne/internal/report"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

type reportsOutput struct {
	Reports []report.Report `json:"reports"`
	Error   string          `json:"error,omitempty"`
}

func readReports(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("reports", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "reports does not accept positional arguments: it reads the whole pile")
		printReportsUsage(stderr)
		return 2
	}

	store, err := reportStore(*configPath)
	if err != nil {
		return reportReportsError(stdout, stderr, *jsonOutput, err)
	}
	collected, err := store.List()
	if err != nil {
		return reportReportsError(stdout, stderr, *jsonOutput, err)
	}
	if *jsonOutput {
		// The list is always encoded, so a caller reading JSON tells "nobody has
		// reported anything" from a failure by the field being empty rather than
		// absent.
		if collected == nil {
			collected = []report.Report{}
		}
		return writeJSON(stdout, stderr, reportsOutput{Reports: collected})
	}
	if len(collected) == 0 {
		// "Nothing has been reported" is an answer, and where the pile would be is
		// part of it: an operator looking for a report they were told about needs
		// to know they are reading the right product's store.
		fmt.Fprintf(stdout, "nothing has been reported for this product; the pile would be %s\n", store.Path())
		return 0
	}
	// The whole pile, oldest first, unlike `/reports` in a conversation: that is a
	// listing beside an ongoing conversation and lists the twenty most recent,
	// and this is a command whose output can be paged, filtered, or piped.
	fmt.Fprintf(stdout, "reports (%d collected):\n", len(collected))
	for _, reported := range collected {
		fmt.Fprint(stdout, reported.Render())
	}
	return 0
}

// reportStore resolves the same product-scoped pile every run appends to and
// the conversation reads, from the configuration and the state root alone. It
// deliberately does not go through buildComponents: reading what agents
// reported needs no repository, no worktree manager, and no process runner, and
// a verb that exists to be reachable from anywhere should not refuse to read a
// pile because of where the caller's checkout happens to sit — inside a
// harness-managed worktree, for one.
//
// What that shortcut must not do is address a different pile. The state root is
// runstate.SystemDefaultRoot for every command that has one, and configuration
// has no say in it, so the product id below is the whole of what decides which
// store this is — and a test pins this path to the one buildComponents builds,
// because a reports verb reading the wrong root would answer "nothing has been
// reported" against a pile that is not empty.
func reportStore(configPath string) (*runstate.ReportStore, error) {
	resolved, err := loadConfiguration(configPath)
	if err != nil {
		return nil, err
	}
	stateRoot, err := runstate.SystemDefaultRoot(os.Getenv, os.UserHomeDir)
	if err != nil {
		return nil, err
	}
	return runstate.NewReportStore(stateRoot, resolved.Config.Product.ID)
}

func reportReportsError(stdout, stderr io.Writer, jsonOutput bool, err error) int {
	if jsonOutput {
		if code := writeJSON(stdout, stderr, reportsOutput{Error: err.Error()}); code != 0 {
			return code
		}
		return 1
	}
	fmt.Fprintln(stderr, err)
	return 1
}

func printReportsUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo reports [options]

What agents reported without it stopping their work: a risk one worked around,
an assumption that may not hold, a defect or a stale document outside the work
it was given, something in its environment that stopped it verifying what it
wanted to. Every role files them the same way and they land in one pile per
product, which is what this reads.

Each report names the role and the configured agent that made it, the run or
conversation it came from, the work item where there was one, a severity —
critical, warning, or note — and the text. The whole pile is printed, oldest
first; `+"`/reports`"+` in `+"`yoyo chat`"+` shows the same reports beside the
conversation, listing the twenty most recent.

A report decides nothing and nothing waits on it, so this is read-only: it
retires nothing, marks nothing read, and changes no work.

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --json            emit machine-readable JSON`)
}
