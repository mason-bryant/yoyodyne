package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// runInit writes a project its own complete configuration. The built-in bundle
// is the template it is generated from rather than a layer it keeps inheriting,
// so what the project can read is all of what runs.
func runInit(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("init", flag.ContinueOnError)
	flags.SetOutput(stderr)
	directory := flags.String("directory", ".", "project directory to configure")
	product := flags.String("product", "", "product id (default: the project directory name)")
	force := flags.Bool("force", false, "overwrite files that already exist")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "init does not accept positional arguments")
		return 2
	}

	written, detection, err := initializeProject(*directory, *product, *force)
	if err != nil {
		// A reported failure exits nonzero whichever form it was reported in,
		// so a script reading the JSON does not have to parse it to notice.
		if *jsonOutput {
			if code := writeJSON(stdout, stderr, map[string]any{"status": "failed", "error": err.Error()}); code != 0 {
				return code
			}
		} else {
			fmt.Fprintln(stderr, err)
		}
		return 1
	}

	if *jsonOutput {
		return writeJSON(stdout, stderr, map[string]any{
			"status": "written",
			"bundle": config.BuiltinV1,
			"config": written[0],
			"files":  written,
			// What was written, and what was detected, are reported separately:
			// a candidate is detected and deliberately not written, and a caller
			// that could not tell them apart could not check either.
			"checks":   detection.Commands(),
			"detected": detection,
		})
	}
	for _, path := range written {
		fmt.Fprintf(stdout, "wrote %s\n", path)
	}
	fmt.Fprintln(stdout, "the configuration is complete and inherits nothing")
	fmt.Fprintln(stdout, describeDetection(detection, written[0]))
	return 0
}

// describeDetection says what became of the checks list, which is the one thing
// in a generated configuration an operator still owes a decision on.
func describeDetection(detection config.Detection, path string) string {
	switch {
	case len(detection.Checks) > 0:
		reported := fmt.Sprintf("proposed %s from %s; review the checks in %s before running work",
			countOf(len(detection.Checks), "check"), strings.Join(config.ProposalSources(detection.Checks), ", "), path)
		if len(detection.Candidates) > 0 {
			reported += fmt.Sprintf(", with %s left commented out beside them",
				countOf(len(detection.Candidates), "candidate"))
		}
		return reported
	case len(detection.Candidates) > 0:
		return fmt.Sprintf("checks is empty: %s are commented in %s, and one of them has to be chosen before work can run",
			countOf(len(detection.Candidates), "candidate"), path)
	default:
		return fmt.Sprintf("checks is empty and nothing in this project proposed one; write your own in %s before running work", path)
	}
}

func countOf(count int, noun string) string {
	if count == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", count, noun)
}

// initializeProject renders the scaffold and writes it, configuration file
// first. Every target is checked before anything is written, so a refusal to
// overwrite leaves the project exactly as it was rather than half-configured.
// The detection it returns is what the generated file proposed as checks, so the
// command can report what was found alongside what was written.
func initializeProject(directory, productID string, force bool) ([]string, config.Detection, error) {
	var detection config.Detection
	root, err := filepath.Abs(directory)
	if err != nil {
		return nil, detection, fmt.Errorf("resolve project directory %q: %w", directory, err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, detection, fmt.Errorf("inspect project directory %q: %w", directory, err)
	}
	if !info.IsDir() {
		return nil, detection, fmt.Errorf("project directory %q is not a directory", directory)
	}

	identifier := strings.TrimSpace(productID)
	if identifier == "" {
		identifier = defaultProductID(root)
	}
	if err := domain.ValidateIdentifier("product id", identifier); err != nil {
		return nil, detection, fmt.Errorf("%w; name one with --product", err)
	}

	// The project's own files are read before anything is generated, so what the
	// scaffold proposes for `checks` comes from this repository rather than from
	// the template. Reading is all that happens: no command here is executed.
	detection = config.DetectChecks(root)
	scaffold, err := config.NewScaffold(config.BuiltinV1, config.ScaffoldOptions{
		ProductID: identifier,
		// The configuration describes the repository that contains it, and
		// relative paths resolve against that repository rather than against
		// the .yoyodyne directory, so "." is the project root.
		Repository: ".",
		Detection:  detection,
	})
	if err != nil {
		return nil, detection, err
	}

	configurationDirectory := filepath.Join(root, config.DirectoryName)
	files := scaffold.Files()
	paths := make([]string, 0, len(files))
	for _, file := range files {
		path := filepath.Join(configurationDirectory, filepath.FromSlash(file.Path))
		if !force {
			if _, err := os.Stat(path); err == nil {
				return nil, detection, fmt.Errorf("%s already exists; pass --force to overwrite it", path)
			} else if !os.IsNotExist(err) {
				return nil, detection, fmt.Errorf("inspect %q: %w", path, err)
			}
		}
		paths = append(paths, path)
	}

	for index, file := range files {
		path := paths[index]
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, detection, fmt.Errorf("create %q: %w", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, file.Content, 0o644); err != nil {
			return nil, detection, fmt.Errorf("write %q: %w", path, err)
		}
	}

	// Loading what was just written is the only proof that it is usable: the
	// personas have to resolve from the project directory they were copied into,
	// not from the bundle they came from.
	if _, err := config.Load(paths[0]); err != nil {
		return nil, detection, fmt.Errorf("the generated configuration does not load: %w", err)
	}
	return paths, detection, nil
}

// defaultProductID names the product after the directory being configured,
// which is what an operator would have typed anyway in the ordinary case.
func defaultProductID(root string) string {
	return strings.ToLower(strings.TrimSpace(filepath.Base(root)))
}
