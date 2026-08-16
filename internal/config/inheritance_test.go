package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"yoyodyne/internal/domain"
)

// minimalProjectConfig is everything a project outside the Yoyodyne source tree
// has to write down: its own identity, and the bundle it inherits everything
// else from.
const minimalProjectConfig = `version: 1
extends: builtin:v1
product:
  id: example
  repository: .
`

func TestBuiltinBundleSuppliesCompleteDefaultAgents(t *testing.T) {
	t.Parallel()

	cfg := loadProject(t, minimalProjectConfig, nil).Config
	wantRoles := map[string]domain.AgentRole{
		"product-manager":     domain.RoleProductManager,
		"architect":           domain.RoleArchitect,
		"development-manager": domain.RoleDevelopmentManager,
		"developer":           domain.RoleDeveloper,
		"reviewer":            domain.RoleReviewer,
	}
	if len(cfg.Agents) != len(wantRoles) {
		t.Fatalf("agents = %d, want %d", len(cfg.Agents), len(wantRoles))
	}
	for name, wantRole := range wantRoles {
		agent, ok := cfg.Agents[name]
		if !ok {
			t.Fatalf("agent %q is missing from the built-in bundle", name)
		}
		if agent.Role != wantRole {
			t.Errorf("agent %q role = %q, want %q", name, agent.Role, wantRole)
		}
		if agent.Backend != domain.BackendClaudeCode {
			t.Errorf("agent %q backend = %q, want %q", name, agent.Backend, domain.BackendClaudeCode)
		}
		if err := ValidateModelSelector(agent.Model); err != nil {
			t.Errorf("agent %q model: %v", name, err)
		}
		if agent.Instances < 1 {
			t.Errorf("agent %q instances = %d, want at least 1", name, agent.Instances)
		}
		if agent.Persona.Version == "" || agent.Persona.Path == "" || strings.TrimSpace(agent.Persona.Text) == "" {
			t.Errorf("agent %q persona = %+v, want a versioned nonempty persona", name, agent.Persona)
		}
		if !strings.HasPrefix(agent.Persona.Source, BuiltinV1) {
			t.Errorf("agent %q persona source = %q, want a %s source", name, agent.Persona.Source, BuiltinV1)
		}
	}
	if cfg.Product.RepositoryID != "example" {
		t.Errorf("repository id = %q, want the derived product id", cfg.Product.RepositoryID)
	}
}

func TestProjectOverridesOneFieldWithoutCopyingDefaults(t *testing.T) {
	t.Parallel()

	resolved := loadProject(t, minimalProjectConfig+`agents:
  developer:
    model: claude-opus-5-20260514
`, nil)
	developer := resolved.Config.Agents["developer"]
	if developer.Model != "claude-opus-5-20260514" {
		t.Fatalf("developer model = %q, want the project override", developer.Model)
	}
	// Everything the override did not mention still comes from the bundle.
	if developer.Role != domain.RoleDeveloper || developer.Backend != domain.BackendClaudeCode || developer.Instances != 1 {
		t.Fatalf("developer inherited fields = %+v", developer)
	}
	if strings.TrimSpace(developer.Persona.Text) == "" {
		t.Fatal("developer persona was lost by a model-only override")
	}
	if got := resolved.Origins["agents.developer.model"]; got != resolved.Path {
		t.Errorf("model origin = %q, want %q", got, resolved.Path)
	}
	for key, want := range map[string]string{
		"agents.developer.role":      BuiltinV1,
		"agents.developer.backend":   BuiltinV1,
		"agents.developer.persona":   BuiltinV1,
		"agents.developer.instances": BuiltinV1,
		"approvals.integration":      BuiltinV1,
		"product.id":                 resolved.Path,
		"product.repository_id":      OriginDerived,
	} {
		if got := resolved.Origins[key]; got != want {
			t.Errorf("origin[%q] = %q, want %q", key, got, want)
		}
	}
	if len(resolved.Sources) != 2 || resolved.Sources[0] != BuiltinV1 || resolved.Sources[1] != resolved.Path {
		t.Errorf("sources = %v, want the bundle then the project file", resolved.Sources)
	}
}

func TestProjectPersonaOverrideReplacesTheInheritedPersona(t *testing.T) {
	t.Parallel()

	resolved := loadProject(t, minimalProjectConfig+`agents:
  reviewer:
    persona:
      version: project-1
      path: personas/reviewer.md
`, map[string]string{"personas/reviewer.md": "# House reviewer\n\nCheck the migration path first.\n"})
	reviewer := resolved.Config.Agents["reviewer"]
	if reviewer.Persona.Version != "project-1" || !strings.Contains(reviewer.Persona.Text, "House reviewer") {
		t.Fatalf("reviewer persona = %+v", reviewer.Persona)
	}
	if strings.Contains(reviewer.Persona.Text, "Reviewer persona") {
		t.Fatal("project persona was merged into the inherited one instead of replacing it")
	}
	if reviewer.Model == "" {
		t.Fatal("a persona override dropped the inherited model selector")
	}
	if got := resolved.Origins["agents.reviewer.persona"]; got != resolved.Path {
		t.Errorf("persona origin = %q, want %q", got, resolved.Path)
	}
	// The developer keeps the built-in persona nobody overrode.
	if !strings.HasPrefix(resolved.Config.Agents["developer"].Persona.Source, BuiltinV1) {
		t.Errorf("developer persona source = %q", resolved.Config.Agents["developer"].Persona.Source)
	}
}

func TestDisabledAgentIsRemovedButRequiredRolesStillValidate(t *testing.T) {
	t.Parallel()

	cfg := loadProject(t, minimalProjectConfig+`agents:
  architect:
    disabled: true
`, nil).Config
	if _, present := cfg.Agents["architect"]; present {
		t.Fatal("architect survived disabled: true")
	}
	if _, present := cfg.Agents["developer"]; !present {
		t.Fatal("disabling one agent removed an unrelated one")
	}

	// The roles the invoked workflow executes cannot be disabled away.
	_, err := loadProjectError(t, minimalProjectConfig+`agents:
  developer:
    disabled: true
`, nil)
	if err == nil || !strings.Contains(err.Error(), "at least one developer agent is required") {
		t.Fatalf("disabling the developer error = %v, want a validation failure", err)
	}

	_, err = loadProjectError(t, minimalProjectConfig+`approvals:
  integration: automatic
checks:
  - go test ./...
agents:
  reviewer:
    disabled: true
`, nil)
	if err == nil || !strings.Contains(err.Error(), "automatic integration requires at least one reviewer agent") {
		t.Fatalf("disabling the reviewer error = %v, want a validation failure", err)
	}
}

func TestInvalidInheritanceFailsClosed(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		config  string
		files   map[string]string
		problem string
	}{
		{
			name:    "unknown bundle",
			config:  strings.Replace(minimalProjectConfig, "builtin:v1", "builtin:v9", 1),
			problem: `unknown configuration bundle "builtin:v9"`,
		},
		{
			name:    "unknown agent key",
			config:  minimalProjectConfig + "agents:\n  developer:\n    modle: opus\n",
			problem: "field modle not found",
		},
		{
			name:    "unknown top-level key",
			config:  minimalProjectConfig + "personas: yes\n",
			problem: "field personas not found",
		},
		{
			name:    "disabled agent that is also configured",
			config:  minimalProjectConfig + "agents:\n  architect:\n    disabled: true\n    model: opus\n",
			problem: "is disabled and also configured",
		},
		{
			name:    "disabled agent that was never inherited",
			config:  minimalProjectConfig + "agents:\n  auditor:\n    disabled: true\n",
			problem: "no inherited agent by that name exists",
		},
		{
			name:    "partial persona override",
			config:  minimalProjectConfig + "agents:\n  developer:\n    persona:\n      path: personas/developer.md\n",
			problem: "must declare both version and path",
		},
		{
			name:    "invalid role and backend combination",
			config:  minimalProjectConfig + "agents:\n  architect:\n    backend: codex\n",
			problem: `does not support role "architect"`,
		},
		{
			name:    "invalid effective configuration",
			config:  minimalProjectConfig + "execution:\n  max_concurrent_developers: 4\n",
			problem: "max_concurrent_developers cannot exceed configured developer instances",
		},
		{
			name:    "absolute persona path",
			config:  minimalProjectConfig + "agents:\n  developer:\n    persona:\n      version: p1\n      path: /etc/persona.md\n",
			problem: "must be relative to the project .yoyodyne directory",
		},
		{
			name:    "persona path traversal",
			config:  minimalProjectConfig + "agents:\n  developer:\n    persona:\n      version: p1\n      path: ../../secrets.md\n",
			problem: "must not traverse outside the project .yoyodyne directory",
		},
		{
			name:    "persona that is not Markdown",
			config:  minimalProjectConfig + "agents:\n  developer:\n    persona:\n      version: p1\n      path: personas/developer.txt\n",
			files:   map[string]string{"personas/developer.txt": "text"},
			problem: "must be a Markdown file",
		},
		{
			name:    "missing persona file",
			config:  minimalProjectConfig + "agents:\n  developer:\n    persona:\n      version: p1\n      path: personas/absent.md\n",
			problem: `resolve persona "personas/absent.md"`,
		},
		{
			name:    "empty persona file",
			config:  minimalProjectConfig + "agents:\n  developer:\n    persona:\n      version: p1\n      path: personas/blank.md\n",
			files:   map[string]string{"personas/blank.md": "   \n"},
			problem: "is empty",
		},
		{
			name:    "oversized persona file",
			config:  minimalProjectConfig + "agents:\n  developer:\n    persona:\n      version: p1\n      path: personas/huge.md\n",
			files:   map[string]string{"personas/huge.md": strings.Repeat("g", MaxPersonaBytes+1)},
			problem: "limit is",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := loadProjectError(t, test.config, test.files)
			if err == nil || !strings.Contains(err.Error(), test.problem) {
				t.Fatalf("LoadResolved() error = %v, want %q", err, test.problem)
			}
		})
	}
}

func TestPersonaSymlinkEscapeFailsClosed(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	outside := filepath.Join(project, "outside.md")
	if err := os.WriteFile(outside, []byte("# Outside the project configuration\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	writeProject(t, project, minimalProjectConfig+`agents:
  developer:
    persona:
      version: p1
      path: personas/escape.md
`, nil)
	personas := filepath.Join(project, DirectoryName, "personas")
	if err := os.MkdirAll(personas, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(personas, "escape.md")); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}

	_, err := LoadResolved(filepath.Join(project, DirectoryName, FileName))
	if err == nil || !strings.Contains(err.Error(), "resolves outside") {
		t.Fatalf("LoadResolved() error = %v, want a symlink escape failure", err)
	}
}

// A project states its own schema version even when the bundle it extends
// declares one. Inheriting the version would let a file written against a
// different schema load as whatever the bundle happened to say.
func TestProjectConfigurationMustDeclareItsOwnVersion(t *testing.T) {
	t.Parallel()

	withoutVersion := strings.TrimPrefix(minimalProjectConfig, "version: 1\n")
	if strings.Contains(withoutVersion, "version:") {
		t.Fatalf("test setup left a version key in %q", withoutVersion)
	}

	_, err := loadProjectError(t, withoutVersion, nil)
	var validationErr ValidationError
	if !errors.As(err, &validationErr) || !strings.Contains(err.Error(), "version must be 1 and is required") {
		t.Fatalf("LoadResolved() error = %v, want a missing-version validation failure", err)
	}

	// A standalone configuration has no bundle to inherit from, and the stream
	// path is held to the same rule as a file.
	_, err = DecodeResolved(strings.NewReader(strings.TrimPrefix(validBootstrapConfig, "version: 1\n")))
	if err == nil || !strings.Contains(err.Error(), "version must be 1 and is required") {
		t.Fatalf("DecodeResolved() error = %v, want a missing-version validation failure", err)
	}

	// A version the harness does not implement still fails, as its own problem
	// rather than as a missing key.
	_, err = loadProjectError(t, strings.Replace(minimalProjectConfig, "version: 1", "version: 2", 1), nil)
	if err == nil || !strings.Contains(err.Error(), "version must be 1") || strings.Contains(err.Error(), "is required") {
		t.Fatalf("LoadResolved() error = %v, want an unsupported-version failure", err)
	}
}

// A configuration that does not extend a bundle is a complete standalone file
// and keeps working exactly as it did before bundles existed.
func TestLegacyCompleteConfigurationStillLoads(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	path := filepath.Join(project, LegacyFileName)
	if err := os.WriteFile(path, []byte(validBootstrapConfig), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	resolved, err := LoadResolved(path)
	if err != nil {
		t.Fatalf("LoadResolved() error = %v", err)
	}
	if resolved.Config.Extends != "" {
		t.Fatalf("Extends = %q, want empty for a standalone configuration", resolved.Config.Extends)
	}
	if len(resolved.Config.Agents) != 1 {
		t.Fatalf("agents = %d, want only the ones the file declares", len(resolved.Config.Agents))
	}
	if resolved.Config.Agents["developers"].Persona.Defined() {
		t.Fatal("a standalone configuration inherited a persona it never declared")
	}
	if len(resolved.Sources) != 1 || resolved.Sources[0] != resolved.Path {
		t.Fatalf("sources = %v, want only the file itself", resolved.Sources)
	}
}

// A value nobody configured is reported as a harness default rather than
// attributed to a layer that never mentioned it.
func TestHarnessDefaultsAreRecordedAsSuch(t *testing.T) {
	t.Parallel()

	resolved, err := DecodeResolved(strings.NewReader(`version: 1
product:
  id: example
  repository: .
approvals:
  brief: human
  goals: human
  designs: automatic
  integration: human
agents:
  developer:
    role: developer
    backend: claude-code
    model: opus
`))
	if err != nil {
		t.Fatalf("DecodeResolved() error = %v", err)
	}
	for _, key := range []string{
		"execution.max_concurrent_developers",
		"execution.repair_attempts_before_replan",
		"execution.worktree_root",
		"agents.developer.instances",
	} {
		if got := resolved.Origins[key]; got != OriginDefault {
			t.Errorf("origin[%q] = %q, want %q", key, got, OriginDefault)
		}
	}
	if got := resolved.Origins["agents.developer.model"]; got != OriginInput {
		t.Errorf("model origin = %q, want %q", got, OriginInput)
	}
}

func loadProject(t *testing.T, contents string, personas map[string]string) Resolved {
	t.Helper()
	resolved, err := loadProjectError(t, contents, personas)
	if err != nil {
		t.Fatalf("LoadResolved() error = %v", err)
	}
	return resolved
}

func loadProjectError(t *testing.T, contents string, personas map[string]string) (Resolved, error) {
	t.Helper()
	project := t.TempDir()
	writeProject(t, project, contents, personas)
	return LoadResolved(filepath.Join(project, DirectoryName, FileName))
}

func writeProject(t *testing.T, project, contents string, personas map[string]string) {
	t.Helper()
	directory := filepath.Join(project, DirectoryName)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, FileName), []byte(contents), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	for name, body := range personas {
		path := filepath.Join(directory, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
	}
}

// The bundle supplies both usage-limit pause bounds, so a project inherits a
// harness that waits out a five-hour limit and refuses to sleep through a
// seven-day one without having to write either number down.
func TestBuiltinBundleSuppliesUsageLimitPauseBounds(t *testing.T) {
	t.Parallel()

	resolved := loadProject(t, minimalProjectConfig, nil)
	if got := resolved.Config.Execution.UsageLimitMaxPause; got != Duration(6*time.Hour) {
		t.Fatalf("usage_limit_max_pause = %s, want 6h", got)
	}
	if got := resolved.Config.Execution.UsageLimitInProcessPause; got != Duration(6*time.Hour) {
		t.Fatalf("usage_limit_in_process_pause = %s, want 6h", got)
	}
	for _, key := range []string{"execution.usage_limit_max_pause", "execution.usage_limit_in_process_pause"} {
		if origin := resolved.Origins[key]; origin != BuiltinV1 {
			t.Fatalf("%s origin = %q, want %q", key, origin, BuiltinV1)
		}
	}
}

// A project that cares about the pause overrides only the bound it means, in the
// duration syntax it reads it in, and keeps everything else it inherited.
func TestProjectOverridesOneUsageLimitPauseBound(t *testing.T) {
	t.Parallel()

	resolved := loadProject(t, minimalProjectConfig+`execution:
  usage_limit_in_process_pause: 15m
`, nil)
	if got := resolved.Config.Execution.UsageLimitInProcessPause; got != Duration(15*time.Minute) {
		t.Fatalf("usage_limit_in_process_pause = %s, want 15m", got)
	}
	if got := resolved.Config.Execution.UsageLimitMaxPause; got != Duration(6*time.Hour) {
		t.Fatalf("usage_limit_max_pause = %s, want the inherited 6h", got)
	}
	if origin := resolved.Origins["execution.usage_limit_max_pause"]; origin != BuiltinV1 {
		t.Fatalf("an untouched bound changed origin to %q", origin)
	}
}

// A configured duration is round-tripped in the form it was written in, in both
// renderings of the effective configuration. `config show` is where an operator
// finds out how long a run is allowed to wait, and a nanosecond count is not an
// answer to that question.
func TestConfiguredDurationsRenderAsDurations(t *testing.T) {
	t.Parallel()

	cfg := loadProject(t, minimalProjectConfig, nil).Config
	asYAML, err := yaml.Marshal(cfg.Execution)
	if err != nil {
		t.Fatalf("Marshal() yaml error = %v", err)
	}
	if !strings.Contains(string(asYAML), "usage_limit_max_pause: 6h0m0s") {
		t.Fatalf("effective YAML did not render the pause as a duration:\n%s", asYAML)
	}
	asJSON, err := json.Marshal(cfg.Execution)
	if err != nil {
		t.Fatalf("Marshal() json error = %v", err)
	}
	if !strings.Contains(string(asJSON), `"usage_limit_max_pause":"6h0m0s"`) {
		t.Fatalf("effective JSON did not render the pause as a duration:\n%s", asJSON)
	}
}

func TestUsageLimitPauseBoundsFailClosed(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name    string
		body    string
		wantErr string
	}{
		{
			name:    "not a duration",
			body:    "execution:\n  usage_limit_max_pause: soon\n",
			wantErr: "parse duration",
		},
		{
			// A zero bound is a deliberate "never wait"; only a negative one
			// describes a wait nobody could take.
			name:    "negative",
			body:    "execution:\n  usage_limit_max_pause: -1h\n",
			wantErr: "usage_limit_max_pause cannot be negative",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			project := t.TempDir()
			writeProject(t, project, minimalProjectConfig+testCase.body, nil)
			_, err := LoadResolved(filepath.Join(project, DirectoryName, FileName))
			if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
				t.Fatalf("LoadResolved() error = %v, want one mentioning %q", err, testCase.wantErr)
			}
		})
	}
}
