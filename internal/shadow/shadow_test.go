package shadow

import (
	"errors"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// recordedReviews is the branch review log a comparison reads, and the prices
// beside it. It is a double for the store rather than for the comparison: what
// is under test is the join over what was recorded.
type recordedReviews struct {
	reviews []runstate.BranchReview
	prices  map[string]runstate.ReviewPrice
	failure error
}

func (r recordedReviews) List() ([]runstate.BranchReview, error) {
	if r.failure != nil {
		return nil, r.failure
	}
	return r.reviews, nil
}

func (r recordedReviews) Price(reviewID string) runstate.ReviewPrice {
	if price, found := r.prices[reviewID]; found {
		return price
	}
	return runstate.ReviewPrice{ReviewID: reviewID, Unknown: "the event log is no longer recorded"}
}

func reviewed(id string, shadowed bool, findings ...runstate.Finding) runstate.BranchReview {
	decision := runstate.ReviewApprove
	if len(findings) > 0 {
		decision = runstate.ReviewRepair
	}
	return runstate.BranchReview{
		SchemaVersion: runstate.BranchReviewSchemaVersion,
		ReviewID:      id,
		ProductID:     "yoyodyne",
		Branch:        "milestone",
		BaseRef:       "main",
		BaseCommit:    "1111111111111111111111111111111111111111",
		HeadCommit:    "2222222222222222222222222222222222222222",
		Commits:       6,
		ReviewedAt:    time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC),
		Decision:      decision,
		Summary:       "what the branch adds up to",
		Findings:      findings,
		Shadow:        shadowed,
	}
}

func finding(severity, file, message string) runstate.Finding {
	return runstate.Finding{Severity: severity, Message: message, File: file, Line: 12}
}

// The measurement the experiment exists for: of what one reviewer found, what
// did the other find, what did it miss, and what did it raise alone.
func TestComparePairsAShadowReviewWithTheReviewItShadows(t *testing.T) {
	t.Parallel()

	baseline := reviewed("review-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", false,
		finding(runstate.SeverityMajor, "internal/cli/status.go", "the flag is set from both read loops"),
		finding(runstate.SeverityMajor, "docs/work.md", "the layout claim contradicts the branch's own test"),
		finding(runstate.SeverityMinor, "internal/cli/cost.go", "the CLI path has no test"),
	)
	shadowed := reviewed("review-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", true,
		// The same defect, in the reviewer's own words and at its own line.
		runstate.Finding{Severity: runstate.SeverityMinor, Message: "status.go assigns want twice", File: "internal/cli/status.go", Line: 88},
		finding(runstate.SeverityMinor, "internal/cli/cost.go", "no test covers the new flag"),
		finding(runstate.SeverityMinor, "internal/review/verdict.go", "this comment reads oddly"),
	)
	shadowed.Model = "sonnet"
	comparer := Comparer{Reviews: recordedReviews{
		reviews: []runstate.BranchReview{baseline, shadowed},
		prices: map[string]runstate.ReviewPrice{
			baseline.ReviewID: {ReviewID: baseline.ReviewID, CostUSD: 2.00, Invocations: 1},
			shadowed.ReviewID: {ReviewID: shadowed.ReviewID, CostUSD: 0.40, Invocations: 1},
		},
	}}

	report, err := comparer.Compare("")
	if err != nil {
		t.Fatalf("Compare() error = %v", err)
	}
	if len(report.Comparisons) != 1 || len(report.Unpaired) != 0 {
		t.Fatalf("Compare() = %#v", report)
	}
	comparison := report.Comparisons[0]
	if comparison.Baseline.ReviewID != baseline.ReviewID || comparison.Shadow.ReviewID != shadowed.ReviewID {
		t.Fatalf("comparison sides = %#v", comparison)
	}
	// Two of the baseline's three findings were also anchored by the shadow, and
	// the one that only exists in the accumulated shape of the branch was not.
	classes := map[string]Class{}
	for _, class := range comparison.Classes {
		classes[class.Severity] = class
	}
	if got := classes[runstate.SeverityMajor]; got.Baseline != 2 || got.Matched != 1 || got.Missed != 1 || got.ShadowOnly != 0 {
		t.Errorf("major class = %#v", got)
	}
	if got := classes[runstate.SeverityMinor]; got.Baseline != 1 || got.Shadow != 3 || got.Matched != 1 || got.Missed != 0 || got.ShadowOnly != 1 {
		t.Errorf("minor class = %#v", got)
	}
	// Severity is the class a finding is counted under, and each side is counted
	// under its own: a defect the shadow called minor and the baseline called
	// major is a matched pair whose severities disagree, and both are kept.
	for _, paired := range comparison.Findings {
		if paired.Outcome != OutcomeMatched || paired.Baseline.File != "internal/cli/status.go" {
			continue
		}
		if paired.Baseline.Severity != runstate.SeverityMajor || paired.Shadow.Severity != runstate.SeverityMinor {
			t.Errorf("matched pair lost one side's severity: %#v", paired)
		}
		if paired.Shadow.Message != "status.go assigns want twice" {
			t.Errorf("matched pair lost the shadow's own wording: %#v", paired.Shadow)
		}
	}
	if report.Totals.Missed != 1 || report.Totals.ShadowOnly != 1 || report.Totals.Matched != 2 {
		t.Errorf("totals = %#v", report.Totals)
	}
	if report.Totals.MissRate != 1.0/3.0 {
		t.Errorf("miss rate = %v, want one of three", report.Totals.MissRate)
	}
	if report.Totals.BaselineCostUSD != 2.00 || report.Totals.ShadowCostUSD != 0.40 ||
		report.Totals.UnpricedBaseline != 0 || report.Totals.UnpricedShadow != 0 {
		t.Errorf("costs = %#v", report.Totals)
	}
}

// A comparison is only a comparison if both reviews were given the same
// evidence. The branch is a moving name, so what pairs them is the commit.
func TestCompareOnlyPairsReviewsOfTheSameBranchState(t *testing.T) {
	t.Parallel()

	baseline := reviewed("review-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", false,
		finding(runstate.SeverityMajor, "store.go", "the two halves disagree"))
	moved := reviewed("review-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", true,
		finding(runstate.SeverityMajor, "store.go", "the two halves disagree"))
	moved.HeadCommit = "3333333333333333333333333333333333333333"
	comparer := Comparer{Reviews: recordedReviews{reviews: []runstate.BranchReview{baseline, moved}}}

	report, err := comparer.Compare("")
	if err != nil {
		t.Fatalf("Compare() error = %v", err)
	}
	if len(report.Comparisons) != 0 || len(report.Unpaired) != 1 {
		t.Fatalf("Compare() = %#v", report)
	}
	// A shadow review that measured nothing is reported rather than dropped: it
	// happened, and it cost money.
	if report.Unpaired[0].ReviewID != moved.ReviewID {
		t.Errorf("unpaired = %#v", report.Unpaired[0])
	}
}

// Nothing can pair a finding that names no file, so each one is counted as a
// miss or as the shadow's own and the count is reported: a miss rate read as
// more certain than the pairing behind it is worse than no rate at all.
func TestCompareCountsWhatNothingCouldPair(t *testing.T) {
	t.Parallel()

	baseline := reviewed("review-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", false,
		runstate.Finding{Severity: runstate.SeverityMajor, Message: "the branch as a whole reads as two products"})
	shadowed := reviewed("review-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", true,
		runstate.Finding{Severity: runstate.SeverityMinor, Message: "the commits are hard to follow"})
	comparer := Comparer{Reviews: recordedReviews{reviews: []runstate.BranchReview{baseline, shadowed}}}

	report, err := comparer.Compare("")
	if err != nil {
		t.Fatalf("Compare() error = %v", err)
	}
	comparison := report.Comparisons[0]
	if comparison.UnlocatedBaseline != 1 || comparison.UnlocatedShadow != 1 {
		t.Fatalf("unlocated counts = %#v", comparison)
	}
	if report.Totals.Missed != 1 || report.Totals.ShadowOnly != 1 || report.Totals.Matched != 0 {
		t.Errorf("totals = %#v", report.Totals)
	}
	// Neither review could be priced, so both totals are floors and say so.
	if report.Totals.UnpricedBaseline != 1 || report.Totals.UnpricedShadow != 1 {
		t.Errorf("unpriced sides = %#v, want one on each", report.Totals)
	}
}

// What the cheaper reviewer cost is the figure the experiment is read for, so a
// baseline whose event log is gone must not make it look like a floor.
func TestAnUnpricedReviewOnlyMarksItsOwnSide(t *testing.T) {
	t.Parallel()

	baseline := reviewed("review-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", false)
	shadowed := reviewed("review-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", true)
	comparer := Comparer{Reviews: recordedReviews{
		reviews: []runstate.BranchReview{baseline, shadowed},
		// Only the shadow's log survives, which is the ordinary case when the
		// verdict being measured against is months old.
		prices: map[string]runstate.ReviewPrice{
			shadowed.ReviewID: {ReviewID: shadowed.ReviewID, CostUSD: 0.40, Invocations: 1},
		},
	}}

	report, err := comparer.Compare("")
	if err != nil {
		t.Fatalf("Compare() error = %v", err)
	}
	if report.Totals.UnpricedShadow != 0 {
		t.Errorf("a priced shadow review was counted as unpriced: %#v", report.Totals)
	}
	if report.Totals.UnpricedBaseline != 1 {
		t.Errorf("an unpriced baseline was not counted: %#v", report.Totals)
	}
	if report.Totals.ShadowCostUSD != 0.40 || report.Totals.BaselineCostUSD != 0 {
		t.Errorf("costs = %#v", report.Totals)
	}
}

func TestCompareNarrowsToOneBranchAndReportsWhatItCannotRead(t *testing.T) {
	t.Parallel()

	here := reviewed("review-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", false)
	shadowedHere := reviewed("review-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", true)
	elsewhere := reviewed("review-cccccccccccccccccccccccccccccccc", false)
	elsewhere.Branch = "other"
	shadowedElsewhere := reviewed("review-dddddddddddddddddddddddddddddddd", true)
	shadowedElsewhere.Branch = "other"
	comparer := Comparer{Reviews: recordedReviews{reviews: []runstate.BranchReview{here, shadowedHere, elsewhere, shadowedElsewhere}}}

	report, err := comparer.Compare("other")
	if err != nil {
		t.Fatalf("Compare() error = %v", err)
	}
	if len(report.Comparisons) != 1 || report.Comparisons[0].Shadow.ReviewID != shadowedElsewhere.ReviewID {
		t.Fatalf("Compare(\"other\") = %#v", report)
	}

	// A log that cannot be read is a failure rather than an empty comparison:
	// reporting no difference between two reviewers because nothing was read is
	// the one wrong answer this can give.
	broken := Comparer{Reviews: recordedReviews{failure: errors.New("the log is corrupt")}}
	if _, err := broken.Compare(""); err == nil {
		t.Fatal("Compare() over an unreadable log returned no error")
	}
	if _, err := (Comparer{}).Compare(""); err == nil {
		t.Fatal("Compare() with nothing to read returned no error")
	}
}
