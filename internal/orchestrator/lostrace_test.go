package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// movingTargetReviewer wraps a provider so that each of the first races reviews
// lands somebody else's work on the target branch as the verdict is given. That
// is the window a busy main opens: the change is approved, and by the time it is
// promoted the target is somewhere else.
func movingTargetReviewer(t *testing.T, repository string, provider *fakeBackend, races int) {
	t.Helper()
	inner := provider.run
	reviews := 0
	provider.run = func(request backend.RunRequest) (backend.RunResult, error) {
		result, err := inner(request)
		if err != nil || request.Role != domain.RoleReviewer {
			return result, err
		}
		reviews++
		if reviews <= races {
			name := "elsewhere-" + strconv.Itoa(reviews) + ".txt"
			writePipelineFile(t, repository, name, "somebody else's work\n")
			runPipelineGit(t, repository, "add", name)
			runPipelineGit(t, repository, "commit", "-m", "concurrent target change "+strconv.Itoa(reviews))
		}
		return result, err
	}
}

func implementsFeature(request backend.RunRequest) error {
	return os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600)
}

// A run that loses its race four times, with every replay passing its checks and
// drawing an approval, lands — whatever the integration budget is. Losing the
// race says nothing about the change, so it spends nothing that ends in triage:
// nothing is blocked, nothing is docketed, and no decision is asked of anybody.
func TestARunWhoseReplaysKeepPassingLandsThroughFourLostRaces(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := roleBackend(implementsFeature, approveVerdict)
	movingTargetReviewer(t, repository, provider, 4)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})
	// The default budget, which four lost races exceed twice over. Under the rule
	// this replaced, the third race blocked the item.
	pipeline.Config.Execution.IntegrationRetriesBeforeReconciliation = 2

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil || !outcome.WorkItemClosed {
		t.Fatalf("Run() outcome = %#v, want the change landed and the item closed", outcome)
	}
	if outcome.Blocked || tracker.blocked {
		t.Fatalf("blocked = %t / %t (%q), want a lost race never to block the item", outcome.Blocked, tracker.blocked, tracker.blockReason)
	}
	if outcome.IntegrationRetries != 4 || outcome.ChargedReplays != 0 {
		t.Fatalf("lost races = %d, charged replays = %d; want four races recorded and none charged", outcome.IntegrationRetries, outcome.ChargedReplays)
	}
	// Every replay re-earned the gate: a fresh independent review each time, and
	// the developer never asked for anything.
	if reviews := provider.requestsForRole(domain.RoleReviewer); len(reviews) != 5 {
		t.Fatalf("reviewer invocations = %d, want the first verdict and one per replay", len(reviews))
	}
	if developers := provider.requestsForRole(domain.RoleDeveloper); len(developers) != 1 {
		t.Fatalf("developer invocations = %d, want 1", len(developers))
	}
	for race := 1; race <= 4; race++ {
		if _, err := os.Stat(filepath.Join(repository, "elsewhere-"+strconv.Itoa(race)+".txt")); err != nil {
			t.Fatalf("main lost the work that won race %d: %v", race, err)
		}
	}

	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.IntegrationRetries != 4 || state.ReplaysCharged() != 0 || state.Blocker != "" {
		t.Fatalf("durable record: races %d, charged %d, blocker %q", state.IntegrationRetries, state.ReplaysCharged(), state.Blocker)
	}
	record, err := store.Triage().Counters(tracker.item.ID)
	if err != nil {
		t.Fatalf("Counters() error = %v", err)
	}
	if len(record.Decisions) != 0 || record.ReviewRounds != 0 {
		t.Fatalf("triage record = %#v, want no decision recorded and no round charged", record)
	}
}

// What the budget does bound is a replay that stopped on the change. Here the
// first replay draws a repair verdict, which charges it; the repaired change is
// approved, the target moves again, and with a budget of one the run stops
// there rather than replaying a second time.
func TestAReplayThatDrewARepairSpendsTheIntegrationBudget(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	// Verdicts: the approval that loses race 1, the replay's repair, the approval
	// of the repair that loses race 2.
	provider := roleBackend(implementsFeature, approveVerdict, repairVerdict, approveVerdict)
	inner := provider.run
	reviews := 0
	provider.run = func(request backend.RunRequest) (backend.RunResult, error) {
		result, err := inner(request)
		if err != nil || request.Role != domain.RoleReviewer {
			return result, err
		}
		reviews++
		if reviews == 1 || reviews == 3 {
			name := "elsewhere-" + strconv.Itoa(reviews) + ".txt"
			writePipelineFile(t, repository, name, "somebody else's work\n")
			runPipelineGit(t, repository, "add", name)
			runPipelineGit(t, repository, "commit", "-m", "concurrent target change")
		}
		return result, err
	}
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})
	pipeline.Config.Execution.IntegrationRetriesBeforeReconciliation = 1

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err == nil || !strings.Contains(err.Error(), "1 of 1 permitted replay(s) already stopped on the change") {
		t.Fatalf("Run() error = %v, want the charged replay to have spent the budget", err)
	}
	if !outcome.Blocked || outcome.Integration != nil {
		t.Fatalf("Run() outcome = %#v, want a blocked run with nothing promoted", outcome)
	}
	if !strings.Contains(tracker.blockReason, "Replays that stopped on the change: 1 of 1 permitted") ||
		!strings.Contains(tracker.blockReason, "Races lost to the moving target: 2") {
		t.Fatalf("blocker = %q", tracker.blockReason)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.IntegrationRetries != 1 || state.ChargedReplays != 1 || state.ReplaysCharged() != 1 {
		t.Fatalf("durable record: races replayed %d, charged %d (%d read)", state.IntegrationRetries, state.ChargedReplays, state.ReplaysCharged())
	}
}
