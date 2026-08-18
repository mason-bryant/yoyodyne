package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/staleness"
)

func TestAnAmendedDocumentIsReportedAgainstWhatTracesToIt(t *testing.T) {
	// Not parallel: the state root every command builds its stores under is set
	// for this process.
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())

	configPath := writeConfig(t, validConfig)
	project := filepath.Dir(configPath)
	writeArtifact(t, project, "docs/product/brief.md", revisedArtifact("brief", "brief", nil,
		revision("created", "2026-08-01T00:00:00Z", "recorded when identity arrived"),
		revision("amended", "2026-08-10T00:00:00Z", "the product is for teams as well as individuals"))+
		"\n# Product brief\n\nIntent in, software out.\n")
	writeArtifact(t, project, "docs/product/goals/v1-goals.md", revisedArtifact("v1-goals", "goals", []string{"brief"},
		revision("created", "2026-08-02T00:00:00Z", "recorded when identity arrived"))+`
# V1 goals

An introduction.

## Goals

- Maintain a traceable chain.
`)

	stdout, stderr, code := runCLI(t, "stale", "--config", configPath)
	// Staleness is never a failure: an exit status that treated it as one would
	// make amending a document something to avoid doing.
	if code != 0 {
		t.Fatalf("stale code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{
		"v1-goals [goals] docs/product/goals/v1-goals.md, last revised 2026-08-02",
		"brief was amended 2026-08-10 by the product-manager: the product is for teams as well as individuals",
		"none of this is stopped, closed, or reordered",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("stale stdout = %q, want it to contain %q", stdout, want)
		}
	}

	stdout, stderr, code = runCLI(t, "stale", "--config", configPath, "--json")
	if code != 0 {
		t.Fatalf("stale --json code = %d, stderr = %q", code, stderr)
	}
	var reported struct {
		Documents []struct {
			ID      string `json:"id"`
			Kind    string `json:"kind"`
			Changes []struct {
				ArtifactID string `json:"artifact_id"`
				Action     string `json:"action"`
			} `json:"changes"`
		} `json:"documents"`
	}
	if err := json.Unmarshal([]byte(stdout), &reported); err != nil {
		t.Fatalf("Unmarshal() error = %v over %q", err, stdout)
	}
	if len(reported.Documents) != 1 || reported.Documents[0].ID != "v1-goals" {
		t.Fatalf("documents = %#v", reported.Documents)
	}
	if changes := reported.Documents[0].Changes; len(changes) != 1 || changes[0].ArtifactID != "brief" || changes[0].Action != "amended" {
		t.Fatalf("changes = %#v", changes)
	}
}

func TestARepositoryNothingHasMovedUnderSaysSo(t *testing.T) {
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())

	configPath := writeConfig(t, validConfig)
	project := filepath.Dir(configPath)
	writeArtifact(t, project, "docs/product/brief.md",
		revisedArtifact("brief", "brief", nil, revision("created", "2026-08-01T00:00:00Z", "recorded"))+"\nIntent in, software out.\n")
	writeArtifact(t, project, "docs/product/goals/v1-goals.md",
		revisedArtifact("v1-goals", "goals", []string{"brief"}, revision("created", "2026-08-02T00:00:00Z", "recorded"))+
			"\n# V1 goals\n\nAn introduction.\n\n## Goals\n\n- Maintain a traceable chain.\n")

	stdout, stderr, code := runCLI(t, "stale", "--config", configPath)
	if code != 0 {
		t.Fatalf("stale code = %d, stderr = %q", code, stderr)
	}
	// Whether the headline says "nothing" or "no document" depends on whether a
	// tracker answered here, which this fixture does not decide; what it does
	// decide is that no document is reported.
	if !strings.Contains(stdout, "downstream of a recorded change is unanswered.") || strings.Contains(stdout, "documents:") {
		t.Fatalf("stale stdout = %q", stdout)
	}
	if strings.Contains(stdout, "none of this is stopped") {
		t.Fatalf("stale stdout = %q, want no advice about work nobody found", stdout)
	}
}

func TestStaleWorkIsReportedWithWhatMovedAndWhatWasNotJudged(t *testing.T) {
	t.Parallel()

	report := staleness.Report{
		WorkItems: []staleness.WorkItem{{
			ID:         "ifd.1",
			Title:      "Work built on the old wording",
			Status:     "open",
			Priority:   2,
			AdmittedAt: time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC),
			Goal:       "Maintain a traceable chain.",
			ArtifactID: "v1-goals",
			Changes: []staleness.Change{{
				ArtifactID: "v1-goals",
				Action:     artifact.ActionAmended,
				At:         time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC),
				By:         "product-manager",
				Reason:     "the traceability goal now names verification",
			}},
		}},
		Unjudged: []staleness.Unjudged{{WorkItemID: "ifd.3", Reason: "the tracker records no admission time for it"}},
		Admitted: 4,
		Judged:   2,
	}

	var rendered bytes.Buffer
	printStaleness(&rendered, report, "")
	printed := rendered.String()
	for _, want := range []string{
		"0 documents and 1 open work item are downstream of a change nobody has answered.",
		"ifd.1 [p2, open] Work built on the old wording",
		"admitted 2026-08-05, serving a goal stated in v1-goals: Maintain a traceable chain.",
		"v1-goals was amended 2026-08-10 by the product-manager: the traceability goal now names verification",
		// What was not judged is part of the report: an item missing from a listing
		// reads exactly like an item nothing has moved under.
		"4 admitted items: 2 of them name a goal these changes could be followed to",
		`1 name none this could follow ("yoyo goals attribution" reports those)`,
		"1 could not be judged",
		"ifd.3: the tracker records no admission time for it",
	} {
		if !strings.Contains(printed, want) {
			t.Fatalf("report = %q, want it to contain %q", printed, want)
		}
	}
}

func TestATrackerThatCannotBeReadCostsTheWorkHalfAndNotTheReport(t *testing.T) {
	t.Parallel()

	report := staleness.Report{
		Documents: []staleness.Document{{
			ID:        "v1-goals",
			Kind:      artifact.KindGoals,
			Path:      "docs/product/goals/v1-goals.md",
			RevisedAt: time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC),
			Changes: []staleness.Change{{
				ArtifactID: "brief", Action: artifact.ActionAmended,
				At: time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC), By: "product-manager", Reason: "restated",
			}},
		}},
	}

	var rendered bytes.Buffer
	printStaleness(&rendered, report, "list open work items: bd is not installed")
	printed := rendered.String()
	// The documents still report, and the queue is never rendered as one nothing
	// has moved under when nothing read it: it is left out of the count rather
	// than counted as none.
	if headline, _, _ := strings.Cut(printed, "\n"); headline != "1 document is downstream of a change nobody has answered." {
		t.Fatalf("headline = %q, want the documents counted and the unread queue left out of the count", headline)
	}
	if !strings.Contains(printed, "v1-goals [goals] docs/product/goals/v1-goals.md, last revised 2026-08-02") {
		t.Fatalf("report = %q", printed)
	}
	if !strings.Contains(printed, "no work item was judged: the admitted work could not be read (list open work items: bd is not installed).") {
		t.Fatalf("report = %q", printed)
	}
}

func TestOnlyTheChangesThatFitAreListedAndTheRestAreCounted(t *testing.T) {
	t.Parallel()

	document := staleness.Document{ID: "v1-harness", Kind: artifact.KindDesign, Path: "docs/designs/v1-harness.md"}
	for index := 0; index < maxRenderedChanges+2; index++ {
		document.Changes = append(document.Changes, staleness.Change{
			ArtifactID: "brief", Action: artifact.ActionAmended,
			At: time.Date(2026, 8, 10-index, 0, 0, 0, 0, time.UTC), By: "product-manager", Reason: "restated",
		})
	}

	var rendered bytes.Buffer
	printStaleness(&rendered, staleness.Report{Documents: []staleness.Document{document}}, "")
	if !strings.Contains(rendered.String(), "and 2 earlier change(s) upstream of it") {
		t.Fatalf("report = %q", rendered.String())
	}
}

// revisedArtifact writes an artifact's frontmatter with the revisions a test
// needs, rather than the single creation artifactDocument records: what this
// package reports is decided by when each document last spoke, so the dates are
// the fixture.
func revisedArtifact(id, kind string, supports []string, revisions ...string) string {
	var rendered strings.Builder
	rendered.WriteString("---\nid: " + id + "\nkind: " + kind + "\ntitle: " + id + "\n")
	if len(supports) > 0 {
		rendered.WriteString("supports:\n")
		for _, reference := range supports {
			rendered.WriteString("    - " + reference + "\n")
		}
	}
	rendered.WriteString("status: active\nrevisions:\n")
	for _, entry := range revisions {
		rendered.WriteString(entry)
	}
	rendered.WriteString("---\n")
	return rendered.String()
}

func revision(action, at, reason string) string {
	owner, _ := artifact.Owner(artifact.KindGoals)
	return "    - action: " + action + "\n      by: " + string(owner) +
		"\n      at: " + at + "\n      reason: " + reason + "\n"
}
