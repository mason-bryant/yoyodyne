package execution

// The environment a run's process is given is built from an allowlist, and the
// Slack token pair is what the allowlist exists to keep out. What is asserted
// here is the two halves of that: the list itself, over a synthetic parent, and
// then a real child launched through the runner with the pair exported in this
// process -- which is exactly the shell-profile case the guarantee is for -- not
// seeing them.

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestExplicitEnvironmentCarriesOnlyWhatTheAllowlistAdmits(t *testing.T) {
	t.Parallel()

	got := ExplicitEnvironment([]string{
		"PATH=/usr/bin",
		"HOME=/home/operator",
		"SLACK_BOT_TOKEN=xoxb-secret",
		"SLACK_APP_TOKEN=xapp-secret",
		"GOCACHE=/somewhere",
		"GIT_CONFIG_COUNT=1",
		"LC_ALL=C",
		"XDG_CONFIG_HOME=/home/operator/.config",
		"YOYODYNE_STATE_HOME=/state",
		"AWS_REGION=us-east-1",
		"SHLVL=3",
		"MALFORMED",
	})
	want := []string{
		"PATH=/usr/bin",
		"HOME=/home/operator",
		"GOCACHE=/somewhere",
		"GIT_CONFIG_COUNT=1",
		"LC_ALL=C",
		"XDG_CONFIG_HOME=/home/operator/.config",
		"YOYODYNE_STATE_HOME=/state",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("ExplicitEnvironment() = %v, want %v", got, want)
	}
}

// A provider's own variables are carried under the prefixes its backend names,
// and nothing else's are: what is a provider's own is that backend's to say.
func TestExplicitEnvironmentCarriesTheProvidersOwnFamiliesWhenNamed(t *testing.T) {
	t.Parallel()

	parent := []string{"PATH=/usr/bin", "CLAUDE_CONFIG_DIR=/homes/two", "ANTHROPIC_BASE_URL=https://proxy.example", "CODEX_HOME=/homes/codex"}
	if got := ExplicitEnvironment(parent, "CLAUDE_", "ANTHROPIC_"); !slices.Equal(got, parent[:3]) {
		t.Fatalf("ExplicitEnvironment() = %v, want %v", got, parent[:3])
	}
	if got := ExplicitEnvironment(parent); !slices.Equal(got, parent[:1]) {
		t.Fatalf("ExplicitEnvironment() with no provider = %v, want %v", got, parent[:1])
	}
}

// The credential rule is on top of the allowlist rather than instead of it: a
// name the list admits is still dropped when it reads as a credential, which is
// what keeps a provider's API key out under the provider's own prefix and what
// would keep the token pair out even if the list ever admitted the family.
func TestExplicitEnvironmentDropsACredentialTheAllowlistWouldAdmit(t *testing.T) {
	t.Parallel()

	got := ExplicitEnvironment([]string{
		"ANTHROPIC_API_KEY=sk-secret",
		"CLAUDE_CODE_OAUTH_TOKEN=oauth-secret",
		"ANTHROPIC_MODEL=opus",
		"GOOGLE_APPLICATION_CREDENTIALS=/keys/service.json",
		"GOFLAGS=-mod=mod",
		"YOYODYNE_CLIENT_SECRET=harness-secret",
	}, "CLAUDE_", "ANTHROPIC_")
	want := []string{"ANTHROPIC_MODEL=opus", "GOFLAGS=-mod=mod"}
	if !slices.Equal(got, want) {
		t.Fatalf("ExplicitEnvironment() = %v, want %v", got, want)
	}
	for _, entry := range got {
		name, _, _ := strings.Cut(entry, "=")
		if sensitiveEnvironmentName(name) {
			t.Fatalf("an explicit environment carries %q, which SensitiveEnvironmentValues recognizes", name)
		}
	}
}

// The case the guarantee is for: the token pair exported where every process
// the harness starts would inherit it. A child launched through the runner with
// an explicit environment does not see them, and does see what it needs to run.
func TestAChildGivenAnExplicitEnvironmentDoesNotSeeTheSlackTokens(t *testing.T) {
	// t.Setenv is this process's environment, so this cannot run in parallel.
	t.Setenv("SLACK_BOT_TOKEN", "xoxb-exported-in-the-parent")
	t.Setenv("SLACK_APP_TOKEN", "xapp-exported-in-the-parent")

	result, err := OSProcessRunner{}.Run(context.Background(), Command{
		Name:    "/bin/sh",
		Args:    []string{"-c", "env"},
		Env:     ExplicitEnvironment(nil),
		Timeout: 30 * time.Second,
	}, nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Status != ProcessSucceeded {
		t.Fatalf("Run() = %#v: %s", result.Status, result.Stderr)
	}
	seen := make(map[string]string)
	for _, line := range strings.Split(result.Stdout, "\n") {
		if name, value, named := strings.Cut(line, "="); named {
			seen[name] = value
		}
	}
	for _, name := range []string{"SLACK_BOT_TOKEN", "SLACK_APP_TOKEN"} {
		if value, present := seen[name]; present {
			t.Errorf("the child sees %s=%q, which was exported in the parent and must not reach it", name, value)
		}
	}
	for _, name := range []string{"PATH", "HOME"} {
		if _, present := seen[name]; !present {
			t.Errorf("the child does not see %s, without which it cannot run at all", name)
		}
	}
	// And the fence still arrives on top of it: an explicit environment is the
	// base the runner adds to, not a way around what every process carries.
	if seen[gitConfigCountVariable] == "" {
		t.Errorf("the child carries no Git maintenance fence: %v", result.Stdout)
	}
}
