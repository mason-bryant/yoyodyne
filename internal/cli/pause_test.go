package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The verb an operator reaches for instead of killing processes. It records one
// durable switch, says what that means for work already under way, and is lifted
// by the same verb that releases a run's wait on the provider.
func TestPauseHoldsEverythingAndResumeLiftsIt(t *testing.T) {
	// Not parallel: the state root the command addresses is set here, and the
	// hold it records is written at the top of it.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)
	holds, err := runstate.NewOperatorHoldStore(stateRoot)
	if err != nil {
		t.Fatalf("NewOperatorHoldStore() error = %v", err)
	}

	stdout, stderr, code := runCLI(t, "pause", "--config", configPath)
	if code != 0 {
		t.Fatalf("pause code = %d, stderr = %q", code, stderr)
	}
	// It says what it did to work already under way, and it is honest about the
	// boundary: a call already in flight is not interrupted.
	for _, want := range []string{"paused all harness activity", "not interrupted", "nothing was cancelled", "yoyo resume"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("pause stdout = %q, want it to mention %q", stdout, want)
		}
	}
	recorded, held, err := holds.Held()
	if err != nil || !held {
		t.Fatalf("Held() = %t, %v, want the hold recorded", held, err)
	}

	// Pausing what is already paused says the same thing, and must not restamp
	// it: how long the harness has been quiet is what an operator reads.
	stdout, stderr, code = runCLI(t, "pause", "--config", configPath)
	if code != 0 {
		t.Fatalf("second pause code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "already paused") {
		t.Fatalf("second pause stdout = %q, want it to say the harness was already paused", stdout)
	}
	again, _, err := holds.Held()
	if err != nil || !again.HeldAt.Equal(recorded.HeldAt) {
		t.Fatalf("Held() = %#v, %v, want the hold left as it was placed", again, err)
	}

	// Naming no work item is what lifts it, which is the other half of the verb
	// that releases one run's wait.
	stdout, stderr, code = runCLI(t, "resume", "--config", configPath)
	if code != 0 {
		t.Fatalf("resume code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{"resumed all harness activity", "within seconds", "yoyo run"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("resume stdout = %q, want it to mention %q", stdout, want)
		}
	}
	if _, held, err := holds.Held(); err != nil || held {
		t.Fatalf("Held() after resume = %t, %v, want the hold lifted", held, err)
	}

	// Resuming what is not held is what an operator does when they are unsure,
	// and it means what they want: the harness is running.
	stdout, stderr, code = runCLI(t, "resume", "--config", configPath)
	if code != 0 {
		t.Fatalf("second resume code = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "was not paused") {
		t.Fatalf("second resume stdout = %q, want it to say nothing was holding activity", stdout)
	}
}

// The machine-readable shape reports what the command found as well as what it
// did, because pausing what is paused and resuming what is running are both
// no-ops and a caller has to be able to tell them from the acts themselves.
func TestPauseAndResumeReportTheHoldAsJSON(t *testing.T) {
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	stdout, stderr, code := runCLI(t, "pause", "--config", configPath, "--json")
	if code != 0 {
		t.Fatalf("pause --json code = %d, stderr = %q", code, stderr)
	}
	var paused pauseOutput
	if err := json.Unmarshal([]byte(stdout), &paused); err != nil {
		t.Fatalf("Unmarshal() error = %v over %q", err, stdout)
	}
	if !paused.Held || paused.Hold == nil || paused.Hold.HeldAt.IsZero() {
		t.Fatalf("pause --json = %q, want the recorded hold", stdout)
	}

	stdout, stderr, code = runCLI(t, "resume", "--config", configPath, "--json")
	if code != 0 {
		t.Fatalf("resume --json code = %d, stderr = %q", code, stderr)
	}
	var resumed pauseOutput
	if err := json.Unmarshal([]byte(stdout), &resumed); err != nil {
		t.Fatalf("Unmarshal() error = %v over %q", err, stdout)
	}
	if resumed.Held || resumed.Hold == nil || !resumed.Hold.HeldAt.Equal(paused.Hold.HeldAt) {
		t.Fatalf("resume --json = %q, want the lifted hold reported and activity running", stdout)
	}
}

// An operator who names the item they can see is one word from what they meant,
// so the refusal says which word rather than only that this is not it.
func TestResumingOneItemPointsAtThePauseWhenThatIsWhatHoldsIt(t *testing.T) {
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)
	store := waitingRun(t, stateRoot, "yoyodyne-parked", time.Time{})
	incomplete, err := store.Incomplete()
	if err != nil || len(incomplete) != 1 {
		t.Fatalf("Incomplete() = %#v, %v, want the one run to park", incomplete, err)
	}
	parked := incomplete[0]
	heldSince := time.Now().UTC().Add(-time.Hour)
	parked.OperatorHeldSince = &heldSince
	parked.PauseCause = runstate.PauseOperatorHold
	if err := store.Save(parked); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	_, stderr, code := runCLI(t, "resume", "--config", configPath, "yoyodyne-parked")
	if code == 0 {
		t.Fatal("resume released a run nothing had refused")
	}
	if !strings.Contains(stderr, "all harness activity is paused") || !strings.Contains(stderr, "with no work item") {
		t.Fatalf("resume stderr = %q, want it to point at the verb that lifts the pause", stderr)
	}
}

// Pausing is the whole machine rather than one item, so naming one is a mistake
// worth refusing rather than quietly ignoring.
func TestPauseRefusesToBeAimedAtOneItem(t *testing.T) {
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)

	_, stderr, code := runCLI(t, "pause", "--config", configPath, "yoyodyne-task")
	if code == 0 {
		t.Fatal("pause accepted a work item")
	}
	if !strings.Contains(stderr, "everything") {
		t.Fatalf("pause stderr = %q, want it to say the pause is not aimed at one item", stderr)
	}
}

// A branch review is one provider invocation with nothing durable to park on, so
// a held harness refuses it rather than parking it — and says what lifts the
// refusal.
func TestBranchReviewIsRefusedWhileActivityIsPaused(t *testing.T) {
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)
	if _, _, code := runCLI(t, "pause", "--config", configPath); code != 0 {
		t.Fatal("pause failed")
	}

	_, stderr, code := runCLI(t, "review", "--config", configPath, "--base", "main")
	if code == 0 {
		t.Fatal("a branch review ran while activity was paused")
	}
	if !strings.Contains(stderr, "paused") || !strings.Contains(stderr, "yoyo resume") {
		t.Fatalf("review stderr = %q, want it to say the harness is paused and what lifts it", stderr)
	}
}
