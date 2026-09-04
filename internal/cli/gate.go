package cli

// Recording that a person took the step a gate reserved for them, and reading
// which gates are still waiting.
//
// This is the one door into the record, and its being the only one is the whole
// feature. A human gate is declared on the work it holds — a work item, or a
// state in a workflow definition — and nothing the harness does ever passes one:
// no run, no outcome, no registered action, and above all no work item being
// closed. Closing an item is what machinery does, and an item closed by
// machinery passing a step the operator had reserved is what happened on
// 2026-09-04 and what this replaces.
//
// So `yoyo gate record` is not a convenience over some other way of doing it.
// It is the act. A person names the gate, says who they are, and says what they
// did, and that record is what the queue and the workflow executor read before
// anything proceeds.

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/humangate"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// gateEntry is one declared gate as the listing reports it: what it is, where it
// was declared, and the act that passed it if there is one.
type gateEntry struct {
	Name string `json:"name"`
	// Statement is what the declaration says the person has to do.
	Statement string `json:"statement"`
	// DeclaredBy is the work items that declare this gate. A gate declared by no
	// admitted item and already recorded is still listed, because an operator
	// asking what they have signed off deserves the answer.
	DeclaredBy []string `json:"declared_by,omitempty"`
	// Act is the recorded human act that passed it, if one exists.
	Act *runstate.HumanAct `json:"act,omitempty"`
}

// unreadableGate is one declaration that holds an item and that nobody can
// record an act against, because nothing could read it. It is listed beside the
// gates rather than dropped: the item is not pullable until its author fixes the
// line, and a listing that showed only the well-formed gates would leave them
// with a held item and no reason for it.
type unreadableGate struct {
	WorkItemID string `json:"work_item_id"`
	Problem    string `json:"problem"`
}

type gateOutput struct {
	Gates []gateEntry `json:"gates,omitempty"`
	// Unreadable is every declaration holding an item that nothing could read.
	Unreadable []unreadableGate `json:"unreadable,omitempty"`
	// Recorded is the act this invocation wrote, on `gate record` and nowhere
	// else.
	Recorded *runstate.HumanAct `json:"recorded,omitempty"`
	// Unread says the admitted work could not be listed, so an undischarged gate
	// may be missing from the listing. It is stated rather than reported as an
	// empty answer, which is the rule every listing here holds.
	Unread string `json:"unread,omitempty"`
	Error  string `json:"error,omitempty"`
}

func runGate(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printGateUsage(stdout)
		return 0
	}
	switch args[0] {
	case "list":
		return listGates(args[1:], stdout, stderr)
	case "record":
		return recordGate(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown gate command %q\n\n", args[0])
		printGateUsage(stderr)
		return 2
	}
}

// listGates says which steps are waiting on a person and which have been taken.
// It reads the admitted work for the declarations and the harness's own store
// for the acts, because those are the two halves and neither package holds both.
func listGates(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("gate list", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "gate list does not accept positional arguments")
		printGateUsage(stderr)
		return 2
	}

	parts, err := buildComponents(*configPath)
	if err != nil {
		return reportGateError(stdout, stderr, *jsonOutput, err)
	}
	acts, err := parts.store.HumanActs()
	if err != nil {
		return reportGateError(stdout, stderr, *jsonOutput, err)
	}
	recorded := make(map[string]runstate.HumanAct, len(acts))
	for _, act := range acts {
		recorded[act.Gate] = act
	}

	// The declarations come from the admitted work. A gate on an item that has
	// already left the backlog is not listed as outstanding, because there is no
	// longer anything for it to hold — what is listed instead is the act, if one
	// was recorded, so the operator's own signatures stay readable.
	declared := make(map[string]gateEntry)
	var unreadable []unreadableGate
	tracker := parts.tracker()
	unread := ""
	for _, status := range []string{"open", "blocked", "in_progress"} {
		items, err := listGateItems(tracker, status)
		if err != nil {
			// A tracker that will not answer costs the declarations and not the
			// acts. What a person has already recorded is theirs and is readable
			// without a tracker, and losing it because the work could not be listed
			// would be losing the answer this command exists to give.
			unread = err.Error()
			continue
		}
		unreadable = collectGates(items, declared, unreadable)
	}
	for name, act := range recorded {
		entry, seen := declared[name]
		if !seen {
			entry = gateEntry{Name: name, Statement: act.Statement}
		}
		stored := act
		entry.Act = &stored
		declared[name] = entry
	}

	entries := make([]gateEntry, 0, len(declared))
	for _, entry := range declared {
		sort.Strings(entry.DeclaredBy)
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	sort.Slice(unreadable, func(i, j int) bool {
		if unreadable[i].WorkItemID != unreadable[j].WorkItemID {
			return unreadable[i].WorkItemID < unreadable[j].WorkItemID
		}
		return unreadable[i].Problem < unreadable[j].Problem
	})

	if *jsonOutput {
		return writeJSON(stdout, stderr, gateOutput{Gates: entries, Unreadable: unreadable, Unread: unread})
	}
	if unread != "" {
		fmt.Fprintf(stdout, "the admitted work could not be listed, so a gate declared and not yet recorded is missing from this: %s\n", unread)
	}
	if len(entries) == 0 && len(unreadable) == 0 {
		fmt.Fprintln(stdout, "no work declares a step only a person can take, and no act is recorded")
		fmt.Fprintln(stdout, "an item declares one by naming it after `"+humangate.DeclareMarker+"` on its own line")
		return 0
	}
	for _, entry := range entries {
		if entry.Act != nil {
			fmt.Fprintf(stdout, "%s  passed by %s at %s\n", entry.Name, entry.Act.Person,
				entry.Act.RecordedAt.Format(time.RFC3339))
			fmt.Fprintf(stdout, "    they recorded: %s\n", entry.Act.Statement)
		} else {
			fmt.Fprintf(stdout, "%s  waiting on a person\n", entry.Name)
			fmt.Fprintf(stdout, "    what has to happen: %s\n", entry.Statement)
			fmt.Fprintf(stdout, "    record it with: yoyo gate record %s --by <you> --did \"<what you did>\"\n", entry.Name)
		}
		if len(entry.DeclaredBy) > 0 {
			fmt.Fprintf(stdout, "    declared by: %s\n", strings.Join(entry.DeclaredBy, ", "))
		}
	}
	for _, broken := range unreadable {
		fmt.Fprintf(stdout, "%s  declares a step nothing could read, and is held by it\n", broken.WorkItemID)
		fmt.Fprintf(stdout, "    %s\n", broken.Problem)
		fmt.Fprintln(stdout, "    no act records this one: correct the declaration on the item, and it becomes a gate somebody can pass")
	}
	return 0
}

// recordGate writes one person's act. Everything about it is deliberately
// explicit: who, and what they did. A gate passed by an unnamed somebody who
// said nothing is a flag, and a flag is what was already there.
func recordGate(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("gate record", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	by := flags.String("by", "", "who took the step (default: the operating-system user)")
	did := flags.String("did", "", "what you did, in your own words (required)")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	positional, err := parseArguments(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 1 {
		fmt.Fprintln(stderr, "gate record takes exactly one gate name")
		printGateUsage(stderr)
		return 2
	}
	gate := positional[0]
	if strings.TrimSpace(*did) == "" {
		fmt.Fprintln(stderr, "gate record requires --did: a gate passed with no account of what was done is a signature on a blank page")
		printGateUsage(stderr)
		return 2
	}
	person := strings.TrimSpace(*by)
	if person == "" {
		// The operating-system user is a person's name rather than a role's, which
		// is the whole requirement. It is a default and not an inference: --by
		// overrides it, and somebody recording on another's behalf says so there.
		person = os.Getenv("USER")
	}
	if person == "" {
		fmt.Fprintln(stderr, "gate record requires --by: the record says which person passed the gate, and nothing here could work out who you are")
		return 2
	}

	parts, err := buildComponents(*configPath)
	if err != nil {
		return reportGateError(stdout, stderr, *jsonOutput, err)
	}
	act := runstate.HumanAct{
		SchemaVersion: runstate.HumanActSchemaVersion,
		ProductID:     parts.config.Product.ID,
		Gate:          gate,
		Person:        person,
		Statement:     strings.TrimSpace(*did),
		RecordedAt:    time.Now().UTC(),
	}
	if err := parts.store.RecordHumanAct(act); err != nil {
		return reportGateError(stdout, stderr, *jsonOutput, err)
	}
	if *jsonOutput {
		return writeJSON(stdout, stderr, gateOutput{Recorded: &act})
	}
	fmt.Fprintf(stdout, "recorded that %s passed the gate %q at %s\n", act.Person, act.Gate,
		act.RecordedAt.Format(time.RFC3339))
	fmt.Fprintf(stdout, "what you recorded: %s\n", act.Statement)
	fmt.Fprintln(stdout, "work that was held by this gate is pullable at the next reading of the queue; a workflow standing at it steps when it is next stepped")
	fmt.Fprintln(stdout, "the record is not replaceable: it says who passed the gate, and a second write would change whose it was")
	return 0
}

// collectGates reads what one slice of work items says about steps only a
// person can take, adding to the gates found so far and returning the
// declarations nothing could read.
//
// The unreadable ones are collected rather than skipped because each is holding
// its item exactly as a gate does, and this listing is where its author finds
// out why. A listing that showed every gate but the one somebody has to fix
// would leave them with a held item and no account of it.
func collectGates(items []beads.WorkItem, into map[string]gateEntry, unreadable []unreadableGate) []unreadableGate {
	for _, item := range items {
		reading := humangate.Of(item)
		for _, gate := range reading.Gates {
			entry, seen := into[gate.Name]
			if !seen {
				entry = gateEntry{Name: gate.Name, Statement: gate.Statement}
			}
			entry.DeclaredBy = append(entry.DeclaredBy, item.ID)
			into[gate.Name] = entry
		}
		for _, problem := range reading.Unreadable {
			unreadable = append(unreadable, unreadableGate{WorkItemID: item.ID, Problem: problem})
		}
	}
	return unreadable
}

// listGateItems reads one tracker slice for the gates its items declare. The
// bound is the same one every other tracker read here is given, so a tracker
// that will not answer costs this command an error rather than hanging it.
func listGateItems(tracker beads.Client, status string) ([]beads.WorkItem, error) {
	ctx, cancel := context.WithTimeout(context.Background(), chatTrackerTimeout)
	defer cancel()
	items, err := tracker.List(ctx, status)
	if err != nil {
		return nil, fmt.Errorf("list %s work items: %w", status, err)
	}
	return items, nil
}

func reportGateError(stdout, stderr io.Writer, jsonOutput bool, err error) int {
	if jsonOutput {
		if code := writeJSON(stdout, stderr, gateOutput{Error: err.Error()}); code != 0 {
			return code
		}
		return 1
	}
	fmt.Fprintln(stderr, err)
	return 1
}

func printGateUsage(writer io.Writer) {
	fmt.Fprintln(writer, `Usage: yoyo gate <list|record> [options] [name]

A human gate is a step only a person can take. It is declared on the work it
holds -- a work item, or a state in a workflow definition -- by naming it after
`+"`"+humangate.DeclareMarker+"`"+` on its own line:

  `+humangate.DeclareMarker+` soak-clean — I have read a week of soak runs and they diverge nowhere

Nothing the harness does ever passes one. No run passes it, no check passes it,
and closing a work item does not pass it -- which is the point. A condition that
really reads "a person has to sign this off first" used to have only one
encoding available, an item somebody closes, and machinery closes items. This is
the encoding that does not have that hole.

gate list shows every gate the admitted work declares, which of them a person
has passed and when, and what is still waiting.

gate record writes one person's act, and is the only thing that passes a gate.
It says who took the step and what they did, because a gate passed by nobody in
particular and described by nothing is the flag this replaced. A gate already
passed is refused rather than overwritten: the record says whose act it was.

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --json            emit machine-readable JSON

gate record options:
  --by <person>     who took the step (default: $USER)
  --did <text>      what you did, in your own words (required)`)
}
