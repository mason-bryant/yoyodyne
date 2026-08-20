package cli

import (
	"strings"
	"testing"

	"github.com/mason-bryant/yoyodyne/internal/config"
	"github.com/mason-bryant/yoyodyne/internal/notify"
)

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
