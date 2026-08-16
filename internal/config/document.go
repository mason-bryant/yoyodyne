package config

import (
	"errors"
	"fmt"
	"io"

	"go.yaml.in/yaml/v3"

	"yoyodyne/internal/domain"
)

// configDocument is the on-disk shape of a configuration layer. Every field is
// a pointer so a layer can distinguish "not supplied, inherit it" from
// "supplied, replace it". Unknown keys are rejected by the decoder rather than
// silently ignored, so a typo in an override fails closed instead of leaving
// the inherited value quietly in place.
type configDocument struct {
	Version   *int                     `yaml:"version"`
	Extends   *string                  `yaml:"extends"`
	Product   *productDocument         `yaml:"product"`
	Execution *executionDocument       `yaml:"execution"`
	Approvals *approvalsDocument       `yaml:"approvals"`
	Checks    *[]string                `yaml:"checks"`
	Agents    map[string]agentDocument `yaml:"agents"`
}

type productDocument struct {
	ID           *domain.ProductID    `yaml:"id"`
	RepositoryID *domain.RepositoryID `yaml:"repository_id"`
	Repository   *string              `yaml:"repository"`
}

type executionDocument struct {
	MaxConcurrentDevelopers    *int    `yaml:"max_concurrent_developers"`
	RepairAttemptsBeforeReplan *int    `yaml:"repair_attempts_before_replan"`
	WorktreeRoot               *string `yaml:"worktree_root"`
}

type approvalsDocument struct {
	Brief       *domain.ApprovalMode `yaml:"brief"`
	Goals       *domain.ApprovalMode `yaml:"goals"`
	Designs     *domain.ApprovalMode `yaml:"designs"`
	Integration *domain.ApprovalMode `yaml:"integration"`
}

type agentDocument struct {
	Role      *domain.AgentRole `yaml:"role"`
	Backend   *domain.Backend   `yaml:"backend"`
	Model     *string           `yaml:"model"`
	Instances *int              `yaml:"instances"`
	// Persona replaces an inherited persona completely rather than merging into
	// it, because half of one persona and half of another is guidance nobody
	// wrote.
	Persona *personaDocument `yaml:"persona"`
	// Disabled removes an inherited agent. It is explicit so a project never
	// loses an agent by accidentally omitting it.
	Disabled *bool `yaml:"disabled"`
}

// overridesFields reports whether an agent entry supplies anything besides
// disabled, which is how a contradictory "remove it and also configure it"
// entry is detected.
func (d agentDocument) overridesFields() bool {
	return d.Role != nil || d.Backend != nil || d.Model != nil || d.Instances != nil || d.Persona != nil
}

type personaDocument struct {
	Version *string `yaml:"version"`
	Path    *string `yaml:"path"`
}

func decodeDocument(reader io.Reader) (configDocument, error) {
	decoder := yaml.NewDecoder(reader)
	decoder.KnownFields(true)

	var document configDocument
	if err := decoder.Decode(&document); err != nil {
		if errors.Is(err, io.EOF) {
			return configDocument{}, errors.New("decode config: configuration is empty")
		}
		return configDocument{}, fmt.Errorf("decode config: %w", err)
	}

	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return configDocument{}, errors.New("decode config: multiple YAML documents are not supported")
		}
		return configDocument{}, fmt.Errorf("decode config: %w", err)
	}
	return document, nil
}
