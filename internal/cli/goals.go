package cli

// The goals a repository records, and what the admitted work says it is for.
//
// There is no command here that attributes anything, and that is the same
// decision the artifact commands make about writing artifacts: what a piece of
// work is for is a product judgement, owned by the product manager and made in
// the conversation where the operator can see it. What the harness owns is
// reading the goals, resolving what an item names against them, and saying
// which items are attributed to what — which is what this reports.
//
// One command here does write, and it is the same boundary rather than an
// exception to it: witnessing copies the goal an item's own notes already state
// into the tracker's metadata, where replacing those notes cannot reach it. It
// decides nothing about any work, which is precisely why it can run over a
// backlog somebody else attributed.

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/goal"
)

// goalsTrackerTimeout bounds one tracker command taken to read the admitted
// work, the same way every other command bounds its own: a tracker that has
// gone slow costs this report rather than hanging it.
const goalsTrackerTimeout = 30 * time.Second

type goalsOutput struct {
	Goals []goal.Goal `json:"goals,omitempty"`
	// BriefGoals are what the goals above link upward to, listed so a reader
	// resolving a link by hand has both ends of it.
	BriefGoals   []goal.BriefGoal   `json:"brief_goals,omitempty"`
	Problems     []goal.Problem     `json:"problems,omitempty"`
	LinkProblems []goal.LinkProblem `json:"link_problems,omitempty"`
	WrapProblems []goal.WrapProblem `json:"wrap_problems,omitempty"`
	// Attributions are the admitted work items and what each says it is for.
	Attributions []itemAttribution `json:"attributions,omitempty"`
	// Witnessed are the items a sweep recorded a witness on, and the goal each
	// one's own notes stated.
	Witnessed []itemWitness `json:"witnessed,omitempty"`
	Error     string        `json:"error,omitempty"`
}

// itemAttribution is one admitted work item and the goal it serves, as the
// harness resolves it.
type itemAttribution struct {
	WorkItemID  string           `json:"work_item_id"`
	Title       string           `json:"title"`
	Status      string           `json:"status"`
	Priority    int              `json:"priority"`
	Attribution goal.Attribution `json:"attribution"`
}

// itemWitness is one item a sweep witnessed, and what it witnessed. Failure is
// carried per item rather than stopping the sweep: a tracker that refused one
// write has not made the other items less worth protecting.
type itemWitness struct {
	WorkItemID string `json:"work_item_id"`
	Goal       string `json:"goal"`
	Failure    string `json:"failure,omitempty"`
}

func runGoals(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printGoalsUsage(stdout)
		return 0
	}
	switch args[0] {
	case "list":
		return listGoals(args[1:], stdout, stderr)
	case "attribution":
		return reportAttribution(ctx, args[1:], stdout, stderr)
	case "witness":
		return witnessRecordedGoals(ctx, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown goals command %q\n\n", args[0])
		printGoalsUsage(stderr)
		return 2
	}
}

// listGoals prints the goals work may be attributed to. A goals document whose
// goals could not be read is named on stderr rather than leaving the listing
// looking like the whole of what the product intends.
func listGoals(args []string, stdout, stderr io.Writer) int {
	flags := newGoalsFlags("goals list", stderr)
	if code, ok := flags.parse(args); !ok {
		return code
	}
	goals, err := flags.goals()
	if err != nil {
		return reportGoalsError(stdout, stderr, *flags.jsonOutput, err)
	}
	if *flags.jsonOutput {
		return writeJSON(stdout, stderr, goalsOutput{
			Goals:        goals.Goals,
			BriefGoals:   goals.BriefGoals,
			Problems:     goals.Problems,
			LinkProblems: goals.LinkProblems,
			WrapProblems: goals.WrapProblems,
		})
	}
	if reason, uncheckable := goals.Uncheckable(); uncheckable {
		fmt.Fprintf(stdout, "no goal is in force: %s\n", reason)
	}
	for _, recorded := range goals.Goals {
		state := ""
		if !recorded.InForce {
			// A goal in a document no longer in force is listed and marked, because
			// it is what an old attribution resolves to and reading it as a goal
			// somebody could still name would be wrong.
			state = " [no longer in force]"
		}
		fmt.Fprintf(stdout, "%s%s\n", recorded.Statement, state)
		fmt.Fprintf(stdout, "  stated by: %s (%s)\n", recorded.ArtifactID, recorded.Path)
		if recorded.Supports != "" {
			fmt.Fprintf(stdout, "  supports: %s\n", recorded.Supports)
		}
	}
	printGoalsProblems(stderr, goals)
	return 0
}

// reportAttribution says what the admitted work is for: which items name a goal
// the goals state, which name none, which name something they do not state, and
// which recorded one and lost it.
//
// The exit status separates the three ways an item is not attributed, because
// they are not the same finding. Work admitted before goals were checked names
// none, is grandfathered, and reporting it as a failure would fail a backlog
// nobody has had the chance to attribute yet. An item naming a goal the goals do
// not state is a claim that is wrong, and an item whose recorded goal was written
// over is a record that was destroyed; those two are what this exits non-zero
// for.
func reportAttribution(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := newGoalsFlags("goals attribution", stderr)
	if code, ok := flags.parse(args); !ok {
		return code
	}
	parts, err := buildComponents(*flags.configPath)
	if err != nil {
		return reportGoalsError(stdout, stderr, *flags.jsonOutput, err)
	}
	goals, err := loadGoals(parts.repository, parts.config.Product)
	if err != nil {
		// The goals could not be read, so nothing here is a judgement about any
		// item. It is still reported against the queue, because "nothing checked
		// this" is the answer and an empty report is not.
		fmt.Fprintf(stderr, "warning: %v\n", err)
	}
	admitted, err := admittedWorkItems(ctx, parts.tracker())
	if err != nil {
		return reportGoalsError(stdout, stderr, *flags.jsonOutput, err)
	}

	attributions := attributionsOf(admitted, goals)
	if *flags.jsonOutput {
		if code := writeJSON(stdout, stderr, goalsOutput{
			Attributions: attributions,
			Problems:     goals.Problems,
			BriefGoals:   goals.BriefGoals,
			LinkProblems: goals.LinkProblems,
			WrapProblems: goals.WrapProblems,
		}); code != 0 {
			return code
		}
	} else {
		printAttributions(stdout, attributions, goals)
		printGoalsProblems(stderr, goals)
	}
	return attributionExitCode(attributions)
}

// witnessRecordedGoals records, on every admitted item whose notes state a goal
// and which the tracker holds no witness for, the goal those notes state. It is
// what closes the gap the witness would otherwise leave: an attribution written
// before the witness existed is protected by nothing, and replacing its notes
// tomorrow would leave it reading as work nobody ever attributed.
//
// It writes no attribution, which is why it is here at all. What it stores is
// what the item already says about itself, copied to where a careless writer
// cannot reach it — no judgement about what any work is for, and nothing the
// product manager did not already record in the conversation.
//
// One item's failure does not end the sweep. A tracker that refused one write
// has not made the rest less worth protecting, and every failure is named.
func witnessRecordedGoals(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := newGoalsFlags("goals witness", stderr)
	if code, ok := flags.parse(args); !ok {
		return code
	}
	parts, err := buildComponents(*flags.configPath)
	if err != nil {
		return reportGoalsError(stdout, stderr, *flags.jsonOutput, err)
	}
	tracker := parts.tracker()
	admitted, err := admittedWorkItems(ctx, tracker)
	if err != nil {
		return reportGoalsError(stdout, stderr, *flags.jsonOutput, err)
	}

	witnessed, failures := recordGoalWitnesses(ctx, tracker, admitted)
	if *flags.jsonOutput {
		if code := writeJSON(stdout, stderr, goalsOutput{Witnessed: witnessed}); code != 0 {
			return code
		}
	} else {
		printWitnessed(stdout, len(admitted), witnessed)
	}
	if failures > 0 {
		return 1
	}
	return 0
}

// recordGoalWitnesses is the sweep itself: it writes a witness for every item
// whose notes state a goal and which carries none, and reports what it wrote
// and how many writes the tracker refused.
func recordGoalWitnesses(ctx context.Context, tracker beads.Client, admitted []beads.WorkItem) ([]itemWitness, int) {
	var witnessed []itemWitness
	failures := 0
	for _, item := range admitted {
		statement, records := goal.NamedIn(item.Notes)
		// An item stating no goal is left alone: there is nothing to witness, and a
		// witness written over one would turn work nobody has attributed yet into
		// work that reads as having lost an attribution it never had. One already
		// witnessed is left alone too — the witness records what was written, and
		// rewriting it every sweep would be a write per item for no fact gained.
		if !records || item.GoalWitness.Recorded {
			continue
		}
		recorded := itemWitness{WorkItemID: item.ID, Goal: statement}
		writeCtx, cancel := context.WithTimeout(ctx, goalsTrackerTimeout)
		_, err := tracker.RecordGoalWitness(writeCtx, item.ID, statement)
		cancel()
		if err != nil {
			recorded.Failure = err.Error()
			failures++
		}
		witnessed = append(witnessed, recorded)
	}
	return witnessed, failures
}

func printWitnessed(stdout io.Writer, admitted int, witnessed []itemWitness) {
	fmt.Fprintf(stdout, "%d admitted item(s): %d newly witnessed, %d already witnessed or recording no goal\n",
		admitted, len(witnessed), admitted-len(witnessed))
	for _, entry := range witnessed {
		if entry.Failure != "" {
			fmt.Fprintf(stdout, "  %s could not be witnessed: %s\n", entry.WorkItemID, entry.Failure)
			continue
		}
		fmt.Fprintf(stdout, "  %s witnessed: %s\n", entry.WorkItemID, entry.Goal)
	}
}

// attributionsOf judges every admitted item against the goals: what the item
// records, and what the tracker witnesses was once recorded on it. It is one
// function rather than a loop inside the command so that what the audit reports
// is exactly what anything else asking the same question gets.
func attributionsOf(admitted []beads.WorkItem, goals goal.Set) []itemAttribution {
	attributions := make([]itemAttribution, 0, len(admitted))
	for _, item := range admitted {
		attributions = append(attributions, itemAttribution{
			WorkItemID:  item.ID,
			Title:       item.Title,
			Status:      item.Status,
			Priority:    item.Priority,
			Attribution: goals.AttributionOf(item.Notes, item.GoalWitness),
		})
	}
	return attributions
}

// attributionExitCode is the status the audit exits with: non-zero for an item
// whose attribution is wrong or was destroyed, and zero for one that never had
// one. The rule is here rather than inline because it is the grandfathering
// decision itself, and a rule that quietly started failing legacy items would
// stop a backlog nobody has had the chance to attribute.
//
// A lost attribution fails for the opposite reason to the one grandfathering
// exists for: the item passed the check, and what is wrong is that the record of
// it was overwritten. Reporting that without failing is how six items stayed
// orphaned for as long as it took somebody to read the report by eye.
func attributionExitCode(attributions []itemAttribution) int {
	for _, entry := range attributions {
		if entry.Attribution.State == goal.StateUnresolved || entry.Attribution.State == goal.StateLost {
			return 1
		}
	}
	return 0
}

// admittedWorkItems is the work that has been admitted and is not finished,
// assembled from the same tracker slices the backlog is.
func admittedWorkItems(ctx context.Context, tracker beads.Client) ([]beads.WorkItem, error) {
	var admitted []beads.WorkItem
	for _, status := range backlogStatuses {
		listCtx, cancel := context.WithTimeout(ctx, goalsTrackerTimeout)
		items, err := tracker.List(listCtx, status)
		cancel()
		if err != nil {
			return nil, fmt.Errorf("list %s work items: %w", status, err)
		}
		admitted = append(admitted, items...)
	}
	backlog.Sort(admitted)
	return admitted, nil
}

func printAttributions(stdout io.Writer, attributions []itemAttribution, goals goal.Set) {
	if len(attributions) == 0 {
		fmt.Fprintln(stdout, "no work is admitted, so there is nothing to attribute")
		return
	}
	if reason, uncheckable := goals.Uncheckable(); uncheckable {
		// Nothing was checked, so no count is reported. A tally against goals
		// nobody could read would say the queue traces to intent that was never
		// consulted.
		fmt.Fprintf(stdout, "%d admitted item(s), none of them checked: %s\n", len(attributions), reason)
		// An attribution that was destroyed is still said, because saying it needs
		// no goals document: what it rests on is that the tracker witnesses a goal
		// was written and the item no longer carries one. It is also what this
		// exits non-zero for, and an unexplained failure is worse than none.
		for _, entry := range attributions {
			if entry.Attribution.State == goal.StateLost {
				fmt.Fprintf(stdout, "  %s [p%d, %s] %s\n    %s\n",
					entry.WorkItemID, entry.Priority, entry.Status, entry.Title, entry.Attribution.Reason)
			}
		}
		return
	}
	grouped := map[goal.State][]itemAttribution{}
	for _, entry := range attributions {
		grouped[entry.Attribution.State] = append(grouped[entry.Attribution.State], entry)
	}
	fmt.Fprintf(stdout, "%d admitted item(s): %d serve a recorded goal, %d name none, %d name a goal the goals do not state, %d lost the goal they recorded\n",
		len(attributions), len(grouped[goal.StateAttributed]), len(grouped[goal.StateUnattributed]),
		len(grouped[goal.StateUnresolved]), len(grouped[goal.StateLost]))
	for _, group := range []struct {
		state  goal.State
		saying string
	}{
		// The destroyed attributions are listed first, above the wrong ones. They
		// are the only group that got there without anybody deciding anything, and
		// the only one where the answer may already be known: each item's own line
		// says whether the tracker kept the words to put back.
		{goal.StateLost, "having recorded a goal and lost it, which is a record destroyed rather than a judgement nobody made"},
		{goal.StateUnresolved, "naming a goal no goals document states, which is a claim to correct"},
		{goal.StateUnattributed, "naming no goal, which is what work admitted before goals were checked looks like"},
		{goal.StateAttributed, "serving a recorded goal"},
	} {
		entries := grouped[group.state]
		if len(entries) == 0 {
			continue
		}
		fmt.Fprintf(stdout, "\n%s:\n", group.saying)
		for _, entry := range entries {
			fmt.Fprintf(stdout, "  %s [p%d, %s] %s\n", entry.WorkItemID, entry.Priority, entry.Status, entry.Title)
			fmt.Fprintf(stdout, "    %s\n", attributionDetail(entry.Attribution))
		}
	}
}

// attributionDetail is the one line under an item that says what its
// attribution amounts to: the goal it resolved to, or what is wrong with it.
func attributionDetail(attribution goal.Attribution) string {
	if attribution.Resolved() {
		return fmt.Sprintf("%s (%s)", attribution.Goal.Statement, attribution.Goal.ArtifactID)
	}
	return attribution.Reason
}

func printGoalsProblems(stderr io.Writer, goals goal.Set) {
	for _, problem := range goals.Problems {
		fmt.Fprintf(stderr, "goals not read: %s\n", problem)
	}
	// A broken link upstream is reported beside the goals rather than instead of
	// them: the goal is still stated and work can still be attributed to it, and
	// what is wrong is the chain above it.
	for _, problem := range goals.LinkProblems {
		fmt.Fprintf(stderr, "goal not linked to the brief: %s\n", problem)
	}
	// A hard-wrapped goal is reported for the same reason and in the same place:
	// the statement was rejoined and work can be attributed to it, and what is
	// wrong is that the goal the file records is the rejoining rather than
	// anything the file says outright.
	for _, problem := range goals.WrapProblems {
		fmt.Fprintf(stderr, "goal not written on one line: %s\n", problem)
	}
}

// loadGoals reads the goals the repository records, through the same artifact
// homes every other command reads artifacts from. A failure returns a set that
// says why rather than an empty one: a repository whose goals could not be read
// and one that records none lead to opposite conclusions about the same
// attribution, and a caller that carried on with an empty set would report the
// wrong one.
func loadGoals(repository string, product config.Product) (goal.Set, error) {
	artifacts, err := artifactStore(repository, product).Load()
	if err != nil {
		return goal.Unreadable(err.Error()), fmt.Errorf("read the recorded goals: %w", err)
	}
	return goal.Collect(repository, artifacts), nil
}

// goalsFlags is the flag set every goals command shares.
type goalsFlags struct {
	set        *flag.FlagSet
	name       string
	configPath *string
	jsonOutput *bool
}

func newGoalsFlags(name string, stderr io.Writer) *goalsFlags {
	set := flag.NewFlagSet(name, flag.ContinueOnError)
	set.SetOutput(stderr)
	return &goalsFlags{
		set:        set,
		name:       name,
		configPath: set.String("config", "", "configuration file path (default: the nearest project configuration)"),
		jsonOutput: set.Bool("json", false, "emit machine-readable JSON"),
	}
}

func (f *goalsFlags) parse(args []string) (int, bool) {
	if err := f.set.Parse(args); err != nil {
		return 2, false
	}
	if f.set.NArg() != 0 {
		fmt.Fprintf(f.set.Output(), "%s does not accept positional arguments\n", f.name)
		printGoalsUsage(f.set.Output())
		return 2, false
	}
	return 0, true
}

// goals resolves the recorded goals the same way the artifact commands resolve
// the homes they read: relative to the project rather than to the .yoyodyne
// directory the configuration happens to live in.
func (f *goalsFlags) goals() (goal.Set, error) {
	resolved, err := loadConfiguration(*f.configPath)
	if err != nil {
		return goal.Set{}, err
	}
	repository, err := resolvePath(config.ProjectDirectory(resolved.Path), resolved.Config.Product.Repository)
	if err != nil {
		return goal.Set{}, fmt.Errorf("resolve product repository: %w", err)
	}
	return loadGoals(repository, resolved.Config.Product)
}

func reportGoalsError(stdout, stderr io.Writer, jsonOutput bool, err error) int {
	if jsonOutput {
		if code := writeJSON(stdout, stderr, goalsOutput{Error: err.Error()}); code != 0 {
			return code
		}
		return 1
	}
	fmt.Fprintln(stderr, err)
	return 1
}

func printGoalsUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo goals <list|attribution|witness> [options]

The goals the repository records, read from the goals artifacts themselves: each
statement under a goals document's `+"`Goals`"+` heading is a goal that work can be
attributed to, and an attribution resolves by naming one of them in the words
that document states it in.

Nothing here writes an attribution. What a piece of work is for is the product
manager's judgement, made in the conversation where the operator can see it;
what the harness owns is resolving what an item names and saying what it found.

  list          the goals work may be attributed to, and where each is stated
  attribution   what each admitted work item says it is for
  witness       record, where a careless writer cannot reach it, the goal each
                admitted item's notes already state

An item admitted before goals were checked names none. That is reported and is
not a failure: it is somebody's to attribute, and nothing refuses to run it.
"attribution" exits non-zero for an item naming a goal no goals document states,
which is a claim that is wrong rather than one nobody has made, and for an item
the tracker witnesses recorded a goal and no longer carries one, which is a
record something wrote over rather than a judgement nobody made.

"witness" is the one command here that writes, and it writes no attribution: the
goal it stores is the one the item's own notes state, copied into the tracker's
metadata so that replacing those notes is a loss "attribution" can report rather
than one it cannot see. An attribution made before this existed carries no
witness until it is swept, which is why it is worth running once over a backlog.

Options:
  --config <path>       configuration file (default: the nearest .yoyodyne/config.yaml)
  --json                emit machine-readable JSON`)
}
