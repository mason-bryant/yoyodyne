package readmodel

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/amendment"
	"github.com/mason-bryant/yoyodyne/internal/artifact"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
	"github.com/mason-bryant/yoyodyne/internal/sweep"
)

type fakeSweeps struct {
	recorded []runstate.Sweep
	fail     error
}

func (f fakeSweeps) List() ([]runstate.Sweep, []runstate.UnreadableSweep, error) {
	return f.recorded, nil, f.fail
}

// proposedChange is one undecided change against a design, raised the given
// age before the reading's moment.
func proposedChange(index int, age time.Duration) amendment.Proposal {
	return amendment.Proposal{
		SchemaVersion: amendment.SchemaVersion,
		ID:            fmt.Sprintf("amendment-%032x", index),
		Role:          domain.RoleDeveloper,
		Agent:         "developer",
		RunID:         fmt.Sprintf("run-%032x", index),
		WorkItemID:    fmt.Sprintf("yoyodyne-ifd.%d", 400+index),
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		Artifact:      "v1-design",
		Kind:          artifact.KindDesign,
		Owner:         domain.RoleArchitect,
		Change:        fmt.Sprintf("change %d", index),
		Why:           fmt.Sprintf("reason %d", index),
		RaisedAt:      moment.Add(-age),
	}
}

// architectPass is one recorded firing of the architect's task carrying her
// recommendations.
func architectPass(startedAt time.Time, recommendations ...sweep.Recommendation) runstate.Sweep {
	return runstate.Sweep{
		SchemaVersion: runstate.SweepSchemaVersion,
		ProductID:     "yoyodyne",
		Task:          "architect-amendments",
		Role:          domain.RoleArchitect,
		StartedAt:     startedAt,
		EndedAt:       startedAt.Add(time.Minute),
		Turns:         1,
		Result:        &sweep.Result{Status: sweep.StatusComplete, Summary: "argued", Recommendations: recommendations},
	}
}

// A queue being worked through names each undecided proposal and nothing
// about its age: it is not waiting on a person beyond the decision each
// already asks for. The counts are still carried, for the week's question.
func TestAYoungQueueIsNamedAndCountedButNotAged(t *testing.T) {
	t.Parallel()

	sources := quietSources()
	fresh := proposedChange(1, 2*24*time.Hour)
	sources.Amendments = fakeAmendments{records: []amendment.Record{{Proposal: &fresh}}}
	standing := ReadStanding(context.Background(), sources)
	if standing.Amendments.Undecided != 1 || standing.Amendments.OldestAge != 2*24*time.Hour {
		t.Fatalf("Amendments = %+v", standing.Amendments)
	}
	if len(standing.NeedsHuman) != 1 || !strings.Contains(standing.NeedsHuman[0].What, fresh.ID) {
		t.Fatalf("NeedsHuman = %+v, want the one proposal named and nothing about age", standing.NeedsHuman)
	}
	if strings.Contains(standing.Render(), "proposed change(s) are undecided") {
		t.Fatalf("a young queue is reported as aged:\n%s", standing.Render())
	}
}

// The failure the whole amendment channel has: proposals raised faster than
// anything decides them, which no list of them shows and only the oldest one's
// age does. It is the report pile's line, for the queue.
func TestAQueueNothingIsDrainingIsNamedByItsAge(t *testing.T) {
	t.Parallel()

	sources := quietSources()
	old := proposedChange(1, 23*24*time.Hour)
	fresh := proposedChange(2, time.Hour)
	sources.Amendments = fakeAmendments{records: []amendment.Record{{Proposal: &old}, {Proposal: &fresh}}}
	standing := ReadStanding(context.Background(), sources)
	rendered := standing.Render()
	for _, want := range []string{
		"2 of 2 proposed change(s) are undecided, the oldest raised 23d ago, against the architect's documents",
		"the architect's, or the operator's in their stead",
		"not keeping up, or none is enabled",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered is missing %q:\n%s", want, rendered)
		}
	}
}

// What the owner argued on a recurring pass is the batch the operator decides
// from: said once as one decision list, and beside each proposal it covers. A
// proposal decided since is dropped, and a later pass's recommendation on the
// same proposal stands over an earlier one.
func TestTheOwnersRecommendationsAreTheBatchTheOperatorDecides(t *testing.T) {
	t.Parallel()

	sources := quietSources()
	first := proposedChange(1, 10*24*time.Hour)
	second := proposedChange(2, 9*24*time.Hour)
	decided := proposedChange(3, 8*24*time.Hour)
	unargued := proposedChange(4, 2*24*time.Hour)
	decision, err := decided.Decide(amendment.VerdictApproved, amendment.DeciderOperator, "", moment.Add(-time.Hour))
	if err != nil {
		t.Fatalf("Decide() error = %v", err)
	}
	sources.Amendments = fakeAmendments{records: []amendment.Record{
		{Proposal: &first}, {Proposal: &second}, {Proposal: &decided}, {Proposal: &unargued}, {Decision: &decision},
	}}
	sources.Sweeps = fakeSweeps{recorded: []runstate.Sweep{
		architectPass(moment.Add(-2*24*time.Hour),
			sweep.Recommendation{Proposal: first.ID, Verdict: sweep.RecommendDecline, Reason: "no"},
			sweep.Recommendation{Proposal: decided.ID, Verdict: sweep.RecommendApprove, Reason: "yes"},
		),
		architectPass(moment.Add(-6*time.Hour),
			sweep.Recommendation{Proposal: first.ID, Verdict: sweep.RecommendApprove, Reason: "on reflection, yes"},
			sweep.Recommendation{Proposal: second.ID, Verdict: sweep.RecommendMerge, Reason: "the same change", Into: first.ID},
		),
	}}
	standing := ReadStanding(context.Background(), sources)
	if len(standing.RecommendedAmendments) != 2 {
		t.Fatalf("RecommendedAmendments = %+v, want the two undecided ones argued, and the decided one dropped", standing.RecommendedAmendments)
	}
	if got := standing.RecommendedAmendments[0]; got.Proposal != first.ID || got.Verdict != sweep.RecommendApprove || got.Reason != "on reflection, yes" {
		t.Fatalf("first = %+v, want the later pass's recommendation to stand", got)
	}
	if got := standing.RecommendedAmendments[1]; got.Proposal != second.ID || got.Verdict != sweep.RecommendMerge || got.Into != first.ID {
		t.Fatalf("second = %+v, want the merge with what it merges into", got)
	}
	rendered := standing.Render()
	for _, want := range []string{
		"the architect has recommended on 2 undecided proposed changes (1 to approve, 1 to merge), the latest in the pass of architect-amendments at 2026-08-30T06:00:00Z — the operator's — `yoyo amendment approve|decline <id> --reason ...` records each decision, and `yoyo sweeps --task architect-amendments` has the reasons",
		"a change to v1-design is proposed and undecided (" + first.ID + "); the architect recommends approve — the architect's",
		"a change to v1-design is proposed and undecided (" + second.ID + "); the architect recommends merge into " + first.ID,
		"a change to v1-design is proposed and undecided (" + unargued.ID + ") — the architect's",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered is missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, decided.ID) {
		t.Fatalf("a decided proposal is still on the line:\n%s", rendered)
	}
	// The batch leads the proposals it covers: it is the entry whose next move
	// is the operator's alone.
	if strings.Index(rendered, "has recommended on") > strings.Index(rendered, first.ID) {
		t.Fatalf("the batch comes after the proposals it covers:\n%s", rendered)
	}
}

// A queue that could not be read is never reported as an empty one, and passes
// that could not be read cost the recommendations and say so, never the queue.
func TestAnUnreadableQueueOrPassLogSaysSo(t *testing.T) {
	t.Parallel()

	sources := quietSources()
	sources.Amendments = fakeAmendments{fail: errors.New("the amendment log is torn")}
	standing := ReadStanding(context.Background(), sources)
	if standing.AmendmentsProblem == "" || !strings.Contains(standing.Render(), "the amendment log is torn") {
		t.Fatalf("problem = %q, rendered:\n%s", standing.AmendmentsProblem, standing.Render())
	}

	sources = quietSources()
	fresh := proposedChange(1, time.Hour)
	sources.Amendments = fakeAmendments{records: []amendment.Record{{Proposal: &fresh}}}
	sources.Sweeps = fakeSweeps{fail: errors.New("the sweep log is torn")}
	standing = ReadStanding(context.Background(), sources)
	if standing.Amendments.Undecided != 1 || len(standing.NeedsHuman) != 1 {
		t.Fatalf("Amendments = %+v, NeedsHuman = %+v; want the queue read whole", standing.Amendments, standing.NeedsHuman)
	}
	if !strings.Contains(standing.AmendmentsProblem, "the sweep log is torn") || !strings.Contains(standing.Render(), "recommendations may be incomplete") {
		t.Fatalf("problem = %q, rendered:\n%s", standing.AmendmentsProblem, standing.Render())
	}
}
