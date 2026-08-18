package cli

// Reading the canonical artifacts back: what identity each document carries,
// what it says it supports, and which files in the artifact homes are not
// artifacts at all.
//
// There is deliberately no create, amend, or retire here, unlike the invariant
// commands beside it. An artifact's content is written by the role that owns
// it — the operator and the product manager write the brief and the goals, the
// architect writes designs and decisions — and its frontmatter is edited in the
// same file at the same time. What the harness owns is refusing a document whose
// identity is missing, malformed, or claimed by another file, and reporting a
// revision recorded by a role that does not own the document, which is what this
// prints.
//
// The store those commands would go through has the mutations already, gated by
// the same authorization boundary the invariants use, so a role that runs later
// meets the boundary rather than a persona asking it to respect one. Nothing
// calls them yet, which is why the reporting above is the whole of what is live:
// exposing them needs an answer to how a document's prose reaches a command,
// which is not how anybody writes prose today.

import (
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/config"
)

type artifactOutput struct {
	Artifacts         []artifact.Artifact         `json:"artifacts,omitempty"`
	Problems          []artifact.Problem          `json:"problems,omitempty"`
	ReferenceProblems []artifact.ReferenceProblem `json:"reference_problems,omitempty"`
	Error             string                      `json:"error,omitempty"`
}

func runArtifact(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printArtifactUsage(stdout)
		return 0
	}
	switch args[0] {
	case "list":
		return listArtifacts(args[1:], stdout, stderr)
	case "show":
		return showArtifact(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown artifact command %q\n\n", args[0])
		printArtifactUsage(stderr)
		return 2
	}
}

func listArtifacts(args []string, stdout, stderr io.Writer) int {
	flags := newArtifactFlags("artifact list", stderr)
	kind := flags.set.String("kind", "", "list only artifacts of one kind")
	if code, ok := flags.parse(args, 0); !ok {
		return code
	}
	store, code := flags.store(stderr)
	if code != 0 {
		return code
	}
	set, err := store.Load()
	if err != nil {
		return reportArtifactError(stdout, stderr, *flags.jsonOutput, err)
	}
	listed := set.Artifacts
	if strings.TrimSpace(*kind) != "" {
		selected := artifact.Kind(strings.TrimSpace(*kind))
		if !selected.Valid() {
			return reportArtifactError(stdout, stderr, *flags.jsonOutput, fmt.Errorf("unknown artifact kind %q", *kind))
		}
		listed = set.OfKind(selected)
	}
	if *flags.jsonOutput {
		return writeJSON(stdout, stderr, artifactOutput{Artifacts: listed, Problems: set.Problems, ReferenceProblems: set.ReferenceProblems})
	}
	if len(listed) == 0 {
		fmt.Fprintf(stdout, "no artifacts are recorded in %s\n", strings.Join(set.Homes, ", "))
	}
	for _, recorded := range listed {
		fmt.Fprintf(stdout, "%s [%s, %s] %s\n", recorded.ID, recorded.Kind, recorded.Status, recorded.Title)
		fmt.Fprintf(stdout, "  file: %s\n", recorded.Path)
		fmt.Fprintf(stdout, "  supports: %s\n", artifactSupports(recorded))
	}
	// A document in an artifact home that carries no usable identity is not an
	// artifact anything can refer to, so it is named here rather than left to be
	// discovered by whatever tries to link to it.
	for _, problem := range set.Problems {
		fmt.Fprintf(stderr, "not an artifact: %s\n", problem)
	}
	// A relationship that does not hold is reported over the whole set rather
	// than over what --kind selected: the chain runs between kinds, and a listing
	// narrowed to the goals would otherwise hide the design that names one of
	// them and resolves to nothing.
	for _, problem := range set.ReferenceProblems {
		fmt.Fprintf(stderr, "%s: %s\n", problem.Kind, problem)
	}
	return 0
}

func showArtifact(args []string, stdout, stderr io.Writer) int {
	flags := newArtifactFlags("artifact show", stderr)
	if code, ok := flags.parse(args, 1); !ok {
		return code
	}
	store, code := flags.store(stderr)
	if code != 0 {
		return code
	}
	set, err := store.Load()
	if err != nil {
		return reportArtifactError(stdout, stderr, *flags.jsonOutput, err)
	}
	found, ok := set.Find(flags.set.Arg(0))
	if !ok {
		return reportArtifactError(stdout, stderr, *flags.jsonOutput,
			fmt.Errorf("no artifact %q is recorded in %s", flags.set.Arg(0), strings.Join(set.Homes, ", ")))
	}
	problems := set.ReferenceProblemsFor(found.ID)
	if *flags.jsonOutput {
		return writeJSON(stdout, stderr, artifactOutput{Artifacts: []artifact.Artifact{found}, ReferenceProblems: problems})
	}
	fmt.Fprintf(stdout, "%s [%s, %s] %s\n", found.ID, found.Kind, found.Status, found.Title)
	fmt.Fprintf(stdout, "file: %s\n", found.Path)
	fmt.Fprintf(stdout, "supports: %s\n\n", artifactSupports(found))
	for _, revision := range found.Revisions {
		fmt.Fprintf(stdout, "%s %s by the %s: %s\n",
			revision.At.UTC().Format(time.RFC3339), revision.Action, revision.By, revision.Reason)
	}
	// What this one document's place in the chain is wrong about, so somebody
	// asking after a single artifact is told without reading the whole listing.
	for _, problem := range problems {
		fmt.Fprintf(stderr, "%s: %s\n", problem.Kind, problem.Reason)
	}
	return 0
}

// artifactFlags is the flag set every artifact command shares: which
// configuration to read the homes from, and how to report the result.
type artifactFlags struct {
	set        *flag.FlagSet
	name       string
	configPath *string
	jsonOutput *bool
}

func newArtifactFlags(name string, stderr io.Writer) *artifactFlags {
	set := flag.NewFlagSet(name, flag.ContinueOnError)
	set.SetOutput(stderr)
	return &artifactFlags{
		set:        set,
		name:       name,
		configPath: set.String("config", "", "configuration file path (default: the nearest project configuration)"),
		jsonOutput: set.Bool("json", false, "emit machine-readable JSON"),
	}
}

func (f *artifactFlags) parse(args []string, positional int) (int, bool) {
	if err := f.set.Parse(args); err != nil {
		return 2, false
	}
	if f.set.NArg() != positional {
		if positional == 0 {
			fmt.Fprintf(f.set.Output(), "%s does not accept positional arguments\n", f.name)
		} else {
			fmt.Fprintf(f.set.Output(), "%s requires exactly one artifact id\n", f.name)
		}
		printArtifactUsage(f.set.Output())
		return 2, false
	}
	return 0, true
}

// store resolves the artifact homes the same way every other command resolves
// the repository: relative to the project rather than to the .yoyodyne
// directory the configuration happens to live in.
func (f *artifactFlags) store(stderr io.Writer) (artifact.Store, int) {
	resolved, err := loadConfiguration(*f.configPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return artifact.Store{}, 1
	}
	repository, err := resolvePath(config.ProjectDirectory(resolved.Path), resolved.Config.Product.Repository)
	if err != nil {
		fmt.Fprintf(stderr, "resolve product repository: %v\n", err)
		return artifact.Store{}, 1
	}
	return artifactStore(repository, resolved.Config.Product), 0
}

// artifactStore is how the configured directories become a store, in one place
// rather than at each call site: the three artifact homes, and the invariants
// directory excluded from them because its files carry the identity scheme this
// one was modeled on rather than this one.
func artifactStore(repositoryRoot string, product config.Product) artifact.Store {
	return artifact.Store{
		RepositoryRoot: repositoryRoot,
		Homes:          []string{product.Specifications, product.Designs, product.Decisions},
		Excluded:       []string{product.Invariants},
	}
}

func reportArtifactError(stdout, stderr io.Writer, jsonOutput bool, err error) int {
	if jsonOutput {
		if code := writeJSON(stdout, stderr, artifactOutput{Error: err.Error()}); code != 0 {
			return code
		}
		return 1
	}
	fmt.Fprintln(stderr, err)
	return 1
}

func artifactSupports(recorded artifact.Artifact) string {
	if len(recorded.Supports) == 0 {
		return "nothing upstream"
	}
	return strings.Join(recorded.Supports, ", ")
}

func printArtifactUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo artifact <list|show> [options]

The canonical documents upstream of a work item -- the product brief, the goals,
the designs and specifications, and the decision records -- each carry a stable
id, a kind, a lifecycle status, what they support upstream, and a revision log,
in frontmatter at the top of the file. The id is the file name, so a document
whose frontmatter claims another id, and two documents claiming one id, are
refused rather than reconciled.

The relationships are checked too, and reported rather than refused: a supports
entry naming an id no artifact answers to, and an artifact nothing connects back
to the brief, are named on stderr beside a set that still holds every document
it read. The brief is the root and the decision records are not downstream of
it, so neither is reported for supporting nothing.

Who changed each document is reported the same way. The product manager owns the
brief and the goals and the architect owns the designs, specifications, and
decision records, so a revision log recording a change by any other role is named
on stderr as an unauthorized revision. The document still loads: the log is
append-only, and losing it would leave a document nobody could correct.

  list [--kind <kind>]   list the recorded artifacts, and name what is not one
  show <id>              print one artifact and its recorded revisions

Options:
  --config <path>       configuration file (default: the nearest .yoyodyne/config.yaml)
  --json                emit machine-readable JSON

list options:
  --kind <kind>         brief, goals, non-goals, design, specification, or decision`)
}
