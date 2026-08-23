package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/chat"
	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The remedy an operator at a terminal is told about has to be one they can run
// from there. This drives both halves of that: the verb lifts the hold, and it
// lifts the very record the conversation's `/release` acts on -- the switch is
// declared here as the interface the conversation drives, so a second store or a
// second file would not compile, let alone pass.
func TestReleaseLiftsTheSameHoldTheConversationLifts(t *testing.T) {
	// Not parallel: the state root the command addresses is set here, and the
	// hold it lifts is written under it.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)
	store, err := runstate.NewIntakeHoldStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewIntakeHoldStore() error = %v", err)
	}
	// The conversation holds intake through this interface over this store, so
	// asking it afterwards is asking what a conversation would see.
	var conversation chat.IntakeHolds = store
	held, err := conversation.Hold("the queue needs reordering first", time.Now())
	if err != nil {
		t.Fatalf("Hold() error = %v", err)
	}

	stdout, stderr, code := runCLI(t, "release", "--config", configPath)
	if code != 0 {
		t.Fatalf("release code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{
		"released the hold on intake",
		held.HeldAt.Format(time.RFC3339),
		// Why it was held is what an operator coming back to a quiet queue reads
		// before deciding whether lifting it was right.
		"the queue needs reordering first",
		"yoyo work",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("release stdout = %q, want it to mention %q", stdout, want)
		}
	}
	if _, stillHeld, err := conversation.Held(); err != nil || stillHeld {
		t.Fatalf("Held() after release = %t, %v, want the conversation to see the hold gone", stillHeld, err)
	}

	// Releasing what is not held is what an operator does when they are unsure,
	// and it means what they want: the harness is choosing work.
	stdout, stderr, code = runCLI(t, "release", "--config", configPath)
	if code != 0 {
		t.Fatalf("second release code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "was not held") {
		t.Fatalf("second release stdout = %q, want it to say nothing was holding intake", stdout)
	}
}

// The machine-readable shape reports what the command found as well as what it
// did, because releasing what was never held leaves intake exactly as asked
// while having changed nothing.
func TestReleaseReportsTheLiftedHoldAsJSON(t *testing.T) {
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)
	store, err := runstate.NewIntakeHoldStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewIntakeHoldStore() error = %v", err)
	}
	placed, err := store.Hold("looking at the queue", time.Now())
	if err != nil {
		t.Fatalf("Hold() error = %v", err)
	}

	stdout, stderr, code := runCLI(t, "release", "--config", configPath, "--json")
	if code != 0 {
		t.Fatalf("release --json code = %d, stderr = %q", code, stderr)
	}
	var lifted releaseOutput
	if err := json.Unmarshal([]byte(stdout), &lifted); err != nil {
		t.Fatalf("Unmarshal() error = %v over %q", err, stdout)
	}
	if !lifted.Lifted || lifted.Held || lifted.Hold == nil || !lifted.Hold.HeldAt.Equal(placed.HeldAt) {
		t.Fatalf("release --json = %q, want the lifted hold reported and intake running", stdout)
	}

	stdout, stderr, code = runCLI(t, "release", "--config", configPath, "--json")
	if code != 0 {
		t.Fatalf("second release --json code = %d, stderr = %q", code, stderr)
	}
	var again releaseOutput
	if err := json.Unmarshal([]byte(stdout), &again); err != nil {
		t.Fatalf("Unmarshal() error = %v over %q", err, stdout)
	}
	if again.Lifted || again.Held || again.Hold != nil {
		t.Fatalf("second release --json = %q, want a no-op told apart from the act itself", stdout)
	}
}

// Releasing is the hold on choosing rather than one run's wait, so naming an
// item is a mistake worth refusing -- and the refusal names the verb that does
// take one, because an operator who typed the item they can see is one word from
// what they meant.
func TestReleaseRefusesToBeAimedAtOneItem(t *testing.T) {
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	_, stderr, code := runCLI(t, "release", "--config", configPath, "yoyodyne-task")
	if code == 0 {
		t.Fatal("release accepted a work item")
	}
	if !strings.Contains(stderr, "yoyo resume") {
		t.Fatalf("release stderr = %q, want it to name the verb that releases one run's wait", stderr)
	}
}

// Every place a terminal reports a held intake carries the command that lifts
// it. The defect this closes was a person told what had stopped and given a
// remedy -- `/release` in a conversation -- that they had no way to run from
// where they were reading it.
func TestEveryTerminalReportOfAHeldIntakeNamesTheVerbThatLiftsIt(t *testing.T) {
	t.Parallel()

	hold := runstate.IntakeHold{
		SchemaVersion: runstate.IntakeHoldSchemaVersion,
		ProductID:     "yoyodyne",
		HeldAt:        time.Date(2026, 8, 20, 2, 2, 0, 0, time.UTC),
		Reason:        "the queue looks wrong",
	}

	var runReport strings.Builder
	reportIntakeHold(&runReport, orchestrator.Outcome{
		WorkItemID: "yoyodyne-task", Paused: true, PausedByIntake: &hold,
	})
	if !strings.Contains(runReport.String(), "`yoyo release`") {
		t.Fatalf("run report = %q, want the runnable remedy named", runReport.String())
	}

	var scheduleReport, scheduleErrors strings.Builder
	reportSchedule(&scheduleReport, &scheduleErrors, false, orchestrator.Schedule{
		Stopped: orchestrator.ScheduleIntakeHeld, IntakeHeld: &hold,
	}, nil)
	if !strings.Contains(scheduleReport.String(), "`yoyo release`") {
		t.Fatalf("schedule report = %q, want the runnable remedy named", scheduleReport.String())
	}

	// The brake places a hold the operator never chose, so its line carries the
	// remedy itself rather than relying on the hold being reported beside it.
	braked := orchestrator.Schedule{Braked: &hold, BlockedInARow: 3}
	if !strings.Contains(braked.Render(), "`yoyo release`") {
		t.Fatalf("braked schedule = %q, want the runnable remedy named", braked.Render())
	}

	rerun := orchestrator.RerunResult{
		WorkItemID: "yoyodyne-task", PriorRunID: "run-0123456789abcdef0123456789abcdef", IntakeHeld: &hold,
	}
	if !strings.Contains(rerun.Render(), "`yoyo release`") {
		t.Fatalf("re-run report = %q, want the runnable remedy named", rerun.Render())
	}
}
