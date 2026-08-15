package runstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"yoyodyne/internal/domain"
	"yoyodyne/internal/execution"
)

func TestStoreLifecycleAndIncompleteDiscovery(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	running := testState(t, StatusRunning)
	if err := store.Create(running); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := store.Create(running); err == nil {
		t.Fatal("second Create() error = nil")
	}

	loaded, err := store.Load(running.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded.WorkItemID != running.WorkItemID {
		t.Fatalf("Load() work item = %q", loaded.WorkItemID)
	}

	incomplete, err := store.Incomplete()
	if err != nil {
		t.Fatalf("Incomplete() error = %v", err)
	}
	if len(incomplete) != 1 || incomplete[0].RunID != running.RunID {
		t.Fatalf("Incomplete() = %#v", incomplete)
	}

	completed := running
	completed.Status = StatusSucceeded
	completed.UpdatedAt = completed.UpdatedAt.Add(time.Second)
	completedAt := completed.UpdatedAt
	completed.CompletedAt = &completedAt
	if err := store.Save(completed); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	incomplete, err = store.Incomplete()
	if err != nil {
		t.Fatalf("Incomplete() error = %v", err)
	}
	if len(incomplete) != 0 {
		t.Fatalf("Incomplete() = %#v, want empty", incomplete)
	}
}

func TestStoreEventsAndMalformedDiscovery(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusRunning)
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	event, err := execution.NewEvent(state.RunID, 1, state.StartedAt, execution.EventRunStarted, "harness", nil)
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}
	if err := store.AppendEvent(event); err != nil {
		t.Fatalf("AppendEvent() error = %v", err)
	}
	events, err := store.LoadEvents(state.RunID)
	if err != nil {
		t.Fatalf("LoadEvents() error = %v", err)
	}
	if len(events) != 1 || events[0].Type != execution.EventRunStarted {
		t.Fatalf("LoadEvents() = %#v", events)
	}

	path, err := store.eventPath(state.RunID)
	if err != nil {
		t.Fatalf("eventPath() error = %v", err)
	}
	if err := os.WriteFile(path, []byte("malformed\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := store.LoadEvents(state.RunID); err == nil {
		t.Fatal("LoadEvents() malformed error = nil")
	}
}

func TestStoreDoesNotPersistEnvironmentSecrets(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusRunning)
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	path, err := store.statePath(state.RunID)
	if err != nil {
		t.Fatalf("statePath() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if strings.Contains(string(data), "OPENAI_API_KEY") || strings.Contains(string(data), "ANTHROPIC_API_KEY") {
		t.Fatalf("state file contains credential environment names: %s", data)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if _, exists := decoded["environment"]; exists {
		t.Fatal("state file contains environment")
	}
}

func TestStateRequiresCompleteWorktreeIdentity(t *testing.T) {
	t.Parallel()

	state := testState(t, StatusRunning)
	state.WorktreePath = "/state/worktree"
	if err := state.Validate(); err == nil || !strings.Contains(err.Error(), "recorded together") {
		t.Fatalf("Validate() incomplete worktree error = %v", err)
	}
	state.Branch = "yoyodyne/task/01234567"
	state.BaseCommit = strings.Repeat("a", 40)
	if err := state.Validate(); err != nil {
		t.Fatalf("Validate() complete worktree error = %v", err)
	}
}

func TestIncompleteRejectsCorruptStateRatherThanDuplicatingIt(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	if err := os.MkdirAll(store.Root(), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	runID := "run-0123456789abcdef0123456789abcdef"
	if err := os.WriteFile(filepath.Join(store.Root(), runID+".json"), []byte("{"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := store.Incomplete(); err == nil || !strings.Contains(err.Error(), "discover incomplete runs") {
		t.Fatalf("Incomplete() error = %v", err)
	}
}

func TestDefaultRoot(t *testing.T) {
	t.Parallel()

	getenv := func(key string) string {
		if key == "XDG_STATE_HOME" {
			return "/state"
		}
		return ""
	}
	root, err := DefaultRoot(getenv, func() (string, error) { return "/home/test", nil }, "linux")
	if err != nil {
		t.Fatalf("DefaultRoot() error = %v", err)
	}
	if root != filepath.Join("/state", "yoyodyne") {
		t.Fatalf("DefaultRoot() = %q", root)
	}
	if _, err := DefaultRoot(func(string) string { return "relative" }, func() (string, error) { return "/home/test", nil }, "linux"); err == nil {
		t.Fatal("DefaultRoot() relative override error = nil")
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("NewStore() error = %v", err)
	}
	return store
}

func testState(t *testing.T, status Status) State {
	t.Helper()
	runID, err := NewRunID()
	if err != nil {
		t.Fatalf("NewRunID() error = %v", err)
	}
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	state := State{
		SchemaVersion: StateSchemaVersion,
		RunID:         runID,
		ProductID:     domain.ProductID("yoyodyne"),
		RepositoryID:  "yoyodyne",
		WorkItemID:    "yoyodyne-test",
		Backend:       domain.BackendClaudeCode,
		Status:        status,
		StartedAt:     now,
		UpdatedAt:     now,
	}
	if status.Terminal() {
		state.CompletedAt = &now
	}
	return state
}
