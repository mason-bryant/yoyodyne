package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/beads"
	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/notify"
	"github.com/mason-bryant/yoyodyne/internal/readmodel"
	"github.com/mason-bryant/yoyodyne/internal/slack"
)

// The build revision comes from the yoyodyne binary and the repository comes from
// the product configuration, so the reading asks Git whether they are the same
// history before it counts anything, and counts against the recorded revision
// rather than against a time — the question is what the repository holds that the
// build did not, and a commit's own date answers a different one.
func TestHowFarBehindASessionIsCountedOnlyAgainstItsOwnHistory(t *testing.T) {
	t.Parallel()

	build := "4c1f2b3a9d8e7f6a5b4c3d2e1f0099887766554433221100aabbccddeeff0011"
	runner := &countingGit{answers: []gitAnswer{{}, {stdout: "7\n"}}}
	deployments := repositoryDeployments{repository: "/repo", runner: runner, timeout: time.Second}

	behind, err := deployments.Behind(context.Background(), build)
	if err != nil {
		t.Fatalf("Behind() error = %v", err)
	}
	if behind != 7 {
		t.Fatalf("behind = %d, want 7", behind)
	}
	want := []string{
		"cat-file -e " + build + "^{commit}",
		"rev-list --count " + build + "..HEAD",
	}
	if got := runner.asked(); !slices.Equal(got, want) {
		t.Fatalf("asked git %q, want %q", got, want)
	}
}

// A binary built somewhere else is the ordinary state of every product that is
// not the harness's own source: the sink is pointed at the product's repository
// and the session's revision is not in it. That is reported as its own answer, so
// nothing counts commits out of an unrelated history and calls them harness
// changes, and nothing treats a healthy installation as a fault.
func TestABuildTheRepositoryDoesNotHoldIsNotCountedAgainstIt(t *testing.T) {
	t.Parallel()

	runner := &countingGit{answers: []gitAnswer{{failed: true}}}
	deployments := repositoryDeployments{repository: "/some-other-product", runner: runner, timeout: time.Second}

	_, err := deployments.Behind(context.Background(), strings.Repeat("a", 40))
	if !errors.Is(err, slack.ErrUnrelatedBuild) {
		t.Fatalf("Behind() error = %v, want it reported as an unrelated build", err)
	}
	if len(runner.commands) != 1 {
		t.Fatalf("asked git %q, want the count never attempted", runner.asked())
	}
}

// A repository that will not answer the count is an error rather than a zero,
// because a session reported as current by a comparison that never happened is
// the false all-clear the whole line exists to end. A build that is not a
// revision is refused here rather than handed to Git.
func TestARepositoryOrABuildThatCannotAnswerIsRefusedRatherThanCountedAsCurrent(t *testing.T) {
	t.Parallel()

	refusing := repositoryDeployments{
		repository: "/repo",
		runner:     &countingGit{answers: []gitAnswer{{}, {failed: true, stderr: "fatal: bad revision"}}},
		timeout:    time.Second,
	}
	_, err := refusing.Behind(context.Background(), strings.Repeat("a", 40))
	if err == nil {
		t.Fatal("Behind() error = nil, want a comparison that could not be made reported as one")
	}
	// It is a failure to be retried and said, not the quiet answer an unrelated
	// build gets: this repository does hold the revision and could not count.
	if errors.Is(err, slack.ErrUnrelatedBuild) {
		t.Fatalf("Behind() error = %v, want a repository that broke told apart from one this build is not from", err)
	}
	asked := &countingGit{}
	misbuilt := repositoryDeployments{repository: "/repo", runner: asked, timeout: time.Second}
	if _, err := misbuilt.Behind(context.Background(), "--upload-pack=touch /tmp/x"); err == nil {
		t.Fatal("Behind() error = nil, want a build that is not a revision refused")
	}
	if len(asked.commands) != 0 {
		t.Fatalf("git was asked %v, want nothing run at all", asked.asked())
	}
}

// gitAnswer is what the stand-in Git says to one call.
type gitAnswer struct {
	stdout string
	stderr string
	failed bool
}

// countingGit is a Git that answers whatever a test needs, in order, and
// remembers what it was asked — which is where the shape of the questions and
// the fact that the second one is never reached are both checked. A call past
// the end of the answers succeeds saying nothing, which is what `cat-file -e`
// does when the object is there.
type countingGit struct {
	answers  []gitAnswer
	commands [][]string
}

func (g *countingGit) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	index := len(g.commands)
	g.commands = append(g.commands, command.Args)
	if index >= len(g.answers) {
		return execution.ProcessResult{Status: execution.ProcessSucceeded}, nil
	}
	answer := g.answers[index]
	if answer.failed {
		return execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 1, Stderr: answer.stderr}, nil
	}
	return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: answer.stdout}, nil
}

// asked is every call as one line each, which is what a failure prints.
func (g *countingGit) asked() []string {
	said := make([]string, 0, len(g.commands))
	for _, command := range g.commands {
		said = append(said, strings.Join(command, " "))
	}
	return said
}

// The two tokens belong to this process's environment and to nowhere else. A
// sink started without them has to say which one is missing and name the
// variable rather than anything about its value: nothing in this process ever
// prints a token.
func TestTheSinkRefusesToStartWithoutBothTokens(t *testing.T) {
	// Not parallel: the environment the command reads is set here.
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())
	t.Setenv("SLACK_BOT_TOKEN", "")
	t.Setenv("SLACK_APP_TOKEN", "")
	configPath := writeConfig(t, slackConfig)

	_, stderr, code := runCLI(t, "slack", "--config", configPath, "--once")
	if code != 1 {
		t.Fatalf("slack code = %d, want a refusal", code)
	}
	if !strings.Contains(stderr, "SLACK_BOT_TOKEN") || !strings.Contains(stderr, "SLACK_APP_TOKEN") {
		t.Fatalf("stderr = %q, want both missing variables named at once", stderr)
	}
}

// A project that has not opted in is not a project with a broken sink: the verb
// says so, and says what to set, rather than posting into a workspace nobody
// configured.
func TestTheSinkRefusesAProjectThatHasNotOptedIn(t *testing.T) {
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())
	t.Setenv("SLACK_BOT_TOKEN", "xoxb-test")
	t.Setenv("SLACK_APP_TOKEN", "xapp-test")
	configPath := writeConfig(t, validConfig)

	_, stderr, code := runCLI(t, "slack", "--config", configPath, "--once")
	if code != 1 {
		t.Fatalf("slack code = %d, want a refusal", code)
	}
	if !strings.Contains(stderr, "slack.enabled") {
		t.Fatalf("stderr = %q, want the setting that turns it on named", stderr)
	}
}

// Every speaker's name is qualified by the product it speaks for, and the sink
// takes that product from the state store this builds for it. So an opted-in
// project assembles a sink at all only if that store was rooted at its product,
// which is the one place the id is given.
func TestTheSinkIsAssembledForTheProductItReportsOn(t *testing.T) {
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())
	t.Setenv("SLACK_BOT_TOKEN", "xoxb-test")
	t.Setenv("SLACK_APP_TOKEN", "xapp-test")
	configPath := writeConfig(t, slackConfig)

	if _, _, err := buildSlackSink(configPath, time.Second, time.Minute, readmodel.DefaultStallThreshold, "test", io.Discard); err != nil {
		t.Fatalf("buildSlackSink() error = %v", err)
	}
}

// A pass that runs over a project reporting nowhere has nothing to supervise
// and nothing to complain about. It has to say so and succeed: an unattended
// pass that failed here would report every product on the machine that has not
// turned reporting on, every time it ran.
func TestSupervisingAProjectThatReportsNowhereIsNotAFailure(t *testing.T) {
	t.Setenv("YOYODYNE_STATE_HOME", t.TempDir())
	configPath := writeConfig(t, validConfig)

	stdout, stderr, code := runCLI(t, "slack", "ensure", "--config", configPath)
	if code != 0 {
		t.Fatalf("slack ensure code = %d (%s), want a project with reporting off to be healthy", code, stderr)
	}
	if !strings.Contains(stdout, "slack.enabled") {
		t.Fatalf("stdout = %q, want the setting that turns it on named", stdout)
	}
}

// What the pass says goes to whatever collects an unattended job's output, and
// the exit code is what that job reacts to: a sink already running is the
// ordinary outcome and not news, and tokens nobody stored is the one outcome
// somebody has to act on.
func TestSupervisionFailsOnlyWhereNothingIsReportingAndNothingCanStart(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		supervision slack.Supervision
		code        int
		says        string
	}{
		{slack.Supervision{Product: "yoyodyne", Outcome: slack.OutcomeRunning}, 0, "already reporting"},
		{slack.Supervision{Product: "yoyodyne", Outcome: slack.OutcomeStarted, PID: 91, Log: "/state/sink.log"}, 0, "pid 91"},
		{slack.Supervision{Product: "yoyodyne", Outcome: slack.OutcomeReportingOff}, 0, "no sink to run"},
		{
			slack.Supervision{
				Product: "yoyodyne",
				Outcome: slack.OutcomeSecretsUnavailable,
				Secrets: []string{slack.BotSecret("yoyodyne"), slack.AppSecret("yoyodyne")},
			},
			1, "yoyo-slack-app.yoyodyne",
		},
	} {
		var stdout, stderr strings.Builder
		code := reportSupervision(&stdout, &stderr, false, testCase.supervision)
		if code != testCase.code {
			t.Errorf("%s code = %d, want %d", testCase.supervision.Outcome, code, testCase.code)
		}
		if !strings.Contains(stdout.String(), testCase.says) {
			t.Errorf("%s said %q, want it to mention %q", testCase.supervision.Outcome, stdout.String(), testCase.says)
		}
	}
}

// The verb is one an operator reaches for when they want reporting, so its
// usage has to carry the whole of what it needs: the tokens, the one-sink rule,
// how it is kept running, and where the setup document is.
func TestTheSinkUsageSaysWhatSettingItUpNeeds(t *testing.T) {
	t.Parallel()

	var usage strings.Builder
	printSlackUsage(&usage)
	for _, want := range []string{"SLACK_BOT_TOKEN", "SLACK_APP_TOKEN", "One sink per product", "yoyo slack ensure", "docs/slack/setup.md"} {
		if !strings.Contains(usage.String(), want) {
			t.Errorf("usage does not mention %q", want)
		}
	}
}

// Reporting is one of the harness's verbs, so it belongs in the list an
// operator reads to find out what the harness can do.
func TestSlackIsListedAmongTheCommands(t *testing.T) {
	t.Parallel()

	stdout, _, code := runCLI(t, "help")
	if code != 0 {
		t.Fatalf("help code = %d", code)
	}
	if !strings.Contains(stdout, "slack") {
		t.Fatalf("usage = %q, want the reporting verb listed", stdout)
	}
}

// An avatar is keyed by the speaker it decorates, and two packages have to agree
// on how a speaker is spelled: the configuration checks the key and the notifier
// looks it up. They are separate constants so neither package depends on the
// other, which is exactly the arrangement that can drift — a rename on one side
// would turn every configured avatar into an entry nothing reads, silently,
// because an avatar nobody applies looks like an avatar nobody configured.
func TestTheHarnessIsSpelledTheSameWayInAnAvatarKeyAsInASpeaker(t *testing.T) {
	t.Parallel()

	if config.SlackHarnessAvatar != notify.HarnessSpeaker {
		t.Fatalf("slack.avatars keys the harness as %q and the notifier as %q", config.SlackHarnessAvatar, notify.HarnessSpeaker)
	}
}

const slackConfig = `version: 1
product:
  id: yoyodyne
  repository: .
approvals:
  brief: human
  goals: human
  designs: automatic
  integration: human
checks:
  - go test ./...
agents:
  developer:
    role: developer
    backend: claude-code
    model: opus
slack:
  enabled: true
  channel: C0123456789
`

// The count the sink reads the queue with is what a run could actually be
// started for. The tracker's own readiness is about dependencies alone, so it
// includes work no pull will ever take — a conversation's to carry, or parked —
// and counting that is how the heartbeat sent an operator three times to a line
// that had not stopped. It matters more where the stall watchdog reads it: there
// the number is the whole of what separates a machine that has died from one
// with nothing to do.
func TestTheSinkCountsOnlyTheReadyWorkARunCouldCarry(t *testing.T) {
	t.Parallel()

	ready := `[
	  {"id": "yoyodyne-ifd.1", "title": "an ordinary item", "status": "open"},
	  {"id": "yoyodyne-ifd.2", "title": "the architect's own", "status": "open",
	   "metadata": {"yoyodyne_executor": "conversation:architect"}},
	  {"id": "yoyodyne-ifd.3", "title": "parked", "status": "open",
	   "metadata": {"yoyodyne_parked": "the design is being reworked"}},
	  {"id": "yoyodyne-ifd.4", "title": "another ordinary item", "status": "open"}
	]`
	backlog := readyBacklog{tracker: beads.Client{
		Runner: &scriptedRunner{outputs: map[string]string{"bd": ready}},
		Dir:    t.TempDir(),
	}}
	count, err := backlog.Ready(context.Background())
	if err != nil {
		t.Fatalf("Ready() error = %v", err)
	}
	if count != 2 {
		t.Fatalf("Ready() = %d, want the two items a developer run could be started for", count)
	}
}

// What the sink reads the template comparison through. It is the same derivation
// `yoyo config drift` prints, read afresh as the sink asks for it, so a project
// that adopts a value under a process that has been open for a fortnight stops
// being offered it.
func TestTheSinkReadsTheSameImprovementsTheDriftCommandPrints(t *testing.T) {
	t.Parallel()

	path := driftingProject(t, "agents.developer.model")
	drift, err := configImprovements{path: path}.Offered(context.Background())
	if err != nil {
		t.Fatalf("Offered() error = %v", err)
	}
	available := drift.Available()
	if len(available) != 1 || available[0].Key != "agents.developer.model" {
		t.Fatalf("Available() = %+v, want the one value the template improved", available)
	}
	if !strings.Contains(drift.Improvement(available[0]), "agents.developer.model") {
		t.Errorf("Improvement() = %q, want the setting named", drift.Improvement(available[0]))
	}
}

// A project with no baseline has no third side, so there is nothing it can be
// offered. That is silence rather than a failure: it is the ordinary state of
// every project generated before the record existed, and an hourly complaint
// about a file that decides nothing is the nagging this surface avoids.
func TestAProjectWithNoBaselineIsOfferedNothingAndReportsNoFailure(t *testing.T) {
	t.Parallel()

	path := writeProjectConfig(t, portableConfig)
	drift, err := configImprovements{path: path}.Offered(context.Background())
	if err != nil {
		t.Fatalf("Offered() error = %v", err)
	}
	if len(drift.Available()) != 0 {
		t.Errorf("Available() = %+v, want nothing offered to a project with no baseline", drift.Available())
	}
}

// A baseline that is on disk and will not be read is a file somebody can look
// at, so it is reported rather than swallowed: the sink says it in its own log
// and asks again at the next interval.
func TestAnUnusableBaselineIsReportedRatherThanReadAsNothingOffered(t *testing.T) {
	t.Parallel()

	path := writeProjectConfig(t, portableConfig)
	if err := os.WriteFile(config.LockPath(path),
		[]byte("version: 1\nbundle: builtin:v1\nrevision: bnd-000000000000\nvalues:\n  agents.developer.model: \"opus\"\n"), 0o600); err != nil {
		t.Fatalf("write baseline: %v", err)
	}
	if _, err := (configImprovements{path: path}).Offered(context.Background()); err == nil {
		t.Fatal("a baseline that could not be used read as a project with nothing to offer")
	}
}
