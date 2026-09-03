package conformance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/goal"
)

func TestAConformingRepositoryProducesNoMismatch(t *testing.T) {
	t.Parallel()
	repository := fixture(t)
	assessment := assess(t, gather(repository, admitted("serving the chain")...))

	for _, finding := range assessment.Findings() {
		if finding.Diverges() {
			t.Fatalf("%s diverged: %q, %v", finding.Step, finding.Summary, finding.Mismatches)
		}
	}
	if !assessment.Conforms() {
		t.Fatal("a repository with nothing wrong in it does not conform")
	}
}

func TestADocumentThatIsNotAnArtifactRefusesTheTag(t *testing.T) {
	t.Parallel()
	repository := fixture(t)
	write(t, repository, "docs/designs/half-written.md", "# A design with no identity\n\nNothing above this says what it is.\n")

	finding := findingFor(t, assess(t, gather(repository)).Findings(), CheckArtifacts)
	if !finding.Diverges() {
		t.Fatalf("the artifacts check did not diverge: %q", finding.Summary)
	}
	if !mentions(finding.Mismatches, "half-written.md") {
		t.Fatalf("the mismatch does not name the document: %v", finding.Mismatches)
	}
}

func TestALinkThatResolvesToNothingRefusesTheTag(t *testing.T) {
	t.Parallel()
	repository := fixture(t)
	write(t, repository, "docs/guide.md", "# Guide\n\nSee [the design](designs/gone.md).\n")

	finding := findingFor(t, assess(t, gather(repository)).Findings(), CheckReferences)
	if !finding.Diverges() {
		t.Fatalf("the references check did not diverge: %q", finding.Summary)
	}
	if !mentions(finding.Mismatches, "docs/guide.md") {
		t.Fatalf("the mismatch does not name the document the link is written in: %v", finding.Mismatches)
	}
}

func TestAFileInTheInvariantsHomeThatIsNotOneRefusesTheTag(t *testing.T) {
	t.Parallel()
	repository := fixture(t)
	write(t, repository, "docs/decisions/invariants/nested/buried.md", "# Buried\n")

	finding := findingFor(t, assess(t, gather(repository)).Findings(), CheckInvariants)
	if !finding.Diverges() {
		t.Fatalf("the invariants check did not diverge: %q", finding.Summary)
	}
	if !mentions(finding.Mismatches, "buried.md") {
		t.Fatalf("the mismatch does not name the file: %v", finding.Mismatches)
	}
}

func TestWorkNamingAGoalTheGoalsDoNotStateRefusesTheTag(t *testing.T) {
	t.Parallel()
	repository := fixture(t)
	sources := gather(repository, admitted("chasing something else entirely")...)

	finding := findingFor(t, assess(t, sources).Findings(), CheckGoals)
	if !finding.Diverges() {
		t.Fatalf("the goals check did not diverge: %q", finding.Summary)
	}
	if !mentions(finding.Mismatches, "item-1") {
		t.Fatalf("the mismatch does not name the item: %v", finding.Mismatches)
	}
}

func TestWorkAdmittedBeforeAttributionsWereCheckedIsGrandfathered(t *testing.T) {
	t.Parallel()
	repository := fixture(t)
	// No goal in its notes and no witness that one was ever there: work that
	// predates the check rather than work whose attribution is wrong. The audit
	// grandfathers it and so does the gate, or a release would be refused until a
	// backlog nobody has had the chance to attribute is attributed.
	sources := gather(repository, beads.WorkItem{ID: "item-1", Title: "older than the check", Status: "open"})

	finding := findingFor(t, assess(t, sources).Findings(), CheckGoals)
	if finding.Diverges() {
		t.Fatalf("an unattributed item refused the tag: %v", finding.Mismatches)
	}
}

func TestATrackerNobodyCouldReadRefusesTheTagRatherThanPassing(t *testing.T) {
	t.Parallel()
	repository := fixture(t)
	sources := Gather(repository, product(), nil, "bd list failed: the store is locked")

	finding := findingFor(t, assess(t, sources).Findings(), CheckGoals)
	if !finding.Diverges() {
		t.Fatalf("a gate that checked no attribution at all reported %q", finding.Summary)
	}
	if !mentions(finding.Mismatches, "the store is locked") {
		t.Fatalf("the mismatch does not say why the tracker could not be read: %v", finding.Mismatches)
	}
}

func TestGoalsThatCouldNotBeReadRefuseTheTag(t *testing.T) {
	t.Parallel()
	repository := t.TempDir()
	// An artifact home with nothing in it: no goals are recorded, so nothing any
	// item claims can be checked against anything.
	sources := gather(repository, admitted("serving the chain")...)

	finding := findingFor(t, assess(t, sources).Findings(), CheckGoals)
	if !finding.Diverges() {
		t.Fatalf("a gate with no goals to check against reported %q", finding.Summary)
	}
}

func TestStalenessIsReportedAndNeverRefusesTheTag(t *testing.T) {
	t.Parallel()
	repository := fixture(t)
	// The brief is amended after the goals last spoke, which is what leaves the
	// goals document downstream of a change nobody has answered.
	write(t, repository, "docs/product/brief.md", brief(
		revision("created", "2026-08-01T00:00:00Z", "recorded when identity arrived"),
		revision("amended", "2026-08-20T00:00:00Z", "the brief now says who it is for")))

	result := run(t, repository, admitted("serving the chain")...)
	finding := findingFor(t, result.Findings, CheckStaleness)
	if finding.Outcome != OutcomeNoted {
		t.Fatalf("staleness produced %q rather than %q", finding.Outcome, OutcomeNoted)
	}
	if len(finding.Mismatches) != 0 {
		t.Fatalf("staleness produced mismatches, which would make an amendment something to avoid: %v", finding.Mismatches)
	}
	if !mentions(finding.Notes, "v1-goals") {
		t.Fatalf("staleness did not report the document that is downstream of the amendment: %v", finding.Notes)
	}
	if !result.Conforms {
		t.Fatal("staleness alone refused the tag")
	}
}

func TestAMismatchStopsTheSequenceAndNamesWhatItFound(t *testing.T) {
	t.Parallel()
	repository := fixture(t)
	write(t, repository, "docs/guide.md", "# Guide\n\nSee [the design](designs/gone.md).\n")

	result := run(t, repository, admitted("serving the chain")...)
	if result.Conforms || result.Terminal != TerminalMismatch {
		t.Fatalf("the run ended in %q and conforms = %t", result.Terminal, result.Conforms)
	}
	// The references check is the second state, so the sequence stopped there:
	// the checks after it never ran, and the one that found something reported
	// the whole of its own finding.
	steps := make([]string, 0, len(result.Findings))
	for _, finding := range result.Findings {
		steps = append(steps, finding.Step)
	}
	if want := []string{CheckArtifacts, CheckReferences}; strings.Join(steps, ",") != strings.Join(want, ",") {
		t.Fatalf("the sequence walked %v, want it to stop at the first divergence: %v", steps, want)
	}
	if !mentions(result.Mismatches(), CheckReferences+": docs/guide.md") {
		t.Fatalf("the refusal does not name the step and the document: %v", result.Mismatches())
	}
}

func TestOutcomeRefusesAStepThatRecordedNothing(t *testing.T) {
	t.Parallel()
	assessment := New(Sources{})
	if _, err := Outcome(CheckArtifacts, assessment); err == nil {
		t.Fatal("a state whose check recorded nothing was handed an outcome")
	}
	// A finding is read once. The step after the one it belongs to is refused
	// rather than handed it again, which is what would send an instance somewhere
	// on the strength of the step before it.
	assessment.record(CheckArtifacts, "read", nil, nil)
	if _, err := Outcome("first", assessment); err != nil {
		t.Fatalf("Outcome() error = %v", err)
	}
	if _, err := Outcome("second", assessment); err == nil {
		t.Fatal("the outcome of the step before was reused for the step after it")
	}
	// And the state that performed it is what the finding now carries, whatever
	// the check calls itself.
	if finding := assessment.Findings()[0]; finding.Step != "first" || finding.Check != CheckArtifacts {
		t.Fatalf("the finding records step %q and check %q", finding.Step, finding.Check)
	}
}

func TestOnlyMismatchesAreListedAndTheRestAreCounted(t *testing.T) {
	t.Parallel()
	assessment := New(Sources{})
	mismatches := make([]string, maxReportedMismatches+3)
	for index := range mismatches {
		mismatches[index] = "a mismatch"
	}
	assessment.record(CheckArtifacts, "read", mismatches, nil)

	finding := assessment.Findings()[0]
	if len(finding.Mismatches) != maxReportedMismatches || finding.Truncated != 3 {
		t.Fatalf("listed %d and counted %d", len(finding.Mismatches), finding.Truncated)
	}
}

// findingFor is one step's finding, from an assessment or a result.
// findingFor is one check's finding. It matches on the check rather than on the
// state, because the state is whatever a definition called it and the check is
// what this package knows.
func findingFor(t *testing.T, findings []Finding, check string) Finding {
	t.Helper()
	for _, finding := range findings {
		if finding.Check == check {
			return finding
		}
	}
	t.Fatalf("the sequence recorded no finding for the %q check; it recorded %v", check, checks(findings))
	return Finding{}
}

// steps is the states a sequence walked, as its findings record them.
func steps(findings []Finding) []string {
	named := make([]string, 0, len(findings))
	for _, finding := range findings {
		named = append(named, finding.Step)
	}
	return named
}

// checks is which check each finding came from, which is the same list only
// while a definition names its states after them.
func checks(findings []Finding) []string {
	named := make([]string, 0, len(findings))
	for _, finding := range findings {
		named = append(named, finding.Check)
	}
	return named
}

// assess runs every check directly, in the order the definition puts them in,
// and reports what each found. It is what the checks' own claims are made
// against; that the definition really sequences them this way is the workflow
// suite's claim.
func assess(t *testing.T, sources Sources) *Assessment {
	t.Helper()
	assessment := New(sources)
	for _, check := range []func() error{
		assessment.checkArtifacts,
		assessment.checkReferences,
		assessment.checkInvariants,
		assessment.checkGoals,
		assessment.surveyStaleness,
	} {
		if err := check(); err != nil {
			t.Fatalf("a check failed rather than reporting what it found: %v", err)
		}
	}
	return assessment
}

func gather(repository string, admitted ...beads.WorkItem) Sources {
	return Gather(repository, product(), admitted, "")
}

func product() config.Product {
	return config.Product{
		Repository:     ".",
		Specifications: "docs/product",
		Designs:        "docs/designs",
		Decisions:      "docs/decisions",
		Invariants:     "docs/decisions/invariants",
	}
}

// admitted is one admitted work item recording the goal it serves, written the
// way the harness writes it.
func admitted(statement string) []beads.WorkItem {
	return []beads.WorkItem{{
		ID:     "item-1",
		Title:  "one admitted item",
		Status: "open",
		Notes:  "Admitted by the product manager.\n\n" + goal.Note(statement),
	}}
}

func mentions(lines []string, wanted string) bool {
	for _, line := range lines {
		if strings.Contains(line, wanted) {
			return true
		}
	}
	return false
}

// fixture is a repository that conforms: a brief, goals that trace to it, a
// design that traces to the goals, one invariant, and nothing linking anywhere
// it should not.
func fixture(t *testing.T) string {
	t.Helper()
	repository := t.TempDir()
	write(t, repository, "docs/product/brief.md", brief(revision("created", "2026-08-01T00:00:00Z", "recorded when identity arrived")))
	write(t, repository, "docs/product/goals/v1-goals.md", frontmatter("v1-goals", "goals", []string{"brief"},
		revision("created", "2026-08-10T00:00:00Z", "recorded when identity arrived"))+`
# Goals

## Goals

- serving the chain
  *Supports: intent goes in and merged software comes out.*
`)
	write(t, repository, "docs/designs/a-design.md", frontmatter("a-design", "design", []string{"v1-goals"},
		revisionBy("architect", "created", "2026-08-11T00:00:00Z", "recorded when identity arrived"))+
		"\n# A design\n\nIt serves [the goals](../product/goals/v1-goals.md).\n")
	write(t, repository, "docs/decisions/invariants/nothing-is-promoted-unjudged.md", `---
id: nothing-is-promoted-unjudged
title: Nothing is promoted unjudged
status: active
established_by:
    - item-1
revisions:
    - action: created
      by: architect
      at: 2026-08-12T00:00:00Z
      reason: recorded when the fixture was written
---

## Must hold

No change reaches the target branch without a verdict on it.

## Why

A change promoted on nobody's verdict is a change nobody agreed to.
`)
	return repository
}

func brief(revisions ...string) string {
	return frontmatter("brief", "brief", nil, revisions...) + `
# Brief

## Goals

- **Intent goes in and merged software comes out.** A person says what they want
  and receives working software.
`
}

func frontmatter(id, kind string, supports []string, revisions ...string) string {
	var rendered strings.Builder
	rendered.WriteString("---\nid: " + id + "\nkind: " + kind + "\ntitle: " + id + "\n")
	if len(supports) > 0 {
		rendered.WriteString("supports:\n")
		for _, reference := range supports {
			rendered.WriteString("    - " + reference + "\n")
		}
	}
	rendered.WriteString("status: active\nrevisions:\n")
	for _, entry := range revisions {
		rendered.WriteString(entry)
	}
	rendered.WriteString("---\n")
	return rendered.String()
}

func revision(action, at, reason string) string {
	return revisionBy("product-manager", action, at, reason)
}

func revisionBy(role, action, at, reason string) string {
	return "    - action: " + action + "\n      by: " + role +
		"\n      at: " + at + "\n      reason: " + reason + "\n"
}

func write(t *testing.T, repository, relative, content string) {
	t.Helper()
	path := filepath.Join(repository, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}
