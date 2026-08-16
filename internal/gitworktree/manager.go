package gitworktree

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"yoyodyne/internal/execution"
)

const (
	defaultTimeout = 30 * time.Second
	// defaultRemote is the remote publishing pushes to when nothing names
	// another.
	defaultRemote = "origin"
	// pushTimeout is longer than the local Git timeout because a push talks to
	// another machine. It is still bounded: a hung network must not hold a run
	// open indefinitely.
	pushTimeout = 5 * time.Minute
)

type Manager struct {
	runner                execution.ProcessRunner
	gitBinary             string
	repositoryRoot        string
	worktreeRoot          string
	remote                string
	allowedPrimaryChanges map[string]struct{}
	timeout               time.Duration
}

type Options struct {
	Runner         execution.ProcessRunner
	GitBinary      string
	RepositoryRoot string
	WorktreeRoot   string
	// Remote names the Git remote publishing pushes to. It defaults to origin,
	// and a repository that has no remote by that name is never pushed to.
	Remote string
	// AllowedPrimaryChanges lists repository-relative control-plane files that
	// may be updated after preflight without becoming part of the worktree base.
	AllowedPrimaryChanges []string
	Timeout               time.Duration
}

type CreateRequest struct {
	RunID      string
	WorkItemID string
	BaseRef    string
	// TargetBranch names the local branch the finished work is meant to be
	// promoted into. It is recorded on the worktree and never changes
	// afterwards. When it is empty the worktree can be created and inspected
	// but never integrated.
	TargetBranch string
}

type Worktree struct {
	RunID      string `json:"run_id"`
	WorkItemID string `json:"work_item_id"`
	Path       string `json:"path"`
	Branch     string `json:"branch"`
	BaseRef    string `json:"base_ref"`
	BaseCommit string `json:"base_commit"`
	// TargetBranch is the immutable integration target recorded at creation.
	TargetBranch string `json:"target_branch,omitempty"`
	// HarnessCommit is the last commit the harness itself made on this branch,
	// empty until it makes one. It is what permits the worktree's HEAD to have
	// moved at all: publishing needs a commit before it can push, and naming the
	// permitted commit in advance is what keeps "Git commits are owned by the
	// harness" a fact about recorded hashes rather than about what a commit looks
	// like. A caller carries it forward from the publication that produced it.
	HarnessCommit string `json:"harness_commit,omitempty"`
}

type Inspection struct {
	Registered bool
	Dirty      bool
	Branch     string
}

// Observation is what a run's recorded artifacts actually look like now. It is
// read-only and every field is optional, because the question it answers is
// asked exactly when a process died partway through creating or removing them:
// an absent worktree, a deleted branch, and a target that has moved on are all
// observations rather than failures.
type Observation struct {
	WorktreeRegistered bool   `json:"worktree_registered"`
	WorktreePresent    bool   `json:"worktree_present"`
	WorktreeDirty      bool   `json:"worktree_dirty"`
	WorktreeBranch     string `json:"worktree_branch,omitempty"`
	WorktreeHead       string `json:"worktree_head,omitempty"`
	BranchExists       bool   `json:"branch_exists"`
	BranchCommit       string `json:"branch_commit,omitempty"`
	TargetExists       bool   `json:"target_exists"`
	TargetCommit       string `json:"target_commit,omitempty"`
	// BranchIntegrated reports that the run branch carries a commit past its
	// base and that commit is contained in the recorded target. It is the only
	// evidence that an integration nobody managed to write down actually
	// promoted the work, so it is deliberately not satisfied by the base commit
	// the target already contains by construction.
	BranchIntegrated bool `json:"branch_integrated"`
}

// Integration reports exactly which commits an integration produced, so a
// caller can record the promoted work and prove the target moved where it
// expected before removing anything.
type Integration struct {
	Branch               string `json:"branch"`
	TargetBranch         string `json:"target_branch"`
	SourceCommit         string `json:"source_commit"`
	TargetCommit         string `json:"target_commit"`
	PreviousTargetCommit string `json:"previous_target_commit"`
}

// CleanupRequest names exactly which artifacts may be removed and the evidence
// that permits removing them. Carrying the source commit rather than only a
// branch name is what makes cleanup resumable: the proof that the work was
// integrated survives the branch that carried it.
type CleanupRequest struct {
	Worktree     Worktree
	TargetBranch string
	SourceCommit string
}

// Cleanup reports what is absent after a cleanup attempt, whether this attempt
// removed it or a previous one did. Reporting the two artifacts separately is
// what lets a caller describe a partial cleanup truthfully.
type Cleanup struct {
	WorktreeRemoved bool `json:"worktree_removed"`
	BranchRemoved   bool `json:"branch_removed"`
}

// Complete reports whether nothing is left to clean up.
func (c Cleanup) Complete() bool {
	return c.WorktreeRemoved && c.BranchRemoved
}

// Identity of the harness-owned integration commit. It is deliberately not the
// developer's identity: the harness, not the agent, authors Git history.
const (
	harnessCommitAuthorName  = "Yoyodyne Harness"
	harnessCommitAuthorEmail = "harness@yoyodyne.invalid"
)

var (
	// ErrNoChanges reports that there is nothing to integrate. It is a refusal,
	// not a failure: an empty commit would claim work that does not exist.
	ErrNoChanges = errors.New("worktree has no changes to integrate")
	// ErrTargetDrift reports that the target branch moved away from the base the
	// work was written against, so the change must be reconciled rather than
	// merged.
	ErrTargetDrift = errors.New("integration target moved away from the recorded base commit")
	// ErrNotFastForward reports that the target could not be advanced by a
	// fast-forward. The harness never resolves this by forcing or resetting.
	ErrNotFastForward = errors.New("integration target cannot be fast-forwarded")
)

type ChangeSummary struct {
	Status   string `json:"status"`
	DiffStat string `json:"diff_stat,omitempty"`
}

// Default bounds for the unified change representation handed to a reviewer.
const (
	DefaultMaxDiffBytes     = 256 << 10
	DefaultMaxDiffFileBytes = 64 << 10
	DefaultMaxDiffFiles     = 200
)

// DiffLimits bounds how much of a worktree's change a caller is willing to
// carry. Zero fields fall back to the defaults above.
type DiffLimits struct {
	// MaxTotalBytes bounds the complete tracked-and-untracked patch.
	MaxTotalBytes int
	// MaxFileBytes bounds each untracked file before Git renders its patch.
	// Tracked changes remain bounded by MaxTotalBytes.
	MaxFileBytes int
	// MaxFiles bounds separately rendered untracked files. Tracked changes are
	// already rendered together and remain bounded by MaxTotalBytes.
	MaxFiles int
}

// ChangeDiff is a bounded unified view of everything a developer changed in a
// worktree. Untracked files are diffed against /dev/null so they appear in the
// same patch as tracked edits without staging them or otherwise mutating the
// worktree. Truncated reports that the bounds dropped part of the change, so a
// caller never mistakes a clamped patch for the whole story.
type ChangeDiff struct {
	Status         string   `json:"status"`
	DiffStat       string   `json:"diff_stat,omitempty"`
	Patch          string   `json:"patch,omitempty"`
	UntrackedFiles []string `json:"untracked_files,omitempty"`
	OmittedFiles   []string `json:"omitted_files,omitempty"`
	Truncated      bool     `json:"truncated"`
}

var (
	runIDPattern    = regexp.MustCompile(`^run-[a-f0-9]{32}$`)
	workItemPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	refPattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)
	commitPattern   = regexp.MustCompile(`^[a-f0-9]{40}([a-f0-9]{24})?$`)
	remotePattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
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
	remote := options.Remote
	if remote == "" {
		remote = defaultRemote
	}
	if !remotePattern.MatchString(remote) {
		return nil, fmt.Errorf("remote %q must be a plain Git remote name", remote)
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
		remote:                remote,
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
	// An integratable worktree must start from exactly the branch it will be
	// promoted into, so the recorded pair is coherent from the moment it is
	// written and integration can insist on that equality later.
	if request.TargetBranch != "" {
		targetCommit, err := m.resolveBranchCommit(ctx, request.TargetBranch)
		if err != nil {
			return Worktree{}, fmt.Errorf("resolve integration target: %w", err)
		}
		if targetCommit != baseCommit {
			return Worktree{}, fmt.Errorf("integration target %s is at %s, which is not the base commit %s", request.TargetBranch, targetCommit, baseCommit)
		}
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

	result, err := m.run(ctx, "-C", m.repositoryRoot, "worktree", "add", "-b", branch, path, baseCommit)
	if err != nil {
		return Worktree{}, err
	}
	if result.Status != execution.ProcessSucceeded {
		return Worktree{}, fmt.Errorf("create worktree failed with exit code %d: %s", result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	worktree := Worktree{
		RunID:        request.RunID,
		WorkItemID:   request.WorkItemID,
		Path:         path,
		Branch:       branch,
		BaseRef:      request.BaseRef,
		BaseCommit:   baseCommit,
		TargetBranch: request.TargetBranch,
	}
	inspection, err := m.Inspect(ctx, worktree)
	if err != nil {
		return worktree, fmt.Errorf("verify created worktree: %w", err)
	}
	if !inspection.Registered || inspection.Branch != branch {
		return worktree, errors.New("created worktree is not registered with the expected branch")
	}
	return worktree, nil
}

// CurrentBranch reports the local branch the primary checkout is on, which is
// the branch finished work is promoted back into. A detached HEAD names no
// branch, so it is refused rather than guessed at.
func (m *Manager) CurrentBranch(ctx context.Context) (string, error) {
	result, err := m.run(ctx, "-C", m.repositoryRoot, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return "", err
	}
	if result.Status != execution.ProcessSucceeded {
		if result.ExitCode == 1 {
			return "", errors.New("primary checkout has no current branch; HEAD is detached")
		}
		return "", fmt.Errorf("resolve current branch failed with exit code %d: %s", result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	branch := strings.TrimSpace(result.Stdout)
	if err := validateTargetBranch(branch); err != nil {
		return "", err
	}
	return branch, nil
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
	path, _, err := m.verifyOwnedHead(ctx, worktree)
	if err != nil {
		return ChangeSummary{}, err
	}
	return m.summarize(ctx, path, worktree)
}

// UnifiedChanges reports the actual change a developer produced, tracked and
// untracked, as one bounded patch. It only reads: the untracked half is built
// from `git diff --no-index` rather than from a staged index, so inspecting a
// worktree never changes what the developer left behind.
func (m *Manager) UnifiedChanges(ctx context.Context, worktree Worktree, limits DiffLimits) (ChangeDiff, error) {
	limits, err := limits.resolve()
	if err != nil {
		return ChangeDiff{}, err
	}
	path, _, err := m.verifyOwnedHead(ctx, worktree)
	if err != nil {
		return ChangeDiff{}, err
	}
	summary, err := m.summarize(ctx, path, worktree)
	if err != nil {
		return ChangeDiff{}, err
	}
	changes := ChangeDiff{Status: summary.Status, DiffStat: summary.DiffStat}

	tracked, err := m.run(ctx, "-C", path, "diff", "--no-ext-diff", "--patch", worktree.BaseCommit, "--")
	if err != nil {
		return ChangeDiff{}, err
	}
	if tracked.Status != execution.ProcessSucceeded {
		return ChangeDiff{}, fmt.Errorf("diff tracked worktree changes failed with exit code %d: %s", tracked.ExitCode, strings.TrimSpace(tracked.Stderr))
	}
	untracked, err := m.untrackedFiles(ctx, path)
	if err != nil {
		return ChangeDiff{}, err
	}

	var patch strings.Builder
	remaining := limits.MaxTotalBytes
	clamped, truncated := clampToWholeLines(tracked.Stdout, remaining)
	patch.WriteString(clamped)
	remaining -= len(clamped)
	changes.Truncated = truncated || containsBinaryDiff(tracked.Stdout)

	for _, relative := range untracked {
		if len(changes.UntrackedFiles)+len(changes.OmittedFiles) >= limits.MaxFiles {
			changes.OmittedFiles = append(changes.OmittedFiles, relative)
			changes.Truncated = true
			continue
		}
		included, filePatch, err := m.untrackedPatch(ctx, path, relative, limits.MaxFileBytes)
		if err != nil {
			return ChangeDiff{}, err
		}
		if !included || containsBinaryDiff(filePatch) || len(filePatch) > remaining {
			changes.OmittedFiles = append(changes.OmittedFiles, relative)
			changes.Truncated = true
			continue
		}
		patch.WriteString(filePatch)
		remaining -= len(filePatch)
		changes.UntrackedFiles = append(changes.UntrackedFiles, relative)
	}
	changes.Patch = patch.String()
	return changes, nil
}

// verifyOwnedHead confirms the worktree is the one the harness created and that
// its HEAD is exactly where the harness left it: the recorded base commit, or
// the one commit the harness itself made and recorded. It reports the path and
// that HEAD.
//
// Publishing is why this is not simply "HEAD never moved". A branch cannot be
// pushed before it carries a commit, so a published run has a harness commit on
// it well before integration. What makes that safe is that the permitted commit
// is named in advance by durable run state rather than recognized by how it
// looks: an agent with a shell in the worktree can imitate the harness identity,
// but it cannot make its commit be the hash the harness already wrote down.
func (m *Manager) verifyOwnedHead(ctx context.Context, worktree Worktree) (string, string, error) {
	path, err := m.validateOwnedPath(worktree)
	if err != nil {
		return "", "", err
	}
	if worktree.HarnessCommit != "" && !commitPattern.MatchString(worktree.HarnessCommit) {
		return "", "", errors.New("recorded harness commit is invalid")
	}
	registered, branch, err := m.registeredWorktree(ctx, path)
	if err != nil {
		return "", "", err
	}
	if !registered || branch != worktree.Branch {
		return "", "", errors.New("worktree is not registered with the expected branch")
	}
	head, err := m.run(ctx, "-C", path, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return "", "", err
	}
	if head.Status != execution.ProcessSucceeded {
		return "", "", fmt.Errorf("resolve worktree HEAD failed with exit code %d: %s", head.ExitCode, strings.TrimSpace(head.Stderr))
	}
	commit := strings.TrimSpace(head.Stdout)
	// Only one HEAD is permitted: the commit the harness recorded when it made
	// it, or the base when it has made none. A run that has published nothing
	// therefore permits no movement at all, which is the pre-publishing rule
	// unchanged.
	expected := worktree.BaseCommit
	if worktree.HarnessCommit != "" {
		expected = worktree.HarnessCommit
	}
	if commit != expected {
		return "", "", fmt.Errorf("worktree HEAD is %s, want the commit the harness recorded (%s); Git commits are owned by the harness", commit, expected)
	}
	if commit == worktree.BaseCommit {
		return path, commit, nil
	}
	if err := m.verifyHarnessHistory(ctx, path, worktree.BaseCommit, commit); err != nil {
		return "", "", err
	}
	return path, commit, nil
}

// verifyHarnessHistory is the secondary assertion on a HEAD that already
// matched the commit durable state named: the commit descends from the recorded
// base, and it carries the harness identity. Neither check is what keeps an
// agent's commit out — the recorded hash does that — but a recorded commit that
// fails either one means the repository is not what the harness recorded, which
// is worth refusing rather than promoting.
func (m *Manager) verifyHarnessHistory(ctx context.Context, path, baseCommit, headCommit string) error {
	ancestor, err := m.run(ctx, "-C", path, "merge-base", "--is-ancestor", baseCommit, headCommit)
	if err != nil {
		return err
	}
	if ancestor.Status != execution.ProcessSucceeded {
		return errors.New("recorded harness commit does not descend from the worktree base; Git commits are owned by the harness")
	}
	identities, err := m.run(ctx, "-C", path, "log", "--format=%ae %ce", baseCommit+".."+headCommit)
	if err != nil {
		return err
	}
	if identities.Status != execution.ProcessSucceeded {
		return fmt.Errorf("inspect worktree commit identities failed with exit code %d: %s", identities.ExitCode, strings.TrimSpace(identities.Stderr))
	}
	expected := harnessCommitAuthorEmail + " " + harnessCommitAuthorEmail
	for _, line := range strings.Split(strings.TrimSuffix(identities.Stdout, "\n"), "\n") {
		if line == "" {
			continue
		}
		if line != expected {
			return errors.New("recorded harness commit does not carry the harness identity; Git commits are owned by the harness")
		}
	}
	return nil
}

// summarize describes the change against the worktree's recorded base commit.
// Every part of it is base-relative, including the file list: publishing
// commits each developer attempt, so a summary built from the working tree's
// uncommitted status would describe a published change as no change at all,
// printed beside a diff that plainly shows one. What a reviewer is handed has
// to be the change, not the part of it that happens not to be committed yet.
func (m *Manager) summarize(ctx context.Context, path string, worktree Worktree) (ChangeSummary, error) {
	names, err := m.run(ctx, "-C", path, "diff", "--name-status", "--no-ext-diff", worktree.BaseCommit, "--")
	if err != nil {
		return ChangeSummary{}, err
	}
	if names.Status != execution.ProcessSucceeded {
		return ChangeSummary{}, fmt.Errorf("summarize worktree status failed with exit code %d: %s", names.ExitCode, strings.TrimSpace(names.Stderr))
	}
	// Untracked files are not part of a diff against the base until something
	// adds them, so they are listed alongside it in the shape Git reports them.
	untracked, err := m.untrackedFiles(ctx, path)
	if err != nil {
		return ChangeSummary{}, err
	}
	diffStat, err := m.run(ctx, "-C", path, "diff", "--stat", "--no-ext-diff", worktree.BaseCommit, "--")
	if err != nil {
		return ChangeSummary{}, err
	}
	if diffStat.Status != execution.ProcessSucceeded {
		return ChangeSummary{}, fmt.Errorf("summarize worktree diff failed with exit code %d: %s", diffStat.ExitCode, strings.TrimSpace(diffStat.Stderr))
	}
	return ChangeSummary{
		Status:   renderChangedNames(names.Stdout, untracked),
		DiffStat: strings.TrimSpace(diffStat.Stdout),
	}, nil
}

// renderChangedNames turns Git's tab-separated name-status output and the
// untracked file list into one short-status-shaped listing, so a reader sees
// the same `<code> <path>` lines whether or not the change has been committed.
func renderChangedNames(nameStatus string, untracked []string) string {
	var lines []string
	for _, line := range strings.Split(strings.TrimSuffix(nameStatus, "\n"), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		switch len(fields) {
		case 0, 1:
			lines = append(lines, line)
		case 2:
			lines = append(lines, fields[0]+" "+fields[1])
		default:
			// A rename or copy names both paths, and which file became which is
			// exactly what a reviewer needs from a line like this.
			lines = append(lines, fields[0]+" "+fields[1]+" -> "+strings.Join(fields[2:], " "))
		}
	}
	for _, file := range untracked {
		lines = append(lines, "?? "+file)
	}
	return strings.Join(lines, "\n")
}

// untrackedFiles lists ignored-file-free untracked paths in a stable order.
// The NUL-separated form is used so paths containing spaces, quotes, or
// newlines survive without Git's quoting.
func (m *Manager) untrackedFiles(ctx context.Context, path string) ([]string, error) {
	result, err := m.run(ctx, "-C", path, "ls-files", "-z", "--others", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	if result.Status != execution.ProcessSucceeded {
		return nil, fmt.Errorf("list untracked worktree files failed with exit code %d: %s", result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	var files []string
	for _, entry := range strings.Split(strings.TrimSuffix(result.Stdout, "\n"), "\x00") {
		if entry != "" {
			files = append(files, entry)
		}
	}
	sort.Strings(files)
	return files, nil
}

// untrackedPatch renders one untracked file as a new-file patch. It reports
// included=false when the path is unsafe, is not a regular file, or is larger
// than the per-file bound, leaving the caller to record it as omitted.
func (m *Manager) untrackedPatch(ctx context.Context, path, relative string, maxFileBytes int) (bool, string, error) {
	clean := filepath.Clean(relative)
	if filepath.IsAbs(relative) || clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return false, "", nil
	}
	info, err := os.Lstat(filepath.Join(path, filepath.FromSlash(clean)))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, "", nil
		}
		return false, "", fmt.Errorf("inspect untracked file: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > int64(maxFileBytes) {
		return false, "", nil
	}
	result, err := m.run(ctx, "-C", path, "diff", "--no-index", "--no-ext-diff", "--patch", "--", os.DevNull, clean)
	if err != nil {
		return false, "", err
	}
	// `git diff` exits 1 to report differences, which is the normal outcome
	// here because every untracked file differs from /dev/null.
	if result.Status != execution.ProcessSucceeded && result.ExitCode != 1 {
		return false, "", fmt.Errorf("diff untracked worktree file failed with exit code %d: %s", result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	return true, result.Stdout, nil
}

func (l DiffLimits) resolve() (DiffLimits, error) {
	if l.MaxTotalBytes == 0 {
		l.MaxTotalBytes = DefaultMaxDiffBytes
	}
	if l.MaxFileBytes == 0 {
		l.MaxFileBytes = DefaultMaxDiffFileBytes
	}
	if l.MaxFiles == 0 {
		l.MaxFiles = DefaultMaxDiffFiles
	}
	if l.MaxTotalBytes < 0 || l.MaxFileBytes < 0 || l.MaxFiles < 0 {
		return DiffLimits{}, errors.New("diff limits cannot be negative")
	}
	return l, nil
}

// clampToWholeLines keeps the longest whole-line prefix that fits in limit so a
// bounded patch never ends mid-line and read as a different change.
func clampToWholeLines(text string, limit int) (string, bool) {
	if len(text) <= limit {
		return text, false
	}
	cut := strings.LastIndexByte(text[:limit], '\n')
	if cut < 0 {
		return "", true
	}
	return text[:cut+1], true
}

// containsBinaryDiff detects Git's metadata-only representation of a binary
// change. Such a patch proves that a file changed but does not carry content an
// independent reviewer can evaluate, so the caller must treat it as truncated.
func containsBinaryDiff(patch string) bool {
	for _, line := range strings.Split(patch, "\n") {
		if strings.HasPrefix(line, "Binary files ") && strings.HasSuffix(line, " differ") {
			return true
		}
	}
	return false
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

// Observe reports the current state of one run's recorded artifacts without
// changing any of them. Reconciliation needs this because durable run state
// says what a process intended and only the repository says what it achieved.
// Ownership is still proven first: the path is derived from the recorded
// identifiers rather than trusted, so this never reports on a directory or a
// branch that is not this run's.
func (m *Manager) Observe(ctx context.Context, worktree Worktree) (Observation, error) {
	path, err := m.ownedPath(worktree)
	if err != nil {
		return Observation{}, err
	}
	registered, branch, err := m.registeredWorktree(ctx, path)
	if err != nil {
		return Observation{}, err
	}
	observation := Observation{WorktreeRegistered: registered, WorktreeBranch: branch}
	info, statErr := os.Lstat(path)
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return Observation{}, fmt.Errorf("inspect worktree path: %w", statErr)
	}
	observation.WorktreePresent = statErr == nil
	if observation.WorktreePresent && info.Mode()&os.ModeSymlink != 0 {
		return Observation{}, errors.New("worktree path must not be a symlink")
	}
	// A registration whose directory is gone has nothing left to read, so the
	// content of the checkout is only inspected while both are still there.
	if registered && observation.WorktreePresent {
		observation.WorktreeDirty, err = m.isDirty(ctx, path)
		if err != nil {
			return Observation{}, err
		}
		head, err := m.run(ctx, "-C", path, "rev-parse", "--verify", "HEAD^{commit}")
		if err != nil {
			return Observation{}, err
		}
		if head.Status != execution.ProcessSucceeded {
			return Observation{}, fmt.Errorf("resolve worktree HEAD failed with exit code %d: %s", head.ExitCode, strings.TrimSpace(head.Stderr))
		}
		observation.WorktreeHead = strings.TrimSpace(head.Stdout)
	}

	observation.BranchCommit, observation.BranchExists, err = m.optionalBranchCommit(ctx, worktree.Branch)
	if err != nil {
		return Observation{}, err
	}
	if worktree.TargetBranch == "" {
		return observation, nil
	}
	if err := validateTargetBranch(worktree.TargetBranch); err != nil {
		return Observation{}, err
	}
	observation.TargetCommit, observation.TargetExists, err = m.optionalBranchCommit(ctx, worktree.TargetBranch)
	if err != nil {
		return Observation{}, err
	}
	if observation.TargetExists && observation.BranchExists && observation.BranchCommit != worktree.BaseCommit {
		observation.BranchIntegrated, err = m.contains(ctx, observation.BranchCommit, worktree.TargetBranch)
		if err != nil {
			return Observation{}, err
		}
	}
	return observation, nil
}

// optionalBranchCommit resolves a branch that may legitimately be gone, which
// is the ordinary case after cleanup deleted the run's branch.
func (m *Manager) optionalBranchCommit(ctx context.Context, branch string) (string, bool, error) {
	existing, err := m.run(ctx, "-C", m.repositoryRoot, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	if err != nil {
		return "", false, err
	}
	switch {
	case existing.ExitCode == 1:
		return "", false, nil
	case existing.Status != execution.ProcessSucceeded:
		return "", false, fmt.Errorf("check branch %s failed with exit code %d: %s", branch, existing.ExitCode, strings.TrimSpace(existing.Stderr))
	}
	commit, err := m.resolveBranchCommit(ctx, branch)
	if err != nil {
		return "", false, err
	}
	return commit, true, nil
}

// contains reports whether a commit is already part of a branch's history. Any
// answer other than a clean yes is reported as no, because containment is what
// permits destructive follow-up work and an unclear answer must never authorize
// it.
func (m *Manager) contains(ctx context.Context, commit, branch string) (bool, error) {
	result, err := m.run(ctx, "-C", m.repositoryRoot, "merge-base", "--is-ancestor", commit, "refs/heads/"+branch)
	if err != nil {
		return false, err
	}
	return result.Status == execution.ProcessSucceeded, nil
}

// Integrate promotes an already checked and approved worktree into its
// recorded target branch. The harness owns every Git write here: it commits
// whatever the developer left uncommitted, revalidates the target, and advances
// it with a fast-forward-only update. Nothing is forced, reset, or removed. When
// any step refuses, the worktree and its harness commits stay exactly as they
// are so the failure can be reconciled rather than reconstructed.
//
// A published run arrives here with its work already in harness commits on the
// run branch, because a branch cannot be pushed before it has one. That changes
// nothing about the promotion: the source commit is the branch tip either way,
// and the target is still only ever fast-forwarded onto it.
func (m *Manager) Integrate(ctx context.Context, worktree Worktree, message string) (Integration, error) {
	target := worktree.TargetBranch
	if err := validateTargetBranch(target); err != nil {
		return Integration{}, err
	}
	if target == worktree.Branch {
		return Integration{}, errors.New("integration target must differ from the worktree branch")
	}
	message = strings.TrimSpace(message)
	if message == "" {
		message = defaultCommitMessage(worktree)
	}

	// Ownership, registration, the expected branch, and a HEAD carrying only
	// harness commits are all revalidated here rather than trusted from run
	// state.
	path, head, err := m.verifyOwnedHead(ctx, worktree)
	if err != nil {
		return Integration{}, err
	}
	dirty, err := m.isDirty(ctx, path)
	if err != nil {
		return Integration{}, err
	}
	if !dirty && head == worktree.BaseCommit {
		return Integration{}, ErrNoChanges
	}
	if err := m.ValidateReady(ctx); err != nil {
		return Integration{}, fmt.Errorf("primary checkout is not ready for integration: %w", err)
	}
	previousTarget, err := m.resolveBranchCommit(ctx, target)
	if err != nil {
		return Integration{}, err
	}
	if previousTarget != worktree.BaseCommit {
		return Integration{}, fmt.Errorf("%w: %s is at %s, recorded base is %s", ErrTargetDrift, target, previousTarget, worktree.BaseCommit)
	}
	inPrimary, err := m.targetCheckout(ctx, target)
	if err != nil {
		return Integration{}, err
	}

	sourceCommit := head
	if dirty {
		sourceCommit, err = m.commitWorktree(ctx, path, message)
		if err != nil {
			return Integration{}, err
		}
	}
	if sourceCommit == worktree.BaseCommit {
		return Integration{}, ErrNoChanges
	}
	if err := m.fastForward(ctx, worktree.Branch, target, previousTarget, sourceCommit, inPrimary); err != nil {
		return Integration{}, err
	}
	integrated, err := m.resolveBranchCommit(ctx, target)
	if err != nil {
		return Integration{}, err
	}
	if integrated != sourceCommit {
		return Integration{}, fmt.Errorf("%w: %s is at %s after the update, want %s", ErrNotFastForward, target, integrated, sourceCommit)
	}
	return Integration{
		Branch:               worktree.Branch,
		TargetBranch:         target,
		SourceCommit:         sourceCommit,
		TargetCommit:         integrated,
		PreviousTargetCommit: previousTarget,
	}, nil
}

// commitWorktree stages tracked edits, deletions, and untracked files, then
// records one harness-owned commit. The identity, hooks, and signing are all
// pinned so the commit does not depend on whatever the developer left in the
// worktree's Git configuration.
func (m *Manager) commitWorktree(ctx context.Context, path, message string) (string, error) {
	staged, err := m.run(ctx, "-C", path, "add", "--all")
	if err != nil {
		return "", err
	}
	if staged.Status != execution.ProcessSucceeded {
		return "", fmt.Errorf("stage worktree changes failed with exit code %d: %s", staged.ExitCode, strings.TrimSpace(staged.Stderr))
	}
	pending, err := m.run(ctx, "-C", path, "diff", "--cached", "--quiet")
	if err != nil {
		return "", err
	}
	switch {
	case pending.Status == execution.ProcessSucceeded:
		return "", ErrNoChanges
	case pending.ExitCode != 1:
		return "", fmt.Errorf("inspect staged worktree changes failed with exit code %d: %s", pending.ExitCode, strings.TrimSpace(pending.Stderr))
	}
	// Command-line config disables every repository hook, including post-commit
	// and reference-transaction hooks that --no-verify does not bypass. Explicit
	// environment values take precedence over ambient GIT_* identity variables.
	committed, err := m.runWithEnvironment(ctx, harnessCommitEnvironment(), "-C", path,
		"-c", "core.hooksPath="+os.DevNull,
		"-c", "user.name="+harnessCommitAuthorName,
		"-c", "user.email="+harnessCommitAuthorEmail,
		"commit", "--no-verify", "--no-gpg-sign", "--message", message)
	if err != nil {
		return "", err
	}
	if committed.Status != execution.ProcessSucceeded {
		return "", fmt.Errorf("commit worktree changes failed with exit code %d: %s", committed.ExitCode, strings.TrimSpace(committed.Stderr))
	}
	head, err := m.run(ctx, "-C", path, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return "", err
	}
	if head.Status != execution.ProcessSucceeded {
		return "", fmt.Errorf("resolve integrated commit failed with exit code %d: %s", head.ExitCode, strings.TrimSpace(head.Stderr))
	}
	commit := strings.TrimSpace(head.Stdout)
	if !commitPattern.MatchString(commit) {
		return "", fmt.Errorf("integrated commit %q is invalid", commit)
	}
	return commit, nil
}

// fastForward advances the target branch and refuses anything else. When the
// primary checkout is on the target, a fast-forward-only merge keeps its
// working tree consistent with the moved branch; otherwise the ref is advanced
// with a compare-and-swap so a target that drifted since the earlier check
// still loses the race instead of being overwritten.
func (m *Manager) fastForward(ctx context.Context, branch, target, previousTarget, sourceCommit string, inPrimary bool) error {
	if inPrimary {
		// Merge the exact commit we just created, not the mutable source branch.
		// A concurrent ref update must never redirect approved integration work.
		merged, err := m.runWithEnvironment(ctx, os.Environ(), "-C", m.repositoryRoot,
			"-c", "core.hooksPath="+os.DevNull,
			"merge", "--ff-only", sourceCommit)
		if err != nil {
			return err
		}
		if merged.Status != execution.ProcessSucceeded {
			return fmt.Errorf("%w: merge %s from %s into %s failed with exit code %d: %s", ErrNotFastForward, sourceCommit, branch, target, merged.ExitCode, strings.TrimSpace(merged.Stderr))
		}
		return nil
	}
	updated, err := m.runWithEnvironment(ctx, os.Environ(), "-C", m.repositoryRoot,
		"-c", "core.hooksPath="+os.DevNull,
		"update-ref", "refs/heads/"+target, sourceCommit, previousTarget)
	if err != nil {
		return err
	}
	if updated.Status != execution.ProcessSucceeded {
		return fmt.Errorf("%w: update %s to %s failed with exit code %d: %s", ErrNotFastForward, target, sourceCommit, updated.ExitCode, strings.TrimSpace(updated.Stderr))
	}
	return nil
}

// targetCheckout reports whether the target branch is checked out in the
// primary repository. A target checked out in some other worktree is refused
// outright: moving it would silently invalidate that checkout's working tree.
func (m *Manager) targetCheckout(ctx context.Context, target string) (bool, error) {
	entries, err := m.listWorktrees(ctx)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if entry.branch != target {
			continue
		}
		if samePath(entry.path, m.repositoryRoot) {
			return true, nil
		}
		return false, fmt.Errorf("integration target %s is checked out in another worktree: %s", target, entry.path)
	}
	return false, nil
}

// samePath compares two checkout paths, tolerating symlinked ancestors. A
// primary checkout mistaken for a foreign one would be advanced by a bare ref
// update and left holding a working tree its branch no longer describes.
func samePath(left, right string) bool {
	if filepath.Clean(left) == filepath.Clean(right) {
		return true
	}
	resolvedLeft, err := filepath.EvalSymlinks(left)
	if err != nil {
		return false
	}
	resolvedRight, err := filepath.EvalSymlinks(right)
	if err != nil {
		return false
	}
	return resolvedLeft == resolvedRight
}

func (m *Manager) resolveBranchCommit(ctx context.Context, branch string) (string, error) {
	result, err := m.run(ctx, "-C", m.repositoryRoot, "rev-parse", "--verify", "refs/heads/"+branch+"^{commit}")
	if err != nil {
		return "", err
	}
	if result.Status != execution.ProcessSucceeded {
		return "", fmt.Errorf("resolve branch %s failed with exit code %d: %s", branch, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	commit := strings.TrimSpace(result.Stdout)
	if !commitPattern.MatchString(commit) {
		return "", fmt.Errorf("resolved commit %q for branch %s is invalid", commit, branch)
	}
	return commit, nil
}

func defaultCommitMessage(worktree Worktree) string {
	return fmt.Sprintf("yoyodyne: integrate %s\n\nRun: %s\nBranch: %s\nBase: %s\n",
		worktree.WorkItemID, worktree.RunID, worktree.Branch, worktree.BaseCommit)
}

func harnessCommitEnvironment() []string {
	return append(os.Environ(),
		"GIT_AUTHOR_NAME="+harnessCommitAuthorName,
		"GIT_AUTHOR_EMAIL="+harnessCommitAuthorEmail,
		"GIT_COMMITTER_NAME="+harnessCommitAuthorName,
		"GIT_COMMITTER_EMAIL="+harnessCommitAuthorEmail,
	)
}

// CleanupIntegrated removes the artifacts of a proven-integrated run: first the
// worktree, then its branch. Removal is two Git operations that cannot be made
// atomic, so this is written to be resumable from durable evidence rather than
// to assume it runs once. It re-derives the current state of both artifacts and
// finishes whatever remains, including when the worktree is already gone and
// only the branch is left, and it reports what is actually absent afterwards so
// a caller never has to infer it from an error.
//
// Resumability does not relax ownership. Every attempt still proves the exact
// recorded source commit reached the recorded target, and it refuses to delete
// a branch that no longer points at that commit or a directory Git does not
// manage.
func (m *Manager) CleanupIntegrated(ctx context.Context, request CleanupRequest) (Cleanup, error) {
	worktree := request.Worktree
	if err := validateTargetBranch(request.TargetBranch); err != nil {
		return Cleanup{}, err
	}
	// The request describes an integration that already happened, so it must
	// agree with what the worktree recorded at creation. Checking the two
	// independently would let a caller name a branch this worktree was never
	// aimed at and satisfy the containment proof below against that branch
	// instead of the real target.
	if worktree.TargetBranch != "" && request.TargetBranch != worktree.TargetBranch {
		return Cleanup{}, fmt.Errorf("cleanup target %q does not match the worktree's recorded target %q", request.TargetBranch, worktree.TargetBranch)
	}
	if !commitPattern.MatchString(request.SourceCommit) {
		return Cleanup{}, fmt.Errorf("integrated source commit %q is invalid", request.SourceCommit)
	}
	// The base commit is contained in the target by construction, so accepting
	// it as the integrated commit would make the containment proof vacuous:
	// it would hold for a worktree that never integrated anything.
	if request.SourceCommit == worktree.BaseCommit {
		return Cleanup{}, fmt.Errorf("integrated source commit %s is the worktree's base commit, which proves no integration", request.SourceCommit)
	}
	path, err := m.ownedPath(worktree)
	if err != nil {
		return Cleanup{}, err
	}
	// Nothing is removed until the recorded commit is proven to be in the
	// recorded target. This holds on a retry even after the branch is gone,
	// because it asks about the commit rather than the branch that carried it.
	integrated, err := m.run(ctx, "-C", m.repositoryRoot, "merge-base", "--is-ancestor", request.SourceCommit, "refs/heads/"+request.TargetBranch)
	if err != nil {
		return Cleanup{}, err
	}
	if integrated.Status != execution.ProcessSucceeded {
		return Cleanup{}, fmt.Errorf("integrated commit %s is not contained in %s", request.SourceCommit, request.TargetBranch)
	}

	cleanup, err := m.removeIntegratedWorktree(ctx, worktree, path, request.SourceCommit)
	if err != nil {
		return cleanup, err
	}
	branchRemoved, err := m.deleteIntegratedBranch(ctx, worktree.Branch, request.SourceCommit)
	cleanup.BranchRemoved = branchRemoved
	return cleanup, err
}

// removeIntegratedWorktree brings the worktree to absent from whatever state a
// previous attempt left it in. Every path acts on the one recorded owned path:
// a registration whose directory is already gone is removed by name rather than
// by pruning the repository, so an unrelated stale registration is never
// touched. A directory Git does not manage is never deleted, because the
// harness only removes what it registered.
func (m *Manager) removeIntegratedWorktree(ctx context.Context, worktree Worktree, path, sourceCommit string) (Cleanup, error) {
	registered, branch, err := m.registeredWorktree(ctx, path)
	if err != nil {
		return Cleanup{}, err
	}
	info, statErr := os.Lstat(path)
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return Cleanup{}, fmt.Errorf("inspect worktree path: %w", statErr)
	}
	present := statErr == nil
	if present && info.Mode()&os.ModeSymlink != 0 {
		return Cleanup{}, errors.New("worktree path must not be a symlink")
	}

	if !registered {
		if present {
			return Cleanup{}, fmt.Errorf("worktree path %s exists but is not a registered worktree; it must be inspected by hand", path)
		}
		return Cleanup{WorktreeRemoved: true}, nil
	}
	if branch != worktree.Branch {
		return Cleanup{}, fmt.Errorf("worktree branch %q does not match recorded branch %q", branch, worktree.Branch)
	}
	// A registration whose directory is already gone is an interrupted removal.
	// There is nothing on disk left to inspect, so the content checks below only
	// apply while the worktree is still there.
	if present {
		// The integrated worktree is left at the harness commit. Anything else is
		// a different worktree or one that moved on, and is not ours to remove.
		head, err := m.run(ctx, "-C", path, "rev-parse", "--verify", "HEAD^{commit}")
		if err != nil {
			return Cleanup{}, err
		}
		if head.Status != execution.ProcessSucceeded {
			return Cleanup{}, fmt.Errorf("resolve worktree HEAD failed with exit code %d: %s", head.ExitCode, strings.TrimSpace(head.Stderr))
		}
		if strings.TrimSpace(head.Stdout) != sourceCommit {
			return Cleanup{}, fmt.Errorf("worktree HEAD is %s, want the integrated commit %s", strings.TrimSpace(head.Stdout), sourceCommit)
		}
		dirty, err := m.isDirty(ctx, path)
		if err != nil {
			return Cleanup{}, err
		}
		if dirty {
			return Cleanup{}, errors.New("refusing to remove a dirty worktree")
		}
	}
	removed, err := m.run(ctx, "-C", m.repositoryRoot, "worktree", "remove", path)
	if err != nil {
		return Cleanup{}, err
	}
	if removed.Status != execution.ProcessSucceeded {
		return Cleanup{}, fmt.Errorf("remove worktree failed with exit code %d: %s", removed.ExitCode, strings.TrimSpace(removed.Stderr))
	}
	// Removal is only believed once the exact registration is gone. If the check
	// itself cannot run, the removal that already succeeded is still reported:
	// the artifact is gone whether or not this command could confirm it, and
	// claiming otherwise would send an operator after a worktree that no longer
	// exists. Only an observation that it survived clears the flag.
	stillRegistered, _, err := m.registeredWorktree(ctx, path)
	if err != nil {
		return Cleanup{WorktreeRemoved: true}, fmt.Errorf("verify removal of worktree %s: %w", path, err)
	}
	if stillRegistered {
		return Cleanup{}, fmt.Errorf("worktree %s is still registered after removal", path)
	}
	return Cleanup{WorktreeRemoved: true}, nil
}

// deleteIntegratedBranch deletes the run's branch, tolerating a branch a
// previous attempt already deleted and refusing one that no longer points at
// the integrated commit.
//
// Deletion is a compare-and-swap on the exact recorded commit rather than
// `git branch -d`. That keeps it deterministic: `-d` decides mergedness against
// the current HEAD or a configured upstream, so it can refuse a branch already
// proven to be contained in the recorded target simply because the target is
// not the branch that happens to be checked out.
func (m *Manager) deleteIntegratedBranch(ctx context.Context, branch, sourceCommit string) (bool, error) {
	existing, err := m.run(ctx, "-C", m.repositoryRoot, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	if err != nil {
		return false, err
	}
	switch {
	case existing.ExitCode == 1:
		return true, nil
	case existing.Status != execution.ProcessSucceeded:
		return false, fmt.Errorf("check branch %s failed with exit code %d: %s", branch, existing.ExitCode, strings.TrimSpace(existing.Stderr))
	}
	commit, err := m.resolveBranchCommit(ctx, branch)
	if err != nil {
		return false, err
	}
	if commit != sourceCommit {
		return false, fmt.Errorf("branch %s is at %s, want the integrated commit %s", branch, commit, sourceCommit)
	}
	// A ref update cannot see checkouts, so a branch still checked out anywhere
	// is refused rather than deleted out from under that working tree.
	entries, err := m.listWorktrees(ctx)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if entry.branch == branch {
			return false, fmt.Errorf("branch %s is still checked out in %s", branch, entry.path)
		}
	}
	deleted, err := m.runWithEnvironment(ctx, os.Environ(), "-C", m.repositoryRoot,
		"-c", "core.hooksPath="+os.DevNull,
		"update-ref", "-d", "refs/heads/"+branch, sourceCommit)
	if err != nil {
		return false, err
	}
	if deleted.Status != execution.ProcessSucceeded {
		return false, fmt.Errorf("delete integrated branch %s at %s failed with exit code %d: %s", branch, sourceCommit, deleted.ExitCode, strings.TrimSpace(deleted.Stderr))
	}
	// As with the worktree, a compare-and-swap deletion that already succeeded
	// stays reported as a deletion even when this confirmation cannot run. Only
	// observing the branch still there clears the flag.
	gone, err := m.run(ctx, "-C", m.repositoryRoot, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	if err != nil {
		return true, fmt.Errorf("verify deletion of branch %s: %w", branch, err)
	}
	if gone.ExitCode != 1 {
		return false, fmt.Errorf("branch %s still exists after deletion", branch)
	}
	return true, nil
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

type worktreeEntry struct {
	path   string
	branch string
}

func (m *Manager) listWorktrees(ctx context.Context) ([]worktreeEntry, error) {
	result, err := m.run(ctx, "-C", m.repositoryRoot, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	if result.Status != execution.ProcessSucceeded {
		return nil, fmt.Errorf("list worktrees failed with exit code %d: %s", result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	var entries []worktreeEntry
	var current worktreeEntry
	flush := func() {
		if current.path != "" {
			entries = append(entries, current)
		}
		current = worktreeEntry{}
	}
	for _, line := range strings.Split(result.Stdout, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			current.path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "branch refs/heads/"):
			current.branch = strings.TrimPrefix(line, "branch refs/heads/")
		}
	}
	flush()
	return entries, nil
}

func (m *Manager) registeredWorktree(ctx context.Context, path string) (bool, string, error) {
	entries, err := m.listWorktrees(ctx)
	if err != nil {
		return false, "", err
	}
	for _, entry := range entries {
		if entry.path == path {
			return true, entry.branch, nil
		}
	}
	return false, "", nil
}

// ownedPath derives where this run's worktree must live from its recorded
// identifiers, without requiring the directory to still exist. A resumed
// cleanup needs ownership proven for a worktree that is already gone.
func (m *Manager) ownedPath(worktree Worktree) (string, error) {
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
	return expectedPath, nil
}

func (m *Manager) validateOwnedPath(worktree Worktree) (string, error) {
	path, err := m.ownedPath(worktree)
	if err != nil {
		return "", err
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
	if resolved != path {
		return "", errors.New("worktree path resolves outside its owned location")
	}
	return path, nil
}

func (m *Manager) run(ctx context.Context, args ...string) (execution.ProcessResult, error) {
	return m.runWithEnvironment(ctx, nil, args...)
}

func (m *Manager) runWithEnvironment(ctx context.Context, environment []string, args ...string) (execution.ProcessResult, error) {
	return m.runBounded(ctx, environment, m.timeout, args...)
}

// runRemote runs a Git command that talks to the remote. It is bounded by its
// own longer timeout, because a network round trip is not a local ref update
// and holding both to the same deadline would either starve the push or let a
// local command hang.
func (m *Manager) runRemote(ctx context.Context, args ...string) (execution.ProcessResult, error) {
	return m.runBounded(ctx, nil, m.remoteTimeout(), args...)
}

func (m *Manager) remoteTimeout() time.Duration {
	if m.timeout > pushTimeout {
		return m.timeout
	}
	return pushTimeout
}

func (m *Manager) runBounded(ctx context.Context, environment []string, timeout time.Duration, args ...string) (execution.ProcessResult, error) {
	result, err := m.runner.Run(ctx, execution.Command{
		Name:    m.gitBinary,
		Args:    args,
		Env:     environment,
		Timeout: timeout,
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
	if request.TargetBranch != "" {
		if err := validateTargetBranch(request.TargetBranch); err != nil {
			return err
		}
		if request.TargetBranch == branchName(request.WorkItemID, request.RunID) {
			return errors.New("integration target must differ from the worktree branch")
		}
	}
	return validateRef(request.BaseRef)
}

// validateTargetBranch keeps an integration target a plain local branch name.
// Fully qualified refs and HEAD are refused so a caller can never aim a
// fast-forward at something other than refs/heads/<target>.
func validateTargetBranch(branch string) error {
	if branch == "" {
		return errors.New("worktree has no recorded integration target")
	}
	if err := validateRef(branch); err != nil {
		return fmt.Errorf("invalid integration target: %w", err)
	}
	if branch == "HEAD" || strings.HasPrefix(branch, "refs/") {
		return fmt.Errorf("integration target %q must be a local branch name", branch)
	}
	return nil
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
