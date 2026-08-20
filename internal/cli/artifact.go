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
//
// `approve` is here for the opposite reason, and is the one thing these commands
// write. An approval is the operator's, the operator is who runs a command, and
// what it records is a fact about them rather than prose about the product — so
// unlike an amendment it needs no answer to how a document gets written, and
// unlike a role's mutation it is nobody else's to make. Nothing here refuses an
// unapproved document and nothing stops loading one, and the one thing that
// turns on the record lives elsewhere: work is admitted to the queue without the
// operator being asked where it traces to a goal an approved goals document
// states, so approving the goals is what a project running that way is doing
// when it runs this.

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
	Artifacts []artifact.Artifact `json:"artifacts,omitempty"`
	// Approvals pairs each listed artifact with what is recorded about its
	// approval and what the configuration asks for, keyed by artifact id. The
	// state is reported rather than left to be derived, because "approved" and
	// "approved at a revision that has been amended since" is exactly the
	// distinction a reader needs and exactly the one that is easy to get wrong.
	Approvals         map[string]artifactApproval `json:"approvals,omitempty"`
	Problems          []artifact.Problem          `json:"problems,omitempty"`
	ReferenceProblems []artifact.ReferenceProblem `json:"reference_problems,omitempty"`
	Error             string                      `json:"error,omitempty"`
}

// artifactApproval is what a machine-readable listing says about one document's
// approval.
type artifactApproval struct {
	State artifact.ApprovalState `json:"state"`
	// Required is whether this project asked for the operator's approval of this
	// kind of document, and Setting names the configuration that decided.
	Required bool   `json:"required"`
	Setting  string `json:"setting,omitempty"`
	Mode     string `json:"mode,omitempty"`
	// Approval is the most recent one recorded, absent when there is none.
	Approval *artifact.Approval `json:"approval,omitempty"`
	// RevisionsSinceApproval is how far the document has moved since, and is zero
	// unless the state is amended.
	RevisionsSinceApproval int `json:"revisions_since_approval,omitempty"`
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
	case "approve":
		return approveArtifact(args[1:], stdout, stderr)
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
	store, policy, code := flags.store(stderr)
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
		return writeJSON(stdout, stderr, artifactOutput{
			Artifacts:         listed,
			Approvals:         artifactApprovals(listed, policy),
			Problems:          set.Problems,
			ReferenceProblems: set.ReferenceProblems,
		})
	}
	if len(listed) == 0 {
		fmt.Fprintf(stdout, "no artifacts are recorded in %s\n", strings.Join(set.Homes, ", "))
	}
	for _, recorded := range listed {
		fmt.Fprintf(stdout, "%s [%s, %s] %s\n", recorded.ID, recorded.Kind, recorded.Status, recorded.Title)
		fmt.Fprintf(stdout, "  file: %s\n", recorded.Path)
		fmt.Fprintf(stdout, "  supports: %s\n", artifactSupports(recorded))
		fmt.Fprintf(stdout, "  approval: %s\n", renderArtifactApproval(recorded, policy))
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
	store, policy, code := flags.store(stderr)
	if code != 0 {
		return code
	}
	set, err := store.Load()
	if err != nil {
		return reportArtifactError(stdout, stderr, *flags.jsonOutput, err)
	}
	found, ok := set.Find(flags.id())
	if !ok {
		return reportArtifactError(stdout, stderr, *flags.jsonOutput,
			fmt.Errorf("no artifact %q is recorded in %s", flags.id(), strings.Join(set.Homes, ", ")))
	}
	problems := set.ReferenceProblemsFor(found.ID)
	if *flags.jsonOutput {
		return writeJSON(stdout, stderr, artifactOutput{
			Artifacts:         []artifact.Artifact{found},
			Approvals:         artifactApprovals([]artifact.Artifact{found}, policy),
			ReferenceProblems: problems,
		})
	}
	fmt.Fprintf(stdout, "%s [%s, %s] %s\n", found.ID, found.Kind, found.Status, found.Title)
	fmt.Fprintf(stdout, "file: %s\n", found.Path)
	fmt.Fprintf(stdout, "supports: %s\n", artifactSupports(found))
	fmt.Fprintf(stdout, "approval: %s\n\n", renderArtifactApproval(found, policy))
	for index, revision := range found.Revisions {
		fmt.Fprintf(stdout, "%s %s by the %s: %s\n",
			revision.At.UTC().Format(time.RFC3339), revision.Action, revision.By, revision.Reason)
		// Each approval is printed under the revision it was given for, because
		// which version of the document the operator saw is the whole of what
		// distinguishes an approval that still stands from one that has been
		// amended out from under.
		for _, approval := range found.Approvals {
			if approval.Revision == index {
				fmt.Fprintf(stdout, "  approved by the %s %s: %s\n",
					approval.By, approval.At.UTC().Format(time.RFC3339), approval.Reason)
			}
		}
	}
	// What this one document's place in the chain is wrong about, so somebody
	// asking after a single artifact is told without reading the whole listing.
	for _, problem := range problems {
		fmt.Fprintf(stderr, "%s: %s\n", problem.Kind, problem.Reason)
	}
	return 0
}

// approveArtifact records that the operator approved a document as it now
// stands. The revision it applies to is never asked for: it is the last one the
// document records, so an approval cannot be recorded against a version of the
// document nobody was looking at.
func approveArtifact(args []string, stdout, stderr io.Writer) int {
	flags := newArtifactFlags("artifact approve", stderr)
	reason := flags.set.String("reason", "", "how this approval was given and what it covered; required")
	if code, ok := flags.parse(args, 1); !ok {
		return code
	}
	store, policy, code := flags.store(stderr)
	if code != 0 {
		return code
	}
	approved, err := store.Approve(flags.id(), *reason, time.Now())
	if err != nil {
		return reportArtifactError(stdout, stderr, *flags.jsonOutput, err)
	}
	listed := []artifact.Artifact{approved}
	if *flags.jsonOutput {
		return writeJSON(stdout, stderr, artifactOutput{Artifacts: listed, Approvals: artifactApprovals(listed, policy)})
	}
	fmt.Fprintf(stdout, "%s [%s, %s] %s\n", approved.ID, approved.Kind, approved.Status, approved.Title)
	fmt.Fprintf(stdout, "file: %s\n", approved.Path)
	fmt.Fprintf(stdout, "approval: %s\n", renderArtifactApproval(approved, policy))
	// Said every time, because the one way this could quietly become something
	// else is somebody reading an approval as a change to the document or as a
	// gate that has now opened. It is neither.
	fmt.Fprintln(stdout, "recorded in the document's frontmatter; nothing the document says changed, and no gate moved")
	return 0
}

// artifactFlags is the flag set every artifact command shares: which
// configuration to read the homes from, and how to report the result.
type artifactFlags struct {
	set        *flag.FlagSet
	name       string
	configPath *string
	jsonOutput *bool
	// args are the positional arguments, collected by parse rather than read off
	// the flag set, because the flags may come after them.
	args []string
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

// parse reads the flags and the positional arguments, in whatever order they
// were typed. Go's flag package stops at the first word that is not a flag, so
// `artifact approve brief --reason ...` would otherwise arrive as three
// positional arguments and be refused for naming three artifacts — and an id
// before the flags that describe what is being done to it is how anybody types
// it.
func (f *artifactFlags) parse(args []string, positional int) (int, bool) {
	remaining := args
	for {
		if err := f.set.Parse(remaining); err != nil {
			return 2, false
		}
		if f.set.NArg() == 0 {
			break
		}
		f.args = append(f.args, f.set.Arg(0))
		remaining = f.set.Args()[1:]
	}
	if len(f.args) != positional {
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

// id is the artifact a command was given, for the commands that take one.
func (f *artifactFlags) id() string {
	if len(f.args) == 0 {
		return ""
	}
	return f.args[0]
}

// store resolves the artifact homes the same way every other command resolves
// the repository: relative to the project rather than to the .yoyodyne
// directory the configuration happens to live in.
func (f *artifactFlags) store(stderr io.Writer) (artifact.Store, artifact.Policy, int) {
	resolved, err := loadConfiguration(*f.configPath)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return artifact.Store{}, artifact.Policy{}, 1
	}
	repository, err := resolvePath(config.ProjectDirectory(resolved.Path), resolved.Config.Product.Repository)
	if err != nil {
		fmt.Fprintf(stderr, "resolve product repository: %v\n", err)
		return artifact.Store{}, artifact.Policy{}, 1
	}
	return artifactStore(repository, resolved.Config.Product), artifactPolicy(resolved.Config.Approvals), 0
}

// artifactPolicy is the approvals configuration in the terms the artifact
// package thinks in. What requires the operator's approval is decided by the
// project's configuration rather than by the kind of document: a project that
// says its designs need approving gets that, and one that says its goals do not
// is told so rather than nagged.
func artifactPolicy(approvals config.Approvals) artifact.Policy {
	return artifact.Policy{Brief: approvals.Brief, Goals: approvals.Goals, Designs: approvals.Designs}
}

// artifactStore is how the configured directories become a store, in one place
// rather than at each call site: the three artifact homes, and the invariants
// directory excluded from them because its files carry the identity scheme this
// one was modeled on rather than this one.
func artifactStore(repositoryRoot string, product config.Product) artifact.Store {
	return artifact.StoreFor(repositoryRoot, product)
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

// renderArtifactApproval says what is recorded about one document's approval,
// and what this project asked for. A document nobody approved is reported
// differently depending on whether anything wanted it approved: an unapproved
// brief is something for the operator to do, and an unapproved design in a
// project whose designs are automatic is nothing at all.
func renderArtifactApproval(recorded artifact.Artifact, policy artifact.Policy) string {
	setting, mode, governed := policy.Setting(recorded.Kind)
	latest, approved := recorded.LatestApproval()
	if approved {
		given := fmt.Sprintf("given by the %s %s, for revision %d",
			latest.By, latest.At.UTC().Format(time.RFC3339), latest.Revision)
		if recorded.ApprovalState() == artifact.ApprovalApproved {
			return "approved as it stands, " + given
		}
		return fmt.Sprintf("approved and amended since — %s, and %s recorded after it, so the document as it now reads is not what was approved",
			given, laterRevisions(recorded.RevisionsSinceApproval()))
	}
	switch {
	case policy.Requires(recorded.Kind):
		return fmt.Sprintf("none recorded, and %s is %s, so this document is yours to approve", setting, mode)
	case governed:
		return fmt.Sprintf("none recorded; %s is %s, so none is asked for", setting, mode)
	default:
		return fmt.Sprintf("none recorded; no approval setting governs a %s artifact", recorded.Kind)
	}
}

func laterRevisions(count int) string {
	if count == 1 {
		return "one revision was"
	}
	return fmt.Sprintf("%d revisions were", count)
}

// artifactApprovals is the same reading as renderArtifactApproval, for a
// machine: the state, what the configuration asked for, and the approval itself.
func artifactApprovals(artifacts []artifact.Artifact, policy artifact.Policy) map[string]artifactApproval {
	approvals := make(map[string]artifactApproval, len(artifacts))
	for _, recorded := range artifacts {
		setting, mode, governed := policy.Setting(recorded.Kind)
		reported := artifactApproval{
			State:                  recorded.ApprovalState(),
			Required:               policy.Requires(recorded.Kind),
			RevisionsSinceApproval: recorded.RevisionsSinceApproval(),
		}
		if governed {
			reported.Setting, reported.Mode = setting, string(mode)
		}
		if latest, approved := recorded.LatestApproval(); approved {
			reported.Approval = &latest
		}
		approvals[recorded.ID] = reported
	}
	return approvals
}

func printArtifactUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo artifact <list|show|approve> [options]

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

Your approval of one of these documents is recorded in the same frontmatter,
against the revision it was given for, so a document amended after you approved
it reads as approved-and-amended-since rather than as approved. What needs your
approval is your configuration's to say: approvals.brief and approvals.goals
default to human, approvals.designs to automatic, and a decision record is the
architect's account of a decision rather than a statement of intent, so nothing
asks you to approve one.

An unapproved document still loads, still governs what is downstream of it, and
stops nothing that reads it; approving writes nothing but the approval, and the
document itself stays the owning role's to change. What your approval of the
goals decides is what reaches the work queue: under approvals.work_items:
automatic, work that traces to a goal an approved goals document states is
admitted without asking you, and anything else is still put to you.

  list [--kind <kind>]   list the recorded artifacts, and name what is not one
  show <id>              print one artifact and its recorded revisions
  approve <id>           record your approval of a document as it now stands

Options:
  --config <path>       configuration file (default: the nearest .yoyodyne/config.yaml)
  --json                emit machine-readable JSON

list options:
  --kind <kind>         brief, goals, non-goals, design, specification, or decision

approve options:
  --reason <text>       how the approval was given and what it covered; required`)
}
