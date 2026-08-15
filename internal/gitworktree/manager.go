package gitworktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"yoyodyne/internal/execution"
)

const defaultTimeout = 30 * time.Second

type Manager struct {
	runner                execution.ProcessRunner
	gitBinary             string
	repositoryRoot        string
	worktreeRoot          string
	allowedPrimaryChanges map[string]struct{}
	timeout               time.Duration
}

type Options struct {
	Runner         execution.ProcessRunner
	GitBinary      string
	RepositoryRoot string
	WorktreeRoot   string
	// AllowedPrimaryChanges lists repository-relative control-plane files that
	// may be updated after preflight without becoming part of the worktree base.
	AllowedPrimaryChanges []string
	Timeout               time.Duration
}

type CreateRequest struct {
	RunID      string
	WorkItemID string
	BaseRef    string
}

type Worktree struct {
	RunID      string `json:"run_id"`
	WorkItemID string `json:"work_item_id"`
	Path       string `json:"path"`
	Branch     string `json:"branch"`
	BaseRef    string `json:"base_ref"`
	BaseCommit string `json:"base_commit"`
}

type Inspection struct {
	Registered bool
	Dirty      bool
	Branch     string
}

type ChangeSummary struct {
	Status   string `json:"status"`
	DiffStat string `json:"diff_stat,omitempty"`
}

var (
	runIDPattern    = regexp.MustCompile(`^run-[a-f0-9]{32}$`)
	workItemPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	refPattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)
	commitPattern   = regexp.MustCompile(`^[a-f0-9]{40}([a-f0-9]{24})?$`)
)

func New(options Options) (*Manager, error) {
	if options.Runner == nil {
		return nil, errors.New("Git process runner is required")
	}
	repositoryRoot, err := filepath.Abs(options.RepositoryRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve repository root: %w", err)
	}
	repositoryRoot, err = filepath.EvalSymlinks(repositoryRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve repository root symlinks: %w", err)
	}
	worktreeRoot, err := filepath.Abs(options.WorktreeRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve worktree root: %w", err)
	}
	worktreeRoot, err = canonicalizeFuturePath(worktreeRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve worktree root symlinks: %w", err)
	}
	if isFilesystemRoot(worktreeRoot) {
		return nil, errors.New("worktree root cannot be a filesystem root")
	}
	if containsPath(repositoryRoot, worktreeRoot) || containsPath(worktreeRoot, repositoryRoot) {
		return nil, errors.New("repository and worktree roots must not contain one another")
	}
	binary := options.GitBinary
	if binary == "" {
		binary = "git"
	}
	timeout := options.Timeout
	if timeout == 0 {
		timeout = defaultTimeout
	}
	allowedPrimaryChanges := make(map[string]struct{}, len(options.AllowedPrimaryChanges))
	for _, path := range options.AllowedPrimaryChanges {
		clean := filepath.Clean(path)
		if filepath.IsAbs(path) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("allowed primary change %q must be a repository-relative file path", path)
		}
		allowedPrimaryChanges[filepath.ToSlash(clean)] = struct{}{}
	}
	return &Manager{
		runner:                options.Runner,
		gitBinary:             binary,
		repositoryRoot:        repositoryRoot,
		worktreeRoot:          worktreeRoot,
		allowedPrimaryChanges: allowedPrimaryChanges,
		timeout:               timeout,
	}, nil
}

func (m *Manager) Create(ctx context.Context, request CreateRequest) (Worktree, error) {
	if err := validateCreateRequest(request); err != nil {
		return Worktree{}, err
	}
	if err := m.ValidateReady(ctx); err != nil {
		return Worktree{}, err
	}
	baseResult, err := m.run(ctx, "-C", m.repositoryRoot, "rev-parse", "--verify", request.BaseRef+"^{commit}")
	if err != nil {
		return Worktree{}, err
	}
	if baseResult.Status != execution.ProcessSucceeded {
		return Worktree{}, fmt.Errorf("resolve base ref %s failed with exit code %d: %s", request.BaseRef, baseResult.ExitCode, strings.TrimSpace(baseResult.Stderr))
	}
	baseCommit := strings.TrimSpace(baseResult.Stdout)
	if !commitPattern.MatchString(baseCommit) {
		return Worktree{}, fmt.Errorf("resolved base commit %q is invalid", baseCommit)
	}
	if err := os.MkdirAll(m.worktreeRoot, 0o700); err != nil {
		return Worktree{}, fmt.Errorf("create worktree root: %w", err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(m.worktreeRoot)
	if err != nil {
		return Worktree{}, fmt.Errorf("resolve worktree root symlinks: %w", err)
	}
	if resolvedRoot != m.worktreeRoot {
		return Worktree{}, errors.New("worktree root must not be a symlink")
	}

	branch := branchName(request.WorkItemID, request.RunID)
	path := filepath.Join(m.worktreeRoot, worktreeDirectoryName(request.WorkItemID, request.RunID))
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return Worktree{}, fmt.Errorf("worktree path already exists: %s", path)
		}
		return Worktree{}, fmt.Errorf("inspect worktree path: %w", err)
	}
	branchResult, err := m.run(ctx, "-C", m.repositoryRoot, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	if err != nil {
		return Worktree{}, err
	}
	if branchResult.Status == execution.ProcessSucceeded {
		return Worktree{}, fmt.Errorf("branch already exists: %s", branch)
	}
	if branchResult.ExitCode != 1 {
		return Worktree{}, fmt.Errorf("check branch %s failed with exit code %d: %s", branch, branchResult.ExitCode, strings.TrimSpace(branchResult.Stderr))
	}

	result, err := m.run(ctx, "-C", m.repositoryRoot, "worktree", "add", "-b", branch, path, request.BaseRef)
	if err != nil {
		return Worktree{}, err
	}
	if result.Status != execution.ProcessSucceeded {
		return Worktree{}, fmt.Errorf("create worktree failed with exit code %d: %s", result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	worktree := Worktree{RunID: request.RunID, WorkItemID: request.WorkItemID, Path: path, Branch: branch, BaseRef: request.BaseRef, BaseCommit: baseCommit}
	inspection, err := m.Inspect(ctx, worktree)
	if err != nil {
		return Worktree{}, fmt.Errorf("verify created worktree: %w", err)
	}
	if !inspection.Registered || inspection.Branch != branch {
		return Worktree{}, errors.New("created worktree is not registered with the expected branch")
	}
	return worktree, nil
}

func (m *Manager) ValidateReady(ctx context.Context) error {
	if err := m.validateRepository(ctx); err != nil {
		return err
	}
	unexpected, err := m.unexpectedPrimaryChanges(ctx)
	if err != nil {
		return err
	}
	if len(unexpected) > 0 {
		return fmt.Errorf("primary repository has uncommitted changes: %s", strings.Join(unexpected, ", "))
	}
	return nil
}

func (m *Manager) unexpectedPrimaryChanges(ctx context.Context) ([]string, error) {
	result, err := m.run(ctx, "-C", m.repositoryRoot, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	if result.Status != execution.ProcessSucceeded {
		return nil, fmt.Errorf("inspect primary Git status failed with exit code %d: %s", result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	var unexpected []string
	for _, line := range strings.Split(strings.TrimSuffix(result.Stdout, "\n"), "\n") {
		if line == "" {
			continue
		}
		if len(line) < 4 || strings.Contains(line, " -> ") {
			unexpected = append(unexpected, line)
			continue
		}
		path := filepath.ToSlash(line[3:])
		if _, allowed := m.allowedPrimaryChanges[path]; !allowed {
			unexpected = append(unexpected, path)
		}
	}
	return unexpected, nil
}

func (m *Manager) SummarizeChanges(ctx context.Context, worktree Worktree) (ChangeSummary, error) {
	path, err := m.validateOwnedPath(worktree)
	if err != nil {
		return ChangeSummary{}, err
	}
	registered, branch, err := m.registeredWorktree(ctx, path)
	if err != nil {
		return ChangeSummary{}, err
	}
	if !registered || branch != worktree.Branch {
		return ChangeSummary{}, errors.New("worktree is not registered with the expected branch")
	}
	head, err := m.run(ctx, "-C", path, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return ChangeSummary{}, err
	}
	if head.Status != execution.ProcessSucceeded {
		return ChangeSummary{}, fmt.Errorf("resolve worktree HEAD failed with exit code %d: %s", head.ExitCode, strings.TrimSpace(head.Stderr))
	}
	if strings.TrimSpace(head.Stdout) != worktree.BaseCommit {
		return ChangeSummary{}, errors.New("developer changed worktree HEAD; Git commits are owned by the harness")
	}
	status, err := m.run(ctx, "-C", path, "status", "--short", "--untracked-files=all")
	if err != nil {
		return ChangeSummary{}, err
	}
	if status.Status != execution.ProcessSucceeded {
		return ChangeSummary{}, fmt.Errorf("summarize worktree status failed with exit code %d: %s", status.ExitCode, strings.TrimSpace(status.Stderr))
	}
	diffStat, err := m.run(ctx, "-C", path, "diff", "--stat", "--no-ext-diff", worktree.BaseCommit, "--")
	if err != nil {
		return ChangeSummary{}, err
	}
	if diffStat.Status != execution.ProcessSucceeded {
		return ChangeSummary{}, fmt.Errorf("summarize worktree diff failed with exit code %d: %s", diffStat.ExitCode, strings.TrimSpace(diffStat.Stderr))
	}
	return ChangeSummary{
		Status:   strings.TrimSpace(status.Stdout),
		DiffStat: strings.TrimSpace(diffStat.Stdout),
	}, nil
}

func (m *Manager) Inspect(ctx context.Context, worktree Worktree) (Inspection, error) {
	path, err := m.validateOwnedPath(worktree)
	if err != nil {
		return Inspection{}, err
	}
	registered, branch, err := m.registeredWorktree(ctx, path)
	if err != nil {
		return Inspection{}, err
	}
	inspection := Inspection{Registered: registered, Branch: branch}
	if registered {
		inspection.Dirty, err = m.isDirty(ctx, path)
		if err != nil {
			return Inspection{}, err
		}
	}
	return inspection, nil
}

func (m *Manager) CleanupIntegrated(ctx context.Context, worktree Worktree, integratedInto string) error {
	if err := validateRef(integratedInto); err != nil {
		return fmt.Errorf("invalid integration target: %w", err)
	}
	path, err := m.validateOwnedPath(worktree)
	if err != nil {
		return err
	}
	inspection, err := m.Inspect(ctx, worktree)
	if err != nil {
		return err
	}
	if !inspection.Registered {
		return errors.New("worktree is not registered")
	}
	if inspection.Branch != worktree.Branch {
		return fmt.Errorf("worktree branch %q does not match recorded branch %q", inspection.Branch, worktree.Branch)
	}
	if inspection.Dirty {
		return errors.New("refusing to remove a dirty worktree")
	}
	ancestor, err := m.run(ctx, "-C", m.repositoryRoot, "merge-base", "--is-ancestor", worktree.Branch, integratedInto)
	if err != nil {
		return err
	}
	if ancestor.Status != execution.ProcessSucceeded {
		return fmt.Errorf("branch %s is not integrated into %s", worktree.Branch, integratedInto)
	}
	removed, err := m.run(ctx, "-C", m.repositoryRoot, "worktree", "remove", path)
	if err != nil {
		return err
	}
	if removed.Status != execution.ProcessSucceeded {
		return fmt.Errorf("remove worktree failed with exit code %d: %s", removed.ExitCode, strings.TrimSpace(removed.Stderr))
	}
	deleted, err := m.run(ctx, "-C", m.repositoryRoot, "branch", "-d", worktree.Branch)
	if err != nil {
		return err
	}
	if deleted.Status != execution.ProcessSucceeded {
		return fmt.Errorf("delete integrated branch failed with exit code %d: %s", deleted.ExitCode, strings.TrimSpace(deleted.Stderr))
	}
	return nil
}

func (m *Manager) validateRepository(ctx context.Context) error {
	result, err := m.run(ctx, "-C", m.repositoryRoot, "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	if result.Status != execution.ProcessSucceeded {
		return fmt.Errorf("repository validation failed: %s", strings.TrimSpace(result.Stderr))
	}
	topLevel, err := filepath.EvalSymlinks(strings.TrimSpace(result.Stdout))
	if err != nil {
		return fmt.Errorf("resolve Git top-level path: %w", err)
	}
	if topLevel != m.repositoryRoot {
		return fmt.Errorf("configured repository %s is inside Git repository %s", m.repositoryRoot, topLevel)
	}
	return nil
}

func (m *Manager) isDirty(ctx context.Context, path string) (bool, error) {
	result, err := m.run(ctx, "-C", path, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return false, err
	}
	if result.Status != execution.ProcessSucceeded {
		return false, fmt.Errorf("inspect Git status failed with exit code %d: %s", result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	return strings.TrimSpace(result.Stdout) != "", nil
}

func (m *Manager) registeredWorktree(ctx context.Context, path string) (bool, string, error) {
	result, err := m.run(ctx, "-C", m.repositoryRoot, "worktree", "list", "--porcelain")
	if err != nil {
		return false, "", err
	}
	if result.Status != execution.ProcessSucceeded {
		return false, "", fmt.Errorf("list worktrees failed with exit code %d: %s", result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	var currentPath string
	var currentBranch string
	flush := func() (bool, string) {
		if currentPath == path {
			return true, currentBranch
		}
		return false, ""
	}
	for _, line := range strings.Split(result.Stdout, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			if found, branch := flush(); found {
				return true, branch, nil
			}
			currentPath = strings.TrimPrefix(line, "worktree ")
			currentBranch = ""
		case strings.HasPrefix(line, "branch refs/heads/"):
			currentBranch = strings.TrimPrefix(line, "branch refs/heads/")
		case line == "":
			if found, branch := flush(); found {
				return true, branch, nil
			}
			currentPath = ""
			currentBranch = ""
		}
	}
	if found, branch := flush(); found {
		return true, branch, nil
	}
	return false, "", nil
}

func (m *Manager) validateOwnedPath(worktree Worktree) (string, error) {
	if !runIDPattern.MatchString(worktree.RunID) || !workItemPattern.MatchString(worktree.WorkItemID) {
		return "", errors.New("worktree ownership identifiers are invalid")
	}
	if !commitPattern.MatchString(worktree.BaseCommit) {
		return "", errors.New("worktree base commit is invalid")
	}
	expectedBranch := branchName(worktree.WorkItemID, worktree.RunID)
	if worktree.Branch != expectedBranch {
		return "", fmt.Errorf("worktree branch %q does not match owned branch %q", worktree.Branch, expectedBranch)
	}
	expectedPath := filepath.Join(m.worktreeRoot, worktreeDirectoryName(worktree.WorkItemID, worktree.RunID))
	path, err := filepath.Abs(worktree.Path)
	if err != nil {
		return "", fmt.Errorf("resolve worktree path: %w", err)
	}
	if filepath.Clean(path) != expectedPath {
		return "", fmt.Errorf("worktree path %s does not match owned path %s", path, expectedPath)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("inspect worktree path: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("worktree path must not be a symlink")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve worktree path symlinks: %w", err)
	}
	if resolved != expectedPath {
		return "", errors.New("worktree path resolves outside its owned location")
	}
	return path, nil
}

func (m *Manager) run(ctx context.Context, args ...string) (execution.ProcessResult, error) {
	result, err := m.runner.Run(ctx, execution.Command{
		Name:    m.gitBinary,
		Args:    args,
		Timeout: m.timeout,
	}, nil)
	if err != nil {
		return execution.ProcessResult{}, fmt.Errorf("run Git command: %w", err)
	}
	return result, nil
}

func validateCreateRequest(request CreateRequest) error {
	if !runIDPattern.MatchString(request.RunID) {
		return errors.New("run id is invalid")
	}
	if !workItemPattern.MatchString(request.WorkItemID) {
		return errors.New("work item id is invalid")
	}
	return validateRef(request.BaseRef)
}

func validateRef(value string) error {
	if !refPattern.MatchString(value) || strings.Contains(value, "..") || strings.Contains(value, "//") || strings.HasSuffix(value, "/") {
		return fmt.Errorf("Git ref %q is invalid", value)
	}
	return nil
}

func branchName(workItemID, runID string) string {
	item := strings.ToLower(strings.ReplaceAll(workItemID, ".", "-"))
	return "yoyodyne/" + item + "/" + strings.TrimPrefix(runID, "run-")[:8]
}

func worktreeDirectoryName(workItemID, runID string) string {
	item := strings.ToLower(strings.ReplaceAll(workItemID, ".", "-"))
	return item + "-" + strings.TrimPrefix(runID, "run-")[:8]
}

func containsPath(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func isFilesystemRoot(path string) bool {
	return filepath.Dir(path) == path
}

func canonicalizeFuturePath(path string) (string, error) {
	path = filepath.Clean(path)
	current := path
	var missing []string
	for {
		_, err := os.Lstat(current)
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("no existing ancestor for %s", path)
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
	resolved, err := filepath.EvalSymlinks(current)
	if err != nil {
		return "", err
	}
	for index := len(missing) - 1; index >= 0; index-- {
		resolved = filepath.Join(resolved, missing[index])
	}
	return resolved, nil
}
