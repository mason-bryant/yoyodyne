package gitworktree

// The branch half of the change representation. One work item's change is a
// worktree against the commit it was created at; an accumulated change is a
// branch against the base it grew from, which is many commits made by many work
// items. Both are rendered into the same bounded ChangeDiff, because what reads
// them — the independent reviewer — must not have to care which it was handed:
// what varies is the change under review, not the review.

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/mason-bryant/yoyodyne/internal/execution"
)

const (
	// DefaultMaxBranchDiffBytes bounds an accumulated change's patch. It is
	// larger than the per-work-item bound because a branch is many work items by
	// construction, and it is still well inside the reviewer's own input bound,
	// so a large accumulated change is described rather than refused for its
	// size. It is what a branch-scope caller passes; DiffLimits itself keeps the
	// per-work-item default, because a worktree is not this.
	DefaultMaxBranchDiffBytes = 512 << 10
	// DefaultMaxDiffCommits bounds how many of a range's commits are described.
	// A branch with more of them is described by its most recent ones and
	// reported as truncated, because the accumulated history is evidence in the
	// same way the patch is.
	DefaultMaxDiffCommits = 500
	// maxDescribedSubjectBytes bounds one described commit subject, so a single
	// pathological subject cannot crowd out the commits either side of it.
	maxDescribedSubjectBytes = 200
)

// ErrNoAccumulatedChange reports that a branch carries no commit its base does
// not. It is a refusal rather than a failure, in the same way an empty
// integration is: there is no accumulated change to judge, and a review of
// nothing would decide nothing.
var ErrNoAccumulatedChange = errors.New("branch carries no commits over its base")

// BranchRequest names the accumulated change to describe: a local branch, and
// the base it is measured against.
type BranchRequest struct {
	// Branch is a plain local branch name, held to the same rule an integration
	// target is: never HEAD and never a fully qualified ref, so a caller can
	// only ever name refs/heads/<branch>.
	Branch string
	// BaseRef is the base the branch is measured against. It may be a branch
	// name or a commit; what is recorded afterwards is the commit it resolved
	// to, because that is the only thing a later reader can rely on.
	BaseRef string
}

// Commit is one commit of an accumulated change. Only the identifier and the
// subject are kept: the bodies are the developers' own account of their work,
// and what the patch actually did is already below in the patch.
type Commit struct {
	Commit  string `json:"commit"`
	Subject string `json:"subject"`
}

// BranchChange is what one branch accumulated over a base: the commits it
// carries, and the bounded diff of the whole range as a single patch. The range
// is base..head rather than a merge base, because the base is named by the
// caller and answering a different question than the one asked is how a review
// ends up judging a range nobody chose.
type BranchChange struct {
	Branch     string   `json:"branch"`
	BaseRef    string   `json:"base_ref"`
	BaseCommit string   `json:"base_commit"`
	HeadCommit string   `json:"head_commit"`
	Commits    []Commit `json:"commits"`
	// CommitsOmitted counts the commits the bound dropped, oldest first. It is
	// recorded beside the truncation flag on the diff because they are separate
	// losses: a patch can be complete while the history that produced it is not.
	CommitsOmitted int        `json:"commits_omitted,omitempty"`
	Changes        ChangeDiff `json:"changes"`
}

// BranchChanges reports what a branch accumulated over a base commit as one
// bounded change. It only reads, and it reads the repository rather than a
// worktree: an accumulated change is committed by construction, so there is no
// untracked half to render and no working tree whose state could alter what is
// described. The per-file bounds in DiffLimits therefore do not apply here —
// they bound separately rendered untracked files — and the whole patch is
// bounded by MaxTotalBytes exactly as a worktree's tracked half is.
//
// The base must be an ancestor of the branch. A base that has moved away
// describes a reconciliation rather than an accumulated change, and quietly
// diffing from a merge base instead would review a range the caller never
// named.
func (m *Manager) BranchChanges(ctx context.Context, request BranchRequest, limits DiffLimits) (BranchChange, error) {
	limits, err := limits.resolve()
	if err != nil {
		return BranchChange{}, err
	}
	if err := validateBranchRequest(request); err != nil {
		return BranchChange{}, err
	}
	if err := m.validateRepository(ctx); err != nil {
		return BranchChange{}, err
	}
	headCommit, err := m.resolveBranchCommit(ctx, request.Branch)
	if err != nil {
		return BranchChange{}, err
	}
	baseResult, err := m.run(ctx, "-C", m.repositoryRoot, "rev-parse", "--verify", request.BaseRef+"^{commit}")
	if err != nil {
		return BranchChange{}, err
	}
	if baseResult.Status != execution.ProcessSucceeded {
		return BranchChange{}, fmt.Errorf("resolve base ref %s failed with exit code %d: %s", request.BaseRef, baseResult.ExitCode, strings.TrimSpace(baseResult.Stderr))
	}
	baseCommit := strings.TrimSpace(baseResult.Stdout)
	if !commitPattern.MatchString(baseCommit) {
		return BranchChange{}, fmt.Errorf("resolved base commit %q is invalid", baseCommit)
	}
	if baseCommit == headCommit {
		return BranchChange{}, ErrNoAccumulatedChange
	}
	ancestor, err := m.run(ctx, "-C", m.repositoryRoot, "merge-base", "--is-ancestor", baseCommit, headCommit)
	if err != nil {
		return BranchChange{}, err
	}
	if ancestor.Status != execution.ProcessSucceeded {
		return BranchChange{}, fmt.Errorf("base commit %s is not an ancestor of branch %s; name the commit the branch was grown from", baseCommit, request.Branch)
	}

	change := BranchChange{
		Branch:     request.Branch,
		BaseRef:    request.BaseRef,
		BaseCommit: baseCommit,
		HeadCommit: headCommit,
	}
	total, err := m.countCommits(ctx, baseCommit, headCommit)
	if err != nil {
		return BranchChange{}, err
	}
	if total == 0 {
		return BranchChange{}, ErrNoAccumulatedChange
	}
	change.Commits, err = m.describeCommits(ctx, baseCommit, headCommit, limits.MaxCommits)
	if err != nil {
		return BranchChange{}, err
	}
	change.CommitsOmitted = total - len(change.Commits)

	changes, err := m.rangeDiff(ctx, baseCommit, headCommit, limits.MaxTotalBytes)
	if err != nil {
		return BranchChange{}, err
	}
	// A history the caller cannot see is as incomplete as a patch it cannot see:
	// the reviewer is told the change is truncated either way, and the rule that
	// an incomplete change cannot be approved holds without knowing which half
	// went missing.
	if change.CommitsOmitted > 0 {
		changes.Truncated = true
	}
	change.Changes = changes
	return change, nil
}

// countCommits reports how many commits the range holds, which is what makes an
// omission countable rather than merely visible as a shorter list.
func (m *Manager) countCommits(ctx context.Context, baseCommit, headCommit string) (int, error) {
	result, err := m.run(ctx, "-C", m.repositoryRoot, "rev-list", "--count", baseCommit+".."+headCommit)
	if err != nil {
		return 0, err
	}
	if result.Status != execution.ProcessSucceeded {
		return 0, fmt.Errorf("count accumulated commits failed with exit code %d: %s", result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	count, err := strconv.Atoi(strings.TrimSpace(result.Stdout))
	if err != nil {
		return 0, fmt.Errorf("parse accumulated commit count: %w", err)
	}
	return count, nil
}

// describeCommits lists the range's commits oldest first, keeping the most
// recent ones when the bound cannot hold them all: what a branch did last is
// what its accumulated shape most recently became.
func (m *Manager) describeCommits(ctx context.Context, baseCommit, headCommit string, maxCommits int) ([]Commit, error) {
	// Merges are described like every other commit. A branch that accumulated
	// its work through pull requests is mostly merge commits, and a listing that
	// dropped them would describe a range as emptier than it is while the count
	// beside it said otherwise.
	result, err := m.run(ctx, "-C", m.repositoryRoot, "log", "--reverse",
		"--max-count="+strconv.Itoa(maxCommits), "--format=%H%x00%s", baseCommit+".."+headCommit)
	if err != nil {
		return nil, err
	}
	if result.Status != execution.ProcessSucceeded {
		return nil, fmt.Errorf("describe accumulated commits failed with exit code %d: %s", result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	var commits []Commit
	for _, line := range strings.Split(strings.TrimSuffix(result.Stdout, "\n"), "\n") {
		if line == "" {
			continue
		}
		commit, subject, found := strings.Cut(line, "\x00")
		if !found {
			return nil, fmt.Errorf("describe accumulated commits returned an unreadable line %q", line)
		}
		commits = append(commits, Commit{Commit: commit, Subject: boundSubject(subject)})
	}
	return commits, nil
}

// rangeDiff renders the whole range as one bounded patch, alongside the same
// status and diff stat a worktree's change carries.
func (m *Manager) rangeDiff(ctx context.Context, baseCommit, headCommit string, maxTotalBytes int) (ChangeDiff, error) {
	names, err := m.run(ctx, "-C", m.repositoryRoot, "diff", "--name-status", "--no-ext-diff", baseCommit, headCommit, "--")
	if err != nil {
		return ChangeDiff{}, err
	}
	if names.Status != execution.ProcessSucceeded {
		return ChangeDiff{}, fmt.Errorf("summarize accumulated status failed with exit code %d: %s", names.ExitCode, strings.TrimSpace(names.Stderr))
	}
	diffStat, err := m.run(ctx, "-C", m.repositoryRoot, "diff", "--stat", "--no-ext-diff", baseCommit, headCommit, "--")
	if err != nil {
		return ChangeDiff{}, err
	}
	if diffStat.Status != execution.ProcessSucceeded {
		return ChangeDiff{}, fmt.Errorf("summarize accumulated diff failed with exit code %d: %s", diffStat.ExitCode, strings.TrimSpace(diffStat.Stderr))
	}
	patch, err := m.run(ctx, "-C", m.repositoryRoot, "diff", "--no-ext-diff", "--patch", baseCommit, headCommit, "--")
	if err != nil {
		return ChangeDiff{}, err
	}
	if patch.Status != execution.ProcessSucceeded {
		return ChangeDiff{}, fmt.Errorf("diff accumulated changes failed with exit code %d: %s", patch.ExitCode, strings.TrimSpace(patch.Stderr))
	}
	clamped, truncated := clampToWholeLines(patch.Stdout, maxTotalBytes)
	return ChangeDiff{
		Status:    renderChangedNames(names.Stdout, nil),
		DiffStat:  strings.TrimSpace(diffStat.Stdout),
		Patch:     clamped,
		Truncated: truncated || containsBinaryDiff(patch.Stdout),
	}, nil
}

func validateBranchRequest(request BranchRequest) error {
	var problems []error
	switch {
	case strings.TrimSpace(request.Branch) == "":
		problems = append(problems, errors.New("reviewed branch is required"))
	default:
		if err := validateRef(request.Branch); err != nil {
			problems = append(problems, fmt.Errorf("invalid reviewed branch: %w", err))
		} else if request.Branch == "HEAD" || strings.HasPrefix(request.Branch, "refs/") {
			problems = append(problems, fmt.Errorf("reviewed branch %q must be a local branch name", request.Branch))
		}
	}
	if strings.TrimSpace(request.BaseRef) == "" {
		problems = append(problems, errors.New("base ref is required"))
	} else if err := validateRef(request.BaseRef); err != nil {
		problems = append(problems, fmt.Errorf("invalid base ref: %w", err))
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid branch change request: %w", errors.Join(problems...))
	}
	return nil
}

// boundSubject keeps one commit subject to its bound and says that it was cut,
// so nobody reads a clipped subject as the whole of what a commit claimed.
func boundSubject(subject string) string {
	subject = strings.TrimSpace(subject)
	if len(subject) <= maxDescribedSubjectBytes {
		return subject
	}
	cut := maxDescribedSubjectBytes
	for cut > 0 && !utf8.RuneStart(subject[cut]) {
		cut--
	}
	return strings.TrimSpace(subject[:cut]) + "…"
}
