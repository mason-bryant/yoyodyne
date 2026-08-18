package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/goal"
)

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
