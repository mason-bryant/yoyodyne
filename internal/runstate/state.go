package runstate

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"yoyodyne/internal/domain"
)

const StateSchemaVersion = 1

type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
	StatusTimedOut  Status = "timed_out"
)

// Phase names the step a run reached. It is recorded alongside the status so a
// terminal run says where it stopped and an interrupted run says what was in
// flight, which is what reconciliation and diagnosis both need.
type Phase string

const (
	PhaseDeveloping  Phase = "developing"
	PhaseChecking    Phase = "checking"
	PhaseReviewing   Phase = "reviewing"
	PhaseIntegrating Phase = "integrating"
	PhaseCompleting  Phase = "completing"
	PhaseCleaningUp  Phase = "cleaning_up"
	PhaseComplete    Phase = "complete"
)

// Review decisions are duplicated here rather than imported so the durable
// schema stays independent of the review implementation that produces them.
const (
	ReviewApprove = "approve"
	ReviewRepair  = "repair"
)

// Integration is the durable evidence of a completed promotion: exactly which
// commit the harness created and which commit the target moved from and to.
type Integration struct {
	TargetBranch         string `json:"target_branch"`
	SourceCommit         string `json:"source_commit"`
	TargetCommit         string `json:"target_commit"`
	PreviousTargetCommit string `json:"previous_target_commit"`
}

type State struct {
	SchemaVersion int              `json:"schema_version"`
	RunID         string           `json:"run_id"`
	ProductID     domain.ProductID `json:"product_id"`
	RepositoryID  string           `json:"repository_id"`
	WorkItemID    string           `json:"work_item_id"`
	Backend       domain.Backend   `json:"backend"`
	// ProviderSessionID is the developer session. The reviewer's session is
	// recorded separately because the two are always distinct invocations.
	ProviderSessionID string `json:"provider_session_id,omitempty"`
	// ProviderModel is the selector the developer invocation requested and
	// ProviderResolvedModel is what the provider reported serving it. A
	// floating alias makes the resolved identifier the only real audit record.
	ProviderModel         string     `json:"provider_model,omitempty"`
	ProviderResolvedModel string     `json:"provider_resolved_model,omitempty"`
	Status                Status     `json:"status"`
	Phase                 Phase      `json:"phase,omitempty"`
	LastSequence          uint64     `json:"last_sequence"`
	StartedAt             time.Time  `json:"started_at"`
	UpdatedAt             time.Time  `json:"updated_at"`
	CompletedAt           *time.Time `json:"completed_at,omitempty"`
	WorktreePath          string     `json:"worktree_path,omitempty"`
	Branch                string     `json:"branch,omitempty"`
	BaseCommit            string     `json:"base_commit,omitempty"`
	// WorktreeRemoved and BranchRemoved record the two cleanup steps
	// separately, because they cannot be performed atomically. Recording them
	// apart is what lets an interrupted cleanup be resumed and what keeps a
	// preserved-artifact claim truthful.
	WorktreeRemoved     bool         `json:"worktree_removed,omitempty"`
	BranchRemoved       bool         `json:"branch_removed,omitempty"`
	ReviewSessionID     string       `json:"review_session_id,omitempty"`
	ReviewModel         string       `json:"review_model,omitempty"`
	ReviewResolvedModel string       `json:"review_resolved_model,omitempty"`
	ReviewDecision      string       `json:"review_decision,omitempty"`
	ReviewSummary       string       `json:"review_summary,omitempty"`
	ReviewFindings      int          `json:"review_findings,omitempty"`
	Integration         *Integration `json:"integration,omitempty"`
	Failure             string       `json:"failure,omitempty"`
	// CleanupFailure explains why post-completion cleanup did not finish
	// cleanly. The run's work is already integrated, closed, and durable when it
	// is set, so it is reconciliation input rather than a run failure. It says
	// nothing on its own about what survives: WorktreeRemoved and BranchRemoved
	// carry that, and either can still be false, leaving a real artifact behind,
	// or both can be true because the removals succeeded and only the check that
	// confirms them could not run. A reconciler resumes cleanup in both cases,
	// which is a safe no-op over artifacts that are already gone.
	CleanupFailure string `json:"cleanup_failure,omitempty"`
}

var (
	runIDPattern  = regexp.MustCompile(`^run-[a-f0-9]{32}$`)
	commitPattern = regexp.MustCompile(`^[a-f0-9]{40}([a-f0-9]{24})?$`)
)

func NewRunID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate run id: %w", err)
	}
	return "run-" + hex.EncodeToString(bytes), nil
}

func (s State) Validate() error {
	var problems []error
	if s.SchemaVersion != StateSchemaVersion {
		problems = append(problems, fmt.Errorf("schema_version must be %d", StateSchemaVersion))
	}
	if !runIDPattern.MatchString(s.RunID) {
		problems = append(problems, errors.New("run_id is invalid"))
	}
	if err := domain.ValidateIdentifier("product id", string(s.ProductID)); err != nil {
		problems = append(problems, err)
	}
	if strings.TrimSpace(s.RepositoryID) == "" {
		problems = append(problems, errors.New("repository_id is required"))
	}
	if strings.TrimSpace(s.WorkItemID) == "" {
		problems = append(problems, errors.New("work_item_id is required"))
	}
	if !s.Backend.Valid() {
		problems = append(problems, errors.New("backend is invalid"))
	}
	if !s.Status.Valid() {
		problems = append(problems, errors.New("status is invalid"))
	}
	if s.StartedAt.IsZero() || s.UpdatedAt.IsZero() {
		problems = append(problems, errors.New("started_at and updated_at are required"))
	}
	if s.UpdatedAt.Before(s.StartedAt) {
		problems = append(problems, errors.New("updated_at cannot be before started_at"))
	}
	if s.Status.Terminal() && s.CompletedAt == nil {
		problems = append(problems, errors.New("terminal status requires completed_at"))
	}
	if !s.Status.Terminal() && s.CompletedAt != nil {
		problems = append(problems, errors.New("non-terminal status cannot have completed_at"))
	}
	worktreeFields := 0
	for _, value := range []string{s.WorktreePath, s.Branch, s.BaseCommit} {
		if value != "" {
			worktreeFields++
		}
	}
	if worktreeFields != 0 && worktreeFields != 3 {
		problems = append(problems, errors.New("worktree_path, branch, and base_commit must be recorded together"))
	}
	if s.BaseCommit != "" && !commitPattern.MatchString(s.BaseCommit) {
		problems = append(problems, errors.New("base_commit is invalid"))
	}
	if s.Phase != "" && !s.Phase.Valid() {
		problems = append(problems, errors.New("phase is invalid"))
	}
	if s.ReviewDecision != "" && s.ReviewDecision != ReviewApprove && s.ReviewDecision != ReviewRepair {
		problems = append(problems, errors.New("review_decision is invalid"))
	}
	if s.ReviewFindings < 0 {
		problems = append(problems, errors.New("review_findings cannot be negative"))
	}
	if s.Integration != nil {
		// Recorded integration is a claim that approved work was promoted, so it
		// is only coherent with the approval, the worktree that produced it, and
		// the two independent invocations that authorized it.
		if s.ReviewDecision != ReviewApprove {
			problems = append(problems, errors.New("integration requires an approving review decision"))
		}
		if s.BaseCommit == "" {
			problems = append(problems, errors.New("integration requires the integrated worktree"))
		}
		problems = append(problems, s.validateIndependentInvocations()...)
		if err := s.Integration.Validate(); err != nil {
			problems = append(problems, err)
		}
	}
	if (s.WorktreeRemoved || s.BranchRemoved) && s.Integration == nil {
		problems = append(problems, errors.New("removed artifacts require recorded integration"))
	}
	// A run is complete only once nothing is left to clean up; anything else is
	// still an outstanding-cleanup marker.
	if s.Integration != nil && s.Phase == PhaseComplete && (!s.WorktreeRemoved || !s.BranchRemoved) {
		problems = append(problems, errors.New("complete phase requires both the worktree and branch to be removed"))
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid run state: %w", errors.Join(problems...))
	}
	return nil
}

// validateIndependentInvocations enforces what an integrated change claims: two
// separate provider invocations, each with its own recorded session and its own
// declared model selector. A missing or reused session identity means nothing
// independent was proven, so it must never appear alongside integration.
func (s State) validateIndependentInvocations() []error {
	var problems []error
	// Compare the normalized identifiers: two sessions that differ only in
	// surrounding whitespace are one session, and must not read as independent.
	developer := strings.TrimSpace(s.ProviderSessionID)
	reviewer := strings.TrimSpace(s.ReviewSessionID)
	if developer == "" || reviewer == "" {
		problems = append(problems, errors.New("integration requires recorded developer and reviewer session identifiers"))
	} else if developer == reviewer {
		problems = append(problems, errors.New("integration requires distinct developer and reviewer session identifiers"))
	}
	if strings.TrimSpace(s.ProviderModel) == "" || strings.TrimSpace(s.ReviewModel) == "" {
		problems = append(problems, errors.New("integration requires recorded developer and reviewer model selectors"))
	}
	return problems
}

// Validate rejects integration evidence that cannot describe a real promotion.
func (i Integration) Validate() error {
	var problems []error
	if strings.TrimSpace(i.TargetBranch) == "" {
		problems = append(problems, errors.New("integration target_branch is required"))
	}
	for _, commit := range []struct {
		field string
		value string
	}{
		{field: "source_commit", value: i.SourceCommit},
		{field: "target_commit", value: i.TargetCommit},
		{field: "previous_target_commit", value: i.PreviousTargetCommit},
	} {
		if !commitPattern.MatchString(commit.value) {
			problems = append(problems, fmt.Errorf("integration %s is invalid", commit.field))
		}
	}
	if i.SourceCommit != "" && i.SourceCommit == i.PreviousTargetCommit {
		problems = append(problems, errors.New("integration did not move the target"))
	}
	// Integration is fast-forward only: the target ends up at exactly the commit
	// the harness created. Any other pair describes a merge or a reset that this
	// harness never performs, so it is rejected rather than recorded.
	if i.SourceCommit != "" && i.TargetCommit != "" && i.SourceCommit != i.TargetCommit {
		problems = append(problems, errors.New("integration target_commit must equal the fast-forwarded source_commit"))
	}
	return errors.Join(problems...)
}

func (p Phase) Valid() bool {
	switch p {
	case PhaseDeveloping, PhaseChecking, PhaseReviewing, PhaseIntegrating, PhaseCompleting, PhaseCleaningUp, PhaseComplete:
		return true
	default:
		return false
	}
}

func (s Status) Valid() bool {
	switch s {
	case StatusPending, StatusRunning, StatusSucceeded, StatusFailed, StatusCancelled, StatusTimedOut:
		return true
	default:
		return false
	}
}

func (s Status) Terminal() bool {
	return s == StatusSucceeded || s == StatusFailed || s == StatusCancelled || s == StatusTimedOut
}

func DefaultRoot(getenv func(string) string, userHomeDir func() (string, error), goos string) (string, error) {
	if value := strings.TrimSpace(getenv("YOYODYNE_STATE_HOME")); value != "" {
		if !filepath.IsAbs(value) {
			return "", errors.New("YOYODYNE_STATE_HOME must be an absolute path")
		}
		return filepath.Clean(value), nil
	}
	if value := strings.TrimSpace(getenv("XDG_STATE_HOME")); value != "" {
		if !filepath.IsAbs(value) {
			return "", errors.New("XDG_STATE_HOME must be an absolute path")
		}
		return filepath.Join(filepath.Clean(value), "yoyodyne"), nil
	}

	home, err := userHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home directory: %w", err)
	}
	switch goos {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Yoyodyne", "state"), nil
	case "windows":
		if localAppData := strings.TrimSpace(getenv("LOCALAPPDATA")); localAppData != "" {
			if !filepath.IsAbs(localAppData) {
				return "", errors.New("LOCALAPPDATA must be an absolute path")
			}
			return filepath.Join(localAppData, "Yoyodyne", "state"), nil
		}
		return filepath.Join(home, "AppData", "Local", "Yoyodyne", "state"), nil
	default:
		return filepath.Join(home, ".local", "state", "yoyodyne"), nil
	}
}

func SystemDefaultRoot(getenv func(string) string, userHomeDir func() (string, error)) (string, error) {
	return DefaultRoot(getenv, userHomeDir, runtime.GOOS)
}
