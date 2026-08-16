// Package publish opens and inspects the pull requests a run's work is
// published through. It is a harness-owned adapter over the forge CLI for the
// same reason the Git operations are harness-owned: the developer's phase is
// what causes a pull request to exist and the reviewer's verdict is what causes
// it to be merged, but the harness is what invokes the CLI, and neither role is
// given a credential, a tool, or a request to invoke it themselves.
package publish

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"yoyodyne/internal/execution"
)

const (
	defaultBinary    = "gh"
	defaultGitBinary = "git"
	defaultRemote    = "origin"
	defaultTimeout   = 60 * time.Second
	// maxBodyBytes bounds the description carried onto the forge. A pull request
	// body summarizes a run; it is not a place to republish everything the run
	// produced.
	maxBodyBytes = 16 << 10
	// maxTitleBytes keeps a title a title.
	maxTitleBytes = 200
)

// Availability reports whether the forge CLI can actually be used. It is
// checked before a run claims anything, so a project that asked for pull
// requests never discovers halfway through that it cannot open one.
type Availability struct {
	Installed     bool   `json:"installed"`
	Authenticated bool   `json:"authenticated"`
	Version       string `json:"version,omitempty"`
}

// PullRequest is one pull request as the forge reports it.
type PullRequest struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	State  string `json:"state,omitempty"`
	Merged bool   `json:"merged,omitempty"`
	// AutoMerge reports a merge the forge is holding for this request: it will
	// perform it once the base branch's requirements are met. It is what tells a
	// queued merge that has not landed yet from one the forge dropped, which is
	// otherwise the same observation — an open, unmerged request.
	AutoMerge bool `json:"auto_merge,omitempty"`
}

// Request describes the pull request a published run branch must have open.
type Request struct {
	Head  string
	Base  string
	Title string
	Body  string
}

// MergeMethod names how the forge brings a pull request onto its base branch.
// The three produce different remote histories, so a caller states which one it
// means rather than inheriting whatever the repository happens to default to.
//
// Only one of them preserves the commits that were reviewed. A squash replaces
// them with a single commit the head never had, and GitHub's rebase always
// updates committer information and mints new SHAs — even for a request that
// needs no rebasing at all — so both leave the base carrying copies of the work
// rather than the work itself.
type MergeMethod string

const (
	// MergeCommit joins the request onto its base under a merge commit, which
	// keeps the request's own commits on the base exactly as they were pushed:
	// the merge commit's second parent is the head commit itself.
	MergeCommit MergeMethod = "merge"
	// MergeRebase and MergeSquash both rewrite what they merge, and are named
	// here because the method is part of what a caller decides rather than
	// because anything in this repository asks for them.
	MergeRebase MergeMethod = "rebase"
	MergeSquash MergeMethod = "squash"
)

// MergeRequest names the pull request to merge, the commit it must still be at,
// and the method to merge it by.
type MergeRequest struct {
	Number     int
	HeadCommit string
	Method     MergeMethod
}

// MergeResult is what the forge did with a merge request. The harness asks for
// the merge to happen when the base branch's requirements are met rather than
// now, so the answer has to say which of the two the forge did: a queued merge
// completes minutes later, without the harness watching, and a caller that
// assumed otherwise would report a publication as unfinished on every protected
// branch.
type MergeResult struct {
	// Queued reports a merge the forge accepted and will perform itself once the
	// pull request's requirements are met. A result that is not queued is a
	// merge the forge performed while it was asked.
	Queued bool
}

// AutoMergeUnavailable reports a repository that does not offer the queued
// merge the harness asks for. It is distinct from a refusal because the remedy
// is a repository setting rather than an unmet requirement of this change: a
// repository whose base branch requires checks and whose settings forbid
// auto-merge cannot be published to this way at all, and the operator has to be
// told which setting to change rather than left with a merge that fails on
// every run.
type AutoMergeUnavailable struct {
	Number int
	Reason string
}

func (e AutoMergeUnavailable) Error() string {
	message := fmt.Sprintf("the forge cannot queue a merge for pull request %d: enable \"Allow auto-merge\" in the repository's settings, or remove the required checks from the base branch's protection rules", e.Number)
	if e.Reason != "" {
		message += ": " + e.Reason
	}
	return message
}

// MergeRefused reports a forge that declined to merge a pull request. It is a
// distinct error because a refusal is an answer about the repository's rules —
// a required check that has not run, a review the base branch demands, a head
// commit that moved — rather than a harness failure, and an operator has to be
// told which requirement was unmet.
type MergeRefused struct {
	Number int
	Method MergeMethod
	// Status is the forge's own merge state for the request, when it could be
	// read; Reason is what the forge printed when it refused.
	Status string
	Reason string
}

func (e MergeRefused) Error() string {
	message := fmt.Sprintf("the forge refused to merge pull request %d with the %s method", e.Number, e.Method)
	if requirement := mergeRequirement(e.Status); requirement != "" {
		message += ": " + requirement
	}
	if e.Reason != "" {
		message += ": " + e.Reason
	}
	return message
}

// mergeRequirement states in words what the forge's merge state says is unmet,
// so a refusal reads as the rule it is rather than as a status code. A state
// this does not recognize is left to the forge's own message, which is reported
// beside it either way.
func mergeRequirement(status string) string {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "BLOCKED":
		return "the base branch's protection rules are not satisfied (BLOCKED)"
	case "BEHIND":
		return "the base branch moved ahead of the pull request (BEHIND)"
	case "DIRTY":
		return "the pull request conflicts with the base branch (DIRTY)"
	case "UNSTABLE":
		return "a required check has not succeeded (UNSTABLE)"
	case "DRAFT":
		return "the pull request is still a draft (DRAFT)"
	case "HAS_HOOKS":
		return "a repository hook rejected the merge (HAS_HOOKS)"
	case "UNKNOWN":
		return "the forge has not finished deciding whether the pull request can merge (UNKNOWN)"
	}
	return ""
}

// GitHub publishes through the `gh` CLI, which holds its own credentials the
// way the coding-agent CLIs hold theirs. The harness reports whether that
// authentication is present and never manages it.
type GitHub struct {
	Runner  execution.ProcessRunner
	Binary  string
	Dir     string
	Timeout time.Duration
	// Remote names the Git remote this client speaks about, so the forge CLI
	// acts on the repository the project configured rather than on whichever
	// one it would infer from the working directory. A checkout with more than
	// one remote is the case that makes inference wrong rather than merely
	// redundant.
	Remote string
	// GitBinary resolves the remote to a repository URL. It is a field so the
	// resolution is testable without a real checkout.
	GitBinary string
	// RedactValues are the sensitive environment values scrubbed from anything
	// the CLI prints, so a token in a diagnostic never reaches a report.
	RedactValues []string
}

func (g GitHub) Availability(ctx context.Context) (Availability, error) {
	if g.Runner == nil {
		return Availability{}, errors.New("pull request process runner is required")
	}
	version, err := g.exec(ctx, "--version")
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return Availability{Installed: false}, nil
		}
		return Availability{}, fmt.Errorf("check GitHub CLI version: %w", err)
	}
	if version.Status != execution.ProcessSucceeded {
		return Availability{Installed: false}, nil
	}
	availability := Availability{Installed: true, Version: firstLine(version.Stdout)}
	status, err := g.exec(ctx, "auth", "status")
	if err != nil {
		return availability, fmt.Errorf("check GitHub CLI authentication: %w", err)
	}
	availability.Authenticated = status.Status == execution.ProcessSucceeded
	return availability, nil
}

// Ensure returns the pull request for a published branch, opening one if the
// branch does not have it yet. It is called after every developer attempt, so
// it has to be idempotent: a repair attempt updates the pull request its first
// attempt opened rather than opening a second one for the same branch.
func (g GitHub) Ensure(ctx context.Context, request Request) (PullRequest, error) {
	if err := request.validate(); err != nil {
		return PullRequest{}, err
	}
	existing, found, err := g.find(ctx, request.Head)
	if err != nil {
		return PullRequest{}, err
	}
	if found {
		// A branch whose pull request is already closed or merged cannot receive
		// this run's work. Opening a second one would publish the same branch
		// twice and leave two answers about what is being reviewed.
		if existing.State != "" && !strings.EqualFold(existing.State, "OPEN") {
			return PullRequest{}, fmt.Errorf("pull request %d for branch %s is %s and cannot be republished into", existing.Number, request.Head, strings.ToLower(existing.State))
		}
		return existing, nil
	}
	scope, err := g.repoArgs(ctx)
	if err != nil {
		return PullRequest{}, err
	}
	created, err := g.exec(ctx, append([]string{"pr", "create"}, append(scope,
		"--base", request.Base,
		"--head", request.Head,
		"--title", boundedTitle(request.Title),
		"--body", boundedBody(request.Body))...)...)
	if err != nil {
		return PullRequest{}, fmt.Errorf("open pull request for %s: %w", request.Head, err)
	}
	if created.Status != execution.ProcessSucceeded {
		return PullRequest{}, fmt.Errorf("open pull request for %s failed with exit code %d: %s", request.Head, created.ExitCode, g.redact(strings.TrimSpace(created.Stderr)))
	}
	opened, found, err := g.find(ctx, request.Head)
	if err != nil {
		return PullRequest{}, err
	}
	if !found {
		return PullRequest{}, fmt.Errorf("pull request for %s was created but cannot be found", request.Head)
	}
	return opened, nil
}

// Merge asks the forge to merge a pull request when its requirements are met,
// which is what brings a promotion onto the remote target branch. The head
// commit is passed along so the forge refuses a request that moved since the
// harness published it: what merges must be the commit the run integrated and
// nothing else.
//
// The request is queued rather than demanded. A protected branch will not
// accept a merge until its required checks have passed, and those take longer
// than the moment between an approving verdict and this call, so asking to
// merge now is asking for a refusal. Queuing is what the branch protection is
// asking for, and the forge performs the merge itself once the requirements it
// names are satisfied. Administrator override is deliberately not offered here:
// merging with it would bypass the very checks the protection expresses, which
// removes the gate rather than satisfying it.
//
// A refusal comes back as MergeRefused rather than as a generic failure. A
// protected branch declining the merge because the pull request conflicts with
// its base is the repository's rules being applied, not the harness failing,
// and the answer has to name the requirement. A repository that does not offer
// queued merges at all is reported separately, as AutoMergeUnavailable, because
// its remedy is a setting rather than a requirement of this change.
func (g GitHub) Merge(ctx context.Context, request MergeRequest) (MergeResult, error) {
	if err := request.validate(); err != nil {
		return MergeResult{}, err
	}
	scope, err := g.repoArgs(ctx)
	if err != nil {
		return MergeResult{}, err
	}
	arguments := append([]string{"pr", "merge", strconv.Itoa(request.Number)}, append(scope,
		request.Method.flag(),
		"--match-head-commit", request.HeadCommit)...)
	// The queued request is the same command with one flag more, built as its own
	// slice so the arguments stay intact for the immediate merge below.
	queueing := append(append([]string{}, arguments...), "--auto")
	queued, err := g.exec(ctx, queueing...)
	if err != nil {
		return MergeResult{}, fmt.Errorf("merge pull request %d: %w", request.Number, err)
	}
	if queued.Status == execution.ProcessSucceeded {
		return MergeResult{Queued: true}, nil
	}
	reason := g.redact(strings.TrimSpace(queued.Stderr))
	if autoMergeUnavailable(reason) {
		return MergeResult{}, AutoMergeUnavailable{Number: request.Number, Reason: reason}
	}
	if !nothingLeftToWaitFor(reason) {
		return MergeResult{}, MergeRefused{
			Number: request.Number,
			Method: request.Method,
			Status: g.mergeState(ctx, request.Number),
			Reason: reason,
		}
	}
	// The forge has nothing left to wait for, and says so by refusing to queue a
	// merge it would perform immediately. Merging now is then what the queued
	// request asked for rather than a way around it: every requirement the base
	// branch names is already satisfied, and nothing is overridden to get there.
	merged, err := g.exec(ctx, arguments...)
	if err != nil {
		return MergeResult{}, fmt.Errorf("merge pull request %d: %w", request.Number, err)
	}
	if merged.Status != execution.ProcessSucceeded {
		return MergeResult{}, MergeRefused{
			Number: request.Number,
			Method: request.Method,
			Status: g.mergeState(ctx, request.Number),
			Reason: g.redact(strings.TrimSpace(merged.Stderr)),
		}
	}
	return MergeResult{}, nil
}

// autoMergeUnavailable recognizes the forge saying that this repository does
// not allow queued merges. It is matched on the message because that is the
// only place the forge reports it: the CLI exits the same way it does for any
// other refusal, and an operator told "the merge was refused" would have no way
// to discover that a repository setting is what needs changing.
func autoMergeUnavailable(reason string) bool {
	normalized := normalizeAutoMerge(reason)
	return strings.Contains(normalized, "auto merge is not allowed") ||
		strings.Contains(normalized, "auto merge is not enabled") ||
		strings.Contains(normalized, "auto merge is disabled")
}

// nothingLeftToWaitFor recognizes the forge refusing to queue a merge because
// the pull request already meets every requirement its base branch names. A
// repository with no required checks answers this way to every queued merge, so
// treating it as a refusal would leave the unprotected case — the one that
// worked before queuing existed — unable to publish at all.
func nothingLeftToWaitFor(reason string) bool {
	normalized := strings.ToLower(reason)
	return strings.Contains(normalized, "clean status") ||
		strings.Contains(normalized, "not in the correct state") ||
		strings.Contains(normalized, "does not need auto-merge")
}

// normalizeAutoMerge folds the spellings the forge uses for the setting into
// one, so a message is matched on what it says rather than on how it hyphenates.
func normalizeAutoMerge(reason string) string {
	lowered := strings.ToLower(reason)
	lowered = strings.ReplaceAll(lowered, "auto-merge", "auto merge")
	return strings.ReplaceAll(lowered, "automerge", "auto merge")
}

// mergeState asks the forge what it thinks of a pull request it would not
// merge. It reports an empty state rather than an error: this runs only to
// explain a refusal that already happened, and a second failure must not
// replace the answer the forge already gave.
func (g GitHub) mergeState(ctx context.Context, number int) string {
	scope, err := g.repoArgs(ctx)
	if err != nil {
		return ""
	}
	viewed, err := g.exec(ctx, append([]string{"pr", "view", strconv.Itoa(number)}, append(scope, "--json", "mergeStateStatus")...)...)
	if err != nil || viewed.Status != execution.ProcessSucceeded {
		return ""
	}
	var reported struct {
		MergeStateStatus string `json:"mergeStateStatus"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(viewed.Stdout)), &reported); err != nil {
		return ""
	}
	return strings.TrimSpace(reported.MergeStateStatus)
}

// State reports what the forge currently says about a branch's pull request. It
// is how the harness confirms that the merge it asked for actually happened
// rather than assuming it did, and — for a merge the forge queued and has not
// performed yet — whether that merge is still waiting or was dropped.
func (g GitHub) State(ctx context.Context, head string) (PullRequest, error) {
	if err := validateArgument("head branch", head); err != nil {
		return PullRequest{}, err
	}
	found, exists, err := g.find(ctx, head)
	if err != nil {
		return PullRequest{}, err
	}
	if !exists {
		return PullRequest{}, fmt.Errorf("no pull request exists for branch %s", head)
	}
	return found, nil
}

// find lists the one pull request a run branch may have, in any state. Listing
// is deliberate: it reports "there is none" as an empty result rather than as a
// failed command, so an absent pull request is never confused with a forge that
// could not be reached.
func (g GitHub) find(ctx context.Context, head string) (PullRequest, bool, error) {
	scope, err := g.repoArgs(ctx)
	if err != nil {
		return PullRequest{}, false, err
	}
	result, err := g.exec(ctx, append([]string{"pr", "list"}, append(scope,
		"--head", head,
		"--state", "all",
		"--limit", "1",
		"--json", "number,url,state,mergedAt,autoMergeRequest")...)...)
	if err != nil {
		return PullRequest{}, false, fmt.Errorf("list pull requests for %s: %w", head, err)
	}
	if result.Status != execution.ProcessSucceeded {
		return PullRequest{}, false, fmt.Errorf("list pull requests for %s failed with exit code %d: %s", head, result.ExitCode, g.redact(strings.TrimSpace(result.Stderr)))
	}
	var reported []struct {
		Number   int    `json:"number"`
		URL      string `json:"url"`
		State    string `json:"state"`
		MergedAt string `json:"mergedAt"`
		// AutoMergeRequest is the merge the forge is holding, and is null on a
		// request that has none. Its contents say who asked and how; only its
		// presence matters here.
		AutoMergeRequest *struct {
			MergeMethod string `json:"mergeMethod"`
		} `json:"autoMergeRequest"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(result.Stdout)), &reported); err != nil {
		return PullRequest{}, false, fmt.Errorf("decode pull requests for %s: %w", head, err)
	}
	if len(reported) == 0 {
		return PullRequest{}, false, nil
	}
	one := reported[0]
	if one.Number <= 0 {
		return PullRequest{}, false, fmt.Errorf("pull request for %s reported no number", head)
	}
	return PullRequest{
		Number:    one.Number,
		URL:       one.URL,
		State:     one.State,
		Merged:    strings.EqualFold(one.State, "MERGED") || strings.TrimSpace(one.MergedAt) != "",
		AutoMerge: one.AutoMergeRequest != nil,
	}, true, nil
}

// repoArgs scopes a forge command to the configured remote's repository. It
// fails rather than falling back to inference: publishing to a repository the
// project did not name is the mistake worth refusing, and a silent fallback is
// how that mistake would happen.
func (g GitHub) repoArgs(ctx context.Context) ([]string, error) {
	url, err := g.remoteURL(ctx)
	if err != nil {
		return nil, err
	}
	return []string{"--repo", url}, nil
}

func (g GitHub) remoteURL(ctx context.Context) (string, error) {
	remote := strings.TrimSpace(g.Remote)
	if remote == "" {
		remote = defaultRemote
	}
	if err := validateArgument("remote", remote); err != nil {
		return "", err
	}
	result, err := g.Runner.Run(ctx, execution.Command{
		Name:     g.gitBinary(),
		Args:     []string{"-C", g.Dir, "remote", "get-url", remote},
		Timeout:  g.timeout(),
		Redactor: execution.NewRedactor(g.RedactValues...),
	}, nil)
	if err != nil {
		return "", fmt.Errorf("resolve remote %s: %w", remote, err)
	}
	if result.Status != execution.ProcessSucceeded {
		return "", fmt.Errorf("resolve remote %s failed with exit code %d: %s", remote, result.ExitCode, g.redact(strings.TrimSpace(result.Stderr)))
	}
	url := strings.TrimSpace(result.Stdout)
	if url == "" {
		return "", fmt.Errorf("remote %s reported no URL", remote)
	}
	return url, nil
}

func (g GitHub) gitBinary() string {
	if strings.TrimSpace(g.GitBinary) == "" {
		return defaultGitBinary
	}
	return g.GitBinary
}

func (g GitHub) exec(ctx context.Context, args ...string) (execution.ProcessResult, error) {
	return g.Runner.Run(ctx, execution.Command{
		Name:     g.binary(),
		Args:     args,
		Dir:      g.Dir,
		Timeout:  g.timeout(),
		Redactor: execution.NewRedactor(g.RedactValues...),
	}, nil)
}

func (g GitHub) binary() string {
	if strings.TrimSpace(g.Binary) == "" {
		return defaultBinary
	}
	return g.Binary
}

func (g GitHub) timeout() time.Duration {
	if g.Timeout <= 0 {
		return defaultTimeout
	}
	return g.Timeout
}

// redact scrubs sensitive values out of anything the CLI printed before it
// becomes part of an error a run records.
func (g GitHub) redact(value string) string {
	return execution.NewRedactor(g.RedactValues...).Redact(value)
}

func (r Request) validate() error {
	if err := validateArgument("head branch", r.Head); err != nil {
		return err
	}
	if err := validateArgument("base branch", r.Base); err != nil {
		return err
	}
	if strings.TrimSpace(r.Title) == "" {
		return errors.New("pull request title is required")
	}
	return nil
}

func (r MergeRequest) validate() error {
	var problems []error
	if r.Number <= 0 {
		problems = append(problems, errors.New("pull request number is required"))
	}
	if !commitPattern.MatchString(r.HeadCommit) {
		problems = append(problems, fmt.Errorf("head commit %q is invalid", r.HeadCommit))
	}
	if r.Method.flag() == "" {
		problems = append(problems, fmt.Errorf("merge method %q is not one the forge offers", r.Method))
	}
	return errors.Join(problems...)
}

// flag is the CLI option for a merge method, and the empty string for anything
// that is not one. The forge requires a method to be named, so an unset one is
// refused rather than left to a default nobody chose.
func (m MergeMethod) flag() string {
	switch m {
	case MergeRebase:
		return "--rebase"
	case MergeCommit:
		return "--merge"
	case MergeSquash:
		return "--squash"
	}
	return ""
}

// commitPattern keeps a commit a commit, for the same reason branch names are
// validated: it reaches a command line.
var commitPattern = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

// validateArgument keeps a branch name a branch name. These values reach a
// command line, so anything that could read as an option is refused rather than
// passed along.
func validateArgument(kind, value string) error {
	trimmed := strings.TrimSpace(value)
	switch {
	case trimmed == "":
		return fmt.Errorf("%s is required", kind)
	case strings.HasPrefix(trimmed, "-"):
		return fmt.Errorf("%s %q cannot start with a dash", kind, value)
	case strings.IndexFunc(trimmed, func(r rune) bool { return r <= ' ' }) >= 0:
		return fmt.Errorf("%s %q cannot contain whitespace", kind, value)
	}
	return nil
}

func boundedTitle(title string) string {
	folded := strings.Join(strings.Fields(title), " ")
	return bounded(folded, maxTitleBytes)
}

func boundedBody(body string) string {
	return bounded(body, maxBodyBytes)
}

// bounded keeps the head of a value within a limit, cut on a rune boundary so
// truncated text stays text.
func bounded(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	cut := limit
	for cut > 0 && !isRuneStart(value[cut]) {
		cut--
	}
	return strings.TrimSpace(value[:cut])
}

func isRuneStart(b byte) bool {
	return b&0xC0 != 0x80
}

func firstLine(value string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(value), "\n")
	return line
}
