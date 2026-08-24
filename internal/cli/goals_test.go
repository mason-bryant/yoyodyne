package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/console"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/goal"
)

// ansiEscapes is what a theme adds and nothing else, so a test can check that a
// listing with the dressing stripped out is the listing that was written.
var ansiEscapes = regexp.MustCompile("\x1b\\[[0-9;]*[a-zA-Z]")

// listingRunner answers each bd listing with the slice it asked for, so a test
// can exercise a read path that consults more than one of them. What it answers
// with is bd's own listing shape, metadata included, because the metadata is
// where the audit reads the witness that a goal was written.
type listingRunner struct {
	items map[string][]map[string]any
}

func (r *listingRunner) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	listed := []map[string]any{}
	for _, argument := range command.Args {
		if status, asked := strings.CutPrefix(argument, "--status="); asked {
			listed = r.items[status]
		}
	}
	encoded, err := json.Marshal(listed)
	if err != nil {
		return execution.ProcessResult{}, err
	}
	return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: string(encoded)}, nil
}

func TestTheGoalsWorkCanBeAttributedToAreListedWithWhereTheyAreStated(t *testing.T) {
	t.Parallel()

	configPath := writeConfig(t, validConfig)
	project := filepath.Dir(configPath)
	writeArtifact(t, project, "docs/product/brief.md", artifactDocument("brief", "brief", "Product brief", nil)+"\nIntent in, software out.\n")
	writeArtifact(t, project, "docs/product/goals/v1-goals.md", artifactDocument("v1-goals", "goals", "V1 goals", []string{"brief"})+`
# V1 goals

An introduction.

## Goals

- Maintain a traceable chain from the brief through to verification.
- Isolate implementation tasks in harness-managed worktrees.
`)

	stdout, stderr, code := runCLI(t, "goals", "list", "--config", configPath)
	if code != 0 {
		t.Fatalf("list code = %d, stderr = %q", code, stderr)
	}
	// The listing is laid out to be read: one goal to an entry, a blank line
	// between entries, where it is stated indented under it, and a closing line
	// saying where the chain goes above these goals. None of that is dressing —
	// this is a buffer rather than a terminal, and it is the whole of what the
	// listing says.
	want := `Maintain a traceable chain from the brief through to verification.
  stated by: v1-goals (docs/product/goals/v1-goals.md)

Isolate implementation tasks in harness-managed worktrees.
  stated by: v1-goals (docs/product/goals/v1-goals.md)

upstream: these goals support the goals the product brief states, in docs/product/brief.md
`
	if stdout != want {
		t.Fatalf("list stdout = %q, want %q", stdout, want)
	}
	if strings.Contains(stdout, "\x1b") {
		t.Fatalf("a listing written to something that is not a terminal carried escapes: %q", stdout)
	}

	stdout, stderr, code = runCLI(t, "goals", "list", "--config", configPath, "--json")
	if code != 0 {
		t.Fatalf("list --json code = %d, stderr = %q", code, stderr)
	}
	var listed struct {
		Goals []struct {
			Statement  string `json:"statement"`
			ArtifactID string `json:"artifact_id"`
			InForce    bool   `json:"in_force"`
		} `json:"goals"`
	}
	if err := json.Unmarshal([]byte(stdout), &listed); err != nil {
		t.Fatalf("Unmarshal() error = %v over %q", err, stdout)
	}
	if len(listed.Goals) != 2 || listed.Goals[0].ArtifactID != "v1-goals" || !listed.Goals[0].InForce {
		t.Fatalf("listed = %#v", listed.Goals)
	}
	// What is read by a program carries none of what was laid out for a person:
	// no escapes, and no closing line to be parsed as a goal.
	if strings.Contains(stdout, "\x1b") || strings.Contains(stdout, "upstream:") {
		t.Fatalf("--json carried the listing's presentation: %q", stdout)
	}
}

// The dressing the listing is read with on a terminal, and the promise it is
// held to: the statement weighted, the lines about it slanted, and every
// distinction still there once the escapes are gone. What separates two goals is
// the blank line, what says a line is about the goal above it is the indent, and
// what says a goal is no longer in force is said in words — so the listing on a
// terminal that cannot be dressed is the same listing.
func TestTheGoalsListingIsDressedWithoutTheDressingCarryingAnything(t *testing.T) {
	t.Parallel()

	goals := goal.Set{
		Sources: []string{"v1-goals"},
		Goals: []goal.Goal{
			{
				Statement:  "Maintain a traceable chain from the brief through to verification.",
				Supports:   "Every change traces to intent somebody approved",
				ArtifactID: "v1-goals",
				Path:       "docs/product/goals/v1-goals.md",
				InForce:    true,
			},
			{
				Statement:  "Ship the first version by hand.",
				ArtifactID: "v0-goals",
				Path:       "docs/product/goals/v0-goals.md",
			},
		},
		BriefPath: "docs/product/brief.md",
	}

	var plain strings.Builder
	printGoals(&plain, console.Theme{}, goals)

	var dressed strings.Builder
	printGoals(&dressed, console.NewTheme(
		func(name string) string { return map[string]string{"TERM": "xterm-256color"}[name] },
		func() int { return 80 },
	), goals)

	if !strings.Contains(dressed.String(), "\x1b") {
		t.Fatal("a terminal that permits dressing was written an undressed listing")
	}
	if stripped := ansiEscapes.ReplaceAllString(dressed.String(), ""); stripped != plain.String() {
		t.Fatalf("stripping the escapes changed the listing:\n%q\nwant\n%q", stripped, plain.String())
	}
	// The statement is the entry and the lines under it are about it, and the two
	// are dressed as what they are rather than alike.
	lines := strings.Split(dressed.String(), "\n")
	if !strings.HasPrefix(lines[0], "\x1b[1m") {
		t.Fatalf("the statement was not weighted: %q", lines[0])
	}
	for _, index := range []int{1, 2} {
		if !strings.HasPrefix(lines[index], "\x1b[3m") {
			t.Fatalf("a line about the goal was not slanted: %q", lines[index])
		}
	}
	// The listing without any dressing at all is the whole of what it says: the
	// blank line between entries, the marker on a goal nobody may name now, and
	// the closing line naming the brief upstream.
	want := `Maintain a traceable chain from the brief through to verification.
  stated by: v1-goals (docs/product/goals/v1-goals.md)
  supports: Every change traces to intent somebody approved

Ship the first version by hand. [no longer in force]
  stated by: v0-goals (docs/product/goals/v0-goals.md)

upstream: these goals support the goals the product brief states, in docs/product/brief.md
`
	if plain.String() != want {
		t.Fatalf("undressed listing = %q, want %q", plain.String(), want)
	}
}

// A repository that records no brief is still told what these goals are for. It
// is not sent to a file that is not there — what is wrong is reported with the
// other broken links upstream, and inventing a path would send a reader looking
// for a document nobody wrote.
func TestTheClosingLineNamesNoBriefWhereTheRepositoryRecordsNone(t *testing.T) {
	t.Parallel()

	var out strings.Builder
	printGoals(&out, console.Theme{}, goal.Set{
		Sources: []string{"v1-goals"},
		Goals:   []goal.Goal{{Statement: "Run development nearly autonomously.", ArtifactID: "v1-goals", Path: "docs/product/goals/v1-goals.md", InForce: true}},
	})
	if !strings.HasSuffix(out.String(), "\nupstream: these goals support the goals the product brief states\n") {
		t.Fatalf("listing = %q", out.String())
	}
}

func TestAGoalsDocumentStatingNoGoalsIsNamedRatherThanReadAsFewerGoals(t *testing.T) {
	t.Parallel()

	configPath := writeConfig(t, validConfig)
	project := filepath.Dir(configPath)
	writeArtifact(t, project, "docs/product/brief.md", artifactDocument("brief", "brief", "Product brief", nil))
	writeArtifact(t, project, "docs/product/goals/v1-goals.md",
		artifactDocument("v1-goals", "goals", "V1 goals", []string{"brief"})+"\n# V1 goals\n\nThe goals are still to be written.\n")

	stdout, stderr, code := runCLI(t, "goals", "list", "--config", configPath)
	if code != 0 {
		t.Fatalf("list code = %d, stderr = %q", code, stderr)
	}
	// Nothing can be attributed to it, and the operator is told which document to
	// open rather than being shown a listing that is simply short.
	if !strings.Contains(stderr, "goals not read: docs/product/goals/v1-goals.md") {
		t.Fatalf("list stderr = %q", stderr)
	}
	if !strings.Contains(stdout, "no goal is in force") {
		t.Fatalf("list stdout = %q", stdout)
	}
}

// The audit's own read path, end to end from the bd command line: a
// decomposition child carrying the note a creation wrote is found by the lookup
// the command actually performs and reported as serving its goal.
//
// It exists because the symptom that started this was phrased in this command's
// words — six children of yoyodyne-ifd.102 reading "it records no goal" — and a
// test that resolved the goal by calling the parse directly would leave open
// whether the loss was in how the audit locates an item and pulls its notes. So
// this runs admittedWorkItems, which is the whole of that lookup, against a bd
// that answers with the item's notes, and then reportAttribution's own judging
// and rendering over what came back.
func TestTheAuditFindsADecompositionChildsGoalThroughItsOwnLookup(t *testing.T) {
	t.Parallel()

	autonomy := "Run development nearly autonomously."
	// The note a decomposition writes, in the shape internal/chat builds it:
	// provenance, the reason, and the goal on its own line at the end.
	child := "Created under yoyodyne-ifd.102, decomposing it by the development manager " +
		"in conversation chat-419cedb4, after turn 3.\n\nReason: nothing routes stopped work today.\n\n" +
		goal.Note(autonomy)
	bd := &listingRunner{items: map[string][]map[string]any{
		"open": {{
			"id": "yoyodyne-ifd.102.2", "title": "Triage docket", "status": "open",
			"priority": 1, "issue_type": "task", "notes": child,
		}},
		"blocked": {{
			// A blocked sibling too: the audit reads two slices of the tracker, and
			// an item found in only one of them would be reported on by only one.
			"id": "yoyodyne-ifd.102.7", "title": "Re-arm a dropped queued merge", "status": "blocked",
			"priority": 3, "issue_type": "task", "notes": child,
		}},
	}}

	admitted, err := admittedWorkItems(context.Background(), beads.Client{Runner: bd, Binary: "bd-test", Dir: "/repo"})
	if err != nil {
		t.Fatalf("admittedWorkItems() error = %v", err)
	}
	if len(admitted) != 2 {
		t.Fatalf("the audit's lookup found %d item(s): %#v", len(admitted), admitted)
	}

	goals := goal.Set{
		Sources: []string{"v1-goals"},
		Goals:   []goal.Goal{{Statement: autonomy, ArtifactID: "v1-goals", InForce: true}},
	}
	attributions := attributionsOf(admitted, goals)
	if code := attributionExitCode(attributions); code != 0 {
		t.Fatalf("the audit failed a decomposition child: %#v", attributions)
	}

	var rendered bytes.Buffer
	printAttributions(&rendered, attributions, goals)
	report := rendered.String()
	// The words the symptom was reported in. If a decomposition child ever reads
	// as naming no goal again, it fails here in the same language the operator saw.
	if strings.Contains(report, "it records no goal") {
		t.Fatalf("a decomposition child reads as naming no goal:\n%s", report)
	}
	if !strings.Contains(report, "2 admitted item(s): 2 serve a recorded goal, 0 name none") {
		t.Fatalf("report = %q", report)
	}
}

// An item whose notes were replaced rather than appended to: the goal it was
// created under is gone from them, and the witness the tracker carries beside
// them is not. That is what actually happened to the six children of
// yoyodyne-ifd.102, and what made it cost a week is that the audit reported it
// as the one state it does not fail on.
func TestTheAuditFailsAnItemWhoseRecordedGoalWasWrittenOver(t *testing.T) {
	t.Parallel()

	autonomy := "Run development nearly autonomously."
	bd := &listingRunner{items: map[string][]map[string]any{
		"open": {{
			"id": "yoyodyne-ifd.102.2", "title": "Triage docket", "status": "open",
			"priority": 1, "issue_type": "task",
			// Everything a careless writer left behind, and nothing of what it
			// replaced — except in the metadata it could not reach, which is where
			// the goal that was written survives it.
			"notes":    "Constraints from the architect, recorded 2026-08-19.",
			"metadata": map[string]any{"yoyodyne_goal_recorded": autonomy},
		}},
		"blocked": {{
			// Beside it, an item that genuinely predates the check: no goal, and no
			// witness that one was ever written. It must stay grandfathered, or the
			// audit fails a backlog nobody has had the chance to attribute.
			"id": "yoyodyne-ifd.45", "title": "Admitted long ago", "status": "blocked",
			"priority": 2, "issue_type": "task", "notes": "Admitted by hand.",
		}},
	}}

	admitted, err := admittedWorkItems(context.Background(), beads.Client{Runner: bd, Binary: "bd-test", Dir: "/repo"})
	if err != nil {
		t.Fatalf("admittedWorkItems() error = %v", err)
	}
	goals := goal.Set{
		Sources: []string{"v1-goals"},
		Goals:   []goal.Goal{{Statement: autonomy, ArtifactID: "v1-goals", InForce: true}},
	}
	attributions := attributionsOf(admitted, goals)

	states := map[string]goal.State{}
	for _, entry := range attributions {
		states[entry.WorkItemID] = entry.Attribution.State
	}
	if states["yoyodyne-ifd.102.2"] != goal.StateLost {
		t.Fatalf("the overwritten item reads as %q: %#v", states["yoyodyne-ifd.102.2"], attributions)
	}
	if states["yoyodyne-ifd.45"] != goal.StateUnattributed {
		t.Fatalf("the legacy item reads as %q: %#v", states["yoyodyne-ifd.45"], attributions)
	}
	// Loudly: the audit fails, rather than listing it among the items somebody
	// has yet to attribute.
	if code := attributionExitCode(attributions); code != 1 {
		t.Fatalf("exit code over a destroyed attribution = %d", code)
	}

	var rendered bytes.Buffer
	printAttributions(&rendered, attributions, goals)
	report := rendered.String()
	for _, want := range []string{
		"1 lost the goal they recorded",
		"having recorded a goal and lost it",
		"yoyodyne-ifd.102.2",
		"written over rather than never made",
		// The words to put back, quoted where the tracker kept them. A report that
		// only said an attribution was destroyed would leave whoever reads it to
		// re-derive a judgement somebody already made.
		autonomy,
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("report = %q, want it to contain %q", report, want)
		}
	}
}

// The sweep that closes the gap the witness leaves behind it: work attributed
// before any of this existed carries no witness, so replacing its notes would
// still read as work nobody ever attributed. It copies each item's own recorded
// goal to where a careless writer cannot reach it, and judges nothing.
func TestWitnessingRecordsTheGoalAnItemAlreadyStatesAndDecidesNothing(t *testing.T) {
	t.Parallel()

	autonomy := "Run development nearly autonomously."
	bd := &sweepRunner{listed: map[string][]map[string]any{
		"open": {
			// Attributed long before the witness existed: the goal is in the notes
			// and nothing outside them says so.
			{"id": "yoyodyne-ifd.102.2", "title": "Triage docket", "status": "open",
				"priority": 1, "issue_type": "task", "notes": "Admitted long ago.\n\n" + goal.Note(autonomy)},
			// Already witnessed: nothing to do, and writing again would be a second
			// write per item on every sweep.
			{"id": "yoyodyne-ifd.68", "title": "Slack reporting", "status": "open",
				"priority": 2, "issue_type": "task", "notes": goal.Note(autonomy),
				"metadata": map[string]any{"yoyodyne_goal_recorded": autonomy}},
			// Records no goal: there is nothing to witness, and a witness written
			// here would turn work nobody has attributed yet into work that reads as
			// having lost an attribution it never had. This is the one the sweep must
			// not touch.
			{"id": "yoyodyne-ifd.45", "title": "Admitted long ago", "status": "open",
				"priority": 3, "issue_type": "task", "notes": "Admitted by hand."},
		},
		// The two slices the backlog leaves out, and the two the recorded losses
		// actually landed in: the item somebody is working on right now, and the
		// closed items that were written over after they closed. A sweep scoped to
		// the queue protects neither.
		"in_progress": {
			{"id": "yoyodyne-ifd.99", "title": "Configurable roles", "status": "in_progress",
				"priority": 1, "issue_type": "task", "notes": goal.Note(autonomy)},
		},
		"closed": {
			{"id": "yoyodyne-ifd.4", "title": "Run one work item", "status": "closed",
				"priority": 1, "issue_type": "task", "notes": goal.Note(autonomy)},
		},
	}}

	tracker := beads.Client{Runner: bd, Binary: "bd-test", Dir: "/repo"}
	swept, err := workItemsWithStatus(context.Background(), tracker, witnessStatuses)
	if err != nil {
		t.Fatalf("workItemsWithStatus() error = %v", err)
	}
	witnessed, failures := recordGoalWitnesses(context.Background(), tracker, swept)
	if failures != 0 {
		t.Fatalf("witnessed = %#v", witnessed)
	}
	if len(bd.written) != 3 {
		t.Fatalf("the sweep wrote %#v, want every unwitnessed attributed item whatever its status", bd.written)
	}
	for _, protected := range []string{"yoyodyne-ifd.102.2", "yoyodyne-ifd.99", "yoyodyne-ifd.4"} {
		if bd.written[protected] != autonomy {
			t.Fatalf("%s was not witnessed: the sweep wrote %#v", protected, bd.written)
		}
	}

	var rendered bytes.Buffer
	printWitnessed(&rendered, len(swept), witnessed)
	for _, want := range []string{"3 newly witnessed", "yoyodyne-ifd.102.2 witnessed: " + autonomy} {
		if !strings.Contains(rendered.String(), want) {
			t.Fatalf("witness stdout = %q, want it to contain %q", rendered.String(), want)
		}
	}

	// The item it just witnessed now reads as protected: replacing its notes
	// tomorrow is a loss the audit reports and fails, which is the whole point of
	// having swept it. Before the sweep the same replacement read as work nobody
	// had attributed.
	goals := goal.Set{Sources: []string{"v1-goals"}, Goals: []goal.Goal{{Statement: autonomy, ArtifactID: "v1-goals", InForce: true}}}
	overwritten := beads.WorkItem{ID: "yoyodyne-ifd.102.2", Notes: "Constraints from the architect.",
		GoalWitness: goal.Witness{Recorded: true, Statement: bd.written["yoyodyne-ifd.102.2"]}}
	if lost := goals.AttributionOf(overwritten.Notes, overwritten.GoalWitness); lost.State != goal.StateLost || lost.Recorded != autonomy {
		t.Fatalf("a swept item is not protected: %#v", lost)
	}

	// An item the tracker refused is named and does not stop the sweep, and the
	// sweep reports the failure: a sweep reported as done while an item stayed
	// uncovered is how a gap gets believed closed.
	refusing := &sweepRunner{
		listed:  map[string][]map[string]any{"open": {{"id": "yoyodyne-ifd.102.2", "title": "Triage docket", "status": "open", "priority": 1, "issue_type": "task", "notes": goal.Note(autonomy)}}},
		refuse:  true,
		refusal: "bd: the tracker is read-only",
	}
	refused := beads.Client{Runner: refusing, Binary: "bd-test", Dir: "/repo"}
	unwitnessed, err := workItemsWithStatus(context.Background(), refused, witnessStatuses)
	if err != nil {
		t.Fatalf("workItemsWithStatus() error = %v", err)
	}
	attempted, refusals := recordGoalWitnesses(context.Background(), refused, unwitnessed)
	if refusals != 1 || len(attempted) != 1 || attempted[0].Failure == "" {
		t.Fatalf("a refused write was not reported: %#v", attempted)
	}
	var refusedReport bytes.Buffer
	printWitnessed(&refusedReport, len(unwitnessed), attempted)
	if !strings.Contains(refusedReport.String(), "yoyodyne-ifd.102.2 could not be witnessed") {
		t.Fatalf("witness stdout = %q", refusedReport.String())
	}
}

// The other half of the sweep above, standing in front of the writer instead of
// behind it: the tool call an agent session is about to make, decided before the
// notes are replaced rather than reported after they were.
//
// This exercises the decision, which is what the harness owns. Whether Claude
// Code accepts the settings block that installs it, fires it for a Bash call, and
// finds `yoyo` on the run's PATH is an end-to-end fact about the provider that no
// unit test can reach; the path fails open, so a mistake there is silent and the
// witness above is what still holds the words to put back.
func TestTheGuardRefusesTheWriterAndSaysNothingAboutAnythingElse(t *testing.T) {
	t.Parallel()

	autonomy := "Run development nearly autonomously."
	for _, test := range []struct {
		name    string
		payload string
		denied  bool
	}{
		{
			name:    "the writer that destroys an attribution",
			payload: `{"tool_name":"Bash","tool_input":{"command":"bd update yoyodyne-ifd.45 --notes=\"replaced\""}}`,
			denied:  true,
		},
		{
			name:    "the same write carrying the attribution through",
			payload: `{"tool_name":"Bash","tool_input":{"command":"bd update yoyodyne-ifd.45 --notes=\"` + goal.Note(autonomy) + `\""}}`,
		},
		{
			name:    "the spelling that adds rather than replaces",
			payload: `{"tool_name":"Bash","tool_input":{"command":"bd update yoyodyne-ifd.45 --append-notes=\"what I did\""}}`,
		},
		{
			// Nothing but a shell command can carry the writer, and a guard with an
			// opinion about reading a file is a guard in the way of every run.
			name:    "a tool that is not a shell",
			payload: `{"tool_name":"Read","tool_input":{"command":"bd update yoyodyne-ifd.45 --notes=replaced"}}`,
		},
		{
			// A payload this cannot read allows the command and says so. Refusing it
			// would turn one unrecognised hook shape into a session that can run no
			// commands at all -- the guard being the outage instead of preventing one.
			name:    "a tool call it cannot read",
			payload: `not json at all`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			if code := guardNotesReplacement(nil, strings.NewReader(test.payload), &stdout, &stderr); code != 0 {
				t.Fatalf("guard exit code = %d, stderr = %q", code, stderr.String())
			}
			if !test.denied {
				if stdout.String() != "" {
					t.Fatalf("guard said something about a command it has no opinion on: %q", stdout.String())
				}
				return
			}
			var decision hookDecision
			if err := json.Unmarshal(stdout.Bytes(), &decision); err != nil {
				t.Fatalf("guard stdout = %q: %v", stdout.String(), err)
			}
			if decision.Output.EventName != "PreToolUse" || decision.Output.Decision != "deny" {
				t.Fatalf("guard decision = %#v", decision.Output)
			}
			if !strings.Contains(decision.Output.Reason, "yoyodyne-ifd.45") ||
				!strings.Contains(decision.Output.Reason, "--append-notes") {
				t.Fatalf("guard reason = %q, want it to name the item and what to run instead", decision.Output.Reason)
			}
		})
	}
}

// The sweep is deliberately wider than the audit, and every document that
// describes either one now says which is which. Pinning the relationship is what
// keeps the prose from drifting back into the overclaim it was corrected for: the
// sweep must reach everything the audit reads, or an item could be reported as
// having lost a goal with no witness holding the words to put back, and it must
// reach more than that, which is the part that buys recovery on work the audit
// never looks at.
func TestTheSweepReachesEveryStatusTheAuditReadsAndMore(t *testing.T) {
	t.Parallel()

	swept := map[string]bool{}
	for _, status := range witnessStatuses {
		swept[status] = true
	}
	for _, audited := range backlogStatuses {
		if !swept[audited] {
			t.Fatalf("the audit reads %q and the sweep does not reach it, so a loss there could be reported with no witness to put back", audited)
		}
	}
	if len(witnessStatuses) <= len(backlogStatuses) {
		t.Fatalf("the sweep reaches %v, which is no wider than the audit's %v", witnessStatuses, backlogStatuses)
	}
}

// sweepRunner is a bd that answers listings and keeps what a witness wrote, so
// a sweep can be checked for what it did and did not touch.
type sweepRunner struct {
	listed  map[string][]map[string]any
	written map[string]string
	refuse  bool
	refusal string
}

func (r *sweepRunner) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	if len(command.Args) > 0 && command.Args[0] == "update" {
		if r.refuse {
			return execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: r.refusal}, nil
		}
		id := command.Args[1]
		for _, argument := range command.Args {
			statement, carried := strings.CutPrefix(argument, "--set-metadata=yoyodyne_goal_recorded=")
			if !carried {
				continue
			}
			if r.written == nil {
				r.written = map[string]string{}
			}
			r.written[id] = statement
			item := map[string]any{"id": id, "title": "t", "status": "open", "priority": 1, "issue_type": "task",
				"metadata": map[string]any{"yoyodyne_goal_recorded": statement}}
			encoded, err := json.Marshal([]map[string]any{item})
			if err != nil {
				return execution.ProcessResult{}, err
			}
			return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: string(encoded)}, nil
		}
	}
	listed := []map[string]any{}
	for _, argument := range command.Args {
		if status, asked := strings.CutPrefix(argument, "--status="); asked {
			listed = r.listed[status]
		}
	}
	encoded, err := json.Marshal(listed)
	if err != nil {
		return execution.ProcessResult{}, err
	}
	return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: string(encoded)}, nil
}

func TestTheAuditFailsAWrongAttributionAndNotAMissingOne(t *testing.T) {
	t.Parallel()

	// The grandfathering decision, as the exit status states it: work admitted
	// before goals were checked names none and is somebody's to attribute, and a
	// rule that failed it would stop a backlog rather than close a gap. An item
	// naming a goal the goals do not state is a claim that is wrong.
	legacy := []itemAttribution{
		{WorkItemID: "ifd.1", Attribution: goal.Attribution{State: goal.StateUnattributed}},
		{WorkItemID: "ifd.2", Attribution: goal.Attribution{State: goal.StateAttributed}},
	}
	if code := attributionExitCode(legacy); code != 0 {
		t.Fatalf("exit code over a grandfathered backlog = %d", code)
	}
	wrong := append(legacy, itemAttribution{WorkItemID: "ifd.3", Attribution: goal.Attribution{State: goal.StateUnresolved}})
	if code := attributionExitCode(wrong); code != 1 {
		t.Fatalf("exit code over a wrong attribution = %d", code)
	}
}

func TestTheAuditSeparatesWorkWithNoGoalFromWorkWhoseGoalIsWrong(t *testing.T) {
	t.Parallel()

	goals := goal.Set{
		Sources: []string{"v1-goals"},
		Goals:   []goal.Goal{{Statement: "Maintain a traceable chain.", ArtifactID: "v1-goals", InForce: true}},
	}
	attributions := []itemAttribution{
		{WorkItemID: "ifd.1", Title: "Attributed work", Attribution: goals.Attribute("Maintain a traceable chain.")},
		{WorkItemID: "ifd.2", Title: "Legacy work", Attribution: goals.AttributionOf("Admitted long ago.", goal.Witness{})},
		{WorkItemID: "ifd.3", Title: "Misattributed work", Attribution: goals.Attribute("Ship the prototype.")},
	}

	var rendered bytes.Buffer
	printAttributions(&rendered, attributions, goals)
	report := rendered.String()
	if !strings.Contains(report, "3 admitted item(s): 1 serve a recorded goal, 1 name none, 1 name a goal the goals do not state, 0 lost the goal they recorded") {
		t.Fatalf("report = %q", report)
	}
	// Each item is under the heading that says what to do about it, so the two
	// ways of not being attributed never read as one pile of failures.
	for _, want := range []string{
		"naming a goal no goals document states",
		"naming no goal, which is what work admitted before goals were checked looks like",
		"serving a recorded goal",
		"ifd.3",
		"ifd.2",
		"ifd.1",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("report = %q, want it to contain %q", report, want)
		}
	}
}

func TestTheAuditReportsNothingCheckedRatherThanNothingFound(t *testing.T) {
	t.Parallel()

	// A repository whose goals could not be read must not have its queue reported
	// as unattributed: nothing was checked, and saying so is the whole of what is
	// honest.
	var rendered bytes.Buffer
	printAttributions(&rendered, []itemAttribution{{WorkItemID: "ifd.1"}}, goal.Unreadable("the artifact homes are outside the repository"))
	if !strings.Contains(rendered.String(), "none of them checked: the goals could not be read") {
		t.Fatalf("report = %q", rendered.String())
	}

	// A destroyed attribution is the exception, because saying it needs no goals
	// document: the tracker witnesses a goal was written and the item no longer
	// carries one. It is also what the audit exits non-zero for here, and a
	// failure with nothing said about it is worse than none.
	unreadable := goal.Unreadable("the artifact homes are outside the repository")
	lost := []itemAttribution{
		{WorkItemID: "ifd.1"},
		{WorkItemID: "ifd.102.2", Title: "Triage docket", Status: "open", Priority: 1,
			Attribution: unreadable.AttributionOf("Constraints from the architect.", goal.Witness{Recorded: true, Statement: "Run development nearly autonomously."})},
	}
	var withLoss bytes.Buffer
	printAttributions(&withLoss, lost, unreadable)
	if !strings.Contains(withLoss.String(), "ifd.102.2") || !strings.Contains(withLoss.String(), "written over rather than never made") {
		t.Fatalf("report = %q", withLoss.String())
	}
	if code := attributionExitCode(lost); code != 1 {
		t.Fatalf("exit code over a destroyed attribution nothing could be checked against = %d", code)
	}
}

func TestTheListPrintsTheBriefLinkAndReportsEachWayItBreaks(t *testing.T) {
	t.Parallel()

	configPath := writeConfig(t, validConfig)
	project := filepath.Dir(configPath)
	writeArtifact(t, project, "docs/product/brief.md", artifactDocument("brief", "brief", "Product brief", nil)+`
# Product brief

An introduction.

## Goals

- **Intent in, software out** — the harness carries approved intent to merged code.
`)
	writeArtifact(t, project, "docs/product/goals/v1-goals.md", artifactDocument("v1-goals", "goals", "V1 goals", []string{"brief"})+`
# V1 goals

An introduction.

## Goals

- Maintain a traceable chain from intent to verification.
  *Supports: intent in, software out.*
- Isolate implementation tasks in harness-managed worktrees.
- Publish work as pull requests the harness opens.
  *Supports: a claim the brief does not state.*
`)

	stdout, stderr, code := runCLI(t, "goals", "list", "--config", configPath)
	if code != 0 {
		t.Fatalf("list code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "supports: intent in, software out.") {
		t.Fatalf("list stdout = %q, want the resolved link printed beside the goal", stdout)
	}
	for _, want := range []string{
		"goal not linked to the brief:",
		"it names no brief goal",
		"a claim the brief does not state",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("list stderr = %q, want it to contain %q", stderr, want)
		}
	}

	jsonStdout, jsonStderr, jsonCode := runCLI(t, "goals", "list", "--json", "--config", configPath)
	if jsonCode != 0 {
		t.Fatalf("json list code = %d, stderr = %q", jsonCode, jsonStderr)
	}
	// The field names are the operator-facing contract, so the test decodes
	// them by name rather than through goalsOutput.
	var decoded struct {
		BriefGoals   []goal.BriefGoal   `json:"brief_goals"`
		LinkProblems []goal.LinkProblem `json:"link_problems"`
	}
	if err := json.Unmarshal([]byte(jsonStdout), &decoded); err != nil {
		t.Fatalf("decode json listing: %v", err)
	}
	if len(decoded.BriefGoals) != 1 || decoded.BriefGoals[0].Name != "Intent in, software out" {
		t.Fatalf("brief_goals = %+v, want the one bolded brief claim by name", decoded.BriefGoals)
	}
	if len(decoded.LinkProblems) != 2 {
		t.Fatalf("link_problems = %+v, want the unstated and dangling goals reported", decoded.LinkProblems)
	}
}
