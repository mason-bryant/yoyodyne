package staleness

import (
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/goal"
)

func TestAnAmendmentIsReportedAgainstEverythingDownstreamOfIt(t *testing.T) {
	t.Parallel()

	// The brief is amended, so the goals that serve it and the design that serves
	// those goals have both stopped being documents anybody has been over since.
	// The chain is followed as far as it runs rather than one link.
	artifacts := set(
		document("brief", artifact.KindBrief, nil,
			created("2026-08-01T00:00:00Z"),
			amended("2026-08-10T00:00:00Z", "the product is for teams as well as individuals")),
		document("v1-goals", artifact.KindGoals, []string{"brief"}, created("2026-08-02T00:00:00Z")),
		document("v1-harness", artifact.KindDesign, []string{"v1-goals"}, created("2026-08-03T00:00:00Z")),
	)

	report := Survey(artifacts, goal.Set{}, nil)
	stale := documentsByID(report)
	if len(stale) != 2 {
		t.Fatalf("stale documents = %#v, want the goals and the design", report.Documents)
	}
	goals, reported := stale["v1-goals"]
	if !reported || len(goals.Changes) != 1 {
		t.Fatalf("v1-goals = %#v", goals)
	}
	change := goals.Changes[0]
	if change.ArtifactID != "brief" || change.Action != artifact.ActionAmended ||
		change.Reason != "the product is for teams as well as individuals" {
		t.Fatalf("change = %#v, want the amendment named with what it was for", change)
	}
	if design, reported := stale["v1-harness"]; !reported || len(design.Changes) != 1 || design.Changes[0].ArtifactID != "brief" {
		t.Fatalf("v1-harness = %#v, want the amendment two links upstream of it", design)
	}
	// The document that was amended is not downstream of itself.
	if _, reported := stale["brief"]; reported {
		t.Fatalf("the amended document reported itself as stale: %#v", report.Documents)
	}
}

func TestADocumentRevisedAfterTheChangeIsNotReported(t *testing.T) {
	t.Parallel()

	// This is the only way staleness clears for a document, and it is the record
	// that somebody looked: the owner amended it after the change upstream.
	artifacts := set(
		document("brief", artifact.KindBrief, nil,
			created("2026-08-01T00:00:00Z"),
			amended("2026-08-10T00:00:00Z", "what finished means, restated")),
		document("v1-goals", artifact.KindGoals, []string{"brief"},
			created("2026-08-02T00:00:00Z"),
			amended("2026-08-11T00:00:00Z", "reconciled against the brief's new wording")),
		// And the design below it, which nobody has been over, still is.
		document("v1-harness", artifact.KindDesign, []string{"v1-goals"}, created("2026-08-03T00:00:00Z")),
	)

	report := Survey(artifacts, goal.Set{}, nil)
	stale := documentsByID(report)
	if _, reported := stale["v1-goals"]; reported {
		t.Fatalf("a document revised after the change upstream is still reported: %#v", report.Documents)
	}
	design, reported := stale["v1-harness"]
	if !reported || len(design.Changes) != 2 {
		t.Fatalf("v1-harness = %#v, want both the brief's amendment and the goals' own", design)
	}
	// Most recent first, so what an operator reads first is what moved last.
	if !design.Changes[0].At.After(design.Changes[1].At) {
		t.Fatalf("changes = %#v, want the most recent first", design.Changes)
	}
}

func TestWhatIsNotReportedAsStale(t *testing.T) {
	t.Parallel()

	artifacts := set(
		document("brief", artifact.KindBrief, nil,
			created("2026-08-01T00:00:00Z"),
			amended("2026-08-10T00:00:00Z", "restated")),
		// A document created after the change was written knowing it.
		document("late-goals", artifact.KindGoals, []string{"brief"}, created("2026-08-12T00:00:00Z")),
		// A document that has ended is not asked to answer for what happened
		// upstream of it afterwards.
		ended("old-goals", artifact.KindGoals, []string{"brief"},
			created("2026-08-02T00:00:00Z"),
			artifact.Revision{Action: artifact.ActionRetired, By: domain.RoleProductManager,
				At: moment("2026-08-05T00:00:00Z"), Reason: "v1 ended"}),
		// A reference that resolves to nothing is reported where the chain is
		// checked, and is not guessed at here.
		document("loose-design", artifact.KindDesign, []string{"goals-that-moved"}, created("2026-08-02T00:00:00Z")),
	)

	report := Survey(artifacts, goal.Set{}, nil)
	if len(report.Documents) != 0 {
		t.Fatalf("stale documents = %#v, want none", report.Documents)
	}
	if report.Anything() {
		t.Fatalf("report claims something to look at: %#v", report)
	}
}

func TestCreatingAnArtifactUpstreamChangesNothingDownstream(t *testing.T) {
	t.Parallel()

	// A document that did not exist cannot be what anybody was working from, so
	// its creation is not a change anything downstream has to answer for -- even
	// when it is recorded after the document that names it.
	artifacts := set(
		document("brief", artifact.KindBrief, nil, created("2026-08-20T00:00:00Z")),
		document("v1-goals", artifact.KindGoals, []string{"brief"}, created("2026-08-02T00:00:00Z")),
	)

	if report := Survey(artifacts, goal.Set{}, nil); len(report.Documents) != 0 {
		t.Fatalf("stale documents = %#v, want none", report.Documents)
	}
}

func TestTwoArtifactsThatSupportEachOtherAreReportedRatherThanFollowedForever(t *testing.T) {
	t.Parallel()

	artifacts := set(
		document("first", artifact.KindDesign, []string{"second"},
			created("2026-08-01T00:00:00Z"),
			amended("2026-08-10T00:00:00Z", "one half of a cycle")),
		document("second", artifact.KindDesign, []string{"first"}, created("2026-08-02T00:00:00Z")),
	)

	report := Survey(artifacts, goal.Set{}, nil)
	stale := documentsByID(report)
	if len(stale) != 1 {
		t.Fatalf("stale documents = %#v, want only the one that has not been over the change", report.Documents)
	}
	if second, reported := stale["second"]; !reported || len(second.Changes) != 1 {
		t.Fatalf("second = %#v", second)
	}
}

func TestWorkAdmittedBeforeTheGoalMovedIsReportedAndWorkAdmittedAfterIsNot(t *testing.T) {
	t.Parallel()

	artifacts := set(
		document("brief", artifact.KindBrief, nil, created("2026-08-01T00:00:00Z")),
		document("v1-goals", artifact.KindGoals, []string{"brief"},
			created("2026-08-02T00:00:00Z"),
			amended("2026-08-10T00:00:00Z", "the traceability goal now names verification")),
	)
	goals := statedGoals("Maintain a traceable chain.")
	items := []beads.WorkItem{
		admitted("ifd.1", "Work built on the old wording", "2026-08-05T00:00:00Z", "Maintain a traceable chain."),
		admitted("ifd.2", "Work admitted after the amendment", "2026-08-11T00:00:00Z", "Maintain a traceable chain."),
	}

	report := Survey(artifacts, goals, items)
	if report.Admitted != 2 || report.Judged != 2 {
		t.Fatalf("report = %d admitted, %d judged", report.Admitted, report.Judged)
	}
	if len(report.WorkItems) != 1 || report.WorkItems[0].ID != "ifd.1" {
		t.Fatalf("stale work = %#v, want only the item admitted before the amendment", report.WorkItems)
	}
	stale := report.WorkItems[0]
	if stale.ArtifactID != "v1-goals" || stale.Goal != "Maintain a traceable chain." {
		t.Fatalf("stale = %#v, want the goal it serves and the document stating it", stale)
	}
	if len(stale.Changes) != 1 || stale.Changes[0].Reason != "the traceability goal now names verification" {
		t.Fatalf("changes = %#v, want the amendment named with what it was for", stale.Changes)
	}
	// Marking work stale must not change what it is: nothing about the item's
	// place in the queue moves, and its status is reported as the tracker holds
	// it.
	if stale.Status != items[0].Status || stale.Priority != items[0].Priority {
		t.Fatalf("stale = %#v, want the item's own status and priority", stale)
	}
}

func TestWorkIsStaleWhenAnythingUpstreamOfItsGoalsDocumentChanges(t *testing.T) {
	t.Parallel()

	artifacts := set(
		document("brief", artifact.KindBrief, nil,
			created("2026-08-01T00:00:00Z"),
			amended("2026-08-10T00:00:00Z", "who the product is for")),
		document("v1-goals", artifact.KindGoals, []string{"brief"}, created("2026-08-02T00:00:00Z")),
	)
	items := []beads.WorkItem{admitted("ifd.1", "Work", "2026-08-05T00:00:00Z", "Maintain a traceable chain.")}

	report := Survey(artifacts, statedGoals("Maintain a traceable chain."), items)
	if len(report.WorkItems) != 1 || len(report.WorkItems[0].Changes) != 1 ||
		report.WorkItems[0].Changes[0].ArtifactID != "brief" {
		t.Fatalf("stale work = %#v, want the amendment upstream of the goals document", report.WorkItems)
	}
}

func TestWorkThisCannotAnswerForIsCountedRatherThanReportedAsUnmoved(t *testing.T) {
	t.Parallel()

	artifacts := set(
		document("brief", artifact.KindBrief, nil, created("2026-08-01T00:00:00Z")),
		document("v1-goals", artifact.KindGoals, []string{"brief"},
			created("2026-08-02T00:00:00Z"),
			amended("2026-08-10T00:00:00Z", "reworded")),
	)
	goals := statedGoals("Maintain a traceable chain.")
	unattributed := beads.WorkItem{ID: "ifd.2", Title: "Admitted before goals were checked", Status: "open",
		CreatedAt: moment("2026-08-05T00:00:00Z")}
	undated := admitted("ifd.3", "Admitted at no recorded time", "", "Maintain a traceable chain.")
	items := []beads.WorkItem{
		admitted("ifd.1", "Work", "2026-08-05T00:00:00Z", "Maintain a traceable chain."),
		unattributed,
		undated,
	}

	report := Survey(artifacts, goals, items)
	if report.Admitted != 3 || report.Judged != 1 {
		t.Fatalf("report = %d admitted, %d judged, want 3 and 1", report.Admitted, report.Judged)
	}
	// An item whose admission time the tracker does not carry is named rather than
	// left out: silence about it reads exactly like an item nothing has moved
	// under.
	if len(report.Unjudged) != 1 || report.Unjudged[0].WorkItemID != "ifd.3" {
		t.Fatalf("unjudged = %#v, want the item with no admission time", report.Unjudged)
	}
	if len(report.WorkItems) != 1 || report.WorkItems[0].ID != "ifd.1" {
		t.Fatalf("stale work = %#v", report.WorkItems)
	}
}

func TestAnAttributionToAGoalNoDocumentStatesIsNotFollowed(t *testing.T) {
	t.Parallel()

	// A claim that is wrong is a gap in the chain, reported where attributions
	// are. Following it here would be inventing the reference it does not have.
	artifacts := set(
		document("brief", artifact.KindBrief, nil, created("2026-08-01T00:00:00Z")),
		document("v1-goals", artifact.KindGoals, []string{"brief"},
			created("2026-08-02T00:00:00Z"),
			amended("2026-08-10T00:00:00Z", "reworded")),
	)
	items := []beads.WorkItem{admitted("ifd.1", "Work", "2026-08-05T00:00:00Z", "Ship the prototype.")}

	report := Survey(artifacts, statedGoals("Maintain a traceable chain."), items)
	if len(report.WorkItems) != 0 || report.Judged != 0 || len(report.Unjudged) != 0 {
		t.Fatalf("report = %#v, want an item this cannot follow to be counted and not judged", report)
	}
}

// set assembles an artifact set the way the store hands one over: sorted by id,
// so what a survey reports over it is in the same order twice.
func set(artifacts ...artifact.Artifact) artifact.Set {
	sorted := make([]artifact.Artifact, len(artifacts))
	copy(sorted, artifacts)
	for first := range sorted {
		for second := first + 1; second < len(sorted); second++ {
			if sorted[second].ID < sorted[first].ID {
				sorted[first], sorted[second] = sorted[second], sorted[first]
			}
		}
	}
	return artifact.Set{Artifacts: sorted}
}

func document(id string, kind artifact.Kind, supports []string, revisions ...artifact.Revision) artifact.Artifact {
	return artifact.Artifact{
		ID:        id,
		Kind:      kind,
		Title:     id,
		Supports:  supports,
		Status:    artifact.StatusActive,
		Revisions: revisions,
		Path:      "docs/" + id + ".md",
	}
}

func ended(id string, kind artifact.Kind, supports []string, revisions ...artifact.Revision) artifact.Artifact {
	recorded := document(id, kind, supports, revisions...)
	recorded.Status = artifact.StatusRetired
	return recorded
}

func created(at string) artifact.Revision {
	return artifact.Revision{Action: artifact.ActionCreated, By: domain.RoleProductManager, At: moment(at), Reason: "recorded"}
}

func amended(at, reason string) artifact.Revision {
	return artifact.Revision{Action: artifact.ActionAmended, By: domain.RoleProductManager, At: moment(at), Reason: reason}
}

func moment(at string) time.Time {
	if at == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339, at)
	if err != nil {
		panic(err)
	}
	return parsed
}

// statedGoals is the goals as a repository states them, resolved out of the
// goals artifact the tests amend.
func statedGoals(statements ...string) goal.Set {
	goals := goal.Set{Sources: []string{"v1-goals"}}
	for _, statement := range statements {
		goals.Goals = append(goals.Goals, goal.Goal{
			Statement:  statement,
			ArtifactID: "v1-goals",
			Path:       "docs/v1-goals.md",
			InForce:    true,
		})
	}
	return goals
}

func admitted(id, title, at, servingGoal string) beads.WorkItem {
	return beads.WorkItem{
		ID:        id,
		Title:     title,
		Status:    "open",
		Priority:  2,
		Notes:     goal.Note(servingGoal),
		CreatedAt: moment(at),
	}
}

func documentsByID(report Report) map[string]Document {
	indexed := make(map[string]Document, len(report.Documents))
	for _, document := range report.Documents {
		indexed[document.ID] = document
	}
	return indexed
}
