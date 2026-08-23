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
		return runConfig(args[1:], stdout, stderr)
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
	case "goals":
		return runGoals(ctx, args[1:], stdout, stderr)
	case "stale":
		return reportStaleness(ctx, args[1:], stdout, stderr)
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

func runConfig(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printConfigUsage(stdout)
		return 0
	}
	switch args[0] {
	case "validate":
		return runConfigValidate(args[1:], stdout, stderr)
	case "show":
		return runConfigShow(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown config command %q\n\n", args[0])
		printConfigUsage(stderr)
		return 2
	}
}

func runConfigValidate(args []string, stdout, stderr io.Writer) int {
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

	if *jsonOutput {
		return writeJSON(stdout, stderr, map[string]any{
			"status":     "valid",
			"config":     resolved.Path,
			"sources":    resolved.Sources,
			"product_id": resolved.Config.Product.ID,
			"agents":     len(resolved.Config.Agents),
			"revision":   resolved.Config.Revision(),
		})
	}
	fmt.Fprintf(stdout, "configuration valid: %s (revision %s)\n", resolved.Path, resolved.Config.Revision())
	return 0
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
  artifact          read the canonical artifacts, and record your approval of one
  amendment         read changes proposed to artifacts, and decide them
  goals             read the recorded goals and what work serves, and witness it
  stale             read what a change upstream left unanswered downstream
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
	fmt.Fprintln(writer, `Usage: yoyo config <validate|show> [options]

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --json            emit machine-readable JSON

config show options:
  --effective       print the effective configuration after inheritance (default)
  --origins         print where every effective value came from`)
}
