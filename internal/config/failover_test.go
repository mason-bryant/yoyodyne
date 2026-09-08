package config

import (
	"strings"
	"testing"
)

// Nothing acquires failover by inheriting a bundle or by upgrading the
// executable. It is the operator's opt-in, so a project that has not written it
// down has every agent answering only on the model it named.
func TestFailoverIsOffForEveryAgentUntilOneSaysOtherwise(t *testing.T) {
	t.Parallel()

	cfg := loadProject(t, minimalProjectConfig, nil).Config
	for name := range cfg.Agents {
		if alternate := cfg.AgentFailoverModel(name); alternate != "" {
			t.Fatalf("agent %q failover model = %q, want nothing until the agent enables it", name, alternate)
		}
	}
}

// The alternate is read from the agent's own block, beside the persona binding
// and the account, because which models a persona is interchangeable across is
// stated per agent rather than derived from the role.
func TestAnAgentNamesTheOneAlternateItsTurnMayBeServedBy(t *testing.T) {
	t.Parallel()

	cfg := loadProject(t, minimalProjectConfig+`agents:
  development-manager:
    model: fable
    failover:
      enabled: true
      model: opus
`, nil).Config
	if alternate := cfg.AgentFailoverModel("development-manager"); alternate != "opus" {
		t.Fatalf("failover model = %q, want the alternate the agent named", alternate)
	}
	// And nothing else moved: enabling it for one agent says nothing about any
	// other, which is the whole of what "per agent" means here.
	if alternate := cfg.AgentFailoverModel("developer"); alternate != "" {
		t.Fatalf("developer failover model = %q, want nothing", alternate)
	}
}

// A named alternate an operator switched off is not one. That is how the
// behaviour is turned off without losing the model already chosen, which is what
// "willing to try it" needs: turning it back on is one word.
func TestANamedAlternateThatIsSwitchedOffServesNothing(t *testing.T) {
	t.Parallel()

	cfg := loadProject(t, minimalProjectConfig+`agents:
  development-manager:
    model: fable
    failover:
      enabled: false
      model: opus
`, nil).Config
	if alternate := cfg.AgentFailoverModel("development-manager"); alternate != "" {
		t.Fatalf("failover model = %q, want nothing while it is switched off", alternate)
	}
	if named := cfg.Agents["development-manager"].Failover.Model; named != "opus" {
		t.Fatalf("named alternate = %q, want the choice kept so it can be switched back on", named)
	}
}

// The block replaces whatever was inherited rather than merging into it. A layer
// that switched failover on over an alternate some layer underneath named would
// be serving an agent's turns from a model nobody chose for it.
func TestAFailoverOverrideReplacesTheInheritedBlock(t *testing.T) {
	t.Parallel()

	resolved, err := resolveLayers([]layer{
		{origin: "bundle", document: mustDecodeDocument(t, `version: 1
agents:
  development-manager:
    failover:
      enabled: true
      model: opus
`)},
		{origin: "project", document: mustDecodeDocument(t, `version: 1
agents:
  development-manager:
    failover:
      enabled: false
`)},
	})
	if err != nil {
		t.Fatalf("resolveLayers() error = %v", err)
	}
	// The alternate goes with the enablement rather than surviving it: a layer
	// that switched failover on again would otherwise reach a model this layer
	// never named.
	failover := resolved.Config.Agents["development-manager"].Failover
	if failover.Enabled || failover.Model != "" {
		t.Fatalf("failover = %#v, want the project's block whole rather than half of each layer's", failover)
	}
	if origin := resolved.Origins["agents.development-manager.failover"]; origin != "project" {
		t.Fatalf("failover origin = %q, want project", origin)
	}
}

func TestFailoverIsRefusedWhenItCouldNotServeATurn(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		agent string
		want  string
	}{
		{
			name: "enabled with no alternate",
			agent: `    model: fable
    failover:
      enabled: true
`,
			want: "names no alternate model",
		},
		{
			name: "an alternate that is the endpoint whose window closed",
			agent: `    model: fable
    failover:
      enabled: true
      model: fable
`,
			want: "names its own endpoint as its failover",
		},
		{
			name: "an alternate that could not name a model",
			agent: `    model: fable
    failover:
      enabled: true
      model: "--dangerously-skip-permissions"
`,
			want: "failover model selector",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := loadProjectError(t, minimalProjectConfig+"agents:\n  development-manager:\n"+test.agent, nil)
			if err == nil {
				t.Fatal("LoadResolved() error = nil, want a failover that could not serve a turn refused")
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want it to name %q", err, test.want)
			}
		})
	}
}

// An alternate is checked even where it is switched off, because a value nobody
// validates is one that fails the day somebody turns it on.
func TestASwitchedOffAlternateIsStillChecked(t *testing.T) {
	t.Parallel()

	_, err := loadProjectError(t, minimalProjectConfig+`agents:
  development-manager:
    model: fable
    failover:
      enabled: false
      model: fable
`, nil)
	if err == nil {
		t.Fatal("LoadResolved() error = nil, want an alternate checked before somebody switches it on")
	}
}
