package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"yoyodyne/internal/config"
	"yoyodyne/internal/domain"
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

	written, err := initializeProject(*directory, *product, *force)
	if err != nil {
		if *jsonOutput {
			return writeJSON(stdout, stderr, map[string]any{"status": "failed", "error": err.Error()})
		}
		fmt.Fprintln(stderr, err)
		return 1
	}

	if *jsonOutput {
		return writeJSON(stdout, stderr, map[string]any{
			"status": "written",
			"bundle": config.BuiltinV1,
			"config": written[0],
			"files":  written,
		})
	}
	for _, path := range written {
		fmt.Fprintf(stdout, "wrote %s\n", path)
	}
	fmt.Fprintln(stdout, "the configuration is complete and inherits nothing; edit `checks` before running work")
	return 0
}

// initializeProject renders the scaffold and writes it, configuration file
// first. Every target is checked before anything is written, so a refusal to
// overwrite leaves the project exactly as it was rather than half-configured.
func initializeProject(directory, productID string, force bool) ([]string, error) {
	root, err := filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("resolve project directory %q: %w", directory, err)
	}
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("inspect project directory %q: %w", directory, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("project directory %q is not a directory", directory)
	}

	identifier := strings.TrimSpace(productID)
	if identifier == "" {
		identifier = defaultProductID(root)
	}
	if err := domain.ValidateIdentifier("product id", identifier); err != nil {
		return nil, fmt.Errorf("%w; name one with --product", err)
	}

	scaffold, err := config.NewScaffold(config.BuiltinV1, config.ScaffoldOptions{
		ProductID: identifier,
		// The configuration describes the repository that contains it, and
		// relative paths resolve against that repository rather than against
		// the .yoyodyne directory, so "." is the project root.
		Repository: ".",
	})
	if err != nil {
		return nil, err
	}

	configurationDirectory := filepath.Join(root, config.DirectoryName)
	files := scaffold.Files()
	paths := make([]string, 0, len(files))
	for _, file := range files {
		path := filepath.Join(configurationDirectory, filepath.FromSlash(file.Path))
		if !force {
			if _, err := os.Stat(path); err == nil {
				return nil, fmt.Errorf("%s already exists; pass --force to overwrite it", path)
			} else if !os.IsNotExist(err) {
				return nil, fmt.Errorf("inspect %q: %w", path, err)
			}
		}
		paths = append(paths, path)
	}

	for index, file := range files {
		path := paths[index]
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("create %q: %w", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, file.Content, 0o644); err != nil {
			return nil, fmt.Errorf("write %q: %w", path, err)
		}
	}

	// Loading what was just written is the only proof that it is usable: the
	// personas have to resolve from the project directory they were copied into,
	// not from the bundle they came from.
	if _, err := config.Load(paths[0]); err != nil {
		return nil, fmt.Errorf("the generated configuration does not load: %w", err)
	}
	return paths, nil
}

// defaultProductID names the product after the directory being configured,
// which is what an operator would have typed anyway in the ordinary case.
func defaultProductID(root string) string {
	return strings.ToLower(strings.TrimSpace(filepath.Base(root)))
}
