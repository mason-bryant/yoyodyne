package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"go.yaml.in/yaml/v3"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/research"
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
	Exchange  *exchangeDocument        `yaml:"exchange"`
	Research  *researchDocument        `yaml:"research"`
	Approvals *approvalsDocument       `yaml:"approvals"`
	Checks    *[]string                `yaml:"checks"`
	Agents    map[string]agentDocument `yaml:"agents"`
	// Operators replaces an inherited mapping entirely rather than merging into
	// it, for the reason the check list does and the allow-list it absorbed did:
	// who may act is a decision, and a mapping silently assembled from two layers
	// is not the mapping either layer wrote. It decodes straight into the
	// effective type because there is no per-field override to distinguish.
	Operators *map[string]Operator `yaml:"operators"`
	// Accounts replaces an inherited mapping entirely rather than merging into
	// it, for the reason the operators mapping does: what accounts exist is one
	// statement, and a mapping half from a bundle and half from a project is not
	// the set of accounts either layer named. It decodes straight into the
	// effective type because there is no per-field override to distinguish.
	Accounts *map[string]Account `yaml:"accounts"`
	// Providers replaces an inherited mapping entirely rather than merging into
	// it, for the reason the accounts mapping does: which providers a project may
	// name is one statement, and a provider assembled half from a bundle and half
	// from a project is a dialect nobody wrote. It decodes straight into the
	// effective type because there is no per-field override to distinguish.
	Providers *map[string]backend.ProviderPlugin `yaml:"providers"`
	Slack     *slackDocument                     `yaml:"slack"`
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
	PushRemote                             *string   `yaml:"push_remote"`
	UsageLimitMaxPause                     *Duration `yaml:"usage_limit_max_pause"`
	UsageLimitInProcessPause               *Duration `yaml:"usage_limit_in_process_pause"`
	UsageLimitUnknownResetPause            *Duration `yaml:"usage_limit_unknown_reset_pause"`
	ServerOverloadPause                    *Duration `yaml:"server_overload_pause"`
	CheckTimeout                           *Duration `yaml:"check_timeout"`
	WorkPoll                               *Duration `yaml:"work_poll"`
	BlockedRunsBeforeIntakeHold            *int      `yaml:"blocked_runs_before_intake_hold"`
	// DeclarativeDelivery is absent from every file written before it existed and
	// from every file whose project is content with the default. A layer that does
	// not supply it leaves the harness default in force, which is the declarative
	// path; a layer that writes `false` is a project rolling back to the legacy
	// one.
	DeclarativeDelivery *bool `yaml:"declarative_delivery"`
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

type exchangeDocument struct {
	MaxRounds *int `yaml:"max_rounds"`
}

// researchDocument is absent from every file written before research existed,
// which is what leaves the capability off for them: a layer supplying no sources
// permits none, and that is the state a project stays in until its operator
// names one.
type researchDocument struct {
	// Sources replaces an inherited list wholesale rather than adding to it, the
	// way the check list and the work-item exemptions do: what the harness may
	// reach outside this machine is one statement, and a list half from a bundle
	// and half from a project is a reach nobody decided.
	Sources           *[]research.Source `yaml:"sources"`
	MaxQueriesPerTurn *int               `yaml:"max_queries_per_turn"`
	Timeout           *Duration          `yaml:"timeout"`
}

type approvalsDocument struct {
	Brief   *domain.ApprovalMode `yaml:"brief"`
	Goals   *domain.ApprovalMode `yaml:"goals"`
	Designs *domain.ApprovalMode `yaml:"designs"`
	// WorkItems is absent from files written before per-item approval became a
	// policy rather than the only behavior. A layer that does not supply it
	// leaves the harness default in place, which is the per-item gate such a
	// file was written for.
	WorkItems *domain.ApprovalMode `yaml:"work_items"`
	// WorkItemExemptions replaces an inherited list wholesale rather than adding
	// to it, the way the check list does and unlike the Slack avatars: the list is
	// one statement about how far a project's gate comes down, and half of one
	// operator's answer joined to half of another's is a policy nobody decided.
	WorkItemExemptions *[]domain.WorkItemClass `yaml:"work_item_exemptions"`
	Integration        *domain.ApprovalMode    `yaml:"integration"`
	Publishing         *domain.ApprovalMode    `yaml:"publishing"`
}

// slackDocument deliberately has no operators key. A file that still carries one
// is refused by the decoder, which names the key and the line it is on, because
// who may steer the harness now lives in the top-level operators mapping — and
// an allow-list left under `slack` would be an authority decision nothing reads.
type slackDocument struct {
	Enabled *bool              `yaml:"enabled"`
	Channel *string            `yaml:"channel"`
	Avatars *map[string]string `yaml:"avatars"`
}

type agentDocument struct {
	Role    *domain.AgentRole `yaml:"role"`
	Backend *domain.Backend   `yaml:"backend"`
	Model   *string           `yaml:"model"`
	// Account is absent from most files. A layer that does not supply it leaves
	// the agent assigned to the project's single account, which is a derivation
	// rather than an inherited value: it follows whatever account the effective
	// mapping turns out to declare rather than whatever some layer underneath was
	// written against.
	Account   *string `yaml:"account"`
	Instances *int    `yaml:"instances"`
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
	return d.Role != nil || d.Backend != nil || d.Model != nil || d.Account != nil || d.Instances != nil || d.Persona != nil
}

type personaDocument struct {
	Version *string `yaml:"version"`
	Path    *string `yaml:"path"`
}

func decodeDocument(reader io.Reader) (configDocument, error) {
	// The source is held rather than streamed so a file that fails the strict
	// decode can be read a second time to tell an unknown key from a retired one.
	source, err := io.ReadAll(reader)
	if err != nil {
		return configDocument{}, fmt.Errorf("decode config: %w", err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(source))
	decoder.KnownFields(true)

	var document configDocument
	if err := decoder.Decode(&document); err != nil {
		if errors.Is(err, io.EOF) {
			return configDocument{}, errors.New("decode config: configuration is empty")
		}
		if migration := retiredSlackOperators(source); migration != "" {
			return configDocument{}, errors.New(migration)
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

// retiredSlackOperators reports the migration a file still carrying the old
// allow-list needs, and the empty string for a file that failed to decode for
// any other reason. The decoder's own answer for the key is "field operators not
// found", which is true and useless: somebody who wrote that key wrote a
// decision about who may steer the harness, so the refusal says where that
// decision lives now rather than leaving them to find it.
//
// It runs only after the strict decode has already failed, and it is lenient
// about everything else in the file, because the only question left is whether
// the reason is worth a better answer.
func retiredSlackOperators(source []byte) string {
	var retired struct {
		Slack *struct {
			Operators *[]string `yaml:"operators"`
		} `yaml:"slack"`
	}
	if err := yaml.Unmarshal(source, &retired); err != nil {
		return ""
	}
	if retired.Slack == nil || retired.Slack.Operators == nil {
		return ""
	}
	return fmt.Sprintf(`decode config: slack.operators has moved to the top-level operators mapping, `+
		`where each human binds their identifier namespaces and the grants attach to the human rather than to any one of them. `+
		`The Slack allow-list is derived from it now — the humans granted %q who bound a slack_member_id — so delete the key and write, beside "slack":

operators:
  <your-name>:
    slack_member_id: %s
    grants:
      - %s
`, GrantDirectWork, exampleSlackMemberID, GrantDirectWork)
}

// exampleSlackMemberID is the shape of a member id, for a refusal that shows the
// entry to write rather than describing it.
const exampleSlackMemberID = "U0123456789"
