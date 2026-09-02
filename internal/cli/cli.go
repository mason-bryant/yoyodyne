package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"go.yaml.in/yaml/v3"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

func Run(args []string, stdout, stderr io.Writer, version string) int {
	return RunContext(context.Background(), args, stdout, stderr, version)
}

func RunContext(ctx context.Context, args []string, stdout, stderr io.Writer, version string) int {
	if len(args) == 0 {
		printUsage(stdout)
		return 0
	}

	switch args[0] {
	case "help", "-h", "--help":
		printUsage(stdout)
		return 0
	case "version":
		return runVersion(args[1:], stdout, stderr, version)
	case "init":
		return runInit(ctx, args[1:], stdout, stderr)
	case "setup":
		// Setup asks the operator yes or no before each step it takes, so it is
		// bound to the process's own input the way a conversation is.
		return runSetup(ctx, args[1:], os.Stdin, stdout, stderr, version)
	case "config":
		return runConfig(ctx, args[1:], stdout, stderr)
	case "chat":
		// A conversation is the one command that reads from the operator, so
		// this is where the process's own input is bound to it.
		return runChat(ctx, args[1:], os.Stdin, stdout, stderr)
	case "agent":
		// `agent chat` is a conversation like any other, so it is bound to the
		// same input for the same reason.
		return runAgentCommand(ctx, args[1:], os.Stdin, stdout, stderr)
	case "artifact":
		return runArtifact(args[1:], stdout, stderr)
	case "amendment":
		return runAmendment(args[1:], stdout, stderr)
	case "evaluation":
		return runEvaluation(args[1:], stdout, stderr)
	case "goals":
		// `goals guard` is handed the tool call it decides on stdin, so this is
		// where the process's own input is bound to it.
		return runGoals(ctx, args[1:], os.Stdin, stdout, stderr)
	case "stale":
		return reportStaleness(ctx, args[1:], stdout, stderr)
	case "conformance":
		return runConformance(ctx, args[1:], stdout, stderr)
	case "invariant":
		return runInvariant(args[1:], stdout, stderr)
	case "directive":
		return runDirective(args[1:], stdout, stderr)
	case "exchange":
		return runExchange(ctx, args[1:], stdout, stderr)
	case "reports":
		return readReports(args[1:], stdout, stderr)
	case "run":
		return runWorkItem(ctx, args[1:], stdout, stderr)
	case "work":
		return scheduleWork(ctx, args[1:], stdout, stderr)
	case "triage":
		return runTriage(ctx, args[1:], stdout, stderr)
	case "status":
		return reportRunStatus(args[1:], stdout, stderr)
	case "pause":
		return pauseHarness(args[1:], stdout, stderr)
	case "resume":
		return resumeWorkItem(args[1:], stdout, stderr)
	case "release":
		return releaseIntake(args[1:], stdout, stderr)
	case "review":
		return reviewBranch(ctx, args[1:], stdout, stderr)
	case "cost":
		return reportCosts(ctx, args[1:], stdout, stderr)
	case "reconcile":
		return reconcileRuns(ctx, args[1:], stdout, stderr)
	case "slack":
		return runSlack(ctx, args[1:], stdout, stderr, version)
	case "doctor":
		return runDoctor(ctx, args[1:], stdout, stderr, version)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n", args[0])
		printUsage(stderr)
		return 2
	}
}

func runVersion(args []string, stdout, stderr io.Writer, version string) int {
	flags := flag.NewFlagSet("version", flag.ContinueOnError)
	flags.SetOutput(stderr)
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "version does not accept positional arguments")
		return 2
	}

	if *jsonOutput {
		return writeJSON(stdout, stderr, map[string]string{"version": version})
	}
	fmt.Fprintln(stdout, version)
	return 0
}

func runConfig(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printConfigUsage(stdout)
		return 0
	}
	switch args[0] {
	case "validate":
		return runConfigValidate(ctx, args[1:], stdout, stderr)
	case "show":
		return runConfigShow(args[1:], stdout, stderr)
	case "drift":
		return runConfigDrift(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown config command %q\n\n", args[0])
		printConfigUsage(stderr)
		return 2
	}
}

func runConfigValidate(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("config validate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "config validate does not accept positional arguments")
		return 2
	}

	resolved, err := loadConfiguration(*path)
	if err != nil {
		if *jsonOutput {
			code := writeJSON(stdout, stderr, map[string]any{
				"status": "invalid",
				"config": reportedPath(*path, resolved),
				"error":  err.Error(),
			})
			if code != 0 {
				return code
			}
		} else {
			fmt.Fprintln(stderr, err)
		}
		return 1
	}

	// A configuration can be entirely valid and reach no other machine, so the
	// one command an operator runs to be told whether it is right says so. It is
	// not a validity failure and does not become one: the exit code is what it
	// would have been, and the warning is on the stream a warning belongs on.
	ignored := configurationIgnored(ctx, execution.OSProcessRunner{}, configuredRepository(resolved), resolved.Path)
	// What the project's template has improved since this configuration was
	// generated, said without being asked for it. It is silent unless there is
	// something, it is on the stream an aside belongs on, and it never moves the
	// exit code: an improvement available is a fact about a valid configuration
	// rather than something wrong with one.
	drift, _ := config.ReadDrift(resolved)

	if *jsonOutput {
		return writeJSON(stdout, stderr, map[string]any{
			"status":     "valid",
			"config":     resolved.Path,
			"sources":    resolved.Sources,
			"product_id": resolved.Config.Product.ID,
			"agents":     len(resolved.Config.Agents),
			"revision":   resolved.Config.Revision(),
			"ignored":    ignored,
			"drift":      drift,
		})
	}
	fmt.Fprintf(stdout, "configuration valid: %s (revision %s)\n", resolved.Path, resolved.Config.Revision())
	if ignored.Ignored {
		fmt.Fprintln(stderr, describeIgnoredConfiguration(ignored))
	}
	if notice := drift.Notice(); notice != "" {
		fmt.Fprintln(stderr, notice)
	}
	return 0
}

// runConfigDrift is the report the notices point at: the whole three-way
// comparison, including the two classes the unprompted surfaces deliberately
// stay quiet about.
//
// A project with no baseline is told it has none and exits 0. It is a project
// that predates the record or deleted it, which decides nothing about how it
// runs, and refusing would break it over a file nothing loads.
func runConfigDrift(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("config drift", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	all := flags.Bool("all", false, "print every compared value, including the ones neither side moved")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "config drift does not accept positional arguments")
		return 2
	}

	resolved, err := loadConfiguration(*path)
	if err != nil {
		if *jsonOutput {
			if code := writeJSON(stdout, stderr, map[string]any{
				"status": "invalid",
				"config": reportedPath(*path, resolved),
				"error":  err.Error(),
			}); code != 0 {
				return code
			}
		} else {
			fmt.Fprintln(stderr, err)
		}
		return 1
	}

	drift, unknown := config.ReadDrift(resolved)
	if *jsonOutput {
		return writeJSON(stdout, stderr, map[string]any{
			"config":  resolved.Path,
			"drift":   drift,
			"unknown": unknown,
		})
	}
	if !drift.Known {
		renderUnknownBaseline(stdout, unknown)
		return 0
	}
	renderDrift(stdout, drift, *all)
	return 0
}

// renderUnknownBaseline says why there is no comparison and what to do about it,
// which is a different answer for each of the two reasons there can be none.
//
// Neither answer is `yoyo init --force` on its own. That command does write a
// fresh baseline, and it regenerates the configuration and every persona from
// the template in the same pass -- so an operator who reached this report because
// their baseline is missing or corrupt, and followed it, would discard exactly
// the edits the report exists to surface. What it costs is said wherever it is
// named, rather than left to be discovered by running it.
func renderUnknownBaseline(stdout io.Writer, unknown config.Unknown) {
	if unknown.Absent {
		fmt.Fprintf(stdout, "no baseline: %s\n", unknown.Reason)
		fmt.Fprintf(stdout, "nothing writes one on its own, and this costs the project nothing else: what %s says is what runs either way.\n", config.FileName)
		fmt.Fprintf(stdout, "`yoyo init --force` would write one, but it regenerates %s and every persona from %s in the same pass and discards\n",
			config.FileName, config.BuiltinV1)
		fmt.Fprintln(stdout, "your edits to them, so it is the right answer only for a project you mean to regenerate whole.")
		return
	}
	fmt.Fprintf(stdout, "unusable baseline: %s\n", unknown.Reason)
	fmt.Fprintf(stdout, "the file is there and is being refused rather than missing, so the copy in version control is what puts it back.\n")
	fmt.Fprintf(stdout, "`yoyo init --force` is the only thing that writes a fresh one, and it regenerates %s and every persona from the\n", config.FileName)
	fmt.Fprintln(stdout, "template in the same pass, so reaching for it here would discard the edits this report exists to surface.")
}

// renderDrift prints the comparison a class at a time, most actionable first.
// Every value carries what it was and what it is, because the report's job is to
// let the operator decide rather than to tell them a verdict.
func renderDrift(stdout io.Writer, drift config.Drift, all bool) {
	fmt.Fprintf(stdout, "template: %s\n", drift.Bundle)
	if drift.Current() {
		fmt.Fprintf(stdout, "current: nothing this project inherited has moved since it was generated (%s)\n", drift.BaselineRevision)
	} else {
		fmt.Fprintf(stdout, "moved: %s when generated, %s now\n", drift.BaselineRevision, drift.BundleRevision)
	}
	printed := 0
	for _, group := range []struct {
		class  config.Class
		header string
	}{
		{config.ClassAvailable, "available -- improved by the template, never edited here"},
		{config.ClassConflicting, "conflicting -- both moved; nothing is adopted until you settle it"},
		{config.ClassYours, "yours -- changed here and not by the template; never touched"},
		{config.ClassUnchanged, "unchanged -- neither side moved it"},
	} {
		if !all && (group.class == config.ClassUnchanged || group.class == config.ClassYours) {
			continue
		}
		matched := drift.OfClass(group.class)
		if len(matched) == 0 {
			continue
		}
		fmt.Fprintf(stdout, "\n%s\n", group.header)
		for _, value := range matched {
			fmt.Fprintf(stdout, "  %s\n", value.Key)
			fmt.Fprintf(stdout, "    yours:    %s\n", value.Yours)
			fmt.Fprintf(stdout, "    template: %s\n", value.Bundle)
		}
		printed += len(matched)
	}
	if printed == 0 {
		fmt.Fprintln(stdout, "\nnothing to report")
	}
}

func runConfigShow(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("config show", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	effective := flags.Bool("effective", false, "print the effective configuration after inheritance")
	origins := flags.Bool("origins", false, "print where every effective value came from")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "config show does not accept positional arguments")
		return 2
	}
	// Showing the effective configuration is the default, because an operator
	// asking what the configuration is means the values actually in force.
	showEffective := *effective || !*origins

	resolved, err := loadConfiguration(*path)
	if err != nil {
		if *jsonOutput {
			code := writeJSON(stdout, stderr, map[string]any{
				"status": "invalid",
				"config": reportedPath(*path, resolved),
				"error":  err.Error(),
			})
			if code != 0 {
				return code
			}
		} else {
			fmt.Fprintln(stderr, err)
		}
		return 1
	}

	if *jsonOutput {
		payload := map[string]any{
			"config":  resolved.Path,
			"sources": resolved.Sources,
			// The revision names these values as one thing, so a run record and the
			// configuration it was made under can be held up against each other
			// without diffing two files.
			"revision": resolved.Config.Revision(),
		}
		if showEffective {
			payload["effective"] = resolved.Config
		}
		if *origins {
			payload["origins"] = resolved.Origins
		}
		return writeJSON(stdout, stderr, payload)
	}

	fmt.Fprintf(stdout, "# configuration: %s\n", resolved.Path)
	for _, source := range resolved.Sources {
		fmt.Fprintf(stdout, "# layer: %s\n", source)
	}
	fmt.Fprintf(stdout, "# revision: %s\n", resolved.Config.Revision())
	if showEffective {
		encoded, err := yaml.Marshal(resolved.Config)
		if err != nil {
			fmt.Fprintf(stderr, "render effective configuration: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "\n%s", encoded)
	}
	if *origins {
		fmt.Fprintln(stdout, "\n# value origins")
		for _, key := range resolved.OriginKeys() {
			fmt.Fprintf(stdout, "%s: %s\n", key, resolved.Origins[key])
		}
	}
	return 0
}

// parseArguments reads a command's flags and its positional arguments in
// whatever order they were typed, and returns the positional ones. Go's flag
// package stops parsing at the first word that is not a flag, so
// `amendment approve <id> --reason ...` would otherwise arrive as three
// positional arguments and be refused for naming three proposals — and an id
// before the flags that describe what is being done to it is how anybody types
// it, and what the usage text and the documentation say to type.
//
// Every command that takes an id parses through here rather than calling Parse
// itself, because an ordering that works for one command and not the next is
// worse than one that never worked: the operator learns the rule from the
// command they happened to type first.
func parseArguments(set *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	remaining := args
	for {
		if err := set.Parse(remaining); err != nil {
			return nil, err
		}
		if set.NArg() == 0 {
			return positional, nil
		}
		positional = append(positional, set.Arg(0))
		remaining = set.Args()[1:]
	}
}

// argumentAt is the positional argument a command was given at one place, for
// the commands whose argument is optional. It is empty when nothing was typed
// there rather than a bounds failure.
func argumentAt(positional []string, index int) string {
	if index >= len(positional) {
		return ""
	}
	return positional[index]
}

// loadConfiguration resolves an explicit path when one is given and otherwise
// discovers the nearest project configuration, so Yoyodyne runs from a project
// root or any directory beneath it.
func loadConfiguration(explicitPath string) (config.Resolved, error) {
	path := explicitPath
	if path == "" {
		workingDirectory, err := os.Getwd()
		if err != nil {
			return config.Resolved{}, fmt.Errorf("resolve working directory: %w", err)
		}
		discovered, err := config.Discover(workingDirectory)
		if err != nil {
			return config.Resolved{}, err
		}
		path = discovered
	}
	return config.LoadResolved(path)
}

// reportedPath names the configuration in a failure report. A discovery failure
// has no path to report, so the request is described instead of inventing one.
func reportedPath(explicitPath string, resolved config.Resolved) string {
	if resolved.Path != "" {
		return resolved.Path
	}
	if explicitPath != "" {
		return explicitPath
	}
	return "(discovered)"
}

func writeJSON(stdout, stderr io.Writer, value any) int {
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		fmt.Fprintf(stderr, "encode JSON output: %v\n", err)
		return 1
	}
	return 0
}

func printUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo <command> [options]

Commands:
  setup             walk this project to an installation that can run work
  init              write a project its own configuration, personas, and checks
  chat              talk with the product manager and steer the work from there
  agent             read the configured agents and their state, and address one
  config validate   validate a Yoyodyne configuration
  config show       print the effective configuration and value origins
  config drift      compare this project against the template it was generated from
  artifact          read the canonical artifacts, and record your approval of one
  amendment         read changes proposed to artifacts, and decide them
  evaluation        read what the product manager made of the ideas you brought it
  goals             read the goals and what work serves, and witness and guard it
  stale             read what a change upstream left unanswered downstream
  conformance       check the product against what it records about itself
  invariant         record, amend, retire, and read architectural invariants
  directive         record, resolve, and read durable user directives
  exchange          read what the roles have asked each other, and what it cost
  reports           read what agents reported without it stopping their work
  run               run one Beads work item in an isolated worktree
  work              schedule the ready work the harness chooses for itself
  triage            carry out what the development manager decided about a stoppage
  status            read what became of recent runs, and why one of them failed
  pause             pause everything the harness would spend on a provider
  resume            lift that pause, or release one run's wait on the provider
  release           lift a hold on intake, so the harness chooses work again
  review            review what a branch accumulated over a base, as one change
  cost              price work items from the runs made for them, and record it
  reconcile         settle interrupted runs, then converge local state on the forge
  slack             report what the harness is doing into a Slack channel
  doctor            check this installation, and say what would fix what is wrong
  version           print version information
  help              show this help`)
}

func printConfigUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo config <validate|show|drift> [options]

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --json            emit machine-readable JSON

config show options:
  --effective       print the effective configuration after inheritance (default)
  --origins         print where every effective value came from

config drift compares three sides -- what this project's template supplied when
the configuration was generated, what it says now, and what the project says
today -- so a value you changed and a value the template improved are told apart
rather than both reported as differences. It changes nothing, and it reads the
config.lock beside the configuration, which nothing consults when work runs.
A project with no baseline says so and exits 0.

config drift options:
  --all             print every compared value, including the ones neither side moved`)
}
