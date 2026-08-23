package config

import (
	"strings"
	"testing"
)

func TestARevisionIsTheSameForTheSameEffectiveValues(t *testing.T) {
	t.Parallel()

	first, err := Decode(strings.NewReader(validBootstrapConfig))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	second, err := Decode(strings.NewReader(validBootstrapConfig))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if first.Revision() != second.Revision() {
		t.Fatalf("Revision() = %q and %q for one configuration", first.Revision(), second.Revision())
	}
	if !strings.HasPrefix(first.Revision(), RevisionPrefix) {
		t.Fatalf("Revision() = %q, want it to say what it identifies", first.Revision())
	}
	if len(first.Revision()) != len(RevisionPrefix)+RevisionDigits {
		t.Fatalf("Revision() = %q, want %d digits after the prefix", first.Revision(), RevisionDigits)
	}
}

func TestARevisionMovesWithEveryValueTheHarnessRuns(t *testing.T) {
	t.Parallel()

	base, err := Decode(strings.NewReader(validBootstrapConfig))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	for name, change := range map[string]func(Config) Config{
		"a check": func(c Config) Config {
			c.Checks = append(append([]string(nil), c.Checks...), "go vet ./...")
			return c
		},
		"an approval": func(c Config) Config {
			c.Approvals.Publishing = "automatic"
			return c
		},
		"an agent's model": func(c Config) Config {
			agent := c.Agents["developers"]
			agent.Model = "claude-opus-5"
			c.Agents = map[string]AgentConfig{"developers": agent}
			return c
		},
		"the account it runs under": func(c Config) Config {
			c.Accounts = map[string]Account{"work": {}}
			return c
		},
		// The persona is excluded from the serialized configuration so that
		// `config show` stays readable, and it is what every prompt is written
		// against: two revisions that agreed across it would call two differently
		// instructed harnesses one configuration.
		"an agent's persona": func(c Config) Config {
			agent := c.Agents["developers"]
			agent.Persona = Persona{Version: "v1", Path: "personas/developer.md", Text: "work carefully"}
			c.Agents = map[string]AgentConfig{"developers": agent}
			return c
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if changed := change(base).Revision(); changed == base.Revision() {
				t.Fatalf("Revision() = %q with %s changed, want it to move", changed, name)
			}
		})
	}
}

// The revision describes what governs a run rather than what a file says, so a
// project that inherits a value and one that states the same value are the same
// configuration — which is the answer that makes a revision worth correlating
// runs by.
func TestARevisionIsOfTheEffectiveConfigurationRatherThanTheFile(t *testing.T) {
	t.Parallel()

	stated, err := Decode(strings.NewReader(validBootstrapConfig))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	inherited, err := Decode(strings.NewReader(strings.Replace(validBootstrapConfig,
		"  max_concurrent_developers: 1\n", "", 1)))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if stated.Revision() != inherited.Revision() {
		t.Fatalf("Revision() = %q stated and %q defaulted, want one revision for one set of effective values",
			stated.Revision(), inherited.Revision())
	}
}
