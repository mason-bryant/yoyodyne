package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
)

// driftingProject is a project whose template has since improved one value it
// never edited: the baseline says the template supplied "sonnet", the project
// still says "sonnet", and the template in this executable says "opus".
//
// It returns the configuration path. The project extends nothing, because a
// project that inherits has no gap for a baseline to close.
func driftingProject(t *testing.T, improved string) string {
	t.Helper()

	lock, err := config.NewLock(config.BuiltinV1)
	if err != nil {
		t.Fatalf("NewLock() error = %v", err)
	}
	if _, recorded := lock.Values[improved]; !recorded {
		t.Fatalf("the baseline records no %s, so nothing here would be an improvement", improved)
	}
	lock.Values[improved] = "sonnet"

	path := writeProjectConfig(t, `version: 1
product:
  id: drifting
  repository: .
approvals:
  brief: human
  goals: human
  designs: automatic
  integration: human
checks:
  - make test
agents:
  developer:
    role: developer
    backend: claude-code
    model: sonnet
  reviewer:
    role: reviewer
    backend: claude-code
    model: opus
`)
	if err := os.WriteFile(config.LockPath(path), lock.Render(), 0o600); err != nil {
		t.Fatalf("write baseline: %v", err)
	}
	return path
}

// The operator's decision: the available class speaks without being asked, on a
// command already being run, and changes nothing about what that command
// reported.
func TestConfigValidateSpeaksAnAvailableImprovementWithoutBeingAsked(t *testing.T) {
	t.Parallel()

	path := driftingProject(t, "agents.developer.model")

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"config", "validate", "--config", path}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, want the exit code unchanged by a notice; stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "configuration valid") {
		t.Fatalf("stdout = %q, want the validation the operator asked for", stdout.String())
	}
	// On standard error, beside the answer, rather than mixed into it: the
	// notice is an aside about a configuration that is valid.
	notice := stderr.String()
	for _, want := range []string{"agents.developer.model", config.BuiltinV1, "yoyo config drift"} {
		if !strings.Contains(notice, want) {
			t.Errorf("stderr = %q, want it to name %q", notice, want)
		}
	}
}

// Silence is what makes speaking on every run acceptable. A project that has
// nothing available says nothing at all, and a project that never recorded a
// baseline is not told about it on every invocation either.
func TestConfigValidateIsSilentWhenThereIsNothingToSay(t *testing.T) {
	t.Parallel()

	withBaseline := driftingProject(t, "agents.developer.model")
	// Adopt it by hand, which is what the report is for: the project and the
	// template now agree, so there is nothing to say.
	current, err := os.ReadFile(withBaseline)
	if err != nil {
		t.Fatalf("read configuration: %v", err)
	}
	adopted := strings.Replace(string(current), "    model: sonnet", "    model: opus", 1)
	if err := os.WriteFile(withBaseline, []byte(adopted), 0o600); err != nil {
		t.Fatalf("write configuration: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"config", "validate", "--config", withBaseline}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want silence once the improvement is taken", stderr.String())
	}

	// And a project with no baseline at all.
	unbaselined := writeProjectConfig(t, portableConfig)
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"config", "validate", "--config", unbaselined}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want silence where there is no baseline to compare against", stderr.String())
	}
}

// The same derivation reaches the machine-readable form, so anything automating
// a repair reads it structurally rather than parsing the line.
func TestConfigValidateCarriesTheComparisonInItsJSON(t *testing.T) {
	t.Parallel()

	path := driftingProject(t, "agents.developer.model")

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"config", "validate", "--config", path, "--json"}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	var report struct {
		Status string       `json:"status"`
		Drift  config.Drift `json:"drift"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("Unmarshal() error = %v, stdout = %q", err, stdout.String())
	}
	if report.Status != "valid" {
		t.Fatalf("status = %q, want a valid configuration", report.Status)
	}
	available := report.Drift.Available()
	if len(available) != 1 || available[0].Key != "agents.developer.model" {
		t.Fatalf("available = %+v, want the one improved value", available)
	}
	if available[0].Bundle != "opus" || available[0].Yours != "sonnet" {
		t.Errorf("available = %+v, want both sides carried", available[0])
	}
}

// doctor says the same thing the same way. Two surfaces disagreeing about
// whether a project is current is a disagreement only the operator could settle.
func TestDoctorSpeaksTheSameNoticeAsConfigValidate(t *testing.T) {
	t.Parallel()

	path := driftingProject(t, "agents.developer.model")

	var validateOut, validateErr bytes.Buffer
	if code := Run([]string{"config", "validate", "--config", path}, &validateOut, &validateErr, "test"); code != 0 {
		t.Fatalf("config validate code = %d", code)
	}
	var doctorOut, doctorErr bytes.Buffer
	// doctor exits 1 here: this temporary project has no repository, no tracker,
	// and no authenticated provider. What is asserted is the notice beside that
	// diagnosis, which the diagnosis neither suppresses nor changes.
	Run([]string{"doctor", "--config", path, "--quiet"}, &doctorOut, &doctorErr, "test")

	notice := strings.TrimSpace(validateErr.String())
	if notice == "" {
		t.Fatal("config validate said nothing, so there is nothing to compare doctor against")
	}
	if !strings.Contains(doctorErr.String(), notice) {
		t.Errorf("doctor stderr = %q, want the same line config validate printed: %q", doctorErr.String(), notice)
	}
	// The report itself is untouched: a notice is not a finding, and it does not
	// appear among the things that would stop work running.
	if strings.Contains(doctorOut.String(), "config drift") {
		t.Errorf("doctor stdout = %q, want the notice kept out of the diagnosis", doctorOut.String())
	}
}

func TestDoctorCarriesTheNoticeBesideItsJSONWithoutDisturbingIt(t *testing.T) {
	t.Parallel()

	path := driftingProject(t, "agents.developer.model")

	var stdout, stderr bytes.Buffer
	Run([]string{"doctor", "--config", path, "--json"}, &stdout, &stderr, "test")
	var report struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("Unmarshal() error = %v, stdout = %q", err, stdout.String())
	}
	if report.SchemaVersion == 0 {
		t.Fatalf("stdout = %q, want the report unchanged on standard output", stdout.String())
	}
	if !strings.Contains(stderr.String(), "agents.developer.model") {
		t.Errorf("stderr = %q, want the notice beside the machine-readable report", stderr.String())
	}
}

// The command the notices point at has to exist and show what the notice
// deliberately leaves out.
func TestConfigDriftShowsBothSidesAndTheClassesTheNoticeStaysQuietAbout(t *testing.T) {
	t.Parallel()

	path := driftingProject(t, "agents.developer.model")
	// A value this project moved and the template did not, which the notice
	// never mentions and this report names as the project's own.
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read configuration: %v", err)
	}
	edited := strings.Replace(string(current), "    model: opus", "    model: haiku", 1)
	if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
		t.Fatalf("write configuration: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"config", "drift", "--config", path, "--all"}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	report := stdout.String()
	for _, want := range []string{"available", "agents.developer.model", "sonnet", "opus", "yours", "agents.reviewer.model", "haiku"} {
		if !strings.Contains(report, want) {
			t.Errorf("stdout = %q, want it to carry %q", report, want)
		}
	}
}

// A project that predates the baseline keeps working and is told what it does
// not have. Refusing would break it over a file that decides nothing about how
// it runs.
func TestConfigDriftAnswersUnknownForAProjectWithNoBaseline(t *testing.T) {
	t.Parallel()

	path := writeProjectConfig(t, portableConfig)

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"config", "drift", "--config", path}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, want a project with no baseline to be reported rather than refused; stderr = %q", code, stderr.String())
	}
	report := stdout.String()
	if !strings.Contains(report, "no baseline") {
		t.Errorf("stdout = %q, want it to say there is no baseline", report)
	}
	// `yoyo init --force` is the only thing that writes a baseline, and it
	// regenerates the configuration and every persona from the template while it
	// does. Offering it as "regenerate one" would send an operator to discard the
	// edits this report exists to surface, so its cost is stated where it is
	// named.
	if !strings.Contains(report, "init --force") {
		t.Fatalf("stdout = %q, want the only command that writes a baseline named", report)
	}
	for _, want := range []string{"every persona", "regenerate whole"} {
		if !strings.Contains(report, want) {
			t.Errorf("stdout = %q, want it to say %q about `yoyo init --force`", report, want)
		}
	}
}

// A baseline that is on disk and cannot be used is a different situation from
// one that was never written, and the operator can see the file. Reporting it as
// missing would tell them the opposite of the facts, and would point them at a
// remedy for the wrong problem.
func TestConfigDriftTellsAnUnusableBaselineApartFromAMissingOne(t *testing.T) {
	t.Parallel()

	path := writeProjectConfig(t, portableConfig)
	// A baseline whose revision does not digest its values: what a hand edit
	// leaves behind.
	if err := os.WriteFile(config.LockPath(path),
		[]byte("version: 1\nbundle: builtin:v1\nrevision: bnd-000000000000\nvalues:\n  agents.developer.model: \"opus\"\n"), 0o600); err != nil {
		t.Fatalf("write baseline: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"config", "drift", "--config", path}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, want an unusable baseline reported rather than refused; stderr = %q", code, stderr.String())
	}
	report := stdout.String()
	if strings.Contains(report, "no baseline") {
		t.Errorf("stdout = %q, want a baseline that is present and refused not called missing", report)
	}
	if !strings.Contains(report, "unusable baseline") {
		t.Errorf("stdout = %q, want the headline to say the baseline cannot be used", report)
	}
	// The remedy for a corrupt record is the copy in version control, not a
	// regeneration that takes the configuration and the personas with it.
	if !strings.Contains(report, "version control") {
		t.Errorf("stdout = %q, want the non-destructive way back named first", report)
	}
	if strings.Contains(report, "init --force") && !strings.Contains(report, "every persona") {
		t.Errorf("stdout = %q, names `yoyo init --force` without saying what it overwrites", report)
	}
}

// The baseline is written where the configuration is, by the command that
// materializes a project, because a project that never recorded one has nothing
// to compare and this is the only moment the record is true by construction.
func TestInitWritesTheBaselineBesideTheConfigurationItGenerated(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"init", "--directory", directory, "--product", "baselined"}, &stdout, &stderr, "test"); code != 0 {
		t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
	}
	lockPath := filepath.Join(directory, config.DirectoryName, config.LockFileName)
	if !strings.Contains(stdout.String(), lockPath) {
		t.Errorf("stdout = %q, want the baseline reported among the files written", stdout.String())
	}
	content, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read baseline: %v", err)
	}
	lock, err := config.DecodeLock(bytes.NewReader(content))
	if err != nil {
		t.Fatalf("DecodeLock() error = %v", err)
	}
	if lock.Bundle != config.BuiltinV1 {
		t.Errorf("bundle = %q, want %q", lock.Bundle, config.BuiltinV1)
	}
	// A project generated a moment ago has nothing available, which is what
	// makes the notice's silence mean something.
	resolved, err := config.LoadResolved(filepath.Join(directory, config.DirectoryName, config.FileName))
	if err != nil {
		t.Fatalf("LoadResolved() error = %v", err)
	}
	drift, unknown := config.ReadDrift(resolved)
	if !drift.Current() {
		t.Fatalf("a project generated a moment ago is not current: %s", unknown.Reason)
	}
	if notice := drift.Notice(); notice != "" {
		t.Errorf("Notice() = %q, want silence", notice)
	}

	// The property the whole comparison rests on, and the one nothing above
	// would catch: every value the baseline recorded is the value the generated
	// project actually holds. A persona copied into the project that did not
	// digest to what the baseline recorded would be reported as the operator's
	// own for ever after, so a later improvement to it would reach them as a
	// conflict they never made rather than as an improvement they could take.
	values := config.ProjectValues(resolved.Config)
	for key, recorded := range lock.Values {
		if got := values[key]; got != recorded {
			t.Errorf("%s: the generated project holds %q and the baseline recorded %q", key, got, recorded)
		}
	}
	if _, digested := lock.Values["agents.developer.persona.text"]; !digested {
		t.Error("the baseline recorded no persona text, so the loop above proved nothing about the personas")
	}
}
