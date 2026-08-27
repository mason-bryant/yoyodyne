package goal

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/config"
)

const goalsHome = "docs/product"

func TestTheGoalsAGoalsDocumentStatesAreWhatWorkCanBeAttributedTo(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	// The shape Yoyodyne's own goals are in: an introduction, a `Goals` heading,
	// and one entry per goal with prose underneath saying what it supports.
	write(t, root, "docs/product/goals/v1-goals.md", `---
id: v1-goals
---

# V1 goals

These are the outcomes the first version is built to reach.

## Goals

- Maintain a traceable chain from the product brief through goals, designs, work, code changes, and verification.
  *Supports: every change traces to intent somebody approved.*
- Isolate implementation tasks in harness-managed Git worktrees.
  *Supports: intent goes in and merged software comes out.*
`)

	set := Collect(root, setOf(recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md")))
	if len(set.Problems) != 0 {
		t.Fatalf("problems = %v", set.Problems)
	}
	if statements := stated(set.Goals); len(statements) != 2 {
		t.Fatalf("goals = %q", statements)
	}
	first := set.Goals[0]
	// The document that states a goal travels with it: knowing the words matched
	// is not knowing which document they were agreed in.
	if first.ArtifactID != "v1-goals" || first.Path != "docs/product/goals/v1-goals.md" || !first.InForce {
		t.Fatalf("goal = %#v", first)
	}
	// The prose under an entry describes the goal above it rather than being a
	// second goal.
	if strings.Contains(first.Statement, "Supports:") {
		t.Fatalf("goal statement swallowed the prose under it: %q", first.Statement)
	}
}

func TestAGoalIsResolvedThroughWordingThatDiffersOnlyInHowItWasTyped(t *testing.T) {
	t.Parallel()

	set := setWithGoals(t, "Maintain a traceable chain from the brief through to verification.")
	for _, named := range []string{
		"Maintain a traceable chain from the brief through to verification.",
		"maintain a traceable chain from the brief through to verification",
		"  Maintain a traceable chain   from the brief through to  verification  ",
	} {
		attribution := set.Attribute(named)
		if !attribution.Resolved() {
			t.Fatalf("Attribute(%q) = %#v", named, attribution)
		}
		if attribution.Goal.ArtifactID != "v1-goals" {
			t.Fatalf("Attribute(%q) resolved to %#v", named, attribution.Goal)
		}
	}
}

func TestAGoalNoDocumentStatesIsUnresolvedRatherThanApproximatelyRight(t *testing.T) {
	t.Parallel()

	set := setWithGoals(t, "Maintain a traceable chain from the brief through to verification.")
	// One word different is a different claim. Deciding it was near enough is
	// exactly the inference an attribution that resolves is supposed to replace.
	attribution := set.Attribute("Maintain a traceable chain from the brief through to review.")
	if attribution.State != StateUnresolved {
		t.Fatalf("attribution = %#v", attribution)
	}
	if !strings.Contains(attribution.Reason, "v1-goals") {
		t.Fatalf("reason does not say where it looked: %q", attribution.Reason)
	}
	if attribution.Named == "" {
		t.Fatalf("attribution does not carry what was named: %#v", attribution)
	}
}

func TestAGoalStatedOnlyByADocumentNoLongerInForceIsRefusedWithTheReasonNamed(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	write(t, root, "docs/product/goals/v0-goals.md", goalsDocument("Ship the prototype by the end of the quarter."))
	write(t, root, "docs/product/goals/v1-goals.md", goalsDocument("Maintain a traceable chain from the brief through to verification."))
	set := Collect(root, setOf(
		recorded("v0-goals", artifact.KindGoals, artifact.StatusSuperseded, "docs/product/goals/v0-goals.md"),
		recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md"),
	))

	attribution := set.Attribute("Ship the prototype by the end of the quarter.")
	// Not the same answer as a goal nobody ever wrote: this one was agreed and
	// then replaced, and whoever attributed work to it needs to be told which.
	if attribution.State != StateUnresolved {
		t.Fatalf("attribution = %#v", attribution)
	}
	if !strings.Contains(attribution.Reason, "v0-goals") || !strings.Contains(attribution.Reason, "no longer in force") {
		t.Fatalf("reason = %q", attribution.Reason)
	}
}

func TestAnAttributionIsUncheckableRatherThanWrongWhenThereAreNoGoalsToCheckIt(t *testing.T) {
	t.Parallel()

	// A repository that has not written its goals down yet, and one whose goals
	// could not be read, are different things to be told and neither of them is
	// "this attribution is wrong".
	empty := Set{}.Attribute("Maintain a traceable chain.")
	if empty.State != StateUncheckable || !strings.Contains(empty.Reason, "records no goals artifact") {
		t.Fatalf("attribution = %#v", empty)
	}
	unreadable := Unreadable("the artifact homes are outside the repository").Attribute("Maintain a traceable chain.")
	if unreadable.State != StateUncheckable || !strings.Contains(unreadable.Reason, "outside the repository") {
		t.Fatalf("attribution = %#v", unreadable)
	}
}

func TestAnItemThatRecordsNoGoalIsUnattributedRatherThanWrong(t *testing.T) {
	t.Parallel()

	set := setWithGoals(t, "Maintain a traceable chain from the brief through to verification.")
	attribution := set.AttributionOf("Admitted to the backlog by the product manager.\n\nReason: the operator asked for it.", Witness{})
	// Work admitted before attributions were checked says nothing, and nothing is
	// not a false claim. Telling the two apart is what lets legacy work be
	// grandfathered without also excusing an attribution that is wrong.
	if attribution.State != StateUnattributed {
		t.Fatalf("attribution = %#v", attribution)
	}
	if attribution.Named != "" {
		t.Fatalf("attribution names something the item did not: %#v", attribution)
	}
}

func TestAnItemWitnessedToHaveHadAGoalHasLostItRatherThanNeverHadOne(t *testing.T) {
	t.Parallel()

	statement := "Maintain a traceable chain from the brief through to verification."
	set := setWithGoals(t, statement)
	// The same notes an item has after something replaced them: the provenance
	// and the goal that were written at creation are gone, and what is left says
	// nothing about either. Read against the tracker's witness that a goal was
	// written here, that is a record destroyed rather than a record never made —
	// and it must not land in the one state that is deliberately not failed.
	replaced := "Constraints from the architect, recorded 2026-08-19."
	lost := set.AttributionOf(replaced, Witness{Recorded: true, Statement: statement})
	if lost.State != StateLost {
		t.Fatalf("attribution = %#v", lost)
	}
	if !strings.Contains(lost.Reason, "written over") {
		t.Fatalf("the reason does not say what happened: %#v", lost)
	}
	// The words the tracker kept are what a restoration puts back, so they are
	// carried rather than left for somebody to find: an item told only that it
	// lost a goal is an item somebody has to go and re-derive one for.
	if lost.Recorded != statement || !strings.Contains(lost.Reason, statement) {
		t.Fatalf("the lost attribution does not say which goal to put back: %#v", lost)
	}
	// It is still not an answer. The state is decided from the notes, so an item
	// whose notes lost their goal reads as lost however much the tracker
	// remembers — a loss that resolved out of the metadata would report as intact
	// while the item stayed empty.
	if lost.Resolved() || lost.Goal.Statement != "" {
		t.Fatalf("the witness answered for the item: %#v", lost)
	}
	// A witness from before the words were kept says what it knows and no more.
	bare := set.AttributionOf(replaced, Witness{Recorded: true})
	if bare.State != StateLost || bare.Recorded != "" {
		t.Fatalf("attribution = %#v", bare)
	}
	if !strings.Contains(bare.Reason, "does not hold which goal") {
		t.Fatalf("the reason claims a record the tracker does not have: %#v", bare)
	}
	// The same notes with no witness are the legacy item they look like, which is
	// what keeps the check from failing a backlog nobody has attributed yet.
	if unwitnessed := set.AttributionOf(replaced, Witness{}); unwitnessed.State != StateUnattributed {
		t.Fatalf("attribution = %#v", unwitnessed)
	}
	// A witness on an item that still carries its goal changes nothing: the goal
	// in the notes is the attribution, and the witness only ever says what was
	// written.
	kept := set.AttributionOf(replaced+"\n\n"+Note(statement), Witness{Recorded: true, Statement: statement})
	if !kept.Resolved() {
		t.Fatalf("attribution = %#v", kept)
	}
}

func TestTheNewestAttributionOnAnItemIsTheOneThatCounts(t *testing.T) {
	t.Parallel()

	set := setWithGoals(t, "Maintain a traceable chain from the brief through to verification.")
	// An item acquires an attribution by having one appended to notes nothing
	// rewrites, so the record holds both the wrong one and the correction.
	notes := strings.Join([]string{
		"Admitted to the backlog by the product manager.",
		"",
		Note("Ship the prototype by the end of the quarter."),
		"",
		"Attributed to a goal by the product manager.",
		"",
		Note("Maintain a traceable chain from the brief through to verification."),
	}, "\n")
	attribution := set.AttributionOf(notes, Witness{Recorded: true})
	if !attribution.Resolved() {
		t.Fatalf("attribution = %#v", attribution)
	}
	if attribution.Named != "Maintain a traceable chain from the brief through to verification." {
		t.Fatalf("attribution = %#v", attribution)
	}
}

func TestWhatIsWrittenAsAnAttributionIsWhatIsReadBack(t *testing.T) {
	t.Parallel()

	statement := "Maintain a traceable chain from the brief through to verification."
	named, found := NamedIn("some provenance\n\n" + Note(statement))
	if !found || named != statement {
		t.Fatalf("NamedIn() = %q, %v", named, found)
	}
}

func TestADocumentThatStatesNoGoalsIsReportedRatherThanReadAsFewerGoals(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	// A non-goals document states its content under its own heading. It is a
	// goals artifact by kind in no repository that files it correctly, but a
	// document whose goals cannot be found must be named either way: a set that
	// silently shrank is a set that starts refusing correct attributions.
	write(t, root, "docs/product/goals/v1-goals.md", "# V1 goals\n\nThe goals are still to be written.\n")
	set := Collect(root, setOf(recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md")))
	if len(set.Goals) != 0 || len(set.Problems) != 1 {
		t.Fatalf("set = %#v", set)
	}
	if !strings.Contains(set.Problems[0].Reason, "`Goals` heading") {
		t.Fatalf("problem = %v", set.Problems[0])
	}
	// The document was still read, so the set says where it looked.
	if len(set.Sources) != 1 || set.Sources[0] != "v1-goals" {
		t.Fatalf("sources = %v", set.Sources)
	}
}

func TestAGoalsSectionIsReadToItsEndAndNoFurther(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	write(t, root, "docs/product/goals/v1-goals.md", `# V1 goals

An introduction.

## Goals

- Maintain a traceable chain.

### Goals added after the brief was written

- Review every change independently.

## Non-goals

- Support every provider.

`+"```"+`
- Not a goal; a fenced example.
`+"```"+`
`)

	set := Collect(root, setOf(recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md")))
	statements := stated(set.Goals)
	// A heading below the goals divides them rather than ending them; a heading
	// at the same level ends the section, so a non-goal is never collected as
	// something work can be attributed to.
	if len(statements) != 2 || statements[0] != "Maintain a traceable chain." || statements[1] != "Review every change independently." {
		t.Fatalf("goals = %q", statements)
	}
}

func TestANonGoalIsNeverReadAsSomethingWorkMayServe(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	// The case a level test alone cannot catch: the document's title is the
	// goals heading, so everything after it is nested below level 1 and no
	// heading can end the section by level. Filed this way, every non-goal would
	// be collected as a goal work may be admitted under — the opposite of what
	// the document says, and worse than reading no goals at all.
	write(t, root, "docs/product/goals/v1-goals.md", `# Goals

- Maintain a traceable chain.

## Non-goals

- Support every provider.
- Replace the operator's judgement.
`)

	set := Collect(root, setOf(recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md")))
	if statements := stated(set.Goals); len(statements) != 1 || statements[0] != "Maintain a traceable chain." {
		t.Fatalf("goals = %q", statements)
	}
	if set.Attribute("Support every provider.").State != StateUnresolved {
		t.Fatalf("a non-goal resolved as a goal work may serve")
	}
}

func TestATitleThatOpensWithTheWordIsATitleRatherThanTheGoalsSection(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	// `# Goals for V1` is what the document is called. Reading it as the section
	// would open the goals at level 1 and collect every top-level entry below,
	// whatever heading it was written under.
	write(t, root, "docs/product/goals/v1-goals.md", `# Goals for V1

An introduction.

## Goals

- Maintain a traceable chain.

## Open questions

- Whether to support a second tracker.
`)

	set := Collect(root, setOf(recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md")))
	if statements := stated(set.Goals); len(statements) != 1 || statements[0] != "Maintain a traceable chain." {
		t.Fatalf("goals = %q", statements)
	}

	// A document with no such section at all states no goals, and is reported
	// rather than read as though its title were the heading.
	titleOnly := newRepository(t)
	write(t, titleOnly, "docs/product/goals/v1-goals.md", "# Goals for V1\n\n- Maintain a traceable chain.\n")
	reported := Collect(titleOnly, setOf(recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md")))
	if len(reported.Goals) != 0 || len(reported.Problems) != 1 {
		t.Fatalf("set = %#v", reported)
	}
	if !strings.Contains(reported.Problems[0].Reason, "whole text is `Goals`") {
		t.Fatalf("problem = %v", reported.Problems[0])
	}
}

func TestOnlyTopLevelEntriesAreGoals(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	write(t, root, "docs/product/goals/v1-goals.md", `# V1 goals

An introduction.

## Goals

- Maintain a traceable chain.
  - which includes the tracker
  *Supports: every change traces to intent somebody approved.*
`)

	set := Collect(root, setOf(recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md")))
	if statements := stated(set.Goals); len(statements) != 1 || statements[0] != "Maintain a traceable chain." {
		t.Fatalf("goals = %q", statements)
	}
}

func TestAGoalHardWrappedAcrossLinesIsRecordedWhole(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	// Markdown is normally hard-wrapped, and a goal written that way is the
	// ordinary case rather than the odd one. Recording only its first physical
	// line would record a fragment of every goal in a repository whose editor
	// wraps, and the attribution naming the words the document states would then
	// resolve to nothing.
	write(t, root, "docs/product/goals/v1-goals.md", `# V1 goals

An introduction.

## Goals

- Run development nearly autonomously. The human's routine interface is the
  product manager: they state intent, approve the brief and goals, and answer
  questions the product manager escalates.
  *Supports: the human's attention goes only where it is needed.*
- Maintain a traceable chain.
`)

	set := Collect(root, setOf(recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md")))
	if len(set.Problems) != 0 {
		t.Fatalf("problems = %v", set.Problems)
	}
	whole := "Run development nearly autonomously. The human's routine interface is the product manager: they state intent, approve the brief and goals, and answer questions the product manager escalates."
	statements := stated(set.Goals)
	// The wrapping is how the document was typed, not what it says: the entry
	// states one goal, and the entry after it is still a second one.
	if len(statements) != 2 || statements[0] != whole || statements[1] != "Maintain a traceable chain." {
		t.Fatalf("goals = %q", statements)
	}
	// The trailer names what the goal supports upstream. Joined into the
	// statement it would corrupt the goal exactly as truncation does.
	if strings.Contains(statements[0], "Supports:") {
		t.Fatalf("goal statement swallowed the trailer under it: %q", statements[0])
	}
	if !set.Attribute(whole).Resolved() {
		t.Fatalf("the whole statement the document makes does not resolve: %#v", set.Attribute(whole))
	}
	// The fragment is not a goal the document states, and reading it as one is
	// what let a truncated goal look like it resolved.
	if set.Attribute("Run development nearly autonomously. The human's routine interface is the").State != StateUnresolved {
		t.Fatalf("a fragment of a wrapped goal resolved as a goal")
	}
}

func TestAGoalHardWrappedAcrossLinesIsReportedThoughItIsStillRecorded(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	// Rejoining the wrap is a reading of the file rather than something the file
	// says. The goal it produces is what an attribution has to match word for
	// word, so a goal that only exists once the wrap is rejoined can be changed
	// into a different goal by an edit that changes none of its words.
	write(t, root, "docs/product/goals/v1-goals.md", frontmatter("v1-goals", "goals", "V1 goals", []string{"brief"})+`
# V1 goals

An introduction.

## Goals

- Run development nearly autonomously. The human's routine interface is the
  product manager: they state intent, approve the brief and goals, and answer
  questions the product manager escalates.
  *Supports: the human's attention goes only where it is needed.*
- Maintain a traceable chain.
  *Supports: every change traces to intent somebody approved.*
`)

	set := Collect(root, setOf(recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md")))
	if len(set.Problems) != 0 {
		t.Fatalf("problems = %v", set.Problems)
	}
	// Nothing is dropped over one: the wrapped goal is still a goal work can be
	// attributed to, exactly as a goal with a broken link upstream is.
	if statements := stated(set.Goals); len(statements) != 2 {
		t.Fatalf("goals = %q", statements)
	}
	if len(set.WrapProblems) != 1 {
		t.Fatalf("wrap problems = %v", set.WrapProblems)
	}
	problem := set.WrapProblems[0]
	if problem.Lines != 3 || problem.ArtifactID != "v1-goals" || problem.Path != "docs/product/goals/v1-goals.md" {
		t.Fatalf("wrap problem = %#v", problem)
	}
	// The line is counted from the top of the file rather than from the end of
	// the frontmatter, because what is reported has to name a place to open.
	if got := lineOf(t, root, "docs/product/goals/v1-goals.md", problem.Line); !strings.HasPrefix(got, "- Run development nearly autonomously.") {
		t.Fatalf("line %d is %q, want the entry the goal opens on", problem.Line, got)
	}
	if problem.Statement != set.Goals[0].Statement {
		t.Fatalf("wrap problem names %q, want the goal as it was rejoined", problem.Statement)
	}
}

func TestAGoalOnOneLineIsNotReportedForWhatIsWrittenUnderIt(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	// The trailer, a wrapped trailer, and a paragraph about the goal are all
	// written under the entry and none of them is the statement. Counting any of
	// them as a wrap would report every goal in this repository, which is a check
	// nobody could satisfy without deleting what the documents say.
	write(t, root, "docs/product/goals/v1-goals.md", `# V1 goals

An introduction.

## Goals

- Maintain a traceable chain from the product brief through goals, designs, work, code changes, and verification.
  *Supports: every change traces to intent somebody approved, from the brief
  through the goals and down to the work.*

  This matters because a change nobody can trace is a change nobody approved.

- Review every change independently.
  *Supports: nothing lands unreviewed by someone other than its author.*
`)

	set := Collect(root, setOf(recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md")))
	if len(set.WrapProblems) != 0 {
		t.Fatalf("wrap problems = %v", set.WrapProblems)
	}
}

func TestAWrappedGoalInADocumentNoLongerInForceIsNotReported(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	// The same rule the link upstream is judged by: a goal in a superseded
	// document is not one work can name, and reporting how it is written would
	// leave a permanent finding against a file nobody is going to open again.
	write(t, root, "docs/product/goals/v0-goals.md", `# V0 goals

An introduction.

## Goals

- Run development nearly autonomously. The human's routine interface is the
  product manager.
`)

	set := Collect(root, setOf(recorded("v0-goals", artifact.KindGoals, artifact.StatusSuperseded, "docs/product/goals/v0-goals.md")))
	if len(set.Goals) != 1 {
		t.Fatalf("goals = %q", stated(set.Goals))
	}
	if len(set.WrapProblems) != 0 {
		t.Fatalf("wrap problems = %v", set.WrapProblems)
	}
}

func TestProseUnderAGoalIsNotJoinedIntoIt(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	// A wrapped statement and a paragraph about the goal are told apart the way
	// Markdown tells them apart: the statement is the entry's opening paragraph,
	// and a blank line starts prose that describes it.
	write(t, root, "docs/product/goals/v1-goals.md", `# V1 goals

An introduction.

## Goals

- Maintain a traceable chain from the product brief through goals, designs,
  work, code changes, and verification.

  This matters because a change nobody can trace is a change nobody approved.

- Review every change independently.
  **Supports: nothing lands unreviewed by someone other than its author.**
Not indented, so not part of the entry above it.
`)

	set := Collect(root, setOf(recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md")))
	statements := stated(set.Goals)
	if len(statements) != 2 {
		t.Fatalf("goals = %q", statements)
	}
	if statements[0] != "Maintain a traceable chain from the product brief through goals, designs, work, code changes, and verification." {
		t.Fatalf("goal = %q", statements[0])
	}
	// A trailer bolded rather than italicized means the same thing by it, and
	// swallowing it corrupts the goal exactly as truncation does.
	if statements[1] != "Review every change independently." {
		t.Fatalf("goal = %q", statements[1])
	}
}

func TestATrailerWrappedAcrossLinesIsNotJoinedIntoTheGoal(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	// A trailer long enough to wrap is as ordinary as a goal long enough to wrap,
	// and joining one into the statement corrupts the recorded goal exactly as
	// truncating the statement does: the words the document states as the goal
	// then resolve to nothing.
	write(t, root, "docs/product/goals/v1-goals.md", `# V1 goals

An introduction.

## Goals

- Publish that work as pull requests the harness opens, and has the forge
  merge, on the roles' behalf.
  *Supports: safety invariants hold whatever the configuration says, and no
  agent pushes or merges.*
- Maintain a traceable chain.
`)

	set := Collect(root, setOf(recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md")))
	if len(set.Problems) != 0 {
		t.Fatalf("problems = %v", set.Problems)
	}
	statements := stated(set.Goals)
	whole := "Publish that work as pull requests the harness opens, and has the forge merge, on the roles' behalf."
	if len(statements) != 2 || statements[0] != whole || statements[1] != "Maintain a traceable chain." {
		t.Fatalf("goals = %q", statements)
	}
	if !set.Attribute(whole).Resolved() {
		t.Fatalf("the whole statement the document makes does not resolve: %#v", set.Attribute(whole))
	}
}

func TestAWrappedLineThatOpensWithEmphasisStillContinuesTheStatement(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	// The trailer is told from prose by whether the emphasis closes and the
	// sentence carries on. A continuation that opens with an emphasized phrase —
	// and closes with one, so that the line begins and ends in a marker — is the
	// rest of the statement, not an annotation of it.
	write(t, root, "docs/product/goals/v1-goals.md", `# V1 goals

An introduction.

## Goals

- Run development nearly autonomously. The human's routine interface is the
  *product manager*: they state intent and answer *questions*
  the product manager escalates.
  *Supports: the human's attention goes only where it is needed.*
`)

	set := Collect(root, setOf(recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md")))
	whole := "Run development nearly autonomously. The human's routine interface is the *product manager*: they state intent and answer *questions* the product manager escalates."
	if statements := stated(set.Goals); len(statements) != 1 || statements[0] != whole {
		t.Fatalf("goals = %q", stated(set.Goals))
	}
}

func TestAGoalTooLongToNameOnAWorkItemIsReportedRatherThanOffered(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	write(t, root, "docs/product/goals/v1-goals.md", goalsDocument(strings.Repeat("a", MaxStatementBytes+1)))
	set := Collect(root, setOf(recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md")))
	// Collecting it would offer an attribution that is refused every time it is
	// used, which reads as the harness disagreeing with itself.
	if len(set.Goals) != 0 || len(set.Problems) != 1 {
		t.Fatalf("set = %#v", set)
	}
	if !strings.Contains(set.Problems[0].Reason, "limit is") {
		t.Fatalf("problem = %v", set.Problems[0])
	}
}

func TestAGoalsDocumentThatCannotBeReadIsReportedBesideTheGoalsThatWere(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	write(t, root, "docs/product/goals/v1-goals.md", goalsDocument("Maintain a traceable chain."))
	set := Collect(root, setOf(
		recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md"),
		recorded("v2-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v2-goals.md"),
	))
	if len(set.Goals) != 1 {
		t.Fatalf("goals = %q", stated(set.Goals))
	}
	if len(set.Problems) != 1 || set.Problems[0].Path != "docs/product/goals/v2-goals.md" {
		t.Fatalf("problems = %v", set.Problems)
	}
}

func TestOnlyGoalsDocumentsAreReadForGoals(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	// A brief and a design both state goals in prose, and neither is what work is
	// attributed to: the goals are the product manager's document, and reading
	// intent out of anything that happens to have a `Goals` heading is how a
	// design comes to authorize its own work.
	write(t, root, "docs/product/brief.md", goalsDocument("Be a harness somebody would use."))
	write(t, root, "docs/designs/v1-harness.md", goalsDocument("Keep the pipeline resumable."))
	set := Collect(root, setOf(
		recorded("brief", artifact.KindBrief, artifact.StatusActive, "docs/product/brief.md"),
		recorded("v1-harness", artifact.KindDesign, artifact.StatusActive, "docs/designs/v1-harness.md"),
	))
	if len(set.Goals) != 0 || len(set.Sources) != 0 || len(set.Problems) != 0 {
		t.Fatalf("set = %#v", set)
	}
}

func TestTheGoalsAreReadFromTheArtifactsAsTheStoreLoadsThem(t *testing.T) {
	t.Parallel()

	// The two halves meet here: the store decides which documents are goals, and
	// this reads what those documents state. A test that constructed the set by
	// hand would never notice the two disagreeing about a file.
	root := newRepository(t)
	write(t, root, "docs/product/brief.md", frontmatter("brief", "brief", "Product brief", nil)+"\n# Brief\n")
	write(t, root, "docs/product/goals/v1-goals.md",
		frontmatter("v1-goals", "goals", "V1 goals", []string{"brief"})+"\n"+goalsDocument("Maintain a traceable chain."))
	store := artifact.Store{RepositoryRoot: root, Homes: []string{goalsHome}}
	artifacts, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	set := Collect(root, artifacts)
	if len(set.Goals) != 1 || set.Goals[0].ArtifactID != "v1-goals" {
		t.Fatalf("set = %#v", set)
	}
	if !set.Attribute("Maintain a traceable chain.").Resolved() {
		t.Fatalf("the goal the document states does not resolve")
	}
}

func TestAGoalNamesTheBriefGoalItSupportsRatherThanOnlySayingSoInProse(t *testing.T) {
	t.Parallel()

	// The link upstream is what the frontmatter cannot carry: `supports: brief`
	// says the document serves the brief and says nothing about which of the
	// brief's goals any one entry in it reaches.
	set := setLinkedToBrief(t, `# V1 goals

An introduction.

## Goals

- Maintain a traceable chain from the brief through to verification.
  *Supports: every change traces to intent somebody approved.*
- Review every change independently.
  **Supports: nothing lands unreviewed by someone other than its author.**
`)
	if len(set.LinkProblems) != 0 {
		t.Fatalf("link problems = %v", set.LinkProblems)
	}
	if len(set.BriefGoals) != 2 {
		t.Fatalf("brief goals = %#v", set.BriefGoals)
	}
	// The brief's goal is named by the claim it opens with, not by the paragraph
	// enlarging on it.
	if set.BriefGoals[0].Name != "Every change traces to intent somebody approved." {
		t.Fatalf("brief goal = %#v", set.BriefGoals[0])
	}
	// Either spelling of the emphasis means the same thing by it, and the
	// trailing marker is markup rather than part of what is named.
	if set.Goals[0].Supports != "every change traces to intent somebody approved." {
		t.Fatalf("supports = %q", set.Goals[0].Supports)
	}
	if set.Goals[1].Supports != "nothing lands unreviewed by someone other than its author." {
		t.Fatalf("supports = %q", set.Goals[1].Supports)
	}
}

func TestATrailerWrappedAcrossLinesNamesTheWholeBriefGoal(t *testing.T) {
	t.Parallel()

	// A trailer long enough to wrap is as ordinary as a goal long enough to wrap.
	// Reading only its first physical line would name a fragment, which resolves
	// to nothing and reports the fragment rather than the truncation.
	set := setLinkedToBrief(t, `# V1 goals

An introduction.

## Goals

- Publish that work as pull requests the harness opens.
  *Supports: nothing lands unreviewed by someone other than
  its author.*
`)
	if len(set.LinkProblems) != 0 {
		t.Fatalf("link problems = %v", set.LinkProblems)
	}
	if set.Goals[0].Supports != "nothing lands unreviewed by someone other than its author." {
		t.Fatalf("supports = %q", set.Goals[0].Supports)
	}
	// The trailer still has to stay out of the goal itself.
	if strings.Contains(set.Goals[0].Statement, "Supports:") {
		t.Fatalf("goal = %q", set.Goals[0].Statement)
	}
}

func TestAGoalNamingABriefGoalTheBriefDoesNotStateIsReported(t *testing.T) {
	t.Parallel()

	set := setLinkedToBrief(t, `# V1 goals

An introduction.

## Goals

- Maintain a traceable chain from the brief through to verification.
  *Supports: every change is cheap to make.*
`)
	if len(set.LinkProblems) != 1 || set.LinkProblems[0].Kind != LinkDangling {
		t.Fatalf("link problems = %#v", set.LinkProblems)
	}
	problem := set.LinkProblems[0]
	if problem.ArtifactID != "v1-goals" || problem.Path != "docs/product/goals/v1-goals.md" {
		t.Fatalf("link problem = %#v", problem)
	}
	// What is reported has to name the claim that does not resolve, because that
	// is the string somebody has to correct.
	if !strings.Contains(problem.Reason, "every change is cheap to make.") {
		t.Fatalf("reason = %q", problem.Reason)
	}
	// Nothing is dropped over a broken link: the goal is still what the document
	// states, and work naming it still resolves.
	if !set.Attribute("Maintain a traceable chain from the brief through to verification.").Resolved() {
		t.Fatalf("a goal with a broken link upstream stopped resolving")
	}
}

func TestAGoalThatNamesNothingUpstreamIsReportedAsAnOrphan(t *testing.T) {
	t.Parallel()

	// A goal with no trailer at all, and one whose trailer annotates the goal
	// rather than linking it, are the same thing: neither says what the goal is
	// for.
	set := setLinkedToBrief(t, `# V1 goals

An introduction.

## Goals

- Maintain a traceable chain from the brief through to verification.
- Review every change independently.
  *Added when the backlog was checked against the brief.*
`)
	if len(set.LinkProblems) != 2 {
		t.Fatalf("link problems = %#v", set.LinkProblems)
	}
	for _, problem := range set.LinkProblems {
		if problem.Kind != LinkUnstated {
			t.Fatalf("link problem = %#v", problem)
		}
	}
	if set.Goals[1].Supports != "" {
		t.Fatalf("a trailer that names no brief goal was read as naming one: %q", set.Goals[1].Supports)
	}
}

func TestABriefStatingNoGoalsIsReportedOnceRatherThanAgainstEveryGoal(t *testing.T) {
	t.Parallel()

	// Naming the missing root beats reporting every goal as separately unlinked:
	// what somebody has to fix is the brief, and it is one thing rather than one
	// per goal below it.
	root := newRepository(t)
	write(t, root, "docs/product/brief.md", "# Product brief\n\nWhat this is for, with no goals stated under a heading.\n")
	write(t, root, "docs/product/goals/v1-goals.md", `# V1 goals

An introduction.

## Goals

- Maintain a traceable chain from the brief through to verification.
  *Supports: every change traces to intent somebody approved.*
- Review every change independently.
  *Supports: nothing lands unreviewed by someone other than its author.*
`)
	set := Collect(root, setOf(
		recorded("brief", artifact.KindBrief, artifact.StatusActive, "docs/product/brief.md"),
		recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md"),
	))
	if len(set.LinkProblems) != 1 || set.LinkProblems[0].Kind != LinkNoBriefGoals {
		t.Fatalf("link problems = %#v", set.LinkProblems)
	}
	if !strings.Contains(set.LinkProblems[0].Reason, "brief") {
		t.Fatalf("reason = %q", set.LinkProblems[0].Reason)
	}
	// A brief that states no goals is not a goals document that could not be
	// read. It is the root either way.
	if len(set.Problems) != 0 {
		t.Fatalf("problems = %v", set.Problems)
	}
}

func TestAGoalNoLongerInForceIsNotHeldToItsLinkUpstream(t *testing.T) {
	t.Parallel()

	// A superseded document states intent that was replaced. Reporting its links
	// would leave a permanent finding against a decision somebody already made.
	root := newRepository(t)
	write(t, root, "docs/product/brief.md", briefDocument())
	write(t, root, "docs/product/goals/v0-goals.md", `# V0 goals

An introduction.

## Goals

- Something the product no longer intends.
  *Supports: a brief goal nobody ever wrote.*
`)
	set := Collect(root, setOf(
		recorded("brief", artifact.KindBrief, artifact.StatusActive, "docs/product/brief.md"),
		recorded("v0-goals", artifact.KindGoals, artifact.StatusSuperseded, "docs/product/goals/v0-goals.md"),
	))
	if len(set.LinkProblems) != 0 {
		t.Fatalf("link problems = %#v", set.LinkProblems)
	}
}

func TestABriefNoLongerInForceStatesNoGoalAGoalCanName(t *testing.T) {
	t.Parallel()

	// Both ends of the link are held to the same rule. A goal resolving against a
	// brief goal the product replaced would be traceability pointing at intent
	// nobody holds any more, which is worse than reporting the link as unmet.
	root := newRepository(t)
	write(t, root, "docs/product/brief.md", briefDocument())
	write(t, root, "docs/product/goals/v1-goals.md", `# V1 goals

An introduction.

## Goals

- Maintain a traceable chain from the brief through to verification.
  *Supports: every change traces to intent somebody approved.*
`)
	set := Collect(root, setOf(
		recorded("brief", artifact.KindBrief, artifact.StatusSuperseded, "docs/product/brief.md"),
		recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md"),
	))
	if len(set.BriefGoals) != 0 {
		t.Fatalf("a brief no longer in force offered link targets: %#v", set.BriefGoals)
	}
	if len(set.LinkProblems) != 1 || set.LinkProblems[0].Kind != LinkNoBriefGoals {
		t.Fatalf("link problems = %#v", set.LinkProblems)
	}
	// What is reported has to say which of the three things to do about it, so it
	// names the brief that ended rather than reading as a brief that states none.
	if !strings.Contains(set.LinkProblems[0].Reason, "no longer in force") {
		t.Fatalf("reason = %q", set.LinkProblems[0].Reason)
	}
}

// TestYoyodynesOwnArtifactsLoadAndItsGoalsAreReadFromThem runs the real store
// and the real collector over this repository's own documents, which is the only
// place either of them meets a document somebody wrote rather than one a test
// constructed. The homes come from the project's own configuration rather than
// from constants written beside this test, because a test naming the directories
// itself would be checking the set it chose rather than the set the harness
// reads, and would keep passing after the project moved them.
//
// What this refuses and what it tolerates is a decided boundary rather than an
// oversight, and the line is a contradiction against an absence. A repository
// asserting something false about itself is a defect whoever wrote it and
// whatever they meant; a repository that has written less down than it will is
// intent part-way through being recorded, and failing a build over one makes
// writing intent down the riskiest thing anybody can do.
//
// Refused, because each is the repository contradicting itself:
//
//   - The store failing to load, a recorded document it could not read, a
//     `supports:` naming an artifact nobody records, a revision under no
//     authority. TestThisRepositoryOwnArtifactsAreReadableByTheHarness refuses
//     these one layer up for the same reason, and the reason is written down
//     there: the harness reports them and refuses nothing over one, so a warning
//     nobody is made to read is how one of them breaks unnoticed.
//
//   - A goal naming a brief goal the brief does not state. The goals document
//     asserts the brief says something it does not, which is the same broken
//     link as a dangling `supports:` one layer up, and it is what silently
//     orphans the work attributed under it.
//
// Tolerated, because each is intent not yet finished rather than intent
// contradicted, and each is already reported by `yoyo goals list` on stderr:
//
//   - A goals document stating no goals, and no goal in force at all. A goals
//     set is legitimately empty before the goals are written, and Collect
//     reporting it is the correct report rather than a defect.
//
//   - A goal naming nothing upstream. The goal has yet to say what it is for,
//     which is a sentence somebody still has to write and not a false one.
//
//   - A brief stating no goals for anything to name.
//
// What is asserted about the tolerated states instead is that the report of them
// is usable: a legitimate state described by a problem naming no file to open is
// still a defect here, and that part is ours to fix rather than the product
// manager's.
func TestYoyodynesOwnArtifactsLoadAndItsGoalsAreReadFromThem(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	store := configuredStore(t, root)
	artifacts, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(artifacts.Problems) != 0 || len(artifacts.ReferenceProblems) != 0 {
		t.Fatalf("problems = %v, reference problems = %v", artifacts.Problems, artifacts.ReferenceProblems)
	}

	set := Collect(root, artifacts)
	for _, problem := range set.Problems {
		if problem.Path == "" {
			t.Errorf("a goals document was not read and the report does not say which: %q", problem.Reason)
		}
		t.Logf("goals not read: %s", problem)
	}
	for _, problem := range set.LinkProblems {
		switch problem.Kind {
		case LinkDangling:
			t.Errorf("a goal names a brief goal the brief does not state, so the work attributed under it traces to nothing: %s", problem)
		case LinkUnstated:
			// Naming the document is this package's job whatever the goal says, so
			// it is refused even though the state being reported is tolerated.
			if problem.Path == "" || problem.ArtifactID == "" {
				t.Errorf("a goal with no link upstream does not name the document it is written in: %#v", problem)
			}
			t.Logf("goal names nothing upstream: %s", problem)
		default:
			t.Logf("goal not linked to the brief: %s", problem)
		}
	}
	// A brief that is recorded at all has to be somewhere a reader can be sent,
	// whether or not it states a goal: the goals listing closes by sending them
	// there. This is about what Collect carries rather than about what the brief
	// says, which is why it is refused where the contents of the brief are not.
	if len(artifacts.OfKind(artifact.KindBrief)) != 0 && set.BriefPath == "" {
		t.Errorf("a brief is recorded and the collected goals do not say where to open it")
	}
}

func TestNoDocumentUnderDocsCarriesIdentityOutsideAConfiguredHome(t *testing.T) {
	t.Parallel()

	// Identity outside a configured home is identity nothing reads. The store
	// never walks the file, so it is neither an artifact nor a reported problem,
	// and a document can look governed to a reader while nothing downstream can
	// refer to it — which is how the v1 harness design sat for as long as it did.
	// The load being clean is therefore not evidence on its own, and this is the
	// check that makes it so.
	root := repositoryRoot(t)
	store := configuredStore(t, root)
	homes := append([]string(nil), store.Homes...)

	documents := filepath.Join(root, "docs")
	// A project that has written nothing down carries identity nowhere, which is
	// the same legitimate state as a home nobody has created: there is nothing
	// here to be outside a home.
	if _, err := os.Stat(documents); errors.Is(err, os.ErrNotExist) {
		return
	}
	err := filepath.WalkDir(documents, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			return walkErr
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(strings.TrimPrefix(string(content), "\ufeff"), "---") {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		slashed := filepath.ToSlash(relative)
		for _, home := range homes {
			if strings.HasPrefix(slashed, home+"/") {
				return nil
			}
		}
		t.Errorf("%s carries artifact frontmatter but is inside none of the configured homes (%s), so nothing reads its identity",
			slashed, strings.Join(homes, ", "))
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", documents, err)
	}
}

// setLinkedToBrief is a repository whose brief states the goals a goals document
// links up to, so a test about that link says only that.
func setLinkedToBrief(t *testing.T, goals string) Set {
	t.Helper()
	root := newRepository(t)
	write(t, root, "docs/product/brief.md", briefDocument())
	write(t, root, "docs/product/goals/v1-goals.md", goals)
	set := Collect(root, setOf(
		recorded("brief", artifact.KindBrief, artifact.StatusActive, "docs/product/brief.md"),
		recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md"),
	))
	if len(set.Problems) != 0 {
		t.Fatalf("problems = %v", set.Problems)
	}
	return set
}

// briefDocument is a brief in the shape Yoyodyne's own is written in: each goal
// a bolded claim followed by a paragraph enlarging on it.
func briefDocument() string {
	return `# Product brief

What the product is for.

## Goals

- **Every change traces to intent somebody approved.** A reader can follow any
  merged change back through the work, the design, and the goal to the brief.
- **Nothing lands unreviewed by someone other than its author.**
`
}

// configuredStore is the artifact store this project actually reads, built from
// its own configuration: the three homes and the invariants directory excluded
// from them, exactly as the commands assemble it.
func configuredStore(t *testing.T, root string) artifact.Store {
	t.Helper()
	path, err := config.Discover(root)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	product := loaded.Product
	// The store is assembled through the same path the commands use, so a home
	// added to the production assembly is covered here without this list being
	// maintained by hand.
	//
	// Nothing here checks that the homes exist. A home the configuration names and
	// nobody has created yet is a legitimate state — a project has no designs
	// before its first one is written — and the store already reads it as the
	// empty set rather than an error, so demanding the directory here would fail a
	// repository the harness itself is perfectly happy with. A home that exists
	// and is not a directory is still refused, by `Load` rather than by this: the
	// store cannot walk it, and says so.
	//
	// What the check that used to stand here was reaching for is a
	// governed-looking document that no home covers, and that is found directly by
	// TestNoDocumentUnderDocsCarriesIdentityOutsideAConfiguredHome — which looks
	// for the document itself, wherever it sits, instead of inferring one from an
	// absent directory.
	return artifact.StoreFor(root, product)
}

// repositoryRoot is the checkout these tests run in, so the seeded artifacts are
// read where they actually live rather than from a copy.
func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("Abs() error = %v", err)
	}
	if root, err = filepath.EvalSymlinks(root); err != nil {
		t.Fatalf("EvalSymlinks() error = %v", err)
	}
	return root
}

// setWithGoals is a repository whose only goals document states what a test
// names, so a test about attribution says only that.
func setWithGoals(t *testing.T, statements ...string) Set {
	t.Helper()
	root := newRepository(t)
	write(t, root, "docs/product/goals/v1-goals.md", goalsDocument(statements...))
	set := Collect(root, setOf(recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md")))
	if len(set.Problems) != 0 {
		t.Fatalf("problems = %v", set.Problems)
	}
	return set
}

func goalsDocument(statements ...string) string {
	var rendered strings.Builder
	rendered.WriteString("# The goals of some product\n\nAn introduction saying what this covers.\n\n## Goals\n\n")
	for _, statement := range statements {
		rendered.WriteString("- " + statement + "\n")
	}
	return rendered.String()
}

func frontmatter(id, kind, title string, supports []string) string {
	var rendered strings.Builder
	rendered.WriteString("---\nid: " + id + "\nkind: " + kind + "\ntitle: " + title + "\n")
	rendered.WriteString("supports:\n")
	for _, reference := range supports {
		rendered.WriteString("    - " + reference + "\n")
	}
	rendered.WriteString("status: active\nrevisions:\n")
	rendered.WriteString("    - action: created\n      by: product-manager\n      at: 2026-08-17T12:00:00Z\n      reason: recorded when identity arrived\n")
	rendered.WriteString("---\n")
	return rendered.String()
}

func recorded(id string, kind artifact.Kind, status artifact.Status, path string) artifact.Artifact {
	return artifact.Artifact{ID: id, Kind: kind, Title: id, Status: status, Path: path}
}

func setOf(artifacts ...artifact.Artifact) artifact.Set {
	return artifact.Set{Artifacts: artifacts}
}

// lineOf is what a document says on one physical line, counted from one, so a
// test about a reported position checks where the position lands rather than
// restating the arithmetic that produced it.
func lineOf(t *testing.T, root, relative string, line int) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	lines := strings.Split(string(content), "\n")
	if line < 1 || line > len(lines) {
		t.Fatalf("line %d is outside %s, which has %d lines", line, relative, len(lines))
	}
	return lines[line-1]
}

func stated(goals []Goal) []string {
	statements := make([]string, 0, len(goals))
	for _, recorded := range goals {
		statements = append(statements, recorded.Statement)
	}
	return statements
}

func newRepository(t *testing.T) string {
	t.Helper()
	// A temporary directory on macOS is a symlink, and the artifact store
	// resolves the root through symlinks, so both halves are given what it
	// resolves to.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks() error = %v", err)
	}
	return root
}

func write(t *testing.T, root, relative, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func TestTheSupportsTrailerIsReadWhicheverSideOfAnAnnotationItSits(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	// Two trailers under one goal, in both orders. The link reads the Supports
	// trailer; the annotation is prose beside it, not part of the link and not
	// something that may swallow it.
	write(t, root, "docs/product/goals/v1-goals.md", `---
id: v1-goals
---

# V1 goals

An introduction.

## Goals

- Maintain a traceable chain from intent to verification.
  *Supports: every change traces to intent somebody approved.*
  *Added when the backlog was checked against the brief.*
- Isolate implementation tasks in harness-managed worktrees.
  *Added when the backlog was checked against the brief.*
  *Supports: intent goes in and merged software comes out.*
`)

	set := Collect(root, setOf(recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md")))
	if len(set.Problems) != 0 {
		t.Fatalf("problems = %v", set.Problems)
	}
	want := []string{
		"every change traces to intent somebody approved.",
		"intent goes in and merged software comes out.",
	}
	for i, goal := range set.Goals {
		if goal.Supports != want[i] {
			t.Fatalf("goal %d supports = %q, want %q (annotation must not swallow or precede away the link)", i, goal.Supports, want[i])
		}
	}
}

func TestAnUnreadableGoalsDocumentDoesNotReportTheBriefAsUnreadable(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	// The brief reads fine and legitimately states no goals; a goals document
	// beside it cannot be read at all. The link report must point at the brief's
	// real condition, not at a read failure belonging to a different document.
	write(t, root, "docs/product/brief.md", `---
id: brief
---

# Product brief

An introduction with no Goals heading.
`)
	write(t, root, "docs/product/goals/v1-goals.md", `---
id: v1-goals
---

# V1 goals

An introduction.

## Goals

- Maintain a traceable chain from intent to verification.
`)

	set := Collect(root, setOf(
		recorded("brief", artifact.KindBrief, artifact.StatusActive, "docs/product/brief.md"),
		recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md"),
		recorded("v2-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/missing.md"),
	))
	if len(set.Problems) != 1 || !strings.Contains(set.Problems[0].Path, "missing.md") {
		t.Fatalf("problems = %v, want exactly the unreadable goals document", set.Problems)
	}
	if len(set.LinkProblems) != 1 {
		t.Fatalf("link problems = %v, want the brief reported once", set.LinkProblems)
	}
	reason := set.LinkProblems[0].Reason
	if strings.Contains(reason, "could not be read") || !strings.Contains(reason, "states no goals") {
		t.Fatalf("link reason = %q: the brief states no goals and was read fine; a goals document failing to read must not be pinned on the brief", reason)
	}
}

// A goal carries the operator's approval of the document stating it, because
// that is now what decides whether work serving it reaches the queue without a
// person. An approval given against an earlier revision is deliberately not
// carried forward: the approval still stands for what it was given for, and the
// goal as the document now reads is not what anybody saw.
func TestAGoalCarriesWhetherTheOperatorApprovedTheDocumentStatingIt(t *testing.T) {
	t.Parallel()

	root := newRepository(t)
	write(t, root, "docs/product/goals/v1-goals.md", `---
id: v1-goals
---

# V1 goals

## Goals

- Run development nearly autonomously.
`)
	approved := recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md")
	approved.Revisions = []artifact.Revision{{}}
	approved.Approvals = []artifact.Approval{{Revision: 0}}

	set := Collect(root, setOf(approved))
	if len(set.Goals) != 1 || !set.Goals[0].Approved() {
		t.Fatalf("goals = %#v", set.Goals)
	}
	if gap := set.Attribute("Run development nearly autonomously.").ApprovalGap(); gap != "" {
		t.Fatalf("an approved goal reports a gap: %q", gap)
	}

	// The same document with a revision recorded after the approval.
	amended := approved
	amended.Revisions = []artifact.Revision{{}, {}}
	amendedSet := Collect(root, setOf(amended))
	if len(amendedSet.Goals) != 1 || amendedSet.Goals[0].Approved() {
		t.Fatalf("an amended document still reads as approved: %#v", amendedSet.Goals)
	}
	if gap := amendedSet.Attribute("Run development nearly autonomously.").ApprovalGap(); !strings.Contains(gap, "amended since") {
		t.Fatalf("gap = %q, want it to say the document moved since the approval", gap)
	}

	// And one nobody ever approved.
	unapproved := recorded("v1-goals", artifact.KindGoals, artifact.StatusActive, "docs/product/goals/v1-goals.md")
	unapprovedSet := Collect(root, setOf(unapproved))
	if len(unapprovedSet.Goals) != 1 || unapprovedSet.Goals[0].Approved() {
		t.Fatalf("an unapproved document reads as approved: %#v", unapprovedSet.Goals)
	}
	gap := unapprovedSet.Attribute("Run development nearly autonomously.").ApprovalGap()
	if !strings.Contains(gap, "records no approval") || !strings.Contains(gap, "v1-goals") {
		t.Fatalf("gap = %q, want it to name the unapproved document", gap)
	}
}

// An attribution that does not resolve reports the gap it already reports, in
// the same words the rest of the harness reports it in. There is no second
// vocabulary for the same failure.
func TestAnUnresolvedAttributionReportsItsOwnReasonAsTheApprovalGap(t *testing.T) {
	t.Parallel()

	for _, attribution := range []Attribution{
		{State: StateUnattributed, Reason: "it names no goal"},
		{State: StateUnresolved, Named: "Grow the ecosystem.", Reason: "no goal recorded in v1-goals is stated in those words"},
		{State: StateUncheckable, Named: "Anything.", Reason: "the repository records no goals artifact"},
	} {
		if gap := attribution.ApprovalGap(); gap != attribution.Reason {
			t.Fatalf("%s gap = %q, want %q", attribution.State, gap, attribution.Reason)
		}
	}
}
