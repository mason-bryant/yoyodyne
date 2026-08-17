package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"yoyodyne/internal/domain"
)

// A generated project owns everything it runs on: one layer, no bundle, and
// every agent field written down where the operator can read and edit it.
func TestScaffoldedProjectLoadsWithoutTheBundle(t *testing.T) {
	t.Parallel()

	resolved := loadScaffold(t, ScaffoldOptions{ProductID: "example", Repository: "."})
	if resolved.Config.Extends != "" {
		t.Fatalf("extends = %q, want a configuration that inherits nothing", resolved.Config.Extends)
	}
	if len(resolved.Sources) != 1 || resolved.Sources[0] != resolved.Path {
		t.Fatalf("sources = %v, want only the project file", resolved.Sources)
	}
	// Every value is the project file's own. The single exception is the
	// repository id, which the generated file lets follow from the product id
	// it does state -- a derivation from something in the same file rather than
	// a value arriving from outside it. Nothing is a harness default either,
	// because the generated file writes down what the harness would have filled
	// in. This is what the documentation promises an operator reading
	// `config show --origins`, so it is asserted exactly.
	for key, origin := range resolved.Origins {
		want := resolved.Path
		if key == "product.repository_id" {
			want = OriginDerived
		}
		if origin != want {
			t.Errorf("origin[%q] = %q, want %q", key, origin, want)
		}
	}

	wantRoles := map[string]domain.AgentRole{
		"product-manager":     domain.RoleProductManager,
		"architect":           domain.RoleArchitect,
		"development-manager": domain.RoleDevelopmentManager,
		"developer":           domain.RoleDeveloper,
		"reviewer":            domain.RoleReviewer,
	}
	if len(resolved.Config.Agents) != len(wantRoles) {
		t.Fatalf("agents = %d, want %d", len(resolved.Config.Agents), len(wantRoles))
	}
	for name, wantRole := range wantRoles {
		agent, ok := resolved.Config.Agents[name]
		if !ok {
			t.Fatalf("agent %q is missing from the generated configuration", name)
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
		if agent.Instances != 1 {
			t.Errorf("agent %q instances = %d, want 1", name, agent.Instances)
		}
		if strings.TrimSpace(agent.Persona.Text) == "" {
			t.Errorf("agent %q has no effective persona", name)
		}
		// The persona has to have been read from the project rather than from
		// the bundle, which is the whole point of copying it there.
		if strings.HasPrefix(agent.Persona.Source, BuiltinV1) {
			t.Errorf("agent %q persona source = %q, want the project directory", name, agent.Persona.Source)
		}
		for _, field := range []string{"role", "backend", "model", "instances", "persona"} {
			key := "agents." + name + "." + field
			if got := resolved.Origins[key]; got != resolved.Path {
				t.Errorf("origin[%q] = %q, want %q", key, got, resolved.Path)
			}
		}
	}
}

// The generated file has to say what the bundle says, or an operator reading it
// is reading a description of something else. Only the persona source differs,
// because the text now lives in the project.
func TestScaffoldStatesExactlyWhatTheBundleWouldHaveSupplied(t *testing.T) {
	t.Parallel()

	generated := loadScaffold(t, ScaffoldOptions{ProductID: "example", Repository: "."}).Config
	inherited := loadProject(t, minimalProjectConfig, nil).Config

	if len(generated.Agents) != len(inherited.Agents) {
		t.Fatalf("agents = %d, want the %d the bundle supplies", len(generated.Agents), len(inherited.Agents))
	}
	for name, want := range inherited.Agents {
		got, ok := generated.Agents[name]
		if !ok {
			t.Fatalf("agent %q is missing from the generated configuration", name)
		}
		want.Persona.Source = got.Persona.Source
		if got != want {
			t.Errorf("agent %q = %+v, want %+v", name, got, want)
		}
	}
	if generated.Execution != inherited.Execution {
		t.Errorf("execution = %+v, want %+v", generated.Execution, inherited.Execution)
	}
	if generated.Approvals != inherited.Approvals {
		t.Errorf("approvals = %+v, want %+v", generated.Approvals, inherited.Approvals)
	}
	if generated.Product.Specifications != inherited.Product.Specifications {
		t.Errorf("specifications = %q, want %q", generated.Product.Specifications, inherited.Product.Specifications)
	}
}

func TestScaffoldCarriesTheProjectsOwnChecks(t *testing.T) {
	t.Parallel()

	resolved := loadScaffold(t, ScaffoldOptions{
		ProductID:  "example",
		Repository: ".",
		Checks:     []string{"go test ./...", "go vet ./..."},
	})
	if len(resolved.Config.Checks) != 2 || resolved.Config.Checks[0] != "go test ./..." {
		t.Fatalf("checks = %v", resolved.Config.Checks)
	}
}

func TestScaffoldRefusesAProductItCannotName(t *testing.T) {
	t.Parallel()

	if _, err := NewScaffold(BuiltinV1, ScaffoldOptions{ProductID: "Not An Id", Repository: "."}); err == nil {
		t.Fatal("NewScaffold() accepted an invalid product id")
	}
	if _, err := NewScaffold("builtin:v99", ScaffoldOptions{ProductID: "example", Repository: "."}); err == nil {
		t.Fatal("NewScaffold() accepted an unknown bundle")
	}
}

// Durations are written back in the spelling an operator would type, not in the
// one Go prints.
func TestScaffoldWritesReadableDurations(t *testing.T) {
	t.Parallel()

	scaffold, err := NewScaffold(BuiltinV1, ScaffoldOptions{ProductID: "example", Repository: "."})
	if err != nil {
		t.Fatalf("NewScaffold() error = %v", err)
	}
	rendered := string(scaffold.Config.Content)
	for _, want := range []string{"usage_limit_max_pause: 6h\n", "usage_limit_unknown_reset_pause: 30m\n"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered configuration does not contain %q", want)
		}
	}
}

// loadScaffold writes a generated project into a temporary directory and loads
// it back, which is the only way to prove that what init writes is what the
// harness can read.
func loadScaffold(t *testing.T, options ScaffoldOptions) Resolved {
	t.Helper()
	scaffold, err := NewScaffold(BuiltinV1, options)
	if err != nil {
		t.Fatalf("NewScaffold() error = %v", err)
	}
	if len(scaffold.Personas) == 0 {
		t.Fatal("scaffold copied no personas")
	}
	directory := filepath.Join(t.TempDir(), DirectoryName)
	for _, file := range scaffold.Files() {
		path := filepath.Join(directory, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		if err := os.WriteFile(path, file.Content, 0o600); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
	}
	resolved, err := LoadResolved(filepath.Join(directory, FileName))
	if err != nil {
		t.Fatalf("LoadResolved() error = %v", err)
	}
	return resolved
}
