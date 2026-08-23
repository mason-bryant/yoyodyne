package cli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/domain"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/slack"
)

// The whole of what setup promises a newcomer: from a directory with nothing in
// it but their own code to an installation `yoyo doctor` calls healthy, having
// answered questions rather than read documents.
func TestSetupWalksABlankProjectToAnInstallationThatCanRunWork(t *testing.T) {
	t.Parallel()

	world := newSetupWorld(t)
	world.trackerReady = false
	world.trackerRemote = ""
	// The tracker, the configuration, and where the tracker syncs: all three
	// proposed yes.
	world.answers = "\n\n\n"

	report := world.walk()

	if step := world.step(report, stepTracker); step.Status != setupDone {
		t.Fatalf("tracker step = %s (%s), want it initialized", step.Status, step.Summary)
	}
	if step := world.step(report, stepConfiguration); step.Status != setupDone {
		t.Fatalf("configuration step = %s (%s), want it written", step.Status, step.Summary)
	}
	if step := world.step(report, stepTrackerRemote); step.Status != setupDone {
		t.Fatalf("tracker-remote step = %s (%s), want it pointed at the project's Git remote", step.Status, step.Summary)
	}
	if world.trackerRemote != "file:///origin.git" {
		t.Errorf("the tracker syncs through %q, want this project's own Git remote", world.trackerRemote)
	}
	if !world.runner.ran("bd init") {
		t.Fatal("setup never initialized the tracker")
	}
	if _, err := config.LoadResolved(filepath.Join(world.project, config.DirectoryName, config.FileName)); err != nil {
		t.Fatalf("the configuration setup wrote does not load: %v", err)
	}
	// The end of the walk is a diagnosis rather than setup's own opinion of
	// itself, and on a project whose checks it read from a Makefile that
	// diagnosis has nothing left to complain about.
	if !report.Diagnosis.Healthy() {
		t.Fatalf("doctor did not pass at the end of setup:%s", renderSetupReport(report))
	}
	if report.Product != "calc" {
		t.Errorf("product = %q, want the project directory name", report.Product)
	}
}

// Running it again is the same command, and the second time it does nothing.
// This is the resumability bar too: setup keeps no record of its own, so a walk
// interrupted anywhere is resumed by running it again, and every step that was
// already true is reported as already true rather than repeated.
func TestSetupRunAgainReportsWhatIsAlreadyTrueAndChangesNothing(t *testing.T) {
	t.Parallel()

	world := newSetupWorld(t)
	world.trackerReady = false
	world.answers = "\n\n"
	world.walk()

	path := filepath.Join(world.project, config.DirectoryName, config.FileName)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	// The second walk is given no answers at all: if it asks anything, it is
	// asking about something it should have found already done.
	world.answers = ""
	world.runner.commands = nil
	report := world.walk()

	for _, name := range []string{stepTracker, stepConfiguration, stepTrackerRemote} {
		if step := world.step(report, name); step.Status != setupAlready {
			t.Errorf("%s step = %s (%s), want it left alone", name, step.Status, step.Summary)
		}
	}
	if world.runner.ran("bd init") {
		t.Error("setup initialized the tracker a second time")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Error("setup rewrote a configuration that was already there")
	}
	if !report.Diagnosis.Healthy() {
		t.Fatalf("the second walk left the installation broken:%s", renderSetupReport(report))
	}
}

// The ordinary way a second machine arrives at a configured project:
// `.yoyodyne/config.yaml` is a committed file, so it is cloned rather than
// written, and nothing about that clone carries the tracker's own database or
// the remote it syncs through. A walk that only pointed the tracker somewhere
// while writing a configuration would never reach this case at all, which is
// what makes it a step of its own rather than something the write does on its
// way past.
func TestSetupPointsTheTrackerAtTheGitRemoteOnAProjectThatIsAlreadyConfigured(t *testing.T) {
	t.Parallel()

	world := newSetupWorld(t)
	world.trackerReady = false
	world.answers = "\n\n"
	world.walk()

	// The clone: the configuration is there and committed, and this machine's
	// tracker has neither a database nor a remote.
	world.trackerReady = false
	world.trackerRemote = ""
	world.runner.commands = nil
	world.answers = "\n\n" // the tracker, and then where it syncs

	report := world.walk()

	if step := world.step(report, stepConfiguration); step.Status != setupAlready {
		t.Fatalf("configuration step = %s (%s), want the cloned one left alone", step.Status, step.Summary)
	}
	if step := world.step(report, stepTrackerRemote); step.Status != setupDone {
		t.Fatalf("tracker-remote step = %s (%s), want it converged on an already-configured project", step.Status, step.Summary)
	}
	if world.trackerRemote != "file:///origin.git" {
		t.Fatalf("the tracker syncs through %q, so this machine's backlog is its own", world.trackerRemote)
	}

	// And converged means converged: a third walk finds it done rather than
	// doing it again.
	world.answers = ""
	world.runner.commands = nil
	again := world.walk()
	if step := world.step(again, stepTrackerRemote); step.Status != setupAlready {
		t.Fatalf("tracker-remote step = %s (%s) on a third walk, want it left alone", step.Status, step.Summary)
	}
	if world.runner.ran("dolt remote add") {
		t.Error("setup configured a sync remote the tracker already held")
	}
}

// Asking what a machine needs is not consent to do it. This is the state that
// distinguishes a report from an act -- a configured project whose tracker
// answers and whose sync remote is not set, which is the one thing left that
// setup could carry out with no question answered -- so it is arranged
// deliberately rather than left to a blank directory where nothing is reachable
// anyway.
func TestSetupChangesNothingWhenItIsOnlyBeingAskedForAReport(t *testing.T) {
	t.Parallel()

	world := newSetupWorld(t)
	world.trackerRemote = ""
	world.answers = "\nn\n" // configure the project, and decline the sync remote for now
	world.walk()
	if world.trackerRemote != "" {
		t.Fatalf("a declined sync remote was configured anyway: %q", world.trackerRemote)
	}

	// What `--json` without `--yes` leaves the walk as: nothing may be asked, so
	// nothing may be done.
	world.closed = true
	world.answers = ""
	world.runner.commands = nil
	report := world.walk()

	step := world.step(report, stepTrackerRemote)
	if step.Status != setupSkipped {
		t.Fatalf("tracker-remote step = %s (%s), want it left for a walk somebody answers", step.Status, step.Summary)
	}
	if step.Remedy == "" {
		t.Error("the step nobody was asked about says nothing about what would do it")
	}
	if world.runner.ran("dolt remote add") || world.trackerRemote != "" {
		t.Fatal("reading a report configured the tracker's sync remote")
	}
	// And the machine is otherwise exactly as it was: nothing here writes.
	for _, forbidden := range []string{"bd init", "add-generic-password"} {
		if world.runner.ran(forbidden) {
			t.Errorf("reading a report ran %q", forbidden)
		}
	}
}

// The questioner is where consent is decided, so the two states that decide it
// are pinned here rather than inferred from the walks above: --yes takes the
// answer setup proposes, and a walk nobody can answer declines everything --
// including the questions whose proposed answer is yes.
func TestQuestionerTakesDefaultsOnlyWhenItWasToldTo(t *testing.T) {
	t.Parallel()

	assuming := &questioner{out: io.Discard, defaults: true}
	if !assuming.confirm("proposed yes", true) || assuming.confirm("proposed no", false) {
		t.Error("--yes did not answer with what setup proposed")
	}

	unanswerable := &questioner{out: io.Discard, closed: true}
	if unanswerable.confirm("proposed yes", true) || unanswerable.confirm("proposed no", false) {
		t.Error("a question nobody could answer was taken as a yes")
	}
	if unanswerable.line("which channel?", "C1") != "" {
		t.Error("a line nobody typed was taken from the command line instead")
	}
}

// A configuration that does not load is somebody's work in progress. Setup will
// not regenerate it, because regenerating it is deleting their edits to fix a
// typo in them -- and the state matters more than it sounds, since it is exactly
// what an operator has in front of them when they reach for a setup command.
func TestSetupRefusesToOverwriteAConfigurationThatDoesNotLoad(t *testing.T) {
	t.Parallel()

	world := newSetupWorld(t)
	directory := filepath.Join(world.project, config.DirectoryName)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	broken := "version: 1\nproduct:\n  id: calc\n  repository: .\nchecks: [oops\n"
	path := filepath.Join(directory, config.FileName)
	if err := os.WriteFile(path, []byte(broken), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	world.answers = "y\ny\ny\ny\n" // every question answered yes; none of them may overwrite this
	report := world.walk()

	step := world.step(report, stepConfiguration)
	if step.Status != setupHandedOff {
		t.Fatalf("configuration step = %s (%s), want it handed back untouched", step.Status, step.Summary)
	}
	if step.Remedy == "" {
		t.Error("setup handed the configuration back without saying what to run")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(content) != broken {
		t.Fatalf("setup rewrote a configuration it could not read:\n%s", content)
	}
}

// The optional tier. Turning reporting on is three lines in a file the operator
// owns and has been editing, so it is edited rather than regenerated: everything
// they wrote, and every comment explaining what a value is for, survives it.
func TestSetupTurnsReportingOnByEditingTheConfigurationRatherThanRewritingIt(t *testing.T) {
	t.Parallel()

	world := newSetupWorld(t)
	world.answers = "\n\n" // the tracker is ready, so these settle the configuration
	world.walk()

	path := filepath.Join(world.project, config.DirectoryName, config.FileName)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if err := os.WriteFile(path, append(before, []byte("\n# an operator's own note\n")...), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	// Reporting, the app already made, the channel, and then the two tokens.
	world.answers = "y\ny\nC0123456789\ny\n"
	report := world.walk()

	if step := world.step(report, stepSlack); step.Status != setupDone {
		t.Fatalf("slack step = %s (%s), want reporting turned on", step.Status, step.Summary)
	}
	resolved, err := config.LoadResolved(path)
	if err != nil {
		t.Fatalf("LoadResolved() error = %v", err)
	}
	if !resolved.Config.Slack.Enabled || resolved.Config.Slack.Channel != "C0123456789" {
		t.Fatalf("slack = %+v, want reporting into the channel that was named", resolved.Config.Slack)
	}
	edited, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	for _, kept := range []string{"# Yoyodyne project configuration.", "# an operator's own note"} {
		if !strings.Contains(string(edited), kept) {
			t.Errorf("editing the configuration lost %q:\n%s", kept, edited)
		}
	}
	if !report.Diagnosis.Healthy() {
		t.Fatalf("turning reporting on left the installation unable to run work:%s", renderSetupReport(report))
	}
}

// `yoyo init` writes the artifact-home indexes, so the installation that needs
// them written is the one that was configured before they existed -- which is
// every project already running. Setup is the repair half of what doctor reports.
func TestSetupWritesTheIndexesAProjectConfiguredEarlierNeverGot(t *testing.T) {
	t.Parallel()

	world := newSetupWorld(t)
	world.answers = "\n\n"
	world.walk()

	// The state a project configured before this existed is actually in.
	designs := filepath.Join(world.project, "docs", "designs", "README.md")
	if err := os.RemoveAll(filepath.Join(world.project, "docs")); err != nil {
		t.Fatalf("RemoveAll() error = %v", err)
	}

	world.answers = "y\nn\n" // write the indexes, and leave reporting off
	report := world.walk()

	step := world.step(report, stepArtifactReadmes)
	if step.Status != setupDone {
		t.Fatalf("artifact-readmes step = %s (%s), want the indexes written", step.Status, step.Summary)
	}
	content, err := os.ReadFile(designs)
	if err != nil {
		t.Fatalf("setup wrote no index for the designs home: %v", err)
	}
	for _, answer := range []string{"**Purpose.**", "**Owner.**", "**Editing by hand.**"} {
		if !strings.Contains(string(content), answer) {
			t.Errorf("the index setup wrote does not state %s:\n%s", answer, content)
		}
	}
	if !report.Diagnosis.Healthy() {
		t.Fatalf("the walk left the installation unable to run work:%s", renderSetupReport(report))
	}

	// And converged means converged: the next walk finds them and asks nothing.
	world.answers = "n\n"
	again := world.walk()
	if step := world.step(again, stepArtifactReadmes); step.Status != setupAlready {
		t.Fatalf("artifact-readmes step = %s (%s) on a second walk, want it left alone", step.Status, step.Summary)
	}
}

// An index somebody rewrote is their prose. Setup asks about replacing it
// separately from writing one that is not there, the question defaults to no,
// and a no leaves the file byte for byte -- setup does not overwrite what is
// already there, and an index somebody customized is exactly that rule's case.
func TestSetupDoesNotReplaceAnIndexSomebodyWroteWithoutBeingTold(t *testing.T) {
	t.Parallel()

	world := newSetupWorld(t)
	world.answers = "\n\n"
	world.walk()

	decisions := filepath.Join(world.project, "docs", "decisions", "README.md")
	theirs := "# docs/decisions\n\nOurs, and it says nothing the template says.\n"
	if err := os.WriteFile(decisions, []byte(theirs), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	world.answers = "\nn\n" // take the proposed answer on the index, then decline reporting
	report := world.walk()

	step := world.step(report, stepArtifactReadmes)
	if step.Status != setupSkipped {
		t.Fatalf("artifact-readmes step = %s (%s), want the operator's own file left alone", step.Status, step.Summary)
	}
	if step.Remedy == "" {
		t.Error("the index that was left says nothing about what would finish it")
	}
	content, err := os.ReadFile(decisions)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(content) != theirs {
		t.Errorf("setup replaced an index somebody wrote:\n%s", content)
	}
}

// The tokens are namespaced by product and the keychain does the asking. Both
// halves are load-bearing: one person running several harnesses is the ordinary
// case, and a token that reached this program would be a token in an argument
// list, a scrollback, and whatever collects them.
func TestSetupStoresTheTokensUnderThisProductsNamesWithoutEverHoldingOne(t *testing.T) {
	t.Parallel()

	world := newSetupWorld(t)
	world.answers = "\ny\ny\nC0123456789\n\n"
	report := world.walk()

	if step := world.step(report, stepSlackSecrets); step.Status != setupDone {
		t.Fatalf("slack-secrets step = %s (%s), want the pair stored", step.Status, step.Summary)
	}
	productID := domain.ProductID("calc")
	for _, name := range []string{slack.BotSecret(productID), slack.AppSecret(productID)} {
		if !world.runner.keychain[name] {
			t.Errorf("the keychain has no %s", name)
		}
	}
	for _, invocation := range world.runner.commands {
		if len(invocation) == 0 || invocation[0] != "security" || invocation[1] != "add-generic-password" {
			continue
		}
		// `-w` with no value is what makes the keychain prompt for the token
		// itself. Anything after it would be the token, in an argument list.
		if invocation[len(invocation)-1] != "-w" {
			t.Fatalf("setup passed something after -w, so a token was in an argument list: %v", invocation)
		}
	}
}

// A pair already in the keychain belongs to whoever put it there. Overwriting
// one is how the second harness on a machine breaks the first, which is the
// whole reason the names carry the product.
func TestSetupLeavesAStoredTokenPairExactlyAsItIs(t *testing.T) {
	t.Parallel()

	world := newSetupWorld(t)
	productID := domain.ProductID("calc")
	world.runner.keychain[slack.BotSecret(productID)] = true
	world.runner.keychain[slack.AppSecret(productID)] = true

	world.answers = "\ny\ny\nC0123456789\n"
	report := world.walk()

	step := world.step(report, stepSlackSecrets)
	if step.Status != setupAlready {
		t.Fatalf("slack-secrets step = %s (%s), want the stored pair left alone", step.Status, step.Summary)
	}
	if world.runner.ran("add-generic-password") {
		t.Fatal("setup wrote over a token pair that was already stored")
	}
}

// A walk with nobody at the terminal is the other way this is used -- a second
// machine, a script, a colleague's checkout -- and naming the channel is how the
// optional tier is answered without being asked.
func TestSetupWalksTheOptionalTierWithoutAskingWhenTheChannelIsNamed(t *testing.T) {
	t.Parallel()

	world := newSetupWorld(t)
	world.slackChannel = "C0123456789"
	world.defaults = true
	world.trackerReady = false

	report := world.walk()

	for _, name := range []string{stepTracker, stepConfiguration, stepSlack, stepSlackSecrets} {
		if step := world.step(report, name); step.Status != setupDone {
			t.Fatalf("%s step = %s (%s), want it carried out", name, step.Status, step.Summary)
		}
	}
	resolved, err := config.LoadResolved(filepath.Join(world.project, config.DirectoryName, config.FileName))
	if err != nil {
		t.Fatalf("LoadResolved() error = %v", err)
	}
	if resolved.Config.Slack.Channel != "C0123456789" {
		t.Fatalf("channel = %q, want the one named on the command line", resolved.Config.Slack.Channel)
	}
	if !report.Diagnosis.Healthy() {
		t.Fatalf("an unattended walk left the installation unable to run work:%s", renderSetupReport(report))
	}
}

// Every state setup can leave a step in without finishing it has to name what
// somebody would run. A step that says what is undone and not what to do about
// it is the failure this whole path exists to end, and it is the same promise
// doctor makes about a finding.
func TestEverySetupStepThatLeavesWorkCarriesACommand(t *testing.T) {
	t.Parallel()

	for name, arrange := range unfinishedSetups() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			world := newSetupWorld(t)
			arrange(world)
			report := world.walk()

			var unfinished int
			for _, step := range report.Steps {
				if step.Status == setupAlready || step.Status == setupDone {
					continue
				}
				unfinished++
				if strings.TrimSpace(step.Remedy) == "" {
					t.Fatalf("step %q is %s with nothing to run: %s", step.Step, step.Status, step.Summary)
				}
				if strings.HasSuffix(step.Remedy, ".") {
					t.Fatalf("step %q offers prose rather than a command: %q", step.Step, step.Remedy)
				}
			}
			if unfinished == 0 {
				t.Fatalf("%q finished every step, so this case is checking nothing:%s", name, renderSetupReport(report))
			}
		})
	}
}

// The states a walk stops short in. Two of them are the operator declining,
// which is not a failure and must not read as one; the rest are the machine.
func unfinishedSetups() map[string]func(*setupWorld) {
	return map[string]func(*setupWorld){
		"the tracker is not installed": func(w *setupWorld) {
			w.trackerReady = false
			w.missing["bd"] = true
			w.answers = "\n\n"
		},
		"bd refuses to initialize the project": func(w *setupWorld) {
			w.trackerReady = false
			w.runner.refuse("bd init")
			w.answers = "\n\n"
		},
		"the operator declines the tracker": func(w *setupWorld) {
			w.trackerReady = false
			w.answers = "n\nn\n"
		},
		"the operator declines the configuration": func(w *setupWorld) {
			w.answers = "n\n"
		},
		"the project has no Git remote for the tracker to sync through": func(w *setupWorld) {
			w.trackerRemote = ""
			w.gitRemote = ""
			w.answers = "\n"
		},
		"the operator declines the tracker's sync remote": func(w *setupWorld) {
			w.trackerRemote = ""
			w.answers = "\nn\n"
		},
		"bd will not say where the tracker syncs": func(w *setupWorld) {
			w.trackerRemote = ""
			w.runner.refuse("dolt remote list")
			w.answers = "\n"
		},
		"reporting is wanted and the Slack app does not exist yet": func(w *setupWorld) {
			w.answers = "\ny\nn\n"
		},
		"reporting is wanted and no channel is named": func(w *setupWorld) {
			w.answers = "\ny\ny\n\n"
		},
		"the keychain refuses the tokens": func(w *setupWorld) {
			w.runner.refuse("add-generic-password")
			w.answers = "\ny\ny\nC0123456789\n\n"
		},
		"this platform has no keychain to put the tokens in": func(w *setupWorld) {
			w.goos = "linux"
			w.answers = "\ny\ny\nC0123456789\n"
		},
		"nothing can be asked": func(w *setupWorld) {
			// A setup with no operator behind it -- a pipe, a CI job -- must
			// change nothing rather than take silence for consent.
			w.trackerReady = false
			w.answers = ""
		},
	}
}

// An unanswerable question is declined rather than assumed. Silence is not
// consent, and a setup that read it as consent would be a command that changes
// a machine nobody was sitting at.
func TestSetupChangesNothingWhenThereIsNobodyToAsk(t *testing.T) {
	t.Parallel()

	world := newSetupWorld(t)
	world.trackerReady = false
	world.answers = ""

	report := world.walk()

	if world.runner.ran("bd init") {
		t.Error("setup initialized the tracker without being told to")
	}
	if _, err := os.Stat(filepath.Join(world.project, config.DirectoryName)); !os.IsNotExist(err) {
		t.Error("setup wrote a configuration without being told to")
	}
	if step := world.step(report, stepTracker); step.Status != setupSkipped {
		t.Fatalf("tracker step = %s (%s), want it skipped", step.Status, step.Summary)
	}
}

// The report a script reads carries the same account as the terminal does, and
// asking for it is not consent to change anything.
func TestSetupJSONAsksNothingAndChangesNothing(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := runSetup(context.Background(), []string{"--directory", project, "--json"}, strings.NewReader(""), &stdout, &stderr, "test")

	if code == 0 {
		t.Fatalf("setup --json on a blank project exited 0, stdout = %q", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(project, config.DirectoryName)); !os.IsNotExist(err) {
		t.Error("setup --json wrote a configuration")
	}
	if !strings.HasPrefix(strings.TrimSpace(stdout.String()), "{") {
		t.Fatalf("stdout is not machine-readable: %q", stdout.String())
	}
	if strings.Contains(stdout.String(), "[Y/n]") {
		t.Fatalf("a question reached the machine-readable report: %q", stdout.String())
	}
}

func TestSetupRefusesPositionalArguments(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	if code := runSetup(context.Background(), []string{"everything"}, strings.NewReader(""), &stdout, &stderr, "test"); code != 2 {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "Usage: yoyo setup") {
		t.Fatalf("stderr = %q, want the usage", stderr.String())
	}
}

// The block is written where there is none and edited where there is one, in
// each of the shapes a configuration that has been lived in actually has.
func TestRenderSlackSectionWritesOrEditsTheBlockInPlace(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		content string
		want    []string
		absent  []string
	}{
		"no block at all": {
			content: "version: 1\nchecks: []\n",
			want:    []string{"slack:", "  enabled: true", "  channel: C1"},
		},
		"a block that turned it off": {
			content: "version: 1\nslack:\n  enabled: false\n  channel: C0\nchecks: []\n",
			want:    []string{"  enabled: true", "  channel: C1", "checks: []"},
			absent:  []string{"enabled: false"},
		},
		"the commented example a generated file carries": {
			content: "approvals:\n  publishing: human\n\n# Reporting into Slack, off. Uncomment the block below.\n#\n# slack:\n#   enabled: true\n#   channel: C0123456789\n\nchecks: []\n",
			want:    []string{"  publishing: human", "slack:\n  enabled: true\n  channel: C1", "checks: []"},
			// The instruction to uncomment goes with the example it was about:
			// a file that both enables reporting and says to enable reporting is
			// one arguing with itself.
			absent: []string{"# slack:", "Uncomment the block below"},
		},
		"a block with the channel commented out": {
			content: "version: 1\nslack:\n  enabled: true\n  # channel: C0\nchecks: []\n",
			want:    []string{"  channel: C1", "  # channel: C0"},
		},
		"a block with its own comments and a blank line in it": {
			content: "version: 1\nslack:\n  # where it reports\n\n  enabled: false\nchecks: []\n",
			want:    []string{"  # where it reports", "  enabled: true", "  channel: C1", "checks: []"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rendered := renderSlackSection(tc.content, "C1")
			for _, want := range tc.want {
				if !strings.Contains(rendered, want) {
					t.Errorf("rendered configuration has no %q:\n%s", want, rendered)
				}
			}
			for _, absent := range tc.absent {
				if strings.Contains(rendered, absent) {
					t.Errorf("rendered configuration still has %q:\n%s", absent, rendered)
				}
			}
			if strings.Count(rendered, "\nslack:")+strings.Count(rendered, "slack:\n")-strings.Count(rendered, "\nslack:\n") > 1 {
				t.Errorf("the configuration has more than one slack block:\n%s", rendered)
			}
		})
	}
}

// What setup edits is what init wrote, so the real generated file is what this
// is run against rather than a copy of its shape: a scaffold that moves its
// commented example otherwise leaves setup appending a second block beneath it,
// silently, in a file nobody re-reads after setup says it wrote one.
func TestRenderSlackSectionReplacesTheExampleTheScaffoldWrote(t *testing.T) {
	t.Parallel()

	scaffold, err := config.NewScaffold(config.BuiltinV1, config.ScaffoldOptions{ProductID: "example", Repository: "."})
	if err != nil {
		t.Fatalf("NewScaffold() error = %v", err)
	}
	rendered := renderSlackSection(string(scaffold.Config.Content), "C1")
	if !strings.Contains(rendered, "\nslack:\n  enabled: true\n  channel: C1\n") {
		t.Errorf("the block was not written:\n%s", rendered)
	}
	for _, absent := range []string{"# slack:", "#   channel: C0123456789", "Uncomment"} {
		if strings.Contains(rendered, absent) {
			t.Errorf("the commented example survives as %q:\n%s", absent, rendered)
		}
	}
	// The operators example is a different section and not setup's to touch.
	if !strings.Contains(rendered, "# operators:") {
		t.Errorf("the operators example was taken with it:\n%s", rendered)
	}

	// And what it wrote has to load, which is the only thing that proves the
	// edit landed at the left margin rather than inside something else.
	scaffold.Config.Content = []byte(rendered)
	directory := filepath.Join(t.TempDir(), config.DirectoryName)
	for _, file := range scaffold.Files() {
		path := filepath.Join(directory, filepath.FromSlash(file.Path))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		if err := os.WriteFile(path, file.Content, 0o600); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
	}
	loaded, err := config.LoadResolved(filepath.Join(directory, config.FileName))
	if err != nil {
		t.Fatalf("LoadResolved() error = %v", err)
	}
	if !loaded.Config.Slack.Enabled || loaded.Config.Slack.Channel != "C1" {
		t.Errorf("slack = %+v, want reporting into C1", loaded.Config.Slack)
	}
}

// setupWorld is one machine a walk happens on: a project directory, the tools
// this machine has, and what the operator types.
type setupWorld struct {
	t             *testing.T
	project       string
	stateRoot     string
	goos          string
	runner        *setupRunner
	missing       map[string]bool
	trackerReady  bool
	trackerRemote string
	gitRemote     string
	slackChannel  string
	defaults      bool
	// closed is the walk with nobody behind it: what `--json` without `--yes`
	// puts the questioner in, and what an input that has run out reaches.
	closed  bool
	answers string
	out     bytes.Buffer
}

func newSetupWorld(t *testing.T) *setupWorld {
	t.Helper()

	root := t.TempDir()
	project := filepath.Join(root, "calc")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	// A Makefile naming its own entry point is a project that tells `yoyo init`
	// what its gate is, which is what lets a walk reach a healthy diagnosis
	// without an operator editing anything.
	if err := os.WriteFile(filepath.Join(project, "Makefile"), []byte("check:\n\techo ok\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	world := &setupWorld{
		t:             t,
		project:       project,
		stateRoot:     filepath.Join(root, "state"),
		goos:          "darwin",
		missing:       map[string]bool{},
		trackerReady:  true,
		trackerRemote: "file:///origin.git",
		gitRemote:     "file:///origin.git",
	}
	world.runner = &setupRunner{world: world, keychain: map[string]bool{}, refusals: map[string]bool{}}
	return world
}

func (w *setupWorld) walk() setupReport {
	w.t.Helper()

	walk := &setup{
		directory:    w.project,
		slackChannel: w.slackChannel,
		version:      "test",
		runner:       w.runner,
		lookPath:     w.lookPath,
		getenv:       w.getenv,
		homeDir:      func() (string, error) { return w.project, nil },
		goos:         w.goos,
		stdin:        strings.NewReader(w.answers),
		ask:          &questioner{reader: bufio.NewReader(strings.NewReader(w.answers)), out: &w.out, defaults: w.defaults, closed: w.closed},
	}
	return walk.converge(context.Background())
}

func (w *setupWorld) step(report setupReport, name string) setupStep {
	w.t.Helper()

	for _, step := range report.Steps {
		if step.Step == name {
			return step
		}
	}
	w.t.Fatalf("setup never reached the %s step:%s", name, renderSetupReport(report))
	return setupStep{}
}

// lookPath resolves what this machine has, enumerated rather than inverted so a
// missing tool is a state a test arranges by naming it.
func (w *setupWorld) lookPath(program string) (string, error) {
	installed := map[string]bool{"yoyo": true, "git": true, "bd": true, "claude": true, "gh": true, "make": true, "security": true}
	if !installed[program] || w.missing[program] {
		return "", errors.New(`exec: "` + program + `": executable file not found in $PATH`)
	}
	return filepath.Join("/usr/local/bin", program), nil
}

func (w *setupWorld) getenv(name string) string {
	if name == "YOYODYNE_STATE_HOME" {
		return w.stateRoot
	}
	return ""
}

// setupRunner answers the tools a walk uses, and remembers what they did to the
// machine: a tracker that was initialized answers afterwards, and a token that
// was stored is found afterwards. Convergence is the thing under test here, so a
// runner that forgot what the walk had already done could not test it.
type setupRunner struct {
	world    *setupWorld
	commands [][]string
	keychain map[string]bool
	refusals map[string]bool
}

func (r *setupRunner) refuse(match string) { r.refusals[match] = true }

func (r *setupRunner) ran(match string) bool {
	for _, invocation := range r.commands {
		if strings.Contains(strings.Join(invocation, " "), match) {
			return true
		}
	}
	return false
}

func (r *setupRunner) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	invocation := append([]string{filepath.Base(command.Name)}, command.Args...)
	r.commands = append(r.commands, invocation)
	joined := strings.Join(invocation, " ")
	if r.world.missing[filepath.Base(command.Name)] {
		return execution.ProcessResult{}, errors.New("executable file not found in $PATH")
	}
	for match := range r.refusals {
		if strings.Contains(joined, match) {
			return execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "refused: " + match}, nil
		}
	}

	switch {
	case joined == "bd stats":
		if !r.world.trackerReady {
			return execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: "no beads database here"}, nil
		}
	case joined == "bd init":
		r.world.trackerReady = true
	case strings.HasPrefix(joined, "bd dolt remote list"):
		if r.world.trackerRemote == "" {
			return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "[]"}, nil
		}
		return execution.ProcessResult{
			Status: execution.ProcessSucceeded,
			Stdout: `[{"name":"origin","url":"` + r.world.trackerRemote + `"}]`,
		}, nil
	case strings.HasPrefix(joined, "bd dolt remote add"):
		r.world.trackerRemote = invocation[len(invocation)-1]
	case strings.Contains(joined, "remote get-url"):
		if r.world.gitRemote == "" {
			return execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 2, Stderr: "error: No such remote 'origin'"}, nil
		}
		return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: r.world.gitRemote}, nil
	case joined == "yoyo version":
		return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "test"}, nil
	case joined == "claude --version":
		return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: "1.0.0"}, nil
	case joined == "claude auth status --json":
		return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: `{"loggedIn":true,"authMethod":"subscription"}`}, nil
	case strings.HasPrefix(joined, "security find-generic-password"):
		if !r.keychain[secretNameOf(command.Args)] {
			return execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 44, Stderr: "The specified item could not be found in the keychain."}, nil
		}
	case strings.HasPrefix(joined, "security add-generic-password"):
		r.keychain[secretNameOf(command.Args)] = true
	}
	return execution.ProcessResult{Status: execution.ProcessSucceeded}, nil
}

func secretNameOf(args []string) string {
	for index, arg := range args {
		if arg == "-s" && index+1 < len(args) {
			return args[index+1]
		}
	}
	return ""
}

// renderSetupReport is what a failing test prints, so a failure says what the
// whole walk did rather than only the field that was compared.
func renderSetupReport(report setupReport) string {
	var built strings.Builder
	built.WriteString("\n")
	for _, step := range report.Steps {
		built.WriteString(string(step.Status) + "\t" + step.Step + "\t" + step.Summary + "\n")
		if step.Remedy != "" {
			built.WriteString("\t\tnext: " + step.Remedy + "\n")
		}
	}
	for _, finding := range report.Diagnosis.Findings {
		if finding.Status == "ok" {
			continue
		}
		built.WriteString(string(finding.Status) + "\t" + finding.Check + "\t" + finding.Summary + "\n")
	}
	return built.String()
}
