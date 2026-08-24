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
// Three different questions are asked of that one read, and they do not all fail
// in the same place. Whether the harness can read this repository's goals at all
// is a question about the reader, which is code and a developer's to fix, so it
// fails here. Whether a document that was read is well-formed is a question about
// the document — which lives in the product manager's home and is refused in
// every developer's diff — so it is escalated to its owner and fails
// `yoyo artifact check` instead. See internal/governeddoc for why: a build red
// for every developer over a file none of them may touch stops the whole loop
// and fixes nothing.
//
// The third is whether the attribution the backlog already carries still
// resolves, and it fails here with the other reader question rather than routing
// anywhere. Putting a defect right is what decides, and this one is put right in
// the tracker and in this file: no protected path has to change, so there is
// nothing to propose an amendment about and nobody to escalate it to.

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

	goals, _ := repositoryGoals(t)
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
	// The attribution the backlog already carries still resolves. This one fails
	// rather than routing anywhere, and the reason is worth stating because every
	// other finding in this file goes the other way: what is wrong here is not in
	// a governed document. Nothing in a protected home has to change to put it
	// right — the items are re-attributed with the tracker's "attribute" action,
	// which appends and rewrites nothing, and the constant below is brought into
	// line in this very file once they are. Escalating it would name an amendment
	// nobody needs to make and leave the class failing in no gate at all.
	//
	// `yoyo goals attribution` is what says which items are affected; it reads the
	// tracker, which is why the check that a specific wording still resolves lives
	// here and not in `yoyo artifact check`.
	if attribution := goals.Attribute(backlogAttribution); !attribution.Resolved() {
		t.Fatalf("the goal this repository's attributed work items name no longer resolves: %s\n"+
			"\tre-attribute them with the tracker's \"attribute\" action, check the rest with `yoyo goals attribution`, "+
			"and bring backlogAttribution in this file into line with what the goals now state",
			attribution.Reason)
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
// red while no developer may open the file. The convention is still held:
// `yoyo artifact check` exits non-zero on it, and that is the command the index
// internal/artifacthome writes at the door of the goals home tells an owner to
// run after editing there.
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
