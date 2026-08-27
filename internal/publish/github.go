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

	"github.com/mason-bryant/yoyodyne/internal/execution"
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
// merge the harness asks for, for a pull request that needs one. It is distinct
// from a refusal because the remedy is a repository setting rather than an
// unmet requirement of this change: a repository whose base branch holds the
// request back and whose settings forbid auto-merge cannot be published to at
// all, and the operator has to be told which setting to change rather than left
// with a merge that fails on every run.
//
// Both halves are required before it is reported. "Allow auto-merge" is off by
// default, and a repository that has it off and has nothing holding the request
// back is merged into immediately instead — refusing that one would break every
// project without branch protection, which is most of them.
type AutoMergeUnavailable struct {
	Number int
	// Status is the forge's merge state for the request, which is what says
	// there was something for a queued merge to wait for; Reason is what the
	// forge printed when it would not queue one.
	Status string
	Reason string
}

func (e AutoMergeUnavailable) Error() string {
	message := fmt.Sprintf("the forge cannot queue a merge for pull request %d, and it cannot merge now either", e.Number)
	if requirement := mergeRequirement(e.Status); requirement != "" {
		message += ": " + requirement
	}
	message += ": enable \"Allow auto-merge\" in the repository's settings, or take the unmet requirement out of the base branch's protection rules"
	if e.Reason != "" {
		message += ": " + e.Reason
	}
	return message
}

// MergeRefused reports a forge that declined to merge a pull request. It is a
// distinct error because a refusal is an answer about the repository's rules —
// a request that conflicts with the base branch, a merge method the repository
// forbids, a head commit that moved — rather than a harness failure, and an
// operator has to be told which requirement was unmet.
//
// A required check that has not finished is deliberately not one of these. The
// harness asks the forge to merge when the requirements are met rather than
// now, so a pending check is what that queued merge waits for; only a request
// the forge will not merge whenever it is asked reaches here.
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
	// redundant. Pull requests are opened against this repository.
	Remote string
	// PushRemote names the Git remote run branches are pushed to, when that is
	// not Remote. It is what makes a pull request cross-repository: the branch
	// lives in the fork this names and the request is opened against Remote, so
	// a contributor without push access to the project's repository publishes
	// the same way anyone with it does. Empty means the branch is on Remote,
	// which is every project that can push to what it publishes into.
	PushRemote string
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
	head, err := g.headRef(ctx, request.Head)
	if err != nil {
		return PullRequest{}, err
	}
	created, err := g.exec(ctx, append([]string{"pr", "create"}, append(scope,
		"--base", request.Base,
		"--head", head,
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
// A forge that will not queue the merge is asked whether it would make one now,
// because a request with nothing holding it back has nothing for a queue to
// wait for. That is the ordinary case for a repository without branch
// protection, and for one whose settings forbid queued merges — "Allow
// auto-merge" is off by default — so both are merged into immediately rather
// than reported as unpublishable.
//
// What is left is a request the forge will neither queue nor merge, and that
// comes back as an answer rather than a generic failure. AutoMergeUnavailable
// says the repository forbids queued merges and something is holding this
// request back, so the remedy is a setting; MergeRefused says the repository's
// rules are being applied to this request — a conflict with its base, a merge
// method the repository forbids — and names the requirement that was unmet.
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
	// The forge declined to queue, and what that means depends on whether the
	// request has anything left to wait for. That question is asked before any
	// other, and asked twice over: the forge says so in words, and the merge state
	// it reports for the request says so in its own vocabulary. Either answer is
	// enough, which is what keeps a repository with nothing holding its requests
	// back — no branch protection, or every requirement already met — publishing
	// whatever the forge's reason for not queuing was, including "Allow
	// auto-merge" being off, which is GitHub's default.
	reason := g.redact(strings.TrimSpace(queued.Stderr))
	status := g.mergeState(ctx, request.Number)
	if !nothingLeftToWaitFor(reason) && !readyToMergeNow(status) {
		// Something is holding the request back and the forge will not hold the
		// merge for it. If that is the repository forbidding queued merges, the
		// remedy is a setting and the operator is told which; anything else is the
		// repository's rules being applied to this request.
		if autoMergeUnavailable(reason) {
			return MergeResult{}, AutoMergeUnavailable{Number: request.Number, Status: status, Reason: reason}
		}
		return MergeResult{}, MergeRefused{
			Number: request.Number,
			Method: request.Method,
			Status: status,
			Reason: reason,
		}
	}
	// The forge has nothing left to wait for, so it refused to queue a merge it
	// would perform immediately. Merging now is then what the queued request
	// asked for rather than a way around it: every requirement the base branch
	// names is already satisfied, and nothing is overridden to get there. A forge
	// that turns out to disagree refuses this merge too, and that refusal is what
	// gets reported.
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
//
// It reads the forge's words, which can be reworded, so it is never the only
// thing asked: readyToMergeNow answers the same question from the merge state
// the forge reports for the request, and either answer is enough.
func nothingLeftToWaitFor(reason string) bool {
	normalized := strings.ToLower(reason)
	return strings.Contains(normalized, "clean status") ||
		strings.Contains(normalized, "not in the correct state") ||
		strings.Contains(normalized, "does not need auto-merge")
}

// readyToMergeNow reports a merge state with nothing outstanding on it. It is
// the forge's own vocabulary rather than its prose, so it survives a reworded
// message, and it is only ever consulted after the forge has already declined
// to queue a merge: a request that is clean has nothing for a queue to wait
// for. A state that could not be read answers no, which leaves the decision to
// what the forge said.
func readyToMergeNow(status string) bool {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	// Mergeable with every requirement met.
	case "CLEAN":
		return true
	// Mergeable, with checks that are running or failing but that no branch
	// protection requires. A repository with CI and no protection reports this
	// rather than CLEAN, and it is the ordinary case: nothing is holding the
	// request back, so there is nothing for a queue to wait for. Treating it as
	// outstanding would leave that repository unable to publish at all.
	case "UNSTABLE":
		return true
	// Mergeable, with repository commit hooks that will run on the merge.
	case "HAS_HOOKS":
		return true
	default:
		// BLOCKED, BEHIND, DIRTY, DRAFT and UNKNOWN all have something to
		// resolve before a merge, so the forge's own words decide.
		return false
	}
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
//
// The head is the plain branch name even when the branch lives in a fork, which
// is the one place a cross-repository request is not qualified: the forge
// filters this listing by head reference name, and a request opened from a fork
// carries the same branch name there as it does in the fork. Qualifying it would
// name a reference the base repository has never heard of. What keeps that from
// matching somebody else's request is the branch name itself — a run branch
// carries its run's identifier, which no other repository's branch has.
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
	url, err := g.remoteURL(ctx, g.remoteName())
	if err != nil {
		return nil, err
	}
	return []string{"--repo", url}, nil
}

// headRef names the branch a pull request is opened from, in the form the forge
// has to be told it. A project whose run branches are pushed to the repository
// it publishes into names the branch and nothing else. One that pushes to a fork
// has to qualify the branch with that fork's owner, because the reference the
// forge is being asked about is not one the base repository has.
//
// The owner is read out of the fork remote's own URL rather than configured
// beside it. The remote already says which repository the branch was pushed to,
// and a second setting is a second thing that can disagree with the first.
func (g GitHub) headRef(ctx context.Context, branch string) (string, error) {
	pushRemote := g.pushRemoteName()
	if pushRemote == g.remoteName() {
		return branch, nil
	}
	url, err := g.remoteURL(ctx, pushRemote)
	if err != nil {
		return "", err
	}
	owner, err := repositoryOwner(url)
	if err != nil {
		return "", fmt.Errorf("resolve the owner of remote %s: %w", pushRemote, err)
	}
	return owner + ":" + branch, nil
}

func (g GitHub) remoteName() string {
	remote := strings.TrimSpace(g.Remote)
	if remote == "" {
		return defaultRemote
	}
	return remote
}

// pushRemoteName is the remote run branches were pushed to, which is the one
// pull requests are opened against unless the project publishes from a fork.
func (g GitHub) pushRemoteName() string {
	pushRemote := strings.TrimSpace(g.PushRemote)
	if pushRemote == "" {
		return g.remoteName()
	}
	return pushRemote
}

func (g GitHub) remoteURL(ctx context.Context, remote string) (string, error) {
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

// ownerPattern keeps a resolved account name an account name. It reaches a
// command line as half of a qualified head reference, so anything that could
// read as an option, as a second reference, or as whitespace is refused rather
// than passed along.
var ownerPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// repositoryOwner reads the account a remote's repository belongs to out of the
// remote's URL. Every form Git accepts names the repository as the last two path
// segments — `owner/name` — whether it arrived over SSH, over HTTPS, or in the
// scp-like syntax, so that is what is read rather than the shape of any one of
// them. A URL that names no such pair is refused: publishing from a fork nobody
// can identify would open the request against the wrong head or none at all.
func repositoryOwner(url string) (string, error) {
	path := strings.TrimSuffix(strings.TrimSuffix(strings.TrimSpace(url), "/"), ".git")
	if scheme := strings.Index(path, "://"); scheme >= 0 {
		authority := path[scheme+len("://"):]
		slash := strings.Index(authority, "/")
		if slash < 0 {
			return "", fmt.Errorf("remote URL %q names no repository", url)
		}
		path = authority[slash+1:]
	} else if colon := strings.LastIndex(path, ":"); colon >= 0 {
		// The scp-like form, `[user@]host:owner/name`, which has no scheme and
		// separates the path with a colon rather than a slash.
		path = path[colon+1:]
	}
	segments := strings.Split(strings.Trim(path, "/"), "/")
	if len(segments) < 2 {
		return "", fmt.Errorf("remote URL %q names no repository owner", url)
	}
	owner := segments[len(segments)-2]
	if !ownerPattern.MatchString(owner) {
		return "", fmt.Errorf("remote URL %q names %q as its owner, which is not an account name", url, owner)
	}
	return owner, nil
}

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
