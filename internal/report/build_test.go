package report

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

const (
	oldBuild     = "0123456789abcdef0123456789abcdef01234567"
	currentBuild = "fedcba9876543210fedcba9876543210fedcba98"
)

// countedBuilds answers from a table and counts how often it was asked, so a
// test can see a pile of reports from one build asked about once.
type countedBuilds struct {
	behind map[string]int
	failed map[string]error
	asked  map[string]int
}

func (b *countedBuilds) Behind(_ context.Context, build string) (int, error) {
	if b.asked == nil {
		b.asked = map[string]int{}
	}
	b.asked[build]++
	if err := b.failed[build]; err != nil {
		return 0, err
	}
	return b.behind[build], nil
}

func TestACollectedReportCarriesTheBuildItsInvocationExecuted(t *testing.T) {
	t.Parallel()

	collected, err := Collect([]Entry{{Severity: SeverityNote, Message: "the index gap is still reported"}}, Attribution{
		Role:         "developer",
		RunID:        "run-0123456789abcdef0123456789abcdef",
		Build:        " " + oldBuild + " ",
		ProductID:    "yoyodyne",
		RepositoryID: "yoyodyne",
	}, time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if collected[0].Build != oldBuild {
		t.Fatalf("build = %q, want the invocation's %q", collected[0].Build, oldBuild)
	}

	reported := collected[0]
	reported.Build = "HEAD~3"
	if err := reported.Validate(); err == nil || !strings.Contains(err.Error(), "is not a revision") {
		t.Fatalf("Validate() = %v, want a build that is not a revision refused", err)
	}
	reported.Build = ""
	if err := reported.Validate(); err != nil {
		t.Fatalf("a report with no build failed validation: %v; every report filed before builds were recorded carries none", err)
	}
}

// The whole point: a report whose build predates a fix says by how many changes,
// so whoever reads it checks the main line before admitting work from it. A
// report with no build says so rather than reading as current.
func TestARenderedReportSaysHowFarItsBuildIsBehindTheTargetBranch(t *testing.T) {
	t.Parallel()

	behind := piledReport("report-00000000000000000000000000000001", SeverityNote, 1)
	behind.Build = oldBuild
	current := piledReport("report-00000000000000000000000000000002", SeverityNote, 2)
	current.Build = currentBuild
	unrecorded := piledReport("report-00000000000000000000000000000003", SeverityNote, 3)

	builds := &countedBuilds{behind: map[string]int{oldBuild: 14, currentBuild: 0}}
	gauge := NewGauge(context.Background(), builds)

	for _, test := range []struct {
		reported Report
		want     string
	}{
		{behind, "(" + behind.RunID + ", build 0123456789ab, 14 change(s) behind the target branch)"},
		{current, "(" + current.RunID + ", build fedcba987654, the target branch's tip)"},
		{unrecorded, "(" + unrecorded.RunID + ", no build recorded)"},
	} {
		if rendered := test.reported.RenderAgainst(gauge); !strings.Contains(rendered, test.want) {
			t.Errorf("RenderAgainst() = %q, want it to contain %q", rendered, test.want)
		}
	}
	// Without anything to count against, the build is still named and nothing
	// claims it is current.
	if rendered := behind.Render(); !strings.Contains(rendered, "("+behind.RunID+", build 0123456789ab)") {
		t.Errorf("Render() = %q, want the build named beside the run and no count", rendered)
	}

	// Several reports from one build are one question.
	again := behind
	again.ID = "report-00000000000000000000000000000004"
	again.RenderAgainst(gauge)
	if builds.asked[oldBuild] != 1 {
		t.Errorf("the build was asked about %d time(s), want once per listing", builds.asked[oldBuild])
	}
	if _, asked := builds.asked[""]; asked {
		t.Error("a report with no build was asked about")
	}
	if problem := gauge.Problem(); problem != "" {
		t.Errorf("Problem() = %q, want nothing when every build was counted", problem)
	}
	if measured := gauge.Measure([]Report{behind, current, unrecorded}); measured[oldBuild].Behind != 14 || len(measured) != 2 {
		t.Errorf("Measure() = %#v, want the two recorded builds", measured)
	}
}

func TestABuildThatCannotBeCountedIsSaidOnceRatherThanReadAsCurrent(t *testing.T) {
	t.Parallel()

	reported := piledReport("report-00000000000000000000000000000001", SeverityWarning, 1)
	reported.Build = oldBuild
	gauge := NewGauge(context.Background(), &countedBuilds{failed: map[string]error{
		oldBuild: errors.New("the build is not a revision this repository holds"),
	}})

	rendered := reported.RenderAgainst(gauge)
	if !strings.Contains(rendered, "build 0123456789ab, not counted against the target branch") {
		t.Fatalf("RenderAgainst() = %q, want the build said to be uncounted", rendered)
	}
	if strings.Contains(rendered, "tip") || strings.Contains(rendered, "behind") {
		t.Fatalf("RenderAgainst() = %q, an uncounted build read as a count", rendered)
	}
	if problem := gauge.Problem(); !strings.Contains(problem, "1 build(s)") || !strings.Contains(problem, "not a revision this repository holds") {
		t.Fatalf("Problem() = %q, want the reason said once", problem)
	}

	var nothing *Gauge
	if nothing.Problem() != "" || nothing.Measure([]Report{reported}) != nil {
		t.Fatal("a gauge with nothing to ask reported something")
	}
}
