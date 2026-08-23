package cli

// The one test here reads this repository's own goals rather than a fixture,
// because the fixtures cannot fail the way that matters.
//
// Everything the goals gate does rests on one document parsing: the heading
// whose whole text is `Goals`, each goal an unindented entry under it, and an
// attribution matching a statement word for word once case, spacing, and
// trailing punctuation are folded. A document that stops satisfying any of that
// does not fail loudly — it yields no goals, `Known()` goes false, and every
// attribution becomes "uncheckable", which stops admission being refused and
// reports the queue as unchecked rather than untraceable. Synthetic documents
// written by a test satisfy the parser by construction and can never catch it.

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/goal"
)

// repositoryConfigPath is this repository's own configuration, read from the
// package the test lives in.
const repositoryConfigPath = "../../.yoyodyne/config.yaml"

// backlogAttribution is the goal named by the work items in this repository's
// backlog that carry an attribution, in the words the item records. It is
// checked verbatim because that is the claim the tracker actually holds: the
// parser being healthy is not the same fact as the attributions already written
// still resolving, and a reworded goal breaks the second while leaving the
// first perfectly green.
const backlogAttribution = "Maintain a traceable chain from the product brief through goals, designs, work, code changes, and verification."

func TestThisRepositoryOwnGoalsAreReadableByTheHarness(t *testing.T) {
	t.Parallel()

	stdout, stderr, code := runCLI(t, "goals", "list", "--config", repositoryConfigPath, "--json")
	if code != 0 {
		t.Fatalf("goals list code = %d, stderr = %q", code, stderr)
	}
	// A goals document that could not be read is reported rather than refused,
	// so a green exit status is not on its own evidence that anything was read.
	if strings.Contains(stderr, "goals not read") {
		t.Fatalf("this repository's goals could not be read: %s", stderr)
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
	if len(listed.Goals) == 0 {
		t.Fatalf("this repository records no goal work can be attributed to; the gate would report every attribution as unchecked")
	}
	for _, recorded := range listed.Goals {
		if !recorded.InForce || strings.TrimSpace(recorded.ArtifactID) == "" {
			t.Fatalf("goal = %#v", recorded)
		}
	}
}

func TestThisRepositoryOwnGoalsResolveAsAttributions(t *testing.T) {
	t.Parallel()

	goals := repositoryGoals(t)
	if _, uncheckable := goals.Uncheckable(); uncheckable {
		t.Fatalf("nothing in this repository can check an attribution; the gate is not running")
	}
	// Every goal the document states can be named on a work item. Reading a
	// statement and resolving one go through different code, and a statement this
	// collected but could not match would be a goal the harness offers and then
	// refuses.
	for _, recorded := range goals.Goals {
		if attribution := goals.Attribute(recorded.Statement); !attribution.Resolved() {
			t.Fatalf("the goal %q does not resolve as an attribution: %s", recorded.Statement, attribution.Reason)
		}
	}
	// The attribution the backlog already carries still resolves. If this fails,
	// a goal was reworded and the items attributed to the old wording now read as
	// naming a goal the goals do not state: re-attribute them with the tracker's
	// "attribute" action, which appends and rewrites nothing, and check the rest
	// with `yoyo goals attribution`.
	if attribution := goals.Attribute(backlogAttribution); !attribution.Resolved() {
		t.Fatalf("the goal this repository's attributed work items name no longer resolves: %s", attribution.Reason)
	}
}

// TestThisRepositoryOwnGoalsAreEachWrittenOnOneLine is the lint itself, run over
// the documents it is for. A wrapped goal is recorded whole — that is what
// closed the silent truncation the class is named for — so nothing fails until
// somebody reads the report, and reading a report by eye is exactly how six
// items stayed orphaned once already. Failing here is what makes the convention
// hold: the words an attribution has to match are then what the goals file says
// outright, rather than what rejoining the wrap produced.
func TestThisRepositoryOwnGoalsAreEachWrittenOnOneLine(t *testing.T) {
	t.Parallel()

	for _, problem := range repositoryGoals(t).WrapProblems {
		// Reported one at a time, because what somebody has to open is one place in
		// one file and a tally sends them looking for it.
		t.Errorf("a goal in this repository is not written on one line: %s", problem)
	}
}

// repositoryGoals collects the goals from this repository as the harness reads
// them, through the configuration rather than a guessed set of artifact homes:
// a test that hardcoded the directories would keep passing after the project
// moved them, which is one of the ways the gate can quietly stop running.
func repositoryGoals(t *testing.T) goal.Set {
	t.Helper()

	resolved, err := loadConfiguration(repositoryConfigPath)
	if err != nil {
		t.Fatalf("loadConfiguration() error = %v", err)
	}
	repository, err := resolvePath(config.ProjectDirectory(resolved.Path), resolved.Config.Product.Repository)
	if err != nil {
		t.Fatalf("resolve product repository: %v", err)
	}
	goals, err := loadGoals(repository, resolved.Config.Product)
	if err != nil {
		t.Fatalf("loadGoals() error = %v", err)
	}
	for _, problem := range goals.Problems {
		t.Fatalf("a goals document in %s could not be read: %s", filepath.Base(repository), problem)
	}
	return goals
}
