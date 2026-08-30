package cli

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/execution"
	"github.com/mason-bryant/yoyodyne/internal/notify"
)

// How far a running session is behind is counted against the revision its binary
// was built from, which is the question — what does the repository hold that the
// build did not — rather than an elapsed time, which answers a different one.
func TestHowFarBehindASessionIsCountedFromItsBuild(t *testing.T) {
	t.Parallel()

	build := "4c1f2b3a9d8e7f6a5b4c3d2e1f0099887766554433221100aabbccddeeff0011"
	runner := &countingGit{stdout: "7\n"}
	deployments := repositoryDeployments{repository: "/repo", runner: runner, timeout: time.Second}

	behind, err := deployments.Behind(context.Background(), build)
	if err != nil {
		t.Fatalf("Behind() error = %v", err)
	}
	if behind != 7 {
		t.Fatalf("behind = %d, want 7", behind)
	}
	want := "rev-list --count " + build + "..HEAD"
	if got := strings.Join(runner.args, " "); got != want {
		t.Fatalf("asked git %q, want %q", got, want)
	}
}

// A repository that will not answer is an error rather than a zero, because a
// session reported as current by a comparison that never happened is the false
// all-clear the whole line exists to end. A build that is not a revision is
// refused here rather than handed to Git.
func TestARepositoryOrABuildThatCannotAnswerIsRefusedRatherThanCountedAsCurrent(t *testing.T) {
	t.Parallel()

	refusing := repositoryDeployments{
		repository: "/repo",
		runner:     &countingGit{failed: true, stderr: "fatal: bad revision"},
		timeout:    time.Second,
	}
	if _, err := refusing.Behind(context.Background(), strings.Repeat("a", 40)); err == nil {
		t.Fatal("Behind() error = nil, want a comparison that could not be made reported as one")
	}
	asked := &countingGit{stdout: "0\n"}
	misbuilt := repositoryDeployments{repository: "/repo", runner: asked, timeout: time.Second}
	if _, err := misbuilt.Behind(context.Background(), "--upload-pack=touch /tmp/x"); err == nil {
		t.Fatal("Behind() error = nil, want a build that is not a revision refused")
	}
	if asked.args != nil {
		t.Fatalf("git was asked %v, want nothing run at all", asked.args)
	}
}

// countingGit is a Git that answers whatever a test needs and remembers what it
// was asked, which is where the shape of the question is checked.
type countingGit struct {
	args   []string
	stdout string
	stderr string
	failed bool
}

func (g *countingGit) Run(_ context.Context, command execution.Command, _ execution.OutputObserver) (execution.ProcessResult, error) {
	g.args = command.Args
	if g.failed {
		return execution.ProcessResult{Status: execution.ProcessFailed, ExitCode: 128, Stderr: g.stderr}, nil
	}
	return execution.ProcessResult{Status: execution.ProcessSucceeded, Stdout: g.stdout}, nil
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

	if _, _, err := buildSlackSink(configPath, time.Second, time.Minute, "test", io.Discard); err != nil {
		t.Fatalf("buildSlackSink() error = %v", err)
	}
}

// The verb is one an operator reaches for when they want reporting, so its
// usage has to carry the whole of what it needs: the tokens, the one-sink rule,
// and where the setup document is.
func TestTheSinkUsageSaysWhatSettingItUpNeeds(t *testing.T) {
	t.Parallel()

	var usage strings.Builder
	printSlackUsage(&usage)
	for _, want := range []string{"SLACK_BOT_TOKEN", "SLACK_APP_TOKEN", "One sink per product", "docs/slack/setup.md"} {
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
