package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/domain"
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
	// Every value is the project file's own. The exceptions are the two the
	// generated file deliberately leaves to follow something it does state: the
	// repository id follows the product id, and the triage repair grant follows
	// the repair budget -- derivations from values in the same file rather than
	// values arriving from outside it. Nothing is a harness default either,
	// because the generated file writes down what the harness would have filled
	// in. This is what the documentation promises an operator reading
	// `config show --origins`, so it is asserted exactly.
	derived := map[string]string{
		"product.repository_id":        OriginDerived,
		"triage.repair_grant_attempts": OriginDerivedRepairGrant,
	}
	for key, origin := range resolved.Origins {
		want := resolved.Path
		if derivedOrigin, ok := derived[key]; ok {
			want = derivedOrigin
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
		// The account the agent runs under is stated in the generated file like
		// everything else about it, rather than left to be derived from a mapping
		// the operator would have to know exists.
		if agent.Account != DefaultAccountAlias {
			t.Errorf("agent %q account = %q, want %q", name, agent.Account, DefaultAccountAlias)
		}
		for _, field := range []string{"role", "backend", "model", "account", "instances", "persona"} {
			key := "agents." + name + "." + field
			if got := resolved.Origins[key]; got != resolved.Path {
				t.Errorf("origin[%q] = %q, want %q", key, got, resolved.Path)
			}
		}
	}
	if aliases := resolved.Config.AccountAliases(); len(aliases) != 1 || aliases[0] != DefaultAccountAlias {
		t.Errorf("accounts = %v, want the one account the generated file states", aliases)
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
	if !reflect.DeepEqual(generated.Approvals, inherited.Approvals) {
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
		Detection: Detection{Checks: []CheckProposal{
			{Command: "go test ./...", Source: "go.mod"},
			{Command: "go vet ./...", Source: "go.mod"},
		}},
	})
	if len(resolved.Config.Checks) != 2 || resolved.Config.Checks[0] != "go test ./..." {
		t.Fatalf("checks = %v", resolved.Config.Checks)
	}
}

// A proposal an operator cannot trace back to something in their own repository
// is a guess, so the generated file names what each entry was derived from, once
// per artifact rather than once per line.
func TestScaffoldNamesWhereEachProposedCheckCameFrom(t *testing.T) {
	t.Parallel()

	scaffold, err := NewScaffold(BuiltinV1, ScaffoldOptions{
		ProductID:  "example",
		Repository: ".",
		Detection: Detection{Checks: []CheckProposal{
			{Command: "go test ./...", Source: "go.mod"},
			{Command: "go vet ./...", Source: "go.mod"},
			{Command: "mvn --batch-mode --quiet verify", Source: "pom.xml"},
		}},
	})
	if err != nil {
		t.Fatalf("NewScaffold() error = %v", err)
	}
	rendered := string(scaffold.Config.Content)
	want := "checks:\n  # from go.mod\n  - go test ./...\n  - go vet ./...\n  # from pom.xml\n  - mvn --batch-mode --quiet verify\n"
	if !strings.Contains(rendered, want) {
		t.Errorf("rendered checks section does not contain:\n%s\ngot:\n%s", want, rendered)
	}
	if strings.Contains(rendered, CandidateMarker) {
		t.Error("a configuration with nothing left to choose still carries the choose-one marker")
	}
}

// A candidate is written so that choosing it costs one character: deleting the
// leading "#" has to leave a valid entry of the list above it.
func TestScaffoldWritesCandidatesCommentedUnderAMarker(t *testing.T) {
	t.Parallel()

	detection := Detection{Candidates: []CheckProposal{
		{Command: "python3 -m pytest -q", Source: "tests", Reason: "nothing here names the test runner"},
		{Command: "python3 -m unittest discover -q -s tests -t .", Source: "tests", Reason: "nothing here names the test runner"},
	}}
	scaffold, err := NewScaffold(BuiltinV1, ScaffoldOptions{ProductID: "example", Repository: ".", Detection: detection})
	if err != nil {
		t.Fatalf("NewScaffold() error = %v", err)
	}
	rendered := string(scaffold.Config.Content)
	if !strings.Contains(rendered, CandidateMarker) {
		t.Fatalf("rendered configuration carries no %q marker:\n%s", CandidateMarker, rendered)
	}
	// Nothing ambiguous was decided on the operator's behalf.
	if !strings.Contains(rendered, "checks: []\n") {
		t.Error("candidates were written into the checks list rather than beside it")
	}
	for _, candidate := range detection.Candidates {
		if !strings.Contains(rendered, "#  - "+candidate.Command+"\n") {
			t.Errorf("candidate %q is not commented out at the list's own indentation", candidate.Command)
		}
	}
	if !strings.Contains(rendered, "nothing here names the test runner") {
		t.Error("the candidates do not say why they could not be chosen")
	}

	// Uncommenting is the whole gesture the file asks for, so it is performed
	// exactly as written -- open the empty list, delete one leading "#" per
	// chosen line, change nothing else -- and the result has to load.
	uncommented := strings.Replace(rendered, "checks: []\n", "checks:\n", 1)
	for _, candidate := range detection.Candidates {
		uncommented = strings.Replace(uncommented, "#  - "+candidate.Command+"\n", "  - "+candidate.Command+"\n", 1)
	}
	directory := filepath.Join(t.TempDir(), DirectoryName)
	for _, file := range scaffold.Files() {
		path := filepath.Join(directory, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		content := file.Content
		if file.Path == FileName {
			content = []byte(uncommented)
		}
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
	}
	loaded, err := Load(filepath.Join(directory, FileName))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(loaded.Checks) != len(detection.Candidates) || loaded.Checks[0] != detection.Candidates[0].Command {
		t.Errorf("checks = %v, want the candidates that were uncommented", loaded.Checks)
	}
}

// A demand to choose is only honest where a run cannot happen until somebody
// does. Where init wrote a usable list and something else about the toolchain is
// merely open, the file says so without demanding anything.
func TestScaffoldDemandsAChoiceOnlyWhereTheListIsEmpty(t *testing.T) {
	t.Parallel()

	scaffold, err := NewScaffold(BuiltinV1, ScaffoldOptions{
		ProductID:  "example",
		Repository: ".",
		Detection: Detection{
			Checks: []CheckProposal{{Command: "make check", Source: `Makefile (its "check" target)`}},
			Candidates: []CheckProposal{
				{Command: "python3 -m pytest -q", Source: "tests/test_calc.py", Reason: "nothing here names the test runner"},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewScaffold() error = %v", err)
	}
	rendered := string(scaffold.Config.Content)
	if strings.Contains(rendered, CandidateMarker) {
		t.Errorf("a configuration that already runs still demands a choice:\n%s", rendered)
	}
	if !strings.Contains(rendered, UndecidedMarker) {
		t.Errorf("the open question is not named at all:\n%s", rendered)
	}
	if !strings.Contains(rendered, "#  - python3 -m pytest -q\n") {
		t.Error("the undecided command is not offered")
	}
}

// A command init read and decided against is not a decision the operator owes,
// so it is written under its own heading rather than under the one that means a
// run is blocked until somebody answers.
func TestScaffoldWritesSupersededCommandsAsAlternativesRatherThanDemands(t *testing.T) {
	t.Parallel()

	scaffold, err := NewScaffold(BuiltinV1, ScaffoldOptions{
		ProductID:  "example",
		Repository: ".",
		Detection: Detection{
			Checks: []CheckProposal{{Command: "make check", Source: `Makefile (its "check" target)`}},
			Alternatives: []CheckProposal{
				{Command: "go test ./...", Source: "go.mod", Reason: "the Makefile above already names this project's entry point"},
				{Command: "go vet ./...", Source: "go.mod", Reason: "the Makefile above already names this project's entry point"},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewScaffold() error = %v", err)
	}
	rendered := string(scaffold.Config.Content)
	for _, unwanted := range []string{CandidateMarker, UndecidedMarker} {
		if strings.Contains(rendered, unwanted) {
			t.Errorf("a settled configuration carries %q:\n%s", unwanted, rendered)
		}
	}
	if !strings.Contains(rendered, AlternativeMarker) {
		t.Errorf("the superseded commands are not headed as alternatives:\n%s", rendered)
	}
	for _, alternative := range []string{"#  - go test ./...\n", "#  - go vet ./...\n"} {
		if !strings.Contains(rendered, alternative) {
			t.Errorf("alternative %q was dropped rather than offered", strings.TrimSpace(alternative))
		}
	}
	if !strings.Contains(rendered, "the Makefile above already names this project's entry point") {
		t.Error("the alternatives do not say what displaced them")
	}
}

// A project that proposed nothing keeps the placeholder and the examples that
// were there before detection existed, plus somewhere to read about them.
func TestScaffoldKeepsThePlaceholderWhenNothingWasDetected(t *testing.T) {
	t.Parallel()

	scaffold, err := NewScaffold(BuiltinV1, ScaffoldOptions{ProductID: "example", Repository: "."})
	if err != nil {
		t.Fatalf("NewScaffold() error = %v", err)
	}
	rendered := string(scaffold.Config.Content)
	for _, want := range []string{"checks: []\n", "#   # Go\n", "#   # TypeScript / Node\n", "#   # Python\n", "#   # Java (Maven)\n", "#   # Java (Gradle)\n", checksGuide} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered configuration does not contain %q", want)
		}
	}
	for _, unwanted := range []string{CandidateMarker, UndecidedMarker, AlternativeMarker} {
		if strings.Contains(rendered, unwanted) {
			t.Errorf("a project with nothing detected carries %q", unwanted)
		}
	}
}

// A new project's first contact with Yoyodyne is the product manager asking
// what the product is, so the briefing discipline that opening obeys has to
// survive into the generated project rather than staying in the bundle. What
// init writes is the bundle's own persona text, so asserting on the generated
// project asserts on the copy shipped inside the executable.
func TestScaffoldCarriesTheBriefingDiscipline(t *testing.T) {
	t.Parallel()

	generated := loadScaffold(t, ScaffoldOptions{ProductID: "example", Repository: "."}).Config
	var persona string
	for _, agent := range generated.Agents {
		if agent.Role == domain.RoleProductManager {
			persona = agent.Persona.Text
		}
	}
	if persona == "" {
		t.Fatal("the generated project has no product-manager persona")
	}
	for _, want := range []string{
		"Ask exactly one question per reply",
		"open with how many there are",
		"say there are three, say the order you will ask them in",
	} {
		if !strings.Contains(persona, want) {
			t.Errorf("generated product-manager persona does not carry %q", want)
		}
	}
}

// A priority change is where the human's directive meets everything already
// queued behind it, and the item it pushed back is only cheap to argue about in
// the reply that made the change. That the product manager names the
// displacement has to ship with the bundle, for the same reason the briefing
// discipline does: it is how the product manager behaves in every project, not
// something this one taught it.
//
// This covers the bundle and nothing else. A run in this repository reads the
// project's own persona copy rather than the bundle's, and that copy is a
// protected path yoyodyne-ifd.126 grants no exception for, so it is unchanged
// and no check here or anywhere else asserts on it.
// TestChatResolvesTheConfiguredProductManager in internal/cli records why,
// beside the copy that would carry the assertion.
func TestScaffoldCarriesTheDisplacementRule(t *testing.T) {
	t.Parallel()

	generated := loadScaffold(t, ScaffoldOptions{ProductID: "example", Repository: "."}).Config
	var persona string
	for _, agent := range generated.Agents {
		if agent.Role == domain.RoleProductManager {
			persona = agent.Persona.Text
		}
	}
	if persona == "" {
		t.Fatal("the generated project has no product-manager persona")
	}
	for _, want := range []string{
		"Report what every priority change displaces",
		"the item or items that were next and are no longer",
		"Say plainly when a change displaces nothing",
	} {
		if !strings.Contains(persona, want) {
			t.Errorf("generated product-manager persona does not carry %q", want)
		}
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
