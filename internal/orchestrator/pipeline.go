package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"yoyodyne/internal/backend"
	"yoyodyne/internal/beads"
	"yoyodyne/internal/checks"
	"yoyodyne/internal/config"
	"yoyodyne/internal/contextbundle"
	"yoyodyne/internal/domain"
	"yoyodyne/internal/execution"
	"yoyodyne/internal/gitworktree"
	"yoyodyne/internal/review"
	"yoyodyne/internal/runstate"
)

// maxCommitSubjectBytes bounds the work item title carried into the
// harness-owned commit subject.
const maxCommitSubjectBytes = 72

type WorkTracker interface {
	Show(ctx context.Context, id string) (beads.WorkItem, error)
	Claim(ctx context.Context, id string) (beads.WorkItem, error)
	RecordOutcome(ctx context.Context, id, notes string) (beads.WorkItem, error)
	Complete(ctx context.Context, id, reason string) (beads.WorkItem, error)
}

type WorktreeManager interface {
	ValidateReady(ctx context.Context) error
	CurrentBranch(ctx context.Context) (string, error)
	Create(ctx context.Context, request gitworktree.CreateRequest) (gitworktree.Worktree, error)
	SummarizeChanges(ctx context.Context, worktree gitworktree.Worktree) (gitworktree.ChangeSummary, error)
	UnifiedChanges(ctx context.Context, worktree gitworktree.Worktree, limits gitworktree.DiffLimits) (gitworktree.ChangeDiff, error)
	Integrate(ctx context.Context, worktree gitworktree.Worktree, message string) (gitworktree.Integration, error)
	CleanupIntegrated(ctx context.Context, request gitworktree.CleanupRequest) (gitworktree.Cleanup, error)
}

// ChangeReviewer runs one independent review of a developer's change. The
// pipeline never decides review semantics itself; it only acts on the verdict.
type ChangeReviewer interface {
	Review(ctx context.Context, request review.Request) (review.Result, error)
}

type StateStore interface {
	Reserve(ctx context.Context, state runstate.State, maxConcurrent int) error
	Save(state runstate.State) error
	AppendEvent(event execution.Event) error
}

type CheckRunner interface {
	Run(ctx context.Context, runID, directory string, commands []string, lastSequence uint64, sink func(execution.Event) error) ([]checks.Result, uint64, error)
}

type Pipeline struct {
	Tracker   WorkTracker
	Worktrees WorktreeManager
	Store     StateStore
	Backend   backend.Backend
	Checks    CheckRunner
	// Reviewer is required only when integration is automatic, because nothing
	// is ever integrated without an independent verdict.
	Reviewer     ChangeReviewer
	Clock        execution.Clock
	NewRunID     func() (string, error)
	Repository   string
	Config       config.Config
	RedactValues []string
}

type Outcome struct {
	RunID        string          `json:"run_id"`
	WorkItemID   string          `json:"work_item_id"`
	Status       runstate.Status `json:"status"`
	Phase        runstate.Phase  `json:"phase,omitempty"`
	Branch       string          `json:"branch,omitempty"`
	WorktreePath string          `json:"worktree_path,omitempty"`
	BaseCommit   string          `json:"base_commit,omitempty"`
	// ProviderSessionID identifies the developer session; ReviewSessionID
	// identifies the separate reviewer session that judged its work. The model
	// pairs are the requested selector and what the provider reported serving.
	ProviderSessionID     string                    `json:"provider_session_id,omitempty"`
	ProviderModel         string                    `json:"provider_model,omitempty"`
	ProviderResolvedModel string                    `json:"provider_resolved_model,omitempty"`
	Checks                []checks.Result           `json:"checks,omitempty"`
	Changes               gitworktree.ChangeSummary `json:"changes"`
	Summary               string                    `json:"summary,omitempty"`
	ReviewSessionID       string                    `json:"review_session_id,omitempty"`
	ReviewModel           string                    `json:"review_model,omitempty"`
	ReviewResolvedModel   string                    `json:"review_resolved_model,omitempty"`
	ReviewDecision        review.Decision           `json:"review_decision,omitempty"`
	ReviewSummary         string                    `json:"review_summary,omitempty"`
	ReviewFindings        []review.Finding          `json:"review_findings,omitempty"`
	Integration           *gitworktree.Integration  `json:"integration,omitempty"`
	WorkItemClosed        bool                      `json:"work_item_closed"`
	// WorktreeRemoved and BranchRemoved report each artifact separately, because
	// cleanup removes them in two steps and a partial result must not describe
	// a deleted artifact as remaining or a surviving one as gone.
	WorktreeRemoved bool   `json:"worktree_removed"`
	BranchRemoved   bool   `json:"branch_removed"`
	Failure         string `json:"failure,omitempty"`
	// CleanupFailure is set when the run completed but at least one artifact
	// survives its post-completion cleanup. The work is integrated and the item
	// is closed.
	CleanupFailure string `json:"cleanup_failure,omitempty"`
	// CompletionRecordingFailure is set when the run completed, both artifacts
	// were removed, and only the final completion record could not be written.
	// It is deliberately distinct from CleanupFailure: nothing remains to clean
	// up, so nothing must be reported as remaining.
	CompletionRecordingFailure string `json:"completion_recording_failure,omitempty"`
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
	// Every invocation names its own model; the harness never lets a provider
	// pick one for it, so the run evidence always says what actually ran.
	if err := config.ValidateModelSelector(developer.Model); err != nil {
		return Outcome{}, fmt.Errorf("developer agent %s", err)
	}
	if len(p.Config.Checks) == 0 {
		return Outcome{}, errors.New("run pipeline requires at least one configured check")
	}
	automatic := p.Config.Approvals.Integration == domain.ApprovalAutomatic
	if automatic {
		if err := p.validateReviewPolicy(); err != nil {
			return Outcome{}, err
		}
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
	// An automatic run is written against exactly the branch it will be promoted
	// into, so the integration target is fixed before any work starts and never
	// inferred afterwards.
	baseRef := "HEAD"
	targetBranch := ""
	if automatic {
		targetBranch, err = p.Worktrees.CurrentBranch(ctx)
		if err != nil {
			return Outcome{}, fmt.Errorf("resolve integration target: %w", err)
		}
		baseRef = targetBranch
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
	if err := p.Store.Reserve(ctx, state, p.Config.Execution.MaxConcurrentDevelopers); err != nil {
		var existing runstate.ExistingWorkItemError
		if errors.As(err, &existing) {
			return Outcome{}, ExistingRunError{State: existing.State}
		}
		return Outcome{}, fmt.Errorf("reserve developer run: %w", err)
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
		outcome.Phase = state.Phase
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
	if err := validateClaimedItem(item, workItemID); err != nil {
		return fail(fmt.Errorf("validate claimed work item: %w", err), runstate.StatusFailed)
	}
	bundle, err := contextbundle.Assemble(contextbundle.Request{RepositoryRoot: p.Repository, WorkItem: item})
	if err != nil {
		return fail(fmt.Errorf("assemble claimed work item context: %w", err), runstate.StatusFailed)
	}
	worktree, err := p.Worktrees.Create(ctx, gitworktree.CreateRequest{
		RunID:        runID,
		WorkItemID:   workItemID,
		BaseRef:      baseRef,
		TargetBranch: targetBranch,
	})
	if err != nil {
		if worktree.Path != "" {
			state.WorktreePath = worktree.Path
			state.Branch = worktree.Branch
			state.BaseCommit = worktree.BaseCommit
			outcome.WorktreePath = worktree.Path
			outcome.Branch = worktree.Branch
			outcome.BaseCommit = worktree.BaseCommit
		}
		return fail(fmt.Errorf("create isolated worktree: %w", err), runstate.StatusFailed)
	}
	state.WorktreePath = worktree.Path
	state.Branch = worktree.Branch
	state.BaseCommit = worktree.BaseCommit
	state.Status = runstate.StatusRunning
	state.Phase = runstate.PhaseDeveloping
	state.UpdatedAt = p.clock().Now()
	if err := p.Store.Save(state); err != nil {
		return fail(fmt.Errorf("save running state: %w", err), runstate.StatusFailed)
	}
	outcome.Branch = worktree.Branch
	outcome.WorktreePath = worktree.Path
	outcome.BaseCommit = worktree.BaseCommit
	outcome.Status = runstate.StatusRunning
	outcome.Phase = state.Phase

	eventSink := func(event execution.Event) error {
		if err := p.Store.AppendEvent(event); err != nil {
			return err
		}
		state.LastSequence = event.Sequence
		state.UpdatedAt = event.Timestamp
		return p.Store.Save(state)
	}
	state.ProviderModel = developer.Model
	outcome.ProviderModel = developer.Model
	providerResult, err := p.Backend.Run(ctx, backend.RunRequest{
		RunID:            runID,
		Role:             domain.RoleDeveloper,
		WorkingDirectory: worktree.Path,
		Prompt:           developerPrompt(bundle.Text),
		Model:            developer.Model,
		PermissionMode:   "acceptEdits",
		LastSequence:     state.LastSequence,
		RedactValues:     p.RedactValues,
		EventSink:        eventSink,
	})
	if err != nil {
		cause := fmt.Errorf("developer backend failed: %w", err)
		summaryCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		changeSummary, summaryErr := p.Worktrees.SummarizeChanges(summaryCtx, worktree)
		cancel()
		if summaryErr != nil {
			cause = errors.Join(cause, fmt.Errorf("summarize changes after developer backend failure: %w", summaryErr))
		} else {
			outcome.Changes = changeSummary
		}
		return fail(cause, statusForContext(ctx))
	}
	state.ProviderSessionID = providerResult.SessionID
	state.ProviderResolvedModel = providerResult.ResolvedModel
	state.LastSequence = providerResult.LastEvent
	state.UpdatedAt = p.clock().Now()
	outcome.ProviderSessionID = providerResult.SessionID
	outcome.ProviderResolvedModel = providerResult.ResolvedModel
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

	state.Phase = runstate.PhaseChecking
	checkResults, lastSequence, err := p.Checks.Run(ctx, runID, worktree.Path, p.Config.Checks, state.LastSequence, eventSink)
	outcome.Checks = checkResults
	state.LastSequence = lastSequence
	if err != nil {
		return fail(fmt.Errorf("verification infrastructure failed: %w", err), statusForContext(ctx))
	}
	// Review and integration are only reachable through passing checks: a failed
	// check ends the run here, before any reviewer is asked and before anything
	// can be promoted.
	for _, check := range checkResults {
		if !check.Passed {
			return fail(fmt.Errorf("verification failed: %s exited with %d", check.Command, check.Process.ExitCode), statusForProcess(check.Process.Status))
		}
	}

	if automatic {
		reviewed, err := p.review(ctx, &state, &outcome, worktree, bundle.Text, checkResults, eventSink)
		if err != nil {
			return fail(err, statusForContext(ctx))
		}
		if reviewed != review.DecisionApprove {
			// A repair verdict stops the run before integration. The worktree,
			// branch, and findings are preserved for the repair attempt that a
			// later Milestone 1 item adds.
			return fail(fmt.Errorf("independent review requires repair: %s", outcome.ReviewSummary), runstate.StatusFailed)
		}
		// An approval only authorizes integration when it demonstrably came from
		// a second invocation. Missing or reused provider identity means the
		// independence the policy relies on was never established.
		if err := validateIndependentInvocations(outcome); err != nil {
			return fail(err, runstate.StatusFailed)
		}

		state.Phase = runstate.PhaseIntegrating
		state.UpdatedAt = p.clock().Now()
		if err := p.Store.Save(state); err != nil {
			return fail(fmt.Errorf("save integrating run state: %w", err), runstate.StatusFailed)
		}
		integration, err := p.Worktrees.Integrate(ctx, worktree, integrationMessage(item, outcome))
		if err != nil {
			return fail(fmt.Errorf("integrate approved change: %w", err), statusForContext(ctx))
		}
		outcome.Integration = &integration
		state.Integration = &runstate.Integration{
			TargetBranch:         integration.TargetBranch,
			SourceCommit:         integration.SourceCommit,
			TargetCommit:         integration.TargetCommit,
			PreviousTargetCommit: integration.PreviousTargetCommit,
		}
		state.Phase = runstate.PhaseCompleting
		state.UpdatedAt = p.clock().Now()
		if err := p.Store.Save(state); err != nil {
			return fail(fmt.Errorf("save integrated run state: %w", err), runstate.StatusFailed)
		}
	}

	// The tracker is updated only once the work is durably where it belongs:
	// after integration when it is automatic, and after passing checks when a
	// human still owns the promotion.
	if _, err := p.Tracker.RecordOutcome(ctx, workItemID, renderOutcomeNotes(outcome)); err != nil {
		return fail(fmt.Errorf("record successful run outcome: %w", err), runstate.StatusFailed)
	}
	if outcome.Integration != nil {
		if _, err := p.Tracker.Complete(ctx, workItemID, completionReason(outcome)); err != nil {
			return fail(fmt.Errorf("close integrated work item: %w", err), runstate.StatusFailed)
		}
		outcome.WorkItemClosed = true
	}

	// The run becomes durably terminal before anything is destroyed. Cleanup is
	// the only remaining step, it removes evidence, and it must never be able to
	// leave a closed item behind a non-terminal run: an interrupted process at
	// this boundary leaves a succeeded run in the cleaning_up phase with
	// worktree_removed still false, which is a resumable instruction rather than
	// a lost run. A reconciler re-runs cleanup, which refuses anything that is
	// not the recorded, registered, already-integrated worktree.
	completedAt := p.clock().Now()
	state.Status = runstate.StatusSucceeded
	state.Phase = runstate.PhaseCleaningUp
	state.UpdatedAt = completedAt
	state.CompletedAt = &completedAt
	if outcome.Integration == nil {
		state.Phase = runstate.PhaseComplete
	}
	if err := p.Store.Save(state); err != nil {
		return fail(fmt.Errorf("save successful run state: %w", err), runstate.StatusFailed)
	}
	outcome.Status = runstate.StatusSucceeded
	outcome.Phase = state.Phase
	if outcome.Integration == nil {
		return outcome, nil
	}

	// Only artifacts proven to be integrated are removed, and only after the
	// tracker agrees the item is done and that fact is durable. Cleanup reports
	// each artifact separately, so a partial removal is recorded as what it is
	// rather than collapsed into a single failed flag.
	cleanup, cleanupErr := p.Worktrees.CleanupIntegrated(ctx, gitworktree.CleanupRequest{
		Worktree:     worktree,
		TargetBranch: outcome.Integration.TargetBranch,
		SourceCommit: outcome.Integration.SourceCommit,
	})
	outcome.WorktreeRemoved = cleanup.WorktreeRemoved
	outcome.BranchRemoved = cleanup.BranchRemoved
	state.WorktreeRemoved = cleanup.WorktreeRemoved
	state.BranchRemoved = cleanup.BranchRemoved
	if cleanupErr != nil {
		// A failure here leaves the run succeeded and reports the outstanding
		// cleanup: the change is integrated and the item is closed. Whatever was
		// removed before the failure is still recorded as removed.
		return p.reportOutstandingCleanup(state, outcome, fmt.Errorf("clean up integrated run artifacts: %w", cleanupErr))
	}
	// Cleanup finished, so the run is complete whatever happens to the record of
	// it. The reported phase follows that fact rather than the write below.
	state.Phase = runstate.PhaseComplete
	state.UpdatedAt = p.clock().Now()
	outcome.Phase = state.Phase
	if err := p.Store.Save(state); err != nil {
		// An interrupted write that recovers must leave a clean terminal record,
		// not a cleanup warning about artifacts that are already gone.
		state.UpdatedAt = p.clock().Now()
		if retryErr := p.Store.Save(state); retryErr != nil {
			return p.reportCompletionRecordingFailure(outcome,
				fmt.Errorf("save completed run state after cleanup: %w", errors.Join(err, retryErr)))
		}
	}
	return outcome, nil
}

// reportCompletionRecordingFailure covers a run whose artifacts were all
// removed but whose final completion record could not be written. Nothing is
// outstanding to clean up, so this must never be described as an incomplete
// cleanup. The durable state keeps the pre-cleanup marker, and resolving it
// costs nothing: a resumed cleanup over absent artifacts is a safe no-op.
func (p Pipeline) reportCompletionRecordingFailure(outcome Outcome, cause error) (Outcome, error) {
	outcome.CompletionRecordingFailure = cause.Error()
	notesCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := p.Tracker.RecordOutcome(notesCtx, outcome.WorkItemID, renderCompletionRecordingNotes(outcome)); err != nil {
		outcome.CompletionRecordingFailure = errors.Join(cause, fmt.Errorf("record the completion problem on the work item: %w", err)).Error()
	}
	return outcome, nil
}

// reportOutstandingCleanup records a post-completion problem without recasting
// a finished run as a failed one. The work is integrated, the item is closed,
// and that is already durable; what is left is a janitorial fact an operator
// and a later reconciler both need to see.
func (p Pipeline) reportOutstandingCleanup(state runstate.State, outcome Outcome, cause error) (Outcome, error) {
	outcome.CleanupFailure = cause.Error()
	state.CleanupFailure = outcome.CleanupFailure
	state.UpdatedAt = p.clock().Now()
	if err := p.Store.Save(state); err != nil {
		outcome.CleanupFailure = errors.Join(cause, fmt.Errorf("record outstanding cleanup: %w", err)).Error()
	}
	notesCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := p.Tracker.RecordOutcome(notesCtx, outcome.WorkItemID, renderCleanupNotes(outcome)); err != nil {
		outcome.CleanupFailure = errors.Join(errors.New(outcome.CleanupFailure), fmt.Errorf("record outstanding cleanup on the work item: %w", err)).Error()
	}
	return outcome, nil
}

// review runs the configured independent reviewer and records exactly what it
// decided, including when it fails or answers with something the verdict
// contract rejects. Every recorded outcome is written into the run state before
// the caller acts on it, so a stopped run still explains why it stopped.
func (p Pipeline) review(
	ctx context.Context,
	state *runstate.State,
	outcome *Outcome,
	worktree gitworktree.Worktree,
	workContext string,
	checkResults []checks.Result,
	eventSink func(execution.Event) error,
) (review.Decision, error) {
	state.Phase = runstate.PhaseReviewing
	state.UpdatedAt = p.clock().Now()
	if err := p.Store.Save(*state); err != nil {
		return "", fmt.Errorf("save reviewing run state: %w", err)
	}
	changes, err := p.Worktrees.UnifiedChanges(ctx, worktree, gitworktree.DiffLimits{})
	if err != nil {
		return "", fmt.Errorf("assemble reviewed change: %w", err)
	}
	result, reviewErr := p.Reviewer.Review(ctx, review.Request{
		RunID:        state.RunID,
		WorkItemID:   state.WorkItemID,
		Context:      workContext,
		WorktreePath: worktree.Path,
		Changes:      changes,
		Checks:       checkResults,
		RedactValues: p.RedactValues,
		LastSequence: state.LastSequence,
		EventSink:    eventSink,
	})
	if result.LastSequence > state.LastSequence {
		state.LastSequence = result.LastSequence
	}
	state.ReviewSessionID = result.SessionID
	state.ReviewModel = result.RequestedModel
	state.ReviewResolvedModel = result.ResolvedModel
	outcome.ReviewSessionID = result.SessionID
	outcome.ReviewModel = result.RequestedModel
	outcome.ReviewResolvedModel = result.ResolvedModel
	if result.Verdict.Summary != "" {
		state.ReviewSummary = result.Verdict.Summary
		state.ReviewFindings = len(result.Verdict.Findings)
		outcome.ReviewSummary = result.Verdict.Summary
		outcome.ReviewFindings = result.Verdict.Findings
	}
	if result.Decision == review.DecisionApprove || result.Decision == review.DecisionRepair {
		state.ReviewDecision = string(result.Decision)
		outcome.ReviewDecision = result.Decision
	}
	if reviewErr != nil {
		return "", fmt.Errorf("independent review failed: %w", reviewErr)
	}
	// The reviewer reports the selector it actually ran with. Auditing that
	// against configuration here keeps the recorded evidence a fact rather than
	// an assumption about how the reviewer was wired.
	configured := p.reviewer().Model
	if result.RequestedModel != configured {
		return "", fmt.Errorf("reviewer ran with model %q, configured reviewer model is %q", result.RequestedModel, configured)
	}
	return result.Decision, nil
}

// validateIndependentInvocations refuses to integrate work whose developer and
// reviewer cannot be told apart. An empty or shared session identifier means
// the second opinion the policy depends on was never demonstrated.
func validateIndependentInvocations(outcome Outcome) error {
	developer := strings.TrimSpace(outcome.ProviderSessionID)
	reviewer := strings.TrimSpace(outcome.ReviewSessionID)
	switch {
	case developer == "" || reviewer == "":
		return fmt.Errorf("integration requires recorded developer and reviewer sessions, got developer %q and reviewer %q", outcome.ProviderSessionID, outcome.ReviewSessionID)
	case developer == reviewer:
		return fmt.Errorf("integration requires an independent reviewer, but both invocations reported session %q", outcome.ProviderSessionID)
	}
	if strings.TrimSpace(outcome.ProviderModel) == "" || strings.TrimSpace(outcome.ReviewModel) == "" {
		return fmt.Errorf("integration requires recorded developer and reviewer model selectors, got developer %q and reviewer %q", outcome.ProviderModel, outcome.ReviewModel)
	}
	return nil
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
	return p.agentForRole(domain.RoleDeveloper)
}

func (p Pipeline) reviewer() config.AgentConfig {
	return p.agentForRole(domain.RoleReviewer)
}

func (p Pipeline) agentForRole(role domain.AgentRole) config.AgentConfig {
	names := make([]string, 0, len(p.Config.Agents))
	for name := range p.Config.Agents {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		agent := p.Config.Agents[name]
		if agent.Role == role {
			return agent
		}
	}
	return config.AgentConfig{}
}

// validateReviewPolicy refuses automatic integration that is not actually
// gated. An unenforceable policy must stop the run before anything is claimed,
// rather than integrate work no independent reviewer ever saw.
func (p Pipeline) validateReviewPolicy() error {
	if p.Reviewer == nil {
		return errors.New("automatic integration requires an independent reviewer")
	}
	reviewer := p.reviewer()
	if reviewer.Role != domain.RoleReviewer {
		return errors.New("automatic integration requires a configured reviewer agent")
	}
	if reviewer.Backend != domain.BackendClaudeCode {
		return fmt.Errorf("run pipeline requires a claude-code reviewer, configured backend is %q", reviewer.Backend)
	}
	if err := config.ValidateModelSelector(reviewer.Model); err != nil {
		return fmt.Errorf("reviewer agent %s", err)
	}
	return nil
}

func validateReadyItem(item beads.WorkItem, requestedID string) error {
	return validateWorkItem(item, requestedID, "open")
}

func validateClaimedItem(item beads.WorkItem, requestedID string) error {
	return validateWorkItem(item, requestedID, "in_progress")
}

func validateWorkItem(item beads.WorkItem, requestedID, expectedStatus string) error {
	if item.ID != requestedID {
		return fmt.Errorf("Beads returned work item %q for requested id %q", item.ID, requestedID)
	}
	if item.Status != expectedStatus {
		return fmt.Errorf("work item %s status is %q, want %s", item.ID, item.Status, expectedStatus)
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
	headline := "Yoyodyne bootstrap run succeeded."
	if outcome.Integration != nil {
		headline = "Yoyodyne run passed checks, was approved by an independent reviewer, and was integrated automatically."
	}
	// These notes are recorded before cleanup, because the item is closed first
	// and only a closed item's artifacts are removed. Cleanup can still fail, so
	// say it is scheduled rather than predicting that it happened.
	worktree := "Worktree: " + outcome.WorktreePath
	if outcome.Integration != nil {
		worktree = "Worktree (cleanup pending): " + outcome.WorktreePath
	}
	lines := []string{
		headline,
		"Run: " + outcome.RunID,
		"Branch: " + outcome.Branch,
		worktree,
		"Base commit: " + outcome.BaseCommit,
	}
	if outcome.ProviderSessionID != "" {
		lines = append(lines, "Claude session: "+outcome.ProviderSessionID)
	}
	if outcome.ProviderModel != "" {
		lines = append(lines, "Developer model: "+renderModel(outcome.ProviderModel, outcome.ProviderResolvedModel))
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
	return strings.Join(append(lines, renderReviewNotes(outcome)...), "\n")
}

func renderFailureNotes(outcome Outcome) string {
	headline := "Yoyodyne bootstrap run failed; branch and worktree are preserved when present."
	switch {
	case outcome.WorktreeRemoved || outcome.BranchRemoved:
		headline = "Yoyodyne run failed after the change was integrated and its artifacts were cleaned up; the integrated commit is the surviving evidence."
	case outcome.Integration != nil:
		headline = "Yoyodyne run failed after the change was already integrated; the integrated commit, branch, and worktree are preserved for reconciliation."
	}
	lines := []string{
		headline,
		"Run: " + outcome.RunID,
		"Failure: " + outcome.Failure,
	}
	if outcome.Phase != "" {
		lines = append(lines, "Phase: "+string(outcome.Phase))
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
	if outcome.ProviderModel != "" {
		lines = append(lines, "Developer model: "+renderModel(outcome.ProviderModel, outcome.ProviderResolvedModel))
	}
	if outcome.Changes.Status != "" {
		lines = append(lines, "Preserved changes:\n"+outcome.Changes.Status)
	}
	if outcome.Changes.DiffStat != "" {
		lines = append(lines, "Preserved diff stat:\n"+outcome.Changes.DiffStat)
	}
	return strings.Join(append(lines, renderReviewNotes(outcome)...), "\n")
}

// renderReviewNotes carries the review and integration evidence into the
// tracker, so an operator reconciling an item never has to reconstruct which
// reviewer decided what or which commit carried the work.
func renderReviewNotes(outcome Outcome) []string {
	var lines []string
	if outcome.ReviewSessionID != "" {
		lines = append(lines, "Reviewer session: "+outcome.ReviewSessionID)
	}
	if outcome.ReviewModel != "" {
		lines = append(lines, "Reviewer model: "+renderModel(outcome.ReviewModel, outcome.ReviewResolvedModel))
	}
	if outcome.ReviewDecision != "" {
		lines = append(lines, "Review decision: "+string(outcome.ReviewDecision))
	}
	if outcome.ReviewSummary != "" {
		lines = append(lines, "Review summary: "+outcome.ReviewSummary)
	}
	for _, finding := range outcome.ReviewFindings {
		location := ""
		if finding.Location != nil {
			location = fmt.Sprintf(" (%s:%d)", finding.Location.File, finding.Location.Line)
		}
		lines = append(lines, fmt.Sprintf("Finding [%s]%s: %s", finding.Severity, location, finding.Message))
	}
	if outcome.Integration != nil {
		lines = append(lines,
			"Integrated into: "+outcome.Integration.TargetBranch,
			"Integrated commit: "+outcome.Integration.SourceCommit,
			"Previous target commit: "+outcome.Integration.PreviousTargetCommit,
		)
	}
	return lines
}

// renderModel reports a requested selector alongside what the provider
// resolved it to, because a floating alias only becomes audit evidence once the
// served model is named.
func renderModel(requested, resolved string) string {
	if resolved == "" || resolved == requested {
		return requested
	}
	return requested + " (resolved: " + resolved + ")"
}

// renderCleanupNotes records that a completed run left its worktree behind. The
// integrated commit and the closed item are already true; this is what an
// operator needs in order to finish the job by hand.
func renderCleanupNotes(outcome Outcome) string {
	// Cleanup can fail after both removals already succeeded, when only the
	// confirmation of them failed. Nothing remains in that case, so it must not
	// be described as unfinished work.
	headline := "Yoyodyne run completed but its post-completion cleanup did not finish. The change is integrated and this item is closed."
	if outcome.WorktreeRemoved && outcome.BranchRemoved {
		headline = "Yoyodyne run completed and both run artifacts were removed, but confirming their removal failed. Nothing is known to remain; a repeated cleanup re-checks and removes nothing."
	}
	lines := []string{
		headline,
		"Run: " + outcome.RunID,
		"Cleanup failure: " + outcome.CleanupFailure,
		"Worktree removed: " + strconv.FormatBool(outcome.WorktreeRemoved),
		"Branch removed: " + strconv.FormatBool(outcome.BranchRemoved),
	}
	// Only artifacts that actually survive are reported as remaining; cleanup
	// is retryable, so naming a deleted one would send an operator after
	// something that is not there.
	if outcome.Branch != "" && !outcome.BranchRemoved {
		lines = append(lines, "Remaining branch: "+outcome.Branch)
	}
	if outcome.WorktreePath != "" && !outcome.WorktreeRemoved {
		lines = append(lines, "Remaining worktree: "+outcome.WorktreePath)
	}
	if outcome.Integration != nil {
		lines = append(lines, "Integrated commit: "+outcome.Integration.SourceCommit)
	}
	return strings.Join(lines, "\n")
}

// renderCompletionRecordingNotes describes a finished run whose completion
// could not be written down. It states that removal is done, because an
// operator reading "cleanup" here must not go looking for artifacts that no
// longer exist.
func renderCompletionRecordingNotes(outcome Outcome) string {
	lines := []string{
		"Yoyodyne run completed and its worktree and branch were both removed, but recording final completion failed. Cleanup is finished; nothing remains to remove.",
		"Run: " + outcome.RunID,
		"Completion recording failure: " + outcome.CompletionRecordingFailure,
		"Worktree removed: " + strconv.FormatBool(outcome.WorktreeRemoved),
		"Branch removed: " + strconv.FormatBool(outcome.BranchRemoved),
	}
	if outcome.Integration != nil {
		lines = append(lines, "Integrated commit: "+outcome.Integration.SourceCommit)
	}
	lines = append(lines, "Durable run state may still show the pre-cleanup marker; reconciling it requires no further removal.")
	return strings.Join(lines, "\n")
}

// integrationMessage describes the promoted work in the harness-owned commit,
// including the review that authorized it.
func integrationMessage(item beads.WorkItem, outcome Outcome) string {
	subject := strings.TrimSpace(fmt.Sprintf("yoyodyne: %s %s", outcome.WorkItemID, singleLine(item.Title, maxCommitSubjectBytes)))
	body := []string{
		"",
		"Run: " + outcome.RunID,
		"Branch: " + outcome.Branch,
		"Base: " + outcome.BaseCommit,
		"Developer session: " + outcome.ProviderSessionID,
		"Reviewer session: " + outcome.ReviewSessionID,
		"Review decision: " + string(outcome.ReviewDecision),
	}
	return subject + "\n" + strings.Join(body, "\n") + "\n"
}

// singleLine folds a tracker-supplied title into one bounded subject line, so
// the commit subject stays a subject whatever the work item contains. It is cut
// on a rune boundary: a subject truncated mid-rune is not text.
func singleLine(value string, limit int) string {
	folded := strings.Join(strings.Fields(value), " ")
	if len(folded) <= limit {
		return folded
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(folded[cut]) {
		cut--
	}
	return strings.TrimSpace(folded[:cut])
}

func completionReason(outcome Outcome) string {
	return fmt.Sprintf("Reviewed and integrated by Yoyodyne run %s: %s is at %s",
		outcome.RunID, outcome.Integration.TargetBranch, outcome.Integration.TargetCommit)
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
