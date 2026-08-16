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
}

// Request describes the pull request a published run branch must have open.
type Request struct {
	Head  string
	Base  string
	Title string
	Body  string
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

// State reports what the forge currently says about a branch's pull request. It
// is how the harness confirms that the promotion it pushed actually merged the
// pull request rather than assuming it did.
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
		"--json", "number,url,state,mergedAt")...)...)
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
		Number: one.Number,
		URL:    one.URL,
		State:  one.State,
		Merged: strings.EqualFold(one.State, "MERGED") || strings.TrimSpace(one.MergedAt) != "",
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
