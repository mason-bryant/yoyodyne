package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/doctor"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

// TestDoctorPutsEveryRemedyUnderWhatItRemedies is the whole of why this reads
// differently from a status listing. A list of problems followed by a list of
// commands is a list somebody has to match up by hand, which is the work this
// command exists to have already done.
func TestDoctorPutsEveryRemedyUnderWhatItRemedies(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	renderDiagnosis(&stdout, doctor.Report{
		Status:  doctor.StatusProblem,
		Product: "example",
		Config:  "/p/.yoyodyne/config.yaml",
		Findings: []doctor.Finding{
			{Check: "git", Status: doctor.StatusOK, Summary: "git version 2.48.1"},
			{Check: "tracker", Status: doctor.StatusProblem, Summary: "bd could not read this project", Remedy: "bd init"},
		},
	}, false)

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	var remedy int
	for index, line := range lines {
		if strings.Contains(line, "fix: bd init") {
			remedy = index
		}
	}
	if remedy == 0 || !strings.Contains(lines[remedy-1], "bd could not read this project") {
		t.Fatalf("the remedy is not under what it remedies:\n%s", stdout.String())
	}
}

// TestDoctorSaysAHealthyInstallationIsHealthy holds the other half of the
// promise. Silence and health read the same, and an operator who has finished
// fixing things has to be told so.
func TestDoctorSaysAHealthyInstallationIsHealthy(t *testing.T) {
	t.Parallel()

	healthy := verdict(doctor.Report{Status: doctor.StatusOK, Product: "example", Findings: []doctor.Finding{
		{Check: "git", Status: doctor.StatusOK, Summary: "git version 2.48.1"},
	}})
	if !strings.Contains(healthy, "ready to run work") || strings.Contains(healthy, "problem") {
		t.Fatalf("verdict = %q, want a plain statement of health", healthy)
	}

	warned := verdict(doctor.Report{Status: doctor.StatusWarning, Product: "example", Findings: []doctor.Finding{
		{Check: "binary", Status: doctor.StatusWarning, Summary: "a different build is on PATH", Remedy: "go install ..."},
	}})
	// A warning is something worth knowing about an installation that works, so
	// it must not be reported as one that does not.
	if !strings.Contains(warned, "ready to run work") || !strings.Contains(warned, "1 warning") {
		t.Fatalf("verdict = %q, want work still runnable with a warning", warned)
	}
}

// TestDoctorQuietLeavesOutWhatIsAlreadyHealthy keeps the flag honest: what it
// removes is the healthy findings and nothing else, so a broken installation
// reads identically either way.
func TestDoctorQuietLeavesOutWhatIsAlreadyHealthy(t *testing.T) {
	t.Parallel()

	report := doctor.Report{Status: doctor.StatusProblem, Product: "example", Findings: []doctor.Finding{
		{Check: "git", Status: doctor.StatusOK, Summary: "git version 2.48.1"},
		{Check: "tracker", Status: doctor.StatusProblem, Summary: "bd could not read this project", Remedy: "bd init"},
	}}
	var quiet bytes.Buffer
	renderDiagnosis(&quiet, report, true)
	if strings.Contains(quiet.String(), "git version") {
		t.Fatalf("--quiet reported what is already healthy:\n%s", quiet.String())
	}
	if !strings.Contains(quiet.String(), "bd init") {
		t.Fatalf("--quiet dropped a remedy:\n%s", quiet.String())
	}
}

// TestDoctorReportsAMissingConfigurationAndExitsNonzero walks the command end to
// end from a directory that has never been initialized, which is where a
// newcomer following the readme is standing when the first thing goes wrong.
func TestDoctorReportsAMissingConfigurationAndExitsNonzero(t *testing.T) {
	t.Chdir(t.TempDir())

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"doctor"}, &stdout, &stderr, "v1.2.3"); code != 1 {
		t.Fatalf("Run() code = %d, want 1 for an installation that cannot run work; stdout = %q", code, stdout.String())
	}
	if !strings.Contains(stdout.String(), "yoyo init") {
		t.Fatalf("stdout = %q, want the command that fixes a missing configuration", stdout.String())
	}
}

// TestDoctorJSONCarriesTheSameFindingsWithRemedies is the contract the skill
// child consumes. What an operator reads and what an agent acts on have to be
// the same findings, so the machine-readable form carries the remedy rather
// than leaving it in the prose.
func TestDoctorJSONCarriesTheSameFindingsWithRemedies(t *testing.T) {
	t.Chdir(t.TempDir())

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"doctor", "--json"}, &stdout, &stderr, "v1.2.3"); code != 1 {
		t.Fatalf("Run() code = %d, want 1; stderr = %q", code, stderr.String())
	}
	var report doctor.Report
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("Unmarshal() error = %v, output = %q", err, stdout.String())
	}
	if report.SchemaVersion != doctor.SchemaVersion {
		t.Fatalf("schema_version = %d, want %d", report.SchemaVersion, doctor.SchemaVersion)
	}
	if report.Status != doctor.StatusProblem || len(report.Findings) == 0 {
		t.Fatalf("report = %#v, want findings against a broken installation", report)
	}
	for _, finding := range report.Findings {
		if finding.Status != doctor.StatusOK && strings.TrimSpace(finding.Remedy) == "" {
			t.Fatalf("finding %q is %s in JSON with no remedy to act on", finding.Check, finding.Status)
		}
	}
}

// TestDoctorReadsThisExecutablesOwnVersionOutput ties the drift check to the
// bytes it actually compares. `yoyo version` prints the bare version, and the
// check is exact string equality against that, so nothing but this holds the two
// together: a banner, a prefix, or a second line there would put a permanent
// spurious warning on every healthy installation, which is how a check stops
// being read at all.
func TestDoctorReadsThisExecutablesOwnVersionOutput(t *testing.T) {
	t.Parallel()

	const version = "v1.2.3"
	var printed, discarded bytes.Buffer
	if code := Run([]string{"version"}, &printed, &discarded, version); code != 0 {
		t.Fatalf("Run(version) code = %d, stderr = %q", code, discarded.String())
	}

	matched := diagnoseAgainstVersionOutput(t, printed.String(), version)
	if matched.Status != doctor.StatusOK {
		t.Fatalf("binary = %s against this executable's own `version` output %q: %s",
			matched.Status, printed.String(), matched.Summary)
	}
	if matched.Remedy != "" {
		t.Fatalf("a healthy build carries the remedy %q", matched.Remedy)
	}

	// And the comparison is live rather than trivially passing: the same output
	// against a different running build is the drift it exists to catch.
	drifted := diagnoseAgainstVersionOutput(t, printed.String(), "v9.9.9")
	if drifted.Status != doctor.StatusWarning || drifted.Remedy == "" {
		t.Fatalf("binary = %#v against a build that really did drift", drifted)
	}
}

// diagnoseAgainstVersionOutput runs one diagnosis whose `yoyo version` answers
// with exactly the given bytes, and returns what it made of the build.
func diagnoseAgainstVersionOutput(t *testing.T, stdout, running string) doctor.Finding {
	t.Helper()
	report := doctor.Diagnose(context.Background(), doctor.Environment{
		Runner:      versionRunner{stdout: stdout},
		LookPath:    func(program string) (string, error) { return "/usr/local/bin/" + program, nil },
		Getenv:      func(string) string { return "" },
		UserHomeDir: func() (string, error) { return t.TempDir(), nil },
		GOOS:        runtime.GOOS,
		Version:     running,
		// The configuration is deliberately unavailable: this is about the build
		// on PATH, which is read before anything a project configures.
		Load: func() (config.Resolved, error) { return config.Resolved{}, errors.New("no configuration here") },
	})
	for _, finding := range report.Findings {
		if finding.Check == "binary" {
			return finding
		}
	}
	t.Fatalf("the diagnosis never compared the build on PATH: %#v", report.Findings)
	return doctor.Finding{}
}

// versionRunner answers `yoyo version` with what this executable's own version
// command printed, and everything else with a bare success.
type versionRunner struct{ stdout string }

func (r versionRunner) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	if len(command.Args) == 1 && command.Args[0] == "version" {
		return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: r.stdout}, nil
	}
	return execution.ProcessResult{Status: execution.ProcessSucceeded}, nil
}

func TestDoctorRejectsPositionalArguments(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"doctor", "everything"}, &stdout, &stderr, "v1.2.3"); code != 2 {
		t.Fatalf("Run() code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "does not accept positional arguments") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

// TestDoctorIsInTheCommandListAndTheHelp keeps the verb discoverable. The
// product manager is described the surfaces this executable ships and cannot
// run a command to find out what they are.
func TestDoctorIsInTheCommandListAndTheHelp(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"help"}, &stdout, &stderr, "v1.2.3"); code != 0 {
		t.Fatalf("Run() code = %d", code)
	}
	if !strings.Contains(stdout.String(), "doctor") {
		t.Fatalf("the command list does not name doctor: %q", stdout.String())
	}
	if !strings.Contains(commandHelp(), "yoyo doctor [options]") {
		t.Fatal("commandHelp() does not carry doctor's own usage")
	}
}
