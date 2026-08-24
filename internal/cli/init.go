package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifacthome"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/repowrite"
)

// runInit writes a project its own complete configuration. The built-in bundle
// is the template it is generated from rather than a layer it keeps inheriting,
// so what the project can read is all of what runs.
func runInit(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("init", flag.ContinueOnError)
	flags.SetOutput(stderr)
	directory := flags.String("directory", ".", "project directory to configure")
	product := flags.String("product", "", "product id (default: the project directory name)")
	trackerRemote := flags.String("tracker-remote", "", "URL the tracker syncs through (default: this project's Git remote)")
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

	// The project root is named from what was written rather than resolved a
	// second time: the configuration is the first file written, and it is always
	// at <root>/.yoyodyne/config.yaml.
	tracker := configureTrackerRemote(ctx, filepath.Dir(filepath.Dir(written[0])), *trackerRemote, execution.OSProcessRunner{})
	// A configuration the repository ignores is one this machine has and no
	// other will, and the moment it was written is the moment somebody can still
	// decide about it cheaply.
	ignored := configurationIgnored(ctx, execution.OSProcessRunner{}, written[0])

	if *jsonOutput {
		return writeJSON(stdout, stderr, map[string]any{
			"status": "written",
			"bundle": config.BuiltinV1,
			"config": written[0],
			"files":  written,
			// What was written, and what was detected, are reported separately:
			// a candidate is detected and deliberately not written, and a caller
			// that could not tell them apart could not check either. The three
			// detected lists are kept apart for the same reason -- they ask the
			// operator for a decision, for an optional one, and for nothing.
			"checks":   detection.Commands(),
			"detected": detection,
			"tracker":  tracker,
			"ignored":  ignored,
		})
	}
	for _, path := range written {
		fmt.Fprintf(stdout, "wrote %s\n", path)
	}
	fmt.Fprintln(stdout, "the configuration is complete and inherits nothing")
	fmt.Fprintln(stdout, describeDetection(detection, written[0]))
	// The tracker step is reported on the stream its outcome belongs to and
	// never changes the exit code. The configuration is written and valid either
	// way, and an init that failed after writing it would be describing a
	// project it had already changed as one it had not.
	if tracker.Status == trackerRemoteFailed {
		fmt.Fprintln(stderr, describeTrackerRemote(tracker))
	} else {
		fmt.Fprintln(stdout, describeTrackerRemote(tracker))
	}
	// Reported like the tracker's failures and for the same reason: the files are
	// written and valid, so an ignored configuration is something to know about
	// an init that worked rather than a reason to call it one that did not.
	if ignored.Ignored {
		fmt.Fprintln(stderr, describeIgnoredConfiguration(ignored))
	}
	return 0
}

// The tracker rides the project's own Git remote by default: its history moves
// under refs of Dolt's own beside the code it tracks, which is one repository,
// one permission model, and nothing to stand up. The remote is named `origin`
// on both sides because that is the remote each tool means by default.
const (
	trackerRemoteName = "origin"
	// trackerCommandTimeout bounds the two commands the tracker step runs:
	// reading where Git points, and telling bd about it. Neither reaches the
	// remote, but bd's first invocation in a project starts its database engine,
	// so the budget is the one every other bd call here is given rather than the
	// shorter one a purely local command would suggest.
	trackerCommandTimeout = 30 * time.Second
)

// What init did about the tracker's sync remote. The four outcomes are kept
// apart because they ask the operator for different things: a configured
// tracker and one left as it was ask nothing, a skipped one asks for a Git
// remote, and a failed one asks for a look at why bd refused.
const (
	trackerRemoteConfigured = "configured"
	trackerRemoteUnchanged  = "unchanged"
	trackerRemoteSkipped    = "skipped"
	trackerRemoteFailed     = "failed"
)

// trackerRemote is what became of the tracker's sync remote, reported beside
// the files init wrote. An unconfigured tracker is the first gap of running
// Yoyodyne as a team -- every machine's backlog drifting apart silently -- and
// it is closed at install time here rather than discovered at first divergence.
type trackerRemote struct {
	Status string `json:"status"`
	Name   string `json:"name,omitempty"`
	URL    string `json:"url,omitempty"`
	// Reason says why a tracker was left unconfigured, and is empty when one
	// was configured. It carries Git's or bd's own words rather than a summary
	// of them, because the remedy is a command to one of those two tools.
	Reason string `json:"reason,omitempty"`
}

// configureTrackerRemote points the tracker at the URL it should sync through.
//
// It never overwrites the remote the tracker already holds under this name
// unless a URL was named explicitly: a project that deliberately syncs its
// tracker somewhere else -- a repository of its own, which bd supports with any
// Git URL -- must not have that decision undone by a later `yoyo init`. A named
// URL does replace it, which is what the flag is for, and bd replaces a remote
// it already holds rather than refusing the name it is given.
//
// Its failures are reported rather than raised. Everything init promises is
// already on disk by the time this runs, and the tracker is a separate tool
// with its own initialization, so a project whose bd is not ready yet is told
// what to run instead of being handed a failed init over files that are fine.
func configureTrackerRemote(ctx context.Context, root, requested string, runner execution.ProcessRunner) trackerRemote {
	client := beads.Client{Runner: runner, Dir: root, Timeout: trackerCommandTimeout}
	url := strings.TrimSpace(requested)
	if url == "" {
		var absence string
		url, absence = gitRemoteURL(ctx, runner, root, trackerRemoteName)
		if url == "" {
			return trackerRemote{Status: trackerRemoteSkipped, Reason: absence}
		}
		existing, err := client.SyncRemotes(ctx)
		if err != nil {
			return trackerRemote{Status: trackerRemoteFailed, Reason: err.Error()}
		}
		for _, remote := range existing {
			if remote.Name == trackerRemoteName {
				return trackerRemote{Status: trackerRemoteUnchanged, Name: remote.Name, URL: remote.URL}
			}
		}
	}
	configured, err := client.SetSyncRemote(ctx, trackerRemoteName, url)
	if err != nil {
		return trackerRemote{Status: trackerRemoteFailed, Reason: err.Error()}
	}
	return trackerRemote{Status: trackerRemoteConfigured, Name: configured.Name, URL: configured.URL}
}

// gitRemoteURL reads where one of the project's Git remotes points. A project
// that has no such remote, and a directory that is not a repository at all,
// both answer with no URL and the reason there is none: neither is a failure of
// init, and both mean there is nothing yet for the tracker to sync to.
func gitRemoteURL(ctx context.Context, runner execution.ProcessRunner, root, remote string) (string, string) {
	result, err := runner.Run(ctx, execution.Command{
		Name:    "git",
		Args:    []string{"-C", root, "remote", "get-url", remote},
		Timeout: trackerCommandTimeout,
	}, nil)
	if err != nil {
		return "", fmt.Sprintf("git could not be run here: %v", err)
	}
	if result.Status != execution.ProcessSucceeded {
		if message := singleLine(firstNonEmpty(result.Stderr, result.Stdout)); message != "" {
			return "", message
		}
		return "", fmt.Sprintf("this project has no Git remote named %s", remote)
	}
	url := strings.TrimSpace(result.Stdout)
	if url == "" {
		return "", fmt.Sprintf("the Git remote %s reported no URL", remote)
	}
	return url, ""
}

// describeTrackerRemote says what became of the tracker's sync remote, and what
// the operator would run to finish the job themselves where it was not done.
// A reason is folded to one line here and kept whole in the JSON report: bd
// answers a question it cannot answer with a hint on a second line, and a line
// an operator reads among the files that were written stays a line.
func describeTrackerRemote(tracker trackerRemote) string {
	switch tracker.Status {
	case trackerRemoteConfigured:
		return fmt.Sprintf("the tracker syncs through %s: %s", tracker.Name, tracker.URL)
	case trackerRemoteUnchanged:
		return fmt.Sprintf("the tracker already syncs through %s: %s; name a URL with --tracker-remote to point it elsewhere",
			tracker.Name, tracker.URL)
	case trackerRemoteSkipped:
		return fmt.Sprintf("the tracker syncs nowhere -- %s; configure one with `bd dolt remote add %s <url>` so the backlog is not per-machine",
			singleLine(tracker.Reason), trackerRemoteName)
	default:
		// bd's own words are carried above, hint and all, so the remedy here
		// names the command to run rather than guessing at what went wrong: an
		// uninitialized tracker and one that refused for another reason are not
		// put right the same way.
		return fmt.Sprintf("the tracker syncs nowhere -- %s; the configuration was written, so configure it with `bd dolt remote add %s <url>` once bd answers here",
			singleLine(tracker.Reason), trackerRemoteName)
	}
}

// describeDetection says what became of the checks list, which is the one thing
// in a generated configuration an operator still owes a decision on.
func describeDetection(detection config.Detection, path string) string {
	switch {
	case len(detection.Checks) > 0:
		reported := fmt.Sprintf("proposed %s from %s; review the checks in %s before running work",
			countOf(len(detection.Checks), "check"), strings.Join(config.ProposalSources(detection.Checks), ", "), path)
		// Alternatives are not counted here: nothing is owed on them, and a line
		// that counts what needs no decision alongside what does is a line that
		// stops distinguishing them.
		if len(detection.Candidates) > 0 {
			reported += fmt.Sprintf(", with %s it could not settle left commented out beside them",
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
	// The project is the repository this writes into, and everything it writes is
	// confined to it: a scaffold that landed outside is a configuration the
	// operator was told about and cannot find, in a place nothing reviews.
	project, err := repowrite.NewRoot(directory)
	if err != nil {
		return nil, detection, fmt.Errorf("open project directory %q: %w", directory, err)
	}
	root := project.Path()
	// What the operator called the project, which is what the files it wrote are
	// reported as. Confinement is decided against the resolved root, but a person
	// who typed one path and is told about another has to work out for themselves
	// that the two are the same directory.
	named, err := filepath.Abs(directory)
	if err != nil {
		return nil, detection, fmt.Errorf("resolve project directory %q: %w", directory, err)
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

	files := scaffold.Files()
	// Every target is resolved before anything is written, so a scaffold that
	// would leave the project — through a `.yoyodyne` somebody symlinked
	// elsewhere, or a directory above it — is refused with the project untouched
	// rather than half of it written somewhere nobody looks.
	targets := make([]string, 0, len(files))
	paths := make([]string, 0, len(files))
	for _, file := range files {
		target := config.DirectoryName + "/" + file.Path
		path := filepath.Join(named, filepath.FromSlash(target))
		// Where the bytes would actually land, which is what an existing file has to
		// be looked for at: a target the project does not contain is refused here,
		// with nothing written.
		resolved, err := project.Resolve(target)
		if err != nil {
			return nil, detection, fmt.Errorf("write %s into %q: %w", target, directory, err)
		}
		if !force {
			if _, err := os.Lstat(resolved); err == nil {
				return nil, detection, fmt.Errorf("%s already exists; pass --force to overwrite it", path)
			} else if !os.IsNotExist(err) {
				return nil, detection, fmt.Errorf("inspect %q: %w", path, err)
			}
		}
		targets = append(targets, target)
		paths = append(paths, path)
	}

	for index := range files {
		if _, err := project.WriteFile(targets[index], files[index].Content); err != nil {
			return nil, detection, fmt.Errorf("write %s into %q: %w", targets[index], directory, err)
		}
	}

	// Loading what was just written is the only proof that it is usable: the
	// personas have to resolve from the project directory they were copied into,
	// not from the bundle they came from.
	loaded, err := config.Load(paths[0])
	if err != nil {
		return nil, detection, fmt.Errorf("the generated configuration does not load: %w", err)
	}

	// The artifact homes the configuration just named each get an index saying
	// what is filed there, whose it is, and whether the operator may edit one by
	// hand. They are written from the loaded configuration rather than from the
	// scaffold, so a project that pointed its designs somewhere else is indexed
	// where its designs actually are.
	indexes, err := writeArtifactHomeIndexes(project, named, loaded)
	if err != nil {
		return nil, detection, err
	}
	return append(paths, indexes...), detection, nil
}

// writeArtifactHomeIndexes writes the README each artifact home gets, and
// returns the ones it wrote.
//
// An index that is already there is left exactly as it is, and `--force` does not
// reach these: that flag is about regenerating the configuration this command
// owns, and a README in a documentation directory is the project's own prose
// rather than something init generated. Refusing the whole init over one would be
// worse still — it would refuse `yoyo init` in every repository that already has
// a word of its own at the door of `docs/`. What to do about an index that is
// there and no longer answers the three questions is `yoyo doctor`'s to report
// and `yoyo setup`'s to offer, where replacing somebody's prose is something they
// are asked about first.
func writeArtifactHomeIndexes(project repowrite.Root, named string, cfg config.Config) ([]string, error) {
	var written []string
	for _, status := range artifacthome.Inspect(project, cfg) {
		if status.State != artifacthome.StateMissing {
			continue
		}
		if _, err := artifacthome.Write(project, status.Home); err != nil {
			return nil, err
		}
		// Named the way the operator named the project, like every other path
		// this command reports, rather than the resolved form the write returns.
		written = append(written, filepath.Join(named, filepath.FromSlash(status.Path)))
	}
	return written, nil
}

// defaultProductID names the product after the directory being configured,
// which is what an operator would have typed anyway in the ordinary case.
func defaultProductID(root string) string {
	return strings.ToLower(strings.TrimSpace(filepath.Base(root)))
}
