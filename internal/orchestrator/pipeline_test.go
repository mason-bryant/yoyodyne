package orchestrator

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"yoyodyne/internal/backend"
	"yoyodyne/internal/beads"
	"yoyodyne/internal/checks"
	"yoyodyne/internal/config"
	"yoyodyne/internal/domain"
	"yoyodyne/internal/execution"
	"yoyodyne/internal/gitworktree"
	"yoyodyne/internal/runstate"
)

const pipelineRunID = "run-0123456789abcdef0123456789abcdef"

func TestPipelineEndToEndWithFakeBackend(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{
		ID:                 "yoyodyne-task",
		Title:              "Add feature",
		Description:        "Follow docs/design.md",
		Design:             "Make a bounded change",
		AcceptanceCriteria: "feature.txt exists",
		Status:             "open",
	}}
	tracker.onClaim = func() error {
		if err := os.MkdirAll(filepath.Join(repository, ".beads"), 0o700); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(repository, ".beads", "issues.jsonl"), []byte("claim control state\n"), 0o600)
	}
	provider := &fakeBackend{run: func(request backend.RunRequest) (backend.RunResult, error) {
		if !strings.Contains(request.Prompt, "design content") {
			return backend.RunResult{}, errors.New("prompt did not contain referenced design")
		}
		if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "feature.txt"), []byte("implemented\n"), 0o600); err != nil {
			return backend.RunResult{}, err
		}
		sequence := execution.NewSequence(request.LastSequence)
		for _, eventType := range []execution.EventType{execution.EventRunStarted, execution.EventAgentMessage, execution.EventRunCompleted} {
			event, err := execution.NewEvent(request.RunID, sequence.Next(), time.Now(), eventType, "fake", nil)
			if err != nil {
				return backend.RunResult{}, err
			}
			if err := request.EventSink(event); err != nil {
				return backend.RunResult{}, err
			}
		}
		return backend.RunResult{
			Backend:   domain.BackendClaudeCode,
			SessionID: "session-1",
			FinalText: "implemented feature",
			Process:   execution.ProcessResult{Status: execution.ProcessSucceeded, ExitCode: 0},
			LastEvent: sequence.Last(),
		}, nil
	}}
	pipeline, store := newPipeline(t, repository, tracker, provider, []string{"test -f feature.txt"})

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusSucceeded || outcome.Branch == "" || outcome.WorktreePath == "" || outcome.BaseCommit == "" || outcome.ProviderSessionID != "session-1" {
		t.Fatalf("Run() outcome = %#v", outcome)
	}
	if !strings.Contains(outcome.Changes.Status, "?? feature.txt") {
		t.Fatalf("change summary = %#v", outcome.Changes)
	}
	if !tracker.claimed || !strings.Contains(tracker.notes, "bootstrap run succeeded") || strings.Contains(tracker.notes, "closed") {
		t.Fatalf("tracker = %#v", tracker)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.Status != runstate.StatusSucceeded || state.Branch != outcome.Branch || state.BaseCommit != outcome.BaseCommit || state.ProviderSessionID != "session-1" {
		t.Fatalf("state = %#v", state)
	}
	events, err := store.LoadEvents(outcome.RunID)
	if err != nil {
		t.Fatalf("LoadEvents() error = %v", err)
	}
	if len(events) != 5 || events[len(events)-1].Type != execution.EventCommandCompleted {
		t.Fatalf("events = %#v", events)
	}
	if _, err := os.Stat(filepath.Join(outcome.WorktreePath, "feature.txt")); err != nil {
		t.Fatalf("preserved worktree change missing: %v", err)
	}
}

func TestPipelinePreservesFailedWorkAndRecordsFailure(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Fail", Status: "open"}}
	provider := &fakeBackend{run: func(request backend.RunRequest) (backend.RunResult, error) {
		if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "partial.txt"), []byte("partial"), 0o600); err != nil {
			return backend.RunResult{}, err
		}
		return backend.RunResult{
			SessionID:  "session-failed",
			FinalText:  "provider failed",
			IsError:    true,
			StopReason: "provider_error",
			Process:    execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1},
		}, nil
	}}
	pipeline, store := newPipeline(t, repository, tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err == nil || !strings.Contains(err.Error(), "developer reported failure") {
		t.Fatalf("Run() error = %v", err)
	}
	if outcome.Status != runstate.StatusFailed || outcome.WorktreePath == "" {
		t.Fatalf("Run() outcome = %#v", outcome)
	}
	if !strings.Contains(tracker.notes, "bootstrap run failed") || !strings.Contains(tracker.notes, outcome.RunID) {
		t.Fatalf("failure notes = %q", tracker.notes)
	}
	if !strings.Contains(tracker.notes, "?? partial.txt") {
		t.Fatalf("failure notes did not include preserved changes: %q", tracker.notes)
	}
	if _, err := os.Stat(filepath.Join(outcome.WorktreePath, "partial.txt")); err != nil {
		t.Fatalf("failed worktree was not preserved: %v", err)
	}
	state, err := store.Load(outcome.RunID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.Status != runstate.StatusFailed || state.CompletedAt == nil {
		t.Fatalf("state = %#v", state)
	}
}

func TestPipelineCapturesChangesWhenBackendReturnsInfrastructureError(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Fail", Status: "open"}}
	provider := &fakeBackend{run: func(request backend.RunRequest) (backend.RunResult, error) {
		if err := os.WriteFile(filepath.Join(request.WorkingDirectory, "partial.txt"), []byte("partial"), 0o600); err != nil {
			return backend.RunResult{}, err
		}
		return backend.RunResult{}, errors.New("malformed terminal stream")
	}}
	pipeline, _ := newPipeline(t, repository, tracker, provider, []string{"exit 0"})

	outcome, err := pipeline.Run(context.Background(), tracker.item.ID)
	if err == nil || !strings.Contains(err.Error(), "developer backend failed") {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(outcome.Changes.Status, "?? partial.txt") {
		t.Fatalf("Run() change summary = %#v", outcome.Changes)
	}
	if !strings.Contains(tracker.notes, "Preserved changes:\n?? partial.txt") {
		t.Fatalf("failure notes omitted preserved changes: %q", tracker.notes)
	}
}

func TestPipelineRefusesUnauthenticatedOrDuplicateRunBeforeClaim(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := &fakeBackend{availability: backend.Availability{Installed: true, Authenticated: false, AuthMethod: "none"}}
	pipeline, _ := newPipeline(t, repository, tracker, provider, []string{"exit 0"})
	if _, err := pipeline.Run(context.Background(), tracker.item.ID); err == nil || !strings.Contains(err.Error(), "claude auth login") {
		t.Fatalf("Run() auth error = %v", err)
	}
	if tracker.claimed {
		t.Fatal("unauthenticated run claimed work")
	}

	provider.availability = backend.Availability{Installed: true, Authenticated: true}
	pipeline, store := newPipeline(t, repository, tracker, provider, []string{"exit 0"})
	now := time.Now().UTC()
	if err := store.Create(runstate.State{
		SchemaVersion: runstate.StateSchemaVersion,
		RunID:         pipelineRunID,
		ProductID:     "yoyodyne",
		RepositoryID:  "yoyodyne",
		WorkItemID:    tracker.item.ID,
		Backend:       domain.BackendClaudeCode,
		Status:        runstate.StatusRunning,
		StartedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatalf("Create() state error = %v", err)
	}
	if _, err := pipeline.Run(context.Background(), tracker.item.ID); err == nil {
		t.Fatal("Run() duplicate error = nil")
	} else {
		var existing ExistingRunError
		if !errors.As(err, &existing) {
			t.Fatalf("Run() error = %T %v, want ExistingRunError", err, err)
		}
	}
	if tracker.claimed {
		t.Fatal("duplicate run claimed work")
	}
}

func TestPipelineRefusesBlockedItemBeforeClaim(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{
		ID:     "yoyodyne-task",
		Title:  "Blocked task",
		Status: "open",
		Dependencies: []beads.Dependency{
			{ID: "yoyodyne-blocker", Type: "blocks", Status: "open"},
			{ID: "yoyodyne-parent", Type: "parent-child", Status: "open"},
		},
	}}
	provider := &fakeBackend{availability: backend.Availability{Installed: true, Authenticated: true}}
	pipeline, store := newPipeline(t, repository, tracker, provider, []string{"exit 0"})

	if _, err := pipeline.Run(context.Background(), tracker.item.ID); err == nil || !strings.Contains(err.Error(), "yoyodyne-blocker") {
		t.Fatalf("Run() blocked error = %v", err)
	}
	if tracker.claimed {
		t.Fatal("blocked run claimed work")
	}
	states, err := store.Incomplete()
	if err != nil {
		t.Fatalf("Incomplete() error = %v", err)
	}
	if len(states) != 0 {
		t.Fatalf("blocked run created state: %#v", states)
	}
}

func TestPipelineRevalidatesBlockersReturnedByClaim(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	tracker.onClaim = func() error {
		tracker.item.Dependencies = []beads.Dependency{{ID: "late-blocker", Type: "blocks", Status: "open"}}
		return nil
	}
	providerCalled := false
	provider := &fakeBackend{run: func(backend.RunRequest) (backend.RunResult, error) {
		providerCalled = true
		return backend.RunResult{}, nil
	}}
	pipeline, _ := newPipeline(t, repository, tracker, provider, []string{"exit 0"})

	if _, err := pipeline.Run(context.Background(), tracker.item.ID); err == nil || !strings.Contains(err.Error(), "late-blocker") {
		t.Fatalf("Run() late blocker error = %v", err)
	}
	if !tracker.claimed || providerCalled {
		t.Fatalf("claimed = %t, provider called = %t", tracker.claimed, providerCalled)
	}
	if !strings.Contains(tracker.notes, "bootstrap run failed") {
		t.Fatalf("failure notes = %q", tracker.notes)
	}
}

func TestValidateClaimedItemRejectsIdentityStatusAndBlockerChanges(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		item beads.WorkItem
		want string
	}{
		{name: "different item", item: beads.WorkItem{ID: "other-task", Status: "in_progress"}, want: "other-task"},
		{name: "not claimed", item: beads.WorkItem{ID: "yoyodyne-task", Status: "open"}, want: "want in_progress"},
		{name: "new blocker", item: beads.WorkItem{ID: "yoyodyne-task", Status: "in_progress", Dependencies: []beads.Dependency{{ID: "late-blocker", Type: "blocks", Status: "open"}}}, want: "late-blocker"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := validateClaimedItem(test.item, "yoyodyne-task"); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateClaimedItem() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestPipelineRejectsAutomaticIntegrationUntilClosedLoopExists(t *testing.T) {
	t.Parallel()

	repository := pipelineRepository(t)
	tracker := &fakeTracker{item: beads.WorkItem{ID: "yoyodyne-task", Title: "Task", Status: "open"}}
	provider := &fakeBackend{availability: backend.Availability{Installed: true, Authenticated: true}}
	pipeline, _ := newPipeline(t, repository, tracker, provider, []string{"exit 0"})
	pipeline.Config.Approvals.Integration = domain.ApprovalAutomatic
	pipeline.Config.Agents["reviewer"] = config.AgentConfig{Role: domain.RoleReviewer, Backend: domain.BackendClaudeCode, Instances: 1}

	if _, err := pipeline.Run(context.Background(), tracker.item.ID); err == nil || !strings.Contains(err.Error(), "does not yet support automatic integration") {
		t.Fatalf("Run() automatic integration error = %v", err)
	}
	if tracker.claimed {
		t.Fatal("unsupported automatic integration claimed work")
	}
}

type fakeTracker struct {
	item    beads.WorkItem
	claimed bool
	notes   string
	onClaim func() error
}

func (f *fakeTracker) Show(context.Context, string) (beads.WorkItem, error) {
	return f.item, nil
}

func (f *fakeTracker) Claim(context.Context, string) (beads.WorkItem, error) {
	if f.onClaim != nil {
		if err := f.onClaim(); err != nil {
			return beads.WorkItem{}, err
		}
	}
	f.claimed = true
	f.item.Status = "in_progress"
	return f.item, nil
}

func (f *fakeTracker) RecordOutcome(_ context.Context, _ string, notes string) (beads.WorkItem, error) {
	f.notes += notes
	return f.item, nil
}

type fakeBackend struct {
	availability backend.Availability
	run          func(backend.RunRequest) (backend.RunResult, error)
}

func (f *fakeBackend) CheckAvailability(context.Context) (backend.Availability, error) {
	if !f.availability.Installed && !f.availability.Authenticated && f.availability.AuthMethod == "" {
		return backend.Availability{Installed: true, Authenticated: true}, nil
	}
	return f.availability, nil
}

func (*fakeBackend) Capabilities() backend.Capabilities {
	return backend.Capabilities{StructuredEvents: true}
}

func (f *fakeBackend) Run(_ context.Context, request backend.RunRequest) (backend.RunResult, error) {
	return f.run(request)
}

func newPipeline(t *testing.T, repository string, tracker *fakeTracker, provider *fakeBackend, commands []string) (Pipeline, *runstate.Store) {
	t.Helper()
	processRunner := execution.OSProcessRunner{}
	worktrees, err := gitworktree.New(gitworktree.Options{
		Runner:                processRunner,
		RepositoryRoot:        repository,
		WorktreeRoot:          filepath.Join(t.TempDir(), "worktrees"),
		AllowedPrimaryChanges: []string{".beads/interactions.jsonl", ".beads/issues.jsonl"},
	})
	if err != nil {
		t.Fatalf("gitworktree.New() error = %v", err)
	}
	store, err := runstate.NewStore(t.TempDir(), "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewStore() error = %v", err)
	}
	cfg := config.Config{
		Version: config.CurrentVersion,
		Product: config.Product{ID: "yoyodyne", RepositoryID: "yoyodyne", Repository: repository},
		Execution: config.Execution{
			MaxConcurrentDevelopers:    1,
			RepairAttemptsBeforeReplan: 2,
			WorktreeRoot:               "auto",
		},
		Approvals: config.Approvals{
			Brief: domain.ApprovalHuman, Goals: domain.ApprovalHuman, Designs: domain.ApprovalAutomatic, Integration: domain.ApprovalHuman,
		},
		Checks: commands,
		Agents: map[string]config.AgentConfig{
			"developer": {Role: domain.RoleDeveloper, Backend: domain.BackendClaudeCode, Instances: 1},
		},
	}
	return Pipeline{
		Tracker: tracker, Worktrees: worktrees, Store: store, Backend: provider,
		Checks: checks.Runner{Process: processRunner}, NewRunID: func() (string, error) { return pipelineRunID, nil },
		Repository: repository, Config: cfg,
	}, store
}

func pipelineRepository(t *testing.T) string {
	t.Helper()
	repository := filepath.Join(t.TempDir(), "repository")
	if err := os.MkdirAll(filepath.Join(repository, "docs"), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(repository, "docs", "design.md"), []byte("design content\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	runPipelineGit(t, repository, "init", "-b", "main")
	runPipelineGit(t, repository, "config", "user.name", "Yoyodyne Test")
	runPipelineGit(t, repository, "config", "user.email", "yoyodyne@example.invalid")
	runPipelineGit(t, repository, "add", ".")
	runPipelineGit(t, repository, "commit", "-m", "initial")
	return repository
}

func runPipelineGit(t *testing.T, repository string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repository}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v error = %v: %s", args, err, output)
	}
}
