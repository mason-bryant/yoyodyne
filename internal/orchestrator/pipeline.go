package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"yoyodyne/internal/backend"
	"yoyodyne/internal/beads"
	"yoyodyne/internal/checks"
	"yoyodyne/internal/config"
	"yoyodyne/internal/contextbundle"
	"yoyodyne/internal/domain"
	"yoyodyne/internal/execution"
	"yoyodyne/internal/gitworktree"
	"yoyodyne/internal/runstate"
)

type WorkTracker interface {
	Show(ctx context.Context, id string) (beads.WorkItem, error)
	Claim(ctx context.Context, id string) (beads.WorkItem, error)
	RecordOutcome(ctx context.Context, id, notes string) (beads.WorkItem, error)
}

type WorktreeManager interface {
	ValidateReady(ctx context.Context) error
	Create(ctx context.Context, request gitworktree.CreateRequest) (gitworktree.Worktree, error)
	SummarizeChanges(ctx context.Context, worktree gitworktree.Worktree) (gitworktree.ChangeSummary, error)
}

type StateStore interface {
	Create(state runstate.State) error
	Save(state runstate.State) error
	Incomplete() ([]runstate.State, error)
	AppendEvent(event execution.Event) error
}

type CheckRunner interface {
	Run(ctx context.Context, runID, directory string, commands []string, lastSequence uint64, sink func(execution.Event) error) ([]checks.Result, uint64, error)
}

type Pipeline struct {
	Tracker    WorkTracker
	Worktrees  WorktreeManager
	Store      StateStore
	Backend    backend.Backend
	Checks     CheckRunner
	Clock      execution.Clock
	NewRunID   func() (string, error)
	Repository string
	Config     config.Config
}

type Outcome struct {
	RunID             string                    `json:"run_id"`
	WorkItemID        string                    `json:"work_item_id"`
	Status            runstate.Status           `json:"status"`
	Branch            string                    `json:"branch,omitempty"`
	WorktreePath      string                    `json:"worktree_path,omitempty"`
	BaseCommit        string                    `json:"base_commit,omitempty"`
	ProviderSessionID string                    `json:"provider_session_id,omitempty"`
	Checks            []checks.Result           `json:"checks,omitempty"`
	Changes           gitworktree.ChangeSummary `json:"changes"`
	Summary           string                    `json:"summary,omitempty"`
	Failure           string                    `json:"failure,omitempty"`
}

type ExistingRunError struct {
	State runstate.State
}

func (e ExistingRunError) Error() string {
	return fmt.Sprintf("work item %s already has incomplete run %s in status %s", e.State.WorkItemID, e.State.RunID, e.State.Status)
}

func (p Pipeline) Run(ctx context.Context, workItemID string) (outcome Outcome, returnedErr error) {
	if err := p.validate(); err != nil {
		return Outcome{}, err
	}
	if err := p.Config.Validate(); err != nil {
		return Outcome{}, err
	}
	developer := p.developer()
	if developer.Backend != domain.BackendClaudeCode {
		return Outcome{}, fmt.Errorf("Milestone 0 run pipeline requires a claude-code developer, configured backend is %q", developer.Backend)
	}
	if len(p.Config.Checks) == 0 {
		return Outcome{}, errors.New("Milestone 0 run pipeline requires at least one configured check")
	}
	availability, err := p.Backend.CheckAvailability(ctx)
	if err != nil {
		return Outcome{}, err
	}
	if !availability.Installed {
		return Outcome{}, errors.New("Claude Code is not installed")
	}
	if !availability.Authenticated {
		return Outcome{}, fmt.Errorf("Claude Code is not authenticated; run `claude auth login` before handing work to Yoyodyne (auth method: %s)", availability.AuthMethod)
	}

	item, err := p.Tracker.Show(ctx, workItemID)
	if err != nil {
		return Outcome{}, fmt.Errorf("load work item: %w", err)
	}
	if err := validateReadyItem(item, workItemID); err != nil {
		return Outcome{}, err
	}
	if _, err := contextbundle.Assemble(contextbundle.Request{RepositoryRoot: p.Repository, WorkItem: item}); err != nil {
		return Outcome{}, fmt.Errorf("validate work item context: %w", err)
	}
	if err := p.Worktrees.ValidateReady(ctx); err != nil {
		return Outcome{}, fmt.Errorf("repository is not ready for an isolated run: %w", err)
	}
	incomplete, err := p.Store.Incomplete()
	if err != nil {
		return Outcome{}, fmt.Errorf("discover incomplete runs: %w", err)
	}
	for _, state := range incomplete {
		if state.WorkItemID == workItemID {
			return Outcome{}, ExistingRunError{State: state}
		}
	}

	runID, err := p.NewRunID()
	if err != nil {
		return Outcome{}, err
	}
	now := p.clock().Now()
	state := runstate.State{
		SchemaVersion: runstate.StateSchemaVersion,
		RunID:         runID,
		ProductID:     p.Config.Product.ID,
		RepositoryID:  string(p.Config.Product.RepositoryID),
		WorkItemID:    workItemID,
		Backend:       domain.BackendClaudeCode,
		Status:        runstate.StatusPending,
		StartedAt:     now,
		UpdatedAt:     now,
	}
	if err := p.Store.Create(state); err != nil {
		return Outcome{}, fmt.Errorf("create run state: %w", err)
	}
	outcome = Outcome{RunID: runID, WorkItemID: workItemID, Status: runstate.StatusPending}
	claimed := false
	fail := func(cause error, status runstate.Status) (Outcome, error) {
		message := cause.Error()
		completedAt := p.clock().Now()
		state.Status = status
		state.UpdatedAt = completedAt
		state.CompletedAt = &completedAt
		state.Failure = message
		if saveErr := p.Store.Save(state); saveErr != nil {
			cause = errors.Join(cause, fmt.Errorf("save failed run state: %w", saveErr))
		}
		outcome.Status = status
		outcome.Failure = message
		outcome.Branch = state.Branch
		outcome.WorktreePath = state.WorktreePath
		outcome.BaseCommit = state.BaseCommit
		outcome.ProviderSessionID = state.ProviderSessionID
		if claimed {
			recordCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_, recordErr := p.Tracker.RecordOutcome(recordCtx, workItemID, renderFailureNotes(outcome))
			cancel()
			if recordErr != nil {
				cause = errors.Join(cause, fmt.Errorf("record failed run outcome: %w", recordErr))
			}
		}
		return outcome, cause
	}

	item, err = p.Tracker.Claim(ctx, workItemID)
	if err != nil {
		return fail(fmt.Errorf("claim work item: %w", err), runstate.StatusFailed)
	}
	claimed = true
	bundle, err := contextbundle.Assemble(contextbundle.Request{RepositoryRoot: p.Repository, WorkItem: item})
	if err != nil {
		return fail(fmt.Errorf("assemble claimed work item context: %w", err), runstate.StatusFailed)
	}
	worktree, err := p.Worktrees.Create(ctx, gitworktree.CreateRequest{RunID: runID, WorkItemID: workItemID, BaseRef: "HEAD"})
	if err != nil {
		return fail(fmt.Errorf("create isolated worktree: %w", err), runstate.StatusFailed)
	}
	state.WorktreePath = worktree.Path
	state.Branch = worktree.Branch
	state.BaseCommit = worktree.BaseCommit
	state.Status = runstate.StatusRunning
	state.UpdatedAt = p.clock().Now()
	if err := p.Store.Save(state); err != nil {
		return fail(fmt.Errorf("save running state: %w", err), runstate.StatusFailed)
	}
	outcome.Branch = worktree.Branch
	outcome.WorktreePath = worktree.Path
	outcome.BaseCommit = worktree.BaseCommit
	outcome.Status = runstate.StatusRunning

	eventSink := func(event execution.Event) error {
		if err := p.Store.AppendEvent(event); err != nil {
			return err
		}
		state.LastSequence = event.Sequence
		state.UpdatedAt = event.Timestamp
		return p.Store.Save(state)
	}
	providerResult, err := p.Backend.Run(ctx, backend.RunRequest{
		RunID:            runID,
		Role:             domain.RoleDeveloper,
		WorkingDirectory: worktree.Path,
		Prompt:           developerPrompt(bundle.Text),
		PermissionMode:   "acceptEdits",
		LastSequence:     state.LastSequence,
		EventSink:        eventSink,
	})
	if err != nil {
		return fail(fmt.Errorf("developer backend failed: %w", err), statusForContext(ctx))
	}
	state.ProviderSessionID = providerResult.SessionID
	state.LastSequence = providerResult.LastEvent
	state.UpdatedAt = p.clock().Now()
	outcome.ProviderSessionID = providerResult.SessionID
	outcome.Summary = providerResult.FinalText
	if err := p.Store.Save(state); err != nil {
		return fail(fmt.Errorf("save developer outcome state: %w", err), runstate.StatusFailed)
	}
	changeSummary, err := p.Worktrees.SummarizeChanges(ctx, worktree)
	if err != nil {
		return fail(fmt.Errorf("summarize developer changes: %w", err), statusForContext(ctx))
	}
	outcome.Changes = changeSummary
	if providerResult.IsError {
		return fail(fmt.Errorf("developer reported failure: %s", nonEmpty(providerResult.StopReason, providerResult.FinalText)), statusForProcess(providerResult.Process.Status))
	}

	checkResults, lastSequence, err := p.Checks.Run(ctx, runID, worktree.Path, p.Config.Checks, state.LastSequence, eventSink)
	outcome.Checks = checkResults
	state.LastSequence = lastSequence
	if err != nil {
		return fail(fmt.Errorf("verification infrastructure failed: %w", err), statusForContext(ctx))
	}
	for _, check := range checkResults {
		if !check.Passed {
			return fail(fmt.Errorf("verification failed: %s exited with %d", check.Command, check.Process.ExitCode), statusForProcess(check.Process.Status))
		}
	}

	notes := renderOutcomeNotes(outcome)
	if _, err := p.Tracker.RecordOutcome(ctx, workItemID, notes); err != nil {
		return fail(fmt.Errorf("record successful run outcome: %w", err), runstate.StatusFailed)
	}
	completedAt := p.clock().Now()
	state.Status = runstate.StatusSucceeded
	state.UpdatedAt = completedAt
	state.CompletedAt = &completedAt
	if err := p.Store.Save(state); err != nil {
		return fail(fmt.Errorf("save successful run state: %w", err), runstate.StatusFailed)
	}
	outcome.Status = runstate.StatusSucceeded
	return outcome, nil
}

func (p Pipeline) validate() error {
	var problems []error
	if p.Tracker == nil {
		problems = append(problems, errors.New("work tracker is required"))
	}
	if p.Worktrees == nil {
		problems = append(problems, errors.New("worktree manager is required"))
	}
	if p.Store == nil {
		problems = append(problems, errors.New("state store is required"))
	}
	if p.Backend == nil {
		problems = append(problems, errors.New("agent backend is required"))
	}
	if p.Checks == nil {
		problems = append(problems, errors.New("check runner is required"))
	}
	if p.NewRunID == nil {
		problems = append(problems, errors.New("run id generator is required"))
	}
	if strings.TrimSpace(p.Repository) == "" {
		problems = append(problems, errors.New("repository is required"))
	}
	if len(problems) > 0 {
		return errors.Join(problems...)
	}
	return nil
}

func (p Pipeline) developer() config.AgentConfig {
	names := make([]string, 0, len(p.Config.Agents))
	for name := range p.Config.Agents {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		agent := p.Config.Agents[name]
		if agent.Role == domain.RoleDeveloper {
			return agent
		}
	}
	return config.AgentConfig{}
}

func validateReadyItem(item beads.WorkItem, requestedID string) error {
	if item.ID != requestedID {
		return fmt.Errorf("Beads returned work item %q for requested id %q", item.ID, requestedID)
	}
	if item.Status != "open" {
		return fmt.Errorf("work item %s is not ready: status is %q, want open", item.ID, item.Status)
	}
	var blockers []string
	for _, dependency := range item.Dependencies {
		if dependency.Type == "blocks" && dependency.Status != "closed" {
			blockers = append(blockers, dependency.ID)
		}
	}
	if len(blockers) > 0 {
		sort.Strings(blockers)
		return fmt.Errorf("work item %s is blocked by: %s", item.ID, strings.Join(blockers, ", "))
	}
	return nil
}

func (p Pipeline) clock() execution.Clock {
	if p.Clock == nil {
		return execution.RealClock{}
	}
	return p.Clock
}

func developerPrompt(bundle string) string {
	return `You are the developer for one bounded Yoyodyne work item.

Work only inside the current assigned worktree. Do not create, remove, or switch branches or worktrees. Do not commit or integrate the change. Do not modify upstream product, goal, design, or specification artifacts; report a proposed upstream change instead. Implement the assigned work, run relevant focused checks, and finish with a concise summary of changes, verification, and any remaining risk.

` + bundle
}

func renderOutcomeNotes(outcome Outcome) string {
	lines := []string{
		"Yoyodyne bootstrap run succeeded.",
		"Run: " + outcome.RunID,
		"Branch: " + outcome.Branch,
		"Worktree: " + outcome.WorktreePath,
		"Base commit: " + outcome.BaseCommit,
	}
	if outcome.ProviderSessionID != "" {
		lines = append(lines, "Claude session: "+outcome.ProviderSessionID)
	}
	if outcome.Changes.Status != "" {
		lines = append(lines, "Changes:\n"+outcome.Changes.Status)
	}
	if outcome.Changes.DiffStat != "" {
		lines = append(lines, "Diff stat:\n"+outcome.Changes.DiffStat)
	}
	for _, check := range outcome.Checks {
		lines = append(lines, fmt.Sprintf("Check: %s (passed=%t, exit=%d)", check.Command, check.Passed, check.Process.ExitCode))
	}
	return strings.Join(lines, "\n")
}

func renderFailureNotes(outcome Outcome) string {
	lines := []string{
		"Yoyodyne bootstrap run failed; branch and worktree are preserved when present.",
		"Run: " + outcome.RunID,
		"Failure: " + outcome.Failure,
	}
	if outcome.Branch != "" {
		lines = append(lines, "Branch: "+outcome.Branch)
	}
	if outcome.WorktreePath != "" {
		lines = append(lines, "Worktree: "+outcome.WorktreePath)
	}
	if outcome.BaseCommit != "" {
		lines = append(lines, "Base commit: "+outcome.BaseCommit)
	}
	if outcome.ProviderSessionID != "" {
		lines = append(lines, "Claude session: "+outcome.ProviderSessionID)
	}
	if outcome.Changes.Status != "" {
		lines = append(lines, "Preserved changes:\n"+outcome.Changes.Status)
	}
	if outcome.Changes.DiffStat != "" {
		lines = append(lines, "Preserved diff stat:\n"+outcome.Changes.DiffStat)
	}
	return strings.Join(lines, "\n")
}

func statusForContext(ctx context.Context) runstate.Status {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return runstate.StatusTimedOut
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return runstate.StatusCancelled
	}
	return runstate.StatusFailed
}

func statusForProcess(status execution.ProcessStatus) runstate.Status {
	switch status {
	case execution.ProcessCancelled:
		return runstate.StatusCancelled
	case execution.ProcessTimedOut:
		return runstate.StatusTimedOut
	default:
		return runstate.StatusFailed
	}
}

func nonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return "unknown provider failure"
}
