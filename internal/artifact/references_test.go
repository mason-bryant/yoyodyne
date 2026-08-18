package artifact

import (
	"strings"
	"testing"
)

func TestTheChainThisRepositoryRecordsIsReportedAsWhole(t *testing.T) {
	t.Parallel()

	// The shape Yoyodyne's own documents are in today: a brief, the goals that
	// support it, the non-goals that bound those, and four decision records that
	// support nothing. Every one of them is correct, and a check that invented a
	// violation for a correct document would be worse than no check — the reader
	// would learn to skip what it says.
	store := newStore(t)
	write(t, store, productHome+"/brief.md", document("brief", "brief", "Product brief", nil, "active"))
	write(t, store, productHome+"/goals/v1-goals.md", document("v1-goals", "goals", "V1 goals", []string{"brief"}, "active"))
	write(t, store, productHome+"/goals/v1-non-goals.md", document("v1-non-goals", "non-goals", "V1 non-goals", []string{"v1-goals"}, "active"))
	for _, decision := range []string{"beads-durable-workflow-store", "claude-code-default-backend", "markdown-source-of-truth", "self-hosting-threshold"} {
		write(t, store, decisionsHome+"/"+decision+".md", document(decision, "decision", "A recorded decision", nil, "active"))
	}

	set, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(set.Artifacts) != 7 || len(set.Problems) != 0 {
		t.Fatalf("set = %#v", set)
	}
	if len(set.ReferenceProblems) != 0 {
		t.Fatalf("reference problems = %v", set.ReferenceProblems)
	}
}

func TestAReferenceThatResolvesToNothingIsReportedWithBothEndsNamed(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	write(t, store, productHome+"/brief.md", document("brief", "brief", "Product brief", nil, "active"))
	write(t, store, designsHome+"/v1-harness.md", document("v1-harness", "design", "V1 harness design", []string{"brief", "goals-that-moved"}, "active"))

	set, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// The design still loads. A broken relationship says something about the
	// chain rather than about whether the document can be read, and dropping it
	// would lose the design over a name somebody has to correct anyway.
	if len(set.Artifacts) != 2 || len(set.Problems) != 0 {
		t.Fatalf("set = %#v", set)
	}
	if len(set.ReferenceProblems) != 1 {
		t.Fatalf("reference problems = %v", set.ReferenceProblems)
	}
	problem := set.ReferenceProblems[0]
	// Both ends: the document the reference is written in, and the id it names.
	// Either one alone leaves somebody searching for the other.
	if problem.Kind != ProblemDanglingReference || problem.ID != "v1-harness" || problem.Path != designsHome+"/v1-harness.md" {
		t.Fatalf("problem = %#v", problem)
	}
	if !strings.Contains(problem.Reason, `"goals-that-moved"`) {
		t.Fatalf("problem does not name what it resolves to nothing: %q", problem.Reason)
	}
	// It reaches the brief by its other reference, so it is not also an orphan.
	if strings.Contains(problem.String(), string(ProblemOrphan)) {
		t.Fatalf("problem = %q", problem.String())
	}
}

func TestAReferenceToARefusedDocumentSaysTheFileIsThere(t *testing.T) {
	t.Parallel()

	// A reference to a document that is sitting in an artifact home and was
	// refused reads as a document nobody wrote, which sends whoever fixes it
	// looking for a file that already exists.
	store := newStore(t)
	write(t, store, productHome+"/brief.md", document("brief", "brief", "Product brief", nil, "active"))
	write(t, store, productHome+"/goals/v1-goals.md", "# Goals somebody wrote before identity arrived\n")
	write(t, store, designsHome+"/v1-harness.md", document("v1-harness", "design", "V1 harness design", []string{"v1-goals"}, "active"))

	set, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(set.Problems) != 1 || set.Problems[0].Path != productHome+"/goals/v1-goals.md" {
		t.Fatalf("problems = %v", set.Problems)
	}
	dangling, found := problemOfKind(set.ReferenceProblems, ProblemDanglingReference)
	if !found {
		t.Fatalf("reference problems = %v", set.ReferenceProblems)
	}
	if !strings.Contains(dangling.Reason, productHome+"/goals/v1-goals.md") {
		t.Fatalf("problem does not name the file that is there: %q", dangling.Reason)
	}
	if !strings.Contains(dangling.Reason, "was not read as one") {
		t.Fatalf("problem does not say why it is not an artifact: %q", dangling.Reason)
	}
}

func TestAnArtifactNothingConnectsToTheBriefIsAnOrphan(t *testing.T) {
	t.Parallel()

	store := newStore(t)
	write(t, store, productHome+"/brief.md", document("brief", "brief", "Product brief", nil, "active"))
	// Goals that name nothing at all.
	write(t, store, productHome+"/goals/loose-goals.md", document("loose-goals", "goals", "Goals nobody tied to the brief", nil, "active"))
	// A design that names something real, which is not the brief and does not
	// reach it. Naming an upstream is not the same as tracing to one.
	write(t, store, decisionsHome+"/markdown-source-of-truth.md", document("markdown-source-of-truth", "decision", "Markdown is the source of truth", nil, "active"))
	write(t, store, designsHome+"/sideways.md", document("sideways", "design", "A design hanging off a decision", []string{"markdown-source-of-truth"}, "active"))
	// And the chain that is right, so the check is shown telling them apart.
	write(t, store, productHome+"/goals/v1-goals.md", document("v1-goals", "goals", "V1 goals", []string{"brief"}, "active"))
	write(t, store, designsHome+"/v1-harness.md", document("v1-harness", "design", "V1 harness design", []string{"v1-goals"}, "active"))

	set, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(set.Artifacts) != 6 || len(set.Problems) != 0 {
		t.Fatalf("set = %#v", set)
	}
	orphans := map[string]string{}
	for _, problem := range set.ReferenceProblems {
		if problem.Kind != ProblemOrphan {
			t.Fatalf("unexpected problem %#v", problem)
		}
		orphans[problem.ID] = problem.Reason
	}
	if len(orphans) != 2 {
		t.Fatalf("orphans = %v", orphans)
	}
	// The two ways of not reaching the brief are told apart, because they are
	// two different things to do about it.
	if reason := orphans["loose-goals"]; !strings.Contains(reason, "names nothing upstream") {
		t.Fatalf("loose-goals = %q", reason)
	}
	if reason := orphans["sideways"]; !strings.Contains(reason, `"markdown-source-of-truth"`) || !strings.Contains(reason, "reaches the brief") {
		t.Fatalf("sideways = %q", reason)
	}
	// The chain that holds is silent, and so is the brief that is the root of it
	// and the decision that is not downstream of intent.
	for _, quiet := range []string{"brief", "v1-goals", "v1-harness", "markdown-source-of-truth"} {
		if problems := set.ReferenceProblemsFor(quiet); len(problems) != 0 {
			t.Fatalf("%s is reported for %v", quiet, problems)
		}
	}
}

func TestTheChainIsTracedThroughAsManyArtifactsAsItRuns(t *testing.T) {
	t.Parallel()

	// Traceability is reaching the brief, not naming something that did. A
	// design supports a goal that supports the brief, and that is one chain
	// rather than two references to check separately.
	store := newStore(t)
	write(t, store, productHome+"/brief.md", document("brief", "brief", "Product brief", nil, "active"))
	write(t, store, productHome+"/goals/v1-goals.md", document("v1-goals", "goals", "V1 goals", []string{"brief"}, "active"))
	write(t, store, designsHome+"/v1-harness.md", document("v1-harness", "design", "V1 harness design", []string{"v1-goals"}, "active"))
	write(t, store, designsHome+"/artifact-identity.md", document("artifact-identity", "specification", "Artifact identity", []string{"v1-harness"}, "active"))

	set, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(set.ReferenceProblems) != 0 {
		t.Fatalf("reference problems = %v", set.ReferenceProblems)
	}
}

func TestArtifactsThatOnlySupportEachOtherReachNothing(t *testing.T) {
	t.Parallel()

	// Two documents that support each other look connected from either end and
	// are connected to nothing. Following a reference twice would not end, so the
	// walk does not.
	store := newStore(t)
	write(t, store, productHome+"/brief.md", document("brief", "brief", "Product brief", nil, "active"))
	write(t, store, designsHome+"/first.md", document("first", "design", "The first of a pair", []string{"second"}, "active"))
	write(t, store, designsHome+"/second.md", document("second", "design", "The second of a pair", []string{"first"}, "active"))

	set, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(set.Artifacts) != 3 {
		t.Fatalf("artifacts = %v", ids(set.Artifacts))
	}
	if len(set.ReferenceProblems) != 2 {
		t.Fatalf("reference problems = %v", set.ReferenceProblems)
	}
	for _, problem := range set.ReferenceProblems {
		if problem.Kind != ProblemOrphan || !strings.Contains(problem.Reason, "reaches the brief") {
			t.Fatalf("problem = %#v", problem)
		}
	}
}

func TestAnUnwrittenBriefIsReportedAsTheMissingRootRatherThanAsEveryDocument(t *testing.T) {
	t.Parallel()

	// A repository with goals and no brief has one thing wrong with it. Reporting
	// each document as separately unconnected would name the symptom in every
	// file and the cause in none of them.
	store := newStore(t)
	write(t, store, productHome+"/goals/v1-goals.md", document("v1-goals", "goals", "V1 goals", []string{"brief"}, "active"))
	write(t, store, designsHome+"/v1-harness.md", document("v1-harness", "design", "V1 harness design", []string{"v1-goals"}, "active"))

	set, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	// The goals still name a brief that is not there, which is its own report:
	// the id may be a typo rather than a document nobody has written.
	dangling, found := problemOfKind(set.ReferenceProblems, ProblemDanglingReference)
	if !found || dangling.ID != "v1-goals" || !strings.Contains(dangling.Reason, `"brief"`) {
		t.Fatalf("reference problems = %v", set.ReferenceProblems)
	}
	for _, problem := range set.ReferenceProblems {
		if problem.Kind != ProblemOrphan {
			continue
		}
		if !strings.Contains(problem.Reason, `no artifact of kind "brief" is recorded`) {
			t.Fatalf("orphan = %#v", problem)
		}
	}
}

func TestAnEndedArtifactStillHoldsItsPlaceInTheChain(t *testing.T) {
	t.Parallel()

	// A superseded goal is still what a design was written against, so it still
	// answers to its id and the design that traces through it is not an orphan.
	// The record of what was intended is what makes the change traceable.
	store := newStore(t)
	write(t, store, productHome+"/brief.md", document("brief", "brief", "Product brief", nil, "active"))
	write(t, store, productHome+"/goals/v1-goals.md", "---\nid: v1-goals\nkind: goals\ntitle: V1 goals\nsupports:\n    - brief\n"+
		"status: superseded\nrevisions:\n"+
		"    - action: created\n      by: product-manager\n      at: 2026-08-01T12:00:00Z\n      reason: the first statement of intent\n"+
		"    - action: superseded\n      by: product-manager\n      at: 2026-08-17T12:00:00Z\n      reason: replaced by the v2 goals\n---\n")
	write(t, store, designsHome+"/v1-harness.md", document("v1-harness", "design", "V1 harness design", []string{"v1-goals"}, "active"))

	set, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(set.ReferenceProblems) != 0 {
		t.Fatalf("reference problems = %v", set.ReferenceProblems)
	}
}

func problemOfKind(problems []ReferenceProblem, kind ReferenceProblemKind) (ReferenceProblem, bool) {
	for _, problem := range problems {
		if problem.Kind == kind {
			return problem, true
		}
	}
	return ReferenceProblem{}, false
}
