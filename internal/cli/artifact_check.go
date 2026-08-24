package cli

// The gate for the governed documents, run by whoever owns one.
//
// The rest of the artifact commands report what is wrong and exit zero, which is
// the right thing for a listing: an unapproved document, a broken relationship,
// and a revision crossing the ownership boundary are all things that stop
// nothing, and a command that refused over one would be a command nobody could
// use to look. This one exits non-zero on any of them, and that is the whole
// difference: it is the check an owner runs after editing, and the place a
// governed-document defect fails.
//
// It exists because the failing used to happen somewhere nobody could act. The
// repository-coupled gates in `make test` read these same documents, and a
// malformed one turned every developer's run red over a file every developer's
// diff refuses — so the only route out was an amendment proposal made while
// every run stayed red. Those gates escalate now (internal/governeddoc), which
// leaves this as the place the defect still fails loudly, run by the one person
// who can put it right. The index at the door of every artifact home names it.

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/goal"
	"github.com/mason-bryant/yoyodyne/internal/governeddoc"
	"github.com/mason-bryant/yoyodyne/internal/invariant"
)

// governedCheck is what the check found, for a machine.
type governedCheck struct {
	// Homes are the directories that were read, so a report that found nothing
	// can say where it looked rather than only that it is clean.
	Homes   []string         `json:"homes,omitempty"`
	Defects []governedDefect `json:"defects,omitempty"`
	Error   string           `json:"error,omitempty"`
}

// governedDefect is one defect and the role it belongs to.
type governedDefect struct {
	Path   string `json:"path"`
	Detail string `json:"detail"`
	// Home and Owner are the artifact home the document is filed in and the role
	// that may change it, and are empty for a file no home claims.
	Home  string `json:"home,omitempty"`
	Owner string `json:"owner,omitempty"`
	// Route is what to do about it, present exactly when the defect is not the
	// reader's own to fix.
	Route string `json:"route,omitempty"`
}

// checkGovernedDocuments reads every governed document and reports what is
// wrong with each, routed to the role that owns it.
//
// It reads all four kinds of finding in one pass rather than leaving three of
// them to three other commands: an owner who has just edited a goals document
// wants one answer about what they wrote, and a check they have to run four
// times is a check they run none of.
func checkGovernedDocuments(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("artifact check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	parsed, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(parsed) != 0 {
		fmt.Fprintln(stderr, "artifact check does not accept positional arguments: it reads every governed document")
		printArtifactUsage(stderr)
		return 2
	}

	resolved, err := loadConfiguration(*configPath)
	if err != nil {
		return reportGovernedCheckError(stdout, stderr, *jsonOutput, err)
	}
	repository, err := resolvePath(config.ProjectDirectory(resolved.Path), resolved.Config.Product.Repository)
	if err != nil {
		return reportGovernedCheckError(stdout, stderr, *jsonOutput, fmt.Errorf("resolve product repository: %w", err))
	}
	product := resolved.Config.Product
	artifacts, err := artifactStore(repository, product).Load()
	if err != nil {
		return reportGovernedCheckError(stdout, stderr, *jsonOutput, err)
	}

	defects := governedDefects(artifacts.Problems, artifacts.ReferenceProblems,
		goal.Collect(repository, artifacts),
		invariantProblems(repository, product))
	routed := governeddoc.Route(resolved.Config, defects...)

	if *jsonOutput {
		report := governedCheck{Homes: artifacts.Homes}
		for _, entry := range routed {
			report.Defects = append(report.Defects, governedDefect{
				Path:   entry.Path,
				Detail: entry.Detail,
				Home:   entry.Home,
				Owner:  string(entry.Owner),
				Route:  entry.Route,
			})
		}
		if code := writeJSON(stdout, stderr, report); code != 0 {
			return code
		}
		if len(routed) > 0 {
			return 1
		}
		return 0
	}

	if len(routed) == 0 {
		fmt.Fprintf(stdout, "every governed document in %s is well-formed\n", strings.Join(artifacts.Homes, ", "))
		return 0
	}
	// One defect to an entry, with the route under it: what somebody has to do is
	// open one file, and a list of defects followed by a list of routes is a list
	// they have to match up by hand.
	for _, entry := range routed {
		fmt.Fprintf(stderr, "%s\n", entry)
	}
	fmt.Fprintf(stderr, "\n%s in the governed documents\n", countOf(len(routed), "defect"))
	return 1
}

// governedDefects folds every reader's own problem type into the one shape the
// routing is done over. Each reason is carried through in the words the reader
// wrote it in and prefixed with what kind of finding it is, because "not read as
// an artifact" and "read, and its place in the chain is wrong" are two different
// things to do about the same file.
func governedDefects(
	refused []artifact.Problem,
	references []artifact.ReferenceProblem,
	goals goal.Set,
	invariants []invariant.Problem,
) []governeddoc.Defect {
	var defects []governeddoc.Defect
	add := func(path, detail string) {
		defects = append(defects, governeddoc.Defect{Path: path, Detail: detail})
	}
	for _, problem := range refused {
		add(problem.Path, "it is not read as an artifact: "+problem.Reason)
	}
	for _, problem := range references {
		add(problem.Path, string(problem.Kind)+": "+problem.Reason)
	}
	for _, problem := range goals.Problems {
		add(problem.Path, "its goals could not be read: "+problem.Reason)
	}
	for _, problem := range goals.LinkProblems {
		add(problem.Path, "a goal is not linked to the brief: "+problem.Reason)
	}
	for _, problem := range goals.WrapProblems {
		add(problem.Path, fmt.Sprintf("line %d: a goal is not written on one line: %s", problem.Line, problem.Reason))
	}
	for _, problem := range invariants {
		add(problem.Path, "it is not read as an invariant: "+problem.Reason)
	}
	return defects
}

// invariantProblems is the invariants home read for the same question. A
// directory that could not be opened at all reports nothing here rather than
// failing the check: a project with no invariants yet simply has none, and the
// missing-directory case is `yoyo doctor`'s to describe.
func invariantProblems(repository string, product config.Product) []invariant.Problem {
	if strings.TrimSpace(product.Invariants) == "" {
		return nil
	}
	set, err := (invariant.Store{RepositoryRoot: repository, Directory: product.Invariants}).Load()
	if err != nil {
		return nil
	}
	return set.Problems
}

func reportGovernedCheckError(stdout, stderr io.Writer, jsonOutput bool, err error) int {
	if jsonOutput {
		if code := writeJSON(stdout, stderr, governedCheck{Error: err.Error()}); code != 0 {
			return code
		}
		return 1
	}
	fmt.Fprintln(stderr, err)
	return 1
}
