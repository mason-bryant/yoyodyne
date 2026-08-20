package cli

// Diagnosing an installation, and saying what would fix it.
//
// The verb the design's CLI surface has promised since Milestone 0, and the one
// place the harness states what a working installation actually is. Everything
// else in this executable assumes that state and fails somewhere downstream of
// it: an unauthenticated provider is discovered when a run is claimed, an
// unrunnable check when a change has already been written, a stopped sink not at
// all. Here the whole of it is asked at once, before anything is spent.
//
// Every finding carries a remedy that is a command. That is what separates this
// from a status listing: an operator reading it is not being asked to work out
// what to do, and neither is an agent -- `--json` carries the same findings with
// the same remedies, structurally, which is what makes an automated repair
// possible without anything having to parse this prose.

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/doctor"
	"github.com/mason-bryant/yoyodyne/internal/execution"
)

func runDoctor(ctx context.Context, args []string, stdout, stderr io.Writer, version string) int {
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "", "configuration file path (default: the nearest project configuration)")
	quiet := flags.Bool("quiet", false, "report only what is wrong, leaving out what is already healthy")
	jsonOutput := flags.Bool("json", false, "emit machine-readable JSON")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "doctor does not accept positional arguments: it checks the whole installation")
		printDoctorUsage(stderr)
		return 2
	}

	report := doctor.Diagnose(ctx, doctor.Environment{
		Runner:      execution.OSProcessRunner{},
		LookPath:    exec.LookPath,
		Getenv:      os.Getenv,
		UserHomeDir: os.UserHomeDir,
		GOOS:        runtime.GOOS,
		Version:     version,
		Load:        func() (config.Resolved, error) { return loadConfiguration(*configPath) },
	})

	if *jsonOutput {
		if code := writeJSON(stdout, stderr, report); code != 0 {
			return code
		}
	} else {
		renderDiagnosis(stdout, report, *quiet)
	}
	// A broken installation exits nonzero whichever form it was reported in, so a
	// script does not have to read the findings to notice. A warning does not:
	// this is work that can run with something worth knowing about it, and an
	// exit code that could not tell the two apart would be one nobody could gate
	// on.
	if !report.Healthy() {
		return 1
	}
	return 0
}

// renderDiagnosis prints the findings in the order they were made, with each
// remedy on a line of its own directly under what it remedies. Keeping them
// adjacent is the whole point: a list of problems followed by a list of commands
// is a list an operator has to match up by hand.
//
// Severity deliberately does not reorder them. The order they were made is the
// order somebody would fix them in -- the tools, then the project, then what the
// project turns on -- and the first problem in that list is usually why the ones
// under it are problems too.
func renderDiagnosis(stdout io.Writer, report doctor.Report, quiet bool) {
	fmt.Fprintln(stdout, verdict(report))
	if report.Config != "" {
		fmt.Fprintf(stdout, "configuration: %s\n", report.Config)
	}
	fmt.Fprintln(stdout)
	for _, finding := range report.Findings {
		if quiet && finding.Status == doctor.StatusOK {
			continue
		}
		fmt.Fprintf(stdout, "%-8s %-22s %s\n", finding.Status, finding.Check, finding.Summary)
		if finding.Detail != "" {
			fmt.Fprintf(stdout, "%-8s %-22s %s\n", "", "", finding.Detail)
		}
		if finding.Remedy != "" {
			fmt.Fprintf(stdout, "%-8s %-22s fix: %s\n", "", "", finding.Remedy)
		}
	}
}

// verdict is the one line somebody reads first. A healthy installation says so
// plainly rather than leaving them to infer it from an absence of complaints.
func verdict(report doctor.Report) string {
	_, warnings, problems := report.Counts()
	product := report.Product
	if product == "" {
		product = "this installation"
	}
	switch {
	case problems > 0 && warnings > 0:
		return fmt.Sprintf("%s cannot run work: %s, and %s worth knowing about",
			product, countOf(problems, "problem"), countOf(warnings, "warning"))
	case problems > 0:
		return fmt.Sprintf("%s cannot run work: %s", product, countOf(problems, "problem"))
	case warnings > 0:
		return fmt.Sprintf("%s is ready to run work, with %s worth knowing about",
			product, countOf(warnings, "warning"))
	default:
		return fmt.Sprintf("%s is ready to run work; everything checked is healthy", product)
	}
}

func printDoctorUsage(writer io.Writer) {
	fmt.Fprintln(writer, strings.TrimSpace(`
Usage: yoyo doctor [options]

Check whether this installation can actually run work, and say what would fix it
where it cannot. It reads the build on PATH against the one running, Git, the
tracker, the configuration, the deterministic checks, each provider the agents
name -- installed always, and authenticated where the harness has an adapter that
can ask -- forge access where the project publishes, and, where reporting is on,
this project's own Slack secrets and the sink that is supposed to be using them.

Every finding that is not healthy carries a remedy, and a remedy is a command:
what it prints under a problem is what to run. `+"`--json`"+` carries the same findings
with the same remedies, so anything automating a repair reads them structurally
rather than parsing this output.

It changes nothing. Nothing here installs, authenticates, restarts, or edits a
configuration, and no credential is read: whether a secret is stored is asked in
the form that answers without producing the value.

It exits 1 when something would stop work running, and 0 otherwise. A warning is
not a small problem: it is something about an installation that works. Every
reporting finding is one, because reporting is an observation and never a gate --
a sink nobody started leaves every run exactly as it was. It is still named in
full, with the command that ends it.

Options:
  --config <path>   configuration file (default: the nearest .yoyodyne/config.yaml)
  --quiet           report only what is wrong, leaving out what is already healthy
  --json            emit machine-readable JSON`))
}
