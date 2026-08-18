package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/runstate"
)

// The verb an operator reaches for when the reset a run is waiting on has
// stopped being true. It records that the claim went stale and changes nothing
// else: the run keeps its claim, its branch, and its worktree, and the process
// serving the wait reads the record and asks the provider again.
func TestResumeReleasesTheWaitOfARunPausedOnAUsageLimit(t *testing.T) {
	// Not parallel: the state root the command addresses is set here, and the run
	// it releases is written under it.
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)
	store := waitingRun(t, stateRoot, "yoyodyne-task", time.Now().UTC().Add(2*time.Hour))

	stdout, stderr, code := runCLI(t, "resume", "--config", configPath, "yoyodyne-task")
	if code != 0 {
		t.Fatalf("resume code = %d, stderr = %q", code, stderr)
	}
	// It says what it released and what happens next in both of the cases the
	// operator can be in: a process is serving the wait, or none is.
	for _, want := range []string{"five_hour", "yoyodyne-task", "within seconds", "yoyo run yoyodyne-task"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("resume stdout = %q, want it to mention %q", stdout, want)
		}
	}

	incomplete, err := store.Incomplete()
	if err != nil {
		t.Fatalf("Incomplete() error = %v", err)
	}
	if len(incomplete) != 1 {
		t.Fatalf("Incomplete() = %#v, want the released run still in flight", incomplete)
	}
	// Releasing is not cancelling: the run and its recorded deadline are both
	// exactly where they were, and only the release beside them is new.
	if incomplete[0].Status != runstate.StatusRunning || incomplete[0].UsageLimitResetsAt == nil {
		t.Fatalf("released run = %#v, want an in-flight run still carrying its deadline", incomplete[0])
	}
	released, found, err := store.ReleasedWait(incomplete[0].RunID)
	if err != nil || !found {
		t.Fatalf("ReleasedWait() = %t, %v, want the release recorded", found, err)
	}
	if released.WorkItemID != "yoyodyne-task" || released.RunID != incomplete[0].RunID {
		t.Fatalf("release = %#v, want it to name the waiting run and its item", released)
	}

	// Releasing twice says the same thing, and an operator cannot see whether the
	// run re-parked in between, so it is not refused.
	if _, stderr, code := runCLI(t, "resume", "--config", configPath, "yoyodyne-task"); code != 0 {
		t.Fatalf("second resume code = %d, stderr = %q", code, stderr)
	}

	stdout, stderr, code = runCLI(t, "resume", "--config", configPath, "--json", "yoyodyne-task")
	if code != 0 {
		t.Fatalf("resume --json code = %d, stderr = %q", code, stderr)
	}
	var reported resumeOutput
	if err := json.Unmarshal([]byte(stdout), &reported); err != nil {
		t.Fatalf("Unmarshal() error = %v over %q", err, stdout)
	}
	if reported.Release == nil || reported.Release.RunID != incomplete[0].RunID {
		t.Fatalf("resume --json = %q, want the recorded release", stdout)
	}
}

// A release recorded against a run that is not waiting would be acted on by
// whatever pause that run takes next — a pause nobody has said anything about —
// so it is refused by name rather than written anyway.
func TestResumeRefusesWhatIsNotWaitingOnAUsageLimit(t *testing.T) {
	stateRoot := t.TempDir()
	t.Setenv("YOYODYNE_STATE_HOME", stateRoot)
	configPath := writeConfig(t, validConfig)
	waitingRun(t, stateRoot, "yoyodyne-waiting", time.Time{})

	for name, args := range map[string][]string{
		"an item with no run in flight":    {"resume", "--config", configPath, "yoyodyne-elsewhere"},
		"a run that is not waiting at all": {"resume", "--config", configPath, "yoyodyne-waiting"},
		"no item at all":                   {"resume", "--config", configPath},
		"more than one item":               {"resume", "--config", configPath, "yoyodyne-waiting", "yoyodyne-other"},
	} {
		_, stderr, code := runCLI(t, args...)
		if code == 0 {
			t.Fatalf("%s: resume succeeded", name)
		}
		if strings.TrimSpace(stderr) == "" {
			t.Fatalf("%s: refused without saying why", name)
		}
	}
}

// waitingRun writes one in-flight run for a work item, waiting out a usage limit
// when a reset time is given. It is what the command finds by reading, which is
// the only way it is allowed to find it: taking the run's lease to release its
// wait would stop the very run the release exists to keep alive.
func waitingRun(t *testing.T, stateRoot, workItemID string, resetsAt time.Time) *runstate.Store {
	t.Helper()
	store, err := runstate.NewStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	runID, err := runstate.NewRunID()
	if err != nil {
		t.Fatalf("NewRunID() error = %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	state := runstate.State{
		SchemaVersion: runstate.StateSchemaVersion,
		RunID:         runID,
		ProductID:     domain.ProductID("yoyodyne"),
		RepositoryID:  "yoyodyne",
		WorkItemID:    workItemID,
		Backend:       domain.BackendClaudeCode,
		Status:        runstate.StatusRunning,
		Phase:         runstate.PhaseDeveloping,
		WorktreePath:  filepath.Join(stateRoot, "worktrees", runID),
		Branch:        "yoyodyne/" + workItemID,
		BaseCommit:    "0123456789abcdef0123456789abcdef01234567",
		StartedAt:     now,
		UpdatedAt:     now,
	}
	if !resetsAt.IsZero() {
		deadline := resetsAt.UTC()
		state.UsageLimitResetsAt = &deadline
		state.UsageLimitKind = "five_hour"
	}
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	return store
}
