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
//
// One command here refuses, and it is the same boundary again seen from in
// front: guarding reads a shell command an agent session is about to run and
// stops the one that would replace an item's notes without carrying its goal
// through. It judges no work either -- what it knows is what the command would
// destroy, which is a fact about the command and not about the item.

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/backlog"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/console"
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

func runGoals(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
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
	case "guard":
		// The tool call being decided arrives on stdin, which is why this is the
		// one goals command bound to the process's own input.
		return guardNotesReplacement(args[1:], stdin, stdout, stderr)
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
	printGoals(stdout, console.ThemeFor(stdout, os.Getenv), goals)
	printGoalsProblems(stderr, goals)
	return 0
}

// printGoals is the listing itself, laid out to be read rather than parsed: one
// goal to an entry, with a blank line between entries, its statement weighted
// and the lines about it slanted and indented under it.
//
// The dressing is an addition and never the meaning, which is the discipline the
// whole theme holds to. What separates one goal from the next is the blank line,
// what says a line is about the goal above it is the indent and its label, and
// what says a goal is no longer something work may name is the marker in words —
// so this listing piped to a file, read where NO_COLOR is set, or shown on a
// terminal that says it is dumb says everything it says on a terminal that can
// be dressed. `--json` carries none of it.
func printGoals(stdout io.Writer, theme console.Theme, goals goal.Set) {
	if reason, uncheckable := goals.Uncheckable(); uncheckable {
		fmt.Fprintf(stdout, "no goal is in force: %s\n", reason)
	}
	for index, recorded := range goals.Goals {
		if index > 0 {
			fmt.Fprintln(stdout)
		}
		state := ""
		if !recorded.InForce {
			// A goal in a document no longer in force is listed and marked, because
			// it is what an old attribution resolves to and reading it as a goal
			// somebody could still name would be wrong.
			state = " [no longer in force]"
		}
		fmt.Fprint(stdout, theme.Entry(fmt.Sprintf("%s%s\n", recorded.Statement, state)))
		fmt.Fprint(stdout, theme.Detail(fmt.Sprintf("  stated by: %s (%s)\n", recorded.ArtifactID, recorded.Path)))
		if recorded.Supports != "" {
			fmt.Fprint(stdout, theme.Detail(fmt.Sprintf("  supports: %s\n", recorded.Supports)))
		}
	}
	printGoalsUpstream(stdout, theme, goals)
}

// printGoalsUpstream closes the listing by saying where the chain goes above it:
// these goals are not the top of it, and the brief they support is a file the
// reader can open. It is what a reader who took this listing for the whole of
// the product's intent was missing, and it is printed whether or not any goal
// was listed — somebody shown no goals at all is the reader most in need of
// being sent upstream.
//
// The brief is named from the artifacts rather than assumed, because where a
// product keeps its brief is that product's to decide. A repository recording
// none is told what these goals support without being sent to a file that is not
// there; what is wrong there is reported on stderr with the other broken links.
func printGoalsUpstream(stdout io.Writer, theme console.Theme, goals goal.Set) {
	line := "upstream: these goals support the goals the product brief states"
	if goals.BriefPath != "" {
		line += ", in " + goals.BriefPath
	}
	fmt.Fprintln(stdout)
	fmt.Fprint(stdout, theme.Detail(line+"\n"))
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

// witnessRecordedGoals records, on every work item whose notes state a goal and
// which the tracker holds no witness for, the goal those notes state. It is
// what closes the gap the witness would otherwise leave: an attribution written
// before the witness existed is protected by nothing, and replacing its notes
// tomorrow would leave it reading as work nobody ever attributed.
//
// It walks every status rather than the backlog -- see witnessStatuses for why
// the item somebody is working on and the item somebody closed are exactly the
// ones a backlog-scoped sweep would have left out.
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
	swept, err := workItemsWithStatus(ctx, tracker, witnessStatuses)
	if err != nil {
		return reportGoalsError(stdout, stderr, *flags.jsonOutput, err)
	}

	witnessed, failures := recordGoalWitnesses(ctx, tracker, swept)
	if *flags.jsonOutput {
		if code := writeJSON(stdout, stderr, goalsOutput{Witnessed: witnessed}); code != 0 {
			return code
		}
	} else {
		printWitnessed(stdout, len(swept), witnessed)
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

// maxToolCallBytes bounds the tool call read from stdin. It is generous because
// a replacement's notes are prose and a command line can carry a lot of it, and
// it is bounded at all for the reason every other read here is: a hook is given
// whatever the session hands it, and a guard that could be made to hold a
// session's whole memory is a guard that becomes the failure.
const maxToolCallBytes = 1 << 20

// toolCall is the tool invocation a Claude Code `PreToolUse` hook is given on
// stdin. Only the two fields this decides on are read; everything else the hook
// carries belongs to whoever put it there.
type toolCall struct {
	ToolName  string `json:"tool_name"`
	ToolInput struct {
		Command string `json:"command"`
	} `json:"tool_input"`
}

// hookDecision is the answer a `PreToolUse` hook gives back, in the shape the
// hook protocol reads it in. Nothing is printed to allow a call: silence is how
// the protocol spells "this hook has no opinion", and it is what nearly every
// command an agent runs gets.
type hookDecision struct {
	Output hookDecisionOutput `json:"hookSpecificOutput"`
}

type hookDecisionOutput struct {
	EventName string `json:"hookEventName"`
	Decision  string `json:"permissionDecision"`
	Reason    string `json:"permissionDecisionReason"`
}

// guardNotesReplacement decides one shell command an agent session is about to
// run: it refuses `bd update --notes` where the replacement would destroy the
// goal the item records, and says nothing about anything else.
//
// It is deliberately the only thing here that never fails a session. A tool call
// this cannot read is allowed, and said so on stderr: this stands in front of
// every shell command an agent takes, so refusing what it cannot parse would
// turn a hook payload it did not recognise into a session that can run no
// commands at all. That is the guard being the outage instead of preventing one.
//
// What makes fail-open honest is that this is not the only protection. An
// attribution the tracker witnesses cannot be destroyed quietly whatever writes
// over the notes -- `goals attribution` reports the loss and exits non-zero.
// This is the writer stopped before the fact; the witness is the loss that
// cannot be hidden after it.
func guardNotesReplacement(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("goals guard", flag.ContinueOnError)
	flags.SetOutput(stderr)
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "goals guard does not accept positional arguments; the tool call it decides is read from stdin")
		return 2
	}
	payload, err := io.ReadAll(io.LimitReader(stdin, maxToolCallBytes))
	if err != nil {
		fmt.Fprintf(stderr, "goals guard read the tool call: %v; the command was allowed unexamined\n", err)
		return 0
	}
	var call toolCall
	if err := json.Unmarshal(payload, &call); err != nil {
		fmt.Fprintf(stderr, "goals guard could not read the tool call: %v; the command was allowed unexamined\n", err)
		return 0
	}
	// Only a shell command can carry the writer. Every other tool reaches the
	// tracker through the harness, which appends rather than replaces.
	if call.ToolName != "Bash" {
		return 0
	}
	destroyed := beads.DestroyedAttribution(call.ToolInput.Command)
	if destroyed == "" {
		return 0
	}
	return writeJSON(stdout, stderr, hookDecision{Output: hookDecisionOutput{
		EventName: "PreToolUse",
		Decision:  "deny",
		Reason:    destroyed,
	}})
}

func printWitnessed(stdout io.Writer, swept int, witnessed []itemWitness) {
	fmt.Fprintf(stdout, "%d work item(s): %d newly witnessed, %d already witnessed or recording no goal\n",
		swept, len(witnessed), swept-len(witnessed))
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

// witnessStatuses are the tracker slices the sweep covers: every status an item
// can be in, rather than the backlog's two.
//
// The scope is wider than the backlog's deliberately, and the difference is the
// whole value of sweeping at all. What destroys an attribution is a command
// somebody types, and it reaches a claimed or closed item exactly as easily as a
// queued one -- of the twelve recorded losses, one was on an item somebody was
// working on and nine were on closed items, several of them written over after
// the item closed. A sweep scoped to the backlog would protect the two slices
// the loss has been least likely to happen in and leave the record of finished
// work, which is the traceability the chain is kept for, covered by nothing.
var witnessStatuses = []string{"open", "in_progress", "blocked", "closed"}

// admittedWorkItems is the work that has been admitted and is not finished,
// assembled from the same tracker slices the backlog is.
func admittedWorkItems(ctx context.Context, tracker beads.Client) ([]beads.WorkItem, error) {
	return workItemsWithStatus(ctx, tracker, backlogStatuses)
}

// workItemsWithStatus reads the tracker one status at a time, because that is
// what the tracker offers, and orders what it read the way the backlog is
// ordered so a report and a sweep walk the same items in the same order.
func workItemsWithStatus(ctx context.Context, tracker beads.Client, statuses []string) ([]beads.WorkItem, error) {
	var read []beads.WorkItem
	for _, status := range statuses {
		listCtx, cancel := context.WithTimeout(ctx, goalsTrackerTimeout)
		items, err := tracker.List(listCtx, status)
		cancel()
		if err != nil {
			return nil, fmt.Errorf("list %s work items: %w", status, err)
		}
		read = append(read, items...)
	}
	backlog.Sort(read)
	return read, nil
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
	fmt.Fprintln(writer, `Usage: yoyo goals <list|attribution|witness|guard> [options]

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
                work item's notes already state
  guard         refuse a shell command that would replace an item's notes and
                destroy the goal recorded in them

"list" is laid out to be read: one goal to an entry, a blank line between
entries, and where the goal is stated indented under it, closing with a line
naming what these goals sit underneath — the goals the product brief states, and
the file to open for them. On a terminal the statement is bold and the lines
about it italic; the emphasis is an addition and nothing else, so a listing piped
to a file, read where `+"`NO_COLOR`"+` is set, or shown on a terminal that says it is
dumb says exactly what it says dressed. `+"`--json`"+` carries none of it.

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
It sweeps every work item the tracker holds and not only the queue: the command
that destroys an attribution reaches a claimed or a closed item just as easily,
and most of the losses on record were on closed ones.

"guard" is the same loss stopped before it happens. It reads a `+"`PreToolUse`"+` tool
call on stdin, as an agent session's hook gives it, and refuses a shell command
running `+"`bd update <id> --notes`"+` -- which replaces an item's notes rather than
adding to them, taking the recorded goal with them. A replacement that carries
the `+"`Goal served:`"+` line through is allowed, because nothing is destroyed by one.
It takes no options, prints nothing at all to allow a command, and allows a tool
call it cannot read rather than failing a session over a payload it did not
recognise.

Options:
  --config <path>       configuration file (default: the nearest .yoyodyne/config.yaml)
  --json                emit machine-readable JSON`)
}
