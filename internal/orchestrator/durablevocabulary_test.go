package orchestrator

import (
	"slices"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/landing"
	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The durable schema keeps its own copy of the reviewer's vocabularies, so a
// record is checked against what a record may hold rather than against whatever
// this version of the harness happens to produce. The copy is worth having; the
// drift between the copies is not. A severity or a decision the reviewer can
// produce and the schema will not store is refused at the moment a run records
// what its reviewer decided — in a process that has already told the tracker and
// the operator, and whose next act is to end the run over it.
//
// This is what makes that a failing check instead. Both lists are closed by
// decision, for the reasons recorded on them in the runstate package, so adding a
// severity or a decision means adding it in both places; this is what says
// whether that was done.
func TestTheDurableSchemaStoresEveryVerdictTheReviewerCanProduce(t *testing.T) {
	t.Parallel()

	durableSeverities := runstate.FindingSeverities()
	for _, severity := range review.Severities() {
		if !slices.Contains(durableSeverities, string(severity)) {
			t.Fatalf("the reviewer can produce severity %q and the durable schema stores only %v", severity, durableSeverities)
		}
		// Checked through the conversion a run actually stores findings by, so the
		// vocabulary is held where it crosses rather than only where it is listed.
		findings := durableFindings([]review.Finding{{
			Severity: severity,
			Message:  "the record has to be able to carry this",
			Location: &review.Location{File: "internal/run.go", Line: 12},
		}})
		if len(findings) != 1 {
			t.Fatalf("durableFindings() dropped the %q finding it was given", severity)
		}
		if err := findings[0].Validate(); err != nil {
			t.Fatalf("a stored %q finding is refused: %v", severity, err)
		}
	}

	durableDecisions := runstate.ReviewDecisions()
	for _, decision := range review.Decisions() {
		if !slices.Contains(durableDecisions, string(decision)) {
			t.Fatalf("the reviewer can decide %q and the durable schema stores only %v", decision, durableDecisions)
		}
	}

	// What an approval approves is the third of these vocabularies and the one that
	// decides most: an approval of evidence closes no work item, so a word the
	// record could not carry would settle the item as though the reviewer had said
	// the other thing.
	durableApprovals := runstate.ReviewApprovals()
	for _, approval := range review.Approvals() {
		if !slices.Contains(durableApprovals, string(approval)) {
			t.Fatalf("the reviewer can approve %q and the durable schema stores only %v", approval, durableApprovals)
		}
		// Checked through a state a run actually saves, so the vocabulary is held
		// where it crosses rather than only where it is listed.
		state := runstate.State{ReviewDecision: runstate.ReviewApprove, ReviewApproves: string(approval)}
		if err := state.Validate(); err != nil && strings.Contains(err.Error(), "review_approves is invalid") {
			t.Fatalf("a stored %q approval is refused: %v", approval, err)
		}
		// And the two derivations have to agree about which approval withholds the
		// closure, because the reviewer answers in one vocabulary and the closure is
		// decided in the other.
		if approval.Discharges() != state.ApprovalDischarges() {
			t.Errorf("approval %q discharges=%t as a verdict and %t as a record", approval,
				approval.Discharges(), state.ApprovalDischarges())
		}
	}
}

// The landing vocabulary is kept in two places for the reason the review
// vocabularies are, and it is held together here for a sharper reason than they
// are: what a landing outcome decides is whether the work item closes. An
// outcome a developer can claim and the schema will not store is refused at the
// save of a run whose change is already integrated, and the closure that then
// follows is decided from a record that never took the claim.
func TestTheDurableSchemaStoresEveryLandingADeveloperCanClaim(t *testing.T) {
	t.Parallel()

	durableOutcomes := runstate.LandingOutcomes()
	for _, outcome := range landing.Outcomes() {
		if !slices.Contains(durableOutcomes, string(outcome)) {
			t.Fatalf("a developer can claim landing %q and the durable schema stores only %v", outcome, durableOutcomes)
		}
		// Checked through a state a run actually saves, so the vocabulary is held
		// where it crosses rather than only where it is listed.
		state := runstate.State{LandingOutcome: string(outcome), LandingReason: "the record has to be able to carry this"}
		if err := state.Validate(); err != nil && strings.Contains(err.Error(), "landing_outcome is invalid") {
			t.Fatalf("a stored %q landing is refused: %v", outcome, err)
		}
	}
	// The two derivations have to agree about which outcome withholds the
	// closure, because the claim is made in one vocabulary and the closure is
	// decided in the other.
	for _, outcome := range landing.Outcomes() {
		claimed := landing.Claim{Outcome: outcome, Why: "recorded"}
		stored := runstate.State{LandingOutcome: string(outcome), LandingReason: "recorded"}
		if claimed.Discharges() != stored.LandingDischarges() {
			t.Errorf("landing %q discharges=%t as a claim and %t as a record", outcome,
				claimed.Discharges(), stored.LandingDischarges())
		}
	}
}
