package config

import (
	"strings"
	"testing"
)

// Nothing acquires a pin by inheriting a bundle or by upgrading the executable.
// The floating alias is the default and stays it, so a project that has written
// no version down has every agent's selector floating exactly as before.
func TestNoAgentIsPinnedUntilOneSaysOtherwise(t *testing.T) {
	t.Parallel()

	cfg := loadProject(t, minimalProjectConfig, nil).Config
	for name := range cfg.Agents {
		if version := cfg.AgentModelVersion(name); version != "" {
			t.Fatalf("agent %q model version = %q, want the alias left floating", name, version)
		}
	}
}

// The version is read from the agent's own block, beside the persona binding and
// the failover alternate, because which agent is worth holding still is stated
// per agent rather than derived from the role.
func TestAnAgentNamesTheVersionItsTurnsAskFor(t *testing.T) {
	t.Parallel()

	cfg := loadProject(t, minimalProjectConfig+`agents:
  architect:
    model: opus
    model_version: claude-opus-5-20260401
`, nil).Config
	if version := cfg.AgentModelVersion("architect"); version != "claude-opus-5-20260401" {
		t.Fatalf("model version = %q, want the version the agent pinned", version)
	}
	// The alias is untouched, because it is what answers when the provider has not
	// got the version.
	if model := cfg.Agents["architect"].Model; model != "opus" {
		t.Fatalf("model = %q, want the family alias kept as the fallback", model)
	}
	// And nothing else moved: pinning one agent says nothing about any other,
	// which is the whole of what "per agent" means here.
	if version := cfg.AgentModelVersion("developer"); version != "" {
		t.Fatalf("developer model version = %q, want nothing", version)
	}
}

// A pin overrides on its own rather than with the model, because the two are
// separate answers: pinning a version over an inherited alias says which version
// of that family to ask for, not which family.
func TestAPinnedVersionOverridesWithoutRestatingTheFamily(t *testing.T) {
	t.Parallel()

	resolved, err := resolveLayers([]layer{
		{origin: "bundle", document: mustDecodeDocument(t, `version: 1
agents:
  architect:
    model: opus
`)},
		{origin: "project", document: mustDecodeDocument(t, `version: 1
agents:
  architect:
    model_version: claude-opus-5-20260401
`)},
	})
	if err != nil {
		t.Fatalf("resolveLayers() error = %v", err)
	}
	agent := resolved.Config.Agents["architect"]
	if agent.Model != "opus" || agent.ModelVersion != "claude-opus-5-20260401" {
		t.Fatalf("agent = %#v, want the inherited family with the project's version pinned over it", agent)
	}
	if origin := resolved.Origins["agents.architect.model_version"]; origin != "project" {
		t.Fatalf("model version origin = %q, want project", origin)
	}
	if origin := resolved.Origins["agents.architect.model"]; origin != "bundle" {
		t.Fatalf("model origin = %q, want the family left where it was", origin)
	}
}

// Stated empty removes an inherited pin, which is how an alias is put back to
// floating without having to know which layer underneath pinned it.
func TestAnEmptyVersionPutsAnInheritedPinBackToFloating(t *testing.T) {
	t.Parallel()

	resolved, err := resolveLayers([]layer{
		{origin: "bundle", document: mustDecodeDocument(t, `version: 1
agents:
  architect:
    model: opus
    model_version: claude-opus-5-20260401
`)},
		{origin: "project", document: mustDecodeDocument(t, `version: 1
agents:
  architect:
    model_version: ""
`)},
	})
	if err != nil {
		t.Fatalf("resolveLayers() error = %v", err)
	}
	if version := resolved.Config.Agents["architect"].ModelVersion; version != "" {
		t.Fatalf("model version = %q, want the inherited pin removed", version)
	}
}

func TestAPinThatCouldNotHoldAVersionStillIsRefused(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		agent string
		want  string
	}{
		{
			name: "a version that is the alias it would fall back to",
			agent: `    model: opus
    model_version: opus
`,
			want: "which is the selector it already names",
		},
		{
			name: "a version that could not name a model",
			agent: `    model: opus
    model_version: "--dangerously-skip-permissions"
`,
			want: "model version model selector",
		},
		{
			// Failing over to the version the provider has just been found not to
			// have is not an alternate, and a turn served that way would record a
			// model as having served that never did.
			name: "a version that is also the failover alternate",
			agent: `    model: opus
    model_version: claude-opus-5-20260401
    failover:
      enabled: true
      model: claude-opus-5-20260401
`,
			want: "names it as its failover model",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := loadProjectError(t, minimalProjectConfig+"agents:\n  architect:\n"+test.agent, nil)
			if err == nil {
				t.Fatal("LoadResolved() error = nil, want a pin that could not hold a version refused")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want it to name %q", err, test.want)
			}
		})
	}
}

// An agent cannot be both removed and pinned, the same way it cannot be removed
// and given any other setting.
func TestAnAgentThatIsDisabledAndPinnedIsRefused(t *testing.T) {
	t.Parallel()

	_, err := resolveLayers([]layer{
		{origin: "bundle", document: mustDecodeDocument(t, `version: 1
agents:
  architect:
    model: opus
`)},
		{origin: "project", document: mustDecodeDocument(t, `version: 1
agents:
  architect:
    disabled: true
    model_version: claude-opus-5-20260401
`)},
	})
	if err == nil {
		t.Fatal("resolveLayers() error = nil, want a contradictory remove-and-configure entry refused")
	}
}
