package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/shadow"
)

// What the comparison is read for: the counts per class, what each review cost,
// and the findings themselves — the classification the counts cannot make is a
// judgement about a finding's content, and the content has to be on the page to
// make it from.
func TestTheComparisonReportsEveryClassTheCostAndTheFindings(t *testing.T) {
	t.Parallel()

	missed := runstate.Finding{Severity: runstate.SeverityMajor, Message: "the flag is set from both read loops", File: "internal/cli/status.go", Line: 40}
	matched := runstate.Finding{Severity: runstate.SeverityMinor, Message: "the CLI path has no test", File: "internal/cli/cost.go", Line: 12}
	raised := runstate.Finding{Severity: runstate.SeverityMinor, Message: "this comment reads oddly", File: "internal/review/verdict.go", Line: 3}
	report := shadow.Report{
		Comparisons: []shadow.Comparison{{
			Branch:     "milestone",
			BaseCommit: "1111111111111111111111111111111111111111",
			HeadCommit: "2222222222222222222222222222222222222222",
			Baseline: shadow.Side{
				ReviewID: "review-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Model: "opus",
				Decision: runstate.ReviewRepair, Findings: 2, CostUSD: 2.00,
			},
			Shadow: shadow.Side{
				ReviewID: "review-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Model: "sonnet",
				Decision: runstate.ReviewRepair, Findings: 2, CostUSD: 0.40,
			},
			Classes: []shadow.Class{
				{Severity: runstate.SeverityMajor, Baseline: 1, Missed: 1},
				{Severity: runstate.SeverityMinor, Baseline: 1, Shadow: 2, Matched: 1, ShadowOnly: 1},
			},
			Findings: []shadow.Paired{
				{Outcome: shadow.OutcomeMissed, Baseline: &missed},
				{Outcome: shadow.OutcomeMatched, Baseline: &matched, Shadow: &matched},
				{Outcome: shadow.OutcomeShadowOnly, Shadow: &raised},
			},
		}},
		Totals: shadow.Totals{
			Comparisons: 1, Baseline: 2, Shadow: 2, Matched: 1, Missed: 1, ShadowOnly: 1,
			MissRate: 0.5, ShadowOnlyRate: 0.5, BaselineCostUSD: 2.00, ShadowCostUSD: 0.40,
		},
	}

	var stdout bytes.Buffer
	printShadowComparison(&stdout, report)
	for _, want := range []string{
		"milestone at 222222222222",
		"review-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa opus",
		"$0.40",
		"major",
		"- major [internal/cli/status.go:40]: the flag is set from both read loops",
		"+ minor [internal/review/verdict.go:3]: this comment reads oddly",
		"missed 1 of 2 baseline finding(s) (50%)",
		"baseline cost $2.00, shadow cost $0.40",
		// The number is a difference between two reviewers, and says so: a
		// shadow-only finding is a candidate false positive, not a proven one.
		"candidate false positive",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("output is missing %q:\n%s", want, stdout.String())
		}
	}
}

// A total nothing could price is a floor rather than a price, marked the same
// way every other figure the harness reports is.
func TestTheComparisonMarksACostItCouldNotRead(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	printShadowComparison(&stdout, shadow.Report{
		Comparisons: []shadow.Comparison{{
			Branch: "milestone", HeadCommit: "2222222222222222222222222222222222222222",
			Baseline: shadow.Side{ReviewID: "review-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Model: "opus", Unpriced: "the event log is no longer recorded"},
			Shadow:   shadow.Side{ReviewID: "review-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Model: "sonnet", CostUSD: 0.40, Truncated: true},
		}},
		Totals: shadow.Totals{Comparisons: 1, ShadowCostUSD: 0.40, UnpricedSides: 1},
	})
	for _, want := range []string{
		"unpriced: the event log is no longer recorded",
		"≥ $0.40",
		// Two reviews given different evidence are not a measurement of the
		// reviewers, and the comparison says so rather than leaving it to be
		// worked out from the record.
		"saw different evidence",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("output is missing %q:\n%s", want, stdout.String())
		}
	}
}

// A product whose reviewers have never been measured is not a failure to read,
// and the report says what would make one rather than printing an empty table.
func TestTheComparisonSaysHowToMakeAShadowReviewWhenThereAreNone(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	printShadowComparison(&stdout, shadow.Report{})
	if !strings.Contains(stdout.String(), "yoyo review --shadow") {
		t.Errorf("output = %q", stdout.String())
	}
}
