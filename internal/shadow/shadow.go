// Package shadow measures one reviewer against another over the same change.
//
// A branch review is a replayable function of a branch state: the same commits
// over the same base, described the same way, judged under the same contract.
// That is what makes a differently configured reviewer measurable rather than
// merely arguable — point it at a branch state something else already decided,
// and the two verdicts are about the same evidence, so the difference between
// them is the difference between the reviewers.
//
// What this package does with the two verdicts is a join and nothing more. It
// says which of the baseline's findings the shadow also anchored to, which it
// did not, and which the shadow made alone, per severity, with what each review
// cost beside it. What it deliberately does not do is decide which reviewer was
// right: a finding only the shadow made is a candidate false positive rather
// than a proven one, and whether a missed finding was a local catch or one that
// only exists in the accumulated shape of the branch is a judgement about the
// finding's content. Both of those need the findings themselves, which is why
// the comparison carries them rather than only the counts.
package shadow

import (
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// severities is the order a comparison reads in: what a reviewer must not miss
// first, and what it can differ about with least consequence last.
var severities = []string{runstate.SeverityBlocker, runstate.SeverityMajor, runstate.SeverityMinor}

// Reviews is the recorded branch review evidence a comparison is read from. It
// is satisfied by runstate.BranchReviewStore, and it is deliberately only the
// reading half: measuring a reviewer records nothing and decides nothing.
type Reviews interface {
	List() ([]runstate.BranchReview, error)
	Price(reviewID string) runstate.ReviewPrice
}

// Outcome is what the pairing made of one finding.
type Outcome string

const (
	// OutcomeMatched is a baseline finding the shadow also anchored to.
	OutcomeMatched Outcome = "matched"
	// OutcomeMissed is a baseline finding the shadow did not make.
	OutcomeMissed Outcome = "missed"
	// OutcomeShadowOnly is a finding the shadow made that the baseline did not.
	// It is a candidate false positive rather than a false positive: the
	// baseline reviewer is the measurement, not the truth.
	OutcomeShadowOnly Outcome = "shadow-only"
)

// Side is one of the two reviews held up against each other: which review it
// was, what made it, what it decided, and what it cost.
type Side struct {
	ReviewID string `json:"review_id"`
	// Branch is the name this side's review was made under. It is carried per
	// side because the two need not match: a historical state is shadowed under
	// whatever local branch was put on that commit.
	Branch        string    `json:"branch,omitempty"`
	Model         string    `json:"model,omitempty"`
	ResolvedModel string    `json:"resolved_model,omitempty"`
	Decision      string    `json:"decision,omitempty"`
	ReviewedAt    time.Time `json:"reviewed_at"`
	Findings      int       `json:"findings"`
	// Truncated says this side was shown an incomplete change. A truncated
	// review held up against a complete one is a comparison of two different
	// pieces of evidence, so it is carried here rather than left to be inferred.
	Truncated bool    `json:"truncated,omitempty"`
	CostUSD   float64 `json:"cost_usd"`
	// Unpriced says why this review's cost could not be read, and is empty on
	// one that was priced. A side carrying it did not cost nothing.
	Unpriced string `json:"unpriced,omitempty"`
}

// Class is what the two reviews did with one severity of finding. Missed and
// ShadowOnly are the two halves the experiment exists to weigh: what a cheaper
// reviewer would have let through, and what it would have raised that the
// reviewer being measured against did not.
type Class struct {
	Severity   string `json:"severity"`
	Baseline   int    `json:"baseline"`
	Shadow     int    `json:"shadow"`
	Matched    int    `json:"matched"`
	Missed     int    `json:"missed"`
	ShadowOnly int    `json:"shadow_only"`
}

// Paired is one finding and what the pairing made of it. A matched pair carries
// both reviewers' own wording and severity, because two findings anchored to the
// same file have not necessarily said the same thing about it and nothing here
// can tell whether they did.
type Paired struct {
	Outcome  Outcome           `json:"outcome"`
	Baseline *runstate.Finding `json:"baseline,omitempty"`
	Shadow   *runstate.Finding `json:"shadow,omitempty"`
}

// Comparison is one shadow review held up against the review it shadows.
type Comparison struct {
	Branch     string   `json:"branch"`
	BaseCommit string   `json:"base_commit"`
	HeadCommit string   `json:"head_commit"`
	Baseline   Side     `json:"baseline"`
	Shadow     Side     `json:"shadow"`
	Classes    []Class  `json:"classes,omitempty"`
	Findings   []Paired `json:"findings,omitempty"`
	// UnlocatedBaseline and UnlocatedShadow count the findings on each side that
	// anchor to no file. Nothing can pair those, so each of them is counted as
	// missed or shadow-only whatever the other reviewer saw, and the count is
	// reported so a miss rate is not read as more certain than it is.
	UnlocatedBaseline int `json:"unlocated_baseline,omitempty"`
	UnlocatedShadow   int `json:"unlocated_shadow,omitempty"`
}

// Unpaired is a shadow review nothing could be compared with, and why. It is
// reported rather than dropped: a shadow review that measured nothing still
// happened and still cost money.
type Unpaired struct {
	ReviewID string `json:"review_id"`
	Branch   string `json:"branch"`
	Reason   string `json:"reason"`
}

// Totals is every comparison added together, which is the experiment's answer:
// the rate at which the shadow reviewer missed what the baseline found, the rate
// at which it raised what the baseline did not, and what each of them cost.
type Totals struct {
	Comparisons int     `json:"comparisons"`
	Baseline    int     `json:"baseline"`
	Shadow      int     `json:"shadow"`
	Matched     int     `json:"matched"`
	Missed      int     `json:"missed"`
	ShadowOnly  int     `json:"shadow_only"`
	MissRate    float64 `json:"miss_rate"`
	// ShadowOnlyRate is the share of the shadow's findings the baseline did not
	// make. It is the false-positive candidate rate rather than a false-positive
	// rate, because a finding the baseline missed is also one it did not make.
	ShadowOnlyRate  float64 `json:"shadow_only_rate"`
	BaselineCostUSD float64 `json:"baseline_cost_usd"`
	ShadowCostUSD   float64 `json:"shadow_cost_usd"`
	// UnpricedBaseline and UnpricedShadow count the reviews on each side whose
	// event log could not be read, while which that side's total is a floor
	// rather than a price. They are counted per side rather than together
	// because the figure this experiment is read for is what the cheaper
	// reviewer cost: marking it as a floor because the other side lost an event
	// log would understate exactly the number the whole comparison is about.
	UnpricedBaseline int `json:"unpriced_baseline,omitempty"`
	UnpricedShadow   int `json:"unpriced_shadow,omitempty"`
}

// Report is what comparing every recorded shadow review produced.
type Report struct {
	Comparisons []Comparison `json:"comparisons,omitempty"`
	Unpaired    []Unpaired   `json:"unpaired,omitempty"`
	Totals      Totals       `json:"totals"`
}

// Comparer holds shadow reviews up against the reviews they shadow.
type Comparer struct {
	Reviews Reviews
}

// Compare pairs every recorded shadow review with the review it shadows and
// reports what the two made of the same branch state. Naming a branch narrows it
// to that branch; naming nothing compares everything recorded.
//
// Two reviews shadow the same change when they judged the same head commit over
// the same base, whatever branch name each was made under. The commits are what
// make that exact rather than approximate: a branch is a moving name, and a
// shadow review of what the branch is today measures nothing about a verdict
// given on what it was last week.
func (c Comparer) Compare(branch string) (Report, error) {
	if c.Reviews == nil {
		return Report{}, errors.New("comparing shadow reviews needs the recorded branch reviews to read")
	}
	recorded, err := c.Reviews.List()
	if err != nil {
		return Report{}, fmt.Errorf("read the recorded branch reviews: %w", err)
	}
	wanted := strings.TrimSpace(branch)

	// The baseline of a branch state is the last review of it that was not a
	// shadow: an operator who reviewed the same state twice meant the second
	// answer, and every shadow of that state is measured against the same one.
	baselines := make(map[string]runstate.BranchReview)
	for _, reviewed := range recorded {
		if reviewed.Shadow || !decided(reviewed) {
			continue
		}
		baselines[stateKey(reviewed)] = reviewed
	}

	report := Report{}
	for _, reviewed := range recorded {
		if !reviewed.Shadow {
			continue
		}
		if wanted != "" && reviewed.Branch != wanted {
			continue
		}
		if !decided(reviewed) {
			report.Unpaired = append(report.Unpaired, Unpaired{
				ReviewID: reviewed.ReviewID, Branch: reviewed.Branch,
				Reason: "the shadow review reached no verdict, so there is nothing to compare",
			})
			continue
		}
		baseline, found := baselines[stateKey(reviewed)]
		if !found {
			report.Unpaired = append(report.Unpaired, Unpaired{
				ReviewID: reviewed.ReviewID, Branch: reviewed.Branch,
				Reason: fmt.Sprintf("no other review decided %s over %s, so this shadow has nothing to be measured against", short(reviewed.HeadCommit), short(reviewed.BaseCommit)),
			})
			continue
		}
		report.Comparisons = append(report.Comparisons, c.compare(baseline, reviewed))
	}
	report.Totals = total(report.Comparisons)
	return report, nil
}

// compare holds one pair of reviews up against each other.
func (c Comparer) compare(baseline, shadowed runstate.BranchReview) Comparison {
	comparison := Comparison{
		Branch:     shadowed.Branch,
		BaseCommit: shadowed.BaseCommit,
		HeadCommit: shadowed.HeadCommit,
		Baseline:   c.side(baseline),
		Shadow:     c.side(shadowed),
	}
	baselineOfShadow, shadowOfBaseline := pair(baseline.Findings, shadowed.Findings)
	classes := make(map[string]*Class, len(severities))
	classOf := func(severity string) *Class {
		if existing, found := classes[severity]; found {
			return existing
		}
		created := &Class{Severity: severity}
		classes[severity] = created
		return created
	}
	for index, finding := range baseline.Findings {
		class := classOf(finding.Severity)
		class.Baseline++
		if anchor(finding) == "" {
			comparison.UnlocatedBaseline++
		}
		if match := shadowOfBaseline[index]; match >= 0 {
			class.Matched++
			comparison.Findings = append(comparison.Findings, Paired{
				Outcome:  OutcomeMatched,
				Baseline: copyFinding(finding),
				Shadow:   copyFinding(shadowed.Findings[match]),
			})
			continue
		}
		class.Missed++
		comparison.Findings = append(comparison.Findings, Paired{Outcome: OutcomeMissed, Baseline: copyFinding(finding)})
	}
	for index, finding := range shadowed.Findings {
		class := classOf(finding.Severity)
		class.Shadow++
		if anchor(finding) == "" {
			comparison.UnlocatedShadow++
		}
		if baselineOfShadow[index] >= 0 {
			continue
		}
		class.ShadowOnly++
		comparison.Findings = append(comparison.Findings, Paired{Outcome: OutcomeShadowOnly, Shadow: copyFinding(finding)})
	}
	comparison.Classes = ordered(classes)
	return comparison
}

// side describes one review and prices it from its own event log.
func (c Comparer) side(reviewed runstate.BranchReview) Side {
	side := Side{
		ReviewID:      reviewed.ReviewID,
		Branch:        reviewed.Branch,
		Model:         reviewed.Model,
		ResolvedModel: reviewed.ResolvedModel,
		Decision:      reviewed.Decision,
		ReviewedAt:    reviewed.ReviewedAt,
		Findings:      len(reviewed.Findings),
		Truncated:     reviewed.Truncated,
	}
	price := c.Reviews.Price(reviewed.ReviewID)
	if !price.Known() {
		side.Unpriced = price.Unknown
		return side
	}
	side.CostUSD = price.CostUSD
	return side
}

// pair matches the two reviews' findings to each other by the file each anchors
// to, and returns for each finding the index of its counterpart or -1.
//
// The file is the whole of the match, deliberately. Two reviewers describing the
// same defect will not agree on its line and will not agree on its wording, so
// anything stricter would report a miss wherever they merely phrased it
// differently; anything looser would pair findings that have nothing to do with
// each other. Findings that anchor to no file are never paired, and are counted
// as such, because there is nothing about them to match on.
// The first result is indexed by shadow finding, the second by baseline
// finding, so each side can be walked in its own order.
func pair(baseline, shadowed []runstate.Finding) (baselineOfShadow, shadowOfBaseline []int) {
	baselineOfShadow = filled(len(shadowed))
	shadowOfBaseline = filled(len(baseline))
	available := make(map[string][]int, len(shadowed))
	for index, finding := range shadowed {
		file := anchor(finding)
		if file == "" {
			continue
		}
		available[file] = append(available[file], index)
	}
	for index, finding := range baseline {
		file := anchor(finding)
		if file == "" {
			continue
		}
		candidates := available[file]
		if len(candidates) == 0 {
			continue
		}
		// Several findings in one file are paired in the order each review made
		// them, which is the only order either of them supplies.
		shadowOfBaseline[index] = candidates[0]
		baselineOfShadow[candidates[0]] = index
		available[file] = candidates[1:]
	}
	return baselineOfShadow, shadowOfBaseline
}

func filled(size int) []int {
	matches := make([]int, size)
	for index := range matches {
		matches[index] = -1
	}
	return matches
}

// anchor is the file a finding points at, normalized so that two reviewers
// naming the same file differently are still matched. A finding with no file
// anchors to nothing and is never paired.
func anchor(finding runstate.Finding) string {
	file := strings.TrimSpace(finding.File)
	if file == "" {
		return ""
	}
	return path.Clean(file)
}

func copyFinding(finding runstate.Finding) *runstate.Finding {
	copied := finding
	return &copied
}

// ordered puts the classes in severity order, and drops the severities neither
// review used: a table with rows nothing happened in reads as though something
// was measured there.
func ordered(classes map[string]*Class) []Class {
	inOrder := make([]Class, 0, len(classes))
	for _, severity := range severities {
		if class, found := classes[severity]; found {
			inOrder = append(inOrder, *class)
			delete(classes, severity)
		}
	}
	// A severity the contract does not define cannot reach a validated record,
	// but a class is a count of what was recorded rather than of what should
	// have been, so anything left is reported instead of silently dropped.
	remaining := make([]string, 0, len(classes))
	for severity := range classes {
		remaining = append(remaining, severity)
	}
	sort.Strings(remaining)
	for _, severity := range remaining {
		inOrder = append(inOrder, *classes[severity])
	}
	return inOrder
}

func total(comparisons []Comparison) Totals {
	totals := Totals{Comparisons: len(comparisons)}
	for _, comparison := range comparisons {
		for _, class := range comparison.Classes {
			totals.Baseline += class.Baseline
			totals.Shadow += class.Shadow
			totals.Matched += class.Matched
			totals.Missed += class.Missed
			totals.ShadowOnly += class.ShadowOnly
		}
		if comparison.Baseline.Unpriced != "" {
			totals.UnpricedBaseline++
		}
		if comparison.Shadow.Unpriced != "" {
			totals.UnpricedShadow++
		}
		totals.BaselineCostUSD += comparison.Baseline.CostUSD
		totals.ShadowCostUSD += comparison.Shadow.CostUSD
	}
	if totals.Baseline > 0 {
		totals.MissRate = float64(totals.Missed) / float64(totals.Baseline)
	}
	if totals.Shadow > 0 {
		totals.ShadowOnlyRate = float64(totals.ShadowOnly) / float64(totals.Shadow)
	}
	return totals
}

// stateKey names the branch state a review judged: the base it was measured
// against and the head it reached. Two reviews share it when they were given the
// same commits over the same base, which is the whole of what makes them
// comparable.
//
// The branch name is deliberately not part of it. A branch is a moving label on
// a commit, and reaching a historical state to shadow it means putting some
// local branch on that commit — so keying on the name would make a review of
// exactly the same code unpairable for having been reached under a different
// label, which is the common case rather than the rare one. What the two
// reviewers were shown then differs by one name in one sentence of the context
// and by nothing in the code under review; the names are carried on each side so
// a reader can see when they differed.
func stateKey(reviewed runstate.BranchReview) string {
	return reviewed.BaseCommit + "\x00" + reviewed.HeadCommit
}

func decided(reviewed runstate.BranchReview) bool {
	return reviewed.Decision == runstate.ReviewApprove || reviewed.Decision == runstate.ReviewRepair
}

func short(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}
