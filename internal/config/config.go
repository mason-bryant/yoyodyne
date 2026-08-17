// Package config loads Yoyodyne's effective configuration from a versioned
// built-in bundle shipped inside the executable and a project configuration
// that overlays it. Projects store only their machine-independent settings and
// sparse agent overrides; the harness supplies everything else, so Yoyodyne is
// usable from a repository that has no access to the Yoyodyne source checkout.
package config

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"yoyodyne/internal/domain"
)

const CurrentVersion = 1

// DefaultSpecifications is where the product manager reads product intent from
// when nothing names another directory. It is the layout the design recommends
// for human-readable product artifacts, so a project that followed that layout
// needs no setting at all.
const DefaultSpecifications = "docs/product"

type Config struct {
	Version int `yaml:"version" json:"version"`
	// Extends names the built-in bundle this configuration inherits from, and
	// is empty for a complete standalone configuration.
	Extends   string                 `yaml:"extends,omitempty" json:"extends,omitempty"`
	Product   Product                `yaml:"product" json:"product"`
	Execution Execution              `yaml:"execution" json:"execution"`
	Approvals Approvals              `yaml:"approvals" json:"approvals"`
	Checks    []string               `yaml:"checks" json:"checks"`
	Agents    map[string]AgentConfig `yaml:"agents" json:"agents"`
}

type Product struct {
	ID           domain.ProductID    `yaml:"id" json:"id"`
	RepositoryID domain.RepositoryID `yaml:"repository_id,omitempty" json:"repository_id,omitempty"`
	Repository   string              `yaml:"repository" json:"repository"`
	// Specifications is the directory the product manager reads product intent
	// from, relative to the repository root. It is the whole of what that role
	// is shown about the product, so it is confined to the repository: a path
	// that escapes it would put arbitrary text in front of the role that decides
	// what the product is for.
	Specifications string `yaml:"specifications" json:"specifications"`
}

type Execution struct {
	MaxConcurrentDevelopers    int    `yaml:"max_concurrent_developers" json:"max_concurrent_developers"`
	RepairAttemptsBeforeReplan int    `yaml:"repair_attempts_before_replan" json:"repair_attempts_before_replan"`
	WorktreeRoot               string `yaml:"worktree_root" json:"worktree_root"`
	// Remote names the Git remote publishing pushes to and opens pull requests
	// against. It is only consulted when `approvals.publishing` is automatic, and
	// a repository that has no remote by this name publishes nothing rather than
	// failing.
	Remote string `yaml:"remote" json:"remote"`
	// UsageLimitMaxPause bounds how long a run may wait for an exhausted
	// provider usage limit to reset. A reset further away than this is treated
	// as no usable reset time at all: the run stops and records a blocker
	// instead of sleeping on it. Zero disables waiting entirely, so every
	// exhausted limit blocks immediately.
	UsageLimitMaxPause Duration `yaml:"usage_limit_max_pause" json:"usage_limit_max_pause"`
	// UsageLimitInProcessPause is how much of that bound a run will spend
	// sleeping inside this process. A shorter wait is simply waited out; a
	// longer one exits with the run still in flight and its deadline recorded,
	// so a later invocation resumes it rather than holding a process open for
	// hours. It is never larger than UsageLimitMaxPause in effect, because a
	// pause beyond that bound is refused before either path is chosen.
	UsageLimitInProcessPause Duration `yaml:"usage_limit_in_process_pause" json:"usage_limit_in_process_pause"`
	// UsageLimitUnknownResetPause is how long a run waits before asking again
	// when the provider reports an exhausted limit without naming a reset time.
	// That is not the same as having no capacity: the monthly overage allowance
	// reports this way while the ordinary rolling window keeps resetting on its
	// usual schedule, so the limit is waitable and simply carries no deadline.
	// The wait still spends UsageLimitMaxPause, so a provider that keeps
	// refusing walks into that bound rather than polling forever.
	UsageLimitUnknownResetPause Duration `yaml:"usage_limit_unknown_reset_pause" json:"usage_limit_unknown_reset_pause"`
}

const (
	// defaultRemote is the remote publishing uses when nothing names another. A
	// repository with no remote by this name is not an error: it simply publishes
	// nothing and behaves exactly as a local-only project does.
	defaultRemote = "origin"
	// defaultUsageLimitMaxPause covers the provider's five-hour limit with slack
	// and stops short of its seven-day one. A run that would have to sleep for
	// days is not a run that should be sleeping: it stops and records a blocker,
	// so the capacity problem reaches a person instead of a timer.
	defaultUsageLimitMaxPause = Duration(6 * time.Hour)
	// defaultUsageLimitInProcessPause equals the maximum pause, so by default
	// every wait the harness will take at all is taken in this process and the
	// run continues on its own — which is what waiting out a usage limit is for.
	// Lowering it trades that for a run that exits with its deadline recorded
	// and is resumed by a later invocation.
	defaultUsageLimitInProcessPause = defaultUsageLimitMaxPause
	// defaultUsageLimitUnknownResetPause is how long to wait before asking again
	// when no reset time was named. Short enough to resume soon after a window
	// rolls, long enough not to hammer a provider that is refusing.
	defaultUsageLimitUnknownResetPause = Duration(30 * time.Minute)
)

type Approvals struct {
	Brief       domain.ApprovalMode `yaml:"brief" json:"brief"`
	Goals       domain.ApprovalMode `yaml:"goals" json:"goals"`
	Designs     domain.ApprovalMode `yaml:"designs" json:"designs"`
	Integration domain.ApprovalMode `yaml:"integration" json:"integration"`
	// Publishing decides whether the harness pushes a run's branch and opens the
	// pull request its reviewer's verdict merges. It sits beside integration
	// because publishing has the wider blast radius of the two: integration moves
	// a local branch, publishing puts the work somewhere other people see it.
	// `human` leaves pushing and pull requests to the operator, which is what a
	// project gets until it opts in.
	Publishing domain.ApprovalMode `yaml:"publishing" json:"publishing"`
}

type AgentConfig struct {
	Role    domain.AgentRole `yaml:"role" json:"role"`
	Backend domain.Backend   `yaml:"backend" json:"backend"`
	// Model is the required provider model selector for every instance of this
	// agent. There is no implicit harness default: a family alias such as
	// "opus" intentionally floats to the backend's current default for that
	// family, while an exact provider identifier pins a version.
	Model     string `yaml:"model" json:"model"`
	Instances int    `yaml:"instances,omitempty" json:"instances"`
	// Persona is the resolved role guidance handed to this agent's prompt. It
	// may specialize how an agent works; it can never remove a harness
	// invariant, which is why the immutable contracts stay in Go.
	Persona Persona `yaml:"persona,omitempty" json:"persona,omitempty"`
}

// MaxPersonaBytes bounds a persona so role guidance stays guidance rather than
// an unbounded document smuggled into every prompt.
const MaxPersonaBytes = 32 << 10

// Persona is one resolved persona: the declared revision, the path it was
// declared with, where the text was actually read from, and the text itself.
// Text is excluded from serialized diagnostics, which report its size instead,
// so `config show` stays readable.
type Persona struct {
	Version string `yaml:"version,omitempty" json:"version,omitempty"`
	Path    string `yaml:"path,omitempty" json:"path,omitempty"`
	Source  string `yaml:"source,omitempty" json:"source,omitempty"`
	Bytes   int    `yaml:"bytes,omitempty" json:"bytes,omitempty"`
	Text    string `yaml:"-" json:"-"`
}

// Defined reports whether an agent declares a persona at all. Agents in legacy
// complete configurations may declare none, and prompt construction then falls
// back to the immutable harness contract alone.
func (p Persona) Defined() bool {
	return strings.TrimSpace(p.Version) != "" || strings.TrimSpace(p.Path) != "" || strings.TrimSpace(p.Text) != ""
}

func (c Config) Validate() error {
	var problems []string

	if c.Version != CurrentVersion {
		problems = append(problems, fmt.Sprintf("version must be %d", CurrentVersion))
	}
	if err := domain.ValidateIdentifier("product id", string(c.Product.ID)); err != nil {
		problems = append(problems, err.Error())
	}
	if err := domain.ValidateIdentifier("repository id", string(c.Product.RepositoryID)); err != nil {
		problems = append(problems, err.Error())
	}
	if strings.TrimSpace(c.Product.Repository) == "" {
		problems = append(problems, "product repository is required")
	}
	if err := validateSpecificationsDirectory(c.Product.Specifications); err != nil {
		problems = append(problems, err.Error())
	}
	if c.Execution.MaxConcurrentDevelopers < 1 {
		problems = append(problems, "max_concurrent_developers must be at least 1")
	}
	if c.Execution.RepairAttemptsBeforeReplan < 0 {
		problems = append(problems, "repair_attempts_before_replan cannot be negative")
	}
	if strings.TrimSpace(c.Execution.WorktreeRoot) == "" {
		problems = append(problems, "worktree_root is required")
	}
	if err := validateRemoteName(c.Execution.Remote); err != nil {
		problems = append(problems, err.Error())
	}
	// Zero is a deliberate choice — never wait, block on every exhausted limit —
	// so only a negative bound, which describes no wait anybody could take, is
	// refused.
	if c.Execution.UsageLimitMaxPause < 0 {
		problems = append(problems, "usage_limit_max_pause cannot be negative")
	}
	if c.Execution.UsageLimitUnknownResetPause <= 0 {
		problems = append(problems, "execution.usage_limit_unknown_reset_pause must be positive")
	}
	if c.Execution.UsageLimitInProcessPause < 0 {
		problems = append(problems, "usage_limit_in_process_pause cannot be negative")
	}

	approvalValues := []struct {
		name string
		mode domain.ApprovalMode
	}{
		{name: "brief", mode: c.Approvals.Brief},
		{name: "goals", mode: c.Approvals.Goals},
		{name: "designs", mode: c.Approvals.Designs},
		{name: "integration", mode: c.Approvals.Integration},
		{name: "publishing", mode: c.Approvals.Publishing},
	}
	for _, approval := range approvalValues {
		if !approval.mode.Valid() {
			problems = append(problems, fmt.Sprintf("approval %s must be %q or %q", approval.name, domain.ApprovalHuman, domain.ApprovalAutomatic))
		}
	}

	if len(c.Agents) == 0 {
		problems = append(problems, "at least one agent is required")
	}

	agentNames := make([]string, 0, len(c.Agents))
	for name := range c.Agents {
		agentNames = append(agentNames, name)
	}
	sort.Strings(agentNames)

	developers := 0
	reviewers := 0
	for _, name := range agentNames {
		agent := c.Agents[name]
		if err := domain.ValidateIdentifier("agent name", name); err != nil {
			problems = append(problems, err.Error())
		}
		if strings.TrimSpace(string(agent.Role)) == "" {
			problems = append(problems, fmt.Sprintf("agent %q role is required", name))
		}
		if !agent.Backend.Valid() {
			problems = append(problems, fmt.Sprintf("agent %q has unsupported backend %q", name, agent.Backend))
		} else if !agent.Backend.SupportsRole(agent.Role) {
			problems = append(problems, fmt.Sprintf("backend %q does not support role %q for agent %q", agent.Backend, agent.Role, name))
		}
		// Every executable agent declares its own selector; the harness never
		// falls back to a provider default nobody chose or recorded.
		if err := validateModelSelector(agent.Model); err != nil {
			problems = append(problems, fmt.Sprintf("agent %q %s", name, err))
		}
		if agent.Instances < 1 {
			problems = append(problems, fmt.Sprintf("agent %q instances must be at least 1", name))
		}
		problems = append(problems, agent.Persona.problems(name)...)
		if agent.Role == domain.RoleDeveloper {
			developers += agent.Instances
		}
		if agent.Role == domain.RoleReviewer {
			reviewers += agent.Instances
		}
	}
	if developers == 0 {
		problems = append(problems, "at least one developer agent is required")
	}
	if developers > 0 && c.Execution.MaxConcurrentDevelopers > developers {
		problems = append(problems, "max_concurrent_developers cannot exceed configured developer instances")
	}

	for index, check := range c.Checks {
		if strings.TrimSpace(check) == "" {
			problems = append(problems, fmt.Sprintf("check %d cannot be empty", index))
		}
	}
	if c.Approvals.Integration == domain.ApprovalAutomatic {
		if len(c.Checks) == 0 {
			problems = append(problems, "automatic integration requires at least one check")
		}
		if reviewers == 0 {
			problems = append(problems, "automatic integration requires at least one reviewer agent")
		}
	}

	if len(problems) > 0 {
		return ValidationError{Problems: problems}
	}
	return nil
}

// problems reports what makes a declared persona unusable. A persona that
// resolved to nothing is a configuration error rather than a silent fallback:
// an agent whose configured guidance vanished is not the agent that was
// configured.
func (p Persona) problems(agentName string) []string {
	if !p.Defined() {
		return nil
	}
	var problems []string
	if strings.TrimSpace(p.Version) == "" {
		problems = append(problems, fmt.Sprintf("agent %q persona version is required", agentName))
	}
	if strings.TrimSpace(p.Path) == "" {
		problems = append(problems, fmt.Sprintf("agent %q persona path is required", agentName))
	}
	if strings.TrimSpace(p.Text) == "" {
		problems = append(problems, fmt.Sprintf("agent %q persona %q is empty", agentName, p.Path))
	}
	if len(p.Text) > MaxPersonaBytes {
		problems = append(problems, fmt.Sprintf("agent %q persona %q is %d bytes, limit is %d", agentName, p.Path, len(p.Text), MaxPersonaBytes))
	}
	return problems
}

// validateSpecificationsDirectory keeps the product manager's inputs inside the
// repository. The path is resolved against the repository root, so an absolute
// path or one that climbs out of it names something the repository does not
// contain and is refused rather than read.
func validateSpecificationsDirectory(directory string) error {
	trimmed := strings.TrimSpace(directory)
	if trimmed == "" {
		return errors.New("product specifications directory is required")
	}
	clean := filepath.Clean(trimmed)
	if filepath.IsAbs(trimmed) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("product specifications %q must be a directory inside the repository", directory)
	}
	return nil
}

// remoteNamePattern keeps a configured remote a plain remote name. The harness
// puts it on a `git push` command line, so anything that could read as an
// option, a path, or a refspec is refused rather than passed along.
var remoteNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func validateRemoteName(remote string) error {
	trimmed := strings.TrimSpace(remote)
	if trimmed == "" {
		return errors.New("remote is required")
	}
	if !remoteNamePattern.MatchString(trimmed) {
		return fmt.Errorf("remote %q must be a plain Git remote name", remote)
	}
	return nil
}

// MaxModelSelectorBytes bounds a configured selector so it stays a model name
// rather than an argument smuggled onto a provider command line.
const MaxModelSelectorBytes = 128

// ValidateModelSelector reports whether a configured model selector is usable.
// It deliberately accepts both floating family aliases and pinned identifiers,
// and rejects only what cannot name a model.
func ValidateModelSelector(model string) error {
	return validateModelSelector(model)
}

func validateModelSelector(model string) error {
	trimmed := strings.TrimSpace(model)
	switch {
	case trimmed == "":
		return errors.New("model selector is required; there is no implicit harness default")
	case len(trimmed) > MaxModelSelectorBytes:
		return fmt.Errorf("model selector is %d bytes, limit is %d", len(trimmed), MaxModelSelectorBytes)
	case strings.IndexFunc(trimmed, unicode.IsSpace) >= 0 || strings.HasPrefix(trimmed, "-"):
		return fmt.Errorf("model selector %q must be a single model name", model)
	}
	return nil
}

type ValidationError struct {
	Problems []string
}

func (e ValidationError) Error() string {
	return "invalid configuration: " + strings.Join(e.Problems, "; ")
}
