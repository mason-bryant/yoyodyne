package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// What one work item has cost in review rounds is not something any run can
// answer: a run's repair budget starts again at zero each time, so an item run
// four times has spent four budgets and nothing recorded that. The round is
// counted where the verdict is reached, against the item, and it is counted
// before the verdict is acted on.
func TestPipelineCountsEveryVerdictADeveloperAttemptProduced(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	attempts := 0
	provider := roleBackend(func(request backend.RunRequest) error {
		attempts++
		content := "incomplete\n"
		if attempts > 1 {
			content = "implemented\n"
		}
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte(content), 0o600)
	}, repairVerdict, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Integration == nil || outcome.RepairAttempts != 1 {
		t.Fatalf("Run() outcome = %#v, want the repaired change integrated", outcome)
	}

	counters, err := store.Triage().Counters(tracker.item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	// Two developer attempts, two verdicts, two rounds -- and the count is on the
	// item rather than on the run, so it is still there when the run is cleaned
	// up.
	if counters.ReviewRounds != 2 {
		t.Fatalf("review rounds = %d, want one per developer attempt", counters.ReviewRounds)
	}
	// The round is recorded under the attempt that produced it, and the attempt
	// is named by the run it was made in, which is what makes the count add up
	// across runs rather than restart with each one.
	if want := runstate.RoundKey(outcome.RunID, 1); counters.LastRound != want {
		t.Fatalf("last counted round = %q, want the repair attempt %q", counters.LastRound, want)
	}
	// Nothing triaged this item: rounds are what the work cost, not something
	// anybody granted it.
	if counters.Passes() != 0 {
		t.Fatalf("triage passes = %d, want none on an item nobody triaged", counters.Passes())
	}
}

// A review the reviewer never answered is not a round. The first reply here
// could not be read as a verdict at all, so the reviewer said nothing about the
// change, and the item is charged for the one verdict that was actually reached.
func TestPipelineCountsNoRoundForAReviewThatReachedNoVerdict(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, "Sure! Here is my review.", approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if reviews := len(provider.requestsForRole(domain.RoleReviewer)); reviews != 2 {
		t.Fatalf("reviewer invocations = %d, want the unreadable reply asked again once", reviews)
	}
	counters, err := store.Triage().Counters(tracker.item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if counters.ReviewRounds != 1 {
		t.Fatalf("review rounds = %d, want only the verdict that was reached (run %s)", counters.ReviewRounds, outcome.RunID)
	}
}

// The caps a triage action is refused past are the configured ones, assembled in
// one place so the action that refuses and the listing that says how close an
// item is to being refused cannot be working from different numbers.
func TestTriageCapsComeFromTheConfiguration(t *testing.T) {
	t.Parallel()

	caps := TriageCaps(config.Execution{
		TriageRepairGrantsPerItem: 3,
		TriageRerunsPerItem:       2,
		TriageMergeRearmsPerItem:  1,
		TriageReviewRoundsPerItem: 7,
	})
	want := runstate.TriageCaps{RepairGrants: 3, Reruns: 2, MergeRearms: 1, ReviewRounds: 7}
	if caps != want {
		t.Fatalf("TriageCaps() = %+v, want %+v", caps, want)
	}
}
