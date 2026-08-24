package cli

// The tests here read this repository's own goals rather than a fixture,
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
//
// Two different questions are asked of that one read, and they fail in two
// different places on purpose. Whether the harness can read this repository's
// goals at all is a question about the reader, which is code and a developer's
// to fix, so it fails here. Whether a document that was read is well-formed is a
// question about the document — which lives in the product manager's home and is
// refused in every developer's diff — so it is escalated to its owner and fails
// `yoyo artifact check` instead. See internal/governeddoc for why: a build red
// for every developer over a file none of them may touch stops the whole loop
// and fixes nothing.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/goal"
	"github.com/mason-bryant/yoyodyne/internal/governeddoc"
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
	// Reading nothing at all is the reader's failure rather than any one
	// document's: it is what a tightened parser, a moved home, and a broken
	// collection all look like, and none of those is fixed by an amendment.
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

	goals, cfg := repositoryGoals(t)
	if _, uncheckable := goals.Uncheckable(); uncheckable {
		t.Fatalf("nothing in this repository can check an attribution; the gate is not running")
	}
	// Every goal the document states can be named on a work item. Reading a
	// statement and resolving one go through different code, and a statement this
	// collected but could not match would be a goal the harness offers and then
	// refuses. That is a disagreement between two readers rather than anything
	// wrong with the document, so it fails here rather than routing anywhere.
	for _, recorded := range goals.Goals {
		if attribution := goals.Attribute(recorded.Statement); !attribution.Resolved() {
			t.Fatalf("the goal %q does not resolve as an attribution: %s", recorded.Statement, attribution.Reason)
		}
	}
	// The attribution the backlog already carries still resolves. If this is
	// reported, a goal was reworded and the items attributed to the old wording
	// now read as naming a goal the goals do not state: re-attribute them with
	// the tracker's "attribute" action, which appends and rewrites nothing, and
	// check the rest with `yoyo goals attribution`. It is the wording of a
	// document nobody but its owner may change, so it is escalated rather than
	// failed.
	if attribution := goals.Attribute(backlogAttribution); !attribution.Resolved() {
		detail := "the goal this repository's attributed work items name no longer resolves: " + attribution.Reason
		reworded := governeddoc.Defect{Path: goalsHomeOf(cfg), Detail: detail}
		governeddoc.Report(cfg, []governeddoc.Defect{reworded}, t.Errorf, governeddoc.Escalate)
	}
}

// TestThisRepositoryOwnGoalsAreEachWrittenOnOneLine is the lint itself, run over
// the documents it is for. A wrapped goal is recorded whole — that is what
// closed the silent truncation the class is named for — so nothing fails until
// somebody reads the report, and reading a report by eye is exactly how six
// items stayed orphaned once already.
//
// What it reports is a defect in a goals document, so it is escalated to the
// product manager rather than failed: this is the very case the item behind
// internal/governeddoc names, a hard-wrapped goal turning every developer run
// red while no developer may open the file. The convention is still held —
// `yoyo artifact check` exits non-zero on it, and the index at the door of the
// goals home says to run it after an edit.
func TestThisRepositoryOwnGoalsAreEachWrittenOnOneLine(t *testing.T) {
	t.Parallel()

	goals, cfg := repositoryGoals(t)
	defects := make([]governeddoc.Defect, 0, len(goals.WrapProblems))
	for _, problem := range goals.WrapProblems {
		defects = append(defects, governeddoc.Defect{Path: problem.Path, Detail: problem.Reason})
	}
	governeddoc.Report(cfg, defects, t.Errorf, governeddoc.Escalate)
}

// repositoryGoals collects the goals from this repository as the harness reads
// them, through the configuration rather than a guessed set of artifact homes:
// a test that hardcoded the directories would keep passing after the project
// moved them, which is one of the ways the gate can quietly stop running.
//
// A goals document that could not be read is escalated rather than failed. It is
// one document among several, reported beside the goals the rest of them did
// state, and the file it names is the product manager's — so it is a defect
// somebody has to open that document to fix, and not one any run here can.
func repositoryGoals(t *testing.T) (goal.Set, config.Config) {
	t.Helper()

	repository, cfg := repositoryConfiguration(t)
	goals, err := loadGoals(repository, cfg.Product)
	if err != nil {
		t.Fatalf("loadGoals() error = %v", err)
	}
	defects := make([]governeddoc.Defect, 0, len(goals.Problems))
	for _, problem := range goals.Problems {
		defects = append(defects, governeddoc.Defect{Path: problem.Path, Detail: problem.Reason})
	}
	governeddoc.Report(cfg, defects, t.Errorf, governeddoc.Escalate)
	return goals, cfg
}

// repositoryConfiguration is this repository read the way every command reads
// it: the configuration, and the repository the product's homes are relative
// to.
func repositoryConfiguration(t *testing.T) (string, config.Config) {
	t.Helper()

	resolved, err := loadConfiguration(repositoryConfigPath)
	if err != nil {
		t.Fatalf("loadConfiguration() error = %v", err)
	}
	repository, err := resolvePath(config.ProjectDirectory(resolved.Path), resolved.Config.Product.Repository)
	if err != nil {
		t.Fatalf("resolve product repository: %v", err)
	}
	return repository, resolved.Config
}

// goalsHomeOf is where a finding about the goals as a whole, rather than about
// one file in them, is reported against. The convention the harness reads is
// that a goals document lives in a `goals` directory under the specifications
// home, which is the same derivation the artifact homes make.
func goalsHomeOf(cfg config.Config) string {
	return strings.TrimRight(cfg.Product.Specifications, "/") + "/goals"
}
