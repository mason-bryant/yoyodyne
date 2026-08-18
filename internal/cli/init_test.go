package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
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

// The three-step adoption path breaks if the operator has to hand-write a YAML
// list to get past step two, so init reads what the repository already
// announces and proposes checks from it. What it wrote and what it only found
// are reported separately, because a candidate is deliberately not written.
func TestRunInitProposesChecksFromTheProjectsOwnFiles(t *testing.T) {
	t.Parallel()

	project := filepath.Join(t.TempDir(), "example-project")
	if err := os.MkdirAll(filepath.Join(project, "tests"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, "go.mod"), []byte("module example\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, "tests", "test_calc.py"), []byte("import unittest\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := Run([]string{"init", "--directory", project, "--json"}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	var result struct {
		Checks   []string `json:"checks"`
		Detected struct {
			Checks []struct {
				Command string `json:"command"`
				Source  string `json:"source"`
			} `json:"checks"`
			Candidates []struct {
				Command string `json:"command"`
				Reason  string `json:"reason"`
			} `json:"candidates"`
		} `json:"detected"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if want := []string{"go test ./...", "go vet ./..."}; !reflect.DeepEqual(result.Checks, want) {
		t.Errorf("checks = %v, want %v", result.Checks, want)
	}
	for _, detected := range result.Detected.Checks {
		if detected.Source != "go.mod" {
			t.Errorf("check %q source = %q, want the file it was derived from", detected.Command, detected.Source)
		}
	}
	if len(result.Detected.Candidates) == 0 {
		t.Error("the Python tests with no runner named were decided rather than offered")
	}

	// The written file is the thing an operator reads, so what it says is
	// asserted there rather than only in the report.
	path := filepath.Join(project, config.DirectoryName, config.FileName)
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(loaded.Checks) != 2 || loaded.Checks[0] != "go test ./..." {
		t.Errorf("checks = %v, want the proposed list", loaded.Checks)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	for _, want := range []string{"# from go.mod", config.CandidateMarker, "python3 -m pytest -q"} {
		if !strings.Contains(string(contents), want) {
			t.Errorf("the generated configuration does not contain %q", want)
		}
	}
}

// A repository that announces nothing keeps the placeholder it always had, and
// is told where the per-language examples are.
func TestRunInitKeepsThePlaceholderWhenNothingIsDetected(t *testing.T) {
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
	contents, err := os.ReadFile(filepath.Join(project, config.DirectoryName, config.FileName))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !strings.Contains(string(contents), "checks: []\n") {
		t.Error("a project with nothing detected did not keep its empty checks list")
	}
	if strings.Contains(string(contents), config.CandidateMarker) {
		t.Error("a project with nothing detected was told to choose between nothing")
	}
	for _, want := range []string{"#   # Go\n", "#   # Python\n", "docs/configuration.md#checks"} {
		if !strings.Contains(string(contents), want) {
			t.Errorf("the generated configuration does not contain %q", want)
		}
	}
	if !strings.Contains(stdout.String(), "nothing in this project proposed one") {
		t.Errorf("stdout = %q, want it to say that nothing was proposed", stdout.String())
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
