package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"unicode"

	"go.yaml.in/yaml/v3"

	"yoyodyne/internal/domain"
)

const CurrentVersion = 1

type Config struct {
	Version   int                    `yaml:"version" json:"version"`
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
}

type Execution struct {
	MaxConcurrentDevelopers    int    `yaml:"max_concurrent_developers" json:"max_concurrent_developers"`
	RepairAttemptsBeforeReplan int    `yaml:"repair_attempts_before_replan" json:"repair_attempts_before_replan"`
	WorktreeRoot               string `yaml:"worktree_root" json:"worktree_root"`
}

type Approvals struct {
	Brief       domain.ApprovalMode `yaml:"brief" json:"brief"`
	Goals       domain.ApprovalMode `yaml:"goals" json:"goals"`
	Designs     domain.ApprovalMode `yaml:"designs" json:"designs"`
	Integration domain.ApprovalMode `yaml:"integration" json:"integration"`
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
}

type configDocument struct {
	Version   int                      `yaml:"version"`
	Product   Product                  `yaml:"product"`
	Execution executionDocument        `yaml:"execution"`
	Approvals Approvals                `yaml:"approvals"`
	Checks    []string                 `yaml:"checks"`
	Agents    map[string]agentDocument `yaml:"agents"`
}

type executionDocument struct {
	MaxConcurrentDevelopers    *int    `yaml:"max_concurrent_developers"`
	RepairAttemptsBeforeReplan *int    `yaml:"repair_attempts_before_replan"`
	WorktreeRoot               *string `yaml:"worktree_root"`
}

type agentDocument struct {
	Role      domain.AgentRole `yaml:"role"`
	Backend   domain.Backend   `yaml:"backend"`
	Model     string           `yaml:"model"`
	Instances *int             `yaml:"instances,omitempty"`
}

func Load(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open config %q: %w", path, err)
	}
	defer file.Close()

	return Decode(file)
}

func Decode(reader io.Reader) (Config, error) {
	decoder := yaml.NewDecoder(reader)
	decoder.KnownFields(true)

	var document configDocument
	if err := decoder.Decode(&document); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}

	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return Config{}, errors.New("decode config: multiple YAML documents are not supported")
		}
		return Config{}, fmt.Errorf("decode config: %w", err)
	}

	cfg := document.resolve()
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (d configDocument) resolve() Config {
	execution := Execution{
		MaxConcurrentDevelopers:    1,
		RepairAttemptsBeforeReplan: 2,
		WorktreeRoot:               "auto",
	}
	if d.Execution.MaxConcurrentDevelopers != nil {
		execution.MaxConcurrentDevelopers = *d.Execution.MaxConcurrentDevelopers
	}
	if d.Execution.RepairAttemptsBeforeReplan != nil {
		execution.RepairAttemptsBeforeReplan = *d.Execution.RepairAttemptsBeforeReplan
	}
	if d.Execution.WorktreeRoot != nil {
		execution.WorktreeRoot = *d.Execution.WorktreeRoot
	}

	product := d.Product
	if product.RepositoryID == "" {
		product.RepositoryID = domain.RepositoryID(product.ID)
	}

	agents := make(map[string]AgentConfig, len(d.Agents))
	for name, rawAgent := range d.Agents {
		instances := 1
		if rawAgent.Instances != nil {
			instances = *rawAgent.Instances
		}
		agents[name] = AgentConfig{
			Role:      rawAgent.Role,
			Backend:   rawAgent.Backend,
			Model:     strings.TrimSpace(rawAgent.Model),
			Instances: instances,
		}
	}

	return Config{
		Version:   d.Version,
		Product:   product,
		Execution: execution,
		Approvals: d.Approvals,
		Checks:    d.Checks,
		Agents:    agents,
	}
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
	if c.Execution.MaxConcurrentDevelopers < 1 {
		problems = append(problems, "max_concurrent_developers must be at least 1")
	}
	if c.Execution.RepairAttemptsBeforeReplan < 0 {
		problems = append(problems, "repair_attempts_before_replan cannot be negative")
	}
	if strings.TrimSpace(c.Execution.WorktreeRoot) == "" {
		problems = append(problems, "worktree_root is required")
	}

	approvalValues := []struct {
		name string
		mode domain.ApprovalMode
	}{
		{name: "brief", mode: c.Approvals.Brief},
		{name: "goals", mode: c.Approvals.Goals},
		{name: "designs", mode: c.Approvals.Designs},
		{name: "integration", mode: c.Approvals.Integration},
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
