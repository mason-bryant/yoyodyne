package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"yoyodyne/internal/backend"
	"yoyodyne/internal/config"
	"yoyodyne/internal/domain"
	"yoyodyne/internal/execution"
	"yoyodyne/internal/gitworktree"
	"yoyodyne/internal/report"
	"yoyodyne/internal/review"
	"yoyodyne/internal/runstate"
)

const branchReviewID = "run-abcdef0123456789abcdef0123456789"

// accumulatedRepository builds the shape a branch review exists for: several
// commits, each of which is a complete and consistent change on its own, whose
// combination is the only place a defect between them could be seen.
func accumulatedRepository(t *testing.T) string {
	t.Helper()
	repository := pipelineRepository(t)
	runPipelineGit(t, repository, "checkout", "-b", "milestone")
	for _, commit := range []struct{ file, content, message string }{
		{"store.go", "package harness\n\nfunc write(key string) {}\n", "record the durable evidence"},
		{"reader.go", "package harness\n\nfunc read(other string) {}\n", "read the durable evidence back"},
		{"docs/design.md", "design content\nand what the two of them do\n", "describe both halves"},
	} {
		if err := os.WriteFile(filepath.Join(repository, filepath.FromSlash(commit.file)), []byte(commit.content), 0o600); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
		runPipelineGit(t, repository, "add", commit.file)
		runPipelineGit(t, repository, "commit", "-m", commit.message)
	}
	runPipelineGit(t, repository, "checkout", "main")
	return repository
}

func newBranchReviewer(t *testing.T, repository string, provider *fakeBackend) (BranchReviewer, *runstate.BranchReviewStore, *runstate.ReportStore) {
	t.Helper()
	worktrees, err := gitworktree.New(gitworktree.Options{
		Runner:         execution.OSProcessRunner{},
		RepositoryRoot: repository,
		WorktreeRoot:   filepath.Join(t.TempDir(), "worktrees"),
	})
	if err != nil {
		t.Fatalf("gitworktree.New() error = %v", err)
	}
	stateRoot := t.TempDir()
	reviews, err := runstate.NewBranchReviewStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewBranchReviewStore() error = %v", err)
	}
	reports, err := runstate.NewReportStore(stateRoot, "yoyodyne")
	if err != nil {
		t.Fatalf("runstate.NewReportStore() error = %v", err)
	}
	cfg := config.Config{
		Version: config.CurrentVersion,
		Product: config.Product{
			ID: "yoyodyne", RepositoryID: "yoyodyne", Repository: repository,
			Specifications: config.DefaultSpecifications,
			Invariants:     config.DefaultInvariants,
		},
		Agents: map[string]config.AgentConfig{
			"reviewer": {Role: domain.RoleReviewer, Backend: domain.BackendClaudeCode, Model: testReviewerModel, Instances: 1},
		},
	}
	return BranchReviewer{
		Worktrees: worktrees,
		// The real reviewer over a fake provider: the contract, the verdict
		// decoding, and the independence evidence are all exercised, and only the
		// provider itself is a double.
		Reviewer: review.Reviewer{
			Backend: provider,
			Model:   testReviewerModel,
			Clock:   fixedBranchClock{},
		},
		Reviews:     reviews,
		Reports:     reports,
		Clock:       fixedBranchClock{},
		NewReviewID: func() (string, error) { return branchReviewID, nil },
		Repository:  repository,
		Config:      cfg,
	}, reviews, reports
}

// branchProvider answers one reviewer invocation with a fixed reply.
func branchProvider(reply string) *fakeBackend {
	return &fakeBackend{run: func(request backend.RunRequest) (backend.RunResult, error) {
		return backend.RunResult{
			Backend:       domain.BackendClaudeCode,
			SessionID:     "branch-review-session",
			ResolvedModel: "claude-opus-5",
			FinalText:     reply,
			Process:       execution.ProcessResult{Status: execution.ProcessSucceeded},
		}, nil
	}}
}

type fixedBranchClock struct{}

func (fixedBranchClock) Now() time.Time { return time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC) }

func TestBranchReviewJudgesEveryCommitTogetherAndRecordsTheVerdict(t *testing.T) {
	t.Parallel()

	repository := accumulatedRepository(t)
	provider := branchProvider(`{"decision":"approve","summary":"the three commits agree with one another"}` + "\n" +
		"```yoyodyne-report\n{\"reports\":[{\"severity\":\"note\",\"message\":\"the two halves are named differently\"}]}\n```")
	reviewer, reviews, reports := newBranchReviewer(t, repository, provider)

	outcome, err := reviewer.Review(context.Background(), BranchReviewRequest{Branch: "milestone", BaseRef: "main"})
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if !outcome.Approved() || outcome.Decision != review.DecisionApprove {
		t.Fatalf("Review() = %#v", outcome)
	}
	if outcome.Commits != 3 || outcome.CommitsOmitted != 0 || outcome.Truncated {
		t.Errorf("reviewed change = %#v", outcome)
	}
	// The same session and model evidence a per-item review carries.
	if outcome.SessionID != "branch-review-session" || outcome.Model != testReviewerModel || outcome.ResolvedModel != "claude-opus-5" {
		t.Errorf("independence evidence = %#v", outcome)
	}
	if outcome.BaseCommit == outcome.HeadCommit || outcome.BaseCommit != gitLine(t, repository, "rev-parse", "refs/heads/main") {
		t.Errorf("reviewed range = %#v", outcome)
	}

	// One provider invocation, made as the reviewer, with no tools and nothing
	// to write with — the independence a per-item review is held to.
	requests := provider.requestsForRole(domain.RoleReviewer)
	if len(requests) != 1 {
		t.Fatalf("reviewer invocations = %d", len(requests))
	}
	request := requests[0]
	if len(request.AllowedTools) != 0 || request.PermissionMode != "plan" || request.SessionID != "" {
		t.Errorf("reviewer invocation = %#v", request)
	}
	// All three commits reached it as one change, described as a history.
	for _, want := range []string{
		"record the durable evidence",
		"read the durable evidence back",
		"describe both halves",
		"b/store.go",
		"b/reader.go",
		"accumulated 3 commit(s)",
	} {
		if !strings.Contains(request.Prompt, want) {
			t.Errorf("accumulated evidence is missing %q", want)
		}
	}
	if !strings.Contains(request.SystemPrompt, "A finding may span commits") {
		t.Error("the reviewer was not told a finding may span commits")
	}

	recorded, err := reviews.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(recorded) != 1 {
		t.Fatalf("recorded reviews = %#v", recorded)
	}
	if !recorded[0].Approved() || recorded[0].Branch != "milestone" || recorded[0].Commits != 3 ||
		recorded[0].SessionID != "branch-review-session" || recorded[0].ResolvedModel != "claude-opus-5" {
		t.Errorf("recorded review = %#v", recorded[0])
	}
	// What the reviewer noticed beside its verdict is collected exactly as a
	// run's reports are.
	collected, err := reports.List()
	if err != nil || len(collected) != 1 || collected[0].Severity != report.SeverityNote {
		t.Fatalf("collected reports = %#v, %v", collected, err)
	}
	if collected[0].RunID != branchReviewID || collected[0].Role != domain.RoleReviewer {
		t.Errorf("report attribution = %#v", collected[0])
	}
}

func TestBranchRepairVerdictIsRecordedAndChangesNothingAlreadyIntegrated(t *testing.T) {
	t.Parallel()

	repository := accumulatedRepository(t)
	head := gitLine(t, repository, "rev-parse", "refs/heads/milestone")
	provider := branchProvider(`{"decision":"repair","summary":"the two halves disagree about the key they use",` +
		`"findings":[{"severity":"major","message":"store.go writes under key and reader.go reads under other","location":{"file":"reader.go","line":3}}]}`)
	reviewer, reviews, _ := newBranchReviewer(t, repository, provider)

	// A repair verdict at this scope is a completed review, not a failed one:
	// the review worked and asked for repairs.
	outcome, err := reviewer.Review(context.Background(), BranchReviewRequest{Branch: "milestone", BaseRef: "main"})
	if err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if outcome.Decision != review.DecisionRepair || len(outcome.Findings) != 1 {
		t.Fatalf("Review() = %#v", outcome)
	}
	// It is not an approval of the branch, and that is the one question anything
	// downstream asks.
	if outcome.Approved() {
		t.Fatal("a repair verdict read as an approval of the branch")
	}
	// And it decides nothing about the work already integrated: the branch is
	// where it was, and nothing was reverted, reopened, or unmerged.
	if now := gitLine(t, repository, "rev-parse", "refs/heads/milestone"); now != head {
		t.Errorf("branch moved from %s to %s on a repair verdict", head, now)
	}

	recorded, err := reviews.List()
	if err != nil || len(recorded) != 1 {
		t.Fatalf("List() = %#v, %v", recorded, err)
	}
	if recorded[0].Decision != runstate.ReviewRepair || len(recorded[0].Findings) != 1 ||
		recorded[0].Findings[0].File != "reader.go" || recorded[0].Findings[0].Severity != runstate.SeverityMajor {
		t.Errorf("recorded repair = %#v", recorded[0])
	}
}

func TestBranchReviewRecordsAReviewThatNeverAnswered(t *testing.T) {
	t.Parallel()

	repository := accumulatedRepository(t)
	provider := &fakeBackend{run: func(backend.RunRequest) (backend.RunResult, error) {
		return backend.RunResult{}, errors.New("claude is not installed")
	}}
	reviewer, reviews, _ := newBranchReviewer(t, repository, provider)

	outcome, err := reviewer.Review(context.Background(), BranchReviewRequest{Branch: "milestone", BaseRef: "main"})
	if err == nil || !strings.Contains(err.Error(), "independent branch review failed") {
		t.Fatalf("Review() error = %v", err)
	}
	if outcome.Approved() || outcome.Decision != "" {
		t.Fatalf("a failed review produced a decision: %#v", outcome)
	}
	// A branch nobody could review is not the same fact as a branch nobody
	// reviewed, so the attempt is durable either way.
	recorded, err := reviews.List()
	if err != nil || len(recorded) != 1 {
		t.Fatalf("List() = %#v, %v", recorded, err)
	}
	if recorded[0].Decision != "" || !strings.Contains(recorded[0].Failure, "claude is not installed") {
		t.Errorf("recorded failure = %#v", recorded[0])
	}
}

func TestBranchReviewCannotApproveAChangeItCouldNotSeeInFull(t *testing.T) {
	t.Parallel()

	repository := accumulatedRepository(t)
	provider := branchProvider(`{"decision":"approve","summary":"looks consistent"}`)
	reviewer, reviews, _ := newBranchReviewer(t, repository, provider)
	// A bound too small for the accumulated patch is the ordinary way a large
	// branch is described, and an approval of what was cut is refused.
	reviewer.Limits = gitworktree.DiffLimits{MaxTotalBytes: 120}

	outcome, err := reviewer.Review(context.Background(), BranchReviewRequest{Branch: "milestone", BaseRef: "main"})
	if err == nil || !strings.Contains(err.Error(), "cannot approve an incomplete change representation") {
		t.Fatalf("Review() error = %v", err)
	}
	if outcome.Approved() {
		t.Fatal("a truncated accumulated change was approved")
	}
	if !outcome.Truncated {
		t.Errorf("a clamped change was not reported as truncated: %#v", outcome)
	}
	recorded, err := reviews.List()
	if err != nil || len(recorded) != 1 || !recorded[0].Truncated || recorded[0].Approved() {
		t.Fatalf("recorded review = %#v, %v", recorded, err)
	}
}

func TestBranchReviewRefusesABranchThatAccumulatedNothing(t *testing.T) {
	t.Parallel()

	repository := accumulatedRepository(t)
	provider := branchProvider(`{"decision":"approve","summary":"fine"}`)
	reviewer, reviews, _ := newBranchReviewer(t, repository, provider)

	if _, err := reviewer.Review(context.Background(), BranchReviewRequest{Branch: "milestone", BaseRef: "milestone"}); !errors.Is(err, gitworktree.ErrNoAccumulatedChange) {
		t.Fatalf("Review() of an empty range error = %v", err)
	}
	// Nothing was invoked and nothing was recorded: there was no change to judge.
	if len(provider.requests) != 0 {
		t.Errorf("the provider was invoked %d times for an empty range", len(provider.requests))
	}
	if recorded, err := reviews.List(); err != nil || len(recorded) != 0 {
		t.Fatalf("List() = %#v, %v", recorded, err)
	}
}
