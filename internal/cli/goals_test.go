package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/goal"
)

// listingRunner answers each bd listing with the slice it asked for, so a test
// can exercise a read path that consults more than one of them.
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
	for _, want := range []string{
		"Maintain a traceable chain from the brief through to verification.",
		"stated by: v1-goals (docs/product/goals/v1-goals.md)",
		"Isolate implementation tasks in harness-managed worktrees.",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("list stdout = %q, want it to contain %q", stdout, want)
		}
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
// decomposition child carrying the note a creation wrote is found by the
// lookup the command actually performs and reported as serving its goal.
//
// It exists because the reported symptom was phrased in this command's words —
// six children of yoyodyne-ifd.102 reading "it records no goal" — and a test
// that resolved the goal by calling the parse directly would leave open whether
// the loss was in how the audit locates an item and pulls its notes. So this
// runs admittedWorkItems, which is the whole of that lookup, against a bd that
// answers with the item's notes, and then asks the same question reportAttribution
// asks of every item it lists.
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

	// From here on this is reportAttribution's own body: the goals it read, and
	// AttributionOf against each item's notes.
	goals := goal.Set{
		Sources: []string{"v1-goals"},
		Goals:   []goal.Goal{{Statement: autonomy, ArtifactID: "v1-goals", InForce: true}},
	}
	attributions := make([]itemAttribution, 0, len(admitted))
	for _, item := range admitted {
		attributions = append(attributions, itemAttribution{
			WorkItemID:  item.ID,
			Title:       item.Title,
			Status:      item.Status,
			Attribution: goals.AttributionOf(item.Notes),
		})
	}
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
		{WorkItemID: "ifd.2", Title: "Legacy work", Attribution: goals.AttributionOf("Admitted long ago.")},
		{WorkItemID: "ifd.3", Title: "Misattributed work", Attribution: goals.Attribute("Ship the prototype.")},
	}

	var rendered bytes.Buffer
	printAttributions(&rendered, attributions, goals)
	report := rendered.String()
	if !strings.Contains(report, "3 admitted item(s): 1 serve a recorded goal, 1 name none, 1 name a goal the goals do not state") {
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
