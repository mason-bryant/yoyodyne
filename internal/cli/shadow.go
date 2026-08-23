package cli

// Reading the shadow-review experiment. A shadow review is made by `yoyo review
// --shadow`; this is the half that says what the collected ones amount to —
// which of the baseline reviewer's findings a differently configured one also
// made, which it missed, which it raised alone, and what each of them cost.
//
// It reads and reports. Nothing here decides that a cheaper reviewer is good
// enough, and nothing here changes what any review is worth: the numbers are
// evidence for a decision somebody else makes.

import (
	"fmt"
	"io"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/shadow"
)

type shadowCompareOutput struct {
	Comparison *shadow.Report `json:"comparison,omitempty"`
	Error      string         `json:"error,omitempty"`
}

// compareShadowReviews reports what every recorded shadow review made of the
// branch state it shadowed. It never invokes a provider: the reviews already
// happened, and this is the join over what they recorded.
func compareShadowReviews(configPath, branch string, jsonOutput bool, stdout, stderr io.Writer) int {
	parts, err := buildComponents(configPath)
	if err != nil {
		return reportCompareFailure(stdout, stderr, jsonOutput, err)
	}
	report, err := shadow.Comparer{Reviews: parts.branchReviews}.Compare(branch)
	if err != nil {
		return reportCompareFailure(stdout, stderr, jsonOutput, err)
	}
	if jsonOutput {
		return writeJSON(stdout, stderr, shadowCompareOutput{Comparison: &report})
	}
	printShadowComparison(stdout, report)
	return 0
}

func printShadowComparison(writer io.Writer, report shadow.Report) {
	if len(report.Comparisons) == 0 && len(report.Unpaired) == 0 {
		fmt.Fprintln(writer, "no shadow reviews are recorded, so there is nothing to compare;")
		fmt.Fprintln(writer, "`yoyo review --shadow --model <name> --base <ref>` makes one")
		return
	}
	for _, comparison := range report.Comparisons {
		printOneComparison(writer, comparison)
	}
	for _, unpaired := range report.Unpaired {
		fmt.Fprintf(writer, "%s compared nothing: %s\n", unpaired.ReviewID, unpaired.Reason)
	}
	printComparisonTotals(writer, report.Totals)
}

func printOneComparison(writer io.Writer, comparison shadow.Comparison) {
	fmt.Fprintf(writer, "\n%s at %s\n", comparison.Branch, shortCommit(comparison.HeadCommit))
	fmt.Fprintf(writer, "  baseline %s\n", renderSide(comparison.Baseline))
	fmt.Fprintf(writer, "  shadow   %s\n", renderSide(comparison.Shadow))
	fmt.Fprintf(writer, "  %-10s %9s %7s %8s %7s %12s\n", "severity", "baseline", "shadow", "matched", "missed", "shadow-only")
	for _, class := range comparison.Classes {
		fmt.Fprintf(writer, "  %-10s %9d %7d %8d %7d %12d\n",
			class.Severity, class.Baseline, class.Shadow, class.Matched, class.Missed, class.ShadowOnly)
	}
	for _, finding := range comparison.Findings {
		printPairedFinding(writer, finding)
	}
	// A finding anchored to no file could not be paired with anything, so it is
	// counted as missed or shadow-only whatever the other reviewer actually saw.
	if comparison.UnlocatedBaseline > 0 || comparison.UnlocatedShadow > 0 {
		fmt.Fprintf(writer, "  %d baseline and %d shadow finding(s) name no file, so nothing could pair them\n",
			comparison.UnlocatedBaseline, comparison.UnlocatedShadow)
	}
	if comparison.Baseline.Truncated || comparison.Shadow.Truncated {
		fmt.Fprintln(writer, "  one of these reviews was shown an incomplete change, so the two saw different evidence")
	}
}

// printPairedFinding lists one finding under the comparison, with its
// counterpart where the pairing found one: whether a missed finding was a local
// catch or one that only exists in the accumulated shape of the branch is a
// judgement about its content, and the content is here to make it from.
func printPairedFinding(writer io.Writer, finding shadow.Paired) {
	switch finding.Outcome {
	case shadow.OutcomeMatched:
		fmt.Fprintf(writer, "  = %s\n", renderComparedFinding(*finding.Baseline))
		fmt.Fprintf(writer, "    shadow said %s\n", renderComparedFinding(*finding.Shadow))
	case shadow.OutcomeMissed:
		fmt.Fprintf(writer, "  - %s\n", renderComparedFinding(*finding.Baseline))
	case shadow.OutcomeShadowOnly:
		fmt.Fprintf(writer, "  + %s\n", renderComparedFinding(*finding.Shadow))
	}
}

func renderComparedFinding(finding runstate.Finding) string {
	location := ""
	if strings.TrimSpace(finding.File) != "" {
		location = fmt.Sprintf(" [%s:%d]", finding.File, finding.Line)
	}
	return fmt.Sprintf("%s%s: %s", finding.Severity, location, singleLine(finding.Message))
}

// renderSide says which review a side was, what made it, and what it cost.
func renderSide(side shadow.Side) string {
	model := side.Model
	if model == "" {
		model = "unrecorded model"
	}
	return fmt.Sprintf("%s %s, %s, %d finding(s), %s", side.ReviewID, model, side.Decision, side.Findings, renderSidePrice(side))
}

func renderSidePrice(side shadow.Side) string {
	if side.Unpriced != "" {
		return "unpriced: " + side.Unpriced
	}
	return fmt.Sprintf("$%.2f", side.CostUSD)
}

// printComparisonTotals is the experiment's answer, and says what it is not: a
// rate over the findings one reviewer happened to make is a measurement of the
// difference between two reviewers, not a measurement of the defects in the
// branch.
func printComparisonTotals(writer io.Writer, totals shadow.Totals) {
	if totals.Comparisons == 0 {
		return
	}
	fmt.Fprintf(writer, "\n%d comparison(s): the shadow reviewer missed %d of %d baseline finding(s) (%.0f%%) and raised %d of its own %d (%.0f%%)\n",
		totals.Comparisons, totals.Missed, totals.Baseline, 100*totals.MissRate,
		totals.ShadowOnly, totals.Shadow, 100*totals.ShadowOnlyRate)
	// Each figure is marked from its own side's unpriced count: a lost baseline
	// event log says nothing about what the shadow reviewer cost, and marking
	// that figure as a floor would understate the number the comparison is read
	// for.
	fmt.Fprintf(writer, "baseline cost %s, shadow cost %s\n",
		renderTotal(totals.BaselineCostUSD, totals.UnpricedBaseline), renderTotal(totals.ShadowCostUSD, totals.UnpricedShadow))
	fmt.Fprintln(writer, "a shadow-only finding is a candidate false positive rather than a proven one:")
	fmt.Fprintln(writer, "the baseline reviewer is what this measures against, not what is true of the branch")
}

func shortCommit(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}

func reportCompareFailure(stdout, stderr io.Writer, jsonOutput bool, err error) int {
	if jsonOutput {
		if code := writeJSON(stdout, stderr, shadowCompareOutput{Error: err.Error()}); code != 0 {
			return code
		}
		return 1
	}
	fmt.Fprintf(stderr, "comparing shadow reviews failed: %v\n", err)
	return 1
}
