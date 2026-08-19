package cli

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/orchestrator"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The hold the scheduler is actually subject to. `yoyo work` is the harness
// choosing, so a held intake stops it before it reads the tracker at all --
// nothing is claimed, nothing is developed, and the operator is told the two
// ways out.
func TestWorkStartsNothingWhileIntakeIsHeld(t *testing.T) {
	// Not parallel: the state root the command addresses is set here, and the
	// hold it reads is written under it.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)
	intake, err := runstate.NewIntakeHoldStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewIntakeHoldStore() error = %v", err)
	}
	if _, err := intake.Hold("the queue needs reordering first", time.Now()); err != nil {
		t.Fatalf("Hold() error = %v", err)
	}

	stdout, stderr, code := runCLI(t, "work", "--config", configPath)
	if code != 0 {
		t.Fatalf("work code = %d, stderr = %q", code, stderr)
	}
	for _, want := range []string{"nothing was started", "holding intake", "the queue needs reordering first", "/release", "yoyo run"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("work stdout = %q, want it to mention %q", stdout, want)
		}
	}

	stdout, stderr, code = runCLI(t, "work", "--config", configPath, "--json")
	if code != 0 {
		t.Fatalf("work --json code = %d, stderr = %q", code, stderr)
	}
	var output struct {
		Schedule orchestrator.Schedule `json:"schedule"`
		Error    string                `json:"error"`
	}
	if err := json.Unmarshal([]byte(stdout), &output); err != nil {
		t.Fatalf("Unmarshal(%q) error = %v", stdout, err)
	}
	if output.Error != "" {
		t.Fatalf("error = %q, want a held intake reported as a schedule rather than a failure", output.Error)
	}
	if len(output.Schedule.Started) != 0 || output.Schedule.IntakeHeld == nil {
		t.Fatalf("schedule = %#v, want nothing started and the hold named", output.Schedule)
	}
	if output.Schedule.Stopped != orchestrator.ScheduleIntakeHeld {
		t.Fatalf("stopped = %q, want the hold to be why the choosing stopped", output.Schedule.Stopped)
	}
}

// The verb takes no item: naming one is `yoyo run`, and the difference between
// the two is exactly the hold above.
func TestWorkRefusesArgumentsItCannotActOn(t *testing.T) {
	t.Parallel()

	configPath := writeConfig(t, validConfig)
	if _, stderr, code := runCLI(t, "work", "--config", configPath, "yoyodyne-1"); code != 2 {
		t.Fatalf("work with an item code = %d, stderr = %q, want a usage refusal", code, stderr)
	}
	if _, stderr, code := runCLI(t, "work", "--config", configPath, "--limit", "-1"); code != 2 {
		t.Fatalf("work with a negative limit code = %d, stderr = %q, want a usage refusal", code, stderr)
	}
}

// The answer to "when is a capacity change picked up?" is documented where an
// operator looks for it. It is a decision rather than an accident, so the usage
// states it rather than leaving it to be inferred from behavior.
func TestWorkUsageSaysWhenAConfigurationChangeTakesEffect(t *testing.T) {
	t.Parallel()

	var usage strings.Builder
	printWorkUsage(&usage)
	for _, want := range []string{
		"re-read before every pull",
		"takes effect at the next selection",
		"keep the configuration they started under",
		"max_concurrent_developers",
	} {
		if !strings.Contains(usage.String(), want) {
			t.Fatalf("work usage = %q, want it to say %q", usage.String(), want)
		}
	}
}
