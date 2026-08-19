package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/domain"
)

const (
	// OriginDefault marks a value no layer supplied, which the harness filled
	// in itself.
	OriginDefault = "harness-default"
	// OriginDerived marks a value computed from another configured value.
	OriginDerived = "derived:product.id"
	// OriginInput marks a configuration decoded from a stream rather than read
	// from a project file.
	OriginInput = "configuration input"
)

// Resolved is an effective configuration together with the provenance needed to
// explain it: which layers were applied, and where every effective value came
// from. Inheritance is only debuggable if the answer to "why is this value
// what it is" is recorded rather than reconstructed.
type Resolved struct {
	Config  Config            `json:"config"`
	Path    string            `json:"path,omitempty"`
	Sources []string          `json:"sources"`
	Origins map[string]string `json:"origins"`
}

// OriginKeys lists every recorded key in a stable order.
func (r Resolved) OriginKeys() []string {
	keys := make([]string, 0, len(r.Origins))
	for key := range r.Origins {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// layer is one configuration document plus where it came from and how its
// persona paths resolve.
type layer struct {
	origin   string
	document configDocument
	personas personaLoader
}

// Load reads and validates the effective configuration at a path.
func Load(path string) (Config, error) {
	resolved, err := LoadResolved(path)
	return resolved.Config, err
}

// LoadResolved reads a project configuration, overlays it on the bundle it
// extends, validates the result, and reports the provenance of every value.
func LoadResolved(path string) (Resolved, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return Resolved{}, fmt.Errorf("resolve config path %q: %w", path, err)
	}
	file, err := os.Open(absolute)
	if err != nil {
		return Resolved{}, fmt.Errorf("open config %q: %w", path, err)
	}
	defer file.Close()

	document, err := decodeDocument(file)
	if err != nil {
		return Resolved{}, err
	}
	resolved, err := resolveDocument(document, absolute, directoryPersonaLoader{root: personaDirectory(absolute)})
	if err != nil {
		return Resolved{}, err
	}
	resolved.Path = absolute
	if err := resolved.Config.Validate(); err != nil {
		return Resolved{}, err
	}
	return resolved, nil
}

// Decode reads a complete configuration from a stream. Personas declared by the
// stream itself cannot be resolved, because a stream has no project directory
// to resolve them against; inherited built-in personas still load.
func Decode(reader io.Reader) (Config, error) {
	resolved, err := DecodeResolved(reader)
	return resolved.Config, err
}

// DecodeResolved is Decode with the provenance of every effective value.
func DecodeResolved(reader io.Reader) (Resolved, error) {
	document, err := decodeDocument(reader)
	if err != nil {
		return Resolved{}, err
	}
	resolved, err := resolveDocument(document, OriginInput, unavailablePersonaLoader{
		reason: "the configuration was decoded from a stream and has no project " + DirectoryName + " directory",
	})
	if err != nil {
		return Resolved{}, err
	}
	if err := resolved.Config.Validate(); err != nil {
		return Resolved{}, err
	}
	return resolved, nil
}

func resolveDocument(document configDocument, origin string, personas personaLoader) (Resolved, error) {
	// The project layer states its own schema version even when the bundle it
	// extends declares one. An inherited version would let a file written
	// against a different schema load as whatever the bundle happened to say,
	// which is the one thing the version is supposed to prevent.
	if document.Version == nil {
		return Resolved{}, ValidationError{Problems: []string{
			fmt.Sprintf("version must be %d and is required in the project configuration; it is not inherited from an extended bundle", CurrentVersion),
		}}
	}

	layers := make([]layer, 0, 2)
	if document.Extends != nil {
		inherited, err := loadBuiltinBundle(*document.Extends)
		if err != nil {
			return Resolved{}, err
		}
		layers = append(layers, layer{origin: inherited.name, document: inherited.document, personas: inherited.personas})
	}
	layers = append(layers, layer{origin: origin, document: document, personas: personas})
	return resolveLayers(layers)
}

func resolveLayers(layers []layer) (Resolved, error) {
	state := newResolution()
	sources := make([]string, 0, len(layers))
	for _, applied := range layers {
		if err := state.apply(applied); err != nil {
			return Resolved{}, err
		}
		sources = append(sources, applied.origin)
	}
	return state.finish(sources)
}

type resolution struct {
	config  Config
	origins map[string]string
	agents  map[string]*agentResolution
}

type agentResolution struct {
	config  AgentConfig
	origins map[string]string
	persona *personaReference
}

type personaReference struct {
	version string
	path    string
	loader  personaLoader
}

func newResolution() *resolution {
	return &resolution{
		config: Config{
			// The artifact directories are harness defaults rather than bundle
			// values for the same reason the rest of `product` is absent from the
			// bundle: they describe the project. A project that follows the
			// recommended layout never writes any of them down.
			Product: Product{
				Specifications: DefaultSpecifications,
				Invariants:     DefaultInvariants,
				Designs:        DefaultDesigns,
				Decisions:      DefaultDecisions,
			},
			Execution: Execution{
				MaxConcurrentDevelopers:    1,
				RepairAttemptsBeforeReplan: 2,
				// Two retries is the same shape of bound as the repair one: enough
				// for a target that moved under a run to be caught up with, and far
				// short of a run that keeps re-reviewing a change it can never land.
				IntegrationRetriesBeforeReconciliation: 2,
				WorktreeRoot:                           "auto",
				Remote:                                 defaultRemote,
				UsageLimitMaxPause:                     defaultUsageLimitMaxPause,
				UsageLimitInProcessPause:               defaultUsageLimitInProcessPause,
				UsageLimitUnknownResetPause:            defaultUsageLimitUnknownResetPause,
				ServerOverloadPause:                    defaultServerOverloadPause,
				CheckTimeout:                           defaultCheckTimeout,
			},
			// Publishing is the one approval with a harness default, because it is
			// the one that was added after configurations existed. A file written
			// before it keeps the behavior it was written for — the harness
			// publishes nothing — rather than failing to load for not mentioning a
			// key that did not exist when it was written.
			Approvals: Approvals{Publishing: domain.ApprovalHuman},
		},
		origins: map[string]string{
			"product.specifications":                              OriginDefault,
			"product.invariants":                                  OriginDefault,
			"product.designs":                                     OriginDefault,
			"product.decisions":                                   OriginDefault,
			"approvals.publishing":                                OriginDefault,
			"execution.max_concurrent_developers":                 OriginDefault,
			"execution.repair_attempts_before_replan":             OriginDefault,
			"execution.integration_retries_before_reconciliation": OriginDefault,
			"execution.worktree_root":                             OriginDefault,
			"execution.remote":                                    OriginDefault,
			"execution.usage_limit_max_pause":                     OriginDefault,
			"execution.usage_limit_in_process_pause":              OriginDefault,
			"execution.check_timeout":                             OriginDefault,
		},
		agents: map[string]*agentResolution{},
	}
}

func (r *resolution) apply(applied layer) error {
	document := applied.document
	setValue(r.origins, "version", document.Version, &r.config.Version, applied.origin)
	setValue(r.origins, "extends", document.Extends, &r.config.Extends, applied.origin)

	if product := document.Product; product != nil {
		setValue(r.origins, "product.id", product.ID, &r.config.Product.ID, applied.origin)
		setValue(r.origins, "product.repository_id", product.RepositoryID, &r.config.Product.RepositoryID, applied.origin)
		setValue(r.origins, "product.repository", product.Repository, &r.config.Product.Repository, applied.origin)
		setValue(r.origins, "product.specifications", product.Specifications, &r.config.Product.Specifications, applied.origin)
		setValue(r.origins, "product.invariants", product.Invariants, &r.config.Product.Invariants, applied.origin)
		setValue(r.origins, "product.designs", product.Designs, &r.config.Product.Designs, applied.origin)
		setValue(r.origins, "product.decisions", product.Decisions, &r.config.Product.Decisions, applied.origin)
	}
	if execution := document.Execution; execution != nil {
		setValue(r.origins, "execution.max_concurrent_developers", execution.MaxConcurrentDevelopers, &r.config.Execution.MaxConcurrentDevelopers, applied.origin)
		setValue(r.origins, "execution.repair_attempts_before_replan", execution.RepairAttemptsBeforeReplan, &r.config.Execution.RepairAttemptsBeforeReplan, applied.origin)
		setValue(r.origins, "execution.integration_retries_before_reconciliation", execution.IntegrationRetriesBeforeReconciliation, &r.config.Execution.IntegrationRetriesBeforeReconciliation, applied.origin)
		setValue(r.origins, "execution.worktree_root", execution.WorktreeRoot, &r.config.Execution.WorktreeRoot, applied.origin)
		setValue(r.origins, "execution.remote", execution.Remote, &r.config.Execution.Remote, applied.origin)
		setValue(r.origins, "execution.usage_limit_max_pause", execution.UsageLimitMaxPause, &r.config.Execution.UsageLimitMaxPause, applied.origin)
		setValue(r.origins, "execution.usage_limit_in_process_pause", execution.UsageLimitInProcessPause, &r.config.Execution.UsageLimitInProcessPause, applied.origin)
		setValue(r.origins, "execution.usage_limit_unknown_reset_pause", execution.UsageLimitUnknownResetPause, &r.config.Execution.UsageLimitUnknownResetPause, applied.origin)
		setValue(r.origins, "execution.server_overload_pause", execution.ServerOverloadPause, &r.config.Execution.ServerOverloadPause, applied.origin)
		setValue(r.origins, "execution.check_timeout", execution.CheckTimeout, &r.config.Execution.CheckTimeout, applied.origin)
	}
	if approvals := document.Approvals; approvals != nil {
		setValue(r.origins, "approvals.brief", approvals.Brief, &r.config.Approvals.Brief, applied.origin)
		setValue(r.origins, "approvals.goals", approvals.Goals, &r.config.Approvals.Goals, applied.origin)
		setValue(r.origins, "approvals.designs", approvals.Designs, &r.config.Approvals.Designs, applied.origin)
		setValue(r.origins, "approvals.integration", approvals.Integration, &r.config.Approvals.Integration, applied.origin)
		setValue(r.origins, "approvals.publishing", approvals.Publishing, &r.config.Approvals.Publishing, applied.origin)
	}
	if slack := document.Slack; slack != nil {
		setValue(r.origins, "slack.enabled", slack.Enabled, &r.config.Slack.Enabled, applied.origin)
		setValue(r.origins, "slack.channel", slack.Channel, &r.config.Slack.Channel, applied.origin)
		if slack.Operators != nil {
			r.config.Slack.Operators = append([]string(nil), (*slack.Operators)...)
			r.origins["slack.operators"] = applied.origin
		}
	}
	// A supplied check list replaces the inherited one entirely: checks are the
	// gate on integration, and a silently concatenated list is not the gate
	// either layer described.
	if document.Checks != nil {
		r.config.Checks = append([]string(nil), (*document.Checks)...)
		r.origins["checks"] = applied.origin
	}

	for _, name := range sortedAgentNames(document.Agents) {
		if err := r.applyAgent(name, document.Agents[name], applied); err != nil {
			return err
		}
	}
	return nil
}

func (r *resolution) applyAgent(name string, document agentDocument, applied layer) error {
	if document.Disabled != nil && *document.Disabled {
		if document.overridesFields() {
			return fmt.Errorf("agent %q is disabled and also configured; remove one of them", name)
		}
		if _, inherited := r.agents[name]; !inherited {
			return fmt.Errorf("agent %q is disabled but no inherited agent by that name exists", name)
		}
		delete(r.agents, name)
		return nil
	}

	agent, exists := r.agents[name]
	if !exists {
		agent = &agentResolution{origins: map[string]string{}}
		r.agents[name] = agent
	}
	setValue(agent.origins, "role", document.Role, &agent.config.Role, applied.origin)
	setValue(agent.origins, "backend", document.Backend, &agent.config.Backend, applied.origin)
	setValue(agent.origins, "instances", document.Instances, &agent.config.Instances, applied.origin)
	if document.Model != nil {
		agent.config.Model = strings.TrimSpace(*document.Model)
		agent.origins["model"] = applied.origin
	}
	if document.Persona != nil {
		// A persona override replaces the inherited persona completely, so both
		// halves of the reference must come from the overriding layer.
		if document.Persona.Version == nil || document.Persona.Path == nil {
			return fmt.Errorf("agent %q persona override must declare both version and path", name)
		}
		agent.persona = &personaReference{
			version: strings.TrimSpace(*document.Persona.Version),
			path:    strings.TrimSpace(*document.Persona.Path),
			loader:  applied.personas,
		}
		agent.origins["persona"] = applied.origin
	}
	return nil
}

func (r *resolution) finish(sources []string) (Resolved, error) {
	effective := r.config
	origins := make(map[string]string, len(r.origins))
	for key, origin := range r.origins {
		origins[key] = origin
	}

	agents := make(map[string]AgentConfig, len(r.agents))
	names := make([]string, 0, len(r.agents))
	for name := range r.agents {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		agent := r.agents[name]
		effectiveAgent := agent.config
		if _, supplied := agent.origins["instances"]; !supplied {
			effectiveAgent.Instances = 1
			agent.origins["instances"] = OriginDefault
		}
		if agent.persona != nil {
			text, source, err := agent.persona.loader.load(agent.persona.path)
			if err != nil {
				return Resolved{}, fmt.Errorf("agent %q: %w", name, err)
			}
			effectiveAgent.Persona = Persona{
				Version: agent.persona.version,
				Path:    agent.persona.path,
				Source:  source,
				Bytes:   len(text),
				Text:    text,
			}
		}
		agents[name] = effectiveAgent
		for field, origin := range agent.origins {
			origins["agents."+name+"."+field] = origin
		}
	}
	effective.Agents = agents

	if effective.Product.RepositoryID == "" && effective.Product.ID != "" {
		effective.Product.RepositoryID = domain.RepositoryID(effective.Product.ID)
		origins["product.repository_id"] = OriginDerived
	}

	return Resolved{Config: effective, Sources: sources, Origins: origins}, nil
}

// setValue applies one supplied field and records where it came from. A field a
// layer did not supply is left alone, which is what makes a sparse override
// inherit the rest.
func setValue[T any](origins map[string]string, key string, supplied *T, target *T, origin string) {
	if supplied == nil {
		return
	}
	*target = *supplied
	origins[key] = origin
}

func sortedAgentNames(agents map[string]agentDocument) []string {
	names := make([]string, 0, len(agents))
	for name := range agents {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
