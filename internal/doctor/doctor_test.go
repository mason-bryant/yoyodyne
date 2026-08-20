package doctor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/slack"
)

// TestEveryUnhealthyFindingCarriesARunnableRemedy is the promise the whole
// package exists to make, held against the matrix of states an installation is
// actually found in. A state that can be described and not routed out of is a
// defect here rather than a fact about the state, so this walks the states
// rather than sampling them: every one of them must produce a remedy, and a
// remedy must be a command rather than an instruction to go and read something.
func TestEveryUnhealthyFindingCarriesARunnableRemedy(t *testing.T) {
	t.Parallel()

	for name, broken := range brokenInstallations() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			world := newWorld(t)
			broken(world)
			report := world.diagnose()

			if report.Status == StatusOK {
				t.Fatalf("Diagnose() found nothing wrong with %q: %s", name, render(report))
			}
			for _, finding := range report.Findings {
				if finding.Status == StatusOK {
					continue
				}
				if strings.TrimSpace(finding.Remedy) == "" {
					t.Fatalf("finding %q is %s with no remedy: %s", finding.Check, finding.Status, finding.Summary)
				}
				if strings.TrimSpace(finding.Summary) == "" {
					t.Fatalf("finding %q has a remedy and says nothing it remedies", finding.Check)
				}
				// A remedy is a command. Prose telling somebody where to look is
				// exactly what this package exists instead of.
				if strings.HasSuffix(finding.Remedy, ".") || strings.Contains(finding.Remedy, " see ") {
					t.Fatalf("finding %q remedy is prose rather than a command: %q", finding.Check, finding.Remedy)
				}
			}
		})
	}
}

// brokenInstallations enumerates the states doctor has to be complete over.
// States produced by editing a working configuration are first-class here
// alongside states produced by never having installed something, because on a
// machine that has been running for months the second kind has stopped
// happening and the first kind has not.
func brokenInstallations() map[string]func(*world) {
	return map[string]func(*world){
		"yoyo is not on PATH": func(w *world) {
			w.absent("yoyo")
		},
		"the yoyo on PATH is a different build": func(w *world) {
			w.runner.reply("yoyo version", succeeded("v0.0.1"))
		},
		"the yoyo on PATH will not run": func(w *world) {
			w.runner.fail("yoyo version", errors.New("exec format error"))
		},
		"git is not installed": func(w *world) {
			w.absent("git")
		},
		"the project is not a repository": func(w *world) {
			w.runner.reply("rev-parse --git-dir", failed("not a git repository"))
		},
		"the repository has no commits": func(w *world) {
			w.runner.reply("rev-parse HEAD", failed("ambiguous argument 'HEAD'"))
		},
		"bd is not installed": func(w *world) {
			w.absent("bd")
		},
		"bd cannot read this project": func(w *world) {
			w.runner.reply("stats", failed("no beads database here"))
		},
		"there is no configuration": func(w *world) {
			w.configError = errors.New("no .yoyodyne/config.yaml was found above this directory")
		},
		"the configuration does not load": func(w *world) {
			w.configError = errors.New("checks must not be empty")
		},
		"no checks are configured": func(w *world) {
			w.configuration = strings.Replace(healthyConfig, "  - go test ./...\n", "", 1)
			w.configuration = strings.Replace(w.configuration, "checks:\n", "checks: []\n", 1)
		},
		"a configured check names a program this machine does not have": func(w *world) {
			w.configuration = strings.Replace(healthyConfig, "go test ./...", "cargo test --all", 1)
		},
		"the provider is not installed": func(w *world) {
			w.absent("claude")
		},
		"the provider is not authenticated": func(w *world) {
			w.runner.reply("auth status --json", succeeded(`{"loggedIn":false}`))
		},
		"the project publishes and gh is not installed": func(w *world) {
			w.configuration = publishingConfig
			w.absent("gh")
		},
		"the project publishes and gh is not authenticated": func(w *world) {
			w.configuration = publishingConfig
			w.runner.reply("gh auth status", failed("You are not logged into any GitHub hosts"))
		},
		"the project publishes and has no such remote": func(w *world) {
			w.configuration = publishingConfig
			w.runner.reply("remote get-url", failed("No such remote 'origin'"))
		},
		"reporting is on and this project's secrets are not stored": func(w *world) {
			w.configuration = reportingConfig
			w.runner.reply("find-generic-password", failed("The specified item could not be found in the keychain."))
			w.sinkRunning(slack.Presence{Version: currentVersion, SecretNamespace: "yoyodyne", Channel: "C1"})
		},
		"reporting is on, this is not macOS, and no environment file holds the secrets": func(w *world) {
			w.goos = "linux"
			w.configuration = reportingConfig
			w.sinkRunning(slack.Presence{Version: currentVersion, SecretNamespace: "yoyodyne", Channel: "C1"})
		},
		"reporting is on and no sink is running": func(w *world) {
			w.configuration = reportingConfig
		},
		"reporting is on and the sink that was running has died": func(w *world) {
			w.configuration = reportingConfig
			w.sinkRecorded(slack.Presence{Version: currentVersion, PID: 4242, SecretNamespace: "yoyodyne"})
		},
		"the running sink is an older build": func(w *world) {
			w.configuration = reportingConfig
			w.sinkRunning(slack.Presence{Version: "v0.0.1", PID: 4242, SecretNamespace: "yoyodyne", Channel: "C1"})
		},
		"the running sink holds another project's secrets": func(w *world) {
			w.configuration = reportingConfig
			w.sinkRunning(slack.Presence{Version: currentVersion, PID: 4242, SecretNamespace: "sibling", Channel: "C1"})
		},
		"the running sink was started from a shell": func(w *world) {
			w.configuration = reportingConfig
			w.sinkRunning(slack.Presence{Version: currentVersion, PID: 4242, Channel: "C1"})
		},
		"the running sink read another configuration": func(w *world) {
			w.configuration = reportingConfig
			w.sinkRunning(slack.Presence{
				Version:         currentVersion,
				PID:             4242,
				SecretNamespace: "yoyodyne",
				Channel:         "C1",
				Config:          "/somewhere/else/.yoyodyne/config.yaml",
			})
		},
		"the state root cannot be written": func(w *world) {
			w.stateRoot = "relative/not/absolute"
		},
	}
}

// TestAHealthyInstallationSaysSo is the other half of the promise. An operator
// who has fixed everything has to be told that in so many words, because an
// empty list of complaints and a diagnosis that never ran read the same.
func TestAHealthyInstallationSaysSo(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	report := world.diagnose()

	if report.Status != StatusOK || !report.Healthy() {
		t.Fatalf("Diagnose() = %s, want a healthy installation: %s", report.Status, render(report))
	}
	if report.Product != "yoyodyne" || report.Config == "" {
		t.Fatalf("Diagnose() product = %q, config = %q", report.Product, report.Config)
	}
	if _, warnings, problems := report.Counts(); warnings != 0 || problems != 0 {
		t.Fatalf("Diagnose() = %d warnings, %d problems: %s", warnings, problems, render(report))
	}
	for _, want := range []string{"path", "binary", "git", "repository", "tracker", "state", "checks", "provider:claude-code", "forge", "slack"} {
		if finding, found := findingFor(report, want); !found {
			t.Fatalf("Diagnose() never checked %q: %s", want, render(report))
		} else if finding.Status != StatusOK {
			t.Fatalf("check %q = %s on a healthy installation", want, finding.Status)
		}
	}
}

// TestAStaleSinkIsNamedAsStale covers the outage this check was written for. A
// sink that keeps running while the binary under it moves on posts what its own
// build knew how to post and silently drops everything added since, and the
// channel reads as a quiet week rather than as a broken installation.
func TestAStaleSinkIsNamedAsStale(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	world.configuration = reportingConfig
	world.sinkRunning(slack.Presence{Version: "v0.0.1", PID: 4242, SecretNamespace: "yoyodyne", Channel: "C1"})
	report := world.diagnose()

	finding, found := findingFor(report, "slack-sink-version")
	if !found {
		t.Fatalf("Diagnose() never compared the sink's build: %s", render(report))
	}
	if finding.Status != StatusProblem {
		t.Fatalf("slack-sink-version = %s, want a problem: %s", finding.Status, finding.Summary)
	}
	// Both builds have to be named. "Restart the sink" over an unnamed drift is
	// advice; the two versions side by side are the evidence.
	if !strings.Contains(finding.Detail, "v0.0.1") || !strings.Contains(finding.Detail, currentVersion) {
		t.Fatalf("slack-sink-version detail = %q, want both builds named", finding.Detail)
	}
	// The remedy stops the sink by the pid it recorded. A pattern that matched
	// its command line would match a sibling project's sink on the same machine.
	if !strings.Contains(finding.Remedy, "kill 4242") {
		t.Fatalf("slack-sink-version remedy = %q, want the recorded pid stopped", finding.Remedy)
	}
	if !strings.Contains(finding.Remedy, "yoyo slack") {
		t.Fatalf("slack-sink-version remedy = %q, want a sink started again", finding.Remedy)
	}
}

// TestSecretsAreCheckedForThisInstanceRatherThanForAnyToken is the
// multi-harness case. "A Slack token exists somewhere on this machine" passes
// for every project on it and is right for at most one, so what is asked for is
// this product's own pair under names that carry the product.
func TestSecretsAreCheckedForThisInstanceRatherThanForAnyToken(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	world.configuration = reportingConfig
	// The sibling's pair is in the keychain and this project's is not, which is
	// exactly the state a generic check calls healthy.
	world.runner.reply("find-generic-password", failed("The specified item could not be found in the keychain."))
	world.runner.reply("yoyo-slack-bot.sibling", succeeded("password has been found"))
	world.runner.reply("yoyo-slack-app.sibling", succeeded("password has been found"))
	report := world.diagnose()

	finding, found := findingFor(report, "slack-secrets")
	if !found || finding.Status != StatusProblem {
		t.Fatalf("slack-secrets = %#v, want this project's missing pair reported: %s", finding, render(report))
	}
	for _, want := range []string{"yoyo-slack-bot.yoyodyne", "yoyo-slack-app.yoyodyne"} {
		if !strings.Contains(finding.Remedy, want) {
			t.Fatalf("slack-secrets remedy = %q, want the namespaced name %q", finding.Remedy, want)
		}
	}
	// Storing a token by pasting it onto a command line puts it in a shell
	// history. The keychain prompts for it instead, which `-w` with no value is.
	if strings.Contains(finding.Remedy, "xoxb") || strings.Contains(finding.Remedy, "-w '") {
		t.Fatalf("slack-secrets remedy = %q, want the value prompted for rather than written", finding.Remedy)
	}
}

// TestAMachineWithNoKeychainIsStillRoutedOut keeps the completeness promise off
// macOS. A platform whose secret store this build cannot ask is not a state
// doctor gets to leave somebody in: the route out is the namespaced environment
// file, and the remedy has to name it.
func TestAMachineWithNoKeychainIsStillRoutedOut(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	world.goos = "linux"
	world.configuration = reportingConfig
	report := world.diagnose()

	secrets, found := findingFor(report, "slack-secrets")
	if !found || secrets.Status != StatusProblem {
		t.Fatalf("slack-secrets = %#v, want a problem: %s", secrets, render(report))
	}
	wanted := filepath.Join("yoyo", "yoyodyne", "slack.env")
	if !strings.Contains(secrets.Remedy, wanted) {
		t.Fatalf("slack-secrets remedy = %q, want the namespaced environment file %q", secrets.Remedy, wanted)
	}
	sink, found := findingFor(report, "slack-sink")
	if !found || !strings.Contains(sink.Remedy, wanted) {
		t.Fatalf("slack-sink remedy = %q, want the sink started from that file", sink.Remedy)
	}
	// The keychain is macOS's, so a machine without one must not be told to use
	// it -- a remedy that cannot run is worse than the problem it answers.
	if strings.Contains(secrets.Remedy, "security ") || strings.Contains(sink.Remedy, "security ") {
		t.Fatalf("a machine with no keychain was given a keychain remedy: %q / %q", secrets.Remedy, sink.Remedy)
	}
}

// TestASinkOnAnotherProjectsSecretsIsNamed is the failure the namespaced
// contract exists to make visible: a sink that connected, authenticated, and is
// posting this product's work through a sibling project's Slack app.
func TestASinkOnAnotherProjectsSecretsIsNamed(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	world.configuration = reportingConfig
	world.sinkRunning(slack.Presence{
		Version:         currentVersion,
		PID:             4242,
		SecretNamespace: "sibling",
		Channel:         "C1",
		Team:            "Sibling Workspace",
	})
	report := world.diagnose()

	finding, found := findingFor(report, "slack-sink-secrets")
	if !found || finding.Status != StatusProblem {
		t.Fatalf("slack-sink-secrets = %#v, want a problem: %s", finding, render(report))
	}
	if !strings.Contains(finding.Summary, "sibling") || !strings.Contains(finding.Summary, "yoyodyne") {
		t.Fatalf("slack-sink-secrets summary = %q, want both products named", finding.Summary)
	}
	// The workspace the tokens actually authenticated into is the one fact here
	// that came from the tokens rather than from what somebody claimed.
	if !strings.Contains(finding.Detail, "Sibling Workspace") {
		t.Fatalf("slack-sink-secrets detail = %q, want the workspace named", finding.Detail)
	}
	if !strings.Contains(finding.Remedy, "YOYO_SLACK_SECRET_NAMESPACE=yoyodyne") {
		t.Fatalf("slack-sink-secrets remedy = %q, want this project's secrets used", finding.Remedy)
	}
}

// TestNothingReadsATokenholds the credential boundary. This is a diagnostic
// whose output reaches a terminal, a scrollback, and whatever collects them, so
// nothing it runs may ask a store to produce a secret.
func TestNothingReadsAToken(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	world.configuration = reportingConfig
	world.sinkRunning(slack.Presence{Version: currentVersion, PID: 4242, SecretNamespace: "yoyodyne", Channel: "C1"})
	world.diagnose()

	for _, command := range world.runner.commands {
		joined := strings.Join(command, " ")
		if !strings.Contains(joined, "find-generic-password") {
			continue
		}
		if contains(command, "-w") {
			t.Fatalf("the diagnosis asked the keychain for a password: %q", joined)
		}
	}
}

// TestADeadSinkIsNotMistakenForALiveOne separates the two questions the record
// and the lease answer. The record survives a process that was killed, so a
// sink's own file saying it is there is never taken as evidence that it is.
func TestADeadSinkIsNotMistakenForALiveOne(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	world.configuration = reportingConfig
	world.sinkRecorded(slack.Presence{Version: currentVersion, PID: 4242, Channel: "C1", StartedAt: time.Now().Add(-3 * time.Hour)})
	report := world.diagnose()

	finding, found := findingFor(report, "slack-sink")
	if !found || finding.Status != StatusProblem {
		t.Fatalf("slack-sink = %#v, want a stopped sink reported: %s", finding, render(report))
	}
	if !strings.Contains(finding.Detail, "4242") {
		t.Fatalf("slack-sink detail = %q, want the sink that died described", finding.Detail)
	}
}

// TestReportingOffIsHealthyRatherThanMissing keeps an opt-in observation from
// reading as a broken component. A project that reports nowhere is a project
// that decided to, and doctor reporting that as a defect would make the check
// one operators learn to ignore.
func TestReportingOffIsHealthyRatherThanMissing(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	report := world.diagnose()

	if finding, found := findingFor(report, "slack"); !found || finding.Status != StatusOK {
		t.Fatalf("slack = %#v, want reporting-off to be healthy", finding)
	}
	for _, absent := range []string{"slack-secrets", "slack-sink", "slack-sink-version", "slack-sink-secrets"} {
		if _, found := findingFor(report, absent); found {
			t.Fatalf("Diagnose() checked %q for a project that reports nowhere", absent)
		}
	}
}

// TestAForgeIsOnlyAskedAboutWhenTheProjectPublishes holds the same line for the
// other opt-in. Everything stays on the machine until a project says otherwise,
// and a missing `gh` is then not a defect.
func TestAForgeIsOnlyAskedAboutWhenTheProjectPublishes(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	world.absent("gh")
	report := world.diagnose()

	if finding, found := findingFor(report, "forge"); !found || finding.Status != StatusOK {
		t.Fatalf("forge = %#v, want a non-publishing project to need no forge", finding)
	}
	if !report.Healthy() {
		t.Fatalf("Diagnose() = %s, want healthy: %s", report.Status, render(report))
	}
}

// TestAMissingConfigurationStillReportsTheToolsAroundIt keeps the first failure
// from swallowing the rest. Somebody in the wrong directory, or in a project
// that has never been initialized, is usually about to hit the tracker next.
func TestAMissingConfigurationStillReportsTheToolsAroundIt(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	world.configError = errors.New("no .yoyodyne/config.yaml was found above this directory")
	report := world.diagnose()

	finding, found := findingFor(report, "configuration")
	if !found || finding.Status != StatusProblem || finding.Remedy != "yoyo init" {
		t.Fatalf("configuration = %#v, want `yoyo init`", finding)
	}
	for _, want := range []string{"path", "git", "tracker"} {
		if _, found := findingFor(report, want); !found {
			t.Fatalf("Diagnose() stopped before %q: %s", want, render(report))
		}
	}
}

// TestAnUnresolvableCheckIsFoundBeforeARunSpendsAnything is the case that costs
// most to discover late: a check naming a program this machine does not have
// fails every run at the same point, after the provider has already been paid.
func TestAnUnresolvableCheckIsFoundBeforeARunSpendsAnything(t *testing.T) {
	t.Parallel()

	world := newWorld(t)
	world.configuration = strings.Replace(healthyConfig, "go test ./...", "cargo test --all", 1)
	report := world.diagnose()

	finding, found := findingFor(report, "checks")
	if !found || finding.Status != StatusProblem {
		t.Fatalf("checks = %#v, want the missing program reported", finding)
	}
	if !strings.Contains(finding.Summary, "cargo") {
		t.Fatalf("checks summary = %q, want the program named", finding.Summary)
	}
}

// TestAShellPrefixOnACheckIsNotMistakenForAMissingProgram keeps the
// resolvability probe from inventing failures. A check line is a shell command,
// and the first word of one is not always a program.
func TestAShellPrefixOnACheckIsNotMistakenForAMissingProgram(t *testing.T) {
	t.Parallel()

	for _, command := range []string{"CGO_ENABLED=0 go test ./...", "cd internal && go build ./...", "  go vet ./..."} {
		world := newWorld(t)
		world.configuration = strings.Replace(healthyConfig, "go test ./...", command, 1)
		report := world.diagnose()
		if finding, _ := findingFor(report, "checks"); finding.Status != StatusOK {
			t.Fatalf("checks for %q = %s: %s", command, finding.Status, finding.Summary)
		}
	}
}

// world is one installation, assembled broken or healthy. Everything a
// diagnosis reads is arranged here, because the states worth being complete
// over are exactly the ones nobody can arrange on a real machine on demand.
type world struct {
	t             *testing.T
	runner        *scriptedRunner
	missing       map[string]bool
	configuration string
	configError   error
	stateRoot     string
	project       string
	goos          string
	leases        []*leaseHold
}

const currentVersion = "v1.2.3"

func newWorld(t *testing.T) *world {
	t.Helper()
	missing := map[string]bool{}
	w := &world{
		t:             t,
		runner:        &scriptedRunner{missing: missing},
		missing:       missing,
		configuration: healthyConfig,
		stateRoot:     t.TempDir(),
		project:       t.TempDir(),
		goos:          "darwin",
	}
	// A machine where everything answers. Each broken installation is this with
	// exactly one thing changed, so what a test arranges is what it is about.
	w.runner.reply("yoyo version", succeeded(currentVersion))
	w.runner.reply("git --version", succeeded("git version 2.48.1"))
	w.runner.reply("rev-parse --git-dir", succeeded(".git"))
	w.runner.reply("rev-parse HEAD", succeeded("0123456789abcdef0123456789abcdef01234567"))
	w.runner.reply("stats", succeeded("open: 3"))
	w.runner.reply("claude --version", succeeded("2.0.0 (Claude Code)"))
	w.runner.reply("auth status --json", succeeded(`{"loggedIn":true,"authMethod":"subscription"}`))
	w.runner.reply("gh --version", succeeded("gh version 2.60.0"))
	w.runner.reply("gh auth status", succeeded("Logged in to github.com"))
	w.runner.reply("remote get-url", succeeded("git@github.com:example/thing.git"))
	w.runner.reply("find-generic-password", succeeded("keychain item"))
	t.Cleanup(func() {
		for _, held := range w.leases {
			held.release()
		}
	})
	return w
}

func (w *world) absent(program string) { w.missing[program] = true }

// sinkRecorded leaves the record a sink writes, without a process behind it.
func (w *world) sinkRecorded(presence slack.Presence) {
	w.t.Helper()
	store, err := slack.NewStore(w.stateRoot, "yoyodyne")
	if err != nil {
		w.t.Fatalf("NewStore() error = %v", err)
	}
	if presence.StartedAt.IsZero() {
		presence.StartedAt = time.Now().Add(-time.Hour)
	}
	if err := store.SavePresence(presence); err != nil {
		w.t.Fatalf("SavePresence() error = %v", err)
	}
}

// sinkRunning leaves that record and holds the lease, which together are what a
// live sink looks like from outside.
func (w *world) sinkRunning(presence slack.Presence) {
	w.t.Helper()
	w.sinkRecorded(presence)
	store, err := slack.NewStore(w.stateRoot, "yoyodyne")
	if err != nil {
		w.t.Fatalf("NewStore() error = %v", err)
	}
	lease, held, err := store.Lease()
	if err != nil || !held {
		w.t.Fatalf("Lease() = %t, %v, want the lease a running sink holds", held, err)
	}
	w.leases = append(w.leases, &leaseHold{release: func() { _ = lease.Release() }})
}

type leaseHold struct{ release func() }

func (w *world) diagnose() Report {
	w.t.Helper()
	return Diagnose(context.Background(), Environment{
		Runner:      w.runner,
		LookPath:    w.lookPath,
		Getenv:      w.getenv,
		UserHomeDir: func() (string, error) { return w.project, nil },
		GOOS:        w.goos,
		Version:     currentVersion,
		Load:        w.load,
		Now:         time.Now,
	})
}

// lookPath resolves only what this machine has. The set is enumerated rather
// than inverted so that a configuration naming a program nobody installed --
// the ordinary way a checks list goes wrong -- is a state a test can arrange by
// writing the configuration alone.
func (w *world) lookPath(program string) (string, error) {
	installed := map[string]bool{"yoyo": true, "git": true, "bd": true, "claude": true, "gh": true, "go": true, "security": true}
	if !installed[program] || w.missing[program] {
		return "", errors.New("exec: \"" + program + "\": executable file not found in $PATH")
	}
	return filepath.Join("/usr/local/bin", program), nil
}

func (w *world) getenv(name string) string {
	switch name {
	case "YOYODYNE_STATE_HOME":
		return w.stateRoot
	case "XDG_CONFIG_HOME":
		// Somewhere with nothing in it, so the keychain is the store under test
		// rather than a file left behind by another case.
		return filepath.Join(w.project, "config")
	default:
		return ""
	}
}

func (w *world) load() (config.Resolved, error) {
	if w.configError != nil {
		return config.Resolved{}, w.configError
	}
	directory := filepath.Join(w.project, config.DirectoryName)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return config.Resolved{}, err
	}
	path := filepath.Join(directory, "config.yaml")
	if err := os.WriteFile(path, []byte(w.configuration), 0o644); err != nil {
		return config.Resolved{}, err
	}
	return config.LoadResolved(path)
}

func findingFor(report Report, check string) (Finding, bool) {
	for _, finding := range report.Findings {
		if finding.Check == check {
			return finding, true
		}
	}
	return Finding{}, false
}

// render is what a failing test prints, so a failure says what the whole
// diagnosis found rather than only the field that was compared.
func render(report Report) string {
	var built strings.Builder
	built.WriteString("\n")
	for _, finding := range report.Findings {
		built.WriteString(string(finding.Status) + "\t" + finding.Check + "\t" + finding.Summary + "\n")
		if finding.Remedy != "" {
			built.WriteString("\t\tfix: " + finding.Remedy + "\n")
		}
	}
	return built.String()
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

// scriptedRunner answers the tools a diagnosis asks about, keyed on any
// substring of the command it would have run.
type scriptedRunner struct {
	commands [][]string
	replies  []scriptedReply
	failures map[string]error
	// missing is shared with the world, so a program this machine does not have
	// refuses to start here exactly as it would refuse to start for real: the
	// adapters that ask a tool about itself distinguish "would not run" from
	// "ran and refused", and a test that could not produce the first would never
	// reach the remedy for it.
	missing map[string]bool
}

type scriptedReply struct {
	match  string
	result execution.ProcessResult
}

// reply registers an answer. Later registrations are matched first, so a case
// can override one of the healthy defaults by naming the same command.
func (r *scriptedRunner) reply(match string, result execution.ProcessResult) {
	r.replies = append([]scriptedReply{{match: match, result: result}}, r.replies...)
}

func (r *scriptedRunner) fail(match string, err error) {
	if r.failures == nil {
		r.failures = map[string]error{}
	}
	r.failures[match] = err
}

func (r *scriptedRunner) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	invocation := append([]string{command.Name}, command.Args...)
	r.commands = append(r.commands, invocation)
	joined := strings.Join(invocation, " ")
	if r.missing[filepath.Base(command.Name)] {
		return execution.ProcessResult{}, exec.ErrNotFound
	}
	for match, err := range r.failures {
		if strings.Contains(joined, match) {
			return execution.ProcessResult{}, err
		}
	}
	for _, reply := range r.replies {
		if strings.Contains(joined, reply.match) {
			return reply.result, nil
		}
	}
	return execution.ProcessResult{Status: execution.ProcessSucceeded}, nil
}

func succeeded(stdout string) execution.ProcessResult {
	return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: stdout}
}

func failed(stderr string) execution.ProcessResult {
	return execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: stderr}
}

const healthyConfig = `version: 1
product:
  id: yoyodyne
  repository: .
approvals:
  brief: human
  goals: human
  designs: automatic
  integration: human
  publishing: human
checks:
  - go test ./...
agents:
  developer:
    role: developer
    backend: claude-code
    model: opus
`

const publishingConfig = `version: 1
product:
  id: yoyodyne
  repository: .
approvals:
  brief: human
  goals: human
  designs: automatic
  integration: human
  publishing: automatic
checks:
  - go test ./...
agents:
  developer:
    role: developer
    backend: claude-code
    model: opus
`

const reportingConfig = `version: 1
product:
  id: yoyodyne
  repository: .
approvals:
  brief: human
  goals: human
  designs: automatic
  integration: human
  publishing: human
checks:
  - go test ./...
slack:
  enabled: true
  channel: C1
agents:
  developer:
    role: developer
    backend: claude-code
    model: opus
`
