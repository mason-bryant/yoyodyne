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
	"github.com/mason-bryant/yoyodyne/internal/orchestrator/orchestratortest"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// movingTargetReviewer wraps a provider so that each of the first races reviews
// lands somebody else's work on the target branch as the verdict is given. That
// is the window a busy main opens: the change is approved, and by the time it is
// promoted the target is somewhere else.
func movingTargetReviewer(t *testing.T, repository string, provider *orchestratortest.Backend, races int) {
	t.Helper()
	inner := provider.Respond
	reviews := 0
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
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
// drawing an approval, lands — whatever the integration budget is, zero
// included. Losing the race says nothing about the change, so it spends nothing
// that ends in triage: nothing is blocked, nothing is docketed, and no decision
// is asked of anybody.
func TestARunWhoseReplaysKeepPassingLandsThroughFourLostRaces(t *testing.T) {
	t.Parallel()

	// Two is the default, which four lost races exceed twice over: under the rule
	// this replaced, the third race blocked the item. Zero permits no replay to
	// stop on the change, and a replay that passes is not one.
	for _, budget := range []int{2, 0} {
		t.Run("budget "+strconv.Itoa(budget), func(t *testing.T) {
			t.Parallel()

			repository := pipelineRepository(t)
			tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
			provider := orchestratortest.RoleBackend(implementsFeature, approveVerdict)
			movingTargetReviewer(t, repository, provider, 4)
			pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})
			pipeline.Config.Execution.IntegrationRetriesBeforeReconciliation = budget

			outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if outcome.Status != runstate.StatusSucceeded || outcome.Integration == nil || !outcome.WorkItemClosed {
				t.Fatalf("Run() outcome = %#v, want the change landed and the item closed", outcome)
			}
			if outcome.Blocked || tracker.Blocked {
				t.Fatalf("blocked = %t / %t (%q), want a lost race never to block the item", outcome.Blocked, tracker.Blocked, tracker.BlockReason)
			}
			if outcome.IntegrationRetries != 4 || outcome.ChargedReplays != 0 {
				t.Fatalf("lost races = %d, charged replays = %d; want four races recorded and none charged", outcome.IntegrationRetries, outcome.ChargedReplays)
			}
			// Every replay re-earned the gate: a fresh independent review each time,
			// and the developer never asked for anything.
			if reviews := provider.RequestsForRole(domain.RoleReviewer); len(reviews) != 5 {
				t.Fatalf("reviewer invocations = %d, want the first verdict and one per replay", len(reviews))
			}
			if developers := provider.RequestsForRole(domain.RoleDeveloper); len(developers) != 1 {
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
			if state.IntegrationRetries != 4 || state.ChargedReplays != 0 || state.Blocker != "" {
				t.Fatalf("durable record: races %d, charged %d, blocker %q", state.IntegrationRetries, state.ChargedReplays, state.Blocker)
			}
			// The replay that landed passed its gate, so nothing is left waiting to
			// be charged on a run that has already promoted.
			if state.ReplayUnjudged {
				t.Fatal("replay_unjudged is still set on a run that landed")
			}
			record, err := store.Triage().Counters(tracker.Item.ID)
			if err != nil {
				t.Fatalf("Counters() error = %v", err)
			}
			if len(record.Decisions) != 0 || record.ReviewRounds != 0 {
				t.Fatalf("triage record = %#v, want no decision recorded and no round charged", record)
			}
		})
	}
}

// scriptedRaces moves the target branch as the reviewer gives the verdicts
// numbered in moves, so a test can say which approvals lose their race.
func scriptedRaces(t *testing.T, repository string, provider *orchestratortest.Backend, moves ...int) {
	t.Helper()
	inner := provider.Respond
	reviews := 0
	provider.Respond = func(request backend.RunRequest) (backend.RunResult, error) {
		result, err := inner(request)
		if err != nil || request.Role != domain.RoleReviewer {
			return result, err
		}
		reviews++
		for _, move := range moves {
			if move == reviews {
				name := "elsewhere-" + strconv.Itoa(reviews) + ".txt"
				writePipelineFile(t, repository, name, "somebody else's work\n")
				runPipelineGit(t, repository, "add", name)
				runPipelineGit(t, repository, "commit", "-m", "concurrent target change")
			}
		}
		return result, err
	}
}

// What the budget bounds is a replay that stops on the change, and it is
// enforced there rather than at a lost race. With a budget of one, the first
// replay draws a repair verdict, is charged, and is repaired like any repair;
// the repaired change is approved and loses its race again, which costs
// nothing; the second replay draws a repair verdict too, and that charge is the
// one past the budget, so the run stops there — on the change, with the repair
// verdict as its reason — rather than at the race.
func TestAReplayThatStopsOnTheChangePastTheBudgetStopsThere(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	// Verdicts: approve (loses race 1), repair (replay 1 charged), approve (loses
	// race 2), repair (replay 2 charged past the budget).
	provider := orchestratortest.RoleBackend(implementsFeature, approveVerdict, repairVerdict, approveVerdict, repairVerdict)
	scriptedRaces(t, repository, provider, 1, 3)
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})
	pipeline.Config.Execution.IntegrationRetriesBeforeReconciliation = 1
	pipeline.Config.Execution.RepairAttemptsBeforeReplan = 5

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil || !strings.Contains(err.Error(), "2 of 1 permitted replay stop(s) spent") || !strings.Contains(err.Error(), "independent review requires repair") {
		t.Fatalf("Run() error = %v, want the second charged replay to stop the run on its repair verdict", err)
	}
	if !outcome.Blocked || outcome.Integration != nil {
		t.Fatalf("Run() outcome = %#v, want a blocked run with nothing promoted", outcome)
	}
	if !strings.Contains(tracker.BlockReason, "Replays that stopped on the change: 2 of 1 permitted") ||
		!strings.Contains(tracker.BlockReason, "Races lost to the moving target: 2") {
		t.Fatalf("blocker = %q", tracker.BlockReason)
	}
	// The first charged replay was repaired, and the second was not handed back.
	if developers := provider.RequestsForRole(domain.RoleDeveloper); len(developers) != 2 {
		t.Fatalf("developer invocations = %d, want the first attempt and the one repair the first charged replay bought", len(developers))
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.IntegrationRetries != 2 || state.ChargedReplays != 2 || state.ReplayUnjudged {
		t.Fatalf("durable record: races %d, charged %d, unjudged %t", state.IntegrationRetries, state.ChargedReplays, state.ReplayUnjudged)
	}
}

// At a budget of zero no replay may stop on the change: the first replay handed
// back for a failing check ends the run there, on the check.
func TestAtBudgetZeroTheFirstReplayThatFailsItsChecksStopsTheRun(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &orchestratortest.Tracker{Item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := orchestratortest.RoleBackend(implementsFeature, approveVerdict)
	scriptedRaces(t, repository, provider, 1)
	// The check passes on the first attempt and fails on every run after it, so
	// the replayed change is the one that fails.
	counter := filepath.Join(t.TempDir(), "checks")
	check := "echo x >> " + strconv.Quote(counter) + " && test $(wc -l < " + strconv.Quote(counter) + ") -le 1"
	pipeline, store := newAutomaticPipeline(t, repository, tracker, provider, []string{check})
	pipeline.Config.Execution.IntegrationRetriesBeforeReconciliation = 0

	outcome, err := pipeline.Run(context.Background(), tracker.Item.ID)
	if err == nil || !strings.Contains(err.Error(), "1 of 0 permitted replay stop(s) spent") {
		t.Fatalf("Run() error = %v, want the failing replay to stop the run", err)
	}
	if !outcome.Blocked || outcome.Integration != nil {
		t.Fatalf("Run() outcome = %#v, want a blocked run with nothing promoted", outcome)
	}
	if developers := provider.RequestsForRole(domain.RoleDeveloper); len(developers) != 1 {
		t.Fatalf("developer invocations = %d, want nothing handed back", len(developers))
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.IntegrationRetries != 1 || state.ChargedReplays != 1 {
		t.Fatalf("durable record: races %d, charged %d", state.IntegrationRetries, state.ChargedReplays)
	}
}
