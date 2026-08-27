package config

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/backend"
	"github.com/mason-bryant/yoyodyne/internal/domain"
)

// declaredProvider is a project reaching a harness this build has never heard
// of. It is the whole of what a user writes: which roles it serves, which tool
// postures it can hold them to, and how to read what it says about rate limits,
// retries, and reset times.
const declaredProvider = `providers:
  my-harness:
    adapter: claude-code
    binary: my-harness
    roles:
      - developer
    postures:
      - worktree-write
    capabilities:
      structured_events: true
      session_resumption: true
    dialect:
      rules:
        - answer: retrying
          type: retry
        - answer: limit-reached
          type: quota
          fields:
            state: exceeded
          kind_field: window
          reset_field: resets_at
          reset_format: unix-seconds
        - answer: served
          type: quota
        - answer: interrupted
          terminal: true
          failed: true
`

// A second provider is added without changing harness code: the configuration
// below is the whole of it, and an agent may name it once it loads.
func TestAProjectDeclaresAProviderOfItsOwn(t *testing.T) {
	t.Parallel()

	input := strings.Replace(validBootstrapConfig, "backend: claude-code", "backend: my-harness", 1) + declaredProvider
	cfg, err := Decode(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if cfg.Agents["developers"].Backend != domain.Backend("my-harness") {
		t.Fatalf("developer backend = %q", cfg.Agents["developers"].Backend)
	}
	registry, err := cfg.ProviderRegistry()
	if err != nil {
		t.Fatalf("ProviderRegistry() error = %v", err)
	}
	descriptor, known := registry.Lookup("my-harness")
	if !known {
		t.Fatal("the declared provider is not in the registry the agents are validated against")
	}
	if descriptor.Dialect == nil || descriptor.Dialect.Name() != "my-harness" {
		t.Fatalf("Dialect = %#v, want the declared one", descriptor.Dialect)
	}
	// The declaration names what launches it and what that adapter runs, which is
	// what turns rules that validate into rules that fire.
	if !descriptor.Runnable() || descriptor.Adapter != domain.BackendClaudeCode {
		t.Fatalf("Adapter = %q, want the adapter this build ships", descriptor.Adapter)
	}
	if descriptor.Binary != "my-harness" {
		t.Fatalf("Binary = %q, want the executable the declaration named", descriptor.Binary)
	}
	// The backends this build ships are still there beside it: declaring a
	// provider adds one rather than replacing the set.
	if _, known := registry.Lookup(domain.BackendClaudeCode); !known {
		t.Fatal("declaring a provider removed the backends this build ships")
	}
}

// Capability validation refuses an unsupported role or posture for a declared
// provider exactly as it does for a backend this build ships, and it refuses it
// where the configuration loads rather than when work is assigned.
func TestADeclaredProviderIsRefusedForWhatItDoesNotServe(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		edit  func(string) string
		wants []string
	}{
		{
			name: "a role the provider does not serve",
			edit: func(input string) string {
				return strings.Replace(input, "      - developer\n", "      - reviewer\n", 1)
			},
			wants: []string{`backend "my-harness" does not support role "developer"`},
		},
		{
			name: "a posture the provider cannot hold",
			edit: func(input string) string {
				return strings.Replace(input, "      - worktree-write\n", "      - read-only\n", 1)
			},
			wants: []string{`cannot hold the "worktree-write" tool posture`},
		},
		{
			name: "a provider nothing declared",
			edit: func(input string) string {
				return strings.Replace(input, "  my-harness:", "  another-harness:", 1)
			},
			wants: []string{`agent "developers" has unsupported backend "my-harness"`},
		},
		{
			name: "a dialect that reads nothing its provider says",
			edit: func(input string) string {
				return strings.Split(input, "    dialect:")[0] + "    dialect:\n      rules: []\n"
			},
			wants: []string{"reads nothing a provider says"},
		},
		{
			// A declaration running on a backend this build ships no adapter for
			// is rules with no invocation to observe, so it is refused where it is
			// written rather than loaded and never fired.
			name: "an adapter this build does not ship",
			edit: func(input string) string {
				return strings.Replace(input, "    adapter: claude-code\n", "    adapter: codex\n", 1)
			},
			wants: []string{"ships no adapter for"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input := strings.Replace(validBootstrapConfig, "backend: claude-code", "backend: my-harness", 1) + declaredProvider
			_, err := Decode(strings.NewReader(test.edit(input)))
			if err == nil {
				t.Fatal("Decode() error = nil, want the declaration refused before any work is assigned")
			}
			for _, want := range test.wants {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("Decode() error %q does not contain %q", err, want)
				}
			}
		})
	}
}

// A provider cannot replace a backend this build ships, because a project that
// silently redefined the default backend would be one whose runs nobody could
// account for.
func TestADeclaredProviderCannotReplaceABuiltIn(t *testing.T) {
	t.Parallel()

	input := validBootstrapConfig + strings.Replace(declaredProvider, "my-harness:", "claude-code:", 1)
	_, err := Decode(strings.NewReader(input))
	if err == nil || !strings.Contains(err.Error(), "a backend this build ships") {
		t.Fatalf("Decode() error = %v, want the declaration refused", err)
	}
}

// A project that declares nothing runs on the backends this build ships and is
// told nothing about providers, which is every project until one needs more.
func TestAProjectThatDeclaresNoProviderIsUnaffected(t *testing.T) {
	t.Parallel()

	cfg, err := Decode(strings.NewReader(validBootstrapConfig))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if len(cfg.Providers) != 0 {
		t.Fatalf("Providers = %#v, want none", cfg.Providers)
	}
	registry, err := cfg.ProviderRegistry()
	if err != nil {
		t.Fatalf("ProviderRegistry() error = %v", err)
	}
	if got := len(registry.Backends()); got != len(backend.BuiltInDescriptors()) {
		t.Fatalf("Backends() = %v, want the built-ins alone", registry.Backends())
	}
}
