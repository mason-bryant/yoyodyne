package orchestrator

import (
	"slices"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/review"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The durable schema keeps its own copy of the reviewer's two vocabularies, so a
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
}
