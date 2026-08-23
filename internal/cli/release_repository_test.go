package cli

// The tests here hold the release verb — scripts/cut-release.sh — to two things
// the Go checks could not otherwise see.
//
// The verb is shell, so until this file nothing in `make check` executed it: its
// own suite ran only as a separate CI step, and a separate step is something
// somebody reads a red X on a day later than the one they broke it. The first
// test runs that suite from here, so the verb is exercised by the same `make
// test` as everything else, on the machine of whoever changed it.
//
// The second holds the verb's list of the tracker's derived exports to the list
// a run declares, in the one direction the two actually imply. They answer
// different questions — what a release cut may commit on the operator's behalf,
// and what a run may tolerate the primary checkout acquiring — and it is the
// first that has to sit inside the second.

import (
	"os"
	"os/exec"
	"regexp"
	"slices"
	"testing"
)

// cutReleaseTestPath is the verb's own suite. It fabricates scratch
// repositories with a stub walkthrough and a stub Makefile and executes every
// refusal against them, so it needs no network, no provider, and no
// cross-compile — which is what makes it cheap enough to run from here.
const cutReleaseTestPath = "../../scripts/cut-release-test.sh"

// cutReleaseScriptPath is the verb itself.
const cutReleaseScriptPath = "../../scripts/cut-release.sh"

// runSourcePath is where a run declares what the primary checkout may acquire
// while it works, which is the wider of the two lists.
const runSourcePath = "run.go"

var derivedExportsPattern = regexp.MustCompile(`(?m)^derived_exports=\(([^)]*)\)`)

var allowedPrimaryChangesPattern = regexp.MustCompile(`AllowedPrimaryChanges:\s*\[\]string\{([^}]*)\}`)

var quotedPathPattern = regexp.MustCompile(`"([^"]*)"`)

// TestTheReleaseVerbRefusesWhatItShould runs the verb's suite. The refusals are
// the whole value of a release gate, and a refusal nobody exercises is a
// refusal nobody has — least of all under a delegated daily cadence, where the
// first person to find out is whoever the cut did not happen for.
func TestTheReleaseVerbRefusesWhatItShould(t *testing.T) {
	t.Parallel()

	for _, tool := range []string{"bash", "git", "make"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s needs %s, which is not on PATH", cutReleaseTestPath, tool)
		}
	}
	suite := exec.Command("bash", cutReleaseTestPath)
	// The suite runs make against the repositories it fabricates. Reached
	// through `make test`, those invocations would otherwise inherit this
	// make's jobserver, which is not theirs to join.
	suite.Env = append(os.Environ(), "MAKEFLAGS=", "MFLAGS=", "MAKELEVEL=0")
	report, err := suite.CombinedOutput()
	if err != nil {
		t.Fatalf("%s did not pass (%v):\n%s", cutReleaseTestPath, err, report)
	}
}

// TestTheReleaseVerbHousekeepsOnlyExportsARunAllows holds the cut's list inside
// the run's. A path the cut commits on the operator's behalf, after every gate
// has passed and with no chance for anybody to look at it, has to be one this
// repository already treats as churn a run may find under it — otherwise the
// cut is committing something nothing else considers derived. The containment
// is deliberately one-way: a run may come to tolerate a path the cut has no
// business committing, and that is a widening rather than a disagreement.
func TestTheReleaseVerbHousekeepsOnlyExportsARunAllows(t *testing.T) {
	t.Parallel()

	verb := releaseQuotedPaths(t, derivedExportsPattern,
		releaseFileText(t, cutReleaseScriptPath), cutReleaseScriptPath, "derived_exports=(...)")
	declared := releaseQuotedPaths(t, allowedPrimaryChangesPattern,
		releaseFileText(t, runSourcePath), runSourcePath, "AllowedPrimaryChanges: []string{...}")
	if len(verb) == 0 {
		t.Fatalf("%s names no derived exports, which is not a state this check could be right about",
			cutReleaseScriptPath)
	}
	for _, path := range verb {
		if !slices.Contains(declared, path) {
			t.Errorf("%s housekeeps %q, which %s does not declare among %v; the cut may only commit paths a run already treats as churn",
				cutReleaseScriptPath, path, runSourcePath, declared)
		}
	}
}

// releaseFileText reads one file of this checkout, reached from the package the
// test lives in.
func releaseFileText(t *testing.T, path string) string {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	return string(content)
}

// releaseQuotedPaths pulls the quoted paths out of the declaration pattern
// names, sorted, so what is compared is the set rather than the order two
// languages happen to spell it in.
func releaseQuotedPaths(t *testing.T, pattern *regexp.Regexp, content, path, shape string) []string {
	t.Helper()

	declaration := pattern.FindStringSubmatch(content)
	if declaration == nil {
		t.Fatalf("%s no longer holds a %s, so the two lists cannot be held to each other", path, shape)
	}
	var paths []string
	for _, quoted := range quotedPathPattern.FindAllStringSubmatch(declaration[1], -1) {
		paths = append(paths, quoted[1])
	}
	slices.Sort(paths)
	return paths
}
