package runstate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"yoyodyne/internal/domain"
	"yoyodyne/internal/execution"
)

func TestCleanupFailedCreateRemovesPartialState(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "partial.json")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	cause := errors.New("sync failed")
	if err := cleanupFailedCreate(file, path, cause); !errors.Is(err, cause) {
		t.Fatalf("cleanupFailedCreate() error = %v, want cause", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial state still exists: %v", err)
	}
}

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

func TestStoreRejectsStateItsReaderCannotLoad(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusRunning)
	state.Failure = strings.Repeat("x", maxEncodedStateBytes)
	if err := store.Create(state); err == nil || !strings.Contains(err.Error(), "encoded run state is") {
		t.Fatalf("Create() oversized state error = %v", err)
	}
	path, err := store.statePath(state.RunID)
	if err != nil {
		t.Fatalf("statePath() error = %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("oversized create left a state file: %v", err)
	}

	state.Failure = ""
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() valid state error = %v", err)
	}
	state.Failure = strings.Repeat("x", maxEncodedStateBytes)
	if err := store.Save(state); err == nil || !strings.Contains(err.Error(), "encoded run state is") {
		t.Fatalf("Save() oversized state error = %v", err)
	}
	loaded, err := store.Load(state.RunID)
	if err != nil {
		t.Fatalf("Load() original state error = %v", err)
	}
	if loaded.Failure != "" {
		t.Fatalf("oversized save replaced original state: failure bytes = %d", len(loaded.Failure))
	}
}

func TestStoreRejectsStateFromAnotherRunFile(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusRunning)
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	otherRunID, err := NewRunID()
	if err != nil {
		t.Fatalf("NewRunID() error = %v", err)
	}
	source, err := store.statePath(state.RunID)
	if err != nil {
		t.Fatalf("statePath() source error = %v", err)
	}
	target, err := store.statePath(otherRunID)
	if err != nil {
		t.Fatalf("statePath() target error = %v", err)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if err := os.WriteFile(target, data, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if _, err := store.Load(otherRunID); err == nil || !strings.Contains(err.Error(), "belongs to run") {
		t.Fatalf("Load() mismatched run error = %v", err)
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

func TestStoreRejectsEventsItsReaderCannotLoad(t *testing.T) {
	t.Parallel()

	store := newTestStore(t)
	state := testState(t, StatusRunning)
	if err := store.Create(state); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	baseEvent, err := execution.NewEvent(state.RunID, 1, state.StartedAt, execution.EventProcessOutput, "harness", map[string]any{
		"text": "",
	})
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}
	baseEncoded, err := encodeEvent(baseEvent)
	if err != nil {
		t.Fatalf("encodeEvent() error = %v", err)
	}
	maxTextBytes := maxEncodedEventBytes - len(baseEncoded)
	boundaryEvent, err := execution.NewEvent(state.RunID, 1, state.StartedAt, execution.EventProcessOutput, "harness", map[string]any{
		"text": strings.Repeat("x", maxTextBytes),
	})
	if err != nil {
		t.Fatalf("NewEvent() boundary error = %v", err)
	}
	if err := store.AppendEvent(boundaryEvent); err != nil {
		t.Fatalf("AppendEvent() boundary error = %v", err)
	}
	if events, err := store.LoadEvents(state.RunID); err != nil || len(events) != 1 {
		t.Fatalf("LoadEvents() boundary = %d events, error %v", len(events), err)
	}

	path, err := store.eventPath(state.RunID)
	if err != nil {
		t.Fatalf("eventPath() error = %v", err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() before oversized append error = %v", err)
	}
	oversizedEvent, err := execution.NewEvent(state.RunID, 2, state.StartedAt, execution.EventProcessOutput, "harness", map[string]any{
		"text": strings.Repeat("x", maxTextBytes+1),
	})
	if err != nil {
		t.Fatalf("NewEvent() oversized error = %v", err)
	}
	if err := store.AppendEvent(oversizedEvent); err == nil || !strings.Contains(err.Error(), "encoded event is") {
		t.Fatalf("AppendEvent() oversized error = %v", err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() after oversized append error = %v", err)
	}
	if after.Size() != before.Size() {
		t.Fatalf("oversized append changed event log size from %d to %d", before.Size(), after.Size())
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
