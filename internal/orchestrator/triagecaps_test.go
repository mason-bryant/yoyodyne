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
//
// What is counted is a verdict that sent the work back. The approval that ends
// the run is not one: the cap exists to stop an item buying the same argument
// another round, and charging the round that settled the argument is what walked
// an approved-then-conflicted item into a cap that refused every decision about
// it.
func TestPipelineCountsTheVerdictThatSentTheWorkBackAndNotTheApproval(t *testing.T) {
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
	// Two developer attempts, two verdicts, one round -- the repair verdict that
	// sent the first attempt back. The count is on the item rather than on the
	// run, so it is still there when the run is cleaned up.
	if counters.ReviewRounds != 1 {
		t.Fatalf("review rounds = %d, want only the verdict that sent the work back", counters.ReviewRounds)
	}
	// The round is recorded under the attempt that produced it, and the attempt
	// is named by the run it was made in, which is what makes the count add up
	// across runs rather than restart with each one. The approval that followed
	// left the head where the counted round put it.
	if want := runstate.RoundKey(outcome.RunID, 0); counters.LastRound != want {
		t.Fatalf("last counted round = %q, want the first attempt %q", counters.LastRound, want)
	}
	// Nothing triaged this item: rounds are what the work cost, not something
	// anybody granted it.
	if counters.Passes() != 0 {
		t.Fatalf("triage passes = %d, want none on an item nobody triaged", counters.Passes())
	}
}

// A review the reviewer never answered is not a round. The first reply here
// could not be read as a verdict at all, so the reviewer said nothing about the
// change; the item is charged for the one verdict that was actually reached and
// sent the work back, and for nothing else.
func TestPipelineCountsNoRoundForAReviewThatReachedNoVerdict(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, "Sure! Here is my review.", repairVerdict, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if reviews := len(provider.requestsForRole(domain.RoleReviewer)); reviews != 3 {
		t.Fatalf("reviewer invocations = %d, want the unreadable reply asked again once and the repair judged", reviews)
	}
	counters, err := store.Triage().Counters(tracker.item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if counters.ReviewRounds != 1 {
		t.Fatalf("review rounds = %d, want only the verdict that was reached (run %s)", counters.ReviewRounds, outcome.RunID)
	}
	// The unreadable reply and the approval are excluded for different reasons and
	// the head says which round the one charge was: the repair verdict on the
	// first developer attempt.
	if want := runstate.RoundKey(outcome.RunID, 0); counters.LastRound != want {
		t.Fatalf("last counted round = %q, want the first attempt %q", counters.LastRound, want)
	}
}

// The caps a triage action is refused past are assembled in one place so the
// action that refuses and the listing that says how close an item is to being
// refused cannot be working from different numbers. One comes from the triage
// vocabulary an operator writes down: the rounds an item may accumulate. The
// other three are the workflow rather than a setting: triage takes each of its
// own decisions once, and a second is an escalation rather than a larger budget,
// so there is nothing for an operator to state.
//
// The re-arm's is one of the three, and it was not always: it was shipped sized
// by the integration retries a single run gets, which granted a publication a
// second re-arm the governed design calls an escalation. The configuration is
// read past here deliberately, so an operator raising that retry budget cannot
// move it back.
func TestTriageCapsComeFromTheConfigurationAndTheWorkflow(t *testing.T) {
	t.Parallel()

	caps := TriageCaps(
		config.Execution{IntegrationRetriesBeforeReconciliation: 3},
		config.Triage{ReviewRoundsCap: 7},
	)
	want := runstate.TriageCaps{ReviewRounds: 7, RepairGrants: 1, Reruns: 1, MergeRearms: 1}
	if caps != want {
		t.Fatalf("TriageCaps() = %+v, want %+v", caps, want)
	}
	// The three that are not configured must not move with what is: an operator
	// raising the round cap is saying an item may cost more, and one raising the
	// integration retries is saying a single promotion may be tried again more
	// often. Neither says triage may decide the same thing twice.
	generous := TriageCaps(
		config.Execution{IntegrationRetriesBeforeReconciliation: 9},
		config.Triage{ReviewRoundsCap: 40},
	)
	if generous.RepairGrants != 1 || generous.Reruns != 1 || generous.MergeRearms != 1 {
		t.Fatalf("TriageCaps() under a raised configuration = %+v, want triage still acting alone once", generous)
	}
}

// A grant is stated in repair attempts and spent in review rounds, and the two
// are the same count: every repair attempt an item is granted is one more
// verdict it will produce. Reading it in one place is what stops a caller
// converting between units that do not need converting.
func TestTheRepairGrantIsTheConfiguredAttempts(t *testing.T) {
	t.Parallel()

	if rounds := TriageRepairGrantRounds(config.Triage{RepairGrantAttempts: 2}); rounds != 2 {
		t.Fatalf("TriageRepairGrantRounds() = %d, want the configured 2", rounds)
	}
}
