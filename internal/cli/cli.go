package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"yoyodyne/internal/config"
)

const defaultConfigPath = ".yoyodyne.yaml"

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
	case "config":
		return runConfig(args[1:], stdout, stderr)
	case "run":
		return runWorkItem(ctx, args[1:], stdout, stderr)
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
	if args[0] != "validate" {
		fmt.Fprintf(stderr, "unknown config command %q\n\n", args[0])
		printConfigUsage(stderr)
		return 2
	}

	flags := flag.NewFlagSet("config validate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	path := flags.String("config", defaultConfigPath, "configuration file path")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "config validate does not accept positional arguments")
		return 2
	}

	cfg, err := config.Load(*path)
	if err != nil {
		if *jsonOutput {
			code := writeJSON(stdout, stderr, map[string]any{
				"status": "invalid",
				"config": *path,
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
			"config":     *path,
			"product_id": cfg.Product.ID,
			"agents":     len(cfg.Agents),
		})
	}
	fmt.Fprintf(stdout, "configuration valid: %s\n", *path)
	return 0
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
	fmt.Fprintln(writer, `Usage: yoyodyne <command> [options]

Commands:
  config validate   validate a Yoyodyne configuration
  run               run one Beads work item in an isolated worktree
  version           print version information
  help              show this help`)
}

func printConfigUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyodyne config validate [options]

Options:
  --config <path>   configuration file (default .yoyodyne.yaml)
  --json            emit machine-readable JSON`)
}
