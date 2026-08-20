package config

import (
	"errors"
	"fmt"
	"io"

	"go.yaml.in/yaml/v3"

	"github.com/mason-bryant/yoyodyne/internal/domain"
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
	Triage    *triageDocument          `yaml:"triage"`
	Approvals *approvalsDocument       `yaml:"approvals"`
	Checks    *[]string                `yaml:"checks"`
	Agents    map[string]agentDocument `yaml:"agents"`
	Slack     *slackDocument           `yaml:"slack"`
}

type productDocument struct {
	ID             *domain.ProductID    `yaml:"id"`
	RepositoryID   *domain.RepositoryID `yaml:"repository_id"`
	Repository     *string              `yaml:"repository"`
	Specifications *string              `yaml:"specifications"`
	Invariants     *string              `yaml:"invariants"`
	Designs        *string              `yaml:"designs"`
	Decisions      *string              `yaml:"decisions"`
}

type executionDocument struct {
	MaxConcurrentDevelopers                *int      `yaml:"max_concurrent_developers"`
	RepairAttemptsBeforeReplan             *int      `yaml:"repair_attempts_before_replan"`
	IntegrationRetriesBeforeReconciliation *int      `yaml:"integration_retries_before_reconciliation"`
	TransientRelaunchesBeforeBlocking      *int      `yaml:"transient_relaunches_before_blocking"`
	WorktreeRoot                           *string   `yaml:"worktree_root"`
	Remote                                 *string   `yaml:"remote"`
	UsageLimitMaxPause                     *Duration `yaml:"usage_limit_max_pause"`
	UsageLimitInProcessPause               *Duration `yaml:"usage_limit_in_process_pause"`
	UsageLimitUnknownResetPause            *Duration `yaml:"usage_limit_unknown_reset_pause"`
	ServerOverloadPause                    *Duration `yaml:"server_overload_pause"`
	CheckTimeout                           *Duration `yaml:"check_timeout"`
}

type triageDocument struct {
	StuckMergeAge   *Duration `yaml:"stuck_merge_age"`
	ReviewRoundsCap *int      `yaml:"review_rounds_cap"`
	// RepairGrantAttempts is absent from most files. A layer that does not
	// supply it leaves the grant to follow execution.repair_attempts_before_replan,
	// which is a derivation rather than an inherited value: it tracks whatever
	// the effective repair budget turns out to be rather than whatever it was
	// when some layer underneath was written.
	RepairGrantAttempts *int `yaml:"repair_grant_attempts"`
}

type approvalsDocument struct {
	Brief   *domain.ApprovalMode `yaml:"brief"`
	Goals   *domain.ApprovalMode `yaml:"goals"`
	Designs *domain.ApprovalMode `yaml:"designs"`
	// WorkItems is absent from files written before per-item approval became a
	// policy rather than the only behavior. A layer that does not supply it
	// leaves the harness default in place, which is the per-item gate such a
	// file was written for.
	WorkItems   *domain.ApprovalMode `yaml:"work_items"`
	Integration *domain.ApprovalMode `yaml:"integration"`
	Publishing  *domain.ApprovalMode `yaml:"publishing"`
}

type slackDocument struct {
	Enabled *bool   `yaml:"enabled"`
	Channel *string `yaml:"channel"`
	// Operators replaces an inherited allow-list entirely rather than adding to
	// it, for the reason the check list does: an allow-list is a decision about
	// who may steer the harness, and one silently concatenated from two layers
	// is not the list either layer wrote.
	Operators *[]string `yaml:"operators"`
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
