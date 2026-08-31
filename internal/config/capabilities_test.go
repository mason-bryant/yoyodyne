package config

// The shipped agents held against the registry their capabilities come from.
//
// There is one source, so the two cannot disagree by construction; what these
// tests are for is that it stays one source. A capability set copied into the
// bundle, or a `capabilities` key admitted to the document, would each be a
// second place the same statement is made — and the whole point of expressing
// the five roles in the vocabulary is that the statement is made once.

import (
	"slices"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/rolecapability"
)

// TestBuiltinAgentsCarryTheirRolesRegisteredCapabilities is the agreement claim:
// every agent the shipped bundle declares holds exactly what the registry says
// its role holds, and the five roles between them are the whole of the registry.
func TestBuiltinAgentsCarryTheirRolesRegisteredCapabilities(t *testing.T) {
	t.Parallel()

	registry, err := rolecapability.Default()
	if err != nil {
		t.Fatalf("rolecapability.Default() error = %v", err)
	}
	resolved := loadProject(t, minimalProjectConfig, nil)

	described := map[domain.AgentRole]bool{}
	for name, agent := range resolved.Config.Agents {
		bundle, known := registry.Bundle(agent.Role)
		if !known {
			t.Fatalf("agent %q fills role %q and the registry describes no such role", name, agent.Role)
		}
		if !slices.Equal(agent.Capabilities, bundle.Holds) {
			t.Errorf("agent %q holds %v, and the registry gives the %s %v", name, agent.Capabilities, agent.Role.Title(), bundle.Holds)
		}
		if len(agent.Capabilities) == 0 {
			t.Errorf("agent %q holds nothing; the bundle implies a role's authority rather than carrying it", name)
		}
		// The set is the registry's rather than any layer's, and the provenance
		// says so: an operator reading `config show --origins` is told where it
		// came from instead of being left to assume some file supplied it.
		if origin := resolved.Origins["agents."+name+".capabilities"]; origin != OriginRoleCapabilities {
			t.Errorf("agent %q capabilities origin = %q, want %q", name, origin, OriginRoleCapabilities)
		}
		described[agent.Role] = true
	}
	for _, role := range registry.Roles() {
		if !described[role] {
			t.Errorf("the registry describes the %s and the shipped bundle configures no agent for it", role.Title())
		}
	}
}

// TestEveryRoleAnAgentMayFillCarriesCapabilities reads the same agreement from
// the other end. A role the harness has and the registry describes is one an
// agent may be configured for, so a project that configures one gets the same
// set the bundle's own agent would have.
func TestEveryRoleAnAgentMayFillCarriesCapabilities(t *testing.T) {
	t.Parallel()

	registry, err := rolecapability.Default()
	if err != nil {
		t.Fatalf("rolecapability.Default() error = %v", err)
	}
	for _, role := range domain.Roles() {
		t.Run(string(role), func(t *testing.T) {
			t.Parallel()
			// Every role but the developer leaves the configuration without one,
			// so the developer agent stays and a second agent fills the role.
			input := validBootstrapConfig + `  others:
    role: ` + string(role) + `
    backend: claude-code
    model: opus
    instances: 1
`
			cfg, err := Decode(strings.NewReader(input))
			if err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			bundle, known := registry.Bundle(role)
			if !known {
				t.Fatalf("the registry describes no %s", role.Title())
			}
			if !slices.Equal(cfg.Agents["others"].Capabilities, bundle.Holds) {
				t.Errorf("agent for role %q holds %v, want %v", role, cfg.Agents["others"].Capabilities, bundle.Holds)
			}
		})
	}
}

// TestCapabilitiesAreNotConfigurable is the other half of one source. A project
// that states a capability set is refused where any unknown key is refused, and
// the refusal is the decoder's ordinary one: there is no `capabilities` key to
// write, in a complete configuration or in an overlay on the shipped bundle.
func TestCapabilitiesAreNotConfigurable(t *testing.T) {
	t.Parallel()

	t.Run("complete configuration", func(t *testing.T) {
		t.Parallel()
		input := validBootstrapConfig + "    capabilities:\n      - review.verdict\n"
		_, err := Decode(strings.NewReader(input))
		if err == nil || !strings.Contains(err.Error(), "field capabilities not found") {
			t.Fatalf("Decode() error = %v, want the unknown key to be refused", err)
		}
	})

	t.Run("overlay on the shipped bundle", func(t *testing.T) {
		t.Parallel()
		_, err := loadProjectError(t, minimalProjectConfig+"agents:\n  developer:\n    capabilities:\n      - review.verdict\n", nil)
		if err == nil || !strings.Contains(err.Error(), "field capabilities not found") {
			t.Fatalf("LoadResolved() error = %v, want the unknown key to be refused", err)
		}
	})
}

// TestASixthRoleIsStillRefused is the admission that is deliberately not made
// here. Expressing the five roles in the vocabulary does not open a sixth, and a
// configuration naming one is refused for its role exactly as it was before any
// agent carried a capability at all.
func TestASixthRoleIsStillRefused(t *testing.T) {
	t.Parallel()

	_, err := loadProjectError(t, minimalProjectConfig+`agents:
  sentinel:
    role: observer
    backend: claude-code
    model: opus
    instances: 1
`, nil)
	if err == nil {
		t.Fatal("LoadResolved() error = nil, want a sixth role to be refused")
	}
	if !strings.Contains(err.Error(), `agent "sentinel" has unknown role "observer"`) {
		t.Fatalf("LoadResolved() error = %v, want the unknown role named", err)
	}
	// The refusal is about the role rather than about what it would hold: a
	// capability set nobody can state is not a second thing to be wrong.
	if strings.Contains(err.Error(), "capabilit") {
		t.Errorf("LoadResolved() error %q blames capabilities for an unknown role", err)
	}
}
