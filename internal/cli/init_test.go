package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"yoyodyne/internal/config"
)

func TestRunInitWritesAProjectThatOwnsItsConfiguration(t *testing.T) {
	t.Parallel()

	project := filepath.Join(t.TempDir(), "example-project")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := Run([]string{"init", "--directory", project}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}

	path := filepath.Join(project, config.DirectoryName, config.FileName)
	resolved, err := config.LoadResolved(path)
	if err != nil {
		t.Fatalf("LoadResolved() error = %v", err)
	}
	if resolved.Config.Product.ID != "example-project" {
		t.Errorf("product id = %q, want the directory name", resolved.Config.Product.ID)
	}
	if resolved.Config.Extends != "" {
		t.Errorf("extends = %q, want a configuration that inherits nothing", resolved.Config.Extends)
	}
	if len(resolved.Sources) != 1 || resolved.Sources[0] != resolved.Path {
		t.Errorf("sources = %v, want only the project file", resolved.Sources)
	}
	// The personas are the project's own files, not a reference back into the
	// executable, so an operator can edit them where they were written.
	for name, agent := range resolved.Config.Agents {
		personaPath := filepath.Join(project, config.DirectoryName, filepath.FromSlash(agent.Persona.Path))
		if _, err := os.Stat(personaPath); err != nil {
			t.Errorf("agent %q persona was not copied into the project: %v", name, err)
		}
	}
	// A generated project cannot run work until its checks are named, and the
	// command says so rather than leaving it to be discovered by a refused run.
	if len(resolved.Config.Checks) != 0 {
		t.Errorf("checks = %v, want an empty list for the operator to fill in", resolved.Config.Checks)
	}
	if !strings.Contains(stdout.String(), "checks") {
		t.Errorf("stdout = %q, want it to name the checks that still have to be written", stdout.String())
	}
}

func TestRunInitRefusesToOverwriteWithoutForce(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := Run([]string{"init", "--directory", project, "--product", "example"}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}

	path := filepath.Join(project, config.DirectoryName, config.FileName)
	edited := "# edited by hand\n"
	if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"init", "--directory", project, "--product", "example"}, &stdout, &stderr, "test"); code != 1 {
		t.Fatalf("Run() code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "--force") {
		t.Errorf("stderr = %q, want it to name the flag that would overwrite", stderr.String())
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(contents) != edited {
		t.Error("a refused init overwrote the existing configuration")
	}

	// The same refusal reported as JSON still exits nonzero, so a script does
	// not have to parse the payload to notice that nothing was written.
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"init", "--directory", project, "--product", "example", "--json"}, &stdout, &stderr, "test"); code != 1 {
		t.Fatalf("Run() code = %d, want 1", code)
	}
	var failure struct {
		Status string `json:"status"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &failure); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if failure.Status != "failed" || !strings.Contains(failure.Error, "--force") {
		t.Fatalf("failure = %+v", failure)
	}

	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"init", "--directory", project, "--product", "example", "--force", "--json"}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	var result struct {
		Status string   `json:"status"`
		Bundle string   `json:"bundle"`
		Config string   `json:"config"`
		Files  []string `json:"files"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if result.Status != "written" || result.Config != path || result.Bundle != config.BuiltinV1 {
		t.Fatalf("result = %+v", result)
	}
	if len(result.Files) < 2 {
		t.Fatalf("files = %v, want the configuration and its personas", result.Files)
	}
	if _, err := config.Load(path); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
}

func TestRunInitRefusesAProductItCannotName(t *testing.T) {
	t.Parallel()

	project := filepath.Join(t.TempDir(), "Not An Id")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := Run([]string{"init", "--directory", project}, &stdout, &stderr, "test"); code != 1 {
		t.Fatalf("Run() code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "--product") {
		t.Errorf("stderr = %q, want it to name the flag that supplies an id", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(project, config.DirectoryName)); !os.IsNotExist(err) {
		t.Error("a refused init left a configuration directory behind")
	}
}

// Inheritance is still a capability rather than the shipped shape: a project
// that extends the bundle keeps loading exactly as it did.
func TestExtendingTheBundleStillWorks(t *testing.T) {
	t.Parallel()

	path := writeProjectConfig(t, portableConfig)
	resolved, err := config.LoadResolved(path)
	if err != nil {
		t.Fatalf("LoadResolved() error = %v", err)
	}
	if resolved.Config.Extends != config.BuiltinV1 {
		t.Fatalf("extends = %q, want %q", resolved.Config.Extends, config.BuiltinV1)
	}
	if len(resolved.Sources) != 2 || resolved.Sources[0] != config.BuiltinV1 {
		t.Fatalf("sources = %v, want the bundle then the project file", resolved.Sources)
	}
	if strings.TrimSpace(resolved.Config.Agents["reviewer"].Persona.Text) == "" {
		t.Error("an inherited persona no longer resolves")
	}
}
