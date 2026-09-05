package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
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
	case "sweeps":
		return readSweeps(args[1:], stdout, stderr)
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
	case "baseline":
		return runConfigBaseline(args[1:], stdout, stderr)
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
	drift, unknown := config.ReadDrift(resolved)

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
			// Carried beside the comparison rather than folded into it, the way
			// `config drift` carries it: an unknown answer has two reasons, and a
			// reader given only `known: false` cannot tell a project that never
			// recorded a baseline from one whose baseline is there and refused.
			"unknown": unknown,
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
// Neither answer is `yoyo init --force`. That command does write a fresh
// baseline, and it regenerates the configuration and every persona from the
// template in the same pass -- so an operator who reached this report because
// their baseline is missing or corrupt, and followed it, would discard exactly
// the edits the report exists to surface. `yoyo config baseline` writes the one
// file and nothing else, which is why it is what both answers name.
func renderUnknownBaseline(stdout io.Writer, unknown config.Unknown) {
	if unknown.Absent {
		fmt.Fprintf(stdout, "no baseline: %s\n", unknown.Reason)
		fmt.Fprintf(stdout, "record one with `yoyo config baseline`, which writes %s and touches nothing else.\n", config.LockFileName)
		fmt.Fprintln(stdout, "it records what the template supplies now, so this project reads as level with it from here: an improvement that")
		fmt.Fprintln(stdout, "landed before today is counted as yours and never reported, because nothing recorded what the template said back then.")
		return
	}
	fmt.Fprintf(stdout, "unusable baseline: %s\n", unknown.Reason)
	fmt.Fprintln(stdout, "the file is there and is being refused rather than missing, so the copy in version control is what puts it back")
	fmt.Fprintf(stdout, "with what it knew. Failing that, `yoyo config baseline --force` writes a fresh one from the template as it is now,\n")
	fmt.Fprintln(stdout, "which starts the comparison over rather than recovering it.")
}

// runConfigBaseline records the baseline a project compares against, for a
// project that has none.
//
// It exists because every other way to get one is a regeneration. A baseline is
// written by `yoyo init`, which also writes the configuration and every persona
// from the template -- so before this, a project that predated the record, or
// lost it, could only obtain one by discarding the edits the whole comparison
// exists to protect. That is the case this command is for, and it writes exactly
// one file.
//
// What it deliberately does not do is work out what the template said when the
// project was generated. It records what the named bundle supplies now, so
// everything the project holds that differs is treated as the project's own and
// the comparison starts level. Improvements that landed before today are
// therefore counted as yours and never reported. The alternative -- deciding
// that a value equal to some older bundle's was never edited -- is guessing
// about the operator's own file, and the portable-configuration design leaves
// whether that is ever acceptable explicitly open. Under-reporting is the safe
// direction: it is silent about something it could have said, rather than
// claiming an improvement the operator never had.
func runConfigBaseline(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("config baseline", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	from := flags.String("from", config.BuiltinV1, "the bundle to record as this project's template")
	force := flags.Bool("force", false, "replace a baseline that is already there")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "config baseline does not accept positional arguments")
		return 2
	}

	resolved, err := loadConfiguration(*path)
	if err != nil {
		return reportBaselineFailure(stdout, stderr, *jsonOutput, reportedPath(*path, resolved), err)
	}
	// A baseline already there is a record of where values came from, and
	// replacing it silently would throw away everything it knows in exchange for
	// a comparison that starts level. An unusable one is refused the same way:
	// the copy in version control is the better answer, and --force is for when
	// there is not one.
	if _, unknown := config.ReadDrift(resolved); !unknown.Absent {
		if !*force {
			existing := fmt.Errorf("%s already records what this project's template supplied; pass --force to replace it with what %s supplies now, "+
				"which starts the comparison level and forgets every improvement it had not been told about", config.LockFileName, *from)
			if unknown.Reason != "" {
				existing = fmt.Errorf("%s is there and cannot be read (%s); restore it from version control, or pass --force to replace it with "+
					"what %s supplies now, which starts the comparison over rather than recovering it", config.LockFileName, unknown.Reason, *from)
			}
			return reportBaselineFailure(stdout, stderr, *jsonOutput, resolved.Path, existing)
		}
	}

	lock, err := config.NewLock(*from)
	if err != nil {
		return reportBaselineFailure(stdout, stderr, *jsonOutput, resolved.Path, err)
	}
	// Confined to the directory the configuration was read from, through the
	// shared primitive like every other repository write: a `.yoyodyne` somebody
	// symlinked elsewhere must not put this file somewhere nothing reads it.
	root, err := repowrite.NewRoot(filepath.Dir(resolved.Path))
	if err != nil {
		return reportBaselineFailure(stdout, stderr, *jsonOutput, resolved.Path, fmt.Errorf("open %q: %w", filepath.Dir(resolved.Path), err))
	}
	written, err := root.WriteFile(config.LockFileName, lock.Render())
	if err != nil {
		return reportBaselineFailure(stdout, stderr, *jsonOutput, resolved.Path, fmt.Errorf("write %s into %q: %w", config.LockFileName, root.Path(), err))
	}

	if *jsonOutput {
		return writeJSON(stdout, stderr, map[string]any{
			"status":   "written",
			"config":   resolved.Path,
			"baseline": written,
			"bundle":   lock.Bundle,
			"revision": lock.Revision,
			"values":   len(lock.Values),
		})
	}
	fmt.Fprintf(stdout, "wrote %s\n", written)
	fmt.Fprintf(stdout, "recorded %s as this project's template at %s, across %s\n", lock.Bundle, lock.Revision, countOf(len(lock.Values), "value"))
	fmt.Fprintln(stdout, "commit it beside the configuration. this project now reads as level with the template, so `yoyo config drift` reports")
	fmt.Fprintln(stdout, "what moves from here rather than what moved before it.")
	return 0
}

// reportBaselineFailure says what stopped a baseline being written, in whichever
// form was asked for, and exits 1. Nothing was written in any of these cases, so
// the project is exactly as it was.
func reportBaselineFailure(stdout, stderr io.Writer, jsonOutput bool, path string, failure error) int {
	if jsonOutput {
		if code := writeJSON(stdout, stderr, map[string]any{
			"status": "failed",
			"config": path,
			"error":  failure.Error(),
		}); code != 0 {
			return code
		}
	} else {
		fmt.Fprintln(stderr, failure)
	}
	return 1
}

// renderDrift prints the comparison a class at a time, most actionable first.
// Every value carries what it was and what it is, because the report's job is to
// let the operator decide rather than to tell them a verdict.
//
// Three of the four classes print by default and `--all` adds the fourth, which
// is the one where neither side moved: a value the project and the template
// agree on asks nothing of anybody, and a report that opened with a hundred of
// them would bury the handful that do.
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
		// Everything either side moved is printed. Only the values nobody moved
		// are held back, because a project's whole configuration listed as
		// agreeing with its template is the one thing here that says nothing:
		// this command is what the unprompted line points at precisely so that
		// `yours` and `conflicting` can be seen, and hiding `yours` behind a flag
		// would make the report quiet about the same class the notice is.
		if !all && group.class == config.ClassUnchanged {
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
  config baseline   record what that template supplies, for a project with no baseline
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
  sweeps            read what the recurring tasks found on their own cadence
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
	fmt.Fprintln(writer, `Usage: yoyo config <validate|show|drift|baseline> [options]

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
  --all             print every compared value, including the ones neither side moved

config baseline writes that config.lock and nothing else, for a project that has
none -- one generated before the record existed, or one that lost it. `+"`yoyo init`"+`
writes a baseline too, along with the configuration and every persona, so this is
the way to get one without regenerating what you have edited. It records what the
template supplies now, so the project reads as level with it from here: an
improvement that landed earlier is counted as yours and never reported, because
nothing recorded what the template said back then.

config baseline options:
  --from <bundle>   the bundle to record as this project's template (default: builtin:v1)
  --force           replace a baseline that is already there`)
}
