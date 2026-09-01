package workflow

// The separation policies, held at the door a project's own topology comes
// through.
//
// `internal/separation` is where the rules are and where each of them is tested
// on its own. These are the tests that matter for the thing this package
// promises: a definition chooses its topology, and no topology it can choose
// puts authorship and judgment in one invocation or reaches the promotion
// without the evidence that earns it. So they are written as a sweep over
// topologies rather than as a handful of examples — the claim is about every
// arrangement, and a claim about every arrangement is worth nothing if what is
// tested is the three somebody thought of.

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/separation"
)

// arranged is a definition over the delivery actions, wired as a chain: each
// state in the order given, leading to the next, and the last one leading to the
// terminal.
//
// A chain rather than an arbitrary graph because a chain is what makes the
// answer decidable by inspection — the evidence a promotion stands on is exactly
// what came before it in the list — so the test can assert what should happen
// rather than re-implement the analysis it is checking.
func arranged(id string, states ...string) Definition {
	definition := Definition{
		Schema:    SchemaVersion,
		ID:        id,
		Initial:   states[0],
		States:    make(map[string]State, len(states)),
		Terminals: map[string]Terminal{"delivered": {Summary: "the sequence ended"}},
	}
	for index, state := range states {
		next := "delivered"
		if index+1 < len(states) {
			next = states[index+1]
		}
		definition.States[state] = State{
			Action: "candidate." + state,
			On:     map[string]string{"done": next},
		}
	}
	return definition
}

// TestTheFixtureRoutingReviewToTheAuthorIsRefusedAtCompile is the case the first
// policy exists for, read from a file the way a project's own definition would
// be.
//
// Everything else about that fixture is correct: the schema is this build's,
// every action it selects is registered, every destination exists, the grant
// confers everything its actions require, and the evidence rule is satisfied —
// the checks run after the change is written and nothing rewrites it afterwards.
// It is refused for one reason and one only — the state returning the verdict is
// the state that wrote the change — and it is refused before an instance exists.
func TestTheFixtureRoutingReviewToTheAuthorIsRefusedAtCompile(t *testing.T) {
	t.Parallel()

	acted := &run{}
	loader := deliveryLoader(t, acted)

	// The action it selects is genuinely registered, so what refuses the
	// definition is the policy rather than a name the catalog does not hold.
	if _, registered := loader.Registry.Lookup("candidate.develop-and-review"); !registered {
		t.Fatal("the fixture's combined action is not registered; this test would prove the wrong refusal")
	}

	graph, err := loader.LoadFile("testdata/review-by-the-author.yaml")
	if err == nil {
		t.Fatal("LoadFile() compiled a definition routing the review to the invocation that wrote the change")
	}
	if !strings.Contains(err.Error(), separation.AuthorshipIsNeverJudgment) {
		t.Errorf("LoadFile() error = %v, and it does not name the policy it refused under", err)
	}
	if !strings.Contains(err.Error(), `the state "develop"`) {
		t.Errorf("LoadFile() error = %v, and it does not name the state it refused", err)
	}
	// And by that policy alone. A fixture refused by two rules at once proves
	// neither of them, so what it is a fixture for is that this one is enough.
	if strings.Contains(err.Error(), separation.IntegrationFollowsEvidence) {
		t.Errorf("LoadFile() error = %v, and the fixture was meant to satisfy the evidence rule", err)
	}
	// A refused compile carries nothing, and it performed nothing on the way to
	// refusing: a definition this build will not run is one nothing was spent on.
	if states := graph.States(); len(states) != 0 || graph.Digest() != "" {
		t.Errorf("a refused compile carries the states %v and the digest %q", states, graph.Digest())
	}
	if len(acted.performed) != 0 {
		t.Errorf("refusing a definition performed %v", acted.performed)
	}
}

// authoring is every state in the sweep below whose action writes the change.
// Two of them, which is what lets a sequence put one after the verdict.
var authoring = []string{"develop", "rework"}

// TestIntegrationIsImpossibleWithoutTrustedEvidenceUnderEveryTopology is the
// epic's acceptance criterion, stated as the test it asks for.
//
// It walks every arrangement of the five states that matter — two that write the
// change, one that runs the project's checks, one that returns the verdict, and
// one that promotes — and, for each, every subset that still contains the
// promotion. That is 1,920 sequences, and every one of them is a topology a
// definition could have chosen.
//
// A sequence compiles if and only if the checks and the verdict both come
// between the last state that writes the change and the promotion. Trusted
// evidence is evidence about the thing being promoted, so a second authoring
// state after the verdict is exactly as bad as no verdict at all — and that is
// the arrangement a sweep over four states could not express, which is why there
// are two authoring states here rather than one.
func TestIntegrationIsImpossibleWithoutTrustedEvidenceUnderEveryTopology(t *testing.T) {
	t.Parallel()

	loader := deliveryLoader(t, &run{})
	catalog, err := CatalogFrom(loader.Registry)
	if err != nil {
		t.Fatalf("CatalogFrom() error = %v", err)
	}

	arrangements := 0
	for _, order := range permutations([]string{"develop", "rework", "check", "review", "integrate"}) {
		for included := 0; included < 1<<len(order); included++ {
			var states []string
			for position, state := range order {
				if included&(1<<position) != 0 {
					states = append(states, state)
				}
			}
			// A sequence with no promotion in it says nothing about this criterion,
			// and an empty one is not a definition at all.
			if !slices.Contains(states, "integrate") {
				continue
			}
			arrangements++

			id := strings.Join(states, "-")
			validated, err := arranged(id, states...).Validate(catalog)
			if err != nil {
				t.Fatalf("Validate(%s) error = %v; every arrangement here is a well-formed state machine", id, err)
			}
			_, err = loader.Compile(validated)

			// The chain runs in order and ends at the terminal, so what stands before
			// the promotion is exactly the states at lower indices — and what still
			// describes the change is what came after the last one that rewrote it.
			promotion := slices.Index(states, "integrate")
			written := -1
			for _, author := range authoring {
				if at := slices.Index(states, author); at >= 0 && at < promotion && at > written {
					written = at
				}
			}
			since := func(state string) bool {
				at := slices.Index(states, state)
				return at > written && at < promotion
			}
			earned := since("check") && since("review")

			switch {
			case earned && err != nil:
				t.Errorf("Compile(%s) error = %v; the checks and the verdict both come after the change was last written and before the promotion", id, err)
			case !earned && err == nil:
				t.Errorf("Compile(%s) compiled a sequence that promotes without the evidence that earns it", id)
			case !earned && !strings.Contains(err.Error(), separation.IntegrationFollowsEvidence):
				t.Errorf("Compile(%s) error = %v, and it does not name the policy it refused under", id, err)
			}
		}
	}
	// The sweep is the claim, so a sweep that quietly stopped covering anything is
	// worse than no sweep: 5! orders, 2^5 subsets, half of which hold the
	// promotion.
	if want := 120 * 16; arrangements != want {
		t.Errorf("the sweep covered %d arrangements, want %d", arrangements, want)
	}
}

// TestAChangeRewrittenAfterTheVerdictIsNotJudged is the case the sweep above
// covers and nobody would find by reading it: the most obvious extra state
// anybody would add to a correct sequence.
//
// Every state here is one the harness already has, in the order it already runs
// them, with one repair step appended after the review. The evidence was
// collected; it stopped describing the change when the change was rewritten.
func TestAChangeRewrittenAfterTheVerdictIsNotJudged(t *testing.T) {
	t.Parallel()

	loader := deliveryLoader(t, &run{})
	catalog, err := CatalogFrom(loader.Registry)
	if err != nil {
		t.Fatalf("CatalogFrom() error = %v", err)
	}
	validated, err := arranged("rework-after-the-verdict", "develop", "check", "review", "rework", "integrate").Validate(catalog)
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	_, err = loader.Compile(validated)
	if err == nil {
		t.Fatal("Compile() compiled a sequence that rewrites the change after the verdict and then promotes it")
	}
	if !strings.Contains(err.Error(), separation.IntegrationFollowsEvidence) {
		t.Errorf("Compile() error = %v, and it does not name the policy it refused under", err)
	}
	// Both kinds of evidence stopped describing the change, so both are reported:
	// a refusal naming only the verdict would read as though the checks survived.
	for _, missing := range []string{"checks.execute", "review.verdict"} {
		if !strings.Contains(err.Error(), missing) {
			t.Errorf("Compile() error = %v, and it does not name the missing %q", err, missing)
		}
	}

	// The same sequence with the repair before the review compiles, so what the
	// refusal caught is the order rather than the presence of a second authoring
	// state.
	repaired, err := arranged("rework-before-the-verdict", "develop", "rework", "check", "review", "integrate").Validate(catalog)
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if _, err := loader.Compile(repaired); err != nil {
		t.Errorf("Compile() error = %v; the change is checked and judged after it was last written", err)
	}
}

// TestNoTopologyPutsAuthorshipAndJudgmentInOneInvocation is the same sweep for
// the other rule. Every arrangement that selects the combined action is refused
// wherever in the sequence it was put, and every arrangement that keeps the two
// apart compiles.
func TestNoTopologyPutsAuthorshipAndJudgmentInOneInvocation(t *testing.T) {
	t.Parallel()

	loader := deliveryLoader(t, &run{})
	catalog, err := CatalogFrom(loader.Registry)
	if err != nil {
		t.Fatalf("CatalogFrom() error = %v", err)
	}

	// The combined action put at each position of an otherwise sound sequence.
	// Wherever it goes, the definition is refused: the rule is about the
	// invocation and not about where the sequence puts it.
	sound := []string{"develop", "check", "review", "integrate"}
	for position := 0; position <= len(sound); position++ {
		states := slices.Insert(slices.Clone(sound), position, "develop-and-review")
		id := fmt.Sprintf("combined-at-%d", position)
		validated, err := arranged(id, states...).Validate(catalog)
		if err != nil {
			t.Fatalf("Validate(%s) error = %v", id, err)
		}
		_, err = loader.Compile(validated)
		if err == nil {
			t.Errorf("Compile(%s) compiled a sequence holding one invocation that writes the change and judges it", id)
			continue
		}
		if !strings.Contains(err.Error(), separation.AuthorshipIsNeverJudgment) {
			t.Errorf("Compile(%s) error = %v, and it does not name the policy it refused under", id, err)
		}
	}

	// The control: the same sequence without it compiles, so what the refusals
	// above catch is the combination rather than the shape.
	validated, err := arranged("sound", sound...).Validate(catalog)
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if _, err := loader.Compile(validated); err != nil {
		t.Errorf("Compile() error = %v; a sequence that keeps authorship and judgment apart is the one the harness runs", err)
	}
}

// TestNoTopologyMovesATargetBranchOutsideTheLease is the invariant
// `one-promotion-per-target-branch` held where a definition could otherwise reach
// around it. The registry holds an action that moves the branch and takes no
// lease; no sequence selecting it compiles, however much evidence it collected
// first.
func TestNoTopologyMovesATargetBranchOutsideTheLease(t *testing.T) {
	t.Parallel()

	loader := deliveryLoader(t, &run{})
	catalog, err := CatalogFrom(loader.Registry)
	if err != nil {
		t.Fatalf("CatalogFrom() error = %v", err)
	}
	validated, err := arranged("unleased", "develop", "check", "review", "promote").Validate(catalog)
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	_, err = loader.Compile(validated)
	if err == nil {
		t.Fatal("Compile() compiled a sequence that moves the target branch outside the lease")
	}
	if !strings.Contains(err.Error(), separation.PromotionIsNeverUnleased) {
		t.Errorf("Compile() error = %v, and it does not name the policy it refused under", err)
	}
}

// TestTheDeliveryFixturesPassEverySeparationPolicy is the parity claim: the
// sequences this repository already writes down are admitted unchanged, so
// nothing here changed what the harness does.
func TestTheDeliveryFixturesPassEverySeparationPolicy(t *testing.T) {
	t.Parallel()

	loader := deliveryLoader(t, &run{})
	for _, fixture := range []string{"delivery.yaml", "delivery-rewritten.yaml", "delivery-amended.yaml"} {
		if _, err := loader.LoadFile("testdata/" + fixture); err != nil {
			t.Errorf("LoadFile(%s) error = %v", fixture, err)
		}
	}
}

// permutations is every ordering of what it is given.
func permutations(items []string) [][]string {
	if len(items) <= 1 {
		return [][]string{slices.Clone(items)}
	}
	var ordered [][]string
	for index := range items {
		rest := slices.Clone(items)
		picked := rest[index]
		rest = slices.Delete(rest, index, index+1)
		for _, tail := range permutations(rest) {
			ordered = append(ordered, append([]string{picked}, tail...))
		}
	}
	return ordered
}
