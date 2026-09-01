package composition

// The gates themselves, run over this repository rather than over a fixture.
// This is the point of the package: the fixtures above prove each gate finds
// what it is for, and these are what make a defect in this project's own shell,
// YAML, JSON, or workflows fail a declared check instead of reaching a reviewer
// with nothing having read it.
//
// They run under `make test`, which is one of the four checks this project
// declares, so they run on every verify pass of every run — including each
// repair — and on the machine of whoever made the change, rather than as a CI
// step somebody reads a red X on the day after they broke it. That distinction
// is the whole of yoyodyne-ifd.165: a check a run does not apply is not a gate
// a run has.

import (
	"os/exec"
	"slices"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
)

// repositoryRoot is the checkout these tests run in, reached from the package
// directory, and repositoryConfigPath the configuration whose checks list they
// are held against. The files are read where they live rather than from a copy,
// because a copy is exactly what cannot carry the defect.
const (
	repositoryRoot       = "../.."
	repositoryConfigPath = "../../.yoyodyne/config.yaml"
)

// statusToolSuitePath is the status tool's own suite. The tool is a wrapper for
// `yoyo status` now — it derived run, conversation, branch-review, and exchange
// state for itself until surfaces-project-one-read-model was ruled to bind it —
// so the suite stubs `yoyo`, checks what the wrapper passed on, and checks that
// no derivation has crept back into it. It needs no provider, no repository, and
// never reads an operator's real state, which is what makes it cheap enough to
// run from here. `bin/yoyo-status` is shell, so without this nothing a run
// applied would execute a line of it.
const statusToolSuitePath = "../../scripts/yoyo-status-test.sh"

// TestThisRepositoryIsAllAccountedFor is the audit. Every file this repository
// carries belongs to a content class, every class recognizes something, and
// every check a class credits its coverage to is one the project actually
// declares.
func TestThisRepositoryIsAllAccountedFor(t *testing.T) {
	t.Parallel()

	files := repositoryFiles(t)
	declared, err := config.Load(repositoryConfigPath)
	if err != nil {
		t.Fatalf("Load(%s) error = %v", repositoryConfigPath, err)
	}
	// A configuration with no checks is refused by the harness, so reaching here
	// with an empty list means the list was read from somewhere it is not.
	if len(declared.Checks) == 0 {
		t.Fatalf("%s declares no checks; the ledger is being held against nothing", repositoryConfigPath)
	}
	problems, err := Check(repositoryRoot, Classes, files, declared.Checks)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	for _, problem := range problems {
		// Reported one at a time rather than as a count: what somebody has to act
		// on is a file or a class, and a tally sends them looking for it.
		t.Errorf("this repository and its recorded composition disagree: %s", problem)
	}
}

func TestEveryShellFileInThisRepositoryParses(t *testing.T) {
	t.Parallel()

	requireTool(t, "bash")
	shell := repositoryMembers(t, ShellClass)
	// The scripts and the status tool are the shell this project has; a census
	// that found none of it would report nothing wrong with all of it.
	if !slices.Contains(shell, "scripts/cut-release.sh") || !slices.Contains(shell, "bin/yoyo-status") {
		t.Fatalf("the shell this repository carries was not found; the census collected %v", shell)
	}
	for _, problem := range ShellSyntax(repositoryRoot, shell) {
		t.Errorf("a shell file in this repository will not parse: %s", problem)
	}
}

func TestEveryStructuredFileInThisRepositoryDecodes(t *testing.T) {
	t.Parallel()

	structured := append(repositoryMembers(t, "yaml"), repositoryMembers(t, "json")...)
	if len(structured) == 0 {
		t.Fatal("no YAML or JSON was found in this repository; the census is looking in the wrong place")
	}
	for _, problem := range StructuredData(repositoryRoot, structured) {
		t.Errorf("a structured file in this repository does not decode: %s", problem)
	}
}

// TestEveryWorkflowInThisRepositoryIsShapedLikeOne covers the content class with
// the longest gap between writing it and running it. The release workflow is
// triggered by a tag push, so what is wrong with it is discovered during a
// publication — which is the one moment a botched workflow costs the most.
func TestEveryWorkflowInThisRepositoryIsShapedLikeOne(t *testing.T) {
	t.Parallel()

	workflows := Workflows(repositoryFiles(t))
	if len(workflows) == 0 {
		t.Fatalf("no workflow was found under %s; this repository has two", WorkflowDirectory)
	}
	for _, problem := range WorkflowShape(repositoryRoot, workflows) {
		t.Errorf("a workflow in this repository is not shaped like one: %s", problem)
	}
}

// TestTheStatusToolDelegatesAsItClaims runs the status tool's suite, for the
// same reason the release verb's and the notes writer's suites are run from Go:
// the tool is shell, what it does with the old name is what the operations guide
// tells an operator they can rely on, and a suite that only runs in CI is one
// whose failure arrives after the change was reviewed and integrated.
func TestTheStatusToolDelegatesAsItClaims(t *testing.T) {
	t.Parallel()

	requireTool(t, "bash")
	suite := exec.Command("bash", statusToolSuitePath)
	report, err := suite.CombinedOutput()
	if err != nil {
		t.Fatalf("%s did not pass (%v):\n%s", statusToolSuitePath, err, report)
	}
}

// repositoryFiles is the census of this checkout.
func repositoryFiles(t *testing.T) []string {
	t.Helper()

	requireTool(t, "git")
	files, err := Files(repositoryRoot)
	if err != nil {
		t.Fatalf("Files() error = %v", err)
	}
	// A census that found nothing would report a repository with nothing wrong
	// with it, which is the one way every gate here passes while checking none of
	// what it is for.
	if !slices.Contains(files, "Makefile") || !slices.Contains(files, "go.mod") {
		t.Fatalf("the census did not find this repository; it collected %d files", len(files))
	}
	return files
}

// repositoryMembers is the files of one content class in this checkout.
func repositoryMembers(t *testing.T, class string) []string {
	t.Helper()

	members, _, err := Classify(repositoryRoot, Classes, repositoryFiles(t))
	if err != nil {
		t.Fatalf("Classify() error = %v", err)
	}
	return members[class]
}
