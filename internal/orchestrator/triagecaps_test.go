package orchestrator

import (
	"context"
	"fmt"
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
	tracker := &fakeTracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
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

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Integration == nil || outcome.RepairAttempts != 1 {
		t.Fatalf("Run() outcome = %#v, want the repaired change integrated", outcome)
	}

	counters, err := store.Triage().Counters(tracker.Item.ID)
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
	tracker := &fakeTracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
	}, "Sure! Here is my review.", repairVerdict, approveVerdict)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if reviews := len(provider.RequestsForRole(domain.RoleReviewer)); reviews != 3 {
		t.Fatalf("reviewer invocations = %d, want the unreadable reply asked again once and the repair judged", reviews)
	}
	counters, err := store.Triage().Counters(tracker.Item.ID)
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

// A verdict on a run with no change present charges nothing, and the worktree
// is what is asked whether a change is present. Which worktree question is
// asked decides whether the rule is yoyodyne-ifd.391's or one that switched the
// cap off: under approvals.publishing: automatic the harness commits the
// developer's work before the checks and the review run, so at the moment the
// reviewer judges a published attempt its worktree has nothing uncommitted. A
// presence question read from uncommitted status would answer "no change" for
// every published attempt, record every repair verdict on one as uncharged, and
// leave the cap counting nothing for a publishing project. The question has to
// be measured against the run's recorded base commit, and this is the run that
// proves it is: a published attempt, judged with its status clean, whose repair
// verdict is charged. The live counters of the first post-391 run sent back on
// a published change, which agree, are quoted in
// docs/diagnoses/yoyodyne-ifd-399-empty-diff-rule-is-base-relative.md.
func TestPipelineChargesTheRepairVerdictOnAPublishedChange(t *testing.T) {
	t.Parallel()

	repository, remote := publishedRepository(t)
	tracker := &fakeTracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &fakeForge{Remote: remote}
	attempts := 0
	provider := roleBackend(func(request backend.RunRequest) error {
		attempts++
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte(fmt.Sprintf("attempt %d\n", attempts)), 0o600)
	}, repairVerdict, approveVerdict)
	// What the worktree's uncommitted status was at the moment each reviewer was
	// invoked in it. The test is about a change that is committed and nothing
	// else, so it records the condition rather than assuming publishing produced
	// it; a status that was not clean here would make the charge below prove
	// nothing about the published case.
	var statusAtReview []string
	judge := provider.Respond
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		if request.Role == domain.RoleReviewer {
			status, err := attemptPipelineGit(request.WorkingDirectory, "status", "--porcelain=v1", "--untracked-files=all")
			if err != nil {
				status = "git status failed: " + err.Error() + ": " + status
			}
			statusAtReview = append(statusAtReview, status)
		}
		return judge(request)
	}
	pipeline, store := newPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Integration == nil || outcome.RepairAttempts != 1 {
		t.Fatalf("Run() outcome = %#v, want the repaired change integrated after one repair", outcome)
	}
	// The condition the charge is being proven under: both attempts were
	// published before they were judged, so each reviewer saw a worktree with
	// its work committed and nothing uncommitted beside it.
	if outcome.PullRequest == nil || outcome.PullRequest.HeadCommit == "" {
		t.Fatalf("outcome = %#v, want the attempts published before review", outcome)
	}
	if len(statusAtReview) != 2 {
		t.Fatalf("reviewer invocations = %d, want the first attempt judged and the repair judged", len(statusAtReview))
	}
	for i, status := range statusAtReview {
		if status != "" {
			t.Fatalf("review %d judged a worktree with uncommitted changes, so this is not the published case:\n%s", i, status)
		}
	}

	counters, err := store.Triage().Counters(tracker.Item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	// One round: the repair verdict on the first published attempt. A count of
	// zero here is the cap switched off for every publishing project.
	if counters.ReviewRounds != 1 {
		t.Fatalf("review rounds = %d, want the repair verdict on a published change charged", counters.ReviewRounds)
	}
	if want := runstate.RoundKey(outcome.RunID, 0); counters.LastRound != want {
		t.Fatalf("last counted round = %q, want the first published attempt %q", counters.LastRound, want)
	}
}

// The other half of the same question, under the same publishing pipeline: a
// verdict on a run whose worktree carries no change against its base charges
// nothing, whatever the verdict said. The developer here does act — it rewrites
// a file the base already holds, byte for byte — so what is empty is the change
// measured against the base commit rather than the developer's activity, and
// there is nothing for publishing to commit. Every verdict is still recorded
// under the attempt it judged, so a re-review of the last attempt stays free.
// TestAnEmptyDeliveryWithNoEnvironmentalCauseIsInNoClassAndSpendsNoRound is the
// same rule seen from the environmental class's side.
func TestPipelineChargesNoRoundForAVerdictOnNoChangeAgainstTheBase(t *testing.T) {
	t.Parallel()

	repository, remote := publishedRepository(t)
	tracker := &fakeTracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	forge := &fakeForge{Remote: remote}
	provider := roleBackend(func(request backend.RunRequest) error {
		return os.WriteFile(filepath.Join(request.WorkingDirectory, "docs", "design.md"), []byte("design content\n"), 0o600)
	}, repairVerdict)
	pipeline, store := newPublishingPipeline(t, repository, tracker, provider, forge, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil {
		t.Fatal("Run() ended without stopping, so no repair verdict was reached on the empty change")
	}
	if !outcome.Blocked {
		t.Fatalf("outcome = %#v, want the run blocked on its unresolved findings", outcome)
	}
	// Nothing was published: there was no change against the base to commit.
	if outcome.PullRequest != nil || len(forge.Opened) != 0 {
		t.Fatalf("outcome pull request = %#v, forge requests = %d; want nothing published for an empty change", outcome.PullRequest, len(forge.Opened))
	}
	if developers := len(provider.RequestsForRole(domain.RoleDeveloper)); developers < 2 {
		t.Fatalf("developer invocations = %d, want the run to have spent its own repair budget on the empty change", developers)
	}
	spent, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	counters, err := store.Triage().Counters(tracker.Item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if counters.ReviewRounds != 0 {
		t.Fatalf("review rounds = %d, want none: no verdict here was against a change present in the worktree", counters.ReviewRounds)
	}
	if want := runstate.RoundKey(outcome.RunID, spent.RepairAttempts); counters.LastJudged != want {
		t.Fatalf("last judged attempt = %q, want %q: an uncharged verdict is still recorded against the attempt it judged", counters.LastJudged, want)
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
